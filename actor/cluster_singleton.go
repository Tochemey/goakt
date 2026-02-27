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
	"time"

	"github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/pointer"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	sup "github.com/tochemey/goakt/v4/supervisor"
)

// clusterSingletonManager is a system actor that manages the lifecycle of singleton actors
// in the cluster. This actor must be started when cluster mode is enabled in all nodes
// before any singleton actor is created.
type clusterSingletonManager struct {
	logger  log.Logger
	pid     *PID
	cluster cluster.Cluster
}

// ensure clusterSingleton implements the Actor interface
var _ Actor = (*clusterSingletonManager)(nil)

// newClusterSingletonManager creates a new cluster singleton manager.
func newClusterSingletonManager() Actor {
	return &clusterSingletonManager{}
}

// PreStart implements the pre-start hook.
func (*clusterSingletonManager) PreStart(*Context) error {
	return nil
}

// Receive handles messages received by the cluster singleton.
func (x *clusterSingletonManager) Receive(ctx *ReceiveContext) {
	// handle PostStart message
	if _, ok := ctx.Message().(*PostStart); ok {
		x.handlePostStart(ctx)
	}
}

// PostStop implements the post-stop hook.
func (*clusterSingletonManager) PostStop(ctx *Context) error {
	ctx.ActorSystem().Logger().Infof("actor=%s stopped successfully", ctx.ActorName())
	return nil
}

// handlePostStart handles PostStart message
func (x *clusterSingletonManager) handlePostStart(ctx *ReceiveContext) {
	x.pid = ctx.Self()
	x.logger = ctx.Logger()
	x.cluster = ctx.ActorSystem().getCluster()
	x.logger.Infof("actor=%s started successfully", x.pid.Name())
}

// spawnSingletonManager creates the singleton manager actor
// this is a system actor that manages the lifecycle of singleton actors
// in the cluster. This actor must be started when cluster mode is enabled in all nodes
// before any singleton actor is created.
func (x *actorSystem) spawnSingletonManager(ctx context.Context) error {
	// only start the singleton manager when clustering is enabled
	if !x.clusterEnabled.Load() {
		return nil
	}

	actorName := reservedName(singletonManagerType)
	x.singletonManager, _ = x.configPID(ctx,
		actorName,
		newClusterSingletonManager(),
		asSystem(),
		WithLongLived(),
		WithSupervisor(
			sup.NewSupervisor(
				sup.WithStrategy(sup.OneForOneStrategy),
				sup.WithAnyErrorDirective(sup.RestartDirective),
			),
		),
	)

	// the singletonManager is a child actor of the system guardian
	return x.actors.addNode(x.systemGuardian, x.singletonManager)
}

func (x *actorSystem) spawnSingletonOnLeader(ctx context.Context, cl cluster.Cluster, name string, actor Actor, spawnTimeout, waitInterval time.Duration, retries int32) (*PID, error) {
	// spawnSingletonOnLeader resolves the cluster coordinator and ensures the singleton is spawned on it.
	//
	// Implementation notes:
	//   - We use Members (includes the local node) rather than Peers (excludes local) so callers can invoke
	//     SpawnSingleton from any node without requiring leader knowledge.
	//   - If the coordinator is the local node, we spawn locally to avoid a needless RemoteSpawn round-trip.
	peers, err := cl.Members(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to spawn singleton actor: %w", err)
	}

	// find the coordinator (leader) node in the cluster
	var leader *cluster.Peer
	for _, peer := range peers {
		if peer.Coordinator {
			leader = peer
			break
		}
	}

	if leader == nil {
		return nil, errors.ErrLeaderNotFound
	}

	// If the leader is the local node, spawn locally.
	localAddr := ""
	if x.clusterNode != nil {
		localAddr = x.clusterNode.PeersAddress()
	}

	if localAddr != "" && leader.PeerAddress() == localAddr {
		return x.spawnSingletonOnLocal(ctx, name, actor, nil, spawnTimeout, waitInterval, retries)
	}

	var (
		actorType = types.Name(actor)
		host      = leader.Host
		port      = leader.RemotingPort
	)

	addr, err := x.remoting.RemoteSpawn(ctx, host, port, &remote.SpawnRequest{
		Name: name,
		Kind: actorType,
		Singleton: &remote.SingletonSpec{
			SpawnTimeout: spawnTimeout,
			WaitInterval: waitInterval,
			MaxRetries:   retries,
		},
	})
	if err != nil {
		return nil, err
	}

	parsedAddr, err := address.Parse(pointer.Deref(addr, ""))
	if err != nil {
		return nil, fmt.Errorf("failed to parse address: %w", err)
	}

	return newRemotePID(parsedAddr, x.remoting), nil
}
