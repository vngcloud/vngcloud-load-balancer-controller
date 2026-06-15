package vngcloud_repo

import (
	"context"
	"strings"
	"time"

	"github.com/anngdinh/operator-helper/contexts"
	entityv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/entity"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/common"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/inter"
	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/consts"
)

// --------------------------- Load Balancer ---------------------------

func (m *vngCloudRepository) ListLoadBalancers(ctx context.Context, tags []string) (*entityv2.ListLoadBalancers, error) {
	logger := contexts.NewContext(ctx).Log()
	lbs, sdkErr := m.client.VLBGateway().V2().LoadBalancerService().ListLoadBalancers(loadbalancerv2.NewListLoadBalancersRequest(defaultOffset, defaultPageSize).WithTags(tags...).AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - ListLoadBalancers: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return nil, domain.SDKError(sdkErr)
	}
	return lbs, nil
}
func (m *vngCloudRepository) GetLoadBalancerByID(ctx context.Context, lbID string) (*entityv2.LoadBalancer, error) {
	logger := contexts.NewContext(ctx).Log()
	lb, sdkErr := m.client.VLBGateway().V2().LoadBalancerService().GetLoadBalancerById(loadbalancerv2.NewGetLoadBalancerByIdRequest(lbID).AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - GetLoadBalancerByID: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return nil, domain.SDKError(sdkErr)
	}
	return lb, nil
}

func (m *vngCloudRepository) GetLoadBalancerByName(ctx context.Context, name string) (*entityv2.LoadBalancer, error) {
	allLBs, err := m.ListLoadBalancers(ctx, nil)
	if err != nil {
		return nil, err
	}
	for _, lb := range allLBs.Items {
		if lb.Name == name {
			return lb, nil
		}
	}
	return nil, nil
}

func (m *vngCloudRepository) CreateLoadBalancer(ctx context.Context, lbOptions loadbalancerv2.ICreateLoadBalancerRequest) (*entityv2.LoadBalancer, error) {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request create load balancer.", domain.RequestIcon)
	newLB, sdkErr := m.client.VLBGateway().V2().LoadBalancerService().CreateLoadBalancer(lbOptions.AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - CreateLoadBalancer: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return nil, domain.SDKError(sdkErr)
	}
	return newLB, nil
}
func (m *vngCloudRepository) DeleteLoadBalancer(ctx context.Context, lbID string) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request delete load balancer %s", domain.RequestIcon, lbID)
	sdkErr := m.client.VLBGateway().V2().LoadBalancerService().DeleteLoadBalancerById(loadbalancerv2.NewDeleteLoadBalancerByIdRequest(lbID).AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - DeleteLoadBalancer: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return domain.SDKError(sdkErr)
	}
	return nil
}
func (m *vngCloudRepository) ResizeLoadBalancer(ctx context.Context, lbID, packageID string) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request resize load balancer %s to package %s", domain.RequestIcon, lbID, packageID)

	opt := loadbalancerv2.NewResizeLoadBalancerRequest(lbID, packageID)
	_, sdkErr := m.client.VLBGateway().V2().LoadBalancerService().ResizeLoadBalancer(opt.AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - ResizeLoadBalancer: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return domain.SDKError(sdkErr)
	}
	return nil
}
func (m *vngCloudRepository) WaitForLBActive(ctx context.Context, lbID string) (*entityv2.LoadBalancer, error) {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Waiting for load balancer %s to be ready", domain.WaitIcon, lbID)
	var resultLb *entityv2.LoadBalancer

	err := wait.ExponentialBackoff(wait.Backoff{
		Duration: 5 * time.Second,
		Factor:   1.2,
		Steps:    30,
	}, func() (done bool, err error) {
		lb, err := m.GetLoadBalancerByID(ctx, lbID)
		if err != nil {
			logger.Errorf("Error getting load balancer %s when wait active: %v", lbID, err)
			return false, err
		}
		if strings.ToUpper(lb.DisplayStatus) == consts.ACTIVE_LOADBALANCER_STATUS &&
			strings.ToUpper(lb.ProgressStatus) == consts.CREATED_LOADBALANCER_STATUS {
			logger.Infof("%s Load balancer %s is ready", domain.ReadyIcon, lbID)
			resultLb = lb
			return true, nil
		}
		if strings.ToUpper(lb.DisplayStatus) == consts.ERROR_LOADBALANCER_STATUS {
			logger.Errorf("Load balancer %s is in error status", lbID)
			resultLb = lb
			return true, domain.ErrorLoadBalancerStatusError
		}

		logger.Infof("%s Load balancer %s is not ready yet, waiting...", domain.WaitIcon, lbID)
		return false, nil
	})

	if wait.Interrupted(err) {
		logger.Errorf("timeout waiting for the loadbalancer %s with lb status %s", lbID, resultLb.Status)
	}

	return resultLb, err
}

func (m *vngCloudRepository) ListLoadBalancerPackageByZone(ctx context.Context, zone common.Zone) (*entityv2.ListLoadBalancerPackages, error) {
	logger := contexts.NewContext(ctx).Log()

	opt := loadbalancerv2.NewListLoadBalancerPackagesRequest()
	packages, sdkErr := m.client.VLBGateway().V2().LoadBalancerService().ListLoadBalancerPackages(opt.AddUserAgent(m.userAgent).WithZoneId(zone))
	if sdkErr != nil {
		logger.Error("[ERROR] - ListLoadBalancerPackageByZone: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return nil, domain.SDKError(sdkErr)
	}
	return packages, nil
}

func (m *vngCloudRepository) CreateInterLoadBalancer(ctx context.Context, lbOptions inter.ICreateLoadBalancerRequest) (*entityv2.LoadBalancer, error) {
	if m.superClient == nil {
		return nil, domain.ErrorSuperClientNotInitialized
	}

	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request create INTERVPC load balancer.", domain.RequestIcon)
	newLB, sdkErr := m.superClient.VLBGateway().Internal().LoadBalancerService().
		CreateLoadBalancer(lbOptions.AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - CreateInterLoadBalancer: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return nil, domain.SDKError(sdkErr)
	}
	return newLB, nil
}
