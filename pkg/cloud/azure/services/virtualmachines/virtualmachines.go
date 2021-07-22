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

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2021-03-01/compute"
	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2021-02-01/network"
	"github.com/Azure/go-autorest/autorest/to"
	"golang.org/x/crypto/ssh"
	"k8s.io/klog/v2"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/apis/azureprovider/v1beta1"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/networkinterfaces"
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
	Name                string
	NICName             string
	SSHKeyData          string
	Size                string
	Zone                string
	Image               v1beta1.Image
	OSDisk              v1beta1.OSDisk
	CustomData          string
	ManagedIdentity     string
	Tags                map[string]string
	Priority            compute.VirtualMachinePriorityTypes
	EvictionPolicy      compute.VirtualMachineEvictionPolicyTypes
	BillingProfile      *compute.BillingProfile
	SecurityProfile     *v1beta1.SecurityProfile
	AvailabilitySetName string
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

	virtualMachine, err := deriveVirtualMachineParameters(vmSpec, s.Scope.MachineConfig.Location, s.Scope.SubscriptionID, s.Scope.MachineConfig.ResourceGroup, nic)
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
func deriveVirtualMachineParameters(vmSpec *Spec, location, subscription, resourceGroup string, nic network.Interface) (*compute.VirtualMachine, error) {
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
			ID: to.StringPtr(fmt.Sprintf("/subscriptions/%s%s", subscription, vmSpec.Image.ResourceID)),
		}
	}

	var diskEncryptionSet *compute.DiskEncryptionSetParameters
	if vmSpec.OSDisk.ManagedDisk.DiskEncryptionSet != nil {
		diskEncryptionSet = &compute.DiskEncryptionSetParameters{ID: to.StringPtr(vmSpec.OSDisk.ManagedDisk.DiskEncryptionSet.ID)}
	}

	var securityProfile *compute.SecurityProfile
	if vmSpec.SecurityProfile != nil {
		securityProfile = &compute.SecurityProfile{
			EncryptionAtHost: vmSpec.SecurityProfile.EncryptionAtHost,
		}
	}

	virtualMachine := &compute.VirtualMachine{
		Location: to.StringPtr(location),
		Tags:     getTagListFromSpec(vmSpec),
		VirtualMachineProperties: &compute.VirtualMachineProperties{
			HardwareProfile: &compute.HardwareProfile{
				VMSize: compute.VirtualMachineSizeTypes(vmSpec.Size),
			},
			StorageProfile: &compute.StorageProfile{
				ImageReference: imageReference,
				OsDisk: &compute.OSDisk{
					Name:         to.StringPtr(fmt.Sprintf("%s_OSDisk", vmSpec.Name)),
					OsType:       compute.OperatingSystemTypes(vmSpec.OSDisk.OSType),
					CreateOption: compute.DiskCreateOptionTypesFromImage,
					DiskSizeGB:   to.Int32Ptr(vmSpec.OSDisk.DiskSizeGB),
					ManagedDisk: &compute.ManagedDiskParameters{
						StorageAccountType: compute.StorageAccountTypes(vmSpec.OSDisk.ManagedDisk.StorageAccountType),
						DiskEncryptionSet:  diskEncryptionSet,
					},
				},
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
			Priority:       vmSpec.Priority,
			EvictionPolicy: vmSpec.EvictionPolicy,
			BillingProfile: vmSpec.BillingProfile,
		},
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
			ID: to.StringPtr(availabilitySetID(subscription, resourceGroup, vmSpec.AvailabilitySetName)),
		}
	}

	return virtualMachine, nil
}

func getTagListFromSpec(spec *Spec) map[string]*string {
	if len(spec.Tags) < 1 {
		return nil
	}

	tagList := map[string]*string{}
	for key, element := range spec.Tags {
		tagList[key] = to.StringPtr(element)
	}
	return tagList
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
