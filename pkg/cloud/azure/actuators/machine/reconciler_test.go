package machine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2021-11-01/compute"
	"github.com/Azure/go-autorest/autorest"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/gomega"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	machinecontroller "github.com/openshift/machine-api-operator/pkg/controller/machine"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/actuators"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/decode"
	mock_azure "github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/mock"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
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
				ProvisioningState: string(machinev1.VMStateSucceeded),
			},
			expected:    true,
			errExpected: false,
		},
		{
			name: "VM has 'Updating' ProvisiongState",
			vmService: &FakeVMService{
				Name:              "machine-test",
				ID:                "machine-test-ID",
				ProvisioningState: string(machinev1.VMStateUpdating),
			},
			expected:    true,
			errExpected: false,
		},
		{
			name: "VM has 'Creating' ProvisiongState",
			vmService: &FakeVMService{
				Name:              "machine-test",
				ID:                "machine-test-ID",
				ProvisioningState: string(machinev1.VMStateCreating),
			},
			expected:    true,
			errExpected: false,
		},
		{
			name: "VM has 'Deleting' ProvisiongState",
			vmService: &FakeVMService{
				Name:              "machine-test",
				ID:                "machine-test-ID",
				ProvisioningState: string(machinev1.VMStateDeleting),
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
			expected:    false,
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

func TestSetMachineCloudProviderSpecifics(t *testing.T) {
	testStatus := "testState"
	testSize := string(compute.VirtualMachineSizeTypesBasicA4)
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
			MachineConfig: &machinev1.AzureMachineProviderSpec{
				SpotVMOptions: &machinev1.SpotVMOptions{
					MaxPrice: &maxPrice,
				},
			},
		},
	}

	vm := &decode.VirtualMachine{
		VirtualMachineProperties: &decode.VirtualMachineProperties{
			ProvisioningState: &testStatus,
			HardwareProfile: &decode.HardwareProfile{
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
		vm                  decode.VirtualMachine
		expectedLabels      map[string]string
		expectedAnnotations map[string]string
		expectedSpecLabels  map[string]string
	}{
		{
			name:  "with a blank vm",
			scope: func(t *testing.T) *actuators.MachineScope { return newFakeScope(t, "worker") },
			vm:    decode.VirtualMachine{},
			expectedLabels: map[string]string{
				actuators.MachineRoleLabel:      "worker",
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
			vm: decode.VirtualMachine{
				VirtualMachineProperties: &decode.VirtualMachineProperties{
					ProvisioningState: ptr.To[string]("Running"),
				},
			},
			expectedLabels: map[string]string{
				actuators.MachineRoleLabel:      "good-worker",
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
			vm: decode.VirtualMachine{
				VirtualMachineProperties: &decode.VirtualMachineProperties{
					HardwareProfile: &decode.HardwareProfile{
						VMSize: "big",
					},
				},
			},
			expectedLabels: map[string]string{
				actuators.MachineRoleLabel:      "sized-worker",
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
			vm: decode.VirtualMachine{
				Location: ptr.To[string]("nowhere"),
			},
			expectedLabels: map[string]string{
				actuators.MachineRoleLabel:      "located-worker",
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
			vm: decode.VirtualMachine{
				Zones: &abcZones,
			},
			expectedLabels: map[string]string{
				actuators.MachineRoleLabel:      "zoned-worker",
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
				scope.MachineConfig.SpotVMOptions = &machinev1.SpotVMOptions{}
				return scope
			},
			vm: decode.VirtualMachine{},
			expectedLabels: map[string]string{
				actuators.MachineRoleLabel:                              "spot-worker",
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
			r.setMachineCloudProviderSpecifics(&tc.vm)

			machine := r.scope.Machine
			g.Expect(machine.Labels).To(Equal(tc.expectedLabels))
			g.Expect(machine.Annotations).To(Equal(tc.expectedAnnotations))
			g.Expect(machine.Spec.Labels).To(Equal(tc.expectedSpecLabels))
		})
	}
}

func TestCreateAvailabilitySet(t *testing.T) {
	g := NewGomegaWithT(t)
	mockCtrl := gomock.NewController(t)

	testCases := []struct {
		name                 string
		expectedError        bool
		expectedASName       string
		labels               map[string]string
		availabilitySetsSvc  func() *mock_azure.MockService
		availabilityZonesSvc func() *mock_azure.MockService
		inputASName          string
		spotVMOptions        *machinev1.SpotVMOptions
	}{
		{
			name:          "Error when availability zones client fails",
			expectedError: true,
			availabilityZonesSvc: func() *mock_azure.MockService {
				availabilityZonesSvc := mock_azure.NewMockService(mockCtrl)
				availabilityZonesSvc.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, errors.New("test error")).Times(1)
				return availabilityZonesSvc
			},
			availabilitySetsSvc: func() *mock_azure.MockService {
				return nil
			},
		},
		{
			name:          "Error when availability zones client returns wrong type",
			expectedError: true,
			availabilityZonesSvc: func() *mock_azure.MockService {
				availabilityZonesSvc := mock_azure.NewMockService(mockCtrl)
				availabilityZonesSvc.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, nil).Times(1)
				return availabilityZonesSvc
			},
			availabilitySetsSvc: func() *mock_azure.MockService {
				return nil
			},
		},
		{
			name:           "Return early when availability zones were found for the zone",
			expectedASName: "",
			expectedError:  false,
			availabilityZonesSvc: func() *mock_azure.MockService {
				availabilityZonesSvc := mock_azure.NewMockService(mockCtrl)
				availabilityZonesSvc.EXPECT().Get(gomock.Any(), gomock.Any()).Return([]string{"a", "b", "c"}, nil).Times(1)
				return availabilityZonesSvc
			},
			availabilitySetsSvc: func() *mock_azure.MockService {
				return nil
			},
		},
		{
			name:          "Error when availability sets client fails",
			expectedError: true,
			availabilityZonesSvc: func() *mock_azure.MockService {
				availabilityZonesSvc := mock_azure.NewMockService(mockCtrl)
				availabilityZonesSvc.EXPECT().Get(gomock.Any(), gomock.Any()).Return([]string{}, nil).Times(1)
				return availabilityZonesSvc
			},
			availabilitySetsSvc: func() *mock_azure.MockService {
				availabilitySetsSvc := mock_azure.NewMockService(mockCtrl)
				availabilitySetsSvc.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any()).Return(errors.New("test error")).Times(1)
				return availabilitySetsSvc
			},
		},
		{
			name:           "Succesfuly create an availability set",
			expectedASName: "cluster_ms-as",
			availabilityZonesSvc: func() *mock_azure.MockService {
				availabilityZonesSvc := mock_azure.NewMockService(mockCtrl)
				availabilityZonesSvc.EXPECT().Get(gomock.Any(), gomock.Any()).Return([]string{}, nil).Times(1)
				return availabilityZonesSvc
			},
			availabilitySetsSvc: func() *mock_azure.MockService {
				availabilitySetsSvc := mock_azure.NewMockService(mockCtrl)
				availabilitySetsSvc.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any()).Return(nil).Times(1)
				return availabilitySetsSvc
			},
		},
		{
			name:   "Skip availability set creation when MachineSet label name is missing",
			labels: map[string]string{},
			availabilityZonesSvc: func() *mock_azure.MockService {
				availabilityZonesSvc := mock_azure.NewMockService(mockCtrl)
				availabilityZonesSvc.EXPECT().Get(gomock.Any(), gomock.Any()).Return([]string{}, nil).Times(1)
				return availabilityZonesSvc
			},
			availabilitySetsSvc: func() *mock_azure.MockService {
				availabilitySetsSvc := mock_azure.NewMockService(mockCtrl)
				// Set call counter to 0 here
				availabilitySetsSvc.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any()).Return(nil).Times(0)
				return availabilitySetsSvc
			},
		},
		{
			name: "Skip availability set creation when using Spot instances",
			availabilityZonesSvc: func() *mock_azure.MockService {
				availabilityZonesSvc := mock_azure.NewMockService(mockCtrl)
				availabilityZonesSvc.EXPECT().Get(gomock.Any(), gomock.Any()).Return([]string{}, nil).Times(1)
				return availabilityZonesSvc
			},
			availabilitySetsSvc: func() *mock_azure.MockService {
				availabilitySetsSvc := mock_azure.NewMockService(mockCtrl)
				availabilitySetsSvc.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any()).Return(nil).Times(0)
				return availabilitySetsSvc
			},
			spotVMOptions: &machinev1.SpotVMOptions{
				MaxPrice: resource.NewQuantity(-1, resource.DecimalSI),
			},
		},
		{
			name:           "Skip availability set creation when name was specified in provider spec",
			labels:         map[string]string{},
			inputASName:    "test-as",
			expectedASName: "test-as",
			availabilityZonesSvc: func() *mock_azure.MockService {
				availabilityZonesSvc := mock_azure.NewMockService(mockCtrl)
				// Set call counter to 0 here
				availabilityZonesSvc.EXPECT().Get(gomock.Any(), gomock.Any()).Return([]string{}, nil).Times(0)
				return availabilityZonesSvc
			},
			availabilitySetsSvc: func() *mock_azure.MockService {
				availabilitySetsSvc := mock_azure.NewMockService(mockCtrl)
				// Set call counter to 0 here
				availabilitySetsSvc.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any()).Return(nil).Times(0)
				return availabilitySetsSvc
			},
		},
		{
			name:           "Availability set does not contain double cluster name when it is present in MachineSet name",
			labels:         map[string]string{MachineSetLabelName: "clustername-msname", machinev1.MachineClusterIDLabel: "clustername"},
			expectedASName: "clustername-msname-as",
			availabilityZonesSvc: func() *mock_azure.MockService {
				availabilityZonesSvc := mock_azure.NewMockService(mockCtrl)
				availabilityZonesSvc.EXPECT().Get(gomock.Any(), gomock.Any()).Return([]string{}, nil).Times(1)
				return availabilityZonesSvc
			},
			availabilitySetsSvc: func() *mock_azure.MockService {
				availabilitySetsSvc := mock_azure.NewMockService(mockCtrl)
				availabilitySetsSvc.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any()).Return(nil).Times(1)
				return availabilitySetsSvc
			},
		},
		{
			name: "Availability set name is truncated properly when over 80 characters",
			labels: map[string]string{
				MachineSetLabelName:             "clustername-msname-1234567890abcdefghijklmnopqrstuvwxyz-1234567890abcdefghijklm",
				machinev1.MachineClusterIDLabel: "clustername",
			},
			expectedASName: "clustername-msname-1234567890abcdefghijklmnopqrstuvwxyz-1234567890abcdefghijk-as",
			availabilityZonesSvc: func() *mock_azure.MockService {
				availabilityZonesSvc := mock_azure.NewMockService(mockCtrl)
				availabilityZonesSvc.EXPECT().Get(gomock.Any(), gomock.Any()).Return([]string{}, nil).Times(1)
				return availabilityZonesSvc
			},
			availabilitySetsSvc: func() *mock_azure.MockService {
				availabilitySetsSvc := mock_azure.NewMockService(mockCtrl)
				availabilitySetsSvc.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any()).Return(nil).Times(1)
				return availabilitySetsSvc
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			labels := map[string]string{MachineSetLabelName: "ms", machinev1.MachineClusterIDLabel: "cluster"}
			if tc.labels != nil {
				labels = tc.labels
			}

			r := Reconciler{
				availabilityZonesSvc: tc.availabilityZonesSvc(),
				availabilitySetsSvc:  tc.availabilitySetsSvc(),
				scope: &actuators.MachineScope{
					Machine: &machinev1.Machine{
						ObjectMeta: metav1.ObjectMeta{
							Labels: labels,
						},
					},
					MachineConfig: &machinev1.AzureMachineProviderSpec{
						VMSize:          "Standard_D2_v2",
						AvailabilitySet: tc.inputASName,
						SpotVMOptions:   tc.spotVMOptions,
					},
				},
			}

			asName, err := r.getOrCreateAvailabilitySet()
			if tc.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			g.Expect(asName).To(Equal(tc.expectedASName))
		})
	}
}

func TestCreateDiagnosticsConfig(t *testing.T) {
	testCases := []struct {
		name           string
		config         *machinev1.AzureMachineProviderSpec
		expectedConfig *compute.DiagnosticsProfile
		expectedError  error
	}{
		{
			name:           "with no boot configuration",
			config:         &machinev1.AzureMachineProviderSpec{},
			expectedConfig: nil,
			expectedError:  nil,
		},
		{
			name: "with no storage account type",
			config: &machinev1.AzureMachineProviderSpec{
				Diagnostics: machinev1.AzureDiagnostics{
					Boot: &machinev1.AzureBootDiagnostics{},
				},
			},
			expectedConfig: nil,
			expectedError:  machinecontroller.InvalidMachineConfiguration("unknown storage account type for boot diagnostics: \"\", supported types are AzureManaged & CustomerManaged"),
		},
		{
			name: "with an invalid storage account type",
			config: &machinev1.AzureMachineProviderSpec{
				Diagnostics: machinev1.AzureDiagnostics{
					Boot: &machinev1.AzureBootDiagnostics{
						StorageAccountType: machinev1.AzureBootDiagnosticsStorageAccountType("foo"),
					},
				},
			},
			expectedConfig: nil,
			expectedError:  machinecontroller.InvalidMachineConfiguration("unknown storage account type for boot diagnostics: \"foo\", supported types are AzureManaged & CustomerManaged"),
		},
		{
			name: "with an Azure managed storage account",
			config: &machinev1.AzureMachineProviderSpec{
				Diagnostics: machinev1.AzureDiagnostics{
					Boot: &machinev1.AzureBootDiagnostics{
						StorageAccountType: machinev1.AzureManagedAzureDiagnosticsStorage,
					},
				},
			},
			expectedConfig: &compute.DiagnosticsProfile{
				BootDiagnostics: &compute.BootDiagnostics{
					Enabled: ptr.To[bool](true),
				},
			},
			expectedError: nil,
		},
		{
			name: "with an Customer managed storage account with no account URI",
			config: &machinev1.AzureMachineProviderSpec{
				Diagnostics: machinev1.AzureDiagnostics{
					Boot: &machinev1.AzureBootDiagnostics{
						StorageAccountType: machinev1.CustomerManagedAzureDiagnosticsStorage,
					},
				},
			},
			expectedConfig: nil,
			expectedError:  machinecontroller.InvalidMachineConfiguration("missing configuration for customer managed storage account URI"),
		},
		{
			name: "with an Customer managed storage account with a valid account URI",
			config: &machinev1.AzureMachineProviderSpec{
				Diagnostics: machinev1.AzureDiagnostics{
					Boot: &machinev1.AzureBootDiagnostics{
						StorageAccountType: machinev1.CustomerManagedAzureDiagnosticsStorage,
						CustomerManaged: &machinev1.AzureCustomerManagedBootDiagnostics{
							StorageAccountURI: "https://myaccount.blob.windows.net/",
						},
					},
				},
			},
			expectedConfig: &compute.DiagnosticsProfile{
				BootDiagnostics: &compute.BootDiagnostics{
					Enabled:    ptr.To[bool](true),
					StorageURI: ptr.To[string]("https://myaccount.blob.windows.net/"),
				},
			},
			expectedError: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			config, err := createDiagnosticsConfig(tc.config)
			if tc.expectedError != nil {
				g.Expect(err).To(MatchError(tc.expectedError))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			g.Expect(config).To(Equal(tc.expectedConfig))
		})
	}
}

func TestValidateCapacityReservationGroupID(t *testing.T) {
	testCases := []struct {
		name          string
		inputID       string
		expectedError error
	}{
		{
			name:          "validation for capacityReservationGroupID should return nil error if field input is valid",
			inputID:       "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myResourceGroupName/providers/Microsoft.Compute/capacityReservationGroups/myCapacityReservationGroup",
			expectedError: nil,
		},
		{
			name:          "validation for capacityReservationGroupID should return error if field input does not start with '/'",
			inputID:       "subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myResourceGroupName/providers/Microsoft.Compute/capacityReservationGroups/myCapacityReservationGroup",
			expectedError: errors.New("invalid resource ID: resource id 'subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/myResourceGroupName/providers/Microsoft.Compute/capacityReservationGroups/myCapacityReservationGroup' must start with '/'"),
		},
		{
			name:          "validation for capacityReservationGroupID should return error if field input does not have field name subscriptions",
			inputID:       "/subscripti/00000000-0000-0000-0000-000000000000/resourceGroups/myResourceGroupName/providers/Microsoft.Compute/capacityReservationGroups/myCapacityReservationGroup",
			expectedError: errors.New("invalid resource ID: /subscripti/00000000-0000-0000-0000-000000000000/resourceGroups/myResourceGroupName/providers/Microsoft.Compute/capacityReservationGroups/myCapacityReservationGroup"),
		},
		{
			name:          "validation for capacityReservationGroupID should return error if field input is empty",
			inputID:       "",
			expectedError: errors.New("invalid resource ID: id cannot be empty"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			err := validateAzureCapacityReservationGroupID(tc.inputID)
			if tc.expectedError != nil {
				g.Expect(err).To(MatchError(tc.expectedError))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}
