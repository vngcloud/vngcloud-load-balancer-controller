// Package nlb_gateway_uc translates a Gateway-API Gateway under the
// vngcloud-nlb GatewayClass into a Layer-4 LoadBalancerConfig CR that the
// existing LBC controller reconciles against the cloud NLB. It is the L4
// (Phase 2) sibling of alb_gateway_uc: TCP/UDP listeners with one default pool
// each, members resolved from attached TCPRoute/UDPRoute backendRefs. There are
// no L7 policies, certificates, or path matching on this path.
//
// Like the ALB path, the Gateway use case is the only writer to the LBC's Spec;
// the LBC controller remains the only writer to the LBC's Status (and to the
// cloud LB itself).
package nlb_gateway_uc

import (
	"context"
	"errors"
	"fmt"

	"github.com/anngdinh/operator-helper/contexts"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/common"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/consts"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/k8s"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

type nlbGatewayUseCase struct {
	k8sRepo          repository.K8sRepository
	vngcloudRepo     repository.VngCloudRepository
	endpointResolver utils.EndpointResolver
	k8sClient        client.Client

	// finalizerManager owns the add/remove-finalizer flow (re-Get + optimistic
	// Patch + retry-on-conflict), shared with the non-Gateway controllers.
	finalizerManager k8s.FinalizerManager

	clusterId         string
	defaultNetworkId  string
	defaultSubnetId   string
	defaultSubnetCIDR string
	defaultZone       common.Zone
}

// NewNLBGatewayUseCase wires the dependencies the NLB Gateway translator needs.
func NewNLBGatewayUseCase(
	clusterId string,
	k8sRepo repository.K8sRepository,
	vngcloudRepo repository.VngCloudRepository,
	endpointResolver utils.EndpointResolver,
	k8sClient client.Client,
	finalizerManager k8s.FinalizerManager,
) usecase.NLBGatewayUseCase {
	return &nlbGatewayUseCase{
		clusterId:        clusterId,
		k8sRepo:          k8sRepo,
		vngcloudRepo:     vngcloudRepo,
		endpointResolver: endpointResolver,
		k8sClient:        k8sClient,
		finalizerManager: finalizerManager,
	}
}

// InitNLBGatewayUseCase discovers cluster-default network info and clusterID —
// same shape as alb_gateway_uc.InitALBGatewayUseCase. Returns an error until
// the data is fully populated so the controller's retry loop keeps firing.
func (uc *nlbGatewayUseCase) InitNLBGatewayUseCase(ctx context.Context) error {
	logger := contexts.NewContext(ctx).Log()

	if uc.defaultNetworkId != "" && uc.defaultSubnetId != "" && uc.defaultSubnetCIDR != "" && uc.defaultZone != "" && uc.clusterId != "" {
		return nil
	}

	nodes, err := uc.listNodesForNetworkProbe(ctx)
	if err != nil {
		return fmt.Errorf("NLB Gateway init: list nodes: %w", err)
	}
	if len(nodes) == 0 {
		return errors.New("NLB Gateway init: cluster has no nodes yet")
	}

	if uc.defaultNetworkId == "" || uc.defaultSubnetId == "" || uc.defaultSubnetCIDR == "" || uc.defaultZone == "" {
		providerID := utils.GetProviderIdFromNode(nodes[0])
		if providerID == "" {
			return errors.New("NLB Gateway init: first node has no providerID")
		}
		zone, networkID, subnetID, cidr, err := uc.vngcloudRepo.GetServerNetworkInfo(ctx, providerID)
		if err != nil {
			return fmt.Errorf("NLB Gateway init: probe vngcloud network info: %w", err)
		}
		uc.defaultZone, uc.defaultNetworkId, uc.defaultSubnetId, uc.defaultSubnetCIDR = zone, networkID, subnetID, cidr
		logger.Infof("NLB Gateway init: defaults zone=%s network=%s subnet=%s cidr=%s", zone, networkID, subnetID, cidr)
	}

	if uc.clusterId == "" {
		clusterID := ""
		for _, n := range nodes {
			if n != nil && n.Labels != nil && n.Labels["vks.vngcloud.vn/cluster-id"] != "" {
				clusterID = n.Labels["vks.vngcloud.vn/cluster-id"]
				break
			}
		}
		if clusterID == "" {
			return errors.New("NLB Gateway init: no clusterID found; specify --cluster-id or label a node with vks.vngcloud.vn/cluster-id")
		}
		uc.clusterId = clusterID
		logger.Infof("NLB Gateway init: clusterID empty in config, picked up from node label: %s", uc.clusterId)
	}
	return nil
}

// EnsureNLBGatewayUseCase is the entry called by the NLB Gateway reconciler.
func (uc *nlbGatewayUseCase) EnsureNLBGatewayUseCase(ctx context.Context, req ctrl.Request) error {
	logger := contexts.NewContext(ctx).Log()

	gw := &gwv1.Gateway{}
	if err := uc.k8sClient.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: req.Name}, gw); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get Gateway %s/%s: %w", req.Namespace, req.Name, err)
	}

	gc, err := uc.fetchGatewayClass(ctx, gw)
	if err != nil {
		return err
	}
	if gc == nil || string(gc.Spec.ControllerName) != consts.GatewayClassControllerNameNLB {
		// Not ours — the ALB controller (or another) owns a different class.
		return nil
	}

	if !gw.DeletionTimestamp.IsZero() {
		return uc.handleDeletion(ctx, gw)
	}
	if err := uc.ensureFinalizer(ctx, gw); err != nil {
		return err
	}

	task := &nlbBuildTask{
		uc:         uc,
		logger:     logger.WithField("gateway", req.Namespace+"/"+req.Name),
		gw:         gw,
		nameHelper: utils.NewNameHelper(uc.clusterId, "gateway", gw.Namespace, gw.Name),
	}
	return task.run(ctx)
}

// DeleteNLBGatewayUseCase is the explicit-delete path; the deletion-finalizer
// path inside Ensure also handles cleanup.
func (uc *nlbGatewayUseCase) DeleteNLBGatewayUseCase(ctx context.Context, req ctrl.Request) error {
	gw := &gwv1.Gateway{}
	if err := uc.k8sClient.Get(ctx, types.NamespacedName{Namespace: req.Namespace, Name: req.Name}, gw); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			return nil
		}
		return fmt.Errorf("get Gateway %s/%s: %w", req.Namespace, req.Name, err)
	}
	return uc.handleDeletion(ctx, gw)
}

func (uc *nlbGatewayUseCase) fetchGatewayClass(ctx context.Context, gw *gwv1.Gateway) (*gwv1.GatewayClass, error) {
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

func (uc *nlbGatewayUseCase) listNodesForNetworkProbe(ctx context.Context) ([]*corev1.Node, error) {
	nodes := &corev1.NodeList{}
	if err := uc.k8sRepo.ListNode(ctx, nodes); err != nil {
		return nil, err
	}
	out := make([]*corev1.Node, 0, len(nodes.Items))
	for i := range nodes.Items {
		out = append(out, &nodes.Items[i])
	}
	return out, nil
}

func (uc *nlbGatewayUseCase) ensureFinalizer(ctx context.Context, gw *gwv1.Gateway) error {
	if err := uc.finalizerManager.AddFinalizers(ctx, gw, domain.GatewayFinalizer); err != nil {
		return fmt.Errorf("add finalizer on Gateway %s/%s: %w", gw.Namespace, gw.Name, err)
	}
	return nil
}

// handleDeletion removes the LBC owned by this Gateway and then drops the
// finalizer. The LBC controller's own finalizer guarantees the cloud LB is
// gone before the LBC object is.
//
// RemoveFinalizers re-Gets the Gateway before patching and treats an
// already-deleted object as success, so a racing delete (the stale-UID
// StorageError we used to log) no longer surfaces as a reconcile error.
func (uc *nlbGatewayUseCase) handleDeletion(ctx context.Context, gw *gwv1.Gateway) error {
	if err := uc.deleteOwnedLBC(ctx, gw); err != nil {
		return err
	}
	if err := uc.finalizerManager.RemoveFinalizers(ctx, gw, domain.GatewayFinalizer); err != nil {
		return fmt.Errorf("remove finalizer on Gateway %s/%s: %w", gw.Namespace, gw.Name, err)
	}
	return nil
}

// listOwnedLBCs returns the LBCs owned by this Gateway (by the gateway-uid label).
func (uc *nlbGatewayUseCase) listOwnedLBCs(ctx context.Context, gw *gwv1.Gateway) ([]*v1alpha1.LoadBalancerConfig, error) {
	var list v1alpha1.LoadBalancerConfigList
	if err := uc.k8sRepo.ListLoadBalancerConfig(ctx, &list,
		client.InNamespace(gw.Namespace),
		client.MatchingLabels{domain.OwnerLabelGatewayUID: string(gw.UID)},
	); err != nil {
		return nil, fmt.Errorf("list LBCs owned by Gateway %s/%s: %w", gw.Namespace, gw.Name, err)
	}
	out := make([]*v1alpha1.LoadBalancerConfig, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, &list.Items[i])
	}
	return out, nil
}

func (uc *nlbGatewayUseCase) deleteOwnedLBC(ctx context.Context, gw *gwv1.Gateway) error {
	lbcs, err := uc.listOwnedLBCs(ctx, gw)
	if err != nil {
		return err
	}
	for i := range lbcs {
		if err := uc.k8sRepo.DeleteLoadBalancerConfig(ctx, lbcs[i]); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete LBC %s/%s: %w", lbcs[i].Namespace, lbcs[i].Name, err)
		}
	}
	return nil
}
