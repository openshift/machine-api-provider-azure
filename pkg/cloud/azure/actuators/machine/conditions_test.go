package machine

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/apis/azureprovider/v1beta1"
)

func TestShouldUpdateCondition(t *testing.T) {
	testCases := []struct {
		oldCondition v1beta1.AzureMachineProviderCondition
		newCondition v1beta1.AzureMachineProviderCondition
		expected     bool
	}{
		{
			oldCondition: v1beta1.AzureMachineProviderCondition{
				Reason:  "foo",
				Message: "bar",
				Status:  corev1.ConditionTrue,
			},
			newCondition: v1beta1.AzureMachineProviderCondition{
				Reason:  "foo",
				Message: "bar",
				Status:  corev1.ConditionTrue,
			},
			expected: false,
		},
		{
			oldCondition: v1beta1.AzureMachineProviderCondition{
				Reason:  "foo",
				Message: "bar",
				Status:  corev1.ConditionTrue,
			},
			newCondition: v1beta1.AzureMachineProviderCondition{
				Reason:  "different reason",
				Message: "bar",
				Status:  corev1.ConditionTrue,
			},
			expected: true,
		},
		{
			oldCondition: v1beta1.AzureMachineProviderCondition{
				Reason:  "foo",
				Message: "different message",
				Status:  corev1.ConditionTrue,
			},
			newCondition: v1beta1.AzureMachineProviderCondition{
				Reason:  "foo",
				Message: "bar",
				Status:  corev1.ConditionTrue,
			},
			expected: true,
		},
		{
			oldCondition: v1beta1.AzureMachineProviderCondition{
				Reason:  "foo",
				Message: "bar",
				Status:  corev1.ConditionTrue,
			},
			newCondition: v1beta1.AzureMachineProviderCondition{
				Reason:  "foo",
				Message: "bar",
				Status:  corev1.ConditionFalse,
			},
			expected: true,
		},
	}

	for _, tc := range testCases {
		if got := shouldUpdateCondition(tc.oldCondition, tc.newCondition); got != tc.expected {
			t.Errorf("Expected %t, got %t", got, tc.expected)
		}
	}
}
