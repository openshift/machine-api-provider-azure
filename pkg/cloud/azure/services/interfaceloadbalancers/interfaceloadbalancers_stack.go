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

package interfaceloadbalancers

import (
	"context"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/profiles/2019-03-01/network/mgmt/network"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
)

// Get provides information about a network interface.
func (s *StackHubService) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	ilbSpec, ok := spec.(*Spec)
	if !ok {
		return []network.LoadBalancer{}, errors.New("invalid network interface specification")
	}

	out := []network.LoadBalancer{}

	iterator, err := s.Client.ListComplete(ctx, ilbSpec.ResourceGroupName, ilbSpec.NicName)
	if err != nil {
		return []network.LoadBalancer{}, fmt.Errorf("could not list interface load balancers: %v", err)
	}

	for iterator.NotDone() {
		out = append(out, iterator.Value())

		if err := iterator.NextWithContext(ctx); err != nil {
			return nil, fmt.Errorf("failed to advance page: %v", err)
		}
	}

	return out, nil
}

// CreateOrUpdate creates or updates a network interface.
func (s *StackHubService) CreateOrUpdate(ctx context.Context, spec azure.Spec) error {
	return errors.New("not implemented")
}

// Delete deletes the network interface with the provided name.
func (s *StackHubService) Delete(ctx context.Context, spec azure.Spec) error {
	return errors.New("not implemented")
}
