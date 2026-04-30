package shared_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/utils/ptr"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
)

func TestMatchSpecificity_PathTypeOrder(t *testing.T) {
	exact := gwv1.HTTPRouteMatch{Path: &gwv1.HTTPPathMatch{Type: ptr.To(gwv1.PathMatchExact), Value: ptr.To("/a")}}
	pathPrefix := gwv1.HTTPRouteMatch{Path: &gwv1.HTTPPathMatch{Type: ptr.To(gwv1.PathMatchPathPrefix), Value: ptr.To("/a")}}
	assert.Greater(t, shared.MatchSpecificity(exact), shared.MatchSpecificity(pathPrefix))
}

func TestMatchSpecificity_LongerPathBeatsShorter(t *testing.T) {
	long := gwv1.HTTPRouteMatch{Path: &gwv1.HTTPPathMatch{Type: ptr.To(gwv1.PathMatchPathPrefix), Value: ptr.To("/a/b/c")}}
	short := gwv1.HTTPRouteMatch{Path: &gwv1.HTTPPathMatch{Type: ptr.To(gwv1.PathMatchPathPrefix), Value: ptr.To("/a")}}
	assert.Greater(t, shared.MatchSpecificity(long), shared.MatchSpecificity(short))
}

func TestMatchSpecificity_HeaderCount(t *testing.T) {
	one := gwv1.HTTPRouteMatch{Headers: []gwv1.HTTPHeaderMatch{{Name: "x"}}}
	two := gwv1.HTTPRouteMatch{Headers: []gwv1.HTTPHeaderMatch{{Name: "x"}, {Name: "y"}}}
	assert.Greater(t, shared.MatchSpecificity(two), shared.MatchSpecificity(one))
}
