/*
Copyright 2019 The Kubernetes Authors.

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

package actuators

import (
	"encoding/json"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func init() {
	if err := machinev1.AddToScheme(scheme.Scheme); err != nil {
		klog.Fatal(err)
	}

	configv1.AddToScheme(scheme.Scheme)
}

func providerSpecFromMachine(in *machinev1.AzureMachineProviderSpec) (*machinev1.ProviderSpec, error) {
	bytes, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	return &machinev1.ProviderSpec{
		Value: &runtime.RawExtension{Raw: bytes},
	}, nil
}

func newMachine(t *testing.T) *machinev1.Machine {
	machineConfig := machinev1.AzureMachineProviderSpec{}
	providerSpec, err := providerSpecFromMachine(&machineConfig)
	if err != nil {
		t.Fatalf("error encoding provider config: %v", err)
	}
	return &machinev1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name: "machine-test",
		},
		Spec: machinev1.MachineSpec{
			ProviderSpec: *providerSpec,
		},
	}
}

func TestCredentialsSecretSuccess(t *testing.T) {
	credentialsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testCredentials",
			Namespace: "dummyNamespace",
		},
		Data: map[string][]byte{
			AzureCredsSubscriptionIDKey: []byte("dummySubID"),
			AzureCredsClientIDKey:       []byte("dummyClientID"),
			AzureCredsClientSecretKey:   []byte("dummyClientSecret"),
			AzureCredsTenantIDKey:       []byte("dummyTenantID"),
			AzureCredsResourceGroupKey:  []byte("dummyResourceGroup"),
			AzureCredsRegionKey:         []byte("dummyRegion"),
			AzureResourcePrefix:         []byte("dummyClusterName"),
		},
	}

	fakeclient := controllerfake.NewFakeClient(credentialsSecret)

	scope := &MachineScope{
		MachineConfig: &machinev1.AzureMachineProviderSpec{
			CredentialsSecret: &corev1.SecretReference{Name: "testCredentials", Namespace: "dummyNamespace"},
		},
		CoreClient: fakeclient,
		cloudEnv:   string(configv1.AzurePublicCloud),
	}
	err := updateFromSecret(
		fakeclient,
		scope)
	if err != nil {
		t.Errorf("Expected New credentials secrets to succeed: %v", err)
	}

	if scope.SubscriptionID != "dummySubID" {
		t.Errorf("Expected subscriptionID to be dummySubID but found %s", scope.SubscriptionID)
	}

	if scope.Location() != "dummyRegion" {
		t.Errorf("Expected location to be dummyRegion but found %s", scope.Location())
	}

	if scope.MachineConfig.Name != "dummyClusterName" {
		t.Errorf("Expected cluster name to be dummyClusterName but found %s", scope.MachineConfig.Name)
	}

	if scope.MachineConfig.ResourceGroup != "dummyResourceGroup" {
		t.Errorf("Expected resourcegroup to be dummyResourceGroup but found %s", scope.MachineConfig.ResourceGroup)
	}
}

func testCredentialFields(credentialsSecret *corev1.Secret) error {
	fakeclient := controllerfake.NewFakeClient(credentialsSecret)

	scope := &MachineScope{
		MachineConfig: &machinev1.AzureMachineProviderSpec{
			CredentialsSecret: &corev1.SecretReference{Name: "testCredentials", Namespace: "dummyNamespace"},
		},
		CoreClient: fakeclient,
		cloudEnv:   string(configv1.AzurePublicCloud),
	}
	return updateFromSecret(
		fakeclient,
		scope)
}

func TestCredentialsSecretFailures(t *testing.T) {
	credentialsSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testCredentials",
			Namespace: "dummyNamespace",
		},
		Data: map[string][]byte{},
	}

	if err := testCredentialFields(credentialsSecret); err == nil {
		t.Errorf("Expected New credentials secrets to fail")
	}

	credentialsSecret.Data["azure_subscription_id"] = []byte("dummyValue")
	if err := testCredentialFields(credentialsSecret); err == nil {
		t.Errorf("Expected New credentials secrets to fail")
	}

	credentialsSecret.Data["azure_client_id"] = []byte("dummyValue")
	if err := testCredentialFields(credentialsSecret); err == nil {
		t.Errorf("Expected New credentials secrets to fail")
	}

	credentialsSecret.Data["azure_client_secret"] = []byte("dummyValue")
	if err := testCredentialFields(credentialsSecret); err == nil {
		t.Errorf("Expected New credentials secrets to fail")
	}

	credentialsSecret.Data["azure_tenant_id"] = []byte("dummyValue")
	if err := testCredentialFields(credentialsSecret); err == nil {
		t.Errorf("Expected New credentials secrets to fail")
	}

	credentialsSecret.Data["azure_resourcegroup"] = []byte("dummyValue")
	if err := testCredentialFields(credentialsSecret); err == nil {
		t.Errorf("Expected New credentials secrets to fail")
	}

	credentialsSecret.Data["azure_region"] = []byte("dummyValue")
	if err := testCredentialFields(credentialsSecret); err == nil {
		t.Errorf("Expected New credentials secrets to fail")
	}

	credentialsSecret.Data["azure_resource_prefix"] = []byte("dummyValue")
	if err := testCredentialFields(credentialsSecret); err != nil {
		t.Errorf("Expected New credentials secrets to succeed but found : %v", err)
	}
}

func testCredentialSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testCredentials",
			Namespace: "dummyNamespace",
		},
		Data: map[string][]byte{
			"azure_subscription_id": []byte("dummySubID"),
			"azure_client_id":       []byte("dummyClientID"),
			"azure_client_secret":   []byte("dummyClientSecret"),
			"azure_tenant_id":       []byte("dummyTenantID"),
			"azure_resourcegroup":   []byte("dummyResourceGroup"),
			"azure_region":          []byte("dummyRegion"),
			"azure_resource_prefix": []byte("dummyClusterName"),
		},
	}
}

func testProviderSpec() *machinev1.AzureMachineProviderSpec {
	return &machinev1.AzureMachineProviderSpec{
		Location:          "test",
		ResourceGroup:     "test",
		CredentialsSecret: &corev1.SecretReference{Name: "testCredentials", Namespace: "dummyNamespace"},
	}
}

func testMachineWithProviderSpec(t *testing.T, providerSpec *machinev1.AzureMachineProviderSpec) *machinev1.Machine {
	providerSpecWithValues, err := providerSpecFromMachine(providerSpec)
	if err != nil {
		t.Fatalf("error encoding provider config: %v", err)
	}

	return &machinev1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test",
			Annotations: map[string]string{},
		},
		Spec: machinev1.MachineSpec{
			ProviderSpec: *providerSpecWithValues,
		},
	}
}

func testMachine(t *testing.T) *machinev1.Machine {
	return testMachineWithProviderSpec(t, testProviderSpec())
}

func TestPersistMachineScope(t *testing.T) {
	machine := testMachine(t)

	infra := &configv1.Infrastructure{
		ObjectMeta: metav1.ObjectMeta{
			Name: globalInfrastuctureName,
		},
		Status: configv1.InfrastructureStatus{
			PlatformStatus: &configv1.PlatformStatus{
				Azure: &configv1.AzurePlatformStatus{
					CloudName: configv1.AzurePublicCloud,
				},
			},
		},
	}

	params := MachineScopeParams{
		Machine:    machine,
		CoreClient: controllerfake.NewFakeClientWithScheme(scheme.Scheme, testCredentialSecret(), infra),
	}

	scope, err := NewMachineScope(params)
	if err != nil {
		t.Fatalf("Unexpected error %v", err)
	}

	nodeAddresses := []corev1.NodeAddress{
		{
			Type:    corev1.NodeHostName,
			Address: "hostname",
		},
	}

	scope.Machine.Annotations["test"] = "testValue"
	scope.MachineStatus.VMID = pointer.StringPtr("vmid")
	scope.Machine.Status.Addresses = make([]corev1.NodeAddress, len(nodeAddresses))
	scope.MachineConfig.InternalLoadBalancer = "test"
	copy(nodeAddresses, scope.Machine.Status.Addresses)

	if err := params.CoreClient.Create(scope.Context, scope.Machine); err != nil {
		t.Fatal(err)
	}

	if err = scope.Persist(); err != nil {
		t.Errorf("Expected MachineScope.Persist to success, got error: %v", err)
	}

	if !equality.Semantic.DeepEqual(scope.Machine.Status.Addresses, nodeAddresses) {
		t.Errorf("Expected node addresses to equal, updated addresses %#v, expected addresses: %#v", scope.Machine.Status.Addresses, nodeAddresses)
	}

	if scope.Machine.Annotations["test"] != "testValue" {
		t.Errorf("Expected annotation 'test' to equal 'testValue', got %q instead", scope.Machine.Annotations["test"])
	}

	machineSpec, err := MachineConfigFromProviderSpec(scope.Machine.Spec.ProviderSpec)
	if err != nil {
		t.Errorf("failed to get machine provider status: %v", err)
	}

	if machineSpec.InternalLoadBalancer != "test" {
		t.Errorf("Expected InternalLoadBalancer to be 'test', got %q instead", machineSpec.InternalLoadBalancer)
	}

	machineStatus, err := ProviderStatusFromRawExtension(scope.Machine.Status.ProviderStatus)
	if err != nil {
		t.Errorf("failed to get machine provider status: %v", err)
	}

	if machineStatus.VMID == nil {
		t.Errorf("Expected VMID to be 'vmid', got nil instead")
	} else if *machineStatus.VMID != "vmid" {
		t.Errorf("Expected VMID to be 'vmid', got %q instead", *machineStatus.VMID)
	}
}

func TestNewMachineScope(t *testing.T) {
	machineConfigNoValues := &machinev1.AzureMachineProviderSpec{
		CredentialsSecret: &corev1.SecretReference{Name: "testCredentials", Namespace: "dummyNamespace"},
	}

	testCases := []struct {
		machine               *machinev1.Machine
		secret                *corev1.Secret
		expectedLocation      string
		expectedResourceGroup string
	}{
		{
			machine:               testMachine(t),
			secret:                testCredentialSecret(),
			expectedLocation:      "test",
			expectedResourceGroup: "test",
		},
		{
			machine:               testMachineWithProviderSpec(t, machineConfigNoValues),
			secret:                testCredentialSecret(),
			expectedLocation:      "dummyRegion",
			expectedResourceGroup: "dummyResourceGroup",
		},
	}

	for _, tc := range testCases {
		infra := &configv1.Infrastructure{
			ObjectMeta: metav1.ObjectMeta{
				Name: globalInfrastuctureName,
			},
			Status: configv1.InfrastructureStatus{
				PlatformStatus: &configv1.PlatformStatus{
					Azure: &configv1.AzurePlatformStatus{
						CloudName: configv1.AzurePublicCloud,
					},
				},
			},
		}

		scope, err := NewMachineScope(MachineScopeParams{
			Machine:    tc.machine,
			CoreClient: controllerfake.NewFakeClientWithScheme(scheme.Scheme, tc.secret, infra),
		})
		if err != nil {
			t.Fatalf("Unexpected error %v", err)
		}

		if scope.MachineConfig.Location != tc.expectedLocation {
			t.Errorf("Expected %v, got: %v", tc.expectedLocation, scope.MachineConfig.Location)
		}
		if scope.MachineConfig.ResourceGroup != tc.expectedResourceGroup {
			t.Errorf("Expected %v, got: %v", tc.expectedResourceGroup, scope.MachineConfig.ResourceGroup)
		}
	}
}

func TestGetCloudEnvironment(t *testing.T) {
	testCases := []struct {
		name                string
		client              client.Client
		expectedError       bool
		expectedEnvironment string
		expectedARMEndpoint string
	}{
		{
			name:          "fail when infrastructure is missing",
			client:        controllerfake.NewFakeClient(),
			expectedError: true,
		},
		{
			name: "when cloud environment is missing default to Azure Public Cloud",
			client: controllerfake.NewFakeClient(&configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{
					Name: globalInfrastuctureName,
				},
				Status: configv1.InfrastructureStatus{},
			}),
			expectedEnvironment: string(configv1.AzurePublicCloud),
		},
		{
			name: "when cloud environment is present return it",
			client: controllerfake.NewFakeClient(&configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{
					Name: globalInfrastuctureName,
				},
				Status: configv1.InfrastructureStatus{
					PlatformStatus: &configv1.PlatformStatus{
						Azure: &configv1.AzurePlatformStatus{
							CloudName: configv1.AzureUSGovernmentCloud,
						},
					},
				},
			}),
			expectedEnvironment: string(configv1.AzureUSGovernmentCloud),
		},
		{
			name: "when cloud environment is present return it",
			client: controllerfake.NewFakeClient(&configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{
					Name: globalInfrastuctureName,
				},
				Status: configv1.InfrastructureStatus{
					PlatformStatus: &configv1.PlatformStatus{
						Azure: &configv1.AzurePlatformStatus{
							CloudName:   configv1.AzureStackCloud,
							ARMEndpoint: "test",
						},
					},
				},
			}),
			expectedEnvironment: string(configv1.AzureStackCloud),
			expectedARMEndpoint: "test",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			env, armEndpoint, err := GetCloudEnvironment(tc.client)

			if tc.expectedError {
				if err == nil {
					t.Fatal("getCloudEnvironment() was expected to return error")
				}
			} else {
				if err != nil {
					t.Fatalf("getCloudEnvironment() was not expected to return error: %v", err)
				}
			}

			if env != tc.expectedEnvironment {
				t.Fatalf("expected environment %s, got: %s", tc.expectedEnvironment, env)
			}

			if armEndpoint != tc.expectedARMEndpoint {
				t.Fatalf("expected arm endpoint %s, got: %s", tc.expectedARMEndpoint, armEndpoint)
			}
		})
	}
}
