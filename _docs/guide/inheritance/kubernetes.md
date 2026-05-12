# Kubernetes

Pika can read `Secret` and `ConfigMap` objects directly from the Kubernetes API and merge their contents into a config via [inheritance](./). It talks to the Kubernetes API over HTTPS using a small built-in client — no `kubectl`, no Helm, no extra dependencies.

This page covers:

- Which resources pika can read.
- The three authentication modes (in-cluster service account, kubeconfig path, inline kubeconfig).
- The RBAC you need to grant.
- How inheritance paths look.

## What pika reads

The client supports two Kubernetes resource kinds:

| Kind        | Endpoint                                              | Value handling                                    |
| ----------- | ----------------------------------------------------- | ------------------------------------------------- |
| `Secret`    | `GET /api/v1/namespaces/{ns}/secrets/{name}`          | Values are auto-base64-decoded into UTF-8 strings. |
| `ConfigMap` | `GET /api/v1/namespaces/{ns}/configmaps/{name}`       | Values are used as-is.                             |

The path browser in the UI also lists namespaces, secrets, and configmaps so you can drill down without typing paths by hand.

## Authentication modes

Configure the resource under **Settings → External Resources → Add Resource → Kubernetes**. Pick one of three modes:

```text
○ In-cluster (service account)
○ Kubeconfig file path
○ Paste kubeconfig
```

Selection priority in the backend (when more than one is set, the highest-priority mode wins):

1. **Inline kubeconfig** — `kubeconfig_content` field.
2. **Kubeconfig path** — `kubeconfig` field, read from the pika server filesystem.
3. **In-cluster** — both fields empty; pika reads its own service-account token.

### 1. In-cluster (service account)

Use this when **pika itself runs as a Pod** in the cluster you want to read from. Leave the form empty and the client picks up the projected service-account token automatically:

| File                                                            | Used for         |
| --------------------------------------------------------------- | ---------------- |
| `/var/run/secrets/kubernetes.io/serviceaccount/token`           | Bearer token.    |
| `/var/run/secrets/kubernetes.io/serviceaccount/ca.crt`          | TLS verification. |
| `KUBERNETES_SERVICE_HOST` / `KUBERNETES_SERVICE_PORT` env vars  | API server URL.  |

Pika **re-reads the token from disk on every request**, so projected token rotation works without restarts.

#### Required RBAC

The default Kubernetes manifests in `ci/kubernetes/` ship a `ServiceAccount` named `pika` but no `Role` / `RoleBinding`. You have to grant access yourself.

Minimal `Role` for a single namespace:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: pika-reader
  namespace: default
rules:
  - apiGroups: [""]
    resources: ["secrets", "configmaps"]
    verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: pika-reader
  namespace: default
subjects:
  - kind: ServiceAccount
    name: pika
    namespace: pika
roleRef:
  kind: Role
  name: pika-reader
  apiGroup: rbac.authorization.k8s.io
```

For access across all namespaces, use a `ClusterRole` + `ClusterRoleBinding` instead:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: pika-reader
rules:
  - apiGroups: [""]
    resources: ["secrets", "configmaps", "namespaces"]
    verbs: ["get", "list"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: pika-reader
subjects:
  - kind: ServiceAccount
    name: pika
    namespace: pika
roleRef:
  kind: ClusterRole
  name: pika-reader
  apiGroup: rbac.authorization.k8s.io
```

::: tip
The `namespaces` resource is only needed if you want the **path browser** in the UI to list namespaces. If you'll always type paths by hand, you can drop it.
:::

::: warning
Granting `secrets` / `get` on a namespace lets pika read every secret in it. Scope the `Role` to the smallest namespace set you actually need, and remember that anyone with `files.write` permission in pika can build an inheritance entry that exposes those secret values through `/data/*`.
:::

### 2. Kubeconfig file path

Use this when pika does **not** run inside the target cluster — for example, pika in cluster A reading from cluster B, or pika running on a bare VM. Mount a kubeconfig onto the pika filesystem and put the path in the form:

```text
Authentication: Kubeconfig file path
Path          : /etc/pika/kubeconfig
```

Pika reads the file lazily on first use and re-reads it whenever a fresh client is built. The file's user must contain either a static `token` or a `client-certificate-data` + `client-key-data` pair — `exec`-style credential plugins are not supported.

In Kubernetes, the cleanest way to mount it is from a `Secret`:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: pika-target-cluster
  namespace: pika
type: Opaque
stringData:
  kubeconfig: |
    apiVersion: v1
    kind: Config
    # ...
```

…and a volume on the StatefulSet:

```yaml
volumes:
  - name: target-kubeconfig
    secret:
      secretName: pika-target-cluster
volumeMounts:
  - name: target-kubeconfig
    mountPath: /etc/pika/kubeconfig
    subPath: kubeconfig
    readOnly: true
```

### 3. Paste kubeconfig (inline)

When you don't want to manage a separate file or `Secret`, paste the full kubeconfig YAML straight into the UI:

```text
Authentication: Paste kubeconfig
Kubeconfig YAML: <textarea>
```

Pika stores the content in its database alongside the rest of the resource. With the encryption key set ([Encryption](../encryption)), it's encrypted at rest. The same supported features apply — bearer tokens and mTLS via `client-certificate-data` / `client-key-data`.

Use this mode when:

- You're running pika outside Kubernetes (Docker, bare metal) and want all configuration in the UI.
- You're managing a fleet of small clusters and don't want a host-level mount per cluster.
- You want a quick "try it out" path without writing files.

::: tip
Pika SHA-256-hashes the inline content to derive a cache key for the in-process client pool, so different inline kubeconfigs don't share a connection.
:::

## Supported kubeconfig features

Pika's parser is intentionally minimal. It reads:

| Field                                                   | Supported |
| ------------------------------------------------------- | --------- |
| `clusters[*].cluster.server`                            | yes       |
| `clusters[*].cluster.certificate-authority-data`        | yes (base64) |
| `clusters[*].cluster.certificate-authority`             | yes (path)   |
| `clusters[*].cluster.insecure-skip-tls-verify`          | yes       |
| `users[*].user.token`                                   | yes       |
| `users[*].user.client-certificate-data` + `client-key-data` | yes (mTLS) |
| `contexts[*]` + `current-context`                       | yes       |
| `users[*].user.exec`                                    | **no** (use a static token instead) |
| `users[*].user.auth-provider`                           | **no** (use a static token instead) |
| `users[*].user.username` / `password`                   | **no**    |

If your kubeconfig depends on `exec` plugins (e.g. `aws eks get-token`, `gke-gcloud-auth-plugin`), generate a long-lived `Secret`-backed service-account token in the target cluster and use that instead.

## Inheritance entry shape

Once the resource is saved, reference it from any config file's **Inherits** section. The `path` is always `<namespace>/<kind>/<name>`:

```json
{
  "resource": "k8s",
  "path": "default/secret/db-credentials",
  "paths": ["password"],
  "inject": "database.password"
}
```

| Kind        | Example path                              |
| ----------- | ----------------------------------------- |
| Secret      | `default/secret/db-credentials`           |
| ConfigMap   | `default/configmap/feature-flags`         |

`paths` and `inject` work the same as for any other inheritance source — see [Inheritance](./).

### Examples

**Pull a single secret value out of a namespace:**

```json
{
  "resource": "k8s",
  "path": "production/secret/myapp",
  "paths": ["DATABASE_URL"],
  "inject": "database.url"
}
```

If the secret has `{ "DATABASE_URL": "postgres://..." }`, the resolved config gains `{ "database": { "url": "postgres://..." } }`.

**Merge an entire ConfigMap into the resolved output:**

```json
{
  "resource": "k8s",
  "path": "kube-system/configmap/cluster-info"
}
```

All keys in the ConfigMap are merged at the document root.

**Combine cluster-supplied secrets with a static base file:**

```json
[
  { "source":   "myapp/config" },
  { "resource": "k8s", "path": "default/secret/myapp", "inject": "secrets" }
]
```

The hand-edited file content overrides both, so emergency overrides are a one-line edit away.

## Operational notes

- **Caching.** Pika keeps one HTTP client per resource configuration (cached by mode + path or hashed content). The first request after a config change rebuilds the client.
- **Failures bubble up.** If the API returns an error or the network fails, `/data/*` returns `502` — the consumer sees the failure rather than a stale cached value.
- **Path browser permissions.** The UI's **Browse** button hits `GET /api/v1/external/{name}/paths`, which requires the `settings.manage` capability. Read-only consumers don't need it.
- **Multiple clusters.** Define one external resource per cluster (e.g. `k8s-prod`, `k8s-staging`). The two are completely independent — different auth, different RBAC, different cache entries.
