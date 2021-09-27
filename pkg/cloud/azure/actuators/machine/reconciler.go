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

package machine

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2021-03-01/compute"
	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2021-02-01/network"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/to"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	machinecontroller "github.com/openshift/machine-api-operator/pkg/controller/machine"
	"github.com/openshift/machine-api-operator/pkg/metrics"
	apicorev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/apis/azureprovider/v1beta1"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/actuators"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/actuators/machineset"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/availabilitysets"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/availabilityzones"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/disks"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/networkinterfaces"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/publicips"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/virtualmachineextensions"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/virtualmachines"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// DefaultBootstrapTokenTTL default ttl for bootstrap token
	DefaultBootstrapTokenTTL = 10 * time.Minute

	// MachineRegionLabelName as annotation name for a machine region
	MachineRegionLabelName = "machine.openshift.io/region"

	// MachineAZLabelName as annotation name for a machine AZ
	MachineAZLabelName = "machine.openshift.io/zone"

	// MachineInstanceStateAnnotationName as annotation name for a machine instance state
	MachineInstanceStateAnnotationName = "machine.openshift.io/instance-state"

	// MachineInstanceTypeLabelName as annotation name for a machine instance type
	MachineInstanceTypeLabelName = "machine.openshift.io/instance-type"

	MachineSetLabelName = "machine.openshift.io/cluster-api-machineset"
)

// Reconciler are list of services required by cluster actuator, easy to create a fake
type Reconciler struct {
	scope                 *actuators.MachineScope
	availabilityZonesSvc  azure.Service
	networkInterfacesSvc  azure.Service
	publicIPSvc           azure.Service
	virtualMachinesSvc    azure.Service
	virtualMachinesExtSvc azure.Service
	disksSvc              azure.Service
	availabilitySetsSvc   azure.Service
}

// NewReconciler populates all the services based on input scope
func NewReconciler(scope *actuators.MachineScope) *Reconciler {
	return &Reconciler{
		scope:                 scope,
		availabilityZonesSvc:  availabilityzones.NewService(scope),
		networkInterfacesSvc:  networkinterfaces.NewService(scope),
		virtualMachinesSvc:    virtualmachines.NewService(scope),
		virtualMachinesExtSvc: virtualmachineextensions.NewService(scope),
		publicIPSvc:           publicips.NewService(scope),
		disksSvc:              disks.NewService(scope),
		availabilitySetsSvc:   availabilitysets.NewService(scope),
	}
}

// Create creates machine if and only if machine exists, handled by cluster-api
func (s *Reconciler) Create(ctx context.Context) error {
	if err := s.CreateMachine(ctx); err != nil {
		s.scope.MachineStatus.Conditions = setMachineProviderCondition(s.scope.MachineStatus.Conditions, v1beta1.AzureMachineProviderCondition{
			Type:    v1beta1.MachineCreated,
			Status:  apicorev1.ConditionTrue,
			Reason:  machineCreationFailedReason,
			Message: err.Error(),
		})
		return err
	}
	return nil
}

// CreateMachine creates machine if and only if machine exists, handled by cluster-api
func (s *Reconciler) CreateMachine(ctx context.Context) error {
	// TODO: update once machine controllers have a way to indicate a machine has been provisoned. https://github.com/kubernetes-sigs/cluster-api/issues/253
	// Seeing a node cannot be purely relied upon because the provisioned control plane will not be registering with
	// the stack that provisions it.
	if s.scope.Machine.Annotations == nil {
		s.scope.Machine.Annotations = map[string]string{}
	}

	nicName := azure.GenerateNetworkInterfaceName(s.scope.Machine.Name)
	if err := s.createNetworkInterface(ctx, nicName); err != nil {
		return fmt.Errorf("failed to create nic %s for machine %s: %w", nicName, s.scope.Machine.Name, err)
	}

	// Availibility set will be created only if no zones were found for a machine
	asName, err := s.createAvailabilitySet()
	if err != nil {
		return fmt.Errorf("failed to create availability set %s for machine %s: %w", asName, s.scope.Machine.Name, err)
	}

	if err := s.createVirtualMachine(ctx, nicName, asName); err != nil {
		return fmt.Errorf("failed to create vm %s: %w", s.scope.Machine.Name, err)
	}

	return nil
}

// Update updates machine if and only if machine exists, handled by cluster-api
func (s *Reconciler) Update(ctx context.Context) error {
	vmSpec := &virtualmachines.Spec{
		Name: s.scope.Machine.Name,
	}
	vmInterface, err := s.virtualMachinesSvc.Get(ctx, vmSpec)
	if err != nil {
		return fmt.Errorf("failed to get vm: %+v", err)
	}

	vm, ok := vmInterface.(compute.VirtualMachine)
	if !ok {
		return errors.New("returned incorrect vm interface")
	}

	// TODO: Uncomment after implementing tagging.
	// Ensure that the tags are correct.
	/*
		_, err = a.ensureTags(computeSvc, machine, scope.MachineStatus.VMID, scope.MachineConfig.AdditionalTags)
		if err != nil {
			return fmt.Errorf("failed to ensure tags: %+v", err)
		}
	*/

	networkAddresses := []apicorev1.NodeAddress{}

	// The computer name for a VM instance is the hostname of the VM
	// TODO(jchaloup): find a way how to propagete the hostname change in case
	// someone/something changes the hostname inside the VM
	if vm.OsProfile != nil && vm.OsProfile.ComputerName != nil {
		networkAddresses = append(networkAddresses, apicorev1.NodeAddress{
			Type:    apicorev1.NodeHostName,
			Address: *vm.OsProfile.ComputerName,
		})

		// csr approved requires node internal dns name to be equal to a node name
		networkAddresses = append(networkAddresses, apicorev1.NodeAddress{
			Type:    apicorev1.NodeInternalDNS,
			Address: *vm.OsProfile.ComputerName,
		})
	}

	if vm.NetworkProfile != nil && vm.NetworkProfile.NetworkInterfaces != nil {
		if s.scope.MachineConfig.Vnet == "" {
			return fmt.Errorf("MachineConfig vnet is missing on machine %s", s.scope.Machine.Name)
		}

		for _, iface := range *vm.NetworkProfile.NetworkInterfaces {
			// Get iface name from the ID
			ifaceName := path.Base(*iface.ID)
			networkIface, err := s.networkInterfacesSvc.Get(ctx, &networkinterfaces.Spec{
				Name:     ifaceName,
				VnetName: s.scope.MachineConfig.Vnet,
			})
			if err != nil {
				klog.Errorf("Unable to get %q network interface: %v", ifaceName, err)
				continue
			}

			niface, ok := networkIface.(network.Interface)
			if !ok {
				klog.Errorf("Network interfaces get returned invalid network interface, getting %T instead", networkIface)
				continue
			}

			// Internal dns name consists of a hostname and internal dns suffix
			if niface.InterfacePropertiesFormat.DNSSettings != nil && niface.InterfacePropertiesFormat.DNSSettings.InternalDomainNameSuffix != nil && vm.OsProfile != nil && vm.OsProfile.ComputerName != nil {
				networkAddresses = append(networkAddresses, apicorev1.NodeAddress{
					Type:    apicorev1.NodeInternalDNS,
					Address: fmt.Sprintf("%s.%s", *vm.OsProfile.ComputerName, *niface.InterfacePropertiesFormat.DNSSettings.InternalDomainNameSuffix),
				})
			}

			if niface.InterfacePropertiesFormat.IPConfigurations == nil {
				continue
			}

			for _, ipConfig := range *niface.InterfacePropertiesFormat.IPConfigurations {
				if ipConfig.PrivateIPAddress != nil {
					networkAddresses = append(networkAddresses, apicorev1.NodeAddress{
						Type:    apicorev1.NodeInternalIP,
						Address: *ipConfig.PrivateIPAddress,
					})
				}

				if ipConfig.PublicIPAddress != nil && ipConfig.PublicIPAddress.ID != nil {
					publicIPInterface, publicIPErr := s.publicIPSvc.Get(ctx, &publicips.Spec{Name: path.Base(*ipConfig.PublicIPAddress.ID)})
					if publicIPErr != nil {
						klog.Errorf("Unable to get %q public IP: %v", path.Base(*ipConfig.PublicIPAddress.ID), publicIPErr)
						continue
					}

					ip, ok := publicIPInterface.(network.PublicIPAddress)
					if !ok {
						klog.Errorf("Public ip get returned invalid network interface, getting %T instead", publicIPInterface)
						continue
					}

					if ip.IPAddress != nil {
						networkAddresses = append(networkAddresses, apicorev1.NodeAddress{
							Type:    apicorev1.NodeExternalIP,
							Address: *ip.IPAddress,
						})
					}

					if ip.DNSSettings != nil && ip.DNSSettings.Fqdn != nil {
						networkAddresses = append(networkAddresses, apicorev1.NodeAddress{
							Type:    apicorev1.NodeExternalDNS,
							Address: *ip.DNSSettings.Fqdn,
						})
					}
				}
			}
		}
	}

	s.scope.Machine.Status.Addresses = networkAddresses

	// Set provider ID
	if vm.OsProfile != nil && vm.OsProfile.ComputerName != nil {
		providerID := azure.GenerateMachineProviderID(
			s.scope.SubscriptionID,
			s.scope.MachineConfig.ResourceGroup,
			*vm.OsProfile.ComputerName)
		s.scope.Machine.Spec.ProviderID = &providerID
	} else {
		klog.Warningf("Unable to set providerID, not able to get vm.OsProfile.ComputerName. Setting ProviderID to nil.")
		s.scope.Machine.Spec.ProviderID = nil
	}

	// Set instance conditions
	s.scope.MachineStatus.Conditions = setMachineProviderCondition(s.scope.MachineStatus.Conditions, v1beta1.AzureMachineProviderCondition{
		Type:    v1beta1.MachineCreated,
		Status:  apicorev1.ConditionTrue,
		Reason:  machineCreationSucceedReason,
		Message: machineCreationSucceedMessage,
	})

	vmState := getVMState(vm)
	s.scope.MachineStatus.VMID = vm.ID
	s.scope.MachineStatus.VMState = &vmState

	s.setMachineCloudProviderSpecifics(vm)

	return nil
}

func getVMState(vm compute.VirtualMachine) v1beta1.VMState {
	if vm.VirtualMachineProperties == nil || vm.ProvisioningState == nil {
		return ""
	}

	if *vm.ProvisioningState != "Succeeded" {
		return v1beta1.VMState(*vm.ProvisioningState)
	}

	if vm.InstanceView == nil || vm.InstanceView.Statuses == nil {
		return ""
	}

	// https://docs.microsoft.com/en-us/java/api/com.azure.resourcemanager.compute.models.powerstate
	for _, status := range *vm.InstanceView.Statuses {
		if status.Code == nil {
			continue
		}
		switch *status.Code {
		case "ProvisioningState/succeeded":
			continue
		case "PowerState/starting":
			return v1beta1.VMStateStarting
		case "PowerState/running":
			return v1beta1.VMStateRunning
		case "PowerState/stopping":
			return v1beta1.VMStateStopping
		case "PowerState/stopped":
			return v1beta1.VMStateStopped
		case "PowerState/deallocating":
			return v1beta1.VMStateDeallocating
		case "PowerState/deallocated":
			return v1beta1.VMStateDeallocated
		default:
			return v1beta1.VMStateUnknown
		}
	}

	return ""
}

func (s *Reconciler) setMachineCloudProviderSpecifics(vm compute.VirtualMachine) {
	if s.scope.Machine.Labels == nil {
		s.scope.Machine.Labels = make(map[string]string)
	}

	if s.scope.Machine.Annotations == nil {
		s.scope.Machine.Annotations = make(map[string]string)
	}

	s.scope.Machine.Annotations[MachineInstanceStateAnnotationName] = string(getVMState(vm))

	if vm.VirtualMachineProperties != nil {
		if vm.VirtualMachineProperties.HardwareProfile != nil {
			s.scope.Machine.Labels[MachineInstanceTypeLabelName] = string(vm.VirtualMachineProperties.HardwareProfile.VMSize)
		}
	}

	if vm.Location != nil {
		s.scope.Machine.Labels[MachineRegionLabelName] = *vm.Location
	}
	if vm.Zones != nil {
		s.scope.Machine.Labels[MachineAZLabelName] = strings.Join(*vm.Zones, ",")
	}

	if s.scope.MachineConfig.SpotVMOptions != nil {
		// Label on the Machine so that an MHC can select spot instances
		s.scope.Machine.Labels[machinecontroller.MachineInterruptibleInstanceLabelName] = ""

		if s.scope.Machine.Spec.Labels == nil {
			s.scope.Machine.Spec.Labels = make(map[string]string)
		}
		// Label on the Spec so that it is propogated to the Node
		s.scope.Machine.Spec.Labels[machinecontroller.MachineInterruptibleInstanceLabelName] = ""
	}
}

// Exists checks if machine exists
func (s *Reconciler) Exists(ctx context.Context) (bool, error) {
	vmSpec := &virtualmachines.Spec{
		Name: s.scope.Name(),
	}
	vmInterface, err := s.virtualMachinesSvc.Get(ctx, vmSpec)

	if err != nil && azure.ResourceNotFound(err) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("Failed to get vm: %w", err)
	}

	vm, ok := vmInterface.(compute.VirtualMachine)
	if !ok {
		return false, fmt.Errorf("returned incorrect vm interface: %T", vmInterface)
	}

	if s.scope.MachineConfig.UserDataSecret == nil {
		vmExtSpec := &virtualmachineextensions.Spec{
			Name:   "startupScript",
			VMName: s.scope.Name(),
		}

		vmExt, err := s.virtualMachinesExtSvc.Get(ctx, vmExtSpec)
		if err != nil && vmExt == nil {
			return false, nil
		}

		if err != nil {
			return false, fmt.Errorf("failed to get vm extension: %w", err)
		}
	}

	switch v1beta1.VMState(*vm.ProvisioningState) {
	case v1beta1.VMStateDeleting:
		return true, fmt.Errorf("vm for machine %s has unexpected 'Deleting' provisioning state", s.scope.Machine.GetName())
	case v1beta1.VMStateFailed:
		return true, fmt.Errorf("vm for machine %s exists, but has unexpected 'Failed' provisioning state", s.scope.Machine.GetName())
	}

	klog.Infof("Provisioning state is '%s' for machine %s", *vm.ProvisioningState, s.scope.Machine.GetName())
	return true, nil
}

// Delete reconciles all the services in pre determined order
func (s *Reconciler) Delete(ctx context.Context) error {
	vmSpec := &virtualmachines.Spec{
		Name: s.scope.Machine.Name,
	}

	// Getting a vm object does not work here so let's assume
	// an instance is really being deleted
	if s.scope.Machine.Annotations == nil {
		s.scope.Machine.Annotations = make(map[string]string)
	}
	s.scope.Machine.Annotations[MachineInstanceStateAnnotationName] = string(v1beta1.VMStateDeleting)
	s.scope.MachineStatus.VMState = &v1beta1.VMStateDeleting

	err := s.virtualMachinesSvc.Delete(ctx, vmSpec)
	if err != nil {
		return fmt.Errorf("failed to delete machine: %w", err)
	}

	osDiskSpec := &disks.Spec{
		Name: azure.GenerateOSDiskName(s.scope.Machine.Name),
	}
	err = s.disksSvc.Delete(ctx, osDiskSpec)
	if err != nil {
		metrics.RegisterFailedInstanceDelete(&metrics.MachineLabels{
			Name:      s.scope.Machine.Name,
			Namespace: s.scope.Machine.Namespace,
			Reason:    err.Error(),
		})
		return fmt.Errorf("failed to delete OS disk: %w", err)
	}

	if s.scope.MachineConfig.Vnet == "" {
		return fmt.Errorf("MachineConfig vnet is missing on machine %s", s.scope.Machine.Name)
	}

	networkInterfaceSpec := &networkinterfaces.Spec{
		Name:     azure.GenerateNetworkInterfaceName(s.scope.Machine.Name),
		VnetName: s.scope.MachineConfig.Vnet,
	}

	err = s.networkInterfacesSvc.Delete(ctx, networkInterfaceSpec)
	if err != nil {
		metrics.RegisterFailedInstanceDelete(&metrics.MachineLabels{
			Name:      s.scope.Machine.Name,
			Namespace: s.scope.Machine.Namespace,
			Reason:    err.Error(),
		})
		return fmt.Errorf("Unable to delete network interface: %w", err)
	}

	if s.scope.MachineConfig.PublicIP {
		publicIPName, err := azure.GenerateMachinePublicIPName(s.scope.MachineConfig.Name, s.scope.Machine.Name)
		if err != nil {
			// Only when the generated name is longer than allowed by the Azure portal
			// That can happen only when
			// - machine name is changed (can't happen without creating a new CR)
			// - cluster name is changed (could happen but then we will get different name anyway)
			// - machine CR was created with too long public ip name (in which case no instance was created)
			klog.Info("Generated public IP name was too long, skipping deletion of the resource")
			return nil
		}

		err = s.publicIPSvc.Delete(ctx, &publicips.Spec{
			Name: publicIPName,
		})
		if err != nil {
			metrics.RegisterFailedInstanceDelete(&metrics.MachineLabels{
				Name:      s.scope.Machine.Name,
				Namespace: s.scope.Machine.Namespace,
				Reason:    err.Error(),
			})
			return fmt.Errorf("unable to delete Public IP: %w", err)
		}
	}

	// Delete the availability set with the given name if no virtual machines are attached to it.
	if err := s.availabilitySetsSvc.Delete(ctx, &availabilitysets.Spec{
		Name: s.getAvailibilitySetName(),
	}); err != nil {
		return fmt.Errorf("failed to delete availability set: %w", err)
	}

	return nil
}

func (s *Reconciler) getZone(ctx context.Context) (string, error) {
	return to.String(s.scope.MachineConfig.Zone), nil
}

func (s *Reconciler) createNetworkInterface(ctx context.Context, nicName string) error {
	if s.scope.MachineConfig.Vnet == "" {
		return machinecontroller.InvalidMachineConfiguration("MachineConfig vnet is missing on machine %s", s.scope.Machine.Name)
	}
	networkInterfaceSpec := &networkinterfaces.Spec{
		Name:     nicName,
		VnetName: s.scope.MachineConfig.Vnet,
	}
	if s.scope.MachineConfig.AcceleratedNetworking {
		if !machineset.InstanceTypes[s.scope.MachineConfig.VMSize].AcceleratedNetworking {
			return machinecontroller.InvalidMachineConfiguration("accelerated networking not supported on instance type: %v", s.scope.MachineConfig.VMSize)
		}
		networkInterfaceSpec.AcceleratedNetworking = s.scope.MachineConfig.AcceleratedNetworking
	}

	if s.scope.MachineConfig.Subnet == "" {
		return machinecontroller.InvalidMachineConfiguration("MachineConfig subnet is missing on machine %s, skipping machine creation", s.scope.Machine.Name)
	}

	networkInterfaceSpec.SubnetName = s.scope.MachineConfig.Subnet

	if s.scope.MachineConfig.PublicLoadBalancer != "" {
		networkInterfaceSpec.PublicLoadBalancerName = s.scope.MachineConfig.PublicLoadBalancer
		if s.scope.MachineConfig.NatRule != nil {
			networkInterfaceSpec.NatRule = s.scope.MachineConfig.NatRule
		}
	}
	if s.scope.MachineConfig.InternalLoadBalancer != "" {
		networkInterfaceSpec.InternalLoadBalancerName = s.scope.MachineConfig.InternalLoadBalancer
	}

	if s.scope.MachineConfig.SecurityGroup != "" {
		networkInterfaceSpec.SecurityGroupName = s.scope.MachineConfig.SecurityGroup
	}

	if len(s.scope.MachineConfig.ApplicationSecurityGroups) > 0 {
		networkInterfaceSpec.ApplicationSecurityGroupNames = s.scope.MachineConfig.ApplicationSecurityGroups
	}

	if s.scope.MachineConfig.PublicIP {
		publicIPName, err := azure.GenerateMachinePublicIPName(s.scope.MachineConfig.Name, s.scope.Machine.Name)
		if err != nil {
			return machinecontroller.InvalidMachineConfiguration("unable to create Public IP: %v", err)
		}
		err = s.publicIPSvc.CreateOrUpdate(ctx, &publicips.Spec{Name: publicIPName})
		if err != nil {
			metrics.RegisterFailedInstanceCreate(&metrics.MachineLabels{
				Name:      s.scope.Machine.Name,
				Namespace: s.scope.Machine.Namespace,
				Reason:    err.Error(),
			})
			return fmt.Errorf("unable to create Public IP: %w", err)
		}
		networkInterfaceSpec.PublicIP = publicIPName
	}

	err := s.networkInterfacesSvc.CreateOrUpdate(ctx, networkInterfaceSpec)
	if err != nil {
		metrics.RegisterFailedInstanceCreate(&metrics.MachineLabels{
			Name:      s.scope.Machine.Name,
			Namespace: s.scope.Machine.Namespace,
			Reason:    err.Error(),
		})
		return fmt.Errorf("unable to create VM network interface: %w", err)
	}

	return err
}

func (s *Reconciler) createVirtualMachine(ctx context.Context, nicName, asName string) error {
	decoded, err := base64.StdEncoding.DecodeString(s.scope.MachineConfig.SSHPublicKey)
	if err != nil {
		return fmt.Errorf("failed to decode ssh public key: %w", err)
	}

	priority, evictionPolicy, billingProfile, err := getSpotVMOptions(s.scope.MachineConfig.SpotVMOptions)
	if err != nil {
		return fmt.Errorf("failed to get Spot VM options %w", err)
	}

	vmSpec := &virtualmachines.Spec{
		Name: s.scope.Machine.Name,
	}

	vmInterface, err := s.virtualMachinesSvc.Get(ctx, vmSpec)
	if err != nil && vmInterface == nil {
		zone, err := s.getZone(ctx)
		if err != nil {
			return fmt.Errorf("failed to get zone: %w", err)
		}

		if s.scope.Machine.Labels == nil || s.scope.Machine.Labels[machinev1.MachineClusterIDLabel] == "" {
			return fmt.Errorf("machine is missing %q label", machinev1.MachineClusterIDLabel)
		}

		vmSpec = &virtualmachines.Spec{
			Name:                s.scope.Machine.Name,
			NICName:             nicName,
			SSHKeyData:          string(decoded),
			Size:                s.scope.MachineConfig.VMSize,
			OSDisk:              s.scope.MachineConfig.OSDisk,
			Image:               s.scope.MachineConfig.Image,
			Zone:                zone,
			Tags:                s.scope.MachineConfig.Tags,
			Priority:            priority,
			EvictionPolicy:      evictionPolicy,
			BillingProfile:      billingProfile,
			SecurityProfile:     s.scope.MachineConfig.SecurityProfile,
			AvailabilitySetName: asName,
		}

		if s.scope.MachineConfig.ManagedIdentity != "" {
			vmSpec.ManagedIdentity = azure.GenerateManagedIdentityName(s.scope.SubscriptionID, s.scope.MachineConfig.ResourceGroup, s.scope.MachineConfig.ManagedIdentity)
		}

		if vmSpec.Tags == nil {
			vmSpec.Tags = map[string]string{}
		}

		vmSpec.Tags[fmt.Sprintf("kubernetes.io-cluster-%v", s.scope.Machine.Labels[machinev1.MachineClusterIDLabel])] = "owned"

		userData, userDataErr := s.getCustomUserData()
		if userDataErr != nil {
			return fmt.Errorf("failed to get custom script data: %w", userDataErr)
		}

		if userData != "" {
			vmSpec.CustomData = userData
		}

		err = s.virtualMachinesSvc.CreateOrUpdate(ctx, vmSpec)
		if err != nil {
			metrics.RegisterFailedInstanceCreate(&metrics.MachineLabels{
				Name:      s.scope.Machine.Name,
				Namespace: s.scope.Machine.Namespace,
				Reason:    err.Error(),
			})

			var detailedError autorest.DetailedError
			if errors.As(err, &detailedError) && detailedError.Message == "Failure sending request" {
				return machinecontroller.InvalidMachineConfiguration("failure sending request for machine %s: %v", s.scope.Machine.Name, err)
			}
			return fmt.Errorf("failed to create or get machine: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get vm: %w", err)
	} else {
		vm, ok := vmInterface.(compute.VirtualMachine)
		if !ok {
			return errors.New("returned incorrect vm interface")
		}
		if vm.ProvisioningState == nil {
			return fmt.Errorf("vm %s is nil provisioning state, reconcile", s.scope.Machine.Name)
		}

		vmState := getVMState(vm)
		s.scope.MachineStatus.VMID = vm.ID
		s.scope.MachineStatus.VMState = &vmState

		s.setMachineCloudProviderSpecifics(vm)

		if *vm.ProvisioningState == "Failed" {
			// If VM failed provisioning, delete it so it can be recreated
			err = s.Delete(ctx)
			if err != nil {
				return fmt.Errorf("failed to delete machine: %w", err)
			}
			return fmt.Errorf("vm %s is deleted, retry creating in next reconcile", s.scope.Machine.Name)
		} else if *vm.ProvisioningState != "Succeeded" {
			return fmt.Errorf("vm %s is still in provisioning state %s, reconcile", s.scope.Machine.Name, *vm.ProvisioningState)
		}
	}

	return nil
}

func (s *Reconciler) getCustomUserData() (string, error) {
	if s.scope.MachineConfig.UserDataSecret == nil {
		return "", nil
	}
	var userDataSecret apicorev1.Secret

	if err := s.scope.CoreClient.Get(context.Background(), client.ObjectKey{Namespace: s.scope.Namespace(), Name: s.scope.MachineConfig.UserDataSecret.Name}, &userDataSecret); err != nil {
		return "", fmt.Errorf("error getting user data secret %s in namespace %s: %w", s.scope.MachineConfig.UserDataSecret.Name, s.scope.Namespace(), err)
	}
	data, exists := userDataSecret.Data["userData"]
	if !exists {
		return "", fmt.Errorf("Secret %v/%v does not have userData field set. Thus, no user data applied when creating an instance.", s.scope.Namespace(), s.scope.MachineConfig.UserDataSecret.Name)
	}

	return base64.StdEncoding.EncodeToString(data), nil
}

func getSpotVMOptions(spotVMOptions *v1beta1.SpotVMOptions) (compute.VirtualMachinePriorityTypes, compute.VirtualMachineEvictionPolicyTypes, *compute.BillingProfile, error) {
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

func (s *Reconciler) createAvailabilitySet() (string, error) {
	// Try to find the zone for the machine location
	availabilityZones, err := s.availabilityZonesSvc.Get(context.Background(), &availabilityzones.Spec{
		VMSize: s.scope.MachineConfig.VMSize,
	})
	if err != nil {
		return "", err
	}

	availabilityZonesSlice, ok := availabilityZones.([]string)
	if !ok {
		return "", fmt.Errorf("unexpected type %T", availabilityZones)
	}

	if len(availabilityZonesSlice) != 0 {
		klog.V(4).Infof("No availability set needed for %s because availability zones were found", s.scope.Machine.Name)
		return "", nil
	}

	klog.V(4).Infof("No availability zones were found for %s, an availability set will be created", s.scope.Machine.Name)

	if err := s.availabilitySetsSvc.CreateOrUpdate(context.Background(), availabilitysets.Spec{
		Name: s.getAvailibilitySetName(),
	}); err != nil {
		return "", err
	}

	return s.getAvailibilitySetName(), nil
}

// getAvailibilitySetName use MachineSet or Machine name + cluster name to
// generate availability set name
func (s *Reconciler) getAvailibilitySetName() string {
	return fmt.Sprintf("%s_%s-as",
		s.scope.Machine.Labels[machinev1.MachineClusterIDLabel],
		s.scope.Machine.Labels[MachineSetLabelName])
}
