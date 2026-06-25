package shared

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	vksv1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
)

// ---- helpers ----------------------------------------------------------------

func nsPtr(s string) *gwv1alpha2.Namespace { v := gwv1alpha2.Namespace(s); return &v }
func kindPtr(s string) *gwv1alpha2.Kind    { v := gwv1alpha2.Kind(s); return &v }

// ---- IndexHTTPRouteByServiceFunc --------------------------------------------

func TestIndexHTTPRouteByServiceFunc_Basic(t *testing.T) {
	ns := gwv1.Namespace("other-ns")
	r := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "route-ns"},
		Spec: gwv1.HTTPRouteSpec{
			Rules: []gwv1.HTTPRouteRule{
				{
					BackendRefs: []gwv1.HTTPBackendRef{
						{BackendRef: gwv1.BackendRef{BackendObjectReference: gwv1.BackendObjectReference{Name: "svc-a"}}},
						{BackendRef: gwv1.BackendRef{BackendObjectReference: gwv1.BackendObjectReference{Name: "svc-b", Namespace: &ns}}},
					},
				},
			},
		},
	}
	keys := IndexHTTPRouteByServiceFunc(r)
	want := map[string]bool{"route-ns/svc-a": true, "other-ns/svc-b": true}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d: %v", len(keys), keys)
	}
	for _, k := range keys {
		if !want[k] {
			t.Errorf("unexpected key %q", k)
		}
	}
}

func TestIndexHTTPRouteByServiceFunc_NoRules(t *testing.T) {
	r := &gwv1.HTTPRoute{ObjectMeta: metav1.ObjectMeta{Namespace: "ns"}}
	if keys := IndexHTTPRouteByServiceFunc(r); len(keys) != 0 {
		t.Errorf("expected no keys, got %v", keys)
	}
}

// ---- IndexHTTPRouteByParentFunc ---------------------------------------------

func TestIndexHTTPRouteByParentFunc_GatewayKind(t *testing.T) {
	ns := gwv1.Namespace("gw-ns")
	r := &gwv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{Namespace: "route-ns"},
	}
	r.Spec.ParentRefs = []gwv1.ParentReference{
		// nil Kind → defaults to Gateway
		{Name: "gw-a"},
		// explicit Gateway kind with cross-namespace
		{Kind: kindPtr("Gateway"), Name: "gw-b", Namespace: &ns},
		// non-Gateway kind should be excluded
		{Kind: kindPtr("Service"), Name: "svc-x"},
	}
	keys := IndexHTTPRouteByParentFunc(r)
	want := map[string]bool{"route-ns/gw-a": true, "gw-ns/gw-b": true}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d: %v", len(keys), keys)
	}
	for _, k := range keys {
		if !want[k] {
			t.Errorf("unexpected key %q", k)
		}
	}
}

// ---- IndexVKSGatewayPolicyByGatewayFunc -------------------------------------

func TestIndexVKSGatewayPolicyByGatewayFunc(t *testing.T) {
	p := &vksv1.VKSGatewayPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns"},
		Spec: vksv1.VKSGatewayPolicySpec{
			TargetRefs: []vksv1.LocalPolicyTargetReferenceWithSectionName{
				{LocalPolicyTargetReference: vksv1.LocalPolicyTargetReference{Name: "gw-1"}},
				{LocalPolicyTargetReference: vksv1.LocalPolicyTargetReference{Name: "gw-2"}},
			},
		},
	}
	keys := IndexVKSGatewayPolicyByGatewayFunc(p)
	want := map[string]bool{"ns/gw-1": true, "ns/gw-2": true}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d: %v", len(keys), keys)
	}
	for _, k := range keys {
		if !want[k] {
			t.Errorf("unexpected key %q", k)
		}
	}
}

// ---- IndexVKSBackendPolicyByServiceFunc -------------------------------------

func TestIndexVKSBackendPolicyByServiceFunc(t *testing.T) {
	p := &vksv1.VKSBackendPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns"},
		Spec: vksv1.VKSBackendPolicySpec{
			TargetRefs: []vksv1.LocalPolicyTargetReference{
				{Name: "svc-a"},
			},
		},
	}
	keys := IndexVKSBackendPolicyByServiceFunc(p)
	if len(keys) != 1 || keys[0] != "ns/svc-a" {
		t.Errorf("unexpected keys %v", keys)
	}
}

func TestIndexVKSBackendPolicyByServiceFunc_Empty(t *testing.T) {
	p := &vksv1.VKSBackendPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns"},
		Spec:       vksv1.VKSBackendPolicySpec{},
	}
	if keys := IndexVKSBackendPolicyByServiceFunc(p); len(keys) != 0 {
		t.Errorf("expected no keys, got %v", keys)
	}
}

// ---- IndexVKSHealthCheckPolicyByServiceFunc ---------------------------------

func TestIndexVKSHealthCheckPolicyByServiceFunc(t *testing.T) {
	p := &vksv1.VKSHealthCheckPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns"},
		Spec: vksv1.VKSHealthCheckPolicySpec{
			Protocol: "HTTP",
			TargetRefs: []vksv1.LocalPolicyTargetReference{
				{Name: "svc-a"},
				{Name: "svc-b"},
			},
		},
	}
	keys := IndexVKSHealthCheckPolicyByServiceFunc(p)
	want := map[string]bool{"ns/svc-a": true, "ns/svc-b": true}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d: %v", len(keys), keys)
	}
	for _, k := range keys {
		if !want[k] {
			t.Errorf("unexpected key %q", k)
		}
	}
}

// ---- IndexVKSRoutePolicyByRouteFunc -----------------------------------------

func TestIndexVKSRoutePolicyByRouteFunc(t *testing.T) {
	p := &vksv1.VKSRoutePolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns"},
		Spec: vksv1.VKSRoutePolicySpec{
			TargetRefs: []vksv1.LocalPolicyTargetReferenceWithSectionName{
				{LocalPolicyTargetReference: vksv1.LocalPolicyTargetReference{Name: "route-1"}},
			},
		},
	}
	keys := IndexVKSRoutePolicyByRouteFunc(p)
	if len(keys) != 1 || keys[0] != "ns/route-1" {
		t.Errorf("unexpected keys %v", keys)
	}
}

// ---- SetCondition -----------------------------------------------------------

func TestSetCondition_AppendNew(t *testing.T) {
	conds := &[]metav1.Condition{}
	c := metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK", Message: "all good"}
	SetCondition(conds, c)
	if len(*conds) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(*conds))
	}
	if (*conds)[0].Type != "Ready" {
		t.Errorf("wrong type: %s", (*conds)[0].Type)
	}
}

func TestSetCondition_UpdateExisting(t *testing.T) {
	old := metav1.Condition{Type: "Ready", Status: metav1.ConditionFalse, Reason: "NotOK", Message: "broken"}
	conds := &[]metav1.Condition{old}
	updated := metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK", Message: "fixed"}
	SetCondition(conds, updated)
	if len(*conds) != 1 {
		t.Fatalf("expected 1 condition after update, got %d", len(*conds))
	}
	if (*conds)[0].Status != metav1.ConditionTrue {
		t.Errorf("expected True, got %v", (*conds)[0].Status)
	}
}

func TestSetCondition_NoopWhenUnchanged(t *testing.T) {
	c := metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK", Message: "all good"}
	conds := &[]metav1.Condition{c}
	SetCondition(conds, c)
	if len(*conds) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(*conds))
	}
}

func TestSetCondition_MultipleTypes(t *testing.T) {
	conds := &[]metav1.Condition{}
	SetCondition(conds, metav1.Condition{Type: "Ready", Status: metav1.ConditionTrue, Reason: "OK", Message: "ok"})
	SetCondition(conds, metav1.Condition{Type: "Accepted", Status: metav1.ConditionFalse, Reason: "Denied", Message: "nope"})
	if len(*conds) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(*conds))
	}
}

// ---- EnsureAncestor ---------------------------------------------------------

func refWithName(name gwv1alpha2.ObjectName) gwv1alpha2.ParentReference {
	return gwv1alpha2.ParentReference{Name: name}
}

func TestEnsureAncestor_AppendNew(t *testing.T) {
	ancestors := &[]gwv1alpha2.PolicyAncestorStatus{}
	ref := refWithName("gw-a")
	conds := []metav1.Condition{{Type: "Accepted", Status: metav1.ConditionTrue, Reason: "OK", Message: "ok"}}
	EnsureAncestor(ancestors, ref, "test-controller", conds)
	if len(*ancestors) != 1 {
		t.Fatalf("expected 1 ancestor, got %d", len(*ancestors))
	}
	if string((*ancestors)[0].ControllerName) != "test-controller" {
		t.Errorf("wrong controller name: %s", (*ancestors)[0].ControllerName)
	}
}

func TestEnsureAncestor_UpdateExisting(t *testing.T) {
	ref := refWithName("gw-a")
	old := gwv1alpha2.PolicyAncestorStatus{
		AncestorRef:    ref,
		ControllerName: "test-controller",
		Conditions:     []metav1.Condition{{Type: "Accepted", Status: metav1.ConditionFalse}},
	}
	ancestors := &[]gwv1alpha2.PolicyAncestorStatus{old}
	newConds := []metav1.Condition{{Type: "Accepted", Status: metav1.ConditionTrue, Reason: "OK", Message: "ok"}}
	EnsureAncestor(ancestors, ref, "test-controller", newConds)
	if len(*ancestors) != 1 {
		t.Fatalf("expected 1 ancestor after update, got %d", len(*ancestors))
	}
	if (*ancestors)[0].Conditions[0].Status != metav1.ConditionTrue {
		t.Errorf("expected updated status True")
	}
}

func TestEnsureAncestor_DifferentControllers(t *testing.T) {
	ref := refWithName("gw-a")
	a1 := gwv1alpha2.PolicyAncestorStatus{AncestorRef: ref, ControllerName: "ctrl-1"}
	ancestors := &[]gwv1alpha2.PolicyAncestorStatus{a1}
	EnsureAncestor(ancestors, ref, "ctrl-2", nil)
	if len(*ancestors) != 2 {
		t.Fatalf("expected 2 ancestors for different controllers, got %d", len(*ancestors))
	}
}

func TestEnsureAncestor_DifferentRefs(t *testing.T) {
	ref1 := refWithName("gw-a")
	ref2 := refWithName("gw-b")
	a1 := gwv1alpha2.PolicyAncestorStatus{AncestorRef: ref1, ControllerName: "ctrl"}
	ancestors := &[]gwv1alpha2.PolicyAncestorStatus{a1}
	EnsureAncestor(ancestors, ref2, "ctrl", nil)
	if len(*ancestors) != 2 {
		t.Fatalf("expected 2 ancestors for different refs, got %d", len(*ancestors))
	}
}

// ---- parentRefEqual ---------------------------------------------------------

func TestParentRefEqual_AllNilFields(t *testing.T) {
	a := gwv1alpha2.ParentReference{Name: "gw"}
	b := gwv1alpha2.ParentReference{Name: "gw"}
	if !parentRefEqual(a, b) {
		t.Fatal("identical refs should be equal")
	}
}

func TestParentRefEqual_ExplicitNS(t *testing.T) {
	a := gwv1alpha2.ParentReference{Name: "gw", Namespace: nsPtr("ns-a")}
	b := gwv1alpha2.ParentReference{Name: "gw", Namespace: nsPtr("ns-b")}
	if parentRefEqual(a, b) {
		t.Fatal("different namespace should not be equal")
	}
}

func TestParentRefEqual_ExplicitSection(t *testing.T) {
	sec := gwv1alpha2.SectionName("listener-a")
	a := gwv1alpha2.ParentReference{Name: "gw", SectionName: &sec}
	b := gwv1alpha2.ParentReference{Name: "gw"}
	if parentRefEqual(a, b) {
		t.Fatal("one with section vs none should not be equal")
	}
}

func TestParentRefEqual_ExplicitGroupAndKind(t *testing.T) {
	grp := gwv1alpha2.Group("gateway.networking.k8s.io")
	knd := gwv1alpha2.Kind("Gateway")
	a := gwv1alpha2.ParentReference{Name: "gw", Group: &grp, Kind: &knd}
	b := gwv1alpha2.ParentReference{Name: "gw"}
	// b has nil group/kind → derefGroup returns "" which differs from the explicit value
	if parentRefEqual(a, b) {
		t.Fatal("explicit group/kind vs nil should not be equal")
	}
}
