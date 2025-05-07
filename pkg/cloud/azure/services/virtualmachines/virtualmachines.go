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

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
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
	Priority                   armcompute.VirtualMachinePriorityTypes
	EvictionPolicy             armcompute.VirtualMachineEvictionPolicyTypes
	BillingProfile             *armcompute.BillingProfile
	SecurityProfile            *machinev1.SecurityProfile
	DiagnosticsProfile         *armcompute.DiagnosticsProfile
	UltraSSDCapability         machinev1.AzureUltraSSDCapabilityState
	AvailabilitySetName        string
	CapacityReservationGroupID string
}

// Get provides information about a virtual network.
func (s *Service) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	vmSpec, ok := spec.(*Spec)
	if !ok {
		return armcompute.VirtualMachine{}, errors.New("invalid vm specification")
	}
	vm, err := s.Client.Get(ctx, s.Scope.MachineConfig.ResourceGroup, vmSpec.Name, &armcompute.VirtualMachinesClientGetOptions{
		Expand: ptr.To(armcompute.InstanceViewTypesInstanceView),
	})
	if err != nil && azure.ResourceNotFound(err) {
		klog.Warningf("vm %s not found: %v", vmSpec.Name, err)
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

	if _, err := s.Client.BeginCreateOrUpdate(
		ctx,
		s.Scope.MachineConfig.ResourceGroup,
		vmSpec.Name,
		*virtualMachine, nil); err != nil {
		return fmt.Errorf("cannot create vm: %w", err)
	}

	// Do not wait on the returned poller so the call to Create actuator
	// operation is async.

	klog.V(2).Infof("successfully created vm %s ", vmSpec.Name)
	return err
}

func generateOSProfile(vmSpec *Spec) (*armcompute.OSProfile, error) {
	sshKeyData := vmSpec.SSHKeyData
	if sshKeyData == "" && armcompute.OperatingSystemTypes(vmSpec.OSDisk.OSType) != armcompute.OperatingSystemTypesWindows {
		privateKey, perr := rsa.GenerateKey(rand.Reader, 2048)
		if perr != nil {
			return nil, fmt.Errorf("failed to generate private key: %w", perr)
		}

		publicRsaKey, perr := ssh.NewPublicKey(&privateKey.PublicKey)
		if perr != nil {
			return nil, fmt.Errorf("failed to generate public key: %w", perr)
		}
		sshKeyData = string(ssh.MarshalAuthorizedKey(publicRsaKey))
	}

	randomPassword, err := GenerateRandomString(32)
	if err != nil {
		return nil, fmt.Errorf("failed to generate random string: %w", err)
	}

	osProfile := &armcompute.OSProfile{
		ComputerName:  to.StringPtr(vmSpec.Name),
		AdminUsername: to.StringPtr(azure.DefaultUserName),
		AdminPassword: to.StringPtr(randomPassword),
	}

	if armcompute.OperatingSystemTypes(vmSpec.OSDisk.OSType) == armcompute.OperatingSystemTypesWindows {
		osProfile.WindowsConfiguration = &armcompute.WindowsConfiguration{
			EnableAutomaticUpdates: to.BoolPtr(false),
			AdditionalUnattendContent: []*armcompute.AdditionalUnattendContent{
				{
					PassName:      ptr.To("OobeSystem"),
					ComponentName: ptr.To("Microsoft-Windows-Shell-Setup"),
					SettingName:   ptr.To(armcompute.SettingNamesAutoLogon),
					Content:       to.StringPtr(fmt.Sprintf(winAutoLogonFormatString, *osProfile.AdminUsername, *osProfile.AdminPassword)),
				},
				{
					PassName:      ptr.To("OobeSystem"),
					ComponentName: ptr.To("Microsoft-Windows-Shell-Setup"),
					SettingName:   ptr.To(armcompute.SettingNamesFirstLogonCommands),
					Content:       to.StringPtr(winFirstLogonCommandsString),
				},
			},
		}
	} else if sshKeyData != "" {
		osProfile.LinuxConfiguration = &armcompute.LinuxConfiguration{
			SSH: &armcompute.SSHConfiguration{
				PublicKeys: []*armcompute.SSHPublicKey{
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
func (s *Service) deriveVirtualMachineParameters(vmSpec *Spec, nic network.Interface) (*armcompute.VirtualMachine, error) {
	osProfile, err := generateOSProfile(vmSpec)
	if err != nil {
		return nil, err
	}

	imageReference := &armcompute.ImageReference{
		Publisher: to.StringPtr(vmSpec.Image.Publisher),
		Offer:     to.StringPtr(vmSpec.Image.Offer),
		SKU:       to.StringPtr(vmSpec.Image.SKU),
		Version:   to.StringPtr(vmSpec.Image.Version),
	}
	if vmSpec.Image.ResourceID != "" {
		imageReference = &armcompute.ImageReference{
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

	virtualMachine := &armcompute.VirtualMachine{
		Location: to.StringPtr(s.Scope.MachineConfig.Location),
		Tags:     vmSpec.Tags,
		Plan:     generateImagePlan(vmSpec.Image),
		Properties: &armcompute.VirtualMachineProperties{
			HardwareProfile: &armcompute.HardwareProfile{
				VMSize: ptr.To(armcompute.VirtualMachineSizeTypes(vmSpec.Size)),
			},
			StorageProfile: &armcompute.StorageProfile{
				ImageReference: imageReference,
				OSDisk:         osDisk,
				DataDisks:      dataDisks,
			},
			SecurityProfile: securityProfile,
			OSProfile:       osProfile,
			NetworkProfile: &armcompute.NetworkProfile{
				NetworkInterfaces: []*armcompute.NetworkInterfaceReference{
					{
						ID: nic.ID,
						Properties: &armcompute.NetworkInterfaceReferenceProperties{
							Primary: to.BoolPtr(true),
						},
					},
				},
			},
			Priority:           &priority,
			EvictionPolicy:     &evictionPolicy,
			BillingProfile:     billingProfile,
			DiagnosticsProfile: vmSpec.DiagnosticsProfile,
		},
	}

	// Ephemeral storage related options
	if vmSpec.OSDisk.CachingType != "" {
		virtualMachine.Properties.StorageProfile.OSDisk.Caching = ptr.To(armcompute.CachingTypes(vmSpec.OSDisk.CachingType))
	}
	if vmSpec.OSDisk.DiskSettings.EphemeralStorageLocation == "Local" {
		virtualMachine.Properties.StorageProfile.OSDisk.DiffDiskSettings = &armcompute.DiffDiskSettings{
			Option: ptr.To(armcompute.DiffDiskOptions(vmSpec.OSDisk.DiskSettings.EphemeralStorageLocation)),
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

	virtualMachine.Properties.AdditionalCapabilities = &armcompute.AdditionalCapabilities{
		UltraSSDEnabled: ultraSSDEnabled,
	}

	if vmSpec.ManagedIdentity != "" {
		virtualMachine.Identity = &armcompute.VirtualMachineIdentity{
			Type: ptr.To(armcompute.ResourceIdentityTypeUserAssigned),
			UserAssignedIdentities: map[string]*armcompute.UserAssignedIdentitiesValue{
				vmSpec.ManagedIdentity: &armcompute.UserAssignedIdentitiesValue{},
			},
		}
	}

	if vmSpec.Zone != "" {
		virtualMachine.Zones = []*string{&vmSpec.Zone}
	}

	if vmSpec.AvailabilitySetName != "" {
		virtualMachine.Properties.AvailabilitySet = &armcompute.SubResource{
			ID: to.StringPtr(availabilitySetID(s.Scope.SubscriptionID, s.Scope.MachineConfig.ResourceGroup, vmSpec.AvailabilitySetName)),
		}
	}

	// configure capacity reservation ID
	if vmSpec.CapacityReservationGroupID != "" {
		virtualMachine.Properties.CapacityReservation = &armcompute.CapacityReservationProfile{
			CapacityReservationGroup: &armcompute.SubResource{
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
	_, err := s.Client.BeginDelete(ctx, s.Scope.MachineConfig.ResourceGroup, vmSpec.Name, nil)
	if err != nil && azure.ResourceNotFound(err) {
		// already deleted
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete vm %s in resource group %s: %w", vmSpec.Name, s.Scope.MachineConfig.ResourceGroup, err)
	}

	// Do not wait on the returned poller so the call to Delete actuator
	// operation is async.

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

func getSpotVMOptions(spotVMOptions *machinev1.SpotVMOptions) (armcompute.VirtualMachinePriorityTypes, armcompute.VirtualMachineEvictionPolicyTypes, *armcompute.BillingProfile, error) {
	// Spot VM not requested, return zero values to apply defaults
	if spotVMOptions == nil {
		return armcompute.VirtualMachinePriorityTypes(""), armcompute.VirtualMachineEvictionPolicyTypes(""), nil, nil
	}
	var billingProfile *armcompute.BillingProfile
	if spotVMOptions.MaxPrice != nil && spotVMOptions.MaxPrice.AsDec().String() != "" {
		maxPrice, err := strconv.ParseFloat(spotVMOptions.MaxPrice.AsDec().String(), 64)
		if err != nil {
			return armcompute.VirtualMachinePriorityTypes(""), armcompute.VirtualMachineEvictionPolicyTypes(""), nil, err
		}
		billingProfile = &armcompute.BillingProfile{
			MaxPrice: &maxPrice,
		}
	}

	return armcompute.VirtualMachinePriorityTypesSpot, armcompute.VirtualMachineEvictionPolicyTypesDelete, billingProfile, nil
}

func generateImagePlan(image machinev1.Image) *armcompute.Plan {
	// We only need a purchase plan for third-party marketplace images.
	if image.Type == "" || image.Type == machinev1.AzureImageTypeMarketplaceNoPlan {
		return nil
	}

	if image.Publisher == "" || image.SKU == "" || image.Offer == "" {
		return nil
	}

	return &armcompute.Plan{
		Publisher: to.StringPtr(image.Publisher),
		Name:      to.StringPtr(image.SKU),
		Product:   to.StringPtr(image.Offer),
	}
}

func generateOSDisk(vmSpec *Spec) *armcompute.OSDisk {
	osDisk := &armcompute.OSDisk{
		Name:         to.StringPtr(fmt.Sprintf("%s_OSDisk", vmSpec.Name)),
		OSType:       ptr.To(armcompute.OperatingSystemTypes(vmSpec.OSDisk.OSType)),
		CreateOption: ptr.To(armcompute.DiskCreateOptionTypesFromImage),
		ManagedDisk:  &armcompute.ManagedDiskParameters{},
		DiskSizeGB:   to.Int32Ptr(vmSpec.OSDisk.DiskSizeGB),
	}

	if vmSpec.OSDisk.ManagedDisk.StorageAccountType != "" {
		osDisk.ManagedDisk.StorageAccountType = ptr.To(armcompute.StorageAccountTypes(vmSpec.OSDisk.ManagedDisk.StorageAccountType))
	}
	if vmSpec.OSDisk.ManagedDisk.DiskEncryptionSet != nil {
		osDisk.ManagedDisk.DiskEncryptionSet = &armcompute.DiskEncryptionSetParameters{ID: to.StringPtr(vmSpec.OSDisk.ManagedDisk.DiskEncryptionSet.ID)}
	}
	if vmSpec.OSDisk.ManagedDisk.SecurityProfile.SecurityEncryptionType != "" {
		osDisk.ManagedDisk.SecurityProfile = &armcompute.VMDiskSecurityProfile{}

		osDisk.ManagedDisk.SecurityProfile.SecurityEncryptionType = ptr.To(armcompute.SecurityEncryptionTypes(string(vmSpec.OSDisk.ManagedDisk.SecurityProfile.SecurityEncryptionType)))

		if vmSpec.OSDisk.ManagedDisk.SecurityProfile.DiskEncryptionSet.ID != "" {
			osDisk.ManagedDisk.SecurityProfile.DiskEncryptionSet = &armcompute.DiskEncryptionSetParameters{ID: ptr.To[string](vmSpec.OSDisk.ManagedDisk.SecurityProfile.DiskEncryptionSet.ID)}
		}
	}

	return osDisk
}

func generateSecurityProfile(vmSpec *Spec, osDisk *armcompute.OSDisk) (*armcompute.SecurityProfile, error) {
	if vmSpec.SecurityProfile == nil {
		return nil, nil
	}

	securityProfile := &armcompute.SecurityProfile{
		EncryptionAtHost: vmSpec.SecurityProfile.EncryptionAtHost,
	}

	if osDisk.ManagedDisk != nil &&
		osDisk.ManagedDisk.SecurityProfile != nil &&
		osDisk.ManagedDisk.SecurityProfile.SecurityEncryptionType != nil {

		if vmSpec.SecurityProfile.Settings.SecurityType != machinev1.SecurityTypesConfidentialVM {
			return nil, apierrors.InvalidMachineConfiguration("failed to generate security profile for vm %s. "+
				"SecurityType should be set to %s when SecurityEncryptionType is defined.",
				vmSpec.Name, armcompute.SecurityTypesConfidentialVM)
		}

		if vmSpec.SecurityProfile.Settings.ConfidentialVM == nil {
			return nil, apierrors.InvalidMachineConfiguration("failed to generate security profile for vm %s. "+
				"UEFISettings should be set when SecurityEncryptionType is defined.", vmSpec.Name)
		}

		if vmSpec.SecurityProfile.Settings.ConfidentialVM.UEFISettings.VirtualizedTrustedPlatformModule != machinev1.VirtualizedTrustedPlatformModulePolicyEnabled {
			return nil, apierrors.InvalidMachineConfiguration("failed to generate security profile for vm %s. "+
				"VirtualizedTrustedPlatformModule should be enabled when SecurityEncryptionType is defined.", vmSpec.Name)
		}

		if ptr.Deref(osDisk.ManagedDisk.SecurityProfile.SecurityEncryptionType, "") == armcompute.SecurityEncryptionTypesDiskWithVMGuestState {
			if vmSpec.SecurityProfile.EncryptionAtHost != nil && *vmSpec.SecurityProfile.EncryptionAtHost {
				return nil, apierrors.InvalidMachineConfiguration("failed to generate security profile for vm %s. "+
					"EncryptionAtHost cannot be set to true when SecurityEncryptionType is set to %s.",
					vmSpec.Name, armcompute.SecurityEncryptionTypesDiskWithVMGuestState)
			}
			if vmSpec.SecurityProfile.Settings.ConfidentialVM.UEFISettings.SecureBoot != machinev1.SecureBootPolicyEnabled {
				return nil, apierrors.InvalidMachineConfiguration("failed to generate security profile for vm %s. "+
					"SecureBoot should be enabled when SecurityEncryptionType is set to %s.",
					vmSpec.Name, armcompute.SecurityEncryptionTypesDiskWithVMGuestState)
			}
		}

		securityProfile.SecurityType = ptr.To(armcompute.SecurityTypesConfidentialVM)

		securityProfile.UefiSettings = &armcompute.UefiSettings{
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
			vmSpec.Name, armcompute.SecurityTypesTrustedLaunch)
	}

	if vmSpec.SecurityProfile.Settings.TrustedLaunch != nil &&
		(vmSpec.SecurityProfile.Settings.TrustedLaunch.UEFISettings.SecureBoot == machinev1.SecureBootPolicyEnabled ||
			vmSpec.SecurityProfile.Settings.TrustedLaunch.UEFISettings.VirtualizedTrustedPlatformModule == machinev1.VirtualizedTrustedPlatformModulePolicyEnabled) {

		if vmSpec.SecurityProfile.Settings.SecurityType != machinev1.SecurityTypesTrustedLaunch {
			return nil, apierrors.InvalidMachineConfiguration("failed to generate security profile for vm %s. "+
				"SecurityType should be set to %s when UEFISettings are defined.",
				vmSpec.Name, armcompute.SecurityTypesTrustedLaunch)
		}

		securityProfile.SecurityType = ptr.To(armcompute.SecurityTypesTrustedLaunch)

		securityProfile.UefiSettings = &armcompute.UefiSettings{
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

func generateDataDisks(vmSpec *Spec) ([]*armcompute.DataDisk, error) {
	seenDataDiskLuns := make(map[int32]struct{})
	seenDataDiskNames := make(map[string]struct{})
	// defines rules for matching. strings must start and finish with an alphanumeric character
	// and can only contain letters, numbers, underscores, periods or hyphens.
	reg := regexp.MustCompile(`^[a-zA-Z0-9](?:[\w\.-]*[a-zA-Z0-9])?$`)
	dataDisks := make([]*armcompute.DataDisk, len(vmSpec.DataDisks))

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

		dataDisks[i] = &armcompute.DataDisk{
			CreateOption: ptr.To(armcompute.DiskCreateOptionTypesEmpty),
			DiskSizeGB:   to.Int32Ptr(disk.DiskSizeGB),
			Lun:          to.Int32Ptr(disk.Lun),
			Name:         to.StringPtr(dataDiskName),
			Caching:      ptr.To(armcompute.CachingTypes(disk.CachingType)),
			DeleteOption: ptr.To(armcompute.DiskDeleteOptionTypes(disk.DeletionPolicy)),
		}

		dataDisks[i].ManagedDisk = &armcompute.ManagedDiskParameters{
			StorageAccountType: ptr.To(armcompute.StorageAccountTypes(disk.ManagedDisk.StorageAccountType)),
		}

		if disk.ManagedDisk.DiskEncryptionSet != nil {
			dataDisks[i].ManagedDisk.DiskEncryptionSet = &armcompute.DiskEncryptionSetParameters{
				ID: to.StringPtr(disk.ManagedDisk.DiskEncryptionSet.ID),
			}
		}
	}

	return dataDisks, nil
}
