#!/bin/sh

if [ -z "$AZURE_SUBSCRIPTION_ID" ]; then
  echo "AZURE_SUBSCRIPTION_ID env is not set"
  exit 1
fi

if [ -z "$AZURE_TENANT_ID" ]; then
  echo "AZURE_TENANT_ID env is not set"
  exit 1
fi

if [ -z "$AZURE_CLIENT_ID" ]; then
  echo "AZURE_CLIENT_ID env is not set"
  exit 1
fi

if [ -z "$AZURE_CLIENT_SECRET" ]; then
  echo "AZURE_CLIENT_SECRET env is not set"
  exit 1
fi

script_dir="$(cd $(dirname "${BASH_SOURCE[0]}") && pwd -P)"

secrethash=$(cat $script_dir/bootstrap.sh | \
  sed "s/  azure_client_id: FILLIN/  azure_client_id: $(echo -n $AZURE_CLIENT_ID | base64)/" | \
  sed "s/  azure_client_secret: FILLIN/  azure_client_secret: $(echo -n $AZURE_CLIENT_SECRET | base64)/" | \
  sed "s/  azure_subscription_id: FILLIN/  azure_subscription_id: $(echo -n $AZURE_SUBSCRIPTION_ID | base64)/" | \
  sed "s/  azure_tenant_id: FILLIN/  azure_tenant_id: $(echo -n $AZURE_TENANT_ID | base64)/" | \
  base64 --w=0)

cat <<EOF > $script_dir/bootstrap.yaml
apiVersion: v1
kind: Secret
metadata:
  name: master-user-data-secret
  namespace: default
type: Opaque
data:
  userData: $secrethash
EOF
