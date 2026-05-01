/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

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
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	gatewayv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/k8s_repo"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/vngcloud_repo/vngcloud_mocks"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/alb_gateway_uc"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/annotations"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/k8s"
	metricsutil "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/metrics/util"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

const mockClusterID = "k8s-00000000-0000-0000-0000-000000000000"

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
)

func TestALBGatewayControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "ALB Gateway Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	logrus.SetLevel(logrus.DebugLevel)

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

	By("starting manager with ALB Gateway reconcilers")
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme.Scheme})
	Expect(err).ToNot(HaveOccurred())

	finalizerManager := k8s.NewDefaultFinalizerManager(mgr.GetClient(), ctrl.Log)
	k8sRepo := k8s_repo.NewK8sRepository(mgr.GetClient())
	vngcloudRepo := vngcloud_mocks.NewMockProvider()
	Expect(vngcloudRepo.Init(nil)).To(Succeed())

	annotationParser := annotations.NewSuffixAnnotationParser("gateway.vks.vngcloud.vn")
	cniDetector := new(utils.MockCniDetector)
	cniDetector.EXPECT().DetectCNIType(mock.Anything).Return(utils.CiliumNativeRouting, nil).Maybe()
	endpointResolver := utils.NewDefaultEndpointResolver(ctx, mgr.GetClient())
	reconcileCounters := metricsutil.NewReconcileCounters()

	albUC := alb_gateway_uc.NewALBGatewayUseCase(
		mockClusterID, k8sRepo, vngcloudRepo, annotationParser, cniDetector, endpointResolver,
	)

	Expect(NewGatewayClassReconciler(mgr.GetClient(), mgr.GetScheme()).SetupWithManager(mgr)).To(Succeed())
	Expect(NewGatewayReconciler(albUC, mgr.GetClient(), mgr.GetScheme(), finalizerManager,
		mgr.GetEventRecorderFor("gateway-alb"), reconcileCounters, 1).SetupWithManager(mgr)).To(Succeed())
	Expect(NewHTTPRouteReconciler(albUC, mgr.GetClient(), mgr.GetScheme(), reconcileCounters).SetupWithManager(mgr)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).To(Succeed())
	}()
})

var _ = AfterSuite(func() {
	By("tearing down test environment")
	cancel()
	if testEnv != nil {
		Expect(testEnv.Stop()).To(Succeed())
	}
})

// envTestBinaryDir mirrors the helper used by other suites so this suite runs
// from an IDE without `make setup-envtest`. Returns "" when the binaries
// directory is absent — envtest then falls back to KUBEBUILDER_ASSETS.
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

func init() { _ = runtime.GOOS }
