package availabilitysets

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2021-11-01/compute"
	"github.com/Azure/go-autorest/autorest/to"
	machinecontroller "github.com/openshift/machine-api-operator/pkg/controller/machine"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/resourceskus"
)

// Spec input specification for Get/CreateOrUpdate/Delete calls
type Spec struct {
	Name string
}

// CreateOrUpdate creates or updates the availability set with the given name.
func (s *Service) CreateOrUpdate(ctx context.Context, spec azure.Spec) error {
	availabilitysetsSpec, ok := spec.(*Spec)
	if !ok {
		return errors.New("invalid availability set specification")
	}
	// Get the MaximumPlatformFaultDomainCount
	skuService := resourceskus.NewService(s.Scope)
	skuSpec := resourceskus.Spec{
		Name:         s.Scope.MachineConfig.VMSize,
		ResourceType: resourceskus.VirtualMachines,
	}
	skuI, err := skuService.Get(ctx, skuSpec)
	if err != nil {
		if errors.Is(err, resourceskus.ErrResourceNotFound) {
			return machinecontroller.InvalidMachineConfiguration("failed to obtain instance type information for VMSize '%s' from Azure: %s", skuSpec.Name, err)
		} else {
			return fmt.Errorf("failed to obtain instance type information for VMSize '%s' from Azure: %w", skuSpec.Name, err)
		}
	}

	sku := skuI.(resourceskus.SKU)

	faultDomainCountStr, ok := sku.GetCapability("MaximumPlatformFaultDomainCount")
	if !ok {
		fmt.Printf("failed to get MaximumPlatformFaultDomainCount from capabilities: %v", sku.Capabilities)
	}

	faultDomainCount, err := strconv.ParseInt(faultDomainCountStr, 10, 32)
	if err != nil {
		return fmt.Errorf("failed to parse fault domain count to int: %w", err)
	}

	asParams := compute.AvailabilitySet{
		Name: to.StringPtr(availabilitysetsSpec.Name),
		Sku: &compute.Sku{
			Name: to.StringPtr(string(compute.AvailabilitySetSkuTypesAligned)),
		},
		Location: to.StringPtr(s.Scope.Location()),
		AvailabilitySetProperties: &compute.AvailabilitySetProperties{
			PlatformFaultDomainCount:  to.Int32Ptr(int32(faultDomainCount)),
			PlatformUpdateDomainCount: to.Int32Ptr(int32(5)),
		},
		Tags: s.Scope.Tags,
	}

	_, err = s.Client.CreateOrUpdate(ctx, s.Scope.MachineConfig.ResourceGroup, availabilitysetsSpec.Name, asParams)
	if err != nil {
		return fmt.Errorf("failed to create availability set %s: %w", availabilitysetsSpec.Name, err)
	}

	return nil
}

// Get returns the availability set with the given name.
func (s *Service) Get(ctx context.Context, spec azure.Spec) (interface{}, error) {
	availabilitysetsSpec, ok := spec.(*Spec)
	if !ok {
		return nil, errors.New("invalid availability set specification")
	}

	as, err := s.Client.Get(ctx, s.Scope.MachineConfig.ResourceGroup, availabilitysetsSpec.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get availability set %s: %w", availabilitysetsSpec.Name, err)
	}

	return as, nil
}

// Delete deletes the availability set with the given name if no virtual machines are attached to it.
func (s *Service) Delete(ctx context.Context, spec azure.Spec) error {
	availabilitysetsSpec, ok := spec.(*Spec)
	if !ok {
		return errors.New("invalid availability set specification")
	}

	as, err := s.Client.Get(ctx, s.Scope.MachineConfig.ResourceGroup, availabilitysetsSpec.Name)
	if err != nil && azure.ResourceNotFound(err) {
		// already deleted
		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to get availability set %s: %w", availabilitysetsSpec.Name, err)
	}

	// only delete when the availability set does not have any vms
	if as.AvailabilitySetProperties != nil && as.VirtualMachines != nil && len(*as.VirtualMachines) > 0 {
		return nil
	}

	_, err = s.Client.Delete(ctx, s.Scope.MachineConfig.ResourceGroup, availabilitysetsSpec.Name)
	if err != nil && azure.ResourceNotFound(err) {
		// already deleted
		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to delete availability set %s: %w", availabilitysetsSpec.Name, err)
	}

	return nil
}
