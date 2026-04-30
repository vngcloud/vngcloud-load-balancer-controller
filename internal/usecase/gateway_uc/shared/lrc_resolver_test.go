package shared_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	gatewayv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/shared"
)

func TestExtractLRCRefs_FromFilters(t *testing.T) {
	extRef := gwv1.LocalObjectReference{Group: "gateway.vks.vngcloud.vn", Kind: "ListenerRuleConfig", Name: "rule-x"}
	filters := []gwv1.HTTPRouteFilter{{Type: gwv1.HTTPRouteFilterExtensionRef, ExtensionRef: &extRef}}
	out := shared.ExtractLRCRefsFromFilters(filters)
	assert.Equal(t, []string{"rule-x"}, out)
}

func TestExtractLRCRefs_IgnoresOtherExtensionRefs(t *testing.T) {
	extRef := gwv1.LocalObjectReference{Group: "other.example.com", Kind: "Whatever", Name: "ignored"}
	filters := []gwv1.HTTPRouteFilter{{Type: gwv1.HTTPRouteFilterExtensionRef, ExtensionRef: &extRef}}
	assert.Empty(t, shared.ExtractLRCRefsFromFilters(filters))
}

func TestFindLRC(t *testing.T) {
	lrcs := []gatewayv1alpha1.ListenerRuleConfig{
		{ObjectMeta: metav1.ObjectMeta{Name: "rule-x", Namespace: "ns"}},
	}
	out, ok := shared.FindLRC(lrcs, "rule-x")
	assert.True(t, ok)
	assert.Equal(t, "rule-x", out.Name)

	_, ok = shared.FindLRC(lrcs, "missing")
	assert.False(t, ok)
}
