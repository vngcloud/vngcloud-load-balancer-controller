package gateway_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

func TestHostnameToL7Rule(t *testing.T) {
	cases := []struct {
		name    string
		host    string
		wantCmp string
		wantVal string
	}{
		{"literal", "api.example.com", "EQUAL_TO", "api.example.com"},
		{"prefix wildcard", "*.example.com", "REGEX", `^[^.]+\.example\.com$`},
		{"empty", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmp, val := gateway.HostnameToL7Rule(c.host)
			assert.Equal(t, c.wantCmp, cmp)
			assert.Equal(t, c.wantVal, val)
		})
	}
}

func TestPathToL7Rule(t *testing.T) {
	cases := []struct {
		name     string
		pathType string
		path     string
		wantCmp  string
		wantVal  string
	}{
		{"exact", "Exact", "/foo", "EQUAL_TO", "/foo"},
		{"prefix", "PathPrefix", "/foo", "STARTS_WITH", "/foo"},
		{"regex", "RegularExpression", "^/foo/[0-9]+$", "REGEX", "^/foo/[0-9]+$"},
		{"impl-specific defaults to exact", "ImplementationSpecific", "/foo", "EQUAL_TO", "/foo"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmp, val := gateway.PathToL7Rule(c.pathType, c.path)
			assert.Equal(t, c.wantCmp, cmp)
			assert.Equal(t, c.wantVal, val)
		})
	}
}
