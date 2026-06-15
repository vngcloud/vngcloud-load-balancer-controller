package vngcloud_repo

import (
	"context"
	"strings"
	"time"

	"github.com/anngdinh/operator-helper/contexts"
	"github.com/pkg/errors"
	entityv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/entity"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/common"
	computev2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/compute/v2"
	networkv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/network/v2"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/consts"
	"k8s.io/apimachinery/pkg/util/wait"
)

func (r *vngCloudRepository) GetSubnetByID(ctx context.Context, networkID, subnetID string) (*entityv2.Subnet, error) {
	logger := contexts.NewContext(ctx).Log()
	subnet, sdkErr := r.client.VServerGateway().V2().NetworkService().GetSubnetById(networkv2.NewGetSubnetByIdRequest(networkID, subnetID).AddUserAgent(r.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - GetSubnetByID: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return nil, domain.SDKError(sdkErr)
	}
	return subnet, nil
}

func (m *vngCloudRepository) GetServerNetworkInfo(ctx context.Context, instanceID string) (zoneID common.Zone, networkID, subnetID, subnetCIDR string, err error) {
	logger := contexts.NewContext(ctx).Log()

	if instanceID == "" {
		logger.Error("[ERROR] - GetServerNetworkInfo: serverID is empty")
		return "", "", "", "", domain.ErrorInvalidInput
	}

	server, sdkErr := m.GetServerByID(ctx, instanceID)
	if sdkErr != nil {
		return "", "", "", "", sdkErr
	}
	if server == nil {
		return "", "", "", "", domain.ErrorNotFound
	}

	networkID = server.InternalInterfaces[0].NetworkUuid
	subnetID = server.InternalInterfaces[0].SubnetUuid
	zoneID = common.Zone(server.ZoneId)

	if subnetID == "" {
		logger.Errorf("[ERROR] - GetServerNetworkInfo: failed to get network information, subnetID: %s", subnetID)
		return "", "", "", "", domain.ErrorNotFound
	}

	subnetCIDR, err = m.getSubnetCIDR(ctx, networkID, subnetID)
	if err != nil {
		logger.Errorf("[ERROR] - GetServerNetworkInfo: failed to get subnet CIDR: %v", err)
		return "", "", "", "", err
	}

	return zoneID, networkID, subnetID, subnetCIDR, nil
}

func (m *vngCloudRepository) GetServerByID(ctx context.Context, serverID string) (*entityv2.Server, error) {
	logger := contexts.NewContext(ctx).Log()

	opt := computev2.NewGetServerByIdRequest(serverID)
	server, sdkErr := m.client.VServerGateway().V2().ComputeService().GetServerById(opt.AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - GetServerByID: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return nil, domain.SDKError(sdkErr)
	}
	return server, nil
}

func (m *vngCloudRepository) getSubnetCIDR(ctx context.Context, networkId, subnetID string) (string, error) {
	subnet, err := m.GetSubnetByID(ctx, networkId, subnetID)
	if err != nil {
		return "", err
	}
	if subnet == nil {
		return "", domain.ErrorNotFound
	}
	return subnet.Cidr, nil
}

func (m *vngCloudRepository) ListServerBySecgroupID(ctx context.Context, secgroupID string) (*entityv2.ListServers, error) {
	logger := contexts.NewContext(ctx).Log()

	opt := networkv2.NewListAllServersBySecgroupIdRequest(secgroupID)
	servers, sdkErr := m.client.VServerGateway().V2().NetworkService().ListAllServersBySecgroupId(opt.AddUserAgent(m.userAgent))
	if sdkErr != nil {
		logger.Error("[ERROR] - ListServerBySecgroupID: ", sdkErr, ", params: ", sdkErr.GetListParameters())
		return nil, domain.SDKError(sdkErr)
	}
	return servers, nil
}

func (m *vngCloudRepository) WaitForServerActive(ctx context.Context, serverID string) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Waiting for server %s to be ready", domain.WaitIcon, serverID)

	var server *entityv2.Server
	err := wait.ExponentialBackoff(wait.Backoff{
		Duration: 5 * time.Second,
		Factor:   1.2,
		Steps:    30,
	}, func() (done bool, err error) {
		var _err error
		server, _err = m.GetServerByID(ctx, serverID)
		if _err != nil {
			logger.Errorf("Error getting server %s when wait active: %v", serverID, _err)
			return false, _err
		}
		if strings.ToUpper(server.Status) == consts.ACTIVE_LOADBALANCER_STATUS {
			logger.Infof("%s Server %s is ready", domain.ReadyIcon, serverID)
			return true, nil
		}
		if strings.ToUpper(server.Status) == consts.ERROR_LOADBALANCER_STATUS {
			logger.Errorf("Server %s is in error status", serverID)
			return true, errors.New("server status is error")
		}

		logger.Infof("%s Server %s is not ready yet, waiting...", domain.WaitIcon, serverID)
		return false, nil
	})

	if wait.Interrupted(err) {
		logger.Errorf("timeout waiting for the loadbalancer %s with lb status %s", serverID, server.Status)
	}

	return err
}
