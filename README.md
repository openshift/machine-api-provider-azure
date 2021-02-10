# Cluster API Provider Azure

This repository hosts an implementation of a provider for Azure for the
OpenShift [machine-api](https://github.com/openshift/cluster-api).

This provider runs as a machine-controller deployed by the
[machine-api-operator](https://github.com/openshift/machine-api-operator)

# Upstream Implementation
Other branches of this repository may choose to track the upstream
Kubernetes [Cluster-API Azure provider](https://github.com/kubernetes-sigs/cluster-api-provider-azure/)

In the future, we may align the master branch with the upstream project as it
stabilizes within the community.

# How to deploy and run azure actuator

## Azure cloud resources

1. **Configure RBAC rules so the actuator can CRUD resources**

    ```sh
    $ az role definition update --role-definition azure-role.json
    ```

## Deploy machine API plane with minikube

1. **Install kvm**

    Depending on your virtualization manager you can choose a different [driver](https://github.com/kubernetes/minikube/blob/master/docs/drivers.md).
    In order to install kvm, you can run (as described in the [drivers](https://github.com/kubernetes/minikube/blob/master/docs/drivers.md#kvm2-driver) documentation):

    ```sh
    $ sudo yum install libvirt-daemon-kvm qemu-kvm libvirt-daemon-config-network
    $ systemctl start libvirtd
    $ sudo usermod -a -G libvirt $(whoami)
    $ newgrp libvirt
    ```

    To install to kvm2 driver:

    ```sh
    curl -Lo docker-machine-driver-kvm2 https://storage.googleapis.com/minikube/releases/latest/docker-machine-driver-kvm2 \
    && chmod +x docker-machine-driver-kvm2 \
    && sudo cp docker-machine-driver-kvm2 /usr/local/bin/ \
    && rm docker-machine-driver-kvm2
    ```

2. **Deploying the cluster**

    To install minikube `v1.1.0`, you can run:

    ```sg
    $ curl -Lo minikube https://storage.googleapis.com/minikube/releases/v1.1.0/minikube-linux-amd64 && chmod +x minikube && sudo mv minikube /usr/local/bin/
    ```

    To deploy the cluster:

    ```
    $ minikube start --vm-driver kvm2 --kubernetes-version v1.13.1 --v 5
    $ eval $(minikube docker-env)
    ```

3. **Deploying machine API controllers**

    For development purposes the azure machine controller itself will run out of the machine API stack.
    Otherwise, docker images needs to be built, pushed into a docker registry and deployed withing the stack.

    To deploy the stack:
    ```
    kustomize build config | kubectl apply --validate=false -f -
    ```

4. **Deploy secret with Azure credentials**

   Azure actuator assumes existence of a secret file (references in machine object) with base64 encoded credentials:

   ```yaml
   apiVersion: v1
   kind: Secret
   metadata:
     name: test
     namespace: default
   type: Opaque
   data:
     azure_client_id: FILLIN
     azure_client_secret: FILLIN
     azure_region: ZWFzdHVzMg==   # eastus2 in base64
     azure_resource_prefix: b3M0LWNvbW1vbg== # os4-common in base64
     azure_resourcegroup: b3M0LWNvbW1vbg==
     azure_subscription_id: FILLIN
     azure_tenant_id: FILLIN
   ```

   ```sh
   $ kubectl apply -f secret.yaml
   ```

## Test locally built azure actuator

1. **Tear down machine-controller**

   Deployed machine API plane (`machine-api-controllers` deployment) is (among other
   controllers) running `machine-controller`. In order to run locally built one,
   simply edit `machine-api-controllers` deployment and remove `machine-controller` container from it.

1. **Build and run azure actuator outside of the cluster**

   ```sh
   $ go build -o bin/machine-controller-manager sigs.k8s.io/cluster-api-provider-azure/cmd/manager
   ```

   ```sh
   $ .bin/machine-controller-manager --kubeconfig ~/.kube/config --logtostderr -v 5 -alsologtostderr
   ```
    If running in cointainer with podman and encountering permission issues, see [hacking-guide](https://github.com/openshift/machine-api-operator/blob/master/docs/dev/hacking-guide.md#troubleshooting-make-targets).

1. **Deploy k8s apiserver through machine manifest**:

   To deploy user data secret with kubernetes apiserver initialization (under [config/master-user-data-secret.yaml](config/master-user-data-secret.yaml)):

   ```yaml
   $ kubectl apply -f config/master-user-data-secret.yaml
   ```

   To deploy kubernetes master machine (under [config/master-machine.yaml](config/master-machine.yaml)):

   ```yaml
   $ kubectl apply -f config/master-machine.yaml
   ```

1. **Pull kubeconfig from created master machine**

   All virtual machines created by machine templates under `config` can be
   accessed by using `config/sshkey` private key.

   The master public IP can be accessed from Azure Portal. Once done, you
   can collect the kube config by running:

   ```
   $ ssh -i config/sshkey capi@PUBLICIP 'sudo cat /root/.kube/config' > kubeconfig
   $ kubectl --kubeconfig=kubeconfig config set-cluster kubernetes --server=https://PUBLICIP:8443
   ```

   Once done, you can access the cluster via `kubectl`. E.g.

   ```sh
   $ kubectl --kubeconfig=kubeconfig get nodes
   ```

## Deploy k8s cluster in Azure with machine API plane deployed

1. **Generate bootstrap user data**

   To generate bootstrap script for machine api plane, simply run:

   ```sh
   $ ./examples/generate-bootstrap.sh
   ```

   The script requires `AZURE_SUBSCRIPTION_ID`, `AZURE_TENANT_ID`, `AZURE_CLIENT_ID` and `AZURE_CLIENT_SECRET` environment variables to be set.
   It generates `config/bootstrap.yaml` secret for master machine
   under `config/master-machine.yaml`.

   The generated bootstrap secret contains user data responsible for:
   - deployment of kube-apiserver
   - deployment of machine API plane with azure machine controllers
   - generating worker machine user data script secret deploying a node
   - deployment of worker machineset

1. **Deploy machine API plane through machine manifest**:

   First, deploy generated bootstrap secret:

   ```yaml
   $ kubectl apply -f config/bootstrap.yaml
   ```

   Then, deploy master machine (under [config/master-machine.yaml](config/master-machine.yaml)):

   ```yaml
   $ kubectl apply -f config/master-machine.yaml
   ```

1. **Pull kubeconfig from created master machine**

   All virtual machines created by machine templates under `config` can be
   accessed by using `config/sshkey` private key.

   The master public IP can be accessed from Azure Portal. Once done, you
   can collect the kube config by running:

   ```
   $ ssh -i config/sshkey capi@PUBLICIP 'sudo cat /root/.kube/config' > kubeconfig
   $ kubectl --kubeconfig=kubeconfig config set-cluster kubernetes --server=https://PUBLICIP:8443
   ```

   Once done, you can access the cluster via `kubectl`. E.g.

   ```sh
   $ kubectl --kubeconfig=kubeconfig get nodes
   ```
