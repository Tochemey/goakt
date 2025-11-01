/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package actor

import (
	"context"
	"fmt"
	"math/rand/v2"
	"runtime"
	"sort"
	"strings"

	"github.com/google/uuid"

	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/internal/pointer"
	"github.com/tochemey/goakt/v3/internal/registry"
	"github.com/tochemey/goakt/v3/remote"
)

// Spawn creates and starts a new actor in the local actor system.
//
// The actor will be registered under the given `name`, allowing other actors
// or components to send messages to it using the returned *PID. If an actor
// with the same name already exists in the local system, an error will be returned.
//
// Parameters:
//   - ctx: A context used to control cancellation and timeouts during the spawn process.
//   - name: A unique identifier for the actor within the local actor system.
//   - actor: An instance implementing the Actor interface, representing the behavior and lifecycle of the actor.
//   - opts: Optional SpawnOptions to customize the actor's behavior (e.g., dependency, mailbox, supervisor strategy).
//
// Returns:
//   - *PID: A pointer to the Process ID of the spawned actor, used for message passing.
//   - error: An error if the actor could not be spawned (e.g., name conflict, invalid configuration).
//
// Example:
//
//	pid, err := system.Spawn(ctx, "user-service", NewUserActor())
//	if err != nil {
//	    log.Fatalf("Failed to spawn actor: %v", err)
//	}
//
// Note: Actors spawned using this method are confined to the local actor system.
// For distributed scenarios, use a SpawnOn method.
func (x *actorSystem) Spawn(ctx context.Context, name string, actor Actor, opts ...SpawnOption) (*PID, error) {
	if !x.Running() {
		return nil, gerrors.ErrActorSystemNotStarted
	}

	// check some preconditions
	if err := x.checkSpawnPreconditions(ctx, name, actor, false, nil); err != nil {
		return nil, err
	}

	actorAddress := x.actorAddress(name)
	pidNode, exist := x.actors.node(actorAddress.String())
	if exist {
		pid := pidNode.value()
		if pid.IsRunning() {
			return pid, nil
		}
	}

	pid, err := x.configPID(ctx, name, actor, opts...)
	if err != nil {
		return nil, err
	}

	x.actorsCounter.Inc()
	// add the given actor to the tree and supervise it
	guardian := x.getUserGuardian()
	_ = x.actors.addNode(guardian, pid)
	x.actors.addWatcher(pid, x.deathWatch)
	return pid, x.putActorOnCluster(pid)
}

// SpawnNamedFromFunc creates an actor with the given receive function and provided name. One can set the PreStart and PostStop lifecycle hooks
// in the given optional options
func (x *actorSystem) SpawnNamedFromFunc(ctx context.Context, name string, receiveFunc ReceiveFunc, opts ...FuncOption) (*PID, error) {
	if !x.Running() {
		return nil, gerrors.ErrActorSystemNotStarted
	}

	config := newFuncConfig(opts...)
	actor := newFuncActor(name, receiveFunc, config)

	// check some preconditions
	if err := x.checkSpawnPreconditions(ctx, name, actor, false, nil); err != nil {
		return nil, err
	}

	actorAddress := x.actorAddress(name)
	pidNode, exist := x.actors.node(actorAddress.String())
	if exist {
		pid := pidNode.value()
		if pid.IsRunning() {
			return pid, nil
		}
	}

	pid, err := x.configPID(ctx, name, actor, WithMailbox(config.mailbox), WithRelocationDisabled())
	if err != nil {
		return nil, err
	}

	x.actorsCounter.Inc()
	_ = x.actors.addNode(x.userGuardian, pid)
	x.actors.addWatcher(pid, x.deathWatch)
	return pid, x.putActorOnCluster(pid)
}

// SpawnOn creates and starts an actor, either locally or on a remote node,
// depending on the configuration of the actor system.
//
// In cluster mode, the actor may be spawned on any node in the cluster
// based on the specified placement strategy. Supported strategies include:
//   - RoundRobin: Distributes actors evenly across available nodes.
//   - Random: Choose a node at random.
//   - Local: Ensures that the actor is created on the local node.
//   - LeastLoad: Places the actor on the node with the fewest active actors.
//
// In non-cluster mode, the actor is created on the local actor system
// just like with the standard `Spawn` function.
//
// Unlike `Spawn`, `SpawnOn` does not return a PID immediately. To interact with
// the spawned actor, use the `ActorOf` method to resolve its PID or Address after it has been
// successfully created.
//
// Parameters:
//   - ctx: A context used to control cancellation and timeouts during the spawn process.
//   - name: A globally unique name for the actor in the cluster.
//   - actor: An instance implementing the Actor interface.
//   - opts: Optional SpawnOptions, such as placement strategy or dependencies, mailbox, supervisor strategy.
//
// Returns:
//   - error: An error if the actor could not be spawned (e.g., name conflict,
//     network failure, or misconfiguration).
//
// Example:
//
//	err := system.SpawnOn(ctx, "actor-1", NewCartActor(), WithPlacement(Random))
//	if err != nil {
//	    log.Fatalf("Failed to spawn actor: %v", err)
//	}
//
// Note: The created actor used the default mailbox set during the creation of the actor system.
func (x *actorSystem) SpawnOn(ctx context.Context, name string, actor Actor, opts ...SpawnOption) error {
	if !x.Running() {
		return gerrors.ErrActorSystemNotStarted
	}

	// check some preconditions
	if err := x.checkSpawnPreconditions(ctx, name, actor, false, nil); err != nil {
		return err
	}

	config := newSpawnConfig(opts...)
	if !x.InCluster() {
		_, err := x.Spawn(ctx, name, actor, opts...)
		return err
	}

	peers, err := x.cluster.Peers(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch cluster nodes: %w", err)
	}

	peers, err = x.filterPeersByRole(peers, config.role)
	if err != nil {
		return err
	}

	if len(peers) == 0 {
		_, err := x.Spawn(ctx, name, actor, opts...)
		return err
	}

	peer, err := x.selectPlacementPeer(ctx, peers, config.placement)
	if err != nil {
		return err
	}

	// spawn the actor on the local node
	if peer == nil {
		_, err := x.Spawn(ctx, name, actor, opts...)
		return err
	}

	return x.remoting.RemoteSpawn(ctx, peer.Host, peer.RemotingPort, &remote.SpawnRequest{
		Name:                name,
		Kind:                registry.Name(actor),
		Singleton:           false,
		Relocatable:         config.relocatable,
		PassivationStrategy: config.passivationStrategy,
		Dependencies:        config.dependencies,
		EnableStashing:      config.enableStash,
	})
}

// SpawnFromFunc creates an actor with the given receive function.
func (x *actorSystem) SpawnFromFunc(ctx context.Context, receiveFunc ReceiveFunc, opts ...FuncOption) (*PID, error) {
	return x.SpawnNamedFromFunc(ctx, uuid.NewString(), receiveFunc, opts...)
}

// SpawnRouter creates and initializes a new router actor with the specified options.
//
// A router is a special type of actor designed to distribute messages of the same type
// across a group of routee actors. This enables concurrent message processing and improves
// throughput by leveraging multiple actors in parallel.
//
// Each individual actor in the group (a "routee") processes only one message at a time,
// preserving the actor model’s single-threaded execution semantics.
//
// Note: Routers are **not** redeployable. If the host node of a router leaves the cluster
// or crashes, the router and its routees will not be automatically re-spawned elsewhere.
//
// Use routers when you need to fan out work across multiple workers while preserving
// the isolation and safety guarantees of the actor model.
func (x *actorSystem) SpawnRouter(ctx context.Context, poolSize int, routeesKind Actor, opts ...RouterOption) (*PID, error) {
	router := newRouter(poolSize, routeesKind, x.logger, opts...)
	routerName := x.reservedName(routerType)
	return x.Spawn(ctx, routerName, router,
		WithRelocationDisabled(),
		asSystem(),
		WithSupervisor(
			NewSupervisor(WithAnyErrorDirective(ResumeDirective)),
		))
}

// SpawnSingleton creates a singleton actor in the system.
//
// A singleton actor is instantiated when cluster mode is enabled.
// A singleton actor like any other actor is created only once within the system and in the cluster.
// A singleton actor is created with the default supervisor strategy and directive.
// A singleton actor once created lives throughout the lifetime of the given actor system.
// One cannot create a child actor for a singleton actor.
//
// The cluster singleton is automatically started on the oldest node in the cluster.
// When the oldest node leaves the cluster unexpectedly, the singleton is restarted on the new oldest node.
// This is useful for managing shared resources or coordinating tasks that should be handled by a single actor.
func (x *actorSystem) SpawnSingleton(ctx context.Context, name string, actor Actor, opts ...ClusterSingletonOption) error {
	if !x.Running() {
		return gerrors.ErrActorSystemNotStarted
	}

	if !x.InCluster() {
		return gerrors.ErrClusterDisabled
	}

	cl := x.getCluster()

	config := newClusterSingletonConfig(opts...)
	role := strings.TrimSpace(pointer.Deref(config.Role(), ""))
	if role != "" {
		return x.spawnSingletonWithRole(ctx, cl, name, actor, role)
	}

	// only create the singleton actor on the oldest node in the cluster
	if !cl.IsLeader(ctx) {
		return x.spawnSingletonOnLeader(ctx, cl, name, actor)
	}

	return x.spawnSingletonOnLocal(ctx, name, actor, nil)
}

func (x *actorSystem) spawnSingletonWithRole(ctx context.Context, cl cluster.Cluster, name string, actor Actor, role string) error {
	// fetch all cluster members
	members, err := cl.Members(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch cluster members: %w", err)
	}

	filtered := make([]*cluster.Peer, 0, len(members))
	for _, peer := range members {
		if peer.HasRole(role) {
			filtered = append(filtered, peer)
		}
	}

	if len(filtered) == 0 {
		return fmt.Errorf("no cluster members found with role %s", role)
	}

	// find the oldest node in the filtered peers
	sort.Slice(filtered, func(i int, j int) bool {
		return filtered[i].CreatedAt < filtered[j].CreatedAt
	})

	leader := filtered[0]
	if leader.PeerAddress() != x.clusterNode.PeersAddress() {
		var (
			actorType = registry.Name(actor)
			host      = leader.Host
			port      = leader.RemotingPort
		)

		return x.remoting.RemoteSpawn(ctx, host, port, &remote.SpawnRequest{
			Name:      name,
			Kind:      actorType,
			Singleton: true,
			Role:      pointer.To(role),
		})
	}

	return x.spawnSingletonOnLocal(ctx, name, actor, pointer.To(role))
}

func (x *actorSystem) spawnSingletonOnLocal(ctx context.Context, name string, actor Actor, role *string) error {
	// check some preconditions
	if err := x.checkSpawnPreconditions(ctx, name, actor, true, role); err != nil {
		return err
	}

	pid, err := x.configPID(ctx, name, actor,
		WithLongLived(),
		withSingleton(),
		WithRole(pointer.Deref(role, "")),
		WithSupervisor(
			NewSupervisor(
				WithStrategy(OneForOneStrategy),
				WithDirective(&gerrors.PanicError{}, StopDirective),
				WithDirective(&gerrors.InternalError{}, StopDirective),
				WithDirective(&runtime.PanicNilError{}, StopDirective),
			),
		))
	if err != nil {
		return err
	}

	x.actorsCounter.Inc()
	// add the given actor to the tree and supervise it
	_ = x.actors.addNode(x.singletonManager, pid)
	x.actors.addWatcher(pid, x.deathWatch)
	return x.putActorOnCluster(pid)
}

func (x *actorSystem) selectPlacementPeer(ctx context.Context, peers []*cluster.Peer, placement SpawnPlacement) (*cluster.Peer, error) {
	switch placement {
	case Random:
		return peers[rand.IntN(len(peers))], nil //nolint:gosec
	case RoundRobin:
		x.spawnOnNext.Inc()
		n := x.spawnOnNext.Load()
		return peers[(int(n)-1)%len(peers)], nil
	case LeastLoad:
		return x.leastLoadedPeer(ctx, peers)
	default:
		return nil, nil
	}
}

func kindRole(kind, role string) string {
	return fmt.Sprintf("%s%s%s", kind, kindRoleSeparator, role)
}
