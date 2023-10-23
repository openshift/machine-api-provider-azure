#!/bin/sh

# Ensure that some home var is set and that it's not the root.
# This is required for the kubebuilder cache.
mkdir -p /tmp/kubebuilder-testing
export HOME=${HOME:=/tmp/kubebuilder-testing}
if [ ${HOME} == "/" ]; then
  export HOME=/tmp/kubebuilder-testing
fi

for TARGET in "${@}"; do
  find "${TARGET}" -name '*.go' ! -path '*/vendor/*' ! -path '*/.build/*' -exec goimports -w {} \+
done
git diff --exit-code

