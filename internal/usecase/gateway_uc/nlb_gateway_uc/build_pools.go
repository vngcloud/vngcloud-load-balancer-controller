package nlb_gateway_uc

import (
	"context"
	"fmt"
	"sort"

	v2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	sharedUC "github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/shared"
	pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

// buildListenersAndPools walks the Gateway's TCP/UDP listeners, attaches the
// oldest matching L4 route to each, and builds one default pool per listener
// from that route's backendRefs. HTTP/HTTPS listeners (L7) on an NLB Gateway
// are unsupported and skipped here (reported UnsupportedProtocol in status).
// A listener with no attached route produces no LBC listener/pool — an L4
// listener has nothing to forward without a backend.
func (t *nlbBuildTask) buildListenersAndPools(ctx context.Context) ([]v1alpha1.Pool, []v1alpha1.Listener, error) {
	routes, err := t.listAttachedRoutes(ctx)
	if err != nil {
		return nil, nil, err
	}

	pools := make([]v1alpha1.Pool, 0)
	listeners := make([]v1alpha1.Listener, 0)

	for i := range t.gw.Spec.Listeners {
		l := &t.gw.Spec.Listeners[i]
		listenerProto, poolProto, hcProto, ok := mapL4Protocol(l.Protocol)
		if !ok {
			t.logger.Warnf("listener %q uses unsupported protocol %q for NLB; skipping", l.Name, l.Protocol)
			continue
		}
		route := oldestRouteForListener(routes, t.gw, l)
		if route == nil {
			t.logger.Warnf("NLB listener %q (%s:%d) has no attached %s route; skipping", l.Name, l.Protocol, l.Port, l.Protocol)
			continue
		}

		pool, err := t.synthesizeL4Pool(ctx, route, poolProto, hcProto)
		if err != nil {
			return nil, nil, err
		}
		pools = append(pools, *pool)

		entry := v1alpha1.Listener{
			Name:            t.cloudListenerName(l),
			Protocol:        listenerProto,
			ProtocolPort:    int32(l.Port),
			DefaultPoolName: &pool.Name,
		}
		t.applyListenerPolicy(&entry)
		listeners = append(listeners, entry)
	}

	if len(listeners) == 0 {
		return nil, nil, fmt.Errorf("gateway %s/%s has no NLB-supported listeners with an attached route", t.gw.Namespace, t.gw.Name)
	}

	sort.SliceStable(pools, func(i, j int) bool { return pools[i].Name < pools[j].Name })
	sort.SliceStable(listeners, func(i, j int) bool { return listeners[i].Name < listeners[j].Name })
	return pools, listeners, nil
}

// mapL4Protocol maps a Gateway listener protocol to the LBC listener/pool/HC
// enums. Only TCP and UDP are supported on the NLB path.
func mapL4Protocol(p gwv1.ProtocolType) (v2.ListenerProtocol, v2.PoolProtocol, v2.HealthCheckProtocol, bool) {
	switch p {
	case gwv1.TCPProtocolType:
		return v2.ListenerProtocolTCP, v2.PoolProtocolTCP, v2.HealthCheckProtocolTCP, true
	case gwv1.UDPProtocolType:
		return v2.ListenerProtocolUDP, v2.PoolProtocolUDP, v2.HealthCheckProtocolPINGUDP, true
	}
	return "", "", "", false
}

// synthesizeL4Pool builds one pool from an L4 route's backendRefs. Mirrors the
// service_uc L4 pool shape (TCP/PING-UDP health, instance/ip members) but
// sources backends from the route and tuning from VKS policies.
func (t *nlbBuildTask) synthesizeL4Pool(ctx context.Context, route *l4Route, poolProto v2.PoolProtocol, hcProto v2.HealthCheckProtocol) (*v1alpha1.Pool, error) {
	if len(route.backendRefs) == 0 {
		return nil, fmt.Errorf("%s %s/%s has no backendRefs", route.kind, route.namespace, route.name)
	}

	keys := make([]pkggw.BackendKey, 0, len(route.backendRefs))
	weights := make([]pkggw.BackendWeight, 0, len(route.backendRefs))
	memberAddrs := make([][]utils.EndpointAddress, 0, len(route.backendRefs))

	for i := range route.backendRefs {
		br := &route.backendRefs[i]
		ns := route.namespace
		if br.Namespace != nil {
			ns = string(*br.Namespace)
		}
		if ns != route.namespace {
			allowed, err := sharedUC.RefGrantAllowed(ctx, t.uc.k8sClient,
				sharedUC.Ref{Group: "", Kind: "Service", Namespace: ns, Name: string(br.Name)},
				sharedUC.Ref{Group: gwv1.GroupName, Kind: route.kind, Namespace: route.namespace, Name: route.name})
			if err != nil {
				return nil, fmt.Errorf("check ReferenceGrant for backendRef %s/%s: %w", ns, br.Name, err)
			}
			if !allowed {
				t.logger.Warnf("backendRef %s/%s on %s %s/%s is cross-namespace and not permitted; skipping",
					ns, br.Name, route.kind, route.namespace, route.name)
				continue
			}
		}
		port := int32(0)
		if br.Port != nil {
			port = int32(*br.Port)
		}
		weight := int32(1)
		if br.Weight != nil {
			weight = *br.Weight
		}
		if weight == 0 {
			continue
		}

		targetType, err := t.resolveTargetType(ctx, ns, string(br.Name))
		if err != nil {
			return nil, err
		}
		nodeLabels, err := t.resolveTargetNodeLabels(ctx, ns, string(br.Name))
		if err != nil {
			return nil, err
		}
		svcKey := types.NamespacedName{Namespace: ns, Name: string(br.Name)}
		resolveOpts := []utils.EndpointResolveOption{
			utils.WithNodeSelector(labels.SelectorFromSet(labels.Set(nodeLabels))),
		}
		var addrs []utils.EndpointAddress
		switch targetType {
		case domain.TargetTypeInstance:
			addrs, err = t.uc.endpointResolver.ResolveNodePortEndpoints(ctx, svcKey, intstr.FromInt(int(port)), resolveOpts...)
		default:
			addrs, err = t.uc.endpointResolver.ResolvePodEndpoints(ctx, svcKey, intstr.FromInt(int(port)), resolveOpts...)
		}
		if err != nil {
			return nil, fmt.Errorf("resolve endpoints for backendRef %s/%s (mode=%s): %w", ns, br.Name, targetType, err)
		}

		keys = append(keys, pkggw.BackendKey{Namespace: ns, Name: string(br.Name), Port: port, Weight: weight})
		weights = append(weights, pkggw.BackendWeight{Weight: weight, Ready: int32(len(addrs))})
		memberAddrs = append(memberAddrs, addrs)
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("%s %s/%s: all backends were dropped (cross-ns or zero-weight)", route.kind, route.namespace, route.name)
	}

	scaled := pkggw.ScaleWeights(weights)
	members := make([]v1alpha1.PoolMember, 0)
	for i, addrs := range memberAddrs {
		w := int(scaled[i])
		for _, a := range addrs {
			members = append(members, v1alpha1.PoolMember{
				Name:        memberName(a.Name),
				IP:          a.IP,
				Port:        a.Port,
				MonitorPort: a.Port,
				Weight:      &w,
			})
		}
	}
	sort.SliceStable(members, func(i, j int) bool {
		if members[i].IP != members[j].IP {
			return members[i].IP < members[j].IP
		}
		return members[i].Port < members[j].Port
	})

	pool := &v1alpha1.Pool{
		Name:     pkggw.SynthPoolName(route.uid, 0, keys),
		Protocol: poolProto,
		Members:  members,
		HealthMonitor: v1alpha1.PoolHealthMonitor{
			Protocol: hcProto,
		},
	}

	first := keys[0]
	if err := t.applyBackendPolicyToPool(ctx, pool, first.Namespace, first.Name); err != nil {
		return nil, err
	}
	if err := t.applyHealthCheckPolicyToPool(ctx, pool, first.Namespace, first.Name); err != nil {
		return nil, err
	}
	return pool, nil
}

// --- VKS policy resolution + overlays (lean L4 versions) ---

func (t *nlbBuildTask) resolveBackendPolicy(ctx context.Context, ns, svcName string) (*gwv1alpha1.VKSBackendPolicy, error) {
	var list gwv1alpha1.VKSBackendPolicyList
	if err := t.uc.k8sClient.List(ctx, &list, listInNamespace(ns)); err != nil {
		return nil, fmt.Errorf("list VKSBackendPolicy in %s: %w", ns, err)
	}
	cands := make([]*gwv1alpha1.VKSBackendPolicy, 0, len(list.Items))
	for i := range list.Items {
		cands = append(cands, &list.Items[i])
	}
	win, _ := sharedUC.ResolveDirectPolicy(cands, pkggw.PolicyTarget{Group: "", Kind: "Service", Namespace: ns, Name: svcName})
	return win, nil
}

func (t *nlbBuildTask) resolveHealthCheckPolicy(ctx context.Context, ns, svcName string) (*gwv1alpha1.VKSHealthCheckPolicy, error) {
	var list gwv1alpha1.VKSHealthCheckPolicyList
	if err := t.uc.k8sClient.List(ctx, &list, listInNamespace(ns)); err != nil {
		return nil, fmt.Errorf("list VKSHealthCheckPolicy in %s: %w", ns, err)
	}
	cands := make([]*gwv1alpha1.VKSHealthCheckPolicy, 0, len(list.Items))
	for i := range list.Items {
		cands = append(cands, &list.Items[i])
	}
	win, _ := sharedUC.ResolveDirectPolicy(cands, pkggw.PolicyTarget{Group: "", Kind: "Service", Namespace: ns, Name: svcName})
	return win, nil
}

func (t *nlbBuildTask) resolveTargetType(ctx context.Context, ns, svcName string) (domain.TargetType, error) {
	bp, err := t.resolveBackendPolicy(ctx, ns, svcName)
	if err != nil {
		return "", err
	}
	if bp != nil && bp.Spec.TargetType != nil {
		switch *bp.Spec.TargetType {
		case string(domain.TargetTypeIP):
			return domain.TargetTypeIP, nil
		case string(domain.TargetTypeInstance):
			return domain.TargetTypeInstance, nil
		}
	}
	return domain.TargetTypeInstance, nil
}

func (t *nlbBuildTask) resolveTargetNodeLabels(ctx context.Context, ns, svcName string) (map[string]string, error) {
	bp, err := t.resolveBackendPolicy(ctx, ns, svcName)
	if err != nil {
		return nil, err
	}
	if bp == nil {
		return nil, nil
	}
	return bp.Spec.TargetNodeLabels, nil
}

// applyBackendPolicyToPool merges algorithm/stickiness onto the pool. L4 pools
// don't terminate TLS, so EnableTLSEncryption is ignored here.
func (t *nlbBuildTask) applyBackendPolicyToPool(ctx context.Context, pool *v1alpha1.Pool, ns, svcName string) error {
	bp, err := t.resolveBackendPolicy(ctx, ns, svcName)
	if err != nil {
		return err
	}
	if bp == nil {
		return nil
	}
	if bp.Spec.PoolAlgorithm != nil {
		alg := v2.PoolAlgorithm(*bp.Spec.PoolAlgorithm)
		pool.Algorithm = &alg
	}
	if bp.Spec.Stickiness != nil {
		v := *bp.Spec.Stickiness
		pool.Stickiness = &v
	}
	// PROXY protocol: switch a TCP pool to the PROXY pool protocol so the cloud
	// LB prepends a PROXY header and an L4 backend (e.g. HAProxy/nginx ingress)
	// can recover the real client IP. TCP-only — UDP pools are left untouched.
	// Mirrors the Service controller's enable-proxy-protocol annotation. Set
	// this before the Gateway is created; pool protocol is fixed at create time.
	if bp.Spec.ProxyProtocol != nil && *bp.Spec.ProxyProtocol && pool.Protocol == v2.PoolProtocolTCP {
		pool.Protocol = v2.PoolProtocolProxy
	}
	return nil
}

// applyHealthCheckPolicyToPool overlays VKSHealthCheckPolicy onto the L4 pool's
// monitor. For TCP/PING-UDP the protocol stays as the listener default unless
// the policy explicitly asks for HTTP/HTTPS (an HTTP probe over a TCP pool is
// valid on the vngcloud API); thresholds/interval/timeout/port always apply.
func (t *nlbBuildTask) applyHealthCheckPolicyToPool(ctx context.Context, pool *v1alpha1.Pool, ns, svcName string) error {
	hp, err := t.resolveHealthCheckPolicy(ctx, ns, svcName)
	if err != nil {
		return err
	}
	if hp == nil {
		return nil
	}
	s := hp.Spec
	mon := pool.HealthMonitor // keep the protocol default (TCP / PING-UDP)
	switch v2.HealthCheckProtocol(s.Protocol) {
	case v2.HealthCheckProtocolHTTP, v2.HealthCheckProtocolHTTPs, v2.HealthCheckProtocolTCP, v2.HealthCheckProtocolPINGUDP:
		mon.Protocol = v2.HealthCheckProtocol(s.Protocol)
	}
	if s.Interval != nil {
		mon.Interval = ptrInt(int(s.Interval.Seconds()))
	}
	if s.Timeout != nil {
		mon.Timeout = ptrInt(int(s.Timeout.Seconds()))
	}
	if s.HealthyThreshold != nil {
		mon.HealthyThreshold = ptrInt(int(*s.HealthyThreshold))
	}
	if s.UnhealthyThreshold != nil {
		mon.UnhealthyThreshold = ptrInt(int(*s.UnhealthyThreshold))
	}
	if mon.Protocol == v2.HealthCheckProtocolHTTP || mon.Protocol == v2.HealthCheckProtocolHTTPs {
		method := v2.HealthCheckMethodGET
		ver := v2.HealthCheckHttpVersionHttp1Minor1
		path := "/"
		code := "200"
		if s.HTTPHealthCheck != nil {
			if s.HTTPHealthCheck.Method != nil {
				method = v2.HealthCheckMethod(*s.HTTPHealthCheck.Method)
			}
			if s.HTTPHealthCheck.HTTPVersion != nil {
				ver = v2.HealthCheckHttpVersion(*s.HTTPHealthCheck.HTTPVersion)
			}
			if s.HTTPHealthCheck.Path != nil {
				path = *s.HTTPHealthCheck.Path
			}
			if s.HTTPHealthCheck.Host != nil {
				mon.DomainName = s.HTTPHealthCheck.Host
			}
			if len(s.HTTPHealthCheck.ExpectedCodes) > 0 {
				code = joinExpectedCodes(s.HTTPHealthCheck.ExpectedCodes)
			}
		}
		mon.HealthCheckMethod = &method
		mon.HttpVersion = &ver
		mon.HealthCheckPath = &path
		mon.SuccessCode = &code
	}
	pool.HealthMonitor = mon
	if s.Port != nil {
		for i := range pool.Members {
			pool.Members[i].MonitorPort = int(*s.Port)
		}
	}
	return nil
}

// applyListenerPolicy copies L4-relevant listener fields (timeouts, allowedCidrs)
// from the unscoped VKSGatewayPolicy. NLB listeners have no insertHeaders/certs.
func (t *nlbBuildTask) applyListenerPolicy(entry *v1alpha1.Listener) {
	p := t.unscopedPolicy
	if p == nil {
		return
	}
	if p.Spec.TimeoutClient != nil {
		entry.TimeoutClient = ptr32(int32(p.Spec.TimeoutClient.Seconds()))
	}
	if p.Spec.TimeoutMember != nil {
		entry.TimeoutMember = ptr32(int32(p.Spec.TimeoutMember.Seconds()))
	}
	if p.Spec.TimeoutConnection != nil {
		entry.TimeoutConnection = ptr32(int32(p.Spec.TimeoutConnection.Seconds()))
	}
	if len(p.Spec.AllowedCIDRs) > 0 {
		s := joinCommaStrings(p.Spec.AllowedCIDRs)
		entry.AllowedCidrs = &s
	}
}

// cloudListenerName: "vks_gw_<uid8>_<lname>", ≤50 chars.
func (t *nlbBuildTask) cloudListenerName(l *gwv1.Listener) string {
	uid := string(t.gw.UID)
	if len(uid) > 8 {
		uid = uid[:8]
	}
	name := fmt.Sprintf("%sgw_%s_%s", domain.VKSResourceNamePrefix, uid, l.Name)
	if len(name) > 50 {
		name = name[:50]
	}
	return name
}
