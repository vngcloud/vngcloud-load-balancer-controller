package vngcloud_repo

import (
	"context"

	"github.com/anngdinh/operator-helper/contexts"
	entityv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/entity"
	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
)

// --------------------------- Pool ---------------------------

//	func (m *vngCloudRepository) GetPoolByName(ctx context.Context,lbID, name string) (*objects.Pool, error) {
//		logger.Error("not implemented yet")
//		return nil, domain.ErrorNotImplemented
//	}
func (m *vngCloudRepository) CreatePool(ctx context.Context, lbID string, opt loadbalancerv2.ICreatePoolRequest) (*entityv2.Pool, error) {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request create pool of load balancer %s", domain.RequestIcon, lbID)
	pool, sdkErr := m.client.VLBGateway().V2().LoadBalancerService().CreatePool(opt.AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - CreatePool: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return nil, domain.SDKError(sdkErr)
	}
	return pool, nil
}
func (m *vngCloudRepository) ListPool(ctx context.Context, lbID string) (*entityv2.ListPools, error) {
	logger := contexts.NewContext(ctx).Log()
	opt := loadbalancerv2.NewListPoolsByLoadBalancerIdRequest(lbID)
	pools, sdkErr := m.client.VLBGateway().V2().LoadBalancerService().ListPoolsByLoadBalancerId(opt.AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - ListPool: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return nil, domain.SDKError(sdkErr)
	}
	return pools, nil
}
func (m *vngCloudRepository) UpdatePoolMembers(ctx context.Context, lbID, poolID string, members loadbalancerv2.IUpdatePoolMembersRequest) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request update pool members of pool %s of load balancer %s", domain.RequestIcon, poolID, lbID)
	sdkErr := m.client.VLBGateway().V2().LoadBalancerService().UpdatePoolMembers(members.AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - UpdatePoolMembers: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return domain.SDKError(sdkErr)
	}
	return nil
}

func (m *vngCloudRepository) GetPoolByID(ctx context.Context, lbID, poolID string) (*entityv2.Pool, error) {
	logger := contexts.NewContext(ctx).Log()
	logger.Error("not implemented yet")
	return nil, domain.ErrorNotImplemented
}

func (m *vngCloudRepository) GetPoolMembers(ctx context.Context, lbID, poolID string) (*entityv2.ListMembers, error) {
	logger := contexts.NewContext(ctx).Log()
	opt := loadbalancerv2.NewListPoolMembersRequest(lbID, poolID)
	members, sdkErr := m.client.VLBGateway().V2().LoadBalancerService().ListPoolMembers(opt.AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - GetPoolMembers: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return nil, domain.SDKError(sdkErr)
	}
	return members, nil
}

func (m *vngCloudRepository) DeletePool(ctx context.Context, lbID, poolID string) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request delete pool %s of load balancer %s", domain.RequestIcon, poolID, lbID)
	opt := loadbalancerv2.NewDeletePoolByIdRequest(lbID, poolID)
	sdkErr := m.client.VLBGateway().V2().LoadBalancerService().DeletePoolById(opt.AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - DeletePool: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return domain.SDKError(sdkErr)
	}
	return nil
}

func (m *vngCloudRepository) UpdatePool(ctx context.Context, lbID, poolID string, opt loadbalancerv2.IUpdatePoolRequest) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request update pool %s of load balancer %s", domain.RequestIcon, poolID, lbID)
	sdkErr := m.client.VLBGateway().V2().LoadBalancerService().UpdatePool(opt.AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - UpdatePool: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return domain.SDKError(sdkErr)
	}
	return nil
}

func (m *vngCloudRepository) GetPoolHealthMonitorById(ctx context.Context, lbID, poolID string) (*entityv2.HealthMonitor, error) {
	logger := contexts.NewContext(ctx).Log()
	opt := loadbalancerv2.NewGetPoolHealthMonitorByIdRequest(lbID, poolID)
	monitor, sdkErr := m.client.VLBGateway().V2().LoadBalancerService().GetPoolHealthMonitorById(opt.AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - GetPoolHealthMonitorById: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return nil, domain.SDKError(sdkErr)
	}
	return monitor, nil
}
