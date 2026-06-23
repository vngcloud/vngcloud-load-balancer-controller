package vglb_uc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	global "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/glb/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/annotations"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

func TestBuildLoadBalancerName(t *testing.T) {
	const clusterID = "test-cluster"

	// expectedDefault returns the value GetLoadBalancerDefaultName produces
	// for the given vglb identity. We delegate to the same NameHelper the
	// production code uses so this test verifies wiring (annotation override
	// vs. default fall-through) without coupling to the helper's internal
	// hash/trim format.
	expectedDefault := func(namespace, name string) string {
		return utils.NewNameHelper(clusterID, "vglb", namespace, name).GetLoadBalancerDefaultName()
	}

	tests := []struct {
		name         string
		vglbName     string
		namespace    string
		annotations  map[string]string
		expectedName string
		description  string
	}{
		{
			name:      "with_custom_name_annotation",
			vglbName:  "my-vglb",
			namespace: "default",
			annotations: map[string]string{
				domain.VGLB_ANNOTATION_PREFIX + "/" + annotations.SuffixLoadBalancerName: "custom-glb-name",
			},
			expectedName: "custom-glb-name",
			description:  "should return the custom load balancer name from annotation",
		},
		{
			name:         "without_annotation_uses_default",
			vglbName:     "my-vglb",
			namespace:    "default",
			annotations:  map[string]string{},
			expectedName: expectedDefault("default", "my-vglb"),
			description:  "should fall through to NameHelper.GetLoadBalancerDefaultName when no annotation",
		},
		{
			name:      "empty_annotation_uses_default",
			vglbName:  "test-vglb",
			namespace: "production",
			annotations: map[string]string{
				domain.VGLB_ANNOTATION_PREFIX + "/" + annotations.SuffixLoadBalancerName: "",
			},
			expectedName: expectedDefault("production", "test-vglb"),
			description:  "should fall through to default when annotation is empty",
		},
		{
			name:         "with_special_characters_in_name",
			vglbName:     "my-test-vglb-123",
			namespace:    "my-namespace",
			annotations:  map[string]string{},
			expectedName: expectedDefault("my-namespace", "my-test-vglb-123"),
			description:  "should fall through to default and handle special characters via NameHelper",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vglb := &v1alpha1.VngcloudGlobalLoadBalancer{
				ObjectMeta: metav1.ObjectMeta{
					Name:        tt.vglbName,
					Namespace:   tt.namespace,
					Annotations: tt.annotations,
				},
			}

			annotationParser := annotations.NewSuffixAnnotationParser(domain.VGLB_ANNOTATION_PREFIX)

			task := &defaultModelBuildTask{
				vglb:             vglb,
				annotationParser: annotationParser,
				nameHelper:       utils.NewNameHelper(clusterID, "vglb", tt.namespace, tt.vglbName),
			}

			result := task.buildLoadBalancerName(context.Background())

			assert.Equal(t, tt.expectedName, result, tt.description)
		})
	}
}

func TestBuildType(t *testing.T) {
	task := &defaultModelBuildTask{}

	result := task.buildType(context.Background())

	assert.Equal(t, global.GlobalLoadBalancerTypeLayer4, result, "should return Layer4 type")
}

func TestBuildLoadBalancerId(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		expectedId  *string
		description string
	}{
		{
			name: "with_load_balancer_id_annotation",
			annotations: map[string]string{
				domain.VGLB_ANNOTATION_PREFIX + "/" + annotations.SuffixLoadBalancerID: "lb-12345",
			},
			expectedId:  strPtr("lb-12345"),
			description: "should return the load balancer ID from annotation",
		},
		{
			name:        "without_annotation",
			annotations: map[string]string{},
			expectedId:  nil,
			description: "should return nil when no annotation is provided",
		},
		{
			name: "with_empty_annotation",
			annotations: map[string]string{
				domain.VGLB_ANNOTATION_PREFIX + "/" + annotations.SuffixLoadBalancerID: "",
			},
			expectedId:  nil,
			description: "should return nil when annotation is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vglb := &v1alpha1.VngcloudGlobalLoadBalancer{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tt.annotations,
				},
			}

			annotationParser := annotations.NewSuffixAnnotationParser(domain.VGLB_ANNOTATION_PREFIX)

			task := &defaultModelBuildTask{
				vglb:             vglb,
				annotationParser: annotationParser,
			}

			result := task.buildLoadBalancerId(context.Background())

			if tt.expectedId == nil {
				assert.Nil(t, result, tt.description)
			} else {
				assert.NotNil(t, result, tt.description)
				assert.Equal(t, *tt.expectedId, *result, tt.description)
			}
		})
	}
}

func TestBuildPackageId(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		expectedId  *string
		description string
	}{
		{
			name: "with_package_id_annotation",
			annotations: map[string]string{
				domain.VGLB_ANNOTATION_PREFIX + "/" + annotations.SuffixPackageID: "pkg-small",
			},
			expectedId:  strPtr("pkg-small"),
			description: "should return the package ID from annotation",
		},
		{
			name:        "without_annotation",
			annotations: map[string]string{},
			expectedId:  nil,
			description: "should return nil when no annotation is provided",
		},
		{
			name: "with_empty_annotation",
			annotations: map[string]string{
				domain.VGLB_ANNOTATION_PREFIX + "/" + annotations.SuffixPackageID: "",
			},
			expectedId:  nil,
			description: "should return nil when annotation is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vglb := &v1alpha1.VngcloudGlobalLoadBalancer{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tt.annotations,
				},
			}

			annotationParser := annotations.NewSuffixAnnotationParser(domain.VGLB_ANNOTATION_PREFIX)

			task := &defaultModelBuildTask{
				vglb:             vglb,
				annotationParser: annotationParser,
			}

			result := task.buildPackageId(context.Background())

			if tt.expectedId == nil {
				assert.Nil(t, result, tt.description)
			} else {
				assert.NotNil(t, result, tt.description)
				assert.Equal(t, *tt.expectedId, *result, tt.description)
			}
		})
	}
}

func TestBuildDescription(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		expected    *string
		description string
	}{
		{
			name: "with_description_annotation",
			annotations: map[string]string{
				domain.VGLB_ANNOTATION_PREFIX + "/" + annotations.SuffixDescription: "My GLB description",
			},
			expected:    strPtr("My GLB description"),
			description: "should return the description from annotation",
		},
		{
			name:        "without_annotation",
			annotations: map[string]string{},
			expected:    nil,
			description: "should return nil when no annotation is provided",
		},
		{
			name: "with_empty_annotation",
			annotations: map[string]string{
				domain.VGLB_ANNOTATION_PREFIX + "/" + annotations.SuffixDescription: "",
			},
			expected:    nil,
			description: "should return nil when annotation is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vglb := &v1alpha1.VngcloudGlobalLoadBalancer{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tt.annotations,
				},
			}

			annotationParser := annotations.NewSuffixAnnotationParser(domain.VGLB_ANNOTATION_PREFIX)

			task := &defaultModelBuildTask{
				vglb:             vglb,
				annotationParser: annotationParser,
			}

			result := task.buildDescription(context.Background())

			if tt.expected == nil {
				assert.Nil(t, result, tt.description)
			} else {
				assert.NotNil(t, result, tt.description)
				assert.Equal(t, *tt.expected, *result, tt.description)
			}
		})
	}
}

func TestGlbcSpecEqual(t *testing.T) {
	tests := []struct {
		name     string
		a        v1alpha1.GlobalLoadBalancerConfigSpec
		b        v1alpha1.GlobalLoadBalancerConfigSpec
		expected bool
	}{
		{
			name: "equal_specs_basic",
			a: v1alpha1.GlobalLoadBalancerConfigSpec{
				Name: "test",
				Type: global.GlobalLoadBalancerTypeLayer4,
			},
			b: v1alpha1.GlobalLoadBalancerConfigSpec{
				Name: "test",
				Type: global.GlobalLoadBalancerTypeLayer4,
			},
			expected: true,
		},
		{
			name: "different_names",
			a: v1alpha1.GlobalLoadBalancerConfigSpec{
				Name: "test1",
				Type: global.GlobalLoadBalancerTypeLayer4,
			},
			b: v1alpha1.GlobalLoadBalancerConfigSpec{
				Name: "test2",
				Type: global.GlobalLoadBalancerTypeLayer4,
			},
			expected: false,
		},
		{
			name: "equal_with_pools_and_listeners",
			a: v1alpha1.GlobalLoadBalancerConfigSpec{
				Name: "test",
				Type: global.GlobalLoadBalancerTypeLayer4,
				GlobalPools: []v1alpha1.GlobalPool{
					{Name: "pool-1", Protocol: global.GlobalPoolProtocolTCP},
				},
				GlobalListeners: []v1alpha1.GlobalListener{
					{Name: "listener-1", Protocol: global.GlobalListenerProtocolTCP, ProtocolPort: 80},
				},
			},
			b: v1alpha1.GlobalLoadBalancerConfigSpec{
				Name: "test",
				Type: global.GlobalLoadBalancerTypeLayer4,
				GlobalPools: []v1alpha1.GlobalPool{
					{Name: "pool-1", Protocol: global.GlobalPoolProtocolTCP},
				},
				GlobalListeners: []v1alpha1.GlobalListener{
					{Name: "listener-1", Protocol: global.GlobalListenerProtocolTCP, ProtocolPort: 80},
				},
			},
			expected: true,
		},
		{
			name: "different_pool_names",
			a: v1alpha1.GlobalLoadBalancerConfigSpec{
				Name: "test",
				Type: global.GlobalLoadBalancerTypeLayer4,
				GlobalPools: []v1alpha1.GlobalPool{
					{Name: "pool-1", Protocol: global.GlobalPoolProtocolTCP},
				},
			},
			b: v1alpha1.GlobalLoadBalancerConfigSpec{
				Name: "test",
				Type: global.GlobalLoadBalancerTypeLayer4,
				GlobalPools: []v1alpha1.GlobalPool{
					{Name: "pool-2", Protocol: global.GlobalPoolProtocolTCP},
				},
			},
			expected: false,
		},
		{
			name: "different_listener_ports",
			a: v1alpha1.GlobalLoadBalancerConfigSpec{
				Name: "test",
				Type: global.GlobalLoadBalancerTypeLayer4,
				GlobalListeners: []v1alpha1.GlobalListener{
					{Name: "listener-1", Protocol: global.GlobalListenerProtocolTCP, ProtocolPort: 80},
				},
			},
			b: v1alpha1.GlobalLoadBalancerConfigSpec{
				Name: "test",
				Type: global.GlobalLoadBalancerTypeLayer4,
				GlobalListeners: []v1alpha1.GlobalListener{
					{Name: "listener-1", Protocol: global.GlobalListenerProtocolTCP, ProtocolPort: 443},
				},
			},
			expected: false,
		},
		{
			name: "different_pool_count",
			a: v1alpha1.GlobalLoadBalancerConfigSpec{
				Name: "test",
				GlobalPools: []v1alpha1.GlobalPool{
					{Name: "pool-1"},
					{Name: "pool-2"},
				},
			},
			b: v1alpha1.GlobalLoadBalancerConfigSpec{
				Name: "test",
				GlobalPools: []v1alpha1.GlobalPool{
					{Name: "pool-1"},
				},
			},
			expected: false,
		},
		{
			name:     "both_empty",
			a:        v1alpha1.GlobalLoadBalancerConfigSpec{},
			b:        v1alpha1.GlobalLoadBalancerConfigSpec{},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := glbcSpecEqual(tt.a, tt.b)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Helper functions

func strPtr(s string) *string {
	return &s
}
