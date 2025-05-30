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

package azure

import (
	"errors"
	"net/http"

	azureerrors "github.com/Azure/azure-sdk-for-go-extensions/pkg/errors"
	"github.com/Azure/go-autorest/autorest"
)

// ResourceNotFound parses the error to check if its a resource not found
func ResourceNotFound(err error) bool {
	if derr, ok := err.(autorest.DetailedError); ok && derr.StatusCode == http.StatusNotFound {
		return true
	}
	return azureerrors.IsNotFoundErr(err)
}

// InvalidCredentials parses the error to check if its an invalid credentials error
func InvalidCredentials(err error) bool {
	detailedError := autorest.DetailedError{}
	if errors.As(err, &detailedError) && detailedError.StatusCode == http.StatusUnauthorized {
		return true
	}

	azErr := azureerrors.IsResponseError(err)
	return azErr != nil && azErr.StatusCode == http.StatusUnauthorized
}
