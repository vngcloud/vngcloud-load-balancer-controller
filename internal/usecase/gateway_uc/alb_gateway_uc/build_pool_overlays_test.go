package alb_gateway_uc

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	v2 "github.com/vngcloud/vngcloud-go-sdk/v2/vngcloud/services/loadbalancer/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwv1alpha2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/domain"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/utils"
)

func newTestTaskWithObjs(t *testing.T, gw *gwv1.Gateway, objs ...runtime.Object) *defaultGatewayBuildTask {
	s := newTestScheme()
	builder := fake.NewClientBuilder().WithScheme(s)
	for _, o := range objs {
		builder = builder.WithRuntimeObjects(o)
	}
	fakeClient := builder.Build()
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
		nameHelper:       utils.NewNameHelper("c1", "gateway", gw.Namespace, gw.Name),
	}
}

func makeBackendPolicy(ns, name, svcName string, alg *string, sticky *bool, tlsEnc *bool, targetType *string) *gwv1alpha1.VKSBackendPolicy {
	bp := &gwv1alpha1.VKSBackendPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: gwv1alpha1.VKSBackendPolicySpec{
			TargetRefs: []gwv1alpha2.LocalPolicyTargetReference{{
				Group: "",
				Kind:  "Service",
				Name:  gwv1alpha2.ObjectName(svcName),
			}},
			PoolAlgorithm:       alg,
			Stickiness:          sticky,
			EnableTLSEncryption: tlsEnc,
			TargetType:          targetType,
		},
	}
	return bp
}

func TestJoinExpectedCodes(t *testing.T) {
	assert.Equal(t, "", joinExpectedCodes(nil))
	assert.Equal(t, "200", joinExpectedCodes([]string{"200"}))
	assert.Equal(t, "200,301,404", joinExpectedCodes([]string{"200", "301", "404"}))
}

func TestApplyBackendPolicyToPool_NilPolicy(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTestTask(t, gw)
	pool := &v1alpha1.Pool{Protocol: v2.PoolProtocolHTTP}
	err := task.applyBackendPolicyToPool(context.Background(), pool, "prod", "svc-no-policy")
	assert.NoError(t, err)
	assert.Nil(t, pool.Algorithm)
	assert.Nil(t, pool.Stickiness)
	assert.Nil(t, pool.TLSEncryption)
}

func TestApplyBackendPolicyToPool_WithPolicy(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}

	alg := "ROUND_ROBIN"
	sticky := true
	tlsEnc := false
	bp := makeBackendPolicy("prod", "bp-1", "my-svc", &alg, &sticky, &tlsEnc, nil)

	s := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(bp).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient}
	task := &defaultGatewayBuildTask{uc: uc, gw: gw, logger: logrus.NewEntry(logrus.New()), listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy), nameHelper: utils.NewNameHelper("c1", "gateway", gw.Namespace, gw.Name)}

	pool := &v1alpha1.Pool{Protocol: v2.PoolProtocolHTTP}
	err := task.applyBackendPolicyToPool(context.Background(), pool, "prod", "my-svc")
	assert.NoError(t, err)
	assert.NotNil(t, pool.Algorithm)
	assert.Equal(t, v2.PoolAlgorithm("ROUND_ROBIN"), *pool.Algorithm)
	assert.NotNil(t, pool.Stickiness)
	assert.True(t, *pool.Stickiness)
	assert.NotNil(t, pool.TLSEncryption)
	assert.False(t, *pool.TLSEncryption)
}

func TestResolveTargetType(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}

	t.Run("no policy defaults to instance", func(t *testing.T) {
		task := newTestTask(t, gw)
		tt, err := task.resolveTargetType(context.Background(), "prod", "svc")
		assert.NoError(t, err)
		assert.Equal(t, domain.TargetTypeInstance, tt)
	})

	t.Run("policy with ip target type", func(t *testing.T) {
		ipType := string(domain.TargetTypeIP)
		bp := makeBackendPolicy("prod", "bp", "svc-ip", nil, nil, nil, &ipType)
		s := newTestScheme()
		fakeClient := fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(bp).Build()
		mockK8s := repository.NewMockK8sRepository(t)
		mockVng := repository.NewMockVngCloudRepository(t)
		uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient}
		task := &defaultGatewayBuildTask{uc: uc, gw: gw, logger: logrus.NewEntry(logrus.New()), listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy), nameHelper: utils.NewNameHelper("c1", "gateway", gw.Namespace, gw.Name)}
		tt, err := task.resolveTargetType(context.Background(), "prod", "svc-ip")
		assert.NoError(t, err)
		assert.Equal(t, domain.TargetTypeIP, tt)
	})

	t.Run("policy with instance target type", func(t *testing.T) {
		instType := string(domain.TargetTypeInstance)
		bp := makeBackendPolicy("prod", "bp", "svc-inst", nil, nil, nil, &instType)
		s := newTestScheme()
		fakeClient := fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(bp).Build()
		mockK8s := repository.NewMockK8sRepository(t)
		mockVng := repository.NewMockVngCloudRepository(t)
		uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient}
		task := &defaultGatewayBuildTask{uc: uc, gw: gw, logger: logrus.NewEntry(logrus.New()), listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy), nameHelper: utils.NewNameHelper("c1", "gateway", gw.Namespace, gw.Name)}
		tt, err := task.resolveTargetType(context.Background(), "prod", "svc-inst")
		assert.NoError(t, err)
		assert.Equal(t, domain.TargetTypeInstance, tt)
	})

	t.Run("policy with invalid target type falls back to instance", func(t *testing.T) {
		invalid := "invalid-type"
		bp := makeBackendPolicy("prod", "bp", "svc-bad", nil, nil, nil, &invalid)
		s := newTestScheme()
		fakeClient := fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(bp).Build()
		mockK8s := repository.NewMockK8sRepository(t)
		mockVng := repository.NewMockVngCloudRepository(t)
		uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient}
		task := &defaultGatewayBuildTask{uc: uc, gw: gw, logger: logrus.NewEntry(logrus.New()), listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy), nameHelper: utils.NewNameHelper("c1", "gateway", gw.Namespace, gw.Name)}
		tt, err := task.resolveTargetType(context.Background(), "prod", "svc-bad")
		assert.NoError(t, err)
		assert.Equal(t, domain.TargetTypeInstance, tt)
	})
}

func TestApplyHealthCheckPolicyToPool_NilPolicy(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	task := newTestTask(t, gw)
	pool := &v1alpha1.Pool{
		HealthMonitor: v1alpha1.PoolHealthMonitor{Protocol: v2.HealthCheckProtocolTCP},
	}
	err := task.applyHealthCheckPolicyToPool(context.Background(), pool, "prod", "svc-no-hc")
	assert.NoError(t, err)
	assert.Equal(t, v2.HealthCheckProtocolTCP, pool.HealthMonitor.Protocol)
}

func TestApplyHealthCheckPolicyToPool_WithPolicy(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}

	path := "/health"
	host := "app.example.com"
	interval := metav1.Duration{Duration: 10 * time.Second}
	timeout := metav1.Duration{Duration: 5 * time.Second}
	hThresh := int32(3)
	uThresh := int32(3)
	hp := &gwv1alpha1.VKSHealthCheckPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "hcp"},
		Spec: gwv1alpha1.VKSHealthCheckPolicySpec{
			TargetRefs:         []gwv1alpha2.LocalPolicyTargetReference{{Group: "", Kind: "Service", Name: "svc-hc"}},
			Protocol:           "HTTP",
			Interval:           &interval,
			Timeout:            &timeout,
			HealthyThreshold:   &hThresh,
			UnhealthyThreshold: &uThresh,
			HTTPHealthCheck: &gwv1alpha1.VKSHTTPHealthCheck{
				Path:          &path,
				Host:          &host,
				ExpectedCodes: []string{"200", "201"},
			},
		},
	}

	s := newTestScheme()
	fakeClient := fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(hp).Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient}
	task := &defaultGatewayBuildTask{uc: uc, gw: gw, logger: logrus.NewEntry(logrus.New()), listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy), nameHelper: utils.NewNameHelper("c1", "gateway", gw.Namespace, gw.Name)}

	pool := &v1alpha1.Pool{HealthMonitor: v1alpha1.PoolHealthMonitor{Protocol: v2.HealthCheckProtocolTCP}}
	err := task.applyHealthCheckPolicyToPool(context.Background(), pool, "prod", "svc-hc")
	assert.NoError(t, err)
	assert.Equal(t, v2.HealthCheckProtocol("HTTP"), pool.HealthMonitor.Protocol)
	assert.NotNil(t, pool.HealthMonitor.Interval)
	assert.Equal(t, 10, *pool.HealthMonitor.Interval)
	assert.NotNil(t, pool.HealthMonitor.Timeout)
	assert.Equal(t, 5, *pool.HealthMonitor.Timeout)
	assert.NotNil(t, pool.HealthMonitor.HealthCheckPath)
	assert.Equal(t, "/health", *pool.HealthMonitor.HealthCheckPath)
	assert.NotNil(t, pool.HealthMonitor.SuccessCode)
	assert.Equal(t, "200,201", *pool.HealthMonitor.SuccessCode)
	assert.NotNil(t, pool.HealthMonitor.HealthyThreshold)
	assert.Equal(t, 3, *pool.HealthMonitor.HealthyThreshold)
}

func makeHCPolicy(ns, name, svcName, protocol string) *gwv1alpha1.VKSHealthCheckPolicy {
	return &gwv1alpha1.VKSHealthCheckPolicy{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec: gwv1alpha1.VKSHealthCheckPolicySpec{
			TargetRefs: []gwv1alpha2.LocalPolicyTargetReference{{Group: "", Kind: "Service", Name: gwv1alpha2.ObjectName(svcName)}},
			Protocol:   protocol,
		},
	}
}

func TestApplyHealthCheckPolicyToPool_HTTPMethodVersionDefault(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	hp := makeHCPolicy("prod", "hcp", "svc-hc", "HTTP")
	task := newTestTaskWithObjs(t, gw, hp)

	pool := &v1alpha1.Pool{HealthMonitor: v1alpha1.PoolHealthMonitor{Protocol: v2.HealthCheckProtocolTCP}}
	err := task.applyHealthCheckPolicyToPool(context.Background(), pool, "prod", "svc-hc")
	assert.NoError(t, err)
	assert.NotNil(t, pool.HealthMonitor.HealthCheckMethod)
	assert.Equal(t, v2.HealthCheckMethodGET, *pool.HealthMonitor.HealthCheckMethod)
	assert.NotNil(t, pool.HealthMonitor.HttpVersion)
	assert.Equal(t, v2.HealthCheckHttpVersionHttp1Minor1, *pool.HealthMonitor.HttpVersion)
}

func TestApplyHealthCheckPolicyToPool_HTTPMethodVersionOverride(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	hp := makeHCPolicy("prod", "hcp", "svc-hc", "HTTP")
	method := "POST"
	version := "1.0"
	hp.Spec.HTTPHealthCheck = &gwv1alpha1.VKSHTTPHealthCheck{Method: &method, HTTPVersion: &version}
	task := newTestTaskWithObjs(t, gw, hp)

	pool := &v1alpha1.Pool{HealthMonitor: v1alpha1.PoolHealthMonitor{Protocol: v2.HealthCheckProtocolTCP}}
	err := task.applyHealthCheckPolicyToPool(context.Background(), pool, "prod", "svc-hc")
	assert.NoError(t, err)
	assert.NotNil(t, pool.HealthMonitor.HealthCheckMethod)
	assert.Equal(t, v2.HealthCheckMethodPOST, *pool.HealthMonitor.HealthCheckMethod)
	assert.NotNil(t, pool.HealthMonitor.HttpVersion)
	assert.Equal(t, v2.HealthCheckHttpVersionHttp1, *pool.HealthMonitor.HttpVersion)
}

func TestApplyHealthCheckPolicyToPool_TCPIgnoresHTTPMethodVersion(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	hp := makeHCPolicy("prod", "hcp", "svc-hc", "TCP")
	method := "POST"
	version := "1.0"
	hp.Spec.HTTPHealthCheck = &gwv1alpha1.VKSHTTPHealthCheck{Method: &method, HTTPVersion: &version}
	task := newTestTaskWithObjs(t, gw, hp)

	pool := &v1alpha1.Pool{HealthMonitor: v1alpha1.PoolHealthMonitor{Protocol: v2.HealthCheckProtocolTCP}}
	err := task.applyHealthCheckPolicyToPool(context.Background(), pool, "prod", "svc-hc")
	assert.NoError(t, err)
	assert.Equal(t, v2.HealthCheckProtocolTCP, pool.HealthMonitor.Protocol)
	assert.Nil(t, pool.HealthMonitor.HealthCheckMethod)
	assert.Nil(t, pool.HealthMonitor.HttpVersion)
}

func TestApplyHealthCheckPolicyToPool_PortOverridesMonitorPort(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	hp := makeHCPolicy("prod", "hcp", "svc-hc", "TCP")
	port := int32(8080)
	hp.Spec.Port = &port
	task := newTestTaskWithObjs(t, gw, hp)

	pool := &v1alpha1.Pool{
		HealthMonitor: v1alpha1.PoolHealthMonitor{Protocol: v2.HealthCheckProtocolTCP},
		Members: []v1alpha1.PoolMember{
			{Name: "a", IP: "10.0.0.1", Port: 30001, MonitorPort: 30001},
			{Name: "b", IP: "10.0.0.2", Port: 30002, MonitorPort: 30002},
		},
	}
	err := task.applyHealthCheckPolicyToPool(context.Background(), pool, "prod", "svc-hc")
	assert.NoError(t, err)
	assert.Equal(t, 8080, pool.Members[0].MonitorPort)
	assert.Equal(t, 8080, pool.Members[1].MonitorPort)
	// Traffic ports untouched.
	assert.Equal(t, 30001, pool.Members[0].Port)
	assert.Equal(t, 30002, pool.Members[1].Port)
}

func TestApplyHealthCheckPolicyToPool_NoPortLeavesMonitorPort(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}
	hp := makeHCPolicy("prod", "hcp", "svc-hc", "TCP")
	task := newTestTaskWithObjs(t, gw, hp)

	pool := &v1alpha1.Pool{
		HealthMonitor: v1alpha1.PoolHealthMonitor{Protocol: v2.HealthCheckProtocolTCP},
		Members:       []v1alpha1.PoolMember{{Name: "a", IP: "10.0.0.1", Port: 30001, MonitorPort: 30001}},
	}
	err := task.applyHealthCheckPolicyToPool(context.Background(), pool, "prod", "svc-hc")
	assert.NoError(t, err)
	assert.Equal(t, 30001, pool.Members[0].MonitorPort)
}

func TestResolveTargetNodeLabels(t *testing.T) {
	gw := &gwv1.Gateway{ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"}}

	t.Run("no policy returns nil", func(t *testing.T) {
		task := newTestTask(t, gw)
		labels, err := task.resolveTargetNodeLabels(context.Background(), "prod", "svc")
		assert.NoError(t, err)
		assert.Nil(t, labels)
	})

	t.Run("policy with node labels returns them", func(t *testing.T) {
		bp := makeBackendPolicy("prod", "bp", "svc-with-labels", nil, nil, nil, nil)
		bp.Spec.TargetNodeLabels = map[string]string{"node-role": "worker"}
		s := newTestScheme()
		fakeClient := fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(bp).Build()
		mockK8s := repository.NewMockK8sRepository(t)
		mockVng := repository.NewMockVngCloudRepository(t)
		uc := &albGatewayUseCase{k8sRepo: mockK8s, vngcloudRepo: mockVng, k8sClient: fakeClient}
		task := &defaultGatewayBuildTask{uc: uc, gw: gw, logger: logrus.NewEntry(logrus.New()), listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy), nameHelper: utils.NewNameHelper("c1", "gateway", gw.Namespace, gw.Name)}
		labels, err := task.resolveTargetNodeLabels(context.Background(), "prod", "svc-with-labels")
		assert.NoError(t, err)
		assert.Equal(t, map[string]string{"node-role": "worker"}, labels)
	})
}
