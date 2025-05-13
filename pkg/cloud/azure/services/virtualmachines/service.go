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

package virtualmachines

import (
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"

	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/actuators"
)

// Service provides operations on resource groups
type Service struct {
	Client *armcompute.VirtualMachinesClient
	Scope  *actuators.MachineScope
}

// NewService creates a new groups service.
func NewService(scope *actuators.MachineScope) azure.Service {
	if scope.IsStackHub() {
		return NewServiceStackHub(scope)
	}

	vmClient, err := armcompute.NewVirtualMachinesClient(scope.SubscriptionID, scope.Token, scope.ARMClientOptions())
	if err != nil {
		return azure.NewServiceClientError(err)
	}

	return &Service{
		Client: vmClient,
		Scope:  scope,
	}
}
