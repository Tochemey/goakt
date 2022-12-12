package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tochemey/goakt/_examples/sample/actors"
	samplepb "github.com/tochemey/goakt/_examples/sample/pinger/v1"
	goakt "github.com/tochemey/goakt/actors"
	"github.com/tochemey/goakt/log"
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
	actor := actorSystem.Spawn(ctx, kind, actors.NewPinger(id))

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
