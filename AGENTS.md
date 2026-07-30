# AGENTS.md

Azure provider for OpenShift Machine API. Reconciles Machine CRs into Azure VMs.

> **Note:** This is the Azure-specific provider only. The main Machine API controller lives in [machine-api-operator](https://github.com/openshift/machine-api-operator).

## Commands

```bash
make build    # Build binaries
make test     # Run tests (Ginkgo + envtest)
make fmt      # Format code (run before committing)
make vet      # Lint (run before committing)
make vendor   # Update vendor after changing go.mod
```

## Development

- [Bumping Kubernetes and Go](docs/development/bump-k8s-go.md) - Complete runbook for k8s/Go version bumps

## Key Locations

- `cmd/manager/` - Main controller entry point
- `cmd/termination-handler/` - Spot instance preemption handler
- `pkg/cloud/azure/actuators/machine/` - VM lifecycle (start here for reconciliation)
- `pkg/cloud/azure/actuators/machineset/` - MachineSet controller
- `pkg/cloud/azure/services/` - Azure API wrappers (one package per resource type)
- `pkg/termination/` - IMDS polling for spot eviction

## Architecture

- **Vendored deps**: Run `go mod vendor` after dependency changes. Commit vendor changes separately.
- **Service interfaces**: All Azure API calls go through interfaces in `services/`. Never call Azure SDK directly.
- **Dual SDK**: Track2 SDK (`armcompute/v5`) for public cloud, ARM Stack (`*_stack.go`) for Azure Stack Hub. Don't mix.
- **Actuator pattern**: `Create()`, `Update()`, `Delete()`, `Exists()` methods.
- **Machine scope**: Encapsulates request context (credentials, clients, spec/status).

## Do

- Use service interfaces for Azure operations
- Wrap errors: `fmt.Errorf("context: %w", err)`
- Use `klog` for logging
- Use `InvalidMachineConfiguration` for Azure 4xx errors
- Add mocks when extending service interfaces
- Follow patterns in `pkg/cloud/azure/actuators/machine/reconciler.go`

## Do NOT

- Edit `vendor/` directly
- Call Azure SDK directly (use service interfaces)
- Return unwrapped errors
- Hardcode resource groups, VM sizes, or locations
- Log credentials, secrets, subscription/tenant IDs
- Commit vendor changes with implementation changes
- Mix ARM Stack and Track2 SDK in the same service

## Testing

- Ginkgo/Gomega with envtest for K8s API
- Mock Azure clients for unit tests
- `stubs.go` for test fixtures
- `*_suite_test.go` for controller setup

### Running tests

Run all tests:
```bash
make test
```

Run tests for a specific package using `GINKGO_EXTRA_ARGS`:
```bash
GINKGO_EXTRA_ARGS="./pkg/cloud/azure/actuators/machine/..." make test
```

## Code Patterns

```go
// Use service interfaces, not direct SDK calls
r.virtualMachinesClient.BeginCreateOrUpdate(ctx, rg, name, vm, nil)  // GOOD
armcompute.NewVirtualMachinesClient(subID, cred, nil)                 // BAD

// Wrap errors with context
return fmt.Errorf("failed to create VM %s: %w", name, err)  // GOOD
return err                                                   // BAD

// Use InvalidMachineConfiguration for 4xx errors
if respErr.StatusCode >= 400 && respErr.StatusCode < 500 {
    return machinecontroller.InvalidMachineConfiguration("error: %v", err)
}

// Access config via MachineScope, not hardcoded
vmSize := scope.AzureMachineProviderSpec().VMSize  // GOOD
vmSize := "Standard_D2s_v3"                         // BAD
```
