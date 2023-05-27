# Go-Akt
[![build](https://img.shields.io/github/actions/workflow/status/Tochemey/goakt/build.yml?branch=main)](https://github.com/Tochemey/goakt/actions/workflows/build.yml)
[![codecov](https://codecov.io/gh/Tochemey/goakt/branch/main/graph/badge.svg?token=J0p9MzwSRH)](https://codecov.io/gh/Tochemey/goakt)
[![Go Report Card](https://goreportcard.com/badge/github.com/tochemey/goakt)](https://goreportcard.com/report/github.com/tochemey/goakt)

Minimal actor framework with goodies to build reactive and distributed system in golang using _**protocol buffers as actor messages**_.

If you are not familiar with the actor model, the blog post from Brian Storti [here](https://www.brianstorti.com/the-actor-model/) is an excellent and short introduction to the actor model. 
Also, check reference section at the end of the post for more material regarding actor model

## Features

- Send a synchronous message to an actor from a non actor system
- Send an asynchronous(fire-and-forget) message to an actor from a non actor system
- Actor to Actor communication (check the [examples'](./examples/actor-to-actor) folder)
- Enable/Disable Passivation mode to remove/keep idle actors 
- PreStart hook for an actor. 
- PostStop hook for an actor for a graceful shutdown
- ActorSystem: Actors live and die withing a system.
- Actor to Actor communication via Tell and Ask message patterns.
- Restart an actor 
- (Un)Watch an actor
- Stop and actor
- Create a child actor
- Supervisory Strategy (Restart and Stop directive are supported) 
- Behaviors (Become/BecomeStacked/UnBecome/UnBecomeStacked)
- Logger interface with a default logger
- Examples (check the [examples'](./examples) folder)
- Integration with [OpenTelemetry](https://github.com/open-telemetry/opentelemetry-go) for traces and metrics.
- Remoting
    - Actors can send messages to other actors on a remote system via Tell and Ask message patterns.
    - Actors can look up other actors' address on a remote system.
- Cluster Mode

## Cluster Mode

To run the system in a cluster mode, each node _is required to have two different ports open_ with the following name tags:
* `clients-port`: help the cluster client to communicate with the rest of cluster.
* `peers-port`: help the cluster engine to communicate with other nodes in the cluster

The rationale behind those ports is that the cluster engine is wholly built on the [embed etcd server](https://pkg.go.dev/github.com/coreos/etcd/embed).

### Operations Guide
The following outlines the cluster mode operations which can help have a healthy GoAkt cluster:
* One can start a single node cluster or a multiple nodes cluster.
* To add more nodes to the cluster, kindly add them one at a time.
* To remove nodes, kindly remove them one at a time. Remember to have a healthy cluster you will need at least three nodes running.

The cluster engine depends upon the [discovery](./discovery/iface.go) mechanism to find other nodes in the cluster. 
At the moment only the [kubernetes](https://kubernetes.io/docs/home/) [api integration](./discovery/kubernetes) is provided and fully functional.

### Kubernetes Discovery Provider setup

To get the kubernetes discovery working as expected, the following pod labels need to be set:
* `app.kubernetes.io/part-of`: set this label with the actor system name
* `app.kubernetes.io/component`: set this label with the application name
* `app.kubernetes.io/name`: set this label with the application name

#### Role Based Access
You’ll also have to grant the Service Account that your pods run under access to list pods. The following configuration can be used as a starting point. 
It creates a Role, pod-reader, which grants access to query pod information. It then binds the default Service Account to the Role by creating a RoleBinding. 
Adjust as necessary:
```
kind: Role
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: pod-reader
rules:
  - apiGroups: [""] # "" indicates the core API group
    resources: ["pods"]
    verbs: ["get", "watch", "list"]
---
kind: RoleBinding
apiVersion: rbac.authorization.k8s.io/v1
metadata:
  name: read-pods
subjects:
  # Uses the default service account. Consider creating a new one.
  - kind: ServiceAccount
    name: default
roleRef:
  kind: Role
  name: pod-reader
  apiGroup: rbac.authorization.k8s.io
```

### Sample Project
A working example can be found [here](./examples/actor-cluster/k8s) with a small [doc](./examples/actor-cluster/k8s/doc.md) showing how to run it.

## Installation
```bash
go get github.com/tochemey/goakt
```

## Examples

Kindly check out the [examples'](./examples) folder.


## Contribution
Contributions are welcome!
The project adheres to [Semantic Versioning](https://semver.org) and [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
This repo uses [Earthly](https://earthly.dev/get-earthly).

To contribute please:
- Fork the repository
- Create a feature branch
- Submit a [pull request](https://help.github.com/articles/using-pull-requests)

### Test & Linter
Prior to submitting a [pull request](https://help.github.com/articles/using-pull-requests), please run:
```bash
earthly +test
```
