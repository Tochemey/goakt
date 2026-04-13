// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package actor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/travisjeffery/go-dynaport"

	"github.com/tochemey/goakt/v4/eventstream"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestDeadletter(t *testing.T) {
	t.Run("With GetDeadlettersCount", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// wait for complete start
		pause.For(time.Second)

		// create a deadletter subscriber
		consumer, err := sys.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create the black hole actor
		actor := &MockUnhandled{}
		actorRef, err := sys.Spawn(ctx, "actor", actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// wait a while
		pause.For(time.Second)

		// every message sent to the actor will result in deadletter
		for range 5 {
			require.NoError(t, Tell(ctx, actorRef, new(testpb.TestSend)))
		}

		pause.For(time.Second)

		var items []*Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			// only listening to deadletter
			deadletter, ok := payload.(*Deadletter)
			if ok {
				items = append(items, deadletter)
			}
		}

		require.Len(t, items, 5)
		reply, err := Ask(ctx, sys.getDeadletter(), &commands.DeadlettersCountRequest{}, 500*time.Millisecond)
		require.NoError(t, err)
		require.NotNil(t, reply)
		response, ok := reply.(*commands.DeadlettersCountResponse)
		require.True(t, ok)
		require.EqualValues(t, 5, response.TotalCount)

		// unsubscribe the consumer
		err = sys.Unsubscribe(consumer)
		require.NoError(t, err)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With GetDeadlettersCount for a specific actor", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// wait for complete start
		pause.For(time.Second)

		// create the black hole actor
		actor := &MockUnhandled{}
		actorName := "actorName"
		actorRef, err := sys.Spawn(ctx, actorName, actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// wait a while
		pause.For(time.Second)

		// every message sent to the actor will result in deadletter
		for range 5 {
			require.NoError(t, Tell(ctx, actorRef, new(testpb.TestSend)))
		}

		pause.For(time.Second)

		address := actorRef.address
		reply, err := Ask(ctx, sys.getDeadletter(), &commands.DeadlettersCountRequest{
			Address: address,
		}, 500*time.Millisecond)
		require.NoError(t, err)
		require.NotNil(t, reply)
		response, ok := reply.(*commands.DeadlettersCountResponse)
		require.True(t, ok)
		require.EqualValues(t, 5, response.TotalCount)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
	t.Run("With GetDeadletters", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		// start the actor system
		err := sys.Start(ctx)
		assert.NoError(t, err)

		// wait for complete start
		pause.For(time.Second)

		// create a deadletter subscriber
		consumer, err := sys.Subscribe()
		require.NoError(t, err)
		require.NotNil(t, consumer)

		// create the black hole actor
		actor := &MockUnhandled{}
		actorRef, err := sys.Spawn(ctx, "actor", actor)
		assert.NoError(t, err)
		assert.NotNil(t, actorRef)

		// wait a while
		pause.For(time.Second)

		// every message sent to the actor will result in deadletter
		for range 5 {
			require.NoError(t, Tell(ctx, actorRef, new(testpb.TestSend)))
		}

		pause.For(time.Second)

		var items []*Deadletter
		for message := range consumer.Iterator() {
			payload := message.Payload()
			// only listening to deadletter
			deadletter, ok := payload.(*Deadletter)
			if ok {
				items = append(items, deadletter)
			}
		}

		require.Len(t, items, 5)
		// let us empty the item list
		items = []*Deadletter{}

		err = Tell(ctx, sys.getDeadletter(), &commands.PublishDeadletters{})
		require.NoError(t, err)

		pause.For(time.Second)

		for message := range consumer.Iterator() {
			payload := message.Payload()
			// only listening to deadletter
			deadletter, ok := payload.(*Deadletter)
			if ok {
				items = append(items, deadletter)
			}
		}
		require.Len(t, items, 1)

		// unsubscribe the consumer
		err = sys.Unsubscribe(consumer)
		require.NoError(t, err)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

// countingTestActor handles testpb.TestSend by incrementing an internal
// counter. Used by remote-tell dead-letter tests to verify that valid
// sibling messages in a batch are still delivered when other siblings fail.
type countingTestActor struct {
	mu      sync.Mutex
	count   int
	lastCtx context.Context
}

func (*countingTestActor) PreStart(*Context) error { return nil }
func (*countingTestActor) PostStop(*Context) error { return nil }
func (a *countingTestActor) Count() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.count
}

func (a *countingTestActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
		a.mu.Lock()
		a.count++
		a.lastCtx = ctx.Context()
		a.mu.Unlock()
	default:
	}
}

// deadlettersFor collects dead-letter payloads currently queued on a stream
// consumer and returns only those whose Receiver.Name matches the given
// name. The consumer's Iterator yields the full event-stream replay, so
// filtering lets a test focus on the failure it triggered.
func deadlettersFor(consumer eventstream.Subscriber, receiverName string) []*Deadletter {
	var out []*Deadletter
	for message := range consumer.Iterator() {
		dl, ok := message.Payload().(*Deadletter)
		if !ok {
			continue
		}
		if dl.Receiver().Name() == receiverName {
			out = append(out, dl)
		}
	}
	return out
}

// TestRemoteTellHandlerPerMessageDeadlettering asserts that per-message
// failures inside a coalesced RemoteTellRequest are dead-lettered locally
// on the server while healthy siblings are still delivered. The batch
// mixes (a) two deliverable messages to different actors, (b) a message
// to a non-existent actor, and (c) a nil entry. Post-run we verify:
//   - the handler returned RemoteTellResponse (whole-batch success)
//   - both live actors received their messages
//   - exactly one dead-letter was published, for the missing receiver
//   - sender/receiver/reason on the dead-letter match the failed message
func TestRemoteTellHandlerPerMessageDeadlettering(t *testing.T) {
	ctx := context.Background()
	host := "127.0.0.1"
	port := dynaport.Get(1)[0]

	sys, err := NewActorSystem(
		"testSys",
		WithLogger(log.DiscardLogger),
		WithRemote(remote.NewConfig(host, port)),
	)
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	pause.For(500 * time.Millisecond)
	t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })

	consumer, err := sys.Subscribe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sys.Unsubscribe(consumer) })

	alice := &countingTestActor{}
	bob := &countingTestActor{}
	_, err = sys.Spawn(ctx, "alice", alice)
	require.NoError(t, err)
	_, err = sys.Spawn(ctx, "bob", bob)
	require.NoError(t, err)

	payload, err := sys.(*actorSystem).remoting.Serializer(new(testpb.TestSend)).Serialize(new(testpb.TestSend))
	require.NoError(t, err)

	senderAddr := address.New("client", "remote", host, port).String()
	req := &internalpb.RemoteTellRequest{
		RemoteMessages: []*internalpb.RemoteMessage{
			{Sender: senderAddr, Receiver: address.New("alice", sys.Name(), host, port).String(), Message: payload},
			{Sender: senderAddr, Receiver: address.New("missing", sys.Name(), host, port).String(), Message: payload},
			{Sender: senderAddr, Receiver: address.New("bob", sys.Name(), host, port).String(), Message: payload},
			nil,
		},
	}
	resp, err := sys.(*actorSystem).remoteTellHandler(ctx, nil, req)
	require.NoError(t, err)
	_, ok := resp.(*internalpb.RemoteTellResponse)
	require.True(t, ok, "per-message failures must not fail the batch")

	require.Eventually(t, func() bool { return alice.Count() == 1 && bob.Count() == 1 }, time.Second, 10*time.Millisecond)
	pause.For(500 * time.Millisecond)

	missing := deadlettersFor(consumer, "missing")
	require.Len(t, missing, 1, "exactly one dead-letter for the unknown-receiver message")
	assert.Equal(t, "client", missing[0].Sender().Name())
	assert.NotEmpty(t, missing[0].Reason())

	// Healthy siblings must NOT appear as dead-letters.
	assert.Empty(t, deadlettersFor(consumer, "alice"))
	assert.Empty(t, deadlettersFor(consumer, "bob"))
}

// TestRemoteTellCoalescedTransportFailureDeadletters verifies that a
// whole-batch transport failure surfaces as one dead-letter per message in
// the failed batch on the sending system's event stream. We point the
// outbound remoteclient at a closed port so the coalescer's flush receives
// a dial/write error and the error-handler fan-out publishes dead-letters
// locally via the actor system's dead-letter actor.
func TestRemoteTellCoalescedTransportFailureDeadletters(t *testing.T) {
	ctx := context.Background()
	host := "127.0.0.1"
	port := dynaport.Get(1)[0]

	sys, err := NewActorSystem(
		"testSys",
		WithLogger(log.DiscardLogger),
		WithRemote(remote.NewConfig(host, port)),
	)
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	pause.For(500 * time.Millisecond)
	t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })

	consumer, err := sys.Subscribe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sys.Unsubscribe(consumer) })

	// Acquire a fresh port and do NOT start a server on it.
	deadPort := dynaport.Get(1)[0]
	sender := address.New("client", sys.Name(), host, port)
	receiver := address.New("ghost", "remote", host, deadPort)

	remoting := sys.(*actorSystem).remoting
	const burst = 10
	for range burst {
		require.NoError(t, remoting.RemoteTell(ctx, sender, receiver, new(testpb.TestSend)))
	}

	// The coalescer flush fails on the dial; the error handler then
	// enqueues the batch and the drain goroutine publishes N dead-letters.
	// We don't assert equality with `burst` because the coalescer is free
	// to batch some calls synchronously into one flush — we just need at
	// least one dead-letter so we know the fan-out wired up.
	require.Eventually(t, func() bool {
		return len(deadlettersFor(consumer, "ghost")) >= 1
	}, 3*time.Second, 50*time.Millisecond)

	for _, dl := range deadlettersFor(consumer, "ghost") {
		assert.Equal(t, "ghost", dl.Receiver().Name())
		assert.Equal(t, "client", dl.Sender().Name())
		assert.NotEmpty(t, dl.Reason())
	}
}

// TestRemoteTellHandlerMetadataErrorDeadlettered asserts that when a
// per-message propagator fails to extract headers, that message is
// dead-lettered locally while a healthy sibling in the same batch still
// reaches its actor. Exercises the bad-metadata branch of
// deliverRemoteTellMessage.
func TestRemoteTellHandlerMetadataErrorDeadlettered(t *testing.T) {
	ctx := context.Background()
	host := "127.0.0.1"
	port := dynaport.Get(1)[0]

	failing := &MockFailingContextPropagator{err: errors.New("extract boom")}
	sys, err := NewActorSystem(
		"testSys",
		WithLogger(log.DiscardLogger),
		WithRemote(remote.NewConfig(host, port, remote.WithContextPropagator(failing))),
	)
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	pause.For(500 * time.Millisecond)
	t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })

	consumer, err := sys.Subscribe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sys.Unsubscribe(consumer) })

	alice := &countingTestActor{}
	_, err = sys.Spawn(ctx, "alice", alice)
	require.NoError(t, err)

	payload, err := sys.(*actorSystem).remoting.Serializer(new(testpb.TestSend)).Serialize(new(testpb.TestSend))
	require.NoError(t, err)
	senderAddr := address.New("client", "remote", host, port).String()

	req := &internalpb.RemoteTellRequest{
		RemoteMessages: []*internalpb.RemoteMessage{
			// alice — healthy, no metadata so messageMetadata returns early
			{Sender: senderAddr, Receiver: address.New("alice", sys.Name(), host, port).String(), Message: payload},
			// bob — carries metadata that will trip the failing propagator
			{
				Sender:   senderAddr,
				Receiver: address.New("bob", sys.Name(), host, port).String(),
				Message:  payload,
				Metadata: map[string]string{"x-trace-id": "boom"},
			},
		},
	}
	resp, err := sys.(*actorSystem).remoteTellHandler(ctx, nil, req)
	require.NoError(t, err)
	_, ok := resp.(*internalpb.RemoteTellResponse)
	require.True(t, ok, "per-message metadata failure must not fail the whole batch")

	require.Eventually(t, func() bool { return alice.Count() == 1 }, time.Second, 10*time.Millisecond)
	pause.For(500 * time.Millisecond)

	bobDL := deadlettersFor(consumer, "bob")
	require.Len(t, bobDL, 1, "bad-metadata message must be dead-lettered")
	assert.Contains(t, bobDL[0].Reason(), "boom")
	assert.Empty(t, deadlettersFor(consumer, "alice"), "healthy sibling must not be dead-lettered")
}

// TestRemoteTellHandlerStoppedActorDeadlettered asserts that a message
// whose target actor has been stopped is dead-lettered. After Shutdown the
// node is removed from the tree so the handler takes the "address not
// found" branch — a distinct code path from the never-spawned case already
// covered by TestRemoteTellHandlerPerMessageDeadlettering.
func TestRemoteTellHandlerStoppedActorDeadlettered(t *testing.T) {
	ctx := context.Background()
	host := "127.0.0.1"
	port := dynaport.Get(1)[0]

	sys, err := NewActorSystem(
		"testSys",
		WithLogger(log.DiscardLogger),
		WithRemote(remote.NewConfig(host, port)),
	)
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	pause.For(500 * time.Millisecond)
	t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })

	consumer, err := sys.Subscribe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sys.Unsubscribe(consumer) })

	target := &countingTestActor{}
	ref, err := sys.Spawn(ctx, "ephemeral", target)
	require.NoError(t, err)
	require.NoError(t, ref.Shutdown(ctx))
	// Wait for the tree to reflect the removal.
	require.Eventually(t, func() bool {
		_, exists := sys.(*actorSystem).actors.node(ref.getAddress().String())
		return !exists
	}, time.Second, 10*time.Millisecond)

	payload, err := sys.(*actorSystem).remoting.Serializer(new(testpb.TestSend)).Serialize(new(testpb.TestSend))
	require.NoError(t, err)
	senderAddr := address.New("client", "remote", host, port).String()
	req := &internalpb.RemoteTellRequest{
		RemoteMessages: []*internalpb.RemoteMessage{
			{Sender: senderAddr, Receiver: address.New("ephemeral", sys.Name(), host, port).String(), Message: payload},
		},
	}
	resp, err := sys.(*actorSystem).remoteTellHandler(ctx, nil, req)
	require.NoError(t, err)
	_, ok := resp.(*internalpb.RemoteTellResponse)
	require.True(t, ok)

	pause.For(500 * time.Millisecond)
	dl := deadlettersFor(consumer, "ephemeral")
	require.Len(t, dl, 1, "message to stopped actor must be dead-lettered")
	assert.Equal(t, "client", dl[0].Sender().Name())
}

// TestRemoteTellCoalescedTransportFailureExactCount asserts that when the
// outbound coalescer's flush fails (peer down), every RemoteTell in the
// failed batch is published as a dead-letter — not just some. This is the
// property operators depend on: no silent loss on transport failure.
func TestRemoteTellCoalescedTransportFailureExactCount(t *testing.T) {
	ctx := context.Background()
	host := "127.0.0.1"
	port := dynaport.Get(1)[0]

	sys, err := NewActorSystem(
		"testSys",
		WithLogger(log.DiscardLogger),
		WithRemote(remote.NewConfig(host, port)),
	)
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	pause.For(500 * time.Millisecond)
	t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })

	consumer, err := sys.Subscribe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sys.Unsubscribe(consumer) })

	deadPort := dynaport.Get(1)[0]
	sender := address.New("client", sys.Name(), host, port)
	receiver := address.New("void", "remote", host, deadPort)

	remoting := sys.(*actorSystem).remoting
	const sendCount = 8
	for range sendCount {
		require.NoError(t, remoting.RemoteTell(ctx, sender, receiver, new(testpb.TestSend)))
	}

	// Poll via the dead-letter actor's counter rather than the subscriber,
	// because Iterator() drains on every call (see eventstream/subscriber.go)
	// — repeated polling there loses messages between snapshots.
	require.Eventually(t, func() bool {
		reply, askErr := Ask(ctx, sys.(*actorSystem).getDeadletter(),
			&commands.DeadlettersCountRequest{Address: receiver}, 500*time.Millisecond)
		if askErr != nil {
			return false
		}
		resp, ok := reply.(*commands.DeadlettersCountResponse)
		return ok && resp.TotalCount >= sendCount
	}, 5*time.Second, 50*time.Millisecond)

	// Now snapshot the subscriber once — all N publications should be there.
	dls := deadlettersFor(consumer, "void")
	assert.GreaterOrEqual(t, len(dls), sendCount, "every failed message must be dead-lettered, not just some")
	for _, dl := range dls {
		assert.Equal(t, "void", dl.Receiver().Name())
		assert.Equal(t, "client", dl.Sender().Name())
		assert.NotEmpty(t, dl.Reason())
	}
}

// TestRemoteTellHandlerCorruptPayloadSkipped asserts that a corrupt payload
// (unknown type URL / garbage bytes) inbound through remoteTellHandler is
// logged and skipped without dispatching, without crashing, and without
// producing a dead-letter entry — we can't describe "what was delivered"
// if we can't decode the payload in the first place. The enclosing batch
// must still return success so healthy siblings aren't collateral damage.
func TestRemoteTellHandlerCorruptPayloadSkipped(t *testing.T) {
	ctx := context.Background()
	host := "127.0.0.1"
	port := dynaport.Get(1)[0]

	sys, err := NewActorSystem(
		"testSys",
		WithLogger(log.DiscardLogger),
		WithRemote(remote.NewConfig(host, port)),
	)
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	pause.For(500 * time.Millisecond)
	t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })

	consumer, err := sys.Subscribe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sys.Unsubscribe(consumer) })

	senderAddr := address.New("client", sys.Name(), host, port).String()
	receiverAddr := address.New("void", sys.Name(), host, port).String()

	req := &internalpb.RemoteTellRequest{
		RemoteMessages: []*internalpb.RemoteMessage{
			{Sender: senderAddr, Receiver: receiverAddr, Message: []byte{0x00, 0xDE, 0xAD, 0xBE, 0xEF}},
		},
	}

	assert.NotPanics(t, func() {
		resp, err := sys.(*actorSystem).remoteTellHandler(ctx, nil, req)
		require.NoError(t, err)
		_, ok := resp.(*internalpb.RemoteTellResponse)
		assert.True(t, ok, "batch must succeed even with a corrupt-payload entry")
	})

	pause.For(300 * time.Millisecond)
	assert.Empty(t, deadlettersFor(consumer, "void"), "corrupt payload must not produce a dead-letter entry")
}

// TestCoalescedDrainCorruptPayloadSkipped exercises the client-side drain
// path: a whole-batch transport failure carries a message with a corrupt
// payload. The drain must log-and-skip the poisoned entry without
// crashing the goroutine and without publishing a dead-letter for it,
// while still publishing dead-letters for the healthy entries in the same
// batch. This guards against one bad message taking down the fan-out.
func TestCoalescedDrainCorruptPayloadSkipped(t *testing.T) {
	ctx := context.Background()
	host := "127.0.0.1"
	port := dynaport.Get(1)[0]

	sys, err := NewActorSystem(
		"testSys",
		WithLogger(log.DiscardLogger),
		WithRemote(remote.NewConfig(host, port)),
	)
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	pause.For(500 * time.Millisecond)
	t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })

	consumer, err := sys.Subscribe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sys.Unsubscribe(consumer) })

	goodPayload, err := sys.(*actorSystem).remoting.Serializer(new(testpb.TestSend)).Serialize(new(testpb.TestSend))
	require.NoError(t, err)

	senderAddr := address.New("client", sys.Name(), host, port).String()
	good := &internalpb.RemoteMessage{
		Sender:   senderAddr,
		Receiver: address.New("good", "remote", host, port).String(),
		Message:  goodPayload,
	}
	bad := &internalpb.RemoteMessage{
		Sender:   senderAddr,
		Receiver: address.New("bad", "remote", host, port).String(),
		Message:  []byte{0x00, 0xDE, 0xAD, 0xBE, 0xEF},
	}

	assert.NotPanics(t, func() {
		sys.(*actorSystem).enqueueCoalescedFailure("10.0.0.1:9999",
			[]*internalpb.RemoteMessage{good, bad}, errors.New("transport down"))
	})

	// The good sibling must still flow through.
	require.Eventually(t, func() bool {
		return len(deadlettersFor(consumer, "good")) >= 1
	}, 2*time.Second, 20*time.Millisecond)

	// The bad sibling must NOT produce a dead-letter — it's dropped with a log.
	assert.Empty(t, deadlettersFor(consumer, "bad"), "corrupt payload must not produce a dead-letter entry")
}

// TestEnqueueCoalescedFailureDroppedDuringShutdown asserts that a
// whole-batch failure reported by the coalescer after shutdown has begun
// is swallowed rather than panicking on a closed channel.
func TestEnqueueCoalescedFailureDroppedDuringShutdown(t *testing.T) {
	ctx := context.Background()
	host := "127.0.0.1"
	port := dynaport.Get(1)[0]

	sys, err := NewActorSystem(
		"testSys",
		WithLogger(log.DiscardLogger),
		WithRemote(remote.NewConfig(host, port)),
	)
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	pause.For(500 * time.Millisecond)

	impl := sys.(*actorSystem)
	// Simulate the middle of shutdown without actually closing everything.
	impl.shuttingDown.Store(true)

	msg := &internalpb.RemoteMessage{
		Sender:   address.New("from", sys.Name(), host, port).String(),
		Receiver: address.New("to", sys.Name(), host, port).String(),
	}
	assert.NotPanics(t, func() {
		impl.enqueueCoalescedFailure("127.0.0.1:0", []*internalpb.RemoteMessage{msg}, errors.New("down"))
	})

	// Restore and tear down cleanly.
	impl.shuttingDown.Store(false)
	assert.NoError(t, sys.Stop(ctx))
}

// TestRemoteTellHandlerAllValidNoDeadletters is the happy-path control: a
// batch whose receivers all resolve to live actors must deliver every
// message without producing any dead-letters.
func TestRemoteTellHandlerAllValidNoDeadletters(t *testing.T) {
	ctx := context.Background()
	host := "127.0.0.1"
	port := dynaport.Get(1)[0]

	sys, err := NewActorSystem(
		"testSys",
		WithLogger(log.DiscardLogger),
		WithRemote(remote.NewConfig(host, port)),
	)
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	pause.For(500 * time.Millisecond)
	t.Cleanup(func() { assert.NoError(t, sys.Stop(ctx)) })

	consumer, err := sys.Subscribe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sys.Unsubscribe(consumer) })

	actorA := &countingTestActor{}
	actorB := &countingTestActor{}
	_, err = sys.Spawn(ctx, "a", actorA)
	require.NoError(t, err)
	_, err = sys.Spawn(ctx, "b", actorB)
	require.NoError(t, err)

	payload, err := sys.(*actorSystem).remoting.Serializer(new(testpb.TestSend)).Serialize(new(testpb.TestSend))
	require.NoError(t, err)
	senderAddr := address.New("client", "remote", host, port).String()

	req := &internalpb.RemoteTellRequest{
		RemoteMessages: []*internalpb.RemoteMessage{
			{Sender: senderAddr, Receiver: address.New("a", sys.Name(), host, port).String(), Message: payload},
			{Sender: senderAddr, Receiver: address.New("b", sys.Name(), host, port).String(), Message: payload},
			{Sender: senderAddr, Receiver: address.New("a", sys.Name(), host, port).String(), Message: payload},
		},
	}
	resp, err := sys.(*actorSystem).remoteTellHandler(ctx, nil, req)
	require.NoError(t, err)
	_, ok := resp.(*internalpb.RemoteTellResponse)
	require.True(t, ok)

	require.Eventually(t, func() bool { return actorA.Count() == 2 && actorB.Count() == 1 }, time.Second, 10*time.Millisecond)
	pause.For(200 * time.Millisecond)

	// No dead-letters for either receiver.
	assert.Empty(t, deadlettersFor(consumer, "a"))
	assert.Empty(t, deadlettersFor(consumer, "b"))
}
