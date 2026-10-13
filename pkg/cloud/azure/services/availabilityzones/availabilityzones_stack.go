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
	"context"
	"errors"
	"strings"

	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
)

// Get provides information about a availability zones.
func (s *StackHubService) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	zones := []string{}
	skusSpec, ok := spec.(*Spec)
	if !ok {
		return zones, errors.New("invalid availability zones specification")
	}

	res, err := s.Client.List(ctx)
	if err != nil {
		return zones, err
	}

	for _, resSku := range res.Values() {
		if strings.EqualFold(*resSku.Name, skusSpec.VMSize) {
			for _, locationInfo := range *resSku.Locations {
				if strings.EqualFold(locationInfo, s.Scope.MachineConfig.Location) {
					zones = append(zones, locationInfo)
				}
			}
		}
	}

	return zones, nil
}

// CreateOrUpdate no-op.
func (s *StackHubService) CreateOrUpdate(ctx context.Context, spec azure.Spec) error {
	// Not implemented since there is nothing to create or update
	return nil
}

// Delete no-op.
func (s *StackHubService) Delete(ctx context.Context, spec azure.Spec) error {
	// Not implemented since there is nothing to delete
	return nil
}
