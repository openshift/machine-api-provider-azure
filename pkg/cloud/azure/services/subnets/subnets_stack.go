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

package subnets

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/profiles/2019-03-01/network/mgmt/network"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/routetables"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/securitygroups"
	"k8s.io/klog/v2"
)

// Get provides information about a route table.
func (s *StackHubService) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	subnetSpec, ok := spec.(*Spec)
	if !ok {
		return network.Subnet{}, errors.New("Invalid Subnet Specification")
	}
	subnet, err := s.Client.Get(ctx, s.Scope.MachineConfig.NetworkResourceGroup, subnetSpec.VnetName, subnetSpec.Name, "")
	if err != nil && azure.ResourceNotFound(err) {
		return nil, fmt.Errorf("subnet %s not found: %w", subnetSpec.Name, err)
	} else if err != nil {
		return subnet, err
	}
	return subnet, nil
}

// CreateOrUpdate creates or updates a route table.
func (s *StackHubService) CreateOrUpdate(ctx context.Context, spec azure.Spec) error {
	subnetSpec, ok := spec.(*Spec)
	if !ok {
		return errors.New("Invalid Subnet Specification")
	}
	subnetProperties := network.SubnetPropertiesFormat{
		AddressPrefix: to.StringPtr(subnetSpec.CIDR),
	}
	if subnetSpec.RouteTableName != "" {
		klog.V(2).Infof("getting route table %s", subnetSpec.RouteTableName)
		rtInterface, err := routetables.NewService(s.Scope).Get(ctx, &routetables.Spec{Name: subnetSpec.RouteTableName})
		if err != nil {
			return err
		}
		rt, rOk := rtInterface.(network.RouteTable)
		if !rOk {
			return errors.New("error getting route table")
		}
		klog.V(2).Infof("sucessfully got route table %s", subnetSpec.RouteTableName)
		subnetProperties.RouteTable = &rt
	}

	klog.V(2).Infof("getting nsg %s", subnetSpec.SecurityGroupName)
	nsgInterface, err := securitygroups.NewService(s.Scope).Get(ctx, &securitygroups.Spec{Name: subnetSpec.SecurityGroupName})
	if err != nil {
		return err
	}
	nsg, ok := nsgInterface.(network.SecurityGroup)
	if !ok {
		return errors.New("error getting network security group1")
	}
	klog.V(2).Infof("got nsg %s", subnetSpec.SecurityGroupName)
	subnetProperties.NetworkSecurityGroup = &nsg

	klog.V(2).Infof("creating subnet %s in vnet %s", subnetSpec.Name, subnetSpec.VnetName)
	f, err := s.Client.CreateOrUpdate(
		ctx,
		s.Scope.MachineConfig.NetworkResourceGroup,
		subnetSpec.VnetName,
		subnetSpec.Name,
		network.Subnet{
			Name:                   to.StringPtr(subnetSpec.Name),
			SubnetPropertiesFormat: &subnetProperties,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create subnet %s in resource group %s: %w", subnetSpec.Name, s.Scope.MachineConfig.NetworkResourceGroup, err)
	}

	err = f.WaitForCompletionRef(ctx, s.Client.Client)
	if err != nil {
		return fmt.Errorf("cannot create, future response: %w", err)
	}

	_, err = f.Result(s.Client)
	if err != nil {
		return fmt.Errorf("result error: %w", err)
	}
	klog.V(2).Infof("successfully created subnet %s in vnet %s", subnetSpec.Name, subnetSpec.VnetName)
	return err
}

// Delete deletes the route table with the provided name.
func (s *StackHubService) Delete(ctx context.Context, spec azure.Spec) error {
	subnetSpec, ok := spec.(*Spec)
	if !ok {
		return errors.New("Invalid Subnet Specification")
	}
	klog.V(2).Infof("deleting subnet %s in vnet %s", subnetSpec.Name, subnetSpec.VnetName)
	f, err := s.Client.Delete(ctx, s.Scope.MachineConfig.NetworkResourceGroup, subnetSpec.VnetName, subnetSpec.Name)
	if err != nil && azure.ResourceNotFound(err) {
		// already deleted
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete route table %s in resource group %s: %w", subnetSpec.Name, s.Scope.MachineConfig.NetworkResourceGroup, err)
	}

	err = f.WaitForCompletionRef(ctx, s.Client.Client)
	if err != nil {
		return fmt.Errorf("cannot create, future response: %w", err)
	}

	_, err = f.Result(s.Client)
	if err != nil {
		return fmt.Errorf("result error: %w", err)
	}
	klog.V(2).Infof("successfully deleted subnet %s in vnet %s", subnetSpec.Name, subnetSpec.VnetName)
	return err
}
