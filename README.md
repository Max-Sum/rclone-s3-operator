# rclone-s3-operator

An experimental Kubernetes operator that provisions on-demand `rclone serve s3` gateways from Custom Resources. Each `Bucket` object spins up an rclone pod using a shared `rclone.conf` Secret in the operator namespace, exposes it via a Service in that namespace, and generates per-endpoint S3-style credentials in a Secret located in the Bucket's namespace.

## What gets installed

- CRD `buckets.storage.max.io`.
- Controller Deployment and RBAC via Helm.
- Optional `rclone.conf` Secret if you provide inline config.

## Quick start

1. Prepare a Secret containing `rclone.conf` (or let the chart create one):
   ```yaml
   apiVersion: v1
   kind: Secret
   metadata:
     name: rclone-secret
     namespace: rclone-s3-operator
   stringData:
     rclone.conf: |
       [minio]
       type = s3
       provider = Minio
       access_key_id = minioadmin
       secret_access_key = minioadmin
       endpoint = http://minio:9000
   ```
2. Install the operator:
   ```shell
   helm install rclone-operator ./charts/rclone-s3-operator \
     --set image.repository=ghcr.io/example/rclone-s3-operator \
     --set image.tag=latest
   ```
3. Create a gateway (CR can live in any namespace):
   ```yaml
   apiVersion: storage.max.io/v1alpha1
   kind: Bucket
   metadata:
     name: photos-s3
     namespace: rclone-s3-operator
   spec:
     remote: minio
     readOnly: false
   ```
4. Read connection details from the generated Secret `<name>-rclone-s3-<uidSuffix>-credentials` (or `spec.secretName` if set) in the Bucket's namespace, and the Service DNS `<name>-rclone-s3-<uidSuffix>.<release-namespace>.svc:<port>` in the operator namespace.

> Buckets can be created in any namespace. The rclone Deployment/Service live in the operator namespace; the credentials Secret lives alongside the Bucket.

## Custom resource

`Bucket.spec` fields:

- `remote` (string, required): rclone remote name/path to serve.
- `port` (int32, default `9000`), `serviceType` (default `ClusterIP`), `serviceName` (base; a UID suffix is auto-appended for uniqueness), `secretName` (name for the generated credential Secret in the Bucket namespace).
- `image` (default `rclone/rclone:latest`), `replicas` (default `1`), `resources`, `readOnly`, `extraArgs`: pod tuning. Use `extraArgs` to set S3 flags.
- `podLabels`, `podAnnotations`, `podAffinity`, `nodeAffinity`: pod placement/metadata controls.

Status surfaces `endpoint`, `secretName`, and a `Ready` condition once dependent resources exist.

## Helm values hints

- `image.repository` / `tag`: operator image.
- `rcloneConfig.enabled=true` and `rcloneConfig.data` to inline a config secret.
- `manager.args`: extra flags to the controller manager.
- `metricsService.enabled`: expose metrics on port 8080.

## Building locally

The repo declares `sigs.k8s.io/controller-runtime` in `go.mod`. Run `go mod tidy` (network access required) and build the manager:

```shell
go mod tidy
go build ./cmd/manager
```
