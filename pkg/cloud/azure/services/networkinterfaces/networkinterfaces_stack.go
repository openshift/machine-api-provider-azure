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

package networkinterfaces

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/profiles/2019-03-01/network/mgmt/network"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/applicationsecuritygroups"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/internalloadbalancers"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/publicips"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/publicloadbalancers"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/securitygroups"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/subnets"
	"k8s.io/klog/v2"
	utilnet "k8s.io/utils/net"
)

// Get provides information about a network interface.
func (s *StackHubService) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	nicSpec, ok := spec.(*Spec)
	if !ok {
		return network.Interface{}, errors.New("invalid network interface specification")
	}
	return s.get(ctx, nicSpec.Name)
}

func (s *StackHubService) get(ctx context.Context, name string) (*network.Interface, error) {
	nic, err := s.Client.Get(ctx, s.Scope.MachineConfig.ResourceGroup, name, "")
	if err != nil && azure.ResourceNotFound(err) {
		return nil, fmt.Errorf("network interface %s not found: %w", name, err)
	} else if err != nil {
		return nil, err
	}
	return &nic, nil
}

// GetID returns the ID of the network interface with the given name
func (s *StackHubService) GetID(ctx context.Context, name string) (string, error) {
	nic, err := s.get(ctx, name)
	if err != nil {
		return "", err
	}

	if nic.ID == nil {
		return "", fmt.Errorf("network interface %s does not have an ID", name)
	}

	return *nic.ID, nil
}

// CreateOrUpdate creates or updates a network interface.
func (s *StackHubService) CreateOrUpdate(ctx context.Context, spec azure.Spec) error {
	nicSpec, ok := spec.(*Spec)
	if !ok {
		return errors.New("invalid network interface specification")
	}

	subnetInterface, err := subnets.NewService(s.Scope).Get(ctx, &subnets.Spec{Name: nicSpec.SubnetName, VnetName: nicSpec.VnetName})
	if err != nil {
		return err
	}

	subnet, ok := subnetInterface.(network.Subnet)
	if !ok {
		return errors.New("subnet get returned invalid network interface")
	}
	nicHasIPv6 := subnetHasIPv6StackHub(subnet)

	nicProp := network.InterfacePropertiesFormat{}
	nicConfig := &network.InterfaceIPConfigurationPropertiesFormat{}
	nicConfigV6 := &network.InterfaceIPConfigurationPropertiesFormat{}

	nicConfig.Subnet = &network.Subnet{ID: subnet.ID}
	nicConfig.PrivateIPAllocationMethod = network.Dynamic
	nicConfig.LoadBalancerInboundNatRules = &[]network.InboundNatRule{}
	if nicHasIPv6 {
		klog.V(2).Infof("Found IPv6 address space. Adding IPv6 configuration to nic: %s", nicSpec.Name)
		nicConfigV6.Subnet = &network.Subnet{ID: subnet.ID}
		nicConfigV6.PrivateIPAllocationMethod = network.Dynamic
		nicConfigV6.PrivateIPAddressVersion = network.IPv6
		nicConfigV6.LoadBalancerInboundNatRules = &[]network.InboundNatRule{}
		nicConfigV6.Primary = to.BoolPtr(false)
	}

	// IP address allocation
	if nicSpec.StaticIPAddress != "" {
		if utilnet.IsIPv6String(nicSpec.StaticIPAddress) {
			nicConfigV6.PrivateIPAllocationMethod = network.Static
			nicConfigV6.PrivateIPAddress = to.StringPtr(nicSpec.StaticIPAddress)
		} else {
			nicConfig.PrivateIPAllocationMethod = network.Static
			nicConfig.PrivateIPAddress = to.StringPtr(nicSpec.StaticIPAddress)
		}
	}

	// associated public IPs
	if nicSpec.PublicIP != "" {
		publicIPInterface, publicIPErr := publicips.NewService(s.Scope).Get(ctx, &publicips.Spec{Name: nicSpec.PublicIP})
		if publicIPErr != nil {
			return publicIPErr
		}

		ip, ok := publicIPInterface.(network.PublicIPAddress)
		if !ok {
			return errors.New("public ip get returned invalid network interface")
		}

		if ip.PublicIPAddressPropertiesFormat.PublicIPAddressVersion == network.IPv6 {
			nicConfigV6.PublicIPAddress = &ip
		} else {
			nicConfig.PublicIPAddress = &ip
		}
	}

	// associated Load Balancers
	backendAddressPools := []network.BackendAddressPool{}
	backendAddressPoolsV6 := []network.BackendAddressPool{}
	if nicSpec.PublicLoadBalancerName != "" {
		lbInterface, lberr := publicloadbalancers.NewService(s.Scope).Get(ctx, &publicloadbalancers.Spec{Name: nicSpec.PublicLoadBalancerName})
		if lberr != nil {
			return lberr
		}

		lb, ok := lbInterface.(network.LoadBalancer)
		if !ok {
			return errors.New("public load balancer get returned invalid network interface")
		}

		loadBalancerInboundNatRules := []network.InboundNatRule{}
		loadBalancerInboundNatRulesV6 := []network.InboundNatRule{}
		// loadbalancers can have multiple frontends and backends with different IP families
		// TODO: this logic is different in public azure, check that no functionality is broken by this
		for i, _ := range *lb.FrontendIPConfigurations {
			// iterate only for the frontends that have backends configured
			if i >= len(*lb.BackendAddressPools) {
				break
			}

			backendAddressPools = append(backendAddressPools,
				network.BackendAddressPool{
					ID: (*lb.BackendAddressPools)[i].ID,
				})

			if nicSpec.NatRule != nil {
				loadBalancerInboundNatRules = append(loadBalancerInboundNatRules,
					network.InboundNatRule{ID: (*lb.InboundNatRules)[*nicSpec.NatRule].ID})
			}
		}

		if nicSpec.NatRule != nil {
			nicConfig.LoadBalancerInboundNatRules = &loadBalancerInboundNatRules
			nicConfigV6.LoadBalancerInboundNatRules = &loadBalancerInboundNatRulesV6
		}

	}
	if nicSpec.InternalLoadBalancerName != "" {
		internallbInterface, ilberr := internalloadbalancers.NewService(s.Scope).Get(ctx, &internalloadbalancers.Spec{Name: nicSpec.InternalLoadBalancerName})
		if ilberr != nil {
			return ilberr
		}

		internallb, ok := internallbInterface.(network.LoadBalancer)
		if !ok {
			return errors.New("internal load balancer get returned invalid network interface")
		}
		// loadbalancers can have multiple frontends and backends with different IP families
		// TODO: this logic is different in public azure, check that no functionality is broken by this
		for i, _ := range *internallb.FrontendIPConfigurations {
			// iterate only for the frontends that have backends configured
			if i >= len(*internallb.BackendAddressPools) {
				break
			}

			backendAddressPools = append(backendAddressPools,
				network.BackendAddressPool{
					ID: (*internallb.BackendAddressPools)[i].ID,
				})
		}
	}
	nicConfig.LoadBalancerBackendAddressPools = &backendAddressPools
	if nicHasIPv6 {
		nicConfigV6.LoadBalancerBackendAddressPools = &backendAddressPoolsV6
	}

	// security groups
	if nicSpec.SecurityGroupName != "" {
		securityGroupInterface, sgerr := securitygroups.NewService(s.Scope).Get(ctx, &securitygroups.Spec{Name: nicSpec.SecurityGroupName})
		if sgerr != nil {
			return sgerr
		}

		sg, ok := securityGroupInterface.(network.SecurityGroup)
		if !ok {
			return errors.New("security group get returned invalid network interface")
		}
		nicProp.NetworkSecurityGroup = &network.SecurityGroup{ID: sg.ID}
	}

	if len(nicSpec.ApplicationSecurityGroupNames) > 0 {
		asgSvc := applicationsecuritygroups.NewService(s.Scope)

		var groups []network.ApplicationSecurityGroup
		for _, asgName := range nicSpec.ApplicationSecurityGroupNames {
			asgInterface, asgerr := asgSvc.Get(ctx, &applicationsecuritygroups.Spec{Name: asgName})
			if err != nil {
				return asgerr
			}
			asg, ok := asgInterface.(network.ApplicationSecurityGroup)
			if !ok {
				return errors.New("application security group get returned invalid network interface")
			}
			groups = append(groups, network.ApplicationSecurityGroup{
				ID: asg.ID,
			})
		}
		nicConfig.ApplicationSecurityGroups = &groups
	}

	// configure the nic with the corresponding IP configurations
	nicProp.IPConfigurations = &[]network.InterfaceIPConfiguration{
		{
			Name:                                     to.StringPtr("pipConfig"),
			InterfaceIPConfigurationPropertiesFormat: nicConfig,
		},
	}

	if nicHasIPv6 {
		ipConfigs := append(*nicProp.IPConfigurations,
			network.InterfaceIPConfiguration{
				Name:                                     to.StringPtr("pipConfig-v6"),
				InterfaceIPConfigurationPropertiesFormat: nicConfigV6,
			},
		)

		nicProp.IPConfigurations = &ipConfigs
	}

	f, err := s.Client.CreateOrUpdate(ctx,
		s.Scope.MachineConfig.ResourceGroup,
		nicSpec.Name,
		network.Interface{
			Location:                  to.StringPtr(s.Scope.MachineConfig.Location),
			InterfacePropertiesFormat: &nicProp,
		})

	if err != nil {
		return fmt.Errorf("failed to create network interface %s in resource group %s: %w", nicSpec.Name, s.Scope.MachineConfig.ResourceGroup, err)
	}

	err = f.WaitForCompletionRef(ctx, s.Client.Client)
	if err != nil {
		return fmt.Errorf("cannot create, future response: %w", err)
	}

	_, err = f.Result(s.Client)
	if err != nil {
		return fmt.Errorf("result error: %w", err)
	}
	klog.V(2).Infof("successfully created network interface %s", nicSpec.Name)
	return err
}

// Delete deletes the network interface with the provided name.
func (s *StackHubService) Delete(ctx context.Context, spec azure.Spec) error {
	nicSpec, ok := spec.(*Spec)
	if !ok {
		return errors.New("invalid network interface Specification")
	}
	klog.V(2).Infof("deleting nic %s", nicSpec.Name)
	f, err := s.Client.Delete(ctx, s.Scope.MachineConfig.ResourceGroup, nicSpec.Name)
	if err != nil && azure.ResourceNotFound(err) {
		// already deleted
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete network interface %s in resource group %s: %w", nicSpec.Name, s.Scope.MachineConfig.ResourceGroup, err)
	}

	err = f.WaitForCompletionRef(ctx, s.Client.Client)
	if err != nil {
		return fmt.Errorf("cannot create, future response: %w", err)
	}

	_, err = f.Result(s.Client)
	if err != nil {
		return fmt.Errorf("result error: %w", err)
	}
	klog.V(2).Infof("successfully deleted nic %s", nicSpec.Name)
	return err
}

func subnetHasIPv6StackHub(subnet network.Subnet) bool {
	var prefixes []string

	// TODO: this logic is different in public azure, check that no functionality is broken by this
	if subnet.AddressPrefix != nil {
		prefixes = append(prefixes, *subnet.AddressPrefix)
	}

	for _, prefix := range prefixes {
		if utilnet.IsIPv6CIDRString(prefix) {
			return true
		}
	}
	return false
}
