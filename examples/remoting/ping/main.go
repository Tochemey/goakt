package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	goakt "github.com/tochemey/goakt/actors"
	samplepb "github.com/tochemey/goakt/examples/protos/pb/v1"
	"github.com/tochemey/goakt/log"
	addresspb "github.com/tochemey/goakt/pb/address/v1"
	"go.uber.org/atomic"
)

const (
	port = 50051
	host = "localhost"
)

func main() {
	ctx := context.Background()

	// use the address default log. real-life implement the log interface`
	logger := log.New(log.DebugLevel, os.Stdout)

	// create the actor system. kindly in real-life application handle the error
	actorSystem, _ := goakt.NewActorSystem("SampleActorSystem",
		goakt.WithPassivationDisabled(), // set big passivation time
		goakt.WithLogger(logger),
		goakt.WithActorInitMaxRetries(3),
		goakt.WithRemoting(host, port))

	// start the actor system
	_ = actorSystem.Start(ctx)

	// create an actor
	pingActor := actorSystem.Spawn(ctx, "Ping", NewPingActor())

	// start the conversation
	timer := time.AfterFunc(time.Second, func() {
		_ = pingActor.RemoteTell(ctx, &addresspb.Address{
			Host: host,
			Port: 50052,
			Name: "Pong",
			Id:   "",
		}, new(samplepb.Ping))
	})
	defer timer.Stop()

	// capture ctrl+c
	interruptSignal := make(chan os.Signal, 1)
	signal.Notify(interruptSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interruptSignal

	// stop the actor system
	_ = actorSystem.Stop(ctx)
	os.Exit(0)
}

type PingActor struct {
	count  *atomic.Int32
	logger log.Logger
}

var _ goakt.Actor = (*PingActor)(nil)

func NewPingActor() *PingActor {
	return &PingActor{}
}

func (p *PingActor) PreStart(ctx context.Context) error {
	// set the log
	p.logger = log.DefaultLogger
	p.count = atomic.NewInt32(0)
	p.logger.Info("About to Start")
	return nil
}

func (p *PingActor) Receive(ctx goakt.ReceiveContext) {
	switch ctx.Message().(type) {
	case *samplepb.Pong:
		// reply the sender in case there is a sender
		if ctx.RemoteSender() != goakt.RemoteNoSender {
			p.logger.Infof("received remote from %s", ctx.RemoteSender().String())
			_ = ctx.Self().RemoteTell(context.Background(), ctx.RemoteSender(), new(samplepb.Ping))
		} else if ctx.Sender() != goakt.NoSender {
			p.logger.Infof("received Pong from %s", ctx.Sender().ActorPath().String())
			_ = ctx.Self().Tell(ctx.Context(), ctx.Sender(), new(samplepb.Ping))
		}
		p.count.Add(1)
	default:
		p.logger.Panic(goakt.ErrUnhandled)
	}
}

func (p *PingActor) PostStop(ctx context.Context) error {
	p.logger.Info("About to stop")
	p.logger.Infof("Processed=%d address", p.count.Load())
	return nil
}
