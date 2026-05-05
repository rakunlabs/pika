# Pika Cluster Example

Set `/etc/hosts` to 3 replicas and we add replica count to 3 in the config so in the quorum, we can have 2 replicas alive at the same time.

```sh
127.0.1.1  pika.local
127.0.1.2  pika.local
127.0.1.3  pika.local
```

Then run on the project root with different terminals

```sh
CONFIG_FILE=example/cluster/1.yaml make run
CONFIG_FILE=example/cluster/2.yaml make run
CONFIG_FILE=example/cluster/3.yaml make run
```
