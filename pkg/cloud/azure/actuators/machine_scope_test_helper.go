/*
Copyright 2025 The Kubernetes Authors.

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

	machinev1 "github.com/openshift/api/machine/v1beta1"
	controllerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// NewFakeMachineScope creates a MachineScope for testing purposes.
// It can set private fields that are not accessible through NewMachineScope.
// This is only intended for use in tests.
func NewFakeMachineScope(params FakeMachineScopeParams) *MachineScope {
	scope := &MachineScope{
		Context:       params.Context,
		AzureClients:  params.AzureClients,
		Machine:       params.Machine,
		CoreClient:    params.CoreClient,
		MachineConfig: params.MachineConfig,
		MachineStatus: params.MachineStatus,
		ClusterName:   params.ClusterName,
		cloudEnv:      params.CloudEnv,
		armEndpoint:   params.ARMEndpoint,
		Tags:          params.Tags,
	}
	return scope
}

// FakeMachineScopeParams defines parameters for creating a test MachineScope.
type FakeMachineScopeParams struct {
	AzureClients
	Context       context.Context
	Machine       *machinev1.Machine
	CoreClient    controllerclient.Client
	MachineConfig *machinev1.AzureMachineProviderSpec
	MachineStatus *machinev1.AzureMachineProviderStatus
	ClusterName   string
	CloudEnv      string
	ARMEndpoint   string
	Tags          map[string]*string
}
