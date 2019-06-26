package machine

import (
	"fmt"

	machinev1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	machinecontroller "github.com/openshift/cluster-api/pkg/controller/machine"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	providerspecv1 "sigs.k8s.io/cluster-api-provider-azure/pkg/apis/azureprovider/v1alpha1"
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

func stubProviderConfig() *providerspecv1.AzureMachineProviderSpec {
	var natRule int
	return &providerspecv1.AzureMachineProviderSpec{
		UserDataSecret:       &corev1.SecretReference{Name: userDataSecretName},
		CredentialsSecret:    &corev1.SecretReference{Name: azureCredentialsSecretName},
		Location:             "eastus2",
		VMSize:               "Standard_B2ms",
		Image:                providerspecv1.Image{ResourceID: "/resourceGroups/os4-common/providers/Microsoft.Compute/images/test1-controlplane-0-image-20190529150403"},
		OSDisk:               providerspecv1.OSDisk{OSType: "Linux", ManagedDisk: providerspecv1.ManagedDisk{StorageAccountType: "Premium_LRS"}, DiskSizeGB: 60},
		SSHPublicKey:         "c3NoLXJzYSBBQUFBQjNOemFDMXljMkVBQUFBREFRQUJBQUFCQVFDNEd4SGQ1L0pLZVJYMGZOMCt4YzM1eXhLdmtvb0Qwb0l2RnNCdDNHNTNaSXZlTWxwNUppQTRWT0Y3YjJ4cHZmL1FHbVpmWWl4d1JYMHdUKzRWUzYxV1ZNeUdZRUhPcE9QYy85MWZTcTg4bTJZbitBYVhTUUxTbFpaVkZJTDZsK296bjlRQ3NaSXhqSlpkTW5BTlRQdlhWMWpjSVNJeDhmU0pKeWVEdlhFU2FEQ2N1VjdPZTdvd01lVVpseCtUUEhkcU85eEV1OXFuREVYUXo1SUVQQUMwTElSQnVicmJVaTRQZUJFUlFieVBQd1p0Um9NN2pFZ3RuRFhDcmxYbXo2T0N3NXNiaE1FNEJCUVliVjBOV0J3Unl2bHJReDJtYzZHNnJjODJ6OWxJMkRKQ3ZJcnNkRW43NytQZThiWm1MVU83V0sxRUFyd2xXY0FiZU1kYUFkbzcgamNoYWxvdXBAZGhjcC0yNC0xNzAuYnJxLnJlZGhhdC5jb20K",
		SSHPrivateKey:        "LS0tLS1CRUdJTiBSU0EgUFJJVkFURSBLRVktLS0tLQpNSUlFcFFJQkFBS0NBUUVBdUJzUjNlZnlTbmtWOUh6ZFBzWE4rY3NTcjVLS0E5S0NMeGJBYmR4dWQyU0wzakphCmVTWWdPRlRoZTI5c2FiMy8wQnBtWDJJc2NFVjlNRS91RlV1dFZsVE1obUJCenFUajNQL2RYMHF2UEp0bUovZ0cKbDBrQzBwV1dWUlNDK3BmcU01L1VBckdTTVl5V1hUSndEVXo3MTFkWTNDRWlNZkgwaVNjbmc3MXhFbWd3bkxsZQp6bnU2TURIbEdaY2ZrengzYWp2Y1JMdmFwd3hGME0rU0JEd0F0Q3lFUWJtNjIxSXVEM2dSRVVHOGp6OEdiVWFECk80eElMWncxd3E1VjVzK2pnc09iRzRUQk9BUVVHRzFkRFZnY0VjcjVhME1kcG5PaHVxM1BOcy9aU05neVFyeUsKN0hSSisrL2ozdkcyWmkxRHUxaXRSQUs4SlZuQUczakhXZ0hhT3dJREFRQUJBb0lCQVFDdDlNWDVDd1NnNGJDaApCcXAyZWFpWjhndUI0ZENPdEFWV1FRVXB5VEtIbFhXalNhaTYrQTlScXNJelE2RUllUUtSdTZBbldEZnREWHV3CmZwWFRnV0lUUktUTUEzK3FwWnE0WXZyazQwaVkxNnk2NzF3cTdrM0FkSjlMWE1vMXhmMEJNbSs4NjlQYkJaKysKQjc1Z2t2RVRFL0ZlYmVCRm1QMFo2dWtuVFlUZGdnSGNHaDFHbU1zcDlHUFcrZkJ1TmVkNGxSSkdUcU5mVWJaeApEZDFvbkJqT3Z2eDF3NDk2UHdsYTlxRkwwRlBZMkg4YjdKM1NramZmTS91RmFWbVZGbTAvQ1pWdHMvWDUzR1ZIClBId2w3ZU5lOWNIQ2VYWFFNRzk3d05LSm9wL0MyRWo3NjY4NHJSVG1QVzUxV2FWTFA2SmQ4b3k3Um00WU52dTAKd0o5bk1PaUJBb0dCQU9nd2syYTQ2ZU16OHlMV2JEdE5McnRvdjZYSEYveTI1NjBBUHQreEMvOXdyMVo3QWJSQgpBMWJkb0ZyYzJ2bDh1UnpTVW93TTc4SFNQTk5Kamg5VVVjYWhFN3pvOTRaL0RiRkFRa04rSmh6K2hWeHY2amhrClR0VXhpUVBaSDBLVFppUEpta25CZlJnUUtoWmhiN0NiL2pyZ0IrNjEzdWFkd3pGcUtZL3lEaHBSQW9HQkFNcjgKTW02TVR4dXV4aUtLTjVQYlJMbytIemVFdzNZSUlmNVIrbEluMHFhdW1KbDFTeVVmR2ZWMjdvZm93ZGlNWEtOSwpjRHE0VjhvWnhmY1l0d3U3bmgyZGxCRi9EMUtUdG5HRGd1Q3FCb1JPT0RML3hBUmp2SVdUeG42UGp3d1B3Q2lkCnl0Qko5QnpiUGtBOXl1L3N5bU1zQnBuQkUxZDJOREwvMXVXZUtEekxBb0dBZnhZdlo5Y29kV3AyMXdla0grVkUKQWVING0rVllWTU5NRlY0QUMvSGREa2lBUUFaOXpVcVVhRlJRTTh1VXMxKzM5bldNSnduaHBTWE1reDA4aEJ0agoweU5SS1dJZU1XaVRkd1FrQU1zb1UxQmdjRkwxVVQ1ZUE4VGtLTTRMbFNZV2p0b0c3LzNPMlgvbmVXNkZjcFkvClZieFB1ekdpdW5sNVlDK3FaaFpuNzdFQ2dZRUF5WlVEa0gyTzRuTURHYkloMTVoZC9JZE5BUm03OHkvSWNvUi8KRDYrMHB3dWxTR0VQcTJIanFiM2V6T0grQUV3RWc3V2RGdk9UVzRXVTcvdC9iUXQ1enZkNjRKVktaanVEWjkrdQp6ZWFNYWtBejE1SGczR3NnQVpmci9Dd2RaMkVNK0VrYjdSWkVjNVBYa256TFdOSFRmQUZ3M0tpOXlKSCs3TmJlClYxSmxxMWtDZ1lFQWovQTROR24xdHVWMTVRdjNDN0xoSGxSbW1yNXhxRnlRVnJ3dUJ3NitQRUFUbjVQOHR5eWkKZ1BjTjJoZk5od251OUN6Zlk5YVlTWEorNVFreE9COEZlWEovWEYxUzBkazRwaGNkYnhiTmlLbnMyaXpldDloYQpDdFpiRllDUENOSE5TMlNndFZaY3ErVkhKQ1JqZ0hKUGhxeDc1aFRjMFlBQm1jV1IyRDc3V0k4PQotLS0tLUVORCBSU0EgUFJJVkFURSBLRVktLS0tLQo",
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
	providerSpec, err := providerspecv1.EncodeMachineSpec(machinePc)
	if err != nil {
		return nil, fmt.Errorf("EncodeMachineSpec failed: %v", err)
	}

	machine := &machinev1.Machine{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-actuator-testing-machine",
			Namespace: defaultNamespace,
			Labels: map[string]string{
				providerspecv1.ClusterIDLabel: clusterID,
			},
			Annotations: map[string]string{
				// skip node draining since it's not mocked
				machinecontroller.ExcludeNodeDrainingAnnotation: "",
			},
		},

		Spec: machinev1.MachineSpec{
			ObjectMeta: metav1.ObjectMeta{
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

func stubAzureCredentialsSecret() *corev1.Secret {
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
