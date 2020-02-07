module sigs.k8s.io/cluster-api-provider-azure

go 1.12

require (
	github.com/Azure/azure-sdk-for-go v32.5.0+incompatible
	github.com/Azure/go-autorest/autorest v0.9.2
	github.com/Azure/go-autorest/autorest/adal v0.7.0
	github.com/Azure/go-autorest/autorest/to v0.3.0
	github.com/Azure/go-autorest/autorest/validation v0.2.0 // indirect
	github.com/ghodss/yaml v1.0.0
	github.com/golang/mock v1.3.1
	github.com/openshift/machine-api-operator v0.2.1-0.20200212112539-a9085e24cf62
	github.com/pkg/errors v0.8.1
	github.com/spf13/cobra v0.0.5
	golang.org/x/crypto v0.0.0-20191011191535-87dc89f01550
	k8s.io/api v0.17.2
	k8s.io/apiextensions-apiserver v0.0.0-20191121021419-88daf26ec3b8 // indirect
	k8s.io/apimachinery v0.17.2
	k8s.io/client-go v11.0.1-0.20190409021438-1a26190bd76a+incompatible
	k8s.io/klog v1.0.0
	k8s.io/utils v0.0.0-20191217005138-9e5e9d854fcc
	sigs.k8s.io/controller-runtime v0.4.0
	sigs.k8s.io/controller-tools v0.2.4
	sigs.k8s.io/yaml v1.1.0
)

// kubernetes-1.16.1
replace k8s.io/api => k8s.io/api v0.0.0-20191003000013-35e20aa79eb8

replace k8s.io/apimachinery => k8s.io/apimachinery v0.0.0-20190913080033-27d36303b655

replace k8s.io/client-go => k8s.io/client-go v0.0.0-20191003000419-f68efa97b39e
