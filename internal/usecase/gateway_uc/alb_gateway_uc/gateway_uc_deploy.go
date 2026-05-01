package alb_gateway_uc

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/anngdinh/operator-helper/contexts"
	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	vksv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

// deployLB persists the desired LoadBalancerConfig spec for the Gateway.
// It applies cluster defaults to network fields when the merged effective
// spec didn't set them, lists existing LBCs owned by the Gateway (label
// selector), and creates or patches the single CRD that the lbc_uc
// reconciler then drives toward vngcloud.
//
// Returns an error if more than one LBC matches the owner labels — that
// indicates a previous deploy left split-brain state and the user must
// reconcile manually.
func (uc *albGatewayUseCase) deployLB(ctx context.Context, gw *gwv1.Gateway, lbSpec *vksv1alpha1.LoadBalancerConfigSpec) (*vksv1alpha1.LoadBalancerConfig, error) {
	logger := contexts.NewContext(ctx).Log()

	if lbSpec.SubnetId == "" {
		lbSpec.SubnetId = uc.defaultSubnetId
	}
	if lbSpec.VpcId == "" {
		lbSpec.VpcId = uc.defaultNetworkId
	}
	if lbSpec.ZoneId == "" {
		lbSpec.ZoneId = uc.defaultZone
	}
	lbSpec.Type = loadbalancerv2.LoadBalancerTypeLayer7
	if uc.clusterId != "" {
		lbSpec.ClusterId = ptr.To(uc.clusterId)
	}

	ownerLabels := client.MatchingLabels{
		domain.LabelOwnerResourceName: gw.Name,
		domain.LabelOwnerResourceKind: gw.Kind,
		domain.LabelOwnerResourceUid:  string(gw.UID),
	}

	list := &vksv1alpha1.LoadBalancerConfigList{}
	if err := uc.k8sRepo.ListLoadBalancerConfig(ctx, list, client.InNamespace(gw.Namespace), ownerLabels); err != nil {
		return nil, fmt.Errorf("list LBCs for gateway %s/%s: %w", gw.Namespace, gw.Name, err)
	}
	if len(list.Items) > 1 {
		return nil, fmt.Errorf("found %d LoadBalancerConfigs owned by gateway %s/%s, expected at most 1", len(list.Items), gw.Namespace, gw.Name)
	}

	if len(list.Items) == 0 {
		fresh := &vksv1alpha1.LoadBalancerConfig{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    gw.Namespace,
				GenerateName: gw.Name + "-",
				Labels: map[string]string{
					domain.LabelOwnerResourceName: gw.Name,
					domain.LabelOwnerResourceKind: gw.Kind,
					domain.LabelOwnerResourceUid:  string(gw.UID),
				},
			},
			Spec: *lbSpec,
		}
		if err := uc.k8sRepo.CreateLoadBalancerConfig(ctx, fresh); err != nil {
			return nil, fmt.Errorf("create LBC for gateway %s/%s: %w", gw.Namespace, gw.Name, err)
		}
		logger.Infof("created LoadBalancerConfig %s/%s for gateway %s", fresh.Namespace, fresh.Name, gw.Name)
		return fresh, nil
	}

	existing := &list.Items[0]
	old := existing.DeepCopy()
	if existing.Labels == nil {
		existing.Labels = map[string]string{}
	}
	existing.Labels[domain.LabelOwnerResourceName] = gw.Name
	existing.Labels[domain.LabelOwnerResourceKind] = gw.Kind
	existing.Labels[domain.LabelOwnerResourceUid] = string(gw.UID)
	existing.Spec = *lbSpec

	if reflect.DeepEqual(old.Spec, existing.Spec) && reflect.DeepEqual(old.Labels, existing.Labels) {
		return existing, nil
	}
	if err := uc.k8sRepo.PatchLoadBalancerConfig(ctx, existing, client.MergeFrom(old)); err != nil {
		return nil, fmt.Errorf("patch LBC %s/%s: %w", existing.Namespace, existing.Name, err)
	}
	logger.Infof("patched LoadBalancerConfig %s/%s for gateway %s", existing.Namespace, existing.Name, gw.Name)
	return existing, nil
}

// InitALBGatewayUseCase resolves cluster-wide defaults the use case needs:
// clusterId from a node label, default zone/network/subnet/CIDR from the
// first node's provider info, and the cluster's CNI type. Mirrors
// InitIngressUseCase. Safe to call multiple times — sync.Once guards the
// shared mutable state.
func (uc *albGatewayUseCase) initImpl(ctx context.Context) error {
	logger := contexts.NewContext(ctx).Log()

	nodes := &corev1.NodeList{}
	if err := uc.k8sRepo.ListNode(ctx, nodes); err != nil {
		return err
	}
	if len(nodes.Items) == 0 {
		return errors.New("no nodes found in cluster")
	}

	if uc.defaultNetworkId == "" || uc.defaultSubnetId == "" || uc.defaultSubnetCIDR == "" || uc.defaultZone == "" {
		first := utils.GetProviderIdFromNode(&nodes.Items[0])
		if first == "" {
			return errors.New("failed to get provider ID from node")
		}
		zone, netID, subnetID, cidr, err := uc.vngcloudRepo.GetServerNetworkInfo(ctx, first)
		if err != nil {
			return err
		}
		if netID == "" || subnetID == "" || cidr == "" || zone == "" {
			return errors.New("default network info is incomplete")
		}
		uc.defaultZone, uc.defaultNetworkId, uc.defaultSubnetId, uc.defaultSubnetCIDR = zone, netID, subnetID, cidr
	}

	if uc.clusterId == "" {
		clusterID := ""
		for _, n := range nodes.Items {
			if v, ok := n.Labels["vks.vngcloud.vn/cluster-id"]; ok && v != "" {
				clusterID = v
				break
			}
		}
		if clusterID == "" {
			return errors.New("no clusterID found, should exist in node label or specify in config")
		}
		uc.clusterId = clusterID
		logger.Infof("ClusterID is empty, got from node label: %s", uc.clusterId)
	}

	cniMode, err := uc.cniDetector.DetectCNIType(ctx)
	if err != nil {
		return fmt.Errorf("detect CNI type: %w", err)
	}
	logger.Infof("Detected CNI type: %s", cniMode)
	return nil
}
