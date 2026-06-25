package alb_gateway_uc

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

// defaultGatewayBuildTask carries per-reconcile state and is the rough analogue
// of ingress_uc's defaultModelBuildTask. One task per Gateway reconcile.
type defaultGatewayBuildTask struct {
	uc         *albGatewayUseCase
	logger     *logrus.Entry
	gw         *gwv1.Gateway
	nameHelper utils.NameHelper

	// Resolved at translation time.
	unscopedPolicy   *gwv1alpha1.VKSGatewayPolicy            // for LB-level fields and listener defaults
	listenerPolicies map[string]*gwv1alpha1.VKSGatewayPolicy // by Gateway listener name
}

// run is the per-reconcile entry. Phase A only translates LB-level fields;
// listeners, certs, pools, and policies land in subsequent phases and will
// extend buildLoadBalancerConfig in place.
func (t *defaultGatewayBuildTask) run(ctx context.Context) error {
	if err := t.resolveGatewayPolicies(ctx); err != nil {
		return err
	}
	buildErr := t.buildLoadBalancerConfig(ctx)
	// HTTPRoute Accepted/ResolvedRefs reflect attachment + backend resolution,
	// which don't depend on cloud provisioning — write them regardless of
	// buildErr (best-effort; must not mask the reconcile result).
	if err := t.writeRouteStatuses(ctx); err != nil {
		t.logger.Warnf("write HTTPRoute statuses for Gateway %s/%s: %v", t.gw.Namespace, t.gw.Name, err)
	}
	return buildErr
}

// resolveGatewayPolicies finds the unscoped policy (LB-level fields + listener
// defaults) and per-listener policies (by sectionName). Conflict losers are
// stamped onto status by the policy validator controllers, not here.
func (t *defaultGatewayBuildTask) resolveGatewayPolicies(ctx context.Context) error {
	var list gwv1alpha1.VKSGatewayPolicyList
	if err := t.uc.k8sClient.List(ctx, &list, client.InNamespace(t.gw.Namespace)); err != nil {
		return fmt.Errorf("list VKSGatewayPolicy: %w", err)
	}
	cands := make([]*gwv1alpha1.VKSGatewayPolicy, 0, len(list.Items))
	for i := range list.Items {
		cands = append(cands, &list.Items[i])
	}

	unscopedTarget := pkggw.PolicyTarget{
		Group: "gateway.networking.k8s.io", Kind: "Gateway",
		Namespace: t.gw.Namespace, Name: t.gw.Name,
	}
	t.unscopedPolicy, _ = sharedUC.ResolveDirectPolicy(cands, unscopedTarget)

	t.listenerPolicies = make(map[string]*gwv1alpha1.VKSGatewayPolicy, len(t.gw.Spec.Listeners))
	for _, l := range t.gw.Spec.Listeners {
		listenerTarget := pkggw.PolicyTarget{
			Group: "gateway.networking.k8s.io", Kind: "Gateway",
			Namespace:   t.gw.Namespace,
			Name:        t.gw.Name,
			SectionName: string(l.Name),
		}
		win, _ := sharedUC.ResolveDirectPolicy(cands, listenerTarget)
		if win != nil {
			t.listenerPolicies[string(l.Name)] = win
		}
	}
	return nil
}

// buildLoadBalancerConfig finds (or creates) the LBC owned by this Gateway and
// rewrites only the fields the Gateway controller is the source of truth for.
// Mirrors ingress_uc's pattern: read existing, mutate Spec, patch only on
// real change to avoid generation churn.
func (t *defaultGatewayBuildTask) buildLoadBalancerConfig(ctx context.Context) error {
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

	// Owner labels — the same {kind, name, uid} set Ingress/Service use, so
	// kubectl describe and existing reverse-lookups all keep working.
	if lbc.Labels == nil {
		lbc.Labels = make(map[string]string)
	}
	lbc.Labels[domain.LabelOwnerResourceKind] = domain.OwnerKindGateway
	lbc.Labels[domain.LabelOwnerResourceName] = t.gw.Name
	lbc.Labels[domain.LabelOwnerResourceUid] = string(t.gw.UID)
	lbc.Labels[domain.OwnerLabelGatewayUID] = string(t.gw.UID)

	// Type, SubnetId, ZoneId, LoadBalancerName are mirrored from the cloud LB
	// by the LBC controller. Writing them on every reconcile fights that sync
	// and produces an infinite reconcile loop (see commit 1bbc823). Resolve and
	// set them only at create time — that also keeps the subnet/zone resolution
	// (which may call VNGCloud to adopt an existing LB or pin a zone) off the
	// per-reconcile hot path. VpcId tracks the cluster network and is cheap, so
	// it is set unconditionally.
	lbc.Spec.VpcId = t.uc.defaultNetworkId
	if !isCreated {
		subnetID, networkID, zone, _, err := t.resolveSubnetAndZone(ctx)
		if err != nil {
			return err
		}
		if subnetID == "" || networkID == "" || zone == "" {
			return fmt.Errorf("could not resolve subnet/zone for Gateway %s/%s; set VKSGatewayPolicy.LoadBalancerSpec.SubnetID/PreferZoneID or wait for cluster default network discovery",
				t.gw.Namespace, t.gw.Name)
		}
		lbc.Spec.VpcId = networkID
		lbc.Spec.Type = v2.LoadBalancerTypeLayer7
		lbc.Spec.SubnetId = subnetID
		lbc.Spec.ZoneId = common.Zone(zone)
		lbc.Spec.LoadBalancerName = t.resolveLoadBalancerName()
	}

	if t.uc.clusterId != "" {
		lbc.Spec.ClusterId = &t.uc.clusterId
	}

	// LB-level fields from VKSGatewayPolicy.LoadBalancerSpec (only the
	// unscoped policy contributes here per the design spec).
	t.applyLoadBalancerSpec(lbc)

	listeners, err := t.buildListeners()
	if err != nil {
		return err
	}
	lbc.Spec.CreateCertificates = t.buildCreateCertificates()

	pools, listenerPolicies, err := t.buildPoolsAndPolicies(ctx)
	if err != nil {
		return err
	}
	lbc.Spec.Pools = pools

	// Fold per-listener policies onto the matching listener entry. The
	// listenerPolicies map is keyed by the *Gateway* listener name (Gateway
	// listeners and the LBC listener slice share the same i — both are
	// emitted in Gateway.Spec.Listeners order, with unsupported-protocol
	// entries skipped from the LBC slice but preserved here in the map for
	// later phases). Iterating against the Gateway listeners keeps the
	// pairing correct even when the LBC listener name was renamed (e.g.
	// short names get a "gw_<uid>_" prefix to satisfy the cloud API's
	// 5-char minimum).
	gwIdx := 0
	for i := range listeners {
		// Advance gwIdx to the next supported-protocol Gateway listener.
		for gwIdx < len(t.gw.Spec.Listeners) {
			if _, ok := mapListenerProtocol(t.gw.Spec.Listeners[gwIdx].Protocol); ok {
				break
			}
			gwIdx++
		}
		if gwIdx >= len(t.gw.Spec.Listeners) {
			break
		}
		if pol, ok := listenerPolicies[string(t.gw.Spec.Listeners[gwIdx].Name)]; ok {
			listeners[i].Policies = pol
		}
		gwIdx++
	}
	lbc.Spec.Listeners = listeners

	if !isCreated {
		if err := t.uc.k8sRepo.CreateLoadBalancerConfig(ctx, lbc); err != nil {
			// Status reflects the failure so the user sees Programmed=False.
			_ = t.writeGatewayStatus(ctx, nil, err)
			return fmt.Errorf("create LBC for Gateway %s/%s: %w", t.gw.Namespace, t.gw.Name, err)
		}
		t.logger.Infof("created LBC %s/%s", lbc.Namespace, lbc.Name)
		return t.writeGatewayStatus(ctx, lbc, nil)
	}
	if !reflect.DeepEqual(oldLBC.Spec, lbc.Spec) || !reflect.DeepEqual(oldLBC.Labels, lbc.Labels) {
		if err := t.uc.k8sRepo.PatchLoadBalancerConfig(ctx, lbc, client.MergeFrom(oldLBC)); err != nil {
			_ = t.writeGatewayStatus(ctx, oldLBC, err)
			return fmt.Errorf("patch LBC %s/%s: %w", lbc.Namespace, lbc.Name, err)
		}
		t.logger.Infof("patched LBC %s/%s", lbc.Namespace, lbc.Name)
	}
	return t.writeGatewayStatus(ctx, lbc, nil)
}

// applyLoadBalancerSpec writes the LB-level fields the unscoped
// VKSGatewayPolicy contributes. Listener-level fields (timeouts, allowedCidrs,
// insertHeaders) are applied per-listener in Phase C.
//
// The unscoped policy is the sole source of truth for these fields, so each
// is overwritten unconditionally (including with nil) — removing a field from
// the policy must un-set it on the LBC, otherwise stale values persist
// forever. This matches the Ingress/Service controller pattern, which
// reassigns every reconcile from annotation-or-nil.
func (t *defaultGatewayBuildTask) applyLoadBalancerSpec(lbc *v1alpha1.LoadBalancerConfig) {
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
		// Replace the whole map (don't merge). A key removed from the
		// policy must disappear from the LBC; merging would leak it.
		lbc.Spec.Tags = make(map[string]string, len(s.Tags))
		for k, v := range s.Tags {
			lbc.Spec.Tags[k] = v
		}
	}
}

// resolveSubnetAndZone returns (subnetID, networkID, zone, cidr, err) for a new
// LB, honoring the unscoped VKSGatewayPolicy.LoadBalancerSpec in the same
// priority order the Ingress controller uses:
//  1. LoadBalancerID — adopt the existing LB; mirror its backend subnet's
//     zone/cidr so the LBC matches the LB the LBC controller adopts (it adopts
//     via Spec.LoadBalancerId; see lbc_uc/deploy_lb.go).
//  2. SubnetID — explicit subnet (zone stays the cluster default).
//  3. PreferZoneID — pin to a zone by finding a cluster node in it.
//  4. cluster defaults.
func (t *defaultGatewayBuildTask) resolveSubnetAndZone(ctx context.Context) (string, string, string, string, error) {
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

// resolveLoadBalancerName returns the cloud LB name to set at create time:
// the unscoped policy's LoadBalancerName when set, else a controller-generated
// default. Mirrors the Ingress "load-balancer-name" annotation.
func (t *defaultGatewayBuildTask) resolveLoadBalancerName() string {
	if t.unscopedPolicy != nil && t.unscopedPolicy.Spec.LoadBalancerSpec != nil {
		if n := t.unscopedPolicy.Spec.LoadBalancerSpec.LoadBalancerName; n != nil && *n != "" {
			return *n
		}
	}
	return t.nameHelper.GetLoadBalancerDefaultName()
}

// listOwnedLBCs finds the LBC(s) owned by gw via the
// vks.vngcloud.vn/owner-resource-uid label. Caller treats >1 as an error.
func (uc *albGatewayUseCase) listOwnedLBCs(ctx context.Context, gw *gwv1.Gateway) ([]*v1alpha1.LoadBalancerConfig, error) {
	var list v1alpha1.LoadBalancerConfigList
	if err := uc.k8sRepo.ListLoadBalancerConfig(ctx, &list,
		client.InNamespace(gw.Namespace),
		client.MatchingLabels{
			domain.LabelOwnerResourceKind: domain.OwnerKindGateway,
			domain.LabelOwnerResourceUid:  string(gw.UID),
		},
	); err != nil {
		return nil, fmt.Errorf("list LBC owned by Gateway %s/%s: %w", gw.Namespace, gw.Name, err)
	}
	out := make([]*v1alpha1.LoadBalancerConfig, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, &list.Items[i])
	}
	return out, nil
}
