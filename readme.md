# Go-Akt

[![build](https://img.shields.io/github/actions/workflow/status/Tochemey/goakt/build.yml?branch=main)](https://github.com/Tochemey/goakt/actions/workflows/build.yml)
[![codecov](https://codecov.io/gh/Tochemey/goakt/branch/main/graph/badge.svg?token=J0p9MzwSRH)](https://codecov.io/gh/Tochemey/goakt)
[![Go Report Card](https://goreportcard.com/badge/github.com/tochemey/goakt)](https://goreportcard.com/report/github.com/tochemey/goakt)

Minimal [Go](https://go.dev/) actor framework with goodies to build reactive and distributed system in golang using
_**protocol buffers as actor messages**_.

GoAkt is highly scalable and available when running in cluster mode. It comes with the necessary features require to
build a distributed actor-based system without
sacrificing performance and reliability.

If you are not familiar with the actor model, the blog post from Brian
Storti [here](https://www.brianstorti.com/the-actor-model/) is an excellent and short introduction to the actor model.
Also, check reference section at the end of the post for more material regarding actor model.

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
- Clustering that offers simple scalability, partitioning (sharding), and re-balancing out-of-the-box.

## Installation

```bash
go get github.com/tochemey/goakt
```

## Actors

Actors in Go-Akt live within an actor system. They can be _long-lived_ actors or be _passivated_ after some period of
time
depending upon the configuration set during their creation. To create an actor one need to implement
the [`Actor`](./actors/actor.go) interface.
Actors in Go-Akt use _Google Protocol Buffers_ message as a mean of communication. The choice of protobuf is due to easy
serialization over wire
and strong schema definition.

In Go-Akt, actors have the following characteristics:

- Each actor has a process id [`PID`](./actors/pid.go). Via the process id any allowable action can be executed by the
  actor.
- Lifecycle via the following methods: [`PreStart`](./actors/actor.go), [`PostStop`](./actors/actor.go). It means they
  can live and die like any other process.
- They handle and respond to messages via the method [`Receive`](./actors/actor.go). While handling messages they can:
    - Create other (child) actors via their process id [`PID`](./actors/pid.go) `SpawnChild` method
    - Send messages to other actors via their process id [`PID`](./actors/pid.go) `Ask`, `RemoteAsk`(request/response
      fashion) and `Tell`, `RemoteTell`(fire-and-forget fashion) methods
    - Stop (child) actors via their process id [`PID`](./actors/pid.go)
    - Watch/Unwatch (child) actors via their process id [`PID`](./actors/pid.go) `Watch` and `UnWatch` methods
    - Supervise the failure behavior of (child) actors. The supervisory strategy to adopt is set during the creation of
      the actor system. (Restart and Stop directive are supported) at the moment
    - Remotely lookup for an actor on another node via their process id [`PID`](./actors/pid.go) `RemoteLookup`. This
      allows them to send messages remotely via `RemoteAsk` or `RemoteTell` methods
- They can adopt various form using the [behavior](./actors/behavior.go) feature
- Few metrics are also accessible:
    - Mailbox size at a given time. That information can be accessed via the process
      id  [`PID`](./actors/pid.go) `MailboxSize` method
    - Total number of messages handled at a given time. That information can be accessed via the process
      id  [`PID`](./actors/pid.go) `ReceivedCount` method
    - Total number of restart. This is accessible via the process id  [`PID`](./actors/pid.go) `RestartCount` method
    - Total number of panic attacks. This is accessible via the process id [`PID`](./actors/pid.go) `ErrorsCount` method

## ActorSystem

Without an actor system, it is not possible to create actors in Go-Akt. Only a single actor system is allowed to be
created per node when using Go-Akt.
To create an actor system one just need to use the [`NewActorSystem`](./actors/actor_system.go) method with the
various [options](./actors/option.go).

## Observability

The actor and actor-system metrics as well traces are accessible via the integration
with [OpenTelemetry](https://github.com/open-telemetry/opentelemetry-go).

## Clustering

The cluster engine depends upon the [discovery](./discovery/provider.go) mechanism to find other nodes in the cluster.
Under the hood, it leverages [Olric](https://github.com/buraksezer/olric)
to scale out and guarantee performant, reliable persistence, simple scalability, partitioning (sharding), and
re-balancing out-of-the-box.

At the moment the following providers are implemented:

* the [kubernetes](https://kubernetes.io/docs/home/) [api integration](./discovery/kubernetes) is provided and fully
  functional.

In addition, one needs to set the following environment variables irrespective of the discovery provider to help
identify the host node on which the cluster service is running:

* `POD_NAME`: the node name. For instance in kubernetes one can just get it from the `metadata.name`
* `POD_IP`: the node host address. For instance in kubernetes one can just get it from the `status.podIP`
* `GOSSIP_PORT`: the gossip protocol engine port.
* `CLUSTER_PORT`: the cluster port to help communicate with other GoAkt nodes in the cluster
* `REMOTING_PORT`: help remoting communication between actors

### Operations Guide

The following outlines the cluster mode operations which can help have a healthy GoAkt cluster:

* One can start a single node cluster or a multiple nodes cluster.
* One can add more nodes to the cluster which will automatically discover the cluster.
* One can remove nodes. However, to avoid losing data, one need to scale down the cluster to the minimum number of nodes
  which started the cluster.

### Kubernetes Discovery Provider setup

To get the kubernetes discovery working as expected, the following pod labels need to be set:

* `app.kubernetes.io/part-of`: set this label with the actor system name
* `app.kubernetes.io/component`: set this label with the application name
* `app.kubernetes.io/name`: set this label with the application name

In addition, each node _is required to have two different ports open_ with the following ports name for the discovery
engine to work as expected:

* `gossip-port`: help the gossip protocol engine.
* `cluster-port`: help the cluster engine to communicate with other GoAkt nodes in the cluster
* `remoting`: help for remoting messaging between actors

#### Role Based Access

You’ll also have to grant the Service Account that your pods run under access to list pods. The following configuration
can be used as a starting point.
It creates a Role, pod-reader, which grants access to query pod information. It then binds the default Service Account
to the Role by creating a RoleBinding.
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

A working example can be found [here](./examples/actor-cluster/k8s) with a
small [doc](./examples/actor-cluster/k8s/doc.md) showing how to run it.

## Examples

Kindly check out the [examples'](./examples) folder.

## Contribution

Contributions are welcome!
The project adheres to [Semantic Versioning](https://semver.org)
and [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/).
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
