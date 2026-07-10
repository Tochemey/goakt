// Package main reproduces github.com/Tochemey/goakt/issues/1252: the remoting
// server used to inherit cancelation from the Start context, so a bounded
// startup context expiring after startup poisoned every inbound handler
// context and all remote operations failed with "context deadline exceeded"
// until the process restarted. With the fix, this sample prints OK.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/internal/address"
	inet "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	goaktlog "github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

type echo struct{}

func (e *echo) PreStart(*actor.Context) error { return nil }
func (e *echo) PostStop(*actor.Context) error { return nil }
func (e *echo) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestReply:
		ctx.Response(new(testpb.Reply))
	default:
		ctx.Unhandled()
	}
}

func main() {
	port := inet.Get(1)[0]
	system, err := actor.NewActorSystem("issue1252",
		actor.WithLogger(goaktlog.DiscardLogger),
		actor.WithRemote(remote.NewConfig("127.0.0.1", port)),
	)
	if err != nil {
		log.Fatal(err)
	}

	// the common footgun: a bounded startup context (DI OnStart hook, startup timeout)
	startCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := system.Start(startCtx); err != nil {
		log.Fatal(err)
	}
	defer func() { _ = system.Stop(context.Background()) }()

	ctx := context.Background()
	if _, err := system.Spawn(ctx, "echo", &echo{}); err != nil {
		log.Fatal(err)
	}

	// let the startup budget expire; before the fix, remoting on this node is
	// permanently poisoned from this moment on
	time.Sleep(1500 * time.Millisecond)

	client := remoteclient.NewClient()
	defer client.Close()

	addr, err := client.RemoteLookup(ctx, system.Host(), int(system.Port()), "echo")
	if err != nil {
		log.Fatalf("REPRO (broken): remote lookup failed after start context expired: %v", err)
	}
	if _, err := client.RemoteAsk(ctx, address.NoSender(), addr, new(testpb.TestReply), 10*time.Second); err != nil {
		log.Fatalf("REPRO (broken): remote ask failed after start context expired: %v", err)
	}
	fmt.Println("OK: remoting survives an expired Start context")
}
