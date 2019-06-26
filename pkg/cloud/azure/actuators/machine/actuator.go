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
	"time"

	clusterv1 "github.com/openshift/cluster-api/pkg/apis/cluster/v1alpha1"
	machinev1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	client "github.com/openshift/cluster-api/pkg/client/clientset_generated/clientset/typed/machine/v1beta1"
	controllerError "github.com/openshift/cluster-api/pkg/controller/error"
	apierrors "github.com/openshift/cluster-api/pkg/errors"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/cloud/azure/actuators"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/deployer"
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
	*deployer.Deployer

	client        client.MachineV1beta1Interface
	coreClient    controllerclient.Client
	eventRecorder record.EventRecorder

	reconcilerBuilder func(scope *actuators.MachineScope) *Reconciler
}

// ActuatorParams holds parameter information for Actuator.
type ActuatorParams struct {
	Client            client.MachineV1beta1Interface
	CoreClient        controllerclient.Client
	EventRecorder     record.EventRecorder
	ReconcilerBuilder func(scope *actuators.MachineScope) *Reconciler
}

// NewActuator returns an actuator.
func NewActuator(params ActuatorParams) *Actuator {
	return &Actuator{
		Deployer:          deployer.New(deployer.Params{ScopeGetter: actuators.DefaultScopeGetter}),
		client:            params.Client,
		coreClient:        params.CoreClient,
		eventRecorder:     params.EventRecorder,
		reconcilerBuilder: params.ReconcilerBuilder,
	}
}

// Set corresponding event based on error. It also returns the original error
// for convenience, so callers can do "return handleMachineError(...)".
func (a *Actuator) handleMachineError(machine *machinev1.Machine, err *apierrors.MachineError, eventAction string) error {
	if eventAction != noEventAction {
		a.eventRecorder.Eventf(machine, corev1.EventTypeWarning, "Failed"+eventAction, "%v: %v", err.Reason, err.Message)
	}

	klog.Errorf("Machine error: %v", err.Message)
	return err
}

// Create creates a machine and is invoked by the machine controller.
func (a *Actuator) Create(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) error {
	klog.Infof("Creating machine %v", machine.Name)

	scope, err := actuators.NewMachineScope(actuators.MachineScopeParams{
		Machine:    machine,
		Cluster:    nil,
		Client:     a.client,
		CoreClient: a.coreClient,
	})
	if err != nil {
		return a.handleMachineError(machine, apierrors.CreateMachine("failed to create machine %q scope: %v", machine.Name, err), createEventAction)
	}
	defer scope.Close()

	err = a.reconcilerBuilder(scope).Create(context.Background())
	if err != nil {
		a.handleMachineError(machine, apierrors.CreateMachine("failed to reconcile machine %qs: %v", machine.Name, err), createEventAction)
		return &controllerError.RequeueAfterError{
			RequeueAfter: time.Minute,
		}
	}

	a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, "Created", "Created machine %q", machine.Name)

	return nil
}

// Delete deletes a machine and is invoked by the Machine Controller.
func (a *Actuator) Delete(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) error {
	klog.Infof("Deleting machine %v", machine.Name)

	scope, err := actuators.NewMachineScope(actuators.MachineScopeParams{
		Machine:    machine,
		Cluster:    nil,
		Client:     a.client,
		CoreClient: a.coreClient,
	})
	if err != nil {
		return a.handleMachineError(machine, apierrors.DeleteMachine("failed to create machine %q scope: %v", machine.Name, err), deleteEventAction)
	}

	defer scope.Close()

	err = a.reconcilerBuilder(scope).Delete(context.Background())
	if err != nil {
		a.handleMachineError(machine, apierrors.DeleteMachine("failed to delete machine %q: %v", machine.Name, err), deleteEventAction)
		return &controllerError.RequeueAfterError{
			RequeueAfter: time.Minute,
		}
	}

	a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, "Deleted", "Deleted machine %q", machine.Name)

	return nil
}

// Update updates a machine and is invoked by the Machine Controller.
// If the Update attempts to mutate any immutable state, the method will error
// and no updates will be performed.
func (a *Actuator) Update(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) error {
	klog.Infof("Updating machine %v", machine.Name)

	scope, err := actuators.NewMachineScope(actuators.MachineScopeParams{
		Machine:    machine,
		Cluster:    nil,
		Client:     a.client,
		CoreClient: a.coreClient,
	})
	if err != nil {
		return a.handleMachineError(machine, apierrors.UpdateMachine("failed to create machine %q scope: %v", machine.Name, err), updateEventAction)
	}

	defer scope.Close()

	err = a.reconcilerBuilder(scope).Update(context.Background())
	if err != nil {
		a.handleMachineError(machine, apierrors.UpdateMachine("failed to update machine %q: %v", machine.Name, err), updateEventAction)
		return &controllerError.RequeueAfterError{
			RequeueAfter: time.Minute,
		}
	}

	a.eventRecorder.Eventf(machine, corev1.EventTypeNormal, "Updated", "Updated machine %q", machine.Name)

	return nil
}

// Exists test for the existence of a machine and is invoked by the Machine Controller
func (a *Actuator) Exists(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) (bool, error) {
	klog.Infof("Checking if machine %v exists", machine.Name)

	scope, err := actuators.NewMachineScope(actuators.MachineScopeParams{
		Machine:    machine,
		Cluster:    nil,
		Client:     a.client,
		CoreClient: a.coreClient,
	})
	if err != nil {
		return false, errors.Errorf("failed to create scope: %+v", err)
	}

	defer scope.Close()

	isExists, err := a.reconcilerBuilder(scope).Exists(context.Background())
	if err != nil {
		klog.Errorf("failed to check machine %s exists: %v", machine.Name, err)
	}

	return isExists, err
}
