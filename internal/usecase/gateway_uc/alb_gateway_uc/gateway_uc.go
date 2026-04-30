// Package alb_gateway_uc implements the ALB Gateway-API use case.
//
// One Gateway corresponds to one vngcloud ALB. The Gateway reconciler is the only
// writer to the LB; HTTPRoute reconciles call EnqueueParentGatewayForRoute, which
// surfaces the route change as a Gateway reconcile so all LB mutations are serialized.
package alb_gateway_uc

import (
	"context"
	"sync"

	"github.com/anngdinh/operator-helper/contexts"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/common"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/annotations"
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
	// Filled in by Task C9.
	_ = req
	return nil
}

func (uc *albGatewayUseCase) DeleteALBGatewayUseCase(ctx context.Context, req ctrl.Request) error {
	// Filled in by Task C9.
	_ = req
	return nil
}

func (uc *albGatewayUseCase) EnqueueParentGatewayForRoute(ctx context.Context, routeRef ctrl.Request) error {
	// Filled in by Task C9.
	_ = routeRef
	return nil
}
