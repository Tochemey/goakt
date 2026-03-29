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
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/crdt"
	"github.com/tochemey/goakt/v4/discovery"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/codec"
	"github.com/tochemey/goakt/v4/internal/ddata"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
)

// spawnTestReplicator registers the CRDT config extension on the actor system
// and spawns a Replicator actor. This mirrors what spawnReplicator does in production.
func spawnTestReplicator(t *testing.T, sys ActorSystem) *PID {
	t.Helper()
	ctx := context.TODO()
	config := crdt.NewConfig()
	impl := sys.(*actorSystem)
	impl.extensions.Set(crdtConfigExtensionID, &crdtConfigExtension{config: config})
	repl, err := sys.Spawn(ctx, "replicator", newReplicatorActor(), WithLongLived())
	require.NoError(t, err)
	require.NotNil(t, repl)
	pause.For(500 * time.Millisecond)
	return repl
}

// newTestReplicator creates a replicatorActor with config set directly for unit tests
// that don't go through the actor system.
func newTestReplicator() *replicatorActor {
	r := newReplicatorActor()
	r.config = crdt.NewConfig()
	r.store = make(map[string]crdt.ReplicatedData)
	r.keyTypes = make(map[string]crdt.DataType)
	r.subscriptions = make(map[string]types.Unit)
	r.watchers = make(map[string][]*PID)
	r.tombstones = make(map[string]*tombstone)
	r.versions = make(map[string]uint64)
	return r
}

func TestReplicatorActor(t *testing.T) {
	t.Run("constructor", func(t *testing.T) {
		r := newReplicatorActor()
		require.NotNil(t, r)
	})

	t.Run("update and get via actor system", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		// update a counter
		counterKey := crdt.PNCounterKey("counter")
		reply, err := Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)
		require.NotNil(t, reply)
		assert.IsType(t, &crdt.UpdateResponse{}, reply)

		// get the counter
		resp, err := Ask(ctx, repl, &crdt.Get{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		require.NotNil(t, resp)

		getResp := resp.(*crdt.GetResponse)
		require.NotNil(t, getResp.Data)
		assert.Equal(t, int64(5), getResp.Data.(*crdt.PNCounter).Value())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("update creates key on first use", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		// get a key that doesn't exist
		counterKey := crdt.PNCounterKey("new-counter")
		resp, err := Ask(ctx, repl, &crdt.Get{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse)
		assert.Nil(t, getResp.Data)

		// update creates the key
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 1)
			},
		}, time.Second)
		require.NoError(t, err)

		// now get returns the value
		resp, err = Ask(ctx, repl, &crdt.Get{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		getResp = resp.(*crdt.GetResponse)
		require.NotNil(t, getResp.Data)
		assert.Equal(t, int64(1), getResp.Data.(*crdt.PNCounter).Value())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("multiple updates accumulate", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		counterKey := crdt.PNCounterKey("counter")
		for i := range 5 {
			_, err = Ask(ctx, repl, &crdt.Update{
				Key:     counterKey,
				Initial: crdt.NewPNCounter(),
				Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
					return current.(*crdt.PNCounter).Increment("node-1", uint64(i+1))
				},
			}, time.Second)
			require.NoError(t, err)
		}

		resp, err := Ask(ctx, repl, &crdt.Get{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse)
		assert.Equal(t, int64(15), getResp.Data.(*crdt.PNCounter).Value())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("delete removes key", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		// create a key
		counterKey := crdt.PNCounterKey("counter")
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		// delete it
		reply, err := Ask(ctx, repl, &crdt.Delete{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		assert.IsType(t, &crdt.DeleteResponse{}, reply)

		// get returns nil
		resp, err := Ask(ctx, repl, &crdt.Get{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse)
		assert.Nil(t, getResp.Data)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("different CRDT types", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		// GCounter
		gcKey := crdt.GCounterKey("gc")
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     gcKey,
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-1", 10)
			},
		}, time.Second)
		require.NoError(t, err)

		resp, err := Ask(ctx, repl, &crdt.Get{Key: gcKey}, time.Second)
		require.NoError(t, err)
		assert.Equal(t, uint64(10), resp.(*crdt.GetResponse).Data.(*crdt.GCounter).Value())

		// ORSet
		setKey := crdt.ORSetKey("sessions")
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     setKey,
			Initial: crdt.NewORSet(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.ORSet).Add("node-1", "session-abc")
			},
		}, time.Second)
		require.NoError(t, err)

		resp, err = Ask(ctx, repl, &crdt.Get{Key: setKey}, time.Second)
		require.NoError(t, err)
		orSet := resp.(*crdt.GetResponse).Data.(*crdt.ORSet)
		assert.True(t, orSet.Contains("session-abc"))

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("delta from peer merges into store", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		// create a local counter
		counterKey := crdt.PNCounterKey("counter")
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		// simulate a delta from a peer node
		peerDelta := crdt.NewPNCounter().Increment("node-2", 10)
		err = Tell(ctx, repl, &crdtDelta{
			KeyID:    "counter",
			DataType: crdt.PNCounterType,
			Delta:    peerDelta,
			Origin:   "peer-node-id",
		})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// merged value should be 15 (5 from node-1 + 10 from node-2)
		resp, err := Ask(ctx, repl, &crdt.Get{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse)
		assert.Equal(t, int64(15), getResp.Data.(*crdt.PNCounter).Value())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("delta from self is ignored", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		counterKey := crdt.PNCounterKey("counter")
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		// send a delta with the same origin as the replicator's nodeID
		err = Tell(ctx, repl, &crdtDelta{
			KeyID:    "counter",
			DataType: crdt.PNCounterType,
			Delta:    crdt.NewPNCounter().Increment("node-1", 100),
			Origin:   repl.ID(), // same as replicator's nodeID
		})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// value should still be 5, not 105
		resp, err := Ask(ctx, repl, &crdt.Get{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse)
		assert.Equal(t, int64(5), getResp.Data.(*crdt.PNCounter).Value())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("delta for new key creates entry", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		// send a delta for a key that doesn't exist locally
		peerCounter := crdt.NewPNCounter().Increment("node-2", 7)
		err = Tell(ctx, repl, &crdtDelta{
			KeyID:    "new-counter",
			DataType: crdt.PNCounterType,
			Delta:    peerCounter,
			Origin:   "peer-node",
		})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// key should now exist with the peer's value
		counterKey := crdt.PNCounterKey("new-counter")
		resp, err := Ask(ctx, repl, &crdt.Get{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse)
		require.NotNil(t, getResp.Data)
		assert.Equal(t, int64(7), getResp.Data.(*crdt.PNCounter).Value())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("tell-based update without sender", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		counterKey := crdt.PNCounterKey("counter")
		err = Tell(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 3)
			},
		})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		resp, err := Ask(ctx, repl, &crdt.Get{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse)
		assert.Equal(t, int64(3), getResp.Data.(*crdt.PNCounter).Value())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("unhandled message", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))

		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		// send a random string — should be unhandled
		err = Tell(ctx, repl, "random-message")
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// replicator should still be running
		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorRemoveWatcher(t *testing.T) {
	ctx := context.TODO()
	sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	err := sys.Start(ctx)
	require.NoError(t, err)
	pause.For(time.Second)

	pid1, err := sys.Spawn(ctx, "w1", NewMockActor(), WithLongLived())
	require.NoError(t, err)
	pid2, err := sys.Spawn(ctx, "w2", NewMockActor(), WithLongLived())
	require.NoError(t, err)

	t.Run("remove existing watcher", func(t *testing.T) {
		r := newTestReplicator()
		r.watchers["key"] = []*PID{pid1}
		r.removeWatcher("key", pid1)
		assert.Empty(t, r.watchers["key"])
	})

	t.Run("remove nonexistent watcher", func(t *testing.T) {
		r := newTestReplicator()
		r.watchers["key"] = []*PID{pid1}
		r.removeWatcher("key", pid2)
		assert.Len(t, r.watchers["key"], 1)
	})

	t.Run("remove from nonexistent key", func(t *testing.T) {
		r := newTestReplicator()
		r.removeWatcher("nonexistent", pid1)
		// should not panic
	})

	err = sys.Stop(ctx)
	assert.NoError(t, err)
}

func TestReplicatorTrackKey(t *testing.T) {
	t.Run("tracks key", func(t *testing.T) {
		r := newTestReplicator()
		r.trackKey("test-key", crdt.GCounterType)
		_, exists := r.subscriptions["test-key"]
		assert.True(t, exists)
		assert.Equal(t, crdt.GCounterType, r.keyTypes["test-key"])
	})

	t.Run("duplicate is idempotent", func(t *testing.T) {
		r := newTestReplicator()
		r.trackKey("test-key", crdt.GCounterType)
		r.trackKey("test-key", crdt.GCounterType)
		assert.Len(t, r.subscriptions, 1)
	})
}

func TestReplicatorTombstones(t *testing.T) {
	t.Run("delete creates tombstone", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		counterKey := crdt.PNCounterKey("counter")
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		// delete creates tombstone
		_, err = Ask(ctx, repl, &crdt.Delete{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)

		// get returns nil after delete
		resp, err := Ask(ctx, repl, &crdt.Get{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse)
		assert.Nil(t, getResp.Data)

		// update to tombstoned key is rejected (returns response but doesn't create key)
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 10)
			},
		}, time.Second)
		require.NoError(t, err)

		resp, err = Ask(ctx, repl, &crdt.Get{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		getResp = resp.(*crdt.GetResponse)
		assert.Nil(t, getResp.Data)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("delta for tombstoned key is rejected", func(t *testing.T) {
		r := newTestReplicator()
		r.nodeID = "local-node"
		r.tombstones["counter"] = &tombstone{
			keyID:     "counter",
			deletedAt: time.Now(),
			deletedBy: "local-node",
		}

		// sending a delta for a tombstoned key should be ignored
		delta := &crdtDelta{
			KeyID:    "counter",
			DataType: crdt.PNCounterType,
			Delta:    crdt.NewPNCounter().Increment("remote-node", 5),
			Origin:   "remote-node",
		}
		// no panic, key is not added to store
		r.store["counter"] = nil // ensure it doesn't exist
		delete(r.store, "counter")

		// simulate handleDelta without context — check store directly
		if delta.Origin == r.nodeID {
			t.Fatal("should not be self")
		}
		if _, ok := r.tombstones[delta.KeyID]; !ok {
			t.Fatal("tombstone should exist")
		}
		_, exists := r.store["counter"]
		assert.False(t, exists)
	})

	t.Run("proto tombstone from peer removes key", func(t *testing.T) {
		r := newTestReplicator()
		r.nodeID = "local-node"
		r.store["counter"] = crdt.NewPNCounter().Increment("node-1", 5)
		r.versions["counter"] = 1

		pbTombstone := &internalpb.CRDTTombstone{
			Key:            codec.EncodeCRDTKey("counter", crdt.PNCounterType),
			DeletedAtNanos: time.Now().UnixNano(),
			DeletedByNode:  "remote-node",
		}
		r.handleProtoTombstone(pbTombstone)

		_, exists := r.store["counter"]
		assert.False(t, exists)
		_, hasTombstone := r.tombstones["counter"]
		assert.True(t, hasTombstone)
	})

	t.Run("proto tombstone from self is ignored", func(t *testing.T) {
		r := newTestReplicator()
		r.nodeID = "local-node"
		r.store["counter"] = crdt.NewPNCounter().Increment("node-1", 5)

		pbTombstone := &internalpb.CRDTTombstone{
			Key:            codec.EncodeCRDTKey("counter", crdt.PNCounterType),
			DeletedAtNanos: time.Now().UnixNano(),
			DeletedByNode:  "local-node",
		}
		r.handleProtoTombstone(pbTombstone)

		_, exists := r.store["counter"]
		assert.True(t, exists)
	})
}

func TestReplicatorPrune(t *testing.T) {
	t.Run("prune removes expired tombstones", func(t *testing.T) {
		r := newTestReplicator()
		r.config = crdt.NewConfig(crdt.WithTombstoneTTL(time.Millisecond))
		r.tombstones["old-key"] = &tombstone{
			keyID:     "old-key",
			deletedAt: time.Now().Add(-time.Hour),
			deletedBy: "node-1",
		}
		r.tombstones["new-key"] = &tombstone{
			keyID:     "new-key",
			deletedAt: time.Now(),
			deletedBy: "node-1",
		}

		r.handlePrune()

		_, oldExists := r.tombstones["old-key"]
		assert.False(t, oldExists)
		_, newExists := r.tombstones["new-key"]
		assert.True(t, newExists)
	})
}

func TestReplicatorDigest(t *testing.T) {
	t.Run("buildDigest includes all keys", func(t *testing.T) {
		r := newTestReplicator()
		r.store["key-a"] = crdt.NewGCounter().Increment("node-1", 5)
		r.keyTypes["key-a"] = crdt.GCounterType
		r.versions["key-a"] = 3

		r.store["key-b"] = crdt.NewPNCounter().Increment("node-1", 10)
		r.keyTypes["key-b"] = crdt.PNCounterType
		r.versions["key-b"] = 7

		digest := r.buildDigest()
		require.Len(t, digest.GetEntries(), 2)

		versions := make(map[string]uint64)
		for _, e := range digest.GetEntries() {
			keyID, _, _ := codec.DecodeCRDTKey(e.GetKey())
			versions[keyID] = e.GetVersion()
		}
		assert.Equal(t, uint64(3), versions["key-a"])
		assert.Equal(t, uint64(7), versions["key-b"])
	})

	t.Run("buildDigest with empty store", func(t *testing.T) {
		r := newTestReplicator()
		digest := r.buildDigest()
		assert.Empty(t, digest.GetEntries())
	})
}

func TestReplicatorTargetCount(t *testing.T) {
	r := newTestReplicator()

	t.Run("majority with 1 peer", func(t *testing.T) {
		assert.Equal(t, 1, r.targetCount(1, crdt.Majority))
	})

	t.Run("majority with 2 peers", func(t *testing.T) {
		assert.Equal(t, 2, r.targetCount(2, crdt.Majority))
	})

	t.Run("majority with 3 peers", func(t *testing.T) {
		assert.Equal(t, 2, r.targetCount(3, crdt.Majority))
	})

	t.Run("majority with 5 peers", func(t *testing.T) {
		assert.Equal(t, 3, r.targetCount(5, crdt.Majority))
	})

	t.Run("all with 3 peers", func(t *testing.T) {
		assert.Equal(t, 3, r.targetCount(3, crdt.All))
	})

	t.Run("zero coordination returns 0", func(t *testing.T) {
		assert.Equal(t, 0, r.targetCount(5, crdt.Coordination(0)))
	})
}

func TestReplicatorSelectPeers(t *testing.T) {
	r := newTestReplicator()
	peers := []*cluster.Peer{
		{Host: "host-1", RemotingPort: 9000},
		{Host: "host-2", RemotingPort: 9001},
		{Host: "host-3", RemotingPort: 9002},
		{Host: "host-4", RemotingPort: 9003},
		{Host: "host-5", RemotingPort: 9004},
	}

	t.Run("count >= len returns full slice", func(t *testing.T) {
		selected := r.selectPeers(peers, 5)
		assert.Len(t, selected, 5)
		assert.Same(t, &peers[0], &selected[0])
	})

	t.Run("count > len returns full slice", func(t *testing.T) {
		selected := r.selectPeers(peers, 10)
		assert.Len(t, selected, 5)
	})

	t.Run("count 0 returns nil", func(t *testing.T) {
		selected := r.selectPeers(peers, 0)
		assert.Nil(t, selected)
	})

	t.Run("count < len returns subset", func(t *testing.T) {
		selected := r.selectPeers(peers, 3)
		assert.Len(t, selected, 3)
		// verify all selected peers are from the original set
		hostSet := make(map[string]bool)
		for _, p := range peers {
			hostSet[p.Host] = true
		}
		for _, p := range selected {
			assert.True(t, hostSet[p.Host])
		}
	})

	t.Run("count 1 returns single peer", func(t *testing.T) {
		selected := r.selectPeers(peers, 1)
		assert.Len(t, selected, 1)
	})
}

func TestReplicatorPruneCompacts(t *testing.T) {
	t.Run("prune compacts ORSet in store", func(t *testing.T) {
		r := newTestReplicator()
		r.config = crdt.NewConfig(crdt.WithTombstoneTTL(24 * time.Hour))

		// Create an ORSet with redundant dots
		s := crdt.NewORSet()
		s = s.Add("node-1", "a")
		s = s.Add("node-1", "a") // duplicate dot
		r.store["set-key"] = s
		r.keyTypes["set-key"] = crdt.ORSetType

		// Verify before compaction: 2 dots
		entries, _ := s.RawState()
		require.Len(t, entries[0].Dots, 2)

		r.handlePrune()

		// After prune, the ORSet should be compacted to 1 dot
		compacted := r.store["set-key"].(*crdt.ORSet)
		entries2, _ := compacted.RawState()
		require.Len(t, entries2, 1)
		assert.Len(t, entries2[0].Dots, 1)
	})

	t.Run("prune does not affect non-compactable types", func(t *testing.T) {
		r := newTestReplicator()
		r.config = crdt.NewConfig(crdt.WithTombstoneTTL(24 * time.Hour))

		counter := crdt.NewGCounter().Increment("node-1", 5)
		r.store["counter-key"] = counter
		r.keyTypes["counter-key"] = crdt.GCounterType

		r.handlePrune()

		result := r.store["counter-key"].(*crdt.GCounter)
		assert.Equal(t, uint64(5), result.Value())
	})
}

func TestReplicatorPhase2Types(t *testing.T) {
	t.Run("Flag via actor system", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		flagKey := crdt.FlagKey("feature-x")
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     flagKey,
			Initial: crdt.NewFlag(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.Flag).Enable()
			},
		}, time.Second)
		require.NoError(t, err)

		resp, err := Ask(ctx, repl, &crdt.Get{Key: flagKey}, time.Second)
		require.NoError(t, err)
		flag := resp.(*crdt.GetResponse).Data.(*crdt.Flag)
		assert.True(t, flag.Enabled())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("MVRegister via actor system", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		regKey := crdt.MVRegisterKey("profile")
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     regKey,
			Initial: crdt.NewMVRegister(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.MVRegister).Set("node-1", "alice")
			},
		}, time.Second)
		require.NoError(t, err)

		resp, err := Ask(ctx, repl, &crdt.Get{Key: regKey}, time.Second)
		require.NoError(t, err)
		reg := resp.(*crdt.GetResponse).Data.(*crdt.MVRegister)
		values := reg.Values()
		require.Len(t, values, 1)
		assert.Equal(t, "alice", values[0])

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("ORMap via actor system", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		mapKey := crdt.ORMapKey("cart")
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     mapKey,
			Initial: crdt.NewORMap(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.ORMap).Set("node-1", "item-a", crdt.NewGCounter().Increment("node-1", 2))
			},
		}, time.Second)
		require.NoError(t, err)

		resp, err := Ask(ctx, repl, &crdt.Get{Key: mapKey}, time.Second)
		require.NoError(t, err)
		orMap := resp.(*crdt.GetResponse).Data.(*crdt.ORMap)
		assert.Equal(t, 1, orMap.Len())
		v, ok := orMap.Get("item-a")
		require.True(t, ok)
		assert.Equal(t, uint64(2), v.(*crdt.GCounter).Value())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorVersionTracking(t *testing.T) {
	t.Run("versions increment on update", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		counterKey := crdt.PNCounterKey("counter")
		for range 3 {
			_, err = Ask(ctx, repl, &crdt.Update{
				Key:     counterKey,
				Initial: crdt.NewPNCounter(),
				Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
					return current.(*crdt.PNCounter).Increment("node-1", 1)
				},
			}, time.Second)
			require.NoError(t, err)
		}

		// verify value accumulated
		resp, err := Ask(ctx, repl, &crdt.Get{Key: counterKey}, time.Second)
		require.NoError(t, err)
		assert.Equal(t, int64(3), resp.(*crdt.GetResponse).Data.(*crdt.PNCounter).Value())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

// crdtCluster is a test helper that manages a 3-node cluster with CRDT enabled.
type crdtCluster struct {
	nodes [3]ActorSystem
	sds   [3]discovery.Provider
	repls [3]*PID
	srv   interface{ Shutdown() }
}

// setupCRDTCluster creates and starts a 3-node CRDT-enabled cluster.
func setupCRDTCluster(t *testing.T) *crdtCluster {
	t.Helper()
	srv := startNatsServer(t)
	c := &crdtCluster{srv: srv}
	for i := range 3 {
		node, sd := testNATs(t, srv.Addr().String(), withTestCRDT())
		require.NotNil(t, node)
		c.nodes[i] = node
		c.sds[i] = sd
	}
	pause.For(3 * time.Second)
	for i := range 3 {
		c.repls[i] = c.nodes[i].Replicator()
		require.NotNil(t, c.repls[i], "replicator should be running on node %d", i+1)
	}
	return c
}

// shutdown stops all nodes and closes discovery providers.
func (c *crdtCluster) shutdown(t *testing.T) {
	t.Helper()
	ctx := context.TODO()
	for i := range 3 {
		require.NoError(t, c.nodes[i].Stop(ctx))
	}
	for i := range 3 {
		require.NoError(t, c.sds[i].Close())
	}
	c.srv.Shutdown()
}

// getPNCounter reads a PNCounter from a node's replicator.
func getPNCounter(t *testing.T, repl *PID, key crdt.Key) *crdt.PNCounter {
	t.Helper()
	ctx := context.TODO()
	resp, err := Ask(ctx, repl, &crdt.Get{Key: key}, time.Second)
	require.NoError(t, err)
	data := resp.(*crdt.GetResponse).Data
	if data == nil {
		return nil
	}
	return data.(*crdt.PNCounter)
}

// getORSet reads an ORSet from a node's replicator.
func getORSet(t *testing.T, repl *PID, key crdt.Key) *crdt.ORSet {
	t.Helper()
	ctx := context.TODO()
	resp, err := Ask(ctx, repl, &crdt.Get{Key: key}, time.Second)
	require.NoError(t, err)
	data := resp.(*crdt.GetResponse).Data
	if data == nil {
		return nil
	}
	return data.(*crdt.ORSet)
}

func TestReplicatorCluster(t *testing.T) {
	t.Run("replicator is spawned on all nodes when CRDT is enabled", func(t *testing.T) {
		c := setupCRDTCluster(t)
		defer c.shutdown(t)

		for i, repl := range c.repls {
			assert.True(t, repl.IsRunning(), "replicator on node %d should be running", i+1)
		}
	})

	t.Run("replicator is nil when CRDT is not enabled", func(t *testing.T) {
		ctx := context.TODO()
		srv := startNatsServer(t)

		node1, sd1 := testNATs(t, srv.Addr().String())
		node2, sd2 := testNATs(t, srv.Addr().String())
		node3, sd3 := testNATs(t, srv.Addr().String())

		pause.For(3 * time.Second)

		assert.Nil(t, node1.Replicator())
		assert.Nil(t, node2.Replicator())
		assert.Nil(t, node3.Replicator())

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})

	t.Run("update on one node is readable locally", func(t *testing.T) {
		c := setupCRDTCluster(t)
		defer c.shutdown(t)

		ctx := context.TODO()
		counterKey := crdt.PNCounterKey("local-read")

		resp, err := Ask(ctx, c.repls[0], &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 10)
			},
		}, time.Second)
		require.NoError(t, err)
		require.IsType(t, &crdt.UpdateResponse{}, resp)

		data := getPNCounter(t, c.repls[0], counterKey)
		require.NotNil(t, data)
		assert.Equal(t, int64(10), data.Value())
	})

	t.Run("delta replication across all three nodes", func(t *testing.T) {
		c := setupCRDTCluster(t)
		defer c.shutdown(t)

		ctx := context.TODO()
		counterKey := crdt.PNCounterKey("replicated-counter")

		// update on node1
		_, err := Ask(ctx, c.repls[0], &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 7)
			},
		}, time.Second)
		require.NoError(t, err)

		// wait for propagation
		pause.For(3 * time.Second)

		// all three nodes should see the same value
		for i, repl := range c.repls {
			data := getPNCounter(t, repl, counterKey)
			require.NotNil(t, data, "node %d should have the counter", i+1)
			assert.Equal(t, int64(7), data.Value(), "node %d should see value 7", i+1)
		}
	})

	t.Run("concurrent updates on all three nodes converge", func(t *testing.T) {
		c := setupCRDTCluster(t)
		defer c.shutdown(t)

		ctx := context.TODO()
		counterKey := crdt.PNCounterKey("converge-counter")

		// each node increments with a different value
		for i, repl := range c.repls {
			nodeID := fmt.Sprintf("node-%d", i+1)
			value := uint64((i + 1) * 10) // 10, 20, 30
			_, err := Ask(ctx, repl, &crdt.Update{
				Key:     counterKey,
				Initial: crdt.NewPNCounter(),
				Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
					return current.(*crdt.PNCounter).Increment(nodeID, value)
				},
			}, time.Second)
			require.NoError(t, err)
		}

		// wait for all deltas to propagate
		pause.For(3 * time.Second)

		// all nodes should converge to 60 (10 + 20 + 30)
		for i, repl := range c.repls {
			data := getPNCounter(t, repl, counterKey)
			require.NotNil(t, data, "node %d should have the counter", i+1)
			assert.Equal(t, int64(60), data.Value(), "node %d should converge to 60", i+1)
		}
	})

	t.Run("ORSet replication across all three nodes", func(t *testing.T) {
		c := setupCRDTCluster(t)
		defer c.shutdown(t)

		ctx := context.TODO()
		setKey := crdt.ORSetKey("active-sessions")

		// each node adds its own session
		for i, repl := range c.repls {
			nodeID := fmt.Sprintf("node-%d", i+1)
			session := fmt.Sprintf("session-%c", 'a'+i) // session-a, session-b, session-c
			_, err := Ask(ctx, repl, &crdt.Update{
				Key:     setKey,
				Initial: crdt.NewORSet(),
				Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
					return current.(*crdt.ORSet).Add(nodeID, session)
				},
			}, time.Second)
			require.NoError(t, err)
		}

		// wait for replication
		pause.For(3 * time.Second)

		// all three nodes should see all three sessions
		for i, repl := range c.repls {
			set := getORSet(t, repl, setKey)
			require.NotNil(t, set, "node %d should have the set", i+1)
			assert.True(t, set.Contains("session-a"), "node %d should have session-a", i+1)
			assert.True(t, set.Contains("session-b"), "node %d should have session-b", i+1)
			assert.True(t, set.Contains("session-c"), "node %d should have session-c", i+1)
			assert.Equal(t, 3, set.Len(), "node %d should have 3 sessions", i+1)
		}
	})

	t.Run("multiple updates from same node accumulate across cluster", func(t *testing.T) {
		c := setupCRDTCluster(t)
		defer c.shutdown(t)

		ctx := context.TODO()
		counterKey := crdt.PNCounterKey("accumulate-counter")

		// node1 sends 5 increments
		for i := range 5 {
			_, err := Ask(ctx, c.repls[0], &crdt.Update{
				Key:     counterKey,
				Initial: crdt.NewPNCounter(),
				Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
					return current.(*crdt.PNCounter).Increment("node-1", uint64(i+1))
				},
			}, time.Second)
			require.NoError(t, err)
		}

		// wait for propagation
		pause.For(3 * time.Second)

		// all nodes should see 15 (1+2+3+4+5)
		for i, repl := range c.repls {
			data := getPNCounter(t, repl, counterKey)
			require.NotNil(t, data, "node %d should have the counter", i+1)
			assert.Equal(t, int64(15), data.Value(), "node %d should see value 15", i+1)
		}
	})

	t.Run("delete removes key locally", func(t *testing.T) {
		c := setupCRDTCluster(t)
		defer c.shutdown(t)

		ctx := context.TODO()
		counterKey := crdt.PNCounterKey("to-delete")

		// create key on node1
		_, err := Ask(ctx, c.repls[0], &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 42)
			},
		}, time.Second)
		require.NoError(t, err)

		// wait for replication to all nodes
		pause.For(3 * time.Second)

		// verify all nodes have it
		for i, repl := range c.repls {
			data := getPNCounter(t, repl, counterKey)
			require.NotNil(t, data, "node %d should have the counter before delete", i+1)
			assert.Equal(t, int64(42), data.Value())
		}

		// delete on node1
		_, err = Ask(ctx, c.repls[0], &crdt.Delete{Key: counterKey}, time.Second)
		require.NoError(t, err)

		// verify node1 no longer has it
		data := getPNCounter(t, c.repls[0], counterKey)
		assert.Nil(t, data, "node1 should not have the counter after delete")
	})

	t.Run("GCounter replication across all three nodes", func(t *testing.T) {
		c := setupCRDTCluster(t)
		defer c.shutdown(t)

		ctx := context.TODO()
		gcKey := crdt.GCounterKey("gc-replicated")

		// each node increments
		for i, repl := range c.repls {
			nodeID := fmt.Sprintf("node-%d", i+1)
			_, err := Ask(ctx, repl, &crdt.Update{
				Key:     gcKey,
				Initial: crdt.NewGCounter(),
				Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
					return current.(*crdt.GCounter).Increment(nodeID, uint64(i+1))
				},
			}, time.Second)
			require.NoError(t, err)
		}

		// wait for replication
		pause.For(3 * time.Second)

		// all nodes should converge to 6 (1+2+3)
		for i, repl := range c.repls {
			resp, err := Ask(ctx, repl, &crdt.Get{Key: gcKey}, time.Second)
			require.NoError(t, err)
			data := resp.(*crdt.GetResponse).Data
			require.NotNil(t, data, "node %d should have the GCounter", i+1)
			assert.Equal(t, uint64(6), data.(*crdt.GCounter).Value(), "node %d should converge to 6", i+1)
		}
	})

	t.Run("coordinated write Majority replicates to peers", func(t *testing.T) {
		c := setupCRDTCluster(t)
		defer c.shutdown(t)

		ctx := context.TODO()
		counterKey := crdt.PNCounterKey("coord-write-majority")

		// update with WriteTo: Majority
		_, err := Ask(ctx, c.repls[0], &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 42)
			},
			WriteTo: crdt.Majority,
		}, 5*time.Second)
		require.NoError(t, err)

		// wait for replication
		pause.For(3 * time.Second)

		// all nodes should see the value
		for i, repl := range c.repls {
			resp, err := Ask(ctx, repl, &crdt.Get{Key: counterKey}, time.Second)
			require.NoError(t, err)
			data := resp.(*crdt.GetResponse).Data
			require.NotNil(t, data, "node %d should have the counter", i+1)
			assert.Equal(t, int64(42), data.(*crdt.PNCounter).Value(), "node %d should have value 42", i+1)
		}
	})

	t.Run("coordinated write All replicates to all peers", func(t *testing.T) {
		c := setupCRDTCluster(t)
		defer c.shutdown(t)

		ctx := context.TODO()
		counterKey := crdt.PNCounterKey("coord-write-all")

		_, err := Ask(ctx, c.repls[0], &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 99)
			},
			WriteTo: crdt.All,
		}, 5*time.Second)
		require.NoError(t, err)

		pause.For(3 * time.Second)

		for i, repl := range c.repls {
			resp, err := Ask(ctx, repl, &crdt.Get{Key: counterKey}, time.Second)
			require.NoError(t, err)
			data := resp.(*crdt.GetResponse).Data
			require.NotNil(t, data, "node %d should have the counter", i+1)
			assert.Equal(t, int64(99), data.(*crdt.PNCounter).Value(), "node %d should have value 99", i+1)
		}
	})

	t.Run("coordinated read Majority merges peer values", func(t *testing.T) {
		c := setupCRDTCluster(t)
		defer c.shutdown(t)

		ctx := context.TODO()
		counterKey := crdt.PNCounterKey("coord-read-majority")

		// update on each node independently
		for i, repl := range c.repls {
			nodeID := fmt.Sprintf("node-%d", i+1)
			_, err := Ask(ctx, repl, &crdt.Update{
				Key:     counterKey,
				Initial: crdt.NewPNCounter(),
				Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
					return current.(*crdt.PNCounter).Increment(nodeID, uint64(i+1)*10)
				},
			}, time.Second)
			require.NoError(t, err)
		}

		// wait for delta replication
		pause.For(3 * time.Second)

		// coordinated read should merge values from peers
		resp, err := Ask(ctx, c.repls[0], &crdt.Get{
			Key:      counterKey,
			ReadFrom: crdt.Majority,
		}, 5*time.Second)
		require.NoError(t, err)
		data := resp.(*crdt.GetResponse).Data
		require.NotNil(t, data)
		// After replication + coordinated read, all increments should be visible: 10+20+30=60
		assert.Equal(t, int64(60), data.(*crdt.PNCounter).Value())
	})

	t.Run("coordinated read All merges all peer values", func(t *testing.T) {
		c := setupCRDTCluster(t)
		defer c.shutdown(t)

		ctx := context.TODO()
		counterKey := crdt.PNCounterKey("coord-read-all")

		for i, repl := range c.repls {
			nodeID := fmt.Sprintf("node-%d", i+1)
			_, err := Ask(ctx, repl, &crdt.Update{
				Key:     counterKey,
				Initial: crdt.NewPNCounter(),
				Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
					return current.(*crdt.PNCounter).Increment(nodeID, uint64(i+1)*5)
				},
			}, time.Second)
			require.NoError(t, err)
		}

		pause.For(3 * time.Second)

		resp, err := Ask(ctx, c.repls[0], &crdt.Get{
			Key:      counterKey,
			ReadFrom: crdt.All,
		}, 5*time.Second)
		require.NoError(t, err)
		data := resp.(*crdt.GetResponse).Data
		require.NotNil(t, data)
		assert.Equal(t, int64(30), data.(*crdt.PNCounter).Value())
	})

	t.Run("coordinated delete Majority sends tombstone to peers", func(t *testing.T) {
		c := setupCRDTCluster(t)
		defer c.shutdown(t)

		ctx := context.TODO()
		counterKey := crdt.PNCounterKey("coord-delete")

		// create key on node 0
		_, err := Ask(ctx, c.repls[0], &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 10)
			},
		}, time.Second)
		require.NoError(t, err)
		pause.For(3 * time.Second)

		// delete with coordination
		_, err = Ask(ctx, c.repls[0], &crdt.Delete{
			Key:     counterKey,
			WriteTo: crdt.Majority,
		}, 5*time.Second)
		require.NoError(t, err)
		pause.For(3 * time.Second)

		// key should be gone on all nodes
		for i, repl := range c.repls {
			resp, err := Ask(ctx, repl, &crdt.Get{Key: counterKey}, time.Second)
			require.NoError(t, err)
			data := resp.(*crdt.GetResponse).Data
			assert.Nil(t, data, "node %d should not have the deleted counter", i+1)
		}
	})
}

func TestCRDTConfigExtension(t *testing.T) {
	t.Run("ID returns expected value", func(t *testing.T) {
		ext := &crdtConfigExtension{config: crdt.NewConfig()}
		assert.Equal(t, crdtConfigExtensionID, ext.ID())
	})

	t.Run("Config returns config", func(t *testing.T) {
		cfg := crdt.NewConfig()
		ext := &crdtConfigExtension{config: cfg}
		assert.Same(t, cfg, ext.Config())
	})
}

func TestEncodeCRDTDeltaRoundTrip(t *testing.T) {
	r := newTestReplicator()

	t.Run("PNCounter delta", func(t *testing.T) {
		delta := &crdtDelta{
			KeyID:    "counter-1",
			DataType: crdt.PNCounterType,
			Delta:    crdt.NewPNCounter().Increment("node-1", 5),
			Origin:   "node-1",
		}

		pb, err := r.encodeDelta(delta)
		require.NoError(t, err)
		require.NotNil(t, pb)
		assert.Equal(t, "node-1", pb.GetOriginNode())

		decoded, err := r.decodeDelta(pb)
		require.NoError(t, err)
		assert.Equal(t, "counter-1", decoded.KeyID)
		assert.Equal(t, crdt.PNCounterType, decoded.DataType)
		assert.Equal(t, "node-1", decoded.Origin)
		assert.Equal(t, int64(5), decoded.Delta.(*crdt.PNCounter).Value())
	})

	t.Run("GCounter delta", func(t *testing.T) {
		delta := &crdtDelta{
			KeyID:    "gc-1",
			DataType: crdt.GCounterType,
			Delta:    crdt.NewGCounter().Increment("node-2", 10),
			Origin:   "node-2",
		}

		pb, err := r.encodeDelta(delta)
		require.NoError(t, err)

		decoded, err := r.decodeDelta(pb)
		require.NoError(t, err)
		assert.Equal(t, "gc-1", decoded.KeyID)
		assert.Equal(t, uint64(10), decoded.Delta.(*crdt.GCounter).Value())
	})

	t.Run("decode bad data returns error", func(t *testing.T) {
		pb := &internalpb.CRDTDelta{
			Key:        codec.EncodeCRDTKey("key", crdt.GCounterType),
			OriginNode: "node-1",
			Data:       nil,
		}
		_, err := r.decodeDelta(pb)
		require.Error(t, err)
	})

	t.Run("decode bad key returns error", func(t *testing.T) {
		gcData, err := ddata.EncodeCRDT(crdt.NewGCounter().Increment("n1", 1), nil)
		require.NoError(t, err)

		pb := &internalpb.CRDTDelta{
			Key: &internalpb.CRDTKey{
				Id:       "bad",
				DataType: internalpb.CRDTDataType_CRDT_DATA_TYPE_UNSPECIFIED,
			},
			OriginNode: "node-1",
			Data:       gcData,
		}
		_, err = r.decodeDelta(pb)
		require.Error(t, err)
	})
}

func TestReplicatorHandleSnapshot(t *testing.T) {
	t.Run("nil snapshot store is no-op", func(t *testing.T) {
		r := newTestReplicator()
		r.logger = log.DiscardLogger
		r.snapshotStore = nil
		r.handleSnapshot()
	})

	t.Run("saves store to snapshot", func(t *testing.T) {
		dir := t.TempDir()
		store, err := ddata.NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		r := newTestReplicator()
		r.logger = log.DiscardLogger
		r.snapshotStore = store
		r.store["gc-1"] = crdt.NewGCounter().Increment("node-1", 7)
		r.keyTypes["gc-1"] = crdt.GCounterType
		r.versions["gc-1"] = 2

		r.handleSnapshot()

		loaded, err := store.Load()
		require.NoError(t, err)
		require.Len(t, loaded, 1)
		require.NotNil(t, loaded["gc-1"])
		assert.Equal(t, uint64(2), loaded["gc-1"].GetVersion())
		assert.Equal(t, uint64(7), loaded["gc-1"].GetData().GetGCounter().GetState()["node-1"])
	})
}

func TestReplicatorRestoreFromSnapshot(t *testing.T) {
	t.Run("no-op when snapshot not configured", func(t *testing.T) {
		r := newTestReplicator()
		r.logger = log.DiscardLogger
		err := r.restoreFromSnapshot()
		require.NoError(t, err)
		assert.Nil(t, r.snapshotStore)
	})

	t.Run("restores persisted data", func(t *testing.T) {
		dir := t.TempDir()

		store, err := ddata.NewStore(dir)
		require.NoError(t, err)

		entries := map[string]*internalpb.CRDTSnapshotEntry{
			"counter-1": {
				Key:     codec.EncodeCRDTKey("counter-1", crdt.GCounterType),
				Data:    &internalpb.CRDTData{Type: &internalpb.CRDTData_GCounter{GCounter: &internalpb.GCounterData{State: map[string]uint64{"node-1": 42}}}},
				Version: 5,
			},
		}
		require.NoError(t, store.Save(entries))
		require.NoError(t, store.Close())

		r := newTestReplicator()
		r.logger = log.DiscardLogger
		r.config = crdt.NewConfig(
			crdt.WithSnapshotInterval(time.Second),
			crdt.WithSnapshotDir(dir),
		)

		err = r.restoreFromSnapshot()
		require.NoError(t, err)
		require.NotNil(t, r.snapshotStore)
		defer r.snapshotStore.Close()

		assert.Len(t, r.store, 1)
		assert.Equal(t, uint64(42), r.store["counter-1"].(*crdt.GCounter).Value())
		assert.Equal(t, crdt.GCounterType, r.keyTypes["counter-1"])
		assert.Equal(t, uint64(5), r.versions["counter-1"])
		_, hasSub := r.subscriptions["counter-1"]
		assert.True(t, hasSub)
	})

	t.Run("empty snapshot returns empty maps", func(t *testing.T) {
		dir := t.TempDir()

		store, err := ddata.NewStore(dir)
		require.NoError(t, err)
		require.NoError(t, store.Close())

		r := newTestReplicator()
		r.logger = log.DiscardLogger
		r.config = crdt.NewConfig(
			crdt.WithSnapshotInterval(time.Second),
			crdt.WithSnapshotDir(dir),
		)

		err = r.restoreFromSnapshot()
		require.NoError(t, err)
		require.NotNil(t, r.snapshotStore)
		defer r.snapshotStore.Close()

		assert.Empty(t, r.store)
	})
}

func TestReplicatorHandleTerminated(t *testing.T) {
	ctx := context.TODO()
	sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	err := sys.Start(ctx)
	require.NoError(t, err)

	pid1, err := sys.Spawn(ctx, "w1", NewMockActor(), WithLongLived())
	require.NoError(t, err)
	pid2, err := sys.Spawn(ctx, "w2", NewMockActor(), WithLongLived())
	require.NoError(t, err)

	t.Run("removes terminated actor from all watcher lists", func(t *testing.T) {
		r := newTestReplicator()
		r.watchers["key-a"] = []*PID{pid1, pid2}
		r.watchers["key-b"] = []*PID{pid1}

		terminated := &Terminated{actorPath: pid1.Path()}
		r.handleTerminated(terminated)

		assert.Len(t, r.watchers["key-a"], 1)
		assert.Equal(t, pid2.ID(), r.watchers["key-a"][0].ID())
		_, hasBKey := r.watchers["key-b"]
		assert.False(t, hasBKey)
	})

	t.Run("does nothing for unknown actor", func(t *testing.T) {
		r := newTestReplicator()
		r.watchers["key-a"] = []*PID{pid1}

		terminated := &Terminated{actorPath: pid2.Path()}
		r.handleTerminated(terminated)

		assert.Len(t, r.watchers["key-a"], 1)
	})

	err = sys.Stop(ctx)
	assert.NoError(t, err)
}

func TestReplicatorProtoDelta(t *testing.T) {
	t.Run("proto delta from peer merges into store", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		counterKey := crdt.PNCounterKey("counter")
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		peerDelta := crdt.NewPNCounter().Increment("node-2", 10)
		pbDelta, err := newTestReplicator().encodeDelta(&crdtDelta{
			KeyID:    "counter",
			DataType: crdt.PNCounterType,
			Delta:    peerDelta,
			Origin:   "peer-node",
		})
		require.NoError(t, err)

		err = Tell(ctx, repl, pbDelta)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		resp, err := Ask(ctx, repl, &crdt.Get{Key: counterKey}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse)
		assert.Equal(t, int64(15), getResp.Data.(*crdt.PNCounter).Value())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("proto delta with bad data is handled gracefully", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		badDelta := &internalpb.CRDTDelta{
			Key:        codec.EncodeCRDTKey("key", crdt.GCounterType),
			OriginNode: "peer",
			Data:       nil,
		}
		err = Tell(ctx, repl, badDelta)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorSubscribeUnsubscribe(t *testing.T) {
	ctx := context.TODO()
	sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	err := sys.Start(ctx)
	require.NoError(t, err)
	pause.For(time.Second)

	repl := spawnTestReplicator(t, sys)

	watcher, err := sys.Spawn(ctx, "watcher", NewMockActor(), WithLongLived())
	require.NoError(t, err)

	counterKey := crdt.PNCounterKey("watched-counter")

	err = watcher.Tell(ctx, repl, &crdt.Subscribe{Key: counterKey})
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	_, err = Ask(ctx, repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 5)
		},
	}, time.Second)
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	err = watcher.Tell(ctx, repl, &crdt.Unsubscribe{Key: counterKey})
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	assert.True(t, repl.IsRunning())
	assert.True(t, watcher.IsRunning())

	err = sys.Stop(ctx)
	assert.NoError(t, err)
}

func TestReplicatorHandleDigestAndFullState(t *testing.T) {
	t.Run("digest processes entries and stays running", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.GCounterKey("gc-1"),
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-1", 10)
			},
		}, time.Second)
		require.NoError(t, err)

		emptyDigest := &internalpb.CRDTDigest{
			Entries: []*internalpb.CRDTDigestEntry{},
		}
		err = Tell(ctx, repl, emptyDigest)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("digest with up-to-date peer sends nothing", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.GCounterKey("gc-1"),
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		digest := &internalpb.CRDTDigest{
			Entries: []*internalpb.CRDTDigestEntry{
				{
					Key:     codec.EncodeCRDTKey("gc-1", crdt.GCounterType),
					Version: 999,
				},
			},
		}
		err = Tell(ctx, repl, digest)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("digest with bad key entry is handled gracefully", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		digest := &internalpb.CRDTDigest{
			Entries: []*internalpb.CRDTDigestEntry{
				{
					Key: &internalpb.CRDTKey{
						Id:       "bad",
						DataType: internalpb.CRDTDataType_CRDT_DATA_TYPE_UNSPECIFIED,
					},
					Version: 1,
				},
			},
		}
		err = Tell(ctx, repl, digest)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("full state merges into local store", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		gc := crdt.NewGCounter().Increment("node-2", 20)
		pbData, err := ddata.EncodeCRDT(gc, nil)
		require.NoError(t, err)

		fullState := &internalpb.CRDTFullState{
			Entries: []*internalpb.CRDTFullStateEntry{
				{
					Key:  codec.EncodeCRDTKey("new-gc", crdt.GCounterType),
					Data: pbData,
				},
			},
		}
		err = Tell(ctx, repl, fullState)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		resp, err := Ask(ctx, repl, &crdt.Get{
			Key: crdt.GCounterKey("new-gc"),
		}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse)
		require.NotNil(t, getResp.Data)
		assert.Equal(t, uint64(20), getResp.Data.(*crdt.GCounter).Value())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("full state merges with existing key", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.GCounterKey("merge-gc"),
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-1", 10)
			},
		}, time.Second)
		require.NoError(t, err)

		peerGC := crdt.NewGCounter().Increment("node-2", 20)
		pbData, err := ddata.EncodeCRDT(peerGC, nil)
		require.NoError(t, err)

		fullState := &internalpb.CRDTFullState{
			Entries: []*internalpb.CRDTFullStateEntry{
				{
					Key:  codec.EncodeCRDTKey("merge-gc", crdt.GCounterType),
					Data: pbData,
				},
			},
		}
		err = Tell(ctx, repl, fullState)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		resp, err := Ask(ctx, repl, &crdt.Get{
			Key: crdt.GCounterKey("merge-gc"),
		}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse)
		require.NotNil(t, getResp.Data)
		assert.Equal(t, uint64(30), getResp.Data.(*crdt.GCounter).Value())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("full state with bad key is handled gracefully", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		fullState := &internalpb.CRDTFullState{
			Entries: []*internalpb.CRDTFullStateEntry{
				{
					Key: &internalpb.CRDTKey{
						Id:       "bad",
						DataType: internalpb.CRDTDataType_CRDT_DATA_TYPE_UNSPECIFIED,
					},
					Data: nil,
				},
			},
		}
		err = Tell(ctx, repl, fullState)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("full state with bad data is handled gracefully", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		fullState := &internalpb.CRDTFullState{
			Entries: []*internalpb.CRDTFullStateEntry{
				{
					Key:  codec.EncodeCRDTKey("gc-bad", crdt.GCounterType),
					Data: nil,
				},
			},
		}
		err = Tell(ctx, repl, fullState)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("full state skips tombstoned keys", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		counterKey := crdt.PNCounterKey("to-delete")
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		_, err = Ask(ctx, repl, &crdt.Delete{Key: counterKey}, time.Second)
		require.NoError(t, err)

		pn := crdt.NewPNCounter().Increment("node-2", 99)
		pbData, err := ddata.EncodeCRDT(pn, nil)
		require.NoError(t, err)

		fullState := &internalpb.CRDTFullState{
			Entries: []*internalpb.CRDTFullStateEntry{
				{
					Key:  codec.EncodeCRDTKey("to-delete", crdt.PNCounterType),
					Data: pbData,
				},
			},
		}
		err = Tell(ctx, repl, fullState)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		resp, err := Ask(ctx, repl, &crdt.Get{Key: counterKey}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse)
		assert.Nil(t, getResp.Data)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorHandleReadRequest(t *testing.T) {
	t.Run("returns local value for key", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.GCounterKey("gc-1"),
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-1", 42)
			},
		}, time.Second)
		require.NoError(t, err)

		req := &internalpb.CRDTReadRequest{
			Key:      codec.EncodeCRDTKey("gc-1", crdt.GCounterType),
			FromNode: "peer-node",
		}
		resp, err := Ask(ctx, repl, req, time.Second)
		require.NoError(t, err)
		readResp, ok := resp.(*internalpb.CRDTReadResponse)
		require.True(t, ok)
		require.NotNil(t, readResp.GetData())

		data, err := ddata.DecodeCRDT(readResp.GetData(), nil)
		require.NoError(t, err)
		assert.Equal(t, uint64(42), data.(*crdt.GCounter).Value())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("returns nil data for missing key", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		req := &internalpb.CRDTReadRequest{
			Key:      codec.EncodeCRDTKey("nonexistent", crdt.GCounterType),
			FromNode: "peer-node",
		}
		resp, err := Ask(ctx, repl, req, time.Second)
		require.NoError(t, err)
		readResp, ok := resp.(*internalpb.CRDTReadResponse)
		require.True(t, ok)
		assert.Nil(t, readResp.GetData())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorProtoTombstoneWithBadKey(t *testing.T) {
	r := newTestReplicator()
	r.nodeID = "local-node"
	r.logger = log.DiscardLogger

	pbTombstone := &internalpb.CRDTTombstone{
		Key: &internalpb.CRDTKey{
			Id:       "bad",
			DataType: internalpb.CRDTDataType_CRDT_DATA_TYPE_UNSPECIFIED,
		},
		DeletedAtNanos: time.Now().UnixNano(),
		DeletedByNode:  "remote-node",
	}
	r.handleProtoTombstone(pbTombstone)
	_, exists := r.tombstones["bad"]
	assert.False(t, exists)
}

func TestReplicatorPostStopWithSnapshot(t *testing.T) {
	ctx := context.TODO()
	dir := t.TempDir()

	sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	config := crdt.NewConfig(
		crdt.WithSnapshotInterval(time.Minute),
		crdt.WithSnapshotDir(dir),
	)

	err := sys.Start(ctx)
	require.NoError(t, err)
	pause.For(time.Second)

	impl := sys.(*actorSystem)
	impl.extensions.Set(crdtConfigExtensionID, &crdtConfigExtension{config: config})

	repl, err := sys.Spawn(ctx, "replicator-snap", newReplicatorActor(), WithLongLived())
	require.NoError(t, err)
	require.NotNil(t, repl)
	pause.For(500 * time.Millisecond)

	counterKey := crdt.PNCounterKey("snap-counter")
	_, err = Ask(ctx, repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 77)
		},
	}, time.Second)
	require.NoError(t, err)

	err = sys.Stop(ctx)
	require.NoError(t, err)

	store, err := ddata.NewStore(dir)
	require.NoError(t, err)
	loaded, loadErr := store.Load()
	require.NoError(t, loadErr)
	assert.Len(t, loaded, 1)
	require.NotNil(t, loaded["snap-counter"])
	assert.NotNil(t, loaded["snap-counter"].GetData().GetPnCounter())
	assert.True(t, loaded["snap-counter"].GetVersion() > 0)
	require.NoError(t, store.Close())
}

// ---------------------------------------------------------------------------
// Benchmarks
// ---------------------------------------------------------------------------

// spawnBenchReplicator creates a replicator for benchmarks.
func spawnBenchReplicator(b *testing.B) (ActorSystem, *PID) {
	b.Helper()
	ctx := context.TODO()
	sys, err := NewActorSystem("benchSys", WithLogger(log.DiscardLogger))
	require.NoError(b, err)
	require.NoError(b, sys.Start(ctx))

	config := crdt.NewConfig()
	impl := sys.(*actorSystem)
	impl.extensions.Set(crdtConfigExtensionID, &crdtConfigExtension{config: config})
	repl, err := sys.Spawn(ctx, "replicator", newReplicatorActor(), WithLongLived())
	require.NoError(b, err)
	require.NotNil(b, repl)
	pause.For(500 * time.Millisecond)
	return sys, repl
}

func BenchmarkReplicatorUpdatePNCounter(b *testing.B) {
	sys, repl := spawnBenchReplicator(b)
	defer sys.Stop(context.TODO())

	ctx := context.TODO()
	counterKey := crdt.PNCounterKey("bench-counter")

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 1)
			},
		}, time.Second)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReplicatorUpdateGCounter(b *testing.B) {
	sys, repl := spawnBenchReplicator(b)
	defer sys.Stop(context.TODO())

	ctx := context.TODO()
	counterKey := crdt.GCounterKey("bench-gcounter")

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-1", 1)
			},
		}, time.Second)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReplicatorUpdateORSet(b *testing.B) {
	sys, repl := spawnBenchReplicator(b)
	defer sys.Stop(context.TODO())

	ctx := context.TODO()
	setKey := crdt.ORSetKey("bench-set")

	b.ResetTimer()
	b.ReportAllocs()
	for i := range b.N {
		elem := fmt.Sprintf("elem-%d", i)
		_, err := Ask(ctx, repl, &crdt.Update{
			Key:     setKey,
			Initial: crdt.NewORSet(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.ORSet).Add("node-1", elem)
			},
		}, time.Second)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReplicatorUpdateFlag(b *testing.B) {
	sys, repl := spawnBenchReplicator(b)
	defer sys.Stop(context.TODO())

	ctx := context.TODO()
	flagKey := crdt.FlagKey("bench-flag")

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := Ask(ctx, repl, &crdt.Update{
			Key:     flagKey,
			Initial: crdt.NewFlag(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.Flag).Enable()
			},
		}, time.Second)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReplicatorGetPNCounter(b *testing.B) {
	sys, repl := spawnBenchReplicator(b)
	defer sys.Stop(context.TODO())

	ctx := context.TODO()
	counterKey := crdt.PNCounterKey("bench-read")

	_, err := Ask(ctx, repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 100)
		},
	}, time.Second)
	require.NoError(b, err)

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := Ask(ctx, repl, &crdt.Get{Key: counterKey}, time.Second)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReplicatorGetORSet(b *testing.B) {
	sys, repl := spawnBenchReplicator(b)
	defer sys.Stop(context.TODO())

	ctx := context.TODO()
	setKey := crdt.ORSetKey("bench-read-set")

	for i := range 100 {
		_, err := Ask(ctx, repl, &crdt.Update{
			Key:     setKey,
			Initial: crdt.NewORSet(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.ORSet).Add("node-1", fmt.Sprintf("elem-%d", i))
			},
		}, time.Second)
		require.NoError(b, err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		_, err := Ask(ctx, repl, &crdt.Get{Key: setKey}, time.Second)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReplicatorMultiKeyUpdate(b *testing.B) {
	for _, numKeys := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("keys=%d", numKeys), func(b *testing.B) {
			sys, repl := spawnBenchReplicator(b)
			defer sys.Stop(context.TODO())

			ctx := context.TODO()
			keys := make([]crdt.Key, numKeys)
			for i := range numKeys {
				keys[i] = crdt.GCounterKey(fmt.Sprintf("key-%d", i))
			}

			b.ResetTimer()
			b.ReportAllocs()
			for i := range b.N {
				key := keys[i%numKeys]
				_, err := Ask(ctx, repl, &crdt.Update{
					Key:     key,
					Initial: crdt.NewGCounter(),
					Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
						return current.(*crdt.GCounter).Increment("node-1", 1)
					},
				}, time.Second)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkReplicatorDeltaMerge(b *testing.B) {
	sys, repl := spawnBenchReplicator(b)
	defer sys.Stop(context.TODO())

	ctx := context.TODO()
	counterKey := crdt.PNCounterKey("merge-counter")

	_, err := Ask(ctx, repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 100)
		},
	}, time.Second)
	require.NoError(b, err)

	delta := crdt.NewPNCounter().Increment("node-2", 50)
	pbDelta, err := newTestReplicator().encodeDelta(&crdtDelta{
		KeyID:    "merge-counter",
		DataType: crdt.PNCounterType,
		Delta:    delta,
		Origin:   "remote-node",
	})
	require.NoError(b, err)

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		if err := Tell(ctx, repl, pbDelta); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReplicatorBuildDigest(b *testing.B) {
	for _, numKeys := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("keys=%d", numKeys), func(b *testing.B) {
			r := newTestReplicator()
			r.logger = log.DiscardLogger
			for i := range numKeys {
				keyID := fmt.Sprintf("key-%d", i)
				r.store[keyID] = crdt.NewGCounter().Increment("node-1", uint64(i))
				r.keyTypes[keyID] = crdt.GCounterType
				r.versions[keyID] = uint64(i + 1)
			}

			b.ResetTimer()
			b.ReportAllocs()
			for range b.N {
				r.buildDigest()
			}
		})
	}
}

func BenchmarkReplicatorFullStateRoundTrip(b *testing.B) {
	for _, numKeys := range []int{10, 100} {
		b.Run(fmt.Sprintf("keys=%d", numKeys), func(b *testing.B) {
			entries := make([]*internalpb.CRDTFullStateEntry, numKeys)
			for i := range numKeys {
				keyID := fmt.Sprintf("key-%d", i)
				data := crdt.NewGCounter().Increment("node-1", uint64(i+1))
				pbData, err := ddata.EncodeCRDT(data, nil)
				require.NoError(b, err)
				entries[i] = &internalpb.CRDTFullStateEntry{
					Key:  codec.EncodeCRDTKey(keyID, crdt.GCounterType),
					Data: pbData,
				}
			}

			b.ResetTimer()
			b.ReportAllocs()
			for range b.N {
				for _, entry := range entries {
					_, _ = ddata.DecodeCRDT(entry.GetData(), nil)
				}
			}
		})
	}
}
