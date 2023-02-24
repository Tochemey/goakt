package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	goakt "github.com/tochemey/goakt/actors"
	samplepb "github.com/tochemey/goakt/examples/protos/pb/v1"
	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
)

func main() {
	ctx := context.Background()

	// use the goakt default logger. real-life implement the logger interface`
	logger := log.DefaultLogger
	// create the actor system configuration. kindly in real-life application handle the error
	config, _ := goakt.NewConfig("SampleActorSystem", "127.0.0.1:0",
		goakt.WithPassivationDisabled(),
		goakt.WithLogger(logger),
		goakt.WithActorInitMaxRetries(3))

	// create the actor system. kindly in real-life application handle the error
	actorSystem, _ := goakt.NewActorSystem(config)

	// start the actor system
	_ = actorSystem.Start(ctx)

	// create an actor
	actor := actorSystem.StartActor(ctx, "Ping", NewPinger())

	startTime := time.Now()

	// send some messages to the actor
	count := 10_000
	for i := 0; i < count; i++ {
		_ = goakt.SendAsync(ctx, actor, new(samplepb.Ping))
	}

	// capture ctrl+c
	interruptSignal := make(chan os.Signal, 1)
	signal.Notify(interruptSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interruptSignal

	// log some stats
	logger.Infof("Actor=%s has processed %d messages in %s", actor.ActorPath().String(), actor.ReceivedCount(), time.Since(startTime))

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
