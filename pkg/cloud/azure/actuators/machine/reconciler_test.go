package machine

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/Azure/go-autorest/autorest"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2020-06-30/compute"
	. "github.com/onsi/gomega"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	machinecontroller "github.com/openshift/machine-api-operator/pkg/controller/machine"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/apis/azureprovider/v1beta1"
	providerspecv1 "sigs.k8s.io/cluster-api-provider-azure/pkg/apis/azureprovider/v1beta1"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/actuators"
)

func TestExists(t *testing.T) {
	testCases := []struct {
		name        string
		vmService   azure.Service
		expected    bool
		errExpected bool
	}{
		{
			name: "VM has 'Succeeded' ProvisiongState",
			vmService: &FakeVMService{
				Name:              "machine-test",
				ID:                "machine-test-ID",
				ProvisioningState: string(v1beta1.VMStateSucceeded),
			},
			expected:    true,
			errExpected: false,
		},
		{
			name: "VM has 'Updating' ProvisiongState",
			vmService: &FakeVMService{
				Name:              "machine-test",
				ID:                "machine-test-ID",
				ProvisioningState: string(v1beta1.VMStateUpdating),
			},
			expected:    true,
			errExpected: false,
		},
		{
			name: "VM has 'Creating' ProvisiongState",
			vmService: &FakeVMService{
				Name:              "machine-test",
				ID:                "machine-test-ID",
				ProvisioningState: string(v1beta1.VMStateCreating),
			},
			expected:    true,
			errExpected: false,
		},
		{
			name: "VM has 'Deleting' ProvisiongState",
			vmService: &FakeVMService{
				Name:              "machine-test",
				ID:                "machine-test-ID",
				ProvisioningState: string(v1beta1.VMStateDeleting),
			},
			expected:    true,
			errExpected: true,
		},
		{
			name: "VM has some arbitrary ProvisiongState",
			vmService: &FakeVMService{
				Name:              "machine-test",
				ID:                "machine-test-ID",
				ProvisioningState: "some random string",
			},
			expected:    true,
			errExpected: false,
		},
		{
			name: "VM has 'Failed' ProvisiongState",
			vmService: &FakeVMService{
				Name:              "machine-test",
				ID:                "machine-test-ID",
				ProvisioningState: "Failed",
			},
			expected:    true,
			errExpected: true,
		},
		{
			name: "VM does not exists",
			vmService: &FakeBrokenVmService{
				ErrorToReturn: autorest.DetailedError{
					StatusCode: 404,
					Message:    "Not found",
				},
			},
			expected:    false,
			errExpected: false,
		},
		{
			name: "Internal service error",
			vmService: &FakeBrokenVmService{
				ErrorToReturn: autorest.DetailedError{
					StatusCode: 500,
					Message:    "boom",
				},
			},
			expected:    false,
			errExpected: true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			r := newFakeReconciler(t)
			r.virtualMachinesSvc = tc.vmService
			exists, err := r.Exists(context.TODO())

			if exists != tc.expected {
				t.Fatalf("Expected: %v, got: %v", tc.expected, exists)
			}

			if err != nil && !tc.errExpected {
				t.Fatalf("Unexpected error: %v", err)
			}
		})
	}
}

func TestGetSpotVMOptions(t *testing.T) {
	maxPrice := resource.MustParse("0.001")
	maxPriceFloat, err := strconv.ParseFloat(maxPrice.AsDec().String(), 64)
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
				MaxPrice: &maxPrice,
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
		{
			name:           "not return an error with empty spot vm options",
			spotVMOptions:  &v1beta1.SpotVMOptions{},
			priority:       compute.Spot,
			evictionPolicy: compute.Deallocate,
			billingProfile: &compute.BillingProfile{
				MaxPrice: nil,
			},
		},
		{
			name: "not return an error if the max price is nil",
			spotVMOptions: &v1beta1.SpotVMOptions{
				MaxPrice: nil,
			},
			priority:       compute.Spot,
			evictionPolicy: compute.Deallocate,
			billingProfile: &compute.BillingProfile{
				MaxPrice: nil,
			},
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
				if tc.billingProfile.MaxPrice != nil {
					if billingProfile == nil {
						t.Fatal("Expected billing profile to not be nil")
					} else if *billingProfile.MaxPrice != *tc.billingProfile.MaxPrice {
						t.Fatalf("Expected billing profile max price %d, got: %d", billingProfile, tc.billingProfile)
					}
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

	maxPrice := resource.MustParse("1")
	r := Reconciler{
		scope: &actuators.MachineScope{
			Machine: &machinev1.Machine{
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

func TestSetMachineCloudProviderSpecificsTable(t *testing.T) {
	abcZones := []string{"a", "b", "c"}

	testCases := []struct {
		name                string
		scope               func(t *testing.T) *actuators.MachineScope
		vm                  compute.VirtualMachine
		expectedLabels      map[string]string
		expectedAnnotations map[string]string
		expectedSpecLabels  map[string]string
	}{
		{
			name:  "with a blank vm",
			scope: func(t *testing.T) *actuators.MachineScope { return newFakeScope(t, "worker") },
			vm:    compute.VirtualMachine{},
			expectedLabels: map[string]string{
				providerspecv1.MachineRoleLabel: "worker",
				machinev1.MachineClusterIDLabel: "clusterID",
			},
			expectedAnnotations: map[string]string{
				MachineInstanceStateAnnotationName: "",
			},
			expectedSpecLabels: nil,
		},
		{
			name:  "with a running vm",
			scope: func(t *testing.T) *actuators.MachineScope { return newFakeScope(t, "good-worker") },
			vm: compute.VirtualMachine{
				VirtualMachineProperties: &compute.VirtualMachineProperties{
					ProvisioningState: pointer.StringPtr("Running"),
				},
			},
			expectedLabels: map[string]string{
				providerspecv1.MachineRoleLabel: "good-worker",
				machinev1.MachineClusterIDLabel: "clusterID",
			},
			expectedAnnotations: map[string]string{
				MachineInstanceStateAnnotationName: "Running",
			},
			expectedSpecLabels: nil,
		},
		{
			name:  "with a VMSize set vm",
			scope: func(t *testing.T) *actuators.MachineScope { return newFakeScope(t, "sized-worker") },
			vm: compute.VirtualMachine{
				VirtualMachineProperties: &compute.VirtualMachineProperties{
					HardwareProfile: &compute.HardwareProfile{
						VMSize: "big",
					},
				},
			},
			expectedLabels: map[string]string{
				providerspecv1.MachineRoleLabel: "sized-worker",
				machinev1.MachineClusterIDLabel: "clusterID",
				MachineInstanceTypeLabelName:    "big",
			},
			expectedAnnotations: map[string]string{
				MachineInstanceStateAnnotationName: "",
			},
			expectedSpecLabels: nil,
		},
		{
			name:  "with a vm location",
			scope: func(t *testing.T) *actuators.MachineScope { return newFakeScope(t, "located-worker") },
			vm: compute.VirtualMachine{
				Location: pointer.StringPtr("nowhere"),
			},
			expectedLabels: map[string]string{
				providerspecv1.MachineRoleLabel: "located-worker",
				machinev1.MachineClusterIDLabel: "clusterID",
				MachineRegionLabelName:          "nowhere",
			},
			expectedAnnotations: map[string]string{
				MachineInstanceStateAnnotationName: "",
			},
			expectedSpecLabels: nil,
		},
		{
			name:  "with a vm with zones",
			scope: func(t *testing.T) *actuators.MachineScope { return newFakeScope(t, "zoned-worker") },
			vm: compute.VirtualMachine{
				Zones: &abcZones,
			},
			expectedLabels: map[string]string{
				providerspecv1.MachineRoleLabel: "zoned-worker",
				machinev1.MachineClusterIDLabel: "clusterID",
				MachineAZLabelName:              "a,b,c",
			},
			expectedAnnotations: map[string]string{
				MachineInstanceStateAnnotationName: "",
			},
			expectedSpecLabels: nil,
		},
		{
			name: "with a vm on spot",
			scope: func(t *testing.T) *actuators.MachineScope {
				scope := newFakeScope(t, "spot-worker")
				scope.MachineConfig.SpotVMOptions = &v1beta1.SpotVMOptions{}
				return scope
			},
			vm: compute.VirtualMachine{},
			expectedLabels: map[string]string{
				providerspecv1.MachineRoleLabel:                         "spot-worker",
				machinev1.MachineClusterIDLabel:                         "clusterID",
				machinecontroller.MachineInterruptibleInstanceLabelName: "",
			},
			expectedAnnotations: map[string]string{
				MachineInstanceStateAnnotationName: "",
			},
			expectedSpecLabels: map[string]string{
				machinecontroller.MachineInterruptibleInstanceLabelName: "",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			r := newFakeReconcilerWithScope(t, tc.scope(t))
			r.setMachineCloudProviderSpecifics(tc.vm)

			machine := r.scope.Machine
			g.Expect(machine.Labels).To(Equal(tc.expectedLabels))
			g.Expect(machine.Annotations).To(Equal(tc.expectedAnnotations))
			g.Expect(machine.Spec.Labels).To(Equal(tc.expectedSpecLabels))
		})
	}
}
