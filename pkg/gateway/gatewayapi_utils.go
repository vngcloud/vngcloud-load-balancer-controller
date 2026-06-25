package gateway

import (
	"regexp"
	"strings"
)

// HostnameToRegex converts a Gateway-API hostname (literal or `*.suffix`) to an
// anchored regex string. Returns isLiteral=true when no wildcard is present.
// Empty input ("") is treated as literal empty (matches anything at the listener layer).
func HostnameToRegex(h string) (string, bool) {
	if h == "" {
		return "", true
	}
	if strings.HasPrefix(h, "*.") {
		suffix := regexp.QuoteMeta(h[2:])
		return "^[^.]+\\." + suffix + "$", false
	}
	return "^" + regexp.QuoteMeta(h) + "$", true
}

// HostnameMatches returns true if a route hostname matches a listener hostname per
// Gateway-API rules. Empty listener hostname matches anything.
func HostnameMatches(listener, route string) bool {
	if listener == "" {
		return true
	}
	re, _ := HostnameToRegex(listener)
	matched, _ := regexp.MatchString(re, route)
	return matched
}
