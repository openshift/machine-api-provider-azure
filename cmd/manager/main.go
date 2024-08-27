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

package main

import (
	"flag"
	"os"
	"strings"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	apifeatures "github.com/openshift/api/features"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/library-go/pkg/features"
	"github.com/openshift/machine-api-operator/pkg/controller/machine"
	"github.com/openshift/machine-api-operator/pkg/metrics"
	actuator "github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/actuators/machine"
	machinesetcontroller "github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/actuators/machineset"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/services/resourceskus"
	"github.com/openshift/machine-api-provider-azure/pkg/record"
	"k8s.io/apiserver/pkg/util/feature"
	"k8s.io/component-base/featuregate"
	"k8s.io/klog/v2"
	"k8s.io/klog/v2/klogr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// The default durations for the leader electrion operations.
var (
	leaseDuration = 120 * time.Second
	renewDealine  = 110 * time.Second
	retryPeriod   = 20 * time.Second
)

func main() {
	watchNamespace := flag.String(
		"namespace",
		"",
		"Namespace that the controller watches to reconcile machine-api objects. If unspecified, the controller watches for machine-api objects across all namespaces.",
	)

	healthAddr := flag.String(
		"health-addr",
		":9440",
		"The address for health checking.",
	)

	metricsAddress := flag.String(
		"metrics-bind-address",
		metrics.DefaultMachineMetricsAddress,
		"Address for hosting metrics",
	)

	leaderElectResourceNamespace := flag.String(
		"leader-elect-resource-namespace",
		"",
		"The namespace of resource object that is used for locking during leader election. If unspecified and running in cluster, defaults to the service account namespace for the controller. Required for leader-election outside of a cluster.",
	)

	leaderElect := flag.Bool(
		"leader-elect",
		false,
		"Start a leader election client and gain leadership before executing the main loop. Enable this when running replicated components for high availability.",
	)

	leaderElectLeaseDuration := flag.Duration(
		"leader-elect-lease-duration",
		leaseDuration,
		"The duration that non-leader candidates will wait after observing a leadership renewal until attempting to acquire leadership of a led but unrenewed leader slot. This is effectively the maximum duration that a leader can be stopped before it is replaced by another candidate. This is only applicable if leader election is enabled.",
	)

	maxConcurrentReconciles := flag.Int(
		"max-concurrent-reconciles",
		1,
		"Maximum number of concurrent reconciles per controller instance.",
	)
	// Sets up feature gates
	defaultMutableGate := feature.DefaultMutableFeatureGate
	gateOpts, err := features.NewFeatureGateOptions(defaultMutableGate, apifeatures.SelfManaged, apifeatures.FeatureGateAzureWorkloadIdentity, apifeatures.FeatureGateMachineAPIMigration)
	if err != nil {
		klog.Fatalf("Error setting up feature gates: %v", err)
	}

	// Add the --feature-gates flag
	gateOpts.AddFlagsToGoFlagSet(nil)

	klog.InitFlags(nil)
	flag.Set("logtostderr", "true")
	flag.Parse()

	cfg := config.GetConfigOrDie()
	syncPeriod := 10 * time.Minute

	opts := manager.Options{
		HealthProbeBindAddress:  *healthAddr,
		LeaderElection:          *leaderElect,
		LeaderElectionNamespace: *leaderElectResourceNamespace,
		LeaderElectionID:        "cluster-api-provider-azure-leader",
		LeaseDuration:           leaderElectLeaseDuration,
		Metrics: server.Options{
			BindAddress: *metricsAddress,
		},
		Cache: cache.Options{
			SyncPeriod: &syncPeriod,
		},
		// Slow the default retry and renew election rate to reduce etcd writes at idle: BZ 1858400
		RetryPeriod:   &retryPeriod,
		RenewDeadline: &renewDealine,
	}

	if *watchNamespace != "" {
		opts.Cache.DefaultNamespaces = map[string]cache.Config{
			*watchNamespace: {},
		}
		klog.Infof("Watching machine-api objects only in namespace %q for reconciliation.", *watchNamespace)
	}

	// Sets feature gates from flags
	klog.Infof("Initializing feature gates: %s", strings.Join(defaultMutableGate.KnownFeatures(), ", "))
	warnings, err := gateOpts.ApplyTo(defaultMutableGate)
	if err != nil {
		klog.Fatalf("Error setting feature gates from flags: %v", err)
	}
	if len(warnings) > 0 {
		klog.Infof("Warnings setting feature gates from flags: %v", warnings)
	}

	klog.Infof("FeatureGateMachineAPIMigration initialised: %t", defaultMutableGate.Enabled(featuregate.Feature(apifeatures.FeatureGateMachineAPIMigration)))
	klog.Infof("FeatureGateAzureWorkloadIdentity initialised: %t", defaultMutableGate.Enabled(featuregate.Feature(apifeatures.FeatureGateAzureWorkloadIdentity)))
	azureWorkloadIdentityEnabled := defaultMutableGate.Enabled(featuregate.Feature(apifeatures.FeatureGateAzureWorkloadIdentity))

	// Setup a Manager
	mgr, err := manager.New(cfg, opts)
	if err != nil {
		klog.Fatalf("Failed to set up overall controller manager: %v", err)
	}

	// Initialize event recorder.
	record.InitFromRecorder(mgr.GetEventRecorderFor("azure-controller"))
	stopSignalContext := ctrl.SetupSignalHandler()

	// Initialize machine actuator.
	machineActuator := actuator.NewActuator(actuator.ActuatorParams{
		CoreClient:                   mgr.GetClient(),
		ReconcilerBuilder:            actuator.NewReconciler,
		EventRecorder:                mgr.GetEventRecorderFor("azure-controller"),
		AzureWorkloadIdentityEnabled: azureWorkloadIdentityEnabled,
	})

	if err := machinev1.AddToScheme(mgr.GetScheme()); err != nil {
		klog.Fatal(err)
	}

	if err := configv1.AddToScheme(mgr.GetScheme()); err != nil {
		klog.Fatal(err)
	}

	if err := machine.AddWithActuatorOpts(mgr, machineActuator, controller.Options{
		MaxConcurrentReconciles: *maxConcurrentReconciles,
	}, defaultMutableGate); err != nil {
		klog.Fatal(err)
	}

	ctrl.SetLogger(klogr.New())
	setupLog := ctrl.Log.WithName("setup")
	if err = (&machinesetcontroller.Reconciler{
		Client:                     mgr.GetClient(),
		Log:                        ctrl.Log.WithName("controllers").WithName("MachineSet"),
		ResourceSkusServiceBuilder: resourceskus.NewService,

		AzureWorkloadIdentityEnabled: azureWorkloadIdentityEnabled,
	}).SetupWithManager(mgr, controller.Options{}); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "MachineSet")
		os.Exit(1)
	}

	if err := mgr.AddReadyzCheck("ping", healthz.Ping); err != nil {
		klog.Fatal(err)
	}

	if err := mgr.AddHealthzCheck("ping", healthz.Ping); err != nil {
		klog.Fatal(err)
	}

	if err := mgr.Start(stopSignalContext); err != nil {
		klog.Fatalf("Failed to run manager: %v", err)
	}
}
