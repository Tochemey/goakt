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
	stdErrors "errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v4/eventstream"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	mockscluster "github.com/tochemey/goakt/v4/mocks/cluster"
	mocksremote "github.com/tochemey/goakt/v4/mocks/remoteclient"
	"github.com/tochemey/goakt/v4/remote"
)

type spawnSingletonSpy struct {
	*actorSystem
	called    bool
	actorName string
	actor     Actor
	config    *clusterSingletonConfig
}

func (s *spawnSingletonSpy) SpawnSingleton(ctx context.Context, name string, actor Actor, opts ...ClusterSingletonOption) (*PID, error) {
	s.called = true
	s.actorName = name
	s.actor = actor
	s.config = newClusterSingletonConfig(opts...)
	return nil, nil
}

// collectRelocationFailedEvents drains the consumer and returns the
// RelocationFailed events it received.
func collectRelocationFailedEvents(consumer eventstream.Subscriber) []*RelocationFailed {
	var events []*RelocationFailed
	for message := range consumer.Iterator() {
		if event, ok := message.Payload().(*RelocationFailed); ok {
			events = append(events, event)
		}
	}
	return events
}

// TestRelocationWorkerPeersError verifies that a failure to fetch the cluster
// peers aborts the whole relocation: every actor and grain of the departed node
// is reported in a RelocationFailed event, the peer state snapshot is removed,
// and the relocation job is released.
func TestRelocationWorkerPeersError(t *testing.T) {
	ctx := context.Background()

	system, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	sys := system.(*actorSystem)

	clusterMock := mockscluster.NewCluster(t)
	expectedErr := stdErrors.New("cluster failure")
	clusterMock.EXPECT().Peers(mock.Anything).Return(nil, expectedErr).Once()

	store := &recordingPeerStateStore{}
	sys.cluster = clusterMock
	sys.clusterStore = store
	sys.relocationEnabled.Store(true)

	peerState := &internalpb.PeerState{
		Host:         "127.0.0.1",
		PeersPort:    9000,
		RemotingPort: 8080,
		Actors: map[string]*internalpb.Actor{
			"a1": {Address: "actor-1"},
		},
	}
	require.True(t, sys.beginRelocation("127.0.0.1:9000", peerState))

	stream := eventstream.New()
	consumer := stream.AddSubscriber()
	stream.Subscribe(consumer, eventsTopic)

	worker := &relocationWorker{
		remoting: remoteclient.NewClient(),
		pid: &PID{
			actorSystem:  system,
			logger:       log.DiscardLogger,
			eventsStream: stream,
		},
		logger: log.DiscardLogger,
	}

	receiveCtx := newReceiveContext(ctx, nil, worker.pid, &internalpb.Rebalance{PeerState: peerState})
	worker.relocate(receiveCtx, peerState)

	// the job is released and the snapshot removed even on abort
	_, inflight := sys.relocationJob("127.0.0.1:9000")
	require.False(t, inflight)
	require.True(t, store.deleteCalled)
	assert.Equal(t, "127.0.0.1:9000", store.deletedAddr)

	events := collectRelocationFailedEvents(consumer)
	require.Len(t, events, 1)
	assert.Equal(t, "127.0.0.1:9000", events[0].Address())
	assert.Equal(t, []string{"actor-1"}, events[0].Actors())
	require.ErrorContains(t, events[0].Error(), expectedErr.Error())
}

// TestRelocationWorkerPartialFailureListsExactlyFailedItems verifies per-item
// failure isolation: a failing actor does not abort the rebalance, skipped
// items are not reported, and the RelocationFailed event lists exactly the
// items that could not be relocated.
func TestRelocationWorkerPartialFailureListsExactlyFailedItems(t *testing.T) {
	ctx := context.Background()

	system, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	sys := system.(*actorSystem)

	clusterMock := mockscluster.NewCluster(t)
	// single-node cluster: no peers, so every relocated actor lands on the leader.
	// The non-relocatable actor is skipped before any registry access, so the
	// strict mock carries no GetActor/RemoveActor expectations for it.
	clusterMock.EXPECT().Peers(mock.Anything).Return(nil, nil).Once()
	// the leader tallies actors per node once to seed load-aware placement
	clusterMock.EXPECT().CountActorsByHost(mock.Anything, mock.Anything).Return(nil, nil).Once()

	store := &recordingPeerStateStore{}
	sys.cluster = clusterMock
	sys.clusterStore = store
	sys.relocationEnabled.Store(true)
	sys.registry.Register(new(MockActor))

	peerState := &internalpb.PeerState{
		Host:         "127.0.0.1",
		PeersPort:    9000,
		RemotingPort: 8080,
		Actors: map[string]*internalpb.Actor{
			"bad": {Address: "invalid-address", Relocatable: true},
			"skipped": {
				Address:     address.New("skipped", "test", "127.0.0.1", 8080).String(),
				Type:        types.Name(new(MockActor)),
				Relocatable: false,
			},
		},
	}
	require.True(t, sys.beginRelocation("127.0.0.1:9000", peerState))

	stream := eventstream.New()
	consumer := stream.AddSubscriber()
	stream.Subscribe(consumer, eventsTopic)

	worker := &relocationWorker{
		remoting: remoteclient.NewClient(),
		pid: &PID{
			actorSystem:  system,
			logger:       log.DiscardLogger,
			eventsStream: stream,
		},
		logger: log.DiscardLogger,
	}

	receiveCtx := newReceiveContext(ctx, nil, worker.pid, &internalpb.Rebalance{PeerState: peerState})
	worker.relocate(receiveCtx, peerState)

	// the rebalance completed: job released and snapshot removed
	_, inflight := sys.relocationJob("127.0.0.1:9000")
	require.False(t, inflight)
	require.True(t, store.deleteCalled)

	// the event lists exactly the failed actor, not the skipped one
	events := collectRelocationFailedEvents(consumer)
	require.Len(t, events, 1)
	assert.Equal(t, []string{"invalid-address"}, events[0].Actors())
	assert.Empty(t, events[0].Grains())
}

// TestRelocationWorkerReleasesLazyGrains verifies that grains relocate lazily
// by default: the worker cleans their directory entry (so they re-activate on
// next use) instead of recreating them, and reports no failure.
func TestRelocationWorkerReleasesLazyGrains(t *testing.T) {
	ctx := context.Background()

	system, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	sys := system.(*actorSystem)

	clusterMock := mockscluster.NewCluster(t)
	// single-node cluster: no peers. With no actors to place, the load scan
	// (CountActorsByHost) is skipped entirely.
	clusterMock.EXPECT().Peers(mock.Anything).Return(nil, nil).Once()
	// the lazy grain's directory entry still points at the departed node, so it
	// is released rather than recreated
	clusterMock.EXPECT().GetGrain(mock.Anything, "kind/lazy").
		Return(&internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: "kind/lazy"},
			Host:    "127.0.0.1",
			Port:    8080,
		}, nil).Once()
	clusterMock.EXPECT().RemoveGrain(mock.Anything, "kind/lazy").Return(nil).Once()

	store := &recordingPeerStateStore{}
	sys.cluster = clusterMock
	sys.clusterStore = store
	sys.relocationEnabled.Store(true)

	peerState := &internalpb.PeerState{
		Host:         "127.0.0.1",
		PeersPort:    9000,
		RemotingPort: 8080,
		Grains: map[string]*internalpb.Grain{
			"lazy": {GrainId: &internalpb.GrainId{Kind: "kind", Name: "lazy", Value: "kind/lazy"}},
		},
	}
	require.True(t, sys.beginRelocation("127.0.0.1:9000", peerState))

	stream := eventstream.New()
	consumer := stream.AddSubscriber()
	stream.Subscribe(consumer, eventsTopic)

	worker := &relocationWorker{
		remoting: remoteclient.NewClient(),
		pid: &PID{
			actorSystem:  system,
			logger:       log.DiscardLogger,
			eventsStream: stream,
		},
		logger: log.DiscardLogger,
	}

	receiveCtx := newReceiveContext(ctx, nil, worker.pid, &internalpb.Rebalance{PeerState: peerState})
	worker.relocate(receiveCtx, peerState)

	_, inflight := sys.relocationJob("127.0.0.1:9000")
	require.False(t, inflight)
	require.True(t, store.deleteCalled)

	// releasing a lazy grain is not an item loss, so no RelocationFailed event
	require.Empty(t, collectRelocationFailedEvents(consumer))
}

// TestRelocationWorkerDistributesLazyGrainsToPeers verifies lazy grains are
// allocated across the peers like any other item instead of being released
// entirely by the worker's node: with one peer and two lazy grains, the worker
// releases one locally and hands the other to the peer in a RelocateBatch
// request.
func TestRelocationWorkerDistributesLazyGrainsToPeers(t *testing.T) {
	ctx := context.Background()

	system, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	sys := system.(*actorSystem)

	peer := &cluster.Peer{Host: "127.0.0.2", RemotingPort: 9002}
	clusterMock := mockscluster.NewCluster(t)
	clusterMock.EXPECT().Peers(mock.Anything).Return([]*cluster.Peer{peer}, nil).Once()
	// with no actors to place, the load scan (CountActorsByHost) is skipped
	// entirely.
	// exactly one grain is released locally (the leader's share); which of the
	// two it is depends on map iteration order
	clusterMock.EXPECT().GetGrain(mock.Anything, mock.Anything).
		Return(&internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: "kind/lazy"},
			Host:    "127.0.0.1",
			Port:    8080,
		}, nil).Once()
	clusterMock.EXPECT().RemoveGrain(mock.Anything, mock.Anything).Return(nil).Once()

	// the other grain travels to the peer inside a RelocateBatch request
	var batched []*internalpb.Grain
	remotingMock := mocksremote.NewClient(t)
	remotingMock.EXPECT().RelocateBatch(mock.Anything, "127.0.0.2", 9002, mock.Anything).
		RunAndReturn(func(_ context.Context, _ string, _ int, request *internalpb.RelocateBatchRequest) (*internalpb.RelocateBatchResponse, error) {
			batched = append(batched, request.GetGrains()...)
			return new(internalpb.RelocateBatchResponse), nil
		}).Once()

	store := &recordingPeerStateStore{}
	sys.cluster = clusterMock
	sys.clusterStore = store
	sys.relocationEnabled.Store(true)

	peerState := &internalpb.PeerState{
		Host:         "127.0.0.1",
		PeersPort:    9000,
		RemotingPort: 8080,
		Grains: map[string]*internalpb.Grain{
			"lazy-1": {GrainId: &internalpb.GrainId{Kind: "kind", Name: "lazy-1", Value: "kind/lazy-1"}},
			"lazy-2": {GrainId: &internalpb.GrainId{Kind: "kind", Name: "lazy-2", Value: "kind/lazy-2"}},
		},
	}
	require.True(t, sys.beginRelocation("127.0.0.1:9000", peerState))

	stream := eventstream.New()
	consumer := stream.AddSubscriber()
	stream.Subscribe(consumer, eventsTopic)

	worker := &relocationWorker{
		remoting: remotingMock,
		pid: &PID{
			actorSystem:  system,
			logger:       log.DiscardLogger,
			eventsStream: stream,
		},
		logger: log.DiscardLogger,
	}

	receiveCtx := newReceiveContext(ctx, nil, worker.pid, &internalpb.Rebalance{PeerState: peerState})
	worker.relocate(receiveCtx, peerState)

	// the peer received exactly one lazy grain
	require.Len(t, batched, 1)
	assert.False(t, batched[0].GetEagerRelocation())

	_, inflight := sys.relocationJob("127.0.0.1:9000")
	require.False(t, inflight)
	require.True(t, store.deleteCalled)
	require.Empty(t, collectRelocationFailedEvents(consumer))
}

// TestRelocationWorkerReassignsShareOnPeerFailure verifies that a share whose
// target peer is unreachable (after the bounded retries) is moved once to the
// next surviving peer instead of being reported as failed.
func TestRelocationWorkerReassignsShareOnPeerFailure(t *testing.T) {
	ctx := context.Background()

	remotingMock := mocksremote.NewClient(t)
	transportErr := stdErrors.New("connection refused")
	remotingMock.EXPECT().RelocateBatch(mock.Anything, "127.0.0.1", 9001, mock.Anything).
		Return(nil, transportErr).Times(relocationBatchMaxAttempts)
	remotingMock.EXPECT().RelocateBatch(mock.Anything, "127.0.0.2", 9002, mock.Anything).
		Return(new(internalpb.RelocateBatchResponse), nil).Once()

	worker := &relocationWorker{remoting: remotingMock, logger: log.DiscardLogger}
	peers := []*cluster.Peer{
		{Host: "127.0.0.1", RemotingPort: 9001},
		{Host: "127.0.0.2", RemotingPort: 9002},
	}
	requests := buildRelocateBatchRequests("127.0.0.1:8080", []*internalpb.Actor{{Address: "actor-1"}}, nil)
	failures := &relocationFailures{}

	worker.relocateShare(ctx, requests, peers[0], peers, failures)

	require.Empty(t, failures.items())
}

// TestRelocationWorkerSkipsSurvivorWithEmptyShare verifies that when the failed
// target's share is redistributed, a survivor that receives neither actors nor
// grains is skipped (no batch is sent to it).
func TestRelocationWorkerSkipsSurvivorWithEmptyShare(t *testing.T) {
	ctx := context.Background()

	gpu := "gpu"
	transportErr := stdErrors.New("connection refused")
	remotingMock := mocksremote.NewClient(t)
	// the original target fails all attempts, forcing redistribution
	remotingMock.EXPECT().RelocateBatch(mock.Anything, "127.0.0.1", 9001, mock.Anything).
		Return(nil, transportErr).Times(relocationBatchMaxAttempts)
	// survivor 0 (no roles) receives the grains
	remotingMock.EXPECT().RelocateBatch(mock.Anything, "127.0.0.2", 9002, mock.Anything).
		Return(new(internalpb.RelocateBatchResponse), nil).Once()
	// survivor 1 (gpu) receives the gpu actor
	remotingMock.EXPECT().RelocateBatch(mock.Anything, "127.0.0.3", 9003, mock.Anything).
		Return(new(internalpb.RelocateBatchResponse), nil).Once()
	// survivor 2 (no roles) gets neither actor nor grain: no call is expected

	worker := &relocationWorker{remoting: remotingMock, logger: log.DiscardLogger}
	peers := []*cluster.Peer{
		{Host: "127.0.0.1", RemotingPort: 9001},
		{Host: "127.0.0.2", RemotingPort: 9002},
		{Host: "127.0.0.3", RemotingPort: 9003, Roles: []string{"gpu"}},
		{Host: "127.0.0.4", RemotingPort: 9004},
	}
	requests := buildRelocateBatchRequests("127.0.0.1:8080",
		[]*internalpb.Actor{{Address: address.New("gpu-actor", "test", "127.0.0.1", 8080).String(), Role: &gpu}},
		[]*internalpb.Grain{{GrainId: &internalpb.GrainId{Value: "grain-1"}, EagerRelocation: true}})
	failures := &relocationFailures{}

	worker.relocateShare(ctx, requests, peers[0], peers, failures)

	require.Empty(t, failures.items())
}

// TestRelocationWorkerShareFailsWithoutFallbackPeer verifies that when the
// target peer is unreachable and no other peer survives, every item of the
// share is reported as failed instead of aborting anything else, except lazy
// grains: their unsent release is a cleanup optimization that self-heals on
// next activation, not an item loss.
func TestRelocationWorkerShareFailsWithoutFallbackPeer(t *testing.T) {
	ctx := context.Background()

	remotingMock := mocksremote.NewClient(t)
	transportErr := stdErrors.New("connection refused")
	remotingMock.EXPECT().RelocateBatch(mock.Anything, "127.0.0.1", 9001, mock.Anything).
		Return(nil, transportErr).Times(relocationBatchMaxAttempts)

	worker := &relocationWorker{remoting: remotingMock, logger: log.DiscardLogger}
	peers := []*cluster.Peer{{Host: "127.0.0.1", RemotingPort: 9001}}
	requests := buildRelocateBatchRequests("127.0.0.1:8080",
		[]*internalpb.Actor{{Address: "actor-1"}},
		[]*internalpb.Grain{
			{GrainId: &internalpb.GrainId{Value: "grain-1"}, EagerRelocation: true},
			{GrainId: &internalpb.GrainId{Value: "grain-lazy"}},
		})
	failures := &relocationFailures{}

	worker.relocateShare(ctx, requests, peers[0], peers, failures)

	items := failures.items()
	require.Len(t, items, 2)

	actors, grains := splitFailures(items)
	assert.Equal(t, []string{"actor-1"}, actors)
	// the eager grain is reported, the lazy grain is not
	assert.Equal(t, []string{"grain-1"}, grains)

	for _, item := range items {
		assert.Contains(t, item.GetMessage(), transportErr.Error())
	}
}

// TestRelocationWorkerReleasesUndeliverableLazyGrains verifies that when a
// share cannot be delivered to any peer, the leader takes it locally through
// the shared dispatch rule: the lazy grain's stale directory entry is removed
// (so the grain is not left permanently unreachable; the TellGrain/AskGrain
// fast path does not self-heal a stale owner) and the eager grain is handled
// by recreateGrainFromWire, which here observes an entry already re-owned by
// another live node and skips it. Neither grain is a failure.
func TestRelocationWorkerReleasesUndeliverableLazyGrains(t *testing.T) {
	ctx := context.Background()

	system, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	sys := system.(*actorSystem)

	transportErr := stdErrors.New("connection refused")
	remotingMock := mocksremote.NewClient(t)
	remotingMock.EXPECT().RelocateBatch(mock.Anything, "127.0.0.1", 9001, mock.Anything).
		Return(nil, transportErr).Times(relocationBatchMaxAttempts)

	clusterMock := mockscluster.NewCluster(t)
	// the leader releases the undeliverable lazy grain's stale directory entry
	clusterMock.EXPECT().GetGrain(mock.Anything, "kind/lazy").
		Return(&internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: "kind/lazy"},
			Host:    "127.0.0.1",
			Port:    8080,
		}, nil).Once()
	clusterMock.EXPECT().RemoveGrain(mock.Anything, "kind/lazy").Return(nil).Once()
	// the eager grain's entry already points at another live node, so the
	// leader-local dispatch skips reactivating it
	clusterMock.EXPECT().GetGrain(mock.Anything, "kind/eager").
		Return(&internalpb.Grain{
			GrainId: &internalpb.GrainId{Value: "kind/eager"},
			Host:    "127.0.0.2",
			Port:    9002,
		}, nil).Once()
	sys.cluster = clusterMock

	worker := &relocationWorker{
		remoting: remotingMock,
		pid:      &PID{actorSystem: system, logger: log.DiscardLogger},
		logger:   log.DiscardLogger,
	}
	peers := []*cluster.Peer{{Host: "127.0.0.1", RemotingPort: 9001}}
	requests := buildRelocateBatchRequests("127.0.0.1:8080", nil,
		[]*internalpb.Grain{
			{GrainId: &internalpb.GrainId{Kind: "kind", Name: "lazy", Value: "kind/lazy"}},
			{GrainId: &internalpb.GrainId{Kind: "kind", Name: "eager", Value: "kind/eager"}, EagerRelocation: true},
		})
	failures := &relocationFailures{}

	worker.relocateShare(ctx, requests, peers[0], peers, failures)

	assert.Empty(t, failures.items())
}

// TestRelocationWorkerReportsFailedUndeliverableLazyRelease verifies that when
// an undeliverable lazy grain's directory release also fails, the grain is
// reported as a relocation failure rather than being silently stranded pointing
// at the dead node.
func TestRelocationWorkerReportsFailedUndeliverableLazyRelease(t *testing.T) {
	ctx := context.Background()

	system, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	sys := system.(*actorSystem)

	transportErr := stdErrors.New("connection refused")
	remotingMock := mocksremote.NewClient(t)
	remotingMock.EXPECT().RelocateBatch(mock.Anything, "127.0.0.1", 9001, mock.Anything).
		Return(nil, transportErr).Times(relocationBatchMaxAttempts)

	clusterMock := mockscluster.NewCluster(t)
	clusterMock.EXPECT().GetGrain(mock.Anything, "kind/lazy").
		Return(&internalpb.Grain{GrainId: &internalpb.GrainId{Value: "kind/lazy"}, Host: "127.0.0.1", Port: 8080}, nil).Once()
	clusterMock.EXPECT().RemoveGrain(mock.Anything, "kind/lazy").Return(stdErrors.New("store down")).Once()
	sys.cluster = clusterMock

	worker := &relocationWorker{
		remoting: remotingMock,
		pid:      &PID{actorSystem: system, logger: log.DiscardLogger},
		logger:   log.DiscardLogger,
	}
	peers := []*cluster.Peer{{Host: "127.0.0.1", RemotingPort: 9001}}
	requests := buildRelocateBatchRequests("127.0.0.1:8080", nil,
		[]*internalpb.Grain{{GrainId: &internalpb.GrainId{Kind: "kind", Name: "lazy", Value: "kind/lazy"}}})
	failures := &relocationFailures{}

	worker.relocateShare(ctx, requests, peers[0], peers, failures)

	items := failures.items()
	require.Len(t, items, 1)
	assert.True(t, items[0].GetGrain())
	assert.Equal(t, "kind/lazy", items[0].GetId())
}

// TestRelocationWorkerMergesRemoteItemFailures verifies that per-item failures
// reported by the target peer are collected.
func TestRelocationWorkerMergesRemoteItemFailures(t *testing.T) {
	ctx := context.Background()

	remoteFailure := &internalpb.RelocationFailure{Id: "actor-9", Grain: false, Message: "spawn boom"}
	remotingMock := mocksremote.NewClient(t)
	remotingMock.EXPECT().RelocateBatch(mock.Anything, "127.0.0.1", 9001, mock.Anything).
		Return(&internalpb.RelocateBatchResponse{Failures: []*internalpb.RelocationFailure{remoteFailure}}, nil).Once()

	worker := &relocationWorker{remoting: remotingMock, logger: log.DiscardLogger}
	peers := []*cluster.Peer{{Host: "127.0.0.1", RemotingPort: 9001}}
	requests := buildRelocateBatchRequests("127.0.0.1:8080", []*internalpb.Actor{{Address: "actor-9"}}, nil)
	failures := &relocationFailures{}

	worker.relocateShare(ctx, requests, peers[0], peers, failures)

	items := failures.items()
	require.Len(t, items, 1)
	assert.Equal(t, "actor-9", items[0].GetId())
	assert.Equal(t, "spawn boom", items[0].GetMessage())
}

// TestRelocationWorkerToleratesDeletePeerStateError verifies that a failure to
// delete the departed node's peer state does not prevent the worker from
// releasing the relocation job.
func TestRelocationWorkerToleratesDeletePeerStateError(t *testing.T) {
	ctx := context.Background()

	system, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	sys := system.(*actorSystem)
	store := &recordingPeerStateStore{deleteErr: stdErrors.New("store down")}
	sys.clusterStore = store

	require.True(t, sys.beginRelocation("127.0.0.1:9000", new(internalpb.PeerState)))

	worker := &relocationWorker{
		pid:    &PID{actorSystem: system, logger: log.DiscardLogger},
		logger: log.DiscardLogger,
	}

	worker.finish(ctx, "127.0.0.1:9000")

	require.True(t, store.deleteCalled)
	_, inflight := sys.relocationJob("127.0.0.1:9000")
	require.False(t, inflight)
}

// TestBuildRelocateBatchRequestsChunksLargeShares verifies a share larger than
// defaultRelocationBatchSize is split into multiple requests, each carrying the
// departed node address.
func TestBuildRelocateBatchRequestsChunksLargeShares(t *testing.T) {
	actors := make([]*internalpb.Actor, defaultRelocationBatchSize+1)
	for i := range actors {
		actors[i] = &internalpb.Actor{Address: fmt.Sprintf("actor-%d", i)}
	}
	grains := []*internalpb.Grain{{GrainId: &internalpb.GrainId{Value: "grain-1"}}}

	requests := buildRelocateBatchRequests("127.0.0.1:8080", actors, grains)

	require.Len(t, requests, 3)
	assert.Len(t, requests[0].GetActors(), defaultRelocationBatchSize)
	assert.Len(t, requests[1].GetActors(), 1)
	assert.Len(t, requests[2].GetGrains(), 1)

	for _, request := range requests {
		assert.Equal(t, "127.0.0.1:8080", request.GetDepartedNode())
	}
}

// TestReassignByRole verifies the role-aware redistribution used when a target
// becomes unreachable: each actor is placed on the first survivor advertising
// its role (role-less actors land on the first survivor), grains are flattened
// out, and an actor no survivor can host is recorded once with an accurate
// message instead of being blamed on a single ring-neighbor.
func TestReassignByRole(t *testing.T) {
	t.Run("distributes across survivors and records the truly unplaceable", testReassignByRoleDistribution)
	t.Run("falls back to the leader for actors only it can host", testReassignByRoleLeaderFallback)
	t.Run("spreads role-less actors across eligible survivors", testReassignByRoleSpreadsLoad)
}

func testReassignByRoleSpreadsLoad(t *testing.T) {
	actors := make([]*internalpb.Actor, 6)
	for i := range actors {
		actors[i] = &internalpb.Actor{Address: address.New(fmt.Sprintf("actor-%d", i), "test", "127.0.0.9", 7000).String()}
	}
	requests := []*internalpb.RelocateBatchRequest{{DepartedNode: "127.0.0.9:7000", Actors: actors}}

	survivors := []*cluster.Peer{
		{Host: "10.0.0.1", RemotingPort: 1},
		{Host: "10.0.0.2", RemotingPort: 2},
		{Host: "10.0.0.3", RemotingPort: 3},
	}

	failures := &relocationFailures{}
	actorShares, leaderActors, _ := reassignByRole(requests, survivors, nil, failures)

	// role-less actors must spread evenly instead of piling onto survivors[0]
	require.Len(t, actorShares, 3)
	for i := range actorShares {
		assert.Len(t, actorShares[i], 2, "survivor %d must receive an even share", i)
	}

	assert.Empty(t, leaderActors)
	assert.Empty(t, failures.items())
}

func testReassignByRoleDistribution(t *testing.T) {
	gpu := "gpu"
	blue := "blue"
	missing := "missing"
	requests := []*internalpb.RelocateBatchRequest{
		{
			DepartedNode: "127.0.0.9:7000",
			Actors: []*internalpb.Actor{
				{Address: address.New("no-role", "test", "127.0.0.9", 7000).String()},
				{Address: address.New("gpu-actor", "test", "127.0.0.9", 7000).String(), Role: &gpu},
				{Address: address.New("blue-actor", "test", "127.0.0.9", 7000).String(), Role: &blue},
			},
			Grains: []*internalpb.Grain{{GrainId: &internalpb.GrainId{Value: "grain-1"}}},
		},
		{
			DepartedNode: "127.0.0.9:7000",
			Actors: []*internalpb.Actor{
				{Address: address.New("unplaceable", "test", "127.0.0.9", 7000).String(), Role: &missing},
			},
		},
	}

	// survivor 0 advertises no role (hosts role-less), survivor 1 advertises gpu,
	// survivor 2 advertises blue; none advertises "missing".
	survivors := []*cluster.Peer{
		{Host: "10.0.0.1", RemotingPort: 1},
		{Host: "10.0.0.2", RemotingPort: 2, Roles: []string{"gpu"}},
		{Host: "10.0.0.3", RemotingPort: 3, Roles: []string{"blue"}},
	}

	failures := &relocationFailures{}
	actorShares, leaderActors, grains := reassignByRole(requests, survivors, nil, failures)

	require.Len(t, actorShares, 3)
	// role-less actor lands on the first survivor
	require.Len(t, actorShares[0], 1)
	assert.Contains(t, actorShares[0][0].GetAddress(), "no-role")
	// gpu actor lands on the gpu survivor
	require.Len(t, actorShares[1], 1)
	assert.Contains(t, actorShares[1][0].GetAddress(), "gpu-actor")
	// blue actor lands on the blue survivor
	require.Len(t, actorShares[2], 1)
	assert.Contains(t, actorShares[2][0].GetAddress(), "blue-actor")

	// a role-less leader takes nothing while survivors can host every actor
	assert.Empty(t, leaderActors)

	// grains are flattened out for the caller to place
	require.Len(t, grains, 1)

	// the actor requiring a role no survivor advertises is recorded once
	items := failures.items()
	require.Len(t, items, 1)
	assert.False(t, items[0].GetGrain())
	assert.Contains(t, items[0].GetId(), "unplaceable")
	assert.Contains(t, items[0].GetMessage(), "missing")
}

func testReassignByRoleLeaderFallback(t *testing.T) {
	gpu := "gpu"
	missing := "missing"
	requests := []*internalpb.RelocateBatchRequest{
		{
			DepartedNode: "127.0.0.9:7000",
			Actors: []*internalpb.Actor{
				{Address: address.New("no-role", "test", "127.0.0.9", 7000).String()},
				{Address: address.New("gpu-actor", "test", "127.0.0.9", 7000).String(), Role: &gpu},
				{Address: address.New("unplaceable", "test", "127.0.0.9", 7000).String(), Role: &missing},
			},
		},
	}

	// no survivor advertises gpu, but the leader does: the gpu actor must be
	// recovered locally instead of being reported as unplaceable
	survivors := []*cluster.Peer{{Host: "10.0.0.1", RemotingPort: 1}}

	failures := &relocationFailures{}
	actorShares, leaderActors, grains := reassignByRole(requests, survivors, []string{"gpu"}, failures)

	require.Len(t, actorShares, 1)
	require.Len(t, actorShares[0], 1)
	assert.Contains(t, actorShares[0][0].GetAddress(), "no-role")

	require.Len(t, leaderActors, 1)
	assert.Contains(t, leaderActors[0].GetAddress(), "gpu-actor")

	assert.Empty(t, grains)

	// only the actor no surviving node (leader included) can host is a failure
	items := failures.items()
	require.Len(t, items, 1)
	assert.Contains(t, items[0].GetId(), "unplaceable")

	// with no survivors at all, every leader-eligible actor goes local
	failures = &relocationFailures{}
	actorShares, leaderActors, _ = reassignByRole(requests, nil, []string{"gpu"}, failures)
	assert.Empty(t, actorShares)
	require.Len(t, leaderActors, 2)
	require.Len(t, failures.items(), 1)
}

// TestSurvivingPeersExcept verifies the failed target is removed from the peer
// set while order is preserved.
func TestSurvivingPeersExcept(t *testing.T) {
	peer1 := &cluster.Peer{Host: "10.0.0.1", RemotingPort: 1}
	peer2 := &cluster.Peer{Host: "10.0.0.2", RemotingPort: 2}
	peer3 := &cluster.Peer{Host: "10.0.0.3", RemotingPort: 3}
	peers := []*cluster.Peer{peer1, peer2, peer3}

	survivors := survivingPeersExcept(peers, peer2)
	require.Len(t, survivors, 2)
	assert.Equal(t, peer1, survivors[0])
	assert.Equal(t, peer3, survivors[1])

	// a target not in the set leaves every peer surviving
	survivors = survivingPeersExcept(peers, &cluster.Peer{Host: "10.9.9.9", RemotingPort: 9})
	assert.Len(t, survivors, 3)
}

// TestReportAbortedRelocation verifies the shared abort accounting used by both
// the worker's Peers() abort and the relocator's abortRelocation: every
// relocatable actor and every eager grain is reported as failed, lazy grains
// have their stale directory entry released locally (and are not reported when
// the release succeeds), and relocation-disabled grains are excluded entirely.
func TestReportAbortedRelocation(t *testing.T) {
	const (
		host     = "127.0.0.1"
		remoting = 7000
	)

	clusterMock := mockscluster.NewCluster(t)
	system := MockReplicationTestSystem(clusterMock)

	// the lazy grain still points at the departed node, so it is released
	clusterMock.EXPECT().GetGrain(mock.Anything, "k/lazy").
		Return(&internalpb.Grain{GrainId: &internalpb.GrainId{Value: "k/lazy"}, Host: host, Port: remoting}, nil).Once()
	clusterMock.EXPECT().RemoveGrain(mock.Anything, "k/lazy").Return(nil).Once()

	stream := eventstream.New()
	consumer := stream.AddSubscriber()
	stream.Subscribe(consumer, eventsTopic)
	pid := &PID{actorSystem: system, logger: log.DiscardLogger, eventsStream: stream}

	peerState := &internalpb.PeerState{
		Host:         host,
		PeersPort:    9000,
		RemotingPort: remoting,
		Actors: map[string]*internalpb.Actor{
			"a1": {Address: "actor-1"},
		},
		Grains: map[string]*internalpb.Grain{
			"eager":    {GrainId: &internalpb.GrainId{Value: "k/eager"}, EagerRelocation: true},
			"lazy":     {GrainId: &internalpb.GrainId{Value: "k/lazy"}},
			"disabled": {GrainId: &internalpb.GrainId{Value: "k/disabled"}, DisableRelocation: true},
		},
	}

	departed := address.FormatHostPort(host, remoting)
	system.reportAbortedRelocation(context.Background(), pid, departed, peerState, 0, stdErrors.New("boom"))

	var events []*RelocationFailed
	for message := range consumer.Iterator() {
		if event, ok := message.Payload().(*RelocationFailed); ok {
			events = append(events, event)
		}
	}

	require.Len(t, events, 1)
	assert.Equal(t, []string{"actor-1"}, events[0].Actors())
	// eager grain reported, lazy released (not reported), disabled excluded
	assert.Equal(t, []string{"k/eager"}, events[0].Grains())
}

// TestDepartedNodeOf verifies the departed-node marker is read from the first
// request that carries one, skipping empty leading entries, and that an empty
// string is returned when no request carries the marker.
func TestDepartedNodeOf(t *testing.T) {
	t.Run("returns the marker skipping empty leading entries", func(t *testing.T) {
		requests := []*internalpb.RelocateBatchRequest{
			{DepartedNode: ""},
			{DepartedNode: "127.0.0.9:7000"},
		}
		assert.Equal(t, "127.0.0.9:7000", departedNodeOf(requests))
	})

	t.Run("returns empty when no request carries the marker", func(t *testing.T) {
		assert.Equal(t, "", departedNodeOf(nil))
		assert.Equal(t, "", departedNodeOf([]*internalpb.RelocateBatchRequest{{DepartedNode: ""}}))
	})
}

// enqueueSpySystem is a minimal ActorSystem test double that intercepts only the
// three recreation/release calls enqueueRelocation makes, so each dispatch
// branch (actor, singleton, eager grain, lazy grain) and its failure path can be
// exercised without a live cluster.
type enqueueSpySystem struct {
	ActorSystem
	recreateActorFn func(*internalpb.Actor) error
	recreateGrainFn func(*internalpb.Grain) error
	releaseLazyFn   func(*internalpb.Grain) error
}

func (s *enqueueSpySystem) recreateActorFromWire(_ context.Context, a *internalpb.Actor, _ string) error {
	return s.recreateActorFn(a)
}

func (s *enqueueSpySystem) recreateGrainFromWire(_ context.Context, g *internalpb.Grain, _ string) error {
	return s.recreateGrainFn(g)
}

func (s *enqueueSpySystem) releaseGrainForLazyRelocation(_ context.Context, g *internalpb.Grain, _ string) error {
	return s.releaseLazyFn(g)
}

// TestEnqueueRelocation verifies the shared dispatch rule used by the leader's
// local share and the peer-side RelocateBatch handler: a plain actor is
// recreated (failures recorded), a singleton is routed through the singleton
// path, an eager grain is reactivated (failures recorded), and a lazy grain only
// has its directory entry released, becoming a failure solely when the release
// fails.
func TestEnqueueRelocation(t *testing.T) {
	spy := &enqueueSpySystem{
		recreateActorFn: func(a *internalpb.Actor) error {
			if a.GetAddress() == "bad-actor" {
				return stdErrors.New("spawn failed")
			}
			return nil
		},
		recreateGrainFn: func(g *internalpb.Grain) error {
			if g.GetGrainId().GetValue() == "k/eager-bad" {
				return stdErrors.New("activate failed")
			}
			return nil
		},
		releaseLazyFn: func(g *internalpb.Grain) error {
			if g.GetGrainId().GetValue() == "k/lazy-bad" {
				return stdErrors.New("release failed")
			}
			return nil
		},
	}

	actors := []*internalpb.Actor{
		{Address: "good-actor"},
		{Address: "bad-actor"},
		// a singleton with an unparseable address routes through the singleton
		// path (recreateSingletonFromWire) and records a failure without needing
		// a live cluster.
		{Address: "bad-singleton", Singleton: &internalpb.SingletonSpec{}},
	}
	grains := []*internalpb.Grain{
		{GrainId: &internalpb.GrainId{Value: "k/eager-ok"}, EagerRelocation: true},
		{GrainId: &internalpb.GrainId{Value: "k/eager-bad"}, EagerRelocation: true},
		{GrainId: &internalpb.GrainId{Value: "k/lazy-ok"}},
		{GrainId: &internalpb.GrainId{Value: "k/lazy-bad"}},
	}

	failures := &relocationFailures{}
	eg := new(errgroup.Group)
	eg.SetLimit(defaultRelocationConcurrency)
	enqueueRelocation(context.Background(), eg, spy, log.DiscardLogger, "127.0.0.1:7000", actors, grains, failures.record)
	require.NoError(t, eg.Wait())

	actorFails, grainFails := splitFailures(failures.items())
	// good-actor and the lazy grain that released cleanly are not reported
	assert.ElementsMatch(t, []string{"bad-actor", "bad-singleton"}, actorFails)
	assert.ElementsMatch(t, []string{"k/eager-bad", "k/lazy-bad"}, grainFails)
}

// TestRelocationWorkerShareFailsOnFallbackPeer verifies that when the target and
// the chosen fallback survivor are both unreachable, the redistributed actor is
// recorded as failed and the lazy grain's directory entry is released, matching
// the no-fallback accounting.
func TestRelocationWorkerShareFailsOnFallbackPeer(t *testing.T) {
	ctx := context.Background()

	system, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)
	sys := system.(*actorSystem)

	transportErr := stdErrors.New("connection refused")
	remotingMock := mocksremote.NewClient(t)
	// the original target fails all its attempts
	remotingMock.EXPECT().RelocateBatch(mock.Anything, "127.0.0.1", 9001, mock.Anything).
		Return(nil, transportErr).Times(relocationBatchMaxAttempts)
	// the fallback survivor also fails all its attempts
	remotingMock.EXPECT().RelocateBatch(mock.Anything, "127.0.0.2", 9002, mock.Anything).
		Return(nil, transportErr).Times(relocationBatchMaxAttempts)

	clusterMock := mockscluster.NewCluster(t)
	// the undeliverable lazy grain still points at the departed node, so the
	// leader releases its stale directory entry
	clusterMock.EXPECT().GetGrain(mock.Anything, "kind/lazy").
		Return(&internalpb.Grain{GrainId: &internalpb.GrainId{Value: "kind/lazy"}, Host: "127.0.0.1", Port: 8080}, nil).Once()
	clusterMock.EXPECT().RemoveGrain(mock.Anything, "kind/lazy").Return(nil).Once()
	sys.cluster = clusterMock

	worker := &relocationWorker{
		remoting: remotingMock,
		pid:      &PID{actorSystem: system, logger: log.DiscardLogger},
		logger:   log.DiscardLogger,
	}
	peers := []*cluster.Peer{
		{Host: "127.0.0.1", RemotingPort: 9001},
		{Host: "127.0.0.2", RemotingPort: 9002},
	}
	requests := buildRelocateBatchRequests("127.0.0.1:8080",
		[]*internalpb.Actor{{Address: "actor-1"}},
		[]*internalpb.Grain{{GrainId: &internalpb.GrainId{Kind: "kind", Name: "lazy", Value: "kind/lazy"}}})
	failures := &relocationFailures{}

	worker.relocateShare(ctx, requests, peers[0], peers, failures)

	items := failures.items()
	actors, grains := splitFailures(items)
	assert.Equal(t, []string{"actor-1"}, actors)
	// the lazy grain is released, not reported as a failure
	assert.Empty(t, grains)
}

// TestReportAbortedRelocationReportsFailedLazyRelease verifies that when a lazy
// grain's directory release fails during an aborted relocation, the grain is
// reported as a failure (its entry still points at the dead node, and the
// fast path does not self-heal a stale owner), preserving the guarantee that
// every relocated grain is either reachable again or listed in RelocationFailed.
func TestReportAbortedRelocationReportsFailedLazyRelease(t *testing.T) {
	const (
		host     = "127.0.0.1"
		remoting = 7000
	)

	clusterMock := mockscluster.NewCluster(t)
	system := MockReplicationTestSystem(clusterMock)

	// the lazy grain still points at the departed node, but its release fails
	clusterMock.EXPECT().GetGrain(mock.Anything, "k/lazy").
		Return(&internalpb.Grain{GrainId: &internalpb.GrainId{Value: "k/lazy"}, Host: host, Port: remoting}, nil).Once()
	clusterMock.EXPECT().RemoveGrain(mock.Anything, "k/lazy").Return(stdErrors.New("store down")).Once()

	stream := eventstream.New()
	consumer := stream.AddSubscriber()
	stream.Subscribe(consumer, eventsTopic)
	pid := &PID{actorSystem: system, logger: log.DiscardLogger, eventsStream: stream}

	peerState := &internalpb.PeerState{
		Host:         host,
		PeersPort:    9000,
		RemotingPort: remoting,
		Grains: map[string]*internalpb.Grain{
			"lazy": {GrainId: &internalpb.GrainId{Value: "k/lazy"}},
		},
	}

	departed := address.FormatHostPort(host, remoting)
	system.reportAbortedRelocation(context.Background(), pid, departed, peerState, 0, stdErrors.New("boom"))

	events := collectRelocationFailedEvents(consumer)
	require.Len(t, events, 1)
	// the lazy grain whose release failed is reported as a failure
	assert.Equal(t, []string{"k/lazy"}, events[0].Grains())
}

// TestRelocatableGrains verifies relocation-disabled grains are dropped from
// the allocation entirely while eager and lazy grains both participate (the
// relocation target dispatches on each grain's eager_relocation flag).
func TestRelocatableGrains(t *testing.T) {
	grains := map[string]*internalpb.Grain{
		"eager": {GrainId: &internalpb.GrainId{Value: "eager"}, EagerRelocation: true},
		"lazy":  {GrainId: &internalpb.GrainId{Value: "lazy"}},
		"disabled": {
			GrainId:           &internalpb.GrainId{Value: "disabled"},
			DisableRelocation: true,
		},
	}

	relocatable := relocatableGrains(grains)

	require.Len(t, relocatable, 2)

	values := []string{
		relocatable[0].GetGrainId().GetValue(),
		relocatable[1].GetGrainId().GetValue(),
	}
	assert.ElementsMatch(t, []string{"eager", "lazy"}, values)
}

// TestAllocateGrainsSlice verifies allocateGrains distributes an eager grain
// slice across the leader and peer shares.
func TestAllocateGrainsSlice(t *testing.T) {
	t.Run("empty slice yields no shares", func(t *testing.T) {
		leader, peers := allocateGrains(3, nil)
		assert.Empty(t, leader)
		assert.Empty(t, peers)
	})

	t.Run("distributes remainder and first chunk to the leader", func(t *testing.T) {
		grains := make([]*internalpb.Grain, 5)
		for i := range grains {
			grains[i] = &internalpb.Grain{GrainId: &internalpb.GrainId{Value: fmt.Sprintf("grain-%d", i)}}
		}

		// totalPeers = leader + 2 peers = 3; quotient 1, remainder 2
		leader, peers := allocateGrains(3, grains)
		// remainder (2) + first chunk (1) go to the leader
		assert.Len(t, leader, 3)
		require.Len(t, peers, 3)
	})
}

// TestAllocateActorsRoleAware verifies allocateActors honors actor role
// constraints, keeps the leader/peer share indexing, routes singletons to the
// leader, and reports role-unsatisfiable actors as unplaceable.
func TestAllocateActorsRoleAware(t *testing.T) {
	newActor := func(name, role string, singleton bool) *internalpb.Actor {
		addr := address.New(name, "test", "127.0.0.9", 7000).String()
		a := &internalpb.Actor{Address: addr, Relocatable: true}
		if role != "" {
			a.Role = &role
		}
		if singleton {
			a.Singleton = &internalpb.SingletonSpec{}
		}
		return a
	}

	t.Run("role-less actors distribute across leader and peers with no unplaceable", func(t *testing.T) {
		peers := []*cluster.Peer{
			{Host: "127.0.0.1", RemotingPort: 9001},
			{Host: "127.0.0.2", RemotingPort: 9002},
		}
		actors := map[string]*internalpb.Actor{}
		for i := range 6 {
			name := fmt.Sprintf("actor-%d", i)
			actors[name] = newActor(name, "", false)
		}
		state := &internalpb.PeerState{Actors: actors}

		leader, peersShares, unplaceable := allocateActors(nil, peers, state, nil)

		assert.Empty(t, unplaceable)
		// index 0 = leader, indexes 1..n map to peers[i-1]
		require.Len(t, peersShares, len(peers)+1)
		assert.ElementsMatch(t, leader, peersShares[0])

		total := 0
		for _, share := range peersShares {
			total += len(share)
		}
		assert.Equal(t, 6, total)
		// balanced across the 3 targets (leader + 2 peers)
		for _, share := range peersShares {
			assert.Equal(t, 2, len(share))
		}
	})

	t.Run("role-constrained actor lands on the matching peer", func(t *testing.T) {
		peers := []*cluster.Peer{
			{Host: "127.0.0.1", RemotingPort: 9001, Roles: []string{"worker"}},
			{Host: "127.0.0.2", RemotingPort: 9002, Roles: []string{"api"}},
		}
		state := &internalpb.PeerState{Actors: map[string]*internalpb.Actor{
			"api-actor": newActor("api-actor", "api", false),
		}}

		leader, peersShares, unplaceable := allocateActors([]string{"control"}, peers, state, nil)

		assert.Empty(t, unplaceable)
		assert.Empty(t, leader)
		// peers[1] advertises "api" -> peersShares index 2
		require.Len(t, peersShares, 3)
		require.Len(t, peersShares[2], 1)
		assert.Equal(t, "api-actor", mustName(t, peersShares[2][0]))
	})

	t.Run("role-constrained actor lands on the leader when only leader matches", func(t *testing.T) {
		peers := []*cluster.Peer{
			{Host: "127.0.0.1", RemotingPort: 9001, Roles: []string{"worker"}},
		}
		state := &internalpb.PeerState{Actors: map[string]*internalpb.Actor{
			"control-actor": newActor("control-actor", "control", false),
		}}

		leader, _, unplaceable := allocateActors([]string{"control"}, peers, state, nil)

		assert.Empty(t, unplaceable)
		require.Len(t, leader, 1)
		assert.Equal(t, "control-actor", mustName(t, leader[0]))
	})

	t.Run("role with no eligible target is reported as unplaceable", func(t *testing.T) {
		peers := []*cluster.Peer{
			{Host: "127.0.0.1", RemotingPort: 9001, Roles: []string{"worker"}},
		}
		state := &internalpb.PeerState{Actors: map[string]*internalpb.Actor{
			"gpu-actor": newActor("gpu-actor", "gpu", false),
		}}

		leader, _, unplaceable := allocateActors([]string{"control"}, peers, state, nil)

		assert.Empty(t, leader)
		require.Len(t, unplaceable, 1)
		assert.Equal(t, "gpu-actor", mustName(t, unplaceable[0]))
	})

	t.Run("singletons always go to the leader share regardless of role", func(t *testing.T) {
		peers := []*cluster.Peer{
			{Host: "127.0.0.1", RemotingPort: 9001, Roles: []string{"api"}},
		}
		state := &internalpb.PeerState{Actors: map[string]*internalpb.Actor{
			"singleton-actor": newActor("singleton-actor", "api", true),
		}}

		leader, _, unplaceable := allocateActors(nil, peers, state, nil)

		assert.Empty(t, unplaceable)
		require.Len(t, leader, 1)
		assert.Equal(t, "singleton-actor", mustName(t, leader[0]))
	})
}

// TestAllocateActorsLoadAware verifies that baseLoads biases placement toward
// the least-loaded surviving targets instead of splitting the departed node's
// actors evenly across all targets.
func TestAllocateActorsLoadAware(t *testing.T) {
	newActor := func(name string) *internalpb.Actor {
		addr := address.New(name, "test", "127.0.0.9", 7000).String()
		return &internalpb.Actor{Address: addr, Relocatable: true}
	}

	peers := []*cluster.Peer{
		{Host: "127.0.0.1", RemotingPort: 9001},
		{Host: "127.0.0.2", RemotingPort: 9002},
	}

	actors := map[string]*internalpb.Actor{}
	for i := range 3 {
		name := fmt.Sprintf("actor-%d", i)
		actors[name] = newActor(name)
	}
	state := &internalpb.PeerState{Actors: actors}

	t.Run("seeded loads steer actors to the least-loaded targets", func(t *testing.T) {
		// leader already hosts 10 actors, peers[0] hosts 5, peers[1] hosts 0.
		// All 3 departed actors should therefore go to peers[1] (index 2) until
		// its load catches up to peers[0].
		baseLoads := []int{10, 5, 0}

		_, peersShares, unplaceable := allocateActors(nil, peers, state, baseLoads)

		assert.Empty(t, unplaceable)
		require.Len(t, peersShares, 3)
		assert.Empty(t, peersShares[0], "over-loaded leader should receive nothing")
		// peers[1] (index 2) starts empty, so it absorbs the first placements
		// until it ties peers[0]'s starting load of 5.
		assert.Len(t, peersShares[2], 3)
		assert.Empty(t, peersShares[1])
	})

	t.Run("mis-sized baseLoads is ignored (even split fallback)", func(t *testing.T) {
		_, peersShares, unplaceable := allocateActors(nil, peers, state, []int{1, 2})

		assert.Empty(t, unplaceable)
		require.Len(t, peersShares, 3)
		total := 0
		for _, share := range peersShares {
			total += len(share)
		}
		assert.Equal(t, 3, total)
		// with three targets starting at zero, each gets exactly one actor
		for _, share := range peersShares {
			assert.Len(t, share, 1)
		}
	})
}

// TestRelocationWorkerTargetLoads verifies targetLoads aligns the cluster's
// per-node actor tally to allocateActors' indexing, that entries for nodes that
// are not relocation targets (e.g. the departed node) never inflate a
// survivor's load, that IPv6 endpoints match the raw, un-bracketed form the
// tally is keyed by, and that a scan failure degrades to nil.
func TestRelocationWorkerTargetLoads(t *testing.T) {
	ctx := context.Background()

	system, err := NewActorSystem("test",
		WithLogger(log.DiscardLogger),
		WithRemote(remote.NewConfig("127.0.0.1", 8080)),
	)
	require.NoError(t, err)
	sys := system.(*actorSystem)

	// index 0 = leader (127.0.0.1:8080); index 1 = IPv4 peer; index 2 = IPv6 peer
	peers := []*cluster.Peer{
		{Host: "127.0.0.2", RemotingPort: 9002},
		{Host: "::1", RemotingPort: 9003},
	}

	worker := &relocationWorker{logger: log.DiscardLogger}

	t.Run("aligns the tally to targets, ignores non-targets, matches IPv6", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		clusterMock.EXPECT().CountActorsByHost(mock.Anything, mock.Anything).Return(map[string]int{
			address.FormatHostPort("127.0.0.1", 8080): 2, // leader
			address.FormatHostPort("127.0.0.2", 9002): 1, // peer 0
			address.FormatHostPort("::1", 9003):       3, // peer 1 (IPv6)
			address.FormatHostPort("127.0.0.9", 7000): 5, // departed node: not a target
		}, nil).Once()
		sys.cluster = clusterMock

		loads := worker.targetLoads(ctx, sys, peers)
		require.Equal(t, []int{2, 1, 3}, loads)
	})

	t.Run("scan failure degrades to nil (even-split fallback)", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		clusterMock.EXPECT().CountActorsByHost(mock.Anything, mock.Anything).
			Return(nil, stdErrors.New("registry unavailable")).Once()
		sys.cluster = clusterMock

		assert.Nil(t, worker.targetLoads(ctx, sys, peers))
	})
}

func mustName(t *testing.T, actor *internalpb.Actor) string {
	t.Helper()
	addr, err := address.Parse(actor.GetAddress())
	require.NoError(t, err)
	return addr.Name()
}

// TestRecreateSingletonFromWireUsesSingletonSpec verifies a relocated singleton
// actor is respawned through the cluster singleton spawn path with its
// serialized singleton configuration restored.
func TestRecreateSingletonFromWireUsesSingletonSpec(t *testing.T) {
	ctx := context.Background()
	system := MockSingletonClusterReadyActorSystem(t)
	clusterMock := mockscluster.NewCluster(t)
	system.locker.Lock()
	system.cluster = clusterMock
	system.locker.Unlock()

	system.registry.Register(new(MockActor))

	singletonSpec := &internalpb.SingletonSpec{
		SpawnTimeout: durationpb.New(3 * time.Second),
		WaitInterval: durationpb.New(250 * time.Millisecond),
		MaxRetries:   int32(4),
	}
	props := &internalpb.Actor{
		Address: address.New("singleton", system.Name(), "127.0.0.1", 8080).String(),
		Type:    types.Name(new(MockActor)),
		Singleton: &internalpb.SingletonSpec{
			SpawnTimeout: singletonSpec.SpawnTimeout,
			WaitInterval: singletonSpec.WaitInterval,
			MaxRetries:   singletonSpec.MaxRetries,
		},
		Role: new("blue"),
	}

	departedNode := address.FormatHostPort("127.0.0.1", 8080)
	clusterMock.EXPECT().GetActor(mock.Anything, "singleton").Return(nil, cluster.ErrActorNotFound).Once()
	clusterMock.EXPECT().RemoveActor(mock.Anything, "singleton").Return(nil).Once()
	clusterMock.EXPECT().RemoveKind(mock.Anything, kindRole(types.Name(new(MockActor)), "blue")).Return(nil).Once()

	spy := &spawnSingletonSpy{actorSystem: system}
	err := recreateSingletonFromWire(ctx, spy, props, departedNode)
	require.NoError(t, err)

	require.True(t, spy.called)
	require.Equal(t, "singleton", spy.actorName)
	require.Equal(t, props.GetType(), types.Name(spy.actor))
	require.NotNil(t, spy.config)
	require.Equal(t, singletonSpec.SpawnTimeout.AsDuration(), spy.config.spawnTimeout)
	require.Equal(t, singletonSpec.WaitInterval.AsDuration(), spy.config.waitInterval)
	require.Equal(t, int(singletonSpec.MaxRetries), spy.config.numberOfRetries)
	require.NotNil(t, spy.config.Role())
	require.Equal(t, props.GetRole(), *spy.config.Role())
}

// TestRecreateSingletonFromWireSkipsWhenAlreadyRelocated verifies a stale re-run
// (a duplicate NodeLeft against a peer-state snapshot a failed DeletePeerState
// left behind) does not tear down and double-spawn a singleton that a survivor
// already re-established: when the registry entry points at a node other than
// the departed one, the relocation is a no-op (no RemoveActor/RemoveKind/spawn).
func TestRecreateSingletonFromWireSkipsWhenAlreadyRelocated(t *testing.T) {
	ctx := context.Background()
	system := MockSingletonClusterReadyActorSystem(t)
	clusterMock := mockscluster.NewCluster(t)
	system.locker.Lock()
	system.cluster = clusterMock
	system.locker.Unlock()

	system.registry.Register(new(MockActor))

	props := &internalpb.Actor{
		Address:   address.New("singleton", system.Name(), "127.0.0.9", 7000).String(),
		Type:      types.Name(new(MockActor)),
		Singleton: &internalpb.SingletonSpec{},
	}

	departedNode := address.FormatHostPort("127.0.0.9", 7000)
	// the registry entry already points at a survivor, not the departed node
	clusterMock.EXPECT().GetActor(mock.Anything, "singleton").
		Return(&internalpb.Actor{
			Address: address.New("singleton", system.Name(), "127.0.0.2", 9002).String(),
		}, nil).Once()

	spy := &spawnSingletonSpy{actorSystem: system}
	err := recreateSingletonFromWire(ctx, spy, props, departedNode)
	require.NoError(t, err)

	// no teardown and no re-spawn: the live singleton is left untouched
	assert.False(t, spy.called)
	clusterMock.AssertNotCalled(t, "RemoveActor", mock.Anything, mock.Anything)
	clusterMock.AssertNotCalled(t, "RemoveKind", mock.Anything, mock.Anything)
}
