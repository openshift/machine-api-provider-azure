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
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2018-10-01/compute"
	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2018-12-01/network"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/pkg/errors"
	apicorev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/apis/azureprovider/v1beta1"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/actuators"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/converters"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/availabilityzones"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/certificates"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/config"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/disks"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/networkinterfaces"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/publicips"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/virtualmachineextensions"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/virtualmachines"

	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerconfig "sigs.k8s.io/controller-runtime/pkg/client/config"
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
}

// NewReconciler populates all the services based on input scope
func NewReconciler(scope *actuators.MachineScope) *Reconciler {
	return &Reconciler{
		scope:                 scope,
		availabilityZonesSvc:  availabilityzones.NewService(scope.Scope),
		networkInterfacesSvc:  networkinterfaces.NewService(scope.Scope),
		virtualMachinesSvc:    virtualmachines.NewService(scope.Scope),
		virtualMachinesExtSvc: virtualmachineextensions.NewService(scope.Scope),
		publicIPSvc:           publicips.NewService(scope.Scope),
		disksSvc:              disks.NewService(scope.Scope),
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
		return errors.Wrapf(err, "failed to create nic %s for machine %s", nicName, s.scope.Machine.Name)
	}

	if err := s.createVirtualMachine(ctx, nicName); err != nil {
		return errors.Wrapf(err, "failed to create vm %s ", s.scope.Machine.Name)
	}

	if s.scope.MachineConfig.UserDataSecret == nil {
		bootstrapToken, err := s.checkControlPlaneMachines()
		if err != nil {
			return errors.Wrap(err, "failed to check control plane machines in cluster")
		}

		scriptData, err := config.GetVMStartupScript(s.scope, bootstrapToken)
		if err != nil {
			return errors.Wrapf(err, "failed to get vm startup script")
		}

		vmExtSpec := &virtualmachineextensions.Spec{
			Name:       "startupScript",
			VMName:     s.scope.Machine.Name,
			ScriptData: base64.StdEncoding.EncodeToString([]byte(scriptData)),
		}
		err = s.virtualMachinesExtSvc.CreateOrUpdate(ctx, vmExtSpec)
		if err != nil {
			return errors.Wrap(err, "failed to create vm extension")
		}
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
		return errors.Errorf("failed to get vm: %+v", err)
	}

	vm, ok := vmInterface.(compute.VirtualMachine)
	if !ok {
		return errors.New("returned incorrect vm interface")
	}

	// We can now compare the various Azure state to the state we were passed.
	// We will check immutable state first, in order to fail quickly before
	// moving on to state that we can mutate.
	if isMachineOutdated(s.scope.MachineConfig, converters.SDKToVM(vm)) {
		return errors.Errorf("found attempt to change immutable state")
	}

	// TODO: Uncomment after implementing tagging.
	// Ensure that the tags are correct.
	/*
		_, err = a.ensureTags(computeSvc, machine, scope.MachineStatus.VMID, scope.MachineConfig.AdditionalTags)
		if err != nil {
			return errors.Errorf("failed to ensure tags: %+v", err)
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
			return errors.Errorf("MachineConfig vnet is missing on machine %s", s.scope.Machine.Name)
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
			s.scope.ClusterConfig.ResourceGroup,
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
	if vm.ProvisioningState == nil {
		return ""
	}

	if *vm.ProvisioningState != "Succeeded" {
		return v1beta1.VMState(*vm.ProvisioningState)
	}

	if vm.InstanceView == nil || vm.InstanceView.Statuses == nil {
		return ""
	}

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

	if vm.HardwareProfile != nil {
		s.scope.Machine.Labels[MachineInstanceTypeLabelName] = string(vm.HardwareProfile.VMSize)
	}
	if vm.Location != nil {
		s.scope.Machine.Labels[MachineRegionLabelName] = *vm.Location
	}
	if vm.Zones != nil {
		s.scope.Machine.Labels[MachineAZLabelName] = strings.Join(*vm.Zones, ",")
	}
}

// Exists checks if machine exists
func (s *Reconciler) Exists(ctx context.Context) (bool, error) {
	vmSpec := &virtualmachines.Spec{
		Name: s.scope.Name(),
	}
	vmInterface, err := s.virtualMachinesSvc.Get(ctx, vmSpec)

	if err != nil && vmInterface == nil {
		return false, nil
	}

	if err != nil {
		return false, errors.Wrap(err, "Failed to get vm")
	}

	vm, ok := vmInterface.(compute.VirtualMachine)
	if !ok {
		return false, errors.Errorf("returned incorrect vm interface: %T", vmInterface)
	}

	klog.Infof("Found vm for machine %s", s.scope.Name())

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
			return false, errors.Wrapf(err, "failed to get vm extension")
		}
	}

	switch v1beta1.VMState(*vm.ProvisioningState) {
	case v1beta1.VMStateSucceeded:
		klog.Infof("Machine %v is running", to.String(vm.VMID))
	case v1beta1.VMStateUpdating:
		klog.Infof("Machine %v is updating", to.String(vm.VMID))
	default:
		return false, nil
	}

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
		return errors.Wrapf(err, "failed to delete machine")
	}

	osDiskSpec := &disks.Spec{
		Name: azure.GenerateOSDiskName(s.scope.Machine.Name),
	}
	err = s.disksSvc.Delete(ctx, osDiskSpec)
	if err != nil {
		return errors.Wrapf(err, "failed to delete OS disk")
	}

	if s.scope.MachineConfig.Vnet == "" {
		return errors.Errorf("MachineConfig vnet is missing on machine %s", s.scope.Machine.Name)
	}

	networkInterfaceSpec := &networkinterfaces.Spec{
		Name:     azure.GenerateNetworkInterfaceName(s.scope.Machine.Name),
		VnetName: s.scope.MachineConfig.Vnet,
	}

	err = s.networkInterfacesSvc.Delete(ctx, networkInterfaceSpec)
	if err != nil {
		return errors.Wrapf(err, "Unable to delete network interface")
	}

	if s.scope.MachineConfig.PublicIP {
		publicIPName, err := azure.GenerateMachinePublicIPName(s.scope.Cluster.Name, s.scope.Machine.Name)
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
			return errors.Wrap(err, "unable to delete Public IP")
		}
	}

	return nil
}

// isMachineOutdated checks that no immutable fields have been updated in an
// Update request.
// Returns a bool indicating if an attempt to change immutable state occurred.
//  - true:  An attempt to change immutable state occurred.
//  - false: Immutable state was untouched.
func isMachineOutdated(machineSpec *v1beta1.AzureMachineProviderSpec, vm *v1beta1.VM) bool {
	// VM Size
	if !strings.EqualFold(machineSpec.VMSize, vm.VMSize) {
		return true
	}

	// TODO: Add additional checks for immutable fields

	// No immutable state changes found.
	return false
}

func (s *Reconciler) isNodeJoin() (bool, error) {
	clusterMachines, err := s.scope.MachineClient.List(metav1.ListOptions{})
	if err != nil {
		return false, errors.Wrapf(err, "failed to retrieve machines in cluster")
	}

	switch set := s.scope.Machine.ObjectMeta.Labels[v1beta1.MachineRoleLabel]; set {
	case v1beta1.Node:
		return true, nil
	case v1beta1.ControlPlane:
		for _, cm := range clusterMachines.Items {
			if cm.ObjectMeta.Labels[v1beta1.MachineRoleLabel] == v1beta1.ControlPlane {
				continue
			}
			vmInterface, err := s.virtualMachinesSvc.Get(context.Background(), &virtualmachines.Spec{Name: cm.Name})
			if err != nil && vmInterface == nil {
				klog.V(2).Infof("Machine %s should join the controlplane: false", s.scope.Name())
				return false, nil
			}

			if err != nil {
				return false, errors.Wrapf(err, "failed to verify existence of machine %s", cm.Name)
			}

			vmExtSpec := &virtualmachineextensions.Spec{
				Name:   "startupScript",
				VMName: cm.Name,
			}

			vmExt, err := s.virtualMachinesExtSvc.Get(context.Background(), vmExtSpec)
			if err != nil && vmExt == nil {
				klog.V(2).Infof("Machine %s should join the controlplane: false", cm.Name)
				return false, nil
			}

			klog.V(2).Infof("Machine %s should join the controlplane: true", s.scope.Name())
			return true, nil
		}

		return len(clusterMachines.Items) > 0, nil
	default:
		return false, errors.Errorf("Unknown value %s for label `set` on machine %s, skipping machine creation", set, s.scope.Name())
	}
}

func (s *Reconciler) checkControlPlaneMachines() (string, error) {
	isJoin, err := s.isNodeJoin()
	if err != nil {
		return "", errors.Wrapf(err, "failed to determine whether machine should join cluster")
	}

	var bootstrapToken string
	if isJoin {
		if s.scope.ClusterConfig == nil {
			return "", errors.Errorf("failed to retrieve corev1 client for empty kubeconfig")
		}
		bootstrapToken, err = certificates.CreateNewBootstrapToken(s.scope.ClusterConfig.AdminKubeconfig, DefaultBootstrapTokenTTL)
		if err != nil {
			return "", errors.Wrapf(err, "failed to create new bootstrap token")
		}
	}
	return bootstrapToken, nil
}

func coreV1Client(kubeconfig string) (corev1.CoreV1Interface, error) {
	if kubeconfig == "" {
		cfg, err := controllerconfig.GetConfig()
		if err != nil {
			return nil, errors.Wrap(err, "failed to get config")
		}
		return corev1.NewForConfig(cfg)
	}
	clientConfig, err := clientcmd.NewClientConfigFromBytes([]byte(kubeconfig))

	if err != nil {
		return nil, errors.Wrapf(err, "failed to get client config for cluster")
	}

	cfg, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get client config for cluster")
	}

	return corev1.NewForConfig(cfg)
}

func (s *Reconciler) getZone(ctx context.Context) (string, error) {
	return to.String(s.scope.MachineConfig.Zone), nil
}

func (s *Reconciler) createNetworkInterface(ctx context.Context, nicName string) error {
	if s.scope.MachineConfig.Vnet == "" {
		return errors.Errorf("MachineConfig vnet is missing on machine %s", s.scope.Machine.Name)
	}

	networkInterfaceSpec := &networkinterfaces.Spec{
		Name:     nicName,
		VnetName: s.scope.MachineConfig.Vnet,
	}

	if s.scope.MachineConfig.Subnet == "" {
		return errors.Errorf("MachineConfig subnet is missing on machine %s, skipping machine creation", s.scope.Machine.Name)
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

	if s.scope.MachineConfig.PublicIP {
		publicIPName, err := azure.GenerateMachinePublicIPName(s.scope.Cluster.Name, s.scope.Machine.Name)
		if err != nil {
			return errors.Wrap(err, "unable to create Public IP")
		}
		err = s.publicIPSvc.CreateOrUpdate(ctx, &publicips.Spec{Name: publicIPName})
		if err != nil {
			return errors.Wrap(err, "unable to create Public IP")
		}
		networkInterfaceSpec.PublicIP = publicIPName
	}

	err := s.networkInterfacesSvc.CreateOrUpdate(ctx, networkInterfaceSpec)
	if err != nil {
		return errors.Wrap(err, "unable to create VM network interface")
	}

	return err
}

func (s *Reconciler) createVirtualMachine(ctx context.Context, nicName string) error {
	decoded, err := base64.StdEncoding.DecodeString(s.scope.MachineConfig.SSHPublicKey)
	if err != nil {
		errors.Wrapf(err, "failed to decode ssh public key")
	}

	vmSpec := &virtualmachines.Spec{
		Name: s.scope.Machine.Name,
	}

	vmInterface, err := s.virtualMachinesSvc.Get(ctx, vmSpec)
	if err != nil && vmInterface == nil {
		zone, err := s.getZone(ctx)
		if err != nil {
			return errors.Wrapf(err, "failed to get zone")
		}

		if s.scope.MachineConfig.ManagedIdentity == "" {
			return errors.Errorf("MachineConfig managedIdentity is missing on machine %s", s.scope.Machine.Name)
		}

		vmSpec = &virtualmachines.Spec{
			Name:            s.scope.Machine.Name,
			NICName:         nicName,
			SSHKeyData:      string(decoded),
			Size:            s.scope.MachineConfig.VMSize,
			OSDisk:          s.scope.MachineConfig.OSDisk,
			Image:           s.scope.MachineConfig.Image,
			Zone:            zone,
			Tags:            s.scope.MachineConfig.Tags,
			ManagedIdentity: azure.GenerateManagedIdentityName(s.scope.SubscriptionID, s.scope.ClusterConfig.ResourceGroup, s.scope.MachineConfig.ManagedIdentity),
		}

		userData, userDataErr := s.getCustomUserData()
		if userDataErr != nil {
			return errors.Wrapf(userDataErr, "failed to get custom script data")
		}

		if userData != "" {
			vmSpec.CustomData = userData
		}

		err = s.virtualMachinesSvc.CreateOrUpdate(ctx, vmSpec)
		if err != nil {
			return errors.Wrapf(err, "failed to create or get machine")
		}
	} else if err != nil {
		return errors.Wrap(err, "failed to get vm")
	} else {
		vm, ok := vmInterface.(compute.VirtualMachine)
		if !ok {
			return errors.New("returned incorrect vm interface")
		}
		if vm.ProvisioningState == nil {
			return errors.Errorf("vm %s is nil provisioning state, reconcile", s.scope.Machine.Name)
		}

		vmState := getVMState(vm)
		s.scope.MachineStatus.VMID = vm.ID
		s.scope.MachineStatus.VMState = &vmState

		s.setMachineCloudProviderSpecifics(vm)

		if *vm.ProvisioningState == "Failed" {
			// If VM failed provisioning, delete it so it can be recreated
			err = s.Delete(ctx)
			if err != nil {
				return errors.Wrapf(err, "failed to delete machine")
			}
			return errors.Errorf("vm %s is deleted, retry creating in next reconcile", s.scope.Machine.Name)
		} else if *vm.ProvisioningState != "Succeeded" {
			return errors.Errorf("vm %s is still in provisioning state %s, reconcile", s.scope.Machine.Name, *vm.ProvisioningState)
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
		return "", errors.Wrapf(err, "error getting user data secret %s in namespace %s", s.scope.MachineConfig.UserDataSecret.Name, s.scope.Namespace())
	}
	data, exists := userDataSecret.Data["userData"]
	if !exists {
		return "", errors.Errorf("Secret %v/%v does not have userData field set. Thus, no user data applied when creating an instance.", s.scope.Namespace(), s.scope.MachineConfig.UserDataSecret.Name)
	}

	return base64.StdEncoding.EncodeToString(data), nil
}
