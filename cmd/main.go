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

package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	vksvngcloudvnv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	corecontroller "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/core"
	gatewayalbcontroller "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/alb"
	gatewaynlbcontroller "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/nlb"
	gatewaypolicies "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/policies"
	gatewayshared "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/glbc_controller"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/lbc_controller"
	networkingcontroller "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/networking"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/nsg_controller"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/service_glb_controller"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/vglb_controller"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/k8s_repo"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository/vngcloud_repo"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/alb_gateway_uc"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/nlb_gateway_uc"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/glbc_uc"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/ingress_uc"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/lbc_uc"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/nsg_uc"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/service_glb_uc"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/service_uc"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/vglb_uc"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/annotations"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/clusterapi"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/config"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/glbc"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/ingress"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/k8s"
	vksvngcloudvn "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/k8s/apis/vks.vngcloud.vn"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/lbc"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/logging"
	lbcmetrics "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/metrics/lbc"
	metricsutil "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/metrics/util"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/nsg"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/service"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/service_glb"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/version"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/vglb"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
	conf     = config.NewConfig()
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(vksvngcloudvnv1alpha1.AddToScheme(scheme))
	utilruntime.Must(gwv1alpha1.AddToScheme(scheme))
	utilruntime.Must(gwv1.Install(scheme))
	utilruntime.Must(gwv1beta1.Install(scheme))
	// v1alpha2 carries the experimental L4 routes (TCPRoute/UDPRoute). Teaching
	// the scheme to decode them is harmless when the CRDs aren't installed; the
	// NLB controller's route watches (which require the CRDs) are gated behind
	// --disable-nlb-gateway-controller (default disabled).
	utilruntime.Must(gwv1a2.Install(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() { //nolint:gocyclo
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var logLevel string
	var disableServiceController bool
	var disableIngressController bool
	var disableLoadBalancerConfigController bool
	var disableGlobalLoadBalancerConfigController bool
	var disableNodeSecurityGroupController bool
	var disableVngcloudGlobalLoadBalancerController bool
	var disableServiceGLBController bool
	var disableALBGatewayController bool
	var disableNLBGatewayController bool
	var syncPeriod time.Duration
	var tlsOpts []func(*tls.Config)
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.StringVar(&logLevel, "log-level", "info",
		"Set the log level (debug, info, warn, error, fatal, panic)")
	flag.BoolVar(&disableServiceController, "disable-service-controller", false,
		"If set, the service controller will be disabled")
	flag.BoolVar(&disableIngressController, "disable-ingress-controller", false,
		"If set, the ingress controller will be disabled")
	flag.BoolVar(&disableLoadBalancerConfigController, "disable-load-balancer-config-controller", false,
		"If set, the LoadBalancerConfig controller will be disabled")
	flag.BoolVar(&disableGlobalLoadBalancerConfigController, "disable-global-load-balancer-config-controller", false,
		"If set, the GlobalLoadBalancerConfig controller will be disabled")
	flag.BoolVar(&disableNodeSecurityGroupController, "disable-node-security-group-controller", false,
		"If set, the NodeSecurityGroup controller will be disabled")
	flag.BoolVar(&disableVngcloudGlobalLoadBalancerController, "disable-vngcloud-global-load-balancer-controller", false,
		"If set, the VngcloudGlobalLoadBalancer controller will be disabled")
	flag.BoolVar(&disableServiceGLBController, "disable-service-glb-controller", false,
		"If set, the ServiceGLB controller will be disabled")
	flag.BoolVar(&disableALBGatewayController, "disable-alb-gateway-controller", true,
		"If set, the Gateway-API ALB controller (vngcloud-alb GatewayClass) is disabled. "+
			"DISABLED by default: the Gateway-API CRDs (GatewayClass/Gateway/HTTPRoute) must be "+
			"installed in the cluster before enabling. In Helm set gatewayApi.alb.enabled=true to enable.")
	flag.BoolVar(&disableNLBGatewayController, "disable-nlb-gateway-controller", true,
		"If set, the Gateway-API NLB controller (vngcloud-nlb GatewayClass, L4) is disabled. "+
			"DISABLED by default: TCPRoute/UDPRoute are Gateway-API experimental-channel CRDs and "+
			"must be installed before enabling. In Helm set gatewayApi.nlb.enabled=true to enable.")
	flag.DurationVar(&syncPeriod, "sync-period", 5*time.Minute,
		"The minimum frequency at which watched resources are reconciled. "+
			"A lower period will correct entropy more quickly, "+
			"but reduce responsiveness to change if there are many watched resources. "+
			"Examples: 5m, 1h, 30s. Defaults to 5 minutes.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// Setup logrus logger
	if err := logging.SetupLogger(logLevel); err != nil {
		setupLog.Error(err, "invalid log level, defaulting to info", "logLevel", logLevel)
	}

	if err := conf.Init(setupLog, "/etc/vngcloud-load-balancer-controller/config.yaml"); err != nil {
		os.Exit(1)
	}

	kubeRestConfig := ctrl.GetConfigOrDie()
	if conf.Cluster.IsRunRemote {
		if conf.Cluster.ClusterID == "" || conf.Cluster.Namespace == "" {
			setupLog.Error(fmt.Errorf("clusterID or namespace is empty"), "clusterID or namespace is empty")
			os.Exit(1)
		}

		// Create a client for the management cluster (current cluster)
		mgmtClient, err := client.New(kubeRestConfig, client.Options{Scheme: scheme})
		if err != nil {
			setupLog.Error(err, "unable to create management cluster client")
			os.Exit(1)
		}

		// Create cluster API client and get the target cluster's rest config
		clusterAPIClient := clusterapi.NewClusterClient(mgmtClient)
		kubeRestConfig, err = clusterAPIClient.GetRestConfig(
			context.Background(), conf.Cluster.Namespace, conf.Cluster.ClusterID)
		if err != nil {
			setupLog.Error(err, "unable to get target cluster rest config")
			os.Exit(1)
		}
	}

	setupLog.Info(fmt.Sprintf(
		"The commit is [%s], version is [%s], chartVersion is [%s]",
		version.Commit, version.Version, conf.ChartVersion))
	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		// TODO(user): TLSOpts is used to allow configuring the TLS config used for the server. If certificates are
		// not provided, self-signed certificates will be generated by default. This option is not recommended for
		// production environments as self-signed certificates do not offer the same level of trust and security
		// as certificates issued by a trusted Certificate Authority (CA). The primary risk is potentially allowing
		// unauthorized access to sensitive metrics data. Consider replacing with CertDir, CertName, and KeyName
		// to provide certificates, ensuring the server communicates using trusted and secure certificates.
		TLSOpts: tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	mgr, err := ctrl.NewManager(kubeRestConfig, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID: fmt.Sprintf(
			"n-%s-n.n-%s-n.lbc.vks.vngcloud.vn",
			conf.Cluster.ClusterID, conf.Cluster.Namespace),
		Cache: cache.Options{
			SyncPeriod: &syncPeriod,
		},
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Apply the CRD
	crdClient, err := apiextensionsclient.NewForConfig(kubeRestConfig)
	if err != nil {
		setupLog.Error(err, "unable to create CRD client")
		os.Exit(1)
	}

	// Install all CRDs at startup
	if err := vksvngcloudvn.InstallAllCRDs(crdClient, conf.ChartVersion, setupLog); err != nil {
		setupLog.Error(err, "failed to install crds")
		os.Exit(1)
	}

	ctx := context.Background()
	finalizerManager := k8s.NewDefaultFinalizerManager(mgr.GetClient(), ctrl.Log)
	k8sRepo := k8s_repo.NewK8sRepository(mgr.GetClient())
	vngcloudRepo, err := vngcloud_repo.NewVngCloudRepository(ctx, conf)
	if err != nil {
		setupLog.Error(err, "unable to create VngCloud repository")
		os.Exit(1)
	}

	reconcileCounters := metricsutil.NewReconcileCounters()
	lbcMetricsCollector := lbcmetrics.NewCollector(
		metrics.Registry, mgr, reconcileCounters, ctrl.Log.WithName("controller_metrics"))
	endpointResolver := utils.NewDefaultEndpointResolver(ctx, mgr.GetClient())

	if !disableServiceController {
		annotationParser := annotations.NewSuffixAnnotationParser(
			domain.SERVICE_ANNOTATION_PREFIX)
		cniDetector := utils.NewDetector(mgr.GetClient())
		serviceUtils := service.NewServiceUtils(domain.ServiceFinalizer, annotationParser)
		serviceUseCase := service_uc.NewServiceUseCase(
			conf.Cluster.ClusterID, k8sRepo, vngcloudRepo, annotationParser, serviceUtils, cniDetector, endpointResolver)
		reconciler := corecontroller.NewServiceReconciler(
			serviceUseCase,
			mgr.GetClient(),
			mgr.GetScheme(),
			finalizerManager,
			mgr.GetEventRecorderFor("service-controller"),
			serviceUtils,
			lbcMetricsCollector,
			reconcileCounters,
			conf.MaxConcurrentReconciles,
		)
		if err = reconciler.SetupWithManager(ctx, mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "Service")
			os.Exit(1)
		}
	}

	if !disableIngressController {
		clientSet, err := kubernetes.NewForConfig(mgr.GetConfig())
		if err != nil {
			setupLog.Error(err, "unable to obtain clientSet")
			os.Exit(1)
		}

		annotationParser := annotations.NewSuffixAnnotationParser(domain.INGRESS_ANNOTATION_PREFIX)
		cniDetector := utils.NewDetector(mgr.GetClient())
		ingressUtils := ingress.NewIngressUtils(domain.IngressFinalizer)
		ingressUseCase := ingress_uc.NewIngressUseCase(
			conf.Cluster.ClusterID, k8sRepo, vngcloudRepo, annotationParser, ingressUtils, cniDetector, endpointResolver)
		reconciler := networkingcontroller.NewIngressReconciler(
			ingressUseCase,
			mgr.GetClient(),
			mgr.GetScheme(),
			finalizerManager,
			mgr.GetEventRecorderFor("ingress-controller"),
			ingressUtils,
			lbcMetricsCollector,
			reconcileCounters,
			conf.MaxConcurrentReconciles,
		)
		if err = reconciler.SetupWithManager(ctx, mgr, clientSet); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "Ingress")
			os.Exit(1)
		}
	}

	if !disableLoadBalancerConfigController {
		lbcUtils := lbc.NewLoadBalancerConfigUtils(domain.LbcFinalizer)
		lbcUseCase := lbc_uc.NewLoadBalancerConfigUseCase(
			conf, k8sRepo, vngcloudRepo,
		)
		reconciler := lbc_controller.NewLoadBalancerConfigReconciler(
			mgr.GetClient(),
			mgr.GetScheme(),
			lbcUseCase,
			mgr.GetEventRecorderFor("load-balancer-config-controller"),
			finalizerManager,
			lbcUtils,
			lbcMetricsCollector,
			reconcileCounters,
			conf.MaxConcurrentReconciles,
		)
		if err = reconciler.SetupWithManager(ctx, mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "LoadBalancerConfig")
			os.Exit(1)
		}
	}

	if !disableGlobalLoadBalancerConfigController {
		glbcUtils := glbc.NewGlobalLoadBalancerConfigUtils(domain.GlbcFinalizer)
		glbcUseCase := glbc_uc.NewGlobalLoadBalancerConfigUseCase(
			conf, k8sRepo, vngcloudRepo,
		)
		reconciler := glbc_controller.NewGlobalLoadBalancerConfigReconciler(
			mgr.GetClient(),
			mgr.GetScheme(),
			glbcUseCase,
			mgr.GetEventRecorderFor("global-load-balancer-config-controller"),
			finalizerManager,
			glbcUtils,
			lbcMetricsCollector,
			reconcileCounters,
		)
		if err = reconciler.SetupWithManager(ctx, mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "GlobalLoadBalancerConfig")
			os.Exit(1)
		}
	}

	if !disableNodeSecurityGroupController {
		nsgUtils := nsg.NewNodeSecurityGroupUtils(domain.NsgFinalizer)
		nsgUseCase := nsg_uc.NewNodeSecurityGroupUseCase(
			conf, k8sRepo, vngcloudRepo,
		)
		reconciler := nsg_controller.NewNodeSecurityGroupReconciler(
			mgr.GetClient(),
			mgr.GetScheme(),
			nsgUseCase,
			mgr.GetEventRecorderFor("node-security-group-controller"),
			finalizerManager,
			nsgUtils,
			lbcMetricsCollector,
			reconcileCounters,
			1, // only 1 because it config same nodes' security groups
		)
		if err := reconciler.SetupWithManager(ctx, mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "NodeSecurityGroup")
			os.Exit(1)
		}
	}

	if !disableVngcloudGlobalLoadBalancerController {
		annotationParser := annotations.NewSuffixAnnotationParser(domain.VGLB_ANNOTATION_PREFIX)
		vglbUtils := vglb.NewVngcloudGlobalLoadBalancerUtils(domain.VglbFinalizer)
		vglbUseCase := vglb_uc.NewVngcloudGlobalLoadBalancerUseCase(
			conf, k8sRepo, vngcloudRepo, annotationParser, endpointResolver,
		)
		reconciler := vglb_controller.NewVngcloudGlobalLoadBalancerReconciler(
			mgr.GetClient(),
			mgr.GetScheme(),
			vglbUseCase,
			mgr.GetEventRecorderFor("vngcloud-global-load-balancer-controller"),
			finalizerManager,
			vglbUtils,
			lbcMetricsCollector,
			reconcileCounters,
			conf.MaxConcurrentReconciles,
		)
		if err := reconciler.SetupWithManager(ctx, mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "VngcloudGlobalLoadBalancer")
			os.Exit(1)
		}
	}

	if !disableServiceGLBController {
		glbAnnotationParser := annotations.NewSuffixAnnotationParser(domain.GLB_ANNOTATION_PREFIX)
		serviceGLBUtils := service_glb.NewServiceGLBUtils(domain.ServiceGLBFinalizer, glbAnnotationParser)
		serviceGLBUseCase := service_glb_uc.NewServiceGLBUseCase(
			conf, k8sRepo, vngcloudRepo, glbAnnotationParser, endpointResolver,
		)
		serviceGLBReconciler := service_glb_controller.NewServiceGLBReconciler(
			mgr.GetClient(),
			mgr.GetScheme(),
			serviceGLBUseCase,
			mgr.GetEventRecorderFor("service-glb-controller"),
			finalizerManager,
			serviceGLBUtils,
			lbcMetricsCollector,
			reconcileCounters,
			conf.MaxConcurrentReconciles,
		)
		if err := serviceGLBReconciler.SetupWithManager(ctx, mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "ServiceGLB")
			os.Exit(1)
		}
	}

	if !disableALBGatewayController {
		albGatewayUseCase := alb_gateway_uc.NewALBGatewayUseCase(
			conf.Cluster.ClusterID,
			k8sRepo,
			vngcloudRepo,
			endpointResolver,
			mgr.GetClient(),
			finalizerManager,
		)
		albGatewayReconciler := gatewayalbcontroller.NewGatewayReconciler(
			mgr.GetClient(),
			mgr.GetScheme(),
			albGatewayUseCase,
			conf.MaxConcurrentReconciles,
		)
		if err := albGatewayReconciler.SetupWithManager(ctx, mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "ALBGateway")
			os.Exit(1)
		}

		gcReconciler := gatewayalbcontroller.NewGatewayClassReconciler(mgr.GetClient(), mgr.GetScheme())
		if err := gcReconciler.SetupWithManager(ctx, mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "ALBGatewayClass")
			os.Exit(1)
		}

		// Policy validators write GEP-713 status (Accepted/Conflicted/
		// TargetNotFound) on the four VKS policy CRDs. Status-only; never touch
		// the LoadBalancer.
		for _, pr := range gatewaypolicies.AllReconcilers(mgr.GetClient()) {
			if err := pr.SetupWithManager(ctx, mgr); err != nil {
				setupLog.Error(err, "unable to create controller", "controller", "GatewayPolicyValidator")
				os.Exit(1)
			}
		}

		// Register the field indexes used by the gateway reconciler's watches
		// (HTTPRoute → parent Gateway, etc.).
		if err := gatewayshared.RegisterIndexes(ctx, mgr); err != nil {
			setupLog.Error(err, "unable to register gateway field indexes")
			os.Exit(1)
		}
	}

	if !disableNLBGatewayController {
		nlbGatewayUseCase := nlb_gateway_uc.NewNLBGatewayUseCase(
			conf.Cluster.ClusterID,
			k8sRepo,
			vngcloudRepo,
			endpointResolver,
			mgr.GetClient(),
			finalizerManager,
		)
		nlbGatewayReconciler := gatewaynlbcontroller.NewGatewayReconciler(
			mgr.GetClient(),
			mgr.GetScheme(),
			nlbGatewayUseCase,
			conf.MaxConcurrentReconciles,
		)
		// SetupWithManager registers the L4 (TCPRoute/UDPRoute) field indexes
		// internally — those CRDs are an experimental-channel prerequisite.
		if err := nlbGatewayReconciler.SetupWithManager(ctx, mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "NLBGateway")
			os.Exit(1)
		}

		nlbGCReconciler := gatewaynlbcontroller.NewGatewayClassReconciler(mgr.GetClient(), mgr.GetScheme())
		if err := nlbGCReconciler.SetupWithManager(ctx, mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "NLBGatewayClass")
			os.Exit(1)
		}
	}

	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	go func() {
		setupLog.Info("starting collect cache size")
		lbcMetricsCollector.StartCollectCacheSize(ctx)
	}()

	go func() {
		setupLog.Info("starting collect top talkers")
		lbcMetricsCollector.StartCollectTopTalkers(ctx)
	}()

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
