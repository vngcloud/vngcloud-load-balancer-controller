package k8s_repo

import (
	"context"
	"fmt"
	"net"
	"os"
	"slices"
	"time"

	"github.com/anngdinh/operator-helper/contexts"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
)

func NewK8sRepository(client client.Client) repository.K8sRepository {
	return &k8sRepository{
		client: client,
	}
}

type k8sRepository struct {
	client client.Client
}

func (r *k8sRepository) Client() client.Client { return r.client }

func (r *k8sRepository) GetService(ctx context.Context, n types.NamespacedName) (*corev1.Service, error) {
	svc := &corev1.Service{}
	err := r.client.Get(ctx, n, svc)
	return svc, err
}

func (r *k8sRepository) UpdateServiceStatusAddress(ctx context.Context, n types.NamespacedName, address string) error {
	if address == "" {
		return nil
	}

	// Update the service with the new address
	svc := &corev1.Service{}
	err := r.client.Get(ctx, n, svc)
	if err != nil {
		return client.IgnoreNotFound(err)
	}

	objectOld := svc.DeepCopy()

	addr := net.ParseIP(address)

	if svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
		// For LoadBalancer: update status.loadBalancer.ingress
		var newHostname string
		if addr != nil {
			newHostname = address + ".nip.io"
		} else {
			newHostname = address
		}

		// Check if hostname already exists
		if len(svc.Status.LoadBalancer.Ingress) > 0 && svc.Status.LoadBalancer.Ingress[0].Hostname == newHostname {
			return nil // already exists
		}

		svc.Status.LoadBalancer.Ingress = []corev1.LoadBalancerIngress{{Hostname: newHostname}}
		logger := contexts.NewContext(ctx).Log()
		logger.Infof("%s Updating Service LoadBalancer status address %s/%s", domain.RequestIcon, n.Namespace, n.Name)
		return r.client.Status().Patch(ctx, svc, client.MergeFrom(objectOld))
	}

	// For NodePort/ClusterIP: update spec.externalIPs (only if address is an IP)
	if addr != nil {
		// Check if IP already exists in externalIPs
		if slices.Contains(svc.Spec.ExternalIPs, address) {
			return nil // already exists
		}
		svc.Spec.ExternalIPs = append(svc.Spec.ExternalIPs, address)

		logger := contexts.NewContext(ctx).Log()
		logger.Infof("%s Updating Service NodePort externalIPs address %s/%s", domain.RequestIcon, n.Namespace, n.Name)
		return r.client.Patch(ctx, svc, client.MergeFrom(objectOld))
	}

	return nil
}

func (r *k8sRepository) ListNode(ctx context.Context, list *corev1.NodeList, opts ...client.ListOption) error {
	return r.client.List(ctx, list, opts...)
}

func (r *k8sRepository) GetLoadBalancerConfig(ctx context.Context, n types.NamespacedName) (*v1alpha1.LoadBalancerConfig, error) {
	lbc := &v1alpha1.LoadBalancerConfig{}
	err := r.client.Get(ctx, n, lbc)
	return lbc, err
}

func (r *k8sRepository) CreateLoadBalancerConfig(ctx context.Context, lbc *v1alpha1.LoadBalancerConfig, opts ...client.CreateOption) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Creating LoadBalancerConfig %s/%s", domain.RequestIcon, lbc.Namespace, lbc.Name)

	err := r.client.Create(ctx, lbc, opts...)
	if err != nil {
		return err
	}

	// retry 3 times to ensure object is created (eventual consistency)
	for i := 0; i < 3; i++ {
		err = r.client.Get(ctx, types.NamespacedName{Namespace: lbc.GetNamespace(), Name: lbc.GetName()}, lbc)
		if err == nil {
			return nil
		}
		if client.IgnoreNotFound(err) != nil {
			return err // non-NotFound error, return immediately
		}
		logger.Warn("Create returned nil but object not found, retrying...")
		time.Sleep(250 * time.Millisecond)
	}
	return err
}

func (r *k8sRepository) DeleteLoadBalancerConfig(ctx context.Context, lbc *v1alpha1.LoadBalancerConfig) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Deleting LoadBalancerConfig %s/%s", domain.RequestIcon, lbc.Namespace, lbc.Name)
	return r.client.Delete(ctx, lbc)
}

func (r *k8sRepository) PatchLoadBalancerConfig(ctx context.Context, lbc *v1alpha1.LoadBalancerConfig, patch client.Patch, opts ...client.PatchOption) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Patching LoadBalancerConfig %s/%s", domain.RequestIcon, lbc.Namespace, lbc.Name)
	return r.client.Patch(ctx, lbc, patch, opts...)
}

func (r *k8sRepository) UpdateLoadBalancerConfig(ctx context.Context, lbc *v1alpha1.LoadBalancerConfig, opts ...client.UpdateOption) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Updating LoadBalancerConfig %s/%s", domain.RequestIcon, lbc.Namespace, lbc.Name)
	return r.client.Update(ctx, lbc, opts...)
}

func (r *k8sRepository) PatchMutateStatusLoadBalancerConfig(
	ctx context.Context,
	lbc *v1alpha1.LoadBalancerConfig,
	mutate func(ctx context.Context, obj *v1alpha1.LoadBalancerConfig) bool,
) error {
	return r.patchMutateStatusObject(ctx, lbc, func(ctx context.Context, obj client.Object) bool {
		// type-assert so you can use strongly typed fields
		return mutate(ctx, obj.(*v1alpha1.LoadBalancerConfig))
	})
}

func (r *k8sRepository) patchMutateStatusObject(
	ctx context.Context,
	obj client.Object,
	mutate func(ctx context.Context, obj client.Object) bool,
) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		// get fresh copy
		objGet := obj.DeepCopyObject().(client.Object)
		if err := r.client.Get(ctx, types.NamespacedName{
			Namespace: obj.GetNamespace(),
			Name:      obj.GetName(),
		}, objGet); err != nil {
			return err
		}

		// deep copy for diff/patch base
		oldObject := objGet.DeepCopyObject().(client.Object)

		// mutate the fetched object (not the input), skip patch if no change needed
		if !mutate(ctx, objGet) {
			return nil
		}

		diff := cmp.Diff(oldObject, objGet,
			// Ignore metadata and spec fields - we only care about Status changes
			cmpopts.IgnoreTypes(metav1.ObjectMeta{}, metav1.TypeMeta{}),
			cmpopts.IgnoreFields(v1alpha1.LoadBalancerConfig{}, "Spec"),
			cmpopts.IgnoreFields(v1alpha1.NodeSecurityGroup{}, "Spec"),
		)
		if diff != "" && logrus.IsLevelEnabled(logrus.DebugLevel) {
			// Print directly to stderr for clean, unescaped formatting
			fmt.Fprintf(os.Stderr, "\n%s  status diff (before mutation -> after mutation):\n%s\n", domain.DebugIcon, diff)
		}

		// patch only status, with optimistic lock
		logger := contexts.NewContext(ctx).Log()
		logger.Infof("%s Patching status %s/%s", domain.RequestIcon, objGet.GetNamespace(), objGet.GetName())
		return r.client.Status().Patch(ctx, objGet,
			client.MergeFromWithOptions(oldObject, client.MergeFromWithOptimisticLock{}))
	})
}

func (r *k8sRepository) ListLoadBalancerConfig(ctx context.Context, list *v1alpha1.LoadBalancerConfigList, opts ...client.ListOption) error {
	return r.client.List(ctx, list, opts...)
}

// NodeSecurityGroup methods would go here

func (r *k8sRepository) GetNodeSecurityGroup(ctx context.Context, n types.NamespacedName) (*v1alpha1.NodeSecurityGroup, error) {
	nsg := &v1alpha1.NodeSecurityGroup{}
	err := r.client.Get(ctx, n, nsg)
	return nsg, err
}

func (r *k8sRepository) ListNodeSecurityGroup(ctx context.Context, list *v1alpha1.NodeSecurityGroupList, opts ...client.ListOption) error {
	return r.client.List(ctx, list, opts...)
}

func (r *k8sRepository) CreateNodeSecurityGroup(ctx context.Context, nsg *v1alpha1.NodeSecurityGroup, opts ...client.CreateOption) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Creating NodeSecurityGroup %s/%s", domain.RequestIcon, nsg.Namespace, nsg.Name)

	err := r.client.Create(ctx, nsg, opts...)
	if err != nil {
		return err
	}

	// retry 3 times to ensure object is created (eventual consistency)
	for i := 0; i < 3; i++ {
		err = r.client.Get(ctx, types.NamespacedName{Namespace: nsg.GetNamespace(), Name: nsg.GetName()}, nsg)
		if err == nil {
			return nil
		}
		if client.IgnoreNotFound(err) != nil {
			return err // non-NotFound error, return immediately
		}
		logger.Warn("Create returned nil but object not found, retrying...")
		time.Sleep(250 * time.Millisecond)
	}
	return err
}

func (r *k8sRepository) DeleteNodeSecurityGroup(ctx context.Context, nsg *v1alpha1.NodeSecurityGroup) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Deleting NodeSecurityGroup %s/%s", domain.RequestIcon, nsg.Namespace, nsg.Name)
	return r.client.Delete(ctx, nsg)
}

func (r *k8sRepository) PatchNodeSecurityGroup(ctx context.Context, nsg *v1alpha1.NodeSecurityGroup, patch client.Patch, opts ...client.PatchOption) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Patching NodeSecurityGroup %s/%s", domain.RequestIcon, nsg.Namespace, nsg.Name)
	return r.client.Patch(ctx, nsg, patch, opts...)
}

func (r *k8sRepository) PatchMutateStatusNodeSecurityGroup(
	ctx context.Context,
	nsg *v1alpha1.NodeSecurityGroup,
	mutate func(ctx context.Context, obj *v1alpha1.NodeSecurityGroup) bool,
) error {
	return r.patchMutateStatusObject(ctx, nsg, func(ctx context.Context, obj client.Object) bool {
		// type-assert so you can use strongly typed fields
		return mutate(ctx, obj.(*v1alpha1.NodeSecurityGroup))
	})
}

// ------------------- ingress ------------------

func (r *k8sRepository) GetIngress(ctx context.Context, n types.NamespacedName) (*networkingv1.Ingress, error) {
	ing := &networkingv1.Ingress{}
	err := r.client.Get(ctx, n, ing)
	return ing, err
}

func (r *k8sRepository) UpdateIngressStatusAddress(ctx context.Context, n types.NamespacedName, address string) error {
	if address == "" {
		return nil
	}

	// Update the ingress status with the new address
	ing := &networkingv1.Ingress{}
	err := r.client.Get(ctx, n, ing)
	if err != nil {
		return client.IgnoreNotFound(err)
	}

	addr := net.ParseIP(address)
	var newHostname string
	if addr != nil {
		newHostname = address + ".nip.io"
	} else {
		newHostname = address
	}

	// Check if hostname already exists
	if len(ing.Status.LoadBalancer.Ingress) > 0 && ing.Status.LoadBalancer.Ingress[0].Hostname == newHostname {
		return nil // already exists
	}

	objectOld := ing.DeepCopy()
	ing.Status.LoadBalancer.Ingress = []networkingv1.IngressLoadBalancerIngress{{Hostname: newHostname}}
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Updating Ingress status address %s/%s", domain.RequestIcon, n.Namespace, n.Name)
	return r.client.Status().Patch(ctx, ing, client.MergeFrom(objectOld))
}

// ------------------- secret ------------------

func (r *k8sRepository) GetSecret(ctx context.Context, n types.NamespacedName) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	err := r.client.Get(ctx, n, secret)
	return secret, err
}

// ------------------- global load balancer config ------------------

func (r *k8sRepository) GetGlobalLoadBalancerConfig(ctx context.Context, n types.NamespacedName) (*v1alpha1.GlobalLoadBalancerConfig, error) {
	glbc := &v1alpha1.GlobalLoadBalancerConfig{}
	err := r.client.Get(ctx, n, glbc)
	return glbc, err
}

func (r *k8sRepository) ListGlobalLoadBalancerConfig(ctx context.Context, list *v1alpha1.GlobalLoadBalancerConfigList, opts ...client.ListOption) error {
	return r.client.List(ctx, list, opts...)
}

func (r *k8sRepository) CreateGlobalLoadBalancerConfig(ctx context.Context, glbc *v1alpha1.GlobalLoadBalancerConfig, opts ...client.CreateOption) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Creating GlobalLoadBalancerConfig %s/%s", domain.RequestIcon, glbc.Namespace, glbc.Name)

	err := r.client.Create(ctx, glbc, opts...)
	if err != nil {
		return err
	}

	// retry 3 times to ensure object is created (eventual consistency)
	for i := 0; i < 3; i++ {
		err = r.client.Get(ctx, types.NamespacedName{Namespace: glbc.GetNamespace(), Name: glbc.GetName()}, glbc)
		if err == nil {
			return nil
		}
		if client.IgnoreNotFound(err) != nil {
			return err // non-NotFound error, return immediately
		}
		logger.Warn("Create returned nil but object not found, retrying...")
		time.Sleep(250 * time.Millisecond)
	}
	return err
}

func (r *k8sRepository) DeleteGlobalLoadBalancerConfig(ctx context.Context, glbc *v1alpha1.GlobalLoadBalancerConfig) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Deleting GlobalLoadBalancerConfig %s/%s", domain.RequestIcon, glbc.Namespace, glbc.Name)
	return r.client.Delete(ctx, glbc)
}

func (r *k8sRepository) PatchGlobalLoadBalancerConfig(ctx context.Context, glbc *v1alpha1.GlobalLoadBalancerConfig, patch client.Patch, opts ...client.PatchOption) error {
	logger := contexts.NewContext(ctx).Log()
	logger.Infof("%s Patching GlobalLoadBalancerConfig %s/%s", domain.RequestIcon, glbc.Namespace, glbc.Name)
	return r.client.Patch(ctx, glbc, patch, opts...)
}

func (r *k8sRepository) PatchMutateStatusGlobalLoadBalancerConfig(
	ctx context.Context,
	glbc *v1alpha1.GlobalLoadBalancerConfig,
	mutate func(ctx context.Context, obj *v1alpha1.GlobalLoadBalancerConfig) bool,
) error {
	return r.patchMutateStatusObject(ctx, glbc, func(ctx context.Context, obj client.Object) bool {
		// type-assert so you can use strongly typed fields
		return mutate(ctx, obj.(*v1alpha1.GlobalLoadBalancerConfig))
	})
}

// ------------------- vngcloud global load balancer ------------------

func (r *k8sRepository) GetVngcloudGlobalLoadBalancer(ctx context.Context, n types.NamespacedName) (*v1alpha1.VngcloudGlobalLoadBalancer, error) {
	vglb := &v1alpha1.VngcloudGlobalLoadBalancer{}
	err := r.client.Get(ctx, n, vglb)
	return vglb, err
}

func (r *k8sRepository) PatchMutateStatusVngcloudGlobalLoadBalancer(
	ctx context.Context,
	vglb *v1alpha1.VngcloudGlobalLoadBalancer,
	mutate func(ctx context.Context, obj *v1alpha1.VngcloudGlobalLoadBalancer) bool,
) error {
	return r.patchMutateStatusObject(ctx, vglb, func(ctx context.Context, obj client.Object) bool {
		// type-assert so you can use strongly typed fields
		return mutate(ctx, obj.(*v1alpha1.VngcloudGlobalLoadBalancer))
	})
}
