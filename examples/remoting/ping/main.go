package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"google.golang.org/protobuf/proto"

	goakt "github.com/tochemey/goakt/actors"
	samplepb "github.com/tochemey/goakt/examples/protos/pb/v1"
	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
)

const (
	port = 9000
	host = "0.0.0.0"
)

func main() {
	ctx := context.Background()

	// use the goakt default logger. real-life implement the logger interface`
	logger := log.DefaultLogger

	// create the actor system configuration. kindly in real-life application handle the error
	config, _ := goakt.NewConfig("SampleActorSystem", fmt.Sprintf("%s:%d", host, port),
		goakt.WithPassivationDisabled(), // set big passivation time
		goakt.WithLogger(logger),
		goakt.WithActorInitMaxRetries(3),
		goakt.WithRemoting())

	// create the actor system. kindly in real-life application handle the error
	actorSystem, _ := goakt.NewActorSystem(config)

	// start the actor system
	_ = actorSystem.Start(ctx)

	// create an actor
	pingActor := actorSystem.StartActor(ctx, "Ping", NewPingActor())

	// start the conversation
	_ = pingActor.RemoteSendAsync(ctx, &pb.Address{
		Host: host,
		Port: 9001,
		Name: "Pong",
		Id:   "",
	}, new(samplepb.Ping))

	//// shutdown both actors after 3 seconds of conversation
	//timer := time.AfterFunc(3*time.Second, func() {
	//	log.DefaultLogger.Infof("PingActor=%s has processed %d messages", pingActor.ActorPath().String(), pingActor.ReceivedCount(ctx))
	//	_ = pingActor.Shutdown(ctx)
	//})
	//defer timer.Stop()

	// capture ctrl+c
	interruptSignal := make(chan os.Signal, 1)
	signal.Notify(interruptSignal, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interruptSignal

	// stop the actor system
	_ = actorSystem.Stop(ctx)
	os.Exit(0)
}

type PingActor struct {
	mu     sync.Mutex
	count  *atomic.Int32
	logger log.Logger
}

var _ goakt.Actor = (*PingActor)(nil)

func NewPingActor() *PingActor {
	return &PingActor{
		mu: sync.Mutex{},
	}
}

func (p *PingActor) PreStart(ctx context.Context) error {
	// set the logger
	p.mu.Lock()
	defer p.mu.Unlock()
	p.logger = log.DefaultLogger
	p.count = atomic.NewInt32(0)
	p.logger.Info("About to Start")
	return nil
}

func (p *PingActor) Receive(ctx goakt.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *samplepb.Pong:
		p.logger.Infof(fmt.Sprintf("received Pong from %s", ctx.Sender().ActorPath().String()))
		// reply the sender in case there is a sender
		if ctx.Sender() != goakt.NoSender {
			// let us reply to the sender
			_ = ctx.Self().SendAsync(ctx.Context(), ctx.Sender(), new(samplepb.Ping))
		}
		p.count.Add(1)
	case *pb.RemoteMessage:
		p.logger.Infof("received remote Pong from %s", msg.GetSender().String())
		pong := new(samplepb.Pong)
		_ = msg.GetMessage().UnmarshalTo(pong)
		if !proto.Equal(msg.GetSender(), goakt.RemoteNoSender) {
			_ = ctx.Self().RemoteSendAsync(context.Background(), msg.GetSender(), new(samplepb.Ping))
			p.count.Add(1)
		}
	default:
		p.logger.Panic(goakt.ErrUnhandled)
	}
}

func (p *PingActor) PostStop(ctx context.Context) error {
	p.logger.Info("About to stop")
	p.logger.Infof("Processed=%d messages", p.count.Load())
	return nil
}
