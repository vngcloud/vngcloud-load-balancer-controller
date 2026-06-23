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

package vglb_controller

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

const (
	vglbTimeout          = time.Second * 30
	vglbInterval         = time.Second * 1
	testDefaultNamespace = "default"
)

var _ = Describe("VngcloudGlobalLoadBalancer Controller", func() {

	Context("Create flow", func() {
		It("should create GLBC from VGLB with matching NodePort Service", func() {
			svcName := "vglb-create-test"
			ns := testDefaultNamespace

			// Create NodePort Service with port 80
			svc := newNodePortService(svcName, ns, []corev1.ServicePort{
				{
					Name:     "http",
					Port:     80,
					Protocol: corev1.ProtocolTCP,
					NodePort: 30080,
				},
			})
			Expect(k8sClient.Create(ctx, svc)).To(Succeed())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, svc)
			})

			// Create VGLB with same name as service
			vglbObj := newVGLBResource(svcName, ns)
			Expect(k8sClient.Create(ctx, vglbObj)).To(Succeed())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, vglbObj)
			})

			// Fetch VGLB again to get UID populated
			Eventually(func() error {
				return k8sClient.Get(ctx, client.ObjectKey{Name: svcName, Namespace: ns}, vglbObj)
			}, vglbTimeout, vglbInterval).Should(Succeed())

			// Eventually: find GLBC by owner labels
			var foundGLBC *v1alpha1.GlobalLoadBalancerConfig
			Eventually(func(g Gomega) {
				// Refresh VGLB to get latest UID
				err := k8sClient.Get(ctx, client.ObjectKey{Name: svcName, Namespace: ns}, vglbObj)
				g.Expect(err).ShouldNot(HaveOccurred())

				glbc, err := findGLBCByOwnerLabels(ctx, k8sClient, vglbObj)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(glbc).ShouldNot(BeNil())
				foundGLBC = glbc
			}, vglbTimeout, vglbInterval).Should(Succeed())

			// Assert GLBC spec: name has the vks- prefix and embeds a
			// recognizable slice of the service name. utils.ValidateName
			// converts the underscores from NameHelper's format string
			// into dashes, and TrimString truncates "vglb-create-test"
			// down to its first 10 characters before assembly.
			Expect(strings.HasPrefix(foundGLBC.Spec.Name, "vks-")).To(BeTrue(),
				"Expected GLBC Name to start with 'vks-', got: %s", foundGLBC.Spec.Name)
			Expect(strings.Contains(foundGLBC.Spec.Name, "vglb-creat")).To(BeTrue(),
				"Expected GLBC Name to contain truncated service name 'vglb-creat', got: %s", foundGLBC.Spec.Name)

			// Assert: 1 pool
			Expect(foundGLBC.Spec.GlobalPools).To(HaveLen(1),
				"Expected 1 global pool, got: %d", len(foundGLBC.Spec.GlobalPools))
			pool := foundGLBC.Spec.GlobalPools[0]
			Expect(pool.Name).To(HavePrefix("pool-"),
				"Expected pool name to start with 'pool-', got: %s", pool.Name)

			// Assert: 1 pool member group named "hcm-net-test-vpc"
			Expect(pool.PoolMembers).To(HaveLen(1),
				"Expected 1 pool member group, got: %d", len(pool.PoolMembers))
			pm := pool.PoolMembers[0]
			Expect(pm.Name).To(Equal("hcm-net-test-vpc"),
				"Expected pool member group name 'hcm-net-test-vpc', got: %s", pm.Name)
			Expect(pm.Region).To(Equal("hcm"),
				"Expected pool member group region 'hcm', got: %s", pm.Region)

			// Assert: 1 listener for port 80
			Expect(foundGLBC.Spec.GlobalListeners).To(HaveLen(1),
				"Expected 1 global listener, got: %d", len(foundGLBC.Spec.GlobalListeners))
			listener := foundGLBC.Spec.GlobalListeners[0]
			Expect(listener.ProtocolPort).To(Equal(80),
				"Expected listener port 80, got: %d", listener.ProtocolPort)
		})
	})

	Context("Delete flow", func() {
		It("should delete GLBC when VGLB is deleted", func() {
			svcName := "vglb-delete-test"
			ns := testDefaultNamespace

			// Create Service and VGLB
			svc := newNodePortService(svcName, ns, []corev1.ServicePort{
				{
					Name:     "http",
					Port:     80,
					Protocol: corev1.ProtocolTCP,
					NodePort: 30081,
				},
			})
			Expect(k8sClient.Create(ctx, svc)).To(Succeed())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, svc)
			})

			vglbObj := newVGLBResource(svcName, ns)
			Expect(k8sClient.Create(ctx, vglbObj)).To(Succeed())

			// Fetch VGLB to get UID
			Eventually(func() error {
				return k8sClient.Get(ctx, client.ObjectKey{Name: svcName, Namespace: ns}, vglbObj)
			}, vglbTimeout, vglbInterval).Should(Succeed())

			// Wait until GLBC exists
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: svcName, Namespace: ns}, vglbObj)
				g.Expect(err).ShouldNot(HaveOccurred())

				glbc, err := findGLBCByOwnerLabels(ctx, k8sClient, vglbObj)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(glbc).ShouldNot(BeNil())
			}, vglbTimeout, vglbInterval).Should(Succeed())

			// Delete the VGLB
			Expect(k8sClient.Delete(ctx, vglbObj)).To(Succeed())

			// Eventually: GLBC is gone (list by owner labels returns 0 items)
			Eventually(func(g Gomega) {
				glbcList := &v1alpha1.GlobalLoadBalancerConfigList{}
				err := k8sClient.List(ctx, glbcList, client.InNamespace(ns))
				g.Expect(err).ShouldNot(HaveOccurred())

				// Filter only GLBCs that had this VGLB as owner (by name label, UID may be reused)
				var ownedByVglb []v1alpha1.GlobalLoadBalancerConfig
				for _, glbc := range glbcList.Items {
					if glbc.Labels["vks.vngcloud.vn/owner-resource-name"] == svcName &&
						glbc.DeletionTimestamp.IsZero() {
						ownedByVglb = append(ownedByVglb, glbc)
					}
				}
				g.Expect(ownedByVglb).To(BeEmpty(),
					"Expected GLBC owned by VGLB to be deleted")
			}, vglbTimeout*2, vglbInterval).Should(Succeed())

			// Verify VGLB itself is deleted
			Eventually(func() bool {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: svcName, Namespace: ns}, vglbObj)
				return errors.IsNotFound(err)
			}, vglbTimeout, vglbInterval).Should(BeTrue(), "Expected VGLB to be deleted")
		})
	})

	Context("Update flow", func() {
		It("should update GLBC when Service ports change", func() {
			svcName := "vglb-update-test"
			ns := testDefaultNamespace

			// Create Service with port 80
			svc := newNodePortService(svcName, ns, []corev1.ServicePort{
				{
					Name:     "http",
					Port:     80,
					Protocol: corev1.ProtocolTCP,
					NodePort: 30082,
				},
			})
			Expect(k8sClient.Create(ctx, svc)).To(Succeed())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, svc)
			})

			vglbObj := newVGLBResource(svcName, ns)
			Expect(k8sClient.Create(ctx, vglbObj)).To(Succeed())

			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, vglbObj)
			})

			// Fetch VGLB to get UID
			Eventually(func() error {
				return k8sClient.Get(ctx, client.ObjectKey{Name: svcName, Namespace: ns}, vglbObj)
			}, vglbTimeout, vglbInterval).Should(Succeed())

			// Wait until GLBC exists with 1 pool and 1 listener for port 80
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: svcName, Namespace: ns}, vglbObj)
				g.Expect(err).ShouldNot(HaveOccurred())

				glbc, err := findGLBCByOwnerLabels(ctx, k8sClient, vglbObj)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(glbc).ShouldNot(BeNil())
				g.Expect(glbc.Spec.GlobalPools).To(HaveLen(1))
				g.Expect(glbc.Spec.GlobalListeners).To(HaveLen(1))
				g.Expect(glbc.Spec.GlobalListeners[0].ProtocolPort).To(Equal(80))
			}, vglbTimeout, vglbInterval).Should(Succeed())

			// Update Service to add port 443
			updatedSvc := svc.DeepCopy()
			Expect(k8sClient.Get(ctx, client.ObjectKey{Name: svcName, Namespace: ns}, updatedSvc)).To(Succeed())
			updatedSvc.Spec.Ports = []corev1.ServicePort{
				{
					Name:     "http",
					Port:     80,
					Protocol: corev1.ProtocolTCP,
					NodePort: 30082,
				},
				{
					Name:     "https",
					Port:     443,
					Protocol: corev1.ProtocolTCP,
					NodePort: 30083,
				},
			}
			Expect(k8sClient.Update(ctx, updatedSvc)).To(Succeed())

			// Eventually: GLBC has 2 pools and 2 listeners
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, client.ObjectKey{Name: svcName, Namespace: ns}, vglbObj)
				g.Expect(err).ShouldNot(HaveOccurred())

				glbc, err := findGLBCByOwnerLabels(ctx, k8sClient, vglbObj)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(glbc).ShouldNot(BeNil())
				g.Expect(glbc.Spec.GlobalPools).To(HaveLen(2),
					"Expected 2 global pools after service port update")
				g.Expect(glbc.Spec.GlobalListeners).To(HaveLen(2),
					"Expected 2 global listeners after service port update")

				// Verify listener ports: one for 80, one for 443
				ports := make([]int, len(glbc.Spec.GlobalListeners))
				for i, l := range glbc.Spec.GlobalListeners {
					ports[i] = l.ProtocolPort
				}
				g.Expect(ports).To(ContainElement(80), "Expected listener with port 80")
				g.Expect(ports).To(ContainElement(443), "Expected listener with port 443")
			}, vglbTimeout, vglbInterval).Should(Succeed())
		})
	})

})
