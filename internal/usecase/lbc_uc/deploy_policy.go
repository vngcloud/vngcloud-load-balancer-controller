package lbc_uc

import (
	"context"
	"fmt"
	"sort"
	"strings"

	entityv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/entity"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/common"
	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

func (t *defaultModelDeployTask) deployPolicies(ctx context.Context, lbId, listenerId string, listenerPort int, policiesSpec []v1alpha1.Policy, newCreatedPools []v1alpha1.CreatedPool) ([]v1alpha1.CreatedPolicy, error) {
	currentPolicies, err := t.vngcloudRepo.ListPolicyOfListener(ctx, lbId, listenerId)
	if err != nil {
		return nil, err
	}

	// Drop orphans (cloud policies whose name isn't in the spec) before
	// creating new ones. Without this we hit POLICY_PER_LB quota on any
	// reconcile where a policy was renamed in the spec — the new name
	// doesn't match a cloud entry, so deployPolicy issues CreatePolicy,
	// the LB rejects it, the deploy errors out, and the existing
	// status-driven garbage collector in delete_policy.go never gets a
	// chance to run. Free quota first, then add.
	if err := t.deleteOrphanPoliciesByName(ctx, lbId, listenerId, policiesSpec, currentPolicies); err != nil {
		return nil, err
	}
	// Re-list so subsequent name lookups see the deletions; otherwise an
	// orphan we just deleted could still be matched by a fresh deployPolicy
	// call as if it existed.
	currentPolicies, err = t.vngcloudRepo.ListPolicyOfListener(ctx, lbId, listenerId)
	if err != nil {
		return nil, err
	}

	createdPolicies := make([]v1alpha1.CreatedPolicy, 0, len(policiesSpec))
	for _, policySpec := range policiesSpec {
		createdPolicy, err := t.deployPolicy(ctx, lbId, listenerId, listenerPort, policySpec, newCreatedPools, currentPolicies)
		if err != nil {
			return nil, err
		}
		createdPolicies = append(createdPolicies, *createdPolicy)
	}
	return createdPolicies, nil
}

// deleteOrphanPoliciesByName removes any cloud policy whose name isn't
// present in policiesSpec. We are the sole owner of every policy on this
// listener (the LBC is fully driven by Spec), so anything not in spec is
// either an old name for a policy that's been renamed or a leftover from
// a previous reconcile — both safe to delete.
//
// This complements (does NOT replace) deployDeleteRedundantPolicies in
// delete_policy.go. The status-driven GC there still cleans up policy
// IDs we recorded but no longer want; this name-driven sweep additionally
// covers cloud entries we lost track of (e.g. status not yet written
// before a previous failure, or a rename across controller versions).
func (t *defaultModelDeployTask) deleteOrphanPoliciesByName(ctx context.Context, lbId, listenerId string, policiesSpec []v1alpha1.Policy, currentPolicies *entityv2.ListPolicies) error {
	if t.lbConfig.Spec.Type == loadbalancerv2.LoadBalancerTypeLayer4 {
		return nil
	}
	if currentPolicies == nil || len(currentPolicies.Items) == 0 {
		return nil
	}
	wanted := make(map[string]struct{}, len(policiesSpec))
	for _, p := range policiesSpec {
		wanted[p.Name] = struct{}{}
	}
	for _, p := range currentPolicies.Items {
		if _, keep := wanted[p.Name]; keep {
			continue
		}
		t.logger.Infof("Deleting orphan policy %s (id=%s) on listener %s — not in spec",
			p.Name, p.UUID, listenerId)
		if err := t.vngcloudRepo.DeletePolicy(ctx, lbId, listenerId, p.UUID); err != nil {
			return err
		}
		if _, err := t.vngcloudRepo.WaitForLBActive(ctx, lbId); err != nil {
			t.logger.Error("Failed to wait for loadbalancer active: ", err)
			return err
		}
	}
	return nil
}

func (t *defaultModelDeployTask) deployPolicy(ctx context.Context, lbId, listenerId string, listenerPort int, policySpec v1alpha1.Policy, newCreatedPools []v1alpha1.CreatedPool, currentPolicies *entityv2.ListPolicies) (*v1alpha1.CreatedPolicy, error) {
	searchPolicyByName := func(name string) *entityv2.Policy {
		for _, p := range currentPolicies.Items {
			if p.Name == name {
				return p
			}
		}
		return nil
	}

	currentPolicy := searchPolicyByName(policySpec.Name)
	if currentPolicy == nil {
		createRequest, err := t.buildPolicyCreateRequest(ctx, lbId, listenerId, policySpec, newCreatedPools)
		if err != nil {
			return nil, err
		}
		newPolicy, err := t.vngcloudRepo.CreatePolicy(ctx, lbId, listenerId, createRequest)
		if err != nil {
			return nil, err
		}
		if err := t.statusAddPolicy(ctx, listenerId, listenerPort, newPolicy.UUID); err != nil {
			return nil, err
		}

		if _, err := t.vngcloudRepo.WaitForLBActive(ctx, lbId); err != nil {
			t.logger.Error("Failed to wait for loadbalancer active: ", err)
			return nil, err
		}
		return &v1alpha1.CreatedPolicy{Id: newPolicy.UUID}, nil
	} else {
		updateRequest, messages, err := t.buildPolicyUpdateRequest(ctx, lbId, listenerId, policySpec, newCreatedPools, currentPolicy)
		if err != nil {
			return nil, err
		}
		if updateRequest != nil {
			t.logger.Infof("Need update policy %s: %s.", policySpec.Name, strings.Join(messages, ", "))
			if err := t.vngcloudRepo.UpdatePolicy(ctx, lbId, listenerId, currentPolicy.UUID, updateRequest); err != nil {
				return nil, err
			}
			if err := t.statusAddPolicy(ctx, listenerId, listenerPort, currentPolicy.UUID); err != nil {
				return nil, err
			}

			if _, err := t.vngcloudRepo.WaitForLBActive(ctx, lbId); err != nil {
				t.logger.Error("Failed to wait for loadbalancer active: ", err)
				return nil, err
			}
		}
	}
	return &v1alpha1.CreatedPolicy{Id: currentPolicy.UUID}, nil
}

func (t *defaultModelDeployTask) buildPolicyCreateRequest(_ context.Context, lbId, listenerId string, policySpec v1alpha1.Policy, newCreatedPools []v1alpha1.CreatedPool) (loadbalancerv2.ICreatePolicyRequest, error) { //nolint:unparam
	request := loadbalancerv2.NewCreatePolicyRequest(lbId, listenerId).
		WithAction(policySpec.Action).
		WithName(policySpec.Name).
		WithRules(
			func() []loadbalancerv2.L7RuleRequest {
				rules := make([]loadbalancerv2.L7RuleRequest, 0, len(policySpec.L7Rules))
				for _, ruleSpec := range policySpec.L7Rules {
					rules = append(rules, loadbalancerv2.L7RuleRequest{
						CompareType: ruleSpec.CompareType,
						RuleValue:   ruleSpec.RuleValue,
						RuleType:    ruleSpec.RuleType,
					})
				}
				return rules
			}()...,
		)

	// options for redirect to pool
	if policySpec.Action == loadbalancerv2.PolicyActionREDIRECTTOPOOL {
		if policySpec.RedirectPoolName != nil {
			findCreatedPoolByName := func(name string) *v1alpha1.CreatedPool {
				for _, p := range newCreatedPools {
					if p.Name == name {
						return &p
					}
				}
				return nil
			}
			if createdPool := findCreatedPoolByName(*policySpec.RedirectPoolName); createdPool != nil {
				request = request.WithRedirectPoolId(createdPool.Id)
			}
		}
	}

	// options for redirect to url
	if policySpec.Action == loadbalancerv2.PolicyActionREDIRECTTOURL {
		if policySpec.RedirectUrl != nil {
			request = request.WithRedirectURL(*policySpec.RedirectUrl)
		}
		if policySpec.RedirectHttpCode != nil {
			request = request.WithRedirectHTTPCode(int(*policySpec.RedirectHttpCode))
		}
		if policySpec.KeepQueryString != nil {
			request = request.WithKeepQueryString(*policySpec.KeepQueryString)
		}
	}
	return request, nil
}

func (t *defaultModelDeployTask) buildPolicyUpdateRequest(_ context.Context, lbId, listenerId string, policySpec v1alpha1.Policy, newCreatedPools []v1alpha1.CreatedPool, currentPolicy *entityv2.Policy) (loadbalancerv2.IUpdatePolicyRequest, []string, error) { //nolint:unparam
	isNeedUpdate := false
	message := make([]string, 0)
	updateOptions := &loadbalancerv2.UpdatePolicyRequest{
		LoadBalancerCommon: common.LoadBalancerCommon{
			LoadBalancerId: lbId,
		},
		ListenerCommon: common.ListenerCommon{
			ListenerId: listenerId,
		},
		PolicyCommon: common.PolicyCommon{
			PolicyId: currentPolicy.UUID,
		},
		Action:           loadbalancerv2.PolicyAction(currentPolicy.Action),
		KeepQueryString:  currentPolicy.KeepQueryString,
		RedirectPoolID:   currentPolicy.RedirectPoolID,
		RedirectURL:      currentPolicy.RedirectURL,
		RedirectHTTPCode: currentPolicy.RedirectHTTPCode,
		Rules: func() []loadbalancerv2.L7RuleRequest {
			rules := make([]loadbalancerv2.L7RuleRequest, 0, len(currentPolicy.L7Rules))
			for _, r := range currentPolicy.L7Rules {
				rules = append(rules, loadbalancerv2.L7RuleRequest{
					CompareType: loadbalancerv2.PolicyCompareType(r.CompareType),
					RuleValue:   r.RuleValue,
					RuleType:    loadbalancerv2.PolicyRuleType(r.RuleType),
				})
			}
			return rules
		}(),
	}

	if policySpec.Action != "" && string(policySpec.Action) != currentPolicy.Action {
		message = append(message, fmt.Sprintf("action (%s -> %s)", currentPolicy.Action, policySpec.Action))
		updateOptions.Action = policySpec.Action
		isNeedUpdate = true
	}

	// options for redirect to pool
	if policySpec.Action == loadbalancerv2.PolicyActionREDIRECTTOPOOL {
		poolId := ""
		if policySpec.RedirectPoolName != nil {
			findCreatedPoolByName := func(name string) *v1alpha1.CreatedPool {
				for _, p := range newCreatedPools {
					if p.Name == name {
						return &p
					}
				}
				return nil
			}

			if createdPool := findCreatedPoolByName(*policySpec.RedirectPoolName); createdPool != nil {
				poolId = createdPool.Id
			}
		}

		if currentPolicy.RedirectPoolID != poolId {
			message = append(message, fmt.Sprintf("redirect pool id (%s -> %s)", currentPolicy.RedirectPoolID, poolId))
			updateOptions.RedirectPoolID = poolId
			isNeedUpdate = true
		}
	}

	// options for redirect to url
	if policySpec.Action == loadbalancerv2.PolicyActionREDIRECTTOURL {
		redirectUrl := ""
		if policySpec.RedirectUrl != nil {
			redirectUrl = *policySpec.RedirectUrl
		}
		if currentPolicy.RedirectURL != redirectUrl {
			message = append(message, fmt.Sprintf("redirect url (%s -> %s)", currentPolicy.RedirectURL, redirectUrl))
			updateOptions.RedirectURL = redirectUrl
			isNeedUpdate = true
		}

		redirectHttpCode := 0
		if policySpec.RedirectHttpCode != nil {
			redirectHttpCode = int(*policySpec.RedirectHttpCode)
		}
		if currentPolicy.RedirectHTTPCode != redirectHttpCode {
			message = append(message, fmt.Sprintf("redirect http code (%d -> %d)", currentPolicy.RedirectHTTPCode, redirectHttpCode))
			updateOptions.RedirectHTTPCode = redirectHttpCode
			isNeedUpdate = true
		}

		keepQueryString := false
		if policySpec.KeepQueryString != nil {
			keepQueryString = *policySpec.KeepQueryString
		}
		if currentPolicy.KeepQueryString != keepQueryString {
			message = append(message, fmt.Sprintf("keep query string (%t -> %t)", currentPolicy.KeepQueryString, keepQueryString))
			updateOptions.KeepQueryString = keepQueryString
			isNeedUpdate = true
		}
	}

	if l7Rules := t.compareL7Rules(policySpec.L7Rules, currentPolicy.L7Rules); l7Rules != nil {
		message = append(message, "rules updated")
		updateOptions.Rules = l7Rules
		isNeedUpdate = true
	}

	if !isNeedUpdate {
		return nil, nil, nil
	}
	return updateOptions, message, nil
}

func (t *defaultModelDeployTask) compareL7Rules(rulesSpec []v1alpha1.L7Rule, currentRules []*entityv2.L7Rule) []loadbalancerv2.L7RuleRequest {
	l7RuleRequests := make([]loadbalancerv2.L7RuleRequest, 0, len(rulesSpec))
	for _, ruleSpec := range rulesSpec {
		l7RuleRequests = append(l7RuleRequests, loadbalancerv2.L7RuleRequest{
			CompareType: ruleSpec.CompareType,
			RuleValue:   ruleSpec.RuleValue,
			RuleType:    ruleSpec.RuleType,
		})
	}

	if len(l7RuleRequests) != len(currentRules) {
		return l7RuleRequests
	}

	for _, r := range l7RuleRequests {
		found := false
		for _, cr := range currentRules {
			if string(r.CompareType) == cr.CompareType && r.RuleValue == cr.RuleValue && string(r.RuleType) == cr.RuleType {
				found = true
				break
			}
		}
		if !found {
			return l7RuleRequests
		}
	}

	return nil
}

// reorder policies based on their positions
func (t *defaultModelDeployTask) deployReorderPolicies(ctx context.Context, lbId, listenerId string, policiesSpec []v1alpha1.Policy) error {
	isNeedCheckReorder := false
	for _, policySpec := range policiesSpec {
		if policySpec.Position != nil {
			isNeedCheckReorder = true
			break
		}
	}
	if !isNeedCheckReorder {
		t.logger.Debugf("No policy has position specified, skip reorder policies.")
		return nil
	}

	currentPolicies, err := t.vngcloudRepo.ListPolicyOfListener(ctx, lbId, listenerId)
	if err != nil {
		return err
	}

	type kv struct {
		policyId        string
		currentPosition int
		expectPosition  int
	}
	ss := make([]kv, 0, len(policiesSpec))
	for _, policySpec := range policiesSpec {
		currentPosition := -1
		policyId := ""
		for _, currentPolicy := range currentPolicies.Items {
			if currentPolicy.Name == policySpec.Name {
				currentPosition = currentPolicy.Position
				policyId = currentPolicy.UUID
				break
			}
		}
		expectPosition := -1
		if policySpec.Position != nil {
			expectPosition = int(*policySpec.Position)
		}
		ss = append(ss, kv{policyId: policyId, currentPosition: currentPosition, expectPosition: expectPosition})
	}

	// sort by expect position asc, if expect position equal, sort by current position asc
	// so that policies with no expect position will be at the end, and keep their current order
	sort.Slice(ss, func(i, j int) bool {
		if ss[i].expectPosition == ss[j].expectPosition {
			return ss[i].currentPosition < ss[j].currentPosition
		}
		return ss[i].expectPosition < ss[j].expectPosition
	})

	// check if reorder is needed
	isNeedReorder := false
	for i := 1; i < len(ss); i++ {
		if ss[i-1].expectPosition < ss[i].expectPosition && ss[i-1].currentPosition > ss[i].currentPosition {
			isNeedReorder = true
			break
		}
	}

	if !isNeedReorder {
		t.logger.Debugf("Policies are already in expected order, no need to reorder.")
		return nil
	}

	// reorder policies
	policyIds := make([]string, len(ss))
	for i, policy := range ss {
		policyIds[i] = policy.policyId
	}

	if err := t.vngcloudRepo.ReorderPolicies(ctx, lbId, listenerId, policyIds); err != nil {
		return err
	}
	if _, err := t.vngcloudRepo.WaitForLBActive(ctx, lbId); err != nil {
		return err
	}

	return nil
}
