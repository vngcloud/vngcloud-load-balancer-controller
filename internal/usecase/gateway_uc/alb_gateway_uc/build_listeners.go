package alb_gateway_uc

import (
	"fmt"
	"strings"

	v2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
)

// buildListeners translates Gateway.Spec.Listeners into v1alpha1.Listener entries.
//
// Phase C scope: protocol/port/timeouts/allowedCidrs/insertHeaders/clientCertId.
// TLS certs (CertificateDefault / CertificateAuthorities) are filled in Phase D.
// L7 Policies are populated in Phase E from attached HTTPRoutes.
//
// Per the design spec, listener-level VKSGatewayPolicy fields win over the
// unscoped policy on the same Gateway, field by field. Listeners with
// unsupported protocols (TCP/UDP/TLS for ALB Phase 1) are dropped here and
// produce a per-listener Accepted=False condition in Phase F status work.
func (t *defaultGatewayBuildTask) buildListeners() ([]v1alpha1.Listener, error) {
	out := make([]v1alpha1.Listener, 0, len(t.gw.Spec.Listeners))
	for i := range t.gw.Spec.Listeners {
		l := &t.gw.Spec.Listeners[i]
		proto, ok := mapListenerProtocol(l.Protocol)
		if !ok {
			// Skip silently for now; Phase F will surface this on the
			// Gateway listener's Accepted condition.
			t.logger.Warnf("listener %q uses unsupported protocol %q for ALB; skipping",
				l.Name, l.Protocol)
			continue
		}
		entry := v1alpha1.Listener{
			Name:         t.cloudListenerName(l),
			Protocol:     proto,
			ProtocolPort: int32(l.Port),
		}
		t.applyListenerPolicy(&entry, l)
		t.applyListenerCertificates(&entry, l)
		out = append(out, entry)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("gateway %s/%s has no ALB-supported listeners", t.gw.Namespace, t.gw.Name)
	}
	return out, nil
}

// mapListenerProtocol converts a Gateway-API protocol to the LBC enum.
// ALB Phase 1 supports only HTTP and HTTPS at the listener level. TLS
// (passthrough) belongs on the future NLB GatewayClass; TCP/UDP are L4.
func mapListenerProtocol(p gwv1.ProtocolType) (v2.ListenerProtocol, bool) {
	switch p {
	case gwv1.HTTPProtocolType:
		return v2.ListenerProtocolHTTP, true
	case gwv1.HTTPSProtocolType:
		return v2.ListenerProtocolHTTPS, true
	}
	return "", false
}

// applyListenerPolicy merges fields from the matching VKSGatewayPolicy into
// the listener. Per-listener (sectionName-scoped) wins over unscoped policy
// per the design spec; fields not set in either are left at their LBC default.
func (t *defaultGatewayBuildTask) applyListenerPolicy(entry *v1alpha1.Listener, l *gwv1.Listener) {
	scoped := t.listenerPolicies[string(l.Name)]
	policies := []*gwv1alpha1.VKSGatewayPolicy{scoped, t.unscopedPolicy}

	// Timeouts: first non-nil wins (per-listener > unscoped).
	if v := firstNonNilDuration(policies, func(p *gwv1alpha1.VKSGatewayPolicy) *durationLike {
		return wrapDur(p.Spec.TimeoutClient)
	}); v != nil {
		entry.TimeoutClient = ptr32(int32(v.Duration.Seconds()))
	}
	if v := firstNonNilDuration(policies, func(p *gwv1alpha1.VKSGatewayPolicy) *durationLike {
		return wrapDur(p.Spec.TimeoutMember)
	}); v != nil {
		entry.TimeoutMember = ptr32(int32(v.Duration.Seconds()))
	}
	if v := firstNonNilDuration(policies, func(p *gwv1alpha1.VKSGatewayPolicy) *durationLike {
		return wrapDur(p.Spec.TimeoutConnection)
	}); v != nil {
		entry.TimeoutConnection = ptr32(int32(v.Duration.Seconds()))
	}

	// AllowedCIDRs: per-listener overrides unscoped entirely (no merge).
	// vngcloud's API accepts a single comma-joined string.
	if cidrs := firstNonEmptyStringSlice(policies, func(p *gwv1alpha1.VKSGatewayPolicy) []string {
		return p.Spec.AllowedCIDRs
	}); len(cidrs) > 0 {
		s := strings.Join(cidrs, ",")
		entry.AllowedCidrs = &s
	}

	// InsertHeaders: per-listener overrides unscoped entirely (no merge).
	// LBC stores headers as []InsertHeader; map[string]string in the policy
	// goes through a deterministic-by-key flatten so generation is stable.
	// Default — when neither policy supplies headers — is the X-Forwarded-*
	// triplet the Ingress controller emits when the annotation is absent
	// (see ingress_uc/build_listener.go: buildAnnotationInsertHeaders).
	hdrs := firstNonEmptyStringMap(policies, func(p *gwv1alpha1.VKSGatewayPolicy) map[string]string {
		return p.Spec.InsertHeaders
	})
	if len(hdrs) == 0 {
		hdrs = defaultInsertHeaders()
	}
	entry.InsertHeaders = flattenInsertHeaders(hdrs)

	// ClientCertificateId: per-listener wins.
	if id := firstNonNilString(policies, func(p *gwv1alpha1.VKSGatewayPolicy) *string {
		return p.Spec.ClientCertificateID
	}); id != nil {
		entry.ClientCertificateId = id
	}
}

// cloudListenerName produces a deterministic listener name keyed on the
// Gateway's UID + the listener's K8s name. Format: "vks_gw_<uid8>_<lname>".
// Truncated to the cloud's 50-char limit; the underlying ListenerCreate
// regex accepts [a-zA-Z0-9_.-]{5,50} so the underscores are fine.
func (t *defaultGatewayBuildTask) cloudListenerName(l *gwv1.Listener) string {
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

// defaultInsertHeaders mirrors the Ingress controller's no-annotation default
// so a Gateway-managed LB receives the same X-Forwarded-* request headers as
// an Ingress-managed one. Users can override (or empty out) the set by
// supplying VKSGatewayPolicy.InsertHeaders explicitly.
func defaultInsertHeaders() map[string]string {
	return map[string]string{
		"X-Forwarded-For":   "true",
		"X-Forwarded-Proto": "true",
		"X-Forwarded-Port":  "true",
	}
}

// flattenInsertHeaders returns headers in deterministic order so spec-equality
// checks (DeepEqual in build_lbc.go) don't churn on map iteration.
func flattenInsertHeaders(h map[string]string) []v1alpha1.InsertHeader {
	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	// stable order via sort
	sortStrings(keys)
	out := make([]v1alpha1.InsertHeader, 0, len(h))
	for _, k := range keys {
		out = append(out, v1alpha1.InsertHeader{HeaderName: k, HeaderValue: h[k]})
	}
	return out
}
