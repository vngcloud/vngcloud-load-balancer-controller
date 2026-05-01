package alb_gateway_uc

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	ctlshared "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/shared"
)

const (
	lbcRefGroup = "vks.vngcloud.vn"
	lbcRefKind  = "LoadBalancerConfig"
)

// resolvedGatewayBuild is the partial output of the resolve phase: the merged
// effective LoadBalancerConfig spec with synthesized listeners. Pools and
// policies are filled by the route-attach phase; persistence and status writes
// happen later in the reconcile.
type resolvedGatewayBuild struct {
	gateway   *gwv1.Gateway
	gwClass   *gwv1.GatewayClass
	effective *vksv1alpha1.LoadBalancerConfigSpec
	invalid   map[string]ctlshared.ListenerInvalidReason
	lbSpec    *vksv1alpha1.LoadBalancerConfigSpec
}

// resolveGatewayBuild loads the Gateway and its GatewayClass, dereferences
// parametersRef on both, merges them into the effective LBC spec, validates
// listeners, and pre-builds the LB spec with per-listener overrides applied.
//
// Returns (nil, nil) when the Gateway has been deleted or its GatewayClass's
// controllerName is not ours (a Gateway with the wrong class is silently
// ignored — status is the controller's job, not the use case's).
func (uc *albGatewayUseCase) resolveGatewayBuild(ctx context.Context, req ctrl.Request) (*resolvedGatewayBuild, error) {
	gw, err := uc.k8sRepo.GetGateway(ctx, req.NamespacedName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	gwc, err := uc.k8sRepo.GetGatewayClass(ctx, string(gw.Spec.GatewayClassName))
	if err != nil {
		return nil, err
	}
	if string(gwc.Spec.ControllerName) != domain.ControllerNameALB {
		return nil, nil
	}

	classLBC, err := uc.lookupClassLBC(ctx, gwc.Spec.ParametersRef)
	if err != nil {
		return nil, err
	}
	gwLBC, err := uc.lookupGatewayLBC(ctx, gw.Spec.Infrastructure, gw.Namespace)
	if err != nil {
		return nil, err
	}
	effective := shared.MergeLBC(classLBC, gwLBC)
	if effective == nil {
		effective = &vksv1alpha1.LoadBalancerConfigSpec{}
	}

	invalid := ctlshared.ValidateListenersForALB(gw.Spec.Listeners)

	lbSpec := BuildLBSpec(gw, effective, uc.clusterId)
	listeners := make([]vksv1alpha1.Listener, 0, len(gw.Spec.Listeners))
	certSrc := uc.CertSourceForGateway(gw.Namespace)
	for _, l := range gw.Spec.Listeners {
		if invalid[string(l.Name)] != ctlshared.ListenerInvalidReasonNone {
			continue
		}
		lbcL := lookupListenerByName(effective.Listeners, string(l.Name))
		built, berr := BuildListener(l, lbcL, certSrc)
		if berr != nil {
			return nil, fmt.Errorf("build listener %q: %w", string(l.Name), berr)
		}
		listeners = append(listeners, *built)
	}
	lbSpec.Listeners = listeners

	return &resolvedGatewayBuild{
		gateway:   gw,
		gwClass:   gwc,
		effective: effective,
		invalid:   invalid,
		lbSpec:    lbSpec,
	}, nil
}

func (uc *albGatewayUseCase) lookupClassLBC(ctx context.Context, ref *gwv1.ParametersReference) (*vksv1alpha1.LoadBalancerConfigSpec, error) {
	if ref == nil {
		return nil, nil
	}
	if string(ref.Group) != lbcRefGroup || string(ref.Kind) != lbcRefKind {
		return nil, fmt.Errorf("gatewayclass parametersRef must point to %s/%s, got %s/%s",
			lbcRefGroup, lbcRefKind, ref.Group, ref.Kind)
	}
	ns := ""
	if ref.Namespace != nil {
		ns = string(*ref.Namespace)
	}
	lbc, err := uc.k8sRepo.GetLoadBalancerConfig(ctx, types.NamespacedName{Namespace: ns, Name: ref.Name})
	if err != nil {
		return nil, err
	}
	return &lbc.Spec, nil
}

func (uc *albGatewayUseCase) lookupGatewayLBC(ctx context.Context, infra *gwv1.GatewayInfrastructure, gwNS string) (*vksv1alpha1.LoadBalancerConfigSpec, error) {
	if infra == nil || infra.ParametersRef == nil {
		return nil, nil
	}
	ref := infra.ParametersRef
	if string(ref.Group) != lbcRefGroup || string(ref.Kind) != lbcRefKind {
		return nil, fmt.Errorf("gateway parametersRef must point to %s/%s, got %s/%s",
			lbcRefGroup, lbcRefKind, ref.Group, ref.Kind)
	}
	lbc, err := uc.k8sRepo.GetLoadBalancerConfig(ctx, types.NamespacedName{Namespace: gwNS, Name: ref.Name})
	if err != nil {
		return nil, err
	}
	return &lbc.Spec, nil
}

func lookupListenerByName(listeners []vksv1alpha1.Listener, name string) *vksv1alpha1.Listener {
	for i := range listeners {
		if listeners[i].Name == name {
			return &listeners[i]
		}
	}
	return nil
}
