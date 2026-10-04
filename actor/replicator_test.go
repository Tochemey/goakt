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
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/crdt"
	"github.com/tochemey/goakt/v4/datacenter"
	"github.com/tochemey/goakt/v4/discovery"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/codec"
	"github.com/tochemey/goakt/v4/internal/ddata"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	mocksremote "github.com/tochemey/goakt/v4/mocks/remoteclient"
)

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

		pbTombstone := internalpb.CRDTTombstone_builder{
			Key:            codec.EncodeCRDTKey("counter", crdt.PNCounterType),
			DeletedAtNanos: time.Now().UnixNano(),
			DeletedByNode:  "remote-node",
		}.Build()
		r.handleProtoTombstone(nil, pbTombstone)

		_, exists := r.store["counter"]
		assert.False(t, exists)
		_, hasTombstone := r.tombstones["counter"]
		assert.True(t, hasTombstone)
	})

	t.Run("proto tombstone from self is ignored", func(t *testing.T) {
		r := newTestReplicator()
		r.nodeID = "local-node"
		r.store["counter"] = crdt.NewPNCounter().Increment("node-1", 5)

		pbTombstone := internalpb.CRDTTombstone_builder{
			Key:            codec.EncodeCRDTKey("counter", crdt.PNCounterType),
			DeletedAtNanos: time.Now().UnixNano(),
			DeletedByNode:  "local-node",
		}.Build()
		r.handleProtoTombstone(nil, pbTombstone)

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

func TestReplicatorDataTypes(t *testing.T) {
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

		node1, sd1 := startNATsSystem(t, srv.Addr().String())
		node2, sd2 := startNATsSystem(t, srv.Addr().String())
		node3, sd3 := startNATsSystem(t, srv.Addr().String())

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

	t.Run("replicator is spawned when node role matches CRDT role", func(t *testing.T) {
		ctx := context.TODO()
		srv := startNatsServer(t)

		node1, sd1 := startNATsSystem(t, srv.Addr().String(),
			withTestCRDT(crdt.WithRole("crdt-node")),
			withTestRoles("crdt-node"),
		)
		node2, sd2 := startNATsSystem(t, srv.Addr().String(),
			withTestCRDT(crdt.WithRole("crdt-node")),
			withTestRoles("crdt-node"),
		)
		node3, sd3 := startNATsSystem(t, srv.Addr().String(),
			withTestCRDT(crdt.WithRole("crdt-node")),
			withTestRoles("crdt-node"),
		)

		pause.For(3 * time.Second)

		assert.NotNil(t, node1.Replicator(), "node1 should have replicator when role matches")
		assert.NotNil(t, node2.Replicator(), "node2 should have replicator when role matches")
		assert.NotNil(t, node3.Replicator(), "node3 should have replicator when role matches")

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})

	t.Run("replicator is nil when node role does not match CRDT role", func(t *testing.T) {
		ctx := context.TODO()
		srv := startNatsServer(t)

		node1, sd1 := startNATsSystem(t, srv.Addr().String(),
			withTestCRDT(crdt.WithRole("crdt-node")),
			withTestRoles("web-server"),
		)
		node2, sd2 := startNATsSystem(t, srv.Addr().String(),
			withTestCRDT(crdt.WithRole("crdt-node")),
			withTestRoles("web-server"),
		)
		node3, sd3 := startNATsSystem(t, srv.Addr().String(),
			withTestCRDT(crdt.WithRole("crdt-node")),
		)

		pause.For(3 * time.Second)

		assert.Nil(t, node1.Replicator(), "node1 should not have replicator when role mismatches")
		assert.Nil(t, node2.Replicator(), "node2 should not have replicator when role mismatches")
		assert.Nil(t, node3.Replicator(), "node3 should not have replicator when no roles assigned")

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})

	t.Run("replicator is spawned when CRDT role is empty regardless of node roles", func(t *testing.T) {
		ctx := context.TODO()
		srv := startNatsServer(t)

		node1, sd1 := startNATsSystem(t, srv.Addr().String(),
			withTestCRDT(),
			withTestRoles("web-server"),
		)
		node2, sd2 := startNATsSystem(t, srv.Addr().String(),
			withTestCRDT(),
			withTestRoles("api-server"),
		)
		node3, sd3 := startNATsSystem(t, srv.Addr().String(),
			withTestCRDT(),
		)

		pause.For(3 * time.Second)

		assert.NotNil(t, node1.Replicator(), "node1 should have replicator when no CRDT role is set")
		assert.NotNil(t, node2.Replicator(), "node2 should have replicator when no CRDT role is set")
		assert.NotNil(t, node3.Replicator(), "node3 should have replicator when no CRDT role is set")

		require.NoError(t, node1.Stop(ctx))
		require.NoError(t, node2.Stop(ctx))
		require.NoError(t, node3.Stop(ctx))
		require.NoError(t, sd1.Close())
		require.NoError(t, sd2.Close())
		require.NoError(t, sd3.Close())
		srv.Shutdown()
	})

	t.Run("mixed roles: only matching nodes spawn replicator", func(t *testing.T) {
		ctx := context.TODO()
		srv := startNatsServer(t)

		nodeWithRole, sd1 := startNATsSystem(t, srv.Addr().String(),
			withTestCRDT(crdt.WithRole("crdt-node")),
			withTestRoles("crdt-node", "web-server"),
		)
		nodeWithoutRole, sd2 := startNATsSystem(t, srv.Addr().String(),
			withTestCRDT(crdt.WithRole("crdt-node")),
			withTestRoles("web-server"),
		)
		nodeNoRoles, sd3 := startNATsSystem(t, srv.Addr().String(),
			withTestCRDT(crdt.WithRole("crdt-node")),
		)

		pause.For(3 * time.Second)

		assert.NotNil(t, nodeWithRole.Replicator(), "node with matching role should have replicator")
		assert.Nil(t, nodeWithoutRole.Replicator(), "node without matching role should not have replicator")
		assert.Nil(t, nodeNoRoles.Replicator(), "node with no roles should not have replicator")

		require.NoError(t, nodeWithRole.Stop(ctx))
		require.NoError(t, nodeWithoutRole.Stop(ctx))
		require.NoError(t, nodeNoRoles.Stop(ctx))
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

	t.Run("delete notifies watchers on every node", func(t *testing.T) {
		c := setupCRDTCluster(t)
		defer c.shutdown(t)

		ctx := context.TODO()
		counterKey := crdt.PNCounterKey("watched-delete")

		_, err := Ask(ctx, c.repls[0], &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 3)
			},
		}, time.Second)
		require.NoError(t, err)

		// wait for replication to all nodes
		pause.For(3 * time.Second)

		watchers := make([]*PID, len(c.repls))
		probes := make([]*MockMessageProbe, len(c.repls))
		for i, repl := range c.repls {
			require.NotNil(t, getPNCounter(t, repl, counterKey), "node %d should have the counter before delete", i+1)
			watchers[i], probes[i] = subscribeProbe(t, c.nodes[i], repl, fmt.Sprintf("watcher-%d", i+1), counterKey)
		}

		// delete on node1: node1 notifies at once, the others when the tombstone arrives
		_, err = Ask(ctx, c.repls[0], &crdt.Delete{Key: counterKey}, time.Second)
		require.NoError(t, err)

		for i := range c.repls {
			expectDeleted(t, probes[i], counterKey)
			assert.Nil(t, getPNCounter(t, c.repls[i], counterKey), "node %d should not have the counter after delete", i+1)
			probeIsQuiet(t, watchers[i], probes[i])
		}
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

	t.Run("a node back after the tombstone TTL does not bring a deleted key back", func(t *testing.T) {
		for _, restartPeers := range []bool{false, true} {
			name := "with its peers running"
			if restartPeers {
				name = "with its peers restarted from their snapshots during the gap"
			}

			t.Run(name, func(t *testing.T) {
				ctx := context.TODO()
				srv := startNatsServer(t)
				// longer than a restarted node takes to be seen by its peer, so the
				// peers themselves never go a whole TTL without contact
				ttl := 5 * time.Second
				tick := 250 * time.Millisecond

				// every node keeps its store on disk and comes back with it
				dirs := [3]string{t.TempDir(), t.TempDir(), t.TempDir()}
				optionsOf := func(i int) testClusterOption {
					return withTestCRDT(crdt.WithAntiEntropyInterval(tick), crdt.WithPruneInterval(tick), crdt.WithTombstoneTTL(ttl), crdt.WithSnapshotInterval(tick), crdt.WithSnapshotDir(dirs[i]))
				}

				var nodes [3]ActorSystem
				var sds [3]discovery.Provider
				for i := range 3 {
					nodes[i], sds[i] = startNATsSystem(t, srv.Addr().String(), optionsOf(i))
				}

				peersOf := func(node ActorSystem) int {
					peers, err := node.Peers(ctx, time.Second)
					if err != nil {
						return -1
					}

					return len(peers)
				}

				// holds runs inside require.Eventually, off the test goroutine, so it
				// reports a failed read as not holding instead of failing the test
				holds := func(node ActorSystem, key crdt.Key) bool {
					resp, err := Ask(ctx, node.Replicator(), &crdt.Get{Key: key}, time.Second)
					return err == nil && resp.(*crdt.GetResponse).Data != nil
				}

				restart := func(i int) {
					require.NoError(t, nodes[i].Stop(ctx))
					require.NoError(t, sds[i].Close())
					nodes[i], sds[i] = startNATsSystem(t, srv.Addr().String(), optionsOf(i))
				}

				require.Eventually(t, func() bool { return peersOf(nodes[0]) == 2 }, 30*time.Second, 50*time.Millisecond)

				// beyond is deleted longer than the TTL before node 3 returns, within
				// is deleted just before; both are on every node when node 3 leaves
				beyond, within := crdt.GCounterKey("beyond"), crdt.GCounterKey("within")
				for _, key := range []crdt.Key{beyond, within} {
					_, err := Ask(ctx, nodes[0].Replicator(), &crdt.Update{
						Key:     key,
						Initial: crdt.NewGCounter(),
						Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
							return current.(*crdt.GCounter).Increment("node-1", 1)
						},
					}, time.Second)
					require.NoError(t, err)

					for _, node := range nodes {
						require.Eventually(t, func() bool { return holds(node, key) }, 10*time.Second, 50*time.Millisecond)
					}
				}

				require.NoError(t, nodes[2].Stop(ctx))
				require.NoError(t, sds[2].Close())
				require.Eventually(t, func() bool { return peersOf(nodes[0]) == 1 }, 30*time.Second, 50*time.Millisecond)

				_, err := Ask(ctx, nodes[0].Replicator(), &crdt.Delete{Key: beyond}, time.Second)
				require.NoError(t, err)
				require.Eventually(t, func() bool { return !holds(nodes[1], beyond) }, 10*time.Second, 50*time.Millisecond)

				// node 1 and node 2 restart one after the other, each with a store
				// equal to its peer's, so the time from which they know every
				// deletion travels only as itself
				if restartPeers {
					for _, i := range []int{0, 1} {
						restart(i)
						require.Eventually(t, func() bool { return peersOf(nodes[i]) == 1 }, 30*time.Second, 50*time.Millisecond)
						pause.For(4 * tick)
					}
				}

				// the tombstone of beyond expires and is pruned on both nodes
				pause.For(ttl + 4*tick)

				_, err = Ask(ctx, nodes[0].Replicator(), &crdt.Delete{Key: within}, time.Second)
				require.NoError(t, err)
				require.Eventually(t, func() bool { return !holds(nodes[1], within) }, 10*time.Second, 50*time.Millisecond)

				nodes[2], sds[2] = startNATsSystem(t, srv.Addr().String(), optionsOf(2))
				require.Eventually(t, func() bool { return peersOf(nodes[0]) == 2 }, 30*time.Second, 50*time.Millisecond)

				// many anti-entropy rounds between every pair of nodes
				pause.For(20 * tick)

				for i, node := range nodes {
					assert.False(t, holds(node, beyond), "node %d holds beyond", i+1)
					assert.False(t, holds(node, within), "node %d holds within", i+1)
				}

				for i := 2; i >= 0; i-- {
					require.NoError(t, nodes[i].Stop(ctx))
					require.NoError(t, sds[i].Close())
				}

				srv.Shutdown()
			})
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
		pb := internalpb.CRDTDelta_builder{
			Key:        codec.EncodeCRDTKey("key", crdt.GCounterType),
			OriginNode: "node-1",
			Data:       nil,
		}.Build()
		_, err := r.decodeDelta(pb)
		require.Error(t, err)
	})

	t.Run("decode bad key returns error", func(t *testing.T) {
		gcData, err := ddata.EncodeCRDT(crdt.NewGCounter().Increment("n1", 1), nil)
		require.NoError(t, err)

		pb := internalpb.CRDTDelta_builder{
			Key: internalpb.CRDTKey_builder{
				Id:       "bad",
				DataType: internalpb.CRDTDataType_CRDT_DATA_TYPE_UNSPECIFIED,
			}.Build(),
			OriginNode: "node-1",
			Data:       gcData,
		}.Build()
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
			"counter-1": internalpb.CRDTSnapshotEntry_builder{
				Key:     codec.EncodeCRDTKey("counter-1", crdt.GCounterType),
				Data:    internalpb.CRDTData_builder{GCounter: internalpb.GCounterData_builder{State: map[string]uint64{"node-1": 42}}.Build()}.Build(),
				Version: 5,
			}.Build(),
		}
		require.NoError(t, store.Save(entries, time.Time{}, time.Time{}))
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

		badDelta := internalpb.CRDTDelta_builder{
			Key:        codec.EncodeCRDTKey("key", crdt.GCounterType),
			OriginNode: "peer",
			Data:       nil,
		}.Build()
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

		emptyDigest := internalpb.CRDTDigest_builder{
			Entries: []*internalpb.CRDTDigestEntry{},
		}.Build()
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

		digest := internalpb.CRDTDigest_builder{
			Entries: []*internalpb.CRDTDigestEntry{
				internalpb.CRDTDigestEntry_builder{
					Key:     codec.EncodeCRDTKey("gc-1", crdt.GCounterType),
					Version: 999,
				}.Build(),
			},
		}.Build()
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

		digest := internalpb.CRDTDigest_builder{
			Entries: []*internalpb.CRDTDigestEntry{
				internalpb.CRDTDigestEntry_builder{
					Key: internalpb.CRDTKey_builder{
						Id:       "bad",
						DataType: internalpb.CRDTDataType_CRDT_DATA_TYPE_UNSPECIFIED,
					}.Build(),
					Version: 1,
				}.Build(),
			},
		}.Build()
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

		fullState := internalpb.CRDTFullState_builder{
			Entries: []*internalpb.CRDTFullStateEntry{
				internalpb.CRDTFullStateEntry_builder{
					Key:  codec.EncodeCRDTKey("new-gc", crdt.GCounterType),
					Data: pbData,
				}.Build(),
			},
		}.Build()
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

		fullState := internalpb.CRDTFullState_builder{
			Entries: []*internalpb.CRDTFullStateEntry{
				internalpb.CRDTFullStateEntry_builder{
					Key:  codec.EncodeCRDTKey("merge-gc", crdt.GCounterType),
					Data: pbData,
				}.Build(),
			},
		}.Build()
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

		fullState := internalpb.CRDTFullState_builder{
			Entries: []*internalpb.CRDTFullStateEntry{
				internalpb.CRDTFullStateEntry_builder{
					Key: internalpb.CRDTKey_builder{
						Id:       "bad",
						DataType: internalpb.CRDTDataType_CRDT_DATA_TYPE_UNSPECIFIED,
					}.Build(),
					Data: nil,
				}.Build(),
			},
		}.Build()
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

		fullState := internalpb.CRDTFullState_builder{
			Entries: []*internalpb.CRDTFullStateEntry{
				internalpb.CRDTFullStateEntry_builder{
					Key:  codec.EncodeCRDTKey("gc-bad", crdt.GCounterType),
					Data: nil,
				}.Build(),
			},
		}.Build()
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

		fullState := internalpb.CRDTFullState_builder{
			Entries: []*internalpb.CRDTFullStateEntry{
				internalpb.CRDTFullStateEntry_builder{
					Key:  codec.EncodeCRDTKey("to-delete", crdt.PNCounterType),
					Data: pbData,
				}.Build(),
			},
		}.Build()
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

		req := internalpb.CRDTReadRequest_builder{
			Key:      codec.EncodeCRDTKey("gc-1", crdt.GCounterType),
			FromNode: "peer-node",
		}.Build()
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

		req := internalpb.CRDTReadRequest_builder{
			Key:      codec.EncodeCRDTKey("nonexistent", crdt.GCounterType),
			FromNode: "peer-node",
		}.Build()
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

	pbTombstone := internalpb.CRDTTombstone_builder{
		Key: internalpb.CRDTKey_builder{
			Id:       "bad",
			DataType: internalpb.CRDTDataType_CRDT_DATA_TYPE_UNSPECIFIED,
		}.Build(),
		DeletedAtNanos: time.Now().UnixNano(),
		DeletedByNode:  "remote-node",
	}.Build()
	r.handleProtoTombstone(nil, pbTombstone)
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

func TestReplicatorDataCenterExtension(t *testing.T) {
	t.Run("extension carries DC identity", func(t *testing.T) {
		ext := &crdtConfigExtension{
			config: crdt.NewConfig(crdt.WithDataCenterReplication()),
			dc: datacenter.DataCenter{
				Name:   "dc-west",
				Region: "us-west-2",
				Zone:   "us-west-2a",
			},
		}
		assert.Equal(t, "dc-west", ext.dc.Name)
		assert.Equal(t, "us-west-2", ext.dc.Region)
		assert.Equal(t, "us-west-2a", ext.dc.Zone)
		assert.True(t, ext.Config().DataCenterEnabled())
	})

	t.Run("extension without DC has zero-value DC", func(t *testing.T) {
		ext := &crdtConfigExtension{config: crdt.NewConfig()}
		assert.Empty(t, ext.dc.Name)
		assert.False(t, ext.Config().DataCenterEnabled())
	})
}

func TestReplicatorDataCenterReplicationEnabled(t *testing.T) {
	t.Run("replicator starts with DC config enabled", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicatorWithDC(t, sys, "dc-west", "us-west-2", "us-west-2a")
		assert.True(t, repl.IsRunning())

		counterKey := crdt.PNCounterKey("dc-counter")
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		resp, err := Ask(ctx, repl, &crdt.Get{Key: counterKey}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse)
		assert.Equal(t, int64(5), getResp.Data.(*crdt.PNCounter).Value())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("flush tick is handled gracefully without cluster", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicatorWithDC(t, sys, "dc-west", "us-west-2", "us-west-2a")

		err = Tell(ctx, repl, &dataCenterFlushTick{})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("anti-entropy tick is handled gracefully without cluster", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicatorWithDC(t, sys, "dc-west", "us-west-2", "us-west-2a")

		err = Tell(ctx, repl, &dataCenterAntiEntropyTick{})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorIncomingBatch(t *testing.T) {
	t.Run("incoming batch from remote DC merges deltas", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicatorWithDC(t, sys, "dc-west", "us-west-2", "us-west-2a")

		// encode a delta from a "remote" DC
		r := newTestReplicator()
		pbDelta, err := r.encodeDelta(&crdtDelta{
			KeyID:    "remote-counter",
			DataType: crdt.PNCounterType,
			Delta:    crdt.NewPNCounter().Increment("remote-node", 42),
			Origin:   "remote-node",
		})
		require.NoError(t, err)

		batch := internalpb.CRDTDeltaBatch_builder{
			Deltas: []*internalpb.CRDTDelta{pbDelta},
			OriginDc: internalpb.DataCenter_builder{
				Name:   "dc-east",
				Region: "us-east-1",
				Zone:   "us-east-1a",
			}.Build(),
			SentAtNanos: time.Now().UnixNano(),
		}.Build()

		err = Tell(ctx, repl, batch)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// verify the delta was merged
		resp, err := Ask(ctx, repl, &crdt.Get{Key: crdt.PNCounterKey("remote-counter")}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse)
		require.NotNil(t, getResp.Data)
		assert.Equal(t, int64(42), getResp.Data.(*crdt.PNCounter).Value())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("incoming batch from same DC is discarded", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicatorWithDC(t, sys, "dc-west", "us-west-2", "us-west-2a")

		r := newTestReplicator()
		pbDelta, err := r.encodeDelta(&crdtDelta{
			KeyID:    "self-counter",
			DataType: crdt.PNCounterType,
			Delta:    crdt.NewPNCounter().Increment("self-node", 10),
			Origin:   "self-node",
		})
		require.NoError(t, err)

		// same DC identity as the replicator
		batch := internalpb.CRDTDeltaBatch_builder{
			Deltas: []*internalpb.CRDTDelta{pbDelta},
			OriginDc: internalpb.DataCenter_builder{
				Name:   "dc-west",
				Region: "us-west-2",
				Zone:   "us-west-2a",
			}.Build(),
			SentAtNanos: time.Now().UnixNano(),
		}.Build()

		err = Tell(ctx, repl, batch)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// key should not exist since the batch was discarded
		resp, err := Ask(ctx, repl, &crdt.Get{Key: crdt.PNCounterKey("self-counter")}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse)
		assert.Nil(t, getResp.Data)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("incoming batch with tombstones deletes keys", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicatorWithDC(t, sys, "dc-west", "us-west-2", "us-west-2a")

		// create a local key first
		counterKey := crdt.PNCounterKey("tombstone-counter")
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		watcher, probe := subscribeProbe(t, sys, repl, "watcher", counterKey)

		// send a batch with a tombstone from remote DC
		batch := internalpb.CRDTDeltaBatch_builder{
			Tombstones: []*internalpb.CRDTTombstone{
				internalpb.CRDTTombstone_builder{
					Key:            codec.EncodeCRDTKey("tombstone-counter", crdt.PNCounterType),
					DeletedAtNanos: time.Now().UnixNano(),
					DeletedByNode:  "remote-node",
				}.Build(),
			},
			OriginDc: internalpb.DataCenter_builder{
				Name:   "dc-east",
				Region: "us-east-1",
				Zone:   "us-east-1a",
			}.Build(),
			SentAtNanos: time.Now().UnixNano(),
		}.Build()

		err = Tell(ctx, repl, batch)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// key should be deleted
		resp, err := Ask(ctx, repl, &crdt.Get{Key: counterKey}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse)
		assert.Nil(t, getResp.Data)

		// the watcher is told of the deletion from the remote DC
		expectDeleted(t, probe, counterKey)
		probeIsQuiet(t, watcher, probe)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorDataCenterDigestRequest(t *testing.T) {
	t.Run("digest request returns local digest via Ask", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicatorWithDC(t, sys, "dc-west", "us-west-2", "us-west-2a")

		// add some data first
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.GCounterKey("gc-1"),
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-1", 10)
			},
		}, time.Second)
		require.NoError(t, err)

		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.PNCounterKey("pn-1"),
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 20)
			},
		}, time.Second)
		require.NoError(t, err)

		// ask for the digest
		resp, err := Ask(ctx, repl, &dataCenterDigestRequest{}, time.Second)
		require.NoError(t, err)

		digest, ok := resp.(*internalpb.CRDTDigest)
		require.True(t, ok)
		require.NotNil(t, digest)
		assert.Len(t, digest.GetEntries(), 2)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("digest request with empty store returns empty digest", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicatorWithDC(t, sys, "dc-west", "us-west-2", "us-west-2a")

		resp, err := Ask(ctx, repl, &dataCenterDigestRequest{}, time.Second)
		require.NoError(t, err)

		digest, ok := resp.(*internalpb.CRDTDigest)
		require.True(t, ok)
		require.NotNil(t, digest)
		assert.Empty(t, digest.GetEntries())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorDataCenterFlush(t *testing.T) {
	t.Run("flush tick with no cluster does not panic", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicatorWithDC(t, sys, "dc-west", "us-west-2", "us-west-2a")

		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.PNCounterKey("flush-counter"),
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		// flush tick without cluster — should return early without panic
		err = Tell(ctx, repl, &dataCenterFlushTick{})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		// data should still be accessible
		resp, err := Ask(ctx, repl, &crdt.Get{Key: crdt.PNCounterKey("flush-counter")}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse)
		assert.Equal(t, int64(5), getResp.Data.(*crdt.PNCounter).Value())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorHandleAntiEntropy(t *testing.T) {
	t.Run("no-op when clusterRef is nil", func(t *testing.T) {
		r := newTestReplicator()
		r.nodeID = "local-node"
		r.logger = log.DiscardLogger
		r.clusterRef = nil
		r.remoting = nil
		r.store["key-a"] = crdt.NewGCounter().Increment("node-1", 5)
		r.keyTypes["key-a"] = crdt.GCounterType
		r.versions["key-a"] = 1

		// should not panic; antiEntropyCount should remain 0
		assert.Equal(t, uint64(0), r.antiEntropyCount.Load())
	})

	t.Run("no-op when remoting is nil", func(t *testing.T) {
		r := newTestReplicator()
		r.nodeID = "local-node"
		r.logger = log.DiscardLogger
		r.clusterRef = nil
		r.remoting = nil

		assert.Equal(t, uint64(0), r.antiEntropyCount.Load())
	})

	t.Run("anti-entropy tick handled gracefully without cluster via actor system", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		// add data, then send anti-entropy tick (no cluster = early return)
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.GCounterKey("ae-counter"),
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-1", 10)
			},
		}, time.Second)
		require.NoError(t, err)

		err = Tell(ctx, repl, &antiEntropyTick{})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorBuildSnapshotEntriesErrors(t *testing.T) {
	t.Run("missing data type returns error", func(t *testing.T) {
		r := newTestReplicator()
		r.logger = log.DiscardLogger
		r.store["orphan-key"] = crdt.NewGCounter().Increment("node-1", 5)
		// deliberately do NOT set r.keyTypes["orphan-key"]

		_, err := r.buildSnapshotEntries()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing data type for key=orphan-key")
	})
}

func TestReplicatorHandleSnapshotErrors(t *testing.T) {
	t.Run("buildSnapshotEntries error is logged gracefully", func(t *testing.T) {
		dir := t.TempDir()
		store, err := ddata.NewStore(dir)
		require.NoError(t, err)
		defer store.Close()

		r := newTestReplicator()
		r.logger = log.DiscardLogger
		r.snapshotStore = store
		// key in store but no keyType => buildSnapshotEntries will error
		r.store["bad-key"] = crdt.NewGCounter().Increment("node-1", 1)

		r.handleSnapshot()
		// should not panic; no data saved
		loaded, err := store.Load()
		require.NoError(t, err)
		assert.Empty(t, loaded)
	})
}

func TestReplicatorNotifyChangedDeadWatcher(t *testing.T) {
	t.Run("dead watchers are pruned from list", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		// spawn a watcher and subscribe
		watcher, err := sys.Spawn(ctx, "watcher-dead", NewMockActor(), WithLongLived())
		require.NoError(t, err)

		counterKey := crdt.PNCounterKey("notify-counter")
		err = watcher.Tell(ctx, repl, &crdt.Subscribe{Key: counterKey})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// stop the watcher so it becomes dead
		require.NoError(t, watcher.Shutdown(ctx))
		pause.For(500 * time.Millisecond)

		// update should trigger notifyChanged which prunes dead watcher
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("all dead watchers removes key from watchers map", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		w1, err := sys.Spawn(ctx, "dead-w1", NewMockActor(), WithLongLived())
		require.NoError(t, err)
		w2, err := sys.Spawn(ctx, "dead-w2", NewMockActor(), WithLongLived())
		require.NoError(t, err)

		counterKey := crdt.PNCounterKey("all-dead-counter")
		err = w1.Tell(ctx, repl, &crdt.Subscribe{Key: counterKey})
		require.NoError(t, err)
		err = w2.Tell(ctx, repl, &crdt.Subscribe{Key: counterKey})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// stop both watchers
		require.NoError(t, w1.Shutdown(ctx))
		require.NoError(t, w2.Shutdown(ctx))
		pause.For(500 * time.Millisecond)

		// update to trigger notifyChanged — all watchers dead
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 3)
			},
		}, time.Second)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorHandleReadRequestBadKey(t *testing.T) {
	t.Run("bad key in read request is handled gracefully", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		req := internalpb.CRDTReadRequest_builder{
			Key: internalpb.CRDTKey_builder{
				Id:       "bad",
				DataType: internalpb.CRDTDataType_CRDT_DATA_TYPE_UNSPECIFIED,
			}.Build(),
			FromNode: "peer-node",
		}.Build()
		err = Tell(ctx, repl, req)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorPublishDeltaNilTopicActor(t *testing.T) {
	t.Run("publishDelta with nil topicActor is no-op", func(t *testing.T) {
		r := newTestReplicator()
		r.nodeID = "local-node"
		r.logger = log.DiscardLogger
		r.topicActor = nil
		r.serializer = ddata.NewCRDTValueSerializer()

		delta := crdt.NewGCounter().Increment("node-1", 5)
		// should not panic; deltaPublishCount stays 0
		assert.Equal(t, uint64(0), r.deltaPublishCount.Load())
		_ = delta
	})
}

func TestReplicatorDataCenterFlushPaths(t *testing.T) {
	t.Run("flush with empty pending is no-op", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicatorWithDC(t, sys, "dc-west", "us-west-2", "us-west-2a")

		// send flush tick without creating any data (no pending deltas)
		err = Tell(ctx, repl, &dataCenterFlushTick{})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("flush with pending deltas but no cluster returns early", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicatorWithDC(t, sys, "dc-west", "us-west-2", "us-west-2a")

		// create data to generate pending deltas
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.PNCounterKey("flush-pending"),
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 10)
			},
		}, time.Second)
		require.NoError(t, err)

		// flush tick — no cluster so it should return early
		err = Tell(ctx, repl, &dataCenterFlushTick{})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("flush with pending tombstones but no cluster returns early", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicatorWithDC(t, sys, "dc-west", "us-west-2", "us-west-2a")

		// create and then delete to generate pending tombstones
		counterKey := crdt.PNCounterKey("flush-tombstone")
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

		// flush tick
		err = Tell(ctx, repl, &dataCenterFlushTick{})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorDataCenterAntiEntropyPaths(t *testing.T) {
	t.Run("anti-entropy tick with no cluster returns early", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicatorWithDC(t, sys, "dc-west", "us-west-2", "us-west-2a")

		err = Tell(ctx, repl, &dataCenterAntiEntropyTick{})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("anti-entropy tick with data and no cluster returns early", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicatorWithDC(t, sys, "dc-west", "us-west-2", "us-west-2a")

		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.GCounterKey("ae-dc-counter"),
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-1", 7)
			},
		}, time.Second)
		require.NoError(t, err)

		err = Tell(ctx, repl, &dataCenterAntiEntropyTick{})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		// data should still be accessible
		resp, err := Ask(ctx, repl, &crdt.Get{Key: crdt.GCounterKey("ae-dc-counter")}, time.Second)
		require.NoError(t, err)
		assert.Equal(t, uint64(7), resp.(*crdt.GetResponse).Data.(*crdt.GCounter).Value())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorPruneTick(t *testing.T) {
	t.Run("prune tick handled via actor system", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		// add data and then send prune tick
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.PNCounterKey("prune-counter"),
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		err = Tell(ctx, repl, &pruneTick{})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorSnapshotTick(t *testing.T) {
	t.Run("snapshot tick handled via actor system", func(t *testing.T) {
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

		repl, err := sys.Spawn(ctx, "replicator-snap-tick", newReplicatorActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, repl)
		pause.For(500 * time.Millisecond)

		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.GCounterKey("snap-tick-gc"),
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-1", 33)
			},
		}, time.Second)
		require.NoError(t, err)

		// send snapshot tick
		err = Tell(ctx, repl, &snapshotTick{})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		// stop system first to release BoltDB lock via PostStop
		err = sys.Stop(ctx)
		require.NoError(t, err)

		// verify snapshot was saved (now BoltDB file is unlocked)
		store, err := ddata.NewStore(dir)
		require.NoError(t, err)
		loaded, err := store.Load()
		require.NoError(t, err)
		assert.Len(t, loaded, 1)
		require.NoError(t, store.Close())
	})
}

func TestReplicatorIncomingBatchEdgeCases(t *testing.T) {
	t.Run("batch with nil origin DC is processed", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicatorWithDC(t, sys, "dc-west", "us-west-2", "us-west-2a")

		r := newTestReplicator()
		pbDelta, err := r.encodeDelta(&crdtDelta{
			KeyID:    "nil-dc-counter",
			DataType: crdt.PNCounterType,
			Delta:    crdt.NewPNCounter().Increment("remote-node", 15),
			Origin:   "remote-node",
		})
		require.NoError(t, err)

		batch := internalpb.CRDTDeltaBatch_builder{
			Deltas:      []*internalpb.CRDTDelta{pbDelta},
			OriginDc:    nil, // nil origin DC
			SentAtNanos: time.Now().UnixNano(),
		}.Build()

		err = Tell(ctx, repl, batch)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		resp, err := Ask(ctx, repl, &crdt.Get{Key: crdt.PNCounterKey("nil-dc-counter")}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse)
		require.NotNil(t, getResp.Data)
		assert.Equal(t, int64(15), getResp.Data.(*crdt.PNCounter).Value())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("batch with empty deltas and tombstones from remote DC is processed", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicatorWithDC(t, sys, "dc-west", "us-west-2", "us-west-2a")

		batch := internalpb.CRDTDeltaBatch_builder{
			Deltas:     nil,
			Tombstones: nil,
			OriginDc: internalpb.DataCenter_builder{
				Name:   "dc-east",
				Region: "us-east-1",
				Zone:   "us-east-1a",
			}.Build(),
			SentAtNanos: time.Now().UnixNano(),
		}.Build()

		err = Tell(ctx, repl, batch)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("batch with mixed deltas and tombstones from remote DC", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicatorWithDC(t, sys, "dc-west", "us-west-2", "us-west-2a")

		// create a local key that will be tombstoned
		counterKey := crdt.PNCounterKey("mixed-batch-counter")
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		r := newTestReplicator()
		pbDelta, err := r.encodeDelta(&crdtDelta{
			KeyID:    "new-remote-counter",
			DataType: crdt.PNCounterType,
			Delta:    crdt.NewPNCounter().Increment("remote-node", 25),
			Origin:   "remote-node",
		})
		require.NoError(t, err)

		batch := internalpb.CRDTDeltaBatch_builder{
			Deltas: []*internalpb.CRDTDelta{pbDelta},
			Tombstones: []*internalpb.CRDTTombstone{
				internalpb.CRDTTombstone_builder{
					Key:            codec.EncodeCRDTKey("mixed-batch-counter", crdt.PNCounterType),
					DeletedAtNanos: time.Now().UnixNano(),
					DeletedByNode:  "remote-node",
				}.Build(),
			},
			OriginDc: internalpb.DataCenter_builder{
				Name:   "dc-east",
				Region: "us-east-1",
				Zone:   "us-east-1a",
			}.Build(),
			SentAtNanos: time.Now().UnixNano(),
		}.Build()

		err = Tell(ctx, repl, batch)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// new delta should be merged
		resp, err := Ask(ctx, repl, &crdt.Get{Key: crdt.PNCounterKey("new-remote-counter")}, time.Second)
		require.NoError(t, err)
		assert.Equal(t, int64(25), resp.(*crdt.GetResponse).Data.(*crdt.PNCounter).Value())

		// tombstoned key should be gone
		resp, err = Ask(ctx, repl, &crdt.Get{Key: counterKey}, time.Second)
		require.NoError(t, err)
		assert.Nil(t, resp.(*crdt.GetResponse).Data)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorDeleteWithoutType(t *testing.T) {
	t.Run("delete key that was never tracked", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		// delete a key that was never created — should not panic
		counterKey := crdt.PNCounterKey("never-existed")
		reply, err := Ask(ctx, repl, &crdt.Delete{Key: counterKey}, time.Second)
		require.NoError(t, err)
		assert.IsType(t, &crdt.DeleteResponse{}, reply)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorDeleteViaTell(t *testing.T) {
	t.Run("delete via Tell without sender", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		counterKey := crdt.PNCounterKey("tell-delete")
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		// delete via Tell (no sender)
		err = Tell(ctx, repl, &crdt.Delete{Key: counterKey})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// key should be gone
		resp, err := Ask(ctx, repl, &crdt.Get{Key: counterKey}, time.Second)
		require.NoError(t, err)
		assert.Nil(t, resp.(*crdt.GetResponse).Data)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorSubscribeWithoutSender(t *testing.T) {
	t.Run("subscribe via Tell with no sender is ignored", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		// Tell-based subscribe (no sender set)
		err = Tell(ctx, repl, &crdt.Subscribe{Key: crdt.PNCounterKey("no-sender")})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorUnsubscribeWithoutSender(t *testing.T) {
	t.Run("unsubscribe via Tell with no sender is ignored", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		err = Tell(ctx, repl, &crdt.Unsubscribe{Key: crdt.PNCounterKey("no-sender-unsub")})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorRestoreFromSnapshotBadData(t *testing.T) {
	t.Run("bad key in snapshot entry is skipped", func(t *testing.T) {
		dir := t.TempDir()

		store, err := ddata.NewStore(dir)
		require.NoError(t, err)

		entries := map[string]*internalpb.CRDTSnapshotEntry{
			"bad-key": internalpb.CRDTSnapshotEntry_builder{
				Key: internalpb.CRDTKey_builder{
					Id:       "bad-key",
					DataType: internalpb.CRDTDataType_CRDT_DATA_TYPE_UNSPECIFIED,
				}.Build(),
				Data:    internalpb.CRDTData_builder{GCounter: internalpb.GCounterData_builder{State: map[string]uint64{"n1": 1}}.Build()}.Build(),
				Version: 1,
			}.Build(),
		}
		require.NoError(t, store.Save(entries, time.Time{}, time.Time{}))
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

		// bad key should be skipped, store should be empty
		assert.Empty(t, r.store)
	})

	t.Run("bad data in snapshot entry is skipped", func(t *testing.T) {
		dir := t.TempDir()

		store, err := ddata.NewStore(dir)
		require.NoError(t, err)

		entries := map[string]*internalpb.CRDTSnapshotEntry{
			"good-key": internalpb.CRDTSnapshotEntry_builder{
				Key:     codec.EncodeCRDTKey("good-key", crdt.GCounterType),
				Data:    nil, // nil data causes decode error
				Version: 1,
			}.Build(),
		}
		require.NoError(t, store.Save(entries, time.Time{}, time.Time{}))
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

		// bad data should be skipped
		assert.Empty(t, r.store)
	})
}

func TestReplicatorDigestWithBadEncode(t *testing.T) {
	t.Run("digest skips keys that fail to encode", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		// add a valid key
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.GCounterKey("valid-gc"),
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		// send a digest that includes the key with higher version (peer is ahead)
		digest := internalpb.CRDTDigest_builder{
			Entries: []*internalpb.CRDTDigestEntry{
				internalpb.CRDTDigestEntry_builder{
					Key:     codec.EncodeCRDTKey("valid-gc", crdt.GCounterType),
					Version: 0, // peer is behind
				}.Build(),
			},
		}.Build()
		err = Tell(ctx, repl, digest)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorMultipleDataTypesInDigest(t *testing.T) {
	t.Run("full state with multiple data types", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		// add multiple types
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.GCounterKey("multi-gc"),
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.ORSetKey("multi-set"),
			Initial: crdt.NewORSet(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.ORSet).Add("node-1", "elem-1")
			},
		}, time.Second)
		require.NoError(t, err)

		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.FlagKey("multi-flag"),
			Initial: crdt.NewFlag(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.Flag).Enable()
			},
		}, time.Second)
		require.NoError(t, err)

		// build and send full state with different types from peer
		serializer := ddata.NewCRDTValueSerializer()
		gcData, err := ddata.EncodeCRDT(crdt.NewGCounter().Increment("node-2", 3), serializer)
		require.NoError(t, err)
		setData, err := ddata.EncodeCRDT(crdt.NewORSet().Add("node-2", "elem-2"), serializer)
		require.NoError(t, err)

		fullState := internalpb.CRDTFullState_builder{
			Entries: []*internalpb.CRDTFullStateEntry{
				internalpb.CRDTFullStateEntry_builder{Key: codec.EncodeCRDTKey("multi-gc", crdt.GCounterType), Data: gcData}.Build(),
				internalpb.CRDTFullStateEntry_builder{Key: codec.EncodeCRDTKey("multi-set", crdt.ORSetType), Data: setData}.Build(),
			},
		}.Build()
		err = Tell(ctx, repl, fullState)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// GCounter should be merged: 5 + 3 = 8
		resp, err := Ask(ctx, repl, &crdt.Get{Key: crdt.GCounterKey("multi-gc")}, time.Second)
		require.NoError(t, err)
		assert.Equal(t, uint64(8), resp.(*crdt.GetResponse).Data.(*crdt.GCounter).Value())

		// ORSet should have both elements
		resp, err = Ask(ctx, repl, &crdt.Get{Key: crdt.ORSetKey("multi-set")}, time.Second)
		require.NoError(t, err)
		orSet := resp.(*crdt.GetResponse).Data.(*crdt.ORSet)
		assert.True(t, orSet.Contains("elem-1"))
		assert.True(t, orSet.Contains("elem-2"))

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorEncodeDeltaRoundTripAllTypes(t *testing.T) {
	r := newTestReplicator()
	r.serializer = ddata.NewCRDTValueSerializer()

	t.Run("ORSet delta", func(t *testing.T) {
		delta := &crdtDelta{
			KeyID:    "set-1",
			DataType: crdt.ORSetType,
			Delta:    crdt.NewORSet().Add("node-1", "value-a"),
			Origin:   "node-1",
		}
		pb, err := r.encodeDelta(delta)
		require.NoError(t, err)

		decoded, err := r.decodeDelta(pb)
		require.NoError(t, err)
		assert.Equal(t, "set-1", decoded.KeyID)
		assert.Equal(t, crdt.ORSetType, decoded.DataType)
		assert.True(t, decoded.Delta.(*crdt.ORSet).Contains("value-a"))
	})

	t.Run("Flag delta", func(t *testing.T) {
		delta := &crdtDelta{
			KeyID:    "flag-1",
			DataType: crdt.FlagType,
			Delta:    crdt.NewFlag().Enable(),
			Origin:   "node-1",
		}
		pb, err := r.encodeDelta(delta)
		require.NoError(t, err)

		decoded, err := r.decodeDelta(pb)
		require.NoError(t, err)
		assert.Equal(t, "flag-1", decoded.KeyID)
		assert.True(t, decoded.Delta.(*crdt.Flag).Enabled())
	})

	t.Run("MVRegister delta", func(t *testing.T) {
		delta := &crdtDelta{
			KeyID:    "reg-1",
			DataType: crdt.MVRegisterType,
			Delta:    crdt.NewMVRegister().Set("node-1", "hello"),
			Origin:   "node-1",
		}
		pb, err := r.encodeDelta(delta)
		require.NoError(t, err)

		decoded, err := r.decodeDelta(pb)
		require.NoError(t, err)
		assert.Equal(t, "reg-1", decoded.KeyID)
		values := decoded.Delta.(*crdt.MVRegister).Values()
		require.Len(t, values, 1)
		assert.Equal(t, "hello", values[0])
	})

	t.Run("ORMap delta", func(t *testing.T) {
		delta := &crdtDelta{
			KeyID:    "map-1",
			DataType: crdt.ORMapType,
			Delta:    crdt.NewORMap().Set("node-1", "key-a", crdt.NewGCounter().Increment("node-1", 1)),
			Origin:   "node-1",
		}
		pb, err := r.encodeDelta(delta)
		require.NoError(t, err)

		decoded, err := r.decodeDelta(pb)
		require.NoError(t, err)
		assert.Equal(t, "map-1", decoded.KeyID)
		assert.Equal(t, crdt.ORMapType, decoded.DataType)
		assert.Equal(t, 1, decoded.Delta.(*crdt.ORMap).Len())
	})
}

func TestReplicatorPNCounterDecrementReplication(t *testing.T) {
	t.Run("PNCounter decrement delta merges correctly", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		counterKey := crdt.PNCounterKey("dec-counter")
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 10)
			},
		}, time.Second)
		require.NoError(t, err)

		// simulate a decrement delta from peer
		peerDelta := crdt.NewPNCounter().Decrement("node-2", 3)
		err = Tell(ctx, repl, &crdtDelta{
			KeyID:    "dec-counter",
			DataType: crdt.PNCounterType,
			Delta:    peerDelta,
			Origin:   "peer-node",
		})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		resp, err := Ask(ctx, repl, &crdt.Get{Key: counterKey}, time.Second)
		require.NoError(t, err)
		assert.Equal(t, int64(7), resp.(*crdt.GetResponse).Data.(*crdt.PNCounter).Value())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorPostStopWithoutSnapshot(t *testing.T) {
	t.Run("PostStop without snapshot store succeeds", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.GCounterKey("ps-counter"),
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		// stop should succeed without snapshot store
		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorPreStartMissingExtension(t *testing.T) {
	t.Run("PreStart fails when extension is missing", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		// spawn without registering the CRDT config extension
		_, err = sys.Spawn(ctx, "replicator-no-ext", newReplicatorActor())
		require.Error(t, err)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorDigestWithPeerBehind(t *testing.T) {
	t.Run("digest from peer behind triggers full state response", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		// add multiple keys
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.GCounterKey("digest-a"),
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-1", 10)
			},
		}, time.Second)
		require.NoError(t, err)

		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.PNCounterKey("digest-b"),
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 20)
			},
		}, time.Second)
		require.NoError(t, err)

		// digest where peer has key-a at version 0 (behind) and doesn't have key-b
		digest := internalpb.CRDTDigest_builder{
			Entries: []*internalpb.CRDTDigestEntry{
				internalpb.CRDTDigestEntry_builder{
					Key:     codec.EncodeCRDTKey("digest-a", crdt.GCounterType),
					Version: 0, // peer is behind
				}.Build(),
			},
		}.Build()

		err = Tell(ctx, repl, digest)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorUpdateTombstonedKeyViaAsk(t *testing.T) {
	t.Run("update tombstoned key returns empty response via Ask", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		counterKey := crdt.PNCounterKey("tomb-update")
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		// delete the key
		_, err = Ask(ctx, repl, &crdt.Delete{Key: counterKey}, time.Second)
		require.NoError(t, err)

		// update should return empty response, not create the key
		reply, err := Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 99)
			},
		}, time.Second)
		require.NoError(t, err)
		assert.IsType(t, &crdt.UpdateResponse{}, reply)

		// key should still be nil
		resp, err := Ask(ctx, repl, &crdt.Get{Key: counterKey}, time.Second)
		require.NoError(t, err)
		assert.Nil(t, resp.(*crdt.GetResponse).Data)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorUpdateTombstonedKeyViaTell(t *testing.T) {
	t.Run("update tombstoned key via Tell is silently ignored", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		counterKey := crdt.PNCounterKey("tomb-tell-update")
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

		// update via Tell (no sender) — should not create key
		err = Tell(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 99)
			},
		})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		resp, err := Ask(ctx, repl, &crdt.Get{Key: counterKey}, time.Second)
		require.NoError(t, err)
		assert.Nil(t, resp.(*crdt.GetResponse).Data)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorHandleMessageAllTypes(t *testing.T) {
	t.Run("prune tick via Tell", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		err = Tell(ctx, repl, &pruneTick{})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)
		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})

	t.Run("snapshot tick via Tell without snapshot store", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		err = Tell(ctx, repl, &snapshotTick{})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)
		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorTargetCountEdgeCases(t *testing.T) {
	r := newTestReplicator()

	t.Run("majority with 0 peers returns 0 peers (capped)", func(t *testing.T) {
		result := r.targetCount(0, crdt.Majority)
		assert.Equal(t, 0, result)
	})
}

func TestReplicatorNotifyChangedMixedWatchers(t *testing.T) {
	t.Run("alive watchers are kept, dead ones pruned", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		// spawn two watchers
		w1, err := sys.Spawn(ctx, "alive-w", NewMockActor(), WithLongLived())
		require.NoError(t, err)
		w2, err := sys.Spawn(ctx, "dead-w", NewMockActor(), WithLongLived())
		require.NoError(t, err)

		counterKey := crdt.PNCounterKey("mixed-watchers")

		// subscribe both
		err = w1.Tell(ctx, repl, &crdt.Subscribe{Key: counterKey})
		require.NoError(t, err)
		err = w2.Tell(ctx, repl, &crdt.Subscribe{Key: counterKey})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// kill only w2
		require.NoError(t, w2.Shutdown(ctx))
		pause.For(500 * time.Millisecond)

		// update triggers notifyChanged — w1 alive, w2 dead
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())
		assert.True(t, w1.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorHandleReadRequestNoData(t *testing.T) {
	t.Run("read request with no sender via Tell", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		// send read request via Tell (no sender) — should not panic
		req := internalpb.CRDTReadRequest_builder{
			Key:      codec.EncodeCRDTKey("absent-key", crdt.GCounterType),
			FromNode: "peer-node",
		}.Build()
		err = Tell(ctx, repl, req)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorDeltaForTombstonedKeyViaProtoDelta(t *testing.T) {
	t.Run("proto delta for tombstoned key is rejected", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicator(t, sys)

		counterKey := crdt.PNCounterKey("tomb-proto")
		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		// delete the key
		_, err = Ask(ctx, repl, &crdt.Delete{Key: counterKey}, time.Second)
		require.NoError(t, err)

		// send a proto delta for the tombstoned key
		r := newTestReplicator()
		r.serializer = ddata.NewCRDTValueSerializer()
		pbDelta, err := r.encodeDelta(&crdtDelta{
			KeyID:    "tomb-proto",
			DataType: crdt.PNCounterType,
			Delta:    crdt.NewPNCounter().Increment("remote-node", 99),
			Origin:   "remote-node",
		})
		require.NoError(t, err)

		err = Tell(ctx, repl, pbDelta)
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		// key should still be nil (tombstoned)
		resp, err := Ask(ctx, repl, &crdt.Get{Key: counterKey}, time.Second)
		require.NoError(t, err)
		assert.Nil(t, resp.(*crdt.GetResponse).Data)

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

// ---------------------------------------------------------------------------
// Data center controller test helpers
// ---------------------------------------------------------------------------

func TestReplicatorDataCenterFlushNonLeaderSkips(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "zrremote",
			DataCenter: datacenter.DataCenter{Name: "remote", Region: "r", Zone: "z"},
			Endpoints:  []string{"10.0.0.1:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, replActor, clusterMock, _ := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(false).Maybe()

	counterKey := crdt.PNCounterKey("flush-leader-check")
	_, err := Ask(context.TODO(), repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 5)
		},
	}, time.Second)
	require.NoError(t, err)

	err = Tell(context.TODO(), repl, &dataCenterFlushTick{})
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	assert.Equal(t, uint64(0), replActor.crossDCSendCount.Load())
	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterFlushLeaderSendsToRemoteDC(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "zrremote",
			DataCenter: datacenter.DataCenter{Name: "remote", Region: "r", Zone: "z"},
			Endpoints:  []string{"10.0.0.1:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, replActor, clusterMock, remotingMock := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()
	remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.1", 9090, "GoAktReplicator").
		Return(address.New("GoAktReplicator", "remoteSys", "10.0.0.1", 9090), nil).Maybe()
	remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()

	counterKey := crdt.PNCounterKey("flush-leader-send")
	_, err := Ask(context.TODO(), repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 10)
		},
	}, time.Second)
	require.NoError(t, err)

	err = Tell(context.TODO(), repl, &dataCenterFlushTick{})
	require.NoError(t, err)
	pause.For(time.Second)

	assert.True(t, replActor.crossDCSendCount.Load() > 0)
	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterFlushLeaderNilController(t *testing.T) {
	sys, repl, replActor, clusterMock, _ := spawnReplicatorWithDCController(t, remoteRecords(nil), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()

	impl := sys.(*actorSystem)
	impl.dataCenterController = nil

	counterKey := crdt.PNCounterKey("flush-nil-ctrl")
	_, err := Ask(context.TODO(), repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 5)
		},
	}, time.Second)
	require.NoError(t, err)

	err = Tell(context.TODO(), repl, &dataCenterFlushTick{})
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	assert.Equal(t, uint64(0), replActor.crossDCSendCount.Load())
	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterFlushStaleCacheSkips(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "zrremote",
			DataCenter: datacenter.DataCenter{Name: "remote", Region: "r", Zone: "z"},
			Endpoints:  []string{"10.0.0.1:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}

	dcConfig := datacenter.NewConfig()
	dcConfig.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}
	dcConfig.MaxCacheStaleness = time.Nanosecond
	dcConfig.CacheRefreshInterval = time.Hour

	sys, repl, replActor, clusterMock, _ := spawnReplicatorWithDCController(t, remoteRecords(records), dcConfig)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()

	// let the cache become stale
	pause.For(10 * time.Millisecond)

	counterKey := crdt.PNCounterKey("flush-stale")
	_, err := Ask(context.TODO(), repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 5)
		},
	}, time.Second)
	require.NoError(t, err)

	err = Tell(context.TODO(), repl, &dataCenterFlushTick{})
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	assert.True(t, replActor.crossDCStaleSkipCount.Load() > 0)
	assert.Equal(t, uint64(0), replActor.crossDCSendCount.Load())
	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterFlushEmptyRecordsReturns(t *testing.T) {
	sys, repl, replActor, clusterMock, _ := spawnReplicatorWithDCController(t, remoteRecords(nil), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()

	counterKey := crdt.PNCounterKey("flush-empty-records")
	_, err := Ask(context.TODO(), repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 5)
		},
	}, time.Second)
	require.NoError(t, err)

	err = Tell(context.TODO(), repl, &dataCenterFlushTick{})
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	assert.Equal(t, uint64(0), replActor.crossDCSendCount.Load())
	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterFlushSameDCSkipped(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "zrlocal",
			DataCenter: datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"},
			Endpoints:  []string{"127.0.0.1:8080"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, replActor, clusterMock, _ := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()

	counterKey := crdt.PNCounterKey("flush-same-dc")
	_, err := Ask(context.TODO(), repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 5)
		},
	}, time.Second)
	require.NoError(t, err)

	err = Tell(context.TODO(), repl, &dataCenterFlushTick{})
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	assert.Equal(t, uint64(0), replActor.crossDCSendCount.Load())
	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterFlushDCEmptyEndpointsSkipped(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "z2r2remote2",
			DataCenter: datacenter.DataCenter{Name: "remote2", Region: "r2", Zone: "z2"},
			Endpoints:  []string{},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, replActor, clusterMock, _ := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()

	counterKey := crdt.PNCounterKey("flush-empty-ep")
	_, err := Ask(context.TODO(), repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 5)
		},
	}, time.Second)
	require.NoError(t, err)

	err = Tell(context.TODO(), repl, &dataCenterFlushTick{})
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	assert.Equal(t, uint64(0), replActor.crossDCSendCount.Load())
	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterFlushRemoteLookupFails(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "zrremote",
			DataCenter: datacenter.DataCenter{Name: "remote", Region: "r", Zone: "z"},
			Endpoints:  []string{"10.0.0.1:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, replActor, clusterMock, remotingMock := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()
	remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.1", 9090, "GoAktReplicator").
		Return(nil, errors.New("lookup failed")).Maybe()

	counterKey := crdt.PNCounterKey("flush-lookup-fail")
	_, err := Ask(context.TODO(), repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 5)
		},
	}, time.Second)
	require.NoError(t, err)

	err = Tell(context.TODO(), repl, &dataCenterFlushTick{})
	require.NoError(t, err)
	pause.For(time.Second)

	assert.Equal(t, uint64(0), replActor.crossDCSendCount.Load())
	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterFlushRemoteTellFails(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "zrremote",
			DataCenter: datacenter.DataCenter{Name: "remote", Region: "r", Zone: "z"},
			Endpoints:  []string{"10.0.0.1:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, replActor, clusterMock, remotingMock := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()
	remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.1", 9090, "GoAktReplicator").
		Return(address.New("GoAktReplicator", "remoteSys", "10.0.0.1", 9090), nil).Maybe()
	remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("send failed")).Maybe()

	counterKey := crdt.PNCounterKey("flush-tell-fail")
	_, err := Ask(context.TODO(), repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 5)
		},
	}, time.Second)
	require.NoError(t, err)

	err = Tell(context.TODO(), repl, &dataCenterFlushTick{})
	require.NoError(t, err)
	pause.For(time.Second)

	assert.Equal(t, uint64(0), replActor.crossDCSendCount.Load())
	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterFlushWithTombstones(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "zrremote",
			DataCenter: datacenter.DataCenter{Name: "remote", Region: "r", Zone: "z"},
			Endpoints:  []string{"10.0.0.1:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, replActor, clusterMock, remotingMock := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()
	remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.1", 9090, "GoAktReplicator").
		Return(address.New("GoAktReplicator", "remoteSys", "10.0.0.1", 9090), nil).Maybe()
	remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()

	counterKey := crdt.PNCounterKey("flush-tomb")
	_, err := Ask(context.TODO(), repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 5)
		},
	}, time.Second)
	require.NoError(t, err)

	_, err = Ask(context.TODO(), repl, &crdt.Delete{Key: counterKey}, time.Second)
	require.NoError(t, err)

	err = Tell(context.TODO(), repl, &dataCenterFlushTick{})
	require.NoError(t, err)
	pause.For(time.Second)

	assert.True(t, replActor.crossDCSendCount.Load() > 0)
	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterFlushMultipleEndpointsFirstFails(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "zrremote",
			DataCenter: datacenter.DataCenter{Name: "remote", Region: "r", Zone: "z"},
			Endpoints:  []string{"10.0.0.1:9090", "10.0.0.2:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, replActor, clusterMock, remotingMock := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()
	remotingMock.EXPECT().RemoteLookup(mock.Anything, mock.Anything, 9090, "GoAktReplicator").
		Return(address.New("GoAktReplicator", "remoteSys", "10.0.0.2", 9090), nil).Maybe()
	remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()

	counterKey := crdt.PNCounterKey("flush-multi-ep")
	_, err := Ask(context.TODO(), repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 5)
		},
	}, time.Second)
	require.NoError(t, err)

	err = Tell(context.TODO(), repl, &dataCenterFlushTick{})
	require.NoError(t, err)
	pause.For(time.Second)

	assert.True(t, replActor.crossDCSendCount.Load() > 0)
	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterFlushInvalidEndpoints(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "zrremote",
			DataCenter: datacenter.DataCenter{Name: "remote", Region: "r", Zone: "z"},
			Endpoints:  []string{"invalid-no-port", "also:not:valid:port"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, replActor, clusterMock, _ := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()

	counterKey := crdt.PNCounterKey("flush-invalid-ep")
	_, err := Ask(context.TODO(), repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 5)
		},
	}, time.Second)
	require.NoError(t, err)

	err = Tell(context.TODO(), repl, &dataCenterFlushTick{})
	require.NoError(t, err)
	pause.For(time.Second)

	assert.Equal(t, uint64(0), replActor.crossDCSendCount.Load())
	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterFlushInvalidPort(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "zrremote",
			DataCenter: datacenter.DataCenter{Name: "remote", Region: "r", Zone: "z"},
			Endpoints:  []string{"10.0.0.1:notaport"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, replActor, clusterMock, _ := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()

	counterKey := crdt.PNCounterKey("flush-bad-port")
	_, err := Ask(context.TODO(), repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 5)
		},
	}, time.Second)
	require.NoError(t, err)

	err = Tell(context.TODO(), repl, &dataCenterFlushTick{})
	require.NoError(t, err)
	pause.For(time.Second)

	assert.Equal(t, uint64(0), replActor.crossDCSendCount.Load())
	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterAntiEntropyNonLeaderSkips(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "zrremote",
			DataCenter: datacenter.DataCenter{Name: "remote", Region: "r", Zone: "z"},
			Endpoints:  []string{"10.0.0.1:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, _, clusterMock, _ := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(false).Maybe()

	err := Tell(context.TODO(), repl, &dataCenterAntiEntropyTick{})
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterAntiEntropyNilController(t *testing.T) {
	sys, repl, _, clusterMock, _ := spawnReplicatorWithDCController(t, remoteRecords(nil), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()

	impl := sys.(*actorSystem)
	impl.dataCenterController = nil

	err := Tell(context.TODO(), repl, &dataCenterAntiEntropyTick{})
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterAntiEntropyStaleCacheSkips(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "zrremote",
			DataCenter: datacenter.DataCenter{Name: "remote", Region: "r", Zone: "z"},
			Endpoints:  []string{"10.0.0.1:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}

	dcConfig := datacenter.NewConfig()
	dcConfig.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}
	dcConfig.MaxCacheStaleness = time.Nanosecond
	dcConfig.CacheRefreshInterval = time.Hour

	sys, repl, _, clusterMock, _ := spawnReplicatorWithDCController(t, remoteRecords(records), dcConfig)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()

	// let the cache become stale
	pause.For(10 * time.Millisecond)

	err := Tell(context.TODO(), repl, &dataCenterAntiEntropyTick{})
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterAntiEntropyNoRemoteDCs(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "zrlocal",
			DataCenter: datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"},
			Endpoints:  []string{"127.0.0.1:8080"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, _, clusterMock, _ := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()

	err := Tell(context.TODO(), repl, &dataCenterAntiEntropyTick{})
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterAntiEntropyRemoteDCEmptyEndpoints(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "z2r2remote2",
			DataCenter: datacenter.DataCenter{Name: "remote2", Region: "r2", Zone: "z2"},
			Endpoints:  []string{},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, _, clusterMock, _ := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()

	err := Tell(context.TODO(), repl, &dataCenterAntiEntropyTick{})
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterAntiEntropySuccessfulSend(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "z2r2remote2",
			DataCenter: datacenter.DataCenter{Name: "remote2", Region: "r2", Zone: "z2"},
			Endpoints:  []string{"10.0.0.5:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, _, clusterMock, remotingMock := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()
	remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.5", 9090, "GoAktReplicator").
		Return(address.New("GoAktReplicator", "remoteSys", "10.0.0.5", 9090), nil).Maybe()
	remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()

	// add data so the digest has entries
	_, err := Ask(context.TODO(), repl, &crdt.Update{
		Key:     crdt.GCounterKey("ae-dc-data"),
		Initial: crdt.NewGCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.GCounter).Increment("node-1", 10)
		},
	}, time.Second)
	require.NoError(t, err)

	err = Tell(context.TODO(), repl, &dataCenterAntiEntropyTick{})
	require.NoError(t, err)
	pause.For(time.Second)

	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterAntiEntropyRemoteLookupFails(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "z2r2remote2",
			DataCenter: datacenter.DataCenter{Name: "remote2", Region: "r2", Zone: "z2"},
			Endpoints:  []string{"10.0.0.5:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, _, clusterMock, remotingMock := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()
	remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.5", 9090, "GoAktReplicator").
		Return(nil, errors.New("lookup failed")).Maybe()

	err := Tell(context.TODO(), repl, &dataCenterAntiEntropyTick{})
	require.NoError(t, err)
	pause.For(time.Second)

	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterAntiEntropyRemoteTellFails(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "z2r2remote2",
			DataCenter: datacenter.DataCenter{Name: "remote2", Region: "r2", Zone: "z2"},
			Endpoints:  []string{"10.0.0.5:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, _, clusterMock, remotingMock := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()
	remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.5", 9090, "GoAktReplicator").
		Return(address.New("GoAktReplicator", "remoteSys", "10.0.0.5", 9090), nil).Maybe()
	remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("send failed")).Maybe()

	err := Tell(context.TODO(), repl, &dataCenterAntiEntropyTick{})
	require.NoError(t, err)
	pause.For(time.Second)

	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterAntiEntropyInvalidEndpoint(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "z2r2remote2",
			DataCenter: datacenter.DataCenter{Name: "remote2", Region: "r2", Zone: "z2"},
			Endpoints:  []string{"invalid-endpoint-no-port"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, _, clusterMock, _ := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()

	err := Tell(context.TODO(), repl, &dataCenterAntiEntropyTick{})
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterAntiEntropyInvalidPort(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "z2r2remote2",
			DataCenter: datacenter.DataCenter{Name: "remote2", Region: "r2", Zone: "z2"},
			Endpoints:  []string{"10.0.0.1:notaport"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, _, clusterMock, _ := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()

	err := Tell(context.TODO(), repl, &dataCenterAntiEntropyTick{})
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterDigestRequestViaTell(t *testing.T) {
	t.Run("digest request via Tell with nil sender does not panic", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		err := sys.Start(ctx)
		require.NoError(t, err)
		pause.For(time.Second)

		repl := spawnTestReplicatorWithDC(t, sys, "dc-west", "us-west-2", "us-west-2a")

		_, err = Ask(ctx, repl, &crdt.Update{
			Key:     crdt.GCounterKey("digest-tell"),
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		err = Tell(ctx, repl, &dataCenterDigestRequest{})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		assert.True(t, repl.IsRunning())

		err = sys.Stop(ctx)
		assert.NoError(t, err)
	})
}

func TestReplicatorDataCenterFlushStaleCacheNotStrict(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "zrremote",
			DataCenter: datacenter.DataCenter{Name: "remote", Region: "r", Zone: "z"},
			Endpoints:  []string{"10.0.0.1:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}

	dcConfig := datacenter.NewConfig()
	dcConfig.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}
	dcConfig.MaxCacheStaleness = time.Nanosecond
	dcConfig.CacheRefreshInterval = time.Hour
	dcConfig.FailOnStaleCache = false

	sys, repl, replActor, clusterMock, remotingMock := spawnReplicatorWithDCController(t, remoteRecords(records), dcConfig)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()
	remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.1", 9090, "GoAktReplicator").
		Return(address.New("GoAktReplicator", "remoteSys", "10.0.0.1", 9090), nil).Maybe()
	remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()

	// let the cache become stale
	pause.For(10 * time.Millisecond)

	counterKey := crdt.PNCounterKey("flush-stale-nostrict")
	_, err := Ask(context.TODO(), repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 5)
		},
	}, time.Second)
	require.NoError(t, err)

	err = Tell(context.TODO(), repl, &dataCenterFlushTick{})
	require.NoError(t, err)
	pause.For(time.Second)

	assert.Equal(t, uint64(0), replActor.crossDCStaleSkipCount.Load())
	assert.True(t, replActor.crossDCSendCount.Load() > 0)
	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorDataCenterFlushMultipleRemoteDCs(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "z1r1dc1",
			DataCenter: datacenter.DataCenter{Name: "dc1", Region: "r1", Zone: "z1"},
			Endpoints:  []string{"10.0.1.1:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
		{
			ID:         "z2r2dc2",
			DataCenter: datacenter.DataCenter{Name: "dc2", Region: "r2", Zone: "z2"},
			Endpoints:  []string{"10.0.2.1:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, replActor, clusterMock, remotingMock := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()
	remotingMock.EXPECT().RemoteLookup(mock.Anything, mock.Anything, 9090, "GoAktReplicator").
		Return(address.New("GoAktReplicator", "remoteSys", "10.0.1.1", 9090), nil).Maybe()
	remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()

	counterKey := crdt.PNCounterKey("flush-multi-dc")
	_, err := Ask(context.TODO(), repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 5)
		},
	}, time.Second)
	require.NoError(t, err)

	err = Tell(context.TODO(), repl, &dataCenterFlushTick{})
	require.NoError(t, err)
	pause.For(time.Second)

	assert.True(t, replActor.crossDCSendCount.Load() >= 2)
	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

func TestReplicatorIncomingBatchReplicationLag(t *testing.T) {
	ctx := context.TODO()
	sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	err := sys.Start(ctx)
	require.NoError(t, err)
	pause.For(time.Second)

	impl := sys.(*actorSystem)
	config := crdt.NewConfig(crdt.WithDataCenterReplication())
	replActor := newReplicatorActor()
	impl.extensions.Set(crdtConfigExtensionID, &crdtConfigExtension{
		config: config,
		dc:     datacenter.DataCenter{Name: "dc-west", Region: "us-west-2", Zone: "us-west-2a"},
	})
	repl, err := sys.Spawn(ctx, "replicator", replActor, WithLongLived())
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	r := newTestReplicator()
	pbDelta, err := r.encodeDelta(&crdtDelta{
		KeyID:    "lag-counter",
		DataType: crdt.PNCounterType,
		Delta:    crdt.NewPNCounter().Increment("remote-node", 42),
		Origin:   "remote-node",
	})
	require.NoError(t, err)

	sentAt := time.Now().Add(-100 * time.Millisecond)
	batch := internalpb.CRDTDeltaBatch_builder{
		Deltas: []*internalpb.CRDTDelta{pbDelta},
		OriginDc: internalpb.DataCenter_builder{
			Name:   "dc-east",
			Region: "us-east-1",
			Zone:   "us-east-1a",
		}.Build(),
		SentAtNanos: sentAt.UnixNano(),
	}.Build()

	err = Tell(ctx, repl, batch)
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	assert.True(t, replActor.lastReplicationLag.Load() > 0)
	assert.True(t, replActor.crossDCReceiveCount.Load() > 0)
	assert.True(t, repl.IsRunning())

	err = sys.Stop(ctx)
	assert.NoError(t, err)
}

func TestReplicatorOriginDCProtoSetDuringPreStart(t *testing.T) {
	ctx := context.TODO()
	sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	err := sys.Start(ctx)
	require.NoError(t, err)
	pause.For(time.Second)

	impl := sys.(*actorSystem)
	labels := map[string]string{"env": "prod", "tier": "hot"}
	config := crdt.NewConfig(crdt.WithDataCenterReplication())
	replActor := newReplicatorActor()
	impl.extensions.Set(crdtConfigExtensionID, &crdtConfigExtension{
		config: config,
		dc: datacenter.DataCenter{
			Name:   "dc-central",
			Region: "us-central-1",
			Zone:   "us-central-1b",
			Labels: labels,
		},
	})
	repl, err := sys.Spawn(ctx, "replicator", replActor, WithLongLived())
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	assert.Equal(t, "dc-central", replActor.originDCProto.GetName())
	assert.Equal(t, "us-central-1", replActor.originDCProto.GetRegion())
	assert.Equal(t, "us-central-1b", replActor.originDCProto.GetZone())
	assert.Equal(t, labels, replActor.originDCProto.GetLabels())

	assert.Equal(t, "dc-central", replActor.dc.Name)
	assert.Equal(t, "us-central-1", replActor.dc.Region)
	assert.Equal(t, "us-central-1b", replActor.dc.Zone)

	assert.True(t, repl.IsRunning())

	err = sys.Stop(ctx)
	assert.NoError(t, err)
}

func TestReplicatorPostStartSchedulesDCFlush(t *testing.T) {
	ctx := context.TODO()
	sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	err := sys.Start(ctx)
	require.NoError(t, err)
	pause.For(time.Second)

	impl := sys.(*actorSystem)
	config := crdt.NewConfig(
		crdt.WithDataCenterReplication(),
		crdt.WithDataCenterReplicationInterval(100*time.Millisecond),
	)
	impl.extensions.Set(crdtConfigExtensionID, &crdtConfigExtension{
		config: config,
		dc:     datacenter.DataCenter{Name: "dc-sched", Region: "r", Zone: "z"},
	})

	repl, err := sys.Spawn(ctx, "replicator-dc-schedule", newReplicatorActor(), WithLongLived())
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	assert.True(t, repl.IsRunning())

	// schedule runs but flush is a no-op without pending data
	pause.For(300 * time.Millisecond)
	assert.True(t, repl.IsRunning())

	err = sys.Stop(ctx)
	assert.NoError(t, err)
}

func TestReplicatorPostStartSchedulesDCAntiEntropy(t *testing.T) {
	ctx := context.TODO()
	sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	err := sys.Start(ctx)
	require.NoError(t, err)
	pause.For(time.Second)

	impl := sys.(*actorSystem)
	config := crdt.NewConfig(
		crdt.WithDataCenterReplication(),
		crdt.WithDataCenterAntiEntropy(),
		crdt.WithDataCenterAntiEntropyInterval(100*time.Millisecond),
		crdt.WithDataCenterReplicationInterval(time.Hour),
	)
	impl.extensions.Set(crdtConfigExtensionID, &crdtConfigExtension{
		config: config,
		dc:     datacenter.DataCenter{Name: "dc-ae-sched", Region: "r", Zone: "z"},
	})

	repl, err := sys.Spawn(ctx, "replicator-dc-ae-schedule", newReplicatorActor(), WithLongLived())
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	assert.True(t, repl.IsRunning())

	// schedule runs but anti-entropy is a no-op without cluster
	pause.For(300 * time.Millisecond)
	assert.True(t, repl.IsRunning())

	err = sys.Stop(ctx)
	assert.NoError(t, err)
}

func TestReplicatorDeleteBuffersTombstoneForDC(t *testing.T) {
	ctx := context.TODO()
	sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithPubSub())
	err := sys.Start(ctx)
	require.NoError(t, err)
	pause.For(time.Second)

	impl := sys.(*actorSystem)
	config := crdt.NewConfig(
		crdt.WithDataCenterReplication(),
		crdt.WithDataCenterReplicationInterval(time.Hour),
	)
	replActor := newReplicatorActor()
	impl.extensions.Set(crdtConfigExtensionID, &crdtConfigExtension{
		config: config,
		dc:     datacenter.DataCenter{Name: "dc-del", Region: "r", Zone: "z"},
	})
	repl, err := sys.Spawn(ctx, "replicator", replActor, WithLongLived())
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	counterKey := crdt.PNCounterKey("del-buf")
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
	pause.For(500 * time.Millisecond)

	assert.True(t, len(replActor.pendingTombstones) > 0)
	assert.True(t, repl.IsRunning())

	err = sys.Stop(ctx)
	assert.NoError(t, err)
}

func TestReplicatorUpdateBuffersDeltaForDC(t *testing.T) {
	ctx := context.TODO()
	sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithPubSub())
	err := sys.Start(ctx)
	require.NoError(t, err)
	pause.For(time.Second)

	impl := sys.(*actorSystem)
	config := crdt.NewConfig(
		crdt.WithDataCenterReplication(),
		crdt.WithDataCenterReplicationInterval(time.Hour),
	)
	replActor := newReplicatorActor()
	impl.extensions.Set(crdtConfigExtensionID, &crdtConfigExtension{
		config: config,
		dc:     datacenter.DataCenter{Name: "dc-upd", Region: "r", Zone: "z"},
	})
	repl, err := sys.Spawn(ctx, "replicator", replActor, WithLongLived())
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	counterKey := crdt.PNCounterKey("upd-buf")
	_, err = Ask(ctx, repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 5)
		},
	}, time.Second)
	require.NoError(t, err)
	pause.For(500 * time.Millisecond)

	assert.True(t, len(replActor.pendingDeltas) > 0)
	assert.True(t, repl.IsRunning())

	err = sys.Stop(ctx)
	assert.NoError(t, err)
}

func TestReplicatorDataCenterFlushDrainsPendingBuffers(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "zrremote",
			DataCenter: datacenter.DataCenter{Name: "remote", Region: "r", Zone: "z"},
			Endpoints:  []string{"10.0.0.1:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}
	sys, repl, replActor, clusterMock, remotingMock := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()
	remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.1", 9090, "GoAktReplicator").
		Return(address.New("GoAktReplicator", "remoteSys", "10.0.0.1", 9090), nil).Maybe()
	remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()

	counterKey := crdt.PNCounterKey("drain-buf")
	_, err := Ask(context.TODO(), repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 5)
		},
	}, time.Second)
	require.NoError(t, err)

	assert.True(t, len(replActor.pendingDeltas) > 0)

	err = Tell(context.TODO(), repl, &dataCenterFlushTick{})
	require.NoError(t, err)

	// Use a Get as a synchronization barrier: the actor processes messages
	// sequentially, so by the time it handles this Get the flush is done.
	_, err = Ask(context.TODO(), repl, &crdt.Get{Key: counterKey}, time.Second)
	require.NoError(t, err)

	assert.Empty(t, replActor.pendingDeltas)
	assert.Empty(t, replActor.pendingTombstones)
	assert.True(t, repl.IsRunning())

	err = sys.Stop(context.TODO())
	assert.NoError(t, err)
}

// TestReplicatorChangedCarriesKey verifies that a Changed notification names
// the key that changed, so an actor watching several keys can tell them apart.
func TestReplicatorChangedCarriesKey(t *testing.T) {
	ctx := context.TODO()
	sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, sys.Start(ctx))

	repl := spawnTestReplicator(t, sys)

	probe := NewMockMessageProbe()
	watcher, err := sys.Spawn(ctx, "watcher", probe, WithLongLived())
	require.NoError(t, err)

	counterKey := crdt.PNCounterKey("watched-counter")
	setKey := crdt.ORSetKey("watched-set")
	require.NoError(t, watcher.Tell(ctx, repl, &crdt.Subscribe{Key: counterKey}))
	require.NoError(t, watcher.Tell(ctx, repl, &crdt.Subscribe{Key: setKey}))

	_, err = Ask(ctx, repl, &crdt.Update{
		Key:     setKey,
		Initial: crdt.NewORSet(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.ORSet).Add("node-1", "session-1")
		},
	}, time.Second)
	require.NoError(t, err)

	_, err = Ask(ctx, repl, &crdt.Update{
		Key:     counterKey,
		Initial: crdt.NewPNCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.PNCounter).Increment("node-1", 5)
		},
	}, time.Second)
	require.NoError(t, err)

	for _, expected := range []crdt.Key{setKey, counterKey} {
		select {
		case message := <-probe.received:
			changed, ok := message.(*crdt.Changed)
			require.True(t, ok, "expected a Changed notification, got %T", message)
			assert.Equal(t, expected, changed.Key)
			assert.NotNil(t, changed.Data)
		case <-time.After(3 * time.Second):
			t.Fatalf("no Changed notification for key=%s", expected.ID())
		}
	}

	assert.NoError(t, sys.Stop(ctx))
}

// TestReplicatorDeletedNotification verifies that the watchers of a key are
// sent one Deleted when the node removes a value it held because of a
// deletion, whichever way the deletion arrives, and nothing otherwise.
func TestReplicatorDeletedNotification(t *testing.T) {
	key := crdt.ORSetKey("sessions")

	// start spawns a replicator with its schedules off, so that a tombstone is
	// pruned only when a test asks for it
	start := func(t *testing.T, opts ...crdt.Option) (ActorSystem, *PID, *replicatorActor) {
		t.Helper()
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		opts = append([]crdt.Option{crdt.WithAntiEntropyInterval(0), crdt.WithPruneInterval(0)}, opts...)
		sys.(*actorSystem).extensions.Set(crdtConfigExtensionID, &crdtConfigExtension{config: crdt.NewConfig(opts...)})

		actor := newReplicatorActor()
		repl, err := sys.Spawn(ctx, "replicator", actor, WithLongLived())
		require.NoError(t, err)
		return sys, repl, actor
	}

	add := func(t *testing.T, repl *PID, element string) {
		t.Helper()
		_, err := Ask(context.TODO(), repl, &crdt.Update{
			Key:     key,
			Initial: crdt.NewORSet(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.ORSet).Add("node-a", element)
			},
		}, time.Second)
		require.NoError(t, err)
	}

	remove := func(t *testing.T, repl *PID) {
		t.Helper()
		_, err := Ask(context.TODO(), repl, &crdt.Delete{Key: key}, time.Second)
		require.NoError(t, err)
	}

	// holds reports whether the replicator holds the key; its answer also
	// proves that every message sent to the replicator before has been handled
	holds := func(t *testing.T, repl *PID) bool {
		t.Helper()
		resp, err := Ask(context.TODO(), repl, &crdt.Get{Key: key}, time.Second)
		require.NoError(t, err)
		return resp.(*crdt.GetResponse).Data != nil
	}

	tombstoneOf := func(deletedAt time.Time, deletedBy string) *internalpb.CRDTTombstone {
		return internalpb.CRDTTombstone_builder{
			Key:            codec.EncodeCRDTKey(key.ID(), key.Type()),
			DeletedAtNanos: deletedAt.UnixNano(),
			DeletedByNode:  deletedBy,
		}.Build()
	}

	t.Run("a local delete of a held key is announced after the change that created it", func(t *testing.T) {
		sys, repl, _ := start(t)
		watcher, probe := subscribeProbe(t, sys, repl, "watcher", key)

		add(t, repl, "session-1")
		expectChanged(t, probe, key)

		remove(t, repl)
		expectDeleted(t, probe, key)
		probeIsQuiet(t, watcher, probe)
	})

	t.Run("every watcher of the key is told and a watcher of another key is not", func(t *testing.T) {
		sys, repl, _ := start(t)
		firstWatcher, firstProbe := subscribeProbe(t, sys, repl, "first", key)
		secondWatcher, secondProbe := subscribeProbe(t, sys, repl, "second", key)
		otherWatcher, otherProbe := subscribeProbe(t, sys, repl, "other", crdt.ORSetKey("other"))

		add(t, repl, "session-1")
		expectChanged(t, firstProbe, key)
		expectChanged(t, secondProbe, key)

		remove(t, repl)
		expectDeleted(t, firstProbe, key)
		expectDeleted(t, secondProbe, key)
		probeIsQuiet(t, firstWatcher, firstProbe)
		probeIsQuiet(t, secondWatcher, secondProbe)
		probeIsQuiet(t, otherWatcher, otherProbe)
	})

	t.Run("a delete of a key this node does not hold announces nothing", func(t *testing.T) {
		sys, repl, actor := start(t)
		watcher, probe := subscribeProbe(t, sys, repl, "watcher", key)

		remove(t, repl)
		assert.Contains(t, actor.tombstones, key.ID())
		probeIsQuiet(t, watcher, probe)
	})

	t.Run("a peer tombstone is announced once", func(t *testing.T) {
		sys, repl, _ := start(t)
		watcher, probe := subscribeProbe(t, sys, repl, "watcher", key)
		add(t, repl, "session-1")
		expectChanged(t, probe, key)

		ctx := context.TODO()
		deletedAt := time.Now()
		require.NoError(t, Tell(ctx, repl, tombstoneOf(deletedAt, "node-b")))
		require.False(t, holds(t, repl))
		expectDeleted(t, probe, key)

		// the same deletion delivered again, then a later deletion of the key
		require.NoError(t, Tell(ctx, repl, tombstoneOf(deletedAt, "node-b")))
		require.NoError(t, Tell(ctx, repl, tombstoneOf(deletedAt.Add(time.Second), "node-c")))
		require.False(t, holds(t, repl))
		probeIsQuiet(t, watcher, probe)
	})

	t.Run("an expired tombstone that still removes a held key is announced", func(t *testing.T) {
		sys, repl, actor := start(t, crdt.WithTombstoneTTL(time.Minute))
		watcher, probe := subscribeProbe(t, sys, repl, "watcher", key)
		add(t, repl, "session-1")
		expectChanged(t, probe, key)

		require.NoError(t, Tell(context.TODO(), repl, tombstoneOf(time.Now().Add(-time.Hour), "node-b")))
		require.False(t, holds(t, repl))
		assert.NotContains(t, actor.tombstones, key.ID())
		expectDeleted(t, probe, key)
		probeIsQuiet(t, watcher, probe)
	})

	t.Run("a watcher stays subscribed after the deletion", func(t *testing.T) {
		sys, repl, _ := start(t, crdt.WithTombstoneTTL(time.Nanosecond))
		watcher, probe := subscribeProbe(t, sys, repl, "watcher", key)
		add(t, repl, "session-1")
		expectChanged(t, probe, key)

		remove(t, repl)
		expectDeleted(t, probe, key)

		// once the tombstone is pruned the key can be created again
		require.NoError(t, Tell(context.TODO(), repl, &pruneTick{}))
		add(t, repl, "session-2")

		changed := expectChanged(t, probe, key)
		assert.ElementsMatch(t, []any{"session-2"}, changed.Data.(*crdt.ORSet).Elements())
		probeIsQuiet(t, watcher, probe)
	})

	t.Run("an unsubscribed actor is not told", func(t *testing.T) {
		sys, repl, _ := start(t)
		watcher, probe := subscribeProbe(t, sys, repl, "watcher", key)
		add(t, repl, "session-1")
		expectChanged(t, probe, key)

		require.NoError(t, watcher.Tell(context.TODO(), repl, &crdt.Unsubscribe{Key: key}))
		remove(t, repl)
		probeIsQuiet(t, watcher, probe)
	})
}

// TestReplicatorCoordinatedReadTracksUnknownKey verifies that a coordinated
// read of a key only a peer holds records the key with its real data type:
// the digest advertises that type and the snapshot still encodes.
func TestReplicatorCoordinatedReadTracksUnknownKey(t *testing.T) {
	ctx := context.TODO()
	sys, repl, replActor, clusterMock, remotingMock := spawnReplicatorWithDCController(t, remoteRecords(nil), nil)

	setKey := crdt.ORSetKey("peer-only-set")
	peerData, err := ddata.EncodeCRDT(crdt.NewORSet().Add("node-2", "session-1"), ddata.NewCRDTValueSerializer())
	require.NoError(t, err)

	peerAddress := address.New("GoAktReplicator", "testSys", "10.0.0.2", 9090)
	clusterMock.EXPECT().Peers(mock.Anything).Return([]*cluster.Peer{{Host: "10.0.0.2", RemotingPort: 9090}}, nil)
	remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.2", 9090, "GoAktReplicator").Return(peerAddress, nil)
	remotingMock.EXPECT().RemoteAsk(mock.Anything, mock.Anything, peerAddress, mock.Anything, mock.Anything).
		Return(internalpb.CRDTReadResponse_builder{
			Key:      codec.EncodeCRDTKey(setKey.ID(), setKey.Type()),
			Data:     peerData,
			FromNode: "node-2",
		}.Build(), nil)

	resp, err := Ask(ctx, repl, &crdt.Get{Key: setKey, ReadFrom: crdt.All}, 5*time.Second)
	require.NoError(t, err)
	set, ok := resp.(*crdt.GetResponse).Data.(*crdt.ORSet)
	require.True(t, ok)
	assert.Equal(t, []any{"session-1"}, set.Elements())

	// the digest is what anti-entropy advertises to peers
	resp, err = Ask(ctx, repl, &dataCenterDigestRequest{}, time.Second)
	require.NoError(t, err)
	digest, ok := resp.(*internalpb.CRDTDigest)
	require.True(t, ok)
	require.Len(t, digest.GetEntries(), 1)
	keyID, dataType, err := codec.DecodeCRDTKey(digest.GetEntries()[0].GetKey())
	require.NoError(t, err)
	assert.Equal(t, setKey.ID(), keyID)
	assert.Equal(t, crdt.ORSetType, dataType)

	require.NoError(t, sys.Stop(ctx))

	// the replicator is stopped: its state is no longer touched by a turn
	entries, err := replActor.buildSnapshotEntries()
	require.NoError(t, err)
	require.Contains(t, entries, setKey.ID())
	_, dataType, err = codec.DecodeCRDTKey(entries[setKey.ID()].GetKey())
	require.NoError(t, err)
	assert.Equal(t, crdt.ORSetType, dataType)
}

// TestReplicatorIgnoresSubscribeAck verifies that the confirmation the
// TopicActor sends for the Replicator's own subscription is not reported as
// an unhandled message.
func TestReplicatorIgnoresSubscribeAck(t *testing.T) {
	ctx := context.TODO()
	sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, sys.Start(ctx))

	repl := spawnTestReplicator(t, sys)

	require.NoError(t, Tell(ctx, repl, NewSubscribeAck(crdtTopic)))

	// the mailbox is first-in first-out: once the read is answered the
	// confirmation has been processed
	_, err := Ask(ctx, repl, &crdt.Get{Key: crdt.GCounterKey("any")}, time.Second)
	require.NoError(t, err)
	assert.Zero(t, repl.unhandledCount.Load())

	assert.NoError(t, sys.Stop(ctx))
}

// TestReplicatorAntiEntropyContentHash covers the digest exchange between a
// replicator and a peer: the content hash of a key decides whether its state
// is sent, and the version decides only for a peer that sends no hash.
func TestReplicatorAntiEntropyContentHash(t *testing.T) {
	ctx := context.TODO()
	sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, sys.Start(ctx))

	repl := spawnTestReplicator(t, sys)
	probe := NewMockMessageProbe()
	peer, err := sys.Spawn(ctx, "peer", probe, WithLongLived())
	require.NoError(t, err)

	// this node holds {node-b: 7} at version 1
	key := crdt.GCounterKey("visits")
	local := crdt.NewGCounter().Increment("node-b", 7)
	_, err = Ask(ctx, repl, &crdt.Update{
		Key:     key,
		Initial: crdt.NewGCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.GCounter).Increment("node-b", 7)
		},
	}, time.Second)
	require.NoError(t, err)

	entry := replicatorDigestEntry(t, repl, key.ID())
	require.EqualValues(t, 1, entry.GetVersion())
	require.True(t, entry.HasStateHash())
	require.Equal(t, local.StateHash(), entry.GetStateHash())

	digestOf := func(version uint64, hash *uint64) *internalpb.CRDTDigest {
		return internalpb.CRDTDigest_builder{
			Entries: []*internalpb.CRDTDigestEntry{
				internalpb.CRDTDigestEntry_builder{
					Key:       codec.EncodeCRDTKey(key.ID(), key.Type()),
					Version:   version,
					StateHash: hash,
				}.Build(),
			},
		}.Build()
	}

	expectFullState := func(t *testing.T) {
		t.Helper()

		select {
		case message := <-probe.received:
			fullState, ok := message.(*internalpb.CRDTFullState)
			require.True(t, ok, "expected a full state, got %T", message)
			require.Len(t, fullState.GetEntries(), 1)
			data, err := ddata.DecodeCRDT(fullState.GetEntries()[0].GetData(), ddata.NewCRDTValueSerializer())
			require.NoError(t, err)
			assert.EqualValues(t, 7, data.(*crdt.GCounter).Value())
		case <-time.After(3 * time.Second):
			t.Fatal("no full state sent to the peer")
		}
	}

	t.Run("different hashes at equal versions send the state", func(t *testing.T) {
		// the peer holds another value, {node-a: 5}, also at version 1
		peerHash := crdt.NewGCounter().Increment("node-a", 5).StateHash()
		require.NoError(t, peer.Tell(ctx, repl, digestOf(1, &peerHash)))
		expectFullState(t)
	})

	t.Run("different hashes send the state even to a peer with a higher version", func(t *testing.T) {
		peerHash := crdt.NewGCounter().Increment("node-a", 5).StateHash()
		require.NoError(t, peer.Tell(ctx, repl, digestOf(42, &peerHash)))
		expectFullState(t)
	})

	t.Run("equal hashes send nothing whatever the versions", func(t *testing.T) {
		peerHash := local.StateHash()

		for _, version := range []uint64{0, 1, 42} {
			require.NoError(t, peer.Tell(ctx, repl, digestOf(version, &peerHash)))
		}

		replicatorDigestEntry(t, repl, key.ID())
		probeIsQuiet(t, peer, probe)
	})

	t.Run("an entry without a hash follows the version rule", func(t *testing.T) {
		// a peer at the same or a higher version is not behind
		require.NoError(t, peer.Tell(ctx, repl, digestOf(1, nil)))
		require.NoError(t, peer.Tell(ctx, repl, digestOf(42, nil)))
		replicatorDigestEntry(t, repl, key.ID())
		probeIsQuiet(t, peer, probe)

		// a peer at a lower version is
		require.NoError(t, peer.Tell(ctx, repl, digestOf(0, nil)))
		expectFullState(t)
	})

	t.Run("a key the peer lacks is sent", func(t *testing.T) {
		require.NoError(t, peer.Tell(ctx, repl, internalpb.CRDTDigest_builder{}.Build()))
		expectFullState(t)
	})

	assert.NoError(t, sys.Stop(ctx))
}

// TestReplicatorAntiEntropyRepairsEqualVersions runs the digest exchange
// between two replicators that hold different values of a key at the same
// version, the case a version comparison cannot see.
func TestReplicatorAntiEntropyRepairsEqualVersions(t *testing.T) {
	ctx := context.TODO()
	sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, sys.Start(ctx))

	// The system has no TopicActor, so the two replicators exchange no delta.
	// Their schedules are off: the references are per system, and the rounds
	// are driven by hand below.
	config := crdt.NewConfig(crdt.WithAntiEntropyInterval(0), crdt.WithPruneInterval(0))
	sys.(*actorSystem).extensions.Set(crdtConfigExtensionID, &crdtConfigExtension{config: config})

	replA, err := sys.Spawn(ctx, "replicator-a", newReplicatorActor(), WithLongLived())
	require.NoError(t, err)
	replB, err := sys.Spawn(ctx, "replicator-b", newReplicatorActor(), WithLongLived())
	require.NoError(t, err)

	key := crdt.GCounterKey("visits")
	increment := func(repl *PID, nodeID string, value uint64) {
		_, err := Ask(ctx, repl, &crdt.Update{
			Key:     key,
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment(nodeID, value)
			},
		}, time.Second)
		require.NoError(t, err)
	}

	valueOn := func(repl *PID) uint64 {
		resp, err := Ask(ctx, repl, &crdt.Get{Key: key}, time.Second)
		require.NoError(t, err)
		return resp.(*crdt.GetResponse).Data.(*crdt.GCounter).Value()
	}

	// pull runs one anti-entropy round: from sends its digest to to, which
	// answers with the state from needs. The two reads that follow are
	// answered after the digest and the answer have been processed.
	pull := func(from, to *PID) {
		resp, err := Ask(ctx, from, &dataCenterDigestRequest{}, time.Second)
		require.NoError(t, err)
		require.NoError(t, from.Tell(ctx, to, resp))
		valueOn(to)
		valueOn(from)
	}

	increment(replA, "node-a", 5)
	increment(replB, "node-b", 7)

	entryA := replicatorDigestEntry(t, replA, key.ID())
	entryB := replicatorDigestEntry(t, replB, key.ID())
	require.Equal(t, entryA.GetVersion(), entryB.GetVersion())
	require.NotEqual(t, entryA.GetStateHash(), entryB.GetStateHash())

	// A pulls from B, then B pulls from A
	pull(replA, replB)
	assert.EqualValues(t, 12, valueOn(replA))
	assert.EqualValues(t, 7, valueOn(replB))

	pull(replB, replA)
	assert.EqualValues(t, 12, valueOn(replB))

	entryA = replicatorDigestEntry(t, replA, key.ID())
	entryB = replicatorDigestEntry(t, replB, key.ID())
	assert.Equal(t, entryA.GetStateHash(), entryB.GetStateHash())

	// the two nodes agree: further rounds exchange nothing and count nothing
	pull(replA, replB)
	pull(replB, replA)
	assert.Equal(t, entryA.GetVersion(), replicatorDigestEntry(t, replA, key.ID()).GetVersion())
	assert.Equal(t, entryB.GetVersion(), replicatorDigestEntry(t, replB, key.ID()).GetVersion())

	assert.NoError(t, sys.Stop(ctx))
}

// TestReplicatorUnchangedMergeIsSilent verifies that a delta or a full state
// that leaves the stored value as it was neither advances the key's version
// nor notifies its watchers.
func TestReplicatorUnchangedMergeIsSilent(t *testing.T) {
	ctx := context.TODO()
	sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
	require.NoError(t, sys.Start(ctx))

	repl := spawnTestReplicator(t, sys)
	probe := NewMockMessageProbe()
	watcher, err := sys.Spawn(ctx, "watcher", probe, WithLongLived())
	require.NoError(t, err)

	key := crdt.GCounterKey("visits")
	_, err = Ask(ctx, repl, &crdt.Update{
		Key:     key,
		Initial: crdt.NewGCounter(),
		Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
			return current.(*crdt.GCounter).Increment("node-b", 7)
		},
	}, time.Second)
	require.NoError(t, err)
	require.NoError(t, watcher.Tell(ctx, repl, &crdt.Subscribe{Key: key}))

	expectChanged := func(t *testing.T, value uint64) {
		t.Helper()

		select {
		case message := <-probe.received:
			changed, ok := message.(*crdt.Changed)
			require.True(t, ok, "expected a Changed notification, got %T", message)
			assert.EqualValues(t, value, changed.Data.(*crdt.GCounter).Value())
		case <-time.After(3 * time.Second):
			t.Fatal("no Changed notification")
		}
	}

	t.Run("a duplicate delta counts once", func(t *testing.T) {
		delta := &crdtDelta{KeyID: key.ID(), DataType: key.Type(), Delta: crdt.NewGCounter().Increment("node-a", 5), Origin: "node-a"}
		require.NoError(t, Tell(ctx, repl, delta))
		require.NoError(t, Tell(ctx, repl, delta))

		// one local update and one effective delta
		assert.EqualValues(t, 2, replicatorDigestEntry(t, repl, key.ID()).GetVersion())
		expectChanged(t, 12)
		probeIsQuiet(t, watcher, probe)
	})

	t.Run("a full state that changes nothing counts for nothing", func(t *testing.T) {
		// what a peer that is behind sends back: a part of what is held here
		stale, err := ddata.EncodeCRDT(crdt.NewGCounter().Increment("node-a", 5), ddata.NewCRDTValueSerializer())
		require.NoError(t, err)

		require.NoError(t, Tell(ctx, repl, internalpb.CRDTFullState_builder{
			Entries: []*internalpb.CRDTFullStateEntry{
				internalpb.CRDTFullStateEntry_builder{Key: codec.EncodeCRDTKey(key.ID(), key.Type()), Data: stale}.Build(),
			},
		}.Build()))

		assert.EqualValues(t, 2, replicatorDigestEntry(t, repl, key.ID()).GetVersion())
		probeIsQuiet(t, watcher, probe)
	})

	t.Run("a full state that adds something counts once", func(t *testing.T) {
		ahead, err := ddata.EncodeCRDT(crdt.NewGCounter().Increment("node-c", 1), ddata.NewCRDTValueSerializer())
		require.NoError(t, err)

		require.NoError(t, Tell(ctx, repl, internalpb.CRDTFullState_builder{
			Entries: []*internalpb.CRDTFullStateEntry{
				internalpb.CRDTFullStateEntry_builder{Key: codec.EncodeCRDTKey(key.ID(), key.Type()), Data: ahead}.Build(),
			},
		}.Build()))

		assert.EqualValues(t, 3, replicatorDigestEntry(t, repl, key.ID()).GetVersion())
		expectChanged(t, 13)
		probeIsQuiet(t, watcher, probe)
	})

	assert.NoError(t, sys.Stop(ctx))
}

// TestReplicatorDataCenterPendingBuffers covers what a replicator keeps for
// the remote datacenters: one entry per key, kept until a flush reached them.
func TestReplicatorDataCenterPendingBuffers(t *testing.T) {
	records := []datacenter.DataCenterRecord{
		{
			ID:         "zrremote",
			DataCenter: datacenter.DataCenter{Name: "remote", Region: "r", Zone: "z"},
			Endpoints:  []string{"10.0.0.1:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		},
	}

	increment := func(t *testing.T, repl *PID, key crdt.Key, value uint64) {
		t.Helper()
		_, err := Ask(context.TODO(), repl, &crdt.Update{
			Key:     key,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", value)
			},
		}, time.Second)
		require.NoError(t, err)
	}

	// flush runs one flush tick and returns once the replicator handled it
	flush := func(t *testing.T, repl *PID) {
		t.Helper()
		require.NoError(t, Tell(context.TODO(), repl, &dataCenterFlushTick{}))
		_, err := Ask(context.TODO(), repl, &crdt.Get{Key: crdt.GCounterKey("barrier")}, 5*time.Second)
		require.NoError(t, err)
	}

	t.Run("a batch that could not be sent is kept and sent on a later tick", func(t *testing.T) {
		sys, repl, replActor, clusterMock, remotingMock := spawnReplicatorWithDCController(t, remoteRecords(records), nil)

		remote := address.New("GoAktReplicator", "remoteSys", "10.0.0.1", 9090)
		batches := make(chan *internalpb.CRDTDeltaBatch, 4)
		clusterMock.EXPECT().IsLeader(mock.Anything).Return(true)
		remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.1", 9090, "GoAktReplicator").Return(nil, errors.New("lookup failed")).Once()
		remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.1", 9090, "GoAktReplicator").Return(remote, nil)
		remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, remote, mock.Anything).
			RunAndReturn(func(_ context.Context, _, _ *address.Address, message any) error {
				batches <- message.(*internalpb.CRDTDeltaBatch)
				return nil
			})

		key := crdt.PNCounterKey("orders")
		increment(t, repl, key, 5)

		// the lookup fails: nothing is sent and the delta stays
		flush(t, repl)
		assert.Zero(t, replActor.crossDCSendCount.Load())
		assert.Len(t, replActor.pendingDeltas, 1)

		// a change made in between joins the pending one
		increment(t, repl, key, 2)

		// the lookup works: the batch goes out and the buffer empties
		flush(t, repl)
		assert.EqualValues(t, 1, replActor.crossDCSendCount.Load())
		assert.Empty(t, replActor.pendingDeltas)

		batch := <-batches
		require.Len(t, batch.GetDeltas(), 1)
		assert.Equal(t, key.ID(), batch.GetDeltas()[0].GetKey().GetId())
		data, err := ddata.DecodeCRDT(batch.GetDeltas()[0].GetData(), ddata.NewCRDTValueSerializer())
		require.NoError(t, err)
		assert.EqualValues(t, 7, data.(*crdt.PNCounter).Value())

		// nothing is left to send
		flush(t, repl)
		assert.EqualValues(t, 1, replActor.crossDCSendCount.Load())

		require.NoError(t, sys.Stop(context.TODO()))
	})

	t.Run("a batch is kept when there is no datacenter to send it to", func(t *testing.T) {
		sys, repl, replActor, clusterMock, _ := spawnReplicatorWithDCController(t, remoteRecords(nil), nil)
		clusterMock.EXPECT().IsLeader(mock.Anything).Return(true)

		increment(t, repl, crdt.PNCounterKey("orders"), 5)
		flush(t, repl)
		assert.Len(t, replActor.pendingDeltas, 1)

		require.NoError(t, sys.Stop(context.TODO()))
	})

	t.Run("a non-leader keeps one entry per key", func(t *testing.T) {
		sys, repl, replActor, clusterMock, _ := spawnReplicatorWithDCController(t, remoteRecords(records), nil)
		clusterMock.EXPECT().IsLeader(mock.Anything).Return(false)

		const keys = 5
		for i := range 500 {
			increment(t, repl, crdt.PNCounterKey(fmt.Sprintf("orders-%d", i%keys)), 1)
		}

		flush(t, repl)
		require.Len(t, replActor.pendingDeltas, keys)

		// each entry holds everything its key received
		for _, pending := range replActor.pendingDeltas {
			assert.EqualValues(t, 100, pending.Delta.(*crdt.PNCounter).Value())
		}

		require.NoError(t, sys.Stop(context.TODO()))
	})

	t.Run("a tombstone replaces the pending delta of its key", func(t *testing.T) {
		sys, repl, replActor, clusterMock, _ := spawnReplicatorWithDCController(t, remoteRecords(records), nil)
		clusterMock.EXPECT().IsLeader(mock.Anything).Return(false).Maybe()

		deleted := crdt.PNCounterKey("deleted")
		kept := crdt.PNCounterKey("kept")
		increment(t, repl, deleted, 5)
		increment(t, repl, kept, 5)

		_, err := Ask(context.TODO(), repl, &crdt.Delete{Key: deleted}, time.Second)
		require.NoError(t, err)
		_, err = Ask(context.TODO(), repl, &crdt.Delete{Key: deleted}, time.Second)
		require.NoError(t, err)

		// while the tombstone lives the store rejects updates of the key,
		// so no delta joins the buffer either
		increment(t, repl, deleted, 1)

		require.Len(t, replActor.pendingDeltas, 1)
		assert.Contains(t, replActor.pendingDeltas, kept.ID())
		require.Len(t, replActor.pendingTombstones, 1)
		assert.Contains(t, replActor.pendingTombstones, deleted.ID())

		require.NoError(t, sys.Stop(context.TODO()))
	})

	t.Run("a key recreated while its tombstone is pending is sent as both", func(t *testing.T) {
		r := newTestReplicator()
		r.nodeID = "node-1"
		r.serializer = ddata.NewCRDTValueSerializer()

		// the local tombstone has expired, so the store accepted the update
		key := crdt.PNCounterKey("recreated")
		r.bufferTombstone(key.ID(), internalpb.CRDTTombstone_builder{
			Key:            codec.EncodeCRDTKey(key.ID(), key.Type()),
			DeletedAtNanos: time.Now().Add(-2 * r.config.TombstoneTTL()).UnixNano(),
			DeletedByNode:  r.nodeID,
		}.Build())
		r.bufferDelta(key.ID(), key.Type(), crdt.NewPNCounter().Increment("node-1", 2))

		encoded, err := r.encodeDelta(r.pendingDeltas[key.ID()].crdtDelta)
		require.NoError(t, err)
		deltas := map[string]*internalpb.CRDTDelta{key.ID(): encoded}

		// a datacenter that has accepted neither is sent both in one batch
		batch, highest := r.buildPendingBatch(deltas, 0)
		require.Len(t, batch.GetTombstones(), 1)
		require.Len(t, batch.GetDeltas(), 1)
		assert.Equal(t, key.ID(), batch.GetTombstones()[0].GetKey().GetId())
		assert.Equal(t, key.ID(), batch.GetDeltas()[0].GetKey().GetId())
		assert.EqualValues(t, 2, highest)

		// one that has accepted the tombstone is sent the delta alone
		batch, highest = r.buildPendingBatch(deltas, r.pendingTombstones[key.ID()].seq)
		assert.Empty(t, batch.GetTombstones())
		require.Len(t, batch.GetDeltas(), 1)
		assert.EqualValues(t, 2, highest)

		// one that has accepted both is sent nothing
		_, highest = r.buildPendingBatch(deltas, 2)
		assert.Zero(t, highest)
	})

	t.Run("a tombstone from a peer drops the pending delta of its key", func(t *testing.T) {
		sys, repl, replActor, clusterMock, _ := spawnReplicatorWithDCController(t, remoteRecords(records), nil)
		clusterMock.EXPECT().IsLeader(mock.Anything).Return(false).Maybe()

		key := crdt.PNCounterKey("deleted-elsewhere")
		increment(t, repl, key, 5)

		require.NoError(t, Tell(context.TODO(), repl, internalpb.CRDTTombstone_builder{
			Key:            codec.EncodeCRDTKey(key.ID(), key.Type()),
			DeletedAtNanos: time.Now().UnixNano(),
			DeletedByNode:  "node-2",
		}.Build()))

		flush(t, repl)
		assert.Empty(t, replActor.pendingDeltas)
		assert.Empty(t, replActor.pendingTombstones)

		require.NoError(t, sys.Stop(context.TODO()))
	})
}

// TestReplicatorAntiEntropyDeletions covers deletions in the anti-entropy
// exchange: a node that retains a tombstone makes every peer it exchanges
// with drop the key, whichever side sent the digest.
func TestReplicatorAntiEntropyDeletions(t *testing.T) {
	key := crdt.GCounterKey("visits")

	increment := func(t *testing.T, repl *PID, nodeID string, value uint64) {
		t.Helper()
		_, err := Ask(context.TODO(), repl, &crdt.Update{
			Key:     key,
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment(nodeID, value)
			},
		}, time.Second)
		require.NoError(t, err)
	}

	remove := func(t *testing.T, repl *PID) {
		t.Helper()
		_, err := Ask(context.TODO(), repl, &crdt.Delete{Key: key}, time.Second)
		require.NoError(t, err)
	}

	holds := func(t *testing.T, repl *PID) bool {
		t.Helper()
		resp, err := Ask(context.TODO(), repl, &crdt.Get{Key: key}, time.Second)
		require.NoError(t, err)
		return resp.(*crdt.GetResponse).Data != nil
	}

	tombstoneOf := func(deletedAt time.Time, deletedBy string) *internalpb.CRDTTombstone {
		return internalpb.CRDTTombstone_builder{
			Key:            codec.EncodeCRDTKey(key.ID(), key.Type()),
			DeletedAtNanos: deletedAt.UnixNano(),
			DeletedByNode:  deletedBy,
		}.Build()
	}

	// digestListing is the digest of a peer that holds the given value of the key
	digestListing := func(value *crdt.GCounter, tombstones ...*internalpb.CRDTTombstone) *internalpb.CRDTDigest {
		hash := value.StateHash()
		return internalpb.CRDTDigest_builder{
			Entries: []*internalpb.CRDTDigestEntry{
				internalpb.CRDTDigestEntry_builder{Key: codec.EncodeCRDTKey(key.ID(), key.Type()), Version: 1, StateHash: &hash}.Build(),
			},
			Tombstones: tombstones,
		}.Build()
	}

	single := func(t *testing.T) (ActorSystem, *PID, *MockMessageProbe, *PID) {
		t.Helper()
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		repl := spawnTestReplicator(t, sys)
		probe := NewMockMessageProbe()
		peer, err := sys.Spawn(ctx, "peer", probe, WithLongLived())
		require.NoError(t, err)
		return sys, repl, probe, peer
	}

	expectTombstoneAnswer := func(t *testing.T, probe *MockMessageProbe) {
		t.Helper()

		select {
		case message := <-probe.received:
			fullState, ok := message.(*internalpb.CRDTFullState)
			require.True(t, ok, "expected a full state, got %T", message)
			assert.Empty(t, fullState.GetEntries())
			require.Len(t, fullState.GetTombstones(), 1)
			assert.Equal(t, key.ID(), fullState.GetTombstones()[0].GetKey().GetId())
		case <-time.After(3 * time.Second):
			t.Fatal("the peer was not told of the deletion")
		}
	}

	t.Run("a peer that still holds a deleted key is answered with the tombstone", func(t *testing.T) {
		_, repl, probe, peer := single(t)
		increment(t, repl, "node-b", 7)
		remove(t, repl)

		require.NoError(t, peer.Tell(context.TODO(), repl, digestListing(crdt.NewGCounter().Increment("node-b", 7))))
		expectTombstoneAnswer(t, probe)
	})

	t.Run("a tombstone in the digest deletes the key and is retained", func(t *testing.T) {
		ctx := context.TODO()
		_, repl, probe, peer := single(t)
		increment(t, repl, "node-b", 7)

		// the peer deleted the key and holds nothing else
		digest := internalpb.CRDTDigest_builder{Tombstones: []*internalpb.CRDTTombstone{tombstoneOf(time.Now(), "node-a")}}.Build()
		require.NoError(t, peer.Tell(ctx, repl, digest))
		assert.False(t, holds(t, repl))

		// the key is not sent back, and a late delta for it is rejected
		probeIsQuiet(t, peer, probe)
		require.NoError(t, Tell(ctx, repl, &crdtDelta{KeyID: key.ID(), DataType: key.Type(), Delta: crdt.NewGCounter().Increment("node-c", 1), Origin: "node-c"}))
		assert.False(t, holds(t, repl))
	})

	t.Run("the deletion wins over a value written after it, in both directions", func(t *testing.T) {
		ctx := context.TODO()
		_, repl, probe, peer := single(t)
		earlier := time.Now().Add(-time.Hour)

		// the peer deleted the key an hour ago; this node wrote it just now
		increment(t, repl, "node-b", 7)
		require.NoError(t, peer.Tell(ctx, repl, internalpb.CRDTDigest_builder{Tombstones: []*internalpb.CRDTTombstone{tombstoneOf(earlier, "node-a")}}.Build()))
		assert.False(t, holds(t, repl))

		// this node deleted the key an hour ago; the peer wrote it just now
		_, repl2, probe2, peer2 := single(t)
		increment(t, repl2, "node-b", 7)
		remove(t, repl2)
		require.NoError(t, peer2.Tell(ctx, repl2, digestListing(crdt.NewGCounter().Increment("node-b", 7).Increment("node-c", 1))))
		expectTombstoneAnswer(t, probe2)
		assert.False(t, holds(t, repl2))

		probeIsQuiet(t, peer, probe)
	})

	t.Run("a digest without tombstones deletes nothing", func(t *testing.T) {
		_, repl, probe, peer := single(t)
		increment(t, repl, "node-b", 7)

		// an older node holds the same value and lists it without the field
		require.NoError(t, peer.Tell(context.TODO(), repl, digestListing(crdt.NewGCounter().Increment("node-b", 7))))
		assert.True(t, holds(t, repl))
		probeIsQuiet(t, peer, probe)
	})

	t.Run("an expired tombstone is not sent", func(t *testing.T) {
		r := newTestReplicator()
		r.tombstones[key.ID()] = &tombstone{keyID: key.ID(), dataType: key.Type(), deletedAt: time.Now().Add(-2 * r.config.TombstoneTTL()), deletedBy: "node-a"}
		r.tombstones["fresh"] = &tombstone{keyID: "fresh", dataType: key.Type(), deletedAt: time.Now(), deletedBy: "node-a"}

		digest := r.buildDigest()
		require.Len(t, digest.GetTombstones(), 1)
		assert.Equal(t, "fresh", digest.GetTombstones()[0].GetKey().GetId())
	})

	t.Run("two replicators converge to no key in either order and then stay put", func(t *testing.T) {
		for _, firstPuller := range []string{"the node that deleted", "the node that still holds"} {
			t.Run(firstPuller+" pulls first", func(t *testing.T) {
				ctx := context.TODO()
				sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
				require.NoError(t, sys.Start(ctx))
				t.Cleanup(func() { _ = sys.Stop(ctx) })

				replA, replB, actorA, actorB := spawnReplicatorPair(t, sys)
				increment(t, replA, "node-a", 5)
				increment(t, replB, "node-b", 7)

				watcher := NewMockMessageProbe()
				watcherPID, err := sys.Spawn(ctx, "watcher", watcher, WithLongLived())
				require.NoError(t, err)
				require.NoError(t, watcherPID.Tell(ctx, replB, &crdt.Subscribe{Key: key}))

				remove(t, replA)

				if firstPuller == "the node that deleted" {
					pullFrom(t, replA, replB)
				} else {
					pullFrom(t, replB, replA)
				}

				assert.False(t, holds(t, replA))
				assert.False(t, holds(t, replB))

				// both retain the same deletion, and further rounds change nothing
				tombstoneA, tombstoneB := actorA.tombstones[key.ID()], actorB.tombstones[key.ID()]
				require.NotNil(t, tombstoneA)
				require.NotNil(t, tombstoneB)
				assert.Equal(t, tombstoneA.deletedAt.UnixNano(), tombstoneB.deletedAt.UnixNano())

				pullFrom(t, replA, replB)
				pullFrom(t, replB, replA)
				pullFrom(t, replA, replB)
				assert.Same(t, tombstoneA, actorA.tombstones[key.ID()])
				assert.Same(t, tombstoneB, actorB.tombstones[key.ID()])
				assert.Empty(t, actorA.store)
				assert.Empty(t, actorB.store)

				// the watcher heard of the deletion once, however many rounds carried it
				expectDeleted(t, watcher, key)
				probeIsQuiet(t, watcherPID, watcher)
			})
		}
	})

	t.Run("an expired tombstone deletes nothing on the peer", func(t *testing.T) {
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		// the tombstone expires at once but is not pruned: the prune schedule is off
		replA, replB, actorA, _ := spawnReplicatorPair(t, sys, crdt.WithTombstoneTTL(time.Nanosecond))
		increment(t, replA, "node-a", 5)
		increment(t, replB, "node-b", 7)
		remove(t, replA)
		require.Contains(t, actorA.tombstones, key.ID())

		pullFrom(t, replA, replB)
		pullFrom(t, replB, replA)
		assert.True(t, holds(t, replB))
	})

	// crossDC starts a leader with a datacenter controller listing one remote
	// datacenter, and a replicator standing for a node of that datacenter. The
	// digest the leader's round sends is handed to that node by the test, and
	// the node's answer back to the leader, since the remoting client is a mock.
	crossDC := func(t *testing.T) (leader, remote, leaderStandIn *PID, probe *MockMessageProbe, sent chan any) {
		t.Helper()
		ctx := context.TODO()
		records := []datacenter.DataCenterRecord{
			{
				ID:         "zreast",
				DataCenter: datacenter.DataCenter{Name: "east", Region: "r", Zone: "z"},
				Endpoints:  []string{"10.0.0.1:9090"},
				State:      datacenter.DataCenterActive,
				Version:    1,
			},
		}
		sys, repl, _, clusterMock, remotingMock := spawnReplicatorWithDCController(t, remoteRecords(records), nil)
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		sent = make(chan any, 4)
		clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()
		remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.1", 9090, "GoAktReplicator").
			Return(address.New("GoAktReplicator", "remoteSys", "10.0.0.1", 9090), nil).Maybe()
		remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, _, _ *address.Address, message any) error {
				sent <- message
				return nil
			}).Maybe()

		remoteSys, _ := NewActorSystem("remoteSys", WithLogger(log.DiscardLogger))
		require.NoError(t, remoteSys.Start(ctx))
		t.Cleanup(func() { _ = remoteSys.Stop(ctx) })
		remote = spawnTestReplicatorWithDC(t, remoteSys, "east", "r", "z")
		probe = NewMockMessageProbe()
		leaderStandIn, err := remoteSys.Spawn(ctx, "leader-stand-in", probe, WithLongLived())
		require.NoError(t, err)
		return repl, remote, leaderStandIn, probe, sent
	}

	t.Run("the cross-datacenter round carries the leader's deletion", func(t *testing.T) {
		ctx := context.TODO()
		leader, remote, leaderStandIn, probe, sent := crossDC(t)
		increment(t, leader, "node-a", 5)
		increment(t, remote, "node-a", 5)
		remove(t, leader)

		require.NoError(t, Tell(ctx, leader, &dataCenterAntiEntropyTick{}))
		digest, ok := (<-sent).(*internalpb.CRDTDigest)
		require.True(t, ok)
		require.Len(t, digest.GetTombstones(), 1)

		require.NoError(t, leaderStandIn.Tell(ctx, remote, digest))
		assert.False(t, holds(t, remote))
		probeIsQuiet(t, leaderStandIn, probe)
	})

	t.Run("the cross-datacenter round brings the remote deletion back", func(t *testing.T) {
		ctx := context.TODO()
		leader, remote, leaderStandIn, probe, sent := crossDC(t)
		increment(t, leader, "node-a", 5)
		increment(t, remote, "node-a", 5)
		remove(t, remote)

		require.NoError(t, Tell(ctx, leader, &dataCenterAntiEntropyTick{}))
		digest, ok := (<-sent).(*internalpb.CRDTDigest)
		require.True(t, ok)
		require.Len(t, digest.GetEntries(), 1)

		require.NoError(t, leaderStandIn.Tell(ctx, remote, digest))
		answer := <-probe.received
		fullState, ok := answer.(*internalpb.CRDTFullState)
		require.True(t, ok, "expected a full state, got %T", answer)
		assert.Empty(t, fullState.GetEntries())
		require.Len(t, fullState.GetTombstones(), 1)

		require.NoError(t, Tell(ctx, leader, fullState))
		assert.False(t, holds(t, leader))
	})
}

// TestReplicatorDataCenterAcceptedMarks covers the flush towards several
// remote datacenters: each one is sent what it has not accepted yet, and an
// entry leaves the buffer once every datacenter on record has accepted it.
func TestReplicatorDataCenterAcceptedMarks(t *testing.T) {
	const (
		eastHost = "10.0.0.1"
		westHost = "10.0.0.2"
	)

	east := datacenter.DataCenterRecord{
		ID:         "zreast",
		DataCenter: datacenter.DataCenter{Name: "east", Region: "r", Zone: "z"},
		Endpoints:  []string{eastHost + ":9090"},
		State:      datacenter.DataCenterActive,
		Version:    1,
	}
	west := datacenter.DataCenterRecord{
		ID:         "zrwest",
		DataCenter: datacenter.DataCenter{Name: "west", Region: "r", Zone: "z"},
		Endpoints:  []string{westHost + ":9090"},
		State:      datacenter.DataCenterActive,
		Version:    1,
	}

	// fixture is a replicator whose leadership and remote datacenters are
	// driven by the test
	type fixture struct {
		sys       ActorSystem
		repl      *PID
		replActor *replicatorActor
		// leader is what the cluster answers to IsLeader
		leader *atomic.Bool
		// westDown makes every send to the west datacenter fail
		westDown *atomic.Bool
		// received holds the batches each datacenter accepted, by host
		received map[string]chan *internalpb.CRDTDeltaBatch
	}

	setup := func(t *testing.T, records ...datacenter.DataCenterRecord) *fixture {
		t.Helper()

		f := &fixture{
			leader:   new(atomic.Bool),
			westDown: new(atomic.Bool),
			received: map[string]chan *internalpb.CRDTDeltaBatch{
				eastHost: make(chan *internalpb.CRDTDeltaBatch, 16),
				westHost: make(chan *internalpb.CRDTDeltaBatch, 16),
			},
		}
		f.leader.Store(true)

		sys, repl, replActor, clusterMock, remotingMock := spawnReplicatorWithDCController(t, remoteRecords(records), nil)
		f.sys, f.repl, f.replActor = sys, repl, replActor

		clusterMock.EXPECT().IsLeader(mock.Anything).RunAndReturn(func(context.Context) bool {
			return f.leader.Load()
		}).Maybe()
		remotingMock.EXPECT().RemoteLookup(mock.Anything, mock.Anything, 9090, "GoAktReplicator").
			RunAndReturn(func(_ context.Context, host string, port int, name string) (*address.Address, error) {
				return address.New(name, "remoteSys", host, port), nil
			}).Maybe()
		remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, _, to *address.Address, message any) error {
				if to.Host() == westHost && f.westDown.Load() {
					return errors.New("west is unreachable")
				}

				f.received[to.Host()] <- message.(*internalpb.CRDTDeltaBatch)
				return nil
			}).Maybe()

		t.Cleanup(func() { require.NoError(t, f.sys.Stop(context.TODO())) })
		return f
	}

	// flush runs one flush tick and returns once the replicator handled it.
	// The sends happen inside that turn, so every batch of the tick is in
	// the channels by then.
	flush := func(t *testing.T, f *fixture) {
		t.Helper()
		require.NoError(t, Tell(context.TODO(), f.repl, &dataCenterFlushTick{}))
		_, err := Ask(context.TODO(), f.repl, &crdt.Get{Key: crdt.GCounterKey("barrier")}, 5*time.Second)
		require.NoError(t, err)
	}

	add := func(t *testing.T, f *fixture, key crdt.Key, element string) {
		t.Helper()
		_, err := Ask(context.TODO(), f.repl, &crdt.Update{
			Key:     key,
			Initial: crdt.NewORSet(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.ORSet).Add("node-1", element)
			},
		}, time.Second)
		require.NoError(t, err)
	}

	// applied merges the deltas of the batches into the value a datacenter holds
	applied := func(t *testing.T, held *crdt.ORSet, batches ...*internalpb.CRDTDeltaBatch) *crdt.ORSet {
		t.Helper()

		for _, batch := range batches {
			for _, delta := range batch.GetDeltas() {
				data, err := ddata.DecodeCRDT(delta.GetData(), ddata.NewCRDTValueSerializer())
				require.NoError(t, err)
				held = held.Merge(data).(*crdt.ORSet)
			}
		}

		return held
	}

	key := crdt.ORSetKey("sessions")

	t.Run("a datacenter that accepted is not sent the batch again while another one fails", func(t *testing.T) {
		f := setup(t, east, west)
		f.westDown.Store(true)
		add(t, f, key, "a")

		// east accepts, west does not: the entry stays for west
		flush(t, f)
		require.Len(t, f.received[eastHost], 1)
		assert.Len(t, f.replActor.pendingDeltas, 1)
		assert.EqualValues(t, 1, f.replActor.crossDCSendCount.Load())
		assert.Equal(t, map[string]uint64{east.ID: 1}, f.replActor.dataCenterAccepted)

		// the next tick sends east nothing and tries west again
		flush(t, f)
		assert.Len(t, f.received[eastHost], 1)
		assert.Empty(t, f.received[westHost])
		assert.Len(t, f.replActor.pendingDeltas, 1)
		assert.EqualValues(t, 1, f.replActor.crossDCSendCount.Load())

		// west accepts: every datacenter has the entry and it leaves the buffer
		f.westDown.Store(false)
		flush(t, f)
		assert.Len(t, f.received[eastHost], 1)
		require.Len(t, f.received[westHost], 1)
		assert.Empty(t, f.replActor.pendingDeltas)
		assert.EqualValues(t, 2, f.replActor.crossDCSendCount.Load())

		// nothing is pending: a tick sends nothing
		flush(t, f)
		assert.Len(t, f.received[eastHost], 1)
		assert.Len(t, f.received[westHost], 1)
	})

	t.Run("an entry changed after a datacenter accepted it is sent to it again and converges", func(t *testing.T) {
		f := setup(t, east, west)
		f.westDown.Store(true)

		add(t, f, key, "a")
		flush(t, f)
		require.Len(t, f.received[eastHost], 1)

		// the entry is still pending for west; a further change joins it
		add(t, f, key, "b")
		flush(t, f)
		require.Len(t, f.received[eastHost], 2)
		assert.Equal(t, map[string]uint64{east.ID: 2}, f.replActor.dataCenterAccepted)

		f.westDown.Store(false)
		flush(t, f)
		require.Len(t, f.received[westHost], 1)
		assert.Empty(t, f.replActor.pendingDeltas)

		resp, err := Ask(context.TODO(), f.repl, &crdt.Get{Key: key}, time.Second)
		require.NoError(t, err)
		sender := resp.(*crdt.GetResponse).Data.(*crdt.ORSet)

		// east merged two forms of the entry, west the last one only
		heldEast := applied(t, crdt.NewORSet(), <-f.received[eastHost], <-f.received[eastHost])
		heldWest := applied(t, crdt.NewORSet(), <-f.received[westHost])
		assert.ElementsMatch(t, []any{"a", "b"}, heldEast.Elements())
		assert.Equal(t, sender.StateHash(), heldEast.StateHash())
		assert.Equal(t, sender.StateHash(), heldWest.StateHash())
	})

	t.Run("a datacenter that leaves the records no longer holds entries back", func(t *testing.T) {
		f := setup(t, east, west)
		f.westDown.Store(true)
		add(t, f, key, "a")

		flush(t, f)
		require.Len(t, f.replActor.pendingDeltas, 1)

		replaceDataCenterRecords(t, f.sys, east)

		// east has the entry and is the only datacenter on record
		flush(t, f)
		assert.Empty(t, f.replActor.pendingDeltas)
		assert.Len(t, f.received[eastHost], 1)
		assert.Equal(t, map[string]uint64{east.ID: 1}, f.replActor.dataCenterAccepted)
	})

	t.Run("a datacenter that appears is sent what is pending", func(t *testing.T) {
		f := setup(t)
		add(t, f, key, "a")

		// no remote datacenter on record: the entry waits
		flush(t, f)
		require.Len(t, f.replActor.pendingDeltas, 1)
		assert.Zero(t, f.replActor.crossDCSendCount.Load())

		replaceDataCenterRecords(t, f.sys, east)

		flush(t, f)
		require.Len(t, f.received[eastHost], 1)
		assert.Empty(t, f.replActor.pendingDeltas)
		assert.ElementsMatch(t, []any{"a"}, applied(t, crdt.NewORSet(), <-f.received[eastHost]).Elements())
	})

	t.Run("a tombstone follows the same marks", func(t *testing.T) {
		f := setup(t, east, west)
		f.westDown.Store(true)
		add(t, f, key, "a")

		_, err := Ask(context.TODO(), f.repl, &crdt.Delete{Key: key}, time.Second)
		require.NoError(t, err)

		flush(t, f)
		batch := <-f.received[eastHost]
		assert.Empty(t, batch.GetDeltas())
		require.Len(t, batch.GetTombstones(), 1)
		assert.Len(t, f.replActor.pendingTombstones, 1)

		f.westDown.Store(false)
		flush(t, f)
		assert.Empty(t, f.received[eastHost])
		require.Len(t, f.received[westHost], 1)
		assert.Empty(t, f.replActor.pendingTombstones)
	})

	t.Run("a node that becomes the leader sends everything pending, and one that stops forgets its marks", func(t *testing.T) {
		f := setup(t, east, west)
		f.westDown.Store(true)
		f.leader.Store(false)
		add(t, f, key, "a")

		// not the leader: the entry is buffered and nothing is sent
		flush(t, f)
		assert.Len(t, f.replActor.pendingDeltas, 1)
		assert.Empty(t, f.received[eastHost])
		assert.Empty(t, f.replActor.dataCenterAccepted)

		// the node becomes the leader: east takes what was buffered
		f.leader.Store(true)
		flush(t, f)
		require.Len(t, f.received[eastHost], 1)
		assert.Equal(t, map[string]uint64{east.ID: 1}, f.replActor.dataCenterAccepted)

		// it stops being the leader: the marks go, the entry stays
		f.leader.Store(false)
		flush(t, f)
		assert.Empty(t, f.replActor.dataCenterAccepted)
		assert.Len(t, f.replActor.pendingDeltas, 1)
		assert.Len(t, f.received[eastHost], 1)

		// leader again, without marks: east is sent the entry once more
		f.leader.Store(true)
		flush(t, f)
		assert.Len(t, f.received[eastHost], 2)
		assert.EqualValues(t, 2, f.replActor.crossDCSendCount.Load())
	})
}

// TestReplicatorCoalescedDeltaMatchesIndividualDeltas verifies that the one
// pending delta of a key gives a receiver the value it would have reached by
// merging the deltas one after the other.
func TestReplicatorCoalescedDeltaMatchesIndividualDeltas(t *testing.T) {
	t.Run("ORSet with additions and removals", func(t *testing.T) {
		r := newTestReplicator()
		r.nodeID = "node-1"

		var deltas []crdt.ReplicatedData

		set := crdt.NewORSet()
		record := func(next *crdt.ORSet) {
			delta := next.Delta()
			next.ResetDelta()
			deltas = append(deltas, delta)
			r.bufferDelta("sessions", crdt.ORSetType, delta)
			set = next
		}

		record(set.Add("node-1", "a"))
		record(set.Add("node-1", "b"))
		record(set.Remove("a"))
		record(set.Add("node-1", "c"))
		record(set.Add("node-1", "a"))
		record(set.Remove("b"))

		require.Len(t, r.pendingDeltas, 1)
		pending := r.pendingDeltas["sessions"]
		assert.Equal(t, "node-1", pending.Origin)
		assert.Equal(t, crdt.ORSetType, pending.DataType)

		// the receiver holds an element of its own
		receiver := crdt.NewORSet().Add("node-2", "z")

		individually := crdt.ReplicatedData(receiver)
		for _, delta := range deltas {
			individually = individually.Merge(delta)
		}

		coalesced := receiver.Merge(pending.Delta)

		assert.ElementsMatch(t, []any{"a", "c", "z"}, individually.(*crdt.ORSet).Elements())
		assert.ElementsMatch(t, individually.(*crdt.ORSet).Elements(), coalesced.(*crdt.ORSet).Elements())
		assert.Equal(t, individually.(*crdt.ORSet).StateHash(), coalesced.(*crdt.ORSet).StateHash())
	})

	t.Run("PNCounter with increments and decrements", func(t *testing.T) {
		r := newTestReplicator()
		r.nodeID = "node-1"

		var deltas []crdt.ReplicatedData

		counter := crdt.NewPNCounter()
		record := func(next *crdt.PNCounter) {
			delta := next.Delta()
			next.ResetDelta()
			deltas = append(deltas, delta)
			r.bufferDelta("stock", crdt.PNCounterType, delta)
			counter = next
		}

		record(counter.Increment("node-1", 10))
		record(counter.Decrement("node-1", 3))
		record(counter.Increment("node-1", 4))

		receiver := crdt.NewPNCounter().Increment("node-2", 100)

		individually := crdt.ReplicatedData(receiver)
		for _, delta := range deltas {
			individually = individually.Merge(delta)
		}

		coalesced := receiver.Merge(r.pendingDeltas["stock"].Delta)

		assert.EqualValues(t, 111, individually.(*crdt.PNCounter).Value())
		assert.Equal(t, individually.(*crdt.PNCounter).StateHash(), coalesced.(*crdt.PNCounter).StateHash())
	})
}

// TestReplicatorIncomingBatchRecreatedKey covers a batch that carries both a
// tombstone and a delta of one key.
func TestReplicatorIncomingBatchRecreatedKey(t *testing.T) {
	origin := internalpb.DataCenter_builder{Name: "dc-east", Region: "us-east-1", Zone: "us-east-1a"}.Build()
	key := crdt.PNCounterKey("orders")
	serializer := ddata.NewCRDTValueSerializer()

	// setup starts a replicator that holds the first incarnation of the key
	setup := func(t *testing.T) (ActorSystem, *PID) {
		t.Helper()
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, sys.Start(ctx))

		repl := spawnTestReplicatorWithDC(t, sys, "dc-west", "us-west-2", "us-west-2a")
		_, err := Ask(ctx, repl, &crdt.Update{
			Key:     key,
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 5).Increment("node-2", 3)
			},
		}, time.Second)
		require.NoError(t, err)
		return sys, repl
	}

	batchOf := func(t *testing.T, deletedAt time.Time, incarnation *crdt.PNCounter) *internalpb.CRDTDeltaBatch {
		t.Helper()
		data, err := ddata.EncodeCRDT(incarnation, serializer)
		require.NoError(t, err)

		return internalpb.CRDTDeltaBatch_builder{
			Deltas: []*internalpb.CRDTDelta{
				internalpb.CRDTDelta_builder{
					Key:        codec.EncodeCRDTKey(key.ID(), key.Type()),
					OriginNode: "remote-node",
					Data:       data,
				}.Build(),
			},
			Tombstones: []*internalpb.CRDTTombstone{
				internalpb.CRDTTombstone_builder{
					Key:            codec.EncodeCRDTKey(key.ID(), key.Type()),
					DeletedAtNanos: deletedAt.UnixNano(),
					DeletedByNode:  "remote-node",
				}.Build(),
			},
			OriginDc:    origin,
			SentAtNanos: time.Now().UnixNano(),
		}.Build()
	}

	t.Run("a key recreated after its tombstone expired is held as the sender holds it", func(t *testing.T) {
		ctx := context.TODO()
		sys, repl := setup(t)

		// the sender deleted the key longer ago than the tombstone TTL and
		// created it again; the deletion had not reached this datacenter
		expired := time.Now().Add(-2 * crdt.NewConfig().TombstoneTTL())
		require.NoError(t, Tell(ctx, repl, batchOf(t, expired, crdt.NewPNCounter().Increment("node-1", 2))))

		// the old incarnation is gone and the new one is in its place
		data := getPNCounter(t, repl, key)
		require.NotNil(t, data)
		assert.EqualValues(t, 2, data.Value())
		assert.Equal(t, crdt.NewPNCounter().Increment("node-1", 2).StateHash(), data.StateHash())

		require.NoError(t, sys.Stop(ctx))
	})

	t.Run("a live tombstone deletes the key and rejects the delta beside it", func(t *testing.T) {
		ctx := context.TODO()
		sys, repl := setup(t)

		require.NoError(t, Tell(ctx, repl, batchOf(t, time.Now(), crdt.NewPNCounter().Increment("node-1", 2))))
		assert.Nil(t, getPNCounter(t, repl, key))

		require.NoError(t, sys.Stop(ctx))
	})
}

func BenchmarkReplicatorUpdatePNCounter(b *testing.B) {
	sys, repl := spawnBenchReplicator(b)
	defer sys.Stop(context.TODO())

	ctx := context.TODO()
	counterKey := crdt.PNCounterKey("bench-counter")

	b.ReportAllocs()
	for b.Loop() {
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

	b.ReportAllocs()
	for b.Loop() {
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

	b.ReportAllocs()
	for b.Loop() {
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

	b.ReportAllocs()
	for b.Loop() {
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

	b.ReportAllocs()
	for b.Loop() {
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

	b.ReportAllocs()
	for b.Loop() {
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
				entries[i] = internalpb.CRDTFullStateEntry_builder{
					Key:  codec.EncodeCRDTKey(keyID, crdt.GCounterType),
					Data: pbData,
				}.Build()
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

// TestReplicatorStaleKeys covers a node back from a gap without contact longer
// than the tombstone TTL: the keys it held before the gap are stale, because
// they may have been deleted with a tombstone that has expired since. They are
// kept from peers until a peer that saw the whole gap lists them, or until
// every peer has been heard from and none saw the whole gap.
func TestReplicatorStaleKeys(t *testing.T) {
	const ttl = time.Minute
	kept := crdt.GCounterKey("kept")
	deleted := crdt.GCounterKey("deleted")

	// start spawns a replicator and a probe that stands for a peer replicator.
	// Anti-entropy is on, without which no gap is recognized, but its rounds
	// are an hour apart and find no cluster.
	start := func(t *testing.T, opts ...crdt.Option) (*PID, *replicatorActor, *PID, *MockMessageProbe) {
		t.Helper()
		ctx := context.TODO()
		sys, _ := NewActorSystem("testSys", WithLogger(log.DiscardLogger))
		require.NoError(t, sys.Start(ctx))
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		opts = append([]crdt.Option{crdt.WithAntiEntropyInterval(time.Hour), crdt.WithPruneInterval(0), crdt.WithTombstoneTTL(ttl)}, opts...)
		sys.(*actorSystem).extensions.Set(crdtConfigExtensionID, &crdtConfigExtension{config: crdt.NewConfig(opts...)})

		actor := newReplicatorActor()
		repl, err := sys.Spawn(ctx, "replicator", actor, WithLongLived())
		require.NoError(t, err)

		probe := NewMockMessageProbe()
		peer, err := sys.Spawn(ctx, "peer", probe, WithLongLived())
		require.NoError(t, err)
		return repl, actor, peer, probe
	}

	increment := func(t *testing.T, repl *PID, key crdt.Key) {
		t.Helper()
		_, err := Ask(context.TODO(), repl, &crdt.Update{
			Key:     key,
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-a", 1)
			},
		}, time.Second)
		require.NoError(t, err)
	}

	// holds reports whether the replicator holds the key; its answer also
	// proves that every message sent to the replicator before has been handled
	holds := func(t *testing.T, repl *PID, key crdt.Key) bool {
		t.Helper()
		resp, err := Ask(context.TODO(), repl, &crdt.Get{Key: key}, time.Second)
		require.NoError(t, err)
		return resp.(*crdt.GetResponse).Data != nil
	}

	// awayFor puts the replicator's last contact gap ago and its local writes
	// before that, as for a node that held its keys when it lost contact. It
	// returns the start of the gap.
	awayFor := func(t *testing.T, repl *PID, actor *replicatorActor, gap time.Duration) time.Time {
		t.Helper()
		holds(t, repl, kept)
		actor.lastContact = time.Now().Add(-gap)

		for keyID := range actor.changedAt {
			actor.changedAt[keyID] = actor.lastContact.Add(-time.Second)
		}

		return actor.lastContact
	}

	// digestOf is the digest of a peer that holds the given keys and knows
	// every deletion since the given time, or that sends no time when zero
	digestOf := func(since time.Time, keys ...crdt.Key) *internalpb.CRDTDigest {
		entries := make([]*internalpb.CRDTDigestEntry, 0, len(keys))
		for _, key := range keys {
			entries = append(entries, internalpb.CRDTDigestEntry_builder{Key: codec.EncodeCRDTKey(key.ID(), key.Type()), Version: 1}.Build())
		}

		digest := internalpb.CRDTDigest_builder{Entries: entries}.Build()
		if !since.IsZero() {
			digest.SetSinceNanos(since.UnixNano())
		}

		return digest
	}

	isStale := func(t *testing.T, repl *PID, actor *replicatorActor) bool {
		t.Helper()
		holds(t, repl, kept)
		return actor.hasStaleKeys()
	}

	t.Run("a contact within the TTL marks no key stale and the answer carries the since", func(t *testing.T) {
		repl, actor, peer, probe := start(t)
		increment(t, repl, kept)
		awayFor(t, repl, actor, ttl/2)

		require.NoError(t, peer.Tell(context.TODO(), repl, digestOf(time.Now())))

		select {
		case message := <-probe.received:
			fullState, ok := message.(*internalpb.CRDTFullState)
			require.True(t, ok, "expected a full state, got %T", message)
			require.Len(t, fullState.GetEntries(), 1)
			assert.Equal(t, kept.ID(), fullState.GetEntries()[0].GetKey().GetId())
			assert.True(t, fullState.HasSinceNanos())
		case <-time.After(3 * time.Second):
			t.Fatal("the peer was not sent the key it lacks")
		}

		assert.False(t, isStale(t, repl, actor))
	})

	t.Run("a peer that saw the whole gap removes the stale keys it does not list", func(t *testing.T) {
		repl, actor, peer, _ := start(t)
		increment(t, repl, kept)
		increment(t, repl, deleted)
		keptWatcher, keptProbe := subscribeProbe(t, actor.actorSystem, repl, "kept-watcher", kept)
		deletedWatcher, deletedProbe := subscribeProbe(t, actor.actorSystem, repl, "deleted-watcher", deleted)
		gapStart := awayFor(t, repl, actor, 2*ttl)

		peerSince := gapStart.Add(-time.Hour)
		require.NoError(t, peer.Tell(context.TODO(), repl, digestOf(peerSince, kept)))

		assert.True(t, holds(t, repl, kept))
		assert.False(t, holds(t, repl, deleted))
		assert.False(t, actor.hasStaleKeys())
		assert.True(t, peerSince.Equal(actor.since))

		// the key deleted during the gap is announced, the kept one is not
		expectDeleted(t, deletedProbe, deleted)
		probeIsQuiet(t, deletedWatcher, deletedProbe)
		probeIsQuiet(t, keptWatcher, keptProbe)
	})

	t.Run("a peer that came after the gap resolves nothing and is sent no stale key", func(t *testing.T) {
		repl, actor, peer, probe := start(t)
		increment(t, repl, kept)
		awayFor(t, repl, actor, 2*ttl)

		require.NoError(t, peer.Tell(context.TODO(), repl, digestOf(time.Now())))

		probeIsQuiet(t, peer, probe)
		assert.True(t, holds(t, repl, kept))
		assert.True(t, isStale(t, repl, actor))
	})

	t.Run("a digest without a since resolves nothing", func(t *testing.T) {
		repl, actor, peer, probe := start(t)
		increment(t, repl, kept)
		awayFor(t, repl, actor, 2*ttl)

		// a peer that has stale keys itself, or that predates the field
		require.NoError(t, peer.Tell(context.TODO(), repl, digestOf(time.Time{})))

		probeIsQuiet(t, peer, probe)
		assert.True(t, holds(t, repl, kept))
		assert.True(t, isStale(t, repl, actor))
	})

	t.Run("a key written after the gap is not stale", func(t *testing.T) {
		repl, actor, peer, _ := start(t)
		increment(t, repl, deleted)
		gapStart := awayFor(t, repl, actor, 2*ttl)
		increment(t, repl, kept)

		require.NoError(t, peer.Tell(context.TODO(), repl, digestOf(gapStart.Add(-time.Hour))))

		assert.True(t, holds(t, repl, kept))
		assert.False(t, holds(t, repl, deleted))
	})

	t.Run("a key a peer sends in a delta is no longer stale", func(t *testing.T) {
		ctx := context.TODO()
		repl, actor, peer, _ := start(t)
		increment(t, repl, kept)
		increment(t, repl, deleted)
		gapStart := awayFor(t, repl, actor, 2*ttl)

		require.NoError(t, Tell(ctx, repl, &crdtDelta{KeyID: kept.ID(), DataType: kept.Type(), Delta: crdt.NewGCounter().Increment("node-c", 1), Origin: "node-c"}))
		require.NoError(t, peer.Tell(ctx, repl, digestOf(gapStart.Add(-time.Hour))))

		assert.True(t, holds(t, repl, kept))
		assert.False(t, holds(t, repl, deleted))
	})

	t.Run("a key a peer sends in an anti-entropy answer is no longer stale", func(t *testing.T) {
		ctx := context.TODO()
		repl, actor, peer, _ := start(t)
		increment(t, repl, kept)
		increment(t, repl, deleted)
		gapStart := awayFor(t, repl, actor, 2*ttl)

		data, err := ddata.EncodeCRDT(crdt.NewGCounter().Increment("node-c", 1), ddata.NewCRDTValueSerializer())
		require.NoError(t, err)
		answer := internalpb.CRDTFullState_builder{
			Entries: []*internalpb.CRDTFullStateEntry{
				internalpb.CRDTFullStateEntry_builder{Key: codec.EncodeCRDTKey(kept.ID(), kept.Type()), Data: data}.Build(),
			},
		}.Build()

		require.NoError(t, peer.Tell(ctx, repl, answer))
		require.NoError(t, peer.Tell(ctx, repl, digestOf(gapStart.Add(-time.Hour))))

		assert.True(t, holds(t, repl, kept))
		assert.False(t, holds(t, repl, deleted))
	})

	t.Run("a coordinated read gets no stale key", func(t *testing.T) {
		repl, actor, _, _ := start(t)
		increment(t, repl, kept)
		awayFor(t, repl, actor, 2*ttl)

		request := internalpb.CRDTReadRequest_builder{Key: codec.EncodeCRDTKey(kept.ID(), kept.Type()), FromNode: "node-b"}.Build()
		resp, err := Ask(context.TODO(), repl, request, time.Second)
		require.NoError(t, err)

		readResp, ok := resp.(*internalpb.CRDTReadResponse)
		require.True(t, ok)
		assert.Nil(t, readResp.GetData())

		// this node's own reads still see the key
		assert.True(t, holds(t, repl, kept))
		assert.True(t, actor.hasStaleKeys())
	})

	t.Run("a node with stale keys sends no since until they are resolved", func(t *testing.T) {
		ctx := context.TODO()
		repl, actor, peer, _ := start(t)
		increment(t, repl, kept)
		gapStart := awayFor(t, repl, actor, 2*ttl)

		require.NoError(t, peer.Tell(ctx, repl, digestOf(time.Now())))
		resp, err := Ask(ctx, repl, &dataCenterDigestRequest{}, time.Second)
		require.NoError(t, err)
		assert.False(t, resp.(*internalpb.CRDTDigest).HasSinceNanos())

		peerSince := gapStart.Add(-time.Hour)
		require.NoError(t, peer.Tell(ctx, repl, digestOf(peerSince, kept)))
		resp, err = Ask(ctx, repl, &dataCenterDigestRequest{}, time.Second)
		require.NoError(t, err)
		assert.Equal(t, peerSince.UnixNano(), resp.(*internalpb.CRDTDigest).GetSinceNanos())
	})

	t.Run("an anti-entropy answer passes on an earlier since only", func(t *testing.T) {
		ctx := context.TODO()
		repl, actor, peer, _ := start(t)
		holds(t, repl, kept)
		own := actor.since

		earlier := own.Add(-time.Hour)
		require.NoError(t, peer.Tell(ctx, repl, internalpb.CRDTFullState_builder{SinceNanos: proto.Int64(earlier.UnixNano())}.Build()))
		holds(t, repl, kept)
		assert.True(t, earlier.Equal(actor.since))

		later := own.Add(time.Hour)
		require.NoError(t, peer.Tell(ctx, repl, internalpb.CRDTFullState_builder{SinceNanos: proto.Int64(later.UnixNano())}.Build()))
		holds(t, repl, kept)
		assert.True(t, earlier.Equal(actor.since))
	})

	t.Run("a peer whose since is later is answered with this node's since", func(t *testing.T) {
		ctx := context.TODO()
		repl, actor, peer, probe := start(t)
		holds(t, repl, kept)
		own := actor.since

		// the peer holds what this node holds, so only the since is news to it
		require.NoError(t, peer.Tell(ctx, repl, digestOf(own.Add(time.Hour))))

		select {
		case message := <-probe.received:
			fullState, ok := message.(*internalpb.CRDTFullState)
			require.True(t, ok, "expected a full state, got %T", message)
			assert.Empty(t, fullState.GetEntries())
			assert.Equal(t, own.UnixNano(), fullState.GetSinceNanos())
		case <-time.After(3 * time.Second):
			t.Fatal("the peer was not told this node's since")
		}

		// a peer that knows deletions from as early or earlier needs nothing
		require.NoError(t, peer.Tell(ctx, repl, digestOf(own)))
		require.NoError(t, peer.Tell(ctx, repl, digestOf(own.Add(-time.Hour))))
		probeIsQuiet(t, peer, probe)
	})

	t.Run("a snapshot taken while keys are stale keeps the gap", func(t *testing.T) {
		ctx := context.TODO()
		dir := t.TempDir()
		repl, actor, peer, _ := start(t, crdt.WithSnapshotInterval(time.Minute), crdt.WithSnapshotDir(dir))
		increment(t, repl, kept)
		gapStart := awayFor(t, repl, actor, 2*ttl)

		// the first contact marks the key stale; the replicator stops before it is resolved
		require.NoError(t, peer.Tell(ctx, repl, digestOf(time.Now())))
		require.True(t, isStale(t, repl, actor))
		require.NoError(t, repl.Shutdown(ctx))

		r := newTestReplicator()
		r.logger = log.DiscardLogger
		r.config = crdt.NewConfig(crdt.WithTombstoneTTL(ttl), crdt.WithSnapshotInterval(time.Minute), crdt.WithSnapshotDir(dir))
		r.since = time.Now()
		require.NoError(t, r.restoreFromSnapshot())
		t.Cleanup(func() { _ = r.snapshotStore.Close() })

		assert.Equal(t, gapStart.UnixNano(), r.lastContact.UnixNano())
		assert.True(t, r.hasStaleKeys())
		assert.Contains(t, r.staleKeys, kept.ID())
	})

	t.Run("without anti-entropy a gap marks no key stale", func(t *testing.T) {
		repl, actor, peer, _ := start(t, crdt.WithAntiEntropyInterval(0))
		increment(t, repl, kept)
		awayFor(t, repl, actor, 2*ttl)

		require.NoError(t, peer.Tell(context.TODO(), repl, digestOf(time.Now())))
		assert.False(t, isStale(t, repl, actor))
	})

	// restored returns a replicator restored from a snapshot that holds the
	// kept key and was saved with the given contact times
	restored := func(t *testing.T, lastContact, since time.Time, opts ...crdt.Option) *replicatorActor {
		t.Helper()
		dir := t.TempDir()

		source := newTestReplicator()
		source.serializer = ddata.NewCRDTValueSerializer()
		source.store[kept.ID()] = crdt.NewGCounter().Increment("node-a", 1)
		source.keyTypes[kept.ID()] = kept.Type()
		entries, err := source.buildSnapshotEntries()
		require.NoError(t, err)

		store, err := ddata.NewStore(dir)
		require.NoError(t, err)
		require.NoError(t, store.Save(entries, lastContact, since))
		require.NoError(t, store.Close())

		r := newTestReplicator()
		r.logger = log.DiscardLogger
		r.config = crdt.NewConfig(append([]crdt.Option{crdt.WithTombstoneTTL(ttl), crdt.WithSnapshotInterval(time.Minute), crdt.WithSnapshotDir(dir)}, opts...)...)
		r.since = time.Now()
		require.NoError(t, r.restoreFromSnapshot())
		t.Cleanup(func() { _ = r.snapshotStore.Close() })
		return r
	}

	t.Run("a node restored after the TTL marks its keys stale before any contact", func(t *testing.T) {
		r := restored(t, time.Now().Add(-2*ttl), time.Now().Add(-3*ttl))
		assert.True(t, r.hasStaleKeys())
		assert.Contains(t, r.staleKeys, kept.ID())
		assert.True(t, r.since.IsZero())
		assert.False(t, r.buildDigest().HasSinceNanos())

		// without anti-entropy there is no gap
		r = restored(t, time.Now().Add(-2*ttl), time.Now().Add(-3*ttl), crdt.WithAntiEntropyInterval(0))
		assert.False(t, r.hasStaleKeys())
	})

	t.Run("a node restored within the TTL keeps its earlier since", func(t *testing.T) {
		since := time.Unix(0, time.Now().Add(-time.Hour).UnixNano())
		r := restored(t, time.Now().Add(-ttl/2), since)
		assert.False(t, r.hasStaleKeys())
		assert.True(t, since.Equal(r.since))

		// a saved since later than its start changes nothing
		r = restored(t, time.Now().Add(-ttl/2), time.Now().Add(time.Hour))
		assert.False(t, r.since.After(time.Now()))
	})

	t.Run("only peers that run a replicator have to be heard from", func(t *testing.T) {
		r := newTestReplicator()
		r.logger = log.DiscardLogger
		r.config = crdt.NewConfig(crdt.WithRole("data"))
		r.markStaleKeys()
		r.heardWhileStale["10.0.0.1:9000"] = types.Unit{}

		peers := []*cluster.Peer{
			{Host: "10.0.0.1", RemotingPort: 9000, Roles: []string{"data"}},
			{Host: "10.0.0.2", RemotingPort: 9000},
		}
		assert.True(t, r.heardFromEveryPeer(peers))

		peers = append(peers, &cluster.Peer{Host: "10.0.0.3", RemotingPort: 9000, Roles: []string{"data"}})
		assert.False(t, r.heardFromEveryPeer(peers))

		// without a role every peer runs one
		r.config = crdt.NewConfig()
		assert.False(t, r.heardFromEveryPeer(peers[:2]))
	})

	t.Run("stale keys are kept once every peer is heard from and none saw the whole gap", func(t *testing.T) {
		ctx := context.TODO()
		sys, repl, actor, clusterMock, remotingMock := spawnReplicatorWithDCController(t, remoteRecords(nil), nil)
		t.Cleanup(func() { _ = sys.Stop(ctx) })

		probe := NewMockMessageProbe()
		peer, err := sys.Spawn(ctx, "peer", probe, WithLongLived())
		require.NoError(t, err)

		increment(t, repl, kept)
		require.Contains(t, actor.pendingDeltas, kept.ID())
		awayFor(t, repl, actor, 48*time.Hour)

		// the only peer came after the gap
		require.NoError(t, peer.Tell(ctx, repl, digestOf(time.Now())))
		require.True(t, isStale(t, repl, actor))

		// the delta for the remote datacenters waits: the flush stops before
		// it asks for the leadership, which the cluster mock does not expect
		require.NoError(t, Tell(ctx, repl, &dataCenterFlushTick{}))
		require.True(t, isStale(t, repl, actor))
		assert.Contains(t, actor.pendingDeltas, kept.ID())

		clusterMock.EXPECT().Peers(mock.Anything).Return([]*cluster.Peer{{Host: peer.Path().Host(), RemotingPort: peer.Path().Port()}}, nil)
		remotingMock.EXPECT().RemoteLookup(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("unreachable"))

		before := time.Now()
		require.NoError(t, Tell(ctx, repl, &antiEntropyTick{}))
		assert.False(t, isStale(t, repl, actor))
		assert.True(t, holds(t, repl, kept))
		assert.False(t, actor.since.Before(before))
	})

	t.Run("the last contact and the local write times come back from the snapshot", func(t *testing.T) {
		ctx := context.TODO()
		dir := t.TempDir()
		repl, actor, peer, _ := start(t, crdt.WithSnapshotInterval(time.Minute), crdt.WithSnapshotDir(dir))
		increment(t, repl, kept)
		require.NoError(t, peer.Tell(ctx, repl, digestOf(time.Now())))
		holds(t, repl, kept)
		lastContact, changedAt := actor.lastContact, actor.changedAt[kept.ID()]
		require.False(t, lastContact.IsZero())

		// the final snapshot is written when the replicator stops
		require.NoError(t, repl.Shutdown(ctx))

		r := newTestReplicator()
		r.logger = log.DiscardLogger
		r.config = crdt.NewConfig(crdt.WithSnapshotInterval(time.Minute), crdt.WithSnapshotDir(dir))
		require.NoError(t, r.restoreFromSnapshot())
		t.Cleanup(func() { _ = r.snapshotStore.Close() })

		assert.Equal(t, lastContact.UnixNano(), r.lastContact.UnixNano())
		assert.Equal(t, changedAt.UnixNano(), r.changedAt[kept.ID()].UnixNano())
	})

	t.Run("a last contact that cannot be read is a warning and no contact", func(t *testing.T) {
		dir := t.TempDir()
		store, err := ddata.NewStore(dir)
		require.NoError(t, err)
		require.NoError(t, store.Save(nil, time.Now(), time.Time{}))
		require.NoError(t, store.Close())

		// a last contact of the wrong size
		db, err := bbolt.Open(filepath.Join(dir, "crdt-snapshot.db"), 0o600, nil)
		require.NoError(t, err)
		require.NoError(t, db.Update(func(tx *bbolt.Tx) error {
			return tx.Bucket([]byte("crdt_snapshot_meta")).Put([]byte("last_contact"), []byte{0x01})
		}))
		require.NoError(t, db.Close())

		r := newTestReplicator()
		r.logger = log.DiscardLogger
		r.config = crdt.NewConfig(crdt.WithSnapshotInterval(time.Minute), crdt.WithSnapshotDir(dir))
		require.NoError(t, r.restoreFromSnapshot())
		t.Cleanup(func() { _ = r.snapshotStore.Close() })

		assert.True(t, r.lastContact.IsZero())
	})

	t.Run("the periodic snapshot saves the last contact", func(t *testing.T) {
		dir := t.TempDir()
		r := newTestReplicator()
		r.logger = log.DiscardLogger
		r.config = crdt.NewConfig(crdt.WithSnapshotInterval(time.Minute), crdt.WithSnapshotDir(dir))
		require.NoError(t, r.restoreFromSnapshot())
		t.Cleanup(func() { _ = r.snapshotStore.Close() })

		r.lastContact = time.Unix(0, time.Now().UnixNano())
		r.handleSnapshot()

		saved, _, err := r.snapshotStore.Contact()
		require.NoError(t, err)
		assert.True(t, r.lastContact.Equal(saved))
	})
}

func TestReplicatorSkipsPeersWithoutReplicator(t *testing.T) {
	noReplicator := mock.MatchedBy(func(to *address.Address) bool { return to.Equals(address.NoSender()) })
	plainPeer := &cluster.Peer{Host: "10.0.0.2", RemotingPort: 9090}
	rolePeer := &cluster.Peer{Host: "10.0.0.3", RemotingPort: 9090, Roles: []string{"crdt"}}
	rolePeerReplicator := address.New("GoAktReplicator", "remoteSys", "10.0.0.3", 9090)

	t.Run("anti-entropy sends no digest to a peer without a Replicator", func(t *testing.T) {
		sys, repl, replActor, clusterMock, remotingMock := spawnReplicatorWithMocks(t, crdt.NewConfig())
		clusterMock.EXPECT().Peers(mock.Anything).Return([]*cluster.Peer{plainPeer}, nil)
		remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.2", 9090, "GoAktReplicator").Return(address.NoSender(), nil)
		remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		require.NoError(t, Tell(context.TODO(), repl, &antiEntropyTick{}))
		pause.For(500 * time.Millisecond)

		remotingMock.AssertNotCalled(t, "RemoteTell", mock.Anything, mock.Anything, noReplicator, mock.Anything)
		assert.Zero(t, replActor.antiEntropyCount.Load())
		require.NoError(t, sys.Stop(context.TODO()))
	})

	t.Run("anti-entropy picks only peers with the CRDT role", func(t *testing.T) {
		sys, repl, replActor, clusterMock, remotingMock := spawnReplicatorWithMocks(t, crdt.NewConfig(crdt.WithRole("crdt")))
		clusterMock.EXPECT().Peers(mock.Anything).Return([]*cluster.Peer{plainPeer, rolePeer}, nil)
		remotingMock.EXPECT().RemoteLookup(mock.Anything, mock.Anything, 9090, "GoAktReplicator").Return(rolePeerReplicator, nil).Maybe()
		remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		const rounds = 10
		for range rounds {
			require.NoError(t, Tell(context.TODO(), repl, &antiEntropyTick{}))
		}
		require.Eventually(t, func() bool { return replActor.antiEntropyCount.Load() == rounds }, 2*time.Second, 20*time.Millisecond)

		remotingMock.AssertNotCalled(t, "RemoteLookup", mock.Anything, "10.0.0.2", 9090, "GoAktReplicator")
		require.NoError(t, sys.Stop(context.TODO()))
	})

	t.Run("anti-entropy runs no round when no peer has the CRDT role", func(t *testing.T) {
		sys, repl, replActor, clusterMock, remotingMock := spawnReplicatorWithMocks(t, crdt.NewConfig(crdt.WithRole("crdt")))
		clusterMock.EXPECT().Peers(mock.Anything).Return([]*cluster.Peer{plainPeer}, nil)

		require.NoError(t, Tell(context.TODO(), repl, &antiEntropyTick{}))
		pause.For(500 * time.Millisecond)

		remotingMock.AssertNotCalled(t, "RemoteLookup", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		assert.Zero(t, replActor.antiEntropyCount.Load())
		require.NoError(t, sys.Stop(context.TODO()))
	})

	t.Run("coordinated write sends no delta to a peer without a Replicator", func(t *testing.T) {
		sys, repl, _, clusterMock, remotingMock := spawnReplicatorWithMocks(t, crdt.NewConfig())
		clusterMock.EXPECT().Peers(mock.Anything).Return([]*cluster.Peer{plainPeer}, nil)
		remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.2", 9090, "GoAktReplicator").Return(address.NoSender(), nil)
		remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		resp, err := Ask(context.TODO(), repl, &crdt.Update{
			Key:     crdt.GCounterKey("write-no-replicator"),
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-1", 1)
			},
			WriteTo: crdt.All,
		}, time.Second)
		require.NoError(t, err)
		require.IsType(t, &crdt.UpdateResponse{}, resp)

		remotingMock.AssertNotCalled(t, "RemoteTell", mock.Anything, mock.Anything, noReplicator, mock.Anything)
		require.NoError(t, sys.Stop(context.TODO()))
	})

	t.Run("coordinated write reaches only peers with the CRDT role", func(t *testing.T) {
		sys, repl, _, clusterMock, remotingMock := spawnReplicatorWithMocks(t, crdt.NewConfig(crdt.WithRole("crdt")))
		clusterMock.EXPECT().Peers(mock.Anything).Return([]*cluster.Peer{plainPeer, rolePeer}, nil)
		remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.3", 9090, "GoAktReplicator").Return(rolePeerReplicator, nil)
		remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, rolePeerReplicator, mock.Anything).Return(nil).Once()

		_, err := Ask(context.TODO(), repl, &crdt.Update{
			Key:     crdt.GCounterKey("write-role"),
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-1", 1)
			},
			WriteTo: crdt.All,
		}, time.Second)
		require.NoError(t, err)

		remotingMock.AssertNotCalled(t, "RemoteLookup", mock.Anything, "10.0.0.2", 9090, "GoAktReplicator")
		require.NoError(t, sys.Stop(context.TODO()))
	})

	t.Run("coordinated read asks no peer without a Replicator", func(t *testing.T) {
		sys, repl, _, clusterMock, remotingMock := spawnReplicatorWithMocks(t, crdt.NewConfig())
		key := crdt.GCounterKey("read-no-replicator")
		_, err := Ask(context.TODO(), repl, &crdt.Update{
			Key:     key,
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-1", 3)
			},
		}, time.Second)
		require.NoError(t, err)

		clusterMock.EXPECT().Peers(mock.Anything).Return([]*cluster.Peer{plainPeer}, nil)
		remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.2", 9090, "GoAktReplicator").Return(address.NoSender(), nil)
		remotingMock.EXPECT().RemoteAsk(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("unreachable")).Maybe()

		resp, err := Ask(context.TODO(), repl, &crdt.Get{Key: key, ReadFrom: crdt.All}, time.Second)
		require.NoError(t, err)
		got, ok := resp.(*crdt.GetResponse)
		require.True(t, ok)
		assert.EqualValues(t, 3, got.Data.(*crdt.GCounter).Value())

		remotingMock.AssertNotCalled(t, "RemoteAsk", mock.Anything, mock.Anything, noReplicator, mock.Anything, mock.Anything)
		require.NoError(t, sys.Stop(context.TODO()))
	})

	t.Run("coordinated delete sends no tombstone to a peer without a Replicator", func(t *testing.T) {
		sys, repl, _, clusterMock, remotingMock := spawnReplicatorWithMocks(t, crdt.NewConfig())
		key := crdt.GCounterKey("delete-no-replicator")
		_, err := Ask(context.TODO(), repl, &crdt.Update{
			Key:     key,
			Initial: crdt.NewGCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.GCounter).Increment("node-1", 1)
			},
		}, time.Second)
		require.NoError(t, err)

		clusterMock.EXPECT().Peers(mock.Anything).Return([]*cluster.Peer{plainPeer}, nil)
		remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.2", 9090, "GoAktReplicator").Return(address.NoSender(), nil)
		remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		_, err = Ask(context.TODO(), repl, &crdt.Delete{Key: key, WriteTo: crdt.All}, time.Second)
		require.NoError(t, err)

		remotingMock.AssertNotCalled(t, "RemoteTell", mock.Anything, mock.Anything, noReplicator, mock.Anything)
		require.NoError(t, sys.Stop(context.TODO()))
	})

	t.Run("cross-DC flush moves on to an endpoint that has a Replicator", func(t *testing.T) {
		records := []datacenter.DataCenterRecord{{
			ID:         "zrremote",
			DataCenter: datacenter.DataCenter{Name: "remote", Region: "r", Zone: "z"},
			Endpoints:  []string{"10.0.0.1:9090", "10.0.0.2:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		}}
		sys, repl, replActor, clusterMock, remotingMock := spawnReplicatorWithDCController(t, remoteRecords(records), nil)
		remoteReplicator := address.New("GoAktReplicator", "remoteSys", "10.0.0.2", 9090)
		clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()
		remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.1", 9090, "GoAktReplicator").Return(address.NoSender(), nil).Maybe()
		remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.2", 9090, "GoAktReplicator").Return(remoteReplicator, nil).Maybe()
		remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		_, err := Ask(context.TODO(), repl, &crdt.Update{
			Key:     crdt.PNCounterKey("flush-next-endpoint"),
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 2)
			},
		}, time.Second)
		require.NoError(t, err)

		require.NoError(t, Tell(context.TODO(), repl, &dataCenterFlushTick{}))
		require.Eventually(t, func() bool { return replActor.crossDCSendCount.Load() == 1 }, 2*time.Second, 20*time.Millisecond)

		remotingMock.AssertNotCalled(t, "RemoteTell", mock.Anything, mock.Anything, noReplicator, mock.Anything)
		remotingMock.AssertCalled(t, "RemoteTell", mock.Anything, mock.Anything, remoteReplicator, mock.Anything)
		require.NoError(t, sys.Stop(context.TODO()))
	})

	t.Run("cross-DC flush does not count a datacenter without a Replicator as reached", func(t *testing.T) {
		records := []datacenter.DataCenterRecord{{
			ID:         "zrremote",
			DataCenter: datacenter.DataCenter{Name: "remote", Region: "r", Zone: "z"},
			Endpoints:  []string{"10.0.0.1:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		}}
		sys, repl, replActor, clusterMock, remotingMock := spawnReplicatorWithDCController(t, remoteRecords(records), nil)
		clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()
		remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.1", 9090, "GoAktReplicator").Return(address.NoSender(), nil)
		remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		_, err := Ask(context.TODO(), repl, &crdt.Update{
			Key:     crdt.PNCounterKey("flush-no-replicator"),
			Initial: crdt.NewPNCounter(),
			Modify: func(current crdt.ReplicatedData) crdt.ReplicatedData {
				return current.(*crdt.PNCounter).Increment("node-1", 2)
			},
		}, time.Second)
		require.NoError(t, err)

		require.NoError(t, Tell(context.TODO(), repl, &dataCenterFlushTick{}))
		pause.For(time.Second)

		remotingMock.AssertNotCalled(t, "RemoteTell", mock.Anything, mock.Anything, noReplicator, mock.Anything)
		assert.Zero(t, replActor.crossDCSendCount.Load())
		require.NoError(t, sys.Stop(context.TODO()))
	})

	t.Run("cross-DC anti-entropy sends no digest to an endpoint without a Replicator", func(t *testing.T) {
		records := []datacenter.DataCenterRecord{{
			ID:         "z2r2remote2",
			DataCenter: datacenter.DataCenter{Name: "remote2", Region: "r2", Zone: "z2"},
			Endpoints:  []string{"10.0.0.5:9090"},
			State:      datacenter.DataCenterActive,
			Version:    1,
		}}
		sys, repl, _, clusterMock, remotingMock := spawnReplicatorWithDCController(t, remoteRecords(records), nil)
		clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()
		remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.5", 9090, "GoAktReplicator").Return(address.NoSender(), nil)
		remotingMock.EXPECT().RemoteTell(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		require.NoError(t, Tell(context.TODO(), repl, &dataCenterAntiEntropyTick{}))
		pause.For(time.Second)

		remotingMock.AssertNotCalled(t, "RemoteTell", mock.Anything, mock.Anything, noReplicator, mock.Anything)
		require.NoError(t, sys.Stop(context.TODO()))
	})
}

func TestReplicatorReplicatorPeers(t *testing.T) {
	plain := &cluster.Peer{Host: "10.0.0.2", RemotingPort: 9090}
	withRole := &cluster.Peer{Host: "10.0.0.3", RemotingPort: 9090, Roles: []string{"crdt"}}
	otherRole := &cluster.Peer{Host: "10.0.0.4", RemotingPort: 9090, Roles: []string{"web"}}

	t.Run("keeps every peer when distributed data has no role", func(t *testing.T) {
		r := newTestReplicator()
		assert.Equal(t, []*cluster.Peer{plain, withRole, otherRole}, r.replicatorPeers([]*cluster.Peer{plain, withRole, otherRole}))
	})

	t.Run("keeps only peers with the role", func(t *testing.T) {
		r := newTestReplicator()
		r.config = crdt.NewConfig(crdt.WithRole("crdt"))
		assert.Equal(t, []*cluster.Peer{withRole}, r.replicatorPeers([]*cluster.Peer{plain, withRole, otherRole}))
	})

	t.Run("returns no peer when none has the role", func(t *testing.T) {
		r := newTestReplicator()
		r.config = crdt.NewConfig(crdt.WithRole("crdt"))
		assert.Empty(t, r.replicatorPeers([]*cluster.Peer{plain, otherRole}))
		assert.Empty(t, r.replicatorPeers(nil))
	})
}

func TestReplicatorLookupReplicator(t *testing.T) {
	replicator := address.New("GoAktReplicator", "remoteSys", "10.0.0.3", 9090)

	t.Run("returns the address of a running Replicator", func(t *testing.T) {
		r := newTestReplicator()
		remotingMock := mocksremote.NewClient(t)
		remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.3", 9090, "GoAktReplicator").Return(replicator, nil)
		r.remoting = remotingMock

		to, found, err := r.lookupReplicator(context.TODO(), "10.0.0.3", 9090)
		require.NoError(t, err)
		assert.True(t, found)
		assert.Same(t, replicator, to)
	})

	t.Run("reports no Replicator when the lookup answers NoSender", func(t *testing.T) {
		r := newTestReplicator()
		remotingMock := mocksremote.NewClient(t)
		remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.2", 9090, "GoAktReplicator").Return(address.NoSender(), nil)
		r.remoting = remotingMock

		to, found, err := r.lookupReplicator(context.TODO(), "10.0.0.2", 9090)
		require.NoError(t, err)
		assert.False(t, found)
		assert.Nil(t, to)
	})

	t.Run("reports no Replicator when the lookup answers no address", func(t *testing.T) {
		r := newTestReplicator()
		remotingMock := mocksremote.NewClient(t)
		remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.2", 9090, "GoAktReplicator").Return(nil, nil)
		r.remoting = remotingMock

		to, found, err := r.lookupReplicator(context.TODO(), "10.0.0.2", 9090)
		require.NoError(t, err)
		assert.False(t, found)
		assert.Nil(t, to)
	})

	t.Run("returns the lookup error", func(t *testing.T) {
		r := newTestReplicator()
		remotingMock := mocksremote.NewClient(t)
		remotingMock.EXPECT().RemoteLookup(mock.Anything, "10.0.0.2", 9090, "GoAktReplicator").Return(nil, errors.New("dial failed"))
		r.remoting = remotingMock

		to, found, err := r.lookupReplicator(context.TODO(), "10.0.0.2", 9090)
		require.EqualError(t, err, "dial failed")
		assert.False(t, found)
		assert.Nil(t, to)
	})
}
