# Kubernetes

Pika ships with a Kustomize bundle in [`ci/kubernetes/`](https://github.com/rakunlabs/pika/tree/main/ci/kubernetes). It includes:

- A `Namespace` (`pika`).
- A `ServiceAccount`.
- A `ConfigMap` with `pika.yaml`.
- A `Secret` carrying the cluster pre-shared key.
- A `StatefulSet` with 3 replicas and per-pod PVCs (`volumeClaimTemplates`).
- A `ClusterIP` `Service` for HTTP traffic (8080 admin, 9090 public).
- A headless `Service` for cluster peer discovery on QUIC (`5000/UDP`).

Ingress is intentionally **not** included — bring your own (`Ingress`, Gateway API `HTTPRoute`, etc.) and point it at the `pika` Service on port 8080.

## Quick deploy

Apply directly from the repository:

```sh
kubectl apply -k https://github.com/rakunlabs/pika/ci/kubernetes
```

Pin to a specific version:

```sh
kubectl apply -k "https://github.com/rakunlabs/pika/ci/kubernetes?ref=v0.1.0"
```

::: warning
Before deploying, change the placeholder `security_key` in [`secret.yaml`](https://github.com/rakunlabs/pika/blob/main/ci/kubernetes/secret.yaml) to a real random value (e.g. `openssl rand -base64 48`). All replicas must share the same key.
:::

## Customising with a remote base

Create your own `kustomization.yaml` that references the upstream manifests and overrides what you need:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - https://github.com/rakunlabs/pika/ci/kubernetes?ref=main

images:
  - name: ghcr.io/rakunlabs/pika
    newTag: v0.3.0

patches:
  - target:
      kind: ConfigMap
      name: pika
    patch: |
      - op: replace
        path: /data/pika.yaml
        value: |
          server:
            port: "8080"
            auth:
              session_ttl: 24h
              cookie:
                secure: true
                same_site: lax
          storage:
            bw:
              path: /data/pika
          cluster:
            enabled: true
            dns_addr: pika-cluster.pika.svc.cluster.local
            replicas: 3
            port: 5000
  - target:
      kind: Secret
      name: pika-cluster
    patch: |
      - op: replace
        path: /stringData/security_key
        value: "your-long-random-string-here"
```

Then apply:

```sh
kubectl apply -k .
```

## Replicas and quorum

The default deploys a 3-replica cluster (see [Clustering](./clustering)). If you change `spec.replicas` on the StatefulSet, also update `cluster.replicas` in the `ConfigMap` — both must match for the quorum math.

## Public port

The public port (9090) is exposed on the `pika` Service alongside the admin port. By default it is reachable only inside the cluster at `pika.pika.svc.cluster.local:9090`. Route it externally with your own Ingress / HTTPRoute if you want to expose `/data/*` to consumers outside the cluster.

## Encryption key

Pika no longer reads its at-rest master key from environment or
config — see [Encryption](./encryption). On every pod restart an
administrator must unlock the server through `POST /api/v1/key/unlock`
or the web UI before any non-allowlisted request will succeed.

Operationally this means:

- **Rolling updates** require an unlock per pod. Plan
  maintenance windows accordingly, or scale to zero and back so
  only one unlock per upgrade is needed if your business model
  tolerates the downtime.
- **OOM / liveness restarts** also require manual unlock — keep
  the runbook accessible to the on-call rotation.
- **Headless auto-unseal** (Vault transit, KMS) is not currently
  supported. If you need it, file an issue describing your
  threat model.

A `readinessProbe` against `/api/v1/key/status` can keep traffic off
locked pods until they are unlocked:

```yaml
- target:
    kind: StatefulSet
    name: pika
  patch: |
    - op: add
      path: /spec/template/spec/containers/0/readinessProbe
      value:
        httpGet:
          path: /api/v1/key/status
          port: 8080
        initialDelaySeconds: 5
        periodSeconds: 10
```

Combine with a JSON-aware probe (e.g. an `ExecAction` running `curl`
+ `jq '.unlocked == true'`) if you need the readiness gate to wait
for the unlock too rather than just for the endpoint to answer.

## External Secrets Operator

If you use [External Secrets Operator](https://external-secrets.io), [`ci/kubernetes/examples/`](https://github.com/rakunlabs/pika/tree/main/ci/kubernetes/examples) contains examples for pulling pika-stored configs and TLS material into Kubernetes Secrets via a `SecretStore` / `ExternalSecret` pair. This lets you keep the source of truth in pika while still feeding existing workloads that consume native `Secret`s.

## Observability

Pika emits metrics, traces, and logs through [tell](https://github.com/rakunlabs/tell) (OpenTelemetry under the hood). Configure exporters under the `telemetry:` key in the ConfigMap, or via `PIKA_TELEMETRY_*` environment variables.

## Probes

Both `/healthz` endpoints (admin port 8080 and public port 9090) return `200 OK` once the storage is open. Add liveness and readiness probes to the StatefulSet patch:

```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 8080
  periodSeconds: 10
readinessProbe:
  httpGet:
    path: /healthz
    port: 8080
  periodSeconds: 5
```
