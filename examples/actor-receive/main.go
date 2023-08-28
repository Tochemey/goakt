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

	// use the address default log. real-life implement the log interface`
	logger := log.DefaultLogger

	// create the actor system. kindly in real-life application handle the error
	actorSystem, _ := goakt.NewActorSystem("SampleActorSystem",
		goakt.WithExpireActorAfter(10*time.Second),
		goakt.WithLogger(logger),
		goakt.WithActorInitMaxRetries(3))

	// start the actor system
	_ = actorSystem.Start(ctx)

	// create an actor
	actor := actorSystem.Spawn(ctx, "Ping", NewPinger())

	startTime := time.Now()

	// send some public to the actor
	count := 10000
	for i := 0; i < count; i++ {
		_ = goakt.Tell(ctx, actor, new(samplepb.Ping))
	}

	// capture ctrl+c
	interruptSignal := make(chan os.Signal, 1)
	signal.Notify(interruptSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interruptSignal

	// log some stats
	logger.Infof("Actor=%s has processed %d address in %d ms", actor.ActorPath().String(), actor.ReceivedCount(ctx), time.Since(startTime).Milliseconds())

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
