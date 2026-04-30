package shared

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SetCondition adds or updates a condition on the given slice. Reuses metav1.Condition
// semantics — LastTransitionTime is only refreshed when Status changes.
func SetCondition(conds *[]metav1.Condition, condType string, status metav1.ConditionStatus, reason, msg string, gen int64) {
	for i := range *conds {
		if (*conds)[i].Type == condType {
			if (*conds)[i].Status != status {
				(*conds)[i].LastTransitionTime = metav1.Now()
			}
			(*conds)[i].Status = status
			(*conds)[i].Reason = reason
			(*conds)[i].Message = msg
			(*conds)[i].ObservedGeneration = gen
			return
		}
	}
	*conds = append(*conds, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            msg,
		ObservedGeneration: gen,
		LastTransitionTime: metav1.Now(),
	})
}
