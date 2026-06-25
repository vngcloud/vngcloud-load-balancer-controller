package alb_gateway_uc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
)

func policyWithTimeout(clientSec, memberSec, connSec float64) *gwv1alpha1.VKSGatewayPolicy {
	p := &gwv1alpha1.VKSGatewayPolicy{}
	if clientSec >= 0 {
		d := metav1.Duration{Duration: time.Duration(clientSec) * time.Second}
		p.Spec.TimeoutClient = &d
	}
	if memberSec >= 0 {
		d := metav1.Duration{Duration: time.Duration(memberSec) * time.Second}
		p.Spec.TimeoutMember = &d
	}
	if connSec >= 0 {
		d := metav1.Duration{Duration: time.Duration(connSec) * time.Second}
		p.Spec.TimeoutConnection = &d
	}
	return p
}

func TestWrapDur(t *testing.T) {
	t.Run("nil returns nil", func(t *testing.T) {
		assert.Nil(t, wrapDur(nil))
	})
	t.Run("non-nil wraps duration", func(t *testing.T) {
		d := &metav1.Duration{Duration: 5 * time.Second}
		got := wrapDur(d)
		assert.NotNil(t, got)
		assert.Equal(t, 5*time.Second, got.Duration)
	})
}

func TestFirstNonNilDuration(t *testing.T) {
	t.Run("empty slice returns nil", func(t *testing.T) {
		got := firstNonNilDuration(nil, func(p *gwv1alpha1.VKSGatewayPolicy) *durationLike {
			return wrapDur(p.Spec.TimeoutClient)
		})
		assert.Nil(t, got)
	})
	t.Run("all nil policies skipped", func(t *testing.T) {
		got := firstNonNilDuration([]*gwv1alpha1.VKSGatewayPolicy{nil, nil}, func(p *gwv1alpha1.VKSGatewayPolicy) *durationLike {
			return wrapDur(p.Spec.TimeoutClient)
		})
		assert.Nil(t, got)
	})
	t.Run("returns first non-nil value", func(t *testing.T) {
		p1 := policyWithTimeout(10, -1, -1)
		p2 := policyWithTimeout(20, -1, -1)
		got := firstNonNilDuration([]*gwv1alpha1.VKSGatewayPolicy{nil, p1, p2}, func(p *gwv1alpha1.VKSGatewayPolicy) *durationLike {
			return wrapDur(p.Spec.TimeoutClient)
		})
		assert.NotNil(t, got)
		assert.Equal(t, 10*time.Second, got.Duration)
	})
	t.Run("skips policy with nil field, returns next", func(t *testing.T) {
		noTimeout := &gwv1alpha1.VKSGatewayPolicy{} // TimeoutClient is nil
		withTimeout := policyWithTimeout(30, -1, -1)
		got := firstNonNilDuration([]*gwv1alpha1.VKSGatewayPolicy{noTimeout, withTimeout}, func(p *gwv1alpha1.VKSGatewayPolicy) *durationLike {
			return wrapDur(p.Spec.TimeoutClient)
		})
		assert.NotNil(t, got)
		assert.Equal(t, 30*time.Second, got.Duration)
	})
}

func TestFirstNonNilString(t *testing.T) {
	str := func(s string) *string { return &s }

	t.Run("all nil returns nil", func(t *testing.T) {
		p := &gwv1alpha1.VKSGatewayPolicy{}
		got := firstNonNilString([]*gwv1alpha1.VKSGatewayPolicy{nil, p}, func(p *gwv1alpha1.VKSGatewayPolicy) *string {
			return p.Spec.ClientCertificateID
		})
		assert.Nil(t, got)
	})
	t.Run("returns first non-nil", func(t *testing.T) {
		p1 := &gwv1alpha1.VKSGatewayPolicy{}
		s := "cert-1"
		p1.Spec.ClientCertificateID = &s
		p2 := &gwv1alpha1.VKSGatewayPolicy{}
		p2.Spec.ClientCertificateID = str("cert-2")
		got := firstNonNilString([]*gwv1alpha1.VKSGatewayPolicy{nil, p1, p2}, func(p *gwv1alpha1.VKSGatewayPolicy) *string {
			return p.Spec.ClientCertificateID
		})
		assert.Equal(t, "cert-1", *got)
	})
}

func TestFirstNonEmptyStringSlice(t *testing.T) {
	t.Run("all nil/empty returns nil", func(t *testing.T) {
		p := &gwv1alpha1.VKSGatewayPolicy{}
		got := firstNonEmptyStringSlice([]*gwv1alpha1.VKSGatewayPolicy{nil, p}, func(p *gwv1alpha1.VKSGatewayPolicy) []string {
			return p.Spec.AllowedCIDRs
		})
		assert.Nil(t, got)
	})
	t.Run("returns first non-empty slice", func(t *testing.T) {
		empty := &gwv1alpha1.VKSGatewayPolicy{}
		p2 := &gwv1alpha1.VKSGatewayPolicy{}
		p2.Spec.AllowedCIDRs = []string{"10.0.0.0/8", "192.168.0.0/16"}
		p3 := &gwv1alpha1.VKSGatewayPolicy{}
		p3.Spec.AllowedCIDRs = []string{"172.16.0.0/12"}
		got := firstNonEmptyStringSlice([]*gwv1alpha1.VKSGatewayPolicy{nil, empty, p2, p3}, func(p *gwv1alpha1.VKSGatewayPolicy) []string {
			return p.Spec.AllowedCIDRs
		})
		assert.Equal(t, []string{"10.0.0.0/8", "192.168.0.0/16"}, got)
	})
}

func TestFirstNonEmptyStringMap(t *testing.T) {
	t.Run("all nil/empty returns nil", func(t *testing.T) {
		p := &gwv1alpha1.VKSGatewayPolicy{}
		got := firstNonEmptyStringMap([]*gwv1alpha1.VKSGatewayPolicy{nil, p}, func(p *gwv1alpha1.VKSGatewayPolicy) map[string]string {
			return p.Spec.InsertHeaders
		})
		assert.Nil(t, got)
	})
	t.Run("returns first non-empty map", func(t *testing.T) {
		empty := &gwv1alpha1.VKSGatewayPolicy{}
		p2 := &gwv1alpha1.VKSGatewayPolicy{}
		p2.Spec.InsertHeaders = map[string]string{"X-A": "1"}
		p3 := &gwv1alpha1.VKSGatewayPolicy{}
		p3.Spec.InsertHeaders = map[string]string{"X-B": "2"}
		got := firstNonEmptyStringMap([]*gwv1alpha1.VKSGatewayPolicy{nil, empty, p2, p3}, func(p *gwv1alpha1.VKSGatewayPolicy) map[string]string {
			return p.Spec.InsertHeaders
		})
		assert.Equal(t, map[string]string{"X-A": "1"}, got)
	})
}

func TestSortStrings(t *testing.T) {
	s := []string{"c", "a", "b"}
	sortStrings(s)
	assert.Equal(t, []string{"a", "b", "c"}, s)
}

func TestPtr32(t *testing.T) {
	v := ptr32(42)
	assert.Equal(t, int32(42), *v)
}
