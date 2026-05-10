---
layout: home

hero:
  name: pika
  text: Config & file server
  tagline: General configuration server, secrets manager, and raw file server with a beautiful web UI and a powerful API.
  image:
    src: /favicon-192x192.png
    alt: pika
  actions:
    - theme: brand
      text: Get started
      link: /guide/getting-started
    - theme: alt
      text: View on GitHub
      link: https://github.com/rakunlabs/pika

features:
  - icon: 📝
    title: Multi-format configs
    details: Store and serve JSON, YAML, and TOML. Convert between formats on the fly with a single query parameter.
  - icon: 🌿
    title: Versions & variants
    details: Every save creates a new version. Tag versions with semver constraints and keep per-environment variants (prod, staging, dev) side by side.
  - icon: 🔗
    title: Config inheritance
    details: Pull values from Vault, Kubernetes Secrets, Consul, etcd, AWS, GCP, Azure, plain HTTP, or other pika files — all merged into a single resolved config.
  - icon: 🔐
    title: Encryption at rest
    details: Optional XChaCha20-Poly1305 envelope encryption with key rotation. Your secrets stay encrypted on disk.
  - icon: 🗂️
    title: Raw file server
    details: Mount local disks, S3, FTP/SFTP, WebDAV, or Vercel Blob and serve them over HTTP, FTP, SFTP, TFTP, or WebDAV.
  - icon: 🪝
    title: Event hooks
    details: Push file and config changes to HTTP webhooks, Kafka, Redis Pub/Sub, or NATS — with custom Go templates for the payload.
  - icon: 🔑
    title: Token-based access
    details: Issue API tokens with glob-scoped read/write/delete permissions. Plug in OAuth2/OIDC, LDAP, or header-based forward auth.
  - icon: 🌐
    title: Cluster-ready
    details: Run a 3+ node HA cluster with QUIC peer discovery and leader-elected writes. Drop-in Kubernetes manifests included.
---

## Quick start

```sh
docker run -d --name pika -v pika:/data -p 8080:8080 ghcr.io/rakunlabs/pika:latest
```

Open `http://localhost:8080` and create the initial admin account.

::: tip Next steps
Read the [getting-started guide](/guide/getting-started) or skip straight to [consuming data](/reference/consuming-data) if you already have a server running.
:::
