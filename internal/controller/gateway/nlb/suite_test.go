package nlb

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/k8s_repo"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/vngcloud_repo/vngcloud_mocks"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/nlb_gateway_uc"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/k8s"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

var (
	ctx          context.Context
	cancel       context.CancelFunc
	testEnv      *envtest.Environment
	cfg          *rest.Config
	k8sClient    client.Client
	vngcloudRepo *vngcloud_mocks.MockProvider
)

func TestNLBController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "NLB Gateway Controller Suite")
}

// gatewayAPIExperimentalCRDPath resolves the gateway-api experimental CRD
// directory (TCPRoute/UDPRoute live only there) from the module cache.
func gatewayAPIExperimentalCRDPath() string {
	out, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		panic(err)
	}
	return filepath.Join(strings.TrimSpace(string(out)), "sigs.k8s.io", "gateway-api@v1.2.0", "config", "crd", "experimental")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	ctx, cancel = context.WithCancel(context.TODO())

	Expect(corev1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(v1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(gwv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(gwv1.Install(scheme.Scheme)).To(Succeed())
	Expect(gwv1a2.Install(scheme.Scheme)).To(Succeed())
	Expect(gwv1beta1.Install(scheme.Scheme)).To(Succeed())

	By("bootstrapping test environment (with experimental gateway-api CRDs)")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "..", "config", "crd", "bases"),
			gatewayAPIExperimentalCRDPath(),
		},
		ErrorIfCRDPathMissing: true,
	}
	if binDir := getFirstFoundEnvTestBinaryDir(); binDir != "" {
		testEnv.BinaryAssetsDirectory = binDir
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	// Disable the metrics listener so the suite doesn't collide with a locally
	// running controller (or another envtest) on :8080.
	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).ToNot(HaveOccurred())

	k8sRepo := k8s_repo.NewK8sRepository(k8sManager.GetClient())
	vngcloudRepo = vngcloud_mocks.NewMockProvider()
	Expect(vngcloudRepo.Init(nil)).To(Succeed())

	By("creating a test node with a vngcloud providerID")
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "test-nlb-node-1"},
		Spec:       corev1.NodeSpec{ProviderID: "vngcloud://" + vngcloud_mocks.ServerId1},
		Status: corev1.NodeStatus{
			Addresses:  []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.1"}},
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
		},
	}
	Expect(k8sClient.Create(ctx, node)).To(Succeed())
	Expect(k8sClient.Status().Update(ctx, node)).To(Succeed())

	endpointResolver := utils.NewDefaultEndpointResolver(ctx, k8sManager.GetClient())
	nlbUC := nlb_gateway_uc.NewNLBGatewayUseCase("test-cluster", k8sRepo, vngcloudRepo, endpointResolver, k8sManager.GetClient(), k8s.NewDefaultFinalizerManager(k8sManager.GetClient(), ctrl.Log))

	gwReconciler := NewGatewayReconciler(k8sManager.GetClient(), k8sManager.GetScheme(), nlbUC, 1)
	Expect(gwReconciler.SetupWithManager(ctx, k8sManager)).To(Succeed())

	gcReconciler := NewGatewayClassReconciler(k8sManager.GetClient(), k8sManager.GetScheme())
	Expect(gcReconciler.SetupWithManager(ctx, k8sManager)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(k8sManager.Start(ctx)).To(Succeed(), "failed to run manager")
	}()
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	Expect(testEnv.Stop()).To(Succeed())
})

func getFirstFoundEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "..", "..", "bin", "k8s")
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
