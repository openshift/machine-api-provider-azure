package termination

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/go-logr/logr"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// azureTerminationEndpointURL see the following link for more details about the endpoint
	// https://docs.microsoft.com/en-us/azure/virtual-machines/windows/scheduled-events#endpoint-discovery
	azureTerminationEndpointURL = "http://169.254.169.254/metadata/scheduledevents?api-version=2019-08-01"
)

// Handler represents a handler that will run to check the termination
// notice endpoint and delete Machine's if the instance termination notice is fulfilled.
type Handler interface {
	Run(stop <-chan struct{}) error
}

// NewHandler constructs a new Handler
func NewHandler(logger logr.Logger, cfg *rest.Config, pollInterval time.Duration, namespace, nodeName string) (Handler, error) {
	machinev1.AddToScheme(scheme.Scheme)
	c, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		return nil, fmt.Errorf("error creating client: %v", err)
	}

	pollURL, err := url.Parse(azureTerminationEndpointURL)
	if err != nil {
		// This should never happen
		panic(err)
	}

	logger = logger.WithValues("node", nodeName, "namespace", namespace)

	return &handler{
		client:       c,
		pollURL:      pollURL,
		pollInterval: pollInterval,
		nodeName:     nodeName,
		namespace:    namespace,
		log:          logger,
	}, nil
}

// handler implements the logic to check the termination endpoint and delete the
// machine associated with the node
type handler struct {
	client       client.Client
	pollURL      *url.URL
	pollInterval time.Duration
	nodeName     string
	namespace    string
	log          logr.Logger
}

// Run starts the handler and runs the termination logic
func (h *handler) Run(stop <-chan struct{}) error {
	ctx, cancel := context.WithCancel(context.Background())

	errs := make(chan error, 1)
	wg := &sync.WaitGroup{}
	wg.Add(1)

	go func() {
		defer wg.Done()
		errs <- h.run(ctx)
	}()

	select {
	case <-stop:
		cancel()
		// Wait for run to stop
		wg.Wait()
		return nil
	case err := <-errs:
		cancel()
		return err
	}
}

func (h *handler) run(ctx context.Context) error {
	if err := wait.PollImmediateUntil(h.pollInterval, func() (bool, error) {
		req, err := http.NewRequest("GET", h.pollURL.String(), nil)
		if err != nil {
			return false, fmt.Errorf("could not create request %q: %w", h.pollURL.String(), err)
		}

		req.Header.Add("Metadata", "true")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return false, fmt.Errorf("could not get URL %q: %w", h.pollURL.String(), err)
		}

		bodyBytes, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return false, fmt.Errorf("failed to read responce body: %w", err)
		}

		s := scheduledEvents{}
		err = json.Unmarshal(bodyBytes, &s)
		if err != nil {
			return false, fmt.Errorf("failed to unmarshal responce body: %w", err)
		}

		for _, event := range s.Events {
			if event.EventType == preemptEventType {
				// Instance marked for termination
				return true, nil
			}
		}

		// Instance not terminated yet
		h.log.V(2).Info("Instance not marked for termination")
		return false, nil
	}, ctx.Done()); err != nil {
		return fmt.Errorf("error polling termination endpoint: %w", err)
	}

	if err := wait.PollImmediateUntil(h.pollInterval, func() (bool, error) {
		h.log.V(1).Info("Getting node for machine")
		machine, err := h.getMachineForNode(ctx)
		if err != nil {
			if errors.Is(err, notFoundMachineForNode{}) {
				h.log.Error(err, "Machine not found for node")
				return true, nil
			}
			h.log.Error(err, "error fetching machine for node:", h.nodeName)
			return false, nil
		}

		// Will only get here if the termination endpoint returned FALSE
		h.log.Info("Instance marked for termination, deleting Machine")
		if err := h.client.Delete(ctx, machine); err != nil {
			if apierrors.IsNotFound(err) {
				h.log.Error(err, "Machine not found when deleting")
				return true, nil
			}
			h.log.Error(err, "Error deleting machine")
			return false, nil
		}

		return true, nil
	}, ctx.Done()); err != nil {
		return fmt.Errorf("error getting and deleting machine: %w", err)
	}

	return nil
}

// getMachineForNodeName finds the Machine associated with the Node name given
func (h *handler) getMachineForNode(ctx context.Context) (*machinev1.Machine, error) {
	machineList := &machinev1.MachineList{}
	err := h.client.List(ctx, machineList, client.InNamespace(h.namespace))
	if err != nil {
		return nil, fmt.Errorf("error listing machines: %w", err)
	}

	for _, machine := range machineList.Items {
		if machine.Status.NodeRef != nil && machine.Status.NodeRef.Name == h.nodeName {
			return &machine, nil
		}
	}

	return nil, notFoundMachineForNode{}
}

const preemptEventType = "Preempt"

// scheduledEvents represents metadata response, more detailed info can be found here:
// https://docs.microsoft.com/en-us/azure/virtual-machines/linux/scheduled-events#use-the-api
type scheduledEvents struct {
	Events []events `json:"Events"`
}

type events struct {
	EventType string `json:"EventType"`
}

// notFoundMachineForNode this error is returned when no machine for node is found in a list of machines
type notFoundMachineForNode struct{}

func (err notFoundMachineForNode) Error() string {
	return "machine not found for node"
}
