package machine

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/apis/azureprovider/v1beta1"
)

const (
	machineCreationSucceedReason  = "MachineCreationSucceeded"
	machineCreationSucceedMessage = "machine successfully created"
	machineCreationFailedReason   = "MachineCreationFailed"
)

func shouldUpdateCondition(
	oldCondition v1beta1.AzureMachineProviderCondition,
	newCondition v1beta1.AzureMachineProviderCondition,
) bool {
	if oldCondition.Status != newCondition.Status ||
		oldCondition.Reason != newCondition.Reason ||
		oldCondition.Message != newCondition.Message {
		return true
	}
	return false
}

// setMachineProviderCondition sets the condition for the machine and
// returns the new slice of conditions.
// If the machine does not already have a condition with the specified type,
// a condition will be added to the slice.
// If the machine does already have a condition with the specified type,
// the condition will be updated if either of the following are true.
// 1) Requested Status is different than existing status.
// 2) requested Reason is different that existing one.
// 3) requested Message is different that existing one.
func setMachineProviderCondition(conditions []v1beta1.AzureMachineProviderCondition, newCondition v1beta1.AzureMachineProviderCondition) []v1beta1.AzureMachineProviderCondition {
	now := metav1.Now()
	currentCondition := findCondition(conditions, newCondition.Type)
	if currentCondition == nil {
		klog.V(4).Infof("Adding new provider condition %v", newCondition)
		conditions = append(
			conditions,
			v1beta1.AzureMachineProviderCondition{
				Type:               newCondition.Type,
				Status:             newCondition.Status,
				Reason:             newCondition.Reason,
				Message:            newCondition.Message,
				LastTransitionTime: now,
				LastProbeTime:      now,
			},
		)
	} else {
		if shouldUpdateCondition(
			*currentCondition,
			newCondition,
		) {
			klog.V(4).Infof("Updating provider condition %v", newCondition)
			if currentCondition.Status != newCondition.Status {
				currentCondition.LastTransitionTime = now
			}
			currentCondition.Status = newCondition.Status
			currentCondition.Reason = newCondition.Reason
			currentCondition.Message = newCondition.Message
			currentCondition.LastProbeTime = now
		}
	}
	return conditions
}

// findCondition finds in the machine the condition that has the
// specified condition type. If none exists, then returns nil.
func findCondition(conditions []v1beta1.AzureMachineProviderCondition, conditionType v1beta1.AzureMachineProviderConditionType) *v1beta1.AzureMachineProviderCondition {
	for i, condition := range conditions {
		if condition.Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}
