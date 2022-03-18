package virtualmachines

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2021-03-01/compute"
	"github.com/Azure/azure-sdk-for-go/services/network/mgmt/2021-02-01/network"
	"github.com/Azure/go-autorest/autorest/to"
	. "github.com/onsi/gomega"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	apierrors "github.com/openshift/machine-api-operator/pkg/controller/machine"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/actuators"
	"k8s.io/apimachinery/pkg/api/resource"
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
		name          string
		updateSpec    func(*Spec)
		validate      func(*WithT, *compute.VirtualMachine)
		expectedError error
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
				vmSpec.SecurityProfile = &machinev1.SecurityProfile{EncryptionAtHost: to.BoolPtr(true)}
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
				vmSpec.SecurityProfile = &machinev1.SecurityProfile{EncryptionAtHost: to.BoolPtr(false)}
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
				vmSpec.SecurityProfile = &machinev1.SecurityProfile{EncryptionAtHost: nil}
			},
			validate: func(g *WithT, vm *compute.VirtualMachine) {
				g.Expect(vm.SecurityProfile).ToNot(BeNil())
				g.Expect(vm.SecurityProfile.EncryptionAtHost).To(BeNil())
			},
		},
		{
			name:       "Non-ThirdParty Marketplace Image",
			updateSpec: nil,
			validate: func(g *WithT, vm *compute.VirtualMachine) {
				g.Expect(vm.Plan).To(BeNil())
			},
		},
		{
			name: "ThirdParty Marketplace Image",
			updateSpec: func(vmSpec *Spec) {
				vmSpec.Image.Type = machinev1.AzureImageTypeMarketplaceWithPlan
			},
			validate: func(g *WithT, vm *compute.VirtualMachine) {
				g.Expect(vm.Plan).ToNot(BeNil())

			},
		},
		{
			name: "AdditionalCapabilities.UltraSSDEnabled to true with an Ultra Disk Data Disk",
			updateSpec: func(vmSpec *Spec) {
				vmSpec.DataDisks = []machinev1.DataDisk{
					{
						NameSuffix: "ultradisk-test",
						DiskSizeGB: 4,
						Lun:        0,
						ManagedDisk: machinev1.DataDiskManagedDiskParameters{
							StorageAccountType: machinev1.StorageAccountUltraSSDLRS,
						},
					},
				}
			},
			validate: func(g *WithT, vm *compute.VirtualMachine) {
				g.Expect(*vm.AdditionalCapabilities.UltraSSDEnabled).To(BeTrue())
			},
		},
		{
			name: "AdditionalCapabilities.UltraSSDEnabled to true with Ultra Disk and UltraSSDCapability enabled",
			updateSpec: func(vmSpec *Spec) {
				vmSpec.DataDisks = []machinev1.DataDisk{
					{
						NameSuffix: "test",
						DiskSizeGB: 4,
						Lun:        0,
						ManagedDisk: machinev1.DataDiskManagedDiskParameters{
							StorageAccountType: machinev1.StorageAccountUltraSSDLRS,
						},
					},
				}
			},
			validate: func(g *WithT, vm *compute.VirtualMachine) {
				g.Expect(*vm.AdditionalCapabilities.UltraSSDEnabled).To(BeTrue())
			},
		},
		{
			name: "AdditionalCapabilities.UltraSSDEnabled to true with no Ultra Disks but UltraSSDCapability enabled",
			updateSpec: func(vmSpec *Spec) {
				vmSpec.UltraSSDCapability = machinev1.AzureUltraSSDCapabilityEnabled
			},
			validate: func(g *WithT, vm *compute.VirtualMachine) {
				g.Expect(*vm.AdditionalCapabilities.UltraSSDEnabled).To(BeTrue())
			},
		},
		{
			name: "AdditionalCapabilities.UltraSSDEnabled to false with UltraSSDCapability disabled",
			updateSpec: func(vmSpec *Spec) {
				vmSpec.UltraSSDCapability = machinev1.AzureUltraSSDCapabilityDisabled
			},
			validate: func(g *WithT, vm *compute.VirtualMachine) {
				g.Expect(*vm.AdditionalCapabilities.UltraSSDEnabled).To(BeFalse())
			},
		},
		{
			name: "AdditionalCapabilities.UltraSSDEnabled to false with Ultra Disks but UltraSSDCapability disabled",
			updateSpec: func(vmSpec *Spec) {
				vmSpec.UltraSSDCapability = machinev1.AzureUltraSSDCapabilityDisabled
				vmSpec.DataDisks = []machinev1.DataDisk{
					{
						NameSuffix: "test",
						DiskSizeGB: 4,
						Lun:        0,
						ManagedDisk: machinev1.DataDiskManagedDiskParameters{
							StorageAccountType: machinev1.StorageAccountUltraSSDLRS,
						},
					},
				}
			},
			validate: func(g *WithT, vm *compute.VirtualMachine) {
				g.Expect(*vm.AdditionalCapabilities.UltraSSDEnabled).To(BeFalse())
			},
		},
		{
			name: "AdditionalCapabilities.UltraSSDEnabled to nil with a non Ultra Disk Data Disk",
			updateSpec: func(vmSpec *Spec) {
				vmSpec.DataDisks = []machinev1.DataDisk{
					{
						NameSuffix: "premiumdisk-test",
						DiskSizeGB: 4,
						Lun:        0,
						ManagedDisk: machinev1.DataDiskManagedDiskParameters{
							StorageAccountType: machinev1.StorageAccountPremiumLRS,
						},
					},
				}
			},
			validate: func(g *WithT, vm *compute.VirtualMachine) {
				if vm.AdditionalCapabilities != nil {
					g.Expect(vm.AdditionalCapabilities.UltraSSDEnabled).To(BeNil())
				} else {
					g.Expect(vm.AdditionalCapabilities).To(BeNil())
				}
			},
		},
		{
			name: "Error when Data Disk lun is too high",
			updateSpec: func(vmSpec *Spec) {
				vmSpec.Name = "testvm"
				vmSpec.DataDisks = []machinev1.DataDisk{
					{
						NameSuffix: "datadisk-test",
						DiskSizeGB: 4,
						Lun:        100,
						ManagedDisk: machinev1.DataDiskManagedDiskParameters{
							StorageAccountType: machinev1.StorageAccountPremiumLRS,
						},
					},
				}
			},
			expectedError: fmt.Errorf("failed to generate data disk spec: %w", apierrors.InvalidMachineConfiguration("failed to create Data Disk: %s for vm %s. "+
				"Invalid value `lun`: %d. `lun` cannot be lower than 0 or higher than 63.",
				"testvm"+"_"+"datadisk-test", "testvm", 100)),
		},
		{
			name: "Error when Data Disk lun is too low",
			updateSpec: func(vmSpec *Spec) {
				vmSpec.Name = "testvm"
				vmSpec.DataDisks = []machinev1.DataDisk{
					{
						NameSuffix: "datadisk-test",
						DiskSizeGB: 4,
						Lun:        -1,
						ManagedDisk: machinev1.DataDiskManagedDiskParameters{
							StorageAccountType: machinev1.StorageAccountPremiumLRS,
						},
					},
				}
			},
			expectedError: fmt.Errorf("failed to generate data disk spec: %w", apierrors.InvalidMachineConfiguration("failed to create Data Disk: %s for vm %s. "+
				"Invalid value `lun`: %d. `lun` cannot be lower than 0 or higher than 63.",
				"testvm"+"_"+"datadisk-test", "testvm", -1)),
		},
		{
			name: "Error when Data Disks luns are not unique",
			updateSpec: func(vmSpec *Spec) {
				vmSpec.Name = "testvm"
				vmSpec.DataDisks = []machinev1.DataDisk{
					{
						NameSuffix: "datadisk-test",
						DiskSizeGB: 4,
						Lun:        0,
						ManagedDisk: machinev1.DataDiskManagedDiskParameters{
							StorageAccountType: machinev1.StorageAccountPremiumLRS,
						},
					},
					{
						NameSuffix: "datadisk-test-2",
						DiskSizeGB: 4,
						Lun:        0,
						ManagedDisk: machinev1.DataDiskManagedDiskParameters{
							StorageAccountType: machinev1.StorageAccountPremiumLRS,
						},
					},
				}
			},
			expectedError: fmt.Errorf("failed to generate data disk spec: %w",
				apierrors.InvalidMachineConfiguration("failed to create Data Disk: %s for vm %s. "+
					"A Data Disk with `lun`: %d, already exists. `lun` must be unique.",
					"testvm"+"_"+"datadisk-test-2", "testvm", 0)),
		},
		{
			name: "Error when Data Disks nameSuffixes are not unique",
			updateSpec: func(vmSpec *Spec) {
				vmSpec.Name = "testvm"
				vmSpec.DataDisks = []machinev1.DataDisk{
					{
						NameSuffix: "datadisk-test",
						DiskSizeGB: 4,
						Lun:        0,
						ManagedDisk: machinev1.DataDiskManagedDiskParameters{
							StorageAccountType: machinev1.StorageAccountPremiumLRS,
						},
					},
					{
						NameSuffix: "datadisk-test",
						DiskSizeGB: 4,
						Lun:        1,
						ManagedDisk: machinev1.DataDiskManagedDiskParameters{
							StorageAccountType: machinev1.StorageAccountPremiumLRS,
						},
					},
				}
			},
			expectedError: fmt.Errorf("failed to generate data disk spec: %w",
				apierrors.InvalidMachineConfiguration("failed to create Data Disk: %s for vm %s. "+
					"A Data Disk with `nameSuffix`: %s, already exists. `nameSuffix` must be unique.",
					"testvm"+"_"+"datadisk-test", "testvm", "datadisk-test")),
		},
		{
			name: "Error when Data Disk nameSuffix is invalid",
			updateSpec: func(vmSpec *Spec) {
				vmSpec.Name = "testvm"
				vmSpec.DataDisks = []machinev1.DataDisk{
					{
						NameSuffix: "inv$alid",
						DiskSizeGB: 4,
						Lun:        0,
						ManagedDisk: machinev1.DataDiskManagedDiskParameters{
							StorageAccountType: machinev1.StorageAccountPremiumLRS,
						},
					},
				}
			},
			expectedError: fmt.Errorf("failed to generate data disk spec: %w",
				apierrors.InvalidMachineConfiguration("failed to create Data Disk: %s for vm %s. "+
					"The nameSuffix can only contain letters, numbers, "+
					"underscores, periods or hyphens. Check your `nameSuffix`.",
					"testvm"+"_"+"inv$alid", "testvm")),
		},
		{
			name: "Error when Data Disk nameSuffix is too long",
			updateSpec: func(vmSpec *Spec) {
				vmSpec.Name = "testvm"
				vmSpec.DataDisks = []machinev1.DataDisk{
					{
						NameSuffix: "qwkuid031j3x3fxktj9saez28zoo2843jkl35w3ner90i9wvwkqphau1l5y7j7k3750960btqljnlthoq",
						DiskSizeGB: 4,
						Lun:        0,
						ManagedDisk: machinev1.DataDiskManagedDiskParameters{
							StorageAccountType: machinev1.StorageAccountPremiumLRS,
						},
					},
				}
			},
			expectedError: fmt.Errorf("failed to generate data disk spec: %w",
				apierrors.InvalidMachineConfiguration("failed to create Data Disk: %s for vm %s. "+
					"The overall disk name name must not exceed 80 chars in length. Check your `nameSuffix`.",
					"testvm"+"_"+"qwkuid031j3x3fxktj9saez28zoo2843jkl35w3ner90i9wvwkqphau1l5y7j7k3750960btqljnlthoq", "testvm")),
		},
		{
			name: "Error when Data Disk is Ultra Disk and cachingType not None",
			updateSpec: func(vmSpec *Spec) {
				vmSpec.Name = "testvm"
				vmSpec.DataDisks = []machinev1.DataDisk{
					{
						NameSuffix: "datadisk-test",
						DiskSizeGB: 4,
						Lun:        0,
						ManagedDisk: machinev1.DataDiskManagedDiskParameters{
							StorageAccountType: machinev1.StorageAccountUltraSSDLRS,
						},
						CachingType: machinev1.CachingTypeReadOnly,
					},
				}
			},
			expectedError: fmt.Errorf("failed to generate data disk spec: %w",
				apierrors.InvalidMachineConfiguration("failed to create Data Disk: %s for vm %s. "+
					"`cachingType`: %s, is not supported for Data Disk of `storageAccountType`: \"%s\". "+
					"Use `storageAccountType`: \"%s\" instead.",
					"testvm"+"_"+"datadisk-test", "testvm",
					machinev1.CachingTypeReadOnly, machinev1.StorageAccountUltraSSDLRS, machinev1.CachingTypeNone)),
		},
		{
			name: "Error when Data Disk is smaller than 4GB",
			updateSpec: func(vmSpec *Spec) {
				vmSpec.Name = "testvm"
				vmSpec.DataDisks = []machinev1.DataDisk{
					{
						NameSuffix: "datadisk-test",
						DiskSizeGB: 3,
						Lun:        0,
						ManagedDisk: machinev1.DataDiskManagedDiskParameters{
							StorageAccountType: machinev1.StorageAccountUltraSSDLRS,
						},
						CachingType: machinev1.CachingTypeReadOnly,
					},
				}
			},
			expectedError: fmt.Errorf("failed to generate data disk spec: %w",
				apierrors.InvalidMachineConfiguration("failed to create Data Disk: %s for vm %s. "+
					"`diskSizeGB`: %d, is invalid, disk size must be greater or equal than 4.",
					"testvm"+"_"+"datadisk-test", "testvm", 3)),
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

			s := Service{
				Scope: &actuators.MachineScope{
					AzureClients: actuators.AzureClients{
						SubscriptionID: subscription,
					},
					MachineConfig: &machinev1.AzureMachineProviderSpec{
						Location:      location,
						ResourceGroup: resourcegroup,
					},
				},
			}

			vm, err := s.deriveVirtualMachineParameters(vmSpec, nic)

			if tc.expectedError != nil {
				g.Expect(err).To(MatchError(tc.expectedError))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				tc.validate(g, vm)
			}
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
		Image: machinev1.Image{
			Publisher: "Red Hat Inc",
			Offer:     "ubi",
			SKU:       "ubi7",
			Version:   "latest",
		},
		OSDisk: machinev1.OSDisk{
			OSType:     "Linux",
			DiskSizeGB: 256,
		},
		DataDisks: []machinev1.DataDisk{
			{
				DiskSizeGB: 4,
				NameSuffix: "testdata",
				Lun:        0,
			},
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

func TestGetSpotVMOptions(t *testing.T) {
	maxPrice := resource.MustParse("0.001")
	maxPriceFloat, err := strconv.ParseFloat(maxPrice.AsDec().String(), 64)
	if err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name           string
		spotVMOptions  *machinev1.SpotVMOptions
		priority       compute.VirtualMachinePriorityTypes
		evictionPolicy compute.VirtualMachineEvictionPolicyTypes
		billingProfile *compute.BillingProfile
	}{
		{
			name: "get spot vm option succefully",
			spotVMOptions: &machinev1.SpotVMOptions{
				MaxPrice: &maxPrice,
			},
			priority:       compute.VirtualMachinePriorityTypesSpot,
			evictionPolicy: compute.VirtualMachineEvictionPolicyTypesDeallocate,
			billingProfile: &compute.BillingProfile{
				MaxPrice: &maxPriceFloat,
			},
		},
		{
			name:           "return empty values on missing options",
			spotVMOptions:  nil,
			priority:       "",
			evictionPolicy: "",
			billingProfile: nil,
		},
		{
			name:           "not return an error with empty spot vm options",
			spotVMOptions:  &machinev1.SpotVMOptions{},
			priority:       compute.VirtualMachinePriorityTypesSpot,
			evictionPolicy: compute.VirtualMachineEvictionPolicyTypesDeallocate,
			billingProfile: &compute.BillingProfile{
				MaxPrice: nil,
			},
		},
		{
			name: "not return an error if the max price is nil",
			spotVMOptions: &machinev1.SpotVMOptions{
				MaxPrice: nil,
			},
			priority:       compute.VirtualMachinePriorityTypesSpot,
			evictionPolicy: compute.VirtualMachineEvictionPolicyTypesDeallocate,
			billingProfile: &compute.BillingProfile{
				MaxPrice: nil,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			priority, evictionPolicy, billingProfile, err := getSpotVMOptions(tc.spotVMOptions)
			if err != nil {
				t.Fatal(err)
			}

			if priority != tc.priority {
				t.Fatalf("Expected priority %s, got: %s", priority, tc.priority)
			}

			if evictionPolicy != tc.evictionPolicy {
				t.Fatalf("Expected eviction policy %s, got: %s", evictionPolicy, tc.evictionPolicy)
			}

			// only check billing profile when spotVMOptions object is not nil
			if tc.spotVMOptions != nil {
				if tc.billingProfile.MaxPrice != nil {
					if billingProfile == nil {
						t.Fatal("Expected billing profile to not be nil")
					} else if *billingProfile.MaxPrice != *tc.billingProfile.MaxPrice {
						t.Fatalf("Expected billing profile max price %d, got: %d", billingProfile, tc.billingProfile)
					}
				}
			} else {
				if billingProfile != nil {
					t.Fatal("Expected billing profile to be nil")
				}
			}
		})
	}
}
