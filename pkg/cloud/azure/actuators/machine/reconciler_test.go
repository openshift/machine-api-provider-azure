package machine

import (
	"context"
	"testing"

	"sigs.k8s.io/cluster-api-provider-azure/pkg/apis/azureprovider/v1beta1"
)

func TestExists(t *testing.T) {
	testCases := []struct {
		vmService *FakeVMService
		expected  bool
	}{
		{
			vmService: &FakeVMService{
				Name:              "machine-test",
				ID:                "machine-test-ID",
				ProvisioningState: string(v1beta1.VMStateSucceeded),
			},
			expected: true,
		},
		{
			vmService: &FakeVMService{
				Name:              "machine-test",
				ID:                "machine-test-ID",
				ProvisioningState: string(v1beta1.VMStateUpdating),
			},
			expected: true,
		},
		{
			vmService: &FakeVMService{
				Name:              "machine-test",
				ID:                "machine-test-ID",
				ProvisioningState: "",
			},
			expected: false,
		},
	}
	for _, tc := range testCases {
		r := newFakeReconciler(t)
		r.virtualMachinesSvc = tc.vmService
		exists, err := r.Exists(context.TODO())
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if exists != tc.expected {
			t.Fatalf("Expected: %v, got: %v", tc.expected, exists)
		}
	}
}
