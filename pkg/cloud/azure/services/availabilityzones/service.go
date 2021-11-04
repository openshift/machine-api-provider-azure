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

package availabilityzones

import (
	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2021-03-01/compute"
	"github.com/Azure/go-autorest/autorest"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/actuators"
)

// Service provides operations on availability zones
type Service struct {
	Client compute.ResourceSkusClient
	Scope  *actuators.MachineScope
}

// getResourceSkusClient creates a new availability zones client from subscriptionid.
func getResourceSkusClient(resourceManagerEndpoint, subscriptionID string, authorizer autorest.Authorizer) compute.ResourceSkusClient {
	skusClient := compute.NewResourceSkusClientWithBaseURI(resourceManagerEndpoint, subscriptionID)
	skusClient.Authorizer = authorizer
	skusClient.AddToUserAgent(azure.UserAgent)
	return skusClient
}

// NewService creates a new availability zones service.
func NewService(scope *actuators.MachineScope) azure.Service {
	if scope.IsStackHub() {
		return NewStackHubService(scope)
	}

	return &Service{
		Client: getResourceSkusClient(scope.ResourceManagerEndpoint, scope.SubscriptionID, scope.Authorizer),
		Scope:  scope,
	}
}
