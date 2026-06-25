package nlb_gateway_uc

import (
	"context"
	"fmt"
	"reflect"

	"github.com/sirupsen/logrus"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/common"
	v2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	sharedUC "github.com/vngcloud/vngcloud-load-balancer-controller/internal/usecase/gateway_uc/shared"
	pkggw "github.com/vngcloud/vngcloud-load-balancer-controller/pkg/gateway"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

// nlbBuildTask carries per-reconcile state. One task per Gateway reconcile.
type nlbBuildTask struct {
	uc         *nlbGatewayUseCase
	logger     *logrus.Entry
	gw         *gwv1.Gateway
	nameHelper utils.NameHelper

	unscopedPolicy *gwv1alpha1.VKSGatewayPolicy // LB-level fields + listener defaults
}

func (t *nlbBuildTask) run(ctx context.Context) error {
	if err := t.resolveGatewayPolicies(ctx); err != nil {
		return err
	}
	buildErr := t.buildLoadBalancerConfig(ctx)
	if err := t.writeRouteStatuses(ctx); err != nil {
		t.logger.Warnf("write L4 route statuses for Gateway %s/%s: %v", t.gw.Namespace, t.gw.Name, err)
	}
	return buildErr
}

// resolveGatewayPolicies finds the unscoped VKSGatewayPolicy (LB-level fields).
// NLB has no per-listener L7 settings, so only the unscoped policy is read.
func (t *nlbBuildTask) resolveGatewayPolicies(ctx context.Context) error {
	var list gwv1alpha1.VKSGatewayPolicyList
	if err := t.uc.k8sClient.List(ctx, &list, client.InNamespace(t.gw.Namespace)); err != nil {
		return fmt.Errorf("list VKSGatewayPolicy: %w", err)
	}
	cands := make([]*gwv1alpha1.VKSGatewayPolicy, 0, len(list.Items))
	for i := range list.Items {
		cands = append(cands, &list.Items[i])
	}
	t.unscopedPolicy, _ = sharedUC.ResolveDirectPolicy(cands, pkggw.PolicyTarget{
		Group: "gateway.networking.k8s.io", Kind: "Gateway",
		Namespace: t.gw.Namespace, Name: t.gw.Name,
	})
	return nil
}

// buildLoadBalancerConfig finds (or creates) the L4 LBC owned by this Gateway
// and rewrites the fields the Gateway controller owns. Mirrors the ALB path but
// emits Type=Network with TCP/UDP listeners, each pointing at one default pool.
func (t *nlbBuildTask) buildLoadBalancerConfig(ctx context.Context) error {
	lbcs, err := t.uc.listOwnedLBCs(ctx, t.gw)
	if err != nil {
		return err
	}
	if len(lbcs) > 1 {
		return fmt.Errorf("multiple LBCs found for Gateway %s/%s; cannot reconcile", t.gw.Namespace, t.gw.Name)
	}

	var (
		lbc       *v1alpha1.LoadBalancerConfig
		oldLBC    *v1alpha1.LoadBalancerConfig
		isCreated bool
	)
	if len(lbcs) == 1 {
		lbc = lbcs[0]
		oldLBC = lbc.DeepCopy()
		isCreated = true
	} else {
		lbc = &v1alpha1.LoadBalancerConfig{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    t.gw.Namespace,
				GenerateName: t.gw.Name + "-",
			},
			Spec: v1alpha1.LoadBalancerConfigSpec{},
		}
	}

	if lbc.Labels == nil {
		lbc.Labels = make(map[string]string)
	}
	lbc.Labels[domain.LabelOwnerResourceKind] = domain.OwnerKindGateway
	lbc.Labels[domain.LabelOwnerResourceName] = t.gw.Name
	lbc.Labels[domain.LabelOwnerResourceUid] = string(t.gw.UID)
	lbc.Labels[domain.OwnerLabelGatewayUID] = string(t.gw.UID)

	// Type/Subnet/Zone/Name are create-only (mirrored from cloud by the LBC
	// controller; writing them every reconcile fights that sync → loop).
	lbc.Spec.VpcId = t.uc.defaultNetworkId
	if !isCreated {
		subnetID, networkID, zone, _, err := t.resolveSubnetAndZone(ctx)
		if err != nil {
			return err
		}
		if subnetID == "" || networkID == "" || zone == "" {
			return fmt.Errorf("could not resolve subnet/zone for Gateway %s/%s", t.gw.Namespace, t.gw.Name)
		}
		lbc.Spec.VpcId = networkID
		lbc.Spec.Type = v2.LoadBalancerTypeLayer4
		lbc.Spec.SubnetId = subnetID
		lbc.Spec.ZoneId = common.Zone(zone)
		lbc.Spec.LoadBalancerName = t.resolveLoadBalancerName()
	}

	if t.uc.clusterId != "" {
		lbc.Spec.ClusterId = &t.uc.clusterId
	}

	t.applyLoadBalancerSpec(lbc)

	pools, listeners, err := t.buildListenersAndPools(ctx)
	if err != nil {
		_ = t.writeGatewayStatus(ctx, nil, err)
		return err
	}
	lbc.Spec.Pools = pools
	lbc.Spec.Listeners = listeners

	if !isCreated {
		if err := t.uc.k8sRepo.CreateLoadBalancerConfig(ctx, lbc); err != nil {
			_ = t.writeGatewayStatus(ctx, nil, err)
			return fmt.Errorf("create LBC for Gateway %s/%s: %w", t.gw.Namespace, t.gw.Name, err)
		}
		t.logger.Infof("created L4 LBC %s/%s", lbc.Namespace, lbc.Name)
		return t.writeGatewayStatus(ctx, lbc, nil)
	}
	if !reflect.DeepEqual(oldLBC.Spec, lbc.Spec) || !reflect.DeepEqual(oldLBC.Labels, lbc.Labels) {
		if err := t.uc.k8sRepo.PatchLoadBalancerConfig(ctx, lbc, client.MergeFrom(oldLBC)); err != nil {
			_ = t.writeGatewayStatus(ctx, oldLBC, err)
			return fmt.Errorf("patch LBC %s/%s: %w", lbc.Namespace, lbc.Name, err)
		}
		t.logger.Infof("patched L4 LBC %s/%s", lbc.Namespace, lbc.Name)
	}
	return t.writeGatewayStatus(ctx, lbc, nil)
}

// applyLoadBalancerSpec writes the LB-level fields the unscoped VKSGatewayPolicy
// contributes. Each is overwritten unconditionally (incl. nil) so removing a
// field from the policy un-sets it on the LBC. Same pattern as the ALB path.
func (t *nlbBuildTask) applyLoadBalancerSpec(lbc *v1alpha1.LoadBalancerConfig) {
	var s *gwv1alpha1.VKSLoadBalancerSpec
	if t.unscopedPolicy != nil {
		s = t.unscopedPolicy.Spec.LoadBalancerSpec
	}

	lbc.Spec.Scheme = nil
	lbc.Spec.PackageId = nil
	lbc.Spec.LoadBalancerId = nil
	lbc.Spec.PrivateSubnetId = nil
	lbc.Spec.PrivateZoneId = nil
	lbc.Spec.EnableAutoscale = nil
	lbc.Spec.IsPoc = nil
	lbc.Spec.Tags = nil

	if s == nil {
		return
	}
	if s.Scheme != nil {
		scheme := v2.LoadBalancerScheme(*s.Scheme)
		lbc.Spec.Scheme = &scheme
	}
	if s.PackageID != nil {
		lbc.Spec.PackageId = s.PackageID
	}
	if s.LoadBalancerID != nil {
		lbc.Spec.LoadBalancerId = s.LoadBalancerID
	}
	if s.PrivateSubnetID != nil {
		lbc.Spec.PrivateSubnetId = s.PrivateSubnetID
	}
	if s.PrivateZoneID != nil {
		zone := common.Zone(*s.PrivateZoneID)
		lbc.Spec.PrivateZoneId = &zone
	}
	if s.EnableAutoscale != nil {
		lbc.Spec.EnableAutoscale = s.EnableAutoscale
	}
	if s.IsPOC != nil {
		lbc.Spec.IsPoc = s.IsPOC
	}
	if len(s.Tags) > 0 {
		lbc.Spec.Tags = make(map[string]string, len(s.Tags))
		for k, v := range s.Tags {
			lbc.Spec.Tags[k] = v
		}
	}
}

// resolveSubnetAndZone mirrors the ALB resolver: LoadBalancerID adoption →
// SubnetID → PreferZoneID (node-scan) → cluster default. Fails closed on an
// unresolvable explicit value.
func (t *nlbBuildTask) resolveSubnetAndZone(ctx context.Context) (string, string, string, string, error) {
	var s *gwv1alpha1.VKSLoadBalancerSpec
	if t.unscopedPolicy != nil {
		s = t.unscopedPolicy.Spec.LoadBalancerSpec
	}

	switch {
	case s != nil && s.LoadBalancerID != nil && *s.LoadBalancerID != "":
		lb, err := t.uc.vngcloudRepo.GetLoadBalancerByID(ctx, *s.LoadBalancerID)
		if err != nil || lb == nil {
			return "", "", "", "", fmt.Errorf("get load balancer by id %s for adoption: %w", *s.LoadBalancerID, err)
		}
		if lb.BackendSubnetID == "" || lb.BackendSubnetID == t.uc.defaultSubnetId {
			return t.uc.defaultSubnetId, t.uc.defaultNetworkId, string(t.uc.defaultZone), t.uc.defaultSubnetCIDR, nil
		}
		subnet, err := t.uc.vngcloudRepo.GetSubnetByID(ctx, t.uc.defaultNetworkId, lb.BackendSubnetID)
		if err != nil || subnet == nil {
			return "", "", "", "", fmt.Errorf("get subnet %s of adopted load balancer %s: %w", lb.BackendSubnetID, *s.LoadBalancerID, err)
		}
		return subnet.Id, t.uc.defaultNetworkId, subnet.ZoneID, subnet.Cidr, nil

	case s != nil && s.SubnetID != nil && *s.SubnetID != "":
		return *s.SubnetID, t.uc.defaultNetworkId, string(t.uc.defaultZone), t.uc.defaultSubnetCIDR, nil

	case s != nil && s.PreferZoneID != nil && *s.PreferZoneID != "":
		if common.Zone(*s.PreferZoneID) == t.uc.defaultZone {
			return t.uc.defaultSubnetId, t.uc.defaultNetworkId, string(t.uc.defaultZone), t.uc.defaultSubnetCIDR, nil
		}
		nodes := &corev1.NodeList{}
		if err := t.uc.k8sRepo.ListNode(ctx, nodes); err != nil {
			return "", "", "", "", fmt.Errorf("list nodes to resolve prefer zone %s: %w", *s.PreferZoneID, err)
		}
		for _, providerID := range utils.GetListProviderIdFromNodeList(nodes) {
			z, n, sn, cidr, err := t.uc.vngcloudRepo.GetServerNetworkInfo(ctx, providerID)
			if err != nil {
				continue
			}
			if string(z) == *s.PreferZoneID {
				return sn, n, string(z), cidr, nil
			}
		}
		return "", "", "", "", fmt.Errorf("no cluster node found in prefer zone %s", *s.PreferZoneID)

	default:
		return t.uc.defaultSubnetId, t.uc.defaultNetworkId, string(t.uc.defaultZone), t.uc.defaultSubnetCIDR, nil
	}
}

// resolveLoadBalancerName returns the cloud LB name to set at create time.
func (t *nlbBuildTask) resolveLoadBalancerName() string {
	if t.unscopedPolicy != nil && t.unscopedPolicy.Spec.LoadBalancerSpec != nil {
		if n := t.unscopedPolicy.Spec.LoadBalancerSpec.LoadBalancerName; n != nil && *n != "" {
			return *n
		}
	}
	return t.nameHelper.GetLoadBalancerDefaultName()
}
