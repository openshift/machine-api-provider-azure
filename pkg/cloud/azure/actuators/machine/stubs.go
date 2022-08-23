package machine

import (
	"fmt"

	machinev1 "github.com/openshift/api/machine/v1beta1"
	machinecontroller "github.com/openshift/machine-api-operator/pkg/controller/machine"
	"github.com/openshift/machine-api-provider-azure/pkg/cloud/azure/actuators"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	defaultNamespace           = "default"
	region                     = "useast2"
	azureCredentialsSecretName = "azure-credentials-secret"
	userDataSecretName         = "azure-actuator-user-data-secret"

	keyName   = "azure-actuator-key-name"
	clusterID = "azure-actuator-cluster"
)

const userDataBlob = `#cloud-config
write_files:
- path: /root/node_bootstrap/node_settings.yaml
  owner: 'root:root'
  permissions: '0640'
  content: |
    node_config_name: node-config-master
runcmd:
- [ cat, /root/node_bootstrap/node_settings.yaml]
`

func stubProviderConfig() *machinev1.AzureMachineProviderSpec {
	var natRule int64
	return &machinev1.AzureMachineProviderSpec{
		UserDataSecret:       &corev1.SecretReference{Name: userDataSecretName, Namespace: defaultNamespace},
		CredentialsSecret:    &corev1.SecretReference{Name: azureCredentialsSecretName, Namespace: defaultNamespace},
		Location:             "eastus2",
		VMSize:               "Standard_B2ms",
		Image:                machinev1.Image{ResourceID: "/resourceGroups/os4-common/providers/Microsoft.Compute/images/test1-controlplane-0-image-20190529150403"},
		OSDisk:               machinev1.OSDisk{OSType: "Linux", ManagedDisk: machinev1.OSDiskManagedDiskParameters{StorageAccountType: "Premium_LRS"}, DiskSizeGB: 60},
		SSHPublicKey:         "c3NoLXJzYSBBQUFBQjNOemFDMXljMkVBQUFBREFRQUJBQUFCQVFDNEd4SGQ1L0pLZVJYMGZOMCt4YzM1eXhLdmtvb0Qwb0l2RnNCdDNHNTNaSXZlTWxwNUppQTRWT0Y3YjJ4cHZmL1FHbVpmWWl4d1JYMHdUKzRWUzYxV1ZNeUdZRUhPcE9QYy85MWZTcTg4bTJZbitBYVhTUUxTbFpaVkZJTDZsK296bjlRQ3NaSXhqSlpkTW5BTlRQdlhWMWpjSVNJeDhmU0pKeWVEdlhFU2FEQ2N1VjdPZTdvd01lVVpseCtUUEhkcU85eEV1OXFuREVYUXo1SUVQQUMwTElSQnVicmJVaTRQZUJFUlFieVBQd1p0Um9NN2pFZ3RuRFhDcmxYbXo2T0N3NXNiaE1FNEJCUVliVjBOV0J3Unl2bHJReDJtYzZHNnJjODJ6OWxJMkRKQ3ZJcnNkRW43NytQZThiWm1MVU83V0sxRUFyd2xXY0FiZU1kYUFkbzcgamNoYWxvdXBAZGhjcC0yNC0xNzAuYnJxLnJlZGhhdC5jb20K",
		PublicIP:             false,
		Subnet:               "stub-machine-subnet",
		PublicLoadBalancer:   "stub-machine-public-lb",
		InternalLoadBalancer: "stub-machine-internal-lb",
		NatRule:              &natRule,
		ManagedIdentity:      "stub-machine-identity",
		Vnet:                 "stub-vnet",
	}
}

func stubMachine() (*machinev1.Machine, error) {
	machinePc := stubProviderConfig()
	providerSpec, err := actuators.RawExtensionFromProviderSpec(machinePc)
	if err != nil {
		return nil, fmt.Errorf("EncodeMachineSpec failed: %v", err)
	}

	machine := &machinev1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-actuator-testing-machine",
			Namespace: defaultNamespace,
			Labels: map[string]string{
				machinev1.MachineClusterIDLabel: clusterID,
			},
			Annotations: map[string]string{
				// skip node draining since it's not mocked
				machinecontroller.ExcludeNodeDrainingAnnotation: "",
			},
		},

		Spec: machinev1.MachineSpec{
			ObjectMeta: machinev1.ObjectMeta{
				Labels: map[string]string{
					"node-role.kubernetes.io/master": "",
					"node-role.kubernetes.io/infra":  "",
				},
			},
			ProviderSpec: machinev1.ProviderSpec{Value: providerSpec},
		},
	}

	return machine, nil
}

func StubAzureCredentialsSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      azureCredentialsSecretName,
			Namespace: defaultNamespace,
		},
		Data: map[string][]byte{
			"azure_client_id":       []byte("FILLIN"),
			"azure_client_secret":   []byte("FILLIN"),
			"azure_region":          []byte("ZWFzdHVzMg=="),     // eastus2 in base64
			"azure_resource_prefix": []byte("b3M0LWNvbW1vbg=="), // os4-common in base64
			"azure_resourcegroup":   []byte("b3M0LWNvbW1vbg=="),
			"azure_subscription_id": []byte("FILLIN"),
			"azure_tenant_id":       []byte("FILLIN"),
		},
	}
}
