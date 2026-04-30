package shared_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/vngcloud/vngcloud-load-balancer-controller/internal/controller/gateway/shared"
)

func TestSetCondition_AddsAndUpdates(t *testing.T) {
	var conds []metav1.Condition
	shared.SetCondition(&conds, "Accepted", metav1.ConditionTrue, "Accepted", "ok", 1)
	assert.Len(t, conds, 1)
	assert.Equal(t, metav1.ConditionTrue, conds[0].Status)

	shared.SetCondition(&conds, "Accepted", metav1.ConditionFalse, "Invalid", "bad", 2)
	assert.Len(t, conds, 1)
	assert.Equal(t, metav1.ConditionFalse, conds[0].Status)
	assert.Equal(t, "Invalid", conds[0].Reason)
	assert.EqualValues(t, 2, conds[0].ObservedGeneration)
}

func TestSetCondition_AddsMultipleTypes(t *testing.T) {
	var conds []metav1.Condition
	shared.SetCondition(&conds, "Accepted", metav1.ConditionTrue, "Accepted", "ok", 1)
	shared.SetCondition(&conds, "Programmed", metav1.ConditionTrue, "Programmed", "ok", 1)
	assert.Len(t, conds, 2)
}
