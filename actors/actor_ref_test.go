package actors

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

const (
	recvDelay      = 1 * time.Second
	recvTimeout    = 100 * time.Millisecond
	passivateAfter = 200 * time.Millisecond

	timeoutMessage     = "timeout"
	recvMessage        = "receive"
	recvReplyMessage   = "receive reply"
	panicAttackMessage = "panic"
)

func TestActorReceive(t *testing.T) {
	t.Run("receive:happy path", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		actorID := "ping-1"
		actor := NewTestActor(actorID)
		assert.NotNil(t, actor)

		// create the actor ref
		actorRef := NewActorRef(ctx, actor,
			WithInitMaxRetries(1),
			WithPassivationAfter(passivateAfter),
			WithSendReplyTimeout(recvTimeout))
		assert.NotNil(t, actorRef)
		// let us send 10 messages to the actor
		count := 10
		for i := 0; i < count; i++ {
			// send a message to the actor
			content := fmt.Sprintf("%s-%d", recvMessage, i)
			msg := &wrapperspb.StringValue{Value: content}
			err := actorRef.Send(ctx, msg)
			assert.NoError(t, err)
		}
		assert.EqualValues(t, count, actor.Count())
		// stop the actor
		actorRef.Shutdown(ctx)
	})
	t.Run("receive: unhappy path: actor not ready", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		actorID := "ping-1"
		actor := NewTestActor(actorID)
		assert.NotNil(t, actor)

		// create the actor ref
		actorRef := NewActorRef(ctx, actor,
			WithInitMaxRetries(1),
			WithPassivationAfter(passivateAfter),
			WithSendReplyTimeout(recvTimeout))
		assert.NotNil(t, actorRef)
		// stop the actor
		actorRef.Shutdown(ctx)
		// let us send message
		msg := &wrapperspb.StringValue{Value: recvMessage}
		err := actorRef.Send(ctx, msg)
		assert.Error(t, err)
		assert.EqualError(t, err, ErrNotReady.Error())
	})
	t.Run("receive: unhappy path:unhandled message", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		actorID := "ping-1"
		actor := NewTestActor(actorID)
		assert.NotNil(t, actor)

		// create the actor ref
		actorRef := NewActorRef(ctx, actor,
			WithInitMaxRetries(1),
			WithPassivationAfter(passivateAfter),
			WithSendReplyTimeout(recvTimeout))
		assert.NotNil(t, actorRef)

		// create the message
		err := actorRef.Send(ctx, &emptypb.Empty{})
		assert.Error(t, err)
		assert.EqualError(t, err, ErrUnhandled.Error())
		// stop the actor
		actorRef.Shutdown(ctx)
	})
	t.Run("receive-reply:happy path", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		actorID := "ping-1"
		actor := NewTestActor(actorID)
		assert.NotNil(t, actor)

		// create the actor ref
		actorRef := NewActorRef(ctx, actor,
			WithInitMaxRetries(1),
			WithPassivationAfter(passivateAfter),
			WithSendReplyTimeout(recvTimeout))
		assert.NotNil(t, actorRef)

		// send a message
		reply, err := actorRef.SendReply(ctx, &wrapperspb.StringValue{Value: recvReplyMessage})
		require.NoError(t, err)
		require.NotNil(t, reply)
		expectedMsg := &wrapperspb.StringValue{Value: fmt.Sprintf("received=%s", recvReplyMessage)}
		assert.True(t, proto.Equal(expectedMsg, reply))
		expectedCount := 1
		assert.EqualValues(t, expectedCount, actor.Count())
		// stop the actor
		actorRef.Shutdown(ctx)
	})
	t.Run("receive-reply:happy path:error return", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		actorID := "ping-1"
		actor := NewTestActor(actorID)
		assert.NotNil(t, actor)

		// create the actor ref
		actorRef := NewActorRef(ctx, actor,
			WithInitMaxRetries(1),
			WithPassivationAfter(passivateAfter),
			WithSendReplyTimeout(recvTimeout))
		assert.NotNil(t, actorRef)

		// send a message
		reply, err := actorRef.SendReply(ctx, &wrapperspb.StringValue{Value: ""})
		require.Error(t, err)
		require.Nil(t, reply)
		assert.EqualError(t, err, "error simulated")
		// stop the actor
		actorRef.Shutdown(ctx)
	})
	t.Run("receive-reply:unhappy path:timeout", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		actorID := "ping-1"
		actor := NewTestActor(actorID)
		assert.NotNil(t, actor)

		// create the actor ref
		actorRef := NewActorRef(ctx, actor,
			WithInitMaxRetries(1),
			WithPassivationAfter(passivateAfter),
			WithSendReplyTimeout(recvTimeout))
		assert.NotNil(t, actorRef)

		// send a message
		reply, err := actorRef.SendReply(ctx, &wrapperspb.StringValue{Value: timeoutMessage})
		assert.Error(t, err)
		assert.EqualError(t, err, ErrTimeout.Error())
		assert.Nil(t, reply)
		assert.EqualValues(t, 0, actor.Count())
		// stop the actor
		actorRef.Shutdown(ctx)
	})
	t.Run("receive-reply:unhappy path:unhandled message", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		actorID := "ping-1"
		actor := NewTestActor(actorID)
		assert.NotNil(t, actor)
		// create the actor ref
		actorRef := NewActorRef(ctx, actor,
			WithInitMaxRetries(1),
			WithPassivationAfter(passivateAfter),
			WithSendReplyTimeout(recvTimeout))
		assert.NotNil(t, actorRef)

		// send a message
		reply, err := actorRef.SendReply(ctx, &emptypb.Empty{})
		require.Error(t, err)
		require.Nil(t, reply)
		// stop the actor
		actorRef.Shutdown(ctx)
	})
	t.Run("passivation", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		actorID := "ping-1"
		actor := NewTestActor(actorID)
		assert.NotNil(t, actor)

		// create the actor ref
		actorRef := NewActorRef(ctx, actor,
			WithInitMaxRetries(1),
			WithPassivationAfter(passivateAfter),
			WithSendReplyTimeout(recvTimeout))
		assert.NotNil(t, actorRef)

		// let us sleep for some time to make the actor idle
		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			time.Sleep(recvDelay)
			wg.Done()
		}()
		// block until timer is up
		wg.Wait()

		// let us send any message
		err := actorRef.Send(ctx, &emptypb.Empty{})
		assert.Error(t, err)
		assert.EqualError(t, err, ErrNotReady.Error())
	})
	t.Run("receive:recover from panic", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		actorID := "ping-1"
		actor := NewTestActor(actorID)
		assert.NotNil(t, actor)

		// create the actor ref
		actorRef := NewActorRef(ctx, actor,
			WithInitMaxRetries(1),
			WithPassivationAfter(passivateAfter),
			WithSendReplyTimeout(recvTimeout))
		assert.NotNil(t, actorRef)

		// send a message
		err := actorRef.Send(ctx, &wrapperspb.StringValue{Value: panicAttackMessage})
		require.Error(t, err)
		assert.EqualError(t, err, "Boom")

		// stop the actor
		actorRef.Shutdown(ctx)
	})
	t.Run("receive-reply:recover from panic", func(t *testing.T) {
		ctx := context.TODO()
		// create a Ping actor
		actorID := "ping-1"
		actor := NewTestActor(actorID)
		assert.NotNil(t, actor)

		// create the actor ref
		actorRef := NewActorRef(ctx, actor,
			WithInitMaxRetries(1),
			WithPassivationAfter(passivateAfter),
			WithSendReplyTimeout(recvTimeout))
		assert.NotNil(t, actorRef)

		// send a message
		reply, err := actorRef.SendReply(ctx, &wrapperspb.StringValue{Value: panicAttackMessage})
		require.Error(t, err)
		require.Nil(t, reply)
		assert.EqualError(t, err, "Boom")

		// stop the actor
		actorRef.Shutdown(ctx)
	})
}

func BenchmarkActor(b *testing.B) {
	b.Run("receive:single sender", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		actorRef := NewActorRef(ctx, actor,
			WithInitMaxRetries(1),
			WithPassivationAfter(5*time.Second),
			WithSendReplyTimeout(recvTimeout))

		actor.Wg.Add(b.N)
		go func() {
			for i := 0; i < b.N; i++ {
				// send a message to the actor
				if err := actorRef.Send(ctx, &emptypb.Empty{}); err != nil {
					fmt.Println("fail to send message")
				}
			}
		}()
		actor.Wg.Wait()
		actorRef.Shutdown(ctx)
	})
	b.Run("receive:send only", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		actorRef := NewActorRef(ctx, actor,
			WithInitMaxRetries(1),
			WithPassivationAfter(5*time.Second),
			WithSendReplyTimeout(recvTimeout))

		actor.Wg.Add(b.N)
		for i := 0; i < b.N; i++ {
			// send a message to the actor
			if err := actorRef.Send(ctx, &emptypb.Empty{}); err != nil {
				fmt.Println("fail to send message")
			}
		}
		actorRef.Shutdown(ctx)
	})
	b.Run("receive:multiple senders", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		actorRef := NewActorRef(ctx, actor,
			WithInitMaxRetries(1),
			WithPassivationAfter(5*time.Second),
			WithSendReplyTimeout(recvTimeout))

		actor.Wg.Add(b.N)
		for i := 0; i < b.N; i++ {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						fmt.Println("Failed to send", r)
					}
				}()
				// send a message to the actor
				if err := actorRef.Send(ctx, &emptypb.Empty{}); err != nil {
					fmt.Println("fail to send message")
				}
			}()
		}
		actor.Wg.Wait()
		actorRef.Shutdown(ctx)
	})
	b.Run("receive:multiple senders times hundred", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		actorRef := NewActorRef(ctx, actor,
			WithInitMaxRetries(1),
			WithPassivationAfter(5*time.Second),
			WithSendReplyTimeout(recvTimeout))

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
					if err := actorRef.Send(ctx, &emptypb.Empty{}); err != nil {
						fmt.Println("fail to send message")
					}
				}
			}()
		}
		actor.Wg.Wait()
		actorRef.Shutdown(ctx)
	})
	b.Run("receive-reply: single sender", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		actorRef := NewActorRef(ctx, actor,
			WithInitMaxRetries(1),
			WithPassivationAfter(5*time.Second),
			WithSendReplyTimeout(recvTimeout))

		actor.Wg.Add(b.N)
		go func() {
			for i := 0; i < b.N; i++ {
				// send a message to the actor
				if _, err := actorRef.SendReply(ctx, &emptypb.Empty{}); err != nil {
					fmt.Println("fail to send message")
				}
			}
		}()
		actor.Wg.Wait()
		actorRef.Shutdown(ctx)
	})
	b.Run("receive-reply: send only", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		actorRef := NewActorRef(ctx, actor,
			WithInitMaxRetries(1),
			WithPassivationAfter(5*time.Second),
			WithSendReplyTimeout(recvTimeout))

		actor.Wg.Add(b.N)
		for i := 0; i < b.N; i++ {
			// send a message to the actor
			if _, err := actorRef.SendReply(ctx, &emptypb.Empty{}); err != nil {
				fmt.Println("fail to send message")
			}
		}
		actorRef.Shutdown(ctx)
	})
	b.Run("receive-reply:multiple senders", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		actorRef := NewActorRef(ctx, actor,
			WithInitMaxRetries(1),
			WithPassivationAfter(5*time.Second),
			WithSendReplyTimeout(recvTimeout))

		actor.Wg.Add(b.N)
		for i := 0; i < b.N; i++ {
			go func() {
				defer func() {
					if r := recover(); r != nil {
						fmt.Println("Failed to send", r)
					}
				}()
				// send a message to the actor
				if _, err := actorRef.SendReply(ctx, &emptypb.Empty{}); err != nil {
					fmt.Println("fail to send message")
				}
			}()
		}
		actor.Wg.Wait()
		actorRef.Shutdown(ctx)
	})
	b.Run("receive-reply:multiple senders times hundred", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		actorRef := NewActorRef(ctx, actor,
			WithInitMaxRetries(1),
			WithPassivationAfter(5*time.Second),
			WithSendReplyTimeout(recvTimeout))

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
					if _, err := actorRef.SendReply(ctx, &emptypb.Empty{}); err != nil {
						fmt.Println("fail to send message")
					}
				}
			}()
		}
		actor.Wg.Wait()
		actorRef.Shutdown(ctx)
	})
}
