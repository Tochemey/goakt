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
	r.subscriptions = make(map[string]types.Unit)
	r.watchers = make(map[string][]*PID)
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
		reply, err := Ask(ctx, repl, &crdt.Update[*crdt.PNCounter]{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current *crdt.PNCounter) *crdt.PNCounter {
				return current.Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)
		require.NotNil(t, reply)
		assert.IsType(t, &crdt.UpdateResponse{}, reply)

		// get the counter
		resp, err := Ask(ctx, repl, &crdt.Get[*crdt.PNCounter]{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		require.NotNil(t, resp)

		getResp := resp.(*crdt.GetResponse[*crdt.PNCounter])
		require.NotNil(t, getResp.Data)
		assert.Equal(t, int64(5), getResp.Data.Value())

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
		resp, err := Ask(ctx, repl, &crdt.Get[*crdt.PNCounter]{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse[*crdt.PNCounter])
		assert.Nil(t, getResp.Data)

		// update creates the key
		_, err = Ask(ctx, repl, &crdt.Update[*crdt.PNCounter]{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current *crdt.PNCounter) *crdt.PNCounter {
				return current.Increment("node-1", 1)
			},
		}, time.Second)
		require.NoError(t, err)

		// now get returns the value
		resp, err = Ask(ctx, repl, &crdt.Get[*crdt.PNCounter]{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		getResp = resp.(*crdt.GetResponse[*crdt.PNCounter])
		require.NotNil(t, getResp.Data)
		assert.Equal(t, int64(1), getResp.Data.Value())

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
			_, err = Ask(ctx, repl, &crdt.Update[*crdt.PNCounter]{
				Key:     counterKey,
				Initial: crdt.NewPNCounter(),
				Modify: func(current *crdt.PNCounter) *crdt.PNCounter {
					return current.Increment("node-1", uint64(i+1))
				},
			}, time.Second)
			require.NoError(t, err)
		}

		resp, err := Ask(ctx, repl, &crdt.Get[*crdt.PNCounter]{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse[*crdt.PNCounter])
		assert.Equal(t, int64(15), getResp.Data.Value())

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
		_, err = Ask(ctx, repl, &crdt.Update[*crdt.PNCounter]{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current *crdt.PNCounter) *crdt.PNCounter {
				return current.Increment("node-1", 5)
			},
		}, time.Second)
		require.NoError(t, err)

		// delete it
		reply, err := Ask(ctx, repl, &crdt.Delete[*crdt.PNCounter]{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		assert.IsType(t, &crdt.DeleteResponse{}, reply)

		// get returns nil
		resp, err := Ask(ctx, repl, &crdt.Get[*crdt.PNCounter]{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse[*crdt.PNCounter])
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
		_, err = Ask(ctx, repl, &crdt.Update[*crdt.GCounter]{
			Key:     gcKey,
			Initial: crdt.NewGCounter(),
			Modify: func(current *crdt.GCounter) *crdt.GCounter {
				return current.Increment("node-1", 10)
			},
		}, time.Second)
		require.NoError(t, err)

		resp, err := Ask(ctx, repl, &crdt.Get[*crdt.GCounter]{Key: gcKey}, time.Second)
		require.NoError(t, err)
		assert.Equal(t, uint64(10), resp.(*crdt.GetResponse[*crdt.GCounter]).Data.Value())

		// ORSet
		setKey := crdt.ORSetKey[string]("sessions")
		_, err = Ask(ctx, repl, &crdt.Update[*crdt.ORSet[string]]{
			Key:     setKey,
			Initial: crdt.NewORSet[string](),
			Modify: func(current *crdt.ORSet[string]) *crdt.ORSet[string] {
				return current.Add("node-1", "session-abc")
			},
		}, time.Second)
		require.NoError(t, err)

		resp, err = Ask(ctx, repl, &crdt.Get[*crdt.ORSet[string]]{Key: setKey}, time.Second)
		require.NoError(t, err)
		orSet := resp.(*crdt.GetResponse[*crdt.ORSet[string]]).Data
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
		_, err = Ask(ctx, repl, &crdt.Update[*crdt.PNCounter]{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current *crdt.PNCounter) *crdt.PNCounter {
				return current.Increment("node-1", 5)
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
		resp, err := Ask(ctx, repl, &crdt.Get[*crdt.PNCounter]{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse[*crdt.PNCounter])
		assert.Equal(t, int64(15), getResp.Data.Value())

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
		_, err = Ask(ctx, repl, &crdt.Update[*crdt.PNCounter]{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current *crdt.PNCounter) *crdt.PNCounter {
				return current.Increment("node-1", 5)
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
		resp, err := Ask(ctx, repl, &crdt.Get[*crdt.PNCounter]{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse[*crdt.PNCounter])
		assert.Equal(t, int64(5), getResp.Data.Value())

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
		resp, err := Ask(ctx, repl, &crdt.Get[*crdt.PNCounter]{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse[*crdt.PNCounter])
		require.NotNil(t, getResp.Data)
		assert.Equal(t, int64(7), getResp.Data.Value())

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
		err = Tell(ctx, repl, &crdt.Update[*crdt.PNCounter]{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current *crdt.PNCounter) *crdt.PNCounter {
				return current.Increment("node-1", 3)
			},
		})
		require.NoError(t, err)
		pause.For(500 * time.Millisecond)

		resp, err := Ask(ctx, repl, &crdt.Get[*crdt.PNCounter]{
			Key: counterKey,
		}, time.Second)
		require.NoError(t, err)
		getResp := resp.(*crdt.GetResponse[*crdt.PNCounter])
		assert.Equal(t, int64(3), getResp.Data.Value())

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
		r.trackKey("test-key")
		_, exists := r.subscriptions["test-key"]
		assert.True(t, exists)
	})

	t.Run("duplicate is idempotent", func(t *testing.T) {
		r := newTestReplicator()
		r.trackKey("test-key")
		r.trackKey("test-key")
		assert.Len(t, r.subscriptions, 1)
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
func getPNCounter(t *testing.T, repl *PID, key crdt.Key[*crdt.PNCounter]) *crdt.PNCounter {
	t.Helper()
	ctx := context.TODO()
	resp, err := Ask(ctx, repl, &crdt.Get[*crdt.PNCounter]{Key: key}, time.Second)
	require.NoError(t, err)
	return resp.(*crdt.GetResponse[*crdt.PNCounter]).Data
}

// getORSet reads an ORSet[string] from a node's replicator.
func getORSet(t *testing.T, repl *PID, key crdt.Key[*crdt.ORSet[string]]) *crdt.ORSet[string] {
	t.Helper()
	ctx := context.TODO()
	resp, err := Ask(ctx, repl, &crdt.Get[*crdt.ORSet[string]]{Key: key}, time.Second)
	require.NoError(t, err)
	return resp.(*crdt.GetResponse[*crdt.ORSet[string]]).Data
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

		resp, err := Ask(ctx, c.repls[0], &crdt.Update[*crdt.PNCounter]{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current *crdt.PNCounter) *crdt.PNCounter {
				return current.Increment("node-1", 10)
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
		_, err := Ask(ctx, c.repls[0], &crdt.Update[*crdt.PNCounter]{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current *crdt.PNCounter) *crdt.PNCounter {
				return current.Increment("node-1", 7)
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
			_, err := Ask(ctx, repl, &crdt.Update[*crdt.PNCounter]{
				Key:     counterKey,
				Initial: crdt.NewPNCounter(),
				Modify: func(current *crdt.PNCounter) *crdt.PNCounter {
					return current.Increment(nodeID, value)
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
		setKey := crdt.ORSetKey[string]("active-sessions")

		// each node adds its own session
		for i, repl := range c.repls {
			nodeID := fmt.Sprintf("node-%d", i+1)
			session := fmt.Sprintf("session-%c", 'a'+i) // session-a, session-b, session-c
			_, err := Ask(ctx, repl, &crdt.Update[*crdt.ORSet[string]]{
				Key:     setKey,
				Initial: crdt.NewORSet[string](),
				Modify: func(current *crdt.ORSet[string]) *crdt.ORSet[string] {
					return current.Add(nodeID, session)
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
			_, err := Ask(ctx, c.repls[0], &crdt.Update[*crdt.PNCounter]{
				Key:     counterKey,
				Initial: crdt.NewPNCounter(),
				Modify: func(current *crdt.PNCounter) *crdt.PNCounter {
					return current.Increment("node-1", uint64(i+1))
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
		_, err := Ask(ctx, c.repls[0], &crdt.Update[*crdt.PNCounter]{
			Key:     counterKey,
			Initial: crdt.NewPNCounter(),
			Modify: func(current *crdt.PNCounter) *crdt.PNCounter {
				return current.Increment("node-1", 42)
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
		_, err = Ask(ctx, c.repls[0], &crdt.Delete[*crdt.PNCounter]{Key: counterKey}, time.Second)
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
			_, err := Ask(ctx, repl, &crdt.Update[*crdt.GCounter]{
				Key:     gcKey,
				Initial: crdt.NewGCounter(),
				Modify: func(current *crdt.GCounter) *crdt.GCounter {
					return current.Increment(nodeID, uint64(i+1))
				},
			}, time.Second)
			require.NoError(t, err)
		}

		// wait for replication
		pause.For(3 * time.Second)

		// all nodes should converge to 6 (1+2+3)
		for i, repl := range c.repls {
			resp, err := Ask(ctx, repl, &crdt.Get[*crdt.GCounter]{Key: gcKey}, time.Second)
			require.NoError(t, err)
			data := resp.(*crdt.GetResponse[*crdt.GCounter]).Data
			require.NotNil(t, data, "node %d should have the GCounter", i+1)
			assert.Equal(t, uint64(6), data.Value(), "node %d should converge to 6", i+1)
		}
	})
}
