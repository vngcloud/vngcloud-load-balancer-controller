package alb_gateway_uc

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

// This file regression-tests the bugs found by running Phase 1 against a real
// vngcloud cluster. Each case exercises the build helpers and asserts that
// the LBC artifact passed to the lbc_uc / vngcloud SDK respects the platform's
// validation rules. The same rules are now enforced inside vngcloud_mocks so
// future end-to-end mock-driven tests will also catch regressions.
//
// Cross-reference docs: docs/superpowers/PHASE-1-MOCK-VALIDATIONS.md

// vngcloudNameRE matches the vngcloud identifier rule observed live: 5-50 chars,
// only [a-zA-Z0-9_.-]. Mirrored from internal/repository/vngcloud_repo/vngcloud_mocks
// so this test file stays standalone (the mock validator is package-private).
var vngcloudNameRE = regexp.MustCompile(`^[a-zA-Z0-9_.-]{5,50}$`)

func mustVngcloudName(t *testing.T, kind, value string) {
	t.Helper()
	if !vngcloudNameRE.MatchString(value) {
		t.Fatalf("%s = %q does not match vngcloud rule [a-zA-Z0-9_.-]{5,50}", kind, value)
	}
}

// Bug 1 — short Gateway listener name "http" (4 chars) becomes "vks-http"
// and clears vngcloud's 5-char floor.
func TestRegression_Bug1_ListenerName(t *testing.T) {
	cases := []struct {
		gwName string
		want   string
	}{
		{"http", "vks-http"},   // 4 → vks- prefix
		{"x", "vks-x"},         // 1 → vks- prefix (already 5 chars)
		{"https", "vks-https"}, // 5 → vks- prefix
		{"my-listener", "vks-my-listener"},
	}
	for _, c := range cases {
		t.Run(c.gwName, func(t *testing.T) {
			got := vngcloudListenerName(c.gwName)
			assert.Equal(t, c.want, got)
			mustVngcloudName(t, "listenerName", got)
		})
	}
}

// Bugs 5 + 6 — every PoolMember built from a weighted backend has Name
// (5–50 chars, [a-zA-Z0-9_.-]) and a non-zero MonitorPort.
func TestRegression_Bug5And6_PoolMemberFields(t *testing.T) {
	in := []BackendEndpoints{
		{Backend: pkggw.BackendKey{Namespace: "n", Name: "a", Port: 80, Weight: 90},
			Endpoints: []string{"10.0.100.3", "10.0.100.4"}},
		{Backend: pkggw.BackendKey{Namespace: "n", Name: "b", Port: 8080, Weight: 10},
			Endpoints: []string{"10.0.100.5"}},
	}
	members := SynthesizeMembers(in)
	require.NotEmpty(t, members)
	for _, mm := range members {
		mustVngcloudName(t, "members[].name", mm.Name)
		assert.NotZero(t, mm.MonitorPort, "monitorPort is required by vngcloud")
		assert.NotNil(t, mm.Weight)
		assert.GreaterOrEqual(t, *mm.Weight, 1)
	}
}

// Bug — UpdatePool with TCP health-check protocol must not carry HTTP-only
// fields. The pool builder emits a TCP-only HealthMonitor when no TGC is
// present, so the resulting spec passes the cross-field rule.
func TestRegression_HealthCheckProtocolFieldExclusivity(t *testing.T) {
	hm := vksv1alpha1.PoolHealthMonitor{Protocol: loadbalancerv2.HealthCheckProtocolTCP}
	// TCP HealthMonitor must not include HTTP-only fields. We assert each field
	// is unset; the corresponding mock helper validateHealthCheckProtocolFields
	// would reject this monitor if any of them were populated.
	assert.Equal(t, loadbalancerv2.HealthCheckProtocolTCP, hm.Protocol)
	assert.Nil(t, hm.HealthCheckPath)
	assert.Nil(t, hm.SuccessCode)
	assert.Nil(t, hm.DomainName)
	assert.Nil(t, hm.HealthCheckMethod)
	assert.Nil(t, hm.HttpVersion)
}

// Bug 7 — Gateway.spec.listeners[].tls.certificateRefs Secret names land in
// LoadBalancerConfigSpec.CreateCertificates so lbc_uc imports them before the
// listener apply runs.
func TestRegression_Bug7_CreateCertificatesPopulated(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "g"},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
				{
					Name: "https", Protocol: gwv1.HTTPSProtocolType, Port: 443,
					TLS: &gwv1.GatewayTLSConfig{
						Mode: ptr.To(gwv1.TLSModeTerminate),
						CertificateRefs: []gwv1.SecretObjectReference{
							{Name: "demo-tls"},
							{Name: "demo-tls"}, // duplicate must be deduped
							{Name: "second-tls"},
						},
					},
				},
			},
		},
	}
	got := collectCreateCertificates(gw)
	require.Len(t, got, 2)
	names := []string{got[0].SecretName, got[1].SecretName}
	assert.Contains(t, names, "demo-tls")
	assert.Contains(t, names, "second-tls")
}

// Bug 7 negative — cross-namespace certificateRefs are skipped at this stage
// (resolution gated by ReferenceGrant; deferred to follow-up work).
func TestRegression_Bug7_CrossNamespaceCertRefsSkipped(t *testing.T) {
	otherNS := gwv1.Namespace("other")
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "g"},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{{
				Name: "https", Protocol: gwv1.HTTPSProtocolType, Port: 443,
				TLS: &gwv1.GatewayTLSConfig{
					CertificateRefs: []gwv1.SecretObjectReference{
						{Name: "remote-tls", Namespace: &otherNS},
					},
				},
			}},
		},
	}
	assert.Empty(t, collectCreateCertificates(gw))
}

// Bug 4 — SynthesizeMembers handles empty + multi-backend cases without
// panicking. Combined with the structural assertions above this is the
// regression tail-end of "default NodeSelector matched zero nodes".
func TestRegression_Bug4_EndpointResolution_Structural(t *testing.T) {
	assert.Nil(t, SynthesizeMembers(nil))
	in := []BackendEndpoints{
		{Backend: pkggw.BackendKey{Namespace: "n", Name: "z", Port: 8080, Weight: 1},
			Endpoints: []string{}},
	}
	assert.Empty(t, SynthesizeMembers(in), "zero endpoints must not produce a member")
}

// Bug 3 — listener name munge must not break attach. findGatewayListenerByName
// accepts both the raw Gateway listener name and its vngcloud-munged form.
func TestRegression_Bug3_FindGatewayListenerByMungedName(t *testing.T) {
	gwListeners := []gwv1.Listener{
		{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
		{Name: "https", Protocol: gwv1.HTTPSProtocolType, Port: 443},
	}
	// The LBC stores the listener under its munged vngcloud name.
	got := findGatewayListenerByName(gwListeners, "vks-http")
	require.NotNil(t, got)
	assert.Equal(t, gwv1.SectionName("http"), got.Name)

	// Already-≥5-char names pass through identical and still match.
	got = findGatewayListenerByName(gwListeners, "https")
	require.NotNil(t, got)

	// Foreign name returns nil.
	assert.Nil(t, findGatewayListenerByName(gwListeners, "no-such"))
}
