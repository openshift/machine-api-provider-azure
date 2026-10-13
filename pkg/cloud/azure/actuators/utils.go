package actuators

import (
	machinev1 "github.com/openshift/api/machine/v1beta1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/json"
)

// ProviderStatusFromRawExtension unmarshals a raw extension into an AzureMachineProviderStatus type.
func ProviderStatusFromRawExtension(extension *runtime.RawExtension) (*machinev1.AzureMachineProviderStatus, error) {
	if extension == nil {
		return &machinev1.AzureMachineProviderStatus{}, nil
	}

	status := new(machinev1.AzureMachineProviderStatus)
	if err := json.Unmarshal(extension.Raw, status); err != nil {
		return nil, err
	}

	return status, nil
}

// RawExtensionFromProviderStatus marshals AzureMachineProviderStatus into a raw extension type.
func RawExtensionFromProviderStatus(status *machinev1.AzureMachineProviderStatus) (*runtime.RawExtension, error) {
	if status == nil {
		return &runtime.RawExtension{}, nil
	}

	var rawBytes []byte
	var err error

	//  TODO: use apimachinery conversion https://godoc.org/k8s.io/apimachinery/pkg/runtime#Convert_runtime_Object_To_runtime_RawExtension
	if rawBytes, err = json.Marshal(status); err != nil {
		return nil, err
	}

	return &runtime.RawExtension{
		Raw: rawBytes,
	}, nil
}

// RawExtensionFromProviderSpec marshals AWSMachineProviderConfig into a raw extension type.
func RawExtensionFromProviderSpec(spec *machinev1.AzureMachineProviderSpec) (*runtime.RawExtension, error) {
	if spec == nil {
		return &runtime.RawExtension{}, nil
	}

	var rawBytes []byte
	var err error

	//  TODO: use apimachinery conversion https://godoc.org/k8s.io/apimachinery/pkg/runtime#Convert_runtime_Object_To_runtime_RawExtension
	if rawBytes, err = json.Marshal(spec); err != nil {
		return nil, err
	}

	return &runtime.RawExtension{
		Raw: rawBytes,
	}, nil
}
