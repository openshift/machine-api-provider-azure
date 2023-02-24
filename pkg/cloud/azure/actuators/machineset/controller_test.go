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
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2021-03-01/compute"
	"github.com/Azure/go-autorest/autorest/to"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gtypes "github.com/onsi/gomega/types"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/actuators"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/resourceskus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

type fakeResourceSkusService struct {
	cache *resourceskus.Cache
}

func NewFakeResourceSkusService(data []compute.ResourceSku, location string) *fakeResourceSkusService {
	return &fakeResourceSkusService{
		cache: resourceskus.NewStaticCache(data, location),
	}
}

// Get returns SKU for given name and resourceType from the cache.
func (f *fakeResourceSkusService) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	skuSpec := spec.(resourceskus.Spec)
	sku, err := f.cache.Get(ctx, skuSpec.Name, skuSpec.ResourceType)

	if err != nil {
		return nil, err
	}

	return sku, nil
}

// CreateOrUpdate no-op.
func (f *fakeResourceSkusService) CreateOrUpdate(ctx context.Context, spec azure.Spec) error {
	// Not implemented since there is nothing to create or update
	return nil
}

// Delete no-op.
func (f *fakeResourceSkusService) Delete(ctx context.Context, spec azure.Spec) error {
	// Not implemented since there is nothing to delete
	return nil
}

var _ = Describe("Reconciler", func() {
	var stopMgr context.CancelFunc
	var fakeRecorder *record.FakeRecorder
	var namespace *corev1.Namespace

	BeforeEach(func() {
		mgr, err := manager.New(cfg, manager.Options{MetricsBindAddress: "0"})
		Expect(err).ToNot(HaveOccurred())

		r := Reconciler{
			Client: k8sClient,
			Log:    log.Log,
		}
		Expect(r.SetupWithManager(mgr, controller.Options{})).To(Succeed())

		fakeRecorder = record.NewFakeRecorder(1)
		r.recorder = fakeRecorder
		fakeResourceSkusService := NewFakeResourceSkusService([]compute.ResourceSku{
			{
				Name:         to.StringPtr("Standard_D4s_v3"),
				ResourceType: to.StringPtr("virtualMachines"),
				Capabilities: &[]compute.ResourceSkuCapabilities{
					{
						Name:  to.StringPtr(resourceskus.VCPUs),
						Value: to.StringPtr("4"),
					},
					{
						Name:  to.StringPtr(resourceskus.MemoryGB),
						Value: to.StringPtr("16"),
					},
				},
			},
			{
				Name:         to.StringPtr("Standard_NC24"),
				ResourceType: to.StringPtr("virtualMachines"),
				Capabilities: &[]compute.ResourceSkuCapabilities{
					{
						Name:  to.StringPtr(resourceskus.VCPUs),
						Value: to.StringPtr("24"),
					},
					{
						Name:  to.StringPtr(resourceskus.MemoryGB),
						Value: to.StringPtr("224"),
					},
					{
						Name:  to.StringPtr(resourceskus.GPUs),
						Value: to.StringPtr("4"),
					},
				},
			},
		}, "centralus")
		r.ResourceSkusServiceBuilder = func(scope *actuators.MachineScope) azure.Service {
			return fakeResourceSkusService
		}
		stopMgr = StartTestManager(mgr)

		namespace = &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "mhc-test-"}}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
	})

	AfterEach(func() {
		Expect(deleteMachineSets(k8sClient, namespace.Name)).To(Succeed())
		stopMgr()
	})

	type reconcileTestCase = struct {
		vmSize              string
		existingAnnotations map[string]string
		expectedAnnotations map[string]string
		expectedEvents      []string
	}

	DescribeTable("when reconciling MachineSets", func(rtc reconcileTestCase) {
		machineSet, err := newTestMachineSet(namespace.Name, rtc.vmSize, rtc.existingAnnotations)
		Expect(err).ToNot(HaveOccurred())

		replicas := int32(1)
		machineSet.Spec.Replicas = &replicas
		Expect(k8sClient.Create(ctx, machineSet)).To(Succeed())

		Eventually(func() map[string]string {
			m := &machinev1.MachineSet{}
			key := client.ObjectKey{Namespace: machineSet.Namespace, Name: machineSet.Name}
			err := k8sClient.Get(ctx, key, m)
			if err != nil {
				return nil
			}
			annotations := m.GetAnnotations()
			if annotations != nil {
				return annotations
			}
			// Return an empty map to distinguish between empty annotations and errors
			return make(map[string]string)
		}, timeout).Should(Equal(rtc.expectedAnnotations))

		// Check which event types were sent
		Eventually(fakeRecorder.Events, timeout).Should(HaveLen(len(rtc.expectedEvents)))
		receivedEvents := []string{}
		eventMatchers := []gtypes.GomegaMatcher{}
		for _, ev := range rtc.expectedEvents {
			receivedEvents = append(receivedEvents, <-fakeRecorder.Events)
			eventMatchers = append(eventMatchers, ContainSubstring(fmt.Sprintf(" %s ", ev)))
		}
		Expect(receivedEvents).To(ConsistOf(eventMatchers))
	},
		Entry("with no vmSize set", reconcileTestCase{
			vmSize:              "",
			existingAnnotations: make(map[string]string),
			expectedAnnotations: make(map[string]string),
			expectedEvents:      []string{"FailedUpdate"},
		}),
		Entry("with a Standard_D4s_v3", reconcileTestCase{
			vmSize:              "Standard_D4s_v3",
			existingAnnotations: make(map[string]string),
			expectedAnnotations: map[string]string{
				cpuKey:    "4",
				memoryKey: "16384",
				gpuKey:    "0",
			},
			expectedEvents: []string{},
		}),
		Entry("with a Standard_NC24", reconcileTestCase{
			vmSize:              "Standard_NC24",
			existingAnnotations: make(map[string]string),
			expectedAnnotations: map[string]string{
				cpuKey:    "24",
				memoryKey: "229376",
				gpuKey:    "4",
			},
			expectedEvents: []string{},
		}),
		Entry("with existing annotations", reconcileTestCase{
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
			expectedEvents: []string{},
		}),
		Entry("with an invalid vmSize", reconcileTestCase{
			vmSize: "r4.xLarge",
			existingAnnotations: map[string]string{
				"existing": "annotation",
				"annother": "existingAnnotation",
			},
			expectedAnnotations: map[string]string{
				"existing": "annotation",
				"annother": "existingAnnotation",
			},
			expectedEvents: []string{"FailedUpdate"},
		}),
	)
})

func deleteMachineSets(c client.Client, namespaceName string) error {
	machineSets := &machinev1.MachineSetList{}
	err := c.List(ctx, machineSets, client.InNamespace(namespaceName))
	if err != nil {
		return err
	}

	for _, ms := range machineSets.Items {
		err := c.Delete(ctx, &ms)
		if err != nil {
			return err
		}
	}

	Eventually(func() error {
		machineSets := &machinev1.MachineSetList{}
		err := c.List(ctx, machineSets)
		if err != nil {
			return err
		}
		if len(machineSets.Items) > 0 {
			return errors.New("MachineSets not deleted")
		}
		return nil
	}, timeout).Should(Succeed())

	return nil
}

func newTestMachineSet(namespace string, vmSize string, existingAnnotations map[string]string) (*machinev1.MachineSet, error) {
	// Copy anntotations map so we don't modify the input
	annotations := make(map[string]string)
	for k, v := range existingAnnotations {
		annotations[k] = v
	}

	machineProviderSpec := &machinev1.AzureMachineProviderSpec{
		VMSize: vmSize,
	}
	providerSpec, err := providerSpecFromMachine(machineProviderSpec)
	if err != nil {
		return nil, err
	}

	return &machinev1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Annotations:  annotations,
			GenerateName: "test-machineset-",
			Namespace:    namespace,
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

func providerSpecFromMachine(in *machinev1.AzureMachineProviderSpec) (machinev1.ProviderSpec, error) {
	bytes, err := json.Marshal(in)
	if err != nil {
		return machinev1.ProviderSpec{}, err
	}
	return machinev1.ProviderSpec{
		Value: &runtime.RawExtension{Raw: bytes},
	}, nil
}
