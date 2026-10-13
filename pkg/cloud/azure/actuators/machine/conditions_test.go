package machine

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestShouldUpdateCondition(t *testing.T) {
	testCases := []struct {
		oldCondition metav1.Condition
		newCondition metav1.Condition
		expected     bool
	}{
		{
			oldCondition: metav1.Condition{
				Reason:  "foo",
				Message: "bar",
				Status:  metav1.ConditionTrue,
			},
			newCondition: metav1.Condition{
				Reason:  "foo",
				Message: "bar",
				Status:  metav1.ConditionTrue,
			},
			expected: false,
		},
		{
			oldCondition: metav1.Condition{
				Reason:  "foo",
				Message: "bar",
				Status:  metav1.ConditionTrue,
			},
			newCondition: metav1.Condition{
				Reason:  "different reason",
				Message: "bar",
				Status:  metav1.ConditionTrue,
			},
			expected: true,
		},
		{
			oldCondition: metav1.Condition{
				Reason:  "foo",
				Message: "different message",
				Status:  metav1.ConditionTrue,
			},
			newCondition: metav1.Condition{
				Reason:  "foo",
				Message: "bar",
				Status:  metav1.ConditionTrue,
			},
			expected: true,
		},
		{
			oldCondition: metav1.Condition{
				Reason:  "foo",
				Message: "bar",
				Status:  metav1.ConditionTrue,
			},
			newCondition: metav1.Condition{
				Reason:  "foo",
				Message: "bar",
				Status:  metav1.ConditionFalse,
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
