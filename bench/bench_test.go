package bench

import (
	"context"
	"testing"
	"time"

	"github.com/tochemey/goakt/actors"
	"github.com/tochemey/goakt/log"
	testspb "github.com/tochemey/goakt/test/data/pb/v1"
)

const (
	receivingTimeout = 100 * time.Millisecond
)

// Benchmarker is an actor that helps run benchmark tests
type Benchmarker struct {
}

func (p *Benchmarker) PreStart(context.Context) error {
	return nil
}

func (p *Benchmarker) Receive(ctx actors.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestSend:
	case *testspb.TestReply:
		ctx.Response(&testspb.Reply{Content: "received message"})
	}
}

func (p *Benchmarker) PostStop(context.Context) error {
	return nil
}

func BenchmarkActor(b *testing.B) {
	b.Run("tell(send only)", func(b *testing.B) {
		ctx := context.TODO()

		// create the actor system
		actorSystem, _ := actors.NewActorSystem("testSys",
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(1),
			actors.WithMailboxSize(uint64(b.N)),
			actors.WithReplyTimeout(receivingTimeout))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// define the benchmark actor
		actor := &Benchmarker{}

		// create the actor ref
		pid := actorSystem.Spawn(ctx, "test", actor)

		b.ResetTimer() // Reset the benchmark timer
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				// send a message to the actor
				_ = actors.Tell(ctx, pid, &testspb.TestSend{})
			}
		})

		_ = pid.Shutdown(ctx)
		_ = actorSystem.Stop(ctx)
	})
	b.Run("ask(send/reply)", func(b *testing.B) {
		ctx := context.TODO()
		// create the actor system
		actorSystem, _ := actors.NewActorSystem("testSys",
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(1),
			actors.WithMailboxSize(uint64(b.N)),
			actors.WithExpireActorAfter(5*time.Second),
			actors.WithReplyTimeout(receivingTimeout))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// define the benchmark actor
		actor := &Benchmarker{}

		// create the actor ref
		pid := actorSystem.Spawn(ctx, "test", actor)

		b.ResetTimer() // Reset the benchmark timer
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				// send a message to the actor
				_, _ = actors.Ask(ctx, pid, new(testspb.TestReply), receivingTimeout)
			}
		})
		_ = pid.Shutdown(ctx)
		_ = actorSystem.Stop(ctx)
	})
}
