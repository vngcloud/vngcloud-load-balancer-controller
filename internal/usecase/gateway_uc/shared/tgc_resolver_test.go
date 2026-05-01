package shared_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	gatewayv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/shared"
)

//nolint:unparam // test helper kept generic for future Service-name variation
func mkTGC(name, target string, def *string, route, rule string, override *string) gatewayv1alpha1.TargetGroupConfig {
	tgc := gatewayv1alpha1.TargetGroupConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns"},
		Spec: gatewayv1alpha1.TargetGroupConfigSpec{
			TargetReference: gatewayv1alpha1.TargetReference{Name: target},
			DefaultConfig:   gatewayv1alpha1.TargetGroupProperties{PoolAlgorithm: def},
		},
	}
	if route != "" {
		rsc := gatewayv1alpha1.RouteSpecificConfig{
			RouteIdentifier: gatewayv1alpha1.RouteIdentifier{Group: "gateway.networking.k8s.io", Kind: "HTTPRoute", Name: route},
			Config:          gatewayv1alpha1.TargetGroupProperties{PoolAlgorithm: override},
		}
		if rule != "" {
			rsc.RouteIdentifier.RuleName = ptr.To(rule)
		}
		tgc.Spec.RouteConfigurations = append(tgc.Spec.RouteConfigurations, rsc)
	}
	return tgc
}

func TestResolveTGC_DefaultOnly(t *testing.T) {
	tgcs := []gatewayv1alpha1.TargetGroupConfig{mkTGC("a", "svc-1", ptr.To("ROUND_ROBIN"), "", "", nil)}
	p, _ := shared.ResolveTargetGroupProps(tgcs, "svc-1", "HTTPRoute", "any-route", nil)
	assert.Equal(t, "ROUND_ROBIN", *p.PoolAlgorithm)
}

func TestResolveTGC_RouteOverride(t *testing.T) {
	tgcs := []gatewayv1alpha1.TargetGroupConfig{mkTGC("a", "svc-1", ptr.To("ROUND_ROBIN"), "route-x", "", ptr.To("LEAST_CONNECTIONS"))}
	p, _ := shared.ResolveTargetGroupProps(tgcs, "svc-1", "HTTPRoute", "route-x", nil)
	assert.Equal(t, "LEAST_CONNECTIONS", *p.PoolAlgorithm)
}

func TestResolveTGC_RuleOverride_Beats_Route(t *testing.T) {
	tgcs := []gatewayv1alpha1.TargetGroupConfig{
		mkTGC("a", "svc-1", ptr.To("RR"), "route-x", "rule-1", ptr.To("RULE_WIN")),
		mkTGC("b", "svc-1", ptr.To("RR"), "route-x", "", ptr.To("ROUTE_WIN")),
	}
	rule := "rule-1"
	p, _ := shared.ResolveTargetGroupProps(tgcs, "svc-1", "HTTPRoute", "route-x", &rule)
	assert.Equal(t, "RULE_WIN", *p.PoolAlgorithm)
}

func TestResolveTGC_NoMatch_ReturnsZero(t *testing.T) {
	p, _ := shared.ResolveTargetGroupProps(nil, "svc-1", "HTTPRoute", "x", nil)
	assert.Equal(t, gatewayv1alpha1.TargetGroupProperties{}, p)
}
