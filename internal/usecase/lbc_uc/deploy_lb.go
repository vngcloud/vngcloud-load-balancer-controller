package lbc_uc

import (
	"context"
	"fmt"
	"strconv"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/entity"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/common"
	"github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/inter"
	loadbalancerv2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/config"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/errs"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/k8sbatch"
)

type defaultModelDeployTask struct {
	logger       *logrus.Entry
	cfg          *config.Config
	vngcloudRepo repository.VngCloudRepository
	k8sRepo      repository.K8sRepository
	lbConfig     *v1alpha1.LoadBalancerConfig

	// batcher coalesces queued Status mutations across the many small
	// statusAdd* helpers in this package. One GET + one Status PATCH
	// per reconcile instead of one per helper call. Flushed once at
	// the end of ensure / DeleteLoadBalancerConfigUseCase.
	batcher *k8sbatch.Batcher
}

func (t *defaultModelDeployTask) deploy(ctx context.Context) error {
	// LAYER 1: Validate the LoadBalancerConfig itself (internal consistency)
	// This runs before we even get the load balancer ID
	if err := t.validateSelf(ctx); err != nil {
		return err
	}

	createdCerts, err := t.deployCerts(ctx)
	if err != nil {
		return err
	}

	lbId, err := t.deployLoadBalancer(ctx, createdCerts)
	if err != nil {
		return err
	}

	// LAYER 2: Validate across LoadBalancerConfigs sharing the same load balancer
	// This runs after we have the load balancer ID
	if err := t.validateCrossLBCs(ctx, lbId); err != nil {
		return err
	}

	newCreatedPools, err := t.deployPools(ctx, lbId)
	if err != nil {
		return err
	}
	newCreatedListeners, err := t.deployListeners(ctx, lbId, newCreatedPools, createdCerts)
	if err != nil {
		return err
	}

	err = t.deleteRedundantListeners(ctx, lbId, newCreatedListeners, newCreatedPools)
	if err != nil {
		return err
	}
	err = t.deleteRedundantPools(ctx, lbId, newCreatedPools)
	if err != nil {
		return err
	}
	err = t.deployDeleteRedundantCerts(ctx)
	if err != nil {
		return err
	}

	// update status
	return t.k8sRepo.PatchMutateStatusLoadBalancerConfig(ctx, t.lbConfig, func(ctx context.Context, obj *v1alpha1.LoadBalancerConfig) bool {
		// check on fresh copy if already equal
		if createdListenersEqual(obj.Status.CreatedListeners, newCreatedListeners) &&
			createdPoolsEqual(obj.Status.CreatedPools, newCreatedPools) &&
			createdCertificatesEqual(obj.Status.CreatedCertificates, createdCerts) {
			return false // no change needed
		}
		obj.Status.CreatedListeners = newCreatedListeners
		obj.Status.CreatedPools = newCreatedPools
		obj.Status.CreatedCertificates = createdCerts
		return true
	})
}

// deployLoadBalancer creates or ensures the load balancer exists, handling migrations if necessary.
// It returns the load balancer ID to be used for further operations.
// The caller should requeue if a new load balancer ID is obtained to acquire the appropriate lock.
func (t *defaultModelDeployTask) deployLoadBalancer(ctx context.Context, createdCerts []v1alpha1.CreatedCertificate) (string, error) {
	errorNewLbIdObtained := errs.NewRequeueNeeded("new load balancer ID obtained, requeue needed")

	// if already an exist lb
	if t.lbConfig.Status.LoadBalancerId != nil && *t.lbConfig.Status.LoadBalancerId != "" {
		if t.lbConfig.Spec.LoadBalancerId != nil && *t.lbConfig.Spec.LoadBalancerId != "" && *t.lbConfig.Spec.LoadBalancerId != *t.lbConfig.Status.LoadBalancerId {
			lbId, err := t.migrateLoadBalancer(ctx, *t.lbConfig.Status.LoadBalancerId, *t.lbConfig.Spec.LoadBalancerId)
			if err != nil {
				return lbId, err
			}
			return lbId, errorNewLbIdObtained
		} else {
			return t.ensureExistLoadBalancer(ctx, *t.lbConfig.Status.LoadBalancerId, nil)
		}
	}

	// if not exist lbId in status
	// try to use spec lbId
	if t.lbConfig.Spec.LoadBalancerId != nil && *t.lbConfig.Spec.LoadBalancerId != "" {
		lbId, err := t.ensureExistLoadBalancer(ctx, *t.lbConfig.Spec.LoadBalancerId, nil)
		if err != nil {
			return lbId, err
		}
		return lbId, errorNewLbIdObtained
	}

	// try to use spec lb Name
	if t.lbConfig.Spec.LoadBalancerName != "" {
		lb, err := t.vngcloudRepo.GetLoadBalancerByName(ctx, t.lbConfig.Spec.LoadBalancerName)
		if err != nil {
			return "", err
		}
		if lb != nil {
			lbId, err := t.ensureExistLoadBalancer(ctx, lb.UUID, lb)
			if err != nil {
				return lbId, err
			}
			return lbId, errorNewLbIdObtained
		}
	}

	// if requeue here, created Listeners and Pools is not saved in status yet, if user delete LBC quickly, may cause orphan resources
	return t.createLoadBalancer(ctx, createdCerts)
}

func (t *defaultModelDeployTask) createLoadBalancer(ctx context.Context, createdCerts []v1alpha1.CreatedCertificate) (string, error) {
	var lbEntity *entity.LoadBalancer
	var err error
	// check if lb scheme is intervpc, need super client
	if t.lbConfig.Spec.Scheme != nil && *t.lbConfig.Spec.Scheme == loadbalancerv2.InterVpcLoadBalancerScheme {
		createRequest, err := t.buildCreateInterVpcLoadBalancerRequest(ctx)
		if err != nil {
			return "", err
		}
		lbEntity, err = t.vngcloudRepo.CreateInterLoadBalancer(ctx, createRequest)
		if err != nil {
			return "", err
		}
		if lbEntity == nil || lbEntity.UUID == "" {
			return "", errors.New("load balancer not have UUID after create, need to retry")
		}
	} else {
		createRequest, err := t.buildCreateLoadBalancerRequest(ctx, createdCerts)
		if err != nil {
			return "", err
		}
		lbEntity, err = t.vngcloudRepo.CreateLoadBalancer(ctx, createRequest)
		if err != nil {
			return "", err
		}
		if lbEntity == nil || lbEntity.UUID == "" {
			return "", errors.New("load balancer not have UUID after create, need to retry")
		}
	}

	if err := t.statusAddLoadBalancerId(ctx, &lbEntity.UUID, &lbEntity.Address); err != nil {
		return "", err
	}

	// wait for loadbalancer active, if lb is error, delete it and return error
	if _, err = t.vngcloudRepo.WaitForLBActive(ctx, lbEntity.UUID); err != nil {
		if err == domain.ErrorLoadBalancerStatusError {
			if err := t.statusAddLoadBalancerId(ctx, nil, nil); err != nil {
				return "", err
			}
			if err := t.vngcloudRepo.DeleteLoadBalancer(ctx, lbEntity.UUID); err != nil {
				t.logger.Error("Failed to delete loadbalancer: ", err)
				return "", err
			}
			t.logger.Infof("Delete loadbalancer \"%s\" because of status error, recreate now.", lbEntity.UUID)
			return "", errs.NewRequeueNeeded("loadbalancer status is error, delete and recreate")
		}
		t.logger.Error("Failed to wait for loadbalancer active: ", err)
		return "", err
	}

	t.logger.Infof("Created load balancer with ID %s", lbEntity.UUID)

	// because load balancer is just created, need to deploy package and tag again and update status
	return t.ensureExistLoadBalancer(ctx, lbEntity.UUID, nil)
}

// when update load balancer id to new value
func (t *defaultModelDeployTask) migrateLoadBalancer(ctx context.Context, oldId, newId string) (string, error) {
	// currently not do anything to old lb
	t.logger.Infof("Migrate load balancer from %s to %s...", oldId, newId)
	return t.ensureExistLoadBalancer(ctx, newId, nil)
}

// ensure tag, package, ...
func (t *defaultModelDeployTask) ensureExistLoadBalancer(ctx context.Context, lbId string, lbEntity *entity.LoadBalancer) (string, error) {
	if lbId == "" {
		return "", errors.New("load balancer id is empty")
	}
	if lbEntity == nil {
		var err error
		lbEntity, err = t.vngcloudRepo.GetLoadBalancerByID(ctx, lbId)
		if err != nil {
			return "", err
		}
	}

	if err := t.syncLBCStatusFromLoadBalancer(ctx, lbEntity); err != nil {
		return "", err
	}

	var err error
	if lbEntity, err = t.vngcloudRepo.WaitForLBActive(ctx, lbId); err != nil {
		return "", err
	}

	// update status
	if err := t.statusAddLoadBalancerId(ctx, &lbId, &lbEntity.Address); err != nil {
		return "", err
	}

	if err := t.deployTags(ctx, lbId); err != nil {
		return "", err
	}

	if err := t.deployPackageId(ctx, lbEntity); err != nil {
		return "", err
	}

	return lbId, nil
}

// syncLBCStatusFromLoadBalancer records the cloud LB's actual name, type, subnet, and zone
// in the LBC Status. The LBC controller never writes to Spec — Spec is the user/owner-controller
// desired state, Status is the controller's observed state.
func (t *defaultModelDeployTask) syncLBCStatusFromLoadBalancer(ctx context.Context, lbEntity *entity.LoadBalancer) error {
	if lbEntity == nil {
		return nil
	}

	// For InterVPC LBs, BackendSubnetID is the backend subnet.
	// For normal LBs, BackendSubnetID is empty; PrivateSubnetID is the subnet.
	subnetId := lbEntity.PrivateSubnetID
	if lbEntity.BackendSubnetID != "" {
		subnetId = lbEntity.BackendSubnetID
	}

	return t.k8sRepo.PatchMutateStatusLoadBalancerConfig(ctx, t.lbConfig, func(ctx context.Context, obj *v1alpha1.LoadBalancerConfig) bool {
		changed := false

		if lbEntity.Name != "" && (obj.Status.LoadBalancerName == nil || *obj.Status.LoadBalancerName != lbEntity.Name) {
			obj.Status.LoadBalancerName = &lbEntity.Name
			changed = true
		}

		if subnetId != "" && (obj.Status.SubnetId == nil || *obj.Status.SubnetId != subnetId) {
			obj.Status.SubnetId = &subnetId
			changed = true
		}

		if lbEntity.Type != "" {
			lbType := loadbalancerv2.LoadBalancerType(lbEntity.Type)
			if obj.Status.Type == nil || *obj.Status.Type != lbType {
				obj.Status.Type = &lbType
				changed = true
			}
		}

		if lbEntity.ZoneID != "" {
			zone := common.Zone(lbEntity.ZoneID)
			if obj.Status.ZoneId == nil || *obj.Status.ZoneId != zone {
				obj.Status.ZoneId = &zone
				changed = true
			}
		}

		return changed
	})
}

// resize load balancer if packageID in spec is different from current one
// ignore if this lb is autoscaled
func (t *defaultModelDeployTask) deployPackageId(ctx context.Context, lbEntity *entity.LoadBalancer) error {
	if t.lbConfig.Spec.PackageId == nil || *t.lbConfig.Spec.PackageId == "" {
		return nil
	}
	if lbEntity == nil {
		return errors.New("load balancer entity is nil")
	}
	if *t.lbConfig.Spec.PackageId == lbEntity.PackageID {
		return nil
	}

	if lbEntity.AutoScalable {
		t.logger.Infof("Loadbalancer is autoscaled, skip resizing.")
		return nil
	}

	t.logger.Infof("Need resize loadbalancer from package %s -> %s", lbEntity.PackageID, *t.lbConfig.Spec.PackageId)
	if err := t.vngcloudRepo.ResizeLoadBalancer(ctx, lbEntity.UUID, *t.lbConfig.Spec.PackageId); err != nil {
		return err
	}
	if _, err := t.vngcloudRepo.WaitForLBActive(ctx, lbEntity.UUID); err != nil {
		t.logger.Error("Failed to wait for loadbalancer active: ", err)
		return err
	}
	return nil
}

func (t *defaultModelDeployTask) buildCreateLoadBalancerRequest(ctx context.Context, createdCerts []v1alpha1.CreatedCertificate) (loadbalancerv2.ICreateLoadBalancerRequest, error) { //nolint:gocyclo
	packageId := ""
	if t.lbConfig.Spec.PackageId != nil && *t.lbConfig.Spec.PackageId != "" {
		packageId = *t.lbConfig.Spec.PackageId
	} else {
		// use default package from config, get default package id from name and zone
		listPackages, err := t.vngcloudRepo.ListLoadBalancerPackageByZone(ctx, t.lbConfig.Spec.ZoneId)
		if err != nil {
			return nil, err
		}
		if t.lbConfig.Spec.Type == loadbalancerv2.LoadBalancerTypeLayer4 && t.cfg.LoadBalancerOpts.DefaultL4PackageName != "" {
			for _, pkg := range listPackages.Items {
				if pkg.Name == t.cfg.LoadBalancerOpts.DefaultL4PackageName {
					packageId = pkg.UUID
					break
				}
			}
			if packageId == "" {
				return nil, errs.NewNoNeedRequeue(fmt.Sprintf("cannot find default load balancer package %s in zone %s", t.cfg.LoadBalancerOpts.DefaultL4PackageName, t.lbConfig.Spec.ZoneId))
			}
		} else if t.lbConfig.Spec.Type == loadbalancerv2.LoadBalancerTypeLayer7 && t.cfg.LoadBalancerOpts.DefaultL7PackageName != "" {
			for _, pkg := range listPackages.Items {
				if pkg.Name == t.cfg.LoadBalancerOpts.DefaultL7PackageName {
					packageId = pkg.UUID
					break
				}
			}
			if packageId == "" {
				return nil, errs.NewNoNeedRequeue(fmt.Sprintf("cannot find default load balancer package %s in zone %s", t.cfg.LoadBalancerOpts.DefaultL7PackageName, t.lbConfig.Spec.ZoneId))
			}
		}
	}

	request := loadbalancerv2.NewCreateLoadBalancerRequest(
		t.lbConfig.Spec.LoadBalancerName,
		packageId,
		t.lbConfig.Spec.SubnetId,
	).WithZoneId(t.lbConfig.Spec.ZoneId).WithScheme(loadbalancerv2.LoadBalancerScheme(t.cfg.LoadBalancerOpts.DefaultScheme))

	if t.lbConfig.Spec.Scheme != nil {
		request = request.WithScheme(*t.lbConfig.Spec.Scheme)
	}

	if t.lbConfig.Spec.EnableAutoscale != nil {
		request = request.WithAutoScalable(*t.lbConfig.Spec.EnableAutoscale)
	}

	if t.lbConfig.Spec.Type != "" {
		request = request.WithType(t.lbConfig.Spec.Type)
	}

	if t.lbConfig.Spec.IsPoc != nil {
		request = request.WithPoc(*t.lbConfig.Spec.IsPoc)
	}

	// if have pool, create first pool, but in L7, only create default pool in this step
	defaultPoolRequest, defaultListenerRequest, err := func() (loadbalancerv2.ICreatePoolRequest, loadbalancerv2.ICreateListenerRequest, error) {
		if len(t.lbConfig.Spec.Pools) == 0 {
			return nil, nil, nil
		}
		poolSpec := t.lbConfig.Spec.Pools[0]
		// if L7, only create default pool
		if t.lbConfig.Spec.Type == loadbalancerv2.LoadBalancerTypeLayer7 {
			isFoundDefaultPool := false
			for _, p := range t.lbConfig.Spec.Pools {
				if p.Name == domain.DEFAULT_NAME_DEFAULT_POOL {
					poolSpec = p
					isFoundDefaultPool = true
					break
				}
			}
			if !isFoundDefaultPool {
				return nil, nil, nil
			}
		}

		// Both listener and pool properties must be required (non null) or both are not required (null); <nil> map[]},
		// if have listener, create first listener
		if len(t.lbConfig.Spec.Listeners) == 0 {
			return nil, nil, nil
		}

		listenerSpec := t.lbConfig.Spec.Listeners[0]
		listenerRequest, err := t.buildCreateListenerRequest(ctx, "", listenerSpec, []v1alpha1.CreatedPool{}, createdCerts)
		if err != nil {
			return nil, nil, err
		}

		poolRequest := t.buildCreatePoolRequest(ctx, "", &poolSpec)

		return poolRequest, listenerRequest, nil
	}()
	if err != nil {
		return nil, err
	}

	if defaultPoolRequest != nil && defaultListenerRequest != nil {
		request = request.WithListener(defaultListenerRequest).WithPool(defaultPoolRequest)
	}

	// if have tags, add tags
	ensuredTags := make(map[string]string)
	if t.lbConfig.Spec.Tags != nil {
		ensuredTags = t.lbConfig.Spec.Tags
	}

	_, newTags := t.buildTag(ctx, map[string]string{}, map[string]string{}, ensuredTags)
	if len(newTags) > 0 {
		tags := make([]string, 0)
		for k, v := range newTags {
			tags = append(tags, k, v)
		}
		request = request.WithTags(tags...)
	}

	return request, nil
}

func (t *defaultModelDeployTask) buildCreateInterVpcLoadBalancerRequest(ctx context.Context) (inter.ICreateLoadBalancerRequest, error) {
	// check if userId is provided
	userId := t.cfg.Global.UserID
	if userId == 0 {
		// try to get from vngcloudRepo
		if userId = t.vngcloudRepo.GetUserId(); userId == 0 {
			return nil, errs.NewNoNeedRequeue("userId is required, cannot get from config or vngcloud repository")
		}
	}

	// build privateSubnetId
	if t.lbConfig.Spec.PrivateSubnetId == nil || *t.lbConfig.Spec.PrivateSubnetId == "" {
		return nil, errs.NewNoNeedRequeue("privateSubnetId is required for InterVpc load balancer")
	}

	// build privateZoneId
	// for InterVPC, use PrivateZoneId if available
	privateZoneId := t.lbConfig.Spec.ZoneId
	if t.lbConfig.Spec.PrivateZoneId != nil && *t.lbConfig.Spec.PrivateZoneId != "" {
		privateZoneId = *t.lbConfig.Spec.PrivateZoneId
	}

	// build packageId
	packageId := ""
	if t.lbConfig.Spec.PackageId != nil && *t.lbConfig.Spec.PackageId != "" {
		packageId = *t.lbConfig.Spec.PackageId
	} else {
		// use default package from config, get default package id from name and zone
		listPackages, err := t.vngcloudRepo.ListLoadBalancerPackageByZone(ctx, privateZoneId)
		if err != nil {
			return nil, err
		}
		for _, pkg := range listPackages.Items {
			if pkg.Name == t.cfg.LoadBalancerOpts.DefaultL4PackageName {
				packageId = pkg.UUID
				break
			}
		}
		if packageId == "" {
			return nil, errs.NewNoNeedRequeue(fmt.Sprintf("cannot find default load balancer package %s in zone %s", t.cfg.LoadBalancerOpts.DefaultL4PackageName, privateZoneId))
		}
	}

	request := inter.NewCreateLoadBalancerRequest(
		strconv.Itoa(userId),
		t.lbConfig.Spec.LoadBalancerName,
		packageId,
		t.lbConfig.Spec.SubnetId,
		*t.lbConfig.Spec.PrivateSubnetId,
	).WithZoneId(privateZoneId)

	// TODO: add more fields (ignore listener and pool for now because have to create inter.ListenerRequest and inter.CreatePoolRequest)
	// WithListener(plistener ICreateListenerRequest) ICreateLoadBalancerRequest
	// WithPool(ppool ICreatePoolRequest) ICreateLoadBalancerRequest

	// if have tags, add tags
	ensuredTags := make(map[string]string)
	if t.lbConfig.Spec.Tags != nil {
		ensuredTags = t.lbConfig.Spec.Tags
	}

	_, newTags := t.buildTag(ctx, map[string]string{}, map[string]string{}, ensuredTags)
	if len(newTags) > 0 {
		tags := make([]string, 0)
		for k, v := range newTags {
			tags = append(tags, k, v)
		}
		request = request.WithTags(tags...)
	}

	return request, nil
}
