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

//go:generate go run go.uber.org/mock/mockgen -destination ./mock/client_generated.go -source ./interfaces.go -package mock_azure
package azure

import (
	"context"
)

const (
	// UserAgent used for communicating with azure
	UserAgent = "cluster-api-azure-services"
)

// Spec defines a generic interface which all services should conform to
type Spec interface {
}

// Service is a generic interface used by components offering a type of service.
// example: Network service would offer get/createorupdate/delete.
type Service interface {
	Get(ctx context.Context, spec Spec) (interface{}, error)
	CreateOrUpdate(ctx context.Context, spec Spec) error
	Delete(ctx context.Context, spec Spec) error
}

func NewServiceClientError(err error) Service {
	return serviceClientError{err}
}

// serviceClientError allows us to return a client initialisation error without
// changing the signature of NewService. All of its methods return the client
// initialisation error.
type serviceClientError struct {
	err error
}

var _ Service = serviceClientError{}

func (e serviceClientError) Get(_ context.Context, _ Spec) (interface{}, error) {
	return nil, e.err
}

func (e serviceClientError) CreateOrUpdate(_ context.Context, _ Spec) error {
	return e.err
}

func (e serviceClientError) Delete(_ context.Context, _ Spec) error {
	return e.err
}
