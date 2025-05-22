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
	"errors"
	"fmt"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	apierrors "github.com/openshift/machine-api-operator/pkg/controller/machine"
	"regexp"

	"github.com/Azure/azure-sdk-for-go/profiles/2019-03-01/compute/mgmt/compute"
	"github.com/Azure/azure-sdk-for-go/profiles/2019-03-01/network/mgmt/network"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/networkinterfaces"
	"golang.org/x/crypto/ssh"
	"k8s.io/klog/v2"
)

// Get provides information about a virtual network.
func (s *StackHubService) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	vmSpec, ok := spec.(*Spec)
	if !ok {
		return compute.VirtualMachine{}, errors.New("invalid vm specification")
	}
	vm, err := s.Client.Get(ctx, s.Scope.MachineConfig.ResourceGroup, vmSpec.Name, compute.InstanceView)
	if err != nil && azure.ResourceNotFound(err) {
		klog.Warningf("vm %s not found: %v", vmSpec.Name, err)
		return nil, err
	} else if err != nil {
		return vm, err
	}
	return vm, nil
}

// CreateOrUpdate creates or updates a virtual network.
func (s *StackHubService) CreateOrUpdate(ctx context.Context, spec azure.Spec) error {
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
		return errors.New("error getting network security group3")
	}
	klog.V(2).Infof("got nic %s", vmSpec.NICName)

	klog.V(2).Infof("creating vm %s ", vmSpec.Name)

	virtualMachine, err := s.deriveVirtualMachineParametersStackHub(vmSpec, nic)
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

func generateOSProfileStackHub(vmSpec *Spec) (*compute.OSProfile, error) {
	sshKeyData := vmSpec.SSHKeyData
	if sshKeyData == "" && compute.OperatingSystemTypes(vmSpec.OSDisk.OSType) != compute.Windows {
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

	if compute.OperatingSystemTypes(vmSpec.OSDisk.OSType) == compute.Windows {
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
func (s *StackHubService) deriveVirtualMachineParametersStackHub(vmSpec *Spec, nic network.Interface) (*compute.VirtualMachine, error) {
	osProfile, err := generateOSProfileStackHub(vmSpec)
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

	dataDisks, err := generateStackHubDataDisks(vmSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate data disk spec: %w", err)
	}

	virtualMachine := &compute.VirtualMachine{
		Location: to.StringPtr(s.Scope.MachineConfig.Location),
		Tags:     vmSpec.Tags,
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
					},
				},
				DataDisks: dataDisks,
			},
			OsProfile: osProfile,
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
		},
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

	return virtualMachine, nil
}

// Delete deletes the virtual network with the provided name.
func (s *StackHubService) Delete(ctx context.Context, spec azure.Spec) error {
	vmSpec, ok := spec.(*Spec)
	if !ok {
		return errors.New("invalid vm Specification")
	}
	klog.V(2).Infof("deleting vm %s ", vmSpec.Name)
	future, err := s.Client.Delete(ctx, s.Scope.MachineConfig.ResourceGroup, vmSpec.Name)
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

func generateStackHubDataDisks(vmSpec *Spec) (*[]compute.DataDisk, error) {
	if len(vmSpec.DataDisks) == 0 {
		return nil, nil
	}

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

		if disk.ManagedDisk.StorageAccountType == machinev1.StorageAccountUltraSSDLRS {
			return nil, apierrors.InvalidMachineConfiguration("failed to create Data Disk: %s for vm %s. "+
				"`storageAccountType`: %s, is not supported for Data Disk in Azure Stack Hub.",
				dataDiskName, vmSpec.Name, machinev1.StorageAccountUltraSSDLRS, machinev1.CachingTypeNone)
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

		seenDataDiskNames[disk.NameSuffix] = struct{}{}
		seenDataDiskLuns[disk.Lun] = struct{}{}

		dataDisks[i] = compute.DataDisk{
			CreateOption: compute.DiskCreateOptionTypesEmpty,
			DiskSizeGB:   to.Int32Ptr(disk.DiskSizeGB),
			Lun:          to.Int32Ptr(disk.Lun),
			Name:         to.StringPtr(dataDiskName),
			Caching:      compute.CachingTypes(disk.CachingType),
		}

		dataDisks[i].ManagedDisk = &compute.ManagedDiskParameters{
			StorageAccountType: compute.StorageAccountTypes(disk.ManagedDisk.StorageAccountType),
		}
	}

	return &dataDisks, nil
}
