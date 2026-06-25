package alb_gateway_uc

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	v2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

func makeRoutePolicy(ns, name, routeName string, ruleName string, actionType string, redirectURL string) *gwv1alpha1.VKSRoutePolicy {
	rp := &gwv1alpha1.VKSRoutePolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: gwv1alpha1.VKSRoutePolicySpec{
			TargetRefs: []gwv1alpha2.LocalPolicyTargetReferenceWithSectionName{{
				LocalPolicyTargetReference: gwv1alpha2.LocalPolicyTargetReference{
					Group: "gateway.networking.k8s.io",
					Kind:  "HTTPRoute",
					Name:  gwv1alpha2.ObjectName(routeName),
				},
			}},
		},
	}
	if ruleName != "" {
		sn := gwv1alpha2.SectionName(ruleName)
		rp.Spec.TargetRefs[0].SectionName = &sn
	}
	if actionType != "" {
		action := gwv1alpha1.VKSRuleAction{Type: actionType}
		if actionType == "Redirect" {
			action.Redirect = &gwv1alpha1.VKSRedirectAction{URL: redirectURL}
		}
		rp.Spec.Actions = []gwv1alpha1.VKSRuleAction{action}
	}
	return rp
}

func TestApplyOneRouteOverlay_Reject(t *testing.T) {
	p := &v1alpha1.Policy{
		Action:           v2.PolicyActionREDIRECTTOPOOL,
		RedirectPoolName: ptr.To("pool-1"),
	}
	rp := &gwv1alpha1.VKSRoutePolicy{
		Spec: gwv1alpha1.VKSRoutePolicySpec{
			Actions: []gwv1alpha1.VKSRuleAction{{Type: "Reject"}},
		},
	}
	applyOneRouteOverlay(p, rp)
	assert.Equal(t, v2.PolicyActionREJECT, p.Action)
	assert.Nil(t, p.RedirectPoolName)
	assert.Nil(t, p.RedirectUrl)
}

func TestApplyOneRouteOverlay_Redirect(t *testing.T) {
	p := &v1alpha1.Policy{
		Action:           v2.PolicyActionREDIRECTTOPOOL,
		RedirectPoolName: ptr.To("pool-1"),
	}
	code := int32(301)
	keepQS := true
	rp := &gwv1alpha1.VKSRoutePolicy{
		Spec: gwv1alpha1.VKSRoutePolicySpec{
			Actions: []gwv1alpha1.VKSRuleAction{{
				Type: "Redirect",
				Redirect: &gwv1alpha1.VKSRedirectAction{
					URL:             "https://new.example.com",
					HTTPCode:        &code,
					KeepQueryString: &keepQS,
				},
			}},
		},
	}
	applyOneRouteOverlay(p, rp)
	assert.Equal(t, v2.PolicyActionREDIRECTTOURL, p.Action)
	assert.Nil(t, p.RedirectPoolName)
	assert.Equal(t, "https://new.example.com", *p.RedirectUrl)
	assert.Equal(t, int32(301), *p.RedirectHttpCode)
	assert.True(t, *p.KeepQueryString)
}

func TestApplyOneRouteOverlay_RedirectNilAction(t *testing.T) {
	p := &v1alpha1.Policy{Action: v2.PolicyActionREDIRECTTOPOOL}
	rp := &gwv1alpha1.VKSRoutePolicy{
		Spec: gwv1alpha1.VKSRoutePolicySpec{
			Actions: []gwv1alpha1.VKSRuleAction{{Type: "Redirect", Redirect: nil}},
		},
	}
	applyOneRouteOverlay(p, rp)
	// nil Redirect → no change
	assert.Equal(t, v2.PolicyActionREDIRECTTOPOOL, p.Action)
}

func TestApplyOneRouteOverlay_Position(t *testing.T) {
	p := &v1alpha1.Policy{}
	pos := int32(5)
	rp := &gwv1alpha1.VKSRoutePolicy{
		Spec: gwv1alpha1.VKSRoutePolicySpec{
			Position: &pos,
		},
	}
	applyOneRouteOverlay(p, rp)
	assert.NotNil(t, p.Position)
	assert.Equal(t, int32(5), *p.Position)
}

func TestApplyOneRouteOverlay_NoActions(t *testing.T) {
	p := &v1alpha1.Policy{Action: v2.PolicyActionREDIRECTTOPOOL}
	rp := &gwv1alpha1.VKSRoutePolicy{Spec: gwv1alpha1.VKSRoutePolicySpec{}}
	applyOneRouteOverlay(p, rp)
	// No actions → no-op on action
	assert.Equal(t, v2.PolicyActionREDIRECTTOPOOL, p.Action)
}

func TestApplyRoutePolicyToPolicies_NoPolicies(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTestTask(t, gw)

	route := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "rt"},
	}
	in := []v1alpha1.Policy{{Name: "p1", Action: v2.PolicyActionREDIRECTTOPOOL}}
	out, err := task.applyRoutePolicyToPolicies(context.Background(), in, route, "")
	assert.NoError(t, err)
	assert.Equal(t, in, out)
}

func TestApplyRoutePolicyToPolicies_WithRouteScopedPolicy(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}

	rp := makeRoutePolicy("prod", "rp1", "my-route", "", "Reject", "")
	s := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(rp).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient}
	task := &defaultGatewayBuildTask{uc: uc, gw: gw, logger: logrus.NewEntry(logrus.New()), listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy), nameHelper: utils.NewNameHelper("c1", "gateway", gw.Namespace, gw.Name)}

	route := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "my-route"},
	}
	in := []v1alpha1.Policy{{Name: "p1", Action: v2.PolicyActionREDIRECTTOPOOL, RedirectPoolName: ptr.To("pool-1")}}
	out, err := task.applyRoutePolicyToPolicies(context.Background(), in, route, "")
	assert.NoError(t, err)
	assert.Len(t, out, 1)
	assert.Equal(t, v2.PolicyActionREJECT, out[0].Action)
}

func TestApplyRoutePolicyToPolicies_RuleScopedWins(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}

	// Route-scoped: Reject
	rpRoute := makeRoutePolicy("prod", "rp-route", "my-route", "", "Reject", "")
	// Rule-scoped: Redirect (should win)
	rpRule := makeRoutePolicy("prod", "rp-rule", "my-route", "my-rule", "Redirect", "https://winner.com")

	s := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(rpRoute, rpRule).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient}
	task := &defaultGatewayBuildTask{uc: uc, gw: gw, logger: logrus.NewEntry(logrus.New()), listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy), nameHelper: utils.NewNameHelper("c1", "gateway", gw.Namespace, gw.Name)}

	route := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "my-route"},
	}
	in := []v1alpha1.Policy{{Name: "p1", Action: v2.PolicyActionREDIRECTTOPOOL, RedirectPoolName: ptr.To("pool-1")}}
	out, err := task.applyRoutePolicyToPolicies(context.Background(), in, route, "my-rule")
	assert.NoError(t, err)
	assert.Len(t, out, 1)
	// Last overlay (rule-scoped Redirect) wins
	assert.Equal(t, v2.PolicyActionREDIRECTTOURL, out[0].Action)
	assert.Equal(t, "https://winner.com", *out[0].RedirectUrl)
}
