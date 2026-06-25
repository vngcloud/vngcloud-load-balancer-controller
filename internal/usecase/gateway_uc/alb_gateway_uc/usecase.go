// Package alb_gateway_uc translates a Gateway-API Gateway under the
// vngcloud-alb GatewayClass into a LoadBalancerConfig CR that the existing
// LBC controller reconciles against the cloud. The Gateway use case is the
// only writer to the LBC's Spec; the LBC controller remains the only writer
// to the LBC's Status (and to the cloud LB itself).
package alb_gateway_uc

import (
	"context"
	"errors"
	"fmt"

	"github.com/anngdinh/operator-helper/contexts"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/common"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/consts"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/k8s"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

type albGatewayUseCase struct {
	k8sRepo          repository.K8sRepository
	vngcloudRepo     repository.VngCloudRepository
	endpointResolver utils.EndpointResolver

	// k8sClient is a controller-runtime client used for Gateway-API resources
	// (Gateway, HTTPRoute, VKS*Policy) that don't have typed accessors on
	// repository.K8sRepository. Passed in alongside k8sRepo at construction.
	k8sClient client.Client

	// finalizerManager owns the add/remove-finalizer flow (re-Get + optimistic
	// Patch + retry-on-conflict), shared with the non-Gateway controllers.
	finalizerManager k8s.FinalizerManager

	clusterId         string
	defaultNetworkId  string
	defaultSubnetId   string
	defaultSubnetCIDR string
	defaultZone       common.Zone
}

// NewALBGatewayUseCase wires the dependencies the Gateway translator needs.
func NewALBGatewayUseCase(
	clusterId string,
	k8sRepo repository.K8sRepository,
	vngcloudRepo repository.VngCloudRepository,
	endpointResolver utils.EndpointResolver,
	k8sClient client.Client,
	finalizerManager k8s.FinalizerManager,
) usecase.ALBGatewayUseCase {
	return &albGatewayUseCase{
		clusterId:        clusterId,
		k8sRepo:          k8sRepo,
		vngcloudRepo:     vngcloudRepo,
		endpointResolver: endpointResolver,
		k8sClient:        k8sClient,
		finalizerManager: finalizerManager,
	}
}

// InitALBGatewayUseCase prepares cluster-scoped state — same shape as the
// other use cases. We discover default network info from a node so that
// VKSGatewayPolicy.LoadBalancerSpec doesn't need to spell it out, and fall
// back to the node-label clusterID when one wasn't supplied at construction
// (matches ingress_uc.InitIngressUseCase / service_uc.InitServiceUseCase).
//
// Returns an error when the data isn't fully populated yet so the gateway
// controller's init retry loop will keep firing until the controller-runtime
// cache is ready and the cloud probe succeeds. This avoids a one-shot init
// that "succeeds" against a not-yet-ready cache and then never retries.
func (uc *albGatewayUseCase) InitALBGatewayUseCase(ctx context.Context) error {
	logger := contexts.NewContext(ctx).Log()

	if uc.defaultNetworkId != "" && uc.defaultSubnetId != "" && uc.defaultSubnetCIDR != "" && uc.defaultZone != "" && uc.clusterId != "" {
		return nil
	}

	nodes, err := uc.listNodesForNetworkProbe(ctx)
	if err != nil {
		// Cache-not-ready is expected during the first few seconds of
		// startup. Returning an error tells the caller to retry.
		return fmt.Errorf("ALB Gateway init: list nodes: %w", err)
	}
	if len(nodes) == 0 {
		return errors.New("ALB Gateway init: cluster has no nodes yet")
	}

	if uc.defaultNetworkId == "" || uc.defaultSubnetId == "" || uc.defaultSubnetCIDR == "" || uc.defaultZone == "" {
		providerID := utils.GetProviderIdFromNode(nodes[0])
		if providerID == "" {
			return errors.New("ALB Gateway init: first node has no providerID")
		}
		zone, networkID, subnetID, cidr, err := uc.vngcloudRepo.GetServerNetworkInfo(ctx, providerID)
		if err != nil {
			return fmt.Errorf("ALB Gateway init: probe vngcloud network info: %w", err)
		}
		uc.defaultZone, uc.defaultNetworkId, uc.defaultSubnetId, uc.defaultSubnetCIDR = zone, networkID, subnetID, cidr
		logger.Infof("ALB Gateway init: defaults zone=%s network=%s subnet=%s cidr=%s", zone, networkID, subnetID, cidr)
	}

	// Same fallback ingress_uc / service_uc use: if --cluster-id wasn't
	// supplied via config, read it off the first node carrying the
	// vks.vngcloud.vn/cluster-id label.
	if uc.clusterId == "" {
		clusterID := ""
		for _, n := range nodes {
			if n != nil && n.Labels != nil && n.Labels["vks.vngcloud.vn/cluster-id"] != "" {
				clusterID = n.Labels["vks.vngcloud.vn/cluster-id"]
				break
			}
		}
		if clusterID == "" {
			return errors.New("ALB Gateway init: no clusterID found; specify --cluster-id or label a node with vks.vngcloud.vn/cluster-id")
		}
		uc.clusterId = clusterID
		logger.Infof("ALB Gateway init: clusterID empty in config, picked up from node label: %s", uc.clusterId)
	}
	return nil
}

// EnsureALBGatewayUseCase is the entry called by the Gateway reconciler.
func (uc *albGatewayUseCase) EnsureALBGatewayUseCase(ctx context.Context, req ctrl.Request) error {
	logger := contexts.NewContext(ctx).Log()

	gw := &gwv1.Gateway{}
	if err := uc.k8sClient.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: req.Name}, gw); err != nil {
		if apierrors.IsNotFound(err) {
			// The reconciler queued this Gateway and it disappeared in the meantime.
			// Nothing to do — finalizer-driven cleanup runs on the previous tombstone.
			return nil
		}
		return fmt.Errorf("get Gateway %s/%s: %w", req.Namespace, req.Name, err)
	}

	// Phase 1 only handles the ALB GatewayClass.
	gc, err := uc.fetchGatewayClass(ctx, gw)
	if err != nil {
		return err
	}
	if gc == nil || string(gc.Spec.ControllerName) != consts.GatewayClassControllerNameALB {
		// Not ours — leave it alone. Other controllers (or a future NLB
		// reconciler) own different GatewayClasses.
		return nil
	}

	// Deletion path: finalizer protects orderly cleanup.
	if !gw.DeletionTimestamp.IsZero() {
		return uc.handleDeletion(ctx, gw)
	}
	if err := uc.ensureFinalizer(ctx, gw); err != nil {
		return err
	}

	task := &defaultGatewayBuildTask{
		uc:         uc,
		logger:     logger.WithField("gateway", req.Namespace+"/"+req.Name),
		gw:         gw,
		nameHelper: utils.NewNameHelper(uc.clusterId, "gateway", gw.Namespace, gw.Name),
	}
	return task.run(ctx)
}

// DeleteALBGatewayUseCase is the explicit-delete path; the deletion-finalizer
// path inside Ensure also handles cleanup.
func (uc *albGatewayUseCase) DeleteALBGatewayUseCase(ctx context.Context, req ctrl.Request) error {
	gw := &gwv1.Gateway{}
	if err := uc.k8sClient.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: req.Name}, gw); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		if meta.IsNoMatchError(err) {
			return nil
		}
		return fmt.Errorf("get Gateway %s/%s: %w", req.Namespace, req.Name, err)
	}
	return uc.handleDeletion(ctx, gw)
}

func (uc *albGatewayUseCase) fetchGatewayClass(ctx context.Context, gw *gwv1.Gateway) (*gwv1.GatewayClass, error) {
	if gw.Spec.GatewayClassName == "" {
		return nil, errors.New("gateway has empty gatewayClassName")
	}
	gc := &gwv1.GatewayClass{}
	if err := uc.k8sClient.Get(ctx, types.NamespacedName{Name: string(gw.Spec.GatewayClassName)}, gc); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("get GatewayClass %s: %w", gw.Spec.GatewayClassName, err)
	}
	return gc, nil
}
