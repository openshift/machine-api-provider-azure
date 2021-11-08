package decode

import (
	"github.com/mitchellh/mapstructure"
)

type VirtualMachine struct {
	*VirtualMachineProperties `json:"properties,omitempty"`
	ID                        *string   `json:"id,omitempty"`
	Zones                     *[]string `json:"zones,omitempty"`
	Location                  *string   `json:"location,omitempty"`
}

type VirtualMachineProperties struct {
	OsProfile         *OSProfile                  `json:"osProfile,omitempty"`
	NetworkProfile    *NetworkProfile             `json:"networkProfile,omitempty"`
	ProvisioningState *string                     `json:"provisioningState,omitempty"`
	InstanceView      *VirtualMachineInstanceView `json:"instanceView,omitempty"`
	HardwareProfile   *HardwareProfile            `json:"hardwareProfile,omitempty"`
}

type HardwareProfile struct {
	VMSize string `json:"vmSize,omitempty"`
}

type VirtualMachineInstanceView struct {
	Statuses *[]InstanceViewStatus `json:"statuses,omitempty"`
}

type InstanceViewStatus struct {
	Code *string `json:"code,omitempty"`
}

type OSProfile struct {
	ComputerName *string `json:"computerName,omitempty"`
}

type NetworkProfile struct {
	NetworkInterfaces *[]NetworkInterfaceReference `json:"networkInterfaces,omitempty"`
}

type NetworkInterfaceReference struct {
	*NetworkInterfaceReferenceProperties `json:"properties,omitempty"`
	ID                                   *string `json:"id,omitempty"`
}

type NetworkInterfaceReferenceProperties struct {
	Primary *bool `json:"primary,omitempty"`
}

func GetVirtualMachine(vm interface{}) (*VirtualMachine, error) {
	decodedVM := &VirtualMachine{}
	err := mapstructure.Decode(vm, &decodedVM)
	if err != nil {
		return nil, err
	}

	return decodedVM, nil
}
