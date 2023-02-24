package actors

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/log"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	testpb "github.com/tochemey/goakt/test/data/pb/v1"
	"go.uber.org/goleak"
	"google.golang.org/protobuf/proto"
)

const (
	recvDelay      = 1 * time.Second
	recvTimeout    = 100 * time.Millisecond
	passivateAfter = 200 * time.Millisecond
)

func TestActorReceive(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/golang/glog.(*loggingT).flushDaemon"),
		goleak.IgnoreTopFunction("github.com/alphadose/zenq/v2.(*ZenQ[...]).selectSender"))
	ctx := context.TODO()

	// create the actor path
	actorPath := NewPath("Test", NewAddress(protocol, "sys", "host", 1))

	// create the actor ref
	pid := newPID(
		ctx,
		actorPath,
		NewTestActor(),
		withInitMaxRetries(1),
		withCustomLogger(log.DefaultLogger),
		withSendReplyTimeout(recvTimeout))

	assert.NotNil(t, pid)
	// let us send 10 messages to the actor
	count := 10
	for i := 0; i < count; i++ {
		recvContext := &receiveContext{
			ctx:            ctx,
			message:        new(testpb.TestSend),
			sender:         NoSender,
			recipient:      pid,
			mu:             sync.Mutex{},
			isAsyncMessage: true,
		}

		pid.doReceive(recvContext)
	}
	assert.EqualValues(t, count, pid.ReceivedCount(ctx))
	// stop the actor
	err := pid.Shutdown(ctx)
	assert.NoError(t, err)
}

func TestActorWithPassivation(t *testing.T) {
	defer goleak.VerifyNone(t,
		goleak.IgnoreTopFunction("github.com/golang/glog.(*loggingT).flushDaemon"),
		goleak.IgnoreTopFunction("github.com/alphadose/zenq/v2.(*ZenQ[...]).selectSender"))
	ctx := context.TODO()
	// create a Ping actor
	opts := []pidOption{
		withInitMaxRetries(1),
		withPassivationAfter(passivateAfter),
		withSendReplyTimeout(recvTimeout),
	}

	// create the actor path
	actorPath := NewPath("Test", NewAddress(protocol, "sys", "host", 1))
	pid := newPID(ctx, actorPath, NewTestActor(), opts...)
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
	err := SendAsync(ctx, pid, new(testpb.TestSend))
	assert.Error(t, err)
	assert.EqualError(t, err, ErrNotReady.Error())
}

func TestActorWithReply(t *testing.T) {
	t.Run("with happy path", func(t *testing.T) {
		defer goleak.VerifyNone(t,
			goleak.IgnoreTopFunction("github.com/golang/glog.(*loggingT).flushDaemon"),
			goleak.IgnoreTopFunction("github.com/alphadose/zenq/v2.(*ZenQ[...]).selectSender"))
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create the actor path
		actorPath := NewPath("Test", NewAddress(protocol, "sys", "host", 1))
		pid := newPID(ctx, actorPath, NewTestActor(), opts...)
		assert.NotNil(t, pid)

		actual, err := SendSync(ctx, pid, new(testpb.TestReply), recvTimeout)
		assert.NoError(t, err)
		assert.NotNil(t, actual)
		expected := &testpb.Reply{Content: "received message"}
		assert.True(t, proto.Equal(expected, actual))
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("with timeout", func(t *testing.T) {
		defer goleak.VerifyNone(t,
			goleak.IgnoreTopFunction("github.com/golang/glog.(*loggingT).flushDaemon"),
			goleak.IgnoreTopFunction("github.com/alphadose/zenq/v2.(*ZenQ[...]).selectSender"))
		ctx := context.TODO()
		// create a Ping actor
		opts := []pidOption{
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
		}

		// create the actor path
		actorPath := NewPath("Test", NewAddress(protocol, "sys", "host", 1))
		pid := newPID(ctx, actorPath, NewTestActor(), opts...)
		assert.NotNil(t, pid)

		actual, err := SendSync(ctx, pid, new(testpb.TestSend), recvTimeout)
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
		defer goleak.VerifyNone(t,
			goleak.IgnoreTopFunction("github.com/golang/glog.(*loggingT).flushDaemon"),
			goleak.IgnoreTopFunction("github.com/alphadose/zenq/v2.(*ZenQ[...]).selectSender"))
		ctx := context.TODO()
		cfg, err := NewConfig("testSys", "localhost:0")
		require.NoError(t, err)
		assert.NotNil(t, cfg)

		// create a Ping actor
		actor := NewTestActor()
		assert.NotNil(t, actor)

		// create the actor path
		actorPath := NewPath("Test", NewAddress(protocol, "sys", "host", 1))
		// create the actor ref
		pid := newPID(ctx, actorPath, actor,
			withInitMaxRetries(1),
			withPassivationAfter(10*time.Second),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(recvTimeout))
		assert.NotNil(t, pid)

		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)

		time.Sleep(time.Second)

		// let us send a message to the actor
		err = SendAsync(ctx, pid, new(testpb.TestSend))
		assert.Error(t, err)
		assert.EqualError(t, err, ErrNotReady.Error())

		// restart the actor
		err = pid.Restart(ctx)
		assert.NoError(t, err)
		assert.True(t, pid.IsOnline())
		// let us send 10 messages to the actor
		count := 10
		for i := 0; i < count; i++ {
			err = SendAsync(ctx, pid, new(testpb.TestSend))
			assert.NoError(t, err)
		}
		assert.EqualValues(t, count, pid.ReceivedCount(ctx))
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
	//t.Run("restart with error: case where shutdown is not fully completed", func(t *testing.T) {
	//	defer goleak.VerifyNone(t)
	//	ctx := context.TODO()
	//	// create the actor path
	//	actorPath := NewPath("Test", NewAddress(protocol, "sys", "host", 1))
	//
	//	// create a Ping actor
	//	pid := newPID(
	//		ctx,
	//		actorPath,
	//		NewTestActor(),
	//		withInitMaxRetries(1),
	//		withSendReplyTimeout(recvTimeout),
	//		withCustomLogger(log.DiscardLogger))
	//	assert.NotNil(t, pid)
	//
	//	// stop the actor
	//	err := pid.Shutdown(ctx)
	//	assert.NoError(t, err)
	//
	//	// restarting this actor
	//	err = pid.Restart(ctx)
	//	assert.EqualError(t, err, ErrUndefinedActor.Error())
	//})
	t.Run("restart an actor", func(t *testing.T) {
		defer goleak.VerifyNone(t,
			goleak.IgnoreTopFunction("github.com/golang/glog.(*loggingT).flushDaemon"),
			goleak.IgnoreTopFunction("github.com/alphadose/zenq/v2.(*ZenQ[...]).selectSender"))
		ctx := context.TODO()

		// create a Ping actor
		actor := NewTestActor()
		assert.NotNil(t, actor)
		// create the actor path
		actorPath := NewPath("Test", NewAddress(protocol, "sys", "host", 1))

		// create the actor ref
		pid := newPID(ctx, actorPath, actor,
			withInitMaxRetries(1),
			withPassivationAfter(passivateAfter),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(recvTimeout))
		assert.NotNil(t, pid)
		// let us send 10 messages to the actor
		count := 10
		for i := 0; i < count; i++ {
			err := SendAsync(ctx, pid, new(testpb.TestSend))
			assert.NoError(t, err)
		}
		assert.EqualValues(t, count, pid.ReceivedCount(ctx))

		// restart the actor
		err := pid.Restart(ctx)
		assert.NoError(t, err)
		assert.True(t, pid.IsOnline())
		// let us send 10 messages to the actor
		for i := 0; i < count; i++ {
			err = SendAsync(ctx, pid, new(testpb.TestSend))
			assert.NoError(t, err)
		}
		assert.EqualValues(t, count, pid.ReceivedCount(ctx))
		// stop the actor
		err = pid.Shutdown(ctx)
		assert.NoError(t, err)
	})
}

func TestChildActor(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		defer goleak.VerifyNone(t,
			goleak.IgnoreTopFunction("github.com/golang/glog.(*loggingT).flushDaemon"),
			goleak.IgnoreTopFunction("github.com/alphadose/zenq/v2.(*ZenQ[...]).selectSender"))

		// create a test context
		ctx := context.TODO()
		// create the actor path
		actorPath := NewPath("Parent", NewAddress(protocol, "sys", "host", 1))

		// create the parent actor
		parent := newPID(ctx, actorPath,
			NewParentActor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(recvTimeout))
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "Child", NewChildActor())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(ctx), 1)
		// let us send 10 messages to the actors
		count := 10
		for i := 0; i < count; i++ {
			assert.NoError(t, SendAsync(ctx, parent, new(testpb.TestSend)))
			assert.NoError(t, SendAsync(ctx, child, new(testpb.TestSend)))
		}
		assert.EqualValues(t, count, parent.ReceivedCount(ctx))
		assert.EqualValues(t, count, child.ReceivedCount(ctx))
		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("test child panic with stop as default strategy", func(t *testing.T) {
		defer goleak.VerifyNone(t,
			goleak.IgnoreTopFunction("github.com/golang/glog.(*loggingT).flushDaemon"),
			goleak.IgnoreTopFunction("github.com/alphadose/zenq/v2.(*ZenQ[...]).selectSender"))

		// create a test context
		ctx := context.TODO()
		// create the actor path
		actorPath := NewPath("Parent", NewAddress(protocol, "sys", "host", 1))

		// create the parent actor
		parent := newPID(ctx,
			actorPath,
			NewParentActor(),
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withPassivationDisabled(),
			withSendReplyTimeout(recvTimeout))
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "Child", NewChildActor())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(ctx), 1)
		// send a test panic message to the actor
		assert.NoError(t, SendAsync(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		time.Sleep(time.Second)

		// assert the actor state
		assert.False(t, child.IsOnline())
		assert.Len(t, parent.Children(ctx), 0)

		//stop the actor
		err = parent.Shutdown(ctx)
		assert.NoError(t, err)
	})
	t.Run("test child panic with restart as default strategy", func(t *testing.T) {
		defer goleak.VerifyNone(t,
			goleak.IgnoreTopFunction("github.com/golang/glog.(*loggingT).flushDaemon"),
			goleak.IgnoreTopFunction("github.com/alphadose/zenq/v2.(*ZenQ[...]).selectSender"))

		// create a test context
		ctx := context.TODO()

		logger := log.DiscardLogger
		// create the actor path
		actorPath := NewPath("Parent", NewAddress(protocol, "sys", "host", 1))
		// create the parent actor
		parent := newPID(ctx,
			actorPath,
			NewParentActor(),
			withInitMaxRetries(1),
			withCustomLogger(logger),
			withPassivationDisabled(),
			withSupervisorStrategy(pb.StrategyDirective_RESTART_DIRECTIVE),
			withSendReplyTimeout(recvTimeout))
		assert.NotNil(t, parent)

		// create the child actor
		child, err := parent.SpawnChild(ctx, "Child", NewChildActor())
		assert.NoError(t, err)
		assert.NotNil(t, child)

		assert.Len(t, parent.Children(ctx), 1)
		// send a test panic message to the actor
		assert.NoError(t, SendAsync(ctx, child, new(testpb.TestPanic)))

		// wait for the child to properly shutdown
		time.Sleep(time.Second)

		// assert the actor state
		assert.True(t, child.IsOnline())
		assert.Len(t, parent.Children(ctx), 1)

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
		// create the actor path
		actorPath := NewPath("Test", NewAddress(protocol, "sys", "host", 1))
		pid := newPID(ctx, actorPath, actor,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(recvTimeout))

		actor.Wg.Add(b.N)
		go func() {
			for i := 0; i < b.N; i++ {
				// send a message to the actor
				_ = SendAsync(ctx, pid, new(testpb.TestSend))
			}
		}()
		actor.Wg.Wait()
		_ = pid.Shutdown(ctx)
	})
	b.Run("receive:send only", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		// create the actor path
		actorPath := NewPath("Test", NewAddress(protocol, "sys", "host", 1))
		pid := newPID(ctx, actorPath, actor,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withSendReplyTimeout(recvTimeout))

		actor.Wg.Add(b.N)
		for i := 0; i < b.N; i++ {
			// send a message to the actor
			_ = SendAsync(ctx, pid, new(testpb.TestSend))
		}
		_ = pid.Shutdown(ctx)
	})
	b.Run("receive:multiple senders", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		// create the actor path
		actorPath := NewPath("Test", NewAddress(protocol, "sys", "host", 1))
		pid := newPID(ctx, actorPath, actor,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
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
				_ = SendAsync(ctx, pid, new(testpb.TestSend))
			}()
		}
		actor.Wg.Wait()
		_ = pid.Shutdown(ctx)
	})
	b.Run("receive:multiple senders times hundred", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor ref
		// create the actor path
		actorPath := NewPath("Test", NewAddress(protocol, "sys", "host", 1))
		pid := newPID(ctx, actorPath, actor,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
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
					_ = SendAsync(ctx, pid, new(testpb.TestSend))
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
		// create the actor path
		actorPath := NewPath("Test", NewAddress(protocol, "sys", "host", 1))
		pid := newPID(ctx, actorPath, actor,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withPassivationAfter(5*time.Second),
			withSendReplyTimeout(recvTimeout))

		actor.Wg.Add(b.N)
		go func() {
			for i := 0; i < b.N; i++ {
				// send a message to the actor
				_, _ = SendSync(ctx, pid, new(testpb.TestReply), recvTimeout)
			}
		}()
		actor.Wg.Wait()
		_ = pid.Shutdown(ctx)
	})
	b.Run("receive-reply: send only", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}

		// create the actor path
		actorPath := NewPath("Test", NewAddress(protocol, "sys", "host", 1))
		// create the actor ref
		pid := newPID(ctx, actorPath, actor,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
			withPassivationAfter(5*time.Second),
			withSendReplyTimeout(recvTimeout))

		actor.Wg.Add(b.N)
		for i := 0; i < b.N; i++ {
			// send a message to the actor
			_, _ = SendSync(ctx, pid, new(testpb.TestReply), recvTimeout)
		}
		_ = pid.Shutdown(ctx)
	})
	b.Run("receive-reply:multiple senders", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}
		// create the actor path
		actorPath := NewPath("Test", NewAddress(protocol, "sys", "host", 1))
		// create the actor ref
		pid := newPID(ctx, actorPath, actor,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
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
				_, _ = SendSync(ctx, pid, new(testpb.TestReply), recvTimeout)
			}()
		}
		actor.Wg.Wait()
		_ = pid.Shutdown(ctx)
	})
	b.Run("receive-reply:multiple senders times hundred", func(b *testing.B) {
		ctx := context.TODO()
		actor := &BenchActor{}
		// create the actor path
		actorPath := NewPath("Test", NewAddress(protocol, "sys", "host", 1))
		// create the actor ref
		pid := newPID(ctx, actorPath, actor,
			withInitMaxRetries(1),
			withCustomLogger(log.DiscardLogger),
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
					_, _ = SendSync(ctx, pid, new(testpb.TestReply), recvTimeout)
				}
			}()
		}
		actor.Wg.Wait()
		_ = pid.Shutdown(ctx)
	})
}
