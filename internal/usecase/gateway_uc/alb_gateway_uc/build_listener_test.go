package alb_gateway_uc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

func TestBuildListener_HTTP(t *testing.T) {
	l := gwv1.Listener{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80}
	out, err := BuildListener(l, nil, nil)
	assert.NoError(t, err)
	assert.Equal(t, loadbalancerv2.ListenerProtocolHTTP, out.Protocol)
	assert.EqualValues(t, 80, out.ProtocolPort)
	assert.Nil(t, out.CertificateDefault)
}

func TestBuildListener_HTTPS_FromLBCCertID(t *testing.T) {
	l := gwv1.Listener{Name: "https", Protocol: gwv1.HTTPSProtocolType, Port: 443}
	lbcL := &vksv1alpha1.Listener{
		Name:               "https",
		CertificateDefault: &vksv1alpha1.ListenerCertificate{Id: ptr.To("cert-1")},
	}
	out, err := BuildListener(l, lbcL, nil)
	assert.NoError(t, err)
	assert.Equal(t, loadbalancerv2.ListenerProtocolHTTPS, out.Protocol)
	assert.NotNil(t, out.CertificateDefault)
	assert.Equal(t, "cert-1", *out.CertificateDefault.Id)
}

func TestBuildListener_HTTPS_FromSecretFallback(t *testing.T) {
	l := gwv1.Listener{
		Name: "https", Protocol: gwv1.HTTPSProtocolType, Port: 443,
		TLS: &gwv1.GatewayTLSConfig{
			CertificateRefs: []gwv1.SecretObjectReference{{Name: gwv1.ObjectName("my-tls")}},
		},
	}
	src := func(ref gwv1.SecretObjectReference) (*vksv1alpha1.ListenerCertificate, error) {
		return &vksv1alpha1.ListenerCertificate{SecretName: ptr.To(string(ref.Name))}, nil
	}
	out, err := BuildListener(l, nil, src)
	assert.NoError(t, err)
	assert.NotNil(t, out.CertificateDefault)
	assert.Equal(t, "my-tls", *out.CertificateDefault.SecretName)
}

func TestBuildListener_LBCMismatchedProtocol_Errors(t *testing.T) {
	l := gwv1.Listener{Name: "https", Protocol: gwv1.HTTPSProtocolType, Port: 443}
	lbcL := &vksv1alpha1.Listener{Name: "https", Protocol: loadbalancerv2.ListenerProtocolHTTP, ProtocolPort: 80}
	_, err := BuildListener(l, lbcL, nil)
	assert.Error(t, err)
}

func TestBuildListener_UnsupportedProtocol_Errors(t *testing.T) {
	l := gwv1.Listener{Name: "x", Protocol: gwv1.TCPProtocolType, Port: 9000}
	_, err := BuildListener(l, nil, nil)
	assert.Error(t, err)
}
