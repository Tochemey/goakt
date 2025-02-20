/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package actors

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"time"

	"github.com/flowchartsman/retry"

	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/types"
	"github.com/tochemey/goakt/v3/log"
)

// clusterSingletonManager is a system actor that manages the lifecycle of singleton actors
// in the cluster. This actor must be started when cluster mode is enabled in all nodes
// before any singleton actor is created.
type clusterSingletonManager struct {
	logger  log.Logger
	pid     *PID
	cluster cluster.Interface
}

// ensure clusterSingleton implements the Actor interface
var _ Actor = (*clusterSingletonManager)(nil)

// newClusterSingletonManager creates a new cluster singleton actor.
func newClusterSingletonManager() Actor {
	return &clusterSingletonManager{}
}

// PreStart implements the pre-start hook.
func (x *clusterSingletonManager) PreStart(context.Context) error {
	return nil
}

// Receive handles messages received by the cluster singleton.
func (x *clusterSingletonManager) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.PostStart:
		x.handlePostStart(ctx)
	default:
		ctx.Unhandled()
	}
}

// PostStop implements the post-stop hook.
func (x *clusterSingletonManager) PostStop(context.Context) error {
	x.logger.Infof("%s stopped successfully", x.pid.Name())
	return nil
}

// handlePostStart handles PostStart message
func (x *clusterSingletonManager) handlePostStart(ctx *ReceiveContext) {
	x.pid = ctx.Self()
	x.logger = ctx.Logger()
	x.cluster = ctx.ActorSystem().getCluster()
	x.logger.Infof("%s started successfully", x.pid.Name())
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

	var err error
	actorName := x.reservedName(singletonManagerType)
	x.singletonManager, err = x.configPID(ctx,
		actorName,
		newClusterSingletonManager(),
		WithSupervisor(
			NewSupervisor(
				WithStrategy(OneForOneStrategy),
				WithDirective(PanicError{}, RestartDirective),
				WithDirective(InternalError{}, RestartDirective),
				WithDirective(&runtime.PanicNilError{}, RestartDirective),
			),
		),
	)
	if err != nil {
		return fmt.Errorf("actor=%s failed to start cluster singleton manager: %w", actorName, err)
	}

	// the singletonManager is a child actor of the system guardian
	_ = x.actors.AddNode(x.systemGuardian, x.singletonManager)
	return nil
}

func (x *actorSystem) spawnSingletonOnLeader(ctx context.Context, cl cluster.Interface, name string, actor Actor) error {
	peers, err := cl.Peers(ctx)
	if err != nil {
		return fmt.Errorf("failed to spawn singleton actor: %w", err)
	}

	// find the oldest node in the cluster
	var leader *cluster.Peer
	for _, peer := range peers {
		if peer.Coordinator {
			leader = peer
			break
		}
	}

	// TODO: instead of using the cache add a method in the cluster to fetch peer info
	if leader == nil {
		return ErrLeaderNotFound
	}

	var peerState *internalpb.PeerState

	// this is expected to be quick
	retrier := retry.NewRetrier(3, 100*time.Millisecond, 300*time.Millisecond)
	err = retrier.RunContext(ctx, func(ctx context.Context) error {
		peerState, err = x.getPeerStateFromStore(leader.PeerAddress())
		if err != nil {
			if errors.Is(err, ErrPeerNotFound) {
				return err
			}

			// here we stop the retry because there is an error
			return retry.Stop(err)
		}
		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to spawn singleton actor: %w", err)
	}

	var (
		actorType = types.Name(actor)
		host      = peerState.GetHost()
		port      = int(peerState.GetRemotingPort())
	)

	return x.remoting.RemoteSpawn(ctx, host, port, name, actorType, true)
}
