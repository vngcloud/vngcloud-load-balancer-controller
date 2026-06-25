package nlb_gateway_uc

import (
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
)

func ptrInt(v int) *int    { return &v }
func ptr32(v int32) *int32 { return &v }
func listInNamespace(ns string) client.ListOption {
	return client.InNamespace(ns)
}

// memberName prefixes a member name with the project-wide "vks_" marker and
// truncates to the cloud's 50-char limit.
func memberName(base string) string {
	name := domain.VKSResourceNamePrefix + base
	if len(name) > 50 {
		name = name[:50]
	}
	return name
}

func joinExpectedCodes(codes []string) string {
	return strings.Join(codes, ",")
}

func joinCommaStrings(in []string) string {
	return strings.Join(in, ",")
}
