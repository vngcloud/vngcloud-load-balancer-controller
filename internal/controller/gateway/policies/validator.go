// Package policies holds the Direct-policy validator controllers. Each watches
// one VKS policy CRD and writes its GEP-713 status (Accepted / Conflicted /
// TargetNotFound) plus per-target ancestor status. The Gateway use case is the
// sole consumer of the resolved (oldest-wins) policy view; these controllers
// only report status, they never touch the LoadBalancer.
package policies

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	gwshared "github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
	ucshared "github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/shared"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/consts"
	pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
)

//nolint:lll
// +kubebuilder:rbac:groups=gateway.vks.vngcloud.vn,resources=vksgatewaypolicies/status;vksbackendpolicies/status;vkshealthcheckpolicies/status;vksroutepolicies/status,verbs=get;update;patch

var albController = gwv1.GatewayController(consts.GatewayClassControllerNameALB)

// targetRef is the normalized form of a policy's targetRef.
type targetRef struct {
	Group, Kind, Name, SectionName string
}

// validatablePolicy is satisfied by all four VKS policy CRDs (pointer types).
type validatablePolicy interface {
	client.Object
	ucshared.PolicyObj
	GetCommonStatus() *gwv1alpha1.CommonStatus
	GetCommonPolicyStatus() *gwv1alpha1.CommonPolicyStatus
}

// adapter carries the per-CRD specifics the generic validator needs.
type adapter[P validatablePolicy] struct {
	name      string
	newObj    func() P
	newList   func() client.ObjectList
	items     func(client.ObjectList) []P
	targetsOf func(P) []targetRef
}

// Reconciler is a generic Direct-policy validator.
type Reconciler[P validatablePolicy] struct {
	client client.Client
	a      adapter[P]
}

// Setupable is the common interface returned by AllReconcilers.
type Setupable interface {
	SetupWithManager(ctx context.Context, mgr manager.Manager) error
}

func (r *Reconciler[P]) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	p := r.a.newObj()
	if err := r.client.Get(ctx, req.NamespacedName, p); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	list := r.a.newList()
	if err := r.client.List(ctx, list, client.InNamespace(p.GetNamespace())); err != nil {
		return ctrl.Result{}, err
	}
	siblings := r.a.items(list)

	ancestors := make([]gwv1alpha1.PolicyAncestorStatus, 0)
	overall := acceptedCond(p.GetGeneration())
	for _, tr := range r.a.targetsOf(p) {
		cond := r.targetCondition(ctx, p, siblings, tr)
		ancestors = append(ancestors, gwv1alpha1.PolicyAncestorStatus{
			AncestorRef:    ancestorRef(tr),
			ControllerName: albController,
			Conditions:     newConds(cond),
		})
		overall = worseCond(overall, cond)
	}

	cur := p.DeepCopyObject().(P)
	cs := cur.GetCommonStatus()
	gwshared.SetCondition(&cs.Conditions, overall)
	cs.ObservedGeneration = p.GetGeneration()
	cur.GetCommonPolicyStatus().Ancestors = ancestors

	if policyStatusEqual(p, cur) {
		return ctrl.Result{}, nil
	}
	if err := r.client.Status().Patch(ctx, cur, client.MergeFrom(p)); err != nil {
		return ctrl.Result{RequeueAfter: 10 * time.Second}, err
	}
	return ctrl.Result{}, nil
}

// targetCondition computes the Accepted condition for one (policy, targetRef):
// TargetNotFound if the target is missing, Conflicted if an older policy owns
// the same (target, sectionName), else Accepted.
func (r *Reconciler[P]) targetCondition(ctx context.Context, p P, siblings []P, tr targetRef) metav1.Condition {
	gen := p.GetGeneration()
	exists, err := r.targetExists(ctx, p.GetNamespace(), tr)
	if err != nil {
		return policyCond(metav1.ConditionFalse, gwv1alpha1.PolicyReasonTargetNotFound,
			fmt.Sprintf("checking target %s %q: %v", tr.Kind, tr.Name, err), gen)
	}
	if !exists {
		return policyCond(metav1.ConditionFalse, gwv1alpha1.PolicyReasonTargetNotFound,
			fmt.Sprintf("target %s %q not found in namespace %s", tr.Kind, tr.Name, p.GetNamespace()), gen)
	}
	pt := pkggw.PolicyTarget{Group: tr.Group, Kind: tr.Kind, Namespace: p.GetNamespace(), Name: tr.Name, SectionName: tr.SectionName}
	winner, _ := ucshared.ResolveDirectPolicy(siblings, pt)
	if sameObject(winner, p) {
		return policyCond(metav1.ConditionTrue, gwv1alpha1.PolicyReasonAccepted, "Policy accepted", gen)
	}
	return policyCond(metav1.ConditionFalse, gwv1alpha1.PolicyReasonConflicted,
		fmt.Sprintf("Shadowed by older policy %s/%s targeting the same %s", winner.GetNamespace(), winner.GetName(), tr.Kind), gen)
}

func (r *Reconciler[P]) targetExists(ctx context.Context, ns string, tr targetRef) (bool, error) {
	var obj client.Object
	switch tr.Kind {
	case "Gateway":
		obj = &gwv1.Gateway{}
	case "HTTPRoute":
		obj = &gwv1.HTTPRoute{}
	case "Service":
		obj = &corev1.Service{}
	default:
		return false, nil
	}
	if err := r.client.Get(ctx, types.NamespacedName{Namespace: ns, Name: tr.Name}, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// SetupWithManager watches the CRD for spec/create/delete changes (status-only
// updates are filtered by GenerationChangedPredicate, so our own status writes
// don't re-trigger). A second watch re-enqueues all same-kind policies in the
// namespace on any such change, so a conflict re-resolves when a sibling is
// created or the winner is deleted.
func (r *Reconciler[P]) SetupWithManager(_ context.Context, mgr manager.Manager) error {
	enqueueSiblings := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		list := r.a.newList()
		if err := r.client.List(ctx, list, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}
		var reqs []reconcile.Request
		for _, it := range r.a.items(list) {
			reqs = append(reqs, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: it.GetNamespace(), Name: it.GetName()}})
		}
		return reqs
	})
	return ctrl.NewControllerManagedBy(mgr).
		Named(r.a.name).
		For(r.a.newObj(), builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(r.a.newObj(), enqueueSiblings, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

// --- helpers ---

func ancestorRef(tr targetRef) gwv1.ParentReference {
	g := gwv1.Group(tr.Group)
	k := gwv1.Kind(tr.Kind)
	ref := gwv1.ParentReference{Group: &g, Kind: &k, Name: gwv1.ObjectName(tr.Name)}
	if tr.SectionName != "" {
		s := gwv1.SectionName(tr.SectionName)
		ref.SectionName = &s
	}
	return ref
}

func policyCond(status metav1.ConditionStatus, reason, msg string, gen int64) metav1.Condition {
	return metav1.Condition{
		Type:               gwv1alpha1.PolicyConditionAccepted,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: gen,
		LastTransitionTime: metav1.Now(),
	}
}

func acceptedCond(gen int64) metav1.Condition {
	return policyCond(metav1.ConditionTrue, gwv1alpha1.PolicyReasonAccepted, "Policy accepted", gen)
}

func newConds(c metav1.Condition) []metav1.Condition {
	var out []metav1.Condition
	gwshared.SetCondition(&out, c)
	return out
}

func condRank(reason string) int {
	switch reason {
	case gwv1alpha1.PolicyReasonTargetNotFound:
		return 3
	case gwv1alpha1.PolicyReasonConflicted:
		return 2
	case gwv1alpha1.PolicyReasonInvalid:
		return 1
	default:
		return 0
	}
}

func worseCond(a, b metav1.Condition) metav1.Condition {
	if condRank(b.Reason) > condRank(a.Reason) {
		return b
	}
	return a
}

func sameObject[P validatablePolicy](a, b P) bool {
	return a.GetName() == b.GetName() && a.GetNamespace() == b.GetNamespace()
}

func policyStatusEqual[P validatablePolicy](a, b P) bool {
	as, bs := a.GetCommonStatus(), b.GetCommonStatus()
	if as.ObservedGeneration != bs.ObservedGeneration {
		return false
	}
	if !conditionsEqualIgnoreTime(as.Conditions, bs.Conditions) {
		return false
	}
	return ancestorsEqual(a.GetCommonPolicyStatus().Ancestors, b.GetCommonPolicyStatus().Ancestors)
}

func ancestorsEqual(a, b []gwv1alpha1.PolicyAncestorStatus) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		var match *gwv1alpha1.PolicyAncestorStatus
		for j := range b {
			if b[j].ControllerName == a[i].ControllerName && b[j].AncestorRef.Name == a[i].AncestorRef.Name {
				match = &b[j]
				break
			}
		}
		if match == nil || !conditionsEqualIgnoreTime(a[i].Conditions, match.Conditions) {
			return false
		}
	}
	return true
}

func conditionsEqualIgnoreTime(a, b []metav1.Condition) bool {
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
