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

package routetables

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2018-12-01/network"
	"github.com/Azure/go-autorest/autorest/to"
	"k8s.io/klog"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure"
)

// Spec specification for routetable
type Spec struct {
	Name string
}

// Get provides information about a route table.
func (s *Service) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	routeTableSpec, ok := spec.(*Spec)
	if !ok {
		return network.RouteTable{}, errors.New("Invalid Route Table Specification")
	}
	routeTable, err := s.Client.Get(ctx, s.Scope.MachineConfig.ResourceGroup, routeTableSpec.Name, "")
	if err != nil && azure.ResourceNotFound(err) {
		return nil, fmt.Errorf("route table %s not found: %w", routeTableSpec.Name, err)
	} else if err != nil {
		return routeTable, err
	}
	return routeTable, nil
}

// CreateOrUpdate creates or updates a route table.
func (s *Service) CreateOrUpdate(ctx context.Context, spec azure.Spec) error {
	routeTableSpec, ok := spec.(*Spec)
	if !ok {
		return errors.New("Invalid Route Table Specification")
	}
	klog.V(2).Infof("creating route table %s", routeTableSpec.Name)
	f, err := s.Client.CreateOrUpdate(
		ctx,
		s.Scope.MachineConfig.ResourceGroup,
		routeTableSpec.Name,
		network.RouteTable{
			Location:                   to.StringPtr(s.Scope.MachineConfig.Location),
			RouteTablePropertiesFormat: &network.RouteTablePropertiesFormat{},
		},
	)
	if err != nil {
		return fmt.Errorf("failed to create route table %s in resource group %s: %w", routeTableSpec.Name, s.Scope.MachineConfig.ResourceGroup, err)
	}

	err = f.WaitForCompletionRef(ctx, s.Client.Client)
	if err != nil {
		return fmt.Errorf("cannot create, future response: %w", err)
	}

	_, err = f.Result(s.Client)
	if err != nil {
		return fmt.Errorf("result error: %w", err)
	}
	klog.V(2).Infof("successfully created route table %s", routeTableSpec.Name)
	return err
}

// Delete deletes the route table with the provided name.
func (s *Service) Delete(ctx context.Context, spec azure.Spec) error {
	routeTableSpec, ok := spec.(*Spec)
	if !ok {
		return errors.New("Invalid Route Table Specification")
	}
	klog.V(2).Infof("deleting route table %s", routeTableSpec.Name)
	f, err := s.Client.Delete(ctx, s.Scope.MachineConfig.ResourceGroup, routeTableSpec.Name)
	if err != nil && azure.ResourceNotFound(err) {
		// already deleted
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete route table %s in resource group %s: %w", routeTableSpec.Name, s.Scope.MachineConfig.ResourceGroup, err)
	}

	err = f.WaitForCompletionRef(ctx, s.Client.Client)
	if err != nil {
		return fmt.Errorf("cannot create, future response: %w", err)
	}

	_, err = f.Result(s.Client)
	if err != nil {
		return fmt.Errorf("result error: %w", err)
	}
	klog.V(2).Infof("successfully deleted route table %s", routeTableSpec.Name)
	return err
}
