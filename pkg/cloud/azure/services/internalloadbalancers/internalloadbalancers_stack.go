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

package internalloadbalancers

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/profiles/2019-03-01/network/mgmt/network"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/subnets"
	"k8s.io/klog/v2"
)

// Get provides information about a route table.
func (s *StackHubService) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	internalLBSpec, ok := spec.(*Spec)
	if !ok {
		return network.LoadBalancer{}, errors.New("invalid internal load balancer specification")
	}
	//lbName := fmt.Sprintf("%s-api-internallb", s.Scope.Cluster.Name)
	lb, err := s.Client.Get(ctx, s.Scope.MachineConfig.ResourceGroup, internalLBSpec.Name, "")
	if err != nil && azure.ResourceNotFound(err) {
		return nil, fmt.Errorf("load balancer %s not found: %w", internalLBSpec.Name, err)
	} else if err != nil {
		return lb, err
	}
	return lb, nil
}

// CreateOrUpdate creates or updates a route table.
func (s *StackHubService) CreateOrUpdate(ctx context.Context, spec azure.Spec) error {
	internalLBSpec, ok := spec.(*Spec)
	if !ok {
		return errors.New("invalid internal load balancer specification")
	}
	klog.V(2).Infof("creating internal load balancer %s", internalLBSpec.Name)
	probeName := "tcpHTTPSProbe"
	frontEndIPConfigName := "controlplane-internal-lbFrontEnd"
	backEndAddressPoolName := "controlplane-internal-backEndPool"
	idPrefix := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/loadBalancers", s.Scope.SubscriptionID, s.Scope.MachineConfig.ResourceGroup)
	lbName := internalLBSpec.Name

	klog.V(2).Infof("getting subnet %s", internalLBSpec.SubnetName)
	subnetInterface, err := subnets.NewService(s.Scope).Get(ctx, &subnets.Spec{Name: internalLBSpec.SubnetName, VnetName: internalLBSpec.VnetName})
	if err != nil {
		return err
	}

	subnet, ok := subnetInterface.(network.Subnet)
	if !ok {
		return errors.New("subnet Get returned invalid interface")
	}
	klog.V(2).Infof("successfully got subnet %s", internalLBSpec.SubnetName)

	// https://docs.microsoft.com/en-us/azure/load-balancer/load-balancer-standard-availability-zones#zone-redundant-by-default
	future, err := s.Client.CreateOrUpdate(ctx,
		s.Scope.MachineConfig.ResourceGroup,
		lbName,
		network.LoadBalancer{
			Sku:      &network.LoadBalancerSku{Name: network.LoadBalancerSkuNameStandard},
			Location: to.StringPtr(s.Scope.MachineConfig.Location),
			LoadBalancerPropertiesFormat: &network.LoadBalancerPropertiesFormat{
				FrontendIPConfigurations: &[]network.FrontendIPConfiguration{
					{
						Name: &frontEndIPConfigName,
						FrontendIPConfigurationPropertiesFormat: &network.FrontendIPConfigurationPropertiesFormat{
							PrivateIPAllocationMethod: network.Static,
							Subnet:                    &subnet,
							PrivateIPAddress:          to.StringPtr(internalLBSpec.IPAddress),
						},
					},
				},
				BackendAddressPools: &[]network.BackendAddressPool{
					{
						Name: &backEndAddressPoolName,
					},
				},
				Probes: &[]network.Probe{
					{
						Name: &probeName,
						ProbePropertiesFormat: &network.ProbePropertiesFormat{
							Protocol:          network.ProbeProtocolTCP,
							Port:              to.Int32Ptr(6443),
							IntervalInSeconds: to.Int32Ptr(15),
							NumberOfProbes:    to.Int32Ptr(4),
						},
					},
				},
				LoadBalancingRules: &[]network.LoadBalancingRule{
					{
						Name: to.StringPtr("LBRuleHTTPS"),
						LoadBalancingRulePropertiesFormat: &network.LoadBalancingRulePropertiesFormat{
							Protocol:             network.TransportProtocolTCP,
							FrontendPort:         to.Int32Ptr(6443),
							BackendPort:          to.Int32Ptr(6443),
							IdleTimeoutInMinutes: to.Int32Ptr(4),
							EnableFloatingIP:     to.BoolPtr(false),
							LoadDistribution:     network.Default,
							FrontendIPConfiguration: &network.SubResource{
								ID: to.StringPtr(fmt.Sprintf("/%s/%s/frontendIPConfigurations/%s", idPrefix, lbName, frontEndIPConfigName)),
							},
							BackendAddressPool: &network.SubResource{
								ID: to.StringPtr(fmt.Sprintf("/%s/%s/backendAddressPools/%s", idPrefix, lbName, backEndAddressPoolName)),
							},
							Probe: &network.SubResource{
								ID: to.StringPtr(fmt.Sprintf("/%s/%s/probes/%s", idPrefix, lbName, probeName)),
							},
						},
					},
				},
			},
		})

	if err != nil {
		return fmt.Errorf("cannot create load balancer: %w", err)
	}

	err = future.WaitForCompletionRef(ctx, s.Client.Client)
	if err != nil {
		return fmt.Errorf("cannot get internal load balancer create or update future response: %w", err)
	}

	_, err = future.Result(s.Client)
	klog.V(2).Infof("successfully created internal load balancer %s", internalLBSpec.Name)
	return err
}

// Delete deletes the route table with the provided name.
func (s *StackHubService) Delete(ctx context.Context, spec azure.Spec) error {
	internalLBSpec, ok := spec.(*Spec)
	if !ok {
		return errors.New("invalid internal load balancer specification")
	}
	klog.V(2).Infof("deleting internal load balancer %s", internalLBSpec.Name)
	f, err := s.Client.Delete(ctx, s.Scope.MachineConfig.ResourceGroup, internalLBSpec.Name)
	if err != nil && azure.ResourceNotFound(err) {
		// already deleted
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete internal load balancer %s in resource group %s: %w", internalLBSpec.Name, s.Scope.MachineConfig.ResourceGroup, err)
	}

	err = f.WaitForCompletionRef(ctx, s.Client.Client)
	if err != nil {
		return fmt.Errorf("cannot create, future response: %w", err)
	}

	_, err = f.Result(s.Client)
	if err != nil {
		return fmt.Errorf("result error: %w", err)
	}
	klog.V(2).Infof("successfully deleted internal load balancer %s", internalLBSpec.Name)
	return err
}
