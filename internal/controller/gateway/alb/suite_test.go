/*
Copyright 2026.
*/

// Package alb's test suite boots an in-memory Kubernetes API server (envtest)
// alongside the vngcloud_mocks fake LB provider and runs the full controller
// stack — Gateway / GatewayClass / HTTPRoute / TargetGroupConfig /
// ListenerRuleConfig (this package) plus the existing LoadBalancerConfig +
// NodeSecurityGroup reconcilers — so each Ginkgo spec exercises a real
// reconcile loop end-to-end without touching a vngcloud cluster.
//
// Mirrors the pattern in internal/controller/networking/suite_test.go.
package alb

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	gatewayv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/listenerruleconfig"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/targetgroupconfig"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/lbc_controller"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/nsg_controller"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/k8s_repo"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/vngcloud_repo/vngcloud_mocks"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/alb_gateway_uc"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/lbc_uc"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/nsg_uc"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/annotations"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/config"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/k8s"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/lbc"
	lbcmetrics "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/metrics/lbc"
	metricsutil "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/metrics/util"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/nsg"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

const mockClusterID = "k8s-00000000-0000-0000-0000-000000000000"

var (
	ctx          context.Context
	cancel       context.CancelFunc
	testEnv      *envtest.Environment
	cfg          *rest.Config
	k8sClient    client.Client
	vngcloudRepo *vngcloud_mocks.MockProvider // exposed so specs can inspect LB / pool / listener state

	mockConfig = &config.Config{
		Cluster: struct {
			IsRunRemote bool   `mapstructure:"isRunRemote"`
			Namespace   string `mapstructure:"namespace"`
			ClusterID   string `mapstructure:"clusterID"`
			Region      string `mapstructure:"region"`
		}{IsRunRemote: false, ClusterID: mockClusterID},
		LoadBalancerOpts: config.LoadBalancerOpts{
			DefaultL7PackageName:      "ALB_Small",
			DefaultPoolAlgorithm:      "ROUND_ROBIN",
			DefaultScheme:             "Internet",
			DefaultTimeoutClient:      50,
			DefaultTimeoutConnection:  5,
			DefaultTimeoutMember:      50,
			DefaultHealthyThreshold:   3,
			DefaultUnhealthyThreshold: 3,
			DefaultInterval:           30,
			DefaultTimeout:            5,
			DefaultAllowedCidrs:       "0.0.0.0/0",
		},
	}
)

func TestALBGatewayControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ALB Gateway Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	logrus.SetLevel(logrus.InfoLevel)

	ctx, cancel = context.WithCancel(context.TODO())

	By("registering schemes")
	Expect(corev1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(vksv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(gatewayv1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(gwv1.Install(scheme.Scheme)).To(Succeed())
	Expect(gwv1alpha2.Install(scheme.Scheme)).To(Succeed())
	Expect(gwv1beta1.Install(scheme.Scheme)).To(Succeed())

	By("bootstrapping test environment with vngcloud + upstream Gateway API CRDs")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "..", "..", "test", "crds", "gateway-api"),
		},
		ErrorIfCRDPathMissing: true,
	}
	if d := envTestBinaryDir(); d != "" {
		testEnv.BinaryAssetsDirectory = d
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	By("starting manager + full reconciler stack (Gateway / GatewayClass / HTTPRoute / TGC / LRC + LBC + NSG)")
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).ToNot(HaveOccurred())

	finalizerManager := k8s.NewDefaultFinalizerManager(mgr.GetClient(), ctrl.Log)
	k8sRepo := k8s_repo.NewK8sRepository(mgr.GetClient())
	vngcloudRepo = vngcloud_mocks.NewMockProvider()
	Expect(vngcloudRepo.Init(nil)).To(Succeed())

	annotationParser := annotations.NewSuffixAnnotationParser("gateway.vks.vngcloud.vn")
	cniDetector := new(utils.MockCniDetector)
	cniDetector.EXPECT().DetectCNIType(mock.Anything).Return(utils.CiliumNativeRouting, nil).Maybe()
	endpointResolver := utils.NewDefaultEndpointResolver(ctx, mgr.GetClient())
	reconcileCounters := metricsutil.NewReconcileCounters()
	lbcMetricsCollector := lbcmetrics.NewCollector(metrics.Registry, mgr, reconcileCounters, ctrl.Log.WithName("controller_metrics"))

	// LBC reconciler — Gateway hands off LB provisioning to this.
	lbcUC := lbc_uc.NewLoadBalancerConfigUseCase(mockConfig, k8sRepo, vngcloudRepo)
	lbcRec := lbc_controller.NewLoadBalancerConfigReconciler(
		mgr.GetClient(), mgr.GetScheme(), lbcUC,
		mgr.GetEventRecorderFor("lbc-controller"),
		finalizerManager, lbc.NewLoadBalancerConfigUtils(domain.LbcFinalizer),
		lbcMetricsCollector, reconcileCounters, domain.DefaultMaxConcurrentReconciles,
	)
	Expect(lbcRec.SetupWithManager(ctx, mgr)).To(Succeed())

	// NSG reconciler — Gateway delete cascade waits on NSG cleanup.
	nsgUC := nsg_uc.NewNodeSecurityGroupUseCase(mockConfig, k8sRepo, vngcloudRepo)
	nsgRec := nsg_controller.NewNodeSecurityGroupReconciler(
		mgr.GetClient(), mgr.GetScheme(), nsgUC,
		mgr.GetEventRecorderFor("nsg-controller"),
		finalizerManager, nsg.NewNodeSecurityGroupUtils(domain.NsgFinalizer),
		lbcMetricsCollector, reconcileCounters, 1,
	)
	Expect(nsgRec.SetupWithManager(ctx, mgr)).To(Succeed())

	// ALB Gateway reconcilers (under test).
	albUC := alb_gateway_uc.NewALBGatewayUseCase(
		mockClusterID, k8sRepo, vngcloudRepo, annotationParser, cniDetector, endpointResolver,
	)
	Expect(NewGatewayClassReconciler(mgr.GetClient(), mgr.GetScheme()).SetupWithManager(mgr)).To(Succeed())
	Expect(NewGatewayReconciler(albUC, mgr.GetClient(), mgr.GetScheme(), finalizerManager,
		mgr.GetEventRecorderFor("gateway-alb"), reconcileCounters, 1).SetupWithManager(mgr)).To(Succeed())
	Expect(NewHTTPRouteReconciler(albUC, mgr.GetClient(), mgr.GetScheme(), reconcileCounters).SetupWithManager(mgr)).To(Succeed())

	// Validation-only reconcilers for the two extension CRDs.
	Expect(targetgroupconfig.New(mgr.GetClient(), mgr.GetScheme()).SetupWithManager(mgr)).To(Succeed())
	Expect(listenerruleconfig.New(mgr.GetClient(), mgr.GetScheme()).SetupWithManager(mgr)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).To(Succeed())
	}()

	By("seeding mock cluster nodes (NodePort target-type needs at least one node)")
	for _, n := range []*corev1.Node{
		vngcloud_mocks.MockNode1, vngcloud_mocks.MockNode2,
	} {
		Expect(k8sClient.Create(ctx, n)).To(Succeed())
	}

	By("creating the vngcloud-alb GatewayClass once for the suite")
	gwc := &gwv1.GatewayClass{}
	gwc.Name = "vngcloud-alb"
	gwc.Spec.ControllerName = gwv1.GatewayController(domain.ControllerNameALB)
	Expect(k8sClient.Create(ctx, gwc)).To(Succeed())
})

var _ = AfterSuite(func() {
	By("tearing down test environment")
	cancel()
	if testEnv != nil {
		Expect(testEnv.Stop()).To(Succeed())
	}
})

// envTestBinaryDir locates the first binary directory in bin/k8s/ so this
// suite runs from an IDE without a manual `make setup-envtest`. Returns ""
// when the directory is absent — envtest then falls back to KUBEBUILDER_ASSETS.
func envTestBinaryDir() string {
	base := filepath.Join("..", "..", "..", "..", "bin", "k8s")
	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			return filepath.Join(base, e.Name())
		}
	}
	return ""
}

// _ = runtime ensures the import isn't dropped on platforms where envtest binary
// directory selection happens to be elsewhere; some IDE-launched runs trigger it.
var _ = runtime.GOOS
