package shared

import (
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func ProtocolAllowedForALB(p gwv1.ProtocolType) bool {
	switch p {
	case gwv1.HTTPProtocolType, gwv1.HTTPSProtocolType, gwv1.TLSProtocolType:
		return true
	}
	return false
}

func ProtocolAllowedForNLB(p gwv1.ProtocolType) bool {
	switch p {
	case gwv1.TCPProtocolType, gwv1.UDPProtocolType, gwv1.TLSProtocolType:
		return true
	}
	return false
}

func HasMixedProtocols(listeners []gwv1.Listener, gwClass string) bool {
	sawL7, sawL4 := false, false
	for _, l := range listeners {
		switch {
		case ProtocolAllowedForALB(l.Protocol) && l.Protocol != gwv1.TLSProtocolType:
			sawL7 = true
		case ProtocolAllowedForNLB(l.Protocol) && l.Protocol != gwv1.TLSProtocolType:
			sawL4 = true
		}
	}
	return sawL7 && sawL4
}
