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
	"reflect"
	"testing"

	"github.com/Azure/go-autorest/autorest/to"
	configv1 "github.com/openshift/api/config/v1"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
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
		},
	}

	fakeclient := controllerfake.NewClientBuilder().WithObjects(credentialsSecret).Build()

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

	if scope.MachineConfig.ResourceGroup != "dummyResourceGroup" {
		t.Errorf("Expected resourcegroup to be dummyResourceGroup but found %s", scope.MachineConfig.ResourceGroup)
	}
}

func testCredentialFields(credentialsSecret *corev1.Secret) error {
	fakeclient := controllerfake.NewClientBuilder().WithObjects(credentialsSecret).Build()

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

func testMachineWithProviderSpecTags(t *testing.T, providerSpec *machinev1.AzureMachineProviderSpec) *machinev1.Machine {
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

func TestPersistMachineScope(t *testing.T) {
	machine := testMachine(t)

	infra := &configv1.Infrastructure{
		ObjectMeta: metav1.ObjectMeta{
			Name: globalInfrastuctureName,
		},
		Status: configv1.InfrastructureStatus{
			InfrastructureName: "test-sdgh",
			PlatformStatus: &configv1.PlatformStatus{
				Azure: &configv1.AzurePlatformStatus{
					CloudName: configv1.AzurePublicCloud,
				},
			},
		},
	}

	params := MachineScopeParams{
		Machine:    machine,
		CoreClient: controllerfake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(testCredentialSecret(), infra).WithStatusSubresource(&machinev1.Machine{}).Build(),
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
	scope.MachineStatus.VMID = ptr.To[string]("vmid")
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

	machineConfigWithTags := &machinev1.AzureMachineProviderSpec{
		Tags: map[string]string{"environment": "test"},
	}

	testCases := []struct {
		machine               *machinev1.Machine
		secret                *corev1.Secret
		expectedLocation      string
		expectedResourceGroup string
		expectedTags          map[string]*string
	}{
		{
			machine:               testMachine(t),
			secret:                testCredentialSecret(),
			expectedLocation:      "test",
			expectedResourceGroup: "test",
			expectedTags: map[string]*string{
				"kubernetes.io_cluster.test-shfj": to.StringPtr("owned"),
				"created-for":                     to.StringPtr("ocp"),
			},
		},
		{
			machine:               testMachineWithProviderSpec(t, machineConfigNoValues),
			secret:                testCredentialSecret(),
			expectedLocation:      "dummyRegion",
			expectedResourceGroup: "dummyResourceGroup",
			expectedTags: map[string]*string{
				"kubernetes.io_cluster.test-shfj": to.StringPtr("owned"),
				"created-for":                     to.StringPtr("ocp"),
			},
		},
		{
			machine: testMachineWithProviderSpecTags(t, machineConfigWithTags),
			secret:  testCredentialSecret(),
			expectedTags: map[string]*string{
				"kubernetes.io_cluster.test-shfj": to.StringPtr("owned"),
				"created-for":                     to.StringPtr("ocp"),
				"environment":                     to.StringPtr("test"),
			},
		},
	}

	for _, tc := range testCases {
		infra := &configv1.Infrastructure{
			ObjectMeta: metav1.ObjectMeta{
				Name: globalInfrastuctureName,
			},
			Status: configv1.InfrastructureStatus{
				InfrastructureName: "test-shfj",
				PlatformStatus: &configv1.PlatformStatus{
					Azure: &configv1.AzurePlatformStatus{
						CloudName: configv1.AzurePublicCloud,
						ResourceTags: []configv1.AzureResourceTag{
							{
								Key:   "created-for",
								Value: "ocp",
							},
						},
					},
				},
			},
		}

		scope, err := NewMachineScope(MachineScopeParams{
			Machine:    tc.machine,
			CoreClient: controllerfake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(tc.secret, infra).Build(),
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
		if !reflect.DeepEqual(scope.Tags, tc.expectedTags) {
			t.Errorf("Expected %+v, Got: %+v, MachineSpec: %+v, InfraStatus: %+v",
				tc.expectedTags, scope.Tags, scope.MachineConfig.Tags, infra.Status.PlatformStatus.Azure.ResourceTags)
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
			client:        controllerfake.NewClientBuilder().Build(),
			expectedError: true,
		},
		{
			name: "when cloud environment is missing default to Azure Public Cloud",
			client: controllerfake.NewClientBuilder().WithObjects(&configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{
					Name: globalInfrastuctureName,
				},
				Status: configv1.InfrastructureStatus{
					InfrastructureName: "test-yuhk",
				},
			}).Build(),
			expectedEnvironment: string(configv1.AzurePublicCloud),
		},
		{
			name: "when cloud environment is present return it",
			client: controllerfake.NewClientBuilder().WithObjects(&configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{
					Name: globalInfrastuctureName,
				},
				Status: configv1.InfrastructureStatus{
					InfrastructureName: "test-yuhk",
					PlatformStatus: &configv1.PlatformStatus{
						Azure: &configv1.AzurePlatformStatus{
							CloudName: configv1.AzureUSGovernmentCloud,
							ResourceTags: []configv1.AzureResourceTag{
								{
									Key:   "created-for",
									Value: "ocp",
								},
							},
						},
					},
				},
			}).Build(),
			expectedEnvironment: string(configv1.AzureUSGovernmentCloud),
		},
		{
			name: "when cloud environment is present return it",
			client: controllerfake.NewClientBuilder().WithObjects(&configv1.Infrastructure{
				ObjectMeta: metav1.ObjectMeta{
					Name: globalInfrastuctureName,
				},
				Status: configv1.InfrastructureStatus{
					InfrastructureName: "test-yuhk",
					PlatformStatus: &configv1.PlatformStatus{
						Azure: &configv1.AzurePlatformStatus{
							CloudName:   configv1.AzureStackCloud,
							ARMEndpoint: "test",
							ResourceTags: []configv1.AzureResourceTag{
								{
									Key:   "created-for",
									Value: "ocp",
								},
								{
									Key:   "environment",
									Value: "test",
								},
							},
						},
					},
				},
			}).Build(),
			expectedEnvironment: string(configv1.AzureStackCloud),
			expectedARMEndpoint: "test",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			infra, err := GetInfrastructure(tc.client)
			if tc.expectedError {
				if err == nil {
					t.Fatal("GetInfrastructure() was expected to return error")
				}

				// We expected an error and got one.
				// Do not proceed as we are happy with this result.
				return
			} else {
				if err != nil {
					t.Fatalf("GetInfrastructure() was not expected to return error: %v", err)
				}
			}

			env, armEndpoint := GetCloudEnvironment(infra)

			if env != tc.expectedEnvironment {
				t.Fatalf("expected environment %s, got: %s", tc.expectedEnvironment, env)
			}

			if armEndpoint != tc.expectedARMEndpoint {
				t.Fatalf("expected arm endpoint %s, got: %s", tc.expectedARMEndpoint, armEndpoint)
			}
		})
	}
}

func TestGetTagList(t *testing.T) {
	ocpDefaultTags := map[string]string{
		"kubernetes.io_cluster.test-fhbv": "owned",
	}

	testCases := []struct {
		name            string
		machineSpecTags map[string]string
		infraStatusTags map[string]string
		ocpTags         map[string]string
		expectedTags    map[string]*string
		wantErr         bool
	}{
		{
			name:            "OCPTags, InfraStatusTags and MachineSpecTags are empty",
			ocpTags:         nil,
			machineSpecTags: nil,
			infraStatusTags: nil,
			expectedTags:    map[string]*string{},
			wantErr:         false,
		},
		{
			name:            "InfraStatusTags and MachineSpecTags are empty",
			ocpTags:         ocpDefaultTags,
			machineSpecTags: nil,
			infraStatusTags: nil,
			expectedTags: map[string]*string{
				"kubernetes.io_cluster.test-fhbv": to.StringPtr("owned"),
			},
			wantErr: false,
		},
		{
			name:            "InfraStatusTags is empty",
			ocpTags:         ocpDefaultTags,
			machineSpecTags: map[string]string{"environment": "test"},
			infraStatusTags: nil,
			expectedTags: map[string]*string{
				"kubernetes.io_cluster.test-fhbv": to.StringPtr("owned"),
				"environment":                     to.StringPtr("test"),
			},
			wantErr: false,
		},
		{
			name:            "MachineSpecTags is empty",
			ocpTags:         ocpDefaultTags,
			machineSpecTags: nil,
			infraStatusTags: map[string]string{"owned-by": "ocp"},
			expectedTags: map[string]*string{
				"kubernetes.io_cluster.test-fhbv": to.StringPtr("owned"),
				"owned-by":                        to.StringPtr("ocp"),
			},
			wantErr: false,
		},
		{
			name:            "InfraStatusTags and MachineSpecTags are not empty",
			ocpTags:         ocpDefaultTags,
			machineSpecTags: map[string]string{"environment": "test"},
			infraStatusTags: map[string]string{"createdFor": "ocp"},
			expectedTags: map[string]*string{
				"kubernetes.io_cluster.test-fhbv": to.StringPtr("owned"),
				"environment":                     to.StringPtr("test"),
				"createdFor":                      to.StringPtr("ocp"),
			},
			wantErr: false,
		},
		{
			name:    "InfraStatusTags and MachineSpecTags differ",
			ocpTags: ocpDefaultTags,
			machineSpecTags: map[string]string{
				"environment": "test",
				"billing":     "test",
				"createdFor":  "ocp-test",
			},
			infraStatusTags: map[string]string{
				"createdFor": "ocp",
			},
			expectedTags: map[string]*string{
				"kubernetes.io_cluster.test-fhbv": to.StringPtr("owned"),
				"createdFor":                      to.StringPtr("ocp-test"),
				"environment":                     to.StringPtr("test"),
				"billing":                         to.StringPtr("test"),
			},
			wantErr: false,
		},
		{
			name:    "InfraStatusTags and MachineSpecTags differ, tag removed",
			ocpTags: ocpDefaultTags,
			machineSpecTags: map[string]string{
				"environment": "test",
				"billing":     "test",
			},
			infraStatusTags: map[string]string{
				"createdFor": "ocp",
			},
			expectedTags: map[string]*string{
				"kubernetes.io_cluster.test-fhbv": to.StringPtr("owned"),
				"createdFor":                      to.StringPtr("ocp"),
				"environment":                     to.StringPtr("test"),
				"billing":                         to.StringPtr("test"),
			},
			wantErr: false,
		},
		{
			name: "OCP reserved tag tally is more than 5",
			ocpTags: map[string]string{
				"key1": "value40", "key2": "value39", "key3": "value38",
				"key4": "value37", "key5": "value36", "key6": "value35",
			},
			machineSpecTags: map[string]string{
				"key1": "value40", "key2": "value39", "key3": "value38",
				"key4": "value37", "key5": "value36", "key6": "value35",
				"key7": "value34", "key8": "value33", "key9": "value32",
				"key10": "value31", "key11": "value30", "key12": "value29",
				"key13": "value28", "key14": "value27", "key15": "value26",
				"key16": "value25", "key17": "value24", "key18": "value23",
				"key19": "value22", "key20": "value21", "key21": "value20",
				"key22": "value19", "key23": "value18", "key24": "value17",
				"key25": "value16", "key26": "value15", "key27": "value14",
				"key28": "value13", "key29": "value12", "key30": "value11",
				"key31": "value10", "key32": "value9", "key33": "value8",
				"key34": "value7", "key35": "value6", "key36": "value5",
				"key37": "value4", "key38": "value3", "key39": "value2",
				"key40": "value1",
			},
			infraStatusTags: map[string]string{
				"key20": "value1", "key26": "value2", "key15": "value3",
				"key79": "value4", "key59": "value5", "key63": "value6",
			},
			expectedTags: nil,
			wantErr:      true,
		},
		{
			name:    "InfraStatusTags and MachineSpecTags together contains more than 45 tags",
			ocpTags: ocpDefaultTags,
			machineSpecTags: map[string]string{
				"key1": "value40", "key2": "value39", "key3": "value38",
				"key4": "value37", "key5": "value36", "key6": "value35",
				"key7": "value34", "key8": "value33", "key9": "value32",
				"key10": "value31", "key11": "value30", "key12": "value29",
				"key13": "value28", "key14": "value27", "key15": "value26",
				"key16": "value25", "key17": "value24", "key18": "value23",
				"key19": "value22", "key20": "value21", "key21": "value20",
				"key22": "value19", "key23": "value18", "key24": "value17",
				"key25": "value16", "key26": "value15", "key27": "value14",
				"key28": "value13", "key29": "value12", "key30": "value11",
				"key31": "value10", "key32": "value9", "key33": "value8",
				"key34": "value7", "key35": "value6", "key36": "value5",
				"key37": "value4", "key38": "value3", "key39": "value2",
				"key40": "value1",
			},
			infraStatusTags: map[string]string{
				"key20": "value1", "key26": "value2", "key15": "value3",
				"key79": "value4", "key59": "value5", "key63": "value6",
				"key73": "value7", "key85": "value8", "key91": "value9",
				"key25": "value10",
			},
			expectedTags: nil,
			wantErr:      true,
		},
		{
			name:    "MachineSpecTags has duplicate tag keys",
			ocpTags: ocpDefaultTags,
			machineSpecTags: map[string]string{
				"environment": "test",
				"billing":     "test",
				"Billing":     "dev",
				"CreatedFor":  "dev",
				"createdfor":  "test",
			},
			infraStatusTags: map[string]string{
				"createdFor": "ocp",
			},
			expectedTags: nil,
			wantErr:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tags, err := getTagList(tc.ocpTags, tc.infraStatusTags, tc.machineSpecTags)

			if (err != nil) != tc.wantErr {
				t.Errorf("Got: %v, wantErr: %v", err, tc.wantErr)
			}

			if !reflect.DeepEqual(tags, tc.expectedTags) {
				t.Errorf("Expected %+v, Got: %+v, OCP: %+v, MachineSpec: %+v, InfraStatus: %+v",
					tc.expectedTags, tags, tc.ocpTags, tc.machineSpecTags, tc.infraStatusTags)
			}
		})
	}
}

func TestGetOCPTagList(t *testing.T) {

	testCases := []struct {
		name         string
		clusterID    string
		expectedTags map[string]string
		wantErr      bool
	}{
		{
			name:      "OCP tag list generation success",
			clusterID: "test-fghd",
			expectedTags: map[string]string{
				"kubernetes.io_cluster.test-fghd": "owned",
			},
			wantErr: false,
		},
		{
			name:         "OCP tag list generation fails, clusterID empty",
			clusterID:    "",
			expectedTags: nil,
			wantErr:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tags, err := getOCPTagList(tc.clusterID)

			if (err != nil) != tc.wantErr {
				t.Errorf("Got: %v, wantErr: %v", err, tc.wantErr)
			}

			if !reflect.DeepEqual(tags, tc.expectedTags) {
				t.Errorf("Expected %+v, Got: %+v", tc.expectedTags, tags)
			}
		})
	}
}
