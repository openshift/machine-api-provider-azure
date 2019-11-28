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

package actuators

import (
	"context"

	"sigs.k8s.io/cluster-api-provider-azure/pkg/apis/azureprovider/v1alpha1"
)

// ScopeParams defines the input parameters used to create a new Scope.
type ScopeParams struct {
	AzureClients
}

// NewScope creates a new Scope from the supplied parameters.
// This is meant to be called for each different actuator iteration.
func NewScope(params ScopeParams) (*Scope, error) {
	return &Scope{
		AzureClients:  params.AzureClients,
		ClusterConfig: &v1alpha1.AzureClusterProviderSpec{},
		Context:       context.Background(),
	}, nil
}

// Scope defines the basic context for an actuator to operate upon.
type Scope struct {
	AzureClients
	ClusterConfig *v1alpha1.AzureClusterProviderSpec
	Context       context.Context
}

// Vnet returns the cluster Vnet.
func (s *Scope) Vnet() *v1alpha1.VnetSpec {
	return &s.ClusterConfig.NetworkSpec.Vnet
}

// Subnets returns the cluster subnets.
func (s *Scope) Subnets() v1alpha1.Subnets {
	return s.ClusterConfig.NetworkSpec.Subnets
}

// Location returns the cluster location.
func (s *Scope) Location() string {
	return s.ClusterConfig.Location
}

// Network returns the cluster network object.
func (s *Scope) Network() *v1alpha1.Network {
	return nil
}
