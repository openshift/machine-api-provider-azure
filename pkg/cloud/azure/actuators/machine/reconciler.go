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
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2021-11-01/compute"
	"github.com/Azure/go-autorest/autorest"
	autorestazure "github.com/Azure/go-autorest/autorest/azure"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	machinecontroller "github.com/openshift/machine-api-operator/pkg/controller/machine"
	"github.com/openshift/machine-api-operator/pkg/metrics"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/actuators"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/decode"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/availabilitysets"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/availabilityzones"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/disks"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/interfaceloadbalancers"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/networkinterfaces"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/publicips"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/resourceskus"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/virtualmachines"
	apicorev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"

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

	MachineSetLabelName           = "machine.openshift.io/cluster-api-machineset"
	azureProviderIDPrefix         = "azure://"
	azureProvidersKey             = "providers"
	azureSubscriptionsKey         = "subscriptions"
	azureResourceGroupsLowerKey   = "resourcegroups"
	azureLocationsKey             = "locations"
	azureBuiltInResourceNamespace = "Microsoft.Resources"
)

// Reconciler are list of services required by cluster actuator, easy to create a fake
type Reconciler struct {
	scope                     *actuators.MachineScope
	availabilityZonesSvc      azure.Service
	interfaceLoadBalancersSvc azure.Service
	networkInterfacesSvc      azure.Service
	publicIPSvc               azure.Service
	virtualMachinesSvc        azure.Service
	virtualMachinesExtSvc     azure.Service
	disksSvc                  azure.Service
	availabilitySetsSvc       azure.Service
	resourcesSkus             azure.Service
}

// NewReconciler populates all the services based on input scope
func NewReconciler(scope *actuators.MachineScope) *Reconciler {
	return &Reconciler{
		scope:                     scope,
		availabilityZonesSvc:      availabilityzones.NewService(scope),
		interfaceLoadBalancersSvc: interfaceloadbalancers.NewService(scope),
		networkInterfacesSvc:      networkinterfaces.NewService(scope),
		virtualMachinesSvc:        virtualmachines.NewService(scope),
		publicIPSvc:               publicips.NewService(scope),
		disksSvc:                  disks.NewService(scope),
		availabilitySetsSvc:       availabilitysets.NewService(scope),
		resourcesSkus:             resourceskus.NewService(scope),
	}
}

// Create creates machine if and only if machine exists, handled by cluster-api
func (s *Reconciler) Create(ctx context.Context) error {
	if err := s.CreateMachine(ctx); err != nil {
		s.scope.MachineStatus.Conditions = setCondition(s.scope.MachineStatus.Conditions, metav1.Condition{
			Type:    string(machinev1.MachineCreated),
			Status:  metav1.ConditionFalse,
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

	// Availability set will be created only if no zones were found for a machine or
	// if availability set name was not specified in provider spec
	asName, err := s.getOrCreateAvailabilitySet()
	if err != nil {
		return fmt.Errorf("failed to create availability set %s for machine %s: %w", asName, s.scope.Machine.Name, err)
	}

	if err := s.createVirtualMachine(ctx, nicName, asName); err != nil {
		return fmt.Errorf("failed to create vm %s: %w", s.scope.Machine.Name, err)
	}

	// Once we have created the machine, attempt to update it.
	// This should set the network addresses and provider ID which should be set as soon
	// as the machine is created, moving it to the provisioned phase.
	if err := s.Update(ctx); err != nil {
		return fmt.Errorf("failed to update machine %s: %w", s.scope.Machine.Name, err)
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

	vm, err := decode.GetVirtualMachine(vmInterface)
	if err != nil {
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

			niface, err := decode.GetNetworkInterface(networkIface)
			if err != nil {
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

					ip, err := decode.GetPublicIPAdress(publicIPInterface)
					if err != nil {
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

		// If the internal load balancer isn't set on a control plane machine,
		// we should attempt to populate it else if the machine is later replaced,
		// its replacement will not get attached to the load balancer.
		if s.scope.Role() == actuators.ControlPlane && s.scope.MachineConfig.InternalLoadBalancer == "" {
			for _, iface := range *vm.NetworkProfile.NetworkInterfaces {
				// Get iface name from the ID
				ifaceName := path.Base(*iface.ID)

				lbsiface, err := s.interfaceLoadBalancersSvc.Get(ctx, &interfaceloadbalancers.Spec{
					NicName:           ifaceName,
					ResourceGroupName: s.scope.MachineConfig.ResourceGroup,
				})
				if err != nil {
					klog.Errorf("Unable to get load balancers for interface %s: %v", ifaceName, err)
					continue
				}

				lbs, err := decode.GetLoadBalancers(lbsiface)
				if err != nil {
					klog.Errorf("LoadBalancers interfaces get returned invalid load balancers interface, getting %T instead", lbsiface)
					continue
				}

				for _, lb := range lbs {
					if lb.LoadBalancerPropertiesFormat == nil || lb.LoadBalancerPropertiesFormat.FrontendIPConfigurations == nil {
						continue
					}

					for _, frontendIP := range *lb.LoadBalancerPropertiesFormat.FrontendIPConfigurations {
						if frontendIP.FrontendIPConfigurationPropertiesFormat != nil && frontendIP.FrontendIPConfigurationPropertiesFormat.PrivateIPAddress != nil {
							// On Azure, each VM can only have one private load balancer and each private load balance can only have
							// private IP addresses. If we have found a load balancer with a private IP address frontend, then this is
							// the internal load balancer for this machine.
							s.scope.MachineConfig.InternalLoadBalancer = ptr.Deref[string](lb.Name, "")
							break
						}
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
	s.scope.MachineStatus.Conditions = setCondition(s.scope.MachineStatus.Conditions, metav1.Condition{
		Type:    string(machinev1.MachineCreated),
		Status:  metav1.ConditionTrue,
		Reason:  machineCreationSucceedReason,
		Message: machineCreationSucceedMessage,
	})

	vmState := getVMState(vm)
	s.scope.MachineStatus.VMID = vm.ID
	s.scope.MachineStatus.VMState = &vmState

	s.setMachineCloudProviderSpecifics(vm)

	return nil
}

func getVMState(vm *decode.VirtualMachine) machinev1.AzureVMState {
	if vm.VirtualMachineProperties == nil || vm.ProvisioningState == nil {
		return ""
	}

	if *vm.ProvisioningState != "Succeeded" {
		return machinev1.AzureVMState(*vm.ProvisioningState)
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
			return machinev1.VMStateStarting
		case "PowerState/running":
			return machinev1.VMStateRunning
		case "PowerState/stopping":
			return machinev1.VMStateStopping
		case "PowerState/stopped":
			return machinev1.VMStateStopped
		case "PowerState/deallocating":
			return machinev1.VMStateDeallocating
		case "PowerState/deallocated":
			return machinev1.VMStateDeallocated
		default:
			return machinev1.VMStateUnknown
		}
	}

	return ""
}

func (s *Reconciler) setMachineCloudProviderSpecifics(vm *decode.VirtualMachine) {
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
		return false, fmt.Errorf("failed to get vm: %w", err)
	}

	vm, err := decode.GetVirtualMachine(vmInterface)
	if err != nil {
		return false, fmt.Errorf("returned incorrect vm interface: %v", err)
	}

	switch machinev1.AzureVMState(*vm.ProvisioningState) {
	case machinev1.VMStateDeleting:
		return true, fmt.Errorf("vm for machine %s has unexpected 'Deleting' provisioning state", s.scope.Machine.GetName())
	case machinev1.VMStateFailed:
		klog.Infof("vm for machine %s has unexpected 'Failed' provisioning state", s.scope.Machine.GetName())
		return false, &apierrors.UnexpectedObjectError{
			Object: s.scope.Machine,
		}
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
	s.scope.Machine.Annotations[MachineInstanceStateAnnotationName] = string(machinev1.VMStateDeleting)
	vmStateDeleting := machinev1.VMStateDeleting
	s.scope.MachineStatus.VMState = &vmStateDeleting

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
			Reason:    "failed to delete OS disk",
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
			Reason:    "failed to delete network interface",
		})
		return fmt.Errorf("Unable to delete network interface: %w", err)
	}

	if s.scope.MachineConfig.PublicIP {
		publicIPName, err := azure.GenerateMachinePublicIPName(s.scope.ClusterName, s.scope.Machine.Name)
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
				Reason:    "failed to delete Public IP",
			})
			return fmt.Errorf("unable to delete Public IP: %w", err)
		}
	}

	// Delete the availability set with the given name if no virtual machines are attached to it.
	if err := s.availabilitySetsSvc.Delete(ctx, &availabilitysets.Spec{
		Name: s.getAvailabilitySetName(),
	}); err != nil {
		return fmt.Errorf("failed to delete availability set: %w", err)
	}

	return nil
}

func (s *Reconciler) getZone(ctx context.Context) (string, error) {
	return s.scope.MachineConfig.Zone, nil
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
		skuSpec := resourceskus.Spec{
			Name:         s.scope.MachineConfig.VMSize,
			ResourceType: resourceskus.VirtualMachines,
		}

		skuI, err := s.resourcesSkus.Get(ctx, skuSpec)
		if err != nil {
			metrics.RegisterFailedInstanceCreate(&metrics.MachineLabels{
				Name:      s.scope.Machine.Name,
				Namespace: s.scope.Machine.Namespace,
				Reason:    "failed to obtain instance type information",
			})

			if errors.Is(err, resourceskus.ErrResourceNotFound) {
				return machinecontroller.InvalidMachineConfiguration("failed to obtain instance type information for VMSize '%s' from Azure: %s", skuSpec.Name, err)
			} else {
				return fmt.Errorf("failed to obtain instance type information for VMSize '%s' from Azure: %w", skuSpec.Name, err)
			}
		}

		sku := skuI.(resourceskus.SKU)

		if !sku.HasCapability(resourceskus.AcceleratedNetworking) {
			metrics.RegisterFailedInstanceCreate(&metrics.MachineLabels{
				Name:      s.scope.Machine.Name,
				Namespace: s.scope.Machine.Namespace,
				Reason:    fmt.Sprintf("accelerated networking not supported on instance type: %v", s.scope.MachineConfig.VMSize),
			})
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
		publicIPName, err := azure.GenerateMachinePublicIPName(s.scope.ClusterName, s.scope.Machine.Name)
		if err != nil {
			return machinecontroller.InvalidMachineConfiguration("unable to create Public IP: %v", err)
		}
		err = s.publicIPSvc.CreateOrUpdate(ctx, &publicips.Spec{Name: publicIPName})
		if err != nil {
			metrics.RegisterFailedInstanceCreate(&metrics.MachineLabels{
				Name:      s.scope.Machine.Name,
				Namespace: s.scope.Machine.Namespace,
				Reason:    "failed to create public IP",
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
			Reason:    "failed to create VM network interface",
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

	vmSpec := &virtualmachines.Spec{
		Name: s.scope.Machine.Name,
	}

	vmInterface, err := s.virtualMachinesSvc.Get(ctx, vmSpec)
	if err != nil && vmInterface == nil {
		zone, err := s.getZone(ctx)
		if err != nil {
			return fmt.Errorf("failed to get zone: %w", err)
		}

		diagnosticsProfile, err := createDiagnosticsConfig(s.scope.MachineConfig)
		if err != nil {
			return fmt.Errorf("failed to configure diagnostics profile: %w", err)
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
			DataDisks:           s.scope.MachineConfig.DataDisks,
			Image:               s.scope.MachineConfig.Image,
			Zone:                zone,
			Tags:                s.scope.Tags,
			SecurityProfile:     s.scope.MachineConfig.SecurityProfile,
			UltraSSDCapability:  s.scope.MachineConfig.UltraSSDCapability,
			AvailabilitySetName: asName,
			DiagnosticsProfile:  diagnosticsProfile,
		}

		if s.scope.MachineConfig.ManagedIdentity != "" {
			vmSpec.ManagedIdentity = azure.GenerateManagedIdentityName(s.scope.SubscriptionID, s.scope.MachineConfig.ResourceGroup, s.scope.MachineConfig.ManagedIdentity)
		}
		if s.scope.MachineConfig.CapacityReservationGroupID != "" {
			if err = validateAzureCapacityReservationGroupID(s.scope.MachineConfig.CapacityReservationGroupID); err != nil {
				return fmt.Errorf("failed to validate capacityReservationGroupID: %w", err)
			}
			vmSpec.CapacityReservationGroupID = s.scope.MachineConfig.CapacityReservationGroupID
		}

		userData, userDataErr := s.getCustomUserData()
		if userDataErr != nil {
			return fmt.Errorf("failed to get custom script data: %w", userDataErr)
		}

		if userData != "" {
			vmSpec.CustomData = userData
		}

		// If we get an AsynOpIncompleteError, this means the VM is being created and we completed the request successfully.
		if err := s.virtualMachinesSvc.CreateOrUpdate(ctx, vmSpec); err != nil && !errors.Is(err, autorestazure.NewAsyncOpIncompleteError("compute.VirtualMachinesCreateOrUpdateFuture")) {
			metrics.RegisterFailedInstanceCreate(&metrics.MachineLabels{
				Name:      s.scope.Machine.Name,
				Namespace: s.scope.Machine.Namespace,
				Reason:    "failed to create VM",
			})

			var detailedError autorest.DetailedError
			if errors.As(err, &detailedError) && detailedError.Message == "Failure sending request" {
				return machinecontroller.InvalidMachineConfiguration("failure sending request for machine %s: %v", s.scope.Machine.Name, err)
			}

			return fmt.Errorf("failed to create VM: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to get vm: %w", err)
	} else {
		vm, err := decode.GetVirtualMachine(vmInterface)
		if err != nil {
			return fmt.Errorf("returned incorrect vm interface: %v", err)
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

func (s *Reconciler) getOrCreateAvailabilitySet() (string, error) {
	if s.scope.MachineConfig.AvailabilitySet != "" {
		return s.scope.MachineConfig.AvailabilitySet, nil
	}

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

	if _, ok := s.scope.Machine.Labels[MachineSetLabelName]; !ok {
		klog.V(4).Infof("MachineSet label name was not found for %s, skipping availability set creation", s.scope.Machine.Name)
		return "", nil
	}

	if s.scope.MachineConfig.SpotVMOptions != nil {
		klog.V(4).Infof("MachineSet %s uses spot instances, skipping availability set creation", s.scope.Machine.Name)
		return "", nil
	}

	klog.V(4).Infof("No availability zones were found for %s, an availability set will be created", s.scope.Machine.Name)

	if err := s.availabilitySetsSvc.CreateOrUpdate(context.Background(), &availabilitysets.Spec{
		Name: s.getAvailabilitySetName(),
	}); err != nil {
		return "", err
	}

	return s.getAvailabilitySetName(), nil
}

// getAvailabilitySetName uses the MachineSet name and the cluster name to
// generate an availability set name with the format
// `<Cluster Name>_<MachineSet Name>-as`. Due to an 80 character restriction
// on availability set names, if the MachineSet name starts with the cluster
// name, then it will not be added a second time and the availability set name
// will be `<MachineSet Name>-as`.
// see https://docs.microsoft.com/en-us/azure/azure-resource-manager/management/resource-name-rules#microsoftcompute
func (s *Reconciler) getAvailabilitySetName() string {
	asname := ""
	if strings.HasPrefix(s.scope.Machine.Labels[MachineSetLabelName], s.scope.Machine.Labels[machinev1.MachineClusterIDLabel]) {
		asname = s.scope.Machine.Labels[MachineSetLabelName]
	} else {
		asname = fmt.Sprintf("%s_%s",
			s.scope.Machine.Labels[machinev1.MachineClusterIDLabel],
			s.scope.Machine.Labels[MachineSetLabelName])
	}
	// due to the 80 character name limit, if the proposed name will be 77 or more
	// characters, we truncate the name before adding `-as`
	if len(asname) >= 77 { // 80 char minus room for adding `-as`
		asname = asname[:77]
	}
	asname = fmt.Sprintf("%s-as", asname)
	return asname
}

// createDiagnosticsConfig sets up the diagnostics configuration for the virtual machine.
func createDiagnosticsConfig(config *machinev1.AzureMachineProviderSpec) (*compute.DiagnosticsProfile, error) {
	boot := config.Diagnostics.Boot
	if boot == nil {
		return nil, nil
	}

	switch boot.StorageAccountType {
	case machinev1.AzureManagedAzureDiagnosticsStorage:
		return &compute.DiagnosticsProfile{
			BootDiagnostics: &compute.BootDiagnostics{
				Enabled: ptr.To[bool](true),
			},
		}, nil
	case machinev1.CustomerManagedAzureDiagnosticsStorage:
		if boot.CustomerManaged == nil || boot.CustomerManaged.StorageAccountURI == "" {
			return nil, machinecontroller.InvalidMachineConfiguration("missing configuration for customer managed storage account URI")
		}

		return &compute.DiagnosticsProfile{
			BootDiagnostics: &compute.BootDiagnostics{
				Enabled:    ptr.To[bool](true),
				StorageURI: ptr.To[string](boot.CustomerManaged.StorageAccountURI),
			},
		}, nil
	default:
		return nil, machinecontroller.InvalidMachineConfiguration(
			"unknown storage account type for boot diagnostics: %q, supported types are %s & %s",
			boot.StorageAccountType,
			machinev1.AzureManagedAzureDiagnosticsStorage,
			machinev1.CustomerManagedAzureDiagnosticsStorage,
		)
	}
}

func validateAzureCapacityReservationGroupID(capacityReservationGroupID string) error {
	id := strings.TrimPrefix(capacityReservationGroupID, azureProviderIDPrefix)
	err := parseAzureResourceID(id)
	if err != nil {
		return err
	}
	return nil
}

// parseAzureResourceID parses a string to an instance of ResourceID
func parseAzureResourceID(id string) error {
	if len(id) == 0 {
		return fmt.Errorf("invalid resource ID: id cannot be empty")
	}
	if !strings.HasPrefix(id, "/") {
		return fmt.Errorf("invalid resource ID: resource id '%s' must start with '/'", id)
	}
	parts := splitStringAndOmitEmpty(id, "/")

	if len(parts) < 2 {
		return fmt.Errorf("invalid resource ID: %s", id)
	}
	if !strings.EqualFold(parts[0], azureSubscriptionsKey) && !strings.EqualFold(parts[0], azureProvidersKey) {
		return fmt.Errorf("invalid resource ID: %s", id)
	}
	return appendNextAzureResourceIDValidation(parts, id)
}

func splitStringAndOmitEmpty(v, sep string) []string {
	r := make([]string, 0)
	for _, s := range strings.Split(v, sep) {
		if len(s) == 0 {
			continue
		}
		r = append(r, s)
	}
	return r
}

func appendNextAzureResourceIDValidation(parts []string, id string) error {
	if len(parts) == 0 {
		return nil
	}
	if len(parts) == 1 {
		// subscriptions and resourceGroups are not valid ids without their names
		if strings.EqualFold(parts[0], azureSubscriptionsKey) || strings.EqualFold(parts[0], azureResourceGroupsLowerKey) {
			return fmt.Errorf("invalid resource ID: %s", id)
		}
		return nil
	}
	if strings.EqualFold(parts[0], azureProvidersKey) && (len(parts) == 2 || strings.EqualFold(parts[2], azureProvidersKey)) {
		return appendNextAzureResourceIDValidation(parts[2:], id)
	}
	if len(parts) > 3 && strings.EqualFold(parts[0], azureProvidersKey) {
		return appendNextAzureResourceIDValidation(parts[4:], id)
	}
	if len(parts) > 1 && !strings.EqualFold(parts[0], azureProvidersKey) {
		return appendNextAzureResourceIDValidation(parts[2:], id)
	}
	return fmt.Errorf("invalid resource ID: %s", id)
}
