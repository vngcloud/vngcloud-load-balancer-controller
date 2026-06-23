package k8sbatch_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/common"
	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
)

func TestK8sbatch(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "k8sbatch Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	ctx, cancel = context.WithCancel(context.TODO())

	Expect(v1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: false,
	}
	if dir := firstFoundEnvTestBinaryDir(); dir != "" {
		testEnv.BinaryAssetsDirectory = dir
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	Expect(testEnv.Stop()).To(Succeed())
})

// firstFoundEnvTestBinaryDir locates the envtest binary directory under
// bin/k8s/, mirroring internal/controller/core/suite_test.go so the suite
// runs from IDEs as well as via `make test`.
func firstFoundEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}

// --- Test fixture helpers ---

// newTestLBC builds a LoadBalancerConfig that satisfies the CRD's required
// fields (loadBalancerName, subnetId, type, vpcId, zoneId) so it can be
// created in envtest. Status fields are left zero-valued; tests mutate them
// via the batcher.
func newTestLBC(namespace, name string) *v1alpha1.LoadBalancerConfig {
	return &v1alpha1.LoadBalancerConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: v1alpha1.LoadBalancerConfigSpec{
			Type:             loadbalancerv2.LoadBalancerTypeLayer4,
			LoadBalancerName: "test-lb-" + name,
			SubnetId:         "subnet-test",
			VpcId:            "vpc-test",
			ZoneId:           common.Zone("zone-test"),
		},
	}
}

// newTestNSG builds a NodeSecurityGroup with no required-field obligations.
func newTestNSG(namespace, name string) *v1alpha1.NodeSecurityGroup {
	return &v1alpha1.NodeSecurityGroup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: v1alpha1.NodeSecurityGroupSpec{
			SelectNodeLabels: map[string]string{"role": "node"},
		},
	}
}
