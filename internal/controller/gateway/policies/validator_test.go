package policies

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
)

func policyScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = gwv1.Install(s)
	_ = gwv1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	return s
}

func newFakeClient(scheme *runtime.Scheme, objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objs...).
		WithStatusSubresource(&gwv1alpha1.VKSGatewayPolicy{}, &gwv1alpha1.VKSBackendPolicy{}).
		Build()
}

// nolint:unparam
func gwPolicyTargeting(name, ns, gwName string, created time.Time) *gwv1alpha1.VKSGatewayPolicy {
	return &gwv1alpha1.VKSGatewayPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, CreationTimestamp: metav1.NewTime(created)},
		Spec: gwv1alpha1.VKSGatewayPolicySpec{
			TargetRefs: []gwv1alpha1.LocalPolicyTargetReferenceWithSectionName{{
				LocalPolicyTargetReference: gwv1alpha2.LocalPolicyTargetReference{
					Group: "gateway.networking.k8s.io", Kind: "Gateway", Name: gwv1alpha2.ObjectName(gwName),
				},
			}},
		},
	}
}

func acceptedCondOf(p *gwv1alpha1.VKSGatewayPolicy) *metav1.Condition {
	for i := range p.Status.Conditions {
		if p.Status.Conditions[i].Type == gwv1alpha1.PolicyConditionAccepted {
			return &p.Status.Conditions[i]
		}
	}
	return nil
}

// nolint:unparam
func reconcileGwPolicy(t *testing.T, c client.Client, name, ns string) *gwv1alpha1.VKSGatewayPolicy {
	t.Helper()
	r := newGatewayPolicyValidator(c)
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: ns, Name: name}})
	assert.NoError(t, err)
	got := &gwv1alpha1.VKSGatewayPolicy{}
	assert.NoError(t, c.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, got))
	return got
}

func TestValidator_AcceptedWhenTargetExists(t *testing.T) {
	s := policyScheme()
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	p := gwPolicyTargeting("only", "prod", "gw", time.Unix(100, 0))
	c := newFakeClient(s, gw, p)

	got := reconcileGwPolicy(t, c, "only", "prod")
	cond := acceptedCondOf(got)
	assert.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionTrue, cond.Status)
	assert.Equal(t, gwv1alpha1.PolicyReasonAccepted, cond.Reason)
	assert.Len(t, got.Status.Ancestors, 1)
	assert.Equal(t, albController, got.Status.Ancestors[0].ControllerName)
}

func TestValidator_TargetNotFound(t *testing.T) {
	s := policyScheme()
	p := gwPolicyTargeting("orphan", "prod", "ghost-gw", time.Unix(100, 0))
	c := newFakeClient(s, p) // no Gateway

	got := reconcileGwPolicy(t, c, "orphan", "prod")
	cond := acceptedCondOf(got)
	assert.NotNil(t, cond)
	assert.Equal(t, metav1.ConditionFalse, cond.Status)
	assert.Equal(t, gwv1alpha1.PolicyReasonTargetNotFound, cond.Reason)
}

func TestValidator_ConflictOldestWins(t *testing.T) {
	s := policyScheme()
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	older := gwPolicyTargeting("older", "prod", "gw", time.Unix(100, 0))
	newer := gwPolicyTargeting("newer", "prod", "gw", time.Unix(200, 0))
	c := newFakeClient(s, gw, older, newer)

	gotOlder := reconcileGwPolicy(t, c, "older", "prod")
	assert.Equal(t, metav1.ConditionTrue, acceptedCondOf(gotOlder).Status)
	assert.Equal(t, gwv1alpha1.PolicyReasonAccepted, acceptedCondOf(gotOlder).Reason)

	gotNewer := reconcileGwPolicy(t, c, "newer", "prod")
	assert.Equal(t, metav1.ConditionFalse, acceptedCondOf(gotNewer).Status)
	assert.Equal(t, gwv1alpha1.PolicyReasonConflicted, acceptedCondOf(gotNewer).Reason)
}

func TestValidator_Idempotent(t *testing.T) {
	s := policyScheme()
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	p := gwPolicyTargeting("only", "prod", "gw", time.Unix(100, 0))
	c := newFakeClient(s, gw, p)

	first := reconcileGwPolicy(t, c, "only", "prod")
	rv1 := first.ResourceVersion
	second := reconcileGwPolicy(t, c, "only", "prod")
	// No status change on the second reconcile -> no patch -> resourceVersion stable.
	assert.Equal(t, rv1, second.ResourceVersion, "idempotent reconcile must not re-patch")
}
