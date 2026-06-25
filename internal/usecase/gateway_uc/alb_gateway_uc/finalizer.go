package alb_gateway_uc

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
)

func (uc *albGatewayUseCase) listNodesForNetworkProbe(ctx context.Context) ([]*corev1.Node, error) {
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

func (uc *albGatewayUseCase) ensureFinalizer(ctx context.Context, gw *gwv1.Gateway) error {
	if err := uc.finalizerManager.AddFinalizers(ctx, gw, domain.GatewayFinalizer); err != nil {
		return fmt.Errorf("add finalizer on Gateway %s/%s: %w", gw.Namespace, gw.Name, err)
	}
	return nil
}

// handleDeletion removes the LBC owned by this Gateway and then drops the
// finalizer so the apiserver can complete the delete. The LBC controller's
// own finalizer ensures the cloud LB is gone before the LBC object is.
//
// RemoveFinalizers re-Gets the Gateway before patching and treats an
// already-deleted object as success, so a racing delete (the stale-UID
// StorageError we used to log) no longer surfaces as a reconcile error.
func (uc *albGatewayUseCase) handleDeletion(ctx context.Context, gw *gwv1.Gateway) error {
	if err := uc.deleteOwnedLBC(ctx, gw); err != nil {
		return err
	}
	if err := uc.finalizerManager.RemoveFinalizers(ctx, gw, domain.GatewayFinalizer); err != nil {
		return fmt.Errorf("remove finalizer on Gateway %s/%s: %w", gw.Namespace, gw.Name, err)
	}
	return nil
}

func (uc *albGatewayUseCase) deleteOwnedLBC(ctx context.Context, gw *gwv1.Gateway) error {
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
