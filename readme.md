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
- Clustering

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

## Example

### Send a fire-forget message to an actor from a non actor system

```go

package main

import (
  "context"
  "os"
  "os/signal"
  "sync"
  "syscall"
  "time"

  goakt "github.com/tochemey/messages/actors"
  samplepb "github.com/tochemey/messages/examples/protos/pb/v1"
  "github.com/tochemey/goakt/log"
  "go.uber.org/atomic"
)

func main() {
  ctx := context.Background()

  // use the messages default log. real-life implement the log interface`
  logger := log.DefaultLogger
  // create the actor system configuration. kindly in real-life application handle the error
  config, _ := goakt.NewConfig("SampleActorSystem", "127.0.0.1:0",
    goakt.WithExpireActorAfter(10*time.Second),
    goakt.WithLogger(logger),
    goakt.WithActorInitMaxRetries(3))

  // create the actor system. kindly in real-life application handle the error
  actorSystem, _ := goakt.NewActorSystem(config)

  // start the actor system
  _ = actorSystem.Start(ctx)

  // create an actor
  actor := actorSystem.StartActor(ctx, "Ping", NewPinger())

  startTime := time.Now()

  // send some public to the actor
  count := 100
  for i := 0; i < count; i++ {
    _ = goakt.SendAsync(ctx, actor, new(samplepb.Ping))
  }

  // capture ctrl+c
  interruptSignal := make(chan os.Signal, 1)
  signal.Notify(interruptSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
  <-interruptSignal

  // log some stats
  logger.Infof("Actor=%s has processed %d public in %s", actor.ActorPath().String(), actor.ReceivedCount(ctx), time.Since(startTime))

  // stop the actor system
  _ = actorSystem.Stop(ctx)
  os.Exit(0)
}

type Pinger struct {
  mu     sync.Mutex
  count  *atomic.Int32
  logger log.Logger
}

var _ goakt.Actor = (*Pinger)(nil)

func NewPinger() *Pinger {
  return &Pinger{
    mu: sync.Mutex{},
  }
}

func (p *Pinger) PreStart(ctx context.Context) error {
  // set the log
  p.mu.Lock()
  defer p.mu.Unlock()
  p.logger = log.DefaultLogger
  p.count = atomic.NewInt32(0)
  p.logger.Info("About to Start")
  return nil
}

func (p *Pinger) Receive(ctx goakt.ReceiveContext) {
  switch ctx.Message().(type) {
  case *samplepb.Ping:
    p.logger.Info("received Ping")
    p.count.Add(1)
  default:
    p.logger.Panic(goakt.ErrUnhandled)
  }
}

func (p *Pinger) PostStop(ctx context.Context) error {
  p.logger.Info("About to stop")
  p.logger.Infof("Processed=%d public", p.count.Load())
  return nil
}
```

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
