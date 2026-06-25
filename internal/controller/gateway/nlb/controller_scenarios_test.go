package nlb

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/consts"
)

// These envtest specs port behavioral scenarios from the Service controller
// ("update port", "all annotations -> LB attrs") and Ingress controller
// ("prefer subnet ID") onto the NLB Gateway path. They assert on the generated
// LoadBalancerConfig against the mock cloud — the same level the source
// controller suites operate at.

func ensureNLBClass() {
	gc := &gwv1.GatewayClass{
		ObjectMeta: metav1.ObjectMeta{Name: "vngcloud-nlb"},
		Spec:       gwv1.GatewayClassSpec{ControllerName: gwv1.GatewayController(consts.GatewayClassControllerNameNLB)},
	}
	Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, gc))).To(Succeed())
}

func mkNodePortSvc(name string, port int32) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: nlbNS, Name: name},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeNodePort,
			Selector: map[string]string{"app": name},
			Ports:    []corev1.ServicePort{{Port: port, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromInt(int(port))}},
		},
	}
}

func mkTCPGateway(name string, port int32) *gwv1.Gateway {
	return &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: nlbNS, Name: name},
		Spec: gwv1.GatewaySpec{
			GatewayClassName: "vngcloud-nlb",
			Listeners:        []gwv1.Listener{{Name: "tcp", Protocol: gwv1.TCPProtocolType, Port: gwv1.PortNumber(port)}},
		},
	}
}

func mkTCPRoute(name, gwName, svc string, port int32) *gwv1a2.TCPRoute {
	p := gwv1.PortNumber(port)
	return &gwv1a2.TCPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: nlbNS, Name: name},
		Spec: gwv1a2.TCPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{ParentRefs: []gwv1.ParentReference{{Name: gwv1.ObjectName(gwName)}}},
			Rules: []gwv1a2.TCPRouteRule{{
				BackendRefs: []gwv1.BackendRef{{BackendObjectReference: gwv1.BackendObjectReference{Name: gwv1.ObjectName(svc), Port: &p}}},
			}},
		},
	}
}

func gwTargetRef(gwName string) gwv1alpha1.LocalPolicyTargetReferenceWithSectionName {
	return gwv1alpha1.LocalPolicyTargetReferenceWithSectionName{
		LocalPolicyTargetReference: gwv1a2.LocalPolicyTargetReference{
			Group: "gateway.networking.k8s.io", Kind: "Gateway", Name: gwv1a2.ObjectName(gwName),
		},
	}
}

var _ = Describe("NLB Gateway scenarios ported from Service/Ingress", func() {
	// Service controller: "When updating service port ... delete old listener
	// and pool, and create new ones with updated port".
	It("reflects a listener port change on the LBC", func() {
		ensureNLBClass()
		svc := mkNodePortSvc("portchg", 6379)
		Expect(k8sClient.Create(ctx, svc)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, svc) })

		gw := mkTCPGateway("portchg-gw", 6379)
		Expect(k8sClient.Create(ctx, gw)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, gw) })
		route := mkTCPRoute("portchg-route", "portchg-gw", "portchg", 6379)
		Expect(k8sClient.Create(ctx, route)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, route) })

		By("LBC listener starts on the original port 6379")
		Eventually(func(g Gomega) {
			lbc := ownedLBC(g, gw)
			g.Expect(lbc).NotTo(BeNil())
			g.Expect(lbc.Spec.Listeners).To(HaveLen(1))
			g.Expect(lbc.Spec.Listeners[0].ProtocolPort).To(Equal(int32(6379)))
		}, 30*time.Second, time.Second).Should(Succeed())

		By("changing the Gateway listener port updates the LBC listener")
		Eventually(func(g Gomega) {
			cur := &gwv1.Gateway{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: nlbNS, Name: "portchg-gw"}, cur)).To(Succeed())
			cur.Spec.Listeners[0].Port = 6380
			g.Expect(k8sClient.Update(ctx, cur)).To(Succeed())
		}, 10*time.Second, time.Second).Should(Succeed())

		Eventually(func(g Gomega) {
			lbc := ownedLBC(g, gw)
			g.Expect(lbc).NotTo(BeNil())
			g.Expect(lbc.Spec.Listeners).To(HaveLen(1))
			g.Expect(lbc.Spec.Listeners[0].ProtocolPort).To(Equal(int32(6380)), "listener moved to the new port")
		}, 30*time.Second, time.Second).Should(Succeed())
	})

	// Service controller: "should create LoadBalancer with correct attributes
	// from annotations" — here the attributes come from VKSGatewayPolicy.
	It("maps LB-level attributes from VKSGatewayPolicy onto the LBC", func() {
		ensureNLBClass()
		pol := &gwv1alpha1.VKSGatewayPolicy{
			ObjectMeta: metav1.ObjectMeta{Namespace: nlbNS, Name: "attrs-pol"},
			Spec: gwv1alpha1.VKSGatewayPolicySpec{
				TargetRefs: []gwv1alpha1.LocalPolicyTargetReferenceWithSectionName{gwTargetRef("attrs-gw")},
				LoadBalancerSpec: &gwv1alpha1.VKSLoadBalancerSpec{
					Scheme: ptr.To("Internet"),
					Tags:   map[string]string{"env": "test"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, pol)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, pol) })

		svc := mkNodePortSvc("attrs", 8080)
		Expect(k8sClient.Create(ctx, svc)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, svc) })
		gw := mkTCPGateway("attrs-gw", 8080)
		Expect(k8sClient.Create(ctx, gw)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, gw) })
		route := mkTCPRoute("attrs-route", "attrs-gw", "attrs", 8080)
		Expect(k8sClient.Create(ctx, route)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, route) })

		Eventually(func(g Gomega) {
			lbc := ownedLBC(g, gw)
			g.Expect(lbc).NotTo(BeNil())
			g.Expect(lbc.Spec.Scheme).NotTo(BeNil())
			g.Expect(string(*lbc.Spec.Scheme)).To(Equal("Internet"))
			g.Expect(lbc.Spec.Tags).To(HaveKeyWithValue("env", "test"))
		}, 30*time.Second, time.Second).Should(Succeed())
	})

	// Real-client-IP use case: VKSBackendPolicy.proxyProtocol switches the TCP
	// pool to the PROXY pool protocol so an L4 backend (e.g. HAProxy ingress)
	// can recover the client IP.
	It("switches the pool to PROXY protocol when VKSBackendPolicy.proxyProtocol is set", func() {
		ensureNLBClass()
		bp := &gwv1alpha1.VKSBackendPolicy{
			ObjectMeta: metav1.ObjectMeta{Namespace: nlbNS, Name: "proxy-bp"},
			Spec: gwv1alpha1.VKSBackendPolicySpec{
				TargetRefs:    []gwv1alpha1.LocalPolicyTargetReference{{Group: "", Kind: "Service", Name: "proxy-svc"}},
				ProxyProtocol: ptr.To(true),
			},
		}
		Expect(k8sClient.Create(ctx, bp)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, bp) })

		svc := mkNodePortSvc("proxy-svc", 8080)
		Expect(k8sClient.Create(ctx, svc)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, svc) })
		gw := mkTCPGateway("proxy-gw", 8080)
		Expect(k8sClient.Create(ctx, gw)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, gw) })
		route := mkTCPRoute("proxy-route", "proxy-gw", "proxy-svc", 8080)
		Expect(k8sClient.Create(ctx, route)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, route) })

		Eventually(func(g Gomega) {
			lbc := ownedLBC(g, gw)
			g.Expect(lbc).NotTo(BeNil())
			g.Expect(lbc.Spec.Pools).To(HaveLen(1))
			g.Expect(lbc.Spec.Pools[0].Protocol).To(Equal(v2.PoolProtocolProxy))
		}, 30*time.Second, time.Second).Should(Succeed())
	})

	// Ingress controller: "When create ingress with prefer subnet ID annotation
	// ... should create load balancer in the specified subnet".
	It("creates the LBC in the subnet from VKSGatewayPolicy.loadBalancerSpec.subnetId", func() {
		ensureNLBClass()
		// Create the policy BEFORE the Gateway: subnetId is a create-only field.
		pol := &gwv1alpha1.VKSGatewayPolicy{
			ObjectMeta: metav1.ObjectMeta{Namespace: nlbNS, Name: "subnet-pol"},
			Spec: gwv1alpha1.VKSGatewayPolicySpec{
				TargetRefs:       []gwv1alpha1.LocalPolicyTargetReferenceWithSectionName{gwTargetRef("subnet-gw")},
				LoadBalancerSpec: &gwv1alpha1.VKSLoadBalancerSpec{SubnetID: ptr.To("subnet-custom-123")},
			},
		}
		Expect(k8sClient.Create(ctx, pol)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, pol) })

		svc := mkNodePortSvc("subnet", 7000)
		Expect(k8sClient.Create(ctx, svc)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, svc) })
		gw := mkTCPGateway("subnet-gw", 7000)
		Expect(k8sClient.Create(ctx, gw)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, gw) })
		route := mkTCPRoute("subnet-route", "subnet-gw", "subnet", 7000)
		Expect(k8sClient.Create(ctx, route)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, route) })

		Eventually(func(g Gomega) {
			lbc := ownedLBC(g, gw)
			g.Expect(lbc).NotTo(BeNil())
			g.Expect(lbc.Spec.Type).To(Equal(v2.LoadBalancerTypeLayer4))
			g.Expect(lbc.Spec.SubnetId).To(Equal("subnet-custom-123"))
		}, 30*time.Second, time.Second).Should(Succeed())
	})

	// Service controller: scheme=InterVPC + private-subnet-id + private-zone-id.
	// The InterVPC scheme needs the client subnet (in another VPC) and its zone;
	// all three must reach the LBC verbatim, same values the Service controller
	// would write.
	It("maps the InterVPC scheme, private subnet and private zone onto the LBC", func() {
		ensureNLBClass()
		// scheme/private-subnet/private-zone are create-only: policy first.
		pol := &gwv1alpha1.VKSGatewayPolicy{
			ObjectMeta: metav1.ObjectMeta{Namespace: nlbNS, Name: "intervpc-pol"},
			Spec: gwv1alpha1.VKSGatewayPolicySpec{
				TargetRefs: []gwv1alpha1.LocalPolicyTargetReferenceWithSectionName{gwTargetRef("intervpc-gw")},
				LoadBalancerSpec: &gwv1alpha1.VKSLoadBalancerSpec{
					Scheme:          ptr.To("InterVPC"),
					PrivateSubnetID: ptr.To("subnet-client-vpc"),
					PrivateZoneID:   ptr.To("HCM03-1A"),
				},
			},
		}
		Expect(k8sClient.Create(ctx, pol)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, pol) })

		svc := mkNodePortSvc("intervpc", 7100)
		Expect(k8sClient.Create(ctx, svc)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, svc) })
		gw := mkTCPGateway("intervpc-gw", 7100)
		Expect(k8sClient.Create(ctx, gw)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, gw) })
		route := mkTCPRoute("intervpc-route", "intervpc-gw", "intervpc", 7100)
		Expect(k8sClient.Create(ctx, route)).To(Succeed())
		DeferCleanup(func() { _ = k8sClient.Delete(ctx, route) })

		Eventually(func(g Gomega) {
			lbc := ownedLBC(g, gw)
			g.Expect(lbc).NotTo(BeNil())
			g.Expect(lbc.Spec.Scheme).NotTo(BeNil())
			g.Expect(string(*lbc.Spec.Scheme)).To(Equal("InterVPC"))
			g.Expect(lbc.Spec.PrivateSubnetId).To(HaveValue(Equal("subnet-client-vpc")))
			g.Expect(lbc.Spec.PrivateZoneId).NotTo(BeNil())
			g.Expect(string(*lbc.Spec.PrivateZoneId)).To(Equal("HCM03-1A"))
		}, 30*time.Second, time.Second).Should(Succeed())
	})
})
