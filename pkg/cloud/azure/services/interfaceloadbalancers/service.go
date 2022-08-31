/*
Copyright 2022 The Kubernetes Authors.

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

package interfaceloadbalancers

import (
	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2021-02-01/network"
	"github.com/Azure/go-autorest/autorest"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/actuators"
)

// Service provides operations on resource groups
type Service struct {
	Client network.InterfaceLoadBalancersClient
	Scope  *actuators.MachineScope
}

// getGroupsClient creates a new groups client from subscriptionid.
func getInterfaceLoadBalancersClient(resourceManagerEndpoint, subscriptionID string, authorizer autorest.Authorizer) network.InterfaceLoadBalancersClient {
	nicLBClient := network.NewInterfaceLoadBalancersClientWithBaseURI(resourceManagerEndpoint, subscriptionID)
	nicLBClient.Authorizer = authorizer
	nicLBClient.AddToUserAgent(azure.UserAgent)
	return nicLBClient
}

// NewService creates a new groups service.
func NewService(scope *actuators.MachineScope) azure.Service {
	if scope.IsStackHub() {
		return NewStackHubService(scope)
	}

	return &Service{
		Client: getInterfaceLoadBalancersClient(scope.ResourceManagerEndpoint, scope.SubscriptionID, scope.Authorizer),
		Scope:  scope,
	}
}
