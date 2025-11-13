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

package actor

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"time"

	"connectrpc.com/connect"
	goset "github.com/deckarep/golang-set/v2"
	"go.akshayshah.org/connectproto"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/internal/compression/brotli"
	"github.com/tochemey/goakt/v3/internal/compression/zstd"
	"github.com/tochemey/goakt/v3/internal/http"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/internalpb/internalpbconnect"
	"github.com/tochemey/goakt/v3/internal/pointer"
	"github.com/tochemey/goakt/v3/remote"
)

// GrainIdentity retrieves or activates a Grain (virtual actor) identified by the given name.
//
// This method ensures that a Grain with the specified name exists in the system. If the Grain is not already active,
// it will be created using the provided factory function. Grains are virtual actors that are automatically managed
// and can be transparently activated or deactivated based on usage.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control.
//   - name: The unique name identifying the Grain.
//   - factory: A function that creates a new Grain instance if activation is required.
//   - opts: Optional configuration options for the Grain (e.g., activation timeout, retries).
//
// Returns:
//   - *GrainIdentity: The identity object representing the located or newly activated Grain.
//   - error: An error if the Grain could not be found, created, or activated.
//
// Note:
//   - This method abstracts away the details of Grain lifecycle management.
//   - Use this to obtain a reference to a Grain for message passing or further operations.
func (x *actorSystem) GrainIdentity(ctx context.Context, name string, factory GrainFactory, opts ...GrainOption) (*GrainIdentity, error) {
	if !x.started.Load() || x.isStopping() {
		return nil, gerrors.ErrActorSystemNotStarted
	}

	logger := x.logger

	grain, err := factory(ctx)
	if err != nil {
		return nil, err
	}

	identity := newGrainIdentity(grain, name)
	logger.Infof("activating grain (%s)...", identity.String())
	if err := identity.Validate(); err != nil {
		return nil, err
	}

	// make sure we don't interfere with system actors.
	if isSystemName(identity.Name()) {
		return nil, gerrors.NewErrReservedName(identity.String())
	}

	config := newGrainConfig(opts...)
	if err := config.Validate(); err != nil {
		return nil, err
	}

	if x.InCluster() {
		grainInfo, err := x.getCluster().GetGrain(ctx, identity.String())
		if err != nil {
			if !errors.Is(err, cluster.ErrGrainNotFound) {
				return nil, err
			}
		}

		if grainInfo != nil && !proto.Equal(grainInfo, new(internalpb.Grain)) {
			remoteClient := x.remoting.RemotingServiceClient(grainInfo.GetHost(), int(grainInfo.GetPort()))
			request := connect.NewRequest(&internalpb.RemoteActivateGrainRequest{
				Grain: grainInfo,
			})

			if _, err := remoteClient.RemoteActivateGrain(ctx, request); err != nil {
				return nil, err
			}
			return identity, nil
		}

		peer, err := x.findActivationPeer(ctx, config)
		if err != nil {
			return nil, err
		}

		if peer != nil && (peer.PeerAddress() != x.PeersAddress()) {
			pid := newGrainPID(identity, grain, x, config)
			grainInfo, err = pid.toWireGrain()
			if err != nil {
				return nil, err
			}

			// override the grain info with peer (host and remoting port)
			grainInfo.Host = peer.Host
			grainInfo.Port = int32(peer.RemotingPort)

			remoteClient := x.remoting.RemotingServiceClient(peer.Host, int(peer.RemotingPort))
			request := connect.NewRequest(&internalpb.RemoteActivateGrainRequest{
				Grain: grainInfo,
			})

			if _, err := remoteClient.RemoteActivateGrain(ctx, request); err != nil {
				return nil, err
			}
			return identity, nil
		}
	}

	pid, ok := x.grains.Get(identity.String())
	if !ok {
		pid = newGrainPID(identity, grain, x, config)
	}

	if !x.registry.Exists(grain) {
		x.registry.Register(grain)
	}

	if !pid.isActive() {
		if err := pid.activate(ctx); err != nil {
			return nil, err
		}
	}

	x.grains.Set(identity.String(), pid)
	return identity, x.putGrainOnCluster(pid)
}

// TellGrain sends an asynchronous message to a Grain (virtual actor) identified by the given identity.
//
// This method locates or activates the target Grain (locally or in the cluster) and delivers the provided
// protobuf message without waiting for a response. Use this for fire-and-forget scenarios where no reply is expected.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control.
//   - identity: The unique identity of the Grain.
//   - message: The protobuf message to send to the Grain.
//
// Returns:
//   - error: An error if the message could not be delivered or the system is not started.
func (x *actorSystem) TellGrain(ctx context.Context, identity *GrainIdentity, message proto.Message) error {
	if !x.started.Load() || x.isStopping() {
		return gerrors.ErrActorSystemNotStarted
	}

	// validate the identity
	if err := identity.Validate(); err != nil {
		return gerrors.NewErrInvalidGrainIdentity(err)
	}

	if x.InCluster() {
		return x.remoteTellGrain(ctx, identity, message, DefaultGrainRequestTimeout)
	}
	_, err := x.localSend(ctx, identity, message, DefaultGrainRequestTimeout, false)
	return err
}

// AskGrain sends a synchronous request message to a Grain (virtual actor) identified by the given identity.
//
// This method locates or activates the target Grain (locally or in the cluster), sends the provided
// protobuf message, and waits for a response or error. The request will block until a response is received,
// the context is canceled, or the timeout elapses.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control.
//   - identity: The unique identity of the Grain.
//   - message: The protobuf message to send to the Grain.
//   - timeout: The maximum duration to wait for a response.
//
// Returns:
//   - response: The response message from the Grain, if successful.
//   - error: An error if the request fails, times out, or the system is not started.
func (x *actorSystem) AskGrain(ctx context.Context, identity *GrainIdentity, message proto.Message, timeout time.Duration) (response proto.Message, err error) {
	if !x.started.Load() || x.isStopping() {
		return nil, gerrors.ErrActorSystemNotStarted
	}

	// validate the identity
	if err := identity.Validate(); err != nil {
		return nil, gerrors.NewErrInvalidGrainIdentity(err)
	}

	if x.InCluster() {
		return x.remoteAskGrain(ctx, identity, message, timeout)
	}
	return x.localSend(ctx, identity, message, timeout, true)
}

// Grains retrieves a list of all active Grains (virtual actors) in the system.
//
// Grains are virtual actors that are automatically managed by the actor system. This method returns a slice of
// GrainIdentity objects representing the currently active Grains. In cluster mode, it attempts to aggregate Grains
// across all nodes in the cluster; if the cluster request fails, only locally active Grains will be returned.
//
// Use this method with caution, as scanning for all Grains (especially in a large cluster) may impact system performance.
// The timeout parameter defines the maximum duration for cluster-based requests before they are terminated.
//
// Parameters:
//   - ctx: Context for cancellation and timeout control.
//   - timeout: The maximum duration to wait for cluster-based queries.
//
// Returns:
//   - []*GrainIdentity: A slice of GrainIdentity objects for all active Grains.
//
// Note:
//   - This method abstracts away the details of Grain lifecycle management.
//   - Use this to obtain references to all active Grains for monitoring, diagnostics, or administrative purposes.
func (x *actorSystem) Grains(ctx context.Context, timeout time.Duration) []*GrainIdentity {
	if !x.started.Load() || x.isStopping() {
		return nil
	}

	x.locker.Lock()
	ids := x.grains.Keys()
	x.locker.Unlock()
	uniques := goset.NewSet(ids...)

	if x.InCluster() {
		if grains, err := x.getCluster().Grains(ctx, timeout); err == nil {
			for _, grain := range grains {
				uniques.Add(grain.GetGrainId().GetValue())
			}
		}
	}

	identities := make([]*GrainIdentity, 0, uniques.Cardinality())
	for _, id := range uniques.ToSlice() {
		if identity, err := toIdentity(id); err == nil {
			identities = append(identities, identity)
		}
	}

	return identities
}

// RemoteAskGrain handles remote requests to a Grain from another node.
//
// It validates the request, creates or locates the target Grain, delivers the message,
// and returns the response or an error. Used internally for cluster communication.
//
// Parameters:
//   - ctx: context for cancellation and timeout control.
//   - request: RemoteAskGrainRequest containing target grain info and message.
//
// Returns:
//   - *internalpb.RemoteMessageGrainResponse: response containing the Grain's reply.
//   - error: error if the request fails or is invalid.
func (x *actorSystem) RemoteAskGrain(ctx context.Context, request *connect.Request[internalpb.RemoteAskGrainRequest]) (*connect.Response[internalpb.RemoteAskGrainResponse], error) {
	logger := x.logger
	msg := request.Msg

	// Remoting must be enabled
	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.ErrRemotingDisabled)
	}

	// Validate host and port
	host := msg.GetGrain().GetHost()
	port := msg.GetGrain().GetPort()
	if err := x.validateRemoteHost(host, port); err != nil {
		return nil, err
	}

	message, _ := msg.GetMessage().UnmarshalNew()
	timeout := msg.GetRequestTimeout()

	identity, err := toIdentity(msg.GetGrain().GetGrainId().GetValue())
	if err != nil {
		if errors.Is(err, gerrors.ErrInvalidGrainIdentity) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.Join(err, gerrors.ErrInvalidGrainIdentity))
	}

	if isSystemName(identity.Name()) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.NewErrReservedName(identity.String()))
	}

	reply, err := x.localSend(ctx, identity, message, timeout.AsDuration(), true)
	if err != nil {
		logger.Errorf("failed to create grain (%s) on [host=%s, port=%d]: reason: (%v)", identity.String(), msg.GetGrain().GetHost(), msg.GetGrain().GetPort(), err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	response, _ := anypb.New(reply)
	return connect.NewResponse(&internalpb.RemoteAskGrainResponse{Message: response}), nil
}

// RemoteTellGrain handles remote fire-and-forget messages to a Grain from another node.
//
// It validates the incoming request, locates or activates the target Grain, and delivers
// the provided message without waiting for a response. This is used internally for cluster
// communication to support asynchronous, one-way messaging between nodes.
//
// Parameters:
//   - ctx: context for cancellation and timeout control.
//   - request: RemoteTellGrainRequest containing the target Grain info and message.
//
// Returns:
//   - *internalpb.RemoteTellGrainResponse: an empty response indicating delivery.
//   - error: error if the request is invalid or delivery fails.
func (x *actorSystem) RemoteTellGrain(ctx context.Context, request *connect.Request[internalpb.RemoteTellGrainRequest]) (*connect.Response[internalpb.RemoteTellGrainResponse], error) {
	logger := x.logger
	msg := request.Msg

	// Remoting must be enabled
	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.ErrRemotingDisabled)
	}

	// Validate host and port
	host := msg.GetGrain().GetHost()
	port := msg.GetGrain().GetPort()
	if err := x.validateRemoteHost(host, port); err != nil {
		return nil, err
	}

	message, _ := msg.GetMessage().UnmarshalNew()

	identity, err := toIdentity(msg.GetGrain().GetGrainId().GetValue())
	if err != nil {
		if errors.Is(err, gerrors.ErrInvalidGrainIdentity) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.Join(err, gerrors.ErrInvalidGrainIdentity))
	}

	if isSystemName(identity.Name()) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.NewErrReservedName(identity.String()))
	}

	_, err = x.localSend(ctx, identity, message, DefaultGrainRequestTimeout, false)
	if err != nil {
		logger.Errorf("failed to create grain (%s) on [host=%s, port=%d]: reason: (%v)", identity.String(), msg.GetGrain().GetHost(), msg.GetGrain().GetPort(), err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&internalpb.RemoteTellGrainResponse{}), nil
}

func (x *actorSystem) RemoteActivateGrain(ctx context.Context, request *connect.Request[internalpb.RemoteActivateGrainRequest]) (*connect.Response[internalpb.RemoteActivateGrainResponse], error) {
	logger := x.logger
	msg := request.Msg

	// Remoting must be enabled
	if !x.remotingEnabled.Load() {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gerrors.ErrRemotingDisabled)
	}

	grain := msg.GetGrain()

	// Validate host and port
	host := grain.GetHost()
	port := grain.GetPort()
	if err := x.validateRemoteHost(host, port); err != nil {
		return nil, err
	}

	if err := x.recreateGrain(ctx, grain); err != nil {
		logger.Errorf("failed to recreate grain (%s) on [host=%s, port=%d]: reason: (%v)", grain.GetGrainId().GetValue(), host, port, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	logger.Infof("recreated grain (%s) on [host=%s, port=%d]", grain.GetGrainId().GetValue(), host, port)
	return connect.NewResponse(&internalpb.RemoteActivateGrainResponse{}), nil
}

// validateRemoteHost checks if the incoming request is for the correct host/port.
func (x *actorSystem) validateRemoteHost(host string, port int32) error {
	addr := fmt.Sprintf("%s:%d", x.remoteConfig.BindAddr(), x.remoteConfig.BindPort())
	if addr != net.JoinHostPort(host, strconv.Itoa(int(port))) {
		return connect.NewError(connect.CodeInvalidArgument, gerrors.ErrInvalidHost)
	}
	return nil
}

// remoteTellGrain sends a message to a Grain in the cluster.
//
// It locates the Grain via the cluster, sends the message remotely, and returns the response.
// Falls back to local delivery if the Grain is not found in the cluster.
//
// Parameters:
//   - ctx: context for cancellation and timeout control.
//   - id: identity of the target Grain.
//   - message: protobuf message to send.
//   - sender: identity of the sender (optional).
//   - timeout: request timeout duration.
//
// Returns:
//   - error: error if the request fails.
func (x *actorSystem) remoteTellGrain(ctx context.Context, id *GrainIdentity, message proto.Message, timeout time.Duration) error {
	grain, err := x.getCluster().GetGrain(ctx, id.String())
	if err != nil {
		if errors.Is(err, cluster.ErrGrainNotFound) {
			_, err := x.localSend(ctx, id, message, timeout, false)
			return err
		}
		return err
	}

	// just send the message without activating the grain
	serialized, _ := anypb.New(message)
	remoteClient := x.remoting.RemotingServiceClient(grain.GetHost(), int(grain.GetPort()))
	request := connect.NewRequest(&internalpb.RemoteTellGrainRequest{
		Grain:   grain,
		Message: serialized,
	})

	_, err = remoteClient.RemoteTellGrain(ctx, request)
	return err
}

// remoteAskGrain sends a message to a Grain in the cluster.
//
// It locates the Grain via the cluster, sends the message remotely, and returns the response.
// Falls back to local delivery if the Grain is not found in the cluster.
//
// Parameters:
//   - ctx: context for cancellation and timeout control.
//   - id: identity of the target Grain.
//   - message: protobuf message to send.
//   - sender: identity of the sender (optional).
//   - timeout: request timeout duration.
//
// Returns:
//   - proto.Message: the response from the Grain.
//   - error: error if the request fails.
func (x *actorSystem) remoteAskGrain(ctx context.Context, id *GrainIdentity, message proto.Message, timeout time.Duration) (proto.Message, error) {
	gw, err := x.getCluster().GetGrain(ctx, id.String())
	if err != nil {
		if errors.Is(err, cluster.ErrGrainNotFound) {
			return x.localSend(ctx, id, message, timeout, true)
		}
		return nil, err
	}

	msg, _ := anypb.New(message)
	remoteClient := x.remoting.RemotingServiceClient(gw.GetHost(), int(gw.GetPort()))
	request := connect.NewRequest(&internalpb.RemoteAskGrainRequest{
		Grain:          gw,
		RequestTimeout: durationpb.New(timeout),
		Message:        msg,
	})

	res, err := remoteClient.RemoteAskGrain(ctx, request)
	if err != nil {
		return nil, err
	}
	return res.Msg.GetMessage().UnmarshalNew()
}

// localSend sends a message to a local Grain.
//
// It creates or locates the Grain locally, delivers the message, and waits for a response or error.
//
// Parameters:
//   - ctx: context for cancellation and timeout control.
//   - id: identity of the target Grain.
//   - message: protobuf message to send.
//   - sender: identity of the sender (optional).
//   - timeout: request timeout duration.
//   - synchronous: whether to wait for a response (true for Ask, false for Tell).
//
// Returns:
//   - proto.Message: the response from the Grain (if synchronous).
//   - error: error if the request fails.
func (x *actorSystem) localSend(ctx context.Context, id *GrainIdentity, message proto.Message, timeout time.Duration, synchronous bool) (proto.Message, error) {
	// Ensure the grain process exists
	pid, err := x.ensureGrainProcess(id)
	if err != nil {
		return nil, err
	}

	// Ensure the process is running
	if !pid.isActive() {
		pid.activated.Store(true)
	}

	// Build and send the grainContext
	grainContext := getGrainContext()
	grainContext.build(ctx, pid, x, id, message, synchronous)
	pid.receive(grainContext)
	timer := timers.Get(timeout)

	// Handle synchronous (Ask) case
	if synchronous {
		select {
		case res := <-grainContext.getResponse():
			timers.Put(timer)
			return res, nil
		case err := <-grainContext.getError():
			timers.Put(timer)
			return nil, err
		case <-ctx.Done():
			timers.Put(timer)
			return nil, errors.Join(ctx.Err(), gerrors.ErrRequestTimeout)
		case <-timer.C:
			timers.Put(timer)
			return nil, gerrors.ErrRequestTimeout
		}
	}

	// Asynchronous (Tell) case
	select {
	case err := <-grainContext.getError():
		return nil, err
	case <-timer.C:
		return nil, gerrors.ErrRequestTimeout
	case <-ctx.Done():
		return nil, errors.Join(ctx.Err(), gerrors.ErrRequestTimeout)
	}
}

// ensureGrainProcess returns an existing grain process or creates and activates a new one.
func (x *actorSystem) ensureGrainProcess(id *GrainIdentity) (*grainPID, error) {
	process, ok := x.grains.Get(id.String())
	if ok {
		if !x.reflection.registry.Exists(process.getGrain()) {
			x.grains.Delete(id.String())
			return nil, gerrors.ErrGrainNotRegistered
		}

		return process, nil
	}

	return process, nil
}

// recreateGrain recreates a serialized Grain.
//
// It instantiates the grain, activates it, registers it locally, and updates the cluster registry.
// Returns an error if any step fails.
func (x *actorSystem) recreateGrain(ctx context.Context, serializedGrain *internalpb.Grain) error {
	logger := x.logger
	logger.Infof("recreating grain (%s)...", serializedGrain.GrainId.GetValue())

	// make sure the grain is not a system grain
	if isSystemName(serializedGrain.GrainId.GetValue()) {
		return gerrors.NewErrReservedName(serializedGrain.GetGrainId().GetValue())
	}

	// Parse grain identity
	identity, err := toIdentity(serializedGrain.GetGrainId().GetValue())
	if err != nil {
		return err
	}

	var (
		process *grainPID
		ok      bool
	)

	process, ok = x.grains.Get(identity.String())
	if !ok {
		grain, err := x.getReflection().NewGrain(identity.Kind())
		if err != nil {
			return err
		}

		dependencies, err := x.getReflection().NewDependencies(serializedGrain.GetDependencies()...)
		if err != nil {
			return err
		}

		config := newGrainConfig(
			WithGrainInitTimeout(serializedGrain.GetActivationTimeout().AsDuration()),
			WithGrainInitMaxRetries(int(serializedGrain.GetActivationRetries())),
			WithGrainDependencies(dependencies...),
		)

		if err := config.Validate(); err != nil {
			return err
		}

		process = newGrainPID(identity, grain, x, config)
		if err := process.activate(ctx); err != nil {
			return err
		}

		// Register locally
		x.getGrains().Set(identity.String(), process)

		// Register in the cluster
		return x.putGrainOnCluster(process)
	}

	if !x.registry.Exists(process.getGrain()) {
		x.registry.Register(process.getGrain())
	}

	if !process.isActive() {
		if err := process.activate(ctx); err != nil {
			return err
		}
	}

	// Register in the cluster
	return x.putGrainOnCluster(process)
}

func (x *actorSystem) findActivationPeer(ctx context.Context, config *grainConfig) (*cluster.Peer, error) {
	peers, err := x.cluster.Members(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch cluster nodes: %w", err)
	}

	peers, err = x.filterPeersByRole(peers, config.role)
	if err != nil {
		return nil, err
	}

	if len(peers) <= 1 {
		return nil, nil
	}

	peer, err := x.selectActivationPeer(ctx, peers, config.activationStrategy)
	if err != nil {
		return nil, err
	}
	return peer, nil
}

func (x *actorSystem) filterPeersByRole(peers []*cluster.Peer, rolePtr *string) ([]*cluster.Peer, error) {
	if rolePtr == nil {
		return peers, nil
	}

	role := pointer.Deref(rolePtr, "")
	filtered := make([]*cluster.Peer, 0, len(peers))
	for _, peer := range peers {
		if peer.HasRole(role) {
			filtered = append(filtered, peer)
		}
	}

	if len(filtered) == 0 {
		return nil, fmt.Errorf("no nodes with role %s found in the cluster", role)
	}

	return filtered, nil
}

func (x *actorSystem) selectActivationPeer(ctx context.Context, peers []*cluster.Peer, strategy ActivationStrategy) (*cluster.Peer, error) {
	switch strategy {
	case RandomActivation:
		return peers[rand.IntN(len(peers))], nil //nolint:gosec
	case RoundRobinActivation:
		return x.grainsRoundRobinActivationPeer(ctx, peers)
	case LeastLoadActivation:
		return x.leastLoadedPeer(ctx, peers)
	default:
		return nil, nil
	}
}

func (x *actorSystem) grainsRoundRobinActivationPeer(ctx context.Context, peers []*cluster.Peer) (*cluster.Peer, error) {
	next, err := x.cluster.NextRoundRobinValue(ctx, cluster.GrainsRoundRobinKey)
	if err != nil {
		return nil, err
	}
	return peers[(next-1)%len(peers)], nil
}

func (x *actorSystem) leastLoadedPeer(ctx context.Context, peers []*cluster.Peer) (*cluster.Peer, error) {
	type nodeMetric struct {
		Peer *cluster.Peer
		Load uint64
	}

	metrics := make([]nodeMetric, len(peers))
	eg, egCtx := errgroup.WithContext(ctx)

	for index, peer := range peers {
		eg.Go(func() error {
			client := x.clusterClient(peer)
			addr := peer.RemotingAddress()
			resp, err := client.GetNodeMetric(egCtx, connect.NewRequest(&internalpb.GetNodeMetricRequest{NodeAddress: addr}))
			if err != nil {
				return fmt.Errorf("failed to fetch node metric from %s: %w", addr, err)
			}
			metrics[index] = nodeMetric{Peer: peer, Load: resp.Msg.GetActorsCount()}
			return nil
		})
	}

	if err := eg.Wait(); err != nil {
		return nil, fmt.Errorf("failed to fetch node metrics: %w", err)
	}

	least := metrics[0]
	for i := 1; i < len(metrics); i++ {
		if metrics[i].Load < least.Load {
			least = metrics[i]
		}
	}

	return least.Peer, nil
}

func (x *actorSystem) clusterClient(peer *cluster.Peer) internalpbconnect.ClusterServiceClient {
	remoting := x.remoting
	var endpoint string
	if x.tlsInfo != nil {
		endpoint = http.URLs(peer.Host, peer.RemotingPort)
	} else {
		endpoint = http.URL(peer.Host, peer.RemotingPort)
	}

	opts := []connect.ClientOption{
		connectproto.WithBinary(
			proto.MarshalOptions{},
			proto.UnmarshalOptions{DiscardUnknown: true},
		),
	}

	if remoting.MaxReadFrameSize() > 0 {
		opts = append(opts,
			connect.WithSendMaxBytes(remoting.MaxReadFrameSize()),
			connect.WithReadMaxBytes(remoting.MaxReadFrameSize()),
		)
	}

	switch remoting.Compression() {
	case remote.GzipCompression:
		opts = append(opts, connect.WithSendGzip())
	case remote.ZstdCompression:
		opts = append(opts, zstd.WithCompression())
		opts = append(opts, connect.WithSendCompression(zstd.Name))
	case remote.BrotliCompression:
		opts = append(opts, brotli.WithCompression())
		opts = append(opts, connect.WithSendCompression(brotli.Name))
	default:
		// pass
	}

	return internalpbconnect.NewClusterServiceClient(
		remoting.HTTPClient(),
		endpoint,
		opts...,
	)
}
