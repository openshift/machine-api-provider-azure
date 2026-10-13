package azure

import (
	"strings"
	"testing"
)

func TestGenerateMachinePublicIPName(t *testing.T) {
	tests := []struct {
		name        string
		clusterName string
		machineName string
		err         bool
	}{
		{
			name:        "Public IP name length less than 64",
			clusterName: "clusterName",
			machineName: "machine",
		},
		{
			name:        "Public IP name length with at least 64 errors",
			clusterName: "clusterName",
			machineName: strings.Repeat("0123456789", 6),
			err:         true,
		},
	}

	for _, test := range tests {
		name, err := GenerateMachinePublicIPName(test.clusterName, test.machineName)
		if test.err && err == nil {
			t.Errorf("Expected error, got none")
		}
		if !test.err && err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		t.Logf("%v: generated name: %v", test.name, name)
		if len(name) > 63 {
			t.Errorf("%v: generated public IP name is longer than 63 chars (%v)", test.name, len(name))
		}
	}
}

func TestGenerateMachineProviderID(t *testing.T) {
	testCases := []struct {
		name              string
		subscriptionID    string
		resourceGroupName string
		machineName       string
		expectedID        string
	}{
		{
			name:              "with all lower case strings",
			subscriptionID:    "test-subscription-id",
			resourceGroupName: "test-resource-group",
			machineName:       "test-machine",
			expectedID:        "azure:///subscriptions/test-subscription-id/resourceGroups/test-resource-group/providers/Microsoft.Compute/virtualMachines/test-machine",
		},
		{
			name:              "with capitals, lower cases subsciption id and resource group name",
			subscriptionID:    "Test-Upper-SUBSCRIPTION-Id",
			resourceGroupName: "Test-Upper-RESOURCE-GROUP",
			machineName:       "Test-MACHINE",
			expectedID:        "azure:///subscriptions/test-upper-subscription-id/resourceGroups/test-upper-resource-group/providers/Microsoft.Compute/virtualMachines/Test-MACHINE",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			id := GenerateMachineProviderID(tc.subscriptionID, tc.resourceGroupName, tc.machineName)
			if id != tc.expectedID {
				t.Errorf("Expected Id: %s, Got Id: %s", tc.expectedID, id)
			}
		})
	}
}
