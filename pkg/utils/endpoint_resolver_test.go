package utils

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/vngcloud/vngcloud-load-balancer-controller/pkg/k8s"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	testclient "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
)

type PodInfo struct {
	Key types.NamespacedName
	UID types.UID

	ContainerPorts []corev1.ContainerPort
	ReadinessGates []corev1.PodReadinessGate
	Conditions     []corev1.PodCondition
	NodeName       string
	PodIP          string
}

func Test_defaultEndpointResolver_ResolvePodEndpoints(t *testing.T) {
	testNS := "test-ns"
	nodeA := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-a",
		},
		Spec: corev1.NodeSpec{
			ProviderID: "aws:///us-west-2b/i-abcdefga",
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}
	nodeB := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-b",
		},
		Spec: corev1.NodeSpec{
			ProviderID: "aws:///us-west-2b/i-abcdefgb",
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionUnknown,
				},
			},
		},
	}
	nodeC := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-c",
		},
		Spec: corev1.NodeSpec{
			ProviderID: "aws:///us-west-2b/i-abcdefgc",
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionFalse,
				},
			},
		},
	}

	pod1 := PodInfo{ // pod ready on ready node
		Key: types.NamespacedName{Namespace: testNS, Name: "pod-1"},
		UID: "pod-uuid-1",
		Conditions: []corev1.PodCondition{
			{
				Type:   corev1.PodReady,
				Status: corev1.ConditionTrue,
			},
			{
				Type:   corev1.ContainersReady,
				Status: corev1.ConditionTrue,
			},
		},
		NodeName: nodeA.Name,
		PodIP:    "192.168.1.1",
	}
	pod2 := PodInfo{ // pod containerReady on unknown node
		Key: types.NamespacedName{Namespace: testNS, Name: "pod-2"},
		UID: "pod-uuid-2",
		Conditions: []corev1.PodCondition{
			{
				Type:   corev1.PodReady,
				Status: corev1.ConditionFalse,
			},
			{
				Type:   corev1.ContainersReady,
				Status: corev1.ConditionTrue,
			},
		},
		NodeName: nodeB.Name,
		PodIP:    "192.168.1.2",
	}

	pod3 := PodInfo{ // pod containerReady on not ready node
		Key: types.NamespacedName{Namespace: testNS, Name: "pod-3"},
		UID: "pod-uuid-3",
		Conditions: []corev1.PodCondition{
			{
				Type:   corev1.PodReady,
				Status: corev1.ConditionFalse,
			},
			{
				Type:   corev1.ContainersReady,
				Status: corev1.ConditionTrue,
			},
		},
		NodeName: nodeC.Name,
		PodIP:    "192.168.1.3",
	}

	pod4 := PodInfo{ // pod containerReady(with readinessGate) on ready node
		Key: types.NamespacedName{Namespace: testNS, Name: "pod-4"},
		UID: "pod-uuid-4",
		ReadinessGates: []corev1.PodReadinessGate{
			{
				ConditionType: "custom-condition",
			},
		},
		Conditions: []corev1.PodCondition{
			{
				Type:   corev1.PodReady,
				Status: corev1.ConditionFalse,
			},
			{
				Type:   corev1.ContainersReady,
				Status: corev1.ConditionTrue,
			},
		},
		NodeName: nodeA.Name,
		PodIP:    "192.168.1.4",
	}

	pod5 := PodInfo{ // pod containerReady(with readinessGate) on unknown node
		Key: types.NamespacedName{Namespace: testNS, Name: "pod-5"},
		UID: "pod-uuid-5",
		ReadinessGates: []corev1.PodReadinessGate{
			{
				ConditionType: "custom-condition",
			},
		},
		Conditions: []corev1.PodCondition{
			{
				Type:   corev1.PodReady,
				Status: corev1.ConditionFalse,
			},
			{
				Type:   corev1.ContainersReady,
				Status: corev1.ConditionTrue,
			},
		},
		NodeName: nodeB.Name,
		PodIP:    "192.168.1.5",
	}

	pod6 := PodInfo{ // pod not containerReady(with readinessGate) on ready node
		Key: types.NamespacedName{Namespace: testNS, Name: "pod-6"},
		UID: "pod-uuid-6",
		ReadinessGates: []corev1.PodReadinessGate{
			{
				ConditionType: "custom-condition",
			},
		},
		Conditions: []corev1.PodCondition{
			{
				Type:   corev1.PodReady,
				Status: corev1.ConditionFalse,
			},
			{
				Type:   corev1.ContainersReady,
				Status: corev1.ConditionFalse,
			},
		},
		NodeName: nodeA.Name,
		PodIP:    "192.168.1.6",
	}

	pod7 := PodInfo{ // pod not containerReady(without readinessGate) on ready node
		Key: types.NamespacedName{Namespace: testNS, Name: "pod-7"},
		UID: "pod-uuid-7",
		Conditions: []corev1.PodCondition{
			{
				Type:   corev1.PodReady,
				Status: corev1.ConditionFalse,
			},
			{
				Type:   corev1.ContainersReady,
				Status: corev1.ConditionFalse,
			},
		},
		NodeName: nodeA.Name,
		PodIP:    "192.168.1.7",
	}
	// pod8 := PodInfo{ // pod containerReady but terminating on ready node
	// 	Key: types.NamespacedName{Namespace: testNS, Name: "pod-8"},
	// 	UID: "pod-uuid-8",
	// 	Conditions: []corev1.PodCondition{
	// 		{
	// 			Type:   corev1.PodReady,
	// 			Status: corev1.ConditionTrue,
	// 		},
	// 		{
	// 			Type:   corev1.ContainersReady,
	// 			Status: corev1.ConditionTrue,
	// 		},
	// 	},
	// 	NodeName: nodeB.Name,
	// 	PodIP:    "192.168.1.8",
	// }

	svc1 := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNS,
			Name:      "svc-1",
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name: "http",
					Port: 80,
				},
				{
					Name: "https",
					Port: 443,
				},
			},
		},
	}

	// svc1WithoutHTTPPort := &corev1.Service{
	// 	ObjectMeta: metav1.ObjectMeta{
	// 		Namespace: testNS,
	// 		Name:      "svc-1",
	// 	},
	// 	Spec: corev1.ServiceSpec{
	// 		Type: corev1.ServiceTypeClusterIP,
	// 		Ports: []corev1.ServicePort{
	// 			{
	// 				Name: "https",
	// 				Port: 443,
	// 			},
	// 		},
	// 	},
	// }
	ep1 := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNS,
			Name:      "svc-1",
		},
		Subsets: []corev1.EndpointSubset{
			{
				Ports: []corev1.EndpointPort{
					{
						Name: "http",
						Port: 8080,
					},
					{
						Name: "https",
						Port: 8443,
					},
				},
				Addresses: []corev1.EndpointAddress{
					{
						IP: pod1.PodIP,
						TargetRef: &corev1.ObjectReference{
							Kind:      "Pod",
							Namespace: pod1.Key.Namespace,
							Name:      pod1.Key.Name,
						},
					},
				},
				NotReadyAddresses: []corev1.EndpointAddress{
					{
						IP: pod2.PodIP,
						TargetRef: &corev1.ObjectReference{
							Kind:      "Pod",
							Namespace: pod2.Key.Namespace,
							Name:      pod2.Key.Name,
						},
					},
					{
						IP: pod3.PodIP,
						TargetRef: &corev1.ObjectReference{
							Kind:      "Pod",
							Namespace: pod3.Key.Namespace,
							Name:      pod3.Key.Name,
						},
					},
					{
						IP: pod4.PodIP,
						TargetRef: &corev1.ObjectReference{
							Kind:      "Pod",
							Namespace: pod4.Key.Namespace,
							Name:      pod4.Key.Name,
						},
					},
					{
						IP: pod5.PodIP,
						TargetRef: &corev1.ObjectReference{
							Kind:      "Pod",
							Namespace: pod5.Key.Namespace,
							Name:      pod5.Key.Name,
						},
					},
					{
						IP: pod6.PodIP,
						TargetRef: &corev1.ObjectReference{
							Kind:      "Pod",
							Namespace: pod6.Key.Namespace,
							Name:      pod6.Key.Name,
						},
					},
					{
						IP: pod7.PodIP,
						TargetRef: &corev1.ObjectReference{
							Kind:      "Pod",
							Namespace: pod7.Key.Namespace,
							Name:      pod7.Key.Name,
						},
					},
				},
			},
		},
	}

	type env struct {
		nodes         []*corev1.Node
		services      []*corev1.Service
		endpointsList []*corev1.Endpoints
	}
	type fields struct {
		failOpenEnabled bool
		// endpointSliceEnabled bool
	}
	type args struct {
		svcKey types.NamespacedName
		port   intstr.IntOrString
		opts   []EndpointResolveOption
	}
	tests := []struct {
		name    string
		env     env
		fields  fields
		args    args
		want    []EndpointAddress
		wantErr error
	}{
		{
			name: "choose every ready pod only when there are ready pods",
			env: env{
				nodes:         []*corev1.Node{nodeA, nodeB, nodeC},
				services:      []*corev1.Service{svc1},
				endpointsList: []*corev1.Endpoints{ep1},
			},
			fields: fields{
				failOpenEnabled: true,
			},
			args: args{
				svcKey: k8s.NamespacedName(svc1),
				port:   intstr.FromString("http"),
				opts:   nil,
			},
			want: []EndpointAddress{
				{IP: pod1.PodIP, Port: 8080, Name: pod1.Key.Name},
				{IP: pod2.PodIP, Port: 8080, Name: pod2.Key.Name},
				{IP: pod3.PodIP, Port: 8080, Name: pod3.Key.Name},
				{IP: pod4.PodIP, Port: 8080, Name: pod4.Key.Name},
				{IP: pod5.PodIP, Port: 8080, Name: pod5.Key.Name},
				{IP: pod6.PodIP, Port: 8080, Name: pod6.Key.Name},
				{IP: pod7.PodIP, Port: 8080, Name: pod7.Key.Name},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			k8sSchema := runtime.NewScheme()
			err := clientgoscheme.AddToScheme(k8sSchema)
			assert.NoError(t, err)
			k8sClient := testclient.NewClientBuilder().WithScheme(k8sSchema).Build()

			ctx := context.Background()
			for _, node := range tt.env.nodes {
				assert.NoError(t, k8sClient.Create(ctx, node.DeepCopy()))
			}
			for _, svc := range tt.env.services {
				assert.NoError(t, k8sClient.Create(ctx, svc.DeepCopy()))
			}
			for _, endpoints := range tt.env.endpointsList {
				assert.NoError(t, k8sClient.Create(ctx, endpoints.DeepCopy()))
			}

			r := &defaultEndpointResolver{
				k8sClient:       k8sClient,
				failOpenEnabled: tt.fields.failOpenEnabled,
			}
			got, err := r.ResolvePodEndpoints(ctx, tt.args.svcKey, tt.args.port, tt.args.opts...)
			if tt.wantErr != nil {
				assert.EqualError(t, err, tt.wantErr.Error())
			} else {
				assert.NoError(t, err)
				// compare got and tt.want
				if len(got) != len(tt.want) {
					t.Errorf("len(got) = %v, want %v", len(got), len(tt.want))
				}
				for i := range got {
					// check existence
					found := false
					for j := range tt.want {
						if got[i].IP == tt.want[j].IP && got[i].Port == tt.want[j].Port && got[i].Name == tt.want[j].Name {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("got[%v] = %v, want %v", i, got[i], tt.want)
					}
				}
			}
		})
	}
}

func Test_defaultEndpointResolver_ResolveNodePortEndpoints(t *testing.T) {
	testNS := "test-ns"
	node1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-1",
			Labels: map[string]string{
				"labelA": "valueA",
			},
		},
		Spec: corev1.NodeSpec{
			ProviderID: "aws:///us-west-2b/i-abcdefg1",
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionTrue,
				},
			},
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "10.0.0.1"},
			},
		},
	}
	node2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-2",
			Labels: map[string]string{
				"labelA": "valueB",
			},
		},
		Spec: corev1.NodeSpec{
			ProviderID: "aws:///us-west-2b/i-abcdefg2",
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionTrue,
				},
			},
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "10.0.0.2"},
			},
		},
	}
	node3 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-3",
			Labels: map[string]string{
				"labelA": "valueA",
			},
		},
		Spec: corev1.NodeSpec{
			ProviderID: "aws:///us-west-2b/i-abcdefg3",
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionUnknown,
				},
			},
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "10.0.0.3"},
			},
		},
	}
	node4 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-4",
			Labels: map[string]string{
				"labelA": "valueB",
			},
		},
		Spec: corev1.NodeSpec{
			ProviderID: "aws:///us-west-2b/i-abcdefg4",
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionUnknown,
				},
			},
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "10.0.0.4"},
			},
		},
	}
	node5 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-5",
			Labels: map[string]string{
				"labelA": "valueA",
			},
		},
		Spec: corev1.NodeSpec{
			ProviderID: "aws:///us-west-2b/i-abcdefg5",
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionFalse,
				},
			},
			Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "10.0.0.5"},
			},
		},
	}
	svc1 := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: testNS,
			Name:      "svc-1",
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort,
			Ports: []corev1.ServicePort{
				{
					Name:     "http",
					Port:     80,
					NodePort: 18080,
				},
				{
					Name:     "https",
					Port:     443,
					NodePort: 18443,
				},
			},
		},
	}
	// svc1WithoutHTTPPort := &corev1.Service{
	// 	ObjectMeta: metav1.ObjectMeta{
	// 		Namespace: testNS,
	// 		Name:      "svc-1",
	// 	},
	// 	Spec: corev1.ServiceSpec{
	// 		Type: corev1.ServiceTypeClusterIP,
	// 		Ports: []corev1.ServicePort{
	// 			{
	// 				Name: "https",
	// 				Port: 443,
	// 			},
	// 		},
	// 	},
	// }
	// svc2 := &corev1.Service{
	// 	ObjectMeta: metav1.ObjectMeta{
	// 		Namespace: testNS,
	// 		Name:      "svc-2",
	// 	},
	// 	Spec: corev1.ServiceSpec{
	// 		Type: corev1.ServiceTypeClusterIP,
	// 		Ports: []corev1.ServicePort{
	// 			{
	// 				Name:     "http",
	// 				Port:     80,
	// 				NodePort: 18080,
	// 			},
	// 			{
	// 				Name:     "https",
	// 				Port:     443,
	// 				NodePort: 18443,
	// 			},
	// 		},
	// 	},
	// }

	type fields struct {
		failOpenEnabled bool
	}
	type env struct {
		nodes    []*corev1.Node
		services []*corev1.Service
	}
	type args struct {
		svcKey types.NamespacedName
		port   intstr.IntOrString
		opts   []EndpointResolveOption
	}
	tests := []struct {
		name    string
		env     env
		fields  fields
		args    args
		want    []EndpointAddress
		wantErr error
	}{
		{
			name: "[with failOpen] choose every ready node only when there are ready nodes",
			env: env{
				nodes:    []*corev1.Node{node1, node2, node3, node4, node5},
				services: []*corev1.Service{svc1},
			},
			fields: fields{
				failOpenEnabled: true,
			},
			args: args{
				svcKey: k8s.NamespacedName(svc1),
				port:   intstr.FromString("http"),
				opts:   []EndpointResolveOption{WithNodeSelector(labels.Everything())},
			},
			want: []EndpointAddress{
				{Port: 18080, Name: node1.Name, IP: node1.Status.Addresses[0].Address},
				{Port: 18080, Name: node2.Name, IP: node2.Status.Addresses[0].Address},
			},
		},
		{
			name: "[without failOpen] choose every ready node only when there are ready nodes",
			env: env{
				nodes:    []*corev1.Node{node1, node2, node3, node4, node5},
				services: []*corev1.Service{svc1},
			},
			fields: fields{
				failOpenEnabled: false,
			},
			args: args{
				svcKey: k8s.NamespacedName(svc1),
				port:   intstr.FromString("http"),
				opts:   []EndpointResolveOption{WithNodeSelector(labels.Everything())},
			},
			want: []EndpointAddress{
				{Port: 18080, Name: node1.Name, IP: node1.Status.Addresses[0].Address},
				{Port: 18080, Name: node2.Name, IP: node2.Status.Addresses[0].Address},
			},
		},
		{
			name: "[with failOpen] choose every unknown node when there are no ready nodes",
			env: env{
				nodes:    []*corev1.Node{node3, node4, node5},
				services: []*corev1.Service{svc1},
			},
			fields: fields{
				failOpenEnabled: true,
			},
			args: args{
				svcKey: k8s.NamespacedName(svc1),
				port:   intstr.FromString("http"),
				opts:   []EndpointResolveOption{WithNodeSelector(labels.Everything())},
			},
			want: []EndpointAddress{
				{Port: 18080, Name: node3.Name, IP: node3.Status.Addresses[0].Address},
				{Port: 18080, Name: node4.Name, IP: node4.Status.Addresses[0].Address},
			},
		},
		{
			name: "[without failOpen] don't choose unknown node when there are no ready nodes",
			env: env{
				nodes:    []*corev1.Node{node3, node4, node5},
				services: []*corev1.Service{svc1},
			},
			fields: fields{
				failOpenEnabled: false,
			},
			args: args{
				svcKey: k8s.NamespacedName(svc1),
				port:   intstr.FromString("http"),
				opts:   []EndpointResolveOption{WithNodeSelector(labels.Everything())},
			},
			want: nil,
		},
		// {
		// 	name: "choose every ready node - matches labelSelector",
		// 	env: env{
		// 		nodes:    []*corev1.Node{node1, node2, node3, node4, node5},
		// 		services: []*corev1.Service{svc1},
		// 	},
		// 	args: args{
		// 		svcKey: k8s.NamespacedName(svc1),
		// 		port:   intstr.FromString("http"),
		// 		opts:   []EndpointResolveOption{WithNodeSelector(labels.Set{"labelA": "valueA"}.AsSelectorPreValidated())},
		// 	},
		// 	want: []NodePortEndpoint{
		// 		{
		// 			InstanceID: "i-abcdefg1",
		// 			Port:       18080,
		// 			Node:       node1,
		// 		},
		// 	},
		// },
		// {
		// 	name: "no node will be chosen by default",
		// 	env: env{
		// 		nodes:    []*corev1.Node{node1, node2, node3, node4, node5},
		// 		services: []*corev1.Service{svc1},
		// 	},
		// 	args: args{
		// 		svcKey: k8s.NamespacedName(svc1),
		// 		port:   intstr.FromString("http"),
		// 		opts:   nil,
		// 	},
		// 	want: nil,
		// },
		// {
		// 	name: "clusterIP service is not supported",
		// 	env: env{
		// 		nodes:    []*corev1.Node{node1, node2, node3, node4},
		// 		services: []*corev1.Service{svc2},
		// 	},
		// 	args: args{
		// 		svcKey: k8s.NamespacedName(svc2),
		// 		port:   intstr.FromString("http"),
		// 		opts:   []EndpointResolveOption{WithNodeSelector(labels.Set{"labelA": "valueA"}.AsSelectorPreValidated())},
		// 	},
		// 	wantErr: errors.New("service type must be either 'NodePort' or 'LoadBalancer': test-ns/svc-2"),
		// },
		// {
		// 	name: "service not found",
		// 	env: env{
		// 		nodes:    []*corev1.Node{node1, node2, node3, node4},
		// 		services: []*corev1.Service{},
		// 	},
		// 	args: args{
		// 		svcKey: k8s.NamespacedName(svc1),
		// 		port:   intstr.FromString("http"),
		// 		opts:   []EndpointResolveOption{WithNodeSelector(labels.Everything())},
		// 	},
		// 	wantErr: fmt.Errorf("%w: %v", ErrNotFound, "services \"svc-1\" not found"),
		// },
		// {
		// 	name: "service port not found",
		// 	env: env{
		// 		nodes:    []*corev1.Node{node1, node2, node3, node4},
		// 		services: []*corev1.Service{svc1WithoutHTTPPort},
		// 	},
		// 	args: args{
		// 		svcKey: k8s.NamespacedName(svc1),
		// 		port:   intstr.FromString("http"),
		// 		opts:   []EndpointResolveOption{WithNodeSelector(labels.Everything())},
		// 	},
		// 	wantErr: fmt.Errorf("%w: %v", ErrNotFound, "unable to find port http on service test-ns/svc-1"),
		// },
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			k8sSchema := runtime.NewScheme()
			assert.NoError(t, clientgoscheme.AddToScheme(k8sSchema))
			k8sClient := testclient.NewClientBuilder().WithScheme(k8sSchema).Build()
			for _, node := range tt.env.nodes {
				assert.NoError(t, k8sClient.Create(ctx, node.DeepCopy()))
			}
			for _, svc := range tt.env.services {
				assert.NoError(t, k8sClient.Create(ctx, svc.DeepCopy()))
			}

			r := &defaultEndpointResolver{
				k8sClient:       k8sClient,
				failOpenEnabled: tt.fields.failOpenEnabled,
			}

			got, err := r.ResolveNodePortEndpoints(ctx, tt.args.svcKey, tt.args.port, tt.args.opts...)
			if tt.wantErr != nil {
				assert.EqualError(t, err, tt.wantErr.Error())
			} else {
				assert.NoError(t, err)
				// compare got and tt.want
				if len(got) != len(tt.want) {
					t.Errorf("len(got) = %v, want %v", len(got), len(tt.want))
				}
				for i := range got {
					// check existence
					found := false
					for j := range tt.want {
						if got[i].IP == tt.want[j].IP && got[i].Port == tt.want[j].Port && got[i].Name == tt.want[j].Name {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("got[%v] = %v, want %v", i, got[i], tt.want)
					}
				}
			}
		})
	}
}

// func Test_defaultEndpointResolver_computeServiceEndpointsData(t *testing.T) {
// 	type env struct {
// 		endpoints      []*corev1.Endpoints
// 		endpointSlices []*discovery.EndpointSlice
// 	}
// 	type fields struct {
// 		endpointSliceEnabled bool
// 	}
// 	type args struct {
// 		svcKey types.NamespacedName
// 	}
// 	tests := []struct {
// 		name   string
// 		env    env
// 		fields fields

// 		args    args
// 		want    []EndpointsData
// 		wantErr error
// 	}{
// 		{
// 			name: "build endpoints from endpoints",
// 			fields: fields{
// 				endpointSliceEnabled: false,
// 			},
// 			env: env{
// 				endpoints: []*corev1.Endpoints{
// 					{
// 						ObjectMeta: metav1.ObjectMeta{
// 							Namespace: "sample-ns",
// 							Name:      "sample-svc",
// 						},
// 						Subsets: []corev1.EndpointSubset{
// 							{
// 								Ports: []corev1.EndpointPort{
// 									{
// 										Name: "http",
// 										Port: 80,
// 									},
// 								},
// 								Addresses: []corev1.EndpointAddress{
// 									{
// 										IP: "192.168.1.1",
// 									},
// 								},
// 							},
// 							{
// 								Ports: []corev1.EndpointPort{
// 									{
// 										Name: "https",
// 										Port: 443,
// 									},
// 								},
// 								NotReadyAddresses: []corev1.EndpointAddress{
// 									{
// 										IP: "192.168.1.1",
// 									},
// 								},
// 							},
// 						},
// 					},
// 				},
// 			},
// 			args: args{
// 				svcKey: types.NamespacedName{Namespace: "sample-ns", Name: "sample-svc"},
// 			},
// 			want: []EndpointsData{
// 				{
// 					Ports: []discovery.EndpointPort{
// 						{
// 							Name: PointerOf("http"),
// 							Port: PointerOf[int32](80),
// 						},
// 					},
// 					Endpoints: []discovery.Endpoint{
// 						{
// 							Addresses: []string{"192.168.1.1"},
// 							Conditions: discovery.EndpointConditions{
// 								Ready:       PointerOf[bool](true),
// 								Serving:     PointerOf[bool](true),
// 								Terminating: PointerOf[bool](false),
// 							},
// 						},
// 					},
// 				},
// 				{
// 					Ports: []discovery.EndpointPort{
// 						{
// 							Name: PointerOf("https"),
// 							Port: PointerOf[int32](443),
// 						},
// 					},
// 					Endpoints: []discovery.Endpoint{
// 						{
// 							Addresses: []string{"192.168.1.1"},
// 							Conditions: discovery.EndpointConditions{
// 								Ready:       PointerOf[bool](false),
// 								Serving:     PointerOf[bool](false),
// 								Terminating: PointerOf[bool](false),
// 							},
// 						},
// 					},
// 				},
// 			},
// 		},
// 		{
// 			name: "build endpoints from endpointSlices",
// 			fields: fields{
// 				endpointSliceEnabled: true,
// 			},
// 			env: env{
// 				endpointSlices: []*discovery.EndpointSlice{
// 					{
// 						ObjectMeta: metav1.ObjectMeta{
// 							Namespace: "sample-ns",
// 							Name:      "sample-svc-1",
// 							Labels: map[string]string{
// 								"kubernetes.io/service-name": "sample-svc",
// 							},
// 						},
// 						Ports: []discovery.EndpointPort{
// 							{
// 								Name: PointerOf("http"),
// 								Port: PointerOf[int32](80),
// 							},
// 						},
// 						Endpoints: []discovery.Endpoint{
// 							{
// 								Addresses: []string{"192.168.1.1"},
// 								Conditions: discovery.EndpointConditions{
// 									Ready:       PointerOf[bool](true),
// 									Serving:     PointerOf[bool](true),
// 									Terminating: PointerOf[bool](false),
// 								},
// 							},
// 						},
// 					},
// 					{
// 						ObjectMeta: metav1.ObjectMeta{
// 							Namespace: "sample-ns",
// 							Name:      "sample-svc-2",
// 							Labels: map[string]string{
// 								"kubernetes.io/service-name": "sample-svc",
// 							},
// 						},
// 						Ports: []discovery.EndpointPort{
// 							{
// 								Name: PointerOf("https"),
// 								Port: PointerOf[int32](443),
// 							},
// 						},
// 						Endpoints: []discovery.Endpoint{
// 							{
// 								Addresses: []string{"192.168.1.1"},
// 								Conditions: discovery.EndpointConditions{
// 									Ready:       PointerOf[bool](false),
// 									Serving:     PointerOf[bool](false),
// 									Terminating: PointerOf[bool](false),
// 								},
// 							},
// 						},
// 					},
// 				},
// 			},
// 			args: args{
// 				svcKey: types.NamespacedName{Namespace: "sample-ns", Name: "sample-svc"},
// 			},
// 			want: []EndpointsData{
// 				{
// 					Ports: []discovery.EndpointPort{
// 						{
// 							Name: PointerOf("http"),
// 							Port: PointerOf[int32](80),
// 						},
// 					},
// 					Endpoints: []discovery.Endpoint{
// 						{
// 							Addresses: []string{"192.168.1.1"},
// 							Conditions: discovery.EndpointConditions{
// 								Ready:       PointerOf[bool](true),
// 								Serving:     PointerOf[bool](true),
// 								Terminating: PointerOf[bool](false),
// 							},
// 						},
// 					},
// 				},
// 				{
// 					Ports: []discovery.EndpointPort{
// 						{
// 							Name: PointerOf("https"),
// 							Port: PointerOf[int32](443),
// 						},
// 					},
// 					Endpoints: []discovery.Endpoint{
// 						{
// 							Addresses: []string{"192.168.1.1"},
// 							Conditions: discovery.EndpointConditions{
// 								Ready:       PointerOf[bool](false),
// 								Serving:     PointerOf[bool](false),
// 								Terminating: PointerOf[bool](false),
// 							},
// 						},
// 					},
// 				},
// 			},
// 		},
// 	}
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			k8sSchema := runtime.NewScheme()
// 			clientgoscheme.AddToScheme(k8sSchema)
// 			k8sClient := testclient.NewClientBuilder().WithScheme(k8sSchema).Build()
// 			ctx := context.Background()
// 			for _, ep := range tt.env.endpoints {
// 				assert.NoError(t, k8sClient.Create(ctx, ep.DeepCopy()))
// 			}
// 			for _, eps := range tt.env.endpointSlices {
// 				assert.NoError(t, k8sClient.Create(ctx, eps.DeepCopy()))
// 			}

// 			r := &defaultEndpointResolver{
// 				k8sClient:            k8sClient,
// 				endpointSliceEnabled: tt.fields.endpointSliceEnabled,
// 			}
// 			got, err := r.computeServiceEndpointsData(context.Background(), tt.args.svcKey)
// 			if tt.wantErr != nil {
// 				assert.EqualError(t, err, tt.wantErr.Error())
// 			} else {
// 				assert.NoError(t, err)
// 				assert.Equal(t, tt.want, got)
// 			}
// 		})
// 	}
// }

// func Test_defaultEndpointResolver_findServiceAndServicePort(t *testing.T) {
// 	type env struct {
// 		services []*corev1.Service
// 	}
// 	type args struct {
// 		svcKey types.NamespacedName
// 		port   intstr.IntOrString
// 	}
// 	tests := []struct {
// 		name        string
// 		env         env
// 		args        args
// 		wantSvc     *corev1.Service
// 		wantSvcPort corev1.ServicePort
// 		wantErr     error
// 	}{
// 		{
// 			name: "found service and servicePort",
// 			env: env{
// 				services: []*corev1.Service{
// 					{
// 						ObjectMeta: metav1.ObjectMeta{
// 							Namespace: "sample-ns",
// 							Name:      "sample-svc",
// 						},
// 						Spec: corev1.ServiceSpec{
// 							Ports: []corev1.ServicePort{
// 								{
// 									Name: "http",
// 									Port: 80,
// 								},
// 							},
// 						},
// 					},
// 				},
// 			},
// 			args: args{
// 				svcKey: types.NamespacedName{Namespace: "sample-ns", Name: "sample-svc"},
// 				port:   intstr.FromString("http"),
// 			},
// 			wantSvc: &corev1.Service{
// 				ObjectMeta: metav1.ObjectMeta{
// 					Namespace: "sample-ns",
// 					Name:      "sample-svc",
// 				},
// 				Spec: corev1.ServiceSpec{
// 					Ports: []corev1.ServicePort{
// 						{
// 							Name: "http",
// 							Port: 80,
// 						},
// 					},
// 				},
// 			},
// 			wantSvcPort: corev1.ServicePort{
// 				Name: "http",
// 				Port: 80,
// 			},
// 		},
// 		{
// 			name: "service not found",
// 			env: env{
// 				services: []*corev1.Service{},
// 			},
// 			args: args{
// 				svcKey: types.NamespacedName{Namespace: "sample-ns", Name: "sample-svc"},
// 				port:   intstr.FromString("http"),
// 			},
// 			wantErr: errors.New("backend not found: services \"sample-svc\" not found"),
// 		},
// 		{
// 			name: "servicePort not found",
// 			env: env{
// 				services: []*corev1.Service{
// 					{
// 						ObjectMeta: metav1.ObjectMeta{
// 							Namespace: "sample-ns",
// 							Name:      "sample-svc",
// 						},
// 						Spec: corev1.ServiceSpec{
// 							Ports: []corev1.ServicePort{
// 								{
// 									Name: "https",
// 									Port: 443,
// 								},
// 							},
// 						},
// 					},
// 				},
// 			},
// 			args: args{
// 				svcKey: types.NamespacedName{Namespace: "sample-ns", Name: "sample-svc"},
// 				port:   intstr.FromString("http"),
// 			},
// 			wantErr: errors.New("backend not found: unable to find port http on service sample-ns/sample-svc"),
// 		},
// 	}
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			k8sSchema := runtime.NewScheme()
// 			clientgoscheme.AddToScheme(k8sSchema)
// 			k8sClient := testclient.NewClientBuilder().WithScheme(k8sSchema).Build()
// 			ctx := context.Background()
// 			for _, svc := range tt.env.services {
// 				assert.NoError(t, k8sClient.Create(ctx, svc.DeepCopy()))
// 			}

// 			r := &defaultEndpointResolver{
// 				k8sClient: k8sClient,
// 			}
// 			gotSvc, gotSvcPort, err := r.findServiceAndServicePort(ctx, tt.args.svcKey, tt.args.port)
// 			if tt.wantErr != nil {
// 				assert.EqualError(t, err, tt.wantErr.Error())
// 			} else {
// 				opt := cmp.Options{
// 					equality.IgnoreFakeClientPopulatedFields(),
// 					cmpopts.SortSlices(func(lhs PodEndpoint, rhs PodEndpoint) bool {
// 						return lhs.IP < rhs.IP
// 					}),
// 				}
// 				assert.NoError(t, err)
// 				assert.True(t, cmp.Equal(tt.wantSvc, gotSvc, opt),
// 					"diff: %v", cmp.Diff(tt.wantSvc, gotSvc, opt))
// 				assert.Equal(t, tt.wantSvcPort, gotSvcPort)
// 			}
// 		})
// 	}
// }

// func Test_filterNodesByReadyConditionStatus(t *testing.T) {
// 	type args struct {
// 		nodes           []*corev1.Node
// 		readyCondStatus corev1.ConditionStatus
// 	}
// 	tests := []struct {
// 		name string
// 		args args
// 		want []*corev1.Node
// 	}{
// 		{
// 			name: "filter ready:true nodes - multiple found",
// 			args: args{
// 				nodes: []*corev1.Node{
// 					{
// 						ObjectMeta: metav1.ObjectMeta{
// 							Name: "node-1",
// 						},
// 						Spec: corev1.NodeSpec{
// 							ProviderID: "aws:///us-west-2b/i-xxxxxa",
// 						},
// 						Status: corev1.NodeStatus{
// 							Conditions: []corev1.NodeCondition{
// 								{
// 									Type:   corev1.NodeReady,
// 									Status: corev1.ConditionTrue,
// 								},
// 							},
// 						},
// 					},
// 					{
// 						ObjectMeta: metav1.ObjectMeta{
// 							Name: "node-2",
// 						},
// 						Spec: corev1.NodeSpec{
// 							ProviderID: "aws:///us-west-2b/i-xxxxxb",
// 						},
// 						Status: corev1.NodeStatus{
// 							Conditions: []corev1.NodeCondition{
// 								{
// 									Type:   corev1.NodeReady,
// 									Status: corev1.ConditionTrue,
// 								},
// 							},
// 						},
// 					},
// 					{
// 						ObjectMeta: metav1.ObjectMeta{
// 							Name: "node-3",
// 						},
// 						Spec: corev1.NodeSpec{
// 							ProviderID: "aws:///us-west-2b/i-xxxxxc",
// 						},
// 						Status: corev1.NodeStatus{
// 							Conditions: []corev1.NodeCondition{
// 								{
// 									Type:   corev1.NodeReady,
// 									Status: corev1.ConditionUnknown,
// 								},
// 							},
// 						},
// 					},
// 					{
// 						ObjectMeta: metav1.ObjectMeta{
// 							Name: "node-4",
// 						},
// 						Spec: corev1.NodeSpec{
// 							ProviderID: "aws:///us-west-2b/i-xxxxxd",
// 						},
// 						Status: corev1.NodeStatus{
// 							Conditions: []corev1.NodeCondition{
// 								{
// 									Type:   corev1.NodeReady,
// 									Status: corev1.ConditionFalse,
// 								},
// 							},
// 						},
// 					},
// 				},
// 				readyCondStatus: corev1.ConditionTrue,
// 			},
// 			want: []*corev1.Node{
// 				{
// 					ObjectMeta: metav1.ObjectMeta{
// 						Name: "node-1",
// 					},
// 					Spec: corev1.NodeSpec{
// 						ProviderID: "aws:///us-west-2b/i-xxxxxa",
// 					},
// 					Status: corev1.NodeStatus{
// 						Conditions: []corev1.NodeCondition{
// 							{
// 								Type:   corev1.NodeReady,
// 								Status: corev1.ConditionTrue,
// 							},
// 						},
// 					},
// 				},
// 				{
// 					ObjectMeta: metav1.ObjectMeta{
// 						Name: "node-2",
// 					},
// 					Spec: corev1.NodeSpec{
// 						ProviderID: "aws:///us-west-2b/i-xxxxxb",
// 					},
// 					Status: corev1.NodeStatus{
// 						Conditions: []corev1.NodeCondition{
// 							{
// 								Type:   corev1.NodeReady,
// 								Status: corev1.ConditionTrue,
// 							},
// 						},
// 					},
// 				},
// 			},
// 		},
// 		{
// 			name: "filter ready:unknown nodes - one found",
// 			args: args{
// 				nodes: []*corev1.Node{
// 					{
// 						ObjectMeta: metav1.ObjectMeta{
// 							Name: "node-3",
// 						},
// 						Spec: corev1.NodeSpec{
// 							ProviderID: "aws:///us-west-2b/i-xxxxxc",
// 						},
// 						Status: corev1.NodeStatus{
// 							Conditions: []corev1.NodeCondition{
// 								{
// 									Type:   corev1.NodeReady,
// 									Status: corev1.ConditionUnknown,
// 								},
// 							},
// 						},
// 					},
// 					{
// 						ObjectMeta: metav1.ObjectMeta{
// 							Name: "node-4",
// 						},
// 						Spec: corev1.NodeSpec{
// 							ProviderID: "aws:///us-west-2b/i-xxxxxd",
// 						},
// 						Status: corev1.NodeStatus{
// 							Conditions: []corev1.NodeCondition{
// 								{
// 									Type:   corev1.NodeReady,
// 									Status: corev1.ConditionFalse,
// 								},
// 							},
// 						},
// 					},
// 				},
// 				readyCondStatus: corev1.ConditionUnknown,
// 			},
// 			want: []*corev1.Node{
// 				{
// 					ObjectMeta: metav1.ObjectMeta{
// 						Name: "node-3",
// 					},
// 					Spec: corev1.NodeSpec{
// 						ProviderID: "aws:///us-west-2b/i-xxxxxc",
// 					},
// 					Status: corev1.NodeStatus{
// 						Conditions: []corev1.NodeCondition{
// 							{
// 								Type:   corev1.NodeReady,
// 								Status: corev1.ConditionUnknown,
// 							},
// 						},
// 					},
// 				},
// 			},
// 		},
// 		{
// 			name: "filter ready:true nodes - none found",
// 			args: args{
// 				nodes: []*corev1.Node{
// 					{
// 						ObjectMeta: metav1.ObjectMeta{
// 							Name: "node-3",
// 						},
// 						Spec: corev1.NodeSpec{
// 							ProviderID: "aws:///us-west-2b/i-xxxxxc",
// 						},
// 						Status: corev1.NodeStatus{
// 							Conditions: []corev1.NodeCondition{
// 								{
// 									Type:   corev1.NodeReady,
// 									Status: corev1.ConditionUnknown,
// 								},
// 							},
// 						},
// 					},
// 					{
// 						ObjectMeta: metav1.ObjectMeta{
// 							Name: "node-4",
// 						},
// 						Spec: corev1.NodeSpec{
// 							ProviderID: "aws:///us-west-2b/i-xxxxxd",
// 						},
// 						Status: corev1.NodeStatus{
// 							Conditions: []corev1.NodeCondition{
// 								{
// 									Type:   corev1.NodeReady,
// 									Status: corev1.ConditionFalse,
// 								},
// 							},
// 						},
// 					},
// 				},
// 				readyCondStatus: corev1.ConditionTrue,
// 			},
// 			want: nil,
// 		},
// 	}
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			got := filterNodesByReadyConditionStatus(tt.args.nodes, tt.args.readyCondStatus)
// 			assert.Equal(t, tt.want, got)
// 		})
// 	}
// }

// func Test_buildEndpointsDataFromEndpoints(t *testing.T) {
// 	type args struct {
// 		eps *corev1.Endpoints
// 	}
// 	tests := []struct {
// 		name string
// 		args args
// 		want []EndpointsData
// 	}{
// 		{
// 			name: "multiple endpoints",
// 			args: args{
// 				eps: &corev1.Endpoints{
// 					Subsets: []corev1.EndpointSubset{
// 						{
// 							Ports: []corev1.EndpointPort{
// 								{
// 									Name: "http",
// 									Port: 80,
// 								},
// 								{
// 									Name: "https",
// 									Port: 443,
// 								},
// 							},
// 							Addresses: []corev1.EndpointAddress{
// 								{
// 									IP: "192.168.1.1",
// 								},
// 								{
// 									IP: "192.168.1.2",
// 								},
// 							},
// 							NotReadyAddresses: []corev1.EndpointAddress{
// 								{
// 									IP: "192.168.1.3",
// 								},
// 							},
// 						},
// 						{
// 							Ports: []corev1.EndpointPort{
// 								{
// 									Name: "http",
// 									Port: 8080,
// 								},
// 								{
// 									Name: "https",
// 									Port: 8443,
// 								},
// 							},
// 							Addresses: []corev1.EndpointAddress{
// 								{
// 									IP: "192.168.3.1",
// 								},
// 								{
// 									IP: "192.168.3.2",
// 								},
// 							},
// 							NotReadyAddresses: []corev1.EndpointAddress{
// 								{
// 									IP: "192.168.3.3",
// 								},
// 							},
// 						},
// 					},
// 				},
// 			},
// 			want: []EndpointsData{
// 				{
// 					Ports: []discovery.EndpointPort{
// 						{
// 							Name: PointerOf("http"),
// 							Port: PointerOf[int32](80),
// 						},
// 						{
// 							Name: PointerOf("https"),
// 							Port: PointerOf[int32](443),
// 						},
// 					},
// 					Endpoints: []discovery.Endpoint{
// 						{
// 							Addresses: []string{"192.168.1.1"},
// 							Conditions: discovery.EndpointConditions{
// 								Ready:       PointerOf[bool](true),
// 								Serving:     PointerOf[bool](true),
// 								Terminating: PointerOf[bool](false),
// 							},
// 						},
// 						{
// 							Addresses: []string{"192.168.1.2"},
// 							Conditions: discovery.EndpointConditions{
// 								Ready:       PointerOf[bool](true),
// 								Serving:     PointerOf[bool](true),
// 								Terminating: PointerOf[bool](false),
// 							},
// 						},
// 						{
// 							Addresses: []string{"192.168.1.3"},
// 							Conditions: discovery.EndpointConditions{
// 								Ready:       PointerOf[bool](false),
// 								Serving:     PointerOf[bool](false),
// 								Terminating: PointerOf[bool](false),
// 							},
// 						},
// 					},
// 				},
// 				{
// 					Ports: []discovery.EndpointPort{
// 						{
// 							Name: PointerOf("http"),
// 							Port: PointerOf[int32](8080),
// 						},
// 						{
// 							Name: PointerOf("https"),
// 							Port: PointerOf[int32](8443),
// 						},
// 					},
// 					Endpoints: []discovery.Endpoint{
// 						{
// 							Addresses: []string{"192.168.3.1"},
// 							Conditions: discovery.EndpointConditions{
// 								Ready:       PointerOf[bool](true),
// 								Serving:     PointerOf[bool](true),
// 								Terminating: PointerOf[bool](false),
// 							},
// 						},
// 						{
// 							Addresses: []string{"192.168.3.2"},
// 							Conditions: discovery.EndpointConditions{
// 								Ready:       PointerOf[bool](true),
// 								Serving:     PointerOf[bool](true),
// 								Terminating: PointerOf[bool](false),
// 							},
// 						},
// 						{
// 							Addresses: []string{"192.168.3.3"},
// 							Conditions: discovery.EndpointConditions{
// 								Ready:       PointerOf[bool](false),
// 								Serving:     PointerOf[bool](false),
// 								Terminating: PointerOf[bool](false),
// 							},
// 						},
// 					},
// 				},
// 			},
// 		},
// 	}
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			got := buildEndpointsDataFromEndpoints(tt.args.eps)
// 			assert.Equal(t, tt.want, got)
// 		})
// 	}
// }

// func Test_buildEndpointsDataFromEndpointSliceList(t *testing.T) {
// 	type args struct {
// 		epsList *discovery.EndpointSliceList
// 	}
// 	tests := []struct {
// 		name string
// 		args args
// 		want []EndpointsData
// 	}{
// 		{
// 			name: "multiple endpointSlices",
// 			args: args{
// 				epsList: &discovery.EndpointSliceList{
// 					Items: []discovery.EndpointSlice{
// 						{
// 							Ports: []discovery.EndpointPort{
// 								{
// 									Name: PointerOf("http"),
// 									Port: PointerOf[int32](80),
// 								},
// 								{
// 									Name: PointerOf("https"),
// 									Port: PointerOf[int32](443),
// 								},
// 							},
// 							Endpoints: []discovery.Endpoint{
// 								{
// 									Addresses: []string{"192.168.1.1"},
// 								},
// 								{
// 									Addresses: []string{"192.168.1.2"},
// 								},
// 							},
// 						},
// 						{
// 							Ports: []discovery.EndpointPort{
// 								{
// 									Name: PointerOf("http"),
// 									Port: PointerOf[int32](8080),
// 								},
// 								{
// 									Name: PointerOf("https"),
// 									Port: PointerOf[int32](8443),
// 								},
// 							},
// 							Endpoints: []discovery.Endpoint{
// 								{
// 									Addresses: []string{"192.168.3.1"},
// 								},
// 								{
// 									Addresses: []string{"192.168.3.2"},
// 								},
// 							},
// 						},
// 					},
// 				},
// 			},
// 			want: []EndpointsData{
// 				{
// 					Ports: []discovery.EndpointPort{
// 						{
// 							Name: PointerOf("http"),
// 							Port: PointerOf[int32](80),
// 						},
// 						{
// 							Name: PointerOf("https"),
// 							Port: PointerOf[int32](443),
// 						},
// 					},
// 					Endpoints: []discovery.Endpoint{
// 						{
// 							Addresses: []string{"192.168.1.1"},
// 						},
// 						{
// 							Addresses: []string{"192.168.1.2"},
// 						},
// 					},
// 				},
// 				{
// 					Ports: []discovery.EndpointPort{
// 						{
// 							Name: PointerOf("http"),
// 							Port: PointerOf[int32](8080),
// 						},
// 						{
// 							Name: PointerOf("https"),
// 							Port: PointerOf[int32](8443),
// 						},
// 					},
// 					Endpoints: []discovery.Endpoint{
// 						{
// 							Addresses: []string{"192.168.3.1"},
// 						},
// 						{
// 							Addresses: []string{"192.168.3.2"},
// 						},
// 					},
// 				},
// 			},
// 		},
// 		{
// 			name: "no endpointSlices",
// 			args: args{
// 				epsList: &discovery.EndpointSliceList{Items: nil},
// 			},
// 			want: nil,
// 		},
// 	}
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			got := buildEndpointsDataFromEndpointSliceList(tt.args.epsList)
// 			assert.Equal(t, tt.want, got)
// 		})
// 	}
// }

// func Test_buildPodEndpoint(t *testing.T) {
// 	type args struct {
// 		pod    PodInfo
// 		epAddr string
// 		port   int32
// 	}
// 	tests := []struct {
// 		name string
// 		args args
// 		want PodEndpoint
// 	}{
// 		{
// 			name: "base case",
// 			args: args{
// 				pod: PodInfo{
// 					Key: types.NamespacedName{Name: "sample-node"},
// 				},
// 				epAddr: "192.168.1.1",
// 				port:   80,
// 			},
// 			want: PodEndpoint{
// 				IP:   "192.168.1.1",
// 				Port: 80,
// 				Pod: PodInfo{
// 					Key: types.NamespacedName{Name: "sample-node"},
// 				},
// 			},
// 		},
// 	}
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			got := buildPodEndpoint(tt.args.pod, tt.args.epAddr, tt.args.port)
// 			assert.Equal(t, tt.want, got)
// 		})
// 	}
// }

// func Test_buildNodePortEndpoint(t *testing.T) {
// 	type args struct {
// 		node       *corev1.Node
// 		instanceID string
// 		nodePort   int32
// 	}
// 	tests := []struct {
// 		name string
// 		args args
// 		want NodePortEndpoint
// 	}{
// 		{
// 			name: "base case",
// 			args: args{
// 				node: &corev1.Node{
// 					ObjectMeta: metav1.ObjectMeta{
// 						Name: "sample-node",
// 					},
// 				},
// 				instanceID: "i-xxxxx",
// 				nodePort:   33382,
// 			},
// 			want: NodePortEndpoint{
// 				Node: &corev1.Node{
// 					ObjectMeta: metav1.ObjectMeta{
// 						Name: "sample-node",
// 					},
// 				},
// 				InstanceID: "i-xxxxx",
// 				Port:       33382,
// 			},
// 		},
// 	}
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			got := buildNodePortEndpoint(tt.args.node, tt.args.instanceID, tt.args.nodePort)
// 			assert.Equal(t, tt.want, got)
// 		})
// 	}
// }

// func Test_convertCoreEndpointPortToDiscoveryEndpointPort(t *testing.T) {
// 	protocolTCP := corev1.ProtocolTCP
// 	type args struct {
// 		port corev1.EndpointPort
// 	}
// 	tests := []struct {
// 		name string
// 		args args
// 		want discovery.EndpointPort
// 	}{
// 		{
// 			name: "port with name",
// 			args: args{
// 				port: corev1.EndpointPort{
// 					Name:        "http",
// 					Port:        42,
// 					Protocol:    protocolTCP,
// 					AppProtocol: PointerOf("grpc"),
// 				},
// 			},
// 			want: discovery.EndpointPort{
// 				Name:        PointerOf("http"),
// 				Port:        PointerOf[int32](42),
// 				Protocol:    &protocolTCP,
// 				AppProtocol: PointerOf("grpc"),
// 			},
// 		},
// 		{
// 			name: "port without name",
// 			args: args{
// 				port: corev1.EndpointPort{
// 					Port:        42,
// 					Protocol:    protocolTCP,
// 					AppProtocol: PointerOf("grpc"),
// 				},
// 			},
// 			want: discovery.EndpointPort{
// 				Port:        PointerOf[int32](42),
// 				Protocol:    &protocolTCP,
// 				AppProtocol: PointerOf("grpc"),
// 			},
// 		},
// 	}
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			got := convertCoreEndpointPortToDiscoveryEndpointPort(tt.args.port)
// 			assert.Equal(t, tt.want, got)
// 		})
// 	}
// }

// func Test_convertCoreEndpointAddressToDiscoveryEndpoint(t *testing.T) {
// 	type args struct {
// 		endpoint corev1.EndpointAddress
// 		ready    bool
// 	}
// 	tests := []struct {
// 		name string
// 		args args
// 		want discovery.Endpoint
// 	}{
// 		{
// 			name: "ready endpoint",
// 			args: args{
// 				endpoint: corev1.EndpointAddress{
// 					IP: "192.168.1.1",
// 					TargetRef: &corev1.ObjectReference{
// 						Kind: "Pod",
// 						Name: "sample-pod",
// 					},
// 					NodeName: PointerOf("ip-172-20-36-42.us-west-2.compute.internal"),
// 					Hostname: "ip-172-20-36-42",
// 				},
// 				ready: true,
// 			},
// 			want: discovery.Endpoint{
// 				Addresses: []string{"192.168.1.1"},
// 				Conditions: discovery.EndpointConditions{
// 					Ready:       PointerOf[bool](true),
// 					Serving:     PointerOf[bool](true),
// 					Terminating: PointerOf[bool](false),
// 				},
// 				TargetRef: &corev1.ObjectReference{
// 					Kind: "Pod",
// 					Name: "sample-pod",
// 				},
// 				NodeName: PointerOf("ip-172-20-36-42.us-west-2.compute.internal"),
// 				Hostname: PointerOf("ip-172-20-36-42"),
// 			},
// 		},
// 		{
// 			name: "ready endpoint - empty hostName",
// 			args: args{
// 				endpoint: corev1.EndpointAddress{
// 					IP: "192.168.1.1",
// 					TargetRef: &corev1.ObjectReference{
// 						Kind: "Pod",
// 						Name: "sample-pod",
// 					},
// 					NodeName: PointerOf("ip-172-20-36-42.us-west-2.compute.internal"),
// 					Hostname: "",
// 				},
// 				ready: true,
// 			},
// 			want: discovery.Endpoint{
// 				Addresses: []string{"192.168.1.1"},
// 				Conditions: discovery.EndpointConditions{
// 					Ready:       PointerOf[bool](true),
// 					Serving:     PointerOf[bool](true),
// 					Terminating: PointerOf[bool](false),
// 				},
// 				TargetRef: &corev1.ObjectReference{
// 					Kind: "Pod",
// 					Name: "sample-pod",
// 				},
// 				NodeName: PointerOf("ip-172-20-36-42.us-west-2.compute.internal"),
// 				Hostname: nil,
// 			},
// 		},
// 		{
// 			name: "not endpoint",
// 			args: args{
// 				endpoint: corev1.EndpointAddress{
// 					IP: "192.168.1.1",
// 					TargetRef: &corev1.ObjectReference{
// 						Kind: "Pod",
// 						Name: "sample-pod",
// 					},
// 					NodeName: PointerOf("ip-172-20-36-42.us-west-2.compute.internal"),
// 					Hostname: "ip-172-20-36-42",
// 				},
// 				ready: false,
// 			},
// 			want: discovery.Endpoint{
// 				Addresses: []string{"192.168.1.1"},
// 				Conditions: discovery.EndpointConditions{
// 					Ready:       PointerOf[bool](false),
// 					Serving:     PointerOf[bool](false),
// 					Terminating: PointerOf[bool](false),
// 				},
// 				TargetRef: &corev1.ObjectReference{
// 					Kind: "Pod",
// 					Name: "sample-pod",
// 				},
// 				NodeName: PointerOf("ip-172-20-36-42.us-west-2.compute.internal"),
// 				Hostname: PointerOf("ip-172-20-36-42"),
// 			},
// 		},
// 		{
// 			name: "not endpoint - empty hostname",
// 			args: args{
// 				endpoint: corev1.EndpointAddress{
// 					IP: "192.168.1.1",
// 					TargetRef: &corev1.ObjectReference{
// 						Kind: "Pod",
// 						Name: "sample-pod",
// 					},
// 					NodeName: PointerOf("ip-172-20-36-42.us-west-2.compute.internal"),
// 					Hostname: "",
// 				},
// 				ready: true,
// 			},
// 			want: discovery.Endpoint{
// 				Addresses: []string{"192.168.1.1"},
// 				Conditions: discovery.EndpointConditions{
// 					Ready:       PointerOf[bool](true),
// 					Serving:     PointerOf[bool](true),
// 					Terminating: PointerOf[bool](false),
// 				},
// 				TargetRef: &corev1.ObjectReference{
// 					Kind: "Pod",
// 					Name: "sample-pod",
// 				},
// 				NodeName: PointerOf("ip-172-20-36-42.us-west-2.compute.internal"),
// 				Hostname: nil,
// 			},
// 		},
// 	}
// 	for _, tt := range tests {
// 		t.Run(tt.name, func(t *testing.T) {
// 			got := convertCoreEndpointAddressToDiscoveryEndpoint(tt.args.endpoint, tt.args.ready)
// 			assert.Equal(t, tt.want, got)
// 		})
// 	}
// }
