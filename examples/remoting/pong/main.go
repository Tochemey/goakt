package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	goakt "github.com/tochemey/goakt/actors"
	samplepb "github.com/tochemey/goakt/examples/protos/pb/v1"
	"github.com/tochemey/goakt/log"
	pb "github.com/tochemey/goakt/messages/v1"
	"go.uber.org/atomic"
	"google.golang.org/protobuf/proto"
)

const (
	port = 9001
	host = "0.0.0.0"
)

func main() {
	ctx := context.Background()

	// use the messages default log. real-life implement the log interface`
	logger := log.DefaultLogger

	// create the actor system configuration. kindly in real-life application handle the error
	config, _ := goakt.NewConfig("SampleActorSystem",
		goakt.WithPassivationDisabled(), // set big passivation time
		goakt.WithLogger(logger),
		goakt.WithActorInitMaxRetries(3),
		goakt.WithRemoting(host, port))

	// create the actor system. kindly in real-life application handle the error
	actorSystem, _ := goakt.NewActorSystem(config)

	// start the actor system
	_ = actorSystem.Start(ctx)

	// create an actor
	actorSystem.StartActor(ctx, "Pong", NewPongActor())

	// capture ctrl+c
	interruptSignal := make(chan os.Signal, 1)
	signal.Notify(interruptSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interruptSignal

	// stop the actor system
	_ = actorSystem.Stop(ctx)
	os.Exit(0)
}

type PongActor struct {
	count  *atomic.Int32
	logger log.Logger
}

var _ goakt.Actor = (*PongActor)(nil)

func NewPongActor() *PongActor {
	return &PongActor{}
}

func (p *PongActor) PreStart(ctx context.Context) error {
	// set the log
	p.logger = log.DefaultLogger
	p.count = atomic.NewInt32(0)
	p.logger.Info("About to Start")
	return nil
}

func (p *PongActor) Receive(ctx goakt.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *samplepb.Ping:
		p.logger.Infof("received Ping from %s", ctx.Sender().ActorPath().String())
		// reply the sender in case there is a sender
		if ctx.Sender() != nil && ctx.Sender() != goakt.NoSender {
			_ = ctx.Self().Tell(ctx.Context(), ctx.Sender(), new(samplepb.Pong))
		}
		p.count.Add(1)
	case *pb.RemoteMessage:
		p.logger.Infof("received remote Ping from %s", msg.GetSender().String())
		ping := new(samplepb.Ping)
		_ = msg.GetMessage().UnmarshalTo(ping)
		if !proto.Equal(msg.GetSender(), goakt.RemoteNoSender) {
			_ = ctx.Self().RemoteTell(context.Background(), msg.GetSender(), new(samplepb.Pong))
			p.count.Add(1)
		}
	default:
		p.logger.Panic(goakt.ErrUnhandled)
	}
}

func (p *PongActor) PostStop(ctx context.Context) error {
	p.logger.Info("About to stop")
	p.logger.Infof("Processed=%d public", p.count.Load())
	return nil
}
