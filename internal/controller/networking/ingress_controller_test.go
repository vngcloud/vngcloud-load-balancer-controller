/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package networking

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/entity"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/common"
	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/vngcloud_repo/vngcloud_mocks"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/annotations"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

const (
	timeout              = time.Second * 5
	interval             = time.Millisecond * 250
	testServiceNameGogsf = "test-service-gogsf"
	testServiceFoo       = "service-foo"
)

var _ = Describe("Ingress Controller", func() {
	BeforeEach(func() {
		// Drain anything a previous spec's AfterEach left behind, then
		// WAIT for the K8s API server to finish the deletes (finalizer
		// chains can be in flight from a failed spec). Without the
		// wait, the next It would hit "object is being deleted" when
		// it tries to Create with the same name. After the K8s side is
		// clean, reset the in-memory mock VNGCloud state.
		cleanupAllEndpoints()
		cleanupAllLBCs()
		cleanupAllNSGs()
		cleanupAllIngreses()
		cleanupAllServices()

		expectNoIngresses()
		expectNoServices()
		expectNoLBCs()
		expectNoNSGs()
		expectNoEndpoints()
		expectNoLoadBalancers()
		expectNoSecurityGroups()

		vngcloudRepo.Reset()
	})

	AfterEach(func() {
		// Cleanup must run BEFORE the assertions below; otherwise an
		// assertion failure (Eventually timeout) short-circuits the
		// AfterEach and leaves resources around for the next spec.
		cleanupAllEndpoints()
		cleanupAllLBCs()
		cleanupAllNSGs()
		cleanupAllIngreses()
		cleanupAllServices()

		expectNoLoadBalancers()
		expectNoSecurityGroups()
		expectNoIngresses()
		expectNoServices()
		expectNoLBCs()
		expectNoNSGs()
		expectNoEndpoints()
	})

	Context("When create ingress with default annotation default", func() {
		It("created load balancer should have specific attribute", func() {

			serviceName := testServiceNameGogsf
			namespace := testDefaultNamespace
			ingressName := testServiceNameGogsf

			// Create endpoint
			endpoint := newEndpointResource(serviceName, namespace)
			Expect(k8sClient.Create(ctx, endpoint)).Should(Succeed())

			// Create Service
			service := newServiceNodePortResource(serviceName, namespace)
			service.Spec.Ports = []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromInt(80), Protocol: corev1.ProtocolTCP, NodePort: 30000},
			}
			Expect(k8sClient.Create(ctx, service)).Should(Succeed())

			// Create Ingress
			ingress := newIngressResource(ingressName, namespace)
			Expect(ingress).NotTo(BeNil())
			ingress.Spec.DefaultBackend = &networkingv1.IngressBackend{
				Service: &networkingv1.IngressServiceBackend{
					Name: serviceName,
					Port: networkingv1.ServiceBackendPort{
						Number: 80,
					},
				},
			}
			Expect(k8sClient.Create(ctx, ingress)).Should(Succeed())

			// Verify LoadBalancer was created in mock repo
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())
				g.Expect(loadbalancer.Name).Should(Equal("vks-k8s-000000-default-test-servi-bea48"))
				g.Expect(loadbalancer.LoadBalancerSchema).Should(Equal(mockConfig.LoadBalancerOpts.DefaultScheme))
				g.Expect(loadbalancer.PackageID).Should(Equal(vngcloud_mocks.MockL7PackageId))
				g.Expect(loadbalancer.BackendSubnetID).Should(BeElementOf(vngcloud_mocks.NodeSubnetIDs))
				g.Expect(loadbalancer.ZoneID).Should(Equal(vngcloud_mocks.MapSubnetToZone[loadbalancer.BackendSubnetID]))
				g.Expect(loadbalancer.Type).Should(Equal(string(loadbalancerv2.LoadBalancerTypeLayer7)))
				g.Expect(loadbalancer.PrivateSubnetCidr).Should(Equal(vngcloud_mocks.MapSubnetToCIDR[loadbalancer.BackendSubnetID]))

				// check pool
				pools, err := vngcloudRepo.ListPool(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(pools).ShouldNot(BeNil())
				g.Expect((pools.Items)).Should(HaveLen(1)) // number of pool
				for _, pool := range pools.Items {
					g.Expect(pool.Name).Should(BeElementOf(
						domain.DEFAULT_NAME_DEFAULT_POOL,
						"vks-k8s-000000-default-test-serv-fbaa0-TCP-80"))
					g.Expect(pool.LoadBalanceMethod).Should(Equal("ROUND_ROBIN"))
					g.Expect(pool.Protocol).Should(Equal("HTTP"))
					g.Expect(pool.Stickiness).Should(BeFalse())
					g.Expect(pool.TLSEncryption).Should(BeFalse())

					g.Expect(pool.HealthMonitor).ShouldNot(BeNil())
					g.Expect(pool.HealthMonitor.HealthCheckProtocol).Should(Equal("TCP"))
					g.Expect(pool.HealthMonitor.HealthyThreshold).Should(Equal(3))
					g.Expect(pool.HealthMonitor.UnhealthyThreshold).Should(Equal(3))
					g.Expect(pool.HealthMonitor.Interval).Should(Equal(30))
					g.Expect(pool.HealthMonitor.Timeout).Should(Equal(5))

					g.Expect(pool.Members).ShouldNot(BeNil())
					g.Expect((pool.Members.Items)).Should(HaveLen(4)) // number of member in pool = number of node or number of endpoint
					for _, member := range pool.Members.Items {
						g.Expect(member.ProtocolPort).Should(Equal(30000))
						g.Expect(member.MonitorPort).Should(Equal(30000))
						g.Expect(member.Address).Should(BeElementOf(
							vngcloud_mocks.MockNode1.Status.Addresses[0].Address,
							vngcloud_mocks.MockNode2.Status.Addresses[0].Address,
							vngcloud_mocks.MockNode3.Status.Addresses[0].Address,
							vngcloud_mocks.MockNode4.Status.Addresses[0].Address))
					}
				}

				// check listener
				listeners, err := vngcloudRepo.ListListenerOfLB(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(listeners).ShouldNot(BeNil())
				g.Expect((listeners.Items)).Should(HaveLen(1)) // number of listener
				for _, listener := range listeners.Items {
					g.Expect(listener.Protocol).Should(Equal("HTTP"))
					g.Expect(listener.ProtocolPort).Should(Equal(80))
					g.Expect(listener.AllowedCidrs).Should(Equal("0.0.0.0/0"))
					g.Expect(listener.DefaultPoolId).Should(Equal(pools.Items[0].UUID))
					g.Expect(listener.DefaultPoolName).Should(Equal(pools.Items[0].Name))
					g.Expect(listener.Name).Should(Equal(domain.DEFAULT_HTTP_LISTENER_NAME))
					g.Expect(listener.TimeoutClient).Should(Equal(50))
					g.Expect(listener.TimeoutConnection).Should(Equal(5))
					g.Expect(listener.TimeoutMember).Should(Equal(50))
				}

				listNsg, err := listNsgByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(listNsg.Items).Should(HaveLen(1))

				nsg := &listNsg.Items[0]
				g.Expect(nsg.Status.ManagedSecurityGroup).ShouldNot(BeNil())
				g.Expect(nsg.Status.ManagedSecurityGroup.Id).ShouldNot(BeNil())

				secgroupId := *nsg.Status.ManagedSecurityGroup.Id
				secgroup, err := vngcloudRepo.GetSecurityGroup(ctx, secgroupId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(secgroup).ShouldNot(BeNil())
				g.Expect(secgroup.Name).Should(Equal(nsg.Spec.ManagedSecurityGroup.Name))
			}, timeout, interval).Should(Succeed())

			// Cleanup
			Expect(k8sClient.Delete(ctx, ingress)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, service)).Should(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, endpoint))).Should(Succeed())
		})
	})

	Context("When create ingress without default backend, only 1 rule", func() {
		It("should create load balancer with correct attributes and update when rule changes", func() {

			serviceName := testServiceNameGogsf
			namespace := testDefaultNamespace
			ingressName := testServiceNameGogsf

			// Create endpoint
			endpoint := newEndpointResource(serviceName, namespace)
			endpoint.Subsets = []corev1.EndpointSubset{
				{
					Addresses: []corev1.EndpointAddress{
						{IP: vngcloud_mocks.MockNode1.Status.Addresses[0].Address},
						{IP: vngcloud_mocks.MockNode2.Status.Addresses[0].Address},
						{IP: vngcloud_mocks.MockNode3.Status.Addresses[0].Address},
						{IP: vngcloud_mocks.MockNode4.Status.Addresses[0].Address},
					},
					Ports: []corev1.EndpointPort{
						{Name: "http", Port: 80, Protocol: corev1.ProtocolTCP},
						{Name: "https", Port: 81, Protocol: corev1.ProtocolTCP},
					},
				},
			}
			Expect(k8sClient.Create(ctx, endpoint)).Should(Succeed())

			// Create Service
			service := newServiceNodePortResource(serviceName, namespace)
			service.Spec.Ports = []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromInt(80), Protocol: corev1.ProtocolTCP, NodePort: 30000},
				{Name: "https", Port: 443, TargetPort: intstr.FromInt(81), Protocol: corev1.ProtocolTCP, NodePort: 30001},
			}
			Expect(k8sClient.Create(ctx, service)).Should(Succeed())

			// Create Ingress
			ingress := newIngressResource(ingressName, namespace)
			Expect(ingress).NotTo(BeNil())
			ingress.Spec.DefaultBackend = nil
			ingress.Spec.Rules = []networkingv1.IngressRule{
				{
					Host: "test.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									PathType: ptr.To(networkingv1.PathTypePrefix),
									Path:     "/",
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: serviceName,
											Port: networkingv1.ServiceBackendPort{Number: 80},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, ingress)).Should(Succeed())

			// Verify LoadBalancer was created in mock repo
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())
				g.Expect(loadbalancer.Name).Should(Equal("vks-k8s-000000-default-test-servi-bea48"))
				g.Expect(loadbalancer.LoadBalancerSchema).Should(Equal(mockConfig.LoadBalancerOpts.DefaultScheme))
				g.Expect(loadbalancer.PackageID).Should(Equal(vngcloud_mocks.MockL7PackageId))
				g.Expect(loadbalancer.BackendSubnetID).Should(BeElementOf(vngcloud_mocks.NodeSubnetIDs))
				g.Expect(loadbalancer.ZoneID).Should(Equal(vngcloud_mocks.MapSubnetToZone[loadbalancer.BackendSubnetID]))
				g.Expect(loadbalancer.Type).Should(Equal(string(loadbalancerv2.LoadBalancerTypeLayer7)))
				g.Expect(loadbalancer.PrivateSubnetCidr).Should(Equal(vngcloud_mocks.MapSubnetToCIDR[loadbalancer.BackendSubnetID]))

				// check pool
				pools, err := vngcloudRepo.ListPool(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(pools).ShouldNot(BeNil())
				g.Expect((pools.Items)).Should(HaveLen(1))
				for _, pool := range pools.Items {
					g.Expect(pool.Name).Should(BeElementOf(
						"vks-bea48-default-test-service-gogsf-80"))
					g.Expect(pool.LoadBalanceMethod).Should(Equal("ROUND_ROBIN"))
					g.Expect(pool.Protocol).Should(Equal("HTTP"))
					g.Expect(pool.Stickiness).Should(BeFalse())
					g.Expect(pool.TLSEncryption).Should(BeFalse())

					g.Expect(pool.HealthMonitor).ShouldNot(BeNil())
					g.Expect(pool.HealthMonitor.HealthCheckProtocol).Should(Equal("TCP"))
					g.Expect(pool.HealthMonitor.HealthyThreshold).Should(Equal(3))
					g.Expect(pool.HealthMonitor.UnhealthyThreshold).Should(Equal(3))
					g.Expect(pool.HealthMonitor.Interval).Should(Equal(30))
					g.Expect(pool.HealthMonitor.Timeout).Should(Equal(5))

					g.Expect(pool.Members).ShouldNot(BeNil())
					g.Expect((pool.Members.Items)).Should(HaveLen(4)) // number of member in pool = number of node or number of endpoint
					for _, member := range pool.Members.Items {
						g.Expect(member.ProtocolPort).Should(Equal(30000))
						g.Expect(member.MonitorPort).Should(Equal(30000))
						g.Expect(member.Address).Should(BeElementOf(
							vngcloud_mocks.MockNode1.Status.Addresses[0].Address,
							vngcloud_mocks.MockNode2.Status.Addresses[0].Address,
							vngcloud_mocks.MockNode3.Status.Addresses[0].Address,
							vngcloud_mocks.MockNode4.Status.Addresses[0].Address))
					}
				}

				// check listener
				listeners, err := vngcloudRepo.ListListenerOfLB(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(listeners).ShouldNot(BeNil())
				g.Expect((listeners.Items)).Should(HaveLen(1))
				for _, listener := range listeners.Items {
					g.Expect(listener.Protocol).Should(Equal("HTTP"))
					g.Expect(listener.ProtocolPort).Should(Equal(80))
					g.Expect(listener.AllowedCidrs).Should(Equal("0.0.0.0/0"))
					g.Expect(listener.DefaultPoolId).Should(Equal(""))
					g.Expect(listener.DefaultPoolName).Should(Equal(""))
					g.Expect(listener.Name).Should(Equal(domain.DEFAULT_HTTP_LISTENER_NAME))
					g.Expect(listener.TimeoutClient).Should(Equal(50))
					g.Expect(listener.TimeoutConnection).Should(Equal(5))
					g.Expect(listener.TimeoutMember).Should(Equal(50))

					// check policy
					policies, err := vngcloudRepo.ListPolicyOfListener(ctx, loadbalancer.UUID, listener.UUID)
					g.Expect(err).ShouldNot(HaveOccurred())
					g.Expect(policies).ShouldNot(BeNil())
					g.Expect((policies.Items)).Should(HaveLen(1))
					for _, policy := range policies.Items {
						g.Expect(policy.Name).Should(Equal("vks-bea48-false-r0-p0"))
						g.Expect(policy.Action).Should(Equal(string(loadbalancerv2.PolicyActionREDIRECTTOPOOL)))
					}
				}

				listNsg, err := listNsgByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(listNsg.Items).Should(HaveLen(1))

				nsg := &listNsg.Items[0]
				g.Expect(nsg.Status.ManagedSecurityGroup).ShouldNot(BeNil())
				g.Expect(nsg.Status.ManagedSecurityGroup.Id).ShouldNot(BeNil())

				secgroupId := *nsg.Status.ManagedSecurityGroup.Id
				secgroup, err := vngcloudRepo.GetSecurityGroup(ctx, secgroupId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(secgroup).ShouldNot(BeNil())
				g.Expect(secgroup.Name).Should(Equal(nsg.Spec.ManagedSecurityGroup.Name))
			}, timeout, interval).Should(Succeed())

			// Update rule to new service port
			Eventually(func(g Gomega) {
				object := &networkingv1.Ingress{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: ingressName, Namespace: namespace}, object)).Should(Succeed())
				object.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Port.Number = 443
				g.Expect(k8sClient.Update(ctx, object)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			// Verify LoadBalancer after update
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())
				g.Expect(loadbalancer.Name).Should(Equal("vks-k8s-000000-default-test-servi-bea48"))
				g.Expect(loadbalancer.LoadBalancerSchema).Should(Equal(mockConfig.LoadBalancerOpts.DefaultScheme))
				g.Expect(loadbalancer.PackageID).Should(Equal(vngcloud_mocks.MockL7PackageId))
				g.Expect(loadbalancer.BackendSubnetID).Should(BeElementOf(vngcloud_mocks.NodeSubnetIDs))
				g.Expect(loadbalancer.ZoneID).Should(Equal(vngcloud_mocks.MapSubnetToZone[loadbalancer.BackendSubnetID]))
				g.Expect(loadbalancer.Type).Should(Equal(string(loadbalancerv2.LoadBalancerTypeLayer7)))
				g.Expect(loadbalancer.PrivateSubnetCidr).Should(Equal(vngcloud_mocks.MapSubnetToCIDR[loadbalancer.BackendSubnetID]))

				// check pool after update
				pools, err := vngcloudRepo.ListPool(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(pools).ShouldNot(BeNil())
				g.Expect((pools.Items)).Should(HaveLen(1))
				for _, pool := range pools.Items {
					g.Expect(pool.Name).Should(BeElementOf(
						"vks-bea48-default-test-service-gogsf-443"))
					g.Expect(pool.LoadBalanceMethod).Should(Equal("ROUND_ROBIN"))
					g.Expect(pool.Protocol).Should(Equal("HTTP"))
					g.Expect(pool.Stickiness).Should(BeFalse())
					g.Expect(pool.TLSEncryption).Should(BeFalse())

					g.Expect(pool.HealthMonitor).ShouldNot(BeNil())
					g.Expect(pool.HealthMonitor.HealthCheckProtocol).Should(Equal("TCP"))
					g.Expect(pool.HealthMonitor.HealthyThreshold).Should(Equal(3))
					g.Expect(pool.HealthMonitor.UnhealthyThreshold).Should(Equal(3))
					g.Expect(pool.HealthMonitor.Interval).Should(Equal(30))
					g.Expect(pool.HealthMonitor.Timeout).Should(Equal(5))

					g.Expect(pool.Members).ShouldNot(BeNil())
					g.Expect((pool.Members.Items)).Should(HaveLen(4))
					for _, member := range pool.Members.Items {
						g.Expect(member.ProtocolPort).Should(Equal(30001))
						g.Expect(member.MonitorPort).Should(Equal(30001))
						g.Expect(member.Address).Should(BeElementOf(
							vngcloud_mocks.MockNode1.Status.Addresses[0].Address,
							vngcloud_mocks.MockNode2.Status.Addresses[0].Address,
							vngcloud_mocks.MockNode3.Status.Addresses[0].Address,
							vngcloud_mocks.MockNode4.Status.Addresses[0].Address))
					}
				}

				// check listener after update
				listeners, err := vngcloudRepo.ListListenerOfLB(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(listeners).ShouldNot(BeNil())
				g.Expect((listeners.Items)).Should(HaveLen(1))
				for _, listener := range listeners.Items {
					g.Expect(listener.Protocol).Should(Equal("HTTP"))
					g.Expect(listener.ProtocolPort).Should(Equal(80))
					g.Expect(listener.AllowedCidrs).Should(Equal("0.0.0.0/0"))
					g.Expect(listener.DefaultPoolId).Should(Equal(""))
					g.Expect(listener.DefaultPoolName).Should(Equal(""))
					g.Expect(listener.Name).Should(Equal(domain.DEFAULT_HTTP_LISTENER_NAME))
					g.Expect(listener.TimeoutClient).Should(Equal(50))
					g.Expect(listener.TimeoutConnection).Should(Equal(5))
					g.Expect(listener.TimeoutMember).Should(Equal(50))

					// check policy
					policies, err := vngcloudRepo.ListPolicyOfListener(ctx, loadbalancer.UUID, listener.UUID)
					g.Expect(err).ShouldNot(HaveOccurred())
					g.Expect(policies).ShouldNot(BeNil())
					g.Expect((policies.Items)).Should(HaveLen(1))
					for _, policy := range policies.Items {
						g.Expect(policy.Name).Should(Equal("vks-bea48-false-r0-p0"))
						g.Expect(policy.Action).Should(Equal(string(loadbalancerv2.PolicyActionREDIRECTTOPOOL)))
					}
				}
			}, timeout, interval).Should(Succeed())

			// Cleanup
			Expect(k8sClient.Delete(ctx, ingress)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, service)).Should(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, endpoint))).Should(Succeed())
		})
	})

	Context("When create ingress with both default backend and rules using port names", func() {
		It("should handle port name resolution and target type changes", func() {

			serviceName := testServiceNameGogsf
			namespace := testDefaultNamespace
			ingressName := testServiceNameGogsf

			// Create endpoint with multiple subsets
			endpoint := newEndpointResource(serviceName, namespace)
			endpoint.Subsets = []corev1.EndpointSubset{
				{
					Addresses: []corev1.EndpointAddress{
						{IP: "100.0.1.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode1.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-1", Kind: "Pod", Namespace: namespace}},
						{IP: "100.0.2.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode2.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-2", Kind: "Pod", Namespace: namespace}},
					},
					NotReadyAddresses: []corev1.EndpointAddress{
						{IP: "100.0.3.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode3.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-3", Kind: "Pod", Namespace: namespace}},
						{IP: "100.0.4.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode4.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-4", Kind: "Pod", Namespace: namespace}},
					},
					Ports: []corev1.EndpointPort{
						{Name: "http", Port: 80},
						{Name: "https", Port: 443},
					},
				},
				{
					Addresses: []corev1.EndpointAddress{
						{IP: "200.0.1.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode1.Name), TargetRef: &corev1.ObjectReference{Name: "fake-pod-1", Kind: "Pod", Namespace: namespace}},
						{IP: "200.0.2.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode2.Name), TargetRef: &corev1.ObjectReference{Name: "fake-pod-2", Kind: "Pod", Namespace: namespace}},
					},
					NotReadyAddresses: []corev1.EndpointAddress{
						{IP: "200.0.3.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode3.Name), TargetRef: &corev1.ObjectReference{Name: "fake-pod-3", Kind: "Pod", Namespace: namespace}},
						{IP: "200.0.4.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode4.Name), TargetRef: &corev1.ObjectReference{Name: "fake-pod-4", Kind: "Pod", Namespace: namespace}},
					},
					Ports: []corev1.EndpointPort{
						{Name: "http", Port: 8080},
						{Name: "https", Port: 6443},
					},
				},
			}
			Expect(k8sClient.Create(ctx, endpoint)).Should(Succeed())

			// Create Service
			service := newServiceNodePortResource(serviceName, namespace)
			service.Spec.Ports = []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromInt(80), Protocol: corev1.ProtocolTCP, NodePort: 30000},
				{Name: "https", Port: 443, TargetPort: intstr.FromInt(81), Protocol: corev1.ProtocolTCP, NodePort: 30001},
			}
			Expect(k8sClient.Create(ctx, service)).Should(Succeed())

			// Create Ingress with both default backend and rules
			ingress := newIngressResource(ingressName, namespace)
			Expect(ingress).NotTo(BeNil())
			ingress.Spec.DefaultBackend = &networkingv1.IngressBackend{
				Service: &networkingv1.IngressServiceBackend{
					Name: serviceName,
					Port: networkingv1.ServiceBackendPort{Number: 80},
				},
			}
			ingress.Spec.Rules = []networkingv1.IngressRule{
				{
					Host: "test.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									PathType: ptr.To(networkingv1.PathTypePrefix),
									Path:     "/",
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: serviceName,
											Port: networkingv1.ServiceBackendPort{Number: 80},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, ingress)).Should(Succeed())

			// Verify LoadBalancer was created
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())
				g.Expect(loadbalancer.Name).Should(Equal("vks-k8s-000000-default-test-servi-bea48"))
				g.Expect(loadbalancer.LoadBalancerSchema).Should(Equal(mockConfig.LoadBalancerOpts.DefaultScheme))
				g.Expect(loadbalancer.PackageID).Should(Equal(vngcloud_mocks.MockL7PackageId))
				g.Expect(loadbalancer.BackendSubnetID).Should(BeElementOf(vngcloud_mocks.NodeSubnetIDs))
				g.Expect(loadbalancer.ZoneID).Should(Equal(vngcloud_mocks.MapSubnetToZone[loadbalancer.BackendSubnetID]))
				g.Expect(loadbalancer.Type).Should(Equal(string(loadbalancerv2.LoadBalancerTypeLayer7)))
				g.Expect(loadbalancer.PrivateSubnetCidr).Should(Equal(vngcloud_mocks.MapSubnetToCIDR[loadbalancer.BackendSubnetID]))

				// check pool
				pools, err := vngcloudRepo.ListPool(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(pools).ShouldNot(BeNil())
				g.Expect((pools.Items)).Should(HaveLen(2))
				for _, pool := range pools.Items {
					g.Expect(pool.Name).Should(BeElementOf(
						domain.DEFAULT_NAME_DEFAULT_POOL,
						"vks-bea48-default-test-service-gogsf-80"))
					g.Expect(pool.LoadBalanceMethod).Should(Equal("ROUND_ROBIN"))
					g.Expect(pool.Protocol).Should(Equal("HTTP"))
					g.Expect(pool.Stickiness).Should(BeFalse())
					g.Expect(pool.TLSEncryption).Should(BeFalse())

					g.Expect(pool.HealthMonitor).ShouldNot(BeNil())
					g.Expect(pool.HealthMonitor.HealthCheckProtocol).Should(Equal("TCP"))
					g.Expect(pool.HealthMonitor.HealthyThreshold).Should(Equal(3))
					g.Expect(pool.HealthMonitor.UnhealthyThreshold).Should(Equal(3))
					g.Expect(pool.HealthMonitor.Interval).Should(Equal(30))
					g.Expect(pool.HealthMonitor.Timeout).Should(Equal(5))

					g.Expect(pool.Members).ShouldNot(BeNil())
					g.Expect((pool.Members.Items)).Should(HaveLen(4))
					for _, member := range pool.Members.Items {
						g.Expect(member.ProtocolPort).Should(Equal(30000))
						g.Expect(member.MonitorPort).Should(Equal(30000))
						g.Expect(member.Address).Should(BeElementOf(
							vngcloud_mocks.MockNode1.Status.Addresses[0].Address,
							vngcloud_mocks.MockNode2.Status.Addresses[0].Address,
							vngcloud_mocks.MockNode3.Status.Addresses[0].Address,
							vngcloud_mocks.MockNode4.Status.Addresses[0].Address))
					}
				}

				// check listener
				listeners, err := vngcloudRepo.ListListenerOfLB(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(listeners).ShouldNot(BeNil())
				g.Expect((listeners.Items)).Should(HaveLen(1))
				for _, listener := range listeners.Items {
					g.Expect(listener.Protocol).Should(Equal("HTTP"))
					g.Expect(listener.ProtocolPort).Should(Equal(80))
					g.Expect(listener.AllowedCidrs).Should(Equal("0.0.0.0/0"))
					g.Expect(listener.DefaultPoolName).Should(Equal(domain.DEFAULT_NAME_DEFAULT_POOL))
					g.Expect(listener.Name).Should(Equal(domain.DEFAULT_HTTP_LISTENER_NAME))
					g.Expect(listener.TimeoutClient).Should(Equal(50))
					g.Expect(listener.TimeoutConnection).Should(Equal(5))
					g.Expect(listener.TimeoutMember).Should(Equal(50))

					// check policy
					policies, err := vngcloudRepo.ListPolicyOfListener(ctx, loadbalancer.UUID, listener.UUID)
					g.Expect(err).ShouldNot(HaveOccurred())
					g.Expect(policies).ShouldNot(BeNil())
					g.Expect((policies.Items)).Should(HaveLen(1))
					for _, policy := range policies.Items {
						g.Expect(policy.Name).Should(ContainSubstring("-false-r0-p0"))
						g.Expect(policy.Action).Should(Equal(string(loadbalancerv2.PolicyActionREDIRECTTOPOOL)))
					}
				}

				listNsg, err := listNsgByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(listNsg.Items).Should(HaveLen(1))

				nsg := &listNsg.Items[0]
				g.Expect(nsg.Status.ManagedSecurityGroup).ShouldNot(BeNil())
				g.Expect(nsg.Status.ManagedSecurityGroup.Id).ShouldNot(BeNil())

				secgroupId := *nsg.Status.ManagedSecurityGroup.Id
				secgroup, err := vngcloudRepo.GetSecurityGroup(ctx, secgroupId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(secgroup).ShouldNot(BeNil())
				g.Expect(secgroup.Name).Should(Equal(nsg.Spec.ManagedSecurityGroup.Name))
			}, timeout, interval).Should(Succeed())

			// Update backend to use port name instead of number (80 -> http)
			Eventually(func(g Gomega) {
				object := &networkingv1.Ingress{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: ingressName, Namespace: namespace}, object)).Should(Succeed())
				object.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Port = networkingv1.ServiceBackendPort{Name: "http"}
				object.Spec.DefaultBackend.Service.Port = networkingv1.ServiceBackendPort{Name: "http"}
				g.Expect(k8sClient.Update(ctx, object)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			// Verify nothing changed (port name resolves to same port number)
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())

				pools, err := vngcloudRepo.ListPool(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect((pools.Items)).Should(HaveLen(2))
				for _, pool := range pools.Items {
					g.Expect(pool.Members.Items).Should(HaveLen(4))
					for _, member := range pool.Members.Items {
						g.Expect(member.ProtocolPort).Should(Equal(30000))
					}
				}
			}, timeout, interval).Should(Succeed())

			// Update annotation to change target type to IP
			Eventually(func(g Gomega) {
				object := &networkingv1.Ingress{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: ingressName, Namespace: namespace}, object)).Should(Succeed())
				if object.Annotations == nil {
					object.Annotations = map[string]string{}
				}
				object.Annotations["vks.vngcloud.vn/target-type"] = "ip"
				g.Expect(k8sClient.Update(ctx, object)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			// Verify pool members changed to IP targets
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())

				pools, err := vngcloudRepo.ListPool(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect((pools.Items)).Should(HaveLen(2))
				for _, pool := range pools.Items {
					g.Expect(pool.Members.Items).Should(HaveLen(8))
					for _, member := range pool.Members.Items {
						g.Expect(member.ProtocolPort).Should(BeElementOf(80, 8080))
						g.Expect(member.MonitorPort).Should(BeElementOf(80, 8080))
						g.Expect(member.Address).Should(BeElementOf(
							"100.0.1.0", "100.0.2.0", "100.0.3.0", "100.0.4.0",
							"200.0.1.0", "200.0.2.0", "200.0.3.0", "200.0.4.0"))
					}
				}
			}, timeout, interval).Should(Succeed())

			// Update backend to use different port name (http -> https)
			Eventually(func(g Gomega) {
				object := &networkingv1.Ingress{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: ingressName, Namespace: namespace}, object)).Should(Succeed())
				object.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Port = networkingv1.ServiceBackendPort{Name: "https"}
				object.Spec.DefaultBackend.Service.Port = networkingv1.ServiceBackendPort{Name: "https"}
				g.Expect(k8sClient.Update(ctx, object)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			// Verify pool members changed to https ports
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())

				pools, err := vngcloudRepo.ListPool(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect((pools.Items)).Should(HaveLen(2))
				for _, pool := range pools.Items {
					g.Expect(pool.Name).Should(BeElementOf(
						domain.DEFAULT_NAME_DEFAULT_POOL,
						"vks-bea48-default-test-service-gogsf-443"))
					g.Expect(pool.Members.Items).Should(HaveLen(8))
					for _, member := range pool.Members.Items {
						g.Expect(member.ProtocolPort).Should(BeElementOf(443, 6443))
						g.Expect(member.MonitorPort).Should(BeElementOf(443, 6443))
						g.Expect(member.Address).Should(BeElementOf(
							"100.0.1.0", "100.0.2.0", "100.0.3.0", "100.0.4.0",
							"200.0.1.0", "200.0.2.0", "200.0.3.0", "200.0.4.0"))
					}
				}
			}, timeout, interval).Should(Succeed())

			// Cleanup
			Expect(k8sClient.Delete(ctx, ingress)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, service)).Should(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, endpoint))).Should(Succeed())
		})
	})

	Context("When create ingress with default annotations of Cilium Native", func() {
		It("should update load balancer tags and security groups correctly", func() {

			serviceName := testServiceNameGogsf
			namespace := testDefaultNamespace
			ingressName := testServiceNameGogsf

			// Configure CNI detector for Cilium Native
			cniDetector.ExpectedCalls = nil
			cniDetector.Calls = nil
			cniDetector.EXPECT().DetectCNIType(mock.Anything).Return(utils.CiliumNativeRouting, nil)

			// Create 2 external security groups for testing
			bigbangSec, err := vngcloudRepo.CreateSecurityGroup(ctx, "bigbang", "the best security group")
			Expect(err).ShouldNot(HaveOccurred())
			Expect(bigbangSec).ShouldNot(BeNil())
			blackpinkSec, err := vngcloudRepo.CreateSecurityGroup(ctx, "blackpink", "the great security group")
			Expect(err).ShouldNot(HaveOccurred())
			Expect(blackpinkSec).ShouldNot(BeNil())

			// Delete security groups when test finishes
			defer func() {
				Eventually(func() bool {
					err = vngcloudRepo.DeleteSecurityGroup(ctx, bigbangSec.Id)
					return err == nil
				}, timeout, interval).Should((BeTrue()), "should delete bigbang security group (wait to detach if in use)")
				Eventually(func() bool {
					err = vngcloudRepo.DeleteSecurityGroup(ctx, blackpinkSec.Id)
					return err == nil
				}, timeout, interval).Should((BeTrue()), "should delete blackpink security group (wait to detach if in use)")
			}()

			// Create endpoint with multiple subsets
			endpoint := newEndpointResource(serviceName, namespace)
			endpoint.Subsets = []corev1.EndpointSubset{
				{
					Addresses: []corev1.EndpointAddress{
						{IP: "100.0.1.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode1.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-1", Kind: "Pod", Namespace: namespace}},
						{IP: "100.0.2.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode2.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-2", Kind: "Pod", Namespace: namespace}},
					},
					NotReadyAddresses: []corev1.EndpointAddress{
						{IP: "100.0.3.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode3.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-3", Kind: "Pod", Namespace: namespace}},
						{IP: "100.0.4.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode4.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-4", Kind: "Pod", Namespace: namespace}},
					},
					Ports: []corev1.EndpointPort{
						{Name: "http", Port: 80},
						{Name: "https", Port: 443},
					},
				},
				{
					Addresses: []corev1.EndpointAddress{
						{IP: "200.0.1.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode1.Name), TargetRef: &corev1.ObjectReference{Name: "fake-pod-1", Kind: "Pod", Namespace: namespace}},
						{IP: "200.0.2.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode2.Name), TargetRef: &corev1.ObjectReference{Name: "fake-pod-2", Kind: "Pod", Namespace: namespace}},
					},
					NotReadyAddresses: []corev1.EndpointAddress{
						{IP: "200.0.3.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode3.Name), TargetRef: &corev1.ObjectReference{Name: "fake-pod-3", Kind: "Pod", Namespace: namespace}},
						{IP: "200.0.4.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode4.Name), TargetRef: &corev1.ObjectReference{Name: "fake-pod-4", Kind: "Pod", Namespace: namespace}},
					},
					Ports: []corev1.EndpointPort{
						{Name: "http", Port: 8080},
						{Name: "https", Port: 6443},
					},
				},
			}
			Expect(k8sClient.Create(ctx, endpoint)).Should(Succeed())

			// Create Service
			service := newServiceNodePortResource(serviceName, namespace)
			service.Spec.Ports = []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromInt(80), Protocol: corev1.ProtocolTCP, NodePort: 30000},
			}
			Expect(k8sClient.Create(ctx, service)).Should(Succeed())

			// Create Ingress with default backend
			ingress := newIngressResource(ingressName, namespace)
			Expect(ingress).NotTo(BeNil())
			ingress.Spec.DefaultBackend = &networkingv1.IngressBackend{
				Service: &networkingv1.IngressServiceBackend{
					Name: serviceName,
					Port: networkingv1.ServiceBackendPort{Number: 80},
				},
			}
			Expect(k8sClient.Create(ctx, ingress)).Should(Succeed())

			// Verify initial load balancer creation with security groups
			var secgroupID string
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())
				g.Expect(loadbalancer.Name).Should(Equal("vks-k8s-000000-default-test-servi-bea48"))

				// Check tags
				tags, err := vngcloudRepo.ListTags(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(tags).ShouldNot(BeNil())
				g.Expect((tags.Items)).Should(HaveLen(3))
				// TODO: verify 3 tags
				// g.Expect(tags.Items[0].Key).Should(Equal(domain.ClusterTagKey))
				// g.Expect(tags.Items[0].Value).Should(Equal(mockConfig.Cluster.ClusterID))

				// Check security groups
				secgroups, err := vngcloudRepo.ListSecurityGroups(ctx)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(secgroups).ShouldNot(BeNil())
				g.Expect((secgroups.Items)).Should(HaveLen(3))
				expectName := []string{"vks-k8s-000000-default-test-servi-bea48", bigbangSec.Name, blackpinkSec.Name}
				for _, secgroup := range secgroups.Items {
					if secgroup.Name == "vks-k8s-000000-default-test-servi-bea48" {
						secgroupID = secgroup.Id
					}
					g.Expect(secgroup.Name).Should(BeElementOf(expectName))
				}

				// Check security group rules
				rules, err := vngcloudRepo.ListSecurityGroupRules(ctx, secgroupID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(rules).ShouldNot(BeNil())
				g.Expect((rules.Items)).Should(HaveLen(9)) // 1 nodeport + 3 x 2 (2 pod ports x 3 subnets (4 nodes in 3 subnets)) + 2 egress rules

				// Check servers have security group attached
				servers, err := vngcloudRepo.ListServerBySecgroupID(ctx, secgroupID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(servers).ShouldNot(BeNil())
				g.Expect((servers.Items)).Should(HaveLen(4))
				for _, server := range servers.Items {
					serverSecgroups := make([]string, 0)
					for _, secgroup := range server.SecGroups {
						serverSecgroups = append(serverSecgroups, secgroup.Uuid)
					}
					g.Expect(serverSecgroups).Should(ContainElement(secgroupID))
				}
			}, timeout, interval).Should(Succeed())

			// Update endpoint to remove second subset
			Eventually(func(g Gomega) {
				object := &corev1.Endpoints{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: serviceName, Namespace: namespace}, object)).Should(Succeed())
				object.Subsets = []corev1.EndpointSubset{
					{
						Addresses: []corev1.EndpointAddress{
							{IP: "100.0.1.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode1.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-1", Kind: "Pod", Namespace: namespace}},
							{IP: "100.0.2.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode2.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-2", Kind: "Pod", Namespace: namespace}},
						},
						NotReadyAddresses: []corev1.EndpointAddress{
							{IP: "100.0.3.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode3.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-3", Kind: "Pod", Namespace: namespace}},
							{IP: "100.0.4.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode4.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-4", Kind: "Pod", Namespace: namespace}},
						},
						Ports: []corev1.EndpointPort{
							{Name: "http", Port: 80},
							{Name: "https", Port: 443},
						},
					},
				}
				g.Expect(k8sClient.Update(ctx, object)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			// Verify endpoint update reflected in security group
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())
				g.Expect(loadbalancer.Name).Should(Equal("vks-k8s-000000-default-test-servi-bea48"))

				// Check tags remain unchanged
				tags, err := vngcloudRepo.ListTags(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(tags).ShouldNot(BeNil())
				g.Expect((tags.Items)).Should(HaveLen(3))
				// TODO
				// g.Expect(tags.Items[0].Key).Should(Equal(domain.ClusterTagKey))
				// g.Expect(tags.Items[0].Value).Should(Equal(mockConfig.Cluster.ClusterID))

				// Check security groups still exist
				secgroups, err := vngcloudRepo.ListSecurityGroups(ctx)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(secgroups).ShouldNot(BeNil())
				g.Expect((secgroups.Items)).Should(HaveLen(3))

				// Check security group rules updated (should be 6 now: 1 nodeport + 3 x 1 pod ports + 2 egress rules)
				rules, err := vngcloudRepo.ListSecurityGroupRules(ctx, secgroupID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(rules).ShouldNot(BeNil())
				g.Expect((rules.Items)).Should(HaveLen(6))

				// Check servers still have security group attached
				servers, err := vngcloudRepo.ListServerBySecgroupID(ctx, secgroupID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(servers).ShouldNot(BeNil())
				g.Expect((servers.Items)).Should(HaveLen(4))
			}, timeout, interval).Should(Succeed())

			// Update annotations to add tags
			Eventually(func(g Gomega) {
				object := &networkingv1.Ingress{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: ingressName, Namespace: namespace}, object)).Should(Succeed())
				if object.Annotations == nil {
					object.Annotations = map[string]string{}
				}
				object.Annotations["vks.vngcloud.vn/tags"] = "tag1=value1,tag2=value2"
				g.Expect(k8sClient.Update(ctx, object)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			// Verify tags were added
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())
				g.Expect(loadbalancer.Name).Should(Equal("vks-k8s-000000-default-test-servi-bea48"))

				// Check tags now include tag1 and tag2 (3 default VKS + 2 custom = 5)
				tags, err := vngcloudRepo.ListTags(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(tags).ShouldNot(BeNil())
				g.Expect((tags.Items)).Should(HaveLen(5))
				tagKeys := make([]string, 0)
				tagValues := make([]string, 0)
				for _, tag := range tags.Items {
					tagKeys = append(tagKeys, tag.Key)
					tagValues = append(tagValues, tag.Value)
				}
				g.Expect(tagKeys).Should(ContainElements(domain.ClusterTagKey, "tag1", "tag2"))
				g.Expect(tagValues).Should(ContainElements(mockConfig.Cluster.ClusterID, "value1", "value2"))
			}, timeout, interval).Should(Succeed())

			// Update annotations to add security groups (delete default secgroup, add bigbangSec)
			Eventually(func(g Gomega) {
				object := &networkingv1.Ingress{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: ingressName, Namespace: namespace}, object)).Should(Succeed())
				if object.Annotations == nil {
					object.Annotations = map[string]string{}
				}
				object.Annotations["vks.vngcloud.vn/security-groups"] = bigbangSec.Id
				g.Expect(k8sClient.Update(ctx, object)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			// Verify security groups were updated
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				// Check security groups (should delete default secgroup)
				secgroups, err := vngcloudRepo.ListSecurityGroups(ctx)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(secgroups).ShouldNot(BeNil())
				g.Expect((secgroups.Items)).Should(HaveLen(2))

				// Check servers now have bigbangSec attached
				servers, err := vngcloudRepo.ListServerBySecgroupID(ctx, bigbangSec.Id)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(servers).ShouldNot(BeNil())
				g.Expect((servers.Items)).Should(HaveLen(4))
			}, timeout, interval).Should(Succeed())

			// Update tags (remove tag1, update tag2, add tag3)
			Eventually(func(g Gomega) {
				object := &networkingv1.Ingress{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: ingressName, Namespace: namespace}, object)).Should(Succeed())
				if object.Annotations == nil {
					object.Annotations = map[string]string{}
				}
				object.Annotations["vks.vngcloud.vn/tags"] = "tag2=value22,tag3=value3"
				g.Expect(k8sClient.Update(ctx, object)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			// Verify tags were updated (tag1 removed, tag2 updated, tag3 added)
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())
				g.Expect(loadbalancer.Name).Should(Equal("vks-k8s-000000-default-test-servi-bea48"))

				// Check tags updated: tag1 removed, tag2 updated to value22, tag3 added (3 default VKS + 2 custom = 5)
				tags, err := vngcloudRepo.ListTags(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(tags).ShouldNot(BeNil())
				g.Expect((tags.Items)).Should(HaveLen(5))
				tagKeys := make([]string, 0)
				tagValues := make([]string, 0)
				for _, tag := range tags.Items {
					tagKeys = append(tagKeys, tag.Key)
					tagValues = append(tagValues, tag.Value)
				}
				g.Expect(tagKeys).Should(ContainElements(domain.ClusterTagKey, "tag2", "tag3"))
				g.Expect(tagValues).Should(ContainElements(mockConfig.Cluster.ClusterID, "value22", "value3"))
				g.Expect(tagKeys).ShouldNot(ContainElement("tag1"))
			}, timeout, interval).Should(Succeed())

			// Update security groups annotation (remove bigbangSec, add blackpinkSec)
			Eventually(func(g Gomega) {
				object := &networkingv1.Ingress{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: ingressName, Namespace: namespace}, object)).Should(Succeed())
				if object.Annotations == nil {
					object.Annotations = map[string]string{}
				}
				object.Annotations["vks.vngcloud.vn/security-groups"] = blackpinkSec.Id
				g.Expect(k8sClient.Update(ctx, object)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			// Verify security groups were swapped
			Eventually(func(g Gomega) {
				// Check security groups still 2 (bigbangSec and blackpinkSec)
				secgroups, err := vngcloudRepo.ListSecurityGroups(ctx)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(secgroups).ShouldNot(BeNil())
				g.Expect((secgroups.Items)).Should(HaveLen(2))

				// Check servers no longer have bigbangSec
				servers, err := vngcloudRepo.ListServerBySecgroupID(ctx, bigbangSec.Id)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(servers).ShouldNot(BeNil())
				g.Expect((servers.Items)).Should(BeEmpty())

				// Check servers now have blackpinkSec attached
				servers, err = vngcloudRepo.ListServerBySecgroupID(ctx, blackpinkSec.Id)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(servers).ShouldNot(BeNil())
				g.Expect((servers.Items)).Should(HaveLen(4))
			}, timeout, interval).Should(Succeed())

			// Cleanup
			Expect(k8sClient.Delete(ctx, ingress)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, service)).Should(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, endpoint))).Should(Succeed())
		})
	})

	Context("When create ingress with default annotations of calico overlay", func() {
		It("should handle calico overlay CNI mode with nodeport-only security group rules and support tag/secgroup updates", func() {

			serviceName := testServiceNameGogsf
			namespace := testDefaultNamespace
			ingressName := testServiceNameGogsf

			// Configure CNI detector for Calico Overlay
			cniDetector.ExpectedCalls = nil
			cniDetector.Calls = nil
			cniDetector.EXPECT().DetectCNIType(mock.Anything).Return(utils.CalicoOverlay, nil)

			// Create 2 external security groups for testing
			bigbangSec, err := vngcloudRepo.CreateSecurityGroup(ctx, "bigbang", "the best security group")
			Expect(err).ShouldNot(HaveOccurred())
			Expect(bigbangSec).ShouldNot(BeNil())
			blackpinkSec, err := vngcloudRepo.CreateSecurityGroup(ctx, "blackpink", "the great security group")
			Expect(err).ShouldNot(HaveOccurred())
			Expect(blackpinkSec).ShouldNot(BeNil())

			// Delete security groups when test finishes
			defer func() {
				Eventually(func() bool {
					err = vngcloudRepo.DeleteSecurityGroup(ctx, bigbangSec.Id)
					return err == nil
				}, timeout, interval).Should((BeTrue()), "should delete bigbang security group (wait to detach if in use)")
				Eventually(func() bool {
					err = vngcloudRepo.DeleteSecurityGroup(ctx, blackpinkSec.Id)
					return err == nil
				}, timeout, interval).Should((BeTrue()), "should delete blackpink security group (wait to detach if in use)")
			}()

			// Create endpoint with multiple subsets
			endpoint := newEndpointResource(serviceName, namespace)
			endpoint.Subsets = []corev1.EndpointSubset{
				{
					Addresses: []corev1.EndpointAddress{
						{IP: "100.0.1.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode1.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-1", Kind: "Pod", Namespace: namespace}},
						{IP: "100.0.2.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode2.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-2", Kind: "Pod", Namespace: namespace}},
					},
					NotReadyAddresses: []corev1.EndpointAddress{
						{IP: "100.0.3.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode3.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-3", Kind: "Pod", Namespace: namespace}},
						{IP: "100.0.4.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode4.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-4", Kind: "Pod", Namespace: namespace}},
					},
					Ports: []corev1.EndpointPort{
						{Name: "http", Port: 80},
						{Name: "https", Port: 443},
					},
				},
				{
					Addresses: []corev1.EndpointAddress{
						{IP: "200.0.1.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode1.Name), TargetRef: &corev1.ObjectReference{Name: "fake-pod-1", Kind: "Pod", Namespace: namespace}},
						{IP: "200.0.2.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode2.Name), TargetRef: &corev1.ObjectReference{Name: "fake-pod-2", Kind: "Pod", Namespace: namespace}},
					},
					NotReadyAddresses: []corev1.EndpointAddress{
						{IP: "200.0.3.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode3.Name), TargetRef: &corev1.ObjectReference{Name: "fake-pod-3", Kind: "Pod", Namespace: namespace}},
						{IP: "200.0.4.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode4.Name), TargetRef: &corev1.ObjectReference{Name: "fake-pod-4", Kind: "Pod", Namespace: namespace}},
					},
					Ports: []corev1.EndpointPort{
						{Name: "http", Port: 8080},
						{Name: "https", Port: 6443},
					},
				},
			}
			Expect(k8sClient.Create(ctx, endpoint)).Should(Succeed())

			// Create Service
			service := newServiceNodePortResource(serviceName, namespace)
			service.Spec.Ports = []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromInt(80), Protocol: corev1.ProtocolTCP, NodePort: 30000},
			}
			Expect(k8sClient.Create(ctx, service)).Should(Succeed())

			// Create Ingress with default backend
			ingress := newIngressResource(ingressName, namespace)
			Expect(ingress).NotTo(BeNil())
			ingress.Spec.DefaultBackend = &networkingv1.IngressBackend{
				Service: &networkingv1.IngressServiceBackend{
					Name: serviceName,
					Port: networkingv1.ServiceBackendPort{
						Number: 80,
					},
				},
			}
			Expect(k8sClient.Create(ctx, ingress)).Should(Succeed())

			// Verify initial load balancer creation
			var secgroupID string
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())
				g.Expect(loadbalancer.Name).Should(Equal("vks-k8s-000000-default-test-servi-bea48"))

				// Check tags (3 default VKS tags)
				tags, err := vngcloudRepo.ListTags(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(tags).ShouldNot(BeNil())
				g.Expect((tags.Items)).Should(HaveLen(3))
				tagKeys := make([]string, 0)
				for _, tag := range tags.Items {
					tagKeys = append(tagKeys, tag.Key)
				}
				g.Expect(tagKeys).Should(ContainElement(domain.ClusterTagKey))

				// Check secgroups
				secgroups, err := vngcloudRepo.ListSecurityGroups(ctx)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(secgroups).ShouldNot(BeNil())
				g.Expect((secgroups.Items)).Should(HaveLen(3))
				expectName := []string{"vks-k8s-000000-default-test-servi-bea48", bigbangSec.Name, blackpinkSec.Name}
				for _, secgroup := range secgroups.Items {
					if secgroup.Name == "vks-k8s-000000-default-test-servi-bea48" {
						secgroupID = secgroup.Id
					}
					g.Expect(secgroup.Name).Should(BeElementOf(expectName))
					expectName = removeFisrt(expectName, secgroup.Name)
				}

				// Check secgroup rule - calico overlay should only have nodeport rules
				rules, err := vngcloudRepo.ListSecurityGroupRules(ctx, secgroupID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(rules).ShouldNot(BeNil())
				g.Expect((rules.Items)).Should(HaveLen(3))       // 1 nodeport + 2 egress rules
				expectPortRangeMax := []int{30000, 65535, 65535} // calico overlay should only have nodeport
				expectPortRangeMin := []int{30000, 0, 0}         // calico overlay should only have nodeport
				for _, rule := range rules.Items {
					g.Expect(rule.PortRangeMax).Should(BeElementOf(expectPortRangeMax))
					expectPortRangeMax = removeFisrt(expectPortRangeMax, rule.PortRangeMax)
					g.Expect(rule.PortRangeMin).Should(BeElementOf(expectPortRangeMin))
					expectPortRangeMin = removeFisrt(expectPortRangeMin, rule.PortRangeMin)
					g.Expect(rule.Direction).Should(BeElementOf([]string{"ingress", "egress"}))
					g.Expect(rule.EtherType).Should(BeElementOf([]string{"IPv4", "IPv6"}))
					g.Expect(rule.Protocol).Should(BeElementOf([]string{"tcp", "any"}))
					// g.Expect(rule.RemoteIPPrefix).Should(Equal(vngcloud_mocks.MockSubnetCIDR)) // TODO: fix me
				}

				// Check server have secgroup
				server, err := vngcloudRepo.ListServerBySecgroupID(ctx, secgroupID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(server).ShouldNot(BeNil())
				g.Expect((server.Items)).Should(HaveLen(4))
				for _, item := range server.Items {
					serverSecgroups := make([]string, 0)
					for _, secgroup := range item.SecGroups {
						serverSecgroups = append(serverSecgroups, secgroup.Uuid)
					}
					g.Expect(serverSecgroups).Should(ContainElement(secgroupID))
				}
			}, timeout, interval).Should(Succeed())

			// Step 1: Update tags (add more tags) and secgroups annotations (delete default secgroup and add additional secgroups)
			Eventually(func(g Gomega) {
				object := &networkingv1.Ingress{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: ingressName, Namespace: namespace}, object)).Should(Succeed())
				if object.Annotations == nil {
					object.Annotations = map[string]string{}
				}
				object.Annotations["vks.vngcloud.vn/tags"] = "tag1=value1,tag2=value2"
				object.Annotations["vks.vngcloud.vn/security-groups"] = bigbangSec.Id
				g.Expect(k8sClient.Update(ctx, object)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			// Verify tags and secgroups were updated
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())
				g.Expect(loadbalancer.Name).Should(Equal("vks-k8s-000000-default-test-servi-bea48"))

				// Check tags (3 default VKS + 2 custom = 5)
				tags, err := vngcloudRepo.ListTags(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(tags).ShouldNot(BeNil())
				g.Expect((tags.Items)).Should(HaveLen(5))
				tagKeys := make([]string, 0)
				for _, tag := range tags.Items {
					tagKeys = append(tagKeys, tag.Key)
				}
				g.Expect(tagKeys).Should(ContainElements(domain.ClusterTagKey, "tag1", "tag2"))

				// Check secgroups
				secgroups, err := vngcloudRepo.ListSecurityGroups(ctx)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(secgroups).ShouldNot(BeNil())
				g.Expect((secgroups.Items)).Should(HaveLen(2)) // should delete default secgroup

				// Check server have secgroup
				server, err := vngcloudRepo.ListServerBySecgroupID(ctx, bigbangSec.Id)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(server).ShouldNot(BeNil())
				g.Expect((server.Items)).Should(HaveLen(4))
			}, timeout, interval).Should(Succeed())

			// Step 2: Update tags (remove, update, add tags) and secgroups annotations (remove and add secgroups in server)
			Eventually(func(g Gomega) {
				object := &networkingv1.Ingress{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: ingressName, Namespace: namespace}, object)).Should(Succeed())
				if object.Annotations == nil {
					object.Annotations = map[string]string{}
				}
				object.Annotations["vks.vngcloud.vn/tags"] = "tag2=value22, tag3=value3"
				object.Annotations["vks.vngcloud.vn/security-groups"] = blackpinkSec.Id
				g.Expect(k8sClient.Update(ctx, object)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			// Verify final tags and secgroups
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())
				g.Expect(loadbalancer.Name).Should(Equal("vks-k8s-000000-default-test-servi-bea48"))

				// Check tags (3 default VKS + 2 custom = 5)
				tags, err := vngcloudRepo.ListTags(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(tags).ShouldNot(BeNil())
				g.Expect((tags.Items)).Should(HaveLen(5))
				tagKeys := make([]string, 0)
				for _, tag := range tags.Items {
					tagKeys = append(tagKeys, tag.Key)
				}
				g.Expect(tagKeys).Should(ContainElements(domain.ClusterTagKey, "tag2", "tag3"))
				g.Expect(tagKeys).ShouldNot(ContainElement("tag1"))

				// Check secgroups
				secgroups, err := vngcloudRepo.ListSecurityGroups(ctx)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(secgroups).ShouldNot(BeNil())
				g.Expect((secgroups.Items)).Should(HaveLen(2)) // should not delete secgroup

				// Check server have secgroup
				server, err := vngcloudRepo.ListServerBySecgroupID(ctx, bigbangSec.Id)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(server).ShouldNot(BeNil())
				g.Expect((server.Items)).Should(BeEmpty())

				server, err = vngcloudRepo.ListServerBySecgroupID(ctx, blackpinkSec.Id)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(server).ShouldNot(BeNil())
				g.Expect((server.Items)).Should(HaveLen(4))
			}, timeout, interval).Should(Succeed())

			// Cleanup
			Expect(k8sClient.Delete(ctx, ingress)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, service)).Should(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, endpoint))).Should(Succeed())
		})
	})

	Context("When load balancer already exists and ingress is created with load-balancer-id annotation", func() {
		It("should update the existing load balancer and preserve original resources after deletion", func() {

			serviceName := testServiceNameGogsf
			namespace := testDefaultNamespace
			ingressName := testServiceNameGogsf

			// Create a load balancer manually with pool, listener, and policy
			healthMonitorOpt := &loadbalancerv2.HealthMonitor{
				HealthCheckProtocol: "HTTP",
				HealthyThreshold:    2,
				UnhealthyThreshold:  3,
				Interval:            5,
				Timeout:             5,
				HealthCheckMethod:   nil,
				HttpVersion:         nil,
				HealthCheckPath:     nil,
				DomainName:          nil,
				SuccessCode:         nil,
			}
			poolOpt := &loadbalancerv2.CreatePoolRequest{
				Algorithm:     loadbalancerv2.PoolAlgorithmLeastConn,
				PoolName:      "test-pool-gogsf",
				PoolProtocol:  "tcp",
				Stickiness:    nil,
				TLSEncryption: nil,
				HealthMonitor: healthMonitorOpt,
				Members:       nil,
			}
			listenerOpt := &loadbalancerv2.CreateListenerRequest{
				AllowedCidrs:                "0.0.0.0/0",
				ListenerName:                "listener-foo",
				ListenerProtocol:            "HTTP",
				ListenerProtocolPort:        80,
				TimeoutClient:               50000,
				TimeoutConnection:           50000,
				TimeoutMember:               50000,
				InsertHeaders:               &[]entity.ListenerInsertHeader{},
				CertificateAuthorities:      nil,
				ClientCertificate:           nil,
				DefaultCertificateAuthority: nil,
			}
			opt := &loadbalancerv2.CreateLoadBalancerRequest{
				Name:         testServiceNameGogsf,
				PackageID:    vngcloud_mocks.MockL7PackageId,
				Scheme:       "internal",
				AutoScalable: true,
				SubnetID:     vngcloud_mocks.MockSubnetID,
				Type:         loadbalancerv2.LoadBalancerTypeLayer7,
			}
			LB, err := vngcloudRepo.CreateLoadBalancer(ctx, opt.WithPool(poolOpt).WithListener(listenerOpt))
			Expect(err).ShouldNot(HaveOccurred())
			Expect(LB).ShouldNot(BeNil())
			Expect(LB.UUID).ShouldNot(Equal(""))

			LB, err = vngcloudRepo.GetLoadBalancerByID(ctx, LB.UUID)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(LB).ShouldNot(BeNil())
			Expect(LB.Name).Should(Equal(testServiceNameGogsf))

			// Verify initial pool
			pools, err := vngcloudRepo.ListPool(ctx, LB.UUID)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(pools).ShouldNot(BeNil())
			Expect((pools.Items)).Should(HaveLen(1))
			Expect(pools.Items[0].Name).Should(Equal("test-pool-gogsf"))

			// Verify initial listener
			listeners, err := vngcloudRepo.ListListenerOfLB(ctx, LB.UUID)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(listeners).ShouldNot(BeNil())
			Expect((listeners.Items)).Should(HaveLen(1))
			Expect(listeners.Items[0].Name).Should(Equal("listener-foo"))

			// Create policy
			policyOpt := &loadbalancerv2.CreatePolicyRequest{
				Name: "test-policy-gogsf",
			}
			policy, err := vngcloudRepo.CreatePolicy(ctx, LB.UUID, listeners.Items[0].UUID, policyOpt)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(policy).ShouldNot(BeNil())

			// Delete load balancer after test
			defer func() {
				err := vngcloudRepo.DeleteLoadBalancer(ctx, LB.UUID)
				Expect(err).ShouldNot(HaveOccurred())
			}()

			// Create endpoint with multiple subsets
			endpoint := newEndpointResource(serviceName, namespace)
			endpoint.Subsets = []corev1.EndpointSubset{
				{
					Addresses: []corev1.EndpointAddress{
						{IP: "100.0.1.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode1.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-1", Kind: "Pod", Namespace: namespace}},
						{IP: "100.0.2.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode2.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-2", Kind: "Pod", Namespace: namespace}},
					},
					NotReadyAddresses: []corev1.EndpointAddress{
						{IP: "100.0.3.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode3.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-3", Kind: "Pod", Namespace: namespace}},
						{IP: "100.0.4.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode4.Name), TargetRef: &corev1.ObjectReference{Name: "mock-pod-4", Kind: "Pod", Namespace: namespace}},
					},
					Ports: []corev1.EndpointPort{
						{Name: "http", Port: 80},
						{Name: "https", Port: 443},
					},
				},
				{
					Addresses: []corev1.EndpointAddress{
						{IP: "200.0.1.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode1.Name), TargetRef: &corev1.ObjectReference{Name: "fake-pod-1", Kind: "Pod", Namespace: namespace}},
						{IP: "200.0.2.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode2.Name), TargetRef: &corev1.ObjectReference{Name: "fake-pod-2", Kind: "Pod", Namespace: namespace}},
					},
					NotReadyAddresses: []corev1.EndpointAddress{
						{IP: "200.0.3.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode3.Name), TargetRef: &corev1.ObjectReference{Name: "fake-pod-3", Kind: "Pod", Namespace: namespace}},
						{IP: "200.0.4.0", Hostname: "", NodeName: ptr.To(vngcloud_mocks.MockNode4.Name), TargetRef: &corev1.ObjectReference{Name: "fake-pod-4", Kind: "Pod", Namespace: namespace}},
					},
					Ports: []corev1.EndpointPort{
						{Name: "http", Port: 8080},
						{Name: "https", Port: 6443},
					},
				},
			}
			Expect(k8sClient.Create(ctx, endpoint)).Should(Succeed())

			// Create Service
			service := newServiceNodePortResource(serviceName, namespace)
			service.Spec.Ports = []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromInt(80), Protocol: corev1.ProtocolTCP, NodePort: 30000},
			}
			Expect(k8sClient.Create(ctx, service)).Should(Succeed())

			// Create Ingress with load-balancer-id annotation pointing to the existing LB
			ingress := newIngressResource(ingressName, namespace)
			Expect(ingress).NotTo(BeNil())
			ingress.Spec.DefaultBackend = nil
			ingress.Spec.Rules = []networkingv1.IngressRule{
				{
					Host: "test.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									PathType: ptr.To(networkingv1.PathTypePrefix),
									Path:     "/",
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: serviceName,
											Port: networkingv1.ServiceBackendPort{Number: 80},
										},
									},
								},
							},
						},
					},
				},
			}
			if ingress.Annotations == nil {
				ingress.Annotations = map[string]string{}
			}
			ingress.Annotations[fmt.Sprintf("%s/%s", domain.INGRESS_ANNOTATION_PREFIX, annotations.SuffixLoadBalancerID)] = LB.UUID
			Expect(k8sClient.Create(ctx, ingress)).Should(Succeed())

			// Verify the ingress controller updated the existing load balancer
			Eventually(func(g Gomega) {
				// Get load balancer by annotation
				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, LB.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())

				// Check pools - should have 2 pools now (original + new one from ingress)
				pools, err := vngcloudRepo.ListPool(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(pools).ShouldNot(BeNil())
				g.Expect((pools.Items)).Should(HaveLen(2)) // Original pool + new pool from ingress
				poolNames := []string{}
				for _, pool := range pools.Items {
					poolNames = append(poolNames, pool.Name)
				}
				g.Expect(poolNames).Should(ContainElement("test-pool-gogsf"))                         // Original pool
				g.Expect(poolNames).Should(ContainElement("vks-bea48-default-test-service-gogsf-80")) // New pool

				// Check listener - should still be 1
				listeners, err := vngcloudRepo.ListListenerOfLB(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(listeners).ShouldNot(BeNil())
				g.Expect((listeners.Items)).Should(HaveLen(1))
				for _, listener := range listeners.Items {
					g.Expect(listener.Protocol).Should(Equal("HTTP"))
					g.Expect(listener.ProtocolPort).Should(Equal(80))
				}
			}, timeout, interval).Should(Succeed())

			// Delete ingress
			Expect(k8sClient.Delete(ctx, ingress)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, service)).Should(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, endpoint))).Should(Succeed())

			// Verify original resources are preserved after deletion
			Eventually(func(g Gomega) {
				LB, err := vngcloudRepo.GetLoadBalancerByID(ctx, LB.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(LB).ShouldNot(BeNil())

				// Check listener - should still have the original one
				listeners, err := vngcloudRepo.ListListenerOfLB(ctx, LB.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(listeners).ShouldNot(BeNil())
				g.Expect((listeners.Items)).Should(HaveLen(1))
				g.Expect(listeners.Items[0].Name).Should(Equal("listener-foo"))

				// Check pool - should only have the original pool
				pools, err := vngcloudRepo.ListPool(ctx, LB.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(pools).ShouldNot(BeNil())
				g.Expect((pools.Items)).Should(HaveLen(1))
				g.Expect(pools.Items[0].Name).Should(Equal("test-pool-gogsf"))
			}, timeout*4, interval).Should(Succeed())
		})
	})

	Context("When 2 ingress resources use the same load balancer with default annotation", func() {
		It("should add members to the same default pool and remove members after resource deletion", func() {

			serviceName1 := testServiceNameGogsf
			serviceName2 := "test-service-gogsf-2"
			ingressName1 := testServiceNameGogsf
			ingressName2 := "test-service-gogsf-2"
			namespace := testDefaultNamespace

			// Create first endpoint
			endpoint1 := newEndpointResource(serviceName1, namespace)
			Expect(k8sClient.Create(ctx, endpoint1)).Should(Succeed())

			// Create first service
			service1 := newServiceNodePortResource(serviceName1, namespace)
			service1.Spec.Ports = []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromInt(80), Protocol: corev1.ProtocolTCP, NodePort: 30000},
			}
			Expect(k8sClient.Create(ctx, service1)).Should(Succeed())

			// Create second endpoint
			endpoint2 := newEndpointResource(serviceName2, namespace)
			Expect(k8sClient.Create(ctx, endpoint2)).Should(Succeed())

			// Create second service
			service2 := newServiceNodePortResource(serviceName2, namespace)
			service2.Spec.Ports = []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromInt(80), Protocol: corev1.ProtocolTCP, NodePort: 30001},
			}
			Expect(k8sClient.Create(ctx, service2)).Should(Succeed())

			// Create first ingress with default backend
			ingress1 := newIngressResource(ingressName1, namespace)
			Expect(ingress1).NotTo(BeNil())
			ingress1.Spec.DefaultBackend = &networkingv1.IngressBackend{
				Service: &networkingv1.IngressServiceBackend{
					Name: serviceName1,
					Port: networkingv1.ServiceBackendPort{
						Number: 80,
					},
				},
			}
			Expect(k8sClient.Create(ctx, ingress1)).Should(Succeed())

			// Verify first ingress created load balancer with default pool and 4 members
			var loadbalancerUUID string
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName1, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())
				loadbalancerUUID = loadbalancer.UUID

				// Check pool
				pools, err := vngcloudRepo.ListPool(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(pools).ShouldNot(BeNil())
				g.Expect((pools.Items)).Should(HaveLen(1)) // Default pool
				for _, pool := range pools.Items {
					g.Expect(pool.Name).Should(Equal(domain.DEFAULT_NAME_DEFAULT_POOL))
					g.Expect(pool.Members).ShouldNot(BeNil())
					g.Expect((pool.Members.Items)).Should(HaveLen(4)) // 4 nodes

					expectAddress := []string{
						vngcloud_mocks.MockNode1.Status.Addresses[0].Address,
						vngcloud_mocks.MockNode2.Status.Addresses[0].Address,
						vngcloud_mocks.MockNode3.Status.Addresses[0].Address,
						vngcloud_mocks.MockNode4.Status.Addresses[0].Address,
					}
					for _, member := range pool.Members.Items {
						g.Expect(member.ProtocolPort).Should(Equal(30000))
						g.Expect(member.MonitorPort).Should(Equal(30000))
						g.Expect(member.Address).Should(BeElementOf(expectAddress))
						expectAddress = removeFisrt(expectAddress, member.Address)
					}
				}

				// Check listener
				listeners, err := vngcloudRepo.ListListenerOfLB(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(listeners).ShouldNot(BeNil())
				g.Expect((listeners.Items)).Should(HaveLen(1))
			}, timeout, interval).Should(Succeed())

			// Create second ingress with load-balancer-id annotation pointing to same LB
			ingress2 := newIngressResource(ingressName2, namespace)
			Expect(ingress2).NotTo(BeNil())
			ingress2.Spec.DefaultBackend = &networkingv1.IngressBackend{
				Service: &networkingv1.IngressServiceBackend{
					Name: serviceName2,
					Port: networkingv1.ServiceBackendPort{
						Number: 80,
					},
				},
			}
			if ingress2.Annotations == nil {
				ingress2.Annotations = map[string]string{}
			}
			ingress2.Annotations[fmt.Sprintf("%s/%s", domain.INGRESS_ANNOTATION_PREFIX, annotations.SuffixLoadBalancerID)] = loadbalancerUUID
			Expect(k8sClient.Create(ctx, ingress2)).Should(Succeed())

			// Verify second ingress added its members to the same default pool (now 8 members)
			Eventually(func(g Gomega) {
				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerUUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())
				g.Expect(loadbalancer.UUID).Should(Equal(loadbalancerUUID))

				// Check pool - should have 8 members now
				pools, err := vngcloudRepo.ListPool(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(pools).ShouldNot(BeNil())
				g.Expect((pools.Items)).Should(HaveLen(1)) // Still default pool
				for _, pool := range pools.Items {
					g.Expect(pool.Name).Should(Equal(domain.DEFAULT_NAME_DEFAULT_POOL))
					g.Expect(pool.Members).ShouldNot(BeNil())
					g.Expect((pool.Members.Items)).Should(HaveLen(8)) // 4 nodes x 2 services

					expectAddress := []string{
						vngcloud_mocks.MockNode1.Status.Addresses[0].Address,
						vngcloud_mocks.MockNode2.Status.Addresses[0].Address,
						vngcloud_mocks.MockNode3.Status.Addresses[0].Address,
						vngcloud_mocks.MockNode4.Status.Addresses[0].Address,
						vngcloud_mocks.MockNode1.Status.Addresses[0].Address,
						vngcloud_mocks.MockNode2.Status.Addresses[0].Address,
						vngcloud_mocks.MockNode3.Status.Addresses[0].Address,
						vngcloud_mocks.MockNode4.Status.Addresses[0].Address,
					}
					expectProtocolPort := []int{30000, 30001, 30000, 30001, 30000, 30001, 30000, 30001}
					for _, member := range pool.Members.Items {
						g.Expect(member.MonitorPort).Should(Equal(member.ProtocolPort))
						g.Expect(member.ProtocolPort).Should(BeElementOf(expectProtocolPort))
						expectProtocolPort = removeFisrt(expectProtocolPort, member.ProtocolPort)
						g.Expect(member.Address).Should(BeElementOf(expectAddress))
						expectAddress = removeFisrt(expectAddress, member.Address)
					}
				}

				// Check listener - still 1
				listeners, err := vngcloudRepo.ListListenerOfLB(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(listeners).ShouldNot(BeNil())
				g.Expect((listeners.Items)).Should(HaveLen(1))
			}, timeout, interval).Should(Succeed())

			// Delete second ingress
			Expect(k8sClient.Delete(ctx, ingress2)).Should(Succeed())

			// Verify pool members from second ingress are removed (back to 4 members)
			// Wait a bit longer to allow for periodic reconciliation
			Eventually(func(g Gomega) {
				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerUUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())

				// Check pool - should have 4 members again (only from first ingress)
				pools, err := vngcloudRepo.ListPool(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(pools).ShouldNot(BeNil())
				g.Expect((pools.Items)).Should(HaveLen(1))
				for _, pool := range pools.Items {
					g.Expect(pool.Name).Should(Equal(domain.DEFAULT_NAME_DEFAULT_POOL))
					g.Expect(pool.Members).ShouldNot(BeNil())
					g.Expect((pool.Members.Items)).Should(HaveLen(4)) // Back to 4 members

					expectAddress := []string{
						vngcloud_mocks.MockNode1.Status.Addresses[0].Address,
						vngcloud_mocks.MockNode2.Status.Addresses[0].Address,
						vngcloud_mocks.MockNode3.Status.Addresses[0].Address,
						vngcloud_mocks.MockNode4.Status.Addresses[0].Address,
					}
					for _, member := range pool.Members.Items {
						g.Expect(member.ProtocolPort).Should(Equal(30000))
						g.Expect(member.MonitorPort).Should(Equal(30000))
						g.Expect(member.Address).Should(BeElementOf(expectAddress))
						expectAddress = removeFisrt(expectAddress, member.Address)
					}
				}

				// Check listener - still 1
				listeners, err := vngcloudRepo.ListListenerOfLB(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(listeners).ShouldNot(BeNil())
				g.Expect((listeners.Items)).Should(HaveLen(1))
			}, timeout*2, interval).Should(Succeed()) // Double timeout for periodic reconciliation

			// Cleanup
			Expect(k8sClient.Delete(ctx, ingress1)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, service1)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, service2)).Should(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, endpoint1))).Should(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, endpoint2))).Should(Succeed())
		})
	})

	Context("When create and update https listener", func() {
		It("should create HTTPS listener with certificates and update correctly", func() {

			serviceName := testServiceFoo
			namespace := testDefaultNamespace
			ingressName := "test-service-https"

			// Create endpoint
			endpoint := newEndpointResource(serviceName, namespace)
			Expect(k8sClient.Create(ctx, endpoint)).Should(Succeed())

			// Create Service
			service := newServiceNodePortResource(serviceName, namespace)
			service.Spec.Ports = []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromInt(80), Protocol: corev1.ProtocolTCP, NodePort: 30000},
			}
			Expect(k8sClient.Create(ctx, service)).Should(Succeed())

			// Create Ingress with HTTPS/TLS configuration
			ingress := newIngressResource(ingressName, namespace)
			Expect(ingress).NotTo(BeNil())
			ingress.Spec.DefaultBackend = nil
			ingress.Spec.TLS = []networkingv1.IngressTLS{
				{Hosts: []string{"test.com"}},
			}
			ingress.Spec.Rules = []networkingv1.IngressRule{
				{
					Host: "test.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									PathType: ptr.To(networkingv1.PathTypePrefix),
									Path:     "/",
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: serviceName,
											Port: networkingv1.ServiceBackendPort{Number: 80},
										},
									},
								},
							},
						},
					},
				},
			}
			if ingress.Annotations == nil {
				ingress.Annotations = map[string]string{}
			}
			ingress.Annotations[fmt.Sprintf("%s/%s", domain.INGRESS_ANNOTATION_PREFIX, annotations.SuffixCertificateIDs)] = fmt.Sprintf("%s,%s", vngcloud_mocks.MockCerts[0], vngcloud_mocks.MockCerts[1])
			Expect(k8sClient.Create(ctx, ingress)).Should(Succeed())

			// Verify LoadBalancer and HTTPS listener were created
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())

				// Check HTTPS listener
				listeners, err := vngcloudRepo.ListListenerOfLB(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(listeners).ShouldNot(BeNil())
				g.Expect(listeners.Items).Should(HaveLen(1))

				listener := listeners.Items[0]
				g.Expect(listener.Protocol).Should(Equal("HTTPS"))
				g.Expect(listener.ProtocolPort).Should(Equal(443))
				g.Expect(listener.AllowedCidrs).Should(Equal("0.0.0.0/0"))
				g.Expect(listener.DefaultPoolId).Should(Equal(""))
				g.Expect(listener.DefaultPoolName).Should(Equal(""))
				g.Expect(listener.Name).Should(Equal(domain.DEFAULT_HTTPS_LISTENER_NAME))
				g.Expect(listener.TimeoutClient).Should(Equal(50))
				g.Expect(listener.TimeoutConnection).Should(Equal(5))
				g.Expect(listener.TimeoutMember).Should(Equal(50))
				g.Expect(listener.InsertHeaders).Should(HaveLen(3))
				g.Expect(*listener.DefaultCertificateAuthority).Should(Equal(vngcloud_mocks.MockCerts[0]))
				g.Expect(listener.CertificateAuthorities).Should(Equal([]string{vngcloud_mocks.MockCerts[1]}))
				g.Expect(listener.ClientCertificateAuthentication).Should(BeNil())

				// Check pool
				pools, err := vngcloudRepo.ListPool(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(pools).ShouldNot(BeNil())
				g.Expect(pools.Items).Should(HaveLen(1))

				pool := pools.Items[0]
				g.Expect(pool.Name).Should(ContainSubstring("default-service-foo-80"))
				g.Expect(pool.Status).Should(Equal("ACTIVE"))
				g.Expect(pool.LoadBalanceMethod).Should(Equal("ROUND_ROBIN"))
				g.Expect(pool.Protocol).Should(Equal("HTTP"))
				g.Expect(pool.Stickiness).Should(BeFalse())
				g.Expect(pool.TLSEncryption).Should(BeFalse())

				g.Expect(pool.HealthMonitor).ShouldNot(BeNil())
				g.Expect(pool.HealthMonitor.HealthCheckProtocol).Should(Equal("TCP"))
				g.Expect(pool.HealthMonitor.HealthyThreshold).Should(Equal(3))
				g.Expect(pool.HealthMonitor.UnhealthyThreshold).Should(Equal(3))
				g.Expect(pool.HealthMonitor.Interval).Should(Equal(30))
				g.Expect(pool.HealthMonitor.Timeout).Should(Equal(5))
			}, timeout, interval).Should(Succeed())

			// Update ingress with different certificates and TLS encryption enabled
			Eventually(func(g Gomega) {
				updatedIngress := &networkingv1.Ingress{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: ingressName, Namespace: namespace}, updatedIngress)).Should(Succeed())

				if updatedIngress.Annotations == nil {
					updatedIngress.Annotations = map[string]string{}
				}
				updatedIngress.Annotations[fmt.Sprintf("%s/%s", domain.INGRESS_ANNOTATION_PREFIX, annotations.SuffixCertificateIDs)] = fmt.Sprintf("%s,%s", vngcloud_mocks.MockCerts[1], vngcloud_mocks.MockCerts[2])
				updatedIngress.Annotations[fmt.Sprintf("%s/%s", domain.INGRESS_ANNOTATION_PREFIX, annotations.SuffixEnableTLSEncryption)] = "true"
				updatedIngress.Annotations[fmt.Sprintf("%s/%s", domain.INGRESS_ANNOTATION_PREFIX, annotations.SuffixEnableStickySession)] = "false"

				g.Expect(k8sClient.Update(ctx, updatedIngress)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			// Verify listener and pool were updated correctly
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())

				// Check listener certificates were updated
				listeners, err := vngcloudRepo.ListListenerOfLB(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(listeners).ShouldNot(BeNil())
				g.Expect(listeners.Items).Should(HaveLen(1))

				listener := listeners.Items[0]
				g.Expect(listener.Protocol).Should(Equal("HTTPS"))
				g.Expect(listener.ProtocolPort).Should(Equal(443))
				g.Expect(listener.AllowedCidrs).Should(Equal("0.0.0.0/0"))
				g.Expect(listener.DefaultPoolId).Should(Equal(""))
				g.Expect(listener.DefaultPoolName).Should(Equal(""))
				g.Expect(listener.Name).Should(Equal(domain.DEFAULT_HTTPS_LISTENER_NAME))
				g.Expect(listener.TimeoutClient).Should(Equal(50))
				g.Expect(listener.TimeoutConnection).Should(Equal(5))
				g.Expect(listener.TimeoutMember).Should(Equal(50))
				g.Expect(listener.InsertHeaders).Should(HaveLen(3))
				g.Expect(*listener.DefaultCertificateAuthority).Should(Equal(vngcloud_mocks.MockCerts[1]))
				g.Expect(listener.CertificateAuthorities).Should(Equal([]string{vngcloud_mocks.MockCerts[2]}))
				g.Expect(listener.ClientCertificateAuthentication).Should(BeNil())

				// Check pool TLS encryption was enabled
				pools, err := vngcloudRepo.ListPool(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(pools).ShouldNot(BeNil())
				g.Expect(pools.Items).Should(HaveLen(1))

				pool := pools.Items[0]
				g.Expect(pool.Name).Should(ContainSubstring("default-service-foo-80"))
				g.Expect(pool.Status).Should(Equal("ACTIVE"))
				g.Expect(pool.LoadBalanceMethod).Should(Equal("ROUND_ROBIN"))
				g.Expect(pool.Protocol).Should(Equal("HTTP"))
				g.Expect(pool.Stickiness).Should(BeFalse())
				g.Expect(pool.TLSEncryption).Should(BeTrue())

				g.Expect(pool.HealthMonitor).ShouldNot(BeNil())
				g.Expect(pool.HealthMonitor.HealthCheckProtocol).Should(Equal("TCP"))
				g.Expect(pool.HealthMonitor.HealthyThreshold).Should(Equal(3))
				g.Expect(pool.HealthMonitor.UnhealthyThreshold).Should(Equal(3))
				g.Expect(pool.HealthMonitor.Interval).Should(Equal(30))
				g.Expect(pool.HealthMonitor.Timeout).Should(Equal(5))
			}, timeout*3, interval).Should(Succeed())

			// Cleanup
			Expect(k8sClient.Delete(ctx, ingress)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, service)).Should(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, endpoint))).Should(Succeed())
		})
	})

	Context("When create ingress with prefer subnet ID annotation", func() {
		It("should create load balancer in the specified subnet", func() {

			serviceName := testServiceFoo
			namespace := testDefaultNamespace
			ingressName := testServiceNameGogsf

			// Create endpoint
			endpoint := newEndpointResource(serviceName, namespace)
			Expect(k8sClient.Create(ctx, endpoint)).Should(Succeed())

			// Create Service
			service := newServiceNodePortResource(serviceName, namespace)
			service.Spec.Ports = []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromInt(80), Protocol: corev1.ProtocolTCP, NodePort: 30000},
			}
			Expect(k8sClient.Create(ctx, service)).Should(Succeed())

			// Create Ingress with prefer subnet ID annotation
			ingress := newIngressResource(ingressName, namespace)
			Expect(ingress).NotTo(BeNil())
			ingress.Spec.DefaultBackend = &networkingv1.IngressBackend{
				Service: &networkingv1.IngressServiceBackend{
					Name: serviceName,
					Port: networkingv1.ServiceBackendPort{
						Number: 80,
					},
				},
			}
			ingress.Annotations[fmt.Sprintf("%s/%s", domain.INGRESS_ANNOTATION_PREFIX, annotations.SuffixPreferSubnetID)] = vngcloud_mocks.MockSubnetID_1b_2
			Expect(k8sClient.Create(ctx, ingress)).Should(Succeed())

			// Verify LoadBalancer was created in the specified subnet
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())

				// Verify the load balancer is in the preferred subnet
				g.Expect(loadbalancer.BackendSubnetID).Should(Equal(vngcloud_mocks.MockSubnetID_1b_2))
				g.Expect(loadbalancer.ZoneID).Should(Equal(vngcloud_mocks.MapSubnetToZone[vngcloud_mocks.MockSubnetID_1b_2]))
			}, timeout, interval).Should(Succeed())

			// Cleanup
			Expect(k8sClient.Delete(ctx, ingress)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, service)).Should(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, endpoint))).Should(Succeed())
		})
	})

	Context("When create ingress with prefer zone ID annotation", func() {
		It("should create load balancer in the specified zone", func() {

			serviceName := testServiceFoo
			namespace := testDefaultNamespace
			ingressName := testServiceNameGogsf

			// Create endpoint
			endpoint := newEndpointResource(serviceName, namespace)
			Expect(k8sClient.Create(ctx, endpoint)).Should(Succeed())

			// Create Service
			service := newServiceNodePortResource(serviceName, namespace)
			service.Spec.Ports = []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromInt(80), Protocol: corev1.ProtocolTCP, NodePort: 30000},
			}
			Expect(k8sClient.Create(ctx, service)).Should(Succeed())

			// Create Ingress with prefer zone ID annotation
			ingress := newIngressResource(ingressName, namespace)
			Expect(ingress).NotTo(BeNil())
			ingress.Spec.DefaultBackend = &networkingv1.IngressBackend{
				Service: &networkingv1.IngressServiceBackend{
					Name: serviceName,
					Port: networkingv1.ServiceBackendPort{
						Number: 80,
					},
				},
			}
			ingress.Annotations[fmt.Sprintf("%s/%s", domain.INGRESS_ANNOTATION_PREFIX, annotations.SuffixPreferZoneID)] = string(common.HCM_03_1B_ZONE)
			Expect(k8sClient.Create(ctx, ingress)).Should(Succeed())

			// Verify LoadBalancer was created in the specified zone
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())

				// Verify the load balancer is in the preferred zone
				// SubnetID should be one of the subnets in that zone
				g.Expect(loadbalancer.BackendSubnetID).Should(BeElementOf(vngcloud_mocks.MockSubnetID_1b_1, vngcloud_mocks.MockSubnetID_1b_2))
				g.Expect(loadbalancer.ZoneID).Should(Equal(string(common.HCM_03_1B_ZONE)))
			}, timeout, interval).Should(Succeed())

			// Cleanup
			Expect(k8sClient.Delete(ctx, ingress)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, service)).Should(Succeed())
		})
	})

	Context("When node status changes from not ready to ready", func() {
		It("should update pool members when node becomes ready", func() {

			serviceName := testServiceFoo
			namespace := testDefaultNamespace
			ingressName := testServiceNameGogsf

			// Set node1 and node2 to NotReady state
			node1 := &corev1.Node{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: vngcloud_mocks.MockNode1.Name}, node1)).Should(Succeed())
			node1.Status.Conditions = []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
			}
			Expect(k8sClient.Status().Update(ctx, node1)).Should(Succeed())

			node2 := &corev1.Node{}
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: vngcloud_mocks.MockNode2.Name}, node2)).Should(Succeed())
			node2.Status.Conditions = []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
			}
			Expect(k8sClient.Status().Update(ctx, node2)).Should(Succeed())

			// Create Endpoint
			endpoint := newEndpointResource(serviceName, namespace)
			Expect(k8sClient.Create(ctx, endpoint)).Should(Succeed())

			// Create Service
			service := newServiceNodePortResource(serviceName, namespace)
			service.Spec.Ports = []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromInt(80), Protocol: corev1.ProtocolTCP, NodePort: 30000},
			}
			Expect(k8sClient.Create(ctx, service)).Should(Succeed())

			// Create Ingress
			ingress := newIngressResource(ingressName, namespace)
			Expect(ingress).NotTo(BeNil())
			ingress.Spec.DefaultBackend = &networkingv1.IngressBackend{
				Service: &networkingv1.IngressServiceBackend{
					Name: serviceName,
					Port: networkingv1.ServiceBackendPort{
						Number: 80,
					},
				},
			}
			Expect(k8sClient.Create(ctx, ingress)).Should(Succeed())

			// Verify pool has only 2 members (from node3 and node4, since node1 and node2 are not ready)
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())

				// Check pool has only 2 members
				pools, err := vngcloudRepo.ListPool(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(pools).ShouldNot(BeNil())
				g.Expect(pools.Items).Should(HaveLen(1))

				pool := pools.Items[0]
				g.Expect(pool.Name).Should(Equal(domain.DEFAULT_NAME_DEFAULT_POOL))
				g.Expect(pool.Members).ShouldNot(BeNil())
				g.Expect(pool.Members.Items).Should(HaveLen(2)) // Only node3 and node4

				expectAddress := []string{
					vngcloud_mocks.MockNode3.Status.Addresses[0].Address,
					vngcloud_mocks.MockNode4.Status.Addresses[0].Address,
				}
				for _, member := range pool.Members.Items {
					g.Expect(member.ProtocolPort).Should(Equal(30000))
					g.Expect(member.MonitorPort).Should(Equal(30000))
					g.Expect(member.Address).Should(BeElementOf(expectAddress))
					expectAddress = removeFisrt(expectAddress, member.Address)
				}

				// Check listener exists
				listeners, err := vngcloudRepo.ListListenerOfLB(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(listeners).ShouldNot(BeNil())
				g.Expect(listeners.Items).Should(HaveLen(1))
			}, timeout, interval).Should(Succeed())

			// Update node1 status to Ready
			Eventually(func(g Gomega) {
				node1 := &corev1.Node{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: vngcloud_mocks.MockNode1.Name}, node1)).Should(Succeed())
				node1.Status.Conditions = []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				}
				g.Expect(k8sClient.Status().Update(ctx, node1)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			// Verify pool now has 3 members (node1, node3, node4)
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())

				// Check pool now has 3 members
				pools, err := vngcloudRepo.ListPool(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(pools).ShouldNot(BeNil())
				g.Expect(pools.Items).Should(HaveLen(1))

				pool := pools.Items[0]
				g.Expect(pool.Name).Should(Equal(domain.DEFAULT_NAME_DEFAULT_POOL))
				g.Expect(pool.Members).ShouldNot(BeNil())
				g.Expect(pool.Members.Items).Should(HaveLen(3)) // node1, node3, node4

				expectAddress := []string{
					vngcloud_mocks.MockNode1.Status.Addresses[0].Address,
					vngcloud_mocks.MockNode3.Status.Addresses[0].Address,
					vngcloud_mocks.MockNode4.Status.Addresses[0].Address,
				}
				for _, member := range pool.Members.Items {
					g.Expect(member.MonitorPort).Should(Equal(member.ProtocolPort))
					g.Expect(member.Address).Should(BeElementOf(expectAddress))
					expectAddress = removeFisrt(expectAddress, member.Address)
				}
			}, timeout*3, interval).Should(Succeed())

			// Cleanup: Delete resources
			Expect(k8sClient.Delete(ctx, ingress)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, service)).Should(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, endpoint))).Should(Succeed())

			// Restore nodes to Ready state
			Eventually(func(g Gomega) {
				node1 := &corev1.Node{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: vngcloud_mocks.MockNode1.Name}, node1)).Should(Succeed())
				node1.Status.Conditions = []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				}
				g.Expect(k8sClient.Status().Update(ctx, node1)).Should(Succeed())

				node2 := &corev1.Node{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: vngcloud_mocks.MockNode2.Name}, node2)).Should(Succeed())
				node2.Status.Conditions = []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				}
				g.Expect(k8sClient.Status().Update(ctx, node2)).Should(Succeed())
			}, timeout, interval).Should(Succeed())
		})
	})

	// Context("When create ingress with ImplementationSpecific path type", func() {
	// 	It("should handle different policy actions correctly", func() {

	// 		serviceName := testServiceFoo
	// 		namespace := testDefaultNamespace
	// 		ingressName := "test-ingress-implement-specific"

	// 		// Create endpoint
	// 		endpoint := newEndpointResource(serviceName, namespace)
	// 		Expect(k8sClient.Create(ctx, endpoint)).Should(Succeed())

	// 		// Create Service
	// 		service := newServiceNodePortResource(serviceName, namespace)
	// 		service.Spec.Ports = []corev1.ServicePort{
	// 			{Name: "http", Port: 80, TargetPort: intstr.FromInt(80), Protocol: corev1.ProtocolTCP, NodePort: 30000},
	// 		}
	// 		Expect(k8sClient.Create(ctx, service)).Should(Succeed())

	// 		// Create Ingress with ImplementationSpecific path type and REJECT action
	// 		ingress := newIngressResource(ingressName, namespace)
	// 		Expect(ingress).NotTo(BeNil())
	// 		ingress.Spec.DefaultBackend = nil
	// 		ingress.Spec.Rules = []networkingv1.IngressRule{
	// 			{
	// 				Host: "",
	// 				IngressRuleValue: networkingv1.IngressRuleValue{
	// 					HTTP: &networkingv1.HTTPIngressRuleValue{
	// 						Paths: []networkingv1.HTTPIngressPath{
	// 							{
	// 								PathType: ptr.To(networkingv1.PathTypeImplementationSpecific),
	// 								Path:     "/haha",
	// 								Backend: networkingv1.IngressBackend{
	// 									Service: &networkingv1.IngressServiceBackend{
	// 										Name: serviceName,
	// 										Port: networkingv1.ServiceBackendPort{Number: 80},
	// 									},
	// 								},
	// 							},
	// 						},
	// 					},
	// 				},
	// 			},
	// 		}
	// 		if ingress.Annotations == nil {
	// 			ingress.Annotations = map[string]string{}
	// 		}
	// 		ingress.Annotations[fmt.Sprintf("%s/%s", domain.INGRESS_ANNOTATION_PREFIX, annotations.SuffixImplementationSpecificParams)] = `[{"path":"/haha","rules":[{"type":"PATH","compare":"EQUAL_TO","value":"/foo#"},{"type":"PATH","compare":"REGEX","value":"/foo#anchor"}],"action":{"action":"REJECT", "redirectUrl": "http://golang.cafe/a", "redirectHttpCode": 301}}]`
	// 		Expect(k8sClient.Create(ctx, ingress)).Should(Succeed())

	// 		// Verify policy with REJECT action is created
	// 		Eventually(func(g Gomega) {
	// 			lbcList, err := listLbcByIngress(ingressName, namespace)
	// 			g.Expect(err).ShouldNot(HaveOccurred())
	// 			g.Expect(lbcList.Items).Should(HaveLen(1))

	// 			lbc := &lbcList.Items[0]
	// 			g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
	// 			loadbalancerId := *lbc.Status.LoadBalancerId

	// 			loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
	// 			g.Expect(err).ShouldNot(HaveOccurred())
	// 			g.Expect(loadbalancer).ShouldNot(BeNil())

	// 			// Check listener
	// 			listeners, err := vngcloudRepo.ListListenerOfLB(ctx, loadbalancer.UUID)
	// 			g.Expect(err).ShouldNot(HaveOccurred())
	// 			g.Expect(listeners).ShouldNot(BeNil())
	// 			g.Expect(listeners.Items).Should(HaveLen(1))

	// 			listener := listeners.Items[0]

	// 			// Check policy with REJECT action
	// 			policies, err := vngcloudRepo.ListPolicyOfListener(ctx, loadbalancer.UUID, listener.UUID)
	// 			g.Expect(err).ShouldNot(HaveOccurred())
	// 			g.Expect(policies).ShouldNot(BeNil())
	// 			g.Expect(policies.Items).Should(HaveLen(1))

	// 			policy := policies.Items[0]
	// 			g.Expect(policy.Action).Should(Equal("REJECT"))
	// 			g.Expect(policy.L7Rules).Should(HaveLen(2))

	// 			expectRuleType := []string{"PATH", "PATH"}
	// 			expectRuleCompare := []string{"EQUAL_TO", "REGEX"}
	// 			expectRuleValue := []string{"/foo#", "/foo#anchor"}
	// 			for _, rule := range policy.L7Rules {
	// 				g.Expect(rule.RuleType).Should(BeElementOf(expectRuleType))
	// 				g.Expect(rule.CompareType).Should(BeElementOf(expectRuleCompare))
	// 				g.Expect(rule.RuleValue).Should(BeElementOf(expectRuleValue))
	// 				expectRuleType = removeFisrt(expectRuleType, rule.RuleType)
	// 				expectRuleCompare = removeFisrt(expectRuleCompare, rule.CompareType)
	// 				expectRuleValue = removeFisrt(expectRuleValue, rule.RuleValue)
	// 			}

	// 			// Check no pools are created for REJECT action
	// 			pools, err := vngcloudRepo.ListPool(ctx, loadbalancer.UUID)
	// 			g.Expect(err).ShouldNot(HaveOccurred())
	// 			g.Expect(pools).ShouldNot(BeNil())
	// 			g.Expect(pools.Items).Should(BeEmpty())
	// 		}, timeout, interval).Should(Succeed())

	// 		// Update to REDIRECT_TO_POOL action
	// 		Eventually(func(g Gomega) {
	// 			updatedIngress := &networkingv1.Ingress{}
	// 			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: ingressName, Namespace: namespace}, updatedIngress)).Should(Succeed())

	// 			if updatedIngress.Annotations == nil {
	// 				updatedIngress.Annotations = map[string]string{}
	// 			}
	// 			updatedIngress.Annotations[fmt.Sprintf("%s/%s", domain.INGRESS_ANNOTATION_PREFIX, annotations.SuffixImplementationSpecificParams)] = `[{"path":"/haha","rules":[{"type":"HOST_NAME","compare":"ENDS_WITH","value":"/hhh"},{"type":"PATH","compare":"STARTS_WITH","value":"/kkk"}],"action":{"action":"REDIRECT_TO_POOL", "redirectUrl": "http://golang.cafe/a", "redirectHttpCode": 302, "keepQueryString": true}}]`

	// 			g.Expect(k8sClient.Update(ctx, updatedIngress)).Should(Succeed())
	// 		}, timeout, interval).Should(Succeed())

	// 		// Verify policy updated to REDIRECT_TO_POOL and pool is created
	// 		Eventually(func(g Gomega) {
	// 			lbcList, err := listLbcByIngress(ingressName, namespace)
	// 			g.Expect(err).ShouldNot(HaveOccurred())
	// 			g.Expect(lbcList.Items).Should(HaveLen(1))

	// 			lbc := &lbcList.Items[0]
	// 			g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
	// 			loadbalancerId := *lbc.Status.LoadBalancerId

	// 			loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
	// 			g.Expect(err).ShouldNot(HaveOccurred())
	// 			g.Expect(loadbalancer).ShouldNot(BeNil())

	// 			// Check listener
	// 			listeners, err := vngcloudRepo.ListListenerOfLB(ctx, loadbalancer.UUID)
	// 			g.Expect(err).ShouldNot(HaveOccurred())
	// 			g.Expect(listeners).ShouldNot(BeNil())
	// 			g.Expect(listeners.Items).Should(HaveLen(1))

	// 			listener := listeners.Items[0]

	// 			// Check policy with REDIRECT_TO_POOL action
	// 			policies, err := vngcloudRepo.ListPolicyOfListener(ctx, loadbalancer.UUID, listener.UUID)
	// 			g.Expect(err).ShouldNot(HaveOccurred())
	// 			g.Expect(policies).ShouldNot(BeNil())
	// 			g.Expect(policies.Items).Should(HaveLen(1))

	// 			policy := policies.Items[0]
	// 			g.Expect(policy.Action).Should(Equal("REDIRECT_TO_POOL"))
	// 			g.Expect(policy.L7Rules).Should(HaveLen(2))

	// 			expectRuleType := []string{"HOST_NAME", "PATH"}
	// 			expectRuleCompare := []string{"ENDS_WITH", "STARTS_WITH"}
	// 			expectRuleValue := []string{"/hhh", "/kkk"}
	// 			for _, rule := range policy.L7Rules {
	// 				g.Expect(rule.RuleType).Should(BeElementOf(expectRuleType))
	// 				g.Expect(rule.CompareType).Should(BeElementOf(expectRuleCompare))
	// 				g.Expect(rule.RuleValue).Should(BeElementOf(expectRuleValue))
	// 				expectRuleType = removeFisrt(expectRuleType, rule.RuleType)
	// 				expectRuleCompare = removeFisrt(expectRuleCompare, rule.CompareType)
	// 				expectRuleValue = removeFisrt(expectRuleValue, rule.RuleValue)
	// 			}

	// 			// Check pool is created for REDIRECT_TO_POOL action
	// 			pools, err := vngcloudRepo.ListPool(ctx, loadbalancer.UUID)
	// 			g.Expect(err).ShouldNot(HaveOccurred())
	// 			g.Expect(pools).ShouldNot(BeNil())
	// 			g.Expect(pools.Items).Should(HaveLen(1))
	// 			g.Expect(pools.Items[0].Name).Should(ContainSubstring("default-service-foo-80"))
	// 		}, timeout*3, interval).Should(Succeed())

	// 		// Update to REDIRECT_TO_URL action
	// 		Eventually(func(g Gomega) {
	// 			updatedIngress := &networkingv1.Ingress{}
	// 			g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: ingressName, Namespace: namespace}, updatedIngress)).Should(Succeed())

	// 			if updatedIngress.Annotations == nil {
	// 				updatedIngress.Annotations = map[string]string{}
	// 			}
	// 			updatedIngress.Annotations[fmt.Sprintf("%s/%s", domain.INGRESS_ANNOTATION_PREFIX, annotations.SuffixImplementationSpecificParams)] = `[{"path":"/haha","rules":[{"type":"HOST_NAME","compare":"ENDS_WITH","value":"/hhh"},{"type":"PATH","compare":"STARTS_WITH","value":"/kkk"}],"action":{"action":"REDIRECT_TO_URL", "redirectUrl": "http://golang.cafe/a", "redirectHttpCode": 302, "keepQueryString": true}}]`

	// 			g.Expect(k8sClient.Update(ctx, updatedIngress)).Should(Succeed())
	// 		}, timeout, interval).Should(Succeed())

	// 		// Verify policy updated to REDIRECT_TO_URL and pool is removed
	// 		Eventually(func(g Gomega) {
	// 			lbcList, err := listLbcByIngress(ingressName, namespace)
	// 			g.Expect(err).ShouldNot(HaveOccurred())
	// 			g.Expect(lbcList.Items).Should(HaveLen(1))

	// 			lbc := &lbcList.Items[0]
	// 			g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
	// 			loadbalancerId := *lbc.Status.LoadBalancerId

	// 			loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
	// 			g.Expect(err).ShouldNot(HaveOccurred())
	// 			g.Expect(loadbalancer).ShouldNot(BeNil())

	// 			// Check listener
	// 			listeners, err := vngcloudRepo.ListListenerOfLB(ctx, loadbalancer.UUID)
	// 			g.Expect(err).ShouldNot(HaveOccurred())
	// 			g.Expect(listeners).ShouldNot(BeNil())
	// 			g.Expect(listeners.Items).Should(HaveLen(1))

	// 			listener := listeners.Items[0]

	// 			// Check policy with REDIRECT_TO_URL action
	// 			policies, err := vngcloudRepo.ListPolicyOfListener(ctx, loadbalancer.UUID, listener.UUID)
	// 			g.Expect(err).ShouldNot(HaveOccurred())
	// 			g.Expect(policies).ShouldNot(BeNil())
	// 			g.Expect(policies.Items).Should(HaveLen(1))

	// 			policy := policies.Items[0]
	// 			g.Expect(policy.Action).Should(Equal("REDIRECT_TO_URL"))
	// 			g.Expect(policy.RedirectURL).Should(Equal("http://golang.cafe/a"))
	// 			g.Expect(policy.RedirectHTTPCode).Should(Equal(302))
	// 			g.Expect(policy.KeepQueryString).Should(BeTrue())
	// 			g.Expect(policy.L7Rules).Should(HaveLen(2))

	// 			expectRuleType := []string{"HOST_NAME", "PATH"}
	// 			expectRuleCompare := []string{"ENDS_WITH", "STARTS_WITH"}
	// 			expectRuleValue := []string{"/hhh", "/kkk"}
	// 			for _, rule := range policy.L7Rules {
	// 				g.Expect(rule.RuleType).Should(BeElementOf(expectRuleType))
	// 				g.Expect(rule.CompareType).Should(BeElementOf(expectRuleCompare))
	// 				g.Expect(rule.RuleValue).Should(BeElementOf(expectRuleValue))
	// 				expectRuleType = removeFisrt(expectRuleType, rule.RuleType)
	// 				expectRuleCompare = removeFisrt(expectRuleCompare, rule.CompareType)
	// 				expectRuleValue = removeFisrt(expectRuleValue, rule.RuleValue)
	// 			}

	// 			// Check pool is removed for REDIRECT_TO_URL action
	// 			pools, err := vngcloudRepo.ListPool(ctx, loadbalancer.UUID)
	// 			g.Expect(err).ShouldNot(HaveOccurred())
	// 			g.Expect(pools).ShouldNot(BeNil())
	// 			g.Expect(pools.Items).Should(BeEmpty())
	// 		}, timeout*3, interval).Should(Succeed())

	// 		// Cleanup
	// 		Expect(k8sClient.Delete(ctx, ingress)).Should(Succeed())
	// 		Expect(k8sClient.Delete(ctx, service)).Should(Succeed())
	// 		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, endpoint))).Should(Succeed())
	// 	})
	// })

	Context("When create ingress with auto-reorder-policies annotation", func() {
		It("should automatically reorder policies when annotation is added", func() {

			serviceName := testServiceNameGogsf
			namespace := testDefaultNamespace
			ingressName := testServiceNameGogsf

			// Create endpoint
			endpoint := newEndpointResource(serviceName, namespace)
			Expect(k8sClient.Create(ctx, endpoint)).Should(Succeed())

			// Create Service
			service := newServiceNodePortResource(serviceName, namespace)
			service.Spec.Ports = []corev1.ServicePort{
				{Name: "http", Port: 80, TargetPort: intstr.FromInt(80), Protocol: corev1.ProtocolTCP, NodePort: 30000},
			}
			Expect(k8sClient.Create(ctx, service)).Should(Succeed())

			// Create Ingress WITHOUT auto-reorder-policies annotation
			ingress := newIngressResource(ingressName, namespace)
			Expect(ingress).NotTo(BeNil())
			ingress.Spec.DefaultBackend = nil
			ingress.Spec.Rules = []networkingv1.IngressRule{
				{
					Host: "test.com",
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									PathType: ptr.To(networkingv1.PathTypePrefix),
									Path:     "/",
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: serviceName,
											Port: networkingv1.ServiceBackendPort{Number: 80},
										},
									},
								},
								{
									PathType: ptr.To(networkingv1.PathTypePrefix),
									Path:     "/web",
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: serviceName,
											Port: networkingv1.ServiceBackendPort{Number: 80},
										},
									},
								},
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, ingress)).Should(Succeed())

			var loadbalancerUUID string
			var listenerUUID string
			var initialRootPosition int
			var initialWebPosition int

			// Verify initial state: "/" has lower position (appears first) than "/web"
			Eventually(func(g Gomega) {
				lbcList, err := listLbcByIngress(ingressName, namespace)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(lbcList.Items).Should(HaveLen(1))

				lbc := &lbcList.Items[0]
				g.Expect(lbc.Status.LoadBalancerId).ShouldNot(BeNil())
				loadbalancerId := *lbc.Status.LoadBalancerId

				loadbalancer, err := vngcloudRepo.GetLoadBalancerByID(ctx, loadbalancerId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(loadbalancer).ShouldNot(BeNil())
				loadbalancerUUID = loadbalancer.UUID

				// Check listener
				listeners, err := vngcloudRepo.ListListenerOfLB(ctx, loadbalancer.UUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(listeners).ShouldNot(BeNil())
				g.Expect(listeners.Items).Should(HaveLen(1))
				listenerUUID = listeners.Items[0].UUID

				// Check policies
				policies, err := vngcloudRepo.ListPolicyOfListener(ctx, loadbalancer.UUID, listenerUUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(policies).ShouldNot(BeNil())
				g.Expect(policies.Items).Should(HaveLen(2))

				// Find "/" and "/web" policies
				for _, policy := range policies.Items {
					if len(policy.L7Rules) > 0 {
						for _, rule := range policy.L7Rules {
							switch rule.RuleValue {
							case "/":
								initialRootPosition = policy.Position
							case "/web":
								initialWebPosition = policy.Position
							}
						}
					}
				}

				// Verify "/" has lower position (higher priority) than "/web" initially
				g.Expect(initialRootPosition).Should(BeNumerically("<", initialWebPosition))
			}, timeout, interval).Should(Succeed())

			// Update ingress to add auto-reorder-policies annotation
			Eventually(func(g Gomega) {
				updatedIngress := &networkingv1.Ingress{}
				err := k8sClient.Get(ctx, client.ObjectKey{Name: ingressName, Namespace: namespace}, updatedIngress)
				g.Expect(err).ShouldNot(HaveOccurred())

				updatedIngress.Annotations[fmt.Sprintf("%s/%s", domain.INGRESS_ANNOTATION_PREFIX, annotations.SuffixAutoReorderPolicies)] = "true"
				err = k8sClient.Update(ctx, updatedIngress)
				g.Expect(err).ShouldNot(HaveOccurred())
			}, timeout, interval).Should(Succeed())

			// Verify policies are reordered: "/web" should now have lower position (higher priority) than "/"
			Eventually(func(g Gomega) {
				policies, err := vngcloudRepo.ListPolicyOfListener(ctx, loadbalancerUUID, listenerUUID)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(policies).ShouldNot(BeNil())
				g.Expect(policies.Items).Should(HaveLen(2))

				var rootPosition int
				var webPosition int

				// Find "/" and "/web" policies
				for _, policy := range policies.Items {
					if len(policy.L7Rules) > 0 {
						for _, rule := range policy.L7Rules {
							switch rule.RuleValue {
							case "/":
								rootPosition = policy.Position
							case "/web":
								webPosition = policy.Position
							}
						}
					}
				}

				// Verify "/web" now has lower position (higher priority) than "/"
				// More specific paths should have higher priority (lower position number)
				g.Expect(webPosition).Should(BeNumerically("<", rootPosition))
			}, timeout*2, interval).Should(Succeed())

			// Cleanup
			Expect(k8sClient.Delete(ctx, ingress)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, service)).Should(Succeed())
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, endpoint))).Should(Succeed())
		})
	})

})
