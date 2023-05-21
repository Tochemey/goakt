# Go-Akt
[![build](https://img.shields.io/github/actions/workflow/status/Tochemey/goakt/build.yml?branch=main)](https://github.com/Tochemey/goakt/actions/workflows/build.yml)
[![codecov](https://codecov.io/gh/Tochemey/goakt/branch/main/graph/badge.svg?token=J0p9MzwSRH)](https://codecov.io/gh/Tochemey/goakt)
[![Go Report Card](https://goreportcard.com/badge/github.com/tochemey/goakt)](https://goreportcard.com/report/github.com/tochemey/goakt)

Minimal actor framework with goodies to build reactive and distributed system in golang using protocol buffers as actor messages.

If you are not familiar with the actor model, the blog post from Brian Storti [here](https://www.brianstorti.com/the-actor-model/) is an excellent and short introduction to the actor model. 
Also, check reference section at the end of the post for more material regarding actor model

## Features

- [x] Send a synchronous message to an actor from a non actor system
- [x] Send an asynchronous(fire-and-forget) message to an actor from a non actor system
- [x] Actor to Actor communication (check the [examples'](./examples/actor-to-actor) folder)
- [x] Enable/Disable Passivation mode to remove/keep idle actors 
- [x] PreStart hook for an actor. 
- [x] PostStop hook for an actor for a graceful shutdown
- [x] ActorSystem 
- [x] Actor to Actor communication
- [x] Restart an actor 
- [x] (Un)Watch an actor
- [X] Stop and actor
- [x] Create a child actor
- [x] Supervisory Strategy (Restart and Stop directive) 
- [x] Behaviors (Become/BecomeStacked/UnBecome/UnBecomeStacked)
- [x] Logger interface with a default logger
- [x] Examples (check the [examples'](./examples) folder)
- [x] Integration with [OpenTelemetry](https://github.com/open-telemetry/opentelemetry-go) for traces and metrics.
- [x] Remoting
    - [x]  Actors can send messages to other actors on a remote system 
    - [x] Actors can look up other actors' address on a remote system
- [x] Clustering: The cluster engine is wholly running on the embed etcd server.
    - Enable the cluster with any number of nodes at least three to start with is recommended
    - Add node to an existing cluster one at a time
    - Node removal is supported, however care should be taken when removing a node.

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
