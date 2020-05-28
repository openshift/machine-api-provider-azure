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

package azure

import (
	"errors"
	"fmt"
	"strings"
)

const (
	// DefaultUserName is the default username for created vm
	DefaultUserName = "capi"
	// DefaultVnetCIDR is the default Vnet CIDR
	DefaultVnetCIDR = "10.0.0.0/8"
	// DefaultControlPlaneSubnetCIDR is the default Control Plane Subnet CIDR
	DefaultControlPlaneSubnetCIDR = "10.0.0.0/16"
	// DefaultNodeSubnetCIDR is the default Node Subnet CIDR
	DefaultNodeSubnetCIDR = "10.1.0.0/16"
	// DefaultInternalLBIPAddress is the default internal load balancer ip address
	DefaultInternalLBIPAddress = "10.0.0.100"
	// DefaultAzureDNSZone is the default provided azure dns zone
	DefaultAzureDNSZone = "cloudapp.azure.com"
)

// GenerateVnetName generates a virtual network name, based on the cluster name.
func GenerateVnetName(clusterName string) string {
	return fmt.Sprintf("%s-%s", clusterName, "vnet")
}

// GenerateControlPlaneSecurityGroupName generates a control plane security group name, based on the cluster name.
func GenerateControlPlaneSecurityGroupName(clusterName string) string {
	return fmt.Sprintf("%s-%s", clusterName, "controlplane-nsg")
}

// GenerateNodeSecurityGroupName generates a node security group name, based on the cluster name.
func GenerateNodeSecurityGroupName(clusterName string) string {
	return fmt.Sprintf("%s-%s", clusterName, "node-nsg")
}

// GenerateNodeRouteTableName generates a node route table name, based on the cluster name.
func GenerateNodeRouteTableName(clusterName string) string {
	return fmt.Sprintf("%s-%s", clusterName, "node-routetable")
}

// GenerateControlPlaneSubnetName generates a node subnet name, based on the cluster name.
func GenerateControlPlaneSubnetName(clusterName string) string {
	return fmt.Sprintf("%s-%s", clusterName, "controlplane-subnet")
}

// GenerateNodeSubnetName generates a node subnet name, based on the cluster name.
func GenerateNodeSubnetName(clusterName string) string {
	return fmt.Sprintf("%s-%s", clusterName, "node-subnet")
}

// GenerateInternalLBName generates a internal load balancer name, based on the cluster name.
func GenerateInternalLBName(clusterName string) string {
	return fmt.Sprintf("%s-%s", clusterName, "internal-lb")
}

// GeneratePublicLBName generates a public load balancer name, based on the cluster name.
func GeneratePublicLBName(clusterName string) string {
	return fmt.Sprintf("%s-%s", clusterName, "public-lb")
}

// GeneratePublicIPName generates a public IP name, based on the cluster name and a hash.
func GeneratePublicIPName(clusterName, hash string) string {
	return fmt.Sprintf("%s-%s", clusterName, hash)
}

// GenerateMachinePublicIPName generates a public IP name for a machine, based on the cluster name and a hash.
func GenerateMachinePublicIPName(clusterName, machineName string) (string, error) {
	name := GeneratePublicIPName(clusterName, machineName)
	if len(name) < 64 {
		return name, nil
	}

	return "", errors.New("machine public IP name is longer than 63 characters")
}

// GenerateFQDN generates a fully qualified domain name, based on the public IP name and cluster location.
func GenerateFQDN(publicIPName, location string) string {
	return fmt.Sprintf("%s.%s.%s", publicIPName, location, DefaultAzureDNSZone)
}

// GenerateManagedIdentityName generates managed identity name.
func GenerateManagedIdentityName(subscriptionID, resourceGroupName, managedIdentityName string) string {
	return fmt.Sprintf(
		"/subscriptions/%s/resourcegroups/%s/providers/Microsoft.ManagedIdentity/userAssignedIdentities/%s",
		subscriptionID,
		resourceGroupName,
		managedIdentityName)
}

// GenerateMachineProviderID generates machine provider id.
func GenerateMachineProviderID(subscriptionID, resourceGroupName, machineName string) string {
	// From https://github.com/kubernetes/kubernetes/blob/e09f5c40b55c91f681a46ee17f9bc447eeacee57/pkg/cloudprovider/providers/azure/azure_standard.go#L68-L75

	// Convert subscriptionID and resourceGroupName to lowercase to match the ID the legacy cloud provider sets on the Node
	// Ref: https://github.com/kubernetes/legacy-cloud-providers/blob/2c79b47a93724ada71ae2d311d3cd9dcdab3c415/azure/azure_instances.go#L375-L377
	return fmt.Sprintf(
		"azure:///subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/virtualMachines/%s",
		strings.ToLower(subscriptionID),
		strings.ToLower(resourceGroupName),
		machineName)
}

// GenerateOSDiskName generates OS disk name used by VM
func GenerateOSDiskName(machineName string) string {
	return fmt.Sprintf("%s_OSDisk", machineName)
}

// GenerateNetworkInterfaceName generates network interface name used by VM
func GenerateNetworkInterfaceName(machineName string) string {
	return fmt.Sprintf("%s-nic", machineName)
}
