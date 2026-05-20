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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/persistence"
)

// ---------------------------------------------------------------------------
// Test types — registered with the CBOR global registry in init().
// ---------------------------------------------------------------------------

type counterCmd struct{ Delta int }
type counterEvent struct{ Delta int }
type counterState struct{ Value int }

// rejectCmd causes the behavior to return a domain error.
type rejectCmd struct{}

// noopCmd causes the behavior to return no events (read-only path).
type noopCmd struct{}

func init() {
	types.GlobalRegistry.Register(&counterCmd{})
	types.GlobalRegistry.Register(&counterEvent{})
	types.GlobalRegistry.Register(&counterState{})
	types.GlobalRegistry.Register(&rejectCmd{})
	types.GlobalRegistry.Register(&noopCmd{})
}

// ---------------------------------------------------------------------------
// Test behavior
// ---------------------------------------------------------------------------

type counterBehavior struct{}

func (c *counterBehavior) ID() string                   { return "counter-behavior" }
func (c *counterBehavior) MarshalBinary() ([]byte, error) { return nil, nil }
func (c *counterBehavior) UnmarshalBinary([]byte) error   { return nil }

func (c *counterBehavior) InitialState() any {
	return &counterState{}
}

func (c *counterBehavior) HandleCommand(_ context.Context, cmd any, _ any) ([]any, error) {
	switch m := cmd.(type) {
	case *counterCmd:
		return []any{&counterEvent{Delta: m.Delta}}, nil
	case *rejectCmd:
		return nil, errors.New("rejected")
	case *noopCmd:
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown command: %T", cmd)
	}
}

func (c *counterBehavior) HandleEvent(_ context.Context, event any, state any) (any, error) {
	switch e := event.(type) {
	case *counterEvent:
		s := state.(*counterState)
		return &counterState{Value: s.Value + e.Delta}, nil
	default:
		return state, fmt.Errorf("unknown event: %T", event)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newESTestSystem builds an actor system wired for event sourcing.
// Pass eventsStore=nil to skip event-sourcing wiring entirely (used to verify
// the "missing events store" path).
func newESTestSystem(t *testing.T, eventsStore persistence.EventsStore, snapshotStore persistence.SnapshotStore, behaviors ...EventSourcedBehavior) ActorSystem {
	t.Helper()
	opts := []Option{WithLogger(log.DiscardLogger)}
	if eventsStore != nil {
		var esOpts []EventSourcingOption
		if snapshotStore != nil {
			esOpts = append(esOpts, WithSnapshotStore(snapshotStore))
		}
		opts = append(opts, WithEventSourcing(eventsStore, behaviors, esOpts...))
	}
	sys, err := NewActorSystem("testES", opts...)
	require.NoError(t, err)
	require.NoError(t, sys.Start(context.Background()))
	t.Cleanup(func() { _ = sys.Stop(context.Background()) })
	return sys
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestEventSourced(t *testing.T) {
	t.Run("basic command-event-state flow", func(t *testing.T) {
		ctx := context.Background()
		store := persistence.NewMemoryEventsStore()
		sys := newESTestSystem(t, store, nil, &counterBehavior{})

		pid, err := sys.SpawnEventSourced(ctx, "counter-1", &counterBehavior{})
		require.NoError(t, err)

		resp, err := Ask(ctx, pid, &counterCmd{Delta: 5}, time.Second)
		require.NoError(t, err)
		state := resp.(*counterState)
		assert.Equal(t, 5, state.Value)

		resp, err = Ask(ctx, pid, &counterCmd{Delta: 3}, time.Second)
		require.NoError(t, err)
		state = resp.(*counterState)
		assert.Equal(t, 8, state.Value)
	})

	t.Run("no-op command returns current state without persisting", func(t *testing.T) {
		ctx := context.Background()
		store := persistence.NewMemoryEventsStore()
		sys := newESTestSystem(t, store, nil, &counterBehavior{})

		pid, err := sys.SpawnEventSourced(ctx, "counter-noop", &counterBehavior{})
		require.NoError(t, err)

		_, err = Ask(ctx, pid, &counterCmd{Delta: 10}, time.Second)
		require.NoError(t, err)

		resp, err := Ask(ctx, pid, &noopCmd{}, time.Second)
		require.NoError(t, err)
		state := resp.(*counterState)
		assert.Equal(t, 10, state.Value)

		latest, err := store.GetLatestEvent(ctx, "counter-noop")
		require.NoError(t, err)
		assert.Equal(t, uint64(1), latest.SequenceNumber)
	})

	t.Run("domain rejection returns error without persisting", func(t *testing.T) {
		ctx := context.Background()
		store := persistence.NewMemoryEventsStore()
		sys := newESTestSystem(t, store, nil, &counterBehavior{})

		pid, err := sys.SpawnEventSourced(ctx, "counter-reject", &counterBehavior{})
		require.NoError(t, err)

		resp, err := Ask(ctx, pid, &rejectCmd{}, time.Second)
		require.NoError(t, err)
		assert.Error(t, resp.(error))

		latest, err := store.GetLatestEvent(ctx, "counter-reject")
		require.NoError(t, err)
		assert.Nil(t, latest)
	})

	t.Run("recovery replays events and restores state", func(t *testing.T) {
		ctx := context.Background()
		store := persistence.NewMemoryEventsStore()
		sys := newESTestSystem(t, store, nil, &counterBehavior{})

		pid, err := sys.SpawnEventSourced(ctx, "counter-recovery", &counterBehavior{})
		require.NoError(t, err)

		for i := 0; i < 4; i++ {
			_, err = Ask(ctx, pid, &counterCmd{Delta: 2}, time.Second)
			require.NoError(t, err)
		}

		require.NoError(t, sys.Kill(ctx, "counter-recovery"))
		pause.For(200 * time.Millisecond)

		pid2, err := sys.SpawnEventSourced(ctx, "counter-recovery", &counterBehavior{})
		require.NoError(t, err)

		resp, err := Ask(ctx, pid2, &noopCmd{}, time.Second)
		require.NoError(t, err)
		state := resp.(*counterState)
		assert.Equal(t, 8, state.Value)
	})

	t.Run("snapshot written on PostStop and used on recovery", func(t *testing.T) {
		ctx := context.Background()
		eventsStore := persistence.NewMemoryEventsStore()
		snapStore := persistence.NewMemorySnapshotStore()
		sys := newESTestSystem(t, eventsStore, snapStore, &counterBehavior{})

		pid, err := sys.SpawnEventSourced(ctx, "counter-snap", &counterBehavior{})
		require.NoError(t, err)

		for i := 0; i < 5; i++ {
			_, err = Ask(ctx, pid, &counterCmd{Delta: 1}, time.Second)
			require.NoError(t, err)
		}

		require.NoError(t, sys.Kill(ctx, "counter-snap"))
		pause.For(200 * time.Millisecond)

		snap, err := snapStore.GetLatestSnapshot(ctx, "counter-snap")
		require.NoError(t, err)
		require.NotNil(t, snap)
		assert.Equal(t, uint64(5), snap.SequenceNumber)

		// Remove all persisted events — recovery must rely on the snapshot alone.
		require.NoError(t, eventsStore.DeleteEvents(ctx, "counter-snap", 5))

		pid2, err := sys.SpawnEventSourced(ctx, "counter-snap", &counterBehavior{})
		require.NoError(t, err)

		resp, err := Ask(ctx, pid2, &noopCmd{}, time.Second)
		require.NoError(t, err)
		state := resp.(*counterState)
		assert.Equal(t, 5, state.Value)
	})

	t.Run("intermediate snapshot written at configured interval", func(t *testing.T) {
		ctx := context.Background()
		eventsStore := persistence.NewMemoryEventsStore()
		snapStore := persistence.NewMemorySnapshotStore()
		sys := newESTestSystem(t, eventsStore, snapStore, &counterBehavior{})

		pid, err := sys.SpawnEventSourced(ctx, "counter-interval", &counterBehavior{},
			WithSnapshotCriteria(&persistence.SnapshotCriteria{SnapshotInterval: 3}))
		require.NoError(t, err)

		for i := 0; i < 3; i++ {
			_, err = Ask(ctx, pid, &counterCmd{Delta: 1}, time.Second)
			require.NoError(t, err)
		}

		// Allow the async snapshot writer to persist the intermediate snapshot.
		pause.For(200 * time.Millisecond)

		snap, err := snapStore.GetLatestSnapshot(ctx, "counter-interval")
		require.NoError(t, err)
		require.NotNil(t, snap)
		assert.Equal(t, uint64(3), snap.SequenceNumber)
	})

	t.Run("missing events store returns error", func(t *testing.T) {
		ctx := context.Background()
		sys := newESTestSystem(t, nil, nil) // WithEventSourcing not configured

		pid, err := sys.SpawnEventSourced(ctx, "counter-nostore", &counterBehavior{})
		assert.ErrorIs(t, err, gerrors.ErrEventsStoreRequired)
		assert.Nil(t, pid)
	})

	t.Run("events store write failure surfaces via Err", func(t *testing.T) {
		ctx := context.Background()
		store := &failingEventsStore{}
		sys := newESTestSystem(t, store, nil, &counterBehavior{})

		pid, err := sys.SpawnEventSourced(ctx, "counter-fail", &counterBehavior{})
		require.NoError(t, err)

		resp, err := Ask(ctx, pid, &counterCmd{Delta: 1}, time.Second)
		// Ask returns the response channel error when the actor sets rctx.Err;
		// the response may be nil or the error may surface as a non-nil err.
		if err == nil {
			assert.Nil(t, resp)
		}
	})

	t.Run("runtime-registered behavior can be spawned", func(t *testing.T) {
		ctx := context.Background()
		store := persistence.NewMemoryEventsStore()
		// Wire event sourcing without declaring counterBehavior at startup.
		sys := newESTestSystem(t, store, nil)

		// Spawning an undeclared behavior must fail.
		pid, err := sys.SpawnEventSourced(ctx, "counter-rt", &counterBehavior{})
		require.Error(t, err)
		assert.Nil(t, pid)

		// Register at runtime and try again.
		require.NoError(t, sys.WithEventSourcedBehavior(&counterBehavior{}))

		pid, err = sys.SpawnEventSourced(ctx, "counter-rt", &counterBehavior{})
		require.NoError(t, err)

		resp, err := Ask(ctx, pid, &counterCmd{Delta: 7}, time.Second)
		require.NoError(t, err)
		assert.Equal(t, 7, resp.(*counterState).Value)
	})
}

// ---------------------------------------------------------------------------
// failingEventsStore always returns an error on writes.
// ---------------------------------------------------------------------------

type failingEventsStore struct{}

func (f *failingEventsStore) WriteEvents(_ context.Context, _ []*persistence.PersistedEvent) error {
	return errors.New("store unavailable")
}

func (f *failingEventsStore) ReplayEvents(_ context.Context, _ string, _, _, _ uint64) ([]*persistence.PersistedEvent, error) {
	return nil, nil
}

func (f *failingEventsStore) GetLatestEvent(_ context.Context, _ string) (*persistence.PersistedEvent, error) {
	return nil, nil
}

func (f *failingEventsStore) DeleteEvents(_ context.Context, _ string, _ uint64) error {
	return nil
}
