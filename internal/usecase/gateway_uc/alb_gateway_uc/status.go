package alb_gateway_uc

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/consts"
)

// writeGatewayStatus persists Gateway.Status from translation outcome plus
// observed LBC.Status. Called as the last step of buildLoadBalancerConfig.
//
// We mirror the "spec/status discipline" of the LBC controller — Gateway is
// only patched when something actually changed, to avoid generation churn
// and reconcile feedback loops.
func (t *defaultGatewayBuildTask) writeGatewayStatus(ctx context.Context, lbc *v1alpha1.LoadBalancerConfig, translateErr error) error {
	cur := t.gw.DeepCopy()

	accepted := metav1.Condition{
		Type:               string(gwv1.GatewayConditionAccepted),
		Status:             metav1.ConditionTrue,
		Reason:             string(gwv1.GatewayReasonAccepted),
		Message:            "Gateway accepted by controller " + consts.GatewayClassControllerNameALB,
		ObservedGeneration: t.gw.Generation,
		LastTransitionTime: metav1.Now(),
	}
	programmed := metav1.Condition{
		Type:               string(gwv1.GatewayConditionProgrammed),
		Status:             metav1.ConditionTrue,
		Reason:             string(gwv1.GatewayReasonProgrammed),
		Message:            "LoadBalancerConfig reconciled successfully",
		ObservedGeneration: t.gw.Generation,
		LastTransitionTime: metav1.Now(),
	}

	switch {
	case translateErr != nil:
		programmed.Status = metav1.ConditionFalse
		programmed.Reason = string(gwv1.GatewayReasonPending)
		programmed.Message = translateErr.Error()
	case lbc == nil:
		programmed.Status = metav1.ConditionFalse
		programmed.Reason = string(gwv1.GatewayReasonPending)
		programmed.Message = "LoadBalancerConfig not yet created"
	case !lbcReady(lbc):
		programmed.Status = metav1.ConditionFalse
		programmed.Reason = string(gwv1.GatewayReasonPending)
		programmed.Message = lbcStatusMessage(lbc)
	}

	shared.SetCondition(&cur.Status.Conditions, accepted)
	shared.SetCondition(&cur.Status.Conditions, programmed)

	cur.Status.Addresses = gatewayAddressesFromLBC(lbc)
	cur.Status.Listeners = listenerStatusesFromGateway(t.gw)

	if conditionsEqual(cur.Status.Conditions, t.gw.Status.Conditions) &&
		gatewayAddressesEqual(cur.Status.Addresses, t.gw.Status.Addresses) &&
		listenerStatusesEqual(cur.Status.Listeners, t.gw.Status.Listeners) {
		return nil
	}
	if err := t.uc.k8sClient.Status().Patch(ctx, cur, client.MergeFrom(t.gw)); err != nil {
		return fmt.Errorf("patch Gateway %s/%s status: %w", t.gw.Namespace, t.gw.Name, err)
	}
	return nil
}

// lbcReady returns true if the LBC's Ready condition is True.
func lbcReady(lbc *v1alpha1.LoadBalancerConfig) bool {
	for _, c := range lbc.Status.Conditions {
		if c.Type == "Ready" {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

func lbcStatusMessage(lbc *v1alpha1.LoadBalancerConfig) string {
	if lbc.Status.LastReconcileMessage != "" {
		return "LBC: " + lbc.Status.LastReconcileMessage
	}
	return "LoadBalancerConfig not yet ready"
}

func gatewayAddressesFromLBC(lbc *v1alpha1.LoadBalancerConfig) []gwv1.GatewayStatusAddress {
	if lbc == nil || lbc.Status.Address == nil || *lbc.Status.Address == "" {
		return nil
	}
	t := gwv1.IPAddressType
	return []gwv1.GatewayStatusAddress{{Type: &t, Value: *lbc.Status.Address}}
}

// listenerStatusesFromGateway emits a baseline ListenerStatus per Spec listener.
// HTTPRoute Accepted/ResolvedRefs counts can be added in a follow-up; this
// minimum viable status keeps the listeners visible to `kubectl describe`.
func listenerStatusesFromGateway(gw *gwv1.Gateway) []gwv1.ListenerStatus {
	out := make([]gwv1.ListenerStatus, 0, len(gw.Spec.Listeners))
	for i := range gw.Spec.Listeners {
		l := &gw.Spec.Listeners[i]
		ls := gwv1.ListenerStatus{
			Name:           l.Name,
			SupportedKinds: supportedKindsForProtocol(l.Protocol),
		}
		shared.SetCondition(&ls.Conditions, metav1.Condition{
			Type:               string(gwv1.ListenerConditionAccepted),
			Status:             listenerAcceptedStatus(l),
			Reason:             listenerAcceptedReason(l),
			ObservedGeneration: gw.Generation,
			LastTransitionTime: metav1.Now(),
		})
		out = append(out, ls)
	}
	return out
}

func supportedKindsForProtocol(p gwv1.ProtocolType) []gwv1.RouteGroupKind {
	switch p {
	case gwv1.HTTPProtocolType, gwv1.HTTPSProtocolType:
		group := gwv1.Group("gateway.networking.k8s.io")
		return []gwv1.RouteGroupKind{{Group: &group, Kind: "HTTPRoute"}}
	}
	return nil
}

func listenerAcceptedStatus(l *gwv1.Listener) metav1.ConditionStatus {
	if _, ok := mapListenerProtocol(l.Protocol); ok {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}

func listenerAcceptedReason(l *gwv1.Listener) string {
	if _, ok := mapListenerProtocol(l.Protocol); ok {
		return string(gwv1.ListenerReasonAccepted)
	}
	return string(gwv1.ListenerReasonUnsupportedProtocol)
}

// conditionsEqual compares condition slices ignoring LastTransitionTime, so
// re-running the reconciler with the same outcome doesn't churn the patch.
func conditionsEqual(a, b []metav1.Condition) bool {
	if len(a) != len(b) {
		return false
	}
	idx := make(map[string]metav1.Condition, len(b))
	for i := range b {
		idx[b[i].Type] = b[i]
	}
	for i := range a {
		other, ok := idx[a[i].Type]
		if !ok {
			return false
		}
		if a[i].Status != other.Status || a[i].Reason != other.Reason ||
			a[i].Message != other.Message || a[i].ObservedGeneration != other.ObservedGeneration {
			return false
		}
	}
	return true
}

func gatewayAddressesEqual(a, b []gwv1.GatewayStatusAddress) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Value != b[i].Value {
			return false
		}
		ta, tb := "", ""
		if a[i].Type != nil {
			ta = string(*a[i].Type)
		}
		if b[i].Type != nil {
			tb = string(*b[i].Type)
		}
		if ta != tb {
			return false
		}
	}
	return true
}

func listenerStatusesEqual(a, b []gwv1.ListenerStatus) bool {
	if len(a) != len(b) {
		return false
	}
	idx := make(map[gwv1.SectionName]gwv1.ListenerStatus, len(b))
	for i := range b {
		idx[b[i].Name] = b[i]
	}
	for i := range a {
		other, ok := idx[a[i].Name]
		if !ok {
			return false
		}
		if !conditionsEqual(a[i].Conditions, other.Conditions) {
			return false
		}
	}
	return true
}

// _unusedCorev1 keeps the import set forward-compatible with future status
// fields that need corev1.ConditionStatus / EndpointAddress shapes.
var _ corev1.ConditionStatus
