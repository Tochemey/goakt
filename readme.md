# Go-Akt
[![build](https://img.shields.io/github/actions/workflow/status/Tochemey/goakt/build.yml?branch=main)](https://github.com/Tochemey/goakt/actions/workflows/build.yml)
[![codecov](https://codecov.io/gh/Tochemey/goakt/branch/main/graph/badge.svg?token=J0p9MzwSRH)](https://codecov.io/gh/Tochemey/goakt)

Minimal actor framework to build reactive and distributed system in golang using protocol buffers.

If you are not familiar with the actor model, the blog post from Brian Storti [here](https://www.brianstorti.com/the-actor-model/) is an excellent and short introduction to the actor model. 
Also, check reference section at the end of the post for more material regarding actor model

## Features

- [x] Send a synchronous message to an actor from a non actor system
- [x] Send an asynchronous(fire-and-forget) message to an actor from a non actor system
- [x] Actor to Actor communication (check the [examples](./examples/actor-to-actor) folder)
- [x] Enable/Disable Passivation mode to remove/keep idle actors 
- [x] PreStart hook for an actor. 
- [x] PostStop hook for an actor for a graceful shutdown
- [x] ActorSystem (WIP) 
- [x] Actor to Actor communication
- [x] Restart an actor 
- [x] (Un)Watch an actor
- [X] Stop and actor
- [x] Create a child actor
- [x] Sample example
- [x] Supervisory Strategy (Restart and Stop directive) 
- [x] Behaviors
- [x] Persistence (WIP)
- [ ] Metrics
- [ ] Clustering
- [ ] Rewrite the logger interface

## Installation
```bash
go get github.com/Tochemey/goakt
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

	samplepb "github.com/tochemey/goakt/_examples/protos/pb/v1"
	goakt "github.com/tochemey/goakt/actors"
	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
)

func main() {
	ctx := context.Background()

	// use the goakt default logger. real-life implement the logger interface`
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
	actor := actorSystem.Spawn(ctx, "Pinger", "123", NewPinger())

	startTime := time.Now()

	// send some messages to the actor
	count := 1_000_000
	for i := 0; i < count; i++ {
		_ = goakt.SendAsync(ctx, actor, new(samplepb.Ping))
	}

	// capture ctrl+c
	interruptSignal := make(chan os.Signal, 1)
	signal.Notify(interruptSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interruptSignal

	// log some stats
	log.DefaultLogger.Infof("Actor=%s has processed %d messages in %s", actor.Address(), actor.TotalProcessed(ctx), time.Since(startTime))

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
	// set the logger
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
	p.logger.Infof("Processed=%d messages", p.count.Load())
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
