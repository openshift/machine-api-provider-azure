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

	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2021-02-01/network"
	"github.com/Azure/go-autorest/autorest"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/actuators"
)

// Service provides operations on resource groups
type Service struct {
	Client network.InterfacesClient
	Scope  *actuators.MachineScope
}

// ServiceWithGetID implements the azure.Service interface, but additionally
// provides a type safe method to return the ID of an object.
//
// The StackHub implementation of networkinterfaces uses a different API version
// of 'network' to the non-StackHub version, which with the legacy SDK used here
// means they return different types. To avoid potential errors from forgetting
// this difference at the point of use, GetID returns a string and handles type
// differences internally.
//
// This is useful in the virtualmachines service, which references a nic by ID
// and does not require the full object.
type ServiceWithGetID interface {
	azure.Service

	// GetID returns the ID of the object with the given name
	GetID(ctx context.Context, name string) (string, error)
}

// getGroupsClient creates a new groups client from subscriptionid.
func getNetworkInterfacesClient(resourceManagerEndpoint, subscriptionID string, authorizer autorest.Authorizer) network.InterfacesClient {
	nicClient := network.NewInterfacesClientWithBaseURI(resourceManagerEndpoint, subscriptionID)
	nicClient.Authorizer = authorizer
	nicClient.AddToUserAgent(azure.UserAgent)
	return nicClient
}

// NewService creates a new groups service.
func NewService(scope *actuators.MachineScope) ServiceWithGetID {
	if scope.IsStackHub() {
		return NewStackHubService(scope)
	}

	return &Service{
		Client: getNetworkInterfacesClient(scope.ResourceManagerEndpoint, scope.SubscriptionID, scope.Authorizer),
		Scope:  scope,
	}
}
