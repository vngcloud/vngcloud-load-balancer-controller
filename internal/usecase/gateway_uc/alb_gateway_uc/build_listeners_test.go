package alb_gateway_uc

import (
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	v2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1beta1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = gwv1.Install(s)
	_ = gwv1alpha1.AddToScheme(s)
	_ = v1alpha1.AddToScheme(s)
	_ = corev1.AddToScheme(s)
	_ = gwv1beta1.Install(s)
	return s
}

func newTestTask(t *testing.T, gw *gwv1.Gateway) *defaultGatewayBuildTask {
	s := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&gwv1.Gateway{}).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{
		k8sRepo:           mockK8s,
		vngcloudRepo:      mockVng,
		k8sClient:         fakeClient,
		defaultZone:       "HCM03-1C",
		defaultNetworkId:  "net-1",
		defaultSubnetId:   "subnet-1",
		defaultSubnetCIDR: "10.0.0.0/24",
		clusterId:         "cluster-1",
	}
	return &defaultGatewayBuildTask{
		uc:               uc,
		gw:               gw,
		logger:           logrus.NewEntry(logrus.New()),
		listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy),
		nameHelper:       utils.NewNameHelper("cluster-1", "gateway", gw.Namespace, gw.Name),
	}
}

func TestMapListenerProtocol(t *testing.T) {
	tests := []struct {
		name    string
		proto   gwv1.ProtocolType
		wantOk  bool
		wantVal v2.ListenerProtocol
	}{
		{"HTTP", gwv1.HTTPProtocolType, true, v2.ListenerProtocolHTTP},
		{"HTTPS", gwv1.HTTPSProtocolType, true, v2.ListenerProtocolHTTPS},
		{"TCP", gwv1.TCPProtocolType, false, ""},
		{"UDP", gwv1.UDPProtocolType, false, ""},
		{"TLS", gwv1.TLSProtocolType, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := mapListenerProtocol(tt.proto)
			assert.Equal(t, tt.wantOk, ok)
			if ok {
				assert.Equal(t, tt.wantVal, got)
			}
		})
	}
}

// TestCloudListenerName checks the per-Gateway listener name. The cloud
// enforces [a-zA-Z0-9_.-]{5,50}; the format is "vks_gw_<uid8>_<lname>".
func TestCloudListenerName(t *testing.T) {
	tests := []struct {
		name         string
		gwUID        string
		listenerName string
		maxLen       int
		checkPrefix  string
	}{
		{
			name:         "short name padded with uid prefix",
			gwUID:        "abcd1234-dead-beef-0000-111122223333",
			listenerName: "http",
			maxLen:       50,
			checkPrefix:  "vks_gw_abcd1234_http",
		},
		{
			name:         "long listener name truncated to 50 chars",
			gwUID:        "abcd1234-dead-beef-0000-111122223333",
			listenerName: "this-is-a-very-long-listener-name-that-exceeds-limit",
			maxLen:       50,
			checkPrefix:  "vks_gw_abcd1234_",
		},
		{
			name:         "uid shorter than 8 chars used as is",
			gwUID:        "short",
			listenerName: "http",
			maxLen:       50,
			checkPrefix:  "vks_gw_short_http",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gw := &gwv1.Gateway{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "prod",
					Name:      "my-gw",
					UID:       types.UID(tt.gwUID),
				},
			}
			task := newTestTask(t, gw)
			l := &gwv1.Listener{Name: gwv1.SectionName(tt.listenerName)}
			got := task.cloudListenerName(l)
			assert.LessOrEqual(t, len(got), tt.maxLen)
			assert.True(t, strings.HasPrefix(got, tt.checkPrefix),
				"expected prefix %q in %q", tt.checkPrefix, got)
		})
	}

	t.Run("deterministic across calls", func(t *testing.T) {
		gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{UID: "uid-1234"}}
		task := newTestTask(t, gw)
		l := &gwv1.Listener{Name: "http"}
		assert.Equal(t, task.cloudListenerName(l), task.cloudListenerName(l))
	})
}

func TestFlattenInsertHeaders(t *testing.T) {
	t.Run("empty map returns empty", func(t *testing.T) {
		got := flattenInsertHeaders(map[string]string{})
		assert.Empty(t, got)
	})
	t.Run("deterministic order regardless of map iteration", func(t *testing.T) {
		h := map[string]string{"Z": "3", "A": "1", "M": "2"}
		got := flattenInsertHeaders(h)
		assert.Len(t, got, 3)
		assert.Equal(t, "A", got[0].HeaderName)
		assert.Equal(t, "M", got[1].HeaderName)
		assert.Equal(t, "Z", got[2].HeaderName)
	})
	t.Run("values preserved", func(t *testing.T) {
		h := map[string]string{"X-Real-IP": "true"}
		got := flattenInsertHeaders(h)
		assert.Len(t, got, 1)
		assert.Equal(t, v1alpha1.InsertHeader{HeaderName: "X-Real-IP", HeaderValue: "true"}, got[0])
	})
}

func TestApplyListenerPolicy(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "my-gw"},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
			},
		},
	}

	t.Run("nil policies: timeouts/cidrs nil but InsertHeaders defaulted", func(t *testing.T) {
		task := newTestTask(t, gw)
		task.unscopedPolicy = nil
		entry := &v1alpha1.Listener{}
		task.applyListenerPolicy(entry, &gw.Spec.Listeners[0])
		assert.Nil(t, entry.TimeoutClient)
		assert.Nil(t, entry.TimeoutMember)
		assert.Nil(t, entry.TimeoutConnection)
		assert.Nil(t, entry.AllowedCidrs)
		// InsertHeaders defaults to the X-Forwarded-* triplet when no policy
		// supplies a value — same default the Ingress controller emits.
		names := make([]string, 0, len(entry.InsertHeaders))
		for _, h := range entry.InsertHeaders {
			names = append(names, h.HeaderName)
		}
		assert.ElementsMatch(t, []string{"X-Forwarded-For", "X-Forwarded-Proto", "X-Forwarded-Port"}, names)
	})

	t.Run("unscoped policy applies timeout", func(t *testing.T) {
		task := newTestTask(t, gw)
		d := metav1.Duration{Duration: 30 * time.Second}
		task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{}
		task.unscopedPolicy.Spec.TimeoutClient = &d
		entry := &v1alpha1.Listener{}
		task.applyListenerPolicy(entry, &gw.Spec.Listeners[0])
		assert.NotNil(t, entry.TimeoutClient)
		assert.Equal(t, int32(30), *entry.TimeoutClient)
	})

	t.Run("per-listener policy wins over unscoped for timeout", func(t *testing.T) {
		task := newTestTask(t, gw)
		unscopedD := metav1.Duration{Duration: 10 * time.Second}
		task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{}
		task.unscopedPolicy.Spec.TimeoutClient = &unscopedD

		scopedD := metav1.Duration{Duration: 60 * time.Second}
		scoped := &gwv1alpha1.VKSGatewayPolicy{}
		scoped.Spec.TimeoutClient = &scopedD
		task.listenerPolicies["http"] = scoped

		entry := &v1alpha1.Listener{}
		task.applyListenerPolicy(entry, &gw.Spec.Listeners[0])
		assert.NotNil(t, entry.TimeoutClient)
		assert.Equal(t, int32(60), *entry.TimeoutClient)
	})

	t.Run("AllowedCIDRs joined with comma", func(t *testing.T) {
		task := newTestTask(t, gw)
		task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{}
		task.unscopedPolicy.Spec.AllowedCIDRs = []string{"10.0.0.0/8", "192.168.0.0/16"}
		entry := &v1alpha1.Listener{}
		task.applyListenerPolicy(entry, &gw.Spec.Listeners[0])
		assert.NotNil(t, entry.AllowedCidrs)
		assert.Equal(t, "10.0.0.0/8,192.168.0.0/16", *entry.AllowedCidrs)
	})

	t.Run("InsertHeaders applied from unscoped", func(t *testing.T) {
		task := newTestTask(t, gw)
		task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{}
		task.unscopedPolicy.Spec.InsertHeaders = map[string]string{"X-Forwarded-For": "true"}
		entry := &v1alpha1.Listener{}
		task.applyListenerPolicy(entry, &gw.Spec.Listeners[0])
		assert.Len(t, entry.InsertHeaders, 1)
		assert.Equal(t, "X-Forwarded-For", entry.InsertHeaders[0].HeaderName)
	})

	t.Run("ClientCertificateId applied from policy", func(t *testing.T) {
		task := newTestTask(t, gw)
		certID := "cert-123"
		task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{}
		task.unscopedPolicy.Spec.ClientCertificateID = &certID
		entry := &v1alpha1.Listener{}
		task.applyListenerPolicy(entry, &gw.Spec.Listeners[0])
		assert.NotNil(t, entry.ClientCertificateId)
		assert.Equal(t, "cert-123", *entry.ClientCertificateId)
	})

	t.Run("AllowedCIDRs: per-listener overrides unscoped", func(t *testing.T) {
		task := newTestTask(t, gw)
		task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{}
		task.unscopedPolicy.Spec.AllowedCIDRs = []string{"0.0.0.0/0"}
		scoped := &gwv1alpha1.VKSGatewayPolicy{}
		scoped.Spec.AllowedCIDRs = []string{"10.0.0.0/8"}
		task.listenerPolicies["http"] = scoped
		entry := &v1alpha1.Listener{}
		task.applyListenerPolicy(entry, &gw.Spec.Listeners[0])
		assert.Equal(t, "10.0.0.0/8", *entry.AllowedCidrs)
	})
}

func TestBuildListeners_AllUnsupported(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "my-gw"},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "tcp", Protocol: gwv1.TCPProtocolType, Port: 443},
			},
		},
	}
	task := newTestTask(t, gw)
	_, err := task.buildListeners()
	assert.Error(t, err) // all listeners unsupported → error
}

func TestBuildListeners_MixedProtocols(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "my-gw", UID: "uid-abc"},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
				{Name: "tcp", Protocol: gwv1.TCPProtocolType, Port: 9000},
				{Name: "https", Protocol: gwv1.HTTPSProtocolType, Port: 443},
			},
		},
	}
	task := newTestTask(t, gw)
	listeners, err := task.buildListeners()
	assert.NoError(t, err)
	assert.Len(t, listeners, 2) // TCP skipped
}
