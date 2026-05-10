# Clustering

Pika supports a 3+ node cluster for high availability. Reads are served locally on every node from the replicated [bw](https://github.com/rakunlabs/bw) store; writes are routed to the elected leader and the diff is pushed to every follower before the leader replies. Peer discovery and leader election use [alan](https://github.com/rakunlabs/alan) over QUIC.

## How it works

```
            ┌──────────────────────────────────────────┐
            │                  Clients                 │
            └──────────┬───────────────┬───────────────┘
                       │ read/write    │ read
                       ▼               ▼
                   ┌──────┐        ┌──────┐
                   │ peer │◄──────►│ peer │
                   │   1  │  QUIC  │   2  │
                   └──┬───┘        └──┬───┘
                      │ ack writes    │
                      ▼               ▼
                          ┌──────┐
                          │ peer │  (this one is leader)
                          │   3  │
                          └──────┘
```

- **Reads** are served locally — no inter-node round-trip.
- **Writes** are forwarded by any non-leader peer to the current leader. The leader applies the change, fans the diff out to followers, and acks the original client only after every follower has applied it.
- **Failover** is automatic. The cluster tolerates losing one node out of three (or two out of five).

## Configuration

Enable clustering in `pika.yaml`:

```yaml
cluster:
  enabled: true
  dns_addr: pika-cluster.pika.svc.cluster.local  # resolves to all peer IPs
  replicas: 3                                    # quorum size
  port: 5000                                     # UDP/QUIC peer port
  security_key: "long-random-string"             # pre-shared key
```

Or via environment variables:

```sh
PIKA_CLUSTER_ENABLED=true
PIKA_CLUSTER_DNS_ADDR=pika-cluster.pika.svc.cluster.local
PIKA_CLUSTER_REPLICAS=3
PIKA_CLUSTER_PORT=5000
PIKA_CLUSTER_SECURITY_KEY=long-random-string
```

| Field                | Default                      | Description                                                         |
| -------------------- | ---------------------------- | ------------------------------------------------------------------- |
| `enabled`            | `false`                      | Master switch.                                                      |
| `dns_addr`           |                              | DNS name that resolves to all peer IPs.                             |
| `bind_addr`          | `0.0.0.0`                    | Local address the QUIC listener binds to.                           |
| `port`               | `5000`                       | UDP port for peer traffic.                                          |
| `replicas`           |                              | Cluster size. **Must match the actual number of peers.**            |
| `security_key`       |                              | Pre-shared key. **Identical across peers.**                         |
| `refresh_interval`   | `30s`                        | How often peers re-resolve `dns_addr`.                              |
| `heartbeat_interval` | `5s`                         | How often heartbeats are sent.                                      |
| `heartbeat_timeout`  | `30s`                        | Treat a peer as dead after this long without a heartbeat.           |
| `lock_key`           | `pika-leader`                | Internal key for leader election.                                   |
| `sync_interval`      | `5m`                         | Periodic full-state reconciliation interval.                        |
| `prefix`             | `pika`                       | Internal namespace.                                                 |
| `forward_timeout`    | `30s`                        | How long a follower waits when forwarding a write to the leader.    |

::: warning
`replicas` controls the quorum math. If you change the actual peer count, update this too. Mismatch leads to split-brain or stalled writes.
:::

## Local 3-node demo

`example/cluster/` contains three configs (`1.yaml`, `2.yaml`, `3.yaml`) that run on three loopback addresses. Set up `/etc/hosts`:

```text
127.0.1.1  pika.local
127.0.1.2  pika.local
127.0.1.3  pika.local
```

`example/cluster/1.yaml` (the others are identical except for ports and bind_addr):

```yaml
storage:
  bw:
    path: /tmp/pika1111
cluster:
  enabled: true
  dns_addr: alan.local
  bind_addr: 127.0.1.1
  replicas: 3
  security_key: 123456
server:
  port: 8091
```

Run all three in separate terminals:

```sh
CONFIG_FILE=example/cluster/1.yaml make run
CONFIG_FILE=example/cluster/2.yaml make run
CONFIG_FILE=example/cluster/3.yaml make run
```

The UIs are reachable on `http://127.0.1.1:8091`, `8092`, `8093`. Save a config on one node and watch it appear on the others.

## Sizing

| Cluster size | Tolerated failures |
| ------------ | ------------------ |
| 1            | 0                  |
| 3            | 1                  |
| 5            | 2                  |
| 7            | 3                  |

Even numbers offer no resilience advantage over the next-lower odd number. Stick with 3 or 5 for typical production deployments.
