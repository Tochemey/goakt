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
	// single-node cluster: no peers, so every relocated actor lands on the leader
	clusterMock.EXPECT().Peers(mock.Anything).Return(nil, nil).Once()
	// the non-relocatable actor has no stale registry entry left
	clusterMock.EXPECT().GetActor(mock.Anything, "skipped").Return(nil, cluster.ErrActorNotFound).Once()

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
// share is reported as failed instead of aborting anything else.
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
		[]*internalpb.Grain{{GrainId: &internalpb.GrainId{Value: "grain-1"}}})
	failures := &relocationFailures{}

	worker.relocateShare(ctx, requests, peers[0], peers, failures)

	items := failures.items()
	require.Len(t, items, 2)

	actors, grains := splitFailures(items)
	assert.Equal(t, []string{"actor-1"}, actors)
	assert.Equal(t, []string{"grain-1"}, grains)

	for _, item := range items {
		assert.Contains(t, item.GetMessage(), transportErr.Error())
	}
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
