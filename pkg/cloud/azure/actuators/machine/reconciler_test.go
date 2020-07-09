package machine

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2019-12-01/compute"
	machinev1beta1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	machinecontroller "github.com/openshift/machine-api-operator/pkg/controller/machine"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/apis/azureprovider/v1beta1"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/actuators"
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

func TestSetMachineCloudProviderSpecifics(t *testing.T) {
	testStatus := "testState"
	testSize := compute.VirtualMachineSizeTypesBasicA4
	testRegion := "testRegion"
	testZone := "testZone"
	testZones := []string{testZone}

	maxPrice := "1"
	r := Reconciler{
		scope: &actuators.MachineScope{
			Machine: &machinev1beta1.Machine{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "",
					Namespace: "",
				},
			},
			MachineConfig: &v1beta1.AzureMachineProviderSpec{
				SpotVMOptions: &v1beta1.SpotVMOptions{
					MaxPrice: &maxPrice,
				},
			},
		},
	}

	vm := compute.VirtualMachine{
		VirtualMachineProperties: &compute.VirtualMachineProperties{
			ProvisioningState: &testStatus,
			HardwareProfile: &compute.HardwareProfile{
				VMSize: testSize,
			},
		},
		Location: &testRegion,
		Zones:    &testZones,
	}

	r.setMachineCloudProviderSpecifics(vm)

	actualInstanceStateAnnotation := r.scope.Machine.Annotations[MachineInstanceStateAnnotationName]
	if actualInstanceStateAnnotation != testStatus {
		t.Errorf("Expected instance state annotation: %v, got: %v", actualInstanceStateAnnotation, vm.VirtualMachineProperties.ProvisioningState)
	}

	actualMachineTypeLabel := r.scope.Machine.Labels[MachineInstanceTypeLabelName]
	if actualMachineTypeLabel != string(vm.HardwareProfile.VMSize) {
		t.Errorf("Expected machine type label: %v, got: %v", actualMachineTypeLabel, string(vm.HardwareProfile.VMSize))
	}

	actualMachineRegionLabel := r.scope.Machine.Labels[machinecontroller.MachineRegionLabelName]
	if actualMachineRegionLabel != *vm.Location {
		t.Errorf("Expected machine region label: %v, got: %v", actualMachineRegionLabel, *vm.Location)
	}

	actualMachineAZLabel := r.scope.Machine.Labels[machinecontroller.MachineAZLabelName]
	if actualMachineAZLabel != strings.Join(*vm.Zones, ",") {
		t.Errorf("Expected machine zone label: %v, got: %v", actualMachineAZLabel, strings.Join(*vm.Zones, ","))
	}

	if _, ok := r.scope.Machine.Spec.Labels[machinecontroller.MachineInterruptibleInstanceLabelName]; !ok {
		t.Error("Missing spot instance label in machine spec")
	}

}
