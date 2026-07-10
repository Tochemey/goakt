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
	"crypto/tls"
	stdErrors "errors"
	"fmt"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/kapetan-io/tackle/autotls"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/eventstream"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/reentrancy"
	"github.com/tochemey/goakt/v4/supervisor"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

// TestRelocatorPublishRelocationFailedIncludesActorsAndGrains verifies the
// RelocationFailed event carries the departed node's address together with the
// affected actor addresses and grain ids.
func TestRelocatorPublishRelocationFailedIncludesActorsAndGrains(t *testing.T) {
	system, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	stream := eventstream.New()
	consumer := stream.AddSubscriber()
	stream.Subscribe(consumer, eventsTopic)

	pid := &PID{
		actorSystem:  system,
		logger:       log.DiscardLogger,
		eventsStream: stream,
	}

	actors := map[string]*internalpb.Actor{
		"a1": {Address: "actor-1"},
	}
	grains := map[string]*internalpb.Grain{
		"g1": {GrainId: &internalpb.GrainId{Value: "grain-1"}},
	}

	publishRelocationFailed(pid, "127.0.0.1:9000", actors, grains, stdErrors.New("boom"))

	var events []*RelocationFailed
	for message := range consumer.Iterator() {
		if event, ok := message.Payload().(*RelocationFailed); ok {
			events = append(events, event)
		}
	}
	require.Len(t, events, 1)
	assert.Equal(t, "127.0.0.1:9000", events[0].Address())
	assert.Equal(t, []string{"actor-1"}, events[0].Actors())
	assert.Equal(t, []string{"grain-1"}, events[0].Grains())
	require.Error(t, events[0].Error())
}

// TestRelocatorPublishRelocationFailedWithoutEventStream verifies the publish
// path is a safe no-op when no event stream is configured on the pid.
func TestRelocatorPublishRelocationFailedWithoutEventStream(t *testing.T) {
	require.NotPanics(t, func() {
		publishRelocationFailed(&PID{}, "127.0.0.1:9000",
			map[string]*internalpb.Actor{"a1": {Address: "actor-1"}}, nil, stdErrors.New("boom"))
	})
}

// TestRelocatorTerminatedAbortsInflightJob verifies that a worker death with a
// still-registered relocation job (the abnormal-death path) publishes a
// RelocationFailed event listing every actor and grain of the departed node,
// removes its peer state snapshot, and releases the job so a future departure
// of the same address can rebalance again.
func TestRelocatorTerminatedAbortsInflightJob(t *testing.T) {
	ctx := context.Background()

	system, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	sys := system.(*actorSystem)
	store := &recordingPeerStateStore{}
	sys.clusterStore = store

	peerState := &internalpb.PeerState{
		Host:      "127.0.0.1",
		PeersPort: 9000,
		Actors: map[string]*internalpb.Actor{
			"a1": {Address: "actor-1"},
		},
		Grains: map[string]*internalpb.Grain{
			"g1": {GrainId: &internalpb.GrainId{Value: "grain-1"}},
		},
	}
	require.True(t, sys.beginRelocation("127.0.0.1:9000", peerState))

	stream := eventstream.New()
	consumer := stream.AddSubscriber()
	stream.Subscribe(consumer, eventsTopic)

	workerName := reservedName(relocationWorkerType) + "-1"
	manager := &relocator{
		pid: &PID{
			actorSystem:  system,
			logger:       log.DiscardLogger,
			eventsStream: stream,
		},
		logger:  log.DiscardLogger,
		workers: map[string]workerJob{workerName: {address: "127.0.0.1:9000", peerState: peerState}},
	}

	terminated := &Terminated{actorPath: newPath(address.New(workerName, "test", "127.0.0.1", 9000))}
	receiveCtx := newReceiveContext(ctx, nil, manager.pid, terminated)
	manager.Receive(receiveCtx)

	// the in-flight job is released so a future departure can rebalance again
	_, inflight := sys.relocationJob("127.0.0.1:9000")
	require.False(t, inflight)
	require.Empty(t, manager.workers)

	// the departed node's peer state is removed
	require.True(t, store.deleteCalled)
	assert.Equal(t, "127.0.0.1:9000", store.deletedAddr)

	// the abnormal death is surfaced as a RelocationFailed event listing all items
	var events []*RelocationFailed
	for message := range consumer.Iterator() {
		if event, ok := message.Payload().(*RelocationFailed); ok {
			events = append(events, event)
		}
	}
	require.Len(t, events, 1)
	assert.Equal(t, "127.0.0.1:9000", events[0].Address())
	assert.Equal(t, []string{"actor-1"}, events[0].Actors())
	assert.Equal(t, []string{"grain-1"}, events[0].Grains())
	require.Error(t, events[0].Error())
}

// TestRelocatorTerminatedAfterNormalCompletionIsNoOp verifies that a worker
// death whose job has already been released (the normal completion path, where
// the worker did its own bookkeeping before stopping) does not publish a
// spurious RelocationFailed event or touch the cluster store.
func TestRelocatorTerminatedAfterNormalCompletionIsNoOp(t *testing.T) {
	ctx := context.Background()

	system, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	sys := system.(*actorSystem)
	store := &recordingPeerStateStore{}
	sys.clusterStore = store

	stream := eventstream.New()
	consumer := stream.AddSubscriber()
	stream.Subscribe(consumer, eventsTopic)

	workerName := reservedName(relocationWorkerType) + "-1"
	manager := &relocator{
		pid: &PID{
			actorSystem:  system,
			logger:       log.DiscardLogger,
			eventsStream: stream,
		},
		logger:  log.DiscardLogger,
		workers: map[string]workerJob{workerName: {address: "127.0.0.1:9000", peerState: &internalpb.PeerState{Host: "127.0.0.1", PeersPort: 9000}}},
	}

	// no beginRelocation: the worker already released the job before stopping
	terminated := &Terminated{actorPath: newPath(address.New(workerName, "test", "127.0.0.1", 9000))}
	receiveCtx := newReceiveContext(ctx, nil, manager.pid, terminated)
	manager.Receive(receiveCtx)

	require.Empty(t, manager.workers)
	require.False(t, store.deleteCalled)

	var events []*RelocationFailed
	for message := range consumer.Iterator() {
		if event, ok := message.Payload().(*RelocationFailed); ok {
			events = append(events, event)
		}
	}
	require.Empty(t, events)
}

// TestRelocatorTerminatedIgnoresUnknownWorker verifies that a Terminated for an
// actor the relocator does not track is ignored.
func TestRelocatorTerminatedIgnoresUnknownWorker(t *testing.T) {
	ctx := context.Background()

	system, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	manager := &relocator{
		pid:     &PID{actorSystem: system, logger: log.DiscardLogger},
		logger:  log.DiscardLogger,
		workers: make(map[string]workerJob),
	}

	terminated := &Terminated{actorPath: newPath(address.New("some-actor", "test", "127.0.0.1", 9000))}
	receiveCtx := newReceiveContext(ctx, nil, manager.pid, terminated)

	require.NotPanics(t, func() {
		manager.Receive(receiveCtx)
	})
}

// TestRelocatorStaleTerminatedDoesNotAbortNewerJob covers the re-departure
// race: worker-1 completed and released its job, the same address departed
// again and a new job was registered before worker-1's Terminated reached the
// relocator. The stale Terminated must leave the newer job untouched instead
// of aborting it.
func TestRelocatorStaleTerminatedDoesNotAbortNewerJob(t *testing.T) {
	ctx := context.Background()

	system, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	sys := system.(*actorSystem)
	store := &recordingPeerStateStore{}
	sys.clusterStore = store

	oldPeerState := &internalpb.PeerState{Host: "127.0.0.1", PeersPort: 9000}
	newPeerState := &internalpb.PeerState{Host: "127.0.0.1", PeersPort: 9000}
	require.True(t, sys.beginRelocation("127.0.0.1:9000", newPeerState))

	stream := eventstream.New()
	consumer := stream.AddSubscriber()
	stream.Subscribe(consumer, eventsTopic)

	workerName := reservedName(relocationWorkerType) + "-1"
	manager := &relocator{
		pid: &PID{
			actorSystem:  system,
			logger:       log.DiscardLogger,
			eventsStream: stream,
		},
		logger:  log.DiscardLogger,
		workers: map[string]workerJob{workerName: {address: "127.0.0.1:9000", peerState: oldPeerState}},
	}

	terminated := &Terminated{actorPath: newPath(address.New(workerName, "test", "127.0.0.1", 9000))}
	receiveCtx := newReceiveContext(ctx, nil, manager.pid, terminated)
	manager.Receive(receiveCtx)

	// the dead worker is untracked but the newer job stays registered
	require.Empty(t, manager.workers)
	registered, inflight := sys.relocationJob("127.0.0.1:9000")
	require.True(t, inflight)
	require.Same(t, newPeerState, registered)

	// no spurious abort: the peer state snapshot is kept and no failure is published
	require.False(t, store.deleteCalled)

	var events []*RelocationFailed
	for message := range consumer.Iterator() {
		if event, ok := message.Payload().(*RelocationFailed); ok {
			events = append(events, event)
		}
	}
	require.Empty(t, events)
}

// TestRelocatorRebalanceForReDepartedAddressIsNotSkipped covers the other half
// of the re-departure race: a Rebalance for an address whose completed worker
// is still tracked (its Terminated not yet processed) must be handled, not
// silently dropped. Here the worker spawn fails (the relocator pid is not part
// of a running actor tree), so handling means aborting the relocation: the job
// is released and the peer state snapshot removed instead of staying orphaned.
func TestRelocatorRebalanceForReDepartedAddressIsNotSkipped(t *testing.T) {
	ctx := context.Background()

	system, err := NewActorSystem("test", WithLogger(log.DiscardLogger))
	require.NoError(t, err)

	sys := system.(*actorSystem)
	store := &recordingPeerStateStore{}
	sys.clusterStore = store

	oldPeerState := &internalpb.PeerState{Host: "127.0.0.1", PeersPort: 9000}
	newPeerState := &internalpb.PeerState{Host: "127.0.0.1", PeersPort: 9000}
	require.True(t, sys.beginRelocation("127.0.0.1:9000", newPeerState))

	workerName := reservedName(relocationWorkerType) + "-1"
	manager := &relocator{
		pid:     &PID{actorSystem: system, logger: log.DiscardLogger},
		logger:  log.DiscardLogger,
		workers: map[string]workerJob{workerName: {address: "127.0.0.1:9000", peerState: oldPeerState}},
	}

	receiveCtx := newReceiveContext(ctx, nil, manager.pid, &internalpb.Rebalance{PeerState: newPeerState})
	manager.Receive(receiveCtx)

	// the rebalance was processed: the failed spawn aborted the relocation,
	// releasing the job and removing the peer state snapshot
	_, inflight := sys.relocationJob("127.0.0.1:9000")
	require.False(t, inflight)
	require.True(t, store.deleteCalled)
	assert.Equal(t, "127.0.0.1:9000", store.deletedAddr)
}

func TestRelocation(t *testing.T) {
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start a system cluster
	node1, sd1 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor1-%d", j)
		pid, err := node1.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor2-%d", j)
		pid, err := node2.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	reentrantName := "Reentrant-Actor"
	pid, err := node2.Spawn(ctx, reentrantName, NewMockActor(),
		WithLongLived(),
		WithReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant), reentrancy.WithMaxInFlight(3))))
	require.NoError(t, err)
	require.NotNil(t, pid)

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor3-%d", j)
		pid, err := node3.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	// Verify actors are on node2 before shutdown
	actorName := "Actor2-1"
	node2Address := net.JoinHostPort(node2.Host(), strconv.Itoa(node2.Port()))
	actorPID, err := node1.ActorOf(ctx, actorName)
	require.NoError(t, err)
	require.NotNil(t, actorPID)
	require.Equal(t, node2Address, actorPID.Path().HostPort(), "Actor %s should be on node2 before shutdown", actorName)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Allow time for the cluster to detect node2 leaving and start relocation
	pause.For(2 * time.Second)

	// Wait for cluster rebalancing - verify actor relocation actually occurred.
	// The relocation process:
	// 1. Node2 shutdown: cleanupCluster removes actors from cluster map (synchronous)
	// 2. NodeLeft event is emitted and processed by leader
	// 3. Leader fetches peer state from cluster store and enqueues for relocation
	// 4. Relocator processes: recreateLocally removes actor from cluster map, then spawns locally
	// 5. New actor is registered in cluster map with NEW address (via putActorOnCluster)
	//
	// The test must verify the actor is relocated (has new address), not just that it exists.
	// During relocation, the actor may temporarily not exist (removed but not yet re-added),
	// so we accept either: actor doesn't exist (relocation in progress) OR actor exists with new address (relocation complete).
	// We reject: actor exists with old address (relocation hasn't started or failed).
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, actorName)
		if err != nil {
			return false
		}
		if !exists {
			// Actor doesn't exist - this is OK during relocation (removed but not yet re-added)
			// We'll keep checking until it's re-added with new address
			return false
		}
		// Actor exists - verify it's on a live node (not node2)
		relocatedPID, err := node1.ActorOf(ctx, actorName)
		if err != nil || relocatedPID == nil {
			return false
		}
		// Critical check: actor must have a NEW address (not node2's address)
		// If it still has node2's address, relocation hasn't happened yet
		return relocatedPID.Path().HostPort() != node2Address
	}, 2*time.Minute, 500*time.Millisecond, "Actor %s should be relocated from node2 (was %s) to a live node", actorName, node2Address)

	// Verify the relocated actor is reachable by sending a message.
	// The actor may still be warming up on the target node, so retry.
	require.Eventually(t, func() bool {
		sender, err := node1.ActorOf(ctx, "Actor1-1")
		if err != nil || sender == nil {
			return false
		}
		return sender.SendAsync(ctx, actorName, new(testpb.TestSend)) == nil
	}, 30*time.Second, 500*time.Millisecond, "Should be able to send to relocated actor %s", actorName)

	// Wait for reentrant actor to be relocated - verify it's on a live node
	// During relocation, the actor may temporarily not exist (removed but not yet re-added),
	// so we accept either: actor doesn't exist (relocation in progress) OR actor exists with new address (relocation complete).
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, reentrantName)
		if err != nil {
			return false
		}
		if !exists {
			// Actor doesn't exist - this is OK during relocation (removed but not yet re-added)
			// We'll keep checking until it's re-added with new address
			return false
		}
		// Verify actor is actually on a live node (node1 or node3), not on dead node2
		reentrantPID, err := node1.ActorOf(ctx, reentrantName)
		if err != nil || reentrantPID == nil {
			return false
		}
		// Actor should be on node1 or node3, not on node2 (which is down)
		return reentrantPID.Path().HostPort() != node2Address
	}, 2*time.Minute, 500*time.Millisecond, "Reentrant actor %s should be relocated from node2 (was %s) to a live node", reentrantName, node2Address)

	reentrantPID, err := node1.ActorOf(ctx, reentrantName)
	require.NoError(t, err)
	if reentrantPID.IsLocal() {
		require.NotNil(t, reentrantPID.reentrancy)
		require.Equal(t, reentrancy.StashNonReentrant, reentrantPID.reentrancy.mode)
		require.Equal(t, 3, reentrantPID.reentrancy.maxInFlight)
	}

	reentrantAddr := reentrantPID.Path()
	if reentrantAddr.HostPort() == net.JoinHostPort(node1.Host(), strconv.Itoa(node1.Port())) {
		localPID, err := node1.ActorOf(ctx, reentrantName)
		require.NoError(t, err)
		require.NotNil(t, localPID.reentrancy)
		require.Equal(t, reentrancy.StashNonReentrant, localPID.reentrancy.mode)
		require.Equal(t, 3, localPID.reentrancy.maxInFlight)
	}

	if reentrantAddr.HostPort() == net.JoinHostPort(node3.Host(), strconv.Itoa(node3.Port())) {
		localPID, err := node3.ActorOf(ctx, reentrantName)
		require.NoError(t, err)
		require.NotNil(t, localPID.reentrancy)
		require.Equal(t, reentrancy.StashNonReentrant, localPID.reentrancy.mode)
		require.Equal(t, 3, localPID.reentrancy.maxInFlight)
	}

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRelocationWithCustomSupervisor(t *testing.T) {
	ctx := context.TODO()
	srv := startNatsServer(t)

	node1, sd1 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	node2, sd2 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	customSupervisor := supervisor.NewSupervisor(
		supervisor.WithStrategy(supervisor.OneForAllStrategy),
		supervisor.WithRetry(3, 2*time.Second),
		supervisor.WithDirective(&errors.InternalError{}, supervisor.RestartDirective),
	)

	actorName := "custom-supervised-actor"
	pid, err := node2.Spawn(ctx, actorName, NewMockActor(), WithSupervisor(customSupervisor))
	require.NoError(t, err)
	require.NotNil(t, pid)

	pause.For(time.Second)

	// Verify actor is on node2 before shutdown
	node2Address := net.JoinHostPort(node2.Host(), strconv.Itoa(node2.Port()))
	actorPID, err := node1.ActorOf(ctx, actorName)
	require.NoError(t, err)
	require.NotNil(t, actorPID)
	require.Equal(t, node2Address, actorPID.Path().HostPort(), "Actor %s should be on node2 before shutdown", actorName)

	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Allow time for the cluster to detect node2 leaving and start relocation
	pause.For(2 * time.Second)

	// Wait for relocation - verify actor exists and is on a live node (not node2)
	// The relocation process:
	// 1. Node2 shutdown: cleanupCluster removes actors from cluster map (synchronous)
	// 2. NodeLeft event is emitted and processed by leader
	// 3. Leader fetches peer state from cluster store and enqueues for relocation
	// 4. Relocator processes: recreateLocally removes actor from cluster map, then spawns locally
	// 5. New actor is registered in cluster map with NEW address (via putActorOnCluster)
	//
	// The test must verify the actor is relocated (has new address), not just that it exists.
	// During relocation, the actor may temporarily not exist (removed but not yet re-added),
	// so we accept either: actor doesn't exist (relocation in progress) OR actor exists with new address (relocation complete).
	// We reject: actor exists with old address (relocation hasn't started or failed).
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, actorName)
		if err != nil {
			return false
		}
		if !exists {
			// Actor doesn't exist - this is OK during relocation (removed but not yet re-added)
			// We'll keep checking until it's re-added with new address
			return false
		}
		// Actor exists - verify it's on a live node (not node2)
		relocatedPID, err := node1.ActorOf(ctx, actorName)
		if err != nil || relocatedPID == nil {
			return false
		}
		// Critical check: actor must have a NEW address (not node2's address)
		// If it still has node2's address, relocation hasn't happened yet
		return relocatedPID.Path().HostPort() != node2Address
	}, 2*time.Minute, 500*time.Millisecond, "Actor %s should be relocated from node2 (was %s) to a live node", actorName, node2Address)

	// Verify the relocated actor has the correct supervisor configuration.
	// Retry to handle transient propagation delays after relocation.
	require.Eventually(t, func() bool {
		relocated, err := node1.ActorOf(ctx, actorName)
		if err != nil || relocated == nil || relocated.supervisor == nil {
			return false
		}
		if relocated.supervisor.Strategy() != supervisor.OneForAllStrategy {
			return false
		}
		if relocated.supervisor.MaxRetries() != 3 {
			return false
		}
		if relocated.supervisor.Timeout() != 2*time.Second {
			return false
		}
		directive, ok := relocated.supervisor.Directive(&errors.InternalError{})
		return ok && directive == supervisor.RestartDirective
	}, 30*time.Second, time.Second, "Relocated actor %s should have correct supervisor config", actorName)

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, sd1.Close())
	srv.Shutdown()
}

func TestRelocationWithTLS(t *testing.T) {
	t.Skip("Github actions is not stable with TLS tests")
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// AutoGenerate TLS certs
	serverConf := autotls.Config{
		CaFile:           "../test/data/certs/ca.cert",
		CertFile:         "../test/data/certs/auto.pem",
		KeyFile:          "../test/data/certs/auto.key",
		ClientAuthCaFile: "../test/data/certs/client-auth-ca.pem",
		ClientAuth:       tls.RequireAndVerifyClientCert,
	}
	require.NoError(t, autotls.Setup(&serverConf))

	clientConf := &autotls.Config{
		CertFile:           "../test/data/certs/client-auth.pem",
		KeyFile:            "../test/data/certs/client-auth.key",
		InsecureSkipVerify: true,
	}
	require.NoError(t, autotls.Setup(clientConf))

	serverConfig := serverConf.ServerTLS
	clientConfig := clientConf.ClientTLS
	serverConfig.NextProtos = []string{"h2", "http/1.1"}
	clientConfig.NextProtos = []string{"h2", "http/1.1"}

	// create and start system cluster
	node1, sd1 := testNATs(t, srv.Addr().String(), withTestTLS(serverConfig, clientConfig))
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start system cluster
	node2, sd2 := testNATs(t, srv.Addr().String(), withTestTLS(serverConfig, clientConfig))
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start system cluster
	node3, sd3 := testNATs(t, srv.Addr().String(), withTestTLS(serverConfig, clientConfig))
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node1-Actor-%d", j)
		pid, err := node1.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node2-Actor-%d", j)
		pid, err := node2.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node3-Actor-%d", j)
		pid, err := node3.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Allow time for the cluster to detect node2 leaving and start relocation
	pause.For(2 * time.Second)

	actorName := "Node2-Actor-1"
	node2Address := net.JoinHostPort(node2.Host(), strconv.Itoa(node2.Port()))
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, actorName)
		if err != nil || !exists {
			return false
		}
		relocatedPID, err := node1.ActorOf(ctx, actorName)
		if err != nil || relocatedPID == nil {
			return false
		}
		return relocatedPID.Path().HostPort() != node2Address
	}, 2*time.Minute, 500*time.Millisecond, "Actor %s should be relocated from node2 to a live node", actorName)

	require.Eventually(t, func() bool {
		sender, err := node1.ActorOf(ctx, "Node1-Actor-1")
		if err != nil || sender == nil {
			return false
		}
		return sender.SendAsync(ctx, actorName, new(testpb.TestSend)) == nil
	}, 30*time.Second, 500*time.Millisecond, "Should be able to send to relocated actor %s", actorName)

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRelocationWithSingletonActor(t *testing.T) {
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start system cluster
	node1, sd1 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start system cluster
	node2, sd2 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start system cluster
	node3, sd3 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// create a singleton actor
	actorName := "actorName"
	_, err := node1.SpawnSingleton(ctx, actorName, NewMockActor())
	require.NoError(t, err)

	pause.For(time.Second)

	// Verify singleton is on node1 before shutdown
	node1Address := net.JoinHostPort(node1.Host(), strconv.Itoa(node1.Port()))
	singletonPID, err := node2.ActorOf(ctx, actorName)
	require.NoError(t, err)
	require.NotNil(t, singletonPID)
	require.Equal(t, node1Address, singletonPID.Path().HostPort(), "Singleton %s should be on node1 before shutdown", actorName)

	// take down node1 since it is the first node created in the cluster
	require.NoError(t, node1.Stop(ctx))
	require.NoError(t, sd1.Close())

	// Allow time for the cluster to detect node1 leaving and start relocation
	pause.For(2 * time.Second)

	// Wait for singleton relocation - verify it exists and is on a live node (not node1)
	// The relocation process:
	// 1. Node1 shutdown: cleanupCluster removes actors from cluster map (synchronous)
	// 2. NodeLeft event is emitted and processed by leader
	// 3. Leader fetches peer state from cluster store and enqueues for relocation
	// 4. Relocator processes: recreateLocally removes actor from cluster map, then spawns locally
	// 5. New singleton is registered in cluster map with NEW address (via putActorOnCluster)
	//
	// The test must verify the singleton is relocated (has new address), not just that it exists.
	// During relocation, the singleton may temporarily not exist (removed but not yet re-added),
	// so we accept either: singleton doesn't exist (relocation in progress) OR singleton exists with new address (relocation complete).
	// We reject: singleton exists with old address (relocation hasn't started or failed).
	require.Eventually(t, func() bool {
		exists, err := node2.ActorExists(ctx, actorName)
		if err != nil {
			return false
		}
		if !exists {
			// Singleton doesn't exist - this is OK during relocation (removed but not yet re-added)
			// We'll keep checking until it's re-added with new address
			return false
		}
		// Singleton exists - verify it's on a live node (not node1)
		relocatedPID, err := node2.ActorOf(ctx, actorName)
		if err != nil || relocatedPID == nil {
			return false
		}
		// Critical check: singleton must have a NEW address (not node1's address)
		// If it still has node1's address, relocation hasn't happened yet
		return relocatedPID.Path().HostPort() != node1Address
	}, 2*time.Minute, 500*time.Millisecond, "Singleton %s should be relocated from node1 (was %s) to a live node", actorName, node1Address)

	assert.NoError(t, node2.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd2.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRelocationWithActorRelocationDisabled(t *testing.T) {
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start system cluster
	node1, sd1 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start system cluster
	node2, sd2 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start system cluster
	node3, sd3 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node1-Actor-%d", j)
		pid, err := node1.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node2-Actor-%d", j)
		pid, err := node2.Spawn(ctx, actorName, NewMockActor(), WithRelocationDisabled())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node3-Actor-%d", j)
		pid, err := node3.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Allow time for the cluster to detect node2 leaving
	pause.For(2 * time.Second)

	// Verify actors with relocation disabled are never relocated.
	// We poll for a reasonable window to confirm the actor stays gone.
	actorName := "Node2-Actor-1"
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, actorName)
		if err != nil {
			return false
		}
		return !exists
	}, 30*time.Second, time.Second, "Actor %s with relocation disabled should not be relocated", actorName)

	sender, err := node1.ActorOf(ctx, "Node1-Actor-1")
	require.NoError(t, err)
	require.NotNil(t, sender)

	err = sender.SendAsync(ctx, actorName, new(testpb.TestSend))
	require.Error(t, err)

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRelocationWithSystemRelocationDisabled(t *testing.T) {
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start a system cluster
	node1, sd1 := testNATs(t, srv.Addr().String(), withoutTestRelocation())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testNATs(t, srv.Addr().String(), withoutTestRelocation())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testNATs(t, srv.Addr().String(), withoutTestRelocation())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node1-Actor-%d", j)
		pid, err := node1.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node2-Actor-%d", j)
		pid, err := node2.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Node3-Actor-%d", j)
		pid, err := node3.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Allow time for the cluster to detect node2 leaving
	pause.For(2 * time.Second)

	// With system relocation disabled, node2 actors should never reappear
	actorName := "Node2-Actor-1"
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, actorName)
		if err != nil {
			return false
		}
		return !exists
	}, 30*time.Second, time.Second, "Actor %s should not be relocated when system relocation is disabled", actorName)

	sender, err := node1.ActorOf(ctx, "Node1-Actor-1")
	require.NoError(t, err)
	require.NotNil(t, sender)

	err = sender.SendAsync(ctx, actorName, new(testpb.TestSend))
	require.Error(t, err)
	require.ErrorIs(t, err, errors.ErrActorNotFound)

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRelocationWithExtension(t *testing.T) {
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// create the state store extension
	stateStoreExtension := NewMockExtension()

	// create and start a system cluster
	node1, sd1 := testNATs(t, srv.Addr().String(), withMockExtension(stateStoreExtension))
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testNATs(t, srv.Addr().String(), withMockExtension(stateStoreExtension))
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testNATs(t, srv.Addr().String(), withMockExtension(stateStoreExtension))
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 entities on each node
	for j := 1; j <= 4; j++ {
		entityID := fmt.Sprintf("node1-entity-%d", j)
		pid, err := node1.Spawn(ctx, entityID, NewMockEntity(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)

		command := &testpb.CreateAccount{
			AccountBalance: 500.00,
		}
		_, err = Ask(ctx, pid, command, time.Minute)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		entityID := fmt.Sprintf("node2-entity-%d", j)
		pid, err := node2.Spawn(ctx, entityID, NewMockEntity(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)

		command := &testpb.CreateAccount{
			AccountBalance: 600.00,
		}
		_, err = Ask(ctx, pid, command, time.Minute)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		entityID := fmt.Sprintf("node3-entity-%d", j)
		pid, err := node3.Spawn(ctx, entityID, NewMockEntity(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)

		command := &testpb.CreateAccount{
			AccountBalance: 700.00,
		}
		_, err = Ask(ctx, pid, command, time.Minute)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	// Verify entity is on node2 before shutdown
	entityID := "node2-entity-1"
	node2Address := net.JoinHostPort(node2.Host(), strconv.Itoa(node2.Port()))
	entityPID, err := node1.ActorOf(ctx, entityID)
	require.NoError(t, err)
	require.NotNil(t, entityPID)
	require.Equal(t, node2Address, entityPID.Path().HostPort(), "Entity %s should be on node2 before shutdown", entityID)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Allow time for the cluster to detect node2 leaving and start relocation
	pause.For(2 * time.Second)

	// Wait for relocation - verify entity exists and is on a live node (not node2)
	// The relocation process:
	// 1. Node2 shutdown: cleanupCluster removes actors from cluster map (synchronous)
	// 2. NodeLeft event is emitted and processed by leader
	// 3. Leader fetches peer state from cluster store and enqueues for relocation
	// 4. Relocator processes: recreateLocally removes actor from cluster map, then spawns locally
	// 5. New actor is registered in cluster map with NEW address (via putActorOnCluster)
	//
	// The test must verify the entity is relocated (has new address), not just that it exists.
	// During relocation, the entity may temporarily not exist (removed but not yet re-added),
	// so we accept either: entity doesn't exist (relocation in progress) OR entity exists with new address (relocation complete).
	// We reject: entity exists with old address (relocation hasn't started or failed).
	//
	// On CI, cluster state updates may take longer, so we use a longer check interval (1s instead of 500ms)
	// to reduce contention and allow cluster state to properly propagate.
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, entityID)
		if err != nil {
			return false
		}
		if !exists {
			// Entity doesn't exist - this is OK during relocation (removed but not yet re-added)
			// We'll keep checking until it's re-added with new address
			return false
		}
		// Entity exists - verify it's on a live node (not node2)
		relocatedPID, err := node1.ActorOf(ctx, entityID)
		if err != nil || relocatedPID == nil {
			return false
		}

		// If the entity is local, it's definitely been relocated to this node
		if relocatedPID.IsLocal() {
			return true
		}

		// Critical check: entity must have a NEW address (not node2's address)
		// If it still has node2's address, relocation hasn't happened yet
		return relocatedPID.Path().HostPort() != node2Address
	}, 2*time.Minute, time.Second, "Entity %s should be relocated from node2 (was %s) to a live node", entityID, node2Address)

	// Verify the relocated entity is reachable and has the expected state.
	// Retry to handle transient propagation delays.
	require.Eventually(t, func() bool {
		sender, err := node1.ActorOf(ctx, "node1-entity-1")
		if err != nil || sender == nil {
			return false
		}
		response, err := sender.SendSync(ctx, entityID, new(testpb.GetAccount), 10*time.Second)
		if err != nil {
			return false
		}
		account, ok := response.(*testpb.Account)
		return ok && account.GetAccountBalance() == 600
	}, 30*time.Second, time.Second, "Relocated entity %s should be reachable with balance 600", entityID)

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRelocationWithDependency(t *testing.T) {
	// create a context
	ctx := context.TODO()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start a system cluster
	node1, sd1 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	dependencyID := "dependency"
	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		entityID := fmt.Sprintf("node1-actor-%d", j)
		// create the dependency
		dependency := NewMockDependency(dependencyID, entityID, "email")
		pid, err := node1.Spawn(ctx, entityID, NewMockActor(), WithDependencies(dependency), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		entityID := fmt.Sprintf("node2-actor-%d", j)
		// create the dependency
		dependency := NewMockDependency(dependencyID, entityID, "email")
		pid, err := node2.Spawn(ctx, entityID, NewMockActor(), WithDependencies(dependency), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	// Verify actor is on node2 before shutdown
	actorName := "node2-actor-1"
	node2Address := net.JoinHostPort(node2.Host(), strconv.Itoa(node2.Port()))
	actorPID, err := node1.ActorOf(ctx, actorName)
	require.NoError(t, err)
	require.NotNil(t, actorPID)
	require.Equal(t, node2Address, actorPID.Path().HostPort(), "Actor %s should be on node2 before shutdown", actorName)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Allow time for the cluster to detect node2 leaving and start relocation
	pause.For(2 * time.Second)

	// Wait for relocation - verify actor exists and is on a live node (not node2)
	// The relocation process:
	// 1. Node2 shutdown: cleanupCluster removes actors from cluster map (synchronous)
	// 2. NodeLeft event is emitted and processed by leader
	// 3. Leader fetches peer state from cluster store and enqueues for relocation
	// 4. Relocator processes: recreateLocally removes actor from cluster map, then spawns locally
	// 5. New actor is registered in cluster map with NEW address (via putActorOnCluster)
	//
	// The test must verify the actor is relocated (has new address), not just that it exists.
	// During relocation, the actor may temporarily not exist (removed but not yet re-added),
	// so we accept either: actor doesn't exist (relocation in progress) OR actor exists with new address (relocation complete).
	// We reject: actor exists with old address (relocation hasn't started or failed).
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, actorName)
		if err != nil {
			return false
		}
		if !exists {
			// Actor doesn't exist - this is OK during relocation (removed but not yet re-added)
			// We'll keep checking until it's re-added with new address
			return false
		}
		// Actor exists - verify it's on a live node (not node2)
		relocatedPID, err := node1.ActorOf(ctx, actorName)
		if err != nil || relocatedPID == nil {
			return false
		}
		// Critical check: actor must have a NEW address (not node2's address)
		// If it still has node2's address, relocation hasn't happened yet
		return relocatedPID.Path().HostPort() != node2Address
	}, 2*time.Minute, 500*time.Millisecond, "Actor %s should be relocated from node2 (was %s) to a live node", actorName, node2Address)

	// Verify the relocated actor is reachable and has the correct dependencies.
	// Retry to handle transient propagation delays after relocation.
	require.Eventually(t, func() bool {
		pid, err := node1.ActorOf(ctx, actorName)
		if err != nil || pid == nil {
			return false
		}
		deps := pid.Dependencies()
		if len(deps) != 1 {
			return false
		}
		dep := pid.Dependency(dependencyID)
		if dep == nil {
			return false
		}
		mockdep, ok := dep.(*MockDependency)
		return ok && mockdep.Username == actorName && mockdep.Email == "email"
	}, 30*time.Second, time.Second, "Relocated actor %s should have correct dependencies", actorName)

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, sd1.Close())
	srv.Shutdown()
}

func TestRelocationIssue781(t *testing.T) {
	// reference: https://github.com/Tochemey/goakt/issues/781
	// create a context
	ctx := t.Context()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start a system cluster
	node1, sd1 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor-1%d", j)
		pid, err := node1.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 5; j++ {
		actorName := fmt.Sprintf("Actor-2%d", j)
		pid, err := node2.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor-3%d", j)
		pid, err := node3.Spawn(ctx, actorName, NewMockActor())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	// let stop actor Actor-21 on node2
	actorName := "Actor-21"
	err := node2.Kill(ctx, actorName)
	require.NoError(t, err)

	pause.For(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Wait for the cluster to process node2's departure and clean up stale entries.
	// After node2 shuts down, cleanupCluster removes its actors from the cluster store.
	// Actor-21 was killed before shutdown, so it should not be in the peer state
	// and must not be relocated.
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, actorName)
		if err != nil {
			return false
		}
		return !exists
	}, 2*time.Minute, time.Second, "Killed actor %s should not exist after node2 departure", actorName)

	sender, err := node1.ActorOf(ctx, "Actor-11")
	require.NoError(t, err)
	require.NotNil(t, sender)

	err = sender.SendAsync(ctx, actorName, new(testpb.TestSend))
	require.Error(t, err)
	require.ErrorIs(t, err, errors.ErrActorNotFound)

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

// nolint
func TestGrainsRelocation(t *testing.T) {
	// create a context
	ctx := t.Context()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start a system cluster
	node1, sd1 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	for j := range 4 {
		identity, err := node1.GrainIdentity(ctx, fmt.Sprintf("Grain-1%d", j), func(ctx context.Context) (Grain, error) {
			return NewMockGrain(), nil
		})
		require.NotNil(t, identity)
		require.NoError(t, err)
		message := new(testpb.TestSend)
		err = node1.TellGrain(ctx, identity, message)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	for j := range 5 {
		identity, err := node2.GrainIdentity(ctx, fmt.Sprintf("Grain-2%d", j), func(ctx context.Context) (Grain, error) {
			return NewMockGrain(), nil
		})
		require.NotNil(t, identity)
		require.NoError(t, err)
		message := new(testpb.TestSend)
		err = node2.TellGrain(ctx, identity, message)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	for j := range 4 {
		identity, err := node3.GrainIdentity(ctx, fmt.Sprintf("Grain-3%d", j), func(ctx context.Context) (Grain, error) {
			return NewMockGrain(), nil
		})
		require.NotNil(t, identity)
		require.NoError(t, err)
		message := new(testpb.TestSend)
		err = node3.TellGrain(ctx, identity, message)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Wait for each relocated grain to become reachable on a surviving node.
	// On CI, relocation can take considerably longer than 1 minute, so we
	// poll with require.Eventually instead of a fixed sleep.
	type grainCase struct {
		node ActorSystem
		name string
	}
	grainCases := []grainCase{
		{node3, "Grain-20"},
		{node1, "Grain-21"},
		{node3, "Grain-22"},
		{node1, "Grain-23"},
		{node1, "Grain-24"},
	}
	for _, gc := range grainCases {
		require.Eventually(t, func() bool {
			identity, err := gc.node.GrainIdentity(ctx, gc.name, func(ctx context.Context) (Grain, error) {
				return NewMockGrain(), nil
			})
			if err != nil {
				return false
			}
			return gc.node.TellGrain(ctx, identity, new(testpb.TestSend)) == nil
		}, 2*time.Minute, time.Second, "grain %s should be accessible after relocation", gc.name)
	}

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

// nolint
func TestPersistenceGrainsRelocation(t *testing.T) {
	// create a context
	ctx := t.Context()
	// start the NATS server
	srv := startNatsServer(t)

	// create the state store extension
	stateStoreExtension := NewMockExtension()

	// create and start a system cluster
	node1, sd1 := testNATs(t, srv.Addr().String(), withMockExtension(stateStoreExtension))
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testNATs(t, srv.Addr().String(), withMockExtension(stateStoreExtension))
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testNATs(t, srv.Addr().String(), withMockExtension(stateStoreExtension))
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	for j := range 4 {
		identity, err := node1.GrainIdentity(ctx, fmt.Sprintf("Grain-1%d", j), func(ctx context.Context) (Grain, error) {
			return NewMockPersistenceGrain(), nil
		})
		require.NotNil(t, identity)
		require.NoError(t, err)

		message := &testpb.CreateAccount{
			AccountBalance: 500.00,
		}
		err = node1.TellGrain(ctx, identity, message)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	for j := range 5 {
		identity, err := node2.GrainIdentity(ctx, fmt.Sprintf("Grain-2%d", j), func(ctx context.Context) (Grain, error) {
			return NewMockPersistenceGrain(), nil
		})
		require.NotNil(t, identity)
		require.NoError(t, err)
		message := &testpb.CreateAccount{
			AccountBalance: 500.00,
		}
		err = node2.TellGrain(ctx, identity, message)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	for j := range 4 {
		identity, err := node3.GrainIdentity(ctx, fmt.Sprintf("Grain-3%d", j), func(ctx context.Context) (Grain, error) {
			return NewMockPersistenceGrain(), nil
		})
		require.NotNil(t, identity)
		require.NoError(t, err)
		message := &testpb.CreateAccount{
			AccountBalance: 500.00,
		}
		err = node3.TellGrain(ctx, identity, message)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// For each persistence grain that was on node2, we:
	//   1. Poll with require.Eventually until the grain is reachable and shows the
	//      pre-relocation balance (500). CreditAccount is not idempotent, so we must
	//      not send it until we are sure the grain is live on a surviving node.
	//   2. Then credit once with a generous timeout to account for grain activation
	//      latency on CI.
	type pgCase struct {
		node ActorSystem
		name string
	}
	pgCases := []pgCase{
		{node3, "Grain-20"},
		{node1, "Grain-21"},
		{node3, "Grain-22"},
		{node1, "Grain-23"},
		{node1, "Grain-24"},
	}
	for _, gc := range pgCases {
		// Step 1: wait until grain is accessible with its pre-relocation balance.
		require.Eventually(t, func() bool {
			identity, err := gc.node.GrainIdentity(ctx, gc.name, func(ctx context.Context) (Grain, error) {
				return NewMockPersistenceGrain(), nil
			})
			if err != nil {
				return false
			}
			resp, err := gc.node.AskGrain(ctx, identity, new(testpb.GetAccount), 5*time.Second)
			if err != nil {
				return false
			}
			account, ok := resp.(*testpb.Account)
			return ok && account.GetAccountBalance() == 500
		}, 2*time.Minute, time.Second, "grain %s should be relocated and readable with balance 500", gc.name)

		// Step 2: credit exactly once and verify the final balance.
		identity, err := gc.node.GrainIdentity(ctx, gc.name, func(ctx context.Context) (Grain, error) {
			return NewMockPersistenceGrain(), nil
		})
		require.NotNil(t, identity)
		require.NoError(t, err)
		response, err := gc.node.AskGrain(ctx, identity, &testpb.CreditAccount{Balance: 500.00}, time.Minute)
		require.NoError(t, err)
		require.NotNil(t, response)
		actual := response.(*testpb.Account)
		require.EqualValues(t, 1000.00, actual.GetAccountBalance())
	}

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

// nolint
func TestGrainsWithDependenciesRelocation(t *testing.T) {
	// create a context
	ctx := t.Context()
	// start the NATS server
	srv := startNatsServer(t)

	// create and start a system cluster
	node1, sd1 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testNATs(t, srv.Addr().String())
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	dependencyID := "dependency"

	for j := range 4 {
		name := fmt.Sprintf("Grain-1%d", j)
		email := fmt.Sprintf("email1%d", j)
		dependency := NewMockDependency(dependencyID, name, email)
		identity, err := node1.GrainIdentity(ctx, name, func(ctx context.Context) (Grain, error) {
			return NewMockGrain(), nil
		}, WithGrainDependencies(dependency))
		require.NotNil(t, identity)
		require.NoError(t, err)
		message := new(testpb.TestSend)
		err = node1.TellGrain(ctx, identity, message)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	for j := range 5 {
		name := fmt.Sprintf("Grain-2%d", j)
		email := fmt.Sprintf("email2%d", j)
		dependency := NewMockDependency(dependencyID, name, email)
		identity, err := node2.GrainIdentity(ctx, name, func(ctx context.Context) (Grain, error) {
			return NewMockGrain(), nil
		}, WithGrainDependencies(dependency))
		require.NotNil(t, identity)
		require.NoError(t, err)
		message := new(testpb.TestSend)
		err = node2.TellGrain(ctx, identity, message)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	for j := range 4 {
		name := fmt.Sprintf("Grain-3%d", j)
		email := fmt.Sprintf("email3%d", j)
		dependency := NewMockDependency(dependencyID, name, email)
		identity, err := node3.GrainIdentity(ctx, name, func(ctx context.Context) (Grain, error) {
			return NewMockGrain(), nil
		}, WithGrainDependencies(dependency))
		require.NotNil(t, identity)
		require.NoError(t, err)
		message := new(testpb.TestSend)
		err = node3.TellGrain(ctx, identity, message)
		require.NoError(t, err)
	}

	pause.For(time.Second)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Wait for each relocated grain (with dependencies) to become reachable on a
	// surviving node. A fixed sleep is unreliable on CI, so we poll instead.
	type gdCase struct {
		node       ActorSystem
		name       string
		email      string
		dependency string
	}
	gdCases := []gdCase{
		{node3, "Grain-20", "email20", dependencyID},
		{node1, "Grain-21", "email21", dependencyID},
		{node3, "Grain-22", "email22", dependencyID},
		{node1, "Grain-23", "email23", dependencyID},
		{node1, "Grain-24", "email24", dependencyID},
	}
	for _, gc := range gdCases {
		require.Eventually(t, func() bool {
			identity, err := gc.node.GrainIdentity(ctx, gc.name, func(ctx context.Context) (Grain, error) {
				return NewMockGrain(), nil
			}, WithGrainDependencies(NewMockDependency(gc.dependency, gc.name, gc.email)))
			if err != nil {
				return false
			}
			return gc.node.TellGrain(ctx, identity, new(testpb.TestSend)) == nil
		}, 2*time.Minute, time.Second, "grain %s should be accessible after relocation", gc.name)
	}

	assert.NoError(t, node1.Stop(ctx))
	assert.NoError(t, node3.Stop(ctx))
	assert.NoError(t, sd1.Close())
	assert.NoError(t, sd3.Close())
	srv.Shutdown()
}

func TestRelocationWithConsulProvider(t *testing.T) {
	// create a context
	ctx := t.Context()
	agent, ready := startConsulAgent(t)
	<-ready

	endpoint, err := agent.ApiEndpoint(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, endpoint)

	// create and start a system cluster
	node1, sd1 := testConsul(t, endpoint)
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testConsul(t, endpoint)
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testConsul(t, endpoint)
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor1%d", j)
		pid, err := node1.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(300 * time.Millisecond) // cluster sync interval is 300ms

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor2%d", j)
		pid, err := node2.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(300 * time.Millisecond)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor3%d", j)
		pid, err := node3.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(300 * time.Millisecond)

	// Verify actor is on node2 before shutdown
	actorName := "Actor21"
	node2Address := net.JoinHostPort(node2.Host(), strconv.Itoa(node2.Port()))
	actorPID, err := node1.ActorOf(ctx, actorName)
	require.NoError(t, err)
	require.NotNil(t, actorPID)
	require.Equal(t, node2Address, actorPID.Path().HostPort(), "Actor %s should be on node2 before shutdown", actorName)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Wait for relocation - verify actor exists and is on a live node (not node2)
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, actorName)
		if err != nil || !exists {
			return false
		}
		relocatedPID, err := node1.ActorOf(ctx, actorName)
		if err != nil || relocatedPID == nil {
			return false
		}
		return relocatedPID.Path().HostPort() != node2Address
	}, 4*time.Minute, 500*time.Millisecond, "Actor %s should be relocated from node2 to a live node", actorName)

	sender, err := node1.ActorOf(ctx, "Actor11")
	require.NoError(t, err)
	require.NotNil(t, sender)

	// let us access some of the node2 actors from node 1 and  node 3
	err = sender.SendAsync(ctx, actorName, new(testpb.TestSend))
	require.NoError(t, err)

	require.NoError(t, node1.Stop(ctx))
	require.NoError(t, node3.Stop(ctx))
	require.NoError(t, sd1.Close())
	require.NoError(t, sd3.Close())
}

func TestRelocationWithSelfManagedProvider(t *testing.T) {
	ctx := t.Context()
	broadcastPort := dynaport.Get(1)[0]

	node1, sd1 := testSelfManaged(t, broadcastPort)
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	node2, sd2 := testSelfManaged(t, broadcastPort)
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	node3, sd3 := testSelfManaged(t, broadcastPort)
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// wait for discovery and cluster formation (UDP broadcast/multicast on loopback)
	// allow up to 15s on slower systems (e.g. macOS)
	deadline := time.Now().Add(15 * time.Second)
	var formed bool
	for time.Now().Before(deadline) {
		peers, err := node1.Peers(ctx, 500*time.Millisecond)
		if err == nil && len(peers) >= 2 {
			formed = true
			break
		}
		pause.For(500 * time.Millisecond)
	}
	if !formed {
		t.Skip("selfmanaged discovery did not form cluster on loopback (expected on some systems)")
	}

	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor1%d", j)
		pid, err := node1.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(300 * time.Millisecond)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor2%d", j)
		pid, err := node2.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(300 * time.Millisecond)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor3%d", j)
		pid, err := node3.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(300 * time.Millisecond)

	// Verify actor is on node2 before shutdown
	actorName := "Actor21"
	node2Address := net.JoinHostPort(node2.Host(), strconv.Itoa(node2.Port()))
	actorPID, err := node1.ActorOf(ctx, actorName)
	require.NoError(t, err)
	require.NotNil(t, actorPID)
	require.Equal(t, node2Address, actorPID.Path().HostPort(), "Actor %s should be on node2 before shutdown", actorName)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Wait for relocation - verify actor exists and is on a live node (not node2)
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, actorName)
		if err != nil || !exists {
			return false
		}
		relocatedPID, err := node1.ActorOf(ctx, actorName)
		if err != nil || relocatedPID == nil {
			return false
		}
		return relocatedPID.Path().HostPort() != node2Address
	}, 4*time.Minute, 500*time.Millisecond, "Actor %s should be relocated from node2 to a live node", actorName)

	sender, err := node1.ActorOf(ctx, "Actor11")
	require.NoError(t, err)
	require.NotNil(t, sender)

	err = sender.SendAsync(ctx, actorName, new(testpb.TestSend))
	require.NoError(t, err)

	require.NoError(t, node1.Stop(ctx))
	require.NoError(t, node3.Stop(ctx))
	require.NoError(t, sd1.Close())
	require.NoError(t, sd3.Close())
}

func TestRelocationWithEtcdProvider(t *testing.T) {
	// create a context
	ctx := t.Context()
	cluster, ready := startEtcdCluster(t)
	<-ready

	endpoints, err := cluster.ClientEndpoints(ctx)
	require.NoError(t, err)

	// create and start a system cluster
	node1, sd1 := testEtcd(t, endpoints[0])
	require.NotNil(t, node1)
	require.NotNil(t, sd1)

	// create and start a system cluster
	node2, sd2 := testEtcd(t, endpoints[0])
	require.NotNil(t, node2)
	require.NotNil(t, sd2)

	// create and start a system cluster
	node3, sd3 := testEtcd(t, endpoints[0])
	require.NotNil(t, node3)
	require.NotNil(t, sd3)

	// let us create 4 actors on each node
	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor1%d", j)
		pid, err := node1.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor2%d", j)
		pid, err := node2.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	pause.For(time.Second)

	for j := 1; j <= 4; j++ {
		actorName := fmt.Sprintf("Actor3%d", j)
		pid, err := node3.Spawn(ctx, actorName, NewMockActor(), WithLongLived())
		require.NoError(t, err)
		require.NotNil(t, pid)
	}

	// Allow cluster to sync and persist peer state to etcd before shutdown
	pause.For(5 * time.Second)

	// Verify actor is on node2 before shutdown
	actorName := "Actor21"
	node2Address := net.JoinHostPort(node2.Host(), strconv.Itoa(node2.Port()))
	node2PeersAddr := node2.PeersAddress()
	actorPID, err := node1.ActorOf(ctx, actorName)
	require.NoError(t, err)
	require.NotNil(t, actorPID)
	require.Equal(t, node2Address, actorPID.Path().HostPort(), "Actor %s should be on node2 before shutdown", actorName)

	// take down node2
	require.NoError(t, node2.Stop(ctx))
	require.NoError(t, sd2.Close())

	// Give the cluster time to detect the node failure and start relocation (etcd can be slower)
	pause.For(5 * time.Second)

	// Wait for cluster rebalancing - verify actor relocation actually occurred.
	// The relocation process:
	// 1. Node2 shutdown: cleanupCluster removes actors from cluster map (synchronous)
	// 2. NodeLeft event is emitted and processed by leader
	// 3. Leader fetches peer state from cluster store and enqueues for relocation
	// 4. Relocator processes: recreateLocally removes actor from cluster map, then spawns locally
	// 5. New actor is registered in cluster map with NEW address (via putActorOnCluster)
	//
	// The test must verify the actor is relocated (has new address), not just that it exists.
	// During relocation, the actor may temporarily not exist (removed but not yet re-added),
	// so we accept either: actor doesn't exist (relocation in progress) OR actor exists with new address (relocation complete).
	// We reject: actor exists with old address (relocation hasn't started or failed).
	//
	// Etcd has higher latency than NATS/Consul; use longer timeout and check both address formats.
	require.Eventually(t, func() bool {
		exists, err := node1.ActorExists(ctx, actorName)
		if err != nil {
			return false
		}
		if !exists {
			// Actor doesn't exist - this is OK during relocation (removed but not yet re-added)
			// We'll keep checking until it's re-added with new address
			return false
		}
		// Actor exists - verify it's on a live node (not node2)
		relocatedPID, err := node1.ActorOf(ctx, actorName)
		if err != nil || relocatedPID == nil {
			return false
		}
		addr := relocatedPID.Path().HostPort()
		// Critical check: actor must have a NEW address (not node2's remoting or peers address)
		return addr != node2Address && addr != node2PeersAddr
	}, 4*time.Minute, 500*time.Millisecond, "Actor %s should be relocated from node2 (was %s) to a live node", actorName, node2Address)

	sender, err := node1.ActorOf(ctx, "Actor11")
	require.NoError(t, err)
	require.NotNil(t, sender)

	// let us access some of the node2 actors from node 1 and  node 3
	err = sender.SendAsync(ctx, actorName, new(testpb.TestSend))
	require.NoError(t, err)

	require.NoError(t, node1.Stop(ctx))
	require.NoError(t, node3.Stop(ctx))
	require.NoError(t, sd1.Close())
	require.NoError(t, sd3.Close())
}
