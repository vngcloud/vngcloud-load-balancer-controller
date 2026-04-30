// Package shared holds Gateway-controller helpers shared by ALB and NLB reconcilers.
package shared

import (
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// ListenerInvalidReason names a per-listener rejection cause for the ALB GatewayClass.
type ListenerInvalidReason string

const (
	// ListenerInvalidReasonNone signals the listener passed validation.
	ListenerInvalidReasonNone ListenerInvalidReason = ""

	// ListenerInvalidReasonUnsupportedProtocol marks listeners whose protocol is not
	// allowed on the ALB GatewayClass (TCP/UDP go to NLB).
	ListenerInvalidReasonUnsupportedProtocol ListenerInvalidReason = "UnsupportedProtocol"

	// ListenerInvalidReasonDupPort marks listeners whose port collides with another
	// listener already accepted on the same Gateway.
	ListenerInvalidReasonDupPort ListenerInvalidReason = "DupPort"
)

// ALBAllowsListener reports whether a listener protocol is supported on the ALB
// GatewayClass: HTTP, HTTPS, and TLS (terminate mode).
func ALBAllowsListener(p gwv1.ProtocolType) bool {
	switch p {
	case gwv1.HTTPProtocolType, gwv1.HTTPSProtocolType, gwv1.TLSProtocolType:
		return true
	}
	return false
}

// ValidateListenersForALB returns a per-listener-name validation result. A listener
// with reason ListenerInvalidReasonNone (or absent from the map) is valid.
// First-port-wins on duplicates; later listeners with the same port get DupPort.
func ValidateListenersForALB(listeners []gwv1.Listener) map[string]ListenerInvalidReason {
	out := make(map[string]ListenerInvalidReason, len(listeners))
	seenPort := map[gwv1.PortNumber]bool{}
	for _, l := range listeners {
		if !ALBAllowsListener(l.Protocol) {
			out[string(l.Name)] = ListenerInvalidReasonUnsupportedProtocol
			continue
		}
		if seenPort[l.Port] {
			out[string(l.Name)] = ListenerInvalidReasonDupPort
			continue
		}
		seenPort[l.Port] = true
		out[string(l.Name)] = ListenerInvalidReasonNone
	}
	return out
}
