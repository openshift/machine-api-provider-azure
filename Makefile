# Copyright 2018 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# If you update this file, please follow
# https://suva.sh/posts/well-documented-makefiles

DBG ?= 0

ifeq ($(DBG),1)
GOGCFLAGS ?= -gcflags=all="-N -l"
endif

VERSION     ?= $(shell git describe --always --abbrev=7)
REPO_PATH   ?= github.com/openshift/machine-api-provider-azure
LD_FLAGS    ?= -X $(REPO_PATH)/pkg/version.Raw=$(VERSION) -extldflags -static
BUILD_IMAGE ?= registry.ci.openshift.org/openshift/release:golang-1.21

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.29.1

PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
CONTROLLER_GEN = go run ${PROJECT_DIR}/vendor/sigs.k8s.io/controller-tools/cmd/controller-gen
ENVTEST = go run ${PROJECT_DIR}/vendor/sigs.k8s.io/controller-runtime/tools/setup-envtest
GINKGO = go run ${PROJECT_DIR}/vendor/github.com/onsi/ginkgo/v2/ginkgo
GOLANGCI_LINT = go run ${PROJECT_DIR}/vendor/github.com/golangci/golangci-lint/cmd/golangci-lint

GO111MODULE = on
export GO111MODULE
GOFLAGS ?= -mod=vendor
export GOFLAGS
GOPROXY ?=
export GOPROXY

GOARCH ?= $(shell go env GOARCH)
GOOS ?= $(shell go env GOOS)

NO_DOCKER ?= 1

ifeq ($(shell command -v podman > /dev/null 2>&1 ; echo $$? ), 0)
	ENGINE=podman
else ifeq ($(shell command -v docker > /dev/null 2>&1 ; echo $$? ), 0)
	ENGINE=docker
else
	NO_DOCKER=1
endif

USE_DOCKER ?= 0
ifeq ($(USE_DOCKER), 1)
	ENGINE=docker
endif

# race tests need CGO_ENABLED, everything else should have it disabled
CGO_ENABLED = 0
unit test : CGO_ENABLED = 1

ifeq ($(NO_DOCKER), 1)
  DOCKER_CMD = CGO_ENABLED=$(CGO_ENABLED) GOARCH=$(GOARCH) GOOS=$(GOOS)
  IMAGE_BUILD_CMD = imagebuilder
  export CGO_ENABLED
else
  DOCKER_CMD = $(ENGINE)  run --rm -e CGO_ENABLED=$(CGO_ENABLED) -e GOARCH=$(GOARCH) -e GOOS=$(GOOS) -v "$(PWD)":/go/src/github.com/openshift/machine-api-provider-azure:Z -w /go/src/github.com/openshift/machine-api-provider-azure $(BUILD_IMAGE)
  IMAGE_BUILD_CMD = $(ENGINE)  build
endif

.DEFAULT_GOAL:=help

help:  ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

.PHONY: vendor
vendor:
	$(DOCKER_CMD) hack/go-mod.sh

.PHONY: fmt
fmt:
	$(DOCKER_CMD) go fmt ./pkg/... ./cmd/...

.PHONY: goimports
goimports: ## Go fmt your code
	$(DOCKER_CMD) hack/goimports.sh .

.PHONY: generate
generate: gogen goimports
	./hack/verify-diff.sh

gogen:
	$(DOCKER_CMD) go generate ./pkg/... ./cmd/...

.PHONY: vet
vet:
	$(DOCKER_CMD) go vet -composites=false ./pkg/... ./cmd/...

.PHONY: test-e2e
test-e2e:
	hack/e2e.sh

.PHONY: build
build: ## build binaries
	$(DOCKER_CMD) go build $(GOGCFLAGS) -o "bin/machine-controller-manager" \
               -ldflags "$(LD_FLAGS)" "$(REPO_PATH)/cmd/manager"
	$(DOCKER_CMD) go build $(GOGCFLAGS) -o "bin/termination-handler" \
               -ldflags "$(LD_FLAGS)" "$(REPO_PATH)/cmd/termination-handler"

.PHONY: test
test: ## Run tests
	@echo -e "\033[32mTesting...\033[0m"
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path --bin-dir $(PROJECT_DIR)/bin  --remote-bucket openshift-kubebuilder-tools)" ./hack/ci-test.sh

.PHONY: unit
unit: # Run unit test
	$(DOCKER_CMD) go test -race -cover ./cmd/... ./pkg/...
