module sigs.k8s.io/cluster-api-provider-azure

go 1.13

require (
	github.com/Azure/azure-sdk-for-go v32.5.0+incompatible
	github.com/Azure/go-autorest/autorest v0.9.2
	github.com/Azure/go-autorest/autorest/adal v0.7.0
	github.com/Azure/go-autorest/autorest/to v0.3.0
	github.com/Azure/go-autorest/autorest/validation v0.2.0 // indirect
	github.com/ghodss/yaml v1.0.0
	github.com/go-logr/logr v0.1.0
	github.com/golang/mock v1.3.1
	github.com/onsi/ginkgo v1.12.0
	github.com/onsi/gomega v1.8.1
	github.com/openshift/api v0.0.0-20200424083944-0422dc17083e
	github.com/openshift/machine-api-operator v0.2.1-0.20200611014855-9a69f85c32dd
	github.com/spf13/cobra v0.0.5
	golang.org/x/crypto v0.0.0-20200220183623-bac4c82f6975

	// kube 1.18
	k8s.io/api v0.18.2
	k8s.io/apimachinery v0.18.2
	k8s.io/client-go v0.18.2
	k8s.io/code-generator v0.18.2
	k8s.io/klog v1.0.0
	k8s.io/kubectl v0.18.0-rc.1
	k8s.io/utils v0.0.0-20200327001022-6496210b90e8
	sigs.k8s.io/controller-runtime v0.6.0
	sigs.k8s.io/controller-tools v0.3.0
	sigs.k8s.io/yaml v1.2.0
)

replace sigs.k8s.io/cluster-api-provider-aws => github.com/openshift/cluster-api-provider-aws v0.2.1-0.20200612013036-1a97c62077cb
