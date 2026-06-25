package shared

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	vksv1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
)

const (
	IndexHTTPRouteByService            = "spec.rules.backendRefs.name"
	IndexHTTPRouteByParentGateway      = "spec.parentRefs.gateway"
	IndexVKSGatewayPolicyByGateway     = "spec.targetRefs.gateway"
	IndexVKSBackendPolicyByService     = "spec.targetRefs.service"
	IndexVKSHealthCheckPolicyByService = "spec.targetRefs.service"
	IndexVKSRoutePolicyByRoute         = "spec.targetRefs.httproute"
	// L4 route → parent Gateway (Phase 2 NLB).
	IndexTCPRouteByParentGateway = "spec.parentRefs.gateway.tcp"
	IndexUDPRouteByParentGateway = "spec.parentRefs.gateway.udp"
	IndexTCPRouteByService       = "spec.rules.backendRefs.name.tcp"
	IndexUDPRouteByService       = "spec.rules.backendRefs.name.udp"
)

func RegisterIndexes(ctx context.Context, mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx, &gwv1.HTTPRoute{}, IndexHTTPRouteByService, IndexHTTPRouteByServiceFunc); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(ctx, &gwv1.HTTPRoute{}, IndexHTTPRouteByParentGateway, IndexHTTPRouteByParentFunc); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(ctx, &vksv1.VKSGatewayPolicy{}, IndexVKSGatewayPolicyByGateway, IndexVKSGatewayPolicyByGatewayFunc); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(ctx, &vksv1.VKSBackendPolicy{}, IndexVKSBackendPolicyByService, IndexVKSBackendPolicyByServiceFunc); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(ctx, &vksv1.VKSHealthCheckPolicy{}, IndexVKSHealthCheckPolicyByService, IndexVKSHealthCheckPolicyByServiceFunc); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(ctx, &vksv1.VKSRoutePolicy{}, IndexVKSRoutePolicyByRoute, IndexVKSRoutePolicyByRouteFunc); err != nil {
		return err
	}
	return nil
}

// RegisterL4Indexes registers the field indexes the NLB Gateway reconciler
// needs (TCPRoute/UDPRoute → parent Gateway and → backend Service). Kept
// separate from RegisterIndexes so the ALB path doesn't require the
// experimental L4 route CRDs to be installed: this is only called when the NLB
// controller is enabled.
func RegisterL4Indexes(ctx context.Context, mgr manager.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx, &gwv1a2.TCPRoute{}, IndexTCPRouteByParentGateway, IndexTCPRouteByParentFunc); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(ctx, &gwv1a2.UDPRoute{}, IndexUDPRouteByParentGateway, IndexUDPRouteByParentFunc); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(ctx, &gwv1a2.TCPRoute{}, IndexTCPRouteByService, IndexTCPRouteByServiceFunc); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(ctx, &gwv1a2.UDPRoute{}, IndexUDPRouteByService, IndexUDPRouteByServiceFunc); err != nil {
		return err
	}
	return nil
}

func IndexTCPRouteByParentFunc(obj client.Object) []string {
	r := obj.(*gwv1a2.TCPRoute)
	return parentGatewayKeys(r.Namespace, r.Spec.ParentRefs)
}

func IndexUDPRouteByParentFunc(obj client.Object) []string {
	r := obj.(*gwv1a2.UDPRoute)
	return parentGatewayKeys(r.Namespace, r.Spec.ParentRefs)
}

func IndexTCPRouteByServiceFunc(obj client.Object) []string {
	r := obj.(*gwv1a2.TCPRoute)
	var keys []string
	for _, rule := range r.Spec.Rules {
		keys = append(keys, backendServiceKeys(r.Namespace, rule.BackendRefs)...)
	}
	return keys
}

func IndexUDPRouteByServiceFunc(obj client.Object) []string {
	r := obj.(*gwv1a2.UDPRoute)
	var keys []string
	for _, rule := range r.Spec.Rules {
		keys = append(keys, backendServiceKeys(r.Namespace, rule.BackendRefs)...)
	}
	return keys
}

// parentGatewayKeys returns "<ns>/<name>" for every parentRef of kind Gateway.
func parentGatewayKeys(routeNS string, refs []gwv1.ParentReference) []string {
	var keys []string
	for _, p := range refs {
		ns := routeNS
		if p.Namespace != nil {
			ns = string(*p.Namespace)
		}
		if p.Kind == nil || *p.Kind == "Gateway" {
			keys = append(keys, ns+"/"+string(p.Name))
		}
	}
	return keys
}

// backendServiceKeys returns "<ns>/<name>" for every backendRef.
func backendServiceKeys(routeNS string, refs []gwv1.BackendRef) []string {
	keys := make([]string, len(refs))
	for i, br := range refs {
		ns := routeNS
		if br.Namespace != nil {
			ns = string(*br.Namespace)
		}
		keys[i] = ns + "/" + string(br.Name)
	}
	return keys
}

func IndexHTTPRouteByServiceFunc(obj client.Object) []string {
	r := obj.(*gwv1.HTTPRoute)
	keys := make([]string, 0, len(r.Spec.Rules))
	for _, rule := range r.Spec.Rules {
		for _, br := range rule.BackendRefs {
			ns := r.Namespace
			if br.Namespace != nil {
				ns = string(*br.Namespace)
			}
			keys = append(keys, ns+"/"+string(br.Name))
		}
	}
	return keys
}

func IndexHTTPRouteByParentFunc(obj client.Object) []string {
	r := obj.(*gwv1.HTTPRoute)
	var keys []string
	for _, p := range r.Spec.ParentRefs {
		ns := r.Namespace
		if p.Namespace != nil {
			ns = string(*p.Namespace)
		}
		if p.Kind == nil || *p.Kind == "Gateway" {
			keys = append(keys, ns+"/"+string(p.Name))
		}
	}
	return keys
}

func IndexVKSGatewayPolicyByGatewayFunc(obj client.Object) []string {
	p := obj.(*vksv1.VKSGatewayPolicy)
	keys := make([]string, len(p.Spec.TargetRefs))
	for i, t := range p.Spec.TargetRefs {
		keys[i] = p.Namespace + "/" + string(t.Name)
	}
	return keys
}

func IndexVKSBackendPolicyByServiceFunc(obj client.Object) []string {
	p := obj.(*vksv1.VKSBackendPolicy)
	keys := make([]string, len(p.Spec.TargetRefs))
	for i, t := range p.Spec.TargetRefs {
		keys[i] = p.Namespace + "/" + string(t.Name)
	}
	return keys
}

func IndexVKSHealthCheckPolicyByServiceFunc(obj client.Object) []string {
	p := obj.(*vksv1.VKSHealthCheckPolicy)
	keys := make([]string, len(p.Spec.TargetRefs))
	for i, t := range p.Spec.TargetRefs {
		keys[i] = p.Namespace + "/" + string(t.Name)
	}
	return keys
}

func IndexVKSRoutePolicyByRouteFunc(obj client.Object) []string {
	p := obj.(*vksv1.VKSRoutePolicy)
	keys := make([]string, len(p.Spec.TargetRefs))
	for i, t := range p.Spec.TargetRefs {
		keys[i] = p.Namespace + "/" + string(t.Name)
	}
	return keys
}
