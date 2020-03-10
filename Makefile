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
REPO_PATH   ?= sigs.k8s.io/cluster-api-provider-azure
LD_FLAGS    ?= -X $(REPO_PATH)/pkg/version.Raw=$(VERSION) -extldflags -static

GO111MODULE = on
export GO111MODULE
GOFLAGS ?= -mod=vendor
export GOFLAGS
GOPROXY ?=
export GOPROXY

NO_DOCKER ?= 0
ifeq ($(NO_DOCKER), 1)
  DOCKER_CMD =
  IMAGE_BUILD_CMD = imagebuilder
  CGO_ENABLED = 1
else
  DOCKER_CMD := docker run --rm -e CGO_ENABLED=1 -v "$(PWD)":/go/src/sigs.k8s.io/cluster-api-provider-azure:Z -w /go/src/sigs.k8s.io/cluster-api-provider-azure openshift/origin-release:golang-1.12
  IMAGE_BUILD_CMD = docker build
endif

.DEFAULT_GOAL:=help

help:  ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

verify:
	./hack/verify_boilerplate.py

.PHONY: vendor
vendor:
	go mod tidy
	go mod vendor
	go mod verify

.PHONY: fmt
fmt:
	go fmt ./pkg/... ./cmd/...

.PHONY: goimports
    goimports: ## Go fmt your code
	hack/goimports.sh .

.PHONY: vet
vet:
	go vet -composites=false ./pkg/... ./cmd/...

.PHONY: verify-boilerplate
verify-boilerplate:
	./hack/verify-boilerplate.sh

.PHONY: test-e2e
test-e2e:
	hack/e2e.sh

.PHONY: build
build: ## build binaries
	$(DOCKER_CMD) go build $(GOGCFLAGS) -o "bin/machine-controller-manager" \
               -ldflags "$(LD_FLAGS)" "$(REPO_PATH)/cmd/manager"
	$(DOCKER_CMD) go build $(GOGCFLAGS) -o bin/manager -ldflags '-extldflags "-static"' \
               "$(REPO_PATH)/vendor/github.com/openshift/machine-api-operator/cmd/machineset"
.PHONY: test
test: ## Run tests
	@echo -e "\033[32mTesting...\033[0m"
	$(DOCKER_CMD) hack/ci-test.sh

## TODO(JoelSpeed): Make CI depend on `test` target and rename `unit-internal` to `unit` to restore original behaviour
.PHONY: unit
unit: test

.PHONY: unit-internal
unit-internal: # Run unit test
	$(DOCKER_CMD) go test -race -cover ./cmd/... ./pkg/...
