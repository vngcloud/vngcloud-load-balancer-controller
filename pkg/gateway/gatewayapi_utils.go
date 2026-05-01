// Package gateway holds Gateway-API-specific utility helpers shared by use-cases and reconcilers.
package gateway

import (
	"regexp"
	"strings"
)

// vngcloud L7 PolicyCompareType values used by the Gateway-API → vngcloud
// rule mapping. Re-declared here as untyped string constants so this package
// stays free of a hard dependency on the SDK enum types — the loadbalancerv2
// PolicyCompareType is itself a string newtype with these literal values.
const (
	policyCmpEqual  = "EQUAL_TO"
	policyCmpStarts = "STARTS_WITH"
	policyCmpRegex  = "REGEX"
)

// HostnameToL7Rule converts a Gateway-API hostname into a vngcloud L7 rule (compare, value).
// Empty host => empty pair (caller must skip the host rule).
// "*.foo.com" => REGEX matching exactly one DNS label before ".foo.com".
func HostnameToL7Rule(host string) (compare, value string) {
	if host == "" {
		return "", ""
	}
	if strings.HasPrefix(host, "*.") {
		rest := strings.TrimPrefix(host, "*.")
		return policyCmpRegex, `^[^.]+\.` + regexp.QuoteMeta(rest) + `$`
	}
	return policyCmpEqual, host
}

// PathToL7Rule converts a Gateway-API HTTPPathMatch (type, value) into a vngcloud L7 rule.
// ImplementationSpecific falls back to EQUAL_TO.
func PathToL7Rule(pathType, path string) (compare, value string) {
	switch pathType {
	case "Exact":
		return policyCmpEqual, path
	case "PathPrefix":
		return policyCmpStarts, path
	case "RegularExpression":
		return policyCmpRegex, path
	default:
		return policyCmpEqual, path
	}
}
