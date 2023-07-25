# local-path-provisioner-exporter

## configuration

```
DEFAULT_PATH=/data/local-path-provisioner
DELAY_SECONDS=30
STORAGE_CLASS=local-path
NODE_NAME=<node-name>
```

## deploy

```
kubectl apply -k kustomization
```
