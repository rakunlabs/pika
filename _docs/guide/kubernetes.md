# Kubernetes

Pika ships with a Kustomize bundle in [`ci/kubernetes/`](https://github.com/rakunlabs/pika/tree/main/ci/kubernetes). It includes:

- A `Namespace` (`pika`).
- A `ServiceAccount`.
- A `ConfigMap` with `pika.yaml`.
- A `Secret` carrying the cluster pre-shared key.
- A `StatefulSet` with 3 replicas and per-pod PVCs (`volumeClaimTemplates`).
- A `ClusterIP` `Service` for internal admin HTTP traffic (8080) and optional Endpoint traffic (9090).
- A headless `Service` for cluster peer discovery on QUIC (`5000/UDP`).

Ingress / Gateway is intentionally **not** included — bring your own (`Ingress`, Gateway API `HTTPRoute`, etc.) and point it at the `pika` Service on port 8080. The Kubernetes bundle disables Pika's in-pod HTTPS so the Gateway can terminate and renew public TLS certificates.

::: tip
The standalone Pika binary still defaults to HTTPS. The Kubernetes manifests set `server.tls.enabled: false` specifically for the common Gateway / cert-manager model where the in-cluster upstream is HTTP.
:::

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
            tls:
              enabled: false
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

## Endpoints

Endpoints are operator-defined listeners configured at runtime (Settings → Endpoints) — each binds its own port and can serve HTTP or HTTPS using the managed certificate. To expose one in Kubernetes:

1. Pick a deterministic port per endpoint (e.g. `9090` for Consul mode).
2. Add the port to the StatefulSet `containerPort` list and to the `pika` Service.
3. Add an Ingress / HTTPRoute if you want external access.

The endpoint configuration itself is stored in pika (not Kubernetes), so a single ConfigMap change is not enough — you also need to add the entry from the UI on a running pod.

## Gateway TLS

The default Kubernetes shape is:

```text
client --HTTPS--> Gateway / Ingress --HTTP--> pika Service:8080
```

Use cert-manager, your cloud Gateway integration, or your GatewayClass-specific certificate mechanism to populate the Gateway listener certificate. Pika does not need to manage the public certificate in this mode.

If you want the Gateway to re-encrypt to Pika instead, override the ConfigMap with `server.tls.enabled: true` and configure backend TLS trust in your Gateway implementation (for Gateway API this is commonly done with `BackendTLSPolicy`, when supported).

Runtime database settings under `settings.server_tls` only apply when process-level `server.tls.enabled` is true. Because this bundle sets `server.tls.enabled: false`, the pod serves HTTP immediately on first install and does not wait for a DB setting.

When `server.tls.enabled: false`, the Pika UI hides **Settings → Certificates** because the managed HTTPS listener and runtime TLS policy are disabled by process config. Certificate lifecycle should be managed on the Gateway / cert-manager side instead.

## Sub-path routing

If your Gateway / Ingress serves Pika under a sub-path and forwards that prefix to the Service, set `server.base_path` in the ConfigMap:

```yaml
server:
  port: "8080"
  base_path: /pika
  tls:
    enabled: false
```

Use this for a public URL like `https://example.com/pika/` when the backend still receives `/pika/...` paths. If the Gateway strips `/pika` before proxying to Pika, leave `server.base_path` unset. The Kubernetes `/healthz` probes continue to work at the root path even when `server.base_path` is set.

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
          scheme: HTTP
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

The admin `/healthz` (port 8080) returns `200 OK` once the storage is open. The default bundle probes over HTTP because public TLS terminates at Gateway / Ingress:

```yaml
livenessProbe:
  httpGet:
    path: /healthz
    port: 8080
    scheme: HTTP
  periodSeconds: 10
readinessProbe:
  httpGet:
    path: /healthz
    port: 8080
    scheme: HTTP
  periodSeconds: 5
```
