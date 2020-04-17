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
	"net"

	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2018-12-01/network"
	"github.com/Azure/go-autorest/autorest/to"
	"k8s.io/klog"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/applicationsecuritygroups"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/internalloadbalancers"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/publicips"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/publicloadbalancers"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/securitygroups"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/services/subnets"
)

// Spec specification for routetable
type Spec struct {
	Name                          string
	SubnetName                    string
	VnetName                      string
	StaticIPAddress               string
	PublicLoadBalancerName        string
	InternalLoadBalancerName      string
	NatRule                       *int
	PublicIP                      string
	SecurityGroupName             string
	ApplicationSecurityGroupNames []string
}

// Get provides information about a network interface.
func (s *Service) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	nicSpec, ok := spec.(*Spec)
	if !ok {
		return network.Interface{}, errors.New("invalid network interface specification")
	}
	nic, err := s.Client.Get(ctx, s.Scope.ClusterConfig.ResourceGroup, nicSpec.Name, "")
	if err != nil && azure.ResourceNotFound(err) {
		return nil, fmt.Errorf("network interface %s not found: %w", nicSpec.Name, err)
	} else if err != nil {
		return nic, err
	}
	return nic, nil
}

// CreateOrUpdate creates or updates a network interface.
func (s *Service) CreateOrUpdate(ctx context.Context, spec azure.Spec) error {
	nicSpec, ok := spec.(*Spec)
	if !ok {
		return errors.New("invalid network interface specification")
	}

	nicProp := network.InterfacePropertiesFormat{}
	nicConfig := &network.InterfaceIPConfigurationPropertiesFormat{}

	subnetInterface, err := subnets.NewService(s.Scope).Get(ctx, &subnets.Spec{Name: nicSpec.SubnetName, VnetName: nicSpec.VnetName})
	if err != nil {
		return err
	}

	subnet, ok := subnetInterface.(network.Subnet)
	if !ok {
		return errors.New("subnet get returned invalid network interface")
	}

	nicConfig.Subnet = &network.Subnet{ID: subnet.ID}
	nicConfig.PrivateIPAllocationMethod = network.Dynamic
	if nicSpec.StaticIPAddress != "" {
		nicConfig.PrivateIPAllocationMethod = network.Static
		nicConfig.PrivateIPAddress = to.StringPtr(nicSpec.StaticIPAddress)
	}

	if nicSpec.PublicIP != "" {
		publicIPInterface, publicIPErr := publicips.NewService(s.Scope).Get(ctx, &publicips.Spec{Name: nicSpec.PublicIP})
		if publicIPErr != nil {
			return publicIPErr
		}

		ip, ok := publicIPInterface.(network.PublicIPAddress)
		if !ok {
			return errors.New("public ip get returned invalid network interface")
		}
		nicConfig.PublicIPAddress = &ip
	}

	backendAddressPools := []network.BackendAddressPool{}
	if nicSpec.PublicLoadBalancerName != "" {
		lbInterface, lberr := publicloadbalancers.NewService(s.Scope).Get(ctx, &publicloadbalancers.Spec{Name: nicSpec.PublicLoadBalancerName})
		if lberr != nil {
			return lberr
		}

		lb, ok := lbInterface.(network.LoadBalancer)
		if !ok {
			return errors.New("public load balancer get returned invalid network interface")
		}

		backendAddressPools = append(backendAddressPools,
			network.BackendAddressPool{
				ID: (*lb.BackendAddressPools)[0].ID,
			})
		if nicSpec.NatRule != nil {
			nicConfig.LoadBalancerInboundNatRules = &[]network.InboundNatRule{
				{
					ID: (*lb.InboundNatRules)[*nicSpec.NatRule].ID,
				},
			}
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
		backendAddressPools = append(backendAddressPools,
			network.BackendAddressPool{
				ID: (*internallb.BackendAddressPools)[0].ID,
			})
	}
	nicConfig.LoadBalancerBackendAddressPools = &backendAddressPools

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

	nicProp.IPConfigurations = &[]network.InterfaceIPConfiguration{
		{
			Name:                                     to.StringPtr("pipConfig"),
			InterfaceIPConfigurationPropertiesFormat: nicConfig,
		},
	}

	if subnetHasIPv6(subnet) {
		klog.V(2).Infof("Found IPv6 address space. Adding IPv6 configuration to nic: %s", nicSpec.Name)
		nicConfigIPv6 := &network.InterfaceIPConfigurationPropertiesFormat{}

		nicConfigIPv6.Subnet = &network.Subnet{ID: subnet.ID}
		nicConfigIPv6.PrivateIPAllocationMethod = network.Dynamic
		nicConfigIPv6.PrivateIPAddressVersion = network.IPv6

		ipConfigs := append(*nicProp.IPConfigurations,
			network.InterfaceIPConfiguration{
				Name:                                     to.StringPtr("pipConfig-v6"),
				InterfaceIPConfigurationPropertiesFormat: nicConfigIPv6,
			},
		)

		nicProp.IPConfigurations = &ipConfigs
	}

	f, err := s.Client.CreateOrUpdate(ctx,
		s.Scope.ClusterConfig.ResourceGroup,
		nicSpec.Name,
		network.Interface{
			Location:                  to.StringPtr(s.Scope.ClusterConfig.Location),
			InterfacePropertiesFormat: &nicProp,
		})

	if err != nil {
		return fmt.Errorf("failed to create network interface %s in resource group %s: %w", nicSpec.Name, s.Scope.ClusterConfig.ResourceGroup, err)
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
func (s *Service) Delete(ctx context.Context, spec azure.Spec) error {
	nicSpec, ok := spec.(*Spec)
	if !ok {
		return errors.New("invalid network interface Specification")
	}
	klog.V(2).Infof("deleting nic %s", nicSpec.Name)
	f, err := s.Client.Delete(ctx, s.Scope.ClusterConfig.ResourceGroup, nicSpec.Name)
	if err != nil && azure.ResourceNotFound(err) {
		// already deleted
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete network interface %s in resource group %s: %w", nicSpec.Name, s.Scope.ClusterConfig.ResourceGroup, err)
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

func subnetHasIPv6(subnet network.Subnet) bool {
	var prefixes []string

	if subnet.AddressPrefix != nil {
		prefixes = append(prefixes, *subnet.AddressPrefix)
	}

	if subnet.AddressPrefixes != nil {
		prefixes = append(prefixes, *subnet.AddressPrefixes...)
	}

	for _, prefix := range prefixes {
		ip, _, err := net.ParseCIDR(prefix)
		if err != nil {
			klog.Errorf("Error parsing subnet address prefix: %v", err)
			continue
		}

		if ip != nil && ip.To4() == nil {
			return true // Found an IPv6 subnet.
		}
	}

	return false
}
