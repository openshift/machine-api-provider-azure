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

.DEFAULT_GOAL:=help

help:  ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

.PHONY: dep-ensure
dep-ensure: check-install ## Ensure dependencies are up to date
	@echo Checking status of dependencies
	@${DEP} status 2>&1 > /dev/null || make dep-install
	@echo Finished verifying dependencies

verify:
	./hack/verify_boilerplate.py

vendor:
	dep version || go get -u github.com/golang/dep/cmd/dep
	dep ensure -v
vendor-update:
	dep version || go get -u github.com/golang/dep/cmd/dep
	dep ensure -v -update
vendor-validate:
	dep check

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
