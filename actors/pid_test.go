package actors

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	actorsv1 "github.com/tochemey/goakt/actors/testdata/actors/v1"
	"go.uber.org/goleak"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

const (
	recvDelay      = 1 * time.Second
	recvTimeout    = 100 * time.Millisecond
	passivateAfter = 200 * time.Millisecond
)

func TestActorReceive(t *testing.T) {
	t.Run("receive:happy path", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		ctx := context.TODO()
		// create a Ping actor
		actorID := "ping-1"
		actor := NewTestActor(actorID)
		assert.NotNil(t, actor)

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
			withPassivationAfter(2*time.Second),
			withSendReplyTimeout(recvTimeout))
		assert.NotNil(t, pid)
		// let us send 10 messages to the actor
		count := 10
		for i := 0; i < count; i++ {
			_ = pid.Send(NewMessage(ctx, &actorsv1.TestSend{}))
		}
		assert.EqualValues(t, count, pid.TotalProcessed(ctx))
		// stop the actor
		err := pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("receive: unhappy path: actor not ready", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		ctx := context.TODO()
		// create a Ping actor
		actorID := "ping-1"
		actor := NewTestActor(actorID)
		assert.NotNil(t, actor)

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
			withPassivationAfter(passivateAfter),
			withSendReplyTimeout(recvTimeout))
		assert.NotNil(t, pid)
		// stop the actor
		err := pid.Shutdown(ctx)
		assert.NoError(t, err)
		// let us create the message
		message := NewMessage(ctx, &actorsv1.TestSend{})
		// let us send message
		err = pid.Send(message)
		assert.Error(t, err)
		assert.EqualError(t, err, ErrNotReady.Error())
	})
	t.Run("receive: unhappy path:unhandled message", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		ctx := context.TODO()
		// create a Ping actor
		actorID := "ping-1"
		actor := NewTestActor(actorID)
		assert.NotNil(t, actor)

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
			withPassivationAfter(passivateAfter),
			withSendReplyTimeout(recvTimeout))
		assert.NotNil(t, pid)

		// let us create the message
		message := NewMessage(ctx, &emptypb.Empty{})
		// let us send message
		err := pid.Send(message)
		assert.Error(t, err)
		assert.EqualError(t, err, ErrUnhandled.Error())
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("receive-reply:happy path", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		ctx := context.TODO()
		// create a Ping actor
		actorID := "ping-1"
		actor := NewTestActor(actorID)
		assert.NotNil(t, actor)

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
			withPassivationAfter(passivateAfter),
			withSendReplyTimeout(recvTimeout))
		assert.NotNil(t, pid)

		// let us create the message
		message := NewMessage(ctx, &actorsv1.TestReply{})
		// let us send message
		err := pid.Send(message)
		require.NoError(t, err)
		require.NotNil(t, message.Response())
		expected := &actorsv1.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expected, message.Response()))
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
	//t.Run("receive-reply:unhappy path:timeout", func(t *testing.T) {
	//	defer goleak.VerifyNone(t)
	//	ctx := context.TODO()
	//	// create a Ping actor
	//	actorID := "ping-1"
	//	actor := NewTestActor(actorID)
	//	assert.NotNil(t, actor)
	//
	//	// create the actor ref
	//	pid := newPID(ctx, actor,
	//		withInitMaxRetries(1),
	//		withPassivationAfter(passivateAfter),
	//		withSendReplyTimeout(recvTimeout))
	//	assert.NotNil(t, pid)
	//
	//	// let us create the message
	//	message := NewMessage(ctx, &actorsv1.TestTimeout{})
	//	// let us send message
	//	err := pid.Send(message)
	//	assert.Error(t, err)
	//	assert.EqualError(t, err, "context deadline exceeded")
	//	assert.Nil(t, message.Response())
	//	// stop the actor
	//	err = pid.Shutdown(ctx)
	//	assert.NoError(t, err)
	//})
	t.Run("passivation", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		ctx := context.TODO()
		// create a Ping actor
		actorID := "ping-1"
		actor := NewTestActor(actorID)
		assert.NotNil(t, actor)

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
			withPassivationAfter(passivateAfter),
			withSendReplyTimeout(recvTimeout))
		assert.NotNil(t, pid)

		// let us sleep for some time to make the actor idle
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			time.Sleep(recvDelay)
			wg.Done()
		}()
		// block until timer is up
		wg.Wait()

		// let us create the message
		message := NewMessage(ctx, &actorsv1.TestSend{})
		// let us send message
		err := pid.Send(message)
		assert.Error(t, err)
		assert.EqualError(t, err, ErrNotReady.Error())
	})
	t.Run("receive:recover from panic", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		ctx := context.TODO()
		// create a Ping actor
		actorID := "ping-1"
		actor := NewTestActor(actorID)
		assert.NotNil(t, actor)

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
			withPassivationAfter(passivateAfter),
			withSendReplyTimeout(recvTimeout))
		assert.NotNil(t, pid)

		// send a message
		// let us create the message
		message := NewMessage(ctx, &actorsv1.TestPanic{})
		// let us send message
		err := pid.Send(message)
		require.Error(t, err)
		assert.EqualError(t, err, "Boom")

		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
}

func TestActorRestart(t *testing.T) {
	t.Run("restart a stopped actor", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		ctx := context.TODO()
		cfg, err := NewConfig("testSys", "localhost:0")
		require.NoError(t, err)
		assert.NotNil(t, cfg)

		actorSys, err := NewActorSystem(cfg)
		require.NoError(t, err)

		// create a Ping actor
		actorID := "ping-1"
		actor := NewTestActor(actorID)
		assert.NotNil(t, actor)

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
			withPassivationAfter(passivateAfter),
			withActorSystem(actorSys),
			withKind("pinger"),
			withSendReplyTimeout(recvTimeout))
		assert.NotNil(t, pid)
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
		// let us create the message
		message := NewMessage(ctx, &actorsv1.TestSend{})
		// let us send message
		err = pid.Send(message)
		assert.Error(t, err)
		assert.EqualError(t, err, ErrNotReady.Error())

		// restart the actor
		err = pid.Restart(ctx)
		assert.NoError(t, err)
		assert.True(t, pid.IsOnline())
		// let us send 10 messages to the actor
		count := 10
		for i := 0; i < count; i++ {
			_ = pid.Send(NewMessage(ctx, &actorsv1.TestSend{}))
		}
		assert.EqualValues(t, count, pid.TotalProcessed(ctx))
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("restart with error", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		ctx := context.TODO()
		// create a Ping actor
		actorID := "ping-1"
		actor := NewTestActor(actorID)
		assert.NotNil(t, actor)

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
			withPassivationAfter(passivateAfter),
			withSendReplyTimeout(recvTimeout))
		assert.NotNil(t, pid)
		// stop the actor
		err := pid.Shutdown(ctx)
		assert.NoError(t, err)
		// let us create the message
		message := NewMessage(ctx, &actorsv1.TestSend{})
		// let us send message
		err = pid.Send(message)
		assert.Error(t, err)
		assert.EqualError(t, err, ErrNotReady.Error())

		// restarting this actor
		err = pid.Restart(ctx)
		assert.EqualError(t, err, ErrUndefinedActor.Error())
	})
	t.Run("restart an actor", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		ctx := context.TODO()
		cfg, err := NewConfig("testSys", "localhost:0")
		require.NoError(t, err)
		assert.NotNil(t, cfg)

		actorSys, err := NewActorSystem(cfg)
		require.NoError(t, err)

		// create a Ping actor
		actorID := "ping-1"
		actor := NewTestActor(actorID)
		assert.NotNil(t, actor)

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
			withPassivationAfter(passivateAfter),
			withActorSystem(actorSys),
			withKind("pinger"),
			withSendReplyTimeout(recvTimeout))
		assert.NotNil(t, pid)
		// let us send 10 messages to the actor
		count := 10
		for i := 0; i < count; i++ {
			_ = pid.Send(NewMessage(ctx, &actorsv1.TestSend{}))
		}
		assert.EqualValues(t, count, pid.TotalProcessed(ctx))

		// restart the actor
		err = pid.Restart(ctx)
		assert.NoError(t, err)
		assert.True(t, pid.IsOnline())
		// let us send 10 messages to the actor
		for i := 0; i < count; i++ {
			_ = pid.Send(NewMessage(ctx, &actorsv1.TestSend{}))
		}
		assert.EqualValues(t, count, pid.TotalProcessed(ctx))
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
}

func TestChildActor(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		defer goleak.VerifyNone(t)

		// create a test context
		ctx := context.TODO()
		// create a basic actor system
		cfg, err := NewConfig("testSys", "localhost:0")
		require.NoError(t, err)
		assert.NotNil(t, cfg)

		actorSys, err := NewActorSystem(cfg)
		require.NoError(t, err)

		// create the parent actor
		pid := newPID(ctx, NewParentActor("p1"),
			withInitMaxRetries(1),
			withPassivationAfter(2*time.Second),
			withActorSystem(actorSys),
			withKind("parent"),
			withSendReplyTimeout(recvTimeout))
		assert.NotNil(t, pid)

		// create the child actor
		cid, err := pid.SpawnChild(ctx, "child", NewChildActor("c1"))
		assert.NoError(t, err)
		assert.NotNil(t, cid)

		// let us send 10 messages to the actors
		count := 10
		for i := 0; i < count; i++ {
			err = pid.Send(NewMessage(ctx, &actorsv1.TestSend{}))
			assert.NoError(t, err)
			err = cid.Send(NewMessage(ctx, &actorsv1.TestSend{}))
			assert.NoError(t, err)
		}
		assert.EqualValues(t, count, pid.TotalProcessed(ctx))
		assert.EqualValues(t, count, cid.TotalProcessed(ctx))
		//stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
}

func BenchmarkActor(b *testing.B) {
	b.Run("receive:single sender", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
			withPassivationAfter(5*time.Second),
			withSendReplyTimeout(recvTimeout))

		actor.Wg.Add(b.N)
		go func() {
			for i := 0; i < b.N; i++ {
				// send a message to the actor
				if err := pid.Send(NewMessage(ctx, &actorsv1.TestSend{})); err != nil {
					fmt.Println("fail to send message")
				}
			}
		}()
		actor.Wg.Wait()
		_ = pid.Shutdown(ctx)
	})
	b.Run("receive:send only", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
			withPassivationAfter(5*time.Second),
			withSendReplyTimeout(recvTimeout))

		actor.Wg.Add(b.N)
		for i := 0; i < b.N; i++ {
			// send a message to the actor
			if err := pid.Send(NewMessage(ctx, &actorsv1.TestSend{})); err != nil {
				fmt.Println("fail to send message")
			}
		}
		_ = pid.Shutdown(ctx)
	})
	b.Run("receive:multiple senders", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
			withPassivationAfter(5*time.Second),
			withSendReplyTimeout(recvTimeout))

		actor.Wg.Add(b.N)
		for i := 0; i < b.N; i++ {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						fmt.Println("Failed to send", r)
					}
				}()
				// send a message to the actor
				if err := pid.Send(NewMessage(ctx, &actorsv1.TestSend{})); err != nil {
					fmt.Println("fail to send message")
				}
			}()
		}
		actor.Wg.Wait()
		_ = pid.Shutdown(ctx)
	})
	b.Run("receive:multiple senders times hundred", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
			withPassivationAfter(5*time.Second),
			withSendReplyTimeout(recvTimeout))

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
					if err := pid.Send(NewMessage(ctx, &actorsv1.TestSend{})); err != nil {
						fmt.Println("fail to send message")
					}
				}
			}()
		}
		actor.Wg.Wait()
		_ = pid.Shutdown(ctx)
	})
	b.Run("receive-reply: single sender", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
			withPassivationAfter(5*time.Second),
			withSendReplyTimeout(recvTimeout))

		actor.Wg.Add(b.N)
		go func() {
			for i := 0; i < b.N; i++ {
				// send a message to the actor
				if err := pid.Send(NewMessage(ctx, &actorsv1.TestReply{})); err != nil {
					fmt.Println("fail to send message")
				}
			}
		}()
		actor.Wg.Wait()
		_ = pid.Shutdown(ctx)
	})
	b.Run("receive-reply: send only", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
			withPassivationAfter(5*time.Second),
			withSendReplyTimeout(recvTimeout))

		actor.Wg.Add(b.N)
		for i := 0; i < b.N; i++ {
			// send a message to the actor
			if err := pid.Send(NewMessage(ctx, &actorsv1.TestReply{})); err != nil {
				fmt.Println("fail to send message")
			}
		}
		_ = pid.Shutdown(ctx)
	})
	b.Run("receive-reply:multiple senders", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
			withPassivationAfter(5*time.Second),
			withSendReplyTimeout(recvTimeout))

		actor.Wg.Add(b.N)
		for i := 0; i < b.N; i++ {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						fmt.Println("Failed to send", r)
					}
				}()
				// send a message to the actor
				if err := pid.Send(NewMessage(ctx, &actorsv1.TestReply{})); err != nil {
					fmt.Println("fail to send message")
				}
			}()
		}
		actor.Wg.Wait()
		_ = pid.Shutdown(ctx)
	})
	b.Run("receive-reply:multiple senders times hundred", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
			withPassivationAfter(5*time.Second),
			withSendReplyTimeout(recvTimeout))

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
					if err := pid.Send(NewMessage(ctx, &actorsv1.TestReply{})); err != nil {
						fmt.Println("fail to send message")
					}
				}
			}()
		}
		actor.Wg.Wait()
		_ = pid.Shutdown(ctx)
	})
}
