package bench

import (
	"context"
	"fmt"
	"sync"
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
	Wg sync.WaitGroup
}

func (p *Benchmarker) PreStart(context.Context) error {
	return nil
}

func (p *Benchmarker) Receive(ctx actors.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testspb.TestSend:
		p.Wg.Done()
	case *testspb.TestReply:
		ctx.Response(&testspb.Reply{Content: "received message"})
		p.Wg.Done()
	}
}

func (p *Benchmarker) PostStop(context.Context) error {
	return nil
}

func BenchmarkActor(b *testing.B) {
	b.Run("receive:single sender", func(b *testing.B) {
		ctx := context.TODO()

		// create the actor system
		actorSystem, _ := actors.NewActorSystem("testSys",
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(1),
			actors.WithReplyTimeout(receivingTimeout))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// define the benchmark actor
		actor := &Benchmarker{}

		// create the actor ref
		pid := actorSystem.Spawn(ctx, "test", actor)

		b.ResetTimer() // Reset the benchmark timer
		actor.Wg.Add(b.N)
		go func() {
			for i := 0; i < b.N; i++ {
				// send a message to the actor
				_ = actors.Tell(ctx, pid, &testspb.TestSend{})
			}
		}()
		actor.Wg.Wait()
		_ = pid.Shutdown(ctx)
		_ = actorSystem.Stop(ctx)
	})
	b.Run("receive:send only", func(b *testing.B) {
		ctx := context.TODO()
		// create the actor system
		actorSystem, _ := actors.NewActorSystem("testSys",
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(1),
			actors.WithReplyTimeout(receivingTimeout))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// define the benchmark actor
		actor := &Benchmarker{}

		// create the actor ref
		pid := actorSystem.Spawn(ctx, "test", actor)

		b.ResetTimer() // Reset the benchmark timer

		actor.Wg.Add(b.N)
		for i := 0; i < b.N; i++ {
			// send a message to the actor
			_ = actors.Tell(ctx, pid, new(testspb.TestSend))
		}
		_ = pid.Shutdown(ctx)
		_ = actorSystem.Stop(ctx)
	})
	b.Run("receive:multiple senders", func(b *testing.B) {
		ctx := context.TODO()
		// create the actor system
		actorSystem, _ := actors.NewActorSystem("testSys",
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(1),
			actors.WithReplyTimeout(receivingTimeout))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// define the benchmark actor
		actor := &Benchmarker{}

		// create the actor ref
		pid := actorSystem.Spawn(ctx, "test", actor)

		b.ResetTimer() // Reset the benchmark timer

		actor.Wg.Add(b.N)
		for i := 0; i < b.N; i++ {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						fmt.Println("Failed to send", r)
					}
				}()
				// send a message to the actor
				_ = actors.Tell(ctx, pid, new(testspb.TestSend))
			}()
		}
		actor.Wg.Wait()
		_ = pid.Shutdown(ctx)
		_ = actorSystem.Stop(ctx)
	})
	b.Run("receive:multiple senders times hundred", func(b *testing.B) {
		ctx := context.TODO()
		// create the actor system
		actorSystem, _ := actors.NewActorSystem("testSys",
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(1),
			actors.WithReplyTimeout(receivingTimeout))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// define the benchmark actor
		actor := &Benchmarker{}

		// create the actor ref
		pid := actorSystem.Spawn(ctx, "test", actor)

		b.ResetTimer() // Reset the benchmark timer

		actor.Wg.Add(b.N * 100)
		for i := 0; i < b.N; i++ {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						fmt.Println("Failed to send", r)
					}
				}()
				for i := 0; i < 100; i++ {
					// send a message to the actor
					_ = actors.Tell(ctx, pid, new(testspb.TestSend))
				}
			}()
		}
		actor.Wg.Wait()
		_ = pid.Shutdown(ctx)
		_ = actorSystem.Stop(ctx)
	})
	b.Run("receive-reply: single sender", func(b *testing.B) {
		ctx := context.TODO()
		// create the actor system
		actorSystem, _ := actors.NewActorSystem("testSys",
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(1),
			actors.WithExpireActorAfter(5*time.Second),
			actors.WithReplyTimeout(receivingTimeout))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// define the benchmark actor
		actor := &Benchmarker{}

		// create the actor ref
		pid := actorSystem.Spawn(ctx, "test", actor)

		b.ResetTimer() // Reset the benchmark timer

		actor.Wg.Add(b.N)
		go func() {
			for i := 0; i < b.N; i++ {
				// send a message to the actor
				_, _ = actors.Ask(ctx, pid, new(testspb.TestReply), receivingTimeout)
			}
		}()
		actor.Wg.Wait()
		_ = pid.Shutdown(ctx)
		_ = actorSystem.Stop(ctx)
	})
	b.Run("receive-reply: send only", func(b *testing.B) {
		ctx := context.TODO()
		// create the actor system
		actorSystem, _ := actors.NewActorSystem("testSys",
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(1),
			actors.WithExpireActorAfter(5*time.Second),
			actors.WithReplyTimeout(receivingTimeout))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// define the benchmark actor
		actor := &Benchmarker{}

		// create the actor ref
		pid := actorSystem.Spawn(ctx, "test", actor)

		b.ResetTimer() // Reset the benchmark timer

		actor.Wg.Add(b.N)
		for i := 0; i < b.N; i++ {
			// send a message to the actor
			_, _ = actors.Ask(ctx, pid, new(testspb.TestReply), receivingTimeout)
		}
		_ = pid.Shutdown(ctx)
		_ = actorSystem.Stop(ctx)
	})
	b.Run("receive-reply:multiple senders", func(b *testing.B) {
		ctx := context.TODO()
		// create the actor system
		actorSystem, _ := actors.NewActorSystem("testSys",
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(1),
			actors.WithExpireActorAfter(5*time.Second),
			actors.WithReplyTimeout(receivingTimeout))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// define the benchmark actor
		actor := &Benchmarker{}

		// create the actor ref
		pid := actorSystem.Spawn(ctx, "test", actor)

		b.ResetTimer() // Reset the benchmark timer

		actor.Wg.Add(b.N)
		for i := 0; i < b.N; i++ {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						fmt.Println("Failed to send", r)
					}
				}()
				// send a message to the actor
				_, _ = actors.Ask(ctx, pid, new(testspb.TestReply), receivingTimeout)
			}()
		}
		actor.Wg.Wait()
		_ = pid.Shutdown(ctx)
		_ = actorSystem.Stop(ctx)
	})
	b.Run("receive-reply:multiple senders times hundred", func(b *testing.B) {
		ctx := context.TODO()
		// create the actor system
		actorSystem, _ := actors.NewActorSystem("testSys",
			actors.WithLogger(log.DiscardLogger),
			actors.WithActorInitMaxRetries(1),
			actors.WithExpireActorAfter(5*time.Second),
			actors.WithReplyTimeout(receivingTimeout))

		// start the actor system
		_ = actorSystem.Start(ctx)

		// define the benchmark actor
		actor := &Benchmarker{}

		// create the actor ref
		pid := actorSystem.Spawn(ctx, "test", actor)

		b.ResetTimer() // Reset the benchmark timer

		actor.Wg.Add(b.N * 100)
		for i := 0; i < b.N; i++ {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						fmt.Println("Failed to send", r)
					}
				}()
				for i := 0; i < 100; i++ {
					// send a message to the actor
					_, _ = actors.Ask(ctx, pid, new(testspb.TestReply), receivingTimeout)
				}
			}()
		}
		actor.Wg.Wait()
		_ = pid.Shutdown(ctx)
		_ = actorSystem.Stop(ctx)
	})
}
