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

type LoadBalancer struct {
	// LoadBalancerPropertiesFormat - Properties of load balancer.
	*LoadBalancerPropertiesFormat `json:"properties,omitempty"`

	Name *string `json:"name,omitempty"`
}

// LoadBalancerPropertiesFormat properties of the load balancer.
type LoadBalancerPropertiesFormat struct {
	FrontendIPConfigurations *[]FrontendIPConfiguration `json:"frontendIPConfigurations,omitempty"`
}

// FrontendIPConfiguration frontend IP address of the load balancer.
type FrontendIPConfiguration struct {
	// FrontendIPConfigurationPropertiesFormat - Properties of the load balancer probe.
	*FrontendIPConfigurationPropertiesFormat `json:"properties,omitempty"`
}

// FrontendIPConfigurationPropertiesFormat properties of Frontend IP Configuration of the load balancer.
type FrontendIPConfigurationPropertiesFormat struct {
	// PrivateIPAddress - The private IP address of the IP configuration.
	PrivateIPAddress *string `json:"privateIPAddress,omitempty"`
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

func GetLoadBalancers(lbs interface{}) ([]LoadBalancer, error) {
	decodedLBs := []LoadBalancer{}

	err := mapstructure.Decode(lbs, &decodedLBs)
	if err != nil {
		return nil, err
	}

	return decodedLBs, nil
}
