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
	// single-node cluster: no peers
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

// TestRelocationWorkerReleasesUndeliverableLazyGrains verifies that when a lazy
// grain's share cannot be delivered to any peer, the leader removes its stale
// directory entry itself so the grain is not left permanently unreachable (the
// TellGrain/AskGrain fast path does not self-heal a stale owner). The lazy grain
// is not reported as a failure; only the eager grain is.
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

	items := failures.items()
	require.Len(t, items, 1)
	assert.True(t, items[0].GetGrain())
	assert.Equal(t, "kind/eager", items[0].GetId())
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

// TestFilterUnplaceableByRole verifies the role-aware fallback filter: actors
// whose required role the fallback peer does not advertise are recorded as
// failures and dropped, while role-less actors, role-matching actors and all
// grains are retained.
func TestFilterUnplaceableByRole(t *testing.T) {
	gpu := "gpu"
	blue := "blue"
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
				{Address: address.New("gpu-only", "test", "127.0.0.9", 7000).String(), Role: &gpu},
			},
		},
	}

	failures := &relocationFailures{}
	filtered := filterUnplaceableByRole(requests, []string{"blue"}, failures)

	// the second request held only a gpu actor and no grains, so it is dropped
	require.Len(t, filtered, 1)
	// role-less and blue actors are kept; the gpu actor is dropped
	require.Len(t, filtered[0].GetActors(), 2)
	require.Len(t, filtered[0].GetGrains(), 1)

	items := failures.items()
	require.Len(t, items, 2)

	for _, item := range items {
		require.False(t, item.GetGrain())
		require.Contains(t, item.GetId(), "gpu")
	}
}

// TestEagerRelocationGrains verifies only eager grains are treated as failures
// when relocation aborts before dispatch: lazy grains self-heal and disabled
// grains are lost by design, so both are excluded.
func TestEagerRelocationGrains(t *testing.T) {
	grains := map[string]*internalpb.Grain{
		"eager":    {GrainId: &internalpb.GrainId{Value: "eager"}, EagerRelocation: true},
		"lazy":     {GrainId: &internalpb.GrainId{Value: "lazy"}},
		"disabled": {GrainId: &internalpb.GrainId{Value: "disabled"}, DisableRelocation: true},
	}

	filtered := eagerRelocationGrains(grains)

	require.Len(t, filtered, 1)
	_, ok := filtered["eager"]
	require.True(t, ok)
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

		leader, peersShares, unplaceable := allocateActors(nil, peers, state)

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

		leader, peersShares, unplaceable := allocateActors([]string{"control"}, peers, state)

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

		leader, _, unplaceable := allocateActors([]string{"control"}, peers, state)

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

		leader, _, unplaceable := allocateActors([]string{"control"}, peers, state)

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

		leader, _, unplaceable := allocateActors(nil, peers, state)

		assert.Empty(t, unplaceable)
		require.Len(t, leader, 1)
		assert.Equal(t, "singleton-actor", mustName(t, leader[0]))
	})
}

func mustName(t *testing.T, actor *internalpb.Actor) string {
	t.Helper()
	addr, err := address.Parse(actor.GetAddress())
	require.NoError(t, err)
	return addr.Name()
}

// TestNextSurvivingPeer verifies fallback peer selection: the next peer in the
// list wrapping around, nil when the target is the only peer, and the first
// peer when the target is unknown.
func TestNextSurvivingPeer(t *testing.T) {
	peer1 := &cluster.Peer{Host: "127.0.0.1", RemotingPort: 9001}
	peer2 := &cluster.Peer{Host: "127.0.0.2", RemotingPort: 9002}
	peer3 := &cluster.Peer{Host: "127.0.0.3", RemotingPort: 9003}
	peers := []*cluster.Peer{peer1, peer2, peer3}

	assert.Equal(t, peer2, nextSurvivingPeer(peers, peer1))
	assert.Equal(t, peer1, nextSurvivingPeer(peers, peer3))
	assert.Nil(t, nextSurvivingPeer([]*cluster.Peer{peer1}, peer1))
	assert.Equal(t, peer1, nextSurvivingPeer(peers, &cluster.Peer{Host: "10.0.0.1", RemotingPort: 1}))
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

	clusterMock.EXPECT().RemoveActor(mock.Anything, "singleton").Return(nil).Once()
	clusterMock.EXPECT().RemoveKind(mock.Anything, kindRole(types.Name(new(MockActor)), "blue")).Return(nil).Once()

	spy := &spawnSingletonSpy{actorSystem: system}
	err := recreateSingletonFromWire(ctx, spy, props)
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
