/*
Copyright 2026.
*/

package alb

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
)

const (
	pollTimeout  = 30 * time.Second
	pollInterval = 250 * time.Millisecond
)

// makeNamespace creates a fresh namespace and registers a cleanup hook.
func makeNamespace(name string) {
	GinkgoHelper()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	DeferCleanup(func() {
		_ = k8sClient.Delete(ctx, ns, &client.DeleteOptions{GracePeriodSeconds: ptr.To[int64](0)})
	})
}

// makeNodePortService creates a Service with type=NodePort backed by no real
// pods (envtest doesn't run kube-proxy or schedule pods); the endpoint resolver
// reads NodePort + Service.spec.ports so this is enough for build_pool to
// produce a valid pool spec.
//
//nolint:unparam // result kept on the API for parity with apply* helpers
func makeNodePortService(ns, name string, port, nodePort int32) *corev1.Service {
	GinkgoHelper()
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeNodePort,
			Selector: map[string]string{"app": name},
			Ports: []corev1.ServicePort{{
				Port:       port,
				TargetPort: intstr.FromInt(int(port)),
				NodePort:   nodePort,
				Protocol:   corev1.ProtocolTCP,
			}},
		},
	}
	Expect(k8sClient.Create(ctx, svc)).To(Succeed())
	return svc
}

// applyGateway builds + creates a Gateway with the given listeners.
func applyGateway(ns, name string, listeners ...gwv1.Listener) *gwv1.Gateway {
	GinkgoHelper()
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: gwv1.GatewaySpec{
			GatewayClassName: "vngcloud-alb",
			Listeners:        listeners,
		},
	}
	Expect(k8sClient.Create(ctx, gw)).To(Succeed())
	return gw
}

// httpListener returns a gwv1.Listener with HTTP protocol.
func httpListener(name string, port int32) gwv1.Listener {
	return gwv1.Listener{
		Name:     gwv1.SectionName(name),
		Protocol: gwv1.HTTPProtocolType,
		Port:     gwv1.PortNumber(port),
	}
}

// applyHTTPRoute creates an HTTPRoute with a single rule pointing at the
// supplied backendRefs (each (svcName, port[, weight])). hostnames may be empty.
func applyHTTPRoute(ns, name, parent string, hostnames []string, backends ...routeBackend) *gwv1.HTTPRoute {
	GinkgoHelper()
	hosts := make([]gwv1.Hostname, 0, len(hostnames))
	for _, h := range hostnames {
		hosts = append(hosts, gwv1.Hostname(h))
	}
	refs := make([]gwv1.HTTPBackendRef, 0, len(backends))
	for _, b := range backends {
		br := gwv1.HTTPBackendRef{
			BackendRef: gwv1.BackendRef{
				BackendObjectReference: gwv1.BackendObjectReference{
					Name: gwv1.ObjectName(b.svc),
					Port: ptr.To(gwv1.PortNumber(b.port)),
				},
			},
		}
		if b.weight != nil {
			br.Weight = b.weight
		}
		if b.namespace != "" {
			ns := gwv1.Namespace(b.namespace)
			br.Namespace = &ns
		}
		refs = append(refs, br)
	}
	rt := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: gwv1.HTTPRouteSpec{
			CommonRouteSpec: gwv1.CommonRouteSpec{
				ParentRefs: []gwv1.ParentReference{{Name: gwv1.ObjectName(parent)}},
			},
			Hostnames: hosts,
			Rules: []gwv1.HTTPRouteRule{{
				Matches: []gwv1.HTTPRouteMatch{{
					Path: &gwv1.HTTPPathMatch{Type: ptr.To(gwv1.PathMatchPathPrefix), Value: ptr.To("/")},
				}},
				BackendRefs: refs,
			}},
		},
	}
	Expect(k8sClient.Create(ctx, rt)).To(Succeed())
	return rt
}

// routeBackend is a small DSL for applyHTTPRoute callers.
type routeBackend struct {
	svc       string
	port      int32
	weight    *int32
	namespace string
}

// b is a shorthand constructor for a routeBackend.
//
//nolint:unparam // port kept parameterized for future per-spec port variation
func b(svc string, port int32) routeBackend { return routeBackend{svc: svc, port: port} }

// expectOwnedLBC polls until exactly one LoadBalancerConfig labeled with the
// Gateway's owner labels exists; returns it. The Gateway-controller creates this.
func expectOwnedLBC(gw *gwv1.Gateway) *vksv1alpha1.LoadBalancerConfig {
	GinkgoHelper()
	var out *vksv1alpha1.LoadBalancerConfig
	Eventually(func(g Gomega) {
		list := &vksv1alpha1.LoadBalancerConfigList{}
		g.Expect(k8sClient.List(ctx, list,
			client.InNamespace(gw.Namespace),
			client.MatchingLabels{
				domain.LabelOwnerResourceName: gw.Name,
				domain.LabelOwnerResourceUid:  string(gw.UID),
			})).To(Succeed())
		g.Expect(list.Items).To(HaveLen(1), "expected exactly one Gateway-owned LBC")
		out = &list.Items[0]
	}, pollTimeout, pollInterval).Should(Succeed())
	return out
}

// gatewayHasFinalizer returns true once the controller has added its finalizer.
func gatewayHasFinalizer(gw *gwv1.Gateway) bool {
	fresh := &gwv1.Gateway{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}, fresh); err != nil {
		return false
	}
	for _, f := range fresh.Finalizers {
		if f == domain.GatewayFinalizer {
			return true
		}
	}
	return false
}

// freshGateway re-Gets the Gateway and returns the live copy.
func freshGateway(gw *gwv1.Gateway) *gwv1.Gateway {
	out := &gwv1.Gateway{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}, out)).To(Succeed())
	return out
}

// listenerByName picks the LBC listener whose Name matches the vngcloud form
// produced by build_listener.vngcloudListenerName(rawName).
//
//nolint:unparam // name kept parameterized for future per-listener tests
func listenerByName(lbc *vksv1alpha1.LoadBalancerConfig, vngcloudName string) *vksv1alpha1.Listener {
	for i := range lbc.Spec.Listeners {
		if lbc.Spec.Listeners[i].Name == vngcloudName {
			return &lbc.Spec.Listeners[i]
		}
	}
	return nil
}

// updateGateway re-fetches the latest Gateway, calls mutate(), and retries
// the Update on 409 Conflict for up to pollTimeout. Avoids the common
// "object was modified" failure when the controller is reconciling at the
// same time the test mutates the spec.
func updateGateway(gw *gwv1.Gateway, mutate func(*gwv1.Gateway)) {
	GinkgoHelper()
	Eventually(func() error {
		fresh := &gwv1.Gateway{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}, fresh); err != nil {
			return err
		}
		mutate(fresh)
		return k8sClient.Update(ctx, fresh)
	}, pollTimeout, pollInterval).Should(Succeed())
}

// updateHTTPRoute is the HTTPRoute equivalent of updateGateway.
func updateHTTPRoute(rt *gwv1.HTTPRoute, mutate func(*gwv1.HTTPRoute)) {
	GinkgoHelper()
	Eventually(func() error {
		fresh := &gwv1.HTTPRoute{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: rt.Namespace, Name: rt.Name}, fresh); err != nil {
			return err
		}
		mutate(fresh)
		return k8sClient.Update(ctx, fresh)
	}, pollTimeout, pollInterval).Should(Succeed())
}

// expectGatewayDeleted waits until the Gateway and its owned LBC are both gone.
func expectGatewayDeleted(gw *gwv1.Gateway) {
	GinkgoHelper()
	Eventually(func() bool {
		fresh := &gwv1.Gateway{}
		err := k8sClient.Get(ctx, types.NamespacedName{Namespace: gw.Namespace, Name: gw.Name}, fresh)
		return apierrors.IsNotFound(err)
	}, pollTimeout*2, pollInterval).Should(BeTrue(), "Gateway never deleted")

	Eventually(func() int {
		list := &vksv1alpha1.LoadBalancerConfigList{}
		_ = k8sClient.List(ctx, list,
			client.InNamespace(gw.Namespace),
			client.MatchingLabels{domain.LabelOwnerResourceUid: string(gw.UID)})
		return len(list.Items)
	}, pollTimeout*2, pollInterval).Should(Equal(0), "owned LBC never cleaned up")
}
