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

package nsg_controller

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/vngcloud_repo/vngcloud_mocks"
)

var _ = Describe("NodeSecurityGroup Controller", func() {
	BeforeEach(func() {
		// Best-effort drain of any K8s NSGs left over from a previous
		// spec, then reset the in-memory mock VNGCloud state so this
		// spec starts from a known-clean slate regardless of the
		// previous spec's finalizer-chain progress.
		nsgList := &v1alpha1.NodeSecurityGroupList{}
		_ = k8sClient.List(ctx, nsgList)
		for i := range nsgList.Items {
			_ = k8sClient.Delete(ctx, &nsgList.Items[i])
		}
		vngcloudRepo.Reset()
	})

	AfterEach(func() {
		// Ensure clean state after test
		expectNoSecurityGroups()
		expectNoNSGs()
	})

	Context("When managing NodeSecurityGroup lifecycle", func() {
		It("should create and manage NodeSecurityGroup with managed security group", func() {
			nsgName := "test-nsg"
			namespace := "default"

			// Create NodeSecurityGroup
			nsg := newNodeSecurityGroupResource(nsgName, namespace)
			Expect(k8sClient.Create(ctx, nsg)).Should(Succeed())

			// Verify finalizer was added
			Eventually(func(g Gomega) {
				updatedNsg := &v1alpha1.NodeSecurityGroup{}
				err := k8sClient.Get(ctx, client.ObjectKey{Name: nsgName, Namespace: namespace}, updatedNsg)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(updatedNsg.Finalizers).Should(ContainElement(domain.NsgFinalizer))
			}, timeout*2, interval).Should(Succeed())

			// Verify managed security group was created in VNG Cloud
			Eventually(func(g Gomega) {
				updatedNsg := &v1alpha1.NodeSecurityGroup{}
				err := k8sClient.Get(ctx, client.ObjectKey{Name: nsgName, Namespace: namespace}, updatedNsg)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(updatedNsg.Status.ManagedSecurityGroup).ShouldNot(BeNil())
				g.Expect(updatedNsg.Status.ManagedSecurityGroup.Id).ShouldNot(BeNil())

				secgroupId := *updatedNsg.Status.ManagedSecurityGroup.Id
				secgroup, err := vngcloudRepo.GetSecurityGroup(ctx, secgroupId)
				g.Expect(err).ShouldNot(HaveOccurred())
				g.Expect(secgroup).ShouldNot(BeNil())
				g.Expect(secgroup.Name).Should(Equal(updatedNsg.Spec.ManagedSecurityGroup.Name))
			}, timeout*2, interval).Should(Succeed())

			// Cleanup - delete the resource created by this test
			Expect(k8sClient.Delete(ctx, nsg)).Should(Succeed())
		})
	})

	Context("When attaching existing security groups to nodes", func() {
		It("should attach security groups to selected nodes and handle updates", func() {
			nsgName := "test-nsg-attach"
			namespace := "default"

			// Step 1: Create 3 security groups before test
			sg1, err := vngcloudRepo.CreateSecurityGroup(ctx, "test-sg-1", "Security group 1")
			Expect(err).ShouldNot(HaveOccurred())
			Expect(sg1).ShouldNot(BeNil())

			sg2, err := vngcloudRepo.CreateSecurityGroup(ctx, "test-sg-2", "Security group 2")
			Expect(err).ShouldNot(HaveOccurred())
			Expect(sg2).ShouldNot(BeNil())

			sg3, err := vngcloudRepo.CreateSecurityGroup(ctx, "test-sg-3", "Security group 3")
			Expect(err).ShouldNot(HaveOccurred())
			Expect(sg3).ShouldNot(BeNil())

			// Step 2: Create NSG using 2 security groups (sg1, sg2) and select 2/4 nodes
			nsg := &v1alpha1.NodeSecurityGroup{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nsgName,
					Namespace: namespace,
				},
				Spec: v1alpha1.NodeSecurityGroupSpec{
					SelectNodeLabels: map[string]string{
						"nodeGroup": "mock-node-group-a", // Selects mock-node-1 and mock-node-2
					},
					AttachSecurityGroups: []string{sg1.Id, sg2.Id},
				},
			}
			Expect(k8sClient.Create(ctx, nsg)).Should(Succeed())

			// Step 3: Verify security groups attached to 2 nodes (node-1, node-2)
			Eventually(func(g Gomega) {
				// Verify sg1 is attached to node-1 and node-2
				verifySecurityGroupAttachedToServers(g, sg1.Id, []string{
					vngcloud_mocks.ServerId1,
					vngcloud_mocks.ServerId2,
				})

				// Verify sg2 is attached to node-1 and node-2
				verifySecurityGroupAttachedToServers(g, sg2.Id, []string{
					vngcloud_mocks.ServerId1,
					vngcloud_mocks.ServerId2,
				})

				// Verify sg3 is NOT attached to any nodes
				verifySecurityGroupAttachedToServers(g, sg3.Id, []string{})

				// Verify NSG status shows 2 selected nodes (node-1, node-2)
				verifyNSGSelectedNodes(g, nsgName, namespace, []string{"mock-node-1", "mock-node-2"})
			}, timeout*4, interval).Should(Succeed())

			// Step 4: Update NSG to use sg2 (keep), sg3 (new), remove sg1
			// Also update node selection to select 3/4 nodes (using flavor label)
			Eventually(func() error {
				updatedNsg := &v1alpha1.NodeSecurityGroup{}
				err := k8sClient.Get(ctx, client.ObjectKey{Name: nsgName, Namespace: namespace}, updatedNsg)
				if err != nil {
					return err
				}
				// Update to select nodes with flavor=s-general-2
				// This will select: mock-node-2, mock-node-3, mock-node-4 (3 nodes)
				// Compared to initial (node-1, node-2):
				// - Kept: node-2
				// - Removed: node-1
				// - New: node-3, node-4
				updatedNsg.Spec.SelectNodeLabels = map[string]string{
					"node.kubernetes.io/flavor": "s-general-2",
				}
				// Change security groups: keep sg2, add sg3, remove sg1
				updatedNsg.Spec.AttachSecurityGroups = []string{sg2.Id, sg3.Id}
				return k8sClient.Update(ctx, updatedNsg)
			}, timeout, interval).Should(Succeed())

			// Step 6: Verify updated security group attachments (node-2, node-3, node-4)
			Eventually(func(g Gomega) {
				// Verify sg1 is detached from all nodes (removed)
				verifySecurityGroupAttachedToServers(g, sg1.Id, []string{})

				// Verify sg2 is attached to node-2, node-3, node-4 (kept)
				verifySecurityGroupAttachedToServers(g, sg2.Id, []string{
					vngcloud_mocks.ServerId2,
					vngcloud_mocks.ServerId3,
					vngcloud_mocks.ServerId4,
				})

				// Verify sg3 is attached to node-2, node-3, node-4 (new)
				verifySecurityGroupAttachedToServers(g, sg3.Id, []string{
					vngcloud_mocks.ServerId2,
					vngcloud_mocks.ServerId3,
					vngcloud_mocks.ServerId4,
				})

				// Verify NSG status shows 3 selected nodes (node-2, node-3, node-4)
				verifyNSGSelectedNodes(g, nsgName, namespace, []string{"mock-node-2", "mock-node-3", "mock-node-4"})
			}, timeout*4, interval).Should(Succeed())

			// Cleanup - delete NSG and security groups
			Expect(k8sClient.Delete(ctx, nsg)).Should(Succeed())

			Eventually(func() error {
				return vngcloudRepo.DeleteSecurityGroup(ctx, sg1.Id)
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				return vngcloudRepo.DeleteSecurityGroup(ctx, sg2.Id)
			}, timeout, interval).Should(Succeed())

			Eventually(func() error {
				return vngcloudRepo.DeleteSecurityGroup(ctx, sg3.Id)
			}, timeout, interval).Should(Succeed())
		})
	})

})

// ============================================================================
// Helper functions to verify security group attachments
// ============================================================================

func verifySecurityGroupAttachedToServers(g Gomega, secgroupId string, expectedServerIds []string) {
	servers, err := vngcloudRepo.ListServerBySecgroupID(ctx, secgroupId)
	g.Expect(err).ShouldNot(HaveOccurred())
	g.Expect(servers).ShouldNot(BeNil())
	g.Expect(servers.Items).Should(HaveLen(len(expectedServerIds)), "security group should be attached to %d nodes", len(expectedServerIds))

	actualServerIds := make([]string, len(servers.Items))
	for i, s := range servers.Items {
		actualServerIds[i] = s.Uuid
	}

	for _, expectedId := range expectedServerIds {
		g.Expect(actualServerIds).Should(ContainElement(expectedId), "should contain server %s", expectedId)
	}
}

func verifyNSGSelectedNodes(g Gomega, nsgName, namespace string, expectedNodeNames []string) {
	updatedNsg := &v1alpha1.NodeSecurityGroup{}
	err := k8sClient.Get(ctx, client.ObjectKey{Name: nsgName, Namespace: namespace}, updatedNsg)
	g.Expect(err).ShouldNot(HaveOccurred())
	g.Expect(updatedNsg.Status.SelectedNodes).Should(HaveLen(len(expectedNodeNames)))

	actualNodeNames := make([]string, len(updatedNsg.Status.SelectedNodes))
	for i, n := range updatedNsg.Status.SelectedNodes {
		actualNodeNames[i] = n.Name
	}

	for _, expectedName := range expectedNodeNames {
		g.Expect(actualNodeNames).Should(ContainElement(expectedName), "should contain node %s", expectedName)
	}
}

// ============================================================================
// Helper functions to create NodeSecurityGroup test resources
// ============================================================================

func newNodeSecurityGroupResource(name, namespace string) *v1alpha1.NodeSecurityGroup {
	description := "Test managed security group"
	return &v1alpha1.NodeSecurityGroup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "NodeSecurityGroup",
			APIVersion: "vks.vngcloud.vn/v1alpha1",
		},
		Spec: v1alpha1.NodeSecurityGroupSpec{
			ManagedSecurityGroup: &v1alpha1.ManagedSecurityGroup{
				Name:        "test-managed-sg",
				Description: &description,
				Rules: []v1alpha1.NodeSecurityGroupRule{
					{
						FromPort:  22,
						ToPort:    22,
						Protocol:  "tcp",
						CIDR:      "0.0.0.0/0",
						Direction: "ingress",
					},
				},
			},
		},
	}
}
