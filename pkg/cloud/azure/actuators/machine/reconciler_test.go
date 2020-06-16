package machine

import (
	"context"
	"strconv"
	"testing"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2019-12-01/compute"
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

func TestGetSpotVMOptions(t *testing.T) {
	maxPriceString := "100"
	maxPriceFloat, err := strconv.ParseFloat(maxPriceString, 64)
	if err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name           string
		spotVMOptions  *v1beta1.SpotVMOptions
		priority       compute.VirtualMachinePriorityTypes
		evictionPolicy compute.VirtualMachineEvictionPolicyTypes
		billingProfile *compute.BillingProfile
	}{
		{
			name: "get spot vm option succefully",
			spotVMOptions: &v1beta1.SpotVMOptions{
				MaxPrice: &maxPriceString,
			},
			priority:       compute.Spot,
			evictionPolicy: compute.Deallocate,
			billingProfile: &compute.BillingProfile{
				MaxPrice: &maxPriceFloat,
			},
		},
		{
			name:           "return empty values on missing options",
			spotVMOptions:  nil,
			priority:       "",
			evictionPolicy: "",
			billingProfile: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			priority, evictionPolicy, billingProfile, err := getSpotVMOptions(tc.spotVMOptions)
			if err != nil {
				t.Fatal(err)
			}

			if priority != tc.priority {
				t.Fatalf("Expected priority %s, got: %s", priority, tc.priority)
			}

			if evictionPolicy != tc.evictionPolicy {
				t.Fatalf("Expected eviction policy %s, got: %s", evictionPolicy, tc.evictionPolicy)
			}

			// only check billing profile when spotVMOptions object is not nil
			if tc.spotVMOptions != nil {
				if *billingProfile.MaxPrice != *tc.billingProfile.MaxPrice {
					t.Fatalf("Expected billing profile max price %d, got: %d", billingProfile, tc.billingProfile)
				}
			} else {
				if billingProfile != nil {
					t.Fatal("Expected billing profile to be nil")
				}
			}
		})
	}
}
