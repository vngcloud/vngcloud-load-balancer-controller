package alb_gateway_uc

import (
	"sort"

	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

// applyListenerCertificates fills CertificateDefault / CertificateAuthorities
// on an HTTPS listener.
//
// Precedence per §3.3 of the design spec:
//  1. Per-listener VKSGatewayPolicy.CertificateIDs — vngcloud cert IDs win
//     (default = first, authorities = rest, order preserved).
//  2. Unscoped VKSGatewayPolicy.CertificateIDs — same shape.
//  3. Listener.TLS.CertificateRefs[] — Secret names; LBC controller imports
//     them via CreateCertificates. Same-namespace only in Phase 1; cross-ns
//     refs are skipped (status surfaces a warning in Phase F).
func (t *defaultGatewayBuildTask) applyListenerCertificates(entry *v1alpha1.Listener, l *gwv1.Listener) {
	if l.Protocol != gwv1.HTTPSProtocolType || l.TLS == nil {
		return
	}

	if ids := t.resolveListenerCertIDs(l); len(ids) > 0 {
		certs := make([]v1alpha1.ListenerCertificate, 0, len(ids))
		for i := range ids {
			certs = append(certs, v1alpha1.ListenerCertificate{Id: &ids[i]})
		}
		entry.CertificateDefault = &certs[0]
		if len(certs) > 1 {
			entry.CertificateAuthorities = certs[1:]
		}
		return
	}

	secretCerts := make([]v1alpha1.ListenerCertificate, 0, len(l.TLS.CertificateRefs))
	for _, ref := range l.TLS.CertificateRefs {
		if ref.Namespace != nil && string(*ref.Namespace) != t.gw.Namespace {
			// Cross-namespace TLS Secret. The LBC ListenerCertificate type has
			// no namespace field, so we can't pass it through to the import
			// path. Phase F surfaces this on the listener Accepted condition.
			t.logger.Warnf("listener %q references cross-namespace Secret %s/%s; "+
				"skipping — LBC.ListenerCertificate has no namespace field",
				l.Name, *ref.Namespace, ref.Name)
			continue
		}
		if string(ref.Name) == "" {
			continue
		}
		secretCerts = append(secretCerts, v1alpha1.ListenerCertificate{
			SecretName: ptr.To(string(ref.Name)),
		})
	}
	if len(secretCerts) == 0 {
		return
	}
	entry.CertificateDefault = &secretCerts[0]
	if len(secretCerts) > 1 {
		entry.CertificateAuthorities = secretCerts[1:]
	}
}

// resolveListenerCertIDs returns CertificateIDs from per-listener policy if
// set, else from the unscoped policy. Order is preserved (first = default).
func (t *defaultGatewayBuildTask) resolveListenerCertIDs(l *gwv1.Listener) []string {
	if scoped, ok := t.listenerPolicies[string(l.Name)]; ok && scoped != nil {
		if len(scoped.Spec.CertificateIDs) > 0 {
			return scoped.Spec.CertificateIDs
		}
	}
	if t.unscopedPolicy != nil && len(t.unscopedPolicy.Spec.CertificateIDs) > 0 {
		return t.unscopedPolicy.Spec.CertificateIDs
	}
	return nil
}

// buildCreateCertificates collects unique same-namespace Secret names across
// every HTTPS listener so the LBC controller imports each Secret once. Cert-ID
// listeners (where CertificateIDs is set) skip this entirely — those IDs are
// already vngcloud-side.
func (t *defaultGatewayBuildTask) buildCreateCertificates() []v1alpha1.CreateCertificate {
	seen := make(map[string]struct{})
	for i := range t.gw.Spec.Listeners {
		l := &t.gw.Spec.Listeners[i]
		if l.Protocol != gwv1.HTTPSProtocolType || l.TLS == nil {
			continue
		}
		if len(t.resolveListenerCertIDs(l)) > 0 {
			continue // cert-ID path; nothing to import
		}
		for _, ref := range l.TLS.CertificateRefs {
			if ref.Namespace != nil && string(*ref.Namespace) != t.gw.Namespace {
				continue
			}
			if string(ref.Name) == "" {
				continue
			}
			seen[string(ref.Name)] = struct{}{}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]v1alpha1.CreateCertificate, 0, len(names))
	for i := range names {
		out = append(out, v1alpha1.CreateCertificate{SecretName: names[i]})
	}
	return out
}

// _ keeps the package import set stable when individual fields are unused.
var _ = gwv1alpha1.GroupVersion
