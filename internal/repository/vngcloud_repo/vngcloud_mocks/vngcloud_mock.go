package vngcloud_mocks

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/anngdinh/operator-helper/contexts"
	clone "github.com/huandu/go-clone"
	"github.com/pkg/errors"
	entityv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/entity"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/common"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/inter"
	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	networkv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/network/v2"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/consts"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/errs"
)

func randID() string {
	number := randRange(1000000, 3999999)
	return fmt.Sprint(number)
}

func randIP() string {
	return fmt.Sprintf("1.1.%d.%d", randRange(1, 255), randRange(1, 255))
}

func randRange(min, max int) int {
	return rand.IntN(max-min) + min
}

var _ repository.VngCloudRepository = &MockProvider{}

type wrapListener struct {
	*entityv2.Listener
	lbID string
}

type wrapPool struct {
	*entityv2.Pool
	lbID string
}
type wrapPolicy struct {
	*entityv2.Policy
	lbID       string
	listenerID string
}
type wrapServer struct {
	*entityv2.Server
}
type wrapSecgroup struct {
	*entityv2.Secgroup
}
type wrapSecgroupRule struct {
	*entityv2.SecgroupRule
}

type wrapCertificate struct {
	*entityv2.Certificate
}

type wrapSubnet struct {
	*entityv2.Subnet
}

type MockProvider struct {
	// securityGroups []*objects.Secgroup
	projectID string
	userID    int
	netID     string
	netCIDR   string

	subnet []*wrapSubnet

	loadBalancers []*entityv2.LoadBalancer
	listeners     []*wrapListener
	pools         []*wrapPool
	policies      []*wrapPolicy
	tags          map[string](map[string]string)
	servers       []*wrapServer
	secgroups     []*wrapSecgroup
	secgroupRules []*wrapSecgroupRule
	certs         []*wrapCertificate

	// glb
	glbs            []*entityv2.GlobalLoadBalancer
	globalListeners []*wrapGlobalListener
	globalPools     []*wrapGlobalPool

	mu            sync.Mutex
	WaitAfterTime time.Duration

	once sync.Once
}

func NewMockProvider() *MockProvider {

	return &MockProvider{
		projectID: MockProjectID,
		netID:     MockNetID,
		netCIDR:   MockNetCIDR,

		subnet: [](*wrapSubnet){
			&wrapSubnet{
				Subnet: &entityv2.Subnet{
					NetworkId: MockNetID,
					Id:        MockSubnetID,
					Name:      MockSubnetID,
					Cidr:      MapSubnetToCIDR[MockSubnetID],
					ZoneID:    MapSubnetToZone[MockSubnetID],
				},
			},
			&wrapSubnet{
				Subnet: &entityv2.Subnet{
					NetworkId: MockNetID,
					Id:        MockSubnetID_1b_1,
					Name:      MockSubnetID_1b_1,
					Cidr:      MapSubnetToCIDR[MockSubnetID_1b_1],
					ZoneID:    MapSubnetToZone[MockSubnetID_1b_1],
				},
			},
			&wrapSubnet{
				Subnet: &entityv2.Subnet{
					NetworkId: MockNetID,
					Id:        MockSubnetID_1b_2,
					Name:      MockSubnetID_1b_2,
					Cidr:      MapSubnetToCIDR[MockSubnetID_1b_2],
					ZoneID:    MapSubnetToZone[MockSubnetID_1b_2],
				},
			},
		},
		loadBalancers: make([]*entityv2.LoadBalancer, 0),
		listeners:     make([]*wrapListener, 0),
		pools:         make([]*wrapPool, 0),
		policies:      make([]*wrapPolicy, 0),
		tags:          make(map[string](map[string]string)),
		servers:       make([]*wrapServer, 0),
		secgroups:     make([]*wrapSecgroup, 0),
		secgroupRules: make([]*wrapSecgroupRule, 0),
		certs:         make([]*wrapCertificate, 0),

		WaitAfterTime: 0,
	}
}

func (m *MockProvider) Init(_ []string) error {
	var err error
	m.once.Do(func() {
		// add server mock
		serverIDs := []string{ServerId1, ServerId2, ServerId3, ServerId4}
		for _, id := range serverIDs {
			m.servers = append(m.servers, &wrapServer{
				Server: &entityv2.Server{
					Uuid:               id,
					BootVolumeId:       "",
					CreatedAt:          time.Now().Format(time.RFC3339),
					EncryptionVolume:   false,
					Licence:            false,
					Location:           "",
					Metadata:           "",
					MigrateState:       "",
					Name:               "mock-server-1",
					Product:            "",
					ServerGroupId:      "",
					ServerGroupName:    "",
					SshKeyName:         "",
					Status:             consts.ACTIVE_LOADBALANCER_STATUS,
					StopBeforeMigrate:  false,
					User:               "",
					Image:              entityv2.Image{},
					Flavor:             entityv2.Flavor{},
					SecGroups:          []entityv2.ServerSecgroup{},
					ExternalInterfaces: []entityv2.NetworkInterface{},
					InternalInterfaces: []entityv2.NetworkInterface{
						{
							NetworkUuid: MockNetID,
							SubnetUuid:  MapServerIdToSubnet[id],
						},
					},
					ZoneId: MapSubnetToZone[MapServerIdToSubnet[id]],
				},
			})
		}

		for _, id := range MockCerts {
			m.certs = append(m.certs, &wrapCertificate{
				Certificate: &entityv2.Certificate{
					UUID:            id,
					Name:            id,
					InUse:           false,
					CertificateType: "TLS/SSL",
				},
			})
		}
	})
	return err
}

// Reset clears all test-created state (load balancers, listeners, pools,
// policies, tags, security groups, certs created post-Init, GLBs) and
// detaches any security groups that tests attached to baseline servers.
// Baseline state populated by NewMockProvider/Init (subnets, servers,
// initial certs) is preserved.
//
// Call this in a BeforeEach to give each spec a clean slate without
// depending on the previous spec's controller-driven cleanup completing
// in time. Avoids cross-test leakage when finalizer chains are slow.
func (m *MockProvider) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.loadBalancers = nil
	m.listeners = nil
	m.pools = nil
	m.policies = nil
	m.tags = nil
	m.secgroups = nil
	m.secgroupRules = nil
	m.glbs = nil
	m.globalListeners = nil
	m.globalPools = nil

	// Detach any test-attached security groups from baseline servers,
	// but keep the servers themselves.
	for _, s := range m.servers {
		if s.Server != nil {
			s.Server.SecGroups = []entityv2.ServerSecgroup{}
		}
	}
}

func (m *MockProvider) GetProjectID() string {
	return m.projectID
}

func (m *MockProvider) GetUserId() int {
	return m.userID
}

func (m *MockProvider) GetNetworkID() string {
	return m.netID
}

func (m *MockProvider) GetNetworkCIDR() string {
	return m.netCIDR
}

func (m *MockProvider) GetDefaultSubnetID() string {
	return m.subnet[0].Id
}

func (m *MockProvider) GetDefaultSubnetCIDR() string {
	return m.subnet[0].Cidr
}

func (m *MockProvider) GetDefaultZone() common.Zone {
	return common.Zone(m.subnet[0].ZoneID)
}

func (m *MockProvider) subnetToCIDR(subnetID string) string {
	for _, s := range m.subnet {
		if s.Id == subnetID {
			return s.Cidr
		}
	}
	return ""
}
func (m *MockProvider) subnetToZone(subnetID string) string {
	for _, s := range m.subnet {
		if s.Id == subnetID {
			return s.ZoneID
		}
	}
	return ""
}

func (m *MockProvider) ListLoadBalancerPackageByZone(ctx context.Context, zone common.Zone) (*entityv2.ListLoadBalancerPackages, error) {
	return &entityv2.ListLoadBalancerPackages{
		Items: []*entityv2.LoadBalancerPackage{
			{
				UUID: MockL7PackageId,
				Name: MockL7PackageName,
				Type: string(loadbalancerv2.LoadBalancerTypeLayer7),
			},
			{
				UUID: MockL4PackageId,
				Name: MockL4PackageName,
				Type: string(loadbalancerv2.LoadBalancerTypeLayer4),
			},
		},
	}, nil
}

func (m *MockProvider) GetServerNetworkInfo(ctx context.Context, serverID string) (zoneID common.Zone, networkID, subnetID, subnetCIDR string, err error) {
	logger := contexts.NewContext(ctx).Log()

	server, sdkErr := m.GetServerByID(ctx, serverID)
	if sdkErr != nil {
		return "", "", "", "", sdkErr
	}
	if server == nil {
		logger.Errorf("[ERROR] - GetServerNetworkInfo: server not found")
		return "", "", "", "", domain.ErrorNotFound
	}

	networkID = server.InternalInterfaces[0].NetworkUuid
	subnetID = server.InternalInterfaces[0].SubnetUuid
	zoneID = common.Zone(server.ZoneId)

	if networkID == "" || subnetID == "" {
		logger.Errorf("[ERROR] - GetServerNetworkInfo: failed to get network information, netID: %s, subnetID: %s", networkID, subnetID)
		return "", "", "", "", domain.ErrorNotFound
	}

	subnet, err := m.GetSubnetByID(ctx, networkID, subnetID)
	if err != nil {
		return "", "", "", "", err
	}
	if subnet == nil {
		logger.Errorf("[ERROR] - GetServerNetworkInfo: subnet not found")
		return "", "", "", "", domain.ErrorNotFound
	}
	subnetCIDR = subnet.Cidr

	return zoneID, networkID, subnetID, subnetCIDR, nil
}

func (m *MockProvider) GetAllSubnetCIRDs(ctx context.Context, providerIDs []string) ([]string, error) {
	// get all subnet CIDRs of all instanceIDs (providerIDs)
	logger := contexts.NewContext(ctx).Log()
	subnetCIDRs := make([]string, 0)
	for _, providerID := range providerIDs {
		server, err := m.GetServerByID(ctx, providerID)
		if err != nil {
			logger.Errorf("Error getting server %s: %v", providerID, err)
			continue
		}
		if server == nil {
			logger.Errorf("Server %s not found", providerID)
			continue
		}
		if len(server.InternalInterfaces) == 0 {
			logger.Errorf("Server %s has no internal interfaces", providerID)
			continue
		}
		subnetID := server.InternalInterfaces[0].SubnetUuid
		if subnetID == "" {
			logger.Errorf("Server %s has no subnet ID", providerID)
			continue
		}
		subnet, err := m.GetSubnetByID(ctx, server.InternalInterfaces[0].NetworkUuid, subnetID)
		if err != nil {
			logger.Errorf("Error getting subnet %s: %v", subnetID, err)
			continue
		}
		if subnet == nil {
			logger.Errorf("Subnet %s not found", subnetID)
			continue
		}
		if !slices.Contains(subnetCIDRs, subnet.Cidr) {
			subnetCIDRs = append(subnetCIDRs, subnet.Cidr)
		}
	}
	return subnetCIDRs, nil
}

// // --------------------------- Security Group ---------------------------

func (m *MockProvider) ListSecurityGroups(ctx context.Context) (*entityv2.ListSecgroups, error) {
	secgroups := make([]*entityv2.Secgroup, 0)
	for _, s := range m.secgroups {
		secgroups = append(secgroups, clone.Clone(s.Secgroup).(*entityv2.Secgroup))
	}
	return &entityv2.ListSecgroups{
		Items: secgroups,
	}, nil
}

func (m *MockProvider) UpdateSecGroupsOfServer(ctx context.Context, instanceID string, secgroups []string) (*entityv2.Server, error) {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request update security groups of server %s", domain.RequestIcon, instanceID)

	var server *entityv2.Server
	for _, s := range m.servers {
		if s.Uuid == instanceID {
			server = s.Server
			break
		}
	}
	if server == nil {
		logger.Errorf("[ERROR] - UpdateSecGroupsOfServer: server not found")
		return nil, domain.ErrorNotFound
	}

	// check secgroups is valid
	secG := make([]entityv2.ServerSecgroup, 0)
	for _, secgroupID := range secgroups {
		s, err := m.GetSecurityGroup(ctx, secgroupID)
		if err != nil {
			return nil, err
		}
		secG = append(secG, entityv2.ServerSecgroup{
			Uuid: s.Id,
			Name: s.Name,
		})
	}

	server.SecGroups = secG
	return clone.Clone(server).(*entityv2.Server), nil
}

func (m *MockProvider) GetSecurityGroup(ctx context.Context, secgroupID string) (*entityv2.Secgroup, error) {
	logger := contexts.NewContext(ctx).Log()
	for _, s := range m.secgroups {
		if s.Id == secgroupID {
			return clone.Clone(s.Secgroup).(*entityv2.Secgroup), nil
		}
	}
	logger.Errorf("[ERROR] - GetSecurityGroup: security group not found")
	// Match production VNGCloud SDK message so domain.IsSecurityGroupNotFound
	// (prefix-matches "Cannot get security group with id secg-") works.
	return nil, fmt.Errorf("Cannot get security group with id %s", secgroupID)
}

func (m *MockProvider) DeleteSecurityGroup(ctx context.Context, secgroupID string) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request delete security group %s", domain.RequestIcon, secgroupID)
	// valid secgroupID
	servers, err := m.ListServerBySecgroupID(ctx, secgroupID)
	if err != nil {
		return err
	}

	if len(servers.Items) > 0 {
		return errs.NewNoNeedRequeue("Security group in use")
	}

	// delete secgroup
	newSecgroups := make([]*wrapSecgroup, 0)
	for i, s := range m.secgroups {
		if s.Id != secgroupID {
			newSecgroups = append(newSecgroups, m.secgroups[i])
		}
	}

	m.mu.Lock()
	m.secgroups = newSecgroups
	m.mu.Unlock()
	return nil
}

func (m *MockProvider) CreateSecurityGroup(ctx context.Context, name string, description string) (*entityv2.Secgroup, error) {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request create security group %s", domain.RequestIcon, name)

	secgroupID := "secgroup-" + randID()
	newSecgroup := &wrapSecgroup{
		&entityv2.Secgroup{
			Id:          secgroupID,
			Name:        name,
			Description: description,
			Status:      consts.ACTIVE_LOADBALANCER_STATUS,
		},
	}

	m.mu.Lock()
	m.secgroups = append(m.secgroups, newSecgroup)
	m.mu.Unlock()
	return clone.Clone(newSecgroup.Secgroup).(*entityv2.Secgroup), nil
}

func (m *MockProvider) CreateSecurityGroupRule(ctx context.Context, secgroupID string, opts networkv2.ICreateSecgroupRuleRequest) (*entityv2.SecgroupRule, error) {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request create security group rule for security group %s", domain.RequestIcon, secgroupID)

	// valid secgroupID
	_, err := m.GetSecurityGroup(ctx, secgroupID)
	if err != nil {
		return nil, err
	}

	rule := opts.(*networkv2.CreateSecgroupRuleRequest)
	newRule := &wrapSecgroupRule{
		&entityv2.SecgroupRule{
			Id:             "sec-rule-" + randID(),
			SecgroupId:     secgroupID,
			Direction:      string(rule.Direction),
			EtherType:      string(rule.EtherType),
			Protocol:       string(rule.Protocol),
			Description:    rule.Description,
			RemoteIPPrefix: rule.RemoteIPPrefix,
			PortRangeMax:   rule.PortRangeMax,
			PortRangeMin:   rule.PortRangeMin,
		},
	}

	m.mu.Lock()
	m.secgroupRules = append(m.secgroupRules, newRule)
	m.mu.Unlock()
	return clone.Clone(newRule.SecgroupRule).(*entityv2.SecgroupRule), nil
}

func (m *MockProvider) DeleteSecurityGroupRule(ctx context.Context, secgroupID string, ruleID string) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request delete security group rule %s of security group %s", domain.RequestIcon, ruleID, secgroupID)

	// valid secgroupID
	_, err := m.GetSecurityGroup(ctx, secgroupID)
	if err != nil {
		return err
	}

	// delete rule
	isFound := false
	newRules := make([]*wrapSecgroupRule, 0)
	for i, r := range m.secgroupRules {
		if r.SecgroupId != secgroupID || r.Id != ruleID {
			newRules = append(newRules, m.secgroupRules[i])
		} else {
			isFound = true
		}
	}

	if !isFound {
		logger.Errorf("[ERROR] - DeleteSecurityGroupRule: rule not found")
		return domain.ErrorNotFound
	}

	m.mu.Lock()
	m.secgroupRules = newRules
	m.mu.Unlock()
	return nil
}

func (m *MockProvider) ListSecurityGroupRules(ctx context.Context, secgroupID string) (*entityv2.ListSecgroupRules, error) {
	// valid secgroupID
	_, err := m.GetSecurityGroup(ctx, secgroupID)
	if err != nil {
		return nil, err
	}

	rules := make([]*entityv2.SecgroupRule, 0)
	for _, r := range m.secgroupRules {
		if r.SecgroupId == secgroupID {
			rules = append(rules, clone.Clone(r.SecgroupRule).(*entityv2.SecgroupRule))
		}
	}
	return &entityv2.ListSecgroupRules{
		Items: rules,
	}, nil
}

// // --------------------------- Tags ---------------------------

func (m *MockProvider) ListTags(ctx context.Context, resourceID string) (*entityv2.ListTags, error) {
	tags := make(map[string]string)
	if t, ok := m.tags[resourceID]; ok {
		tags = t
	}

	tagItems := make([]*entityv2.Tag, 0)
	for k, v := range tags {
		tagItems = append(tagItems, &entityv2.Tag{
			Key:   k,
			Value: v,
		})
	}
	return &entityv2.ListTags{
		Items: tagItems,
	}, nil
}

func (m *MockProvider) CreateTags(ctx context.Context, resourceID string, tags map[string]string) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request create tags for resource %s", domain.RequestIcon, resourceID)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tags == nil {
		m.tags = make(map[string](map[string]string))
	}
	m.tags[resourceID] = tags
	return nil
}

func (m *MockProvider) UpdateTags(ctx context.Context, resourceID string, tags map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tags == nil {
		m.tags = make(map[string](map[string]string))
	}
	if m.tags[resourceID] == nil {
		m.tags[resourceID] = make(map[string]string)
	}
	for k, v := range tags {
		m.tags[resourceID][k] = v
	}
	return nil
}

func (m *MockProvider) GetSubnetByID(ctx context.Context, networkID, subnetID string) (*entityv2.Subnet, error) {
	logger := contexts.NewContext(ctx).Log()

	if networkID != m.netID {
		logger.Errorf("networkID %s not found", networkID)
		return nil, domain.ErrorNotFound
	}

	for _, s := range m.subnet {
		if s.Id == subnetID {
			return clone.Clone(s.Subnet).(*entityv2.Subnet), nil
		}
	}
	logger.Errorf("subnetID %s not found", subnetID)
	return nil, domain.ErrorNotFound
}

// // --------------------------- Server ---------------------------

func (m *MockProvider) GetServerByID(ctx context.Context, serverID string) (*entityv2.Server, error) {
	logger := contexts.NewContext(ctx).Log()
	for _, s := range m.servers {
		if s.Uuid == serverID {
			return clone.Clone(s.Server).(*entityv2.Server), nil
		}
	}
	logger.Errorf("serverID %s not found", serverID)
	// Match production VNGCloud SDK message so domain.IsServerNotFound
	// (prefix-matches "Cannot get server with id") works.
	return nil, fmt.Errorf("Cannot get server with id %s", serverID)
}

func (m *MockProvider) WaitForServerActive(ctx context.Context, serverID string) error {
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

func (m *MockProvider) ListServerBySecgroupID(ctx context.Context, secgroupID string) (*entityv2.ListServers, error) {
	// valid secgroupID
	_, err := m.GetSecurityGroup(ctx, secgroupID)
	if err != nil {
		return nil, err
	}

	// get servers by secgroup
	servers := make([]*entityv2.Server, 0)
	for _, s := range m.servers {
		for _, sg := range s.SecGroups {
			if sg.Uuid == secgroupID {
				servers = append(servers, clone.Clone(s.Server).(*entityv2.Server))
				break
			}
		}
	}
	return &entityv2.ListServers{
		Items: servers,
	}, nil
}

// --------------------------- Load Balancer ---------------------------

func (m *MockProvider) ListLoadBalancers(ctx context.Context, tags []string) (*entityv2.ListLoadBalancers, error) {
	lbs := &entityv2.ListLoadBalancers{
		Items:     m.loadBalancers,
		Page:      1,
		PageSize:  len(m.loadBalancers),
		TotalPage: 1,
		TotalItem: len(m.loadBalancers),
	}
	return lbs, nil
}
func (m *MockProvider) GetLoadBalancerByID(ctx context.Context, lbID string) (*entityv2.LoadBalancer, error) {
	logger := contexts.NewContext(ctx).Log()
	for _, lb := range m.loadBalancers {
		if lb.GetId() == lbID {
			return clone.Clone(lb).(*entityv2.LoadBalancer), nil
		}
	}
	logger.Errorf("[ERROR] - GetLoadBalancerByID: load balancer not found: %s", lbID)
	// Match the production VNGCloud SDK's not-found message so callers using
	// domain.IsLoadBalancerNotFound (which prefix-matches this exact phrase)
	// behave the same against the mock as against the real backend.
	return nil, fmt.Errorf("Cannot get load balancer with id %s", lbID)
}

func (m *MockProvider) GetLoadBalancerByName(ctx context.Context, name string) (*entityv2.LoadBalancer, error) {
	allLBs, err := m.ListLoadBalancers(ctx, nil)
	if err != nil {
		return nil, err
	}
	for _, lb := range allLBs.Items {
		if lb.Name == name {
			return clone.Clone(lb).(*entityv2.LoadBalancer), nil
		}
	}
	return nil, nil
}

func (m *MockProvider) CreateLoadBalancer(ctx context.Context, lbOptions loadbalancerv2.ICreateLoadBalancerRequest) (*entityv2.LoadBalancer, error) {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request create load balancer.", domain.RequestIcon)
	if lbOptions == nil {
		return nil, domain.ErrorInvalidInput
	}

	var lbOpt *loadbalancerv2.CreateLoadBalancerRequest
	if opt, ok := lbOptions.(*loadbalancerv2.CreateLoadBalancerRequest); ok {
		lbOpt = opt
	} else {
		return nil, domain.ErrorInvalidInput
	}

	lbID := "lb-" + randID()
	newLB := &entityv2.LoadBalancer{
		UUID:               lbID,
		Name:               lbOpt.Name,
		Address:            randIP(),
		Type:               string(lbOpt.Type),
		LoadBalancerSchema: string(lbOpt.Scheme),
		PackageID:          lbOpt.PackageID,
		BackendSubnetID:    lbOpt.SubnetID,
		DisplayStatus:      consts.CREATED_LOADBALANCER_STATUS,
		PrivateSubnetID:    lbOpt.SubnetID,
		PrivateSubnetCidr:  m.subnetToCIDR(lbOpt.SubnetID),
		ZoneID:             m.subnetToZone(lbOpt.SubnetID),
		DisplayType:        consts.CREATED_LOADBALANCER_STATUS,
		Description:        "????????",
		Location:           "????????",
		CreatedAt:          time.Now().Format(time.RFC3339),
		UpdatedAt:          time.Now().Format(time.RFC3339),
		ProgressStatus:     consts.CREATED_LOADBALANCER_STATUS,
		Status:             consts.ACTIVE_LOADBALANCER_STATUS,
		Internal:           lbOpt.Scheme == loadbalancerv2.InternalLoadBalancerScheme,
	}

	m.mu.Lock()
	m.loadBalancers = append(m.loadBalancers, newLB)
	m.mu.Unlock()

	defaultPoolID := ""
	if lbOpt.Pool != nil {
		pool, _ := m.CreatePool(ctx, lbID, lbOpt.Pool.WithLoadBalancerId(newLB.UUID))
		defaultPoolID = pool.UUID
	}

	if lbOpt.Listener != nil {
		_, _ = m.CreateListener(ctx, lbID, lbOpt.Listener.WithLoadBalancerId(newLB.UUID).WithDefaultPoolId(defaultPoolID))
	}

	m.updatingStatus(newLB.UUID)
	go m.readyAfterTime(newLB.UUID)

	return &entityv2.LoadBalancer{
		UUID: newLB.UUID,
	}, nil
}

func (m *MockProvider) updatingStatus(lbID string) {
	logger := contexts.NewContext(context.TODO()).Log()
	var o *entityv2.LoadBalancer
	for _, lb := range m.loadBalancers {
		if lb.GetId() == lbID {
			o = lb
			break
		}
	}
	if o == nil {
		logger.Error("Load Balancer not found")
		return
	}

	if m.WaitAfterTime == 0 {
		m.mu.Lock()
		o.UpdatedAt = time.Now().Format(time.RFC3339)
		o.DisplayStatus = consts.ACTIVE_LOADBALANCER_STATUS
		o.ProgressStatus = consts.CREATED_LOADBALANCER_STATUS
		if o.Name == MockLBNameError {
			o.DisplayStatus = consts.ERROR_LOADBALANCER_STATUS
		}
		m.mu.Unlock()
		return
	}

	m.mu.Lock()
	o.DisplayStatus = consts.CREATED_LOADBALANCER_STATUS
	o.ProgressStatus = consts.CREATED_LOADBALANCER_STATUS
	m.mu.Unlock()
}

func (m *MockProvider) readyAfterTime(lbID string) {
	if m.WaitAfterTime == 0 {
		return
	}
	logger := contexts.NewContext(context.TODO()).Log()
	var o *entityv2.LoadBalancer
	for _, lb := range m.loadBalancers {
		if lb.GetId() == lbID {
			o = lb
			break
		}
	}
	if o == nil {
		logger.Error("Load Balancer not found")
		return
	}

	time.Sleep(m.WaitAfterTime)
	m.mu.Lock()
	o.UpdatedAt = time.Now().Format(time.RFC3339)
	o.DisplayStatus = consts.ACTIVE_LOADBALANCER_STATUS
	o.ProgressStatus = consts.CREATED_LOADBALANCER_STATUS
	if o.Name == MockLBNameError {
		o.DisplayStatus = consts.ERROR_LOADBALANCER_STATUS
	}
	m.mu.Unlock()
}

func (m *MockProvider) DeleteLoadBalancer(ctx context.Context, lbID string) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request delete load balancer %s", domain.RequestIcon, lbID)
	newListeners := make([]*wrapListener, 0)
	for i, lb := range m.listeners {
		if lb.lbID != lbID {
			newListeners = append(newListeners, m.listeners[i])
		}
	}
	m.listeners = newListeners

	newPools := make([]*wrapPool, 0)
	for i, lb := range m.pools {
		if lb.lbID != lbID {
			newPools = append(newPools, m.pools[i])
		}
	}
	m.pools = newPools

	m.mu.Lock()
	defer m.mu.Unlock()

	newLBs := make([]*entityv2.LoadBalancer, 0)
	for i, lb := range m.loadBalancers {
		if lb.GetId() != lbID {
			newLBs = append(newLBs, m.loadBalancers[i])
		}
	}
	if len(newLBs) == len(m.loadBalancers) {
		logger.Errorf("[ERROR] - DeleteLoadBalancer: load balancer not found")
		return domain.ErrorNotFound
	}

	m.loadBalancers = newLBs
	return nil
}
func (m *MockProvider) ResizeLoadBalancer(ctx context.Context, lbID, packageID string) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Error("not implemented yet", "ResizeLoadBalancer")
	return domain.ErrorNotImplemented
}
func (m *MockProvider) WaitForLBActive(ctx context.Context, lbID string) (*entityv2.LoadBalancer, error) {
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

func (m *MockProvider) CreateInterLoadBalancer(ctx context.Context, lbOptions inter.ICreateLoadBalancerRequest) (*entityv2.LoadBalancer, error) {
	logger := contexts.NewContext(ctx).Log()
	logger.Error("not implemented yet", "CreateInterLoadBalancer")
	return nil, domain.ErrorNotImplemented
}

// --------------------------- Listener ---------------------------

func (m *MockProvider) GetListenerById(ctx context.Context, lbID, listenerID string) (*entityv2.Listener, error) {
	logger := contexts.NewContext(ctx).Log()
	for _, l := range m.listeners {
		if l.lbID == lbID && l.GetId() == listenerID {
			return clone.Clone(l.Listener).(*entityv2.Listener), nil
		}
	}
	logger.Errorf("[ERROR] - GetListenerById: listener not found")
	return nil, domain.ErrorNotFound
}

//	func (m *MockProvider) GetListenerByPort(ctx context.Context, lbID string, port int) (*objects.Listener, error) {
//		logger.Error("not implemented yet", "GetListenerByPort")
//		return nil, domain.ErrorNotImplemented
//	}
func (m *MockProvider) CreateListener(ctx context.Context, lbID string, opt loadbalancerv2.ICreateListenerRequest) (*entityv2.Listener, error) {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request create listener of load balancer %s", domain.RequestIcon, lbID)
	listener := opt.ToRequestBody().(*loadbalancerv2.CreateListenerRequest)
	newListener := &wrapListener{
		lbID: lbID,
		Listener: &entityv2.Listener{
			UUID:                            "lis-" + randID(),
			Name:                            listener.ListenerName,
			Description:                     "????????",
			Protocol:                        string(listener.ListenerProtocol),
			ProtocolPort:                    listener.ListenerProtocolPort,
			ConnectionLimit:                 0,
			DefaultPoolId:                   "",
			DefaultPoolName:                 "",
			TimeoutClient:                   listener.TimeoutClient,
			TimeoutMember:                   listener.TimeoutMember,
			TimeoutConnection:               listener.TimeoutConnection,
			AllowedCidrs:                    listener.AllowedCidrs,
			DisplayStatus:                   consts.ACTIVE_LOADBALANCER_STATUS,
			CreatedAt:                       time.Now().Format(time.RFC3339),
			UpdatedAt:                       time.Now().Format(time.RFC3339),
			ProgressStatus:                  consts.ACTIVE_LOADBALANCER_STATUS,
			InsertHeaders:                   nil,
			CertificateAuthorities:          nil,
			DefaultCertificateAuthority:     nil,
			ClientCertificateAuthentication: nil,
		},
	}
	if listener.ListenerProtocol == loadbalancerv2.ListenerProtocolHTTPS ||
		listener.ListenerProtocol == loadbalancerv2.ListenerProtocolHTTP {
		if listener.InsertHeaders == nil {
			return nil, errors.New("Missing Headers For HTTP/HTTPS Listener")
		}
		newListener.InsertHeaders = *listener.InsertHeaders
	}
	if listener.ListenerProtocol == loadbalancerv2.ListenerProtocolHTTPS {
		if listener.DefaultCertificateAuthority == nil || *listener.DefaultCertificateAuthority == "" {
			return nil, errors.New("Missing Default Certificate Authority For HTTPS Listener")
		}
		if listener.CertificateAuthorities == nil {
			return nil, errors.New("Missing Certificate Authorities For HTTPS Listener")
		}
		// if listener.ClientCertificate == nil {
		// 	return nil, errors.New("Missing Client Certificate For HTTPS Listener")
		// }
		newListener.CertificateAuthorities = *listener.CertificateAuthorities
		newListener.DefaultCertificateAuthority = listener.DefaultCertificateAuthority
		newListener.ClientCertificateAuthentication = listener.ClientCertificate
	}
	if listener.DefaultPoolId != nil && *listener.DefaultPoolId != "" {
		newListener.DefaultPoolId = *listener.DefaultPoolId
		pool, _ := m.GetPoolByID(ctx, lbID, newListener.DefaultPoolId)
		if pool != nil {
			newListener.DefaultPoolName = pool.Name
		}
	}

	m.mu.Lock()
	m.listeners = append(m.listeners, newListener)
	m.mu.Unlock()

	m.updatingStatus(lbID)
	go m.readyAfterTime(lbID)
	return &entityv2.Listener{
		UUID: newListener.UUID,
	}, nil
}
func (m *MockProvider) ListListenerOfLB(ctx context.Context, lbID string) (*entityv2.ListListeners, error) {
	listeners := make([]*entityv2.Listener, 0)
	for _, l := range m.listeners {
		if l.lbID == lbID {
			listeners = append(listeners, clone.Clone(l.Listener).(*entityv2.Listener))
		}
	}
	return &entityv2.ListListeners{
		Items: listeners,
	}, nil
}
func (m *MockProvider) DeleteListener(ctx context.Context, lbID, listenerID string) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request delete listener %s of load balancer %s", domain.RequestIcon, listenerID, lbID)
	isFound := false
	newListeners := make([]*wrapListener, 0)
	for i, l := range m.listeners {
		if l.lbID != lbID || l.GetId() != listenerID {
			newListeners = append(newListeners, m.listeners[i])
		} else {
			isFound = true
		}
	}
	if !isFound {
		logger.Errorf("[ERROR] - DeleteListener: listener not found")
		return domain.ErrorNotFound
	}
	m.listeners = newListeners

	m.updatingStatus(lbID)
	go m.readyAfterTime(lbID)
	return nil
}
func (m *MockProvider) UpdateListener(ctx context.Context, lbID, listenerID string, opt loadbalancerv2.IUpdateListenerRequest) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request update listener %s of load balancer %s", domain.RequestIcon, listenerID, lbID)
	updateOpt := opt.ToRequestBody().(*loadbalancerv2.UpdateListenerRequest)
	var listener *wrapListener
	for _, l := range m.listeners {
		if l.lbID == lbID && l.GetId() == listenerID {
			listener = l
			break
		}
	}
	if listener == nil {
		logger.Errorf("[ERROR] - UpdateListener: listener not found")
		return domain.ErrorNotFound
	}
	listener.TimeoutClient = updateOpt.TimeoutClient
	listener.TimeoutConnection = updateOpt.TimeoutConnection
	listener.TimeoutMember = updateOpt.TimeoutMember
	listener.AllowedCidrs = updateOpt.AllowedCidrs

	if listener.Protocol == string(loadbalancerv2.HealthCheckProtocolHTTPs) ||
		listener.Protocol == string(loadbalancerv2.HealthCheckProtocolHTTP) {
		if updateOpt.InsertHeaders == nil {
			return errors.New("Missing Headers For HTTP/HTTPS Listener")
		}
		listener.InsertHeaders = *updateOpt.InsertHeaders
	}
	if listener.Protocol == string(loadbalancerv2.HealthCheckProtocolHTTPs) {
		if updateOpt.DefaultCertificateAuthority == nil || *updateOpt.DefaultCertificateAuthority == "" {
			return errors.New("Missing Default Certificate Authority For HTTPS Listener")
		}
		if updateOpt.CertificateAuthorities == nil {
			return errors.New("Missing Certificate Authorities For HTTPS Listener")
		}
		// if updateOpt.ClientCertificate == nil {
		// 	return errors.New("Missing Client Certificate For HTTPS Listener")
		// }
		listener.CertificateAuthorities = *updateOpt.CertificateAuthorities
		listener.DefaultCertificateAuthority = updateOpt.DefaultCertificateAuthority
		listener.ClientCertificateAuthentication = updateOpt.ClientCertificate
	}

	m.updatingStatus(lbID)
	go m.readyAfterTime(lbID)
	return nil
}

// --------------------------- Policy ---------------------------

//	func (m *MockProvider) GetPolicyByName(ctx context.Context, lbID, listenerID, name string) (*objects.Policy, error) {
//		logger.Error("not implemented yet", "GetPolicyByName")
//		return nil, domain.ErrorNotImplemented
//	}
func (m *MockProvider) CreatePolicy(ctx context.Context, lbID, listenerID string, opt loadbalancerv2.ICreatePolicyRequest) (*entityv2.Policy, error) {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request create policy of listener %s of load balancer %s", domain.RequestIcon, listenerID, lbID)
	lb, err := m.GetLoadBalancerByID(ctx, lbID)
	if err != nil {
		return nil, err
	}
	if lb == nil {
		logger.Errorf("[ERROR] - CreatePolicy: load balancer not found")
		return nil, domain.ErrorNotFound
	}
	listeners, err := m.ListListenerOfLB(ctx, lbID)
	if err != nil {
		return nil, err
	}
	if listeners == nil {
		logger.Errorf("[ERROR] - CreatePolicy: listener not found")
		return nil, domain.ErrorNotFound
	}
	isFound := false
	for _, l := range listeners.Items {
		if l.GetId() == listenerID {
			isFound = true
			break
		}
	}
	if !isFound {
		logger.Errorf("[ERROR] - CreatePolicy: listener not found")
		return nil, domain.ErrorNotFound
	}

	// get number of existing policies
	listenerPolicies, err := m.ListPolicyOfListener(ctx, lbID, listenerID)
	if err != nil {
		return nil, err
	}
	lastPosition := 1
	for _, p := range listenerPolicies.Items {
		if p.Position >= lastPosition {
			lastPosition = p.Position + 1
		}
	}

	policy := opt.(*loadbalancerv2.CreatePolicyRequest)
	newPolicy := &wrapPolicy{
		lbID:       lbID,
		listenerID: listenerID,
		Policy: &entityv2.Policy{
			UUID:             "pol-" + randID(),
			Name:             policy.Name,
			Description:      "????????",
			RedirectPoolID:   policy.RedirectPoolID,
			Action:           string(policy.Action),
			RedirectURL:      policy.RedirectURL,
			RedirectPoolName: "",
			RedirectHTTPCode: policy.RedirectHTTPCode,
			KeepQueryString:  policy.KeepQueryString,
			L7Rules:          nil,
			Position:         lastPosition,
			DisplayStatus:    consts.ACTIVE_LOADBALANCER_STATUS,
			CreatedAt:        time.Now().Format(time.RFC3339),
			UpdatedAt:        time.Now().Format(time.RFC3339),
			ProgressStatus:   consts.ACTIVE_LOADBALANCER_STATUS,
		},
	}
	if policy.RedirectPoolID != "" {
		pool, _ := m.GetPoolByID(ctx, lbID, policy.RedirectPoolID)
		if pool != nil {
			newPolicy.RedirectPoolName = pool.Name
		} else {
			logger.Errorf("[ERROR] - CreatePolicy: redirect pool not found")
			return nil, domain.ErrorNotFound
		}
	}
	newRules := make([]*entityv2.L7Rule, 0)
	for _, r := range policy.Rules {
		newRules = append(newRules, &entityv2.L7Rule{
			UUID:               "rule-" + randID(),
			CompareType:        string(r.CompareType),
			RuleValue:          r.RuleValue,
			RuleType:           string(r.RuleType),
			ProvisioningStatus: consts.ACTIVE_LOADBALANCER_STATUS,
			OperatingStatus:    consts.ACTIVE_LOADBALANCER_STATUS,
		})
	}
	newPolicy.L7Rules = newRules
	m.mu.Lock()
	m.policies = append(m.policies, newPolicy)
	m.mu.Unlock()

	m.updatingStatus(lbID)
	go m.readyAfterTime(lbID)
	return &entityv2.Policy{
		UUID: newPolicy.UUID,
	}, nil
}
func (m *MockProvider) ListPolicyOfListener(ctx context.Context, lbID, listenerID string) (*entityv2.ListPolicies, error) {
	policies := make([]*entityv2.Policy, 0)
	for _, p := range m.policies {
		if p.lbID == lbID && p.listenerID == listenerID {
			policies = append(policies, clone.Clone(p.Policy).(*entityv2.Policy))
		}
	}
	return &entityv2.ListPolicies{
		Items: policies,
	}, nil
}

//	func (m *MockProvider) GetPolicyByID(ctx context.Context, policyID string) (*objects.Policy, error) {
//		logger.Error("not implemented yet", "GetPolicyByID")
//		return nil, domain.ErrorNotImplemented
//	}
func (m *MockProvider) UpdatePolicy(ctx context.Context, lbID, listenerID, policyID string, opt loadbalancerv2.IUpdatePolicyRequest) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request update policy %s of listener %s of load balancer %s", domain.RequestIcon, policyID, listenerID, lbID)
	updateOpt := opt.(*loadbalancerv2.UpdatePolicyRequest)
	var policy *wrapPolicy
	for _, p := range m.policies {
		if p.lbID == lbID && p.listenerID == listenerID && p.UUID == policyID {
			policy = p
			break
		}
	}
	if policy == nil {
		logger.Errorf("[ERROR] - UpdatePolicy: policy not found")
		return domain.ErrorNotFound
	}
	policy.RedirectPoolID = updateOpt.RedirectPoolID
	policy.Action = string(updateOpt.Action)
	policy.RedirectURL = updateOpt.RedirectURL
	policy.RedirectHTTPCode = updateOpt.RedirectHTTPCode
	policy.KeepQueryString = updateOpt.KeepQueryString
	newRules := make([]*entityv2.L7Rule, 0)
	for _, r := range updateOpt.Rules {
		newRules = append(newRules, &entityv2.L7Rule{
			UUID:               "rule-" + randID(),
			CompareType:        string(r.CompareType),
			RuleValue:          r.RuleValue,
			RuleType:           string(r.RuleType),
			ProvisioningStatus: consts.ACTIVE_LOADBALANCER_STATUS,
			OperatingStatus:    consts.ACTIVE_LOADBALANCER_STATUS,
		})
	}
	policy.L7Rules = newRules

	m.updatingStatus(lbID)
	go m.readyAfterTime(lbID)
	return nil
}
func (m *MockProvider) DeletePolicy(ctx context.Context, lbID, listenerID, policyID string) error {
	isFound := false
	newPolicies := make([]*wrapPolicy, 0)
	for i, p := range m.policies {
		if p.lbID != lbID || p.listenerID != listenerID || p.UUID != policyID {
			newPolicies = append(newPolicies, m.policies[i])
		} else {
			isFound = true
		}
	}
	if !isFound {
		logger := contexts.NewContext(ctx).Log()
		logger.Errorf("[ERROR] - DeletePolicy: policy not found")
		return domain.ErrorNotFound
	}
	m.policies = newPolicies

	m.updatingStatus(lbID)
	go m.readyAfterTime(lbID)
	return nil
}

func (m *MockProvider) ReorderPolicies(ctx context.Context, lbID, listenerID string, policyIDs []string) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request reorder policies of listener %s of load balancer %s", domain.RequestIcon, listenerID, lbID)
	var listener *wrapListener
	for _, l := range m.listeners {
		if l.lbID == lbID && l.GetId() == listenerID {
			listener = l
			break
		}
	}
	if listener == nil {
		logger.Errorf("[ERROR] - ReorderPolicies: listener not found")
		return domain.ErrorNotFound
	}

	err := func() error {
		m.mu.Lock()
		defer m.mu.Unlock()

		// Build a map of policies for this listener
		policyMap := make(map[string]*wrapPolicy)
		otherPolicies := make([]*wrapPolicy, 0)
		for _, p := range m.policies {
			if p.lbID == lbID && p.listenerID == listenerID {
				policyMap[p.UUID] = p
			} else {
				otherPolicies = append(otherPolicies, p)
			}
		}

		// Verify all policyIDs exist
		for _, pID := range policyIDs {
			if _, exists := policyMap[pID]; !exists {
				logger.Errorf("[ERROR] - ReorderPolicies: policy %s not found", pID)
				return domain.ErrorNotFound
			}
		}

		// Reorder policies based on policyIDs order and update positions
		reorderedPolicies := make([]*wrapPolicy, 0, len(policyIDs))
		for i, pID := range policyIDs {
			policy := policyMap[pID]
			policy.Position = i + 1
			reorderedPolicies = append(reorderedPolicies, policy)
		}

		// Rebuild m.policies with other policies + reordered policies
		m.policies = append(otherPolicies, reorderedPolicies...)
		return nil
	}()
	if err != nil {
		return err
	}

	m.updatingStatus(lbID)
	go m.readyAfterTime(lbID)
	return nil
}

// --------------------------- Pool ---------------------------

//	func (m *MockProvider) GetPoolByName(ctx context.Context, lbID, name string) (*objects.Pool, error) {
//		logger.Error("not implemented yet", "GetPoolByName")
//		return nil, domain.ErrorNotImplemented
//	}
func (m *MockProvider) CreatePool(ctx context.Context, lbID string, opt loadbalancerv2.ICreatePoolRequest) (*entityv2.Pool, error) {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request create pool of load balancer %s", domain.RequestIcon, lbID)
	var (
		pool          *loadbalancerv2.CreatePoolRequest
		healthMonitor *loadbalancerv2.HealthMonitor
		member        []*loadbalancerv2.Member
	)
	pool = opt.ToRequestBody().(*loadbalancerv2.CreatePoolRequest)
	defaultPoolID := "pool-" + randID()

	lb, _ := m.GetLoadBalancerByID(ctx, lbID)
	if lb == nil {
		logger.Errorf("[ERROR] - CreatePool: load balancer not found")
		return nil, domain.ErrorNotFound
	}

	newPool := &wrapPool{
		lbID: lbID,
		Pool: &entityv2.Pool{
			UUID:              defaultPoolID,
			Name:              pool.PoolName,
			Protocol:          string(pool.PoolProtocol),
			Description:       "????????",
			LoadBalanceMethod: string(pool.Algorithm),
			Status:            consts.ACTIVE_LOADBALANCER_STATUS,
			Stickiness:        false,
			TLSEncryption:     false,
			Members:           nil,
			HealthMonitor:     nil,
		},
	}

	if pool.PoolProtocol == loadbalancerv2.PoolProtocolHTTP {
		if pool.Stickiness == nil {
			return nil, errors.New("Missing Stickiness For HTTP Pool")
		}
		if pool.TLSEncryption == nil {
			return nil, errors.New("Missing TLSEncryption For HTTP Pool")
		}
		newPool.TLSEncryption = *pool.TLSEncryption
		newPool.Stickiness = *pool.Stickiness
	}

	if pool.HealthMonitor != nil {
		healthMonitor = pool.HealthMonitor.ToRequestBody().(*loadbalancerv2.HealthMonitor)
		newHealthMonitor := &entityv2.HealthMonitor{
			Timeout:             healthMonitor.Timeout,
			CreatedAt:           time.Now().Format(time.RFC3339),
			UpdatedAt:           time.Now().Format(time.RFC3339),
			DomainName:          healthMonitor.DomainName,
			Interval:            healthMonitor.Interval,
			HealthyThreshold:    healthMonitor.HealthyThreshold,
			UnhealthyThreshold:  healthMonitor.UnhealthyThreshold,
			HealthCheckPath:     healthMonitor.HealthCheckPath,
			SuccessCode:         healthMonitor.SuccessCode,
			ProgressStatus:      consts.ACTIVE_LOADBALANCER_STATUS,
			DisplayStatus:       consts.ACTIVE_LOADBALANCER_STATUS,
			HealthCheckProtocol: string(healthMonitor.HealthCheckProtocol),
			HttpVersion:         nil,
			HealthCheckMethod:   nil,
		}
		if healthMonitor.HttpVersion != nil {
			newHealthMonitor.HttpVersion = ptr.To(string(*healthMonitor.HttpVersion))
		}
		if healthMonitor.HealthCheckMethod != nil {
			newHealthMonitor.HealthCheckMethod = ptr.To(string(*healthMonitor.HealthCheckMethod))
		}
		newPool.HealthMonitor = newHealthMonitor
	}

	if pool.Members != nil {
		for _, m := range pool.Members {
			member = append(member, m.ToRequestBody().(*loadbalancerv2.Member))
		}
		newMembers := &entityv2.ListMembers{
			Items: make([]*entityv2.Member, 0),
		}
		for _, m := range member {
			newMember := &entityv2.Member{
				UUID:           "mem-" + randID(),
				Address:        m.IpAddress,
				ProtocolPort:   m.Port,
				Weight:         m.Weight,
				MonitorPort:    m.MonitorPort,
				SubnetID:       lb.BackendSubnetID,
				Name:           m.Name,
				PoolID:         defaultPoolID,
				TypeCreate:     "????????",
				Backup:         false,
				DisplayStatus:  consts.ACTIVE_LOADBALANCER_STATUS,
				CreatedAt:      time.Now().Format(time.RFC3339),
				UpdatedAt:      time.Now().Format(time.RFC3339),
				CreatedBy:      "????????",
				ProgressStatus: consts.ACTIVE_LOADBALANCER_STATUS,
			}
			newMembers.Items = append(newMembers.Items, newMember)
		}
		newPool.Members = newMembers
	}

	m.mu.Lock()
	m.pools = append(m.pools, newPool)
	m.mu.Unlock()

	m.updatingStatus(lbID)
	go m.readyAfterTime(lbID)
	return &entityv2.Pool{
		UUID: newPool.UUID,
	}, nil
}
func (m *MockProvider) ListPool(ctx context.Context, lbID string) (*entityv2.ListPools, error) {
	pools := make([]*entityv2.Pool, 0)
	for _, p := range m.pools {
		if p.lbID == lbID {
			pools = append(pools, clone.Clone(p.Pool).(*entityv2.Pool))
		}
	}
	return &entityv2.ListPools{
		Items: pools,
	}, nil
}
func (m *MockProvider) UpdatePoolMembers(ctx context.Context, lbID, poolID string, members loadbalancerv2.IUpdatePoolMembersRequest) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request update pool members %s of load balancer %s", domain.RequestIcon, poolID, lbID)
	updateOpt := members.(*loadbalancerv2.UpdatePoolMembersRequest)
	var pool *wrapPool
	for _, p := range m.pools {
		if p.lbID == lbID && p.GetId() == poolID {
			pool = p
			break
		}
	}
	if pool == nil {
		logger.Errorf("[ERROR] - UpdatePoolMembers: pool not found")
		return domain.ErrorNotFound
	}
	newMembers := make([]*entityv2.Member, 0)
	for _, m := range updateOpt.Members {
		mem := m.(*loadbalancerv2.Member)
		newMembers = append(newMembers, &entityv2.Member{
			UUID:           "mem-" + randID(),
			Address:        mem.IpAddress,
			ProtocolPort:   mem.Port,
			Weight:         mem.Weight,
			MonitorPort:    mem.MonitorPort,
			SubnetID:       lbID,
			Name:           mem.Name,
			PoolID:         poolID,
			TypeCreate:     "????????",
			Backup:         false,
			DisplayStatus:  consts.ACTIVE_LOADBALANCER_STATUS,
			CreatedAt:      time.Now().Format(time.RFC3339),
			UpdatedAt:      time.Now().Format(time.RFC3339),
			CreatedBy:      "????????",
			ProgressStatus: consts.ACTIVE_LOADBALANCER_STATUS,
		})
	}
	pool.Members.Items = newMembers

	m.updatingStatus(lbID)
	go m.readyAfterTime(lbID)
	return nil
}

func (m *MockProvider) GetPoolByID(ctx context.Context, lbID, poolID string) (*entityv2.Pool, error) {
	logger := contexts.NewContext(ctx).Log()
	for _, p := range m.pools {
		if p.lbID == lbID && p.GetId() == poolID {
			return clone.Clone(p.Pool).(*entityv2.Pool), nil
		}
	}
	logger.Errorf("[ERROR] - GetPoolByID: pool not found")
	return nil, domain.ErrorNotFound
}

func (m *MockProvider) GetPoolMembers(ctx context.Context, lbID, poolID string) (*entityv2.ListMembers, error) {
	logger := contexts.NewContext(ctx).Log()
	for _, p := range m.pools {
		if p.lbID == lbID && p.GetId() == poolID {
			return clone.Clone(p.Members).(*entityv2.ListMembers), nil
		}
	}
	logger.Errorf("[ERROR] - GetPoolMembers: pool not found")
	return nil, domain.ErrorNotFound
}

func (m *MockProvider) DeletePool(ctx context.Context, lbID, poolID string) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request delete pool %s of load balancer %s", domain.RequestIcon, poolID, lbID)
	isFound := false
	newPools := make([]*wrapPool, 0)
	for i, p := range m.pools {
		if p.lbID != lbID || p.GetId() != poolID {
			newPools = append(newPools, m.pools[i])
		} else {
			isFound = true
		}
	}
	if !isFound {
		logger.Errorf("[ERROR] - DeletePool: pool not found")
		return domain.ErrorNotFound
	}
	m.pools = newPools

	m.updatingStatus(lbID)
	go m.readyAfterTime(lbID)
	return nil
}

func (m *MockProvider) UpdatePool(ctx context.Context, lbID, poolID string, opt loadbalancerv2.IUpdatePoolRequest) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request update pool %s of load balancer %s", domain.RequestIcon, poolID, lbID)
	updateOpt := opt.ToRequestBody().(*loadbalancerv2.UpdatePoolRequest)
	var pool *wrapPool
	for _, p := range m.pools {
		if p.lbID == lbID && p.GetId() == poolID {
			pool = p
			break
		}
	}
	if pool == nil {
		logger.Errorf("[ERROR] - UpdatePool: pool not found")
		return domain.ErrorNotFound
	}
	pool.LoadBalanceMethod = string(updateOpt.Algorithm)

	if pool.Protocol == string(loadbalancerv2.PoolProtocolHTTP) {
		if updateOpt.Stickiness == nil {
			return errors.New("Missing Stickiness For HTTP Pool")
		}
		if updateOpt.TLSEncryption == nil {
			return errors.New("Missing TLSEncryption For HTTP Pool")
		}
		pool.TLSEncryption = *updateOpt.TLSEncryption
		pool.Stickiness = *updateOpt.Stickiness
	}

	m.updatingStatus(lbID)
	go m.readyAfterTime(lbID)
	return nil
}

func (m *MockProvider) GetPoolHealthMonitorById(ctx context.Context, lbID, poolID string) (*entityv2.HealthMonitor, error) {
	logger := contexts.NewContext(ctx).Log()
	for _, p := range m.pools {
		if p.lbID == lbID && p.GetId() == poolID {
			return clone.Clone(p.HealthMonitor).(*entityv2.HealthMonitor), nil
		}
	}
	logger.Errorf("[ERROR] - GetPoolHealthMonitorById: pool not found")
	return nil, domain.ErrorNotFound
}

// // --------------------------- Certificate ---------------------------

func (m *MockProvider) ListCertificates(ctx context.Context) (*entityv2.ListCertificates, error) {
	certs := make([]entityv2.Certificate, 0)
	for _, c := range m.certs {
		certs = append(certs, clone.Clone(*c.Certificate).(entityv2.Certificate))
	}
	return &entityv2.ListCertificates{
		Certificates: certs,
	}, nil
}

func (m *MockProvider) GetCertificateByID(ctx context.Context, certID string) (*entityv2.Certificate, error) {
	logger := contexts.NewContext(ctx).Log()
	for _, c := range m.certs {
		if c.UUID == certID {
			return clone.Clone(*c.Certificate).(*entityv2.Certificate), nil
		}
	}
	logger.Errorf("[ERROR] - GetCertificateByID: certificate not found")
	return nil, domain.ErrorNotFound
}

func (m *MockProvider) ImportCertificate(ctx context.Context, opt loadbalancerv2.ICreateCertificateRequest) (*entityv2.Certificate, error) {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request import certificate", domain.RequestIcon)
	cert := opt.ToRequestBody().(*loadbalancerv2.CreateCertificateRequest)
	newCert := &wrapCertificate{
		Certificate: &entityv2.Certificate{
			UUID:            "cert-" + randID(),
			Name:            cert.Name,
			CertificateType: string(cert.Type),
			InUse:           false,
		},
	}
	m.mu.Lock()
	m.certs = append(m.certs, newCert)
	m.mu.Unlock()
	return &entityv2.Certificate{
		UUID: newCert.UUID,
	}, nil
}

func (m *MockProvider) DeleteCertificate(ctx context.Context, certID string) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Request delete certificate %s", domain.RequestIcon, certID)
	isFound := false
	newCerts := make([]*wrapCertificate, 0)
	for i, c := range m.certs {
		if c.UUID != certID {
			newCerts = append(newCerts, m.certs[i])
		} else {
			isFound = true
		}
	}
	if !isFound {
		logger.Errorf("[ERROR] - DeleteCertificate: certificate not found")
		return domain.ErrorNotFound
	}
	m.certs = newCerts
	return nil
}
