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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// gatedGrain blocks in OnReceive on its first message until gate is closed
// and records which instance handled each later message, and whether that
// instance had already been deactivated.
type gatedGrain struct {
	MockNoopGrain

	instance    int
	gate        chan struct{}
	entered     chan struct{}
	enteredOnce sync.Once
	deactivated bool
	log         *handledLog
}

type handledLog struct {
	mu      sync.Mutex
	entries []handled
}

type handled struct {
	instance    int
	deactivated bool
}

func (l *handledLog) add(instance int, deactivated bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, handled{instance: instance, deactivated: deactivated})
}

func (l *handledLog) snapshot() []handled {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]handled(nil), l.entries...)
}

func (g *gatedGrain) OnReceive(ctx *GrainContext) {
	g.enteredOnce.Do(func() {
		close(g.entered)
		<-g.gate
	})
	g.log.add(g.instance, g.deactivated)
	ctx.Response(new(testpb.Reply))
}

func (g *gatedGrain) OnDeactivate(context.Context, *GrainProps) error {
	g.deactivated = true
	return nil
}

// TestGrainPassivationDoesNotHandQueuedMessagesToTheDeactivatedGrain
// queues messages behind a passivation pill in one turn: the turn keeps
// draining after the pill deactivated the grain, so the queued messages
// must not be handled by the instance whose OnDeactivate already ran. The
// pill travels through the mailbox only for a grain with reentrancy
// configured, see passivationTry.
func TestGrainPassivationDoesNotHandQueuedMessagesToTheDeactivatedGrain(t *testing.T) {
	ctx := t.Context()
	testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, testSystem.Start(ctx))
	defer func() { require.NoError(t, testSystem.Stop(ctx)) }()

	const deactivateAfter = 50 * time.Millisecond
	logbook := new(handledLog)
	var instances int
	gate := make(chan struct{})
	entered := make(chan struct{})
	factory := func(_ context.Context) (Grain, error) {
		instances++
		return &gatedGrain{instance: instances, gate: gate, entered: entered, log: logbook}, nil
	}
	identity, err := testSystem.GrainIdentity(ctx, "gated", factory,
		WithGrainDeactivateAfter(deactivateAfter),
		WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant))),
	)
	require.NoError(t, err)

	// The first message holds the grain's turn open at the gate.
	var asks sync.WaitGroup
	asks.Go(func() {
		_, err := testSystem.AskGrain(ctx, identity, new(testpb.TestReply), 5*time.Second)
		require.NoError(t, err)
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first message never reached the grain")
	}

	// The passivation pill lands behind it, as when the manager fires while
	// the grain is busy, and two more messages queue behind the pill.
	gp, ok := testSystem.(*actorSystem).grains.Get(identity.String())
	require.True(t, ok)
	require.True(t, gp.enqueuePassivationPill())
	results := make(chan error, 2)
	for range 2 {
		asks.Go(func() {
			_, err := testSystem.AskGrain(ctx, identity, new(testpb.TestReply), 5*time.Second)
			results <- err
		})
	}
	// Wait past deactivateAfter so the pill deactivates instead of
	// re-arming, then let the turn go on.
	time.Sleep(2 * deactivateAfter)
	close(gate)
	asks.Wait()

	for _, entry := range logbook.snapshot() {
		require.False(t, entry.deactivated, "message handled by instance %d after its OnDeactivate ran", entry.instance)
	}
	// The grain is gone, and the queued messages either reached a new
	// instance or were refused: they were not silently handled by the
	// deactivated one.
	_, ok = testSystem.(*actorSystem).grains.Get(identity.String())
	require.False(t, ok)
	close(results)
	for err := range results {
		if err == nil {
			require.Equal(t, 2, instances, "a queued message was answered without a new activation")
		}
	}
}

// TestGrainPassivationDoesNotRunOnDeactivateDuringATurn shows the default
// path, without reentrancy: the passivation manager calls deactivate on its
// own goroutine, so OnDeactivate runs while OnReceive is still handling a
// message.
func TestGrainPassivationDoesNotRunOnDeactivateDuringATurn(t *testing.T) {
	ctx := t.Context()
	testSystem, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	require.NoError(t, testSystem.Start(ctx))
	defer func() { require.NoError(t, testSystem.Stop(ctx)) }()

	const deactivateAfter = 50 * time.Millisecond
	logbook := new(handledLog)
	gate := make(chan struct{})
	entered := make(chan struct{})
	identity, err := testSystem.GrainIdentity(ctx, "gated", func(_ context.Context) (Grain, error) {
		return &gatedGrain{instance: 1, gate: gate, entered: entered, log: logbook}, nil
	}, WithGrainDeactivateAfter(deactivateAfter))
	require.NoError(t, err)

	// The message holds the turn open at the gate for longer than
	// deactivateAfter, as a slow database call would.
	var asks sync.WaitGroup
	asks.Go(func() {
		_, err := testSystem.AskGrain(ctx, identity, new(testpb.TestReply), 5*time.Second)
		require.NoError(t, err)
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the message never reached the grain")
	}
	time.Sleep(4 * deactivateAfter)
	close(gate)
	asks.Wait()

	for _, entry := range logbook.snapshot() {
		require.False(t, entry.deactivated, "OnDeactivate ran while OnReceive was handling a message")
	}
}
