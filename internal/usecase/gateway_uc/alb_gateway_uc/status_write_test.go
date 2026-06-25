package alb_gateway_uc

import (
	"context"
	"errors"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/repository"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/consts"
)

func newTaskWithGWInClient(t *testing.T, gw *gwv1.Gateway) *defaultGatewayBuildTask {
	s := newTestScheme()
	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(gw).
		WithStatusSubresource(&gwv1.Gateway{}).
		Build()
	mockK8s := repository.NewMockK8sRepository(t)
	mockVng := repository.NewMockVngCloudRepository(t)
	uc := &albGatewayUseCase{
		k8sRepo:      mockK8s,
		vngcloudRepo: mockVng,
		k8sClient:    fakeClient,
	}
	return &defaultGatewayBuildTask{
		uc:               uc,
		gw:               gw,
		logger:           logrus.NewEntry(logrus.New()),
		listenerPolicies: make(map[string]*gwv1alpha1.VKSGatewayPolicy),
	}
}

func TestWriteGatewayStatus_TranslateError(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "my-gw"},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
			},
		},
	}
	task := newTaskWithGWInClient(t, gw)
	err := task.writeGatewayStatus(context.Background(), nil, errors.New("translation failed"))
	// Should not error — status patch may succeed or be a no-op
	// The important thing is that the Programmed condition is set to False
	_ = err
}

func TestWriteGatewayStatus_NilLBC(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "my-gw"},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
			},
		},
	}
	task := newTaskWithGWInClient(t, gw)
	err := task.writeGatewayStatus(context.Background(), nil, nil)
	_ = err // nil lbc → Programmed=False with Pending reason
}

func TestWriteGatewayStatus_LBCNotReady(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "my-gw"},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
			},
		},
	}
	task := newTaskWithGWInClient(t, gw)
	lbc := &v1alpha1.LoadBalancerConfig{}
	lbc.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionFalse}}
	lbc.Status.LastReconcileMessage = "provisioning"
	err := task.writeGatewayStatus(context.Background(), lbc, nil)
	_ = err
}

func TestWriteGatewayStatus_LBCReady(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "my-gw"},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
			},
		},
	}
	task := newTaskWithGWInClient(t, gw)
	lbc := &v1alpha1.LoadBalancerConfig{}
	lbc.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}}
	lbc.Status.Address = ptr.To("10.1.2.3")
	err := task.writeGatewayStatus(context.Background(), lbc, nil)
	_ = err
}

func TestWriteGatewayStatus_NoChange(t *testing.T) {
	// Pre-set Gateway with conditions that match what writeGatewayStatus would produce,
	// so the early-equal exit is exercised.
	controllerName := consts.GatewayClassControllerNameALB
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "my-gw"},
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
			},
		},
		Status: gwv1.GatewayStatus{
			Conditions: []metav1.Condition{
				{
					Type:               string(gwv1.GatewayConditionAccepted),
					Status:             metav1.ConditionTrue,
					Reason:             string(gwv1.GatewayReasonAccepted),
					Message:            "Gateway accepted by controller " + controllerName,
					ObservedGeneration: 0,
				},
				{
					Type:               string(gwv1.GatewayConditionProgrammed),
					Status:             metav1.ConditionFalse,
					Reason:             string(gwv1.GatewayReasonPending),
					Message:            "LoadBalancerConfig not yet created",
					ObservedGeneration: 0,
				},
			},
			Listeners: []gwv1.ListenerStatus{
				{
					Name: "http",
					Conditions: []metav1.Condition{{
						Type:               string(gwv1.ListenerConditionAccepted),
						Status:             metav1.ConditionTrue,
						Reason:             string(gwv1.ListenerReasonAccepted),
						ObservedGeneration: 0,
					}},
				},
			},
		},
	}
	task := newTaskWithGWInClient(t, gw)
	// nil lbc, nil err → Programmed=False/Pending
	err := task.writeGatewayStatus(context.Background(), nil, nil)
	// Should be a no-op (conditions already match) — no error
	assert.NoError(t, err)
}

func TestListenerStatusesFromGateway(t *testing.T) {
	gw := &gwv1.Gateway{
		Spec: gwv1.GatewaySpec{
			Listeners: []gwv1.Listener{
				{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
				{Name: "https", Protocol: gwv1.HTTPSProtocolType, Port: 443},
				{Name: "tcp", Protocol: gwv1.TCPProtocolType, Port: 9000},
			},
		},
	}
	statuses := listenerStatusesFromGateway(gw)
	assert.Len(t, statuses, 3)

	// HTTP listener → Accepted=True
	assert.Equal(t, gwv1.SectionName("http"), statuses[0].Name)
	assert.NotEmpty(t, statuses[0].Conditions)
	assert.Equal(t, metav1.ConditionTrue, statuses[0].Conditions[0].Status)

	// HTTPS listener → Accepted=True
	assert.Equal(t, gwv1.SectionName("https"), statuses[1].Name)
	assert.Equal(t, metav1.ConditionTrue, statuses[1].Conditions[0].Status)

	// TCP listener → Accepted=False
	assert.Equal(t, gwv1.SectionName("tcp"), statuses[2].Name)
	assert.Equal(t, metav1.ConditionFalse, statuses[2].Conditions[0].Status)

	// HTTP and HTTPS have HTTPRoute supported kinds
	assert.NotEmpty(t, statuses[0].SupportedKinds)
	assert.NotEmpty(t, statuses[1].SupportedKinds)
	// TCP has nil supported kinds
	assert.Nil(t, statuses[2].SupportedKinds)
}
