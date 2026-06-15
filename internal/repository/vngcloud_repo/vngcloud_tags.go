package vngcloud_repo

import (
	"context"

	"github.com/anngdinh/operator-helper/contexts"
	entityv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/entity"
	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
)

// // --------------------------- Tags ---------------------------

func (m *vngCloudRepository) ListTags(ctx context.Context, resourceID string) (*entityv2.ListTags, error) {
	logger := contexts.NewContext(ctx).Log()
	opt := loadbalancerv2.NewListTagsRequest(resourceID)
	tags, sdkErr := m.client.VLBGateway().V2().LoadBalancerService().ListTags(opt.AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - ListTags: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return nil, domain.SDKError(sdkErr)
	}
	return tags, nil
}

func (m *vngCloudRepository) CreateTags(ctx context.Context, resourceID string, tags map[string]string) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request create tags for resource %s", domain.RequestIcon, resourceID)
	opt := loadbalancerv2.NewCreateTagsRequest(resourceID)
	arr := make([]string, 0)
	for k, v := range tags {
		arr = append(arr, k)
		arr = append(arr, v)
	}
	opt.WithTags(arr...)

	sdkErr := m.client.VLBGateway().V2().LoadBalancerService().CreateTags(opt.AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - CreateTags: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return domain.SDKError(sdkErr)
	}
	return nil
}

func (m *vngCloudRepository) UpdateTags(ctx context.Context, resourceID string, tags map[string]string) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request update tags for resource %s", domain.RequestIcon, resourceID)
	opt := loadbalancerv2.NewUpdateTagsRequest(resourceID)
	arr := make([]string, 0)
	for k, v := range tags {
		arr = append(arr, k)
		arr = append(arr, v)
	}
	opt.WithTags(arr...)

	sdkErr := m.client.VLBGateway().V2().LoadBalancerService().UpdateTags(opt.AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - UpdateTags: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return domain.SDKError(sdkErr)
	}
	return nil
}
