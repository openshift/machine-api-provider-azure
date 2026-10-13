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

package disks

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/profiles/2019-03-01/compute/mgmt/compute"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
	"k8s.io/klog/v2"
)

// Get on disk is currently no-op. OS disks should only be deleted and will create with the VM automatically.
func (s *StackHubService) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	return compute.Disk{}, nil
}

// CreateOrUpdate on disk is currently no-op. OS disks should only be deleted and will create with the VM automatically.
func (s *StackHubService) CreateOrUpdate(ctx context.Context, spec azure.Spec) error {
	return nil
}

// Delete deletes the disk associated with a VM.
func (s *StackHubService) Delete(ctx context.Context, spec azure.Spec) error {
	diskSpec, ok := spec.(*Spec)
	if !ok {
		return errors.New("Invalid disk specification")
	}
	klog.V(2).Infof("deleting disk %s", diskSpec.Name)
	future, err := s.Client.Delete(ctx, s.Scope.MachineConfig.ResourceGroup, diskSpec.Name)
	if err != nil && azure.ResourceNotFound(err) {
		// already deleted
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to delete disk %s in resource group %s: %w", diskSpec.Name, s.Scope.MachineConfig.ResourceGroup, err)
	}

	// Do not wait until the operation completes. Just check the result
	// so the call to Delete actuator operation is async.
	_, err = future.Result(s.Client)
	if err != nil {
		return fmt.Errorf("result error: %w", err)
	}
	klog.V(2).Infof("successfully deleted disk %s", diskSpec.Name)
	return err
}
