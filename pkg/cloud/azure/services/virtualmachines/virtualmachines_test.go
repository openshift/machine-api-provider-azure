package virtualmachines

import (
	"reflect"
	"testing"

	"github.com/Azure/go-autorest/autorest/to"
)

func TestGetTagListFromSpec(t *testing.T) {
	testCases := []struct {
		spec     *Spec
		expected map[string]*string
	}{
		{
			spec: &Spec{
				Name: "test",
				Tags: map[string]string{
					"foo": "bar",
				},
			},
			expected: map[string]*string{
				"foo": to.StringPtr("bar"),
			},
		},
		{
			spec: &Spec{
				Name: "test",
			},
			expected: nil,
		},
	}

	for _, tc := range testCases {
		tagList := getTagListFromSpec(tc.spec)
		if !reflect.DeepEqual(tagList, tc.expected) {
			t.Errorf("Expected %v, got: %v", tc.expected, tagList)
		}
	}
}
