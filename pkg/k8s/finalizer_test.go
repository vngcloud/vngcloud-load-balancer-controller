package k8s

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// RemoveFinalizers must treat an already-deleted object as success: the
// finalizer it would strip is gone with the object, so there is nothing left
// to do and the caller should not see an error (which would re-queue).
func TestRemoveFinalizers_ObjectAlreadyGone(t *testing.T) {
	// Build a client with NO objects → Get returns NotFound.
	c := fake.NewClientBuilder().Build()
	m := NewDefaultFinalizerManager(c, logr.Discard())

	obj := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "prod", Name: "gone"},
	}
	err := m.RemoveFinalizers(context.Background(), obj, "test/fin")
	assert.NoError(t, err)
}
