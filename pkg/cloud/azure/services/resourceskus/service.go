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

package resourceskus

import (
	"context"
	"fmt"

	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/actuators"
	"k8s.io/klog/v2"
)

type ResourceSkusServiceBuilderFuncType func(*actuators.MachineScope) azure.Service

type Service struct {
	cache *Cache
	scope *actuators.MachineScope
}

// NewService creates a new groups service.
func NewService(scope *actuators.MachineScope) azure.Service {
	azureClients := actuators.AzureClients{
		SubscriptionID:          scope.SubscriptionID,
		Authorizer:              scope.Authorizer,
		ResourceManagerEndpoint: scope.ResourceManagerEndpoint,
	}
	cache, err := GetCache(azureClients, scope.Location())
	if err != nil {
		// Can't return error here, because ot he interface constraint. Nil cache is checked in the get function.
		klog.Errorf("Could not create resourceSKUs service: %v", err)
	}
	return &Service{
		cache: cache,
		scope: scope,
	}
}

// Spec holds search criteria for a SKU.
type Spec struct {
	Name         string
	ResourceType ResourceType
}

// Get returns SKU for given name and resourceType from the cache.
func (service *Service) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	if service.cache == nil {
		return nil, fmt.Errorf("resourceSKUs service cache is not initialized")
	}
	skuSpec := spec.(Spec)
	sku, err := service.cache.Get(ctx, skuSpec.Name, skuSpec.ResourceType)

	if err != nil {
		return nil, err
	}

	return sku, nil
}

// CreateOrUpdate no-op.
func (s *Service) CreateOrUpdate(ctx context.Context, spec azure.Spec) error {
	// Not implemented since there is nothing to create or update
	return nil
}

// Delete no-op.
func (s *Service) Delete(ctx context.Context, spec azure.Spec) error {
	// Not implemented since there is nothing to delete
	return nil
}
