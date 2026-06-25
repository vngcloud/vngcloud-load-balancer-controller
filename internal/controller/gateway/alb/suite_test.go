package alb

import (
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	gatewaypolicies "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/policies"
	gatewayshared "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/k8s_repo"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/vngcloud_repo/vngcloud_mocks"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/alb_gateway_uc"
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

func TestALBController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ALB Gateway Controller Suite")
}

// gatewayAPICRDPath resolves the gateway-api standard CRD directory from the Go
// module cache. The version must match the one in go.mod.
func gatewayAPICRDPath() string {
	out, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		panic(err)
	}
	return filepath.Join(strings.TrimSpace(string(out)), "sigs.k8s.io", "gateway-api@v1.2.0", "config", "crd", "standard")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	logrus.SetLevel(logrus.DebugLevel)
	logrus.SetReportCaller(true)
	logrus.SetFormatter(&logrus.TextFormatter{
		DisableTimestamp: true,
		CallerPrettyfier: func(frame *goruntime.Frame) (function string, file string) {
			fileName := path.Base(frame.File) + ":" + strconv.Itoa(frame.Line)
			return "", fileName
		},
	})

	ctx, cancel = context.WithCancel(context.TODO())

	var err error
	err = corev1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = v1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = gwv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = gwv1.Install(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = gwv1beta1.Install(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "..", "config", "crd", "bases"),
			gatewayAPICRDPath(),
		},
		ErrorIfCRDPathMissing: true,
	}

	// Retrieve the first found binary directory to allow running tests from IDEs
	if binDir := getFirstFoundEnvTestBinaryDir(); binDir != "" {
		testEnv.BinaryAssetsDirectory = binDir
	}

	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// Setup manager
	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	k8sRepo := k8s_repo.NewK8sRepository(k8sManager.GetClient())

	// Setup mock VNG Cloud repository
	vngcloudRepo = vngcloud_mocks.NewMockProvider()
	err = vngcloudRepo.Init(nil)
	Expect(err).NotTo(HaveOccurred())

	// Create a Node with a providerID that the MockProvider's GetServerNetworkInfo knows about.
	// ServerId1 is "ins-00000000-0000-0000-0000-000000000001" and maps to MockSubnetID in MockServerIdToSubnet.
	By("creating test node with vngcloud providerID")
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-alb-node-1",
		},
		Spec: corev1.NodeSpec{
			// Format expected by GetProviderIdFromNode: "vngcloud://ins-<uuid>"
			ProviderID: "vngcloud://" + vngcloud_mocks.ServerId1,
		},
		Status: corev1.NodeStatus{
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "10.0.0.1"},
			},
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, node)).To(Succeed())
	Expect(k8sClient.Status().Update(ctx, node)).To(Succeed())

	// Build the ALB Gateway use case
	endpointResolver := utils.NewDefaultEndpointResolver(ctx, k8sManager.GetClient())
	albGatewayUseCase := alb_gateway_uc.NewALBGatewayUseCase(
		"test-cluster",
		k8sRepo,
		vngcloudRepo,
		endpointResolver,
		k8sManager.GetClient(),
		k8s.NewDefaultFinalizerManager(k8sManager.GetClient(), ctrl.Log),
	)

	// Setup Gateway reconciler
	gatewayReconciler := NewGatewayReconciler(
		k8sManager.GetClient(),
		k8sManager.GetScheme(),
		albGatewayUseCase,
		1,
	)
	err = gatewayReconciler.SetupWithManager(ctx, k8sManager)
	Expect(err).ToNot(HaveOccurred())

	// Setup GatewayClass reconciler
	gcReconciler := NewGatewayClassReconciler(k8sManager.GetClient(), k8sManager.GetScheme())
	err = gcReconciler.SetupWithManager(ctx, k8sManager)
	Expect(err).ToNot(HaveOccurred())

	// Policy validators (status-only) under test, mirroring cmd/main.go.
	for _, pr := range gatewaypolicies.AllReconcilers(k8sManager.GetClient()) {
		Expect(pr.SetupWithManager(ctx, k8sManager)).To(Succeed())
	}

	// Register field indexes (mirrors cmd/main.go ordering: after SetupWithManager, before mgr.Start)
	err = gatewayshared.RegisterIndexes(ctx, k8sManager)
	Expect(err).ToNot(HaveOccurred())

	// Start the manager
	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

func getFirstFoundEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		logf.Log.Error(err, "Failed to read directory", "path", basePath)
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}
