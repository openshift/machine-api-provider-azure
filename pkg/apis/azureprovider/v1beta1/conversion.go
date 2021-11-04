package v1beta1

import "sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/actuators"

var (
	// MachineStatusFromProviderStatus unmarshals a raw extension into an AzureMachineProviderStatus type.
	// Deprecated: Use machine.ProviderStatusFromRawExtension instead.
	MachineStatusFromProviderStatus = actuators.ProviderStatusFromRawExtension
	// EncodeMachineStatus marshals AzureMachineProviderStatus into a raw extension type.
	// Deprecated: Use machine.RawExtensionFromProviderStatus instead.
	EncodeMachineStatus = actuators.RawExtensionFromProviderStatus
	// EncodeMachineSpec marshals AWSMachineProviderConfig into a raw extension type.
	// Deprecated: Use machine.RawExtensionFromProviderSpec instead.
	EncodeMachineSpec = actuators.RawExtensionFromProviderSpec
)
