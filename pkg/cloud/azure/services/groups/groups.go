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

package groups

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/services/resources/mgmt/2020-06-01/resources"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
	"k8s.io/klog/v2"
)

// MicroSoftComputeVirtualMachines constant because the library does not include one for this value.
const MicroSoftComputeVirtualMachines = "Microsoft.Compute/virtualMachines"

// Get provides information about a resource group.
func (s *Service) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	return s.Client.Get(ctx, s.Scope.MachineConfig.ResourceGroup)
}

// CreateOrUpdate creates or updates a resource group.
func (s *Service) CreateOrUpdate(ctx context.Context, spec azure.Spec) error {
	klog.V(2).Infof("creating resource group %s", s.Scope.MachineConfig.ResourceGroup)
	_, err := s.Client.CreateOrUpdate(ctx, s.Scope.MachineConfig.ResourceGroup, resources.Group{Location: to.StringPtr(s.Scope.MachineConfig.Location)})
	klog.V(2).Infof("successfully created resource group %s", s.Scope.MachineConfig.ResourceGroup)
	return err
}

// Delete deletes the resource group with the provided name.
func (s *Service) Delete(ctx context.Context, spec azure.Spec) error {
	klog.V(2).Infof("deleting resource group %s", s.Scope.MachineConfig.ResourceGroup)
	future, err := s.Client.Delete(ctx, s.Scope.MachineConfig.ResourceGroup, MicroSoftComputeVirtualMachines)
	if err != nil {
		return fmt.Errorf("failed to delete resource group %s: %w", s.Scope.MachineConfig.ResourceGroup, err)
	}

	err = future.WaitForCompletionRef(ctx, s.Client.Client)
	if err != nil {
		return fmt.Errorf("cannot delete, future response: %w", err)
	}

	_, err = future.Result(s.Client)

	klog.V(2).Infof("successfully deleted resource group %s", s.Scope.MachineConfig.ResourceGroup)
	return err
}
