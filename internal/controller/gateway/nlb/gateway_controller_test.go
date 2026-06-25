package nlb

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/consts"
)

const nlbNS = "default"

var _ = Describe("NLB GatewayClass", func() {
	It("accepts the vngcloud-nlb GatewayClass", func() {
		gc := &gwv1.GatewayClass{
			ObjectMeta: metav1.ObjectMeta{Name: "vngcloud-nlb-test"},
			Spec:       gwv1.GatewayClassSpec{ControllerName: gwv1.GatewayController(consts.GatewayClassControllerNameNLB)},
		}
		Expect(k8sClient.Create(ctx, gc)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, gc) })

		Eventually(func() metav1.ConditionStatus {
			cur := &gwv1.GatewayClass{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Name: gc.Name}, cur); err != nil {
				return metav1.ConditionUnknown
			}
			for _, c := range cur.Status.Conditions {
				if c.Type == string(gwv1.GatewayClassConditionStatusAccepted) {
					return c.Status
				}
			}
			return metav1.ConditionUnknown
		}, 20*time.Second, time.Second).Should(Equal(metav1.ConditionTrue))
	})
})

var _ = Describe("NLB Gateway → L4 LoadBalancerConfig", func() {
	It("creates a Type=Network LBC with a TCP listener+pool from an attached TCPRoute", func() {
		gc := &gwv1.GatewayClass{
			ObjectMeta: metav1.ObjectMeta{Name: "vngcloud-nlb"},
			Spec:       gwv1.GatewayClassSpec{ControllerName: gwv1.GatewayController(consts.GatewayClassControllerNameNLB)},
		}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, gc))).To(Succeed())

		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: nlbNS, Name: "redis"},
			Spec: corev1.ServiceSpec{
				Type:     corev1.ServiceTypeNodePort,
				Selector: map[string]string{"app": "redis"},
				Ports:    []corev1.ServicePort{{Port: 6379, TargetPort: intstr.FromInt(6379), NodePort: 31379}},
			},
		}
		Expect(k8sClient.Create(ctx, svc)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, svc) })

		gw := &gwv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Namespace: nlbNS, Name: "nlb-gw"},
			Spec: gwv1.GatewaySpec{
				GatewayClassName: "vngcloud-nlb",
				Listeners: []gwv1.Listener{
					{Name: "tcp", Protocol: gwv1.TCPProtocolType, Port: 6379},
				},
			},
		}
		Expect(k8sClient.Create(ctx, gw)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, gw)
		})

		port := gwv1.PortNumber(6379)
		route := &gwv1a2.TCPRoute{
			ObjectMeta: metav1.ObjectMeta{Namespace: nlbNS, Name: "redis-route"},
			Spec: gwv1a2.TCPRouteSpec{
				CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "nlb-gw"}}},
				Rules: []gwv1a2.TCPRouteRule{{
					BackendRefs: []gwv1.BackendRef{{BackendObjectReference: gwv1.BackendObjectReference{Name: "redis", Port: &port}}},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, route)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, route) })

		By("the controller creates a Type=Network LBC with the TCP listener and pool")
		Eventually(func(g Gomega) {
			lbc := ownedLBC(g, gw)
			g.Expect(lbc).NotTo(BeNil())
			g.Expect(lbc.Spec.Type).To(Equal(v2.LoadBalancerTypeLayer4))
			g.Expect(lbc.Spec.Listeners).To(HaveLen(1))
			g.Expect(lbc.Spec.Listeners[0].Protocol).To(Equal(v2.ListenerProtocolTCP))
			g.Expect(lbc.Spec.Listeners[0].ProtocolPort).To(Equal(int32(6379)))
			g.Expect(lbc.Spec.Pools).To(HaveLen(1))
			g.Expect(lbc.Spec.Pools[0].Protocol).To(Equal(v2.PoolProtocolTCP))
		}, 30*time.Second, time.Second).Should(Succeed())

		By("the TCPRoute reports Accepted=True on its parent")
		Eventually(func(g Gomega) {
			cur := &gwv1a2.TCPRoute{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: nlbNS, Name: "redis-route"}, cur)).To(Succeed())
			g.Expect(cur.Status.Parents).NotTo(BeEmpty())
			var accepted = metav1.ConditionUnknown
			for _, c := range cur.Status.Parents[0].Conditions {
				if c.Type == string(gwv1.RouteConditionAccepted) {
					accepted = c.Status
				}
			}
			g.Expect(accepted).To(Equal(metav1.ConditionTrue))
		}, 30*time.Second, time.Second).Should(Succeed())
	})
})

var _ = Describe("NLB Gateway UDP + deletion", func() {
	It("creates a UDP listener+pool from a UDPRoute", func() {
		gc := &gwv1.GatewayClass{
			ObjectMeta: metav1.ObjectMeta{Name: "vngcloud-nlb"},
			Spec:       gwv1.GatewayClassSpec{ControllerName: gwv1.GatewayController(consts.GatewayClassControllerNameNLB)},
		}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, gc))).To(Succeed())

		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: nlbNS, Name: "dns"},
			Spec: corev1.ServiceSpec{
				Type:     corev1.ServiceTypeNodePort,
				Selector: map[string]string{"app": "dns"},
				Ports:    []corev1.ServicePort{{Port: 5353, Protocol: corev1.ProtocolUDP, TargetPort: intstr.FromInt(5353), NodePort: 32053}},
			},
		}
		Expect(k8sClient.Create(ctx, svc)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, svc) })

		gw := &gwv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Namespace: nlbNS, Name: "nlb-udp-gw"},
			Spec: gwv1.GatewaySpec{
				GatewayClassName: "vngcloud-nlb",
				Listeners:        []gwv1.Listener{{Name: "udp", Protocol: gwv1.UDPProtocolType, Port: 5353}},
			},
		}
		Expect(k8sClient.Create(ctx, gw)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, gw) })

		port := gwv1.PortNumber(5353)
		route := &gwv1a2.UDPRoute{
			ObjectMeta: metav1.ObjectMeta{Namespace: nlbNS, Name: "dns-route"},
			Spec: gwv1a2.UDPRouteSpec{
				CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "nlb-udp-gw"}}},
				Rules: []gwv1a2.UDPRouteRule{{
					BackendRefs: []gwv1.BackendRef{{BackendObjectReference: gwv1.BackendObjectReference{Name: "dns", Port: &port}}},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, route)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, route) })

		Eventually(func(g Gomega) {
			lbc := ownedLBC(g, gw)
			g.Expect(lbc).NotTo(BeNil())
			g.Expect(lbc.Spec.Type).To(Equal(v2.LoadBalancerTypeLayer4))
			g.Expect(lbc.Spec.Listeners).To(HaveLen(1))
			g.Expect(lbc.Spec.Listeners[0].Protocol).To(Equal(v2.ListenerProtocolUDP))
			g.Expect(lbc.Spec.Pools).To(HaveLen(1))
			g.Expect(lbc.Spec.Pools[0].Protocol).To(Equal(v2.PoolProtocolUDP))
		}, 30*time.Second, time.Second).Should(Succeed())
	})

	It("removes the owned LBC when the Gateway is deleted", func() {
		gc := &gwv1.GatewayClass{
			ObjectMeta: metav1.ObjectMeta{Name: "vngcloud-nlb"},
			Spec:       gwv1.GatewayClassSpec{ControllerName: gwv1.GatewayController(consts.GatewayClassControllerNameNLB)},
		}
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, gc))).To(Succeed())

		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: nlbNS, Name: "del-svc"},
			Spec: corev1.ServiceSpec{
				Type:     corev1.ServiceTypeNodePort,
				Selector: map[string]string{"app": "del"},
				Ports:    []corev1.ServicePort{{Port: 9000, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt(9000), NodePort: 30900}},
			},
		}
		Expect(k8sClient.Create(ctx, svc)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, svc) })

		gw := &gwv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Namespace: nlbNS, Name: "nlb-del-gw"},
			Spec: gwv1.GatewaySpec{
				GatewayClassName: "vngcloud-nlb",
				Listeners:        []gwv1.Listener{{Name: "tcp", Protocol: gwv1.TCPProtocolType, Port: 9000}},
			},
		}
		Expect(k8sClient.Create(ctx, gw)).To(Succeed())

		port := gwv1.PortNumber(9000)
		route := &gwv1a2.TCPRoute{
			ObjectMeta: metav1.ObjectMeta{Namespace: nlbNS, Name: "del-route"},
			Spec: gwv1a2.TCPRouteSpec{
				CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: "nlb-del-gw"}}},
				Rules: []gwv1a2.TCPRouteRule{{
					BackendRefs: []gwv1.BackendRef{{BackendObjectReference: gwv1.BackendObjectReference{Name: "del-svc", Port: &port}}},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, route)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, route) })

		By("LBC is created")
		var lbcName string
		Eventually(func(g Gomega) {
			lbc := ownedLBC(g, gw)
			g.Expect(lbc).NotTo(BeNil())
			lbcName = lbc.Name
		}, 30*time.Second, time.Second).Should(Succeed())

		By("deleting the Gateway removes the owned LBC (finalizer cleanup)")
		Expect(k8sClient.Delete(ctx, gw)).To(Succeed())
		Eventually(func(g Gomega) {
			cur := &v1alpha1.LoadBalancerConfig{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: nlbNS, Name: lbcName}, cur)
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "owned LBC must be deleted")
		}, 30*time.Second, time.Second).Should(Succeed())
	})
})

func ownedLBC(g Gomega, gw *gwv1.Gateway) *v1alpha1.LoadBalancerConfig {
	var list v1alpha1.LoadBalancerConfigList
	g.Expect(k8sClient.List(ctx, &list, client.InNamespace(gw.Namespace),
		client.MatchingLabels{domain.OwnerLabelGatewayUID: string(gw.UID)})).To(Succeed())
	if len(list.Items) == 0 {
		return nil
	}
	return &list.Items[0]
}
