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
	"context"
	"fmt"
	"strings"

	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/adal"
	"github.com/Azure/go-autorest/autorest/azure"
	configv1 "github.com/openshift/api/config/v1"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	apierrors "github.com/openshift/machine-api-operator/pkg/controller/machine"
	apicorev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	controllerclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	// AzureCredsSubscriptionIDKey subcription ID
	AzureCredsSubscriptionIDKey = "azure_subscription_id"
	// AzureCredsClientIDKey client id
	AzureCredsClientIDKey = "azure_client_id"
	// AzureCredsClientSecretKey client secret
	AzureCredsClientSecretKey = "azure_client_secret"
	// AzureCredsTenantIDKey tenant ID
	AzureCredsTenantIDKey = "azure_tenant_id"
	// AzureCredsResourceGroupKey resource group
	AzureCredsResourceGroupKey = "azure_resourcegroup"
	// AzureCredsRegionKey region
	AzureCredsRegionKey = "azure_region"
	// AzureResourcePrefix resource prefix for created azure resources
	AzureResourcePrefix = "azure_resource_prefix"

	globalInfrastuctureName = "cluster"

	// ControlPlane machine label
	ControlPlane string = "master"
	// Node machine label
	Node string = "worker"
	// MachineRoleLabel machine label to determine the role
	MachineRoleLabel = "machine.openshift.io/cluster-api-machine-role"
)

// MachineScopeParams defines the input parameters used to create a new MachineScope.
type MachineScopeParams struct {
	AzureClients
	Machine    *machinev1.Machine
	CoreClient controllerclient.Client
}

// NewMachineScope creates a new MachineScope from the supplied parameters.
// This is meant to be called for each machine actuator operation.
func NewMachineScope(params MachineScopeParams) (*MachineScope, error) {
	machineConfig, err := MachineConfigFromProviderSpec(params.Machine.Spec.ProviderSpec)
	if err != nil {
		return nil, apierrors.InvalidMachineConfiguration(err.Error(), "failed to get machine config")
	}

	machineStatus, err := ProviderStatusFromRawExtension(params.Machine.Status.ProviderStatus)
	if err != nil {
		return nil, fmt.Errorf("failed to get machine provider status: %w", err)
	}

	cloudEnv, armEndpoint, err := GetCloudEnvironment(params.CoreClient)
	if err != nil {
		return nil, fmt.Errorf("failed azure environment: %w", err)
	}

	machineScope := &MachineScope{
		Context:      context.Background(),
		AzureClients: params.AzureClients,

		// Deep copy the machine since it's change outside of the machine scope
		// by consumers of the machine scope (e.g. reconciler).
		Machine:       params.Machine.DeepCopy(),
		CoreClient:    params.CoreClient,
		MachineConfig: machineConfig,
		MachineStatus: machineStatus,
		// Once set, they can not be changed. Otherwise, status change computation
		// might be invalid and result in skipping the status update.
		origMachine:        params.Machine.DeepCopy(),
		origMachineStatus:  machineStatus.DeepCopy(),
		machineToBePatched: controllerclient.MergeFrom(params.Machine.DeepCopy()),
		cloudEnv:           cloudEnv,
		armEndpoint:        armEndpoint,
	}

	if err = updateFromSecret(params.CoreClient, machineScope); err != nil {
		return nil, fmt.Errorf("failed to update cluster: %w", err)
	}

	return machineScope, nil
}

// MachineScope defines a scope defined around a machine and its cluster.
type MachineScope struct {
	context.Context
	AzureClients

	Machine       *machinev1.Machine
	CoreClient    controllerclient.Client
	MachineConfig *machinev1.AzureMachineProviderSpec
	MachineStatus *machinev1.AzureMachineProviderStatus

	// cloudEnv azure environment: AzurePublic, AzureStackCloud, etc.
	cloudEnv string

	// armEndpoint azure resource manager endpoint
	armEndpoint string

	// origMachine captures original value of machine before it is updated (to
	// skip object updated if nothing is changed)
	origMachine *machinev1.Machine
	// origMachineStatus captures original value of machine provider status
	// before it is updated (to skip object updated if nothing is changed)
	origMachineStatus *machinev1.AzureMachineProviderStatus

	machineToBePatched controllerclient.Patch
}

// Name returns the machine name.
func (m *MachineScope) Name() string {
	return m.Machine.Name
}

// Namespace returns the machine namespace.
func (m *MachineScope) Namespace() string {
	return m.Machine.Namespace
}

// Role returns the machine role from the labels.
func (m *MachineScope) Role() string {
	return m.Machine.Labels[MachineRoleLabel]
}

// Location returns the machine location.
func (m *MachineScope) Location() string {
	return m.MachineConfig.Location
}

func (m *MachineScope) setMachineSpec() error {
	ext, err := RawExtensionFromProviderSpec(m.MachineConfig)
	if err != nil {
		return err
	}

	klog.V(4).Infof("Storing machine spec for %q, resourceVersion: %v, generation: %v", m.Machine.Name, m.Machine.ResourceVersion, m.Machine.Generation)
	m.Machine.Spec.ProviderSpec.Value = ext

	return nil
}

func (m *MachineScope) setMachineStatus() error {
	if equality.Semantic.DeepEqual(m.MachineStatus, m.origMachineStatus) && equality.Semantic.DeepEqual(m.Machine.Status.Addresses, m.origMachine.Status.Addresses) {
		klog.Infof("%s: status unchanged", m.Machine.Name)
		return nil
	}

	klog.V(4).Infof("Storing machine status for %q, resourceVersion: %v, generation: %v", m.Machine.Name, m.Machine.ResourceVersion, m.Machine.Generation)
	ext, err := RawExtensionFromProviderStatus(m.MachineStatus)
	if err != nil {
		return err
	}

	m.Machine.Status.ProviderStatus = ext
	return err
}

// PatchMachine patch machine and machine status
func (m *MachineScope) PatchMachine() error {
	klog.V(3).Infof("%v: patching machine", m.Machine.GetName())

	statusCopy := *m.Machine.Status.DeepCopy()

	// patch machine
	if err := m.CoreClient.Patch(context.Background(), m.Machine, m.machineToBePatched); err != nil {
		klog.Errorf("Failed to patch machine %q: %v", m.Machine.GetName(), err)
		return err
	}

	m.Machine.Status = statusCopy

	// patch status
	if err := m.CoreClient.Status().Patch(context.Background(), m.Machine, m.machineToBePatched); err != nil {
		klog.Errorf("Failed to patch machine status %q: %v", m.Machine.GetName(), err)
		return err
	}

	return nil
}

// Persist the machine spec and machine status.
func (m *MachineScope) Persist() error {
	// The machine status needs to be updated first since
	// the next call to storeMachineSpec updates entire machine
	// object. If done in the reverse order, the machine status
	// could be updated without setting the LastUpdated field
	// in the machine status. The following might occur:
	// 1. machine object is updated (including its status)
	// 2. the machine object is updated by different component/user meantime
	// 3. storeMachineStatus is called but fails since the machine object
	//    is outdated. The operation is reconciled but given the status
	//    was already set in the previous call, the status is no longer updated
	//    since the status updated condition is already false. Thus,
	//    the LastUpdated is not set/updated properly.
	if err := m.setMachineStatus(); err != nil {
		return fmt.Errorf("[machinescope] failed to set provider status for machine %q in namespace %q: %v", m.Machine.Name, m.Machine.Namespace, err)
	}

	if err := m.setMachineSpec(); err != nil {
		return fmt.Errorf("[machinescope] failed to set machine spec %q in namespace %q: %v", m.Machine.Name, m.Machine.Namespace, err)
	}

	if err := m.PatchMachine(); err != nil {
		return fmt.Errorf("[machinescope] failed to patch machine %q in namespace %q: %v", m.Machine.Name, m.Machine.Namespace, err)
	}

	return nil
}

func (m *MachineScope) IsStackHub() bool {
	return strings.EqualFold(m.cloudEnv, string(configv1.AzureStackCloud))
}

func GetCloudEnvironment(client controllerclient.Client) (string, string, error) {
	infra := &configv1.Infrastructure{}
	infraName := controllerclient.ObjectKey{Name: globalInfrastuctureName}

	if err := client.Get(context.Background(), infraName, infra); err != nil {
		return "", "", err
	}

	// When cloud environment is missing default to Azure Public Cloud
	if infra.Status.PlatformStatus == nil || infra.Status.PlatformStatus.Azure == nil || infra.Status.PlatformStatus.Azure.CloudName == "" {
		return string(configv1.AzurePublicCloud), "", nil
	}

	armEndpoint := ""
	if infra.Status.PlatformStatus != nil || infra.Status.PlatformStatus.Azure != nil || infra.Status.PlatformStatus.Azure.ARMEndpoint != "" {
		armEndpoint = infra.Status.PlatformStatus.Azure.ARMEndpoint
	}

	return string(infra.Status.PlatformStatus.Azure.CloudName), armEndpoint, nil
}

// MachineConfigFromProviderSpec tries to decode the JSON-encoded spec, falling back on getting a MachineClass if the value is absent.
func MachineConfigFromProviderSpec(providerConfig machinev1.ProviderSpec) (*machinev1.AzureMachineProviderSpec, error) {
	var config machinev1.AzureMachineProviderSpec
	if providerConfig.Value != nil {
		klog.V(4).Info("Decoding ProviderConfig from Value")
		return unmarshalProviderSpec(providerConfig.Value)
	}

	return &config, nil
}

func unmarshalProviderSpec(spec *runtime.RawExtension) (*machinev1.AzureMachineProviderSpec, error) {
	var config machinev1.AzureMachineProviderSpec
	if spec != nil {
		if err := yaml.Unmarshal(spec.Raw, &config); err != nil {
			return nil, err
		}
	}
	klog.V(6).Infof("Found ProviderSpec: %+v", config)
	return &config, nil
}

func updateFromSecret(coreClient controllerclient.Client, scope *MachineScope) error {
	if scope.MachineConfig.CredentialsSecret == nil {
		return nil
	}

	secretType := types.NamespacedName{
		Namespace: scope.MachineConfig.CredentialsSecret.Namespace,
		Name:      scope.MachineConfig.CredentialsSecret.Name,
	}
	var secret apicorev1.Secret
	if err := coreClient.Get(
		context.Background(),
		secretType,
		&secret); err != nil {
		return err
	}

	subscriptionID, ok := secret.Data[AzureCredsSubscriptionIDKey]
	if !ok {
		return fmt.Errorf("Azure subscription id %v did not contain key %v",
			secretType.String(), AzureCredsSubscriptionIDKey)
	}
	clientID, ok := secret.Data[AzureCredsClientIDKey]
	if !ok {
		return fmt.Errorf("Azure client id %v did not contain key %v",
			secretType.String(), AzureCredsClientIDKey)
	}
	clientSecret, ok := secret.Data[AzureCredsClientSecretKey]
	if !ok {
		return fmt.Errorf("Azure client secret %v did not contain key %v",
			secretType.String(), AzureCredsClientSecretKey)
	}
	tenantID, ok := secret.Data[AzureCredsTenantIDKey]
	if !ok {
		return fmt.Errorf("Azure tenant id %v did not contain key %v",
			secretType.String(), AzureCredsTenantIDKey)
	}
	resourceGroup, ok := secret.Data[AzureCredsResourceGroupKey]
	if !ok {
		return fmt.Errorf("Azure resource group %v did not contain key %v",
			secretType.String(), AzureCredsResourceGroupKey)
	}
	region, ok := secret.Data[AzureCredsRegionKey]
	if !ok {
		return fmt.Errorf("Azure region %v did not contain key %v",
			secretType.String(), AzureCredsRegionKey)
	}
	clusterName, ok := secret.Data[AzureResourcePrefix]
	if !ok {
		return fmt.Errorf("Azure resource prefix %v did not contain key %v",
			secretType.String(), AzureResourcePrefix)
	}

	env, err := getEnvironment(scope)
	if err != nil {
		return err
	}

	oauthConfig, err := adal.NewOAuthConfig(
		env.ActiveDirectoryEndpoint, string(tenantID))
	if err != nil {
		return err
	}

	token, err := adal.NewServicePrincipalToken(
		*oauthConfig, string(clientID), string(clientSecret), env.TokenAudience)
	if err != nil {
		return err
	}

	authorizer, err := autorest.NewBearerAuthorizer(token), nil
	if err != nil {
		return fmt.Errorf("failed to create azure session: %v", err)
	}

	if scope.MachineConfig.ResourceGroup == "" {
		scope.MachineConfig.ResourceGroup = string(resourceGroup)
	}

	if scope.MachineConfig.NetworkResourceGroup == "" {
		scope.MachineConfig.NetworkResourceGroup = string(resourceGroup)
	}

	if scope.MachineConfig.Location == "" {
		scope.MachineConfig.Location = string(region)
	}

	scope.MachineConfig.ObjectMeta.Name = string(clusterName)
	scope.Authorizer = authorizer
	scope.SubscriptionID = string(subscriptionID)
	scope.ResourceManagerEndpoint = env.ResourceManagerEndpoint

	return nil
}

func getEnvironment(m *MachineScope) (*azure.Environment, error) {
	if m.IsStackHub() {
		env, err := azure.EnvironmentFromURL(m.armEndpoint)
		if err != nil {
			return nil, err
		}

		return &env, nil
	}

	env, err := azure.EnvironmentFromName(m.cloudEnv)
	if err != nil {
		return nil, err
	}

	return &env, nil
}
