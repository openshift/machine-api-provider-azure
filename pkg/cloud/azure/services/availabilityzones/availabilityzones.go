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
	"fmt"
	"strings"

	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
)

// Spec input specification for Get/CreateOrUpdate/Delete calls
type Spec struct {
	VMSize string
}

// Get provides information about a availability zones.
func (s *Service) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	var zones []string
	skusSpec, ok := spec.(*Spec)
	if !ok {
		return zones, errors.New("invalid availability zones specification")
	}

	filter := fmt.Sprintf("location eq '%s'", s.Scope.Location())
	res, err := s.Client.List(ctx, filter, "true")
	if err != nil {
		return zones, err
	}

	for _, resSku := range res.Values() {
		if strings.EqualFold(*resSku.Name, skusSpec.VMSize) {
			for _, locationInfo := range *resSku.LocationInfo {
				if strings.EqualFold(*locationInfo.Location, s.Scope.MachineConfig.Location) {
					zones = *locationInfo.Zones
				}
			}
		}
	}

	return zones, nil
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
