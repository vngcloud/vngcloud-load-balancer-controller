package alb_gateway_uc

import (
	"context"
	"fmt"

	v2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	sharedUC "github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/shared"
	pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

// applyBackendPolicyToPool merges fields from a VKSBackendPolicy targeting
// the named Service into pool. The policy is resolved oldest-wins; conflict
// reporting is the validator controller's job.
//
// vngcloud Pool fields touched: Algorithm, Stickiness, TLSEncryption.
// VKSBackendPolicy.TargetNodeLabels and ManageDFPMembers are controller-side
// (not in LBC.Pool); they're handled when we add target-mode selection in
// a future phase.
func (t *defaultGatewayBuildTask) applyBackendPolicyToPool(ctx context.Context, pool *v1alpha1.Pool, ns, svcName string) error {
	bp, err := t.resolveBackendPolicy(ctx, ns, svcName)
	if err != nil {
		return err
	}
	if bp == nil {
		return nil
	}
	s := bp.Spec
	if s.PoolAlgorithm != nil {
		alg := v2.PoolAlgorithm(*s.PoolAlgorithm)
		pool.Algorithm = &alg
	}
	if s.Stickiness != nil {
		v := *s.Stickiness
		pool.Stickiness = &v
	}
	if s.EnableTLSEncryption != nil {
		v := *s.EnableTLSEncryption
		pool.TLSEncryption = &v
	}
	return nil
}

// applyHealthCheckPolicyToPool merges VKSHealthCheckPolicy fields into the
// pool's health monitor. Conflict resolution is oldest-wins. When two
// backendRefs in the same rule have conflicting policies, the rule fails
// translation; that's enforced one level up.
func (t *defaultGatewayBuildTask) applyHealthCheckPolicyToPool(ctx context.Context, pool *v1alpha1.Pool, ns, svcName string) error {
	hp, err := t.resolveHealthCheckPolicy(ctx, ns, svcName)
	if err != nil {
		return err
	}
	if hp == nil {
		return nil
	}
	s := hp.Spec
	mon := v1alpha1.PoolHealthMonitor{
		Protocol: v2.HealthCheckProtocol(s.Protocol),
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
	// HTTP/HTTPS probes need healthCheckMethod + httpVersion set or the
	// vngcloud API rejects CreatePool. Default to GET / 1.1 — same defaults
	// the Ingress controller falls back to when annotations are absent — and
	// let HTTPHealthCheck.Method / HTTPHealthCheck.HTTPVersion override them.
	// These are HTTP-only; for TCP probes the whole block is skipped, so a
	// method/version set on a TCP policy is silently ignored (matches Ingress).
	if mon.Protocol == v2.HealthCheckProtocolHTTP || mon.Protocol == v2.HealthCheckProtocolHTTPs {
		method := v2.HealthCheckMethodGET
		if s.HTTPHealthCheck != nil && s.HTTPHealthCheck.Method != nil {
			method = v2.HealthCheckMethod(*s.HTTPHealthCheck.Method)
		}
		mon.HealthCheckMethod = &method
		ver := v2.HealthCheckHttpVersionHttp1Minor1
		if s.HTTPHealthCheck != nil && s.HTTPHealthCheck.HTTPVersion != nil {
			ver = v2.HealthCheckHttpVersion(*s.HTTPHealthCheck.HTTPVersion)
		}
		mon.HttpVersion = &ver
		// successCode is also required by the API; default to "200" when the
		// user didn't supply ExpectedCodes.
		if mon.SuccessCode == nil {
			defaultCode := "200"
			mon.SuccessCode = &defaultCode
		}
		// healthCheckPath is required too; default to "/".
		if mon.HealthCheckPath == nil {
			defaultPath := "/"
			mon.HealthCheckPath = &defaultPath
		}
	}
	if s.HTTPHealthCheck != nil {
		if s.HTTPHealthCheck.Path != nil {
			mon.HealthCheckPath = s.HTTPHealthCheck.Path
		}
		if s.HTTPHealthCheck.Host != nil {
			mon.DomainName = s.HTTPHealthCheck.Host
		}
		if len(s.HTTPHealthCheck.ExpectedCodes) > 0 {
			// LBC stores SuccessCode as a single string. Join with commas to
			// preserve any multi-value expression the user wrote (e.g. "200-299,301").
			joined := joinExpectedCodes(s.HTTPHealthCheck.ExpectedCodes)
			mon.SuccessCode = &joined
		}
	}
	pool.HealthMonitor = mon
	// Port overrides the monitor port on every member (protocol-agnostic).
	// Matches the Ingress `healthcheck-port` annotation, which sets each
	// member's MonitorPort regardless of instance/ip target type.
	if s.Port != nil {
		for i := range pool.Members {
			pool.Members[i].MonitorPort = int(*s.Port)
		}
	}
	return nil
}

// resolveTargetType returns the effective member-resolution mode for a
// Service: VKSBackendPolicy.TargetType when set, else domain.TargetTypeInstance
// (matches the Ingress controller's default — works on overlay CNIs where
// pod IPs aren't routable from the cloud LB; user opts into "ip" mode if
// pods are directly routable).
func (t *defaultGatewayBuildTask) resolveTargetType(ctx context.Context, ns, svcName string) (domain.TargetType, error) {
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

// resolveTargetNodeLabels returns the VKSBackendPolicy.TargetNodeLabels for
// the named Service. Empty map → "every node" (labels.Everything()) which
// is the only sensible default when no policy is attached.
func (t *defaultGatewayBuildTask) resolveTargetNodeLabels(ctx context.Context, ns, svcName string) (map[string]string, error) {
	bp, err := t.resolveBackendPolicy(ctx, ns, svcName)
	if err != nil {
		return nil, err
	}
	if bp == nil {
		return nil, nil
	}
	return bp.Spec.TargetNodeLabels, nil
}

// ruleBackendPoliciesDiverge reports whether a rule's same-namespace backends
// resolve to different VKSBackendPolicy / VKSHealthCheckPolicy objects. A
// synthetic pool aggregates all of a rule's backends but can carry only one
// overlay, so divergence must fail the rule closed (rather than silently
// applying the first backend's policy). Single-backend rules never diverge.
func (t *defaultGatewayBuildTask) ruleBackendPoliciesDiverge(ctx context.Context, route *gwv1.HTTPRoute, rule gwv1.HTTPRouteRule) (bool, error) {
	hcKeys := map[string]struct{}{}
	bpKeys := map[string]struct{}{}
	considered := 0
	for i := range rule.BackendRefs {
		br := &rule.BackendRefs[i].BackendRef
		ns := route.Namespace
		if br.Namespace != nil {
			ns = string(*br.Namespace)
		}
		if ns != route.Namespace { // cross-ns handled by ReferenceGrant; not compared here
			continue
		}
		if br.Weight != nil && *br.Weight == 0 {
			continue
		}
		hp, err := t.resolveHealthCheckPolicy(ctx, ns, string(br.Name))
		if err != nil {
			return false, err
		}
		bp, err := t.resolveBackendPolicy(ctx, ns, string(br.Name))
		if err != nil {
			return false, err
		}
		hcKey := ""
		if hp != nil {
			hcKey = hp.Namespace + "/" + hp.Name
		}
		bpKey := ""
		if bp != nil {
			bpKey = bp.Namespace + "/" + bp.Name
		}
		hcKeys[hcKey] = struct{}{}
		bpKeys[bpKey] = struct{}{}
		considered++
	}
	if considered < 2 {
		return false, nil
	}
	return len(hcKeys) > 1 || len(bpKeys) > 1, nil
}

func (t *defaultGatewayBuildTask) resolveBackendPolicy(ctx context.Context, ns, svcName string) (*gwv1alpha1.VKSBackendPolicy, error) {
	var list gwv1alpha1.VKSBackendPolicyList
	if err := t.uc.k8sClient.List(ctx, &list, client.InNamespace(ns)); err != nil {
		return nil, fmt.Errorf("list VKSBackendPolicy in %s: %w", ns, err)
	}
	cands := make([]*gwv1alpha1.VKSBackendPolicy, 0, len(list.Items))
	for i := range list.Items {
		cands = append(cands, &list.Items[i])
	}
	target := pkggw.PolicyTarget{Group: "", Kind: "Service", Namespace: ns, Name: svcName}
	win, _ := sharedUC.ResolveDirectPolicy(cands, target)
	return win, nil
}

func (t *defaultGatewayBuildTask) resolveHealthCheckPolicy(ctx context.Context, ns, svcName string) (*gwv1alpha1.VKSHealthCheckPolicy, error) {
	var list gwv1alpha1.VKSHealthCheckPolicyList
	if err := t.uc.k8sClient.List(ctx, &list, client.InNamespace(ns)); err != nil {
		return nil, fmt.Errorf("list VKSHealthCheckPolicy in %s: %w", ns, err)
	}
	cands := make([]*gwv1alpha1.VKSHealthCheckPolicy, 0, len(list.Items))
	for i := range list.Items {
		cands = append(cands, &list.Items[i])
	}
	target := pkggw.PolicyTarget{Group: "", Kind: "Service", Namespace: ns, Name: svcName}
	win, _ := sharedUC.ResolveDirectPolicy(cands, target)
	return win, nil
}

func ptrInt(v int) *int { return &v }

func joinExpectedCodes(codes []string) string {
	out := ""
	for i, c := range codes {
		if i > 0 {
			out += ","
		}
		out += c
	}
	return out
}
