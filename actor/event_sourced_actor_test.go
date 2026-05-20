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
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/discovery/nats"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/eventstream"
	"github.com/tochemey/goakt/v4/extension"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/persistence"
	"github.com/tochemey/goakt/v4/remote"
)

type counterCmd struct{ Delta int }
type counterEvent struct{ Delta int }
type counterState struct{ Value int }

// rejectCmd causes the behavior to return a domain error.
type rejectCmd struct{}

// noopCmd causes the behavior to return no events (read-only path).
type noopCmd struct{}

// applyFailCmd produces two events: a valid counterEvent and a poisonEvent that
// HandleEvent rejects. It exercises the apply-failure path in handleCommand.
type applyFailCmd struct{ Delta int }

// poisonEvent is the unknown event type that triggers HandleEvent failure.
type poisonEvent struct{}

func init() {
	types.GlobalRegistry.Register(&counterCmd{})
	types.GlobalRegistry.Register(&counterEvent{})
	types.GlobalRegistry.Register(&counterState{})
	types.GlobalRegistry.Register(&rejectCmd{})
	types.GlobalRegistry.Register(&noopCmd{})
	types.GlobalRegistry.Register(&applyFailCmd{})
	types.GlobalRegistry.Register(&poisonEvent{})
}

type counterBehavior struct{}

func (c *counterBehavior) ID() string                     { return "counter-behavior" }
func (c *counterBehavior) MarshalBinary() ([]byte, error) { return nil, nil }
func (c *counterBehavior) UnmarshalBinary([]byte) error   { return nil }

// statefulBehavior carries per-instance configuration used by the wrapper
// round-trip test.
type statefulBehavior struct {
	Multiplier int
}

func (s *statefulBehavior) ID() string { return "stateful-behavior" }
func (s *statefulBehavior) MarshalBinary() ([]byte, error) {
	return fmt.Appendf(nil, "%d", s.Multiplier), nil
}
func (s *statefulBehavior) UnmarshalBinary(data []byte) error {
	if len(data) == 0 {
		s.Multiplier = 0
		return nil
	}
	_, err := fmt.Sscanf(string(data), "%d", &s.Multiplier)
	return err
}
func (s *statefulBehavior) InitialState() any { return &counterState{} }
func (s *statefulBehavior) HandleCommand(_ context.Context, _ any, _ any) ([]any, error) {
	return nil, nil
}
func (s *statefulBehavior) HandleEvent(_ context.Context, _ any, state any) (any, error) {
	return state, nil
}

// userDep is a generic user dependency used in collision tests.
type userDep struct {
	id   string
	data string
}

func (u *userDep) ID() string                     { return u.id }
func (u *userDep) MarshalBinary() ([]byte, error) { return []byte(u.data), nil }
func (u *userDep) UnmarshalBinary(b []byte) error { u.data = string(b); return nil }

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
	case *applyFailCmd:
		// First event applies cleanly; second event triggers HandleEvent failure.
		return []any{&counterEvent{Delta: m.Delta}, &poisonEvent{}}, nil
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

// newEventSourcingTestSystem starts a non-clustered actor system wired for
// event sourcing. Pass eventsStore=nil to skip event-sourcing wiring.
func newEventSourcingTestSystem(t *testing.T, eventsStore persistence.EventsStore, snapshotStore persistence.SnapshotStore, behaviors ...EventSourcedBehavior) ActorSystem {
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

func TestEventSourced(t *testing.T) {
	t.Run("basic command-event-state flow", func(t *testing.T) {
		ctx := context.Background()
		store := persistence.NewMemoryEventsStore()
		sys := newEventSourcingTestSystem(t, store, nil, &counterBehavior{})

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
		sys := newEventSourcingTestSystem(t, store, nil, &counterBehavior{})

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
		sys := newEventSourcingTestSystem(t, store, nil, &counterBehavior{})

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
		sys := newEventSourcingTestSystem(t, store, nil, &counterBehavior{})

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
		sys := newEventSourcingTestSystem(t, eventsStore, snapStore, &counterBehavior{})

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
		sys := newEventSourcingTestSystem(t, eventsStore, snapStore, &counterBehavior{})

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
		sys := newEventSourcingTestSystem(t, nil, nil) // WithEventSourcing not configured

		pid, err := sys.SpawnEventSourced(ctx, "counter-nostore", &counterBehavior{})
		assert.ErrorIs(t, err, gerrors.ErrEventsStoreRequired)
		assert.Nil(t, pid)
	})

	t.Run("events store write failure surfaces via Err", func(t *testing.T) {
		ctx := context.Background()
		store := &failingEventsStore{}
		sys := newEventSourcingTestSystem(t, store, nil, &counterBehavior{})

		pid, err := sys.SpawnEventSourced(ctx, "counter-fail", &counterBehavior{})
		require.NoError(t, err)

		resp, err := Ask(ctx, pid, &counterCmd{Delta: 1}, time.Second)
		// Ask returns the response channel error when the actor sets rctx.Err;
		// the response may be nil or the error may surface as a non-nil err.
		if err == nil {
			assert.Nil(t, resp)
		}
	})

	t.Run("HandleEvent failure leaves the events store empty", func(t *testing.T) {
		ctx := context.Background()
		store := persistence.NewMemoryEventsStore()
		sys := newEventSourcingTestSystem(t, store, nil, &counterBehavior{})

		pid, err := sys.SpawnEventSourced(ctx, "counter-apply-fail", &counterBehavior{})
		require.NoError(t, err)

		// applyFailCmd produces [counterEvent, poisonEvent]; HandleEvent rejects
		// poisonEvent on the second element. With apply-first ordering, the
		// store is never touched when any event in the batch fails to apply.
		_, _ = Ask(ctx, pid, &applyFailCmd{Delta: 5}, time.Second)

		latest, err := store.GetLatestEvent(ctx, "counter-apply-fail")
		require.NoError(t, err)
		assert.Nil(t, latest, "store must be empty when HandleEvent fails")
	})

	t.Run("events store write failure leaves the store empty", func(t *testing.T) {
		ctx := context.Background()
		store := newSpyEventsStore()
		store.setWriteErr(errors.New("store unavailable"))
		sys := newEventSourcingTestSystem(t, store, nil, &counterBehavior{})

		pid, err := sys.SpawnEventSourced(ctx, "counter-rollback", &counterBehavior{})
		require.NoError(t, err)

		// Drive a command that would advance state by 7 if the write succeeded.
		// The actor is expected to fail this command after the write error.
		_, _ = Ask(ctx, pid, &counterCmd{Delta: 7}, time.Second)

		// The underlying store must show no committed events for this persistence ID.
		latest, err := store.GetLatestEvent(ctx, "counter-rollback")
		require.NoError(t, err)
		assert.Nil(t, latest,
			"WriteEvents failure must leave the store empty; currentState commit happens only after a successful write")
	})

	t.Run("runtime-registered behavior can be spawned", func(t *testing.T) {
		ctx := context.Background()
		store := persistence.NewMemoryEventsStore()
		// Wire event sourcing without declaring counterBehavior at startup.
		sys := newEventSourcingTestSystem(t, store, nil)

		// Spawning an undeclared behavior must fail.
		pid, err := sys.SpawnEventSourced(ctx, "counter-rt", &counterBehavior{})
		require.Error(t, err)
		assert.Nil(t, pid)

		// Register at runtime and try again.
		require.NoError(t, sys.RegisterEventSourcedBehavior(&counterBehavior{}))

		pid, err = sys.SpawnEventSourced(ctx, "counter-rt", &counterBehavior{})
		require.NoError(t, err)

		resp, err := Ask(ctx, pid, &counterCmd{Delta: 7}, time.Second)
		require.NoError(t, err)
		assert.Equal(t, 7, resp.(*counterState).Value)
	})

	t.Run("snapshot interval 0 writes no intermediate snapshot", func(t *testing.T) {
		ctx := context.Background()
		eventsStore := persistence.NewMemoryEventsStore()
		snapStore := newSpySnapshotStore()
		sys := newEventSourcingTestSystem(t, eventsStore, snapStore, &counterBehavior{})

		pid, err := sys.SpawnEventSourced(ctx, "counter-interval-0", &counterBehavior{},
			WithSnapshotCriteria(&persistence.SnapshotCriteria{SnapshotInterval: 0}))
		require.NoError(t, err)

		for i := 0; i < 5; i++ {
			_, err = Ask(ctx, pid, &counterCmd{Delta: 1}, time.Second)
			require.NoError(t, err)
		}

		// Give any (incorrectly scheduled) async writer time to land.
		pause.For(200 * time.Millisecond)
		assert.Equal(t, 0, snapStore.writeCount("counter-interval-0"),
			"interval=0 must not write intermediate snapshots")
	})

	t.Run("delete events on snapshot with zero retention deletes everything up to snapshot seq", func(t *testing.T) {
		ctx := context.Background()
		eventsStore := newSpyEventsStore()
		snapStore := persistence.NewMemorySnapshotStore()
		sys := newEventSourcingTestSystem(t, eventsStore, snapStore, &counterBehavior{})

		pid, err := sys.SpawnEventSourced(ctx, "counter-del-events", &counterBehavior{},
			WithSnapshotCriteria(&persistence.SnapshotCriteria{
				SnapshotInterval:       3,
				DeleteEventsOnSnapshot: true,
			}))
		require.NoError(t, err)

		for i := 0; i < 3; i++ {
			_, err = Ask(ctx, pid, &counterCmd{Delta: 1}, time.Second)
			require.NoError(t, err)
		}

		pause.For(200 * time.Millisecond)

		// Snapshot was at seq 3 and retention is 0 → all events ≤ 3 deleted.
		require.Equal(t, []uint64{3}, eventsStore.deleteToSeqs("counter-del-events"))

		remaining, err := eventsStore.ReplayEvents(ctx, "counter-del-events", 1, 0, 0)
		require.NoError(t, err)
		assert.Empty(t, remaining, "all events should be compacted after snapshot")
	})

	t.Run("delete events on snapshot with N retention keeps the last N events", func(t *testing.T) {
		ctx := context.Background()
		eventsStore := newSpyEventsStore()
		snapStore := persistence.NewMemorySnapshotStore()
		sys := newEventSourcingTestSystem(t, eventsStore, snapStore, &counterBehavior{})

		pid, err := sys.SpawnEventSourced(ctx, "counter-retain-2", &counterBehavior{},
			WithSnapshotCriteria(&persistence.SnapshotCriteria{
				SnapshotInterval:       5,
				DeleteEventsOnSnapshot: true,
				EventsRetentionCount:   2,
			}))
		require.NoError(t, err)

		for i := 0; i < 5; i++ {
			_, err = Ask(ctx, pid, &counterCmd{Delta: 1}, time.Second)
			require.NoError(t, err)
		}

		pause.For(200 * time.Millisecond)

		// Snapshot at seq 5, retention 2 → delete events ≤ 3, keep seq 4 and 5.
		require.Equal(t, []uint64{3}, eventsStore.deleteToSeqs("counter-retain-2"))

		remaining, err := eventsStore.ReplayEvents(ctx, "counter-retain-2", 1, 0, 0)
		require.NoError(t, err)
		require.Len(t, remaining, 2)
		assert.Equal(t, uint64(4), remaining[0].SequenceNumber)
		assert.Equal(t, uint64(5), remaining[1].SequenceNumber)
	})

	t.Run("delete events on snapshot is a no-op when retention exceeds snapshot seq", func(t *testing.T) {
		ctx := context.Background()
		eventsStore := newSpyEventsStore()
		snapStore := persistence.NewMemorySnapshotStore()
		sys := newEventSourcingTestSystem(t, eventsStore, snapStore, &counterBehavior{})

		pid, err := sys.SpawnEventSourced(ctx, "counter-retain-big", &counterBehavior{},
			WithSnapshotCriteria(&persistence.SnapshotCriteria{
				SnapshotInterval:       2,
				DeleteEventsOnSnapshot: true,
				EventsRetentionCount:   100,
			}))
		require.NoError(t, err)

		for i := 0; i < 2; i++ {
			_, err = Ask(ctx, pid, &counterCmd{Delta: 1}, time.Second)
			require.NoError(t, err)
		}

		pause.For(200 * time.Millisecond)

		// retention > seq → never call DeleteEvents and never lose events.
		assert.Empty(t, eventsStore.deleteToSeqs("counter-retain-big"))

		remaining, err := eventsStore.ReplayEvents(ctx, "counter-retain-big", 1, 0, 0)
		require.NoError(t, err)
		assert.Len(t, remaining, 2)
	})

	t.Run("delete snapshots on snapshot invokes DeleteSnapshots up to seq-1", func(t *testing.T) {
		ctx := context.Background()
		eventsStore := persistence.NewMemoryEventsStore()
		snapStore := newSpySnapshotStore()
		sys := newEventSourcingTestSystem(t, eventsStore, snapStore, &counterBehavior{})

		pid, err := sys.SpawnEventSourced(ctx, "counter-del-snaps", &counterBehavior{},
			WithSnapshotCriteria(&persistence.SnapshotCriteria{
				SnapshotInterval:          2,
				DeleteSnapshotsOnSnapshot: true,
			}))
		require.NoError(t, err)

		for i := 0; i < 4; i++ {
			_, err = Ask(ctx, pid, &counterCmd{Delta: 1}, time.Second)
			require.NoError(t, err)
		}

		pause.For(200 * time.Millisecond)

		// Two intermediate snapshots fire at seq 2 and seq 4; each should
		// request deletion of all older snapshots (toSeq = seq - 1).
		assert.Equal(t, []uint64{1, 3}, snapStore.deleteToSeqs("counter-del-snaps"))
	})

	t.Run("final snapshot on PostStop applies retention policy", func(t *testing.T) {
		ctx := context.Background()
		eventsStore := newSpyEventsStore()
		snapStore := newSpySnapshotStore()
		sys := newEventSourcingTestSystem(t, eventsStore, snapStore, &counterBehavior{})

		// No SnapshotInterval → no intermediate writes; only the PostStop snapshot.
		pid, err := sys.SpawnEventSourced(ctx, "counter-final-retention", &counterBehavior{},
			WithSnapshotCriteria(&persistence.SnapshotCriteria{
				DeleteEventsOnSnapshot:    true,
				EventsRetentionCount:      1,
				DeleteSnapshotsOnSnapshot: true,
			}))
		require.NoError(t, err)

		for i := 0; i < 3; i++ {
			_, err = Ask(ctx, pid, &counterCmd{Delta: 1}, time.Second)
			require.NoError(t, err)
		}

		require.NoError(t, sys.Kill(ctx, "counter-final-retention"))
		pause.For(300 * time.Millisecond)

		// No intermediate snapshots: only the final PostStop write.
		assert.Equal(t, 1, snapStore.writeCount("counter-final-retention"))

		// Retention 1 at seq 3 → delete events ≤ 2.
		require.Equal(t, []uint64{2}, eventsStore.deleteToSeqs("counter-final-retention"))

		// DeleteSnapshots is called with seq-1 = 2.
		require.Equal(t, []uint64{2}, snapStore.deleteToSeqs("counter-final-retention"))

		remaining, err := eventsStore.ReplayEvents(ctx, "counter-final-retention", 1, 0, 0)
		require.NoError(t, err)
		require.Len(t, remaining, 1)
		assert.Equal(t, uint64(3), remaining[0].SequenceNumber)
	})

	t.Run("snapshot write failure publishes SnapshotWriteFailed event", func(t *testing.T) {
		ctx := context.Background()
		eventsStore := persistence.NewMemoryEventsStore()
		snapStore := newSpySnapshotStore()
		snapStore.setWriteErr(errors.New("snap store unavailable"))
		sys := newEventSourcingTestSystem(t, eventsStore, snapStore, &counterBehavior{})

		sub, err := sys.Subscribe()
		require.NoError(t, err)
		t.Cleanup(func() { _ = sys.Unsubscribe(sub) })

		pid, err := sys.SpawnEventSourced(ctx, "counter-snap-fail", &counterBehavior{},
			WithSnapshotCriteria(&persistence.SnapshotCriteria{SnapshotInterval: 2}))
		require.NoError(t, err)

		for i := 0; i < 2; i++ {
			_, err = Ask(ctx, pid, &counterCmd{Delta: 1}, time.Second)
			require.NoError(t, err)
		}

		got := waitForFailureEvent[*SnapshotWriteFailed](t, sub, "counter-snap-fail", 2*time.Second)
		require.NotNil(t, got)
		assert.Equal(t, "counter-snap-fail", got.PersistenceID())
		assert.Equal(t, uint64(2), got.SequenceNumber())
		require.Error(t, got.Cause())
		assert.Contains(t, got.Cause().Error(), "snap store unavailable")
	})

	t.Run("events delete failure publishes EventsDeleteFailed event", func(t *testing.T) {
		ctx := context.Background()
		eventsStore := newSpyEventsStore()
		eventsStore.setDeleteErr(errors.New("delete unavailable"))
		snapStore := persistence.NewMemorySnapshotStore()
		sys := newEventSourcingTestSystem(t, eventsStore, snapStore, &counterBehavior{})

		sub, err := sys.Subscribe()
		require.NoError(t, err)
		t.Cleanup(func() { _ = sys.Unsubscribe(sub) })

		pid, err := sys.SpawnEventSourced(ctx, "counter-del-events-fail", &counterBehavior{},
			WithSnapshotCriteria(&persistence.SnapshotCriteria{
				SnapshotInterval:       2,
				DeleteEventsOnSnapshot: true,
			}))
		require.NoError(t, err)

		for i := 0; i < 2; i++ {
			_, err = Ask(ctx, pid, &counterCmd{Delta: 1}, time.Second)
			require.NoError(t, err)
		}

		got := waitForFailureEvent[*EventsDeleteFailed](t, sub, "counter-del-events-fail", 2*time.Second)
		require.NotNil(t, got)
		assert.Equal(t, "counter-del-events-fail", got.PersistenceID())
		assert.Equal(t, uint64(2), got.ToSequenceNumber())
		require.Error(t, got.Cause())
		assert.Contains(t, got.Cause().Error(), "delete unavailable")
	})

	t.Run("snapshot delete failure publishes SnapshotDeleteFailed event", func(t *testing.T) {
		ctx := context.Background()
		eventsStore := persistence.NewMemoryEventsStore()
		snapStore := newSpySnapshotStore()
		snapStore.setDeleteErr(errors.New("delete snapshots unavailable"))
		sys := newEventSourcingTestSystem(t, eventsStore, snapStore, &counterBehavior{})

		sub, err := sys.Subscribe()
		require.NoError(t, err)
		t.Cleanup(func() { _ = sys.Unsubscribe(sub) })

		pid, err := sys.SpawnEventSourced(ctx, "counter-del-snaps-fail", &counterBehavior{},
			WithSnapshotCriteria(&persistence.SnapshotCriteria{
				SnapshotInterval:          2,
				DeleteSnapshotsOnSnapshot: true,
			}))
		require.NoError(t, err)

		for i := 0; i < 2; i++ {
			_, err = Ask(ctx, pid, &counterCmd{Delta: 1}, time.Second)
			require.NoError(t, err)
		}

		got := waitForFailureEvent[*SnapshotDeleteFailed](t, sub, "counter-del-snaps-fail", 2*time.Second)
		require.NotNil(t, got)
		assert.Equal(t, "counter-del-snaps-fail", got.PersistenceID())
		assert.Equal(t, uint64(1), got.ToSequenceNumber())
		require.Error(t, got.Cause())
		assert.Contains(t, got.Cause().Error(), "delete snapshots unavailable")
	})
}

// TestEventSourcedRelocation runs a three-node NATS-clustered actor system,
// spawns an event-sourced actor on node2, drives it, then stops node2. It
// asserts the cluster relocates the actor to a live node and that the
// relocated actor recovers its state from the shared events store.
func TestEventSourcedRelocation(t *testing.T) {
	ctx := context.TODO()
	srv := startNatsServer(t)

	// Shared events store across all nodes.
	sharedEvents := persistence.NewMemoryEventsStore()

	node1 := startEventSourcedNATsNode(t, srv.Addr().String(), sharedEvents, nil, &counterBehavior{})
	require.NotNil(t, node1)

	node2 := startEventSourcedNATsNode(t, srv.Addr().String(), sharedEvents, nil, &counterBehavior{})
	require.NotNil(t, node2)

	node3 := startEventSourcedNATsNode(t, srv.Addr().String(), sharedEvents, nil, &counterBehavior{})
	require.NotNil(t, node3)

	// Spawn on node2. WithLongLived prevents passivation before relocation.
	actorName := "counter-reloc"
	pid, err := node2.SpawnEventSourced(ctx, actorName, &counterBehavior{}, WithLongLived())
	require.NoError(t, err)
	require.NotNil(t, pid)

	for i := 0; i < 3; i++ {
		resp, err := Ask(ctx, pid, &counterCmd{Delta: 5}, time.Second)
		require.NoError(t, err)
		assert.IsType(t, &counterState{}, resp)
	}

	pause.For(time.Second)

	// Verify the actor is on node2 before shutdown.
	node2Address := net.JoinHostPort(node2.Host(), strconv.Itoa(node2.Port()))
	actorPID, err := node1.ActorOf(ctx, actorName)
	require.NoError(t, err)
	require.NotNil(t, actorPID)
	require.Equal(t, node2Address, actorPID.Path().HostPort(),
		"actor %s should be on node2 before shutdown", actorName)

	// Take node2 down. sharedEvents persists; the actor recovers from it.
	require.NoError(t, node2.Stop(ctx))

	// Allow the cluster to detect node2 leaving.
	pause.For(2 * time.Second)

	// Wait for the actor to materialize on a live node with a new address.
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, actorName)
		if err != nil || !exists {
			return false
		}
		relocated, err := node1.ActorOf(ctx, actorName)
		if err != nil || relocated == nil {
			return false
		}
		return relocated.Path().HostPort() != node2Address
	}, 2*time.Minute, 500*time.Millisecond,
		"actor %s should be relocated from node2 (was %s) to a live node", actorName, node2Address)

	// Verify recovered state. Retry to absorb the PreStart replay window.
	require.Eventually(t, func() bool {
		relocated, err := node1.ActorOf(ctx, actorName)
		if err != nil || relocated == nil {
			return false
		}
		resp, err := Ask(ctx, relocated, &noopCmd{}, 2*time.Second)
		if err != nil {
			return false
		}
		state, ok := resp.(*counterState)
		return ok && state.Value == 15
	}, 30*time.Second, 500*time.Millisecond,
		"relocated actor must recover its accumulated state (Value=15) from the shared event log")

	// The children guard must survive relocation: the relocated PID also rejects SpawnChild.
	relocated, err := node1.ActorOf(ctx, actorName)
	require.NoError(t, err)
	require.NotNil(t, relocated)
	if relocated.IsLocal() {
		child, err := relocated.SpawnChild(ctx, "child-after-reloc", NewMockActor())
		assert.ErrorIs(t, err, gerrors.ErrEventSourcedChildrenNotAllowed)
		assert.Nil(t, child)
	}

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	srv.Shutdown()
}

// startEventSourcedNATsNode starts a NATS-clustered ActorSystem wired with
// event sourcing. Nodes that share eventsStore and snapshotStore see the same
// persisted state, allowing relocated actors to recover on the receiver.
func startEventSourcedNATsNode(
	t *testing.T,
	natsAddr string,
	eventsStore persistence.EventsStore,
	snapshotStore persistence.SnapshotStore,
	behaviors ...EventSourcedBehavior,
) ActorSystem {
	t.Helper()
	ctx := context.TODO()

	ports := dynaport.Get(3)
	discoveryPort, remotingPort, peersPort := ports[0], ports[1], ports[2]
	host := "127.0.0.1"

	provider := nats.NewDiscovery(&nats.Config{
		NatsServer:    "nats://" + natsAddr,
		NatsSubject:   "some-subject",
		Host:          host,
		DiscoveryPort: discoveryPort,
	}, nats.WithLogger(log.DiscardLogger))

	clusterConfig := NewClusterConfig().
		WithKinds(&eventSourcedActor{}).
		WithPartitionCount(7).
		WithReplicaCount(1).
		WithPeersPort(peersPort).
		WithMinimumPeersQuorum(1).
		WithDiscoveryPort(discoveryPort).
		WithBootstrapTimeout(time.Second).
		WithClusterStateSyncInterval(300 * time.Millisecond).
		WithClusterBalancerInterval(100 * time.Millisecond).
		WithDiscovery(provider)

	var esOpts []EventSourcingOption
	if snapshotStore != nil {
		esOpts = append(esOpts, WithSnapshotStore(snapshotStore))
	}

	options := []Option{
		WithLogger(log.DiscardLogger),
		WithShutdownTimeout(3 * time.Minute),
		WithCluster(clusterConfig),
		WithRemote(remote.NewConfig(host, remotingPort)),
		WithEventSourcing(eventsStore, behaviors, esOpts...),
	}

	// All nodes use the same system name so they converge into one cluster.
	sys, err := NewActorSystem("accountsSystem", options...)
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))
	return sys
}

// waitForFailureEvent polls sub for a message of type T matching
// persistenceID. It returns the zero value on timeout.
func waitForFailureEvent[T interface{ PersistenceID() string }](
	t *testing.T, sub eventstream.Subscriber, persistenceID string, timeout time.Duration,
) T {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var zero T
	for time.Now().Before(deadline) {
		for msg := range sub.Iterator() {
			if e, ok := msg.Payload().(T); ok && e.PersistenceID() == persistenceID {
				return e
			}
		}
		pause.For(25 * time.Millisecond)
	}
	return zero
}

func TestSpawnEventSourced_Hardening(t *testing.T) {
	t.Run("nil behavior is rejected at the API boundary", func(t *testing.T) {
		ctx := context.Background()
		sys := newEventSourcingTestSystem(t, persistence.NewMemoryEventsStore(), nil, &counterBehavior{})

		pid, err := sys.SpawnEventSourced(ctx, "nil-behavior", nil)
		assert.ErrorIs(t, err, gerrors.ErrEventSourcedBehaviorRequired)
		assert.Nil(t, pid)
	})

	t.Run("typed-nil behavior pointer is rejected at the API boundary", func(t *testing.T) {
		ctx := context.Background()
		sys := newEventSourcingTestSystem(t, persistence.NewMemoryEventsStore(), nil, &counterBehavior{})

		// A typed-nil pointer passed through an interface is not == nil.
		var b *counterBehavior
		pid, err := sys.SpawnEventSourced(ctx, "typed-nil", b)
		assert.ErrorIs(t, err, gerrors.ErrEventSourcedBehaviorRequired)
		assert.Nil(t, pid)
	})

	t.Run("user-supplied EventSourcedBehavior in WithDependencies is rejected", func(t *testing.T) {
		ctx := context.Background()
		sys := newEventSourcingTestSystem(t, persistence.NewMemoryEventsStore(), nil, &counterBehavior{})

		pid, err := sys.SpawnEventSourced(ctx, "smuggled", &counterBehavior{},
			WithDependencies(&counterBehavior{}))
		assert.ErrorIs(t, err, gerrors.ErrEventSourcedBehaviorSmuggled)
		assert.Nil(t, pid)
	})

	t.Run("user-supplied dependency with reserved ID is rejected", func(t *testing.T) {
		ctx := context.Background()
		sys := newEventSourcingTestSystem(t, persistence.NewMemoryEventsStore(), nil, &counterBehavior{})

		// Reserved behavior ID.
		pid, err := sys.SpawnEventSourced(ctx, "reserved-bhv", &counterBehavior{},
			WithDependencies(&userDep{id: eventSourcedBehaviorDependencyID, data: "x"}))
		assert.ErrorIs(t, err, gerrors.ErrEventSourcedDependencyIDReserved)
		assert.Nil(t, pid)

		// Reserved config ID.
		pid, err = sys.SpawnEventSourced(ctx, "reserved-cfg", &counterBehavior{},
			WithDependencies(&userDep{id: eventSourcedConfigID, data: "x"}))
		assert.ErrorIs(t, err, gerrors.ErrEventSourcedDependencyIDReserved)
		assert.Nil(t, pid)
	})

	t.Run("user-supplied non-behavior dependency is preserved alongside internals", func(t *testing.T) {
		ctx := context.Background()
		sys := newEventSourcingTestSystem(t, persistence.NewMemoryEventsStore(), nil, &counterBehavior{})

		userD := &userDep{id: "my-user-dep", data: "hello"}
		pid, err := sys.SpawnEventSourced(ctx, "with-userdep", &counterBehavior{},
			WithDependencies(userD))
		require.NoError(t, err)

		// Behavior + es-config + user dep all reach the actor.
		resp, err := Ask(ctx, pid, &counterCmd{Delta: 1}, time.Second)
		require.NoError(t, err)
		assert.Equal(t, 1, resp.(*counterState).Value)
		deps := pid.Dependencies()
		var foundUser, foundBehavior, foundCfg int
		for _, d := range deps {
			switch d.ID() {
			case "my-user-dep":
				foundUser++
			case eventSourcedBehaviorDependencyID:
				foundBehavior++
			case eventSourcedConfigID:
				foundCfg++
			}
		}
		assert.Equal(t, 1, foundUser, "user dep must survive merge")
		assert.Equal(t, 1, foundBehavior, "exactly one behavior dependency must be wired")
		assert.Equal(t, 1, foundCfg, "exactly one es-config dependency must be wired")
	})

	t.Run("SpawnChild on an event-sourced PID is rejected", func(t *testing.T) {
		ctx := context.Background()
		sys := newEventSourcingTestSystem(t, persistence.NewMemoryEventsStore(), nil, &counterBehavior{})

		pid, err := sys.SpawnEventSourced(ctx, "no-children", &counterBehavior{})
		require.NoError(t, err)

		child, err := pid.SpawnChild(ctx, "child", NewMockActor())
		assert.ErrorIs(t, err, gerrors.ErrEventSourcedChildrenNotAllowed)
		assert.Nil(t, child)
	})

	t.Run("Spawn with *eventSourcedActor directly still flips the children guard", func(t *testing.T) {
		// Pin the configPID type-detection branch: when an event-sourced actor
		// is spawned via the generic Spawn (the path the cluster relocator
		// uses), the guard must still be installed.
		ctx := context.Background()
		sys := newEventSourcingTestSystem(t, persistence.NewMemoryEventsStore(), nil, &counterBehavior{})

		deps := []extension.Dependency{
			&eventSourcedDependency{inner: &counterBehavior{}},
			&eventSourcedConfig{},
		}
		pid, err := sys.Spawn(ctx, "direct-es-spawn", &eventSourcedActor{}, WithDependencies(deps...))
		require.NoError(t, err)
		require.NotNil(t, pid)

		child, err := pid.SpawnChild(ctx, "child", NewMockActor())
		assert.ErrorIs(t, err, gerrors.ErrEventSourcedChildrenNotAllowed)
		assert.Nil(t, child)
	})

	t.Run("internal snapshot writer is spawned despite the children guard", func(t *testing.T) {
		ctx := context.Background()
		eventsStore := persistence.NewMemoryEventsStore()
		snapStore := persistence.NewMemorySnapshotStore()
		sys := newEventSourcingTestSystem(t, eventsStore, snapStore, &counterBehavior{})

		pid, err := sys.SpawnEventSourced(ctx, "with-snap-writer", &counterBehavior{})
		require.NoError(t, err)

		// PostStart spawns the snapshot writer asynchronously after the
		// SpawnEventSourced call returns.
		require.Eventually(t, func() bool {
			return pid.ChildrenCount() == 1
		}, time.Second, 25*time.Millisecond,
			"framework must spawn the snapshot writer even when user children are blocked")
	})

	t.Run("RegisterEventSourcedBehavior fails when the system is not running", func(t *testing.T) {
		sys, err := NewActorSystem("rb-not-running",
			WithLogger(log.DiscardLogger),
			WithEventSourcing(persistence.NewMemoryEventsStore(), nil),
		)
		require.NoError(t, err)
		err = sys.RegisterEventSourcedBehavior(&counterBehavior{})
		assert.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
	})

	t.Run("RegisterEventSourcedBehavior fails when no events store is configured", func(t *testing.T) {
		ctx := context.Background()
		sys, err := NewActorSystem("rb-no-store", WithLogger(log.DiscardLogger))
		require.NoError(t, err)
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		err = sys.RegisterEventSourcedBehavior(&counterBehavior{})
		assert.ErrorIs(t, err, gerrors.ErrEventsStoreRequired)
	})

	t.Run("RegisterEventSourcedBehavior rejects a nil behavior", func(t *testing.T) {
		ctx := context.Background()
		sys := newEventSourcingTestSystem(t, persistence.NewMemoryEventsStore(), nil)
		_ = ctx
		err := sys.RegisterEventSourcedBehavior(nil)
		assert.ErrorIs(t, err, gerrors.ErrEventSourcedBehaviorRequired)
	})

	t.Run("SpawnEventSourced fails when the system is not running", func(t *testing.T) {
		sys, err := NewActorSystem("es-not-running",
			WithLogger(log.DiscardLogger),
			WithEventSourcing(persistence.NewMemoryEventsStore(), []EventSourcedBehavior{&counterBehavior{}}),
		)
		require.NoError(t, err)
		pid, err := sys.SpawnEventSourced(context.Background(), "x", &counterBehavior{})
		assert.ErrorIs(t, err, gerrors.ErrActorSystemNotStarted)
		assert.Nil(t, pid)
	})

	t.Run("SpawnEventSourced fails for an unregistered behavior type", func(t *testing.T) {
		ctx := context.Background()
		sys := newEventSourcingTestSystem(t, persistence.NewMemoryEventsStore(), nil)
		pid, err := sys.SpawnEventSourced(ctx, "unreg", &noRegBehavior{})
		assert.ErrorIs(t, err, gerrors.ErrEventSourcedBehaviorNotRegistered)
		assert.Nil(t, pid)
	})

	t.Run("SpawnEventSourced surfaces spawn-config validation errors", func(t *testing.T) {
		ctx := context.Background()
		sys := newEventSourcingTestSystem(t, persistence.NewMemoryEventsStore(), nil, &counterBehavior{})

		// A dependency with a blank ID fails spawnConfig.Validate.
		pid, err := sys.SpawnEventSourced(ctx, "cfg-invalid", &counterBehavior{},
			WithDependencies(&userDep{id: "", data: "x"}))
		require.Error(t, err)
		assert.Nil(t, pid)
	})
}

// noRegBehavior is intentionally never registered, used to exercise the
// "behavior type not registered" error path.
type noRegBehavior struct{}

func (n *noRegBehavior) InitialState() any { return &counterState{} }
func (n *noRegBehavior) HandleCommand(_ context.Context, _ any, _ any) ([]any, error) {
	return nil, nil
}
func (n *noRegBehavior) HandleEvent(_ context.Context, _ any, state any) (any, error) {
	return state, nil
}
func (n *noRegBehavior) MarshalBinary() ([]byte, error) { return nil, nil }
func (n *noRegBehavior) UnmarshalBinary([]byte) error   { return nil }

func TestBehaviorDependency_RoundTrip(t *testing.T) {
	t.Run("marshal then unmarshal restores the inner behavior type", func(t *testing.T) {
		// statefulBehavior must be in the global registry for UnmarshalBinary
		// to locate it (WithEventSourcing does this in production).
		types.GlobalRegistry.Register(&statefulBehavior{})

		// Behaviors travel by type only; per-instance fields are zero on the
		// receiver.
		original := &eventSourcedDependency{inner: &statefulBehavior{Multiplier: 42}}
		data, err := original.MarshalBinary()
		require.NoError(t, err)
		require.NotEmpty(t, data)

		decoded := &eventSourcedDependency{}
		require.NoError(t, decoded.UnmarshalBinary(data))

		inner, ok := decoded.inner.(*statefulBehavior)
		require.True(t, ok, "decoded inner must be *statefulBehavior")
		assert.Equal(t, 0, inner.Multiplier,
			"behaviors travel by type only; per-instance fields must be zero on the receiver")
	})

	t.Run("unmarshal fails when the inner type is not registered", func(t *testing.T) {
		data, err := proto.Marshal(&internalpb.EventSourcedBehaviorEnvelope{
			TypeName: "no.such.behavior",
			Payload:  []byte("anything"),
		})
		require.NoError(t, err)

		dep := &eventSourcedDependency{}
		err = dep.UnmarshalBinary(data)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not registered on this node")
	})

	t.Run("marshal rejects a nil inner behavior", func(t *testing.T) {
		dep := &eventSourcedDependency{}
		_, err := dep.MarshalBinary()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "inner behavior is nil")
	})

	t.Run("ID is stable and matches the reserved constant", func(t *testing.T) {
		dep := &eventSourcedDependency{}
		assert.Equal(t, eventSourcedBehaviorDependencyID, dep.ID())
	})

	t.Run("unmarshal fails on malformed envelope bytes", func(t *testing.T) {
		dep := &eventSourcedDependency{}
		err := dep.UnmarshalBinary([]byte("not-a-valid-protobuf"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshal envelope")
	})

	t.Run("unmarshal fails when the envelope type name is empty", func(t *testing.T) {
		data, err := proto.Marshal(&internalpb.EventSourcedBehaviorEnvelope{})
		require.NoError(t, err)

		dep := &eventSourcedDependency{}
		err = dep.UnmarshalBinary(data)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty type name")
	})

	t.Run("unmarshal fails when the registered type does not implement EventSourcedBehavior", func(t *testing.T) {
		// userDep is a Dependency but not an EventSourcedBehavior.
		types.GlobalRegistry.Register(&userDep{})
		data, err := proto.Marshal(&internalpb.EventSourcedBehaviorEnvelope{
			TypeName: types.Name(&userDep{}),
		})
		require.NoError(t, err)

		dep := &eventSourcedDependency{}
		err = dep.UnmarshalBinary(data)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not implement EventSourcedBehavior")
	})
}

func TestEventSourcedConfig_RoundTrip(t *testing.T) {
	t.Run("MarshalBinary returns empty bytes when criteria is nil", func(t *testing.T) {
		cfg := &eventSourcedConfig{}
		data, err := cfg.MarshalBinary()
		require.NoError(t, err)
		assert.Empty(t, data)
	})

	t.Run("MarshalBinary then UnmarshalBinary preserves criteria", func(t *testing.T) {
		original := &eventSourcedConfig{
			criteria: &persistence.SnapshotCriteria{
				SnapshotInterval:          5,
				DeleteEventsOnSnapshot:    true,
				DeleteSnapshotsOnSnapshot: true,
				EventsRetentionCount:      2,
			},
		}
		data, err := original.MarshalBinary()
		require.NoError(t, err)
		require.NotEmpty(t, data)

		decoded := &eventSourcedConfig{}
		require.NoError(t, decoded.UnmarshalBinary(data))
		require.NotNil(t, decoded.criteria)
		assert.Equal(t, uint64(5), decoded.criteria.SnapshotInterval)
		assert.True(t, decoded.criteria.DeleteEventsOnSnapshot)
		assert.True(t, decoded.criteria.DeleteSnapshotsOnSnapshot)
		assert.Equal(t, uint64(2), decoded.criteria.EventsRetentionCount)
	})

	t.Run("UnmarshalBinary on empty bytes clears criteria", func(t *testing.T) {
		cfg := &eventSourcedConfig{criteria: &persistence.SnapshotCriteria{SnapshotInterval: 99}}
		require.NoError(t, cfg.UnmarshalBinary(nil))
		assert.Nil(t, cfg.criteria)
	})

	t.Run("UnmarshalBinary fails on malformed bytes", func(t *testing.T) {
		cfg := &eventSourcedConfig{}
		err := cfg.UnmarshalBinary([]byte("not-a-valid-protobuf"))
		require.Error(t, err)
	})
}

func TestWithEventSourcedBehaviorOption(t *testing.T) {
	t.Run("nil behavior is ignored", func(t *testing.T) {
		cfg := &eventSourcingConfig{}
		WithEventSourcedBehavior(nil)(cfg)
		assert.Empty(t, cfg.behaviors)
	})

	t.Run("non-nil behavior accumulates", func(t *testing.T) {
		cfg := &eventSourcingConfig{}
		WithEventSourcedBehavior(&counterBehavior{})(cfg)
		WithEventSourcedBehavior(&counterBehavior{})(cfg)
		assert.Len(t, cfg.behaviors, 2)
	})
}

func TestEventSourcedRecoveryFailures(t *testing.T) {
	t.Run("recovery fails when an event in the log cannot be applied", func(t *testing.T) {
		ctx := context.Background()
		store := persistence.NewMemoryEventsStore()

		// Plant a poison event the behavior's HandleEvent will reject.
		s, kind := serializerFor(&poisonEvent{})
		payload, err := s.Serialize(&poisonEvent{})
		require.NoError(t, err)
		require.NoError(t, store.WriteEvents(ctx, []*persistence.PersistedEvent{{
			PersistenceID:  "counter-bad-event",
			SequenceNumber: 1,
			Timestamp:      time.Now(),
			Payload:        payload,
			Manifest:       types.Name(&poisonEvent{}),
			SerializerKind: kind,
		}}))

		sys := newEventSourcingTestSystem(t, store, nil, &counterBehavior{})
		pid, err := sys.SpawnEventSourced(ctx, "counter-bad-event", &counterBehavior{})
		require.Error(t, err)
		assert.Nil(t, pid)
		assert.Contains(t, err.Error(), "apply event seq=1")
	})

	t.Run("recovery fails when an event payload cannot be deserialized", func(t *testing.T) {
		ctx := context.Background()
		store := persistence.NewMemoryEventsStore()
		require.NoError(t, store.WriteEvents(ctx, []*persistence.PersistedEvent{{
			PersistenceID:  "counter-bad-decode",
			SequenceNumber: 1,
			Timestamp:      time.Now(),
			Payload:        []byte{0xff, 0xff, 0xff}, // garbage
			Manifest:       types.Name(&counterEvent{}),
			SerializerKind: persistence.CBORSerializerKind,
		}}))

		sys := newEventSourcingTestSystem(t, store, nil, &counterBehavior{})
		pid, err := sys.SpawnEventSourced(ctx, "counter-bad-decode", &counterBehavior{})
		require.Error(t, err)
		assert.Nil(t, pid)
		assert.Contains(t, err.Error(), "deserialize event seq=1")
	})

	t.Run("recovery fails when the latest snapshot payload cannot be deserialized", func(t *testing.T) {
		ctx := context.Background()
		eventsStore := persistence.NewMemoryEventsStore()
		snapStore := persistence.NewMemorySnapshotStore()
		require.NoError(t, snapStore.WriteSnapshot(ctx, &persistence.PersistedSnapshot{
			PersistenceID:  "counter-bad-snap",
			SequenceNumber: 1,
			Timestamp:      time.Now(),
			Payload:        []byte{0xff, 0xff, 0xff}, // garbage
			Manifest:       types.Name(&counterState{}),
			SerializerKind: persistence.CBORSerializerKind,
		}))

		sys := newEventSourcingTestSystem(t, eventsStore, snapStore, &counterBehavior{})
		pid, err := sys.SpawnEventSourced(ctx, "counter-bad-snap", &counterBehavior{})
		require.Error(t, err)
		assert.Nil(t, pid)
		assert.Contains(t, err.Error(), "deserialize snapshot")
	})
}

func TestSerializerHelpers(t *testing.T) {
	t.Run("serializerFor on a proto.Message returns the proto serializer", func(t *testing.T) {
		s, kind := serializerFor(&internalpb.SnapshotSpec{})
		require.NotNil(t, s)
		assert.Equal(t, persistence.ProtobufSerializerKind, kind)
	})

	t.Run("serializerFor on a non-proto value returns CBOR", func(t *testing.T) {
		s, kind := serializerFor(&counterState{})
		require.NotNil(t, s)
		assert.Equal(t, persistence.CBORSerializerKind, kind)
	})

	t.Run("serializerByKind selects proto for proto kind", func(t *testing.T) {
		require.NotNil(t, serializerByKind(persistence.ProtobufSerializerKind))
	})

	t.Run("serializerByKind selects CBOR for CBOR kind", func(t *testing.T) {
		require.NotNil(t, serializerByKind(persistence.CBORSerializerKind))
	})
}

func TestPublishFailureNilStream(t *testing.T) {
	// Calling publishFailure with a nil stream must be a no-op (no panic).
	publishFailure(nil, NewSnapshotWriteFailed("anything", 0, errors.New("x")))
}

// valueBehavior satisfies EventSourcedBehavior with value receivers, used to
// exercise the non-nilable-kind branch in isBehaviorNil.
type valueBehavior struct{}

func (v valueBehavior) InitialState() any                                            { return nil }
func (v valueBehavior) HandleCommand(_ context.Context, _ any, _ any) ([]any, error) { return nil, nil }
func (v valueBehavior) HandleEvent(_ context.Context, _ any, state any) (any, error) {
	return state, nil
}
func (v valueBehavior) MarshalBinary() ([]byte, error) { return nil, nil }
func (v valueBehavior) UnmarshalBinary(_ []byte) error { return nil }

func TestIsBehaviorNil(t *testing.T) {
	t.Run("untyped-nil interface returns true", func(t *testing.T) {
		assert.True(t, isBehaviorNil(nil))
	})
	t.Run("typed-nil pointer returns true", func(t *testing.T) {
		var b *counterBehavior
		assert.True(t, isBehaviorNil(b))
	})
	t.Run("non-nil pointer returns false", func(t *testing.T) {
		assert.False(t, isBehaviorNil(&counterBehavior{}))
	})
	t.Run("non-pointer struct value returns false", func(t *testing.T) {
		assert.False(t, isBehaviorNil(valueBehavior{}))
	})
}

func TestCheckEventSourcedDependencies_SkipsNilEntries(t *testing.T) {
	// nil entries are skipped without affecting the check outcome.
	assert.NoError(t, checkEventSourcedDependencies([]extension.Dependency{nil}))
}

func TestEventSourcedActor_PreStartValidation(t *testing.T) {
	t.Run("fails when no behavior dependency is injected", func(t *testing.T) {
		ctx := context.Background()
		sys := newEventSourcingTestSystem(t, persistence.NewMemoryEventsStore(), nil, &counterBehavior{})

		// Spawn the wrapper directly with only the es-config dependency.
		pid, err := sys.Spawn(ctx, "no-behavior-dep", &eventSourcedActor{},
			WithDependencies(&eventSourcedConfig{}))
		require.Error(t, err)
		assert.Nil(t, pid)
		assert.Contains(t, err.Error(), "behavior dependency missing")
	})

	t.Run("fails when the behavior dependency is malformed", func(t *testing.T) {
		ctx := context.Background()
		sys := newEventSourcingTestSystem(t, persistence.NewMemoryEventsStore(), nil, &counterBehavior{})

		// A wrapper with no inner behavior is malformed.
		pid, err := sys.Spawn(ctx, "malformed-behavior-dep", &eventSourcedActor{},
			WithDependencies(&eventSourcedDependency{}))
		require.Error(t, err)
		assert.Nil(t, pid)
		assert.Contains(t, err.Error(), "malformed")
	})
}

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

type spyEventsStore struct {
	*persistence.MemoryEventsStore
	mu        sync.Mutex
	deletes   map[string][]uint64
	deleteErr error
	writeErr  error
}

func newSpyEventsStore() *spyEventsStore {
	return &spyEventsStore{
		MemoryEventsStore: persistence.NewMemoryEventsStore(),
		deletes:           make(map[string][]uint64),
	}
}

func (s *spyEventsStore) setDeleteErr(err error) {
	s.mu.Lock()
	s.deleteErr = err
	s.mu.Unlock()
}

func (s *spyEventsStore) setWriteErr(err error) {
	s.mu.Lock()
	s.writeErr = err
	s.mu.Unlock()
}

func (s *spyEventsStore) WriteEvents(ctx context.Context, events []*persistence.PersistedEvent) error {
	s.mu.Lock()
	err := s.writeErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.MemoryEventsStore.WriteEvents(ctx, events)
}

func (s *spyEventsStore) DeleteEvents(ctx context.Context, persistenceID string, toSeq uint64) error {
	s.mu.Lock()
	s.deletes[persistenceID] = append(s.deletes[persistenceID], toSeq)
	err := s.deleteErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.MemoryEventsStore.DeleteEvents(ctx, persistenceID, toSeq)
}

func (s *spyEventsStore) deleteToSeqs(persistenceID string) []uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]uint64, len(s.deletes[persistenceID]))
	copy(out, s.deletes[persistenceID])
	return out
}

type spySnapshotStore struct {
	*persistence.MemorySnapshotStore
	mu        sync.Mutex
	writes    map[string]int
	deletes   map[string][]uint64
	writeErr  error
	deleteErr error
}

func newSpySnapshotStore() *spySnapshotStore {
	return &spySnapshotStore{
		MemorySnapshotStore: persistence.NewMemorySnapshotStore(),
		writes:              make(map[string]int),
		deletes:             make(map[string][]uint64),
	}
}

func (s *spySnapshotStore) setWriteErr(err error) {
	s.mu.Lock()
	s.writeErr = err
	s.mu.Unlock()
}

func (s *spySnapshotStore) setDeleteErr(err error) {
	s.mu.Lock()
	s.deleteErr = err
	s.mu.Unlock()
}

func (s *spySnapshotStore) WriteSnapshot(ctx context.Context, snap *persistence.PersistedSnapshot) error {
	s.mu.Lock()
	s.writes[snap.PersistenceID]++
	err := s.writeErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.MemorySnapshotStore.WriteSnapshot(ctx, snap)
}

func (s *spySnapshotStore) DeleteSnapshots(ctx context.Context, persistenceID string, toSeq uint64) error {
	s.mu.Lock()
	s.deletes[persistenceID] = append(s.deletes[persistenceID], toSeq)
	err := s.deleteErr
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.MemorySnapshotStore.DeleteSnapshots(ctx, persistenceID, toSeq)
}

func (s *spySnapshotStore) writeCount(persistenceID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes[persistenceID]
}

func (s *spySnapshotStore) deleteToSeqs(persistenceID string) []uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]uint64, len(s.deletes[persistenceID]))
	copy(out, s.deletes[persistenceID])
	return out
}
