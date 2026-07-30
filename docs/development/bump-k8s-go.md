# Bumping Kubernetes and Go

This document describes how to bump Kubernetes and Go versions across the
project. It is primarily intended to be consumed by an AI coding agent (e.g. via
`/bump-k8s-go 1.36 1.26`), but the steps can also be followed manually.

The first argument is the target **Kubernetes minor** version (e.g. `1.36`) and
the second is the target **Go minor** version (e.g. `1.26`).

## Prerequisites

This repository depends on several OpenShift repositories that must be bumped
**before** this one. Each prerequisite repository must have a merged PR that
targets the same Kubernetes and Go versions. Verify all of these before
proceeding:

Set the target versions from the skill arguments before running any command in
this section:

```bash
K8S_MINOR="<arg1>"  # e.g. 1.36
GO_MINOR="<arg2>"   # e.g. 1.26
```

### openshift/api

The OpenShift API types repository must be bumped first. It defines the shared
API types consumed by all other OpenShift components.

```bash
gh pr list --repo openshift/api --state merged \
  --search "bump k8s ${K8S_MINOR} OR k8s ${K8S_MINOR}" \
  --limit 5
```

Record the commit SHA from the merged PR. Call this `OPENSHIFT_API_COMMIT`.

### openshift/client-go

The generated client library for OpenShift API types must be updated to match
the new `openshift/api`.

```bash
gh pr list --repo openshift/client-go --state merged \
  --search "bump k8s ${K8S_MINOR} OR k8s ${K8S_MINOR}" \
  --limit 5
```

Record the commit SHA. Call this `OPENSHIFT_CLIENT_GO_COMMIT`.

### openshift/library-go

Shared OpenShift library code, which depends on both `openshift/api` and
`openshift/client-go`.

```bash
gh pr list --repo openshift/library-go --state merged \
  --search "bump k8s ${K8S_MINOR} OR k8s ${K8S_MINOR}" \
  --limit 5
```

Record the commit SHA. Call this `OPENSHIFT_LIBRARY_GO_COMMIT`.

### openshift/machine-api-operator (MAO)

The machine-api-operator is the most critical prerequisite. This provider
imports types and test helpers from MAO, and MAO itself depends on the three
repositories above.

```bash
gh pr list --repo openshift/machine-api-operator --state merged \
  --search "bump k8s ${K8S_MINOR} OR k8s ${K8S_MINOR}" \
  --limit 5
```

Record the commit SHA (the pseudo-version). Call this `MAO_COMMIT`.

### Envtest assets (openshift/api)

The `unit` Makefile target downloads envtest binaries (etcd, kube-apiserver)
from an OpenShift-maintained index at
[openshift/api/envtest-releases.yaml](https://github.com/openshift/api/blob/master/envtest-releases.yaml).
Assets must be published for the target k8s version before the bump can proceed.

Check availability:

```bash
gh api repos/openshift/api/contents/envtest-releases.yaml \
  --jq '.content' | base64 -d | grep "v${K8S_MINOR}"
```

This should return one or more `v${K8S_MINOR}.X:` entries. The **highest patch
version** listed is what `ENVTEST_K8S_VERSION` in the Makefile should be set to.
Call this `ENVTEST_K8S_VERSION`.

If **no entry exists** for the target k8s minor, **stop**. The envtest assets
must be published first — tests will fail without them. Check with the
openshift/api maintainers or wait for the assets to be added.

### Prerequisite verification

Before continuing, confirm that each prerequisite's `go.mod` targets the
expected `k8s.io/*` version and Go version. A quick check:

```bash
for repo in openshift/api openshift/client-go openshift/library-go openshift/machine-api-operator; do
  echo "=== $repo ==="
  gh api "repos/$repo/contents/go.mod" --jq '.content' | base64 -d \
    | grep -E '^go |k8s.io/api ' | head -2
done
```

If any prerequisite is not yet bumped, **stop** and bump it first (or wait for
the responsible team to merge their PR).

## Step 1: Research

Perform these lookups before making any changes. Call the first argument
`K8S_MINOR` and the second `GO_MINOR`.

### 1a. Kubernetes patch version

Find the latest stable patch for the target k8s minor:

```bash
gh api repos/kubernetes/kubernetes/releases \
  --paginate --jq '.[].tag_name' \
  | grep -F "v${K8S_MINOR}." \
  | grep -E '\.[0-9]+$' \
  | grep -v -E '(alpha|beta|rc)' \
  | head -1
```

Call this `K8S_VERSION` (e.g. `v1.36.2`).

### 1b. controller-runtime version

Find the controller-runtime version that targets the new k8s minor. Check which
version the MAO bump uses, or look at the controller-runtime releases:

```bash
gh api repos/kubernetes-sigs/controller-runtime/releases \
  --paginate --jq '.[].tag_name' | head -20
```

Cross-reference with MAO's `go.mod` to ensure compatibility:

```bash
gh api repos/openshift/machine-api-operator/contents/go.mod \
  --jq '.content' | base64 -d | grep 'sigs.k8s.io/controller-runtime'
```

Call this `CONTROLLER_RUNTIME_VERSION` (e.g. `v0.24.1`).

### 1c. controller-tools version

Check if controller-tools needs bumping:

```bash
gh api repos/kubernetes-sigs/controller-tools/releases \
  --paginate --jq '.[].tag_name' | head -10
```

Call this `CONTROLLER_TOOLS_VERSION`.

### 1d. OpenShift release mapping

Determine the target OpenShift release version that corresponds to this k8s
bump. The mapping is roughly:

| k8s minor | OCP version |
|-----------|-------------|
| 1.28      | 4.15        |
| 1.29      | 4.16        |
| 1.30      | 4.17        |
| 1.31      | 4.18        |
| 1.32      | 4.19        |
| 1.33      | 4.20        |
| 1.34      | 4.21        |
| 1.35      | 4.22        |
| 1.36      | 5.0         |

Call this `OCP_VERSION`. This determines the builder image tags.

### 1e. Builder image availability

Verify that CI builder images exist for the target Go version and OCP release:

```
registry.ci.openshift.org/ocp/builder:rhel-9-golang-${GO_MINOR}-openshift-${OCP_VERSION}
```

Also check the release image tag for `.ci-operator.yaml`:

```
rhel-9-release-golang-${GO_MINOR}-openshift-${OCP_VERSION}
```

And the base image for `Dockerfile.rhel`:

```
registry.ci.openshift.org/ocp/${OCP_VERSION}:base-rhel9
```

### 1f. setup-envtest release branch

Determine the setup-envtest release branch matching controller-runtime:

```bash
gh api repos/kubernetes-sigs/controller-runtime/branches \
  --paginate --jq '.[].name' | grep 'release-' | head -5
```

Call this `SETUP_ENVTEST_VER` (e.g. `release-0.24`).

### 1g. Breaking changes

Check the controller-runtime and k8s changelogs for breaking changes:

```bash
gh api repos/kubernetes-sigs/controller-runtime/releases \
  --jq '.[] | select(.tag_name | startswith("v0.XX")) | .body' | head -100
```

Also check if MAO's bump PR had any code changes beyond `go.mod`/vendor:

```bash
gh pr diff <MAO_PR_NUMBER> --repo openshift/machine-api-operator \
  -- '*.go' 'Makefile' 'Dockerfile*'
```

Note any required code changes.

## Step 2: Update go.mod

### 2a. Update Go version

```bash
go mod edit -go=${GO_MINOR}
```

If the `go.mod` has a `toolchain` directive, evaluate whether it should be
updated or removed. When the `go` directive specifies the exact minor (e.g.
`go 1.26`), a separate `toolchain` line is typically unnecessary.

### 2b. Bump Kubernetes dependencies

```bash
K8S_MOD_VERSION="v0.${K8S_VERSION#v1.}"  # e.g. v0.36.2 for K8S_VERSION=v1.36.2

go get k8s.io/api@${K8S_MOD_VERSION}
go get k8s.io/apimachinery@${K8S_MOD_VERSION}
go get k8s.io/apiserver@${K8S_MOD_VERSION}
go get k8s.io/client-go@${K8S_MOD_VERSION}
go get k8s.io/component-base@${K8S_MOD_VERSION}
```

### 2c. Bump controller-runtime and controller-tools

```bash
go get sigs.k8s.io/controller-runtime@${CONTROLLER_RUNTIME_VERSION}
go get sigs.k8s.io/controller-tools@${CONTROLLER_TOOLS_VERSION}
```

### 2d. Bump OpenShift dependencies

Use the pseudo-version format `v0.0.0-YYYYMMDDHHMMSS-<commit-hash>` from the
prerequisite commits:

```bash
go get github.com/openshift/api@${OPENSHIFT_API_COMMIT}
go get github.com/openshift/library-go@${OPENSHIFT_LIBRARY_GO_COMMIT}
go get github.com/openshift/machine-api-operator@${MAO_COMMIT}
```

`openshift/client-go` is an indirect dependency — it will be pulled in
transitively via `openshift/machine-api-operator` and `openshift/library-go`.

### 2e. Update setup-envtest

```bash
go get sigs.k8s.io/controller-runtime/tools/setup-envtest@${SETUP_ENVTEST_VER}
```

## Step 3: Tidy, sync, and vendor

Run the helper script:

```bash
hack/go-mod.sh
```

This runs:
1. `go mod tidy` to update go.sum and resolve indirect dependencies
2. `go mod vendor -v` to create the vendor directory
3. `go mod verify` to verify checksums

If `hack/go-mod.sh` fails, see the Troubleshooting section.

## Step 4: Update build infrastructure

### 4a. Makefile

Update `ENVTEST_K8S_VERSION` to the highest patch version available in the
[openshift/api envtest-releases.yaml](https://github.com/openshift/api/blob/master/envtest-releases.yaml)
index for the target k8s minor (determined in the Prerequisites section):

```makefile
ENVTEST_K8S_VERSION = 1.36.2  # highest patch from envtest-releases.yaml
```

Update `BUILD_IMAGE` to the new builder image:

```makefile
BUILD_IMAGE ?= registry.ci.openshift.org/ocp/builder:rhel-9-golang-${GO_MINOR}-openshift-${OCP_VERSION}
```

Also check the `unit` target's envtest invocation for flags that may need
updating. Historically this has changed multiple times in the AWS provider.
Compare with MAO's Makefile to ensure the flags are aligned.

### 4b. Dockerfile

Update the builder image in the `FROM` line:

```dockerfile
FROM registry.ci.openshift.org/ocp/builder:rhel-9-golang-${GO_MINOR}-openshift-${OCP_VERSION} AS builder
```

### 4c. Dockerfile.rhel

Update the builder image similarly. Note that `Dockerfile.rhel` may already be
ahead if ART has updated it — check before overwriting:

```dockerfile
FROM registry.ci.openshift.org/ocp/builder:rhel-9-golang-${GO_MINOR}-openshift-${OCP_VERSION} AS builder
```

Also update the base image if the OCP version changed:

```dockerfile
FROM registry.ci.openshift.org/ocp/${OCP_VERSION}:base-rhel9
```

### 4d. .ci-operator.yaml

Update the build root image tag:

```yaml
build_root_image:
  name: release
  namespace: openshift
  tag: rhel-9-release-golang-${GO_MINOR}-openshift-${OCP_VERSION}
```

## Step 5: Apply code changes

Using the breaking changes identified in Step 1g, apply any required code
changes. This step varies per bump. Common categories from historical bumps
include:

- **Feature gate API changes**: k8s may change feature gate defaults or
  graduation levels. The `features.NewFeatureGateOptions` function in
  `cmd/manager/main.go` requires a `majorVersion` parameter. This is extracted
  from `pkg/version/version.go`, which parses the build-time version using
  `github.com/blang/semver`. If the API changes, both files may need updates.

- **controller-runtime interface changes**: webhook interfaces, predicate
  signatures, or builder patterns may change between minor versions.

- **openshift/api CRD manifest reorganization**: when openshift/api changes
  where CRD manifests are stored, two things break:
  1. `tools.go` may need new blank imports to pull CRD manifest directories
     into the vendor tree
  2. Test files that reference `CRDDirectoryPaths` need path updates. Check:
     - `pkg/cloud/azure/actuators/machine/suite_test.go`
     - `pkg/cloud/azure/actuators/machineset/suite_test.go`

- **go.mod toolchain directive**: when the Go minor version changes, the
  `toolchain` directive may need to be added or removed.

- **Test framework changes**: test helpers from MAO may have changed signatures.

Review the MAO bump diff for guidance on what code changes were needed there.

## Step 6: Validate

```bash
make build
make test
make fmt
make vet
```

Fix any failures before proceeding.

## Step 7: Commit

Create separate commits for distinct concerns. The typical commit structure for
a bump PR is:

1. **The version bump** (go.mod, go.sum):
   ```
   Bump k8s K8S_MINOR
   ```

2. **Vendor update** (vendor/ directory only):
   ```
   make vendor
   ```

3. **Build infrastructure** (Makefile, Dockerfiles, .ci-operator.yaml):
   ```
   Update build images for Go GO_MINOR and OCP OCP_VERSION
   ```

4. **Code changes** (if any, in a separate commit):
   ```
   Fix code for k8s K8S_MINOR
   ```

All commits should follow this repo's terse style (no conventional commits).

Do NOT push or create a PR unless the user asks.

## Pre-merge checklist

Before marking the bump as ready, verify every item:

- [ ] `go.mod` — Go version, k8s.io/*, controller-runtime, controller-tools,
  OpenShift deps all updated
- [ ] Vendor — `hack/go-mod.sh` ran cleanly, vendor directory updated
- [ ] `Makefile` — `ENVTEST_K8S_VERSION` matches envtest-releases.yaml, `BUILD_IMAGE` updated
- [ ] `Makefile` — envtest invocation flags in `unit` target are current
- [ ] `Dockerfile` — builder image updated
- [ ] `Dockerfile.rhel` — builder and base images updated
- [ ] `.ci-operator.yaml` — build root image tag updated
- [ ] `tools.go` — blank imports still match openshift/api CRD manifest paths (if applicable)
- [ ] Test CRD paths — `CRDDirectoryPaths` in test suites match vendor layout (if applicable)
- [ ] Code compiles — `make build` passes
- [ ] Tests pass — `make test` passes
- [ ] Format clean — `make fmt` produces no changes
- [ ] Lint passes — `make vet` passes

## Troubleshooting

### hack/go-mod.sh fails during tidy

If `go mod tidy` fails due to incompatible transitive dependencies, check if
MAO's `go.mod` has `replace` directives that need to be mirrored:

```bash
gh api repos/openshift/machine-api-operator/contents/go.mod \
  --jq '.content' | base64 -d | grep '^replace'
```

### Vendor conflicts

If `go mod vendor` produces unexpected diffs, run `rm -rf vendor && go mod
vendor -v` to rebuild from scratch.

### ENVTEST_K8S_VERSION assets not available

Envtest asset availability is checked as a prerequisite (see the "Envtest assets"
section above). If assets are missing, the bump should not proceed — tests will
fail in CI. Check with the openshift/api maintainers.