package availabilitysets

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2021-11-01/compute"
	"github.com/Azure/go-autorest/autorest/to"
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

	existingAvailabilitySet, err := s.Client.Get(ctx, s.Scope.MachineConfig.ResourceGroup, availabilitysetsSpec.Name)
	if err != nil && !azure.ResourceNotFound(err) {
		return fmt.Errorf("failed to get availability set %s: %w", availabilitysetsSpec.Name, err)
	}

	var availabilitySet compute.AvailabilitySet
	if azure.ResourceNotFound(err) {
		faultDomainCount, err := s.getMaximumFaultDomainCount(ctx)
		if err != nil {
			return fmt.Errorf("failed to get fault domain count: %w", err)
		}

		availabilitySet = compute.AvailabilitySet{
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
	} else {
		if reflect.DeepEqual(existingAvailabilitySet.Tags, s.Scope.Tags) {
			// Availability set already exists has desired tags.
			// Other properties are immutable.
			return nil
		}

		availabilitySet = existingAvailabilitySet
		availabilitySet.Tags = s.Scope.Tags
	}

	_, err = s.Client.CreateOrUpdate(ctx, s.Scope.MachineConfig.ResourceGroup, availabilitysetsSpec.Name, availabilitySet)
	if err != nil {
		return fmt.Errorf("failed to create or update availability set %s: %w", availabilitysetsSpec.Name, err)
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

// getMaximumFaultDomainCount retrieves the MaximumPlatformFaultDomainCount from the SKU service.
func (s *Service) getMaximumFaultDomainCount(ctx context.Context) (int, error) {
	skuService := resourceskus.NewService(s.Scope)
	skuSpec := resourceskus.Spec{
		Name:         string(compute.AvailabilitySetSkuTypesAligned),
		ResourceType: resourceskus.AvailabilitySets,
	}

	defaultFaultDomainCount := 2
	skuI, err := skuService.Get(ctx, skuSpec)
	if err != nil {
		if errors.Is(err, resourceskus.ErrResourceNotFound) {
			return defaultFaultDomainCount, fmt.Errorf("failed to find aligned SKU '%s' in Azure: %w", skuSpec.Name, err)
		}
		return defaultFaultDomainCount, fmt.Errorf("failed to retrieve SKU '%s' information: %w", skuSpec.Name, err)
	}

	sku := skuI.(resourceskus.SKU)

	faultDomainCountStr, ok := sku.GetCapability(resourceskus.MaximumPlatformFaultDomainCount)
	if !ok || faultDomainCountStr == "" {
		return defaultFaultDomainCount, fmt.Errorf("MaximumPlatformFaultDomainCount not found or empty in SKU capabilities: %+v", sku.Capabilities)
	}

	parsedCount, err := strconv.ParseInt(faultDomainCountStr, 10, 32)
	if err != nil {
		return defaultFaultDomainCount, fmt.Errorf("failed to parse fault domain count: %w", err)
	}

	return int(parsedCount), nil
}
