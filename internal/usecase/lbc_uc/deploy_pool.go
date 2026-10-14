package lbc_uc

import (
	"context"
	"fmt"
	"strings"

	entityv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/entity"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/common"
	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	"k8s.io/utils/ptr"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

// oldPools are in Status
// newPools are in Spec
// ensure them to portal. Don't delete old pool because some listener is using them
func (t *defaultModelDeployTask) deployPools(ctx context.Context, lbId string) ([]v1alpha1.CreatedPool, error) {
	currentPools, err := t.vngcloudRepo.ListPool(ctx, lbId)
	if err != nil {
		return nil, err
	}

	createdPools := make([]v1alpha1.CreatedPool, 0)
	for _, pool := range t.lbConfig.Spec.Pools {
		if createdPool, err := t.deployPool(ctx, lbId, &pool, currentPools); err != nil {
			return nil, err
		} else {
			createdPools = append(createdPools, *createdPool)
		}
	}
	return createdPools, nil
}

func (t *defaultModelDeployTask) deployPool(ctx context.Context, lbId string, pool *v1alpha1.Pool, currentPools *entityv2.ListPools) (*v1alpha1.CreatedPool, error) {
	searchPoolByName := func(name string) *entityv2.Pool {
		for _, p := range currentPools.Items {
			if p.Name == name {
				return p
			}
		}
		return nil
	}

	currentPool := searchPoolByName(pool.Name)
	if currentPool == nil {
		// Create new pool
		_pool, err := t.vngcloudRepo.CreatePool(ctx, lbId,
			t.buildCreatePoolRequest(ctx, lbId, pool),
		)
		if err != nil {
			return nil, err
		}
		if err := t.statusAddPoolMember(ctx, _pool.UUID, pool.Name, pool.Members); err != nil {
			return nil, err
		}

		if _, err := t.vngcloudRepo.WaitForLBActive(ctx, lbId); err != nil {
			return nil, err
		}
		return &v1alpha1.CreatedPool{
			Id:             _pool.UUID,
			Name:           pool.Name,
			CreatedMembers: pool.Members,
		}, nil
	} else {
		if err := t.statusAddPool(ctx, currentPool.UUID, currentPool.Name); err != nil {
			return nil, err
		}
		if err := t.statusAddPoolMember(ctx, currentPool.UUID, currentPool.Name, pool.Members); err != nil {
			return nil, err
		}
	}

	// get health monitor info
	healthMonitor, err := t.vngcloudRepo.GetPoolHealthMonitorById(ctx, lbId, currentPool.UUID)
	if err != nil {
		return nil, fmt.Errorf("failed to get health monitor for pool %s: %v", currentPool.UUID, err)
	}
	currentPool.HealthMonitor = healthMonitor

	// ensure exist pool
	updateOptions, message := t.buildPoolUpdateRequest(ctx, lbId, pool, currentPool)
	if updateOptions != nil {
		t.logger.Info("Need update pool: ", strings.Join(message, ", "))
		err := t.vngcloudRepo.UpdatePool(ctx, lbId, currentPool.UUID, updateOptions)
		if err != nil {
			t.logger.Error("Failed to update pool: ", err)
			return nil, err
		}

		if _, err := t.vngcloudRepo.WaitForLBActive(ctx, lbId); err != nil {
			t.logger.Error("Failed to wait for loadbalancer active: ", err)
			return nil, err
		}
	}

	// ensure pool members
	currentPoolMembers, err := t.vngcloudRepo.GetPoolMembers(ctx, lbId, currentPool.UUID)
	if err != nil {
		t.logger.Error("Failed to get pool members: ", err)
		return nil, err
	}

	// get created members for this pool from status
	createdMemberStatus := []v1alpha1.PoolMember{}
	for _, p := range t.lbConfig.Status.CreatedPools {
		if p.Name == pool.Name {
			createdMemberStatus = p.CreatedMembers
			break
		}
	}

	updateMembers := t.mergePoolMembers(ctx,
		createdMemberStatus,
		convertMemberList(currentPoolMembers),
		pool.Members)

	if !t.comparePoolMembers(ctx, updateMembers, convertMemberList(currentPoolMembers)) {
		convertMembers := make([]loadbalancerv2.IMemberRequest, 0)
		for _, member := range updateMembers {
			convertMembers = append(convertMembers, buildMemberRequest(member))
		}
		updateMemberOptions := loadbalancerv2.NewUpdatePoolMembersRequest(lbId, currentPool.UUID).WithMembers(convertMembers...)

		t.logger.Info("Need update pool members: ", updateMembers)
		if err = t.vngcloudRepo.UpdatePoolMembers(ctx, lbId, currentPool.UUID, updateMemberOptions); err != nil {
			t.logger.Error("Failed to update pool members: ", err)
			return nil, err
		}
		if err := t.statusAddPoolMember(ctx, currentPool.UUID, currentPool.Name, pool.Members); err != nil {
			return nil, err
		}
		if _, err := t.vngcloudRepo.WaitForLBActive(ctx, lbId); err != nil {
			t.logger.Error("Failed to wait for loadbalancer active: ", err)
			return nil, err
		}
	}
	return &v1alpha1.CreatedPool{
		Id:             currentPool.UUID,
		Name:           currentPool.Name,
		CreatedMembers: pool.Members,
	}, nil
}

// create CreatePoolRequest depend on default config and pool value
func (t *defaultModelDeployTask) buildCreatePoolRequest(_ context.Context, lbId string, pool *v1alpha1.Pool) loadbalancerv2.ICreatePoolRequest {
	convertMembers := make([]loadbalancerv2.IMemberRequest, 0)
	for _, member := range pool.Members {
		convertMembers = append(convertMembers, buildMemberRequest(member))
	}
	healthMonitor := loadbalancerv2.HealthMonitor{
		HealthCheckProtocol: pool.HealthMonitor.Protocol,
		HealthyThreshold:    t.cfg.LoadBalancerOpts.DefaultHealthyThreshold,
		UnhealthyThreshold:  t.cfg.LoadBalancerOpts.DefaultUnhealthyThreshold,
		Interval:            t.cfg.LoadBalancerOpts.DefaultInterval,
		Timeout:             t.cfg.LoadBalancerOpts.DefaultTimeout,

		HealthCheckMethod: pool.HealthMonitor.HealthCheckMethod,
		HealthCheckPath:   pool.HealthMonitor.HealthCheckPath,
		SuccessCode:       pool.HealthMonitor.SuccessCode,
		HttpVersion:       pool.HealthMonitor.HttpVersion,
		DomainName:        pool.HealthMonitor.DomainName,
	}
	if pool.HealthMonitor.HealthyThreshold != nil {
		healthMonitor.HealthyThreshold = *pool.HealthMonitor.HealthyThreshold
	}
	if pool.HealthMonitor.UnhealthyThreshold != nil {
		healthMonitor.UnhealthyThreshold = *pool.HealthMonitor.UnhealthyThreshold
	}
	if pool.HealthMonitor.Interval != nil {
		healthMonitor.Interval = *pool.HealthMonitor.Interval
	}
	if pool.HealthMonitor.Timeout != nil {
		healthMonitor.Timeout = *pool.HealthMonitor.Timeout
	}

	r := &loadbalancerv2.CreatePoolRequest{
		LoadBalancerCommon: common.LoadBalancerCommon{LoadBalancerId: lbId},
		Algorithm:          loadbalancerv2.PoolAlgorithm(t.cfg.LoadBalancerOpts.DefaultPoolAlgorithm),
		PoolName:           pool.Name,
		PoolProtocol:       pool.Protocol,
		Stickiness:         nil,
		TLSEncryption:      nil,
		HealthMonitor:      &healthMonitor,
		Members:            convertMembers,
	}
	if pool.Algorithm != nil && *pool.Algorithm != "" {
		r.Algorithm = *pool.Algorithm
	}

	if t.lbConfig.Spec.Type == loadbalancerv2.LoadBalancerTypeLayer7 {
		// Stickiness must be specified for L7 pools
		r.Stickiness = ptr.To(false)
		r.TLSEncryption = ptr.To(false)

		if pool.Stickiness != nil {
			r.Stickiness = pool.Stickiness
		}
		if pool.TLSEncryption != nil {
			r.TLSEncryption = pool.TLSEncryption
		}
	}
	return r
}

// return UpdateRequest and messages
func (t *defaultModelDeployTask) buildPoolUpdateRequest(_ context.Context, lbID string, pool *v1alpha1.Pool, current *entityv2.Pool) (*loadbalancerv2.UpdatePoolRequest, []string) { //nolint:gocyclo
	isNeedUpdate := false
	message := make([]string, 0)

	healthMonitor := &loadbalancerv2.HealthMonitor{
		HealthyThreshold:    current.HealthMonitor.HealthyThreshold,
		UnhealthyThreshold:  current.HealthMonitor.UnhealthyThreshold,
		Interval:            current.HealthMonitor.Interval,
		Timeout:             current.HealthMonitor.Timeout,
		HealthCheckProtocol: loadbalancerv2.HealthCheckProtocol(current.HealthMonitor.HealthCheckProtocol),
		HealthCheckPath:     current.HealthMonitor.HealthCheckPath,
		DomainName:          current.HealthMonitor.DomainName,
		SuccessCode:         current.HealthMonitor.SuccessCode,
		HealthCheckMethod: func() *loadbalancerv2.HealthCheckMethod {
			if current.HealthMonitor != nil && current.HealthMonitor.HealthCheckMethod != nil {
				return ptr.To(loadbalancerv2.HealthCheckMethod(*current.HealthMonitor.HealthCheckMethod))
			}
			return nil
		}(),
		HttpVersion: func() *loadbalancerv2.HealthCheckHttpVersion {
			if current.HealthMonitor != nil && current.HealthMonitor.HttpVersion != nil {
				return ptr.To(loadbalancerv2.HealthCheckHttpVersion(*current.HealthMonitor.HttpVersion))
			}
			return nil
		}(),
	}
	updateOptions := &loadbalancerv2.UpdatePoolRequest{
		PoolCommon: common.PoolCommon{
			PoolId: current.UUID,
		},
		LoadBalancerCommon: common.LoadBalancerCommon{
			LoadBalancerId: lbID,
		},
		Algorithm:     loadbalancerv2.PoolAlgorithm(current.LoadBalanceMethod),
		Stickiness:    nil,
		TLSEncryption: nil,
		HealthMonitor: healthMonitor,
	}
	if t.lbConfig.Spec.Type == loadbalancerv2.LoadBalancerTypeLayer7 {
		updateOptions.Stickiness = &current.Stickiness
		updateOptions.TLSEncryption = &current.TLSEncryption

		if pool.Stickiness != nil && *pool.Stickiness != current.Stickiness {
			message = append(message, fmt.Sprintf("stickiness (%t -> %t)", current.Stickiness, *pool.Stickiness))
			updateOptions.Stickiness = ptr.To(*pool.Stickiness)
			isNeedUpdate = true
		}
		if pool.TLSEncryption != nil && *pool.TLSEncryption != current.TLSEncryption {
			message = append(message, fmt.Sprintf("tls encryption (%t -> %t)", current.TLSEncryption, *pool.TLSEncryption))
			updateOptions.TLSEncryption = ptr.To(*pool.TLSEncryption)
			isNeedUpdate = true
		}
	}
	if pool.Algorithm != nil && *pool.Algorithm != "" && *pool.Algorithm != loadbalancerv2.PoolAlgorithm(current.LoadBalanceMethod) {
		message = append(message, fmt.Sprintf("algorithm (%s -> %s)", current.LoadBalanceMethod, *pool.Algorithm))
		updateOptions.WithAlgorithm(*pool.Algorithm)
		isNeedUpdate = true
	}

	if pool.HealthMonitor.HealthyThreshold != nil && *pool.HealthMonitor.HealthyThreshold != current.HealthMonitor.HealthyThreshold {
		message = append(message, fmt.Sprintf("healthy threshold (%d -> %d)", current.HealthMonitor.HealthyThreshold, *pool.HealthMonitor.HealthyThreshold))
		updateOptions.HealthMonitor.WithHealthyThreshold(*pool.HealthMonitor.HealthyThreshold)
		isNeedUpdate = true
	}
	if pool.HealthMonitor.UnhealthyThreshold != nil && *pool.HealthMonitor.UnhealthyThreshold != current.HealthMonitor.UnhealthyThreshold {
		message = append(message, fmt.Sprintf("unhealthy threshold (%d -> %d)", current.HealthMonitor.UnhealthyThreshold, *pool.HealthMonitor.UnhealthyThreshold))
		updateOptions.HealthMonitor.WithUnhealthyThreshold(*pool.HealthMonitor.UnhealthyThreshold)
		isNeedUpdate = true
	}
	if pool.HealthMonitor.Interval != nil && *pool.HealthMonitor.Interval != current.HealthMonitor.Interval {
		message = append(message, fmt.Sprintf("interval (%d -> %d)", current.HealthMonitor.Interval, *pool.HealthMonitor.Interval))
		updateOptions.HealthMonitor.WithInterval(*pool.HealthMonitor.Interval)
		isNeedUpdate = true
	}
	if pool.HealthMonitor.Timeout != nil && *pool.HealthMonitor.Timeout != current.HealthMonitor.Timeout {
		message = append(message, fmt.Sprintf("timeout (%d -> %d)", current.HealthMonitor.Timeout, *pool.HealthMonitor.Timeout))
		updateOptions.HealthMonitor.WithTimeout(*pool.HealthMonitor.Timeout)
		isNeedUpdate = true
	}

	// compare HTTP health check options
	if string(pool.HealthMonitor.Protocol) != current.HealthMonitor.HealthCheckProtocol {
		message = append(message, fmt.Sprintf("health check protocol (%s -> %s)", current.HealthMonitor.HealthCheckProtocol, pool.HealthMonitor.Protocol))
		updateOptions.HealthMonitor.WithHealthCheckProtocol(pool.HealthMonitor.Protocol)
		isNeedUpdate = true
	}
	// switch from (HTTP, HTTPS) --> (HTTP, HTTPS)
	if (current.HealthMonitor.HealthCheckProtocol == string(loadbalancerv2.HealthCheckProtocolHTTP) || current.HealthMonitor.HealthCheckProtocol == string(loadbalancerv2.HealthCheckProtocolHTTPs)) &&
		(pool.HealthMonitor.Protocol == loadbalancerv2.HealthCheckProtocolHTTP || pool.HealthMonitor.Protocol == loadbalancerv2.HealthCheckProtocolHTTPs) {

		if pool.HealthMonitor.HealthCheckMethod != nil && (current.HealthMonitor.HealthCheckMethod == nil || string(*pool.HealthMonitor.HealthCheckMethod) != *current.HealthMonitor.HealthCheckMethod) {
			message = append(message, fmt.Sprintf("health check method (%v -> %v)", current.HealthMonitor.HealthCheckMethod, *pool.HealthMonitor.HealthCheckMethod))
			updateOptions.HealthMonitor.WithHealthCheckMethod(pool.HealthMonitor.HealthCheckMethod)
			isNeedUpdate = true
		}
		if pool.HealthMonitor.HealthCheckPath != nil && (current.HealthMonitor.HealthCheckPath == nil || *pool.HealthMonitor.HealthCheckPath != *current.HealthMonitor.HealthCheckPath) {
			message = append(message, fmt.Sprintf("health check path (%v -> %v)", current.HealthMonitor.HealthCheckPath, *pool.HealthMonitor.HealthCheckPath))
			updateOptions.HealthMonitor.WithHealthCheckPath(pool.HealthMonitor.HealthCheckPath)
			isNeedUpdate = true
		}
		if pool.HealthMonitor.SuccessCode != nil && (current.HealthMonitor.SuccessCode == nil || *pool.HealthMonitor.SuccessCode != *current.HealthMonitor.SuccessCode) {
			message = append(message, fmt.Sprintf("success code (%v -> %v)", current.HealthMonitor.SuccessCode, *pool.HealthMonitor.SuccessCode))
			updateOptions.HealthMonitor.WithSuccessCode(pool.HealthMonitor.SuccessCode)
			isNeedUpdate = true
		}
		if pool.HealthMonitor.HttpVersion != nil && (current.HealthMonitor.HttpVersion == nil || string(*pool.HealthMonitor.HttpVersion) != *current.HealthMonitor.HttpVersion) {
			message = append(message, fmt.Sprintf("http version (%v -> %v)", current.HealthMonitor.HttpVersion, *pool.HealthMonitor.HttpVersion))
			updateOptions.HealthMonitor.WithHttpVersion(pool.HealthMonitor.HttpVersion)
			isNeedUpdate = true
		}
		if pool.HealthMonitor.DomainName != nil && (current.HealthMonitor.DomainName == nil || *pool.HealthMonitor.DomainName != *current.HealthMonitor.DomainName) {
			message = append(message, fmt.Sprintf("domain name (%v -> %v)", current.HealthMonitor.DomainName, *pool.HealthMonitor.DomainName))
			updateOptions.HealthMonitor.WithDomainName(pool.HealthMonitor.DomainName)
			isNeedUpdate = true
		}
	} else // switch from (HTTP, HTTPS) --> (TCP)
	if (current.HealthMonitor.HealthCheckProtocol == string(loadbalancerv2.HealthCheckProtocolHTTP) || current.HealthMonitor.HealthCheckProtocol == string(loadbalancerv2.HealthCheckProtocolHTTPs)) &&
		pool.HealthMonitor.Protocol == loadbalancerv2.HealthCheckProtocolTCP {

		updateOptions.HealthMonitor.WithHealthCheckMethod(nil)
		updateOptions.HealthMonitor.WithHealthCheckPath(nil)
		updateOptions.HealthMonitor.WithSuccessCode(nil)
		updateOptions.HealthMonitor.WithHttpVersion(nil)
		updateOptions.HealthMonitor.WithDomainName(nil)
		message = append(message, "health check options (http -> tcp)")
		isNeedUpdate = true

	} else // switch from (TCP) --> (HTTP, HTTPS)
	if current.HealthMonitor.HealthCheckProtocol == string(loadbalancerv2.HealthCheckProtocolTCP) &&
		(pool.HealthMonitor.Protocol == loadbalancerv2.HealthCheckProtocolHTTP || pool.HealthMonitor.Protocol == loadbalancerv2.HealthCheckProtocolHTTPs) {

		updateOptions.HealthMonitor.WithHealthCheckMethod(pool.HealthMonitor.HealthCheckMethod)
		updateOptions.HealthMonitor.WithHealthCheckPath(pool.HealthMonitor.HealthCheckPath)
		updateOptions.HealthMonitor.WithSuccessCode(pool.HealthMonitor.SuccessCode)
		updateOptions.HealthMonitor.WithHttpVersion(pool.HealthMonitor.HttpVersion)
		updateOptions.HealthMonitor.WithDomainName(pool.HealthMonitor.DomainName)
		message = append(message, "health check options (tcp -> http)")
		isNeedUpdate = true
	}

	if !isNeedUpdate {
		return nil, nil
	}
	return updateOptions, message
}

// MergePoolMembers merges the pool members
// - keep current member only if it is not in spec and not created by us
// - prefer the spec member (with its desired weight) when its identity matches a current member
func (t *defaultModelDeployTask) mergePoolMembers(_ context.Context, createdMembers, currentMembers, poolMemberSpec []v1alpha1.PoolMember) []v1alpha1.PoolMember {
	mergedPoolMembers := make([]v1alpha1.PoolMember, 0)

	// keep current member only if it is not managed by us (not in spec and not created by us).
	// members that are in spec are added below from poolMemberSpec so the desired weight wins.
	for _, member := range currentMembers {
		if !t.checkIfPoolMemberExistByAddress(poolMemberSpec, &member) && !t.checkIfPoolMemberExistByAddress(createdMembers, &member) {
			mergedPoolMembers = append(mergedPoolMembers, member)
		}
	}

	// add members from spec (carrying their desired weight)
	for _, member := range poolMemberSpec {
		if !t.checkIfPoolMemberExistByAddress(mergedPoolMembers, &member) {
			mergedPoolMembers = append(mergedPoolMembers, member)
		}
	}

	return mergedPoolMembers
}

func (t *defaultModelDeployTask) comparePoolMembers(_ context.Context, poolMembers []v1alpha1.PoolMember, current []v1alpha1.PoolMember) bool {
	if len(poolMembers) != len(current) {
		return false
	}

	for _, member := range poolMembers {
		if !t.checkIfPoolMemberExist(current, &member) {
			return false
		}
	}

	return true
}

// checkIfPoolMemberExistByAddress checks if a member with the same identity
// (IP+Port+MonitorPort) exists in the list, ignoring weight. Used for
// de-duplication in mergePoolMembers and for "can we cover this member" checks.
func (t *defaultModelDeployTask) checkIfPoolMemberExistByAddress(list []v1alpha1.PoolMember, member *v1alpha1.PoolMember) bool {
	for _, r := range list {
		if r.IP == member.IP &&
			r.Port == member.Port &&
			r.MonitorPort == member.MonitorPort {
			return true
		}
	}
	return false
}

// checkIfPoolMemberExist checks if the pool member exists in the pool members
// with the same identity AND the same effective weight. Weight must be part of
// equality so that a weight change (e.g. 80 -> 20) is detected and pushed to the
// load balancer. An unset (nil) weight is treated as the default weight, matching
// what buildMemberRequest sends to the API; this keeps the comparison stable and
// prevents a reconcile loop for members whose weight was never specified.
func (t *defaultModelDeployTask) checkIfPoolMemberExist(list []v1alpha1.PoolMember, member *v1alpha1.PoolMember) bool {
	for _, r := range list {
		if r.IP == member.IP &&
			r.Port == member.Port &&
			r.MonitorPort == member.MonitorPort &&
			effectiveWeight(r.Weight) == effectiveWeight(member.Weight) {
			return true
		}
	}
	return false
}

// defaultMemberWeight is the weight applied when a member does not specify one.
// It mirrors the SDK default used by buildMemberRequest.
const defaultMemberWeight = 1

// effectiveWeight returns the weight a member will be deployed with: its explicit
// value, or the default when unset. A non-positive weight is treated as unset,
// since valid load balancer weights are >= 1.
func effectiveWeight(w *int) int {
	if w == nil || *w <= 0 {
		return defaultMemberWeight
	}
	return *w
}

// buildMemberRequest builds an SDK member request carrying the member's weight.
// The SDK's NewMember constructor hardcodes weight to 1 and exposes no setter, so
// the request struct is built directly. Weight defaults to 1 when unset.
func buildMemberRequest(member v1alpha1.PoolMember) loadbalancerv2.IMemberRequest {
	weight := effectiveWeight(member.Weight)
	backup := false
	if member.Backup != nil {
		backup = *member.Backup
	}
	return &loadbalancerv2.Member{
		Name:        member.Name,
		IpAddress:   member.IP,
		Port:        member.Port,
		MonitorPort: member.MonitorPort,
		Weight:      weight,
		Backup:      backup,
	}
}

func convertMemberList(members *entityv2.ListMembers) []v1alpha1.PoolMember {
	result := make([]v1alpha1.PoolMember, 0)
	if members == nil || len(members.Items) == 0 {
		return result
	}
	for _, member := range members.Items {
		result = append(result, *convertMember(member))
	}
	return result
}

func convertMember(member *entityv2.Member) *v1alpha1.PoolMember {
	return &v1alpha1.PoolMember{
		Name:        member.Name,
		IP:          member.Address,
		Port:        member.ProtocolPort,
		Backup:      &member.Backup,
		Weight:      &member.Weight,
		MonitorPort: member.MonitorPort,
	}
}
