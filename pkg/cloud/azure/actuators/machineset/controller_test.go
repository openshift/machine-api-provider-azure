/*
Copyright The Kubernetes Authors.
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

package machineset

import (
	"testing"

	"github.com/ghodss/yaml"
	. "github.com/onsi/gomega"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	machineproviderv1 "sigs.k8s.io/cluster-api-provider-azure/pkg/apis/azureprovider/v1beta1"
)

func TestReconcile(t *testing.T) {
	testCases := []struct {
		name                string
		vmSize              string
		existingAnnotations map[string]string
		expectedAnnotations map[string]string
		expectErr           bool
	}{
		{
			name:                "with no vmSize set",
			vmSize:              "",
			existingAnnotations: make(map[string]string),
			expectedAnnotations: make(map[string]string),
			expectErr:           true,
		},
		{
			name:                "with a Standard_D4s_v3",
			vmSize:              "Standard_D4s_v3",
			existingAnnotations: make(map[string]string),
			expectedAnnotations: map[string]string{
				cpuKey:    "4",
				memoryKey: "16384",
				gpuKey:    "0",
			},
			expectErr: false,
		},
		{
			name:                "with a Standard_NC24",
			vmSize:              "Standard_NC24",
			existingAnnotations: make(map[string]string),
			expectedAnnotations: map[string]string{
				cpuKey:    "24",
				memoryKey: "229376",
				gpuKey:    "4",
			},
			expectErr: false,
		},
		{
			name:   "with existing annotations",
			vmSize: "Standard_NC24",
			existingAnnotations: map[string]string{
				"existing": "annotation",
				"annother": "existingAnnotation",
			},
			expectedAnnotations: map[string]string{
				"existing": "annotation",
				"annother": "existingAnnotation",
				cpuKey:     "24",
				memoryKey:  "229376",
				gpuKey:     "4",
			},
			expectErr: false,
		},
		{
			name:   "with an invalid vmSize",
			vmSize: "r4.xLarge",
			existingAnnotations: map[string]string{
				"existing": "annotation",
				"annother": "existingAnnotation",
			},
			expectedAnnotations: map[string]string{
				"existing": "annotation",
				"annother": "existingAnnotation",
			},
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(tt *testing.T) {
			g := NewWithT(tt)

			machineSet, err := newTestMachineSet(tc.vmSize, tc.existingAnnotations)
			g.Expect(err).ToNot(HaveOccurred())

			_, err = reconcile(machineSet)
			g.Expect(err != nil).To(Equal(tc.expectErr))
			g.Expect(machineSet.Annotations).To(Equal(tc.expectedAnnotations))
		})
	}
}

func newTestMachineSet(vmSize string, existingAnnotations map[string]string) (*machinev1.MachineSet, error) {
	// Copy anntotations map so we don't modify the input
	annotations := make(map[string]string)
	for k, v := range existingAnnotations {
		annotations[k] = v
	}

	machineProviderSpec := &machineproviderv1.AzureMachineProviderSpec{
		VMSize: vmSize,
	}
	providerSpec, err := providerSpecFromMachine(machineProviderSpec)
	if err != nil {
		return nil, err
	}

	return &machinev1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: annotations,
		},
		Spec: machinev1.MachineSetSpec{
			Template: machinev1.MachineTemplateSpec{
				Spec: machinev1.MachineSpec{
					ProviderSpec: providerSpec,
				},
			},
		},
	}, nil
}

func providerSpecFromMachine(in *machineproviderv1.AzureMachineProviderSpec) (machinev1.ProviderSpec, error) {
	bytes, err := yaml.Marshal(in)
	if err != nil {
		return machinev1.ProviderSpec{}, err
	}
	return machinev1.ProviderSpec{
		Value: &runtime.RawExtension{Raw: bytes},
	}, nil
}
