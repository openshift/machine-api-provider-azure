/*
Copyright 2019 The Kubernetes Authors.

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

package actuators

import (
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/go-autorest/autorest"

	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure"
)

// AzureClients contains all the Azure clients used by the scopes.
type AzureClients struct {
	SubscriptionID          string
	Authorizer              autorest.Authorizer
	ResourceManagerEndpoint string

	// For SDK v2
	Token azcore.TokenCredential
	Cloud cloud.Configuration
}

// ARMClientOptions returns default ARM client options for CAPZ SDK v2 requests.
func (c *AzureClients) ARMClientOptions() *arm.ClientOptions {
	opts := &arm.ClientOptions{}
	opts.Cloud = c.Cloud
	opts.PerCallPolicies = []policy.Policy{
		userAgentPolicy{},
	}
	opts.Retry.MaxRetries = -1 // Less than zero means one try and no retries.

	return opts
}

// userAgentPolicy extends the "User-Agent" header on requests.
// It implements the policy.Policy interface.
type userAgentPolicy struct{}

// Do extends the "User-Agent" header of a request by appending CAPZ's user agent.
func (p userAgentPolicy) Do(req *policy.Request) (*http.Response, error) {
	// FIXME: Ought to include a version after our UA string if we have one
	req.Raw().Header.Set("User-Agent", req.Raw().UserAgent()+" "+azure.UserAgent)
	return req.Next()
}
