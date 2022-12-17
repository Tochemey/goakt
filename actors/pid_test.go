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
)

const (
	recvDelay      = 1 * time.Second
	recvTimeout    = 100 * time.Millisecond
	passivateAfter = 200 * time.Millisecond
)

func TestActorReceive(t *testing.T) {
	defer goleak.VerifyNone(t)
	ctx := context.TODO()

	// create the actor ref
	pid := newPID(
		ctx,
		NewTestActor(),
		withInitMaxRetries(1),
		withSendReplyTimeout(recvTimeout),
		withLocalID("Test", "test-1"))
	assert.NotNil(t, pid)
	// let us send 10 messages to the actor
	count := 10
	for i := 0; i < count; i++ {
		recvContext := &receiveContext{
			ctx:            ctx,
			message:        new(actorsv1.TestSend),
			sender:         NoSender,
			recipient:      pid,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		pid.doReceive(recvContext)
	}
	assert.EqualValues(t, count, pid.TotalProcessed(ctx))
	// stop the actor
	err := pid.Shutdown(ctx)
	assert.NoError(t, err)
}

func TestActorWithPassivation(t *testing.T) {
	defer goleak.VerifyNone(t)
	ctx := context.TODO()
	// create a Ping actor
	opts := []pidOption{
		withInitMaxRetries(1),
		withPassivationAfter(passivateAfter),
		withSendReplyTimeout(recvTimeout),
		withLocalID("Test", "test-1"),
	}

	pid := newPID(ctx, NewTestActor(), opts...)
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
	// let us send a message to the actor
	err := SendAsync(ctx, pid, new(actorsv1.TestSend))
	assert.Error(t, err)
	assert.EqualError(t, err, ErrNotReady.Error())
}

func TestActorWithReply(t *testing.T) {
	t.Run("with happy path", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withLocalID("Test", "test-1"),
		}

		pid := newPID(ctx, NewTestActor(), opts...)
		assert.NotNil(t, pid)

		actual, err := SendSync(ctx, pid, new(actorsv1.TestReply), recvTimeout)
		assert.NoError(t, err)
		assert.NotNil(t, actual)
		expected := &actorsv1.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expected, actual))
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("with timeout", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withLocalID("Test", "test-1"),
		}

		pid := newPID(ctx, NewTestActor(), opts...)
		assert.NotNil(t, pid)

		actual, err := SendSync(ctx, pid, new(actorsv1.TestSend), recvTimeout)
		assert.Error(t, err)
		assert.EqualError(t, err, ErrRequestTimeout.Error())
		assert.Nil(t, actual)
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
		actor := NewTestActor()
		assert.NotNil(t, actor)

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
			withPassivationAfter(10*time.Second),
			withActorSystem(actorSys),
			withLocalID("Test", "test-1"),
			withSendReplyTimeout(recvTimeout))
		assert.NotNil(t, pid)

		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)

		time.Sleep(time.Second)

		// let us send a message to the actor
		err = SendAsync(ctx, pid, new(actorsv1.TestSend))
		assert.Error(t, err)
		assert.EqualError(t, err, ErrNotReady.Error())

		// restart the actor
		err = pid.Restart(ctx)
		assert.NoError(t, err)
		assert.True(t, pid.IsOnline())
		// let us send 10 messages to the actor
		count := 10
		for i := 0; i < count; i++ {
			err = SendAsync(ctx, pid, new(actorsv1.TestSend))
			assert.NoError(t, err)
		}
		assert.EqualValues(t, count, pid.TotalProcessed(ctx))
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("restart with error: case where shutdown is not fully completed", func(t *testing.T) {
		defer goleak.VerifyNone(t)
		ctx := context.TODO()
		// create a Ping actor
		pid := newPID(
			ctx,
			NewTestActor(),
			withInitMaxRetries(1),
			withSendReplyTimeout(recvTimeout),
			withLocalID("Test", "test-1"))
		assert.NotNil(t, pid)

		// stop the actor
		err := pid.Shutdown(ctx)
		assert.NoError(t, err)

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
		actor := NewTestActor()
		assert.NotNil(t, actor)

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
			withPassivationAfter(passivateAfter),
			withActorSystem(actorSys),
			withLocalID("Test", "test-1"),
			withSendReplyTimeout(recvTimeout))
		assert.NotNil(t, pid)
		// let us send 10 messages to the actor
		count := 10
		for i := 0; i < count; i++ {
			err = SendAsync(ctx, pid, new(actorsv1.TestSend))
			assert.NoError(t, err)
		}
		assert.EqualValues(t, count, pid.TotalProcessed(ctx))

		// restart the actor
		err = pid.Restart(ctx)
		assert.NoError(t, err)
		assert.True(t, pid.IsOnline())
		// let us send 10 messages to the actor
		for i := 0; i < count; i++ {
			err = SendAsync(ctx, pid, new(actorsv1.TestSend))
			assert.NoError(t, err)
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
		parent := newPID(ctx,
			NewParentActor(),
			withInitMaxRetries(1),
			withActorSystem(actorSys),
			withLocalID("Parent", "papa"),
			withSendReplyTimeout(recvTimeout))
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "Child", "johnny", NewChildActor())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		// let us send 10 messages to the actors
		count := 10
		for i := 0; i < count; i++ {
			assert.NoError(t, SendAsync(ctx, parent, new(actorsv1.TestSend)))
			assert.NoError(t, SendAsync(ctx, child, new(actorsv1.TestSend)))
		}
		assert.EqualValues(t, count, parent.TotalProcessed(ctx))
		assert.EqualValues(t, count, child.TotalProcessed(ctx))
		//stop the actor
		err = parent.Shutdown(ctx)
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
			withSendReplyTimeout(recvTimeout))

		actor.Wg.Add(b.N)
		go func() {
			for i := 0; i < b.N; i++ {
				// send a message to the actor
				_ = SendAsync(ctx, pid, new(actorsv1.TestSend))
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
			withSendReplyTimeout(recvTimeout))

		actor.Wg.Add(b.N)
		for i := 0; i < b.N; i++ {
			// send a message to the actor
			_ = SendAsync(ctx, pid, new(actorsv1.TestSend))
		}
		_ = pid.Shutdown(ctx)
	})
	b.Run("receive:multiple senders", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		pid := newPID(ctx, actor,
			withInitMaxRetries(1),
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
				_ = SendAsync(ctx, pid, new(actorsv1.TestSend))
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
					_ = SendAsync(ctx, pid, new(actorsv1.TestSend))
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
				_, _ = SendSync(ctx, pid, new(actorsv1.TestReply), recvTimeout)
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
			_, _ = SendSync(ctx, pid, new(actorsv1.TestReply), recvTimeout)
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
				_, _ = SendSync(ctx, pid, new(actorsv1.TestReply), recvTimeout)
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
					_, _ = SendSync(ctx, pid, new(actorsv1.TestReply), recvTimeout)
				}
			}()
		}
		actor.Wg.Wait()
		_ = pid.Shutdown(ctx)
	})
}
