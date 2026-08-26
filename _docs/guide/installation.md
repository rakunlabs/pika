# Installation

Pika is distributed as a single static binary and an OCI container image. Pick whichever matches your environment.

## Docker

```sh
docker run -d \
  --name pika \
  -v pika:/data \
  -p 8080:8080 \
  ghcr.io/rakunlabs/pika:latest
```

| Path / port | Purpose                                                         |
| ----------- | --------------------------------------------------------------- |
| `/data`     | Persistent volume — holds the embedded bw database.             |
| `8080`      | HTTPS admin UI + authenticated `/data/*` endpoint.              |

Extra public ports (e.g. unauthenticated `/data/*`, Consul KV shim, custom Go-template responses) are configured at runtime under **Settings → Endpoints** and bind their own listeners — publish the matching `host:port` from the container as needed. See [Endpoints](./endpoints).

::: tip
The image is published for `linux/amd64` and `linux/arm64`. Pin a tag in production (`ghcr.io/rakunlabs/pika:v0.x.y`) instead of `latest`.
:::

## Docker Compose

```yaml
services:
  pika:
    image: ghcr.io/rakunlabs/pika:latest
    restart: unless-stopped
    environment:
      PIKA_LOG_LEVEL: info
      # At-rest encryption is enabled by default and the master key
      # is supplied through the web UI on first start (and after
      # every restart). See _docs/guide/encryption.md for details.
    volumes:
      - pika:/data
    ports:
      - "8080:8080"

volumes:
  pika:
```

## Kubernetes

A ready-to-apply Kustomize bundle ships in [`ci/kubernetes/`](https://github.com/rakunlabs/pika/tree/main/ci/kubernetes). The default deploys a 3-replica StatefulSet with per-pod PVCs, a ClusterIP Service, and a headless Service for cluster peer discovery. Unlike the standalone binary default, the bundle sets `server.tls.enabled: false` so a Gateway / Ingress can terminate public HTTPS and talk to Pika over in-cluster HTTP.

```sh
kubectl apply -k https://github.com/rakunlabs/pika/ci/kubernetes
```

Pin to a specific version:

```sh
kubectl apply -k "https://github.com/rakunlabs/pika/ci/kubernetes?ref=v0.1.0"
```

::: warning
Before deploying, change the placeholder `security_key` in `secret.yaml` to a real random value (e.g. `openssl rand -base64 48`). All replicas must share the same key.
:::

See the [Kubernetes guide](./kubernetes) for a deeper walk-through and customization patterns.

## Binary

Pre-built binaries are attached to each [GitHub release](https://github.com/rakunlabs/pika/releases). Download, extract, and run:

```sh
./pika
```

Pika listens with HTTPS on `:8080` by default and stores data in `./data/pika`. Override either with environment variables — see [Configuration](./configuration).

## Building from source

You'll need Go 1.22+ and Node 20+ (for the UI):

```sh
git clone https://github.com/rakunlabs/pika.git
cd pika
make build       # builds the UI and the Go binary into ./dist/pika
./dist/pika
```

## First-run setup

On first launch, open `https://localhost:8080`. The default certificate is self-signed, so your browser will ask you to trust it. The UI presents a setup screen to create the initial admin account. After that, sign in and head to **Settings** to:

- Mint your first [API token](/guide/tokens-and-scopes).
- (Optional) Enable [external auth](./authentication) — OAuth2/OIDC or forward-auth headers.
- (Optional) Configure [external resources](./inheritance/) for inheritance.
- (Optional) Add an [Endpoint](./endpoints) — direct config data, External resource, Consul KV, or custom Go-template — for clients that don't speak pika's Bearer-auth `/data/*` API.

Once you're happy with the layout, the [Concepts](./concepts) page is the best next read.
