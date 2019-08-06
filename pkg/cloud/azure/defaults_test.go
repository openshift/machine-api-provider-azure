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
	}{
		{
			name:        "Public IP name length less than 64",
			clusterName: "clusterName",
			machineName: "machine",
		},
		{
			name:        "Public IP name length with at least 64 is truncated to 63",
			clusterName: "clusterName",
			machineName: strings.Repeat("0123456789", 6),
		},
	}

	for _, test := range tests {
		name := GenerateMachinePublicIPName(test.clusterName, test.machineName)
		t.Logf("%v: generated name: %v", test.name, name)
		if len(name) > 63 {
			t.Errorf("%v: generated public IP name is longer than 63 chars (%v)", test.name, len(name))
		}
	}
}
