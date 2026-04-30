package alb_gateway_uc

import (
	"fmt"

	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

// CertSourceForGateway returns a CertSource that converts a Gateway certificateRef into
// a placeholder ListenerCertificate identifying a same-namespace TLS Secret. The lbc_uc
// deploy path imports the Secret into a vngcloud certificate before listener apply.
//
// Cross-namespace refs are gated by ReferenceGrant in the caller, not here.
func (uc *albGatewayUseCase) CertSourceForGateway(_ string) CertSource {
	return func(ref gwv1.SecretObjectReference) (*vksv1alpha1.ListenerCertificate, error) {
		if ref.Group != nil && *ref.Group != "" {
			return nil, fmt.Errorf("unsupported certificate ref group %q", *ref.Group)
		}
		if ref.Kind != nil && *ref.Kind != "Secret" {
			return nil, fmt.Errorf("unsupported certificate ref kind %q", *ref.Kind)
		}
		return &vksv1alpha1.ListenerCertificate{SecretName: ptr.To(string(ref.Name))}, nil
	}
}
