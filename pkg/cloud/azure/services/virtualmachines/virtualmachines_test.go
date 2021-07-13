package virtualmachines

import (
	"fmt"
	"reflect"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2021-03-01/compute"
	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2021-02-01/network"
	"github.com/Azure/go-autorest/autorest/to"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/apis/azureprovider/v1beta1"
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

func TestDeriveVirtualMachineParameters(t *testing.T) {
	testCases := []struct {
		name       string
		updateSpec func(*Spec)
		validate   func(*WithT, *compute.VirtualMachine)
	}{
		{
			name:       "Unspecified security profile",
			updateSpec: nil,
			validate: func(g *WithT, vm *compute.VirtualMachine) {
				g.Expect(vm.SecurityProfile).To(BeNil())
			},
		},
		{
			name: "Security profile with EncryptionAtHost set to true",
			updateSpec: func(vmSpec *Spec) {
				vmSpec.SecurityProfile = &v1beta1.SecurityProfile{EncryptionAtHost: to.BoolPtr(true)}
			},
			validate: func(g *WithT, vm *compute.VirtualMachine) {
				g.Expect(vm.SecurityProfile).ToNot(BeNil())
				g.Expect(vm.SecurityProfile.EncryptionAtHost).ToNot(BeNil())
				g.Expect(*vm.SecurityProfile.EncryptionAtHost).To(BeTrue())
			},
		},
		{
			name: "Security profile with EncryptionAtHost set to false",
			updateSpec: func(vmSpec *Spec) {
				vmSpec.SecurityProfile = &v1beta1.SecurityProfile{EncryptionAtHost: to.BoolPtr(false)}
			},
			validate: func(g *WithT, vm *compute.VirtualMachine) {
				g.Expect(vm.SecurityProfile).ToNot(BeNil())
				g.Expect(vm.SecurityProfile.EncryptionAtHost).ToNot(BeNil())
				g.Expect(*vm.SecurityProfile.EncryptionAtHost).To(BeFalse())
			},
		},
		{
			name: "Security profile with EncryptionAtHost unset",
			updateSpec: func(vmSpec *Spec) {
				vmSpec.SecurityProfile = &v1beta1.SecurityProfile{EncryptionAtHost: nil}
			},
			validate: func(g *WithT, vm *compute.VirtualMachine) {
				g.Expect(vm.SecurityProfile).ToNot(BeNil())
				g.Expect(vm.SecurityProfile.EncryptionAtHost).To(BeNil())
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			vmSpec := getTestVMSpec(tc.updateSpec)
			subscription := "226e02ba-43d1-43d3-a02a-19e584a4ef67"
			resourcegroup := "foobar"
			location := "eastus"
			nic := getTestNic(vmSpec, subscription, resourcegroup, location)

			vm, err := deriveVirtualMachineParameters(vmSpec, location, subscription, nic)

			g.Expect(err).ToNot(HaveOccurred())
			tc.validate(g, vm)
		})
	}
}

func getTestNic(vmSpec *Spec, subscription, resourcegroup, location string) network.Interface {
	return network.Interface{
		Etag:     to.StringPtr("foobar"),
		ID:       to.StringPtr(fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Network/networkInterfaces/%s", subscription, resourcegroup, vmSpec.NICName)),
		Name:     &vmSpec.NICName,
		Type:     to.StringPtr("awesome"),
		Location: to.StringPtr("location"),
		Tags:     map[string]*string{},
	}
}

func getTestVMSpec(updateSpec func(*Spec)) *Spec {
	spec := &Spec{
		Name:       "my-awesome-machine",
		NICName:    "gxqb-master-nic",
		SSHKeyData: "",
		Size:       "Standard_D4s_v3",
		Zone:       "",
		Image: v1beta1.Image{
			Publisher: "Red Hat Inc",
			Offer:     "ubi",
			SKU:       "ubi7",
			Version:   "latest",
		},
		OSDisk: v1beta1.OSDisk{
			OSType:     "Linux",
			DiskSizeGB: 256,
		},
		CustomData:      "",
		ManagedIdentity: "",
		Tags:            map[string]string{},
		Priority:        compute.VirtualMachinePriorityTypesRegular,
		EvictionPolicy:  compute.VirtualMachineEvictionPolicyTypesDelete,
		BillingProfile:  nil,
		SecurityProfile: nil,
	}

	if updateSpec != nil {
		updateSpec(spec)
	}

	return spec
}
