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
	"github.com/Azure/azure-sdk-for-go/profiles/2019-03-01/network/mgmt/network"
	"github.com/Azure/go-autorest/autorest"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/actuators"
)

// StackHub provides operations on resource groups
type StackHubService struct {
	Client network.RouteTablesClient
	Scope  *actuators.MachineScope
}

// getRouteTablesClientStackHub creates a new groups client from subscriptionid.
func getRouteTablesClientStackHub(resourceManagerEndpoint, subscriptionID string, authorizer autorest.Authorizer) network.RouteTablesClient {
	routeTablesClient := network.NewRouteTablesClientWithBaseURI(resourceManagerEndpoint, subscriptionID)
	routeTablesClient.Authorizer = authorizer
	routeTablesClient.AddToUserAgent(azure.UserAgent)
	return routeTablesClient
}

// NewStackHubService creates a new groups service.
func NewStackHubService(scope *actuators.MachineScope) azure.Service {
	return &StackHubService{
		Client: getRouteTablesClientStackHub(scope.ResourceManagerEndpoint, scope.SubscriptionID, scope.Authorizer),
		Scope:  scope,
	}
}
