/*
Copyright 2018 The Kubernetes Authors.

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

package machine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Azure/go-autorest/autorest"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	machineapierrors "github.com/openshift/machine-api-operator/pkg/controller/machine"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog/v2"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/actuators"
	controllerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	createEventAction = "Create"
	updateEventAction = "Update"
	deleteEventAction = "Delete"
	noEventAction     = ""
)

//+kubebuilder:rbac:groups=azureprovider.k8s.io,resources=azuremachineproviderconfigs;azuremachineproviderstatuses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cluster.k8s.io,resources=machines;machines/status;machinedeployments;machinedeployments/status;machinesets;machinesets/status;machineclasses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=cluster.k8s.io,resources=clusters;clusters/status,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=nodes;events,verbs=get;list;watch;create;update;patch;delete

// Actuator is responsible for performing machine reconciliation.
type Actuator struct {
	coreClient    controllerclient.Client
	eventRecorder record.EventRecorder

	reconcilerBuilder func(scope *actuators.MachineScope) *Reconciler
}

// ActuatorParams holds parameter information for Actuator.
type ActuatorParams struct {
	CoreClient        controllerclient.Client
	EventRecorder     record.EventRecorder
	ReconcilerBuilder func(scope *actuators.MachineScope) *Reconciler
}

// NewActuator returns an actuator.
func NewActuator(params ActuatorParams) *Actuator {
	return &Actuator{
		coreClient:        params.CoreClient,
		eventRecorder:     params.EventRecorder,
		reconcilerBuilder: params.ReconcilerBuilder,
	}
}

// Set corresponding event based on error. It also returns the original error
// for convenience, so callers can do "return handleMachineError(...)".
func (a *Actuator) handleMachineError(machine *machinev1.Machine, err *machineapierrors.MachineError, eventAction string) error {
	if eventAction != noEventAction {
		a.eventRecorder.Eventf(machine, corev1.EventTypeWarning, "Failed"+eventAction, "%v: %v", err.Reason, err.Message)
	}

	klog.Errorf("Machine error: %v", err.Message)
	return err
}

// Create creates a machine and is invoked by the machine controller.
func (a *Actuator) Create(ctx context.Context, machine *machinev1.Machine) error {
	klog.Infof("Creating machine %v", machine.Name)

	scope, err := actuators.NewMachineScope(actuators.MachineScopeParams{
		Machine:    machine,
		CoreClient: a.coreClient,
	})
	if err != nil {
		return a.handleMachineError(machine, machineapierrors.InvalidMachineConfiguration("failed to create machine %q scope: %v", machine.Name, err), createEventAction)

	}

	err = a.reconcilerBuilder(scope).Create(context.Background())
	if err != nil {
		// We still want to persist on failure to update MachineStatus
		if err := scope.Persist(); err != nil {
			klog.Errorf("Error storing machine info: %v", err)
		}

		var detailedError autorest.DetailedError
		if errors.As(err, &detailedError) {
			statusCode, ok := detailedError.StatusCode.(int)
			if ok && statusCode >= 400 && statusCode < 500 {
				return a.handleMachineError(machine, machineapierrors.InvalidMachineConfiguration("failed to reconcile machine %q: %v", machine.Name, detailedError), createEventAction)
			}
		}

		var machineErr *machineapierrors.MachineError
		if errors.As(err, &machineErr) {
			return a.handleMachineError(machine, machineapierrors.InvalidMachineConfiguration("failed to reconcile machine %q: %v", machine.Name, err), createEventAction)
		}

		a.handleMachineError(machine, machineapierrors.CreateMachine("failed to reconcile machine %qs: %v", machine.Name, err), createEventAction)

		return &machineapierrors.RequeueAfterError{
			RequeueAfter: 20 * time.Second,
		}
	}

	if err := scope.Persist(); err != nil {
		return fmt.Errorf("error storing machine info: %v", err)
	}

	a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, "Created", "Created machine %q", machine.Name)

	return nil
}

// Delete deletes a machine and is invoked by the Machine Controller.
func (a *Actuator) Delete(ctx context.Context, machine *machinev1.Machine) error {
	klog.Infof("Deleting machine %v", machine.Name)

	scope, err := actuators.NewMachineScope(actuators.MachineScopeParams{
		Machine:    machine,
		CoreClient: a.coreClient,
	})
	if err != nil {
		return a.handleMachineError(machine, machineapierrors.DeleteMachine("failed to create machine %q scope: %v", machine.Name, err), deleteEventAction)
	}

	err = a.reconcilerBuilder(scope).Delete(context.Background())
	if err != nil {
		// We still want to persist on failure to update MachineStatus
		if err := scope.Persist(); err != nil {
			klog.Errorf("Error storing machine info: %v", err)
		}
		a.handleMachineError(machine, machineapierrors.DeleteMachine("failed to delete machine %q: %v", machine.Name, err), deleteEventAction)
		return &machineapierrors.RequeueAfterError{
			RequeueAfter: 20 * time.Second,
		}
	}

	if err := scope.Persist(); err != nil {
		return fmt.Errorf("error storing machine info: %v", err)
	}

	a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, "Deleted", "Deleted machine %q", machine.Name)

	return nil
}

// Update updates a machine and is invoked by the Machine Controller.
// If the Update attempts to mutate any immutable state, the method will error
// and no updates will be performed.
func (a *Actuator) Update(ctx context.Context, machine *machinev1.Machine) error {
	klog.Infof("Updating machine %v", machine.Name)

	scope, err := actuators.NewMachineScope(actuators.MachineScopeParams{
		Machine:    machine,
		CoreClient: a.coreClient,
	})
	if err != nil {
		return a.handleMachineError(machine, machineapierrors.UpdateMachine("failed to create machine %q scope: %v", machine.Name, err), updateEventAction)
	}

	err = a.reconcilerBuilder(scope).Update(context.Background())
	if err != nil {
		// We still want to persist on failure to update MachineStatus
		if err := scope.Persist(); err != nil {
			klog.Errorf("Error storing machine info: %v", err)
		}
		a.handleMachineError(machine, machineapierrors.UpdateMachine("failed to update machine %q: %v", machine.Name, err), updateEventAction)
		return &machineapierrors.RequeueAfterError{
			RequeueAfter: 20 * time.Second,
		}
	}

	previousResourceVersion := scope.Machine.ResourceVersion

	if err := scope.Persist(); err != nil {
		return fmt.Errorf("error storing machine info: %v", err)
	}

	currentResourceVersion := scope.Machine.ResourceVersion

	// Create event only if machine object was modified
	if previousResourceVersion != currentResourceVersion {
		a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, "Updated", "Updated machine %q", machine.Name)
	}

	return nil
}

// Exists test for the existence of a machine and is invoked by the Machine Controller
func (a *Actuator) Exists(ctx context.Context, machine *machinev1.Machine) (bool, error) {
	klog.Infof("%s: actuator checking if machine exists", machine.GetName())

	scope, err := actuators.NewMachineScope(actuators.MachineScopeParams{
		Machine:    machine,
		CoreClient: a.coreClient,
	})
	if err != nil {
		return false, fmt.Errorf("failed to create scope: %+v", err)
	}

	isExists, err := a.reconcilerBuilder(scope).Exists(context.Background())
	if err != nil {
		klog.Errorf("failed to check machine %s exists: %v", machine.Name, err)
	}

	return isExists, err
}
