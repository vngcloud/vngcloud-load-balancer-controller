package policies

import (
	"sigs.k8s.io/controller-runtime/pkg/client"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
)

// AllReconcilers builds the validator for each of the four VKS policy CRDs.
// Registered together whenever a Gateway controller is enabled.
func AllReconcilers(c client.Client) []Setupable {
	return []Setupable{
		newGatewayPolicyValidator(c),
		newBackendPolicyValidator(c),
		newHealthCheckPolicyValidator(c),
		newRoutePolicyValidator(c),
	}
}

func withSectionTargets(refs []gwv1alpha1.LocalPolicyTargetReferenceWithSectionName) []targetRef {
	out := make([]targetRef, 0, len(refs))
	for _, ref := range refs {
		tr := targetRef{Group: string(ref.Group), Kind: string(ref.Kind), Name: string(ref.Name)}
		if ref.SectionName != nil {
			tr.SectionName = string(*ref.SectionName)
		}
		out = append(out, tr)
	}
	return out
}

func plainTargets(refs []gwv1alpha1.LocalPolicyTargetReference) []targetRef {
	out := make([]targetRef, 0, len(refs))
	for _, ref := range refs {
		out = append(out, targetRef{Group: string(ref.Group), Kind: string(ref.Kind), Name: string(ref.Name)})
	}
	return out
}

func newGatewayPolicyValidator(c client.Client) *Reconciler[*gwv1alpha1.VKSGatewayPolicy] {
	return &Reconciler[*gwv1alpha1.VKSGatewayPolicy]{client: c, a: adapter[*gwv1alpha1.VKSGatewayPolicy]{
		name:    "vksgatewaypolicy-validator",
		newObj:  func() *gwv1alpha1.VKSGatewayPolicy { return &gwv1alpha1.VKSGatewayPolicy{} },
		newList: func() client.ObjectList { return &gwv1alpha1.VKSGatewayPolicyList{} },
		items: func(l client.ObjectList) []*gwv1alpha1.VKSGatewayPolicy {
			pl := l.(*gwv1alpha1.VKSGatewayPolicyList)
			out := make([]*gwv1alpha1.VKSGatewayPolicy, 0, len(pl.Items))
			for i := range pl.Items {
				out = append(out, &pl.Items[i])
			}
			return out
		},
		targetsOf: func(p *gwv1alpha1.VKSGatewayPolicy) []targetRef { return withSectionTargets(p.Spec.TargetRefs) },
	}}
}

func newRoutePolicyValidator(c client.Client) *Reconciler[*gwv1alpha1.VKSRoutePolicy] {
	return &Reconciler[*gwv1alpha1.VKSRoutePolicy]{client: c, a: adapter[*gwv1alpha1.VKSRoutePolicy]{
		name:    "vksroutepolicy-validator",
		newObj:  func() *gwv1alpha1.VKSRoutePolicy { return &gwv1alpha1.VKSRoutePolicy{} },
		newList: func() client.ObjectList { return &gwv1alpha1.VKSRoutePolicyList{} },
		items: func(l client.ObjectList) []*gwv1alpha1.VKSRoutePolicy {
			pl := l.(*gwv1alpha1.VKSRoutePolicyList)
			out := make([]*gwv1alpha1.VKSRoutePolicy, 0, len(pl.Items))
			for i := range pl.Items {
				out = append(out, &pl.Items[i])
			}
			return out
		},
		targetsOf: func(p *gwv1alpha1.VKSRoutePolicy) []targetRef { return withSectionTargets(p.Spec.TargetRefs) },
	}}
}

func newBackendPolicyValidator(c client.Client) *Reconciler[*gwv1alpha1.VKSBackendPolicy] {
	return &Reconciler[*gwv1alpha1.VKSBackendPolicy]{client: c, a: adapter[*gwv1alpha1.VKSBackendPolicy]{
		name:    "vksbackendpolicy-validator",
		newObj:  func() *gwv1alpha1.VKSBackendPolicy { return &gwv1alpha1.VKSBackendPolicy{} },
		newList: func() client.ObjectList { return &gwv1alpha1.VKSBackendPolicyList{} },
		items: func(l client.ObjectList) []*gwv1alpha1.VKSBackendPolicy {
			pl := l.(*gwv1alpha1.VKSBackendPolicyList)
			out := make([]*gwv1alpha1.VKSBackendPolicy, 0, len(pl.Items))
			for i := range pl.Items {
				out = append(out, &pl.Items[i])
			}
			return out
		},
		targetsOf: func(p *gwv1alpha1.VKSBackendPolicy) []targetRef { return plainTargets(p.Spec.TargetRefs) },
	}}
}

func newHealthCheckPolicyValidator(c client.Client) *Reconciler[*gwv1alpha1.VKSHealthCheckPolicy] {
	return &Reconciler[*gwv1alpha1.VKSHealthCheckPolicy]{client: c, a: adapter[*gwv1alpha1.VKSHealthCheckPolicy]{
		name:    "vkshealthcheckpolicy-validator",
		newObj:  func() *gwv1alpha1.VKSHealthCheckPolicy { return &gwv1alpha1.VKSHealthCheckPolicy{} },
		newList: func() client.ObjectList { return &gwv1alpha1.VKSHealthCheckPolicyList{} },
		items: func(l client.ObjectList) []*gwv1alpha1.VKSHealthCheckPolicy {
			pl := l.(*gwv1alpha1.VKSHealthCheckPolicyList)
			out := make([]*gwv1alpha1.VKSHealthCheckPolicy, 0, len(pl.Items))
			for i := range pl.Items {
				out = append(out, &pl.Items[i])
			}
			return out
		},
		targetsOf: func(p *gwv1alpha1.VKSHealthCheckPolicy) []targetRef { return plainTargets(p.Spec.TargetRefs) },
	}}
}
