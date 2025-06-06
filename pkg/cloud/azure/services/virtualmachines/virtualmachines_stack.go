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
	"fmt"

	compute "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	"k8s.io/utils/ptr"
)

// Derive virtual machine parameters for CreateOrUpdate API call based
// on the provided virtual machine specification, resource location,
// subscription ID, and the network interface.
func (s *Service) deriveVirtualMachineParametersStackHub(vmSpec *Spec, nicID string) (*compute.VirtualMachine, error) {
	osProfile, err := generateOSProfile(vmSpec)
	if err != nil {
		return nil, err
	}

	imageReference := &compute.ImageReference{
		Publisher: ptr.To(vmSpec.Image.Publisher),
		Offer:     ptr.To(vmSpec.Image.Offer),
		SKU:       ptr.To(vmSpec.Image.SKU),
		Version:   ptr.To(vmSpec.Image.Version),
	}
	if vmSpec.Image.ResourceID != "" {
		imageReference = &compute.ImageReference{
			ID: ptr.To(fmt.Sprintf("/subscriptions/%s%s", s.Scope.SubscriptionID, vmSpec.Image.ResourceID)),
		}
	}

	dataDisks, err := generateDataDisks(vmSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate data disk spec: %w", err)
	}

	virtualMachine := &compute.VirtualMachine{
		Location: ptr.To(s.Scope.MachineConfig.Location),
		Tags:     vmSpec.Tags,
		Properties: &compute.VirtualMachineProperties{
			HardwareProfile: &compute.HardwareProfile{
				VMSize: ptr.To(compute.VirtualMachineSizeTypes(vmSpec.Size)),
			},
			StorageProfile: &compute.StorageProfile{
				ImageReference: imageReference,
				OSDisk: &compute.OSDisk{
					Name:         ptr.To(fmt.Sprintf("%s_OSDisk", vmSpec.Name)),
					OSType:       ptr.To(compute.OperatingSystemTypes(vmSpec.OSDisk.OSType)),
					CreateOption: ptr.To(compute.DiskCreateOptionTypesFromImage),
					DiskSizeGB:   ptr.To(vmSpec.OSDisk.DiskSizeGB),
					ManagedDisk: &compute.ManagedDiskParameters{
						StorageAccountType: ptr.To(compute.StorageAccountTypes(vmSpec.OSDisk.ManagedDisk.StorageAccountType)),
					},
				},
				DataDisks: dataDisks,
			},
			OSProfile: osProfile,
			NetworkProfile: &compute.NetworkProfile{
				NetworkInterfaces: []*compute.NetworkInterfaceReference{
					{
						ID: &nicID,
						Properties: &compute.NetworkInterfaceReferenceProperties{
							Primary: ptr.To(true),
						},
					},
				},
			},
		},
	}

	if vmSpec.Zone != "" {
		zones := []*string{&vmSpec.Zone}
		virtualMachine.Zones = zones
	}

	if vmSpec.AvailabilitySetName != "" {
		virtualMachine.Properties.AvailabilitySet = &compute.SubResource{
			ID: ptr.To(availabilitySetID(s.Scope.SubscriptionID, s.Scope.MachineConfig.ResourceGroup, vmSpec.AvailabilitySetName)),
		}
	}

	return virtualMachine, nil
}
