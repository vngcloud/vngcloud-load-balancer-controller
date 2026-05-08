package alb_gateway_uc

import (
	"context"
	"errors"
	"fmt"

	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	gatewayv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/shared"
	pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

// Sentinel errors for backend-resolution failures. The route status writer
// inspects these via errors.Is to map to gateway-api ResolvedRefs reasons.
var (
	errInvalidBackendKind = errors.New("invalid backend kind")
	errBackendMissingPort = errors.New("backend missing port")
	errRefNotPermitted    = errors.New("reference not permitted")
)

// resolveBackend turns one HTTPRoute backendRef into a BackendEndpoints value
// (one BackendKey + the resolved endpoint IPs). It honors:
//   - Phase 1 only Service backends in the core API group
//   - cross-namespace refs require a ReferenceGrant (shared.RefGrantAllowed)
//   - target-type from the matched TargetGroupConfig (default: instance/NodePort)
//   - target-node selector from the matched TargetGroupConfig
//
// Returns an error when the backend is unsupported, missing required fields,
// not granted across namespaces, or has no resolvable endpoints. Callers (the
// route attacher) typically log+skip on error so partial-rule failures don't
// take the whole reconcile down; status surfacing is C9e's job.
func (uc *albGatewayUseCase) resolveBackend(
	ctx context.Context,
	//nolint:unparam // routeKind is "HTTPRoute" today; carried as a param for Phase-2 extension
	routeNS, routeName, routeKind string,
	ruleName *string,
	backend gwv1.HTTPBackendRef,
	grants []gwv1beta1.ReferenceGrant,
	tgcs []gatewayv1alpha1.TargetGroupConfig,
) (*BackendEndpoints, *gatewayv1alpha1.TargetGroupProperties, error) {
	ref := backend.BackendObjectReference

	group := ""
	if ref.Group != nil {
		group = string(*ref.Group)
	}
	kind := "Service"
	if ref.Kind != nil && *ref.Kind != "" {
		kind = string(*ref.Kind)
	}
	if group != "" || kind != "Service" {
		return nil, nil, fmt.Errorf("%w: %s/%s (Phase 1: core/Service only)", errInvalidBackendKind, group, kind)
	}
	if ref.Port == nil {
		return nil, nil, fmt.Errorf("%w: backendRef %q", errBackendMissingPort, ref.Name)
	}

	backendNS := routeNS
	if ref.Namespace != nil {
		backendNS = string(*ref.Namespace)
	}
	if backendNS != routeNS {
		req := shared.RefRequest{
			FromGroup: gwv1.GroupName, FromKind: "HTTPRoute", FromNS: routeNS,
			ToGroup: "", ToKind: "Service", ToNS: backendNS, ToName: string(ref.Name),
		}
		if !shared.RefGrantAllowed(req, asGrantPtrs(grants)) {
			return nil, nil, fmt.Errorf("%w: cross-namespace backend %s/%s requires ReferenceGrant", errRefNotPermitted, backendNS, ref.Name)
		}
	}

	props, _ := shared.ResolveTargetGroupProps(tgcs, string(ref.Name), routeKind, routeName, ruleName)

	targetType := domain.TargetTypeInstance
	if props.TargetType != nil && *props.TargetType == string(domain.TargetTypeIP) {
		targetType = domain.TargetTypeIP
	}

	// Always pass a NodeSelector — the resolver's default is labels.Nothing()
	// (matches zero nodes), so omitting the option silently yields zero endpoints.
	// SelectorFromSet on an empty/nil map returns labels.Everything(), which is
	// what we want when no TargetGroupConfig.TargetNodeLabels is specified.
	resolveOpts := []utils.EndpointResolveOption{
		utils.WithNodeSelector(labels.SelectorFromSet(labels.Set(props.TargetNodeLabels))),
	}

	svcKey := types.NamespacedName{Namespace: backendNS, Name: string(ref.Name)}
	svcPort := intstr.FromInt(int(*ref.Port))

	var addrs []utils.EndpointAddress
	var err error
	if targetType == domain.TargetTypeIP {
		addrs, err = uc.endpointResolver.ResolvePodEndpoints(ctx, svcKey, svcPort, resolveOpts...)
	} else {
		addrs, err = uc.endpointResolver.ResolveNodePortEndpoints(ctx, svcKey, svcPort, resolveOpts...)
	}
	if err != nil {
		return nil, &props, err
	}
	if len(addrs) == 0 {
		return nil, &props, fmt.Errorf("no endpoints for service %s", svcKey)
	}

	weight := int32(1)
	if backend.Weight != nil {
		weight = *backend.Weight
	}

	ips := make([]string, 0, len(addrs))
	for _, a := range addrs {
		ips = append(ips, a.IP)
	}

	return &BackendEndpoints{
		Backend: pkggw.BackendKey{
			Namespace: backendNS,
			Name:      string(ref.Name),
			Port:      int32(addrs[0].Port),
			Weight:    weight,
		},
		Endpoints: ips,
	}, &props, nil
}

func asGrantPtrs(in []gwv1beta1.ReferenceGrant) []*gwv1beta1.ReferenceGrant {
	out := make([]*gwv1beta1.ReferenceGrant, len(in))
	for i := range in {
		out[i] = &in[i]
	}
	return out
}

// buildPoolFromBackends produces a vngcloud Pool from the resolved backends of
// one HTTPRoute rule. Phase 1 defaults: Protocol=HTTP, HealthMonitor=TCP. The
// matched TargetGroupConfig (if any) overrides algorithm, stickiness, TLS
// encryption, and the health monitor block.
func buildPoolFromBackends(name string, backends []BackendEndpoints, props *gatewayv1alpha1.TargetGroupProperties) vksv1alpha1.Pool {
	pool := vksv1alpha1.Pool{
		Name:     name,
		Protocol: loadbalancerv2.PoolProtocolHTTP,
		HealthMonitor: vksv1alpha1.PoolHealthMonitor{
			Protocol: loadbalancerv2.HealthCheckProtocolTCP,
		},
		Members: SynthesizeMembers(backends),
	}
	if props != nil {
		if props.PoolAlgorithm != nil && *props.PoolAlgorithm != "" {
			alg := loadbalancerv2.PoolAlgorithm(*props.PoolAlgorithm)
			pool.Algorithm = &alg
		}
		if props.EnableStickySession != nil {
			pool.Stickiness = ptr.To(*props.EnableStickySession)
		}
		if props.EnableTLSEncryption != nil {
			pool.TLSEncryption = ptr.To(*props.EnableTLSEncryption)
		}
		if props.HealthCheck != nil {
			pool.HealthMonitor = *props.HealthCheck
		}
	}
	return pool
}
