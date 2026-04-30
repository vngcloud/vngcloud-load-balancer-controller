package alb_gateway_uc

import (
	"fmt"

	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	"k8s.io/utils/ptr"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
)

// CertSource resolves a Gateway listener's TLS certificateRef to a vngcloud listener
// certificate. It may return (nil, nil) for "no cert produced" (e.g., unsupported group).
type CertSource func(certRef gwv1.SecretObjectReference) (*vksv1alpha1.ListenerCertificate, error)

// BuildListener converts a Gateway listener into a vngcloud listener spec, applying
// LBC overrides matched by name. lbcL may be nil. certs may be nil for HTTP listeners.
//
// Errors when the LBC entry's protocol/port disagrees with the Gateway listener — the
// reconciler surfaces this as Programmed=False, reason=InvalidParameters.
func BuildListener(l gwv1.Listener, lbcL *vksv1alpha1.Listener, certs CertSource) (*vksv1alpha1.Listener, error) {
	proto, err := mapProtocol(l.Protocol)
	if err != nil {
		return nil, err
	}
	if lbcL != nil && lbcL.Protocol != "" && lbcL.Protocol != proto {
		return nil, fmt.Errorf("LBC listener %q protocol %s does not match Gateway listener protocol %s", l.Name, lbcL.Protocol, proto)
	}
	if lbcL != nil && lbcL.ProtocolPort != 0 && lbcL.ProtocolPort != int32(l.Port) {
		return nil, fmt.Errorf("LBC listener %q port %d does not match Gateway listener port %d", l.Name, lbcL.ProtocolPort, l.Port)
	}

	out := &vksv1alpha1.Listener{
		Name:         string(l.Name),
		Protocol:     proto,
		ProtocolPort: int32(l.Port),
	}

	if lbcL != nil {
		out.TimeoutClient = lbcL.TimeoutClient
		out.TimeoutMember = lbcL.TimeoutMember
		out.TimeoutConnection = lbcL.TimeoutConnection
		out.AllowedCidrs = lbcL.AllowedCidrs
		out.InsertHeaders = lbcL.InsertHeaders
		out.SSLPolicy = lbcL.SSLPolicy
		out.ALPNPolicy = lbcL.ALPNPolicy
		out.ClientCertificateId = lbcL.ClientCertificateId
		if lbcL.CertificateDefault != nil {
			out.CertificateDefault = lbcL.CertificateDefault
		}
		if len(lbcL.CertificateAuthorities) > 0 {
			out.CertificateAuthorities = lbcL.CertificateAuthorities
		}
	}

	// Fall back to Secret-imported certs when LBC doesn't supply IDs.
	if proto == loadbalancerv2.ListenerProtocolHTTPS && out.CertificateDefault == nil && l.TLS != nil && certs != nil {
		for i, ref := range l.TLS.CertificateRefs {
			cert, err := certs(ref)
			if err != nil {
				return nil, err
			}
			if cert == nil {
				continue
			}
			if i == 0 {
				out.CertificateDefault = cert
			} else {
				out.CertificateAuthorities = append(out.CertificateAuthorities, *cert)
			}
		}
	}

	out.DefaultPoolName = ptr.To(domain.DEFAULT_NAME_DEFAULT_POOL)
	return out, nil
}

func mapProtocol(p gwv1.ProtocolType) (loadbalancerv2.ListenerProtocol, error) {
	switch p {
	case gwv1.HTTPProtocolType:
		return loadbalancerv2.ListenerProtocolHTTP, nil
	case gwv1.HTTPSProtocolType, gwv1.TLSProtocolType:
		return loadbalancerv2.ListenerProtocolHTTPS, nil
	default:
		return "", fmt.Errorf("protocol %q not supported on ALB GatewayClass", p)
	}
}
