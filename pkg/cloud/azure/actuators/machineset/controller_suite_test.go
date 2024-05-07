/*
Copyright The Kubernetes Authors.

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

package machineset

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/actuators/machine"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

const (
	timeout                  = 10 * time.Second
	globalInfrastructureName = "cluster"
)

var (
	cfg       *rest.Config
	testEnv   *envtest.Environment
	k8sClient client.Client

	ctx = context.Background()
)

func TestReconciler(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "..", "..", "vendor", "github.com", "openshift", "api", "machine", "v1beta1", "zz_generated.crd-manifests"),
			filepath.Join("..", "..", "..", "..", "..", "vendor", "github.com", "openshift", "api", "config", "v1", "zz_generated.crd-manifests"),
		},
	}

	Expect(machinev1.AddToScheme(scheme.Scheme)).Should(Succeed())
	Expect(configv1.AddToScheme(scheme.Scheme)).Should(Succeed())

	var err error
	cfg, err = testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	k8sClient, err = client.New(cfg, client.Options{})
	Expect(err).ToNot(HaveOccurred())
	Expect(k8sClient).ToNot(BeNil())

	infra := &configv1.Infrastructure{
		ObjectMeta: metav1.ObjectMeta{
			Name: globalInfrastructureName,
		},
		Spec: configv1.InfrastructureSpec{
			PlatformSpec: configv1.PlatformSpec{
				Type: configv1.AzurePlatformType,
			},
		},
	}
	Expect(k8sClient.Create(ctx, infra)).Should(Succeed())
	Expect(k8sClient.Get(ctx, client.ObjectKey{Name: globalInfrastructureName}, infra)).Should(Succeed())

	infra.Status = configv1.InfrastructureStatus{
		ControlPlaneTopology:   configv1.SingleReplicaTopologyMode,
		InfrastructureTopology: configv1.SingleReplicaTopologyMode,
		InfrastructureName:     "test-rghk",
		PlatformStatus: &configv1.PlatformStatus{
			Type:  configv1.AzurePlatformType,
			Azure: &configv1.AzurePlatformStatus{},
		},
	}
	Expect(k8sClient.Status().Update(ctx, infra)).Should(Succeed())
	Expect(k8sClient.Create(ctx, machine.StubAzureCredentialsSecret())).Should(Succeed())
})

var _ = AfterSuite(func() {
	Expect(testEnv.Stop()).To(Succeed())
})

// StartTestManager adds recFn
func StartTestManager(mgr manager.Manager) context.CancelFunc {
	mgrCtx, cancel := context.WithCancel(ctx)
	go func() {
		defer GinkgoRecover()

		Expect(mgr.Start(mgrCtx)).To(Succeed())
	}()
	return cancel
}
