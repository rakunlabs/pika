# Introduction

**pika** is a self-hosted server that combines three things into a single binary:

1. **A configuration store** — versioned JSON / YAML / TOML configs with per-environment variants and rich inheritance from external sources.
2. **A secrets manager** — encryption at rest with key rotation, plus integrations for pulling values from Vault, Kubernetes, cloud secret managers, and more.
3. **A raw file server** — mount local disks, S3, FTP/SFTP, WebDAV, or Vercel Blob and expose them over HTTP, FTP, SFTP, TFTP, or WebDAV.

Everything is administered through a built-in web UI; consumers fetch resolved data over a small, well-defined HTTP API.

## Why pika?

- **One binary.** No external database, no companion process — pika ships with an embedded [bw](https://github.com/rakunlabs/bw) (BadgerDB) store. Mount a single volume, point it at a port, done.
- **Edit-aware.** Every save is versioned. You can roll back, diff, or pin a consumer to a specific version using either an integer or a semver constraint.
- **Inheritance instead of templating.** Compose a final config out of pieces sourced from Vault, Kubernetes, S3, Consul, etcd, AWS Secrets Manager / SSM, GCP Secret Manager, Azure Key Vault, plain HTTP, or other pika files — without macros or templating languages.
- **Hot reload.** Authentication strategies, hooks, raw mounts, and external sources are managed at runtime from the UI. No restart needed.
- **Cluster-ready.** Run a 3+ node HA cluster with QUIC-based peer discovery, leader-elected writes, and replicated reads.

## Try it in 30 seconds

```sh
docker run -d --name pika -v pika:/data -p 8080:8080 ghcr.io/rakunlabs/pika:latest
```

Open <http://localhost:8080>, create the initial admin account, and you have a working server.

To consume a config, create a file in the UI, then mint an API token under **Settings → Tokens** and read it:

```sh
curl -H "Authorization: Bearer pika_..." http://localhost:8080/data/myapp/config
```

## Where to go next

- [Installation](./installation) — Docker, Kubernetes, or a static binary.
- [Configuration](./configuration) — environment variables and the YAML config file.
- [Concepts](./concepts) — folders, configs, versions, variants, and inheritance.
- [Consuming data](/reference/consuming-data) — the `/data/*` endpoint, authentication, and query parameters.
- [Admin API](/reference/api) — the full REST surface for management operations.

## Project status

Pika is actively developed at [rakunlabs/pika](https://github.com/rakunlabs/pika). Issues and pull requests are welcome.
