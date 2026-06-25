package alb_gateway_uc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	gwv1 "sigs.k8s.io/gateway-api/apis/v1"

	gwv1alpha1 "github.com/vngcloud/vngcloud-load-balancer-controller/api/gateway/v1alpha1"
	"github.com/vngcloud/vngcloud-load-balancer-controller/api/v1alpha1"
)

func makeListenerEntry(_ string) v1alpha1.Listener {
	return v1alpha1.Listener{}
}

func makeHTTPSListener(name string, secretRefs []gwv1.SecretObjectReference) gwv1.Listener {
	return gwv1.Listener{
		Name:     gwv1.SectionName(name),
		Protocol: gwv1.HTTPSProtocolType,
		Port:     443,
		TLS: &gwv1.GatewayTLSConfig{
			CertificateRefs: secretRefs,
		},
	}
}

func makeSecretRef(ns, name string) gwv1.SecretObjectReference {
	ref := gwv1.SecretObjectReference{Name: gwv1.ObjectName(name)}
	if ns != "" {
		n := gwv1.Namespace(ns)
		ref.Namespace = &n
	}
	return ref
}

func TestApplyListenerCertificates_NonHTTPS(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw", UID: types.UID("uid1")},
	}
	task := newTestTask(t, gw)
	task.listenerPolicies = make(map[string]*gwv1alpha1.VKSGatewayPolicy)

	// HTTP listener → should be no-op
	l := &gwv1.Listener{Protocol: gwv1.HTTPProtocolType, TLS: nil}
	entry := makeListenerEntry("http")
	task.applyListenerCertificates(&entry, l)
	assert.Nil(t, entry.CertificateDefault)
	assert.Nil(t, entry.CertificateAuthorities)
}

func TestApplyListenerCertificates_HTTPSNoTLS(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"},
	}
	task := newTestTask(t, gw)
	task.listenerPolicies = make(map[string]*gwv1alpha1.VKSGatewayPolicy)

	// HTTPS but TLS is nil → no-op
	l := &gwv1.Listener{Protocol: gwv1.HTTPSProtocolType, TLS: nil}
	entry := makeListenerEntry("https")
	task.applyListenerCertificates(&entry, l)
	assert.Nil(t, entry.CertificateDefault)
}

func TestApplyListenerCertificates_CertIDs(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"},
	}
	task := newTestTask(t, gw)
	task.listenerPolicies = make(map[string]*gwv1alpha1.VKSGatewayPolicy)

	// Unscoped policy with cert IDs
	task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{}
	task.unscopedPolicy.Spec.CertificateIDs = []string{"cert-1", "cert-2", "cert-3"}

	l := &gwv1.Listener{
		Name:     "https",
		Protocol: gwv1.HTTPSProtocolType,
		TLS:      &gwv1.GatewayTLSConfig{},
	}
	entry := makeListenerEntry("https")
	task.applyListenerCertificates(&entry, l)

	assert.NotNil(t, entry.CertificateDefault)
	assert.Equal(t, "cert-1", *entry.CertificateDefault.Id)
	assert.Len(t, entry.CertificateAuthorities, 2)
	assert.Equal(t, "cert-2", *entry.CertificateAuthorities[0].Id)
	assert.Equal(t, "cert-3", *entry.CertificateAuthorities[1].Id)
}

func TestApplyListenerCertificates_ScopedCertIDsWin(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"},
	}
	task := newTestTask(t, gw)
	task.listenerPolicies = make(map[string]*gwv1alpha1.VKSGatewayPolicy)

	// Per-listener policy wins over unscoped
	scoped := &gwv1alpha1.VKSGatewayPolicy{}
	scoped.Spec.CertificateIDs = []string{"scoped-cert"}
	task.listenerPolicies["https"] = scoped

	task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{}
	task.unscopedPolicy.Spec.CertificateIDs = []string{"unscoped-cert"}

	l := &gwv1.Listener{
		Name:     "https",
		Protocol: gwv1.HTTPSProtocolType,
		TLS:      &gwv1.GatewayTLSConfig{},
	}
	entry := makeListenerEntry("https")
	task.applyListenerCertificates(&entry, l)

	assert.Equal(t, "scoped-cert", *entry.CertificateDefault.Id)
	assert.Empty(t, entry.CertificateAuthorities)
}

func TestApplyListenerCertificates_SecretRefs(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"},
	}
	task := newTestTask(t, gw)
	task.listenerPolicies = make(map[string]*gwv1alpha1.VKSGatewayPolicy)
	task.unscopedPolicy = nil

	l := makeHTTPSListener("https", []gwv1.SecretObjectReference{
		makeSecretRef("", "my-tls-secret"),
		makeSecretRef("", "my-ca-secret"),
	})
	entry := makeListenerEntry("https")
	task.applyListenerCertificates(&entry, &l)

	assert.NotNil(t, entry.CertificateDefault)
	assert.Equal(t, "my-tls-secret", *entry.CertificateDefault.SecretName)
	assert.Len(t, entry.CertificateAuthorities, 1)
	assert.Equal(t, "my-ca-secret", *entry.CertificateAuthorities[0].SecretName)
}

func TestApplyListenerCertificates_CrossNsSkipped(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"},
	}
	task := newTestTask(t, gw)
	task.listenerPolicies = make(map[string]*gwv1alpha1.VKSGatewayPolicy)
	task.unscopedPolicy = nil

	// Cross-namespace ref should be skipped, same-ns ref should be kept
	l := makeHTTPSListener("https", []gwv1.SecretObjectReference{
		makeSecretRef("other-ns", "cross-secret"), // skipped
		makeSecretRef("", "same-ns-secret"),       // kept
	})
	entry := makeListenerEntry("https")
	task.applyListenerCertificates(&entry, &l)

	assert.NotNil(t, entry.CertificateDefault)
	assert.Equal(t, "same-ns-secret", *entry.CertificateDefault.SecretName)
	assert.Empty(t, entry.CertificateAuthorities)
}

func TestApplyListenerCertificates_EmptySecretName(t *testing.T) {
	gw := &gwv1.Gateway{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"},
	}
	task := newTestTask(t, gw)
	task.listenerPolicies = make(map[string]*gwv1alpha1.VKSGatewayPolicy)
	task.unscopedPolicy = nil

	// Empty secret name skipped
	l := makeHTTPSListener("https", []gwv1.SecretObjectReference{
		makeSecretRef("", ""),
	})
	entry := makeListenerEntry("https")
	task.applyListenerCertificates(&entry, &l)

	assert.Nil(t, entry.CertificateDefault)
}

func TestBuildCreateCertificates(t *testing.T) {
	t.Run("no HTTPS listeners returns nil", func(t *testing.T) {
		gw := &gwv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"},
			Spec: gwv1.GatewaySpec{
				Listeners: []gwv1.Listener{
					{Name: "http", Protocol: gwv1.HTTPProtocolType, Port: 80},
				},
			},
		}
		task := newTestTask(t, gw)
		task.listenerPolicies = make(map[string]*gwv1alpha1.VKSGatewayPolicy)
		got := task.buildCreateCertificates()
		assert.Nil(t, got)
	})

	t.Run("cert-ID listeners skip Secret collection", func(t *testing.T) {
		gw := &gwv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"},
			Spec: gwv1.GatewaySpec{
				Listeners: []gwv1.Listener{
					makeHTTPSListener("https", []gwv1.SecretObjectReference{makeSecretRef("", "my-secret")}),
				},
			},
		}
		task := newTestTask(t, gw)
		task.listenerPolicies = make(map[string]*gwv1alpha1.VKSGatewayPolicy)
		task.unscopedPolicy = &gwv1alpha1.VKSGatewayPolicy{}
		task.unscopedPolicy.Spec.CertificateIDs = []string{"cert-id-1"}
		got := task.buildCreateCertificates()
		assert.Nil(t, got) // cert-ID path → no import needed
	})

	t.Run("dedupes same Secret across listeners", func(t *testing.T) {
		gw := &gwv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"},
			Spec: gwv1.GatewaySpec{
				Listeners: []gwv1.Listener{
					makeHTTPSListener("https1", []gwv1.SecretObjectReference{makeSecretRef("", "shared-secret")}),
					makeHTTPSListener("https2", []gwv1.SecretObjectReference{makeSecretRef("", "shared-secret")}),
				},
			},
		}
		task := newTestTask(t, gw)
		task.listenerPolicies = make(map[string]*gwv1alpha1.VKSGatewayPolicy)
		task.unscopedPolicy = nil
		got := task.buildCreateCertificates()
		assert.Len(t, got, 1) // deduped
		assert.Equal(t, "shared-secret", got[0].SecretName)
	})

	t.Run("cross-ns secrets skipped", func(t *testing.T) {
		gw := &gwv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"},
			Spec: gwv1.GatewaySpec{
				Listeners: []gwv1.Listener{
					makeHTTPSListener("https", []gwv1.SecretObjectReference{makeSecretRef("other-ns", "cross")}),
				},
			},
		}
		task := newTestTask(t, gw)
		task.listenerPolicies = make(map[string]*gwv1alpha1.VKSGatewayPolicy)
		task.unscopedPolicy = nil
		got := task.buildCreateCertificates()
		assert.Nil(t, got)
	})

	t.Run("multiple unique secrets sorted", func(t *testing.T) {
		gw := &gwv1.Gateway{
			ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gw"},
			Spec: gwv1.GatewaySpec{
				Listeners: []gwv1.Listener{
					makeHTTPSListener("https1", []gwv1.SecretObjectReference{makeSecretRef("", "z-secret")}),
					makeHTTPSListener("https2", []gwv1.SecretObjectReference{makeSecretRef("", "a-secret")}),
				},
			},
		}
		task := newTestTask(t, gw)
		task.listenerPolicies = make(map[string]*gwv1alpha1.VKSGatewayPolicy)
		task.unscopedPolicy = nil
		got := task.buildCreateCertificates()
		assert.Len(t, got, 2)
		assert.Equal(t, "a-secret", got[0].SecretName)
		assert.Equal(t, "z-secret", got[1].SecretName)
	})
}
