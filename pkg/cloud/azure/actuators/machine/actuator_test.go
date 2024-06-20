/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package machine

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2021-11-01/compute"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/ghodss/yaml"
	"github.com/golang/mock/gomock"
	configv1 "github.com/openshift/api/config/v1"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/machine-api-operator/pkg/controller/machine"
	machineapierrors "github.com/openshift/machine-api-operator/pkg/controller/machine"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/actuators"
	mock_azure "github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/mock"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/virtualmachines"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	controllerfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var (
	_ machine.Actuator = (*Actuator)(nil)
)

const globalInfrastuctureName = "cluster"

func init() {
	if err := machinev1.AddToScheme(scheme.Scheme); err != nil {
		klog.Fatal(err)
	}

	configv1.AddToScheme(scheme.Scheme)
}

func providerSpecFromMachine(in *machinev1.AzureMachineProviderSpec) (*machinev1.ProviderSpec, error) {
	bytes, err := yaml.Marshal(in)
	if err != nil {
		return nil, err
	}
	return &machinev1.ProviderSpec{
		Value: &runtime.RawExtension{Raw: bytes},
	}, nil
}

func newMachine(t *testing.T, machineConfig machinev1.AzureMachineProviderSpec, labels map[string]string) *machinev1.Machine {
	providerSpec, err := providerSpecFromMachine(&machineConfig)
	if err != nil {
		t.Fatalf("error encoding provider config: %v", err)
	}
	return &machinev1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "machine-test",
			Labels: labels,
		},
		Spec: machinev1.MachineSpec{
			ProviderSpec: *providerSpec,
		},
	}
}

func newFakeScope(t *testing.T, label string) *actuators.MachineScope {
	labels := make(map[string]string)
	labels[actuators.MachineRoleLabel] = label
	labels[machinev1.MachineClusterIDLabel] = "clusterID"
	machineConfig := machinev1.AzureMachineProviderSpec{}
	m := newMachine(t, machineConfig, labels)
	return &actuators.MachineScope{
		Context: context.Background(),
		Machine: m,
		MachineConfig: &machinev1.AzureMachineProviderSpec{
			ResourceGroup:   "dummyResourceGroup",
			Location:        "dummyLocation",
			Subnet:          "dummySubnet",
			Vnet:            "dummyVnet",
			ManagedIdentity: "dummyIdentity",
		},
		MachineStatus: &machinev1.AzureMachineProviderStatus{},
	}
}

func newFakeReconciler(t *testing.T) *Reconciler {
	fakeSuccessSvc := &azure.FakeSuccessService{}
	fakeVMSuccessSvc := &FakeVMService{
		Name:              "machine-test",
		ID:                "machine-test-ID",
		ProvisioningState: "Succeeded",
	}
	mockCtrl := gomock.NewController(t)
	availabilityZonesSvc := mock_azure.NewMockService(mockCtrl)
	availabilityZonesSvc.EXPECT().Get(gomock.Any(), gomock.Any()).Return([]string{"testzone"}, nil).AnyTimes()
	availabilitySetsSvc := mock_azure.NewMockService(mockCtrl)
	availabilitySetsSvc.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

	return &Reconciler{
		scope:                 newFakeScope(t, actuators.ControlPlane),
		networkInterfacesSvc:  fakeSuccessSvc,
		virtualMachinesSvc:    fakeVMSuccessSvc,
		virtualMachinesExtSvc: fakeSuccessSvc,
		disksSvc:              fakeSuccessSvc,
		publicIPSvc:           fakeSuccessSvc,
		availabilityZonesSvc:  availabilityZonesSvc,
		availabilitySetsSvc:   availabilitySetsSvc,
	}
}

func newFakeReconcilerWithScope(t *testing.T, scope *actuators.MachineScope) *Reconciler {
	fakeSuccessSvc := &azure.FakeSuccessService{}
	fakeVMSuccessSvc := &FakeVMService{
		Name:              "machine-test",
		ID:                "machine-test-ID",
		ProvisioningState: "Succeeded",
	}
	mockCtrl := gomock.NewController(t)
	availabilityZonesSvc := mock_azure.NewMockService(mockCtrl)
	availabilityZonesSvc.EXPECT().Get(gomock.Any(), gomock.Any()).Return([]string{"testzone"}, nil).AnyTimes()
	return &Reconciler{
		scope:                 scope,
		availabilityZonesSvc:  availabilityZonesSvc,
		networkInterfacesSvc:  fakeSuccessSvc,
		virtualMachinesSvc:    fakeVMSuccessSvc,
		virtualMachinesExtSvc: fakeSuccessSvc,
	}
}

// FakeVMService generic vm service
type FakeVMService struct {
	Name                    string
	ID                      string
	ProvisioningState       string
	GetCallCount            int
	CreateOrUpdateCallCount int
	DeleteCallCount         int
}

// Get returns fake success.
func (s *FakeVMService) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	s.GetCallCount++
	return compute.VirtualMachine{
		ID:   to.StringPtr(s.ID),
		Name: to.StringPtr(s.Name),
		VirtualMachineProperties: &compute.VirtualMachineProperties{
			ProvisioningState: to.StringPtr(s.ProvisioningState),
		},
	}, nil
}

// CreateOrUpdate returns fake success.
func (s *FakeVMService) CreateOrUpdate(ctx context.Context, spec azure.Spec) error {
	s.CreateOrUpdateCallCount++
	return nil
}

// Delete returns fake success.
func (s *FakeVMService) Delete(ctx context.Context, spec azure.Spec) error {
	s.DeleteCallCount++
	return nil
}

type FakeBrokenVmService struct {
	FakeVMService
	ErrorToReturn error
}

// Get returns fake error.
func (s *FakeBrokenVmService) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	s.GetCallCount++
	return nil, s.ErrorToReturn
}

// FakeVMService generic vm service
type FakeCountService struct {
	GetCallCount            int
	CreateOrUpdateCallCount int
	DeleteCallCount         int
}

// Get returns fake success.
func (s *FakeCountService) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	s.GetCallCount++
	return nil, nil
}

// CreateOrUpdate returns fake success.
func (s *FakeCountService) CreateOrUpdate(ctx context.Context, spec azure.Spec) error {
	s.CreateOrUpdateCallCount++
	return nil
}

// Delete returns fake success.
func (s *FakeCountService) Delete(ctx context.Context, spec azure.Spec) error {
	s.DeleteCallCount++
	return nil
}

func TestReconcilerSuccess(t *testing.T) {
	fakeReconciler := newFakeReconciler(t)

	if err := fakeReconciler.Create(context.Background()); err != nil {
		t.Errorf("failed to create machine: %+v", err)
	}

	if err := fakeReconciler.Update(context.Background()); err != nil {
		t.Errorf("failed to update machine: %+v", err)
	}

	if _, err := fakeReconciler.Exists(context.Background()); err != nil {
		t.Errorf("failed to check if machine exists: %+v", err)
	}

	if err := fakeReconciler.Delete(context.Background()); err != nil {
		t.Errorf("failed to delete machine: %+v", err)
	}
}

func TestReconcileFailure(t *testing.T) {
	fakeFailureSvc := &azure.FakeFailureService{}
	fakeReconciler := newFakeReconciler(t)
	fakeReconciler.networkInterfacesSvc = fakeFailureSvc
	fakeReconciler.virtualMachinesSvc = fakeFailureSvc
	fakeReconciler.virtualMachinesExtSvc = fakeFailureSvc

	if err := fakeReconciler.Create(context.Background()); err == nil {
		t.Errorf("expected create to fail")
	}

	if err := fakeReconciler.Update(context.Background()); err == nil {
		t.Errorf("expected update to fail")
	}

	if _, err := fakeReconciler.Exists(context.Background()); err == nil {
		t.Errorf("expected exists to fail")
	}

	if err := fakeReconciler.Delete(context.Background()); err == nil {
		t.Errorf("expected delete to fail")
	}
}

func TestReconcileVMFailedState(t *testing.T) {
	fakeReconciler := newFakeReconciler(t)
	fakeVMService := &FakeVMService{
		Name:              "machine-test",
		ID:                "machine-test-ID",
		ProvisioningState: "Failed",
	}
	fakeReconciler.virtualMachinesSvc = fakeVMService
	fakeDiskService := &FakeCountService{}
	fakeReconciler.disksSvc = fakeDiskService
	fakeNicService := &FakeCountService{}
	fakeReconciler.networkInterfacesSvc = fakeNicService

	if err := fakeReconciler.Create(context.Background()); err == nil {
		t.Errorf("expected create to fail")
	}

	if fakeVMService.GetCallCount != 1 {
		t.Errorf("expected get to be called just once")
	}

	if fakeVMService.DeleteCallCount != 1 {
		t.Errorf("expected delete to be called just once")
	}

	if fakeDiskService.DeleteCallCount != 1 {
		t.Errorf("expected disk delete to be called just once")
	}

	if fakeNicService.DeleteCallCount != 1 {
		t.Errorf("expected nic delete to be called just once")
	}

	if fakeVMService.CreateOrUpdateCallCount != 0 {
		t.Errorf("expected createorupdate not to be called")
	}
}

func TestReconcileVMUpdatingState(t *testing.T) {
	fakeReconciler := newFakeReconciler(t)
	fakeVMService := &FakeVMService{
		Name:              "machine-test",
		ID:                "machine-test-ID",
		ProvisioningState: "Updating",
	}
	fakeReconciler.virtualMachinesSvc = fakeVMService

	if err := fakeReconciler.Create(context.Background()); err == nil {
		t.Errorf("expected create to fail")
	}

	if fakeVMService.GetCallCount != 1 {
		t.Errorf("expected get to be called just once")
	}

	if fakeVMService.DeleteCallCount != 0 {
		t.Errorf("expected delete not to be called")
	}

	if fakeVMService.CreateOrUpdateCallCount != 0 {
		t.Errorf("expected createorupdate not to be called")
	}
}

func TestReconcileVMSuceededState(t *testing.T) {
	fakeReconciler := newFakeReconciler(t)
	fakeVMService := &FakeVMService{
		Name:              "machine-test",
		ID:                "machine-test-ID",
		ProvisioningState: "Succeeded",
	}
	fakeReconciler.virtualMachinesSvc = fakeVMService

	if err := fakeReconciler.Create(context.Background()); err != nil {
		t.Errorf("failed to create machine: %+v", err)
	}

	if fakeVMService.GetCallCount != 2 {
		// One call for create, and one for the update that happens when the create is successful.
		t.Errorf("expected get to be called exactly twice")
	}

	if fakeVMService.DeleteCallCount != 0 {
		t.Errorf("expected delete not to be called")
	}

	if fakeVMService.CreateOrUpdateCallCount != 0 {
		t.Errorf("expected createorupdate not to be called")
	}
}

// FakeVMCheckZonesService generic fake vm zone service
type FakeVMCheckZonesService struct {
	checkZones []string
	created    bool
}

// Get returns fake success.
func (s *FakeVMCheckZonesService) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	if !s.created {
		return nil, errors.New("vm not found")
	}

	return &compute.VirtualMachine{
		VirtualMachineProperties: &compute.VirtualMachineProperties{},
	}, nil
}

// CreateOrUpdate returns fake success.
func (s *FakeVMCheckZonesService) CreateOrUpdate(ctx context.Context, spec azure.Spec) error {
	vmSpec, ok := spec.(*virtualmachines.Spec)
	if !ok {
		return errors.New("invalid vm specification")
	}

	if len(s.checkZones) <= 0 {
		s.created = true
		return nil
	}
	for _, zone := range s.checkZones {
		if strings.EqualFold(zone, vmSpec.Zone) {
			s.created = true
			return nil
		}
	}

	return errors.New("invalid input zone")
}

// Delete returns fake success.
func (s *FakeVMCheckZonesService) Delete(ctx context.Context, spec azure.Spec) error {
	return nil
}

// FakeAvailabilityZonesService generic fake availability zones
type FakeAvailabilityZonesService struct {
	zonesResponse []string
}

// Get returns fake success.
func (s *FakeAvailabilityZonesService) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	return s.zonesResponse, nil
}

// CreateOrUpdate returns fake success.
func (s *FakeAvailabilityZonesService) CreateOrUpdate(ctx context.Context, spec azure.Spec) error {
	return nil
}

// Delete returns fake success.
func (s *FakeAvailabilityZonesService) Delete(ctx context.Context, spec azure.Spec) error {
	return nil
}

func TestAvailabilityZones(t *testing.T) {
	fakeScope := newFakeScope(t, actuators.ControlPlane)
	fakeReconciler := newFakeReconcilerWithScope(t, fakeScope)

	fakeReconciler.scope.MachineConfig.Zone = "2"
	fakeReconciler.virtualMachinesSvc = &FakeVMCheckZonesService{
		checkZones: []string{"2"},
	}
	if err := fakeReconciler.Create(context.Background()); err != nil {
		t.Errorf("failed to create machine: %+v", err)
	}

	fakeReconciler.scope.MachineConfig.Zone = ""
	fakeReconciler.virtualMachinesSvc = &FakeVMCheckZonesService{
		checkZones: []string{""},
	}
	if err := fakeReconciler.Create(context.Background()); err != nil {
		t.Errorf("failed to create machine: %+v", err)
	}

	fakeReconciler.scope.MachineConfig.Zone = "1"
	fakeReconciler.virtualMachinesSvc = &FakeVMCheckZonesService{
		checkZones: []string{"3"},
	}
	if err := fakeReconciler.Create(context.Background()); err == nil {
		t.Errorf("expected create to fail due to zone mismatch")
	}
}

func TestGetZone(t *testing.T) {
	testCases := []struct {
		inputZone string
		expected  string
	}{
		{
			inputZone: "",
			expected:  "",
		},
		{
			inputZone: "3",
			expected:  "3",
		},
	}

	for _, tc := range testCases {
		fakeScope := newFakeScope(t, actuators.ControlPlane)
		fakeReconciler := newFakeReconcilerWithScope(t, fakeScope)
		fakeReconciler.scope.MachineConfig.Zone = tc.inputZone

		zones := []string{"1", "2", "3"}
		fakeReconciler.availabilityZonesSvc = &FakeAvailabilityZonesService{
			zonesResponse: zones,
		}

		got, err := fakeReconciler.getZone(context.Background())
		if err != nil {
			t.Fatalf("unexpected error getting zone")
		}

		if !strings.EqualFold(tc.expected, got) {
			t.Errorf("expected: %v, got: %v", tc.expected, got)
		}
	}
}

func TestCustomUserData(t *testing.T) {
	fakeScope := newFakeScope(t, actuators.Node)
	userDataSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testCustomUserData",
			Namespace: fakeScope.Namespace(),
		},
		Data: map[string][]byte{
			"userData": []byte("test-userdata"),
		},
	}
	fakeScope.CoreClient = controllerfake.NewClientBuilder().WithObjects(userDataSecret).Build()
	fakeScope.MachineConfig.UserDataSecret = &corev1.SecretReference{Name: userDataSecret.Name, Namespace: fakeScope.Namespace()}
	fakeReconciler := newFakeReconcilerWithScope(t, fakeScope)
	fakeReconciler.virtualMachinesSvc = &FakeVMCheckZonesService{}
	if err := fakeReconciler.Create(context.Background()); err != nil {
		t.Errorf("expected create to succeed %v", err)
	}

	userData, err := fakeReconciler.getCustomUserData()
	if err != nil {
		t.Errorf("expected get custom data to succeed %v", err)
	}

	if userData != base64.StdEncoding.EncodeToString([]byte("test-userdata")) {
		t.Errorf("expected userdata to be test-userdata, but found %s", userData)
	}
}

func TestCustomDataFailures(t *testing.T) {
	fakeScope := newFakeScope(t, actuators.Node)
	userDataSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testCustomUserData",
			Namespace: "dummyNamespace",
		},
		Data: map[string][]byte{
			"userData": []byte("test-userdata"),
		},
	}
	fakeScope.CoreClient = controllerfake.NewClientBuilder().WithObjects(userDataSecret).Build()
	fakeScope.MachineConfig.UserDataSecret = &corev1.SecretReference{Name: "testCustomUserData"}
	fakeReconciler := newFakeReconcilerWithScope(t, fakeScope)
	fakeReconciler.virtualMachinesSvc = &FakeVMCheckZonesService{}

	fakeScope.MachineConfig.UserDataSecret = &corev1.SecretReference{Name: "testFailure"}
	if err := fakeReconciler.Create(context.Background()); err == nil {
		t.Errorf("expected create to fail")
	}

	if _, err := fakeReconciler.getCustomUserData(); err == nil {
		t.Errorf("expected get custom data to fail")
	}

	userDataSecret.Data = map[string][]byte{
		"notUserData": []byte("test-notuserdata"),
	}
	fakeScope.CoreClient = controllerfake.NewClientBuilder().WithObjects(userDataSecret).Build()
	if _, err := fakeReconciler.getCustomUserData(); err == nil {
		t.Errorf("expected get custom data to fail, due to missing userdata")
	}
}

func TestMachineEvents(t *testing.T) {
	infra := &configv1.Infrastructure{
		ObjectMeta: metav1.ObjectMeta{
			Name: globalInfrastuctureName,
		},
		Status: configv1.InfrastructureStatus{
			InfrastructureName: "test-ghfd",
			PlatformStatus: &configv1.PlatformStatus{
				Azure: &configv1.AzurePlatformStatus{
					CloudName: configv1.AzurePublicCloud,
				},
			},
		},
	}

	machine, err := stubMachine()
	if err != nil {
		t.Fatal(err)
	}

	machinePc := stubProviderConfig()
	machinePc.Subnet = ""
	machinePc.Vnet = ""
	machinePc.VMSize = ""
	providerSpec, err := actuators.RawExtensionFromProviderSpec(machinePc)
	if err != nil {
		t.Fatalf("EncodeMachineSpec failed: %v", err)
	}

	invalidMachine := machine.DeepCopy()
	invalidMachine.Spec.ProviderSpec = machinev1.ProviderSpec{Value: providerSpec}

	azureCredentialsSecret := StubAzureCredentialsSecret()
	invalidAzureCredentialsSecret := StubAzureCredentialsSecret()
	delete(invalidAzureCredentialsSecret.Data, "azure_client_id")

	cases := []struct {
		name       string
		machine    *machinev1.Machine
		credSecret *corev1.Secret
		error      string
		operation  func(actuator *Actuator, machine *machinev1.Machine)
		event      string
	}{
		{
			name:       "Create machine event failed (scope)",
			machine:    machine,
			credSecret: invalidAzureCredentialsSecret,
			operation: func(actuator *Actuator, machine *machinev1.Machine) {
				actuator.Create(context.TODO(), machine)
			},
			event: "Warning FailedCreate InvalidConfiguration: failed to create machine \"azure-actuator-testing-machine\" scope: failed to update cluster: azure client id not found in secret default/azure-credentials-secret (azure_client_id) or environment variable AZURE_CLIENT_ID",
		},
		{
			name:       "Create machine event failed (reconciler)",
			machine:    invalidMachine,
			credSecret: azureCredentialsSecret,
			operation: func(actuator *Actuator, machine *machinev1.Machine) {
				actuator.Create(context.TODO(), machine)
			},
			event: "Warning FailedCreate InvalidConfiguration: failed to reconcile machine \"azure-actuator-testing-machine\": failed to create nic azure-actuator-testing-machine-nic for machine azure-actuator-testing-machine: MachineConfig vnet is missing on machine azure-actuator-testing-machine",
		},
		{
			name:       "Create machine event succeed",
			machine:    machine,
			credSecret: azureCredentialsSecret,
			operation: func(actuator *Actuator, machine *machinev1.Machine) {
				actuator.Create(context.TODO(), machine)
			},
			event: fmt.Sprintf("Normal Created Created machine %q", machine.Name),
		},
		{
			name:       "Update machine event failed (scope)",
			machine:    machine,
			credSecret: invalidAzureCredentialsSecret,
			operation: func(actuator *Actuator, machine *machinev1.Machine) {
				actuator.Update(context.TODO(), machine)
			},
			event: "Warning FailedUpdate UpdateError: failed to create machine \"azure-actuator-testing-machine\" scope: failed to update cluster: azure client id not found in secret default/azure-credentials-secret (azure_client_id) or environment variable AZURE_CLIENT_ID",
		},
		{
			name:       "Update machine event succeed",
			machine:    machine,
			credSecret: azureCredentialsSecret,
			operation: func(actuator *Actuator, machine *machinev1.Machine) {
				actuator.Update(context.TODO(), machine)
			},
			event: fmt.Sprintf("Normal Updated Updated machine %q", machine.Name),
		},
		{
			name:       "Delete machine event failed (scope)",
			machine:    machine,
			credSecret: invalidAzureCredentialsSecret,
			operation: func(actuator *Actuator, machine *machinev1.Machine) {
				actuator.Delete(context.TODO(), machine)
			},
			event: "Warning FailedDelete DeleteError: failed to create machine \"azure-actuator-testing-machine\" scope: failed to update cluster: azure client id not found in secret default/azure-credentials-secret (azure_client_id) or environment variable AZURE_CLIENT_ID",
		},
		{
			name:       "Delete machine event failed (reconciler)",
			machine:    invalidMachine,
			credSecret: azureCredentialsSecret,
			operation: func(actuator *Actuator, machine *machinev1.Machine) {
				actuator.Delete(context.TODO(), machine)
			},
			event: "Warning FailedDelete DeleteError: failed to delete machine \"azure-actuator-testing-machine\": MachineConfig vnet is missing on machine azure-actuator-testing-machine",
		},
		{
			name:       "Delete machine event succeed",
			machine:    machine,
			credSecret: azureCredentialsSecret,
			operation: func(actuator *Actuator, machine *machinev1.Machine) {
				actuator.Delete(context.TODO(), machine)
			},
			event: fmt.Sprintf("Normal Deleted Deleted machine %q", machine.Name),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := controllerfake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.credSecret, infra).WithStatusSubresource(&machinev1.Machine{}).Build()

			m := tc.machine.DeepCopy()
			if err := cs.Create(context.TODO(), m); err != nil {
				t.Fatal(err)
			}

			mockCtrl := gomock.NewController(t)
			networkSvc := mock_azure.NewMockService(mockCtrl)
			vmSvc := mock_azure.NewMockService(mockCtrl)
			vmExtSvc := mock_azure.NewMockService(mockCtrl)
			pipSvc := mock_azure.NewMockService(mockCtrl)
			disksSvc := mock_azure.NewMockService(mockCtrl)
			availabilityZonesSvc := mock_azure.NewMockService(mockCtrl)
			availabilitySetsSvc := mock_azure.NewMockService(mockCtrl)

			eventsChannel := make(chan string, 1)

			machineActuator := NewActuator(ActuatorParams{
				CoreClient: cs,
				ReconcilerBuilder: func(scope *actuators.MachineScope) *Reconciler {
					return &Reconciler{
						scope:                 scope,
						availabilityZonesSvc:  availabilityZonesSvc,
						availabilitySetsSvc:   availabilitySetsSvc,
						networkInterfacesSvc:  networkSvc,
						virtualMachinesSvc:    vmSvc,
						virtualMachinesExtSvc: vmExtSvc,
						publicIPSvc:           pipSvc,
						disksSvc:              disksSvc,
					}
				},
				// use fake recorder and store an event into one item long buffer for subsequent check
				EventRecorder: &record.FakeRecorder{
					Events: eventsChannel,
				},
			})

			networkSvc.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			vmSvc.EXPECT().Get(gomock.Any(), gomock.Any()).Return(compute.VirtualMachine{
				ID:   pointer.StringPtr("vm-id"),
				Name: pointer.StringPtr("vm-name"),
				VirtualMachineProperties: &compute.VirtualMachineProperties{
					ProvisioningState: pointer.StringPtr("Succeeded"),
					HardwareProfile: &compute.HardwareProfile{
						VMSize: compute.VirtualMachineSizeTypesStandardB2ms,
					},
					OsProfile: &compute.OSProfile{
						ComputerName: pointer.StringPtr("vm-name"),
					},
				},
			}, nil).AnyTimes()
			vmSvc.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			disksSvc.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			networkSvc.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			pipSvc.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
			availabilityZonesSvc.EXPECT().Get(gomock.Any(), gomock.Any()).Return([]string{"testzone"}, nil).AnyTimes()
			availabilitySetsSvc.EXPECT().Delete(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()

			tc.operation(machineActuator, m)

			select {
			case event := <-eventsChannel:
				if event != tc.event {
					t.Errorf("Expected %q event, got %q", tc.event, event)
				}
			default:
				t.Errorf("Expected %q event, got none", tc.event)
			}
		})
	}
}

func TestStatusCodeBasedCreationErrors(t *testing.T) {
	infra := &configv1.Infrastructure{
		ObjectMeta: metav1.ObjectMeta{
			Name: globalInfrastuctureName,
		},
		Status: configv1.InfrastructureStatus{
			InfrastructureName: "test-yuhg",
			PlatformStatus: &configv1.PlatformStatus{
				Azure: &configv1.AzurePlatformStatus{
					CloudName: configv1.AzurePublicCloud,
				},
			},
		},
	}

	machine, err := stubMachine()
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name       string
		operation  func(actuator *Actuator, machine *machinev1.Machine)
		event      string
		statusCode interface{}
		requeable  bool
	}{
		{
			name:       "InvalidConfig",
			event:      "Warning FailedCreate InvalidConfiguration: failed to reconcile machine \"azure-actuator-testing-machine\": compute.VirtualMachinesClient#CreateOrUpdate: MOCK: StatusCode=400",
			statusCode: 400,
			requeable:  false,
		},
		{
			name:       "CreateMachine",
			event:      "Warning FailedCreate CreateError: failed to reconcile machine \"azure-actuator-testing-machine\"s: failed to create vm azure-actuator-testing-machine: failed to create VM: failed to create or get machine: compute.VirtualMachinesClient#CreateOrUpdate: MOCK: StatusCode=300",
			statusCode: 300,
			requeable:  true,
		},
		{
			name:       "CreateMachine",
			event:      "Warning FailedCreate CreateError: failed to reconcile machine \"azure-actuator-testing-machine\"s: failed to create vm azure-actuator-testing-machine: failed to create VM: failed to create or get machine: compute.VirtualMachinesClient#CreateOrUpdate: MOCK: StatusCode=401",
			statusCode: 401,
			requeable:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := controllerfake.NewClientBuilder().WithObjects(
				StubAzureCredentialsSecret(), &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "azure-actuator-user-data-secret",
						Namespace: "default",
					},
					Data: map[string][]byte{
						"userData": []byte("S3CR3T"),
					},
				},
				infra).Build()

			mockCtrl := gomock.NewController(t)
			networkSvc := mock_azure.NewMockService(mockCtrl)
			vmSvc := mock_azure.NewMockService(mockCtrl)
			availabilityZonesSvc := mock_azure.NewMockService(mockCtrl)

			eventsChannel := make(chan string, 1)

			machineActuator := NewActuator(ActuatorParams{
				CoreClient: cs,
				ReconcilerBuilder: func(scope *actuators.MachineScope) *Reconciler {
					return &Reconciler{
						scope:                scope,
						networkInterfacesSvc: networkSvc,
						virtualMachinesSvc:   vmSvc,
						availabilityZonesSvc: availabilityZonesSvc,
					}
				},
				// use fake recorder and store an event into one item long buffer for subsequent check
				EventRecorder: &record.FakeRecorder{
					Events: eventsChannel,
				},
			})

			networkSvc.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any()).Return(nil).Times(1)
			azureErr := autorest.NewError("compute.VirtualMachinesClient", "CreateOrUpdate", "MOCK")
			azureErr.StatusCode = tc.statusCode
			wrapErr := fmt.Errorf("failed to create or get machine: %w", azureErr)
			vmSvc.EXPECT().CreateOrUpdate(gomock.Any(), gomock.Any()).Return(wrapErr).Times(1)
			vmSvc.EXPECT().Get(gomock.Any(), gomock.Any()).Return(nil, autorest.NewError("compute.VirtualMachinesClient", "Get", "MOCK")).Times(1)
			availabilityZonesSvc.EXPECT().Get(gomock.Any(), gomock.Any()).Return([]string{"testzone"}, nil).Times(1)

			_, ok := machineActuator.Create(context.TODO(), machine).(*machineapierrors.RequeueAfterError)
			if ok && !tc.requeable {
				t.Error("Error is not requeable but was requeued")
			}
			if !ok && tc.requeable {
				t.Error("Error is requeable but was not requeued")
			}

			select {
			case event := <-eventsChannel:
				if event != tc.event {
					t.Errorf("Expected %q event, got %q", tc.event, event)
				}
			default:
				t.Errorf("Expected %q event, got none", tc.event)
			}
		})
	}
}

func TestInvalidConfigurationCreationErrors(t *testing.T) {
	infra := &configv1.Infrastructure{
		ObjectMeta: metav1.ObjectMeta{
			Name: globalInfrastuctureName,
		},
		Status: configv1.InfrastructureStatus{
			InfrastructureName: "test-tgfj",
			PlatformStatus: &configv1.PlatformStatus{
				Azure: &configv1.AzurePlatformStatus{
					CloudName: configv1.AzurePublicCloud,
				},
			},
		},
	}

	cases := []struct {
		name          string
		mutateMachine func(*machinev1.Machine)
		mutatePC      func(*machinev1.AzureMachineProviderSpec)
		expectedErr   error
	}{
		{
			name: "Machine Name too long with PublicIP",
			mutateMachine: func(m *machinev1.Machine) {
				m.Name = "MachineNameOverSixtyChars" + "abcdefghijklmnopqrstuvwxyz" + "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
			},
			mutatePC: func(s *machinev1.AzureMachineProviderSpec) {
				s.PublicIP = true
			},
			expectedErr: machineapierrors.InvalidMachineConfiguration("failed to reconcile machine \"MachineNameOverSixtyCharsabcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ\": failed to create nic MachineNameOverSixtyCharsabcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-nic for machine MachineNameOverSixtyCharsabcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ: unable to create Public IP: machine public IP name is longer than 63 characters"),
		},
		{
			name: "Machine Config missing vnet",
			mutatePC: func(s *machinev1.AzureMachineProviderSpec) {
				s.Vnet = ""
			},
			expectedErr: machineapierrors.InvalidMachineConfiguration("failed to reconcile machine \"azure-actuator-testing-machine\": failed to create nic azure-actuator-testing-machine-nic for machine azure-actuator-testing-machine: MachineConfig vnet is missing on machine azure-actuator-testing-machine"),
		},
		{
			name: "Machine Config missing subnet",
			mutatePC: func(s *machinev1.AzureMachineProviderSpec) {
				s.Subnet = ""
			},
			expectedErr: machineapierrors.InvalidMachineConfiguration("failed to reconcile machine \"azure-actuator-testing-machine\": failed to create nic azure-actuator-testing-machine-nic for machine azure-actuator-testing-machine: MachineConfig subnet is missing on machine azure-actuator-testing-machine, skipping machine creation"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := controllerfake.NewClientBuilder().WithObjects(StubAzureCredentialsSecret(), &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "azure-actuator-user-data-secret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"userData": []byte("S3CR3T"),
				},
			},
				infra).Build()

			mockCtrl := gomock.NewController(t)
			networkSvc := mock_azure.NewMockService(mockCtrl)
			vmSvc := mock_azure.NewMockService(mockCtrl)

			eventsChannel := make(chan string, 1)

			machineActuator := NewActuator(ActuatorParams{
				CoreClient: cs,
				ReconcilerBuilder: func(scope *actuators.MachineScope) *Reconciler {
					return &Reconciler{
						scope:                scope,
						networkInterfacesSvc: networkSvc,
						virtualMachinesSvc:   vmSvc,
					}
				},
				// use fake recorder and store an event into one item long buffer for subsequent check
				EventRecorder: &record.FakeRecorder{
					Events: eventsChannel,
				},
			})

			machine, err := stubMachine()
			if err != nil {
				t.Fatal(err)
			}

			if tc.mutateMachine != nil {
				tc.mutateMachine(machine)
			}

			if tc.mutatePC != nil {
				machinePC := stubProviderConfig()
				tc.mutatePC(machinePC)

				providerSpec, err := actuators.RawExtensionFromProviderSpec(machinePC)
				if err != nil {
					t.Fatalf("unexpected error encoding providerspec: %v", err)
				}
				machine.Spec.ProviderSpec.Value = providerSpec
			}

			err = machineActuator.Create(context.TODO(), machine)
			if !reflect.DeepEqual(err, tc.expectedErr) {
				t.Errorf("Expected: %#v, Got: %#v", tc.expectedErr, err)
			}
		})
	}
}
