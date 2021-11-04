package decode

import (
	"github.com/mitchellh/mapstructure"
)

type NetworkInterface struct {
	*InterfacePropertiesFormat `json:"properties,omitempty"`
}

type InterfacePropertiesFormat struct {
	DNSSettings      *InterfaceDNSSettings       `json:"dnsSettings,omitempty"`
	IPConfigurations *[]InterfaceIPConfiguration `json:"ipConfigurations,omitempty"`
}

type InterfaceDNSSettings struct {
	InternalDomainNameSuffix *string `json:"internalDomainNameSuffix,omitempty"`
}

type InterfaceIPConfiguration struct {
	*InterfaceIPConfigurationPropertiesFormat `json:"properties,omitempty"`
}

type InterfaceIPConfigurationPropertiesFormat struct {
	PrivateIPAddress *string          `json:"privateIPAddress,omitempty"`
	PublicIPAddress  *PublicIPAddress `json:"publicIPAddress,omitempty"`
}

type PublicIPAddress struct {
	ID                               *string `json:"id,omitempty"`
	*PublicIPAddressPropertiesFormat `json:"properties,omitempty"`
}

type PublicIPAddressPropertiesFormat struct {
	IPAddress   *string                     `json:"ipAddress,omitempty"`
	DNSSettings *PublicIPAddressDNSSettings `json:"dnsSettings,omitempty"`
}

type PublicIPAddressDNSSettings struct {
	Fqdn *string `json:"fqdn,omitempty"`
}

func GetNetworkInterface(nic interface{}) (*NetworkInterface, error) {
	decodedNic := &NetworkInterface{}
	err := mapstructure.Decode(nic, &decodedNic)
	if err != nil {
		return nil, err
	}

	return decodedNic, nil
}

func GetPublicIPAdress(ip interface{}) (*PublicIPAddress, error) {
	decodedIP := &PublicIPAddress{}
	err := mapstructure.Decode(ip, &decodedIP)
	if err != nil {
		return nil, err
	}

	return decodedIP, nil
}
