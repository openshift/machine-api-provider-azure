package availabilitysets

import (
	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2021-03-01/compute"
	"github.com/Azure/go-autorest/autorest"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/actuators"
)

// Service provides operations on availability zones
type Service struct {
	Client compute.AvailabilitySetsClient
	Scope  *actuators.MachineScope
}

// getAvailabilitySetsClient creates a new availability zones client from subscriptionid.
func getAvailabilitySetsClient(resourceManagerEndpoint, subscriptionID string, authorizer autorest.Authorizer) compute.AvailabilitySetsClient {
	availabilitySetClient := compute.NewAvailabilitySetsClientWithBaseURI(resourceManagerEndpoint, subscriptionID)
	availabilitySetClient.Authorizer = authorizer
	availabilitySetClient.AddToUserAgent(azure.UserAgent)
	return availabilitySetClient
}

// NewService creates a new availability zones service.
func NewService(scope *actuators.MachineScope) azure.Service {
	return &Service{
		Client: getAvailabilitySetsClient(scope.ResourceManagerEndpoint, scope.SubscriptionID, scope.Authorizer),
		Scope:  scope,
	}
}
