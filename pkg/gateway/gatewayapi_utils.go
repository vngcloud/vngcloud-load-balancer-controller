// Package gateway holds Gateway-API-specific utility helpers shared by use-cases and reconcilers.
package gateway

import (
	"regexp"
	"strings"
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
		return "REGEX", `^[^.]+\.` + regexp.QuoteMeta(rest) + `$`
	}
	return "EQUAL_TO", host
}

// PathToL7Rule converts a Gateway-API HTTPPathMatch (type, value) into a vngcloud L7 rule.
// ImplementationSpecific falls back to EQUAL_TO.
func PathToL7Rule(pathType, path string) (compare, value string) {
	switch pathType {
	case "Exact":
		return "EQUAL_TO", path
	case "PathPrefix":
		return "STARTS_WITH", path
	case "RegularExpression":
		return "REGEX", path
	default:
		return "EQUAL_TO", path
	}
}
