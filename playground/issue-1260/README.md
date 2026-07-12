# Issue 1260: crash recovery on the default cluster configuration

Kubernetes (kind) reproduction for [issue #1260](https://github.com/Tochemey/goakt/issues/1260), modeled on the [k8s-v2 example](https://github.com/Tochemey/goakt-examples/tree/main/goakt-cluster/k8s-v2).

## Scenario

A 3-pod StatefulSet runs an actor system with the untouched default cluster configuration: no `WithReplicaCount`, no quorum settings. 60 relocatable workers are spread round-robin across the pods, then one pod is deleted with `--grace-period=0 --force` (kill -9 semantics: no graceful shutdown, no `PeerState` snapshot). The leader must reconstruct the dead pod's relocation set from the replicated cluster registry and respawn every worker on the survivors.

## Expected vs actual

Before the fix (default `replicaCount=1`): the registry partitions owned by the crashed pod died with it. Registry records are partitioned by actor name, so those partitions held a share of every pod's records. After the crash, `GET /verify` reported missing workers, with no error and no event: silent partial recovery.

After the fix (default `replicaCount=2`, olric v0.3.13 backup promotion): every registry partition has a backup that is promoted before the recovery scan runs, `GET /verify` reports all 60 workers found, and the leader publishes a `RelocationDerived` event listing exactly what it reconstructed.

The StatefulSet uses `podManagementPolicy: OrderedReady`, so pods start strictly one after the other. This also exercises the second issue-1260 guarantee: a lone node on the default configuration bootstraps by itself (through the initial-sync empty-partition escape) before any peer exists, the way rolling deployments bring clusters up in practice.

## Layout

| File | Purpose |
| --- | --- |
| `main.go` | Wiring: kubernetes discovery, default cluster config, actor system lifecycle |
| `worker.go` | The relocatable `Worker` actor |
| `recorder.go` | Event stream recorder for `NodeJoined`, `NodeLeft`, `RelocationDerived`, `RelocationFailed` |
| `server.go` | HTTP surface used by the demo to spawn, verify, and inspect events |
| `deploy/k8s.yaml` | RBAC, 3-replica StatefulSet (OrderedReady), headless service |
| `deploy/kind-config.yaml` | Single-node kind cluster |
| `scripts/demo.sh` | The self-validating crash sequence |
| `Dockerfile` | Builds the reproduction from the local working tree (vendored deps) |
| `Makefile` | kind lifecycle, image build and load, deploy, demo |

## HTTP surface

Every pod exposes the same endpoints on port 8080:

| Endpoint | Purpose |
| --- | --- |
| `POST /spawn?count=N` | Spawn N relocatable workers (`worker-0` .. `worker-N-1`) with round-robin placement |
| `GET /verify?count=N` | Check each worker against the cluster registry; returns `found`, `missing`, `complete` |
| `GET /events` | Dump the cluster and relocation events observed by this pod |
| `GET /health` | Readiness and liveness probe |
| `POST /crash` | Kill this pod with kill -9 semantics (`os.Exit`: no graceful shutdown, no snapshot) |

The crash endpoint exists because there is no reliable way to SIGKILL the process from outside: `kubectl delete pod --grace-period=0 --force` still delivers SIGTERM first (the app shuts down gracefully and replicates its `PeerState` snapshot, which bypasses the registry-derived path this issue is about), and SIGKILL sent from inside the container is discarded by the kernel because the app runs as PID 1. `os.Exit` runs no deferred functions and no signal handlers, so it is a faithful crash. The container then restarts in place with the same address, which is exactly how a container crash behaves in production Kubernetes.

## Prerequisites

- Docker
- [kind](https://kind.sigs.k8s.io)
- kubectl

## Run it

```bash
cd playground/issue-1260
make cluster-create   # one-time
make deploy           # builds the image from the local working tree
make demo
```

The demo is self-validating. It fails when any worker is missing after the crash, when no `RelocationDerived` event was published, or when relocation reported failures. On success it prints:

```
PASS: crash recovery on the default configuration is complete (60/60 workers recovered, RelocationDerived observed)
```

Tear down with `make cluster-delete`.
