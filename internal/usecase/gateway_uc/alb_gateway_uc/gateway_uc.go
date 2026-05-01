// Package alb_gateway_uc implements the ALB Gateway-API use case.
//
// One Gateway corresponds to one vngcloud ALB. The Gateway reconciler is the only
// writer to the LB; HTTPRoute reconciles call EnqueueParentGatewayForRoute, which
// surfaces the route change as a Gateway reconcile so all LB mutations are serialized.
package alb_gateway_uc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/anngdinh/operator-helper/contexts"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/common"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/annotations"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/errs"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

type albGatewayUseCase struct {
	k8sRepo          repository.K8sRepository
	vngcloudRepo     repository.VngCloudRepository
	annotationParser annotations.Parser
	cniDetector      utils.CniDetector
	endpointResolver utils.EndpointResolver

	clusterId         string
	defaultNetworkId  string
	defaultSubnetId   string
	defaultSubnetCIDR string
	defaultZone       common.Zone

	initOnce sync.Once
	initErr  error
}

// NewALBGatewayUseCase constructs an ALBGatewayUseCase. The constructor mirrors
// NewIngressUseCase: cluster ID and default network info are auto-resolved on first Init.
func NewALBGatewayUseCase(
	clusterId string,
	k8sRepo repository.K8sRepository,
	vngcloudRepo repository.VngCloudRepository,
	annotationParser annotations.Parser,
	cniDetector utils.CniDetector,
	endpointResolver utils.EndpointResolver,
) usecase.ALBGatewayUseCase {
	return &albGatewayUseCase{
		clusterId:        clusterId,
		k8sRepo:          k8sRepo,
		vngcloudRepo:     vngcloudRepo,
		annotationParser: annotationParser,
		cniDetector:      cniDetector,
		endpointResolver: endpointResolver,
	}
}

func (uc *albGatewayUseCase) InitALBGatewayUseCase(ctx context.Context) error {
	_ = contexts.NewContext(ctx).Log()
	// Phase-1 init shares the same flow as IngressUseCase.Init:
	// resolve clusterId from node labels and pull default network info from the SDK.
	// Filled in alongside C9 wiring; the reconciler tolerates Init errors with backoff.
	return nil
}

func (uc *albGatewayUseCase) EnsureALBGatewayUseCase(ctx context.Context, req ctrl.Request) error {
	res, err := uc.resolveGatewayBuild(ctx, req)
	if err != nil {
		return err
	}
	if res == nil {
		return nil
	}
	// TODO(C9c): attach HTTPRoutes → pools + policies onto res.lbSpec.
	// TODO(C9d): create or patch the LoadBalancerConfig CRD from res.lbSpec.
	// TODO(C9e): write Accepted/Programmed/per-listener Gateway status.
	_ = res
	return nil
}

// DeleteALBGatewayUseCase tears down resources owned by the Gateway. The
// reconciler removes the finalizer only after this returns nil. While owned
// LoadBalancerConfig / NodeSecurityGroup resources are still being deleted,
// a RequeueNeededAfter is returned so the controller polls until they're gone.
func (uc *albGatewayUseCase) DeleteALBGatewayUseCase(ctx context.Context, req ctrl.Request) error {
	gw, err := uc.k8sRepo.GetGateway(ctx, req.NamespacedName)
	if err != nil {
		return client.IgnoreNotFound(err)
	}

	logger := contexts.NewContext(ctx).Log()

	type result struct {
		stillExist []string
		err        error
	}
	resultCh := make(chan result, 2)
	go func() {
		s, e := uc.deleteLoadBalancerConfig(ctx, gw)
		resultCh <- result{stillExist: s, err: e}
	}()
	go func() {
		s, e := uc.deleteNodeSecurityGroup(ctx, gw)
		resultCh <- result{stillExist: s, err: e}
	}()

	var stillExist []string
	var realErrs []error
	for i := 0; i < 2; i++ {
		r := <-resultCh
		if r.err != nil {
			realErrs = append(realErrs, r.err)
		}
		stillExist = append(stillExist, r.stillExist...)
	}

	if len(realErrs) > 0 {
		for _, e := range realErrs {
			logger.Errorf("failed to delete resources for gateway %s/%s: %v", gw.Namespace, gw.Name, e)
		}
		return errors.Join(realErrs...)
	}

	if len(stillExist) > 0 {
		return errs.NewRequeueNeededAfter(
			"waiting for resources to be deleted: "+strings.Join(stillExist, ", "),
			2*time.Second,
		)
	}
	return nil
}

func (uc *albGatewayUseCase) deleteLoadBalancerConfig(ctx context.Context, gw *gwv1.Gateway) ([]string, error) {
	logger := contexts.NewContext(ctx).Log()

	lbcList := &v1alpha1.LoadBalancerConfigList{}
	err := uc.k8sRepo.ListLoadBalancerConfig(ctx, lbcList,
		client.InNamespace(gw.Namespace),
		client.MatchingLabels{
			domain.LabelOwnerResourceName: gw.Name,
			domain.LabelOwnerResourceKind: gw.Kind,
			domain.LabelOwnerResourceUid:  string(gw.UID),
		})
	if err != nil {
		logger.Errorf("failed to list LBCs by label: %v", err)
		return nil, err
	}
	if len(lbcList.Items) == 0 {
		return nil, nil
	}

	stillExist := make([]string, 0, len(lbcList.Items))
	for i := range lbcList.Items {
		lbc := &lbcList.Items[i]
		if lbc.DeletionTimestamp.IsZero() {
			if delErr := uc.k8sRepo.DeleteLoadBalancerConfig(ctx, lbc); client.IgnoreNotFound(delErr) != nil {
				logger.Errorf("failed to delete LBC %s/%s: %v", lbc.Namespace, lbc.Name, delErr)
				return nil, delErr
			}
		}
		stillExist = append(stillExist, "lbc:"+lbc.Namespace+"/"+lbc.Name)
	}
	return stillExist, nil
}

func (uc *albGatewayUseCase) deleteNodeSecurityGroup(ctx context.Context, gw *gwv1.Gateway) ([]string, error) {
	logger := contexts.NewContext(ctx).Log()

	nsgList := &v1alpha1.NodeSecurityGroupList{}
	err := uc.k8sRepo.ListNodeSecurityGroup(ctx, nsgList,
		client.InNamespace(gw.Namespace),
		client.MatchingLabels{
			domain.LabelOwnerResourceName: gw.Name,
			domain.LabelOwnerResourceKind: gw.Kind,
			domain.LabelOwnerResourceUid:  string(gw.UID),
		})
	if err != nil {
		logger.Errorf("failed to list NodeSecurityGroups by label: %v", err)
		return nil, err
	}
	if len(nsgList.Items) == 0 {
		return nil, nil
	}

	stillExist := make([]string, 0, len(nsgList.Items))
	for i := range nsgList.Items {
		nsg := &nsgList.Items[i]
		if nsg.DeletionTimestamp.IsZero() {
			if delErr := uc.k8sRepo.DeleteNodeSecurityGroup(ctx, nsg); client.IgnoreNotFound(delErr) != nil {
				logger.Errorf("failed to delete NodeSecurityGroup %s/%s: %v", nsg.Namespace, nsg.Name, delErr)
				return nil, delErr
			}
		}
		stillExist = append(stillExist, "nsg:"+nsg.Namespace+"/"+nsg.Name)
	}
	return stillExist, nil
}

func (uc *albGatewayUseCase) EnqueueParentGatewayForRoute(ctx context.Context, routeRef ctrl.Request) error {
	// Filled in by Task C9.
	_ = routeRef
	return nil
}
