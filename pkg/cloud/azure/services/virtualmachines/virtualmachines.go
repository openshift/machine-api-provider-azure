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

package virtualmachines

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2021-11-01/compute"
	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2021-02-01/network"
	"github.com/Azure/go-autorest/autorest/to"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	apierrors "github.com/openshift/machine-api-operator/pkg/controller/machine"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/networkinterfaces"

	"golang.org/x/crypto/ssh"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
)

const (
	// winAutoLogonFormatString is the format string used to create the AutoLogon
	// AdditionalUnattendContent configuration for Windows machines.
	winAutoLogonFormatString = `<AutoLogon>
			<Username>%s</Username>
			<Password>
				<Value>%s</Value>
			</Password>
			<Enabled>true</Enabled>
			<LogonCount>1</LogonCount>
		</AutoLogon>`

	// winFirstLogonCommandsString is the string used to create the FirstLogonCommands
	// AdditionalUnattendContent configuration for Windows machines.
	winFirstLogonCommandsString = `<FirstLogonCommands>
			<SynchronousCommand>
				<Description>Copy user data secret contents to init script</Description>
				<CommandLine>cmd /c "copy C:\AzureData\CustomData.bin C:\init.ps1"</CommandLine>
				<Order>11</Order>
			</SynchronousCommand>
			<SynchronousCommand>
				<Description>Launch init script</Description>
				<CommandLine>powershell.exe -NonInteractive -ExecutionPolicy Bypass -File C:\init.ps1</CommandLine>
				<Order>12</Order>
			</SynchronousCommand>
		</FirstLogonCommands>`
)

// Spec input specification for Get/CreateOrUpdate/Delete calls
type Spec struct {
	Name                       string
	NICName                    string
	SSHKeyData                 string
	Size                       string
	Zone                       string
	Image                      machinev1.Image
	OSDisk                     machinev1.OSDisk
	DataDisks                  []machinev1.DataDisk
	CustomData                 string
	ManagedIdentity            string
	Tags                       map[string]*string
	Priority                   compute.VirtualMachinePriorityTypes
	EvictionPolicy             compute.VirtualMachineEvictionPolicyTypes
	BillingProfile             *compute.BillingProfile
	SecurityProfile            *machinev1.SecurityProfile
	DiagnosticsProfile         *compute.DiagnosticsProfile
	UltraSSDCapability         machinev1.AzureUltraSSDCapabilityState
	AvailabilitySetName        string
	CapacityReservationGroupID string
}

// Get provides information about a virtual network.
func (s *Service) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	vmSpec, ok := spec.(*Spec)
	if !ok {
		return compute.VirtualMachine{}, errors.New("invalid vm specification")
	}
	vm, err := s.Client.Get(ctx, s.Scope.MachineConfig.ResourceGroup, vmSpec.Name, compute.InstanceViewTypesInstanceView)
	if err != nil && azure.ResourceNotFound(err) {
		klog.Warningf("vm %s not found: %w", vmSpec.Name, err.Error())
		return nil, err
	} else if err != nil {
		return vm, err
	}
	return vm, nil
}

// CreateOrUpdate creates or updates a virtual network.
func (s *Service) CreateOrUpdate(ctx context.Context, spec azure.Spec) error {
	vmSpec, ok := spec.(*Spec)
	if !ok {
		return errors.New("invalid vm specification")
	}

	klog.V(2).Infof("getting nic %s", vmSpec.NICName)
	nicInterface, err := networkinterfaces.NewService(s.Scope).Get(ctx, &networkinterfaces.Spec{Name: vmSpec.NICName})
	if err != nil {
		return err
	}

	nic, ok := nicInterface.(network.Interface)
	if !ok {
		return errors.New("error getting network security group")
	}
	klog.V(2).Infof("got nic %s", vmSpec.NICName)

	klog.V(2).Infof("creating vm %s ", vmSpec.Name)

	virtualMachine, err := s.deriveVirtualMachineParameters(vmSpec, nic)
	if err != nil {
		return err
	}

	future, err := s.Client.CreateOrUpdate(
		ctx,
		s.Scope.MachineConfig.ResourceGroup,
		vmSpec.Name,
		*virtualMachine)
	if err != nil {
		return fmt.Errorf("cannot create vm: %w", err)
	}

	// Do not wait until the operation completes. Just check the result
	// so the call to Create actuator operation is async.
	_, err = future.Result(s.Client)
	if err != nil {
		return err
	}

	klog.V(2).Infof("successfully created vm %s ", vmSpec.Name)
	return err
}

func generateOSProfile(vmSpec *Spec) (*compute.OSProfile, error) {
	sshKeyData := vmSpec.SSHKeyData
	if sshKeyData == "" && compute.OperatingSystemTypes(vmSpec.OSDisk.OSType) != compute.OperatingSystemTypesWindows {
		privateKey, perr := rsa.GenerateKey(rand.Reader, 2048)
		if perr != nil {
			return nil, fmt.Errorf("Failed to generate private key: %w", perr)
		}

		publicRsaKey, perr := ssh.NewPublicKey(&privateKey.PublicKey)
		if perr != nil {
			return nil, fmt.Errorf("Failed to generate public key: %w", perr)
		}
		sshKeyData = string(ssh.MarshalAuthorizedKey(publicRsaKey))
	}

	randomPassword, err := GenerateRandomString(32)
	if err != nil {
		return nil, fmt.Errorf("failed to generate random string: %w", err)
	}

	osProfile := &compute.OSProfile{
		ComputerName:  to.StringPtr(vmSpec.Name),
		AdminUsername: to.StringPtr(azure.DefaultUserName),
		AdminPassword: to.StringPtr(randomPassword),
	}

	if compute.OperatingSystemTypes(vmSpec.OSDisk.OSType) == compute.OperatingSystemTypesWindows {
		osProfile.WindowsConfiguration = &compute.WindowsConfiguration{
			EnableAutomaticUpdates: to.BoolPtr(false),
			AdditionalUnattendContent: &[]compute.AdditionalUnattendContent{
				{
					PassName:      "OobeSystem",
					ComponentName: "Microsoft-Windows-Shell-Setup",
					SettingName:   "AutoLogon",
					Content:       to.StringPtr(fmt.Sprintf(winAutoLogonFormatString, *osProfile.AdminUsername, *osProfile.AdminPassword)),
				},
				{
					PassName:      "OobeSystem",
					ComponentName: "Microsoft-Windows-Shell-Setup",
					SettingName:   "FirstLogonCommands",
					Content:       to.StringPtr(winFirstLogonCommandsString),
				},
			},
		}
	} else if sshKeyData != "" {
		osProfile.LinuxConfiguration = &compute.LinuxConfiguration{
			SSH: &compute.SSHConfiguration{
				PublicKeys: &[]compute.SSHPublicKey{
					{
						Path:    to.StringPtr(fmt.Sprintf("/home/%s/.ssh/authorized_keys", azure.DefaultUserName)),
						KeyData: to.StringPtr(sshKeyData),
					},
				},
			},
		}
	}

	if vmSpec.CustomData != "" {
		osProfile.CustomData = to.StringPtr(vmSpec.CustomData)
	}

	return osProfile, nil
}

// Derive virtual machine parameters for CreateOrUpdate API call based
// on the provided virtual machine specification, resource location,
// subscription ID, and the network interface.
func (s *Service) deriveVirtualMachineParameters(vmSpec *Spec, nic network.Interface) (*compute.VirtualMachine, error) {
	osProfile, err := generateOSProfile(vmSpec)
	if err != nil {
		return nil, err
	}

	imageReference := &compute.ImageReference{
		Publisher: to.StringPtr(vmSpec.Image.Publisher),
		Offer:     to.StringPtr(vmSpec.Image.Offer),
		Sku:       to.StringPtr(vmSpec.Image.SKU),
		Version:   to.StringPtr(vmSpec.Image.Version),
	}
	if vmSpec.Image.ResourceID != "" {
		imageReference = &compute.ImageReference{
			ID: to.StringPtr(fmt.Sprintf("/subscriptions/%s%s", s.Scope.SubscriptionID, vmSpec.Image.ResourceID)),
		}
	}

	osDisk := generateOSDisk(vmSpec)

	securityProfile, err := generateSecurityProfile(vmSpec, osDisk)
	if err != nil {
		return nil, err
	}

	priority, evictionPolicy, billingProfile, err := getSpotVMOptions(s.Scope.MachineConfig.SpotVMOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to get Spot VM options %w", err)
	}

	dataDisks, err := generateDataDisks(vmSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate data disk spec: %w", err)
	}

	virtualMachine := &compute.VirtualMachine{
		Location: to.StringPtr(s.Scope.MachineConfig.Location),
		Tags:     vmSpec.Tags,
		Plan:     generateImagePlan(vmSpec.Image),
		VirtualMachineProperties: &compute.VirtualMachineProperties{
			HardwareProfile: &compute.HardwareProfile{
				VMSize: compute.VirtualMachineSizeTypes(vmSpec.Size),
			},
			StorageProfile: &compute.StorageProfile{
				ImageReference: imageReference,
				OsDisk:         osDisk,
				DataDisks:      &dataDisks,
			},
			SecurityProfile: securityProfile,
			OsProfile:       osProfile,
			NetworkProfile: &compute.NetworkProfile{
				NetworkInterfaces: &[]compute.NetworkInterfaceReference{
					{
						ID: nic.ID,
						NetworkInterfaceReferenceProperties: &compute.NetworkInterfaceReferenceProperties{
							Primary: to.BoolPtr(true),
						},
					},
				},
			},
			Priority:           priority,
			EvictionPolicy:     evictionPolicy,
			BillingProfile:     billingProfile,
			DiagnosticsProfile: vmSpec.DiagnosticsProfile,
		},
	}

	// Ephemeral storage related options
	if vmSpec.OSDisk.CachingType != "" {
		virtualMachine.VirtualMachineProperties.StorageProfile.OsDisk.Caching = compute.CachingTypes(vmSpec.OSDisk.CachingType)
	}
	if vmSpec.OSDisk.DiskSettings.EphemeralStorageLocation == "Local" {
		virtualMachine.VirtualMachineProperties.StorageProfile.OsDisk.DiffDiskSettings = &compute.DiffDiskSettings{
			Option: compute.DiffDiskOptions(vmSpec.OSDisk.DiskSettings.EphemeralStorageLocation),
		}
	}

	// Enable/Disable UltraSSD Additional capabilities based on UltraSSDCapability
	// If UltraSSDCapability is unset the presence of Ultra Disks as Data Disks will pilot the toggling
	var ultraSSDEnabled *bool
	switch vmSpec.UltraSSDCapability {
	case machinev1.AzureUltraSSDCapabilityDisabled:
		ultraSSDEnabled = to.BoolPtr(false)
	case machinev1.AzureUltraSSDCapabilityEnabled:
		ultraSSDEnabled = to.BoolPtr(true)
	default:
		for _, dataDisk := range vmSpec.DataDisks {
			if dataDisk.ManagedDisk.StorageAccountType == machinev1.StorageAccountUltraSSDLRS &&
				vmSpec.UltraSSDCapability != machinev1.AzureUltraSSDCapabilityDisabled {
				ultraSSDEnabled = to.BoolPtr(true)
			}
		}
	}

	virtualMachine.VirtualMachineProperties.AdditionalCapabilities = &compute.AdditionalCapabilities{
		UltraSSDEnabled: ultraSSDEnabled,
	}

	if vmSpec.ManagedIdentity != "" {
		virtualMachine.Identity = &compute.VirtualMachineIdentity{
			Type: compute.ResourceIdentityTypeUserAssigned,
			UserAssignedIdentities: map[string]*compute.VirtualMachineIdentityUserAssignedIdentitiesValue{
				vmSpec.ManagedIdentity: &compute.VirtualMachineIdentityUserAssignedIdentitiesValue{},
			},
		}
	}

	if vmSpec.Zone != "" {
		zones := []string{vmSpec.Zone}
		virtualMachine.Zones = &zones
	}

	if vmSpec.AvailabilitySetName != "" {
		virtualMachine.AvailabilitySet = &compute.SubResource{
			ID: to.StringPtr(availabilitySetID(s.Scope.SubscriptionID, s.Scope.MachineConfig.ResourceGroup, vmSpec.AvailabilitySetName)),
		}
	}
	// configure capacity reservation ID
	if vmSpec.CapacityReservationGroupID != "" {
		virtualMachine.VirtualMachineProperties.CapacityReservation = &compute.CapacityReservationProfile{
			CapacityReservationGroup: &compute.SubResource{
				ID: &vmSpec.CapacityReservationGroupID,
			},
		}
	}

	return virtualMachine, nil
}

// Delete deletes the virtual network with the provided name.
func (s *Service) Delete(ctx context.Context, spec azure.Spec) error {
	vmSpec, ok := spec.(*Spec)
	if !ok {
		return errors.New("invalid vm Specification")
	}
	klog.V(2).Infof("deleting vm %s ", vmSpec.Name)
	future, err := s.Client.Delete(ctx, s.Scope.MachineConfig.ResourceGroup, vmSpec.Name, nil)
	if err != nil && azure.ResourceNotFound(err) {
		// already deleted
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete vm %s in resource group %s: %w", vmSpec.Name, s.Scope.MachineConfig.ResourceGroup, err)
	}

	// Do not wait until the operation completes. Just check the result
	// so the call to Delete actuator operation is async.
	_, err = future.Result(s.Client)
	if err != nil {
		return err
	}

	klog.V(2).Infof("successfully deleted vm %s ", vmSpec.Name)
	return err
}

// GenerateRandomString returns a URL-safe, base64 encoded
// securely generated random string.
// It will return an error if the system's secure random
// number generator fails to function correctly, in which
// case the caller should not continue.
func GenerateRandomString(n int) (string, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	// Note that err == nil only if we read len(b) bytes.
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), err
}

func availabilitySetID(subscriptionID, resourceGroup, availabilitySetName string) string {
	return fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/availabilitySets/%s", subscriptionID, resourceGroup, availabilitySetName)
}

func getSpotVMOptions(spotVMOptions *machinev1.SpotVMOptions) (compute.VirtualMachinePriorityTypes, compute.VirtualMachineEvictionPolicyTypes, *compute.BillingProfile, error) {
	// Spot VM not requested, return zero values to apply defaults
	if spotVMOptions == nil {
		return compute.VirtualMachinePriorityTypes(""), compute.VirtualMachineEvictionPolicyTypes(""), nil, nil
	}
	var billingProfile *compute.BillingProfile
	if spotVMOptions.MaxPrice != nil && spotVMOptions.MaxPrice.AsDec().String() != "" {
		maxPrice, err := strconv.ParseFloat(spotVMOptions.MaxPrice.AsDec().String(), 64)
		if err != nil {
			return compute.VirtualMachinePriorityTypes(""), compute.VirtualMachineEvictionPolicyTypes(""), nil, err
		}
		billingProfile = &compute.BillingProfile{
			MaxPrice: &maxPrice,
		}
	}

	// We should use deallocate eviction policy it's - "the only supported eviction policy for Single Instance Spot VMs"
	// https://github.com/openshift/enhancements/blob/master/enhancements/machine-api/spot-instances.md#eviction-policies
	return compute.VirtualMachinePriorityTypesSpot, compute.VirtualMachineEvictionPolicyTypesDeallocate, billingProfile, nil
}

func generateImagePlan(image machinev1.Image) *compute.Plan {
	// We only need a purchase plan for third-party marketplace images.
	if image.Type == "" || image.Type == machinev1.AzureImageTypeMarketplaceNoPlan {
		return nil
	}

	if image.Publisher == "" || image.SKU == "" || image.Offer == "" {
		return nil
	}

	return &compute.Plan{
		Publisher: to.StringPtr(image.Publisher),
		Name:      to.StringPtr(image.SKU),
		Product:   to.StringPtr(image.Offer),
	}
}

func generateOSDisk(vmSpec *Spec) *compute.OSDisk {
	osDisk := &compute.OSDisk{
		Name:         to.StringPtr(fmt.Sprintf("%s_OSDisk", vmSpec.Name)),
		OsType:       compute.OperatingSystemTypes(vmSpec.OSDisk.OSType),
		CreateOption: compute.DiskCreateOptionTypesFromImage,
		ManagedDisk:  &compute.ManagedDiskParameters{},
		DiskSizeGB:   to.Int32Ptr(vmSpec.OSDisk.DiskSizeGB),
	}

	if vmSpec.OSDisk.ManagedDisk.StorageAccountType != "" {
		osDisk.ManagedDisk.StorageAccountType = compute.StorageAccountTypes(vmSpec.OSDisk.ManagedDisk.StorageAccountType)
	}
	if vmSpec.OSDisk.ManagedDisk.DiskEncryptionSet != nil {
		osDisk.ManagedDisk.DiskEncryptionSet = &compute.DiskEncryptionSetParameters{ID: to.StringPtr(vmSpec.OSDisk.ManagedDisk.DiskEncryptionSet.ID)}
	}
	if vmSpec.OSDisk.ManagedDisk.SecurityProfile.SecurityEncryptionType != "" {
		osDisk.ManagedDisk.SecurityProfile = &compute.VMDiskSecurityProfile{}

		osDisk.ManagedDisk.SecurityProfile.SecurityEncryptionType = compute.SecurityEncryptionTypes(string(vmSpec.OSDisk.ManagedDisk.SecurityProfile.SecurityEncryptionType))

		if vmSpec.OSDisk.ManagedDisk.SecurityProfile.DiskEncryptionSet.ID != "" {
			osDisk.ManagedDisk.SecurityProfile.DiskEncryptionSet = &compute.DiskEncryptionSetParameters{ID: ptr.To[string](vmSpec.OSDisk.ManagedDisk.SecurityProfile.DiskEncryptionSet.ID)}
		}
	}

	return osDisk
}

func generateSecurityProfile(vmSpec *Spec, osDisk *compute.OSDisk) (*compute.SecurityProfile, error) {
	if vmSpec.SecurityProfile == nil {
		return nil, nil
	}

	securityProfile := &compute.SecurityProfile{
		EncryptionAtHost: vmSpec.SecurityProfile.EncryptionAtHost,
	}

	if osDisk.ManagedDisk != nil &&
		osDisk.ManagedDisk.SecurityProfile != nil &&
		osDisk.ManagedDisk.SecurityProfile.SecurityEncryptionType != "" {

		if vmSpec.SecurityProfile.Settings.SecurityType != machinev1.SecurityTypesConfidentialVM {
			return nil, apierrors.InvalidMachineConfiguration("failed to generate security profile for vm %s. "+
				"SecurityType should be set to %s when SecurityEncryptionType is defined.",
				vmSpec.Name, compute.SecurityTypesConfidentialVM)
		}

		if vmSpec.SecurityProfile.Settings.ConfidentialVM == nil {
			return nil, apierrors.InvalidMachineConfiguration("failed to generate security profile for vm %s. "+
				"UEFISettings should be set when SecurityEncryptionType is defined.", vmSpec.Name)
		}

		if vmSpec.SecurityProfile.Settings.ConfidentialVM.UEFISettings.VirtualizedTrustedPlatformModule != machinev1.VirtualizedTrustedPlatformModulePolicyEnabled {
			return nil, apierrors.InvalidMachineConfiguration("failed to generate security profile for vm %s. "+
				"VirtualizedTrustedPlatformModule should be enabled when SecurityEncryptionType is defined.", vmSpec.Name)
		}

		if osDisk.ManagedDisk.SecurityProfile.SecurityEncryptionType == compute.SecurityEncryptionTypesDiskWithVMGuestState {
			if vmSpec.SecurityProfile.EncryptionAtHost != nil && *vmSpec.SecurityProfile.EncryptionAtHost {
				return nil, apierrors.InvalidMachineConfiguration("failed to generate security profile for vm %s. "+
					"EncryptionAtHost cannot be set to true when SecurityEncryptionType is set to %s.",
					vmSpec.Name, compute.SecurityEncryptionTypesDiskWithVMGuestState)
			}
			if vmSpec.SecurityProfile.Settings.ConfidentialVM.UEFISettings.SecureBoot != machinev1.SecureBootPolicyEnabled {
				return nil, apierrors.InvalidMachineConfiguration("failed to generate security profile for vm %s. "+
					"SecureBoot should be enabled when SecurityEncryptionType is set to %s.",
					vmSpec.Name, compute.SecurityEncryptionTypesDiskWithVMGuestState)
			}
		}

		securityProfile.SecurityType = compute.SecurityTypesConfidentialVM

		securityProfile.UefiSettings = &compute.UefiSettings{
			SecureBootEnabled: ptr.To[bool](false),
			VTpmEnabled:       ptr.To[bool](true),
		}

		if vmSpec.SecurityProfile.Settings.ConfidentialVM.UEFISettings.SecureBoot == machinev1.SecureBootPolicyEnabled {
			securityProfile.UefiSettings.SecureBootEnabled = ptr.To[bool](true)
		}

		return securityProfile, nil
	}

	if vmSpec.SecurityProfile.Settings.SecurityType == machinev1.SecurityTypesTrustedLaunch && vmSpec.SecurityProfile.Settings.TrustedLaunch == nil {
		return nil, apierrors.InvalidMachineConfiguration("failed to generate security profile for vm %s. "+
			"UEFISettings should be set when SecurityType is set to %s.",
			vmSpec.Name, compute.SecurityTypesTrustedLaunch)
	}

	if vmSpec.SecurityProfile.Settings.TrustedLaunch != nil &&
		(vmSpec.SecurityProfile.Settings.TrustedLaunch.UEFISettings.SecureBoot == machinev1.SecureBootPolicyEnabled ||
			vmSpec.SecurityProfile.Settings.TrustedLaunch.UEFISettings.VirtualizedTrustedPlatformModule == machinev1.VirtualizedTrustedPlatformModulePolicyEnabled) {

		if vmSpec.SecurityProfile.Settings.SecurityType != machinev1.SecurityTypesTrustedLaunch {
			return nil, apierrors.InvalidMachineConfiguration("failed to generate security profile for vm %s. "+
				"SecurityType should be set to %s when UEFISettings are defined.",
				vmSpec.Name, compute.SecurityTypesTrustedLaunch)
		}

		securityProfile.SecurityType = compute.SecurityTypesTrustedLaunch

		securityProfile.UefiSettings = &compute.UefiSettings{
			SecureBootEnabled: ptr.To[bool](false),
			VTpmEnabled:       ptr.To[bool](false),
		}

		if vmSpec.SecurityProfile.Settings.TrustedLaunch.UEFISettings.SecureBoot == machinev1.SecureBootPolicyEnabled {
			securityProfile.UefiSettings.SecureBootEnabled = ptr.To[bool](true)
		}

		if vmSpec.SecurityProfile.Settings.TrustedLaunch.UEFISettings.VirtualizedTrustedPlatformModule == machinev1.VirtualizedTrustedPlatformModulePolicyEnabled {
			securityProfile.UefiSettings.VTpmEnabled = ptr.To[bool](true)
		}
	}

	return securityProfile, nil
}

func generateDataDisks(vmSpec *Spec) ([]compute.DataDisk, error) {
	seenDataDiskLuns := make(map[int32]struct{})
	seenDataDiskNames := make(map[string]struct{})
	// defines rules for matching. strings must start and finish with an alphanumeric character
	// and can only contain letters, numbers, underscores, periods or hyphens.
	reg := regexp.MustCompile(`^[a-zA-Z0-9](?:[\w\.-]*[a-zA-Z0-9])?$`)
	dataDisks := make([]compute.DataDisk, len(vmSpec.DataDisks))

	for i, disk := range vmSpec.DataDisks {
		dataDiskName := azure.GenerateDataDiskName(vmSpec.Name, disk.NameSuffix)

		if len(dataDiskName) > 80 {
			return nil, apierrors.InvalidMachineConfiguration("failed to create Data Disk: %s for vm %s. "+
				"The overall disk name name must not exceed 80 chars in length. Check your `nameSuffix`.",
				dataDiskName, vmSpec.Name)
		}

		if matched := reg.MatchString(disk.NameSuffix); !matched {
			return nil, apierrors.InvalidMachineConfiguration("failed to create Data Disk: %s for vm %s. "+
				"The nameSuffix can only contain letters, numbers, "+
				"underscores, periods or hyphens. Check your `nameSuffix`.",
				dataDiskName, vmSpec.Name)
		}

		if disk.DiskSizeGB < 4 {
			return nil, apierrors.InvalidMachineConfiguration("failed to create Data Disk: %s for vm %s. "+
				"`diskSizeGB`: %d, is invalid, disk size must be greater or equal than 4.",
				dataDiskName, vmSpec.Name, disk.DiskSizeGB)
		}

		if _, exists := seenDataDiskNames[disk.NameSuffix]; exists {
			return nil, apierrors.InvalidMachineConfiguration("failed to create Data Disk: %s for vm %s. "+
				"A Data Disk with `nameSuffix`: %s, already exists. `nameSuffix` must be unique.",
				dataDiskName, vmSpec.Name, disk.NameSuffix)
		}

		if (disk.ManagedDisk.StorageAccountType == machinev1.StorageAccountUltraSSDLRS) &&
			(disk.CachingType != machinev1.CachingTypeNone && disk.CachingType != "") {
			return nil, apierrors.InvalidMachineConfiguration("failed to create Data Disk: %s for vm %s. "+
				"`cachingType`: %s, is not supported for Data Disk of `storageAccountType`: \"%s\". "+
				"Use `storageAccountType`: \"%s\" instead.",
				dataDiskName, vmSpec.Name, disk.CachingType, machinev1.StorageAccountUltraSSDLRS, machinev1.CachingTypeNone)
		}

		if disk.Lun < 0 || disk.Lun > 63 {
			return nil, apierrors.InvalidMachineConfiguration("failed to create Data Disk: %s for vm %s. "+
				"Invalid value `lun`: %d. `lun` cannot be lower than 0 or higher than 63.",
				dataDiskName, vmSpec.Name, disk.Lun)
		}

		if _, exists := seenDataDiskLuns[disk.Lun]; exists {
			return nil, apierrors.InvalidMachineConfiguration("failed to create Data Disk: %s for vm %s. "+
				"A Data Disk with `lun`: %d, already exists. `lun` must be unique.",
				dataDiskName, vmSpec.Name, disk.Lun)
		}

		switch disk.DeletionPolicy {
		case machinev1.DiskDeletionPolicyTypeDelete, machinev1.DiskDeletionPolicyTypeDetach:
			// valid
		default:
			return nil, apierrors.InvalidMachineConfiguration("failed to create Data Disk: %s for vm %s. "+
				"Invalid value `deletionPolicy`: \"%s\". Valid values are \"%s\",\"%s\".",
				dataDiskName, vmSpec.Name, disk.DeletionPolicy, machinev1.DiskDeletionPolicyTypeDelete, machinev1.DiskDeletionPolicyTypeDetach)
		}

		seenDataDiskNames[disk.NameSuffix] = struct{}{}
		seenDataDiskLuns[disk.Lun] = struct{}{}

		dataDisks[i] = compute.DataDisk{
			CreateOption: compute.DiskCreateOptionTypesEmpty,
			DiskSizeGB:   to.Int32Ptr(disk.DiskSizeGB),
			Lun:          to.Int32Ptr(disk.Lun),
			Name:         to.StringPtr(dataDiskName),
			Caching:      compute.CachingTypes(disk.CachingType),
			DeleteOption: compute.DiskDeleteOptionTypes(disk.DeletionPolicy),
		}

		dataDisks[i].ManagedDisk = &compute.ManagedDiskParameters{
			StorageAccountType: compute.StorageAccountTypes(disk.ManagedDisk.StorageAccountType),
		}

		if disk.ManagedDisk.DiskEncryptionSet != nil {
			dataDisks[i].ManagedDisk.DiskEncryptionSet = &compute.DiskEncryptionSetParameters{
				ID: to.StringPtr(disk.ManagedDisk.DiskEncryptionSet.ID),
			}
		}
	}

	return dataDisks, nil
}
