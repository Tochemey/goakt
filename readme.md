# Go-Akt
[![GitHub Workflow Status](https://img.shields.io/github/workflow/status/tochemey/goakt/build)](https://github.com/Tochemey/goakt/actions/workflows/build.yml)

Minimal actor framework to build reactive and distributed system in golang using protocol buffers.

If you are not familiar with the actor model, the blog post from Brian Storti [here](https://www.brianstorti.com/the-actor-model/) is an excellent and short introduction to the actor model. 
Also, check reference section at the end of the post for more material regarding actor model

## Features

- [x] Send a message to an actor
- [x] PreStart hook for an actor 
- [x] PostStop hook for an actor 
- [x] ActorSystem (WIP) 
- [x] Actor to Actor communication 
- [x] Restart an actor 
- [x] (Un)Watch an actor
- [X] Stop and actor
- [x] Create a child actor
- [x] Supervisory Strategy (WIP) 
- [ ] Metrics
- [ ] Clustering
- [x] Sample example
- [ ] Send message performance tweaking

## Sample

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	samplepb "github.com/tochemey/goakt/_examples/sample/pinger/v1"
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
	kind := "Pinger"
	id := "some-id"
	actor := actorSystem.Spawn(ctx, kind, NewPinger(id))

	startTime := time.Now()

	// send some messages to the actor
	count := 1_000
	for i := 0; i < count; i++ {
		content := &samplepb.Ping{Id: id}
		// construct a message with no sender
		message := goakt.NewMessage(ctx, content, goakt.WithSender(goakt.NoSender))
		// send the message. kindly in real-life application handle the error
		_ = actor.Send(message)
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
	id     string
	mu     sync.Mutex
	count  *atomic.Int32
	logger log.Logger
}

var _ goakt.Actor = (*Pinger)(nil)

func NewPinger(id string) *Pinger {
	return &Pinger{
		id: id,
		mu: sync.Mutex{},
	}
}

func (p *Pinger) ID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.id
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

func (p *Pinger) Receive(message goakt.Message) error {
	switch received := message.Payload().(type) {
	case *samplepb.Ping:
		p.logger.Infof(fmt.Sprintf("received Ping from %s", received.GetId()))
		// reply the sender in case there is a sender
		if message.Sender() != nil && message.Sender() != goakt.NoSender {
			return message.Sender().Send(
				goakt.NewMessage(message.Context(),
					&samplepb.Pong{Reply: fmt.Sprintf("received Ping from %s", received.GetId())}),
			)
		}
		p.count.Add(1)
		return nil
	default:
		return goakt.ErrUnhandled
	}
}

func (p *Pinger) PostStop(ctx context.Context) error {
	p.logger.Info("About to stop")
	p.logger.Infof("Processed=%d messages", p.count.Load())
	return nil
}
```
