package virtualmachines

import (
	"reflect"
	"testing"

	"github.com/Azure/go-autorest/autorest/to"
	"k8s.io/utils/pointer"
)

func TestGetTagListFromSpecStackHub(t *testing.T) {
	testCases := []struct {
		spec     *Spec
		expected map[string]*string
	}{
		{
			spec: &Spec{
				Name: "test",
				Tags: map[string]*string{
					"foo": pointer.String("bar"),
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
		tagList := getTagListFromSpecStackHub(tc.spec)
		if !reflect.DeepEqual(tagList, tc.expected) {
			t.Errorf("Expected %v, got: %v", tc.expected, tagList)
		}
	}
}
