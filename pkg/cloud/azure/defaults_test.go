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
