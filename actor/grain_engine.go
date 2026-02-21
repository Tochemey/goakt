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
	"math/rand/v2"
	"net"
	"strconv"
	"sync"
	"time"

	goset "github.com/deckarep/golang-set/v2"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tochemey/goakt/v4/datacenter"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/pointer"
	"github.com/tochemey/goakt/v4/remote"
)

type grainOwnerMismatchError struct {
	owner *internalpb.Grain
}

func (e *grainOwnerMismatchError) Error() string {
	if e.owner == nil {
		return "grain owner is unknown"
	}
	return fmt.Sprintf("grain is owned by %s:%d", e.owner.GetHost(), e.owner.GetPort())
}

// DeregisterGrainKind removes a previously registered Grain kind from the local registry.
//
// Deregistration affects future activations that rely on kind lookup (e.g. remote activation/recreation
// requests and lazy/local activation when a GrainIdentity is resolved by kind). It does not stop or
// deactivate already-running Grain instances of that kind; it only prevents new activations via the
// registry.
//
// Notes:
//   - Deregistration is local to the current actor system instance.
//   - The operation is idempotent: deregistering a kind that is not registered is safe.
//   - The provided context is currently unused and is reserved for future enhancements.
//
// Returns ErrActorSystemNotStarted if the actor system is not running.
func (x *actorSystem) DeregisterGrainKind(_ context.Context, kind Grain) error {
	if !x.Running() {
		return gerrors.ErrActorSystemNotStarted
	}

	x.locker.Lock()
	x.registry.Deregister(kind)
	x.locker.Unlock()
	return nil
}

// RegisterGrainKind registers a Grain kind in the local registry.
//
// Registration associates the Grain's kind (as returned by the Grain implementation) with the
// factory/metadata used by the actor system to instantiate that kind on demand.
//
// This is required for:
//   - Remote activation/recreation: when another node asks this node to activate a Grain of a given kind.
//   - Lazy/local activation: when a GrainIdentity is resolved locally via kind lookup.
//
// Notes:
//   - Registration is local to the current actor system instance.
//   - The operation is idempotent: registering the same kind multiple times is safe.
//   - The provided context is currently unused and is reserved for future enhancements.
//
// Returns ErrActorSystemNotStarted if the actor system is not running.
func (x *actorSystem) RegisterGrainKind(_ context.Context, kind Grain) error {
	if !x.Running() {
		return gerrors.ErrActorSystemNotStarted
	}

	x.locker.Lock()
	x.registry.Register(kind)
	x.locker.Unlock()
	return nil
}

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
// Algorithm (cluster-aware):
//  1. Build and validate the Grain identity and config.
//  2. If a remote owner exists, request remote activation and return.
//  3. If unowned, optionally select a peer and attempt a claimed remote activation.
//  4. Otherwise, claim and activate locally, then publish to the cluster registry.
//
// Note:
//   - This method abstracts away the details of Grain lifecycle management.
//   - Use this to obtain a reference to a Grain for message passing or further operations.
func (x *actorSystem) GrainIdentity(ctx context.Context, name string, factory GrainFactory, opts ...GrainOption) (*GrainIdentity, error) {
	if !x.started.Load() || x.isStopping() {
		return nil, gerrors.ErrActorSystemNotStarted
	}

	grain, identity, config, err := x.prepareGrainIdentity(ctx, name, factory, opts...)
	if err != nil {
		return nil, err
	}

	x.logger.Infof("Activating Grain (%s)...", identity.String())
	owner, err := x.resolveGrainOwner(ctx, identity)
	if err != nil {
		x.logger.Errorf("Failed to resolve owner for Grain (%s): %v", identity.String(), err)
		return nil, err
	}

	handled, err := x.tryRemoteGrainActivation(ctx, identity, grain, config, owner)
	if err != nil {
		x.logger.Errorf("Failed to attempt remote activation for Grain (%s): %v", identity.String(), err)
		return nil, err
	}

	if handled {
		x.logger.Infof("Grain (%s) activated remotely.", identity.String())
		return identity, nil
	}

	if err := x.activateGrainLocally(ctx, identity, grain, config, owner); err != nil {
		return nil, err
	}

	x.logger.Infof("Grain (%s) activated locally.", identity.String())
	return identity, nil
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
func (x *actorSystem) TellGrain(ctx context.Context, identity *GrainIdentity, message any) error {
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
func (x *actorSystem) AskGrain(ctx context.Context, identity *GrainIdentity, message any, timeout time.Duration) (response any, err error) {
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

// prepareGrainIdentity executes the factory and validates identity/config for activation.
func (x *actorSystem) prepareGrainIdentity(ctx context.Context, name string, factory GrainFactory, opts ...GrainOption) (Grain, *GrainIdentity, *grainConfig, error) {
	grain, err := factory(ctx)
	if err != nil {
		return nil, nil, nil, err
	}

	identity := newGrainIdentity(grain, name)
	if err := identity.Validate(); err != nil {
		return nil, nil, nil, err
	}

	// make sure we don't interfere with system actors.
	if isSystemName(identity.Name()) {
		return nil, nil, nil, gerrors.NewErrReservedName(identity.String())
	}

	config := newGrainConfig(opts...)
	if err := config.Validate(); err != nil {
		return nil, nil, nil, err
	}

	return grain, identity, config, nil
}

// resolveGrainOwner returns the cluster owner record when clustering is enabled.
func (x *actorSystem) resolveGrainOwner(ctx context.Context, identity *GrainIdentity) (*internalpb.Grain, error) {
	if !x.InCluster() {
		return nil, nil
	}
	return x.getGrainOwner(ctx, identity)
}

// tryRemoteGrainActivation attempts to activate the grain on a remote owner or activation peer.
// It returns true when the caller should stop and return the identity without local activation.
func (x *actorSystem) tryRemoteGrainActivation(ctx context.Context, identity *GrainIdentity, grain Grain, config *grainConfig, owner *internalpb.Grain) (bool, error) {
	if !x.InCluster() {
		return false, nil
	}

	if owner != nil && !proto.Equal(owner, new(internalpb.Grain)) {
		if !x.isLocalGrainOwner(owner) {
			if err := x.sendRemoteActivateGrain(ctx, owner); err != nil {
				// The remote owner is unreachable (e.g. node crashed).
				// Remove the stale grain entry and fall through to local activation.
				x.logger.Warnf("Remote owner for Grain (%s) is unreachable, removing stale entry: %v", identity.String(), err)
				_ = x.getCluster().RemoveGrain(ctx, identity.String())
				return false, nil
			}
			return true, nil
		}
		return false, nil
	}

	if owner != nil {
		return false, nil
	}

	peer, err := x.findActivationPeer(ctx, config)
	if err != nil {
		return false, err
	}

	if peer == nil || peer.PeerAddress() == x.PeersAddress() {
		return false, nil
	}

	return x.tryPeerActivation(ctx, identity, grain, config, peer)
}

// tryPeerActivation claims the grain for a remote peer and triggers remote activation.
func (x *actorSystem) tryPeerActivation(ctx context.Context, identity *GrainIdentity, grain Grain, config *grainConfig, peer *cluster.Peer) (bool, error) {
	pid := newGrainPID(identity, grain, x, config)
	grainInfo, err := pid.toWireGrain()
	if err != nil {
		return false, err
	}

	// override the grain info with peer (host and remoting port)
	grainInfo.Host = peer.Host
	grainInfo.Port = int32(peer.RemotingPort)

	claimed, _, err := x.tryClaimGrain(ctx, grainInfo)
	if err != nil {
		return false, err
	}

	if !claimed {
		return true, nil
	}

	if err := x.sendRemoteActivateGrain(ctx, grainInfo); err != nil {
		_ = x.getCluster().RemoveGrain(ctx, identity.String())
		return false, err
	}

	return true, nil
}

// activateGrainLocally ensures a local grain exists, claims ownership when needed, and activates it.
func (x *actorSystem) activateGrainLocally(ctx context.Context, identity *GrainIdentity, grain Grain, config *grainConfig, owner *internalpb.Grain) error {
	_, err := x.runGrainActivation(identity.String(), func() (*grainPID, error) {
		pid, ok := x.grains.Get(identity.String())
		if !ok {
			pid = newGrainPID(identity, grain, x, config)
		}

		if !x.registry.Exists(grain) {
			x.registry.Register(grain)
		}

		claimed := false
		if x.InCluster() && owner == nil {
			wire, err := pid.toWireGrain()
			if err != nil {
				return nil, err
			}

			var claimOwner *internalpb.Grain
			claimed, claimOwner, err = x.tryClaimGrain(ctx, wire)
			if err != nil {
				return nil, err
			}

			if !claimed && claimOwner != nil && !x.isLocalGrainOwner(claimOwner) {
				return nil, &grainOwnerMismatchError{owner: claimOwner}
			}
		}

		if !pid.isActive() {
			if err := x.waitForGrainActivationBarrier(ctx); err != nil {
				return nil, err
			}

			if err := pid.activate(ctx); err != nil {
				if claimed && x.InCluster() {
					_ = x.getCluster().RemoveGrain(ctx, identity.String())
				}
				return nil, err
			}
		}

		x.grains.Set(identity.String(), pid)
		if err := x.putGrainOnCluster(pid); err != nil {
			return nil, err
		}
		return pid, nil
	})

	if err != nil {
		var ownerErr *grainOwnerMismatchError
		if errors.As(err, &ownerErr) {
			return nil
		}
	}
	return err
}

// sendRemoteActivateGrain triggers activation on a remote node for the provided grain identity.
func (x *actorSystem) sendRemoteActivateGrain(ctx context.Context, grain *internalpb.Grain) error {
	// Convert internalpb.Grain to remote.GrainRequest
	grainRequest := &remote.GrainRequest{
		Name:              grain.GetGrainId().GetValue(),
		Kind:              grain.GetGrainId().GetKind(),
		ActivationTimeout: time.Second,
		ActivationRetries: 5,
	}

	// Call the high-level RemoteActivateGrain method
	return x.remoting.RemoteActivateGrain(ctx, grain.GetHost(), int(grain.GetPort()), grainRequest)
}

// remoteTellGrain sends a message to a Grain in the cluster.
//
// It locates the Grain via the cluster, sends the message remotely, and returns the response.
// Falls back to cross-DC discovery, then local delivery if not found.
//
// Parameters:
//   - ctx: context for cancellation and timeout control.
//   - id: identity of the target Grain.
//   - message: protobuf message to send.
//   - timeout: request timeout duration.
//
// Returns:
//   - error: error if the request fails.
func (x *actorSystem) remoteTellGrain(ctx context.Context, id *GrainIdentity, message any, timeout time.Duration) error {
	// Try local cluster first
	grain, err := x.getCluster().GetGrain(ctx, id.String())
	if err == nil {
		return x.sendRemoteTellGrainRequest(ctx, grain, message)
	}

	if !errors.Is(err, cluster.ErrGrainNotFound) {
		return err
	}

	// Try to find and send to grain in remote datacenters
	if err := x.tellGrainAcrossDataCenters(ctx, id, message, timeout); err == nil {
		return nil
	}

	// Not found anywhere - activate locally
	_, err = x.localSend(ctx, id, message, timeout, false)
	return err
}

// remoteAskGrain sends a message to a Grain in the cluster.
//
// It locates the Grain via the cluster, sends the message remotely, and returns the response.
// Falls back to cross-DC discovery, then local delivery if not found.
//
// Parameters:
//   - ctx: context for cancellation and timeout control.
//   - id: identity of the target Grain.
//   - message: protobuf message to send.
//   - timeout: request timeout duration.
//
// Returns:
//   - proto.Message: the response from the Grain.
//   - error: error if the request fails.
func (x *actorSystem) remoteAskGrain(ctx context.Context, id *GrainIdentity, message any, timeout time.Duration) (any, error) {
	// Try local cluster first
	grain, err := x.getCluster().GetGrain(ctx, id.String())
	if err == nil {
		return x.sendRemoteAskGrainRequest(ctx, grain, message, timeout)
	}

	if !errors.Is(err, cluster.ErrGrainNotFound) {
		return nil, err
	}

	// Try to find and send to grain in remote datacenters
	resp, err := x.askGrainAcrossDataCenters(ctx, id, message, timeout)
	if err == nil {
		return resp, nil
	}

	// Not found anywhere - activate locally
	return x.localSend(ctx, id, message, timeout, true)
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
func (x *actorSystem) localSend(ctx context.Context, id *GrainIdentity, message any, timeout time.Duration, synchronous bool) (any, error) {
	// Ensure the grain process exists and is activated if needed.
	pid, err := x.ensureGrainProcess(ctx, id)
	if err != nil {
		var ownerErr *grainOwnerMismatchError
		if errors.As(err, &ownerErr) {
			return x.sendToGrainOwner(ctx, ownerErr.owner, message, timeout, synchronous)
		}
		return nil, err
	}

	// Build and send the grainContext
	grainContext := getGrainContext()
	grainContext.build(ctx, pid, x, id, message, synchronous)
	errCh := grainContext.err

	timer := timers.Get(timeout)

	// Handle synchronous (Ask) case
	if synchronous {
		responseCh := grainContext.response

		pid.receive(grainContext)
		select {
		case res := <-responseCh:
			timers.Put(timer)
			putResponseChannel(responseCh)
			putErrorChannel(errCh)
			return res, nil
		case err := <-errCh:
			timers.Put(timer)
			putResponseChannel(responseCh)
			putErrorChannel(errCh)
			return nil, err
		case <-ctx.Done():
			// The grain goroutine may still be processing and could send
			// on the channels later. Mark response as closed so
			// Response()/NoErr() CAS guards prevent late sends, and do
			// NOT return channels to the pool -- let them be GC'd.
			grainContext.responseClosed.Store(true)
			timers.Put(timer)
			return nil, errors.Join(ctx.Err(), gerrors.ErrRequestTimeout)
		case <-timer.C:
			grainContext.responseClosed.Store(true)
			timers.Put(timer)
			return nil, gerrors.ErrRequestTimeout
		}
	}

	// Asynchronous (Tell) case
	pid.receive(grainContext)
	select {
	case err := <-errCh:
		timers.Put(timer)
		putErrorChannel(errCh)
		return nil, err
	case <-timer.C:
		// The grain goroutine may still be processing and could send on
		// errCh later. Do NOT return it to the pool -- let it be GC'd.
		timers.Put(timer)
		return nil, gerrors.ErrRequestTimeout
	case <-ctx.Done():
		timers.Put(timer)
		return nil, errors.Join(ctx.Err(), gerrors.ErrRequestTimeout)
	}
}

// tellGrainAcrossDataCenters sends a message to a Grain across all active datacenters.
//
// This method queries all endpoints in every active datacenter concurrently and
// sends the message to the first DC that successfully handles it. Once sent,
// remaining attempts are cancelled to minimize resource usage.
//
// The discovery is best-effort: it uses cached datacenter records and proceeds
// even if the cache is stale (logging a warning in that case).
//
// Parameters:
//   - ctx: Parent context for cancellation propagation
//   - id: Identity of the target Grain
//   - message: The protobuf message to send
//   - timeout: Maximum duration to wait for the operation
//
// Returns:
//   - error: nil if message was sent successfully, error otherwise
func (x *actorSystem) tellGrainAcrossDataCenters(ctx context.Context, id *GrainIdentity, message any, timeout time.Duration) error {
	dcController := x.getDataCenterController()
	if dcController == nil {
		return gerrors.ErrActorNotFound
	}

	dcRecords, stale := dcController.ActiveRecords()
	if stale {
		if dcController.FailOnStaleCache() {
			return gerrors.ErrDataCenterStaleRecords
		}
		// Best-effort routing: proceed with stale cache but log warning
		x.logger.Warn("DC cache is stale, proceeding with best-effort cross-DC grain routing")
	}

	if len(dcRecords) == 0 {
		return gerrors.ErrActorNotFound
	}

	// Count total endpoints for proper channel buffer sizing
	endpointCount := 0
	for _, dcRecord := range dcRecords {
		if dcRecord.State == datacenter.DataCenterActive {
			endpointCount += len(dcRecord.Endpoints)
		}
	}

	if endpointCount == 0 {
		return gerrors.ErrActorNotFound
	}

	// Query remote datacenters in parallel with timeout
	dcConfig := x.getDataCenterConfig()
	requestTimeout := datacenter.DefaultRequestTimeout
	if dcConfig != nil {
		requestTimeout = dcConfig.RequestTimeout
	}

	if timeout > 0 && timeout < requestTimeout {
		requestTimeout = timeout
	}

	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	// Buffer sized for all endpoints to prevent goroutine blocking
	results := make(chan error, endpointCount)
	var wg sync.WaitGroup

	grainReq := &remote.GrainRequest{
		Name: id.Name(),
		Kind: id.Kind(),
	}

	// Query each active datacenter in parallel
	for _, dcRecord := range dcRecords {
		if dcRecord.State != datacenter.DataCenterActive {
			continue
		}

		for _, endpoint := range dcRecord.Endpoints {
			host, portStr, err := net.SplitHostPort(endpoint)
			if err != nil {
				continue
			}

			port, err := strconv.Atoi(portStr)
			if err != nil {
				continue
			}

			wg.Add(1)
			go func(host string, port int) {
				defer wg.Done()
				err := x.remoting.RemoteTellGrain(requestCtx, host, port, grainReq, message)
				results <- err
			}(host, port)
		}
	}

	// Wait for all goroutines to complete and close the channel
	go func() {
		wg.Wait()
		close(results)
	}()

	// Return on first success
	for err := range results {
		if err == nil {
			cancel() // Cancel remaining attempts
			return nil
		}
	}

	return gerrors.ErrActorNotFound
}

// askGrainAcrossDataCenters sends a synchronous message to a Grain across all active datacenters.
//
// This method queries all endpoints in every active datacenter concurrently and
// returns the response from the first DC that successfully handles the request.
// Once a response is received, remaining attempts are cancelled.
//
// The discovery is best-effort: it uses cached datacenter records and proceeds
// even if the cache is stale (logging a warning in that case).
//
// Parameters:
//   - ctx: Parent context for cancellation propagation
//   - id: Identity of the target Grain
//   - message: The protobuf message to send
//   - timeout: Maximum duration to wait for the operation
//
// Returns:
//   - proto.Message: The response from the Grain if found
//   - error: nil if successful, ErrActorNotFound if grain not found in any DC
func (x *actorSystem) askGrainAcrossDataCenters(ctx context.Context, id *GrainIdentity, message any, timeout time.Duration) (any, error) {
	dcController := x.getDataCenterController()
	if dcController == nil {
		return nil, gerrors.ErrActorNotFound
	}

	dcRecords, stale := dcController.ActiveRecords()
	if stale {
		if dcController.FailOnStaleCache() {
			return nil, gerrors.ErrDataCenterStaleRecords
		}
		// Best-effort routing: proceed with stale cache but log warning
		x.logger.Warn("DC cache is stale, proceeding with best-effort cross-DC grain routing")
	}

	if len(dcRecords) == 0 {
		return nil, gerrors.ErrActorNotFound
	}

	// Count total endpoints for proper channel buffer sizing
	endpointCount := 0
	for _, dcRecord := range dcRecords {
		if dcRecord.State == datacenter.DataCenterActive {
			endpointCount += len(dcRecord.Endpoints)
		}
	}

	if endpointCount == 0 {
		return nil, gerrors.ErrActorNotFound
	}

	// Query remote datacenters in parallel with timeout
	dcConfig := x.getDataCenterConfig()
	requestTimeout := datacenter.DefaultRequestTimeout
	if dcConfig != nil {
		requestTimeout = dcConfig.RequestTimeout
	}

	if timeout > 0 && timeout < requestTimeout {
		requestTimeout = timeout
	}

	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	type result struct {
		resp any
		err  error
	}

	// Buffer sized for all endpoints to prevent goroutine blocking
	results := make(chan result, endpointCount)
	var wg sync.WaitGroup

	grainReq := &remote.GrainRequest{
		Name: id.Name(),
		Kind: id.Kind(),
	}

	// Query each active datacenter in parallel
	for _, dcRecord := range dcRecords {
		if dcRecord.State != datacenter.DataCenterActive {
			continue
		}

		for _, endpoint := range dcRecord.Endpoints {
			host, portStr, err := net.SplitHostPort(endpoint)
			if err != nil {
				continue
			}

			port, err := strconv.Atoi(portStr)
			if err != nil {
				continue
			}

			wg.Add(1)
			go func(host string, port int) {
				defer wg.Done()
				resp, err := x.remoting.RemoteAskGrain(requestCtx, host, port, grainReq, message, timeout)
				results <- result{resp: resp, err: err}
			}(host, port)
		}
	}

	// Wait for all goroutines to complete and close the channel
	go func() {
		wg.Wait()
		close(results)
	}()

	// Return first successful response
	for res := range results {
		if res.err == nil && res.resp != nil {
			cancel() // Cancel remaining attempts
			return res.resp, nil
		}
	}

	return nil, gerrors.ErrActorNotFound
}

// runGrainActivation ensures only one activation attempt per grain ID executes at a time.
// Callers share the same result/error for concurrent requests on the same ID.
// When id is empty, the function runs without coordination.
func (x *actorSystem) runGrainActivation(id string, fn func() (*grainPID, error)) (*grainPID, error) {
	if id == "" {
		return fn()
	}

	res, err, _ := x.grainActivation.Do(id, func() (any, error) {
		return fn()
	})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}

	pid, ok := res.(*grainPID)
	if !ok {
		return nil, fmt.Errorf("unexpected grain activation result for %s", id)
	}
	return pid, nil
}

// ensureGrainProcess ensures a local grain process exists and is ready to receive messages.
// Algorithm (cluster-aware):
//  1. If a local process exists, validate registration and (re)activate if needed.
//  2. When clustering is enabled, confirm the cluster owner; if remote, return a mismatch error.
//  3. If no owner is recorded, attempt an atomic claim before local activation.
//  4. On activation failure after a claim, remove the cluster entry to avoid stale ownership.
//  5. Publish successful activation to the cluster registry.
func (x *actorSystem) ensureGrainProcess(ctx context.Context, id *GrainIdentity) (*grainPID, error) {
	return x.runGrainActivation(id.String(), func() (*grainPID, error) {
		if process, ok := x.grains.Get(id.String()); ok {
			return x.ensureExistingGrainProcess(ctx, id, process)
		}

		return x.ensureNewGrainProcess(ctx, id)
	})
}

func (x *actorSystem) ensureExistingGrainProcess(ctx context.Context, id *GrainIdentity, process *grainPID) (*grainPID, error) {
	// Guard against stale entries for grains that are no longer registered.
	if !x.reflection.registry.Exists(process.getGrain()) {
		x.grains.Delete(id.String())
		return nil, gerrors.ErrGrainNotRegistered
	}

	if !process.isActive() {
		if err := x.waitForGrainActivationBarrier(ctx); err != nil {
			return nil, err
		}

		claimed, err := x.ensureGrainOwnership(ctx, id, process)
		if err != nil {
			return nil, err
		}

		// Activate locally; roll back any claim if activation fails.
		if err := process.activate(ctx); err != nil {
			if claimed && x.InCluster() {
				_ = x.getCluster().RemoveGrain(ctx, id.String())
			}
			return nil, err
		}

		// Broadcast the activation to the cluster when clustering is enabled.
		if err := x.putGrainOnCluster(process); err != nil {
			return nil, err
		}
	}

	return process, nil
}

func (x *actorSystem) ensureNewGrainProcess(ctx context.Context, id *GrainIdentity) (*grainPID, error) {
	// No local process yet: create one from the registry and follow the same cluster-claim flow.
	if err := x.waitForGrainActivationBarrier(ctx); err != nil {
		return nil, err
	}

	grain, err := x.getReflection().instantiateGrain(id.Kind())
	if err != nil {
		return nil, err
	}

	config := newGrainConfig()
	if err := config.Validate(); err != nil {
		return nil, err
	}

	process := newGrainPID(id, grain, x, config)
	claimed, err := x.ensureGrainOwnership(ctx, id, process)
	if err != nil {
		return nil, err
	}

	if err := process.activate(ctx); err != nil {
		if claimed && x.InCluster() {
			_ = x.getCluster().RemoveGrain(ctx, id.String())
		}
		return nil, err
	}

	x.grains.Set(id.String(), process)
	return process, x.putGrainOnCluster(process)
}

// ensureGrainOwnership verifies cluster ownership and attempts a claim when unowned.
// It returns whether a claim was made so callers can roll it back if activation fails.
func (x *actorSystem) ensureGrainOwnership(ctx context.Context, id *GrainIdentity, process *grainPID) (bool, error) {
	if !x.InCluster() {
		return false, nil
	}

	owner, err := x.getGrainOwner(ctx, id)
	if err != nil {
		return false, err
	}

	if owner != nil && !x.isLocalGrainOwner(owner) {
		return false, &grainOwnerMismatchError{owner: owner}
	}

	if owner == nil {
		// Claim ownership before local activation to avoid duplicate creation.
		wire, err := process.toWireGrain()
		if err != nil {
			return false, err
		}

		claimed, claimOwner, err := x.tryClaimGrain(ctx, wire)
		if err != nil {
			return false, err
		}

		if !claimed && claimOwner != nil && !x.isLocalGrainOwner(claimOwner) {
			return false, &grainOwnerMismatchError{owner: claimOwner}
		}

		return claimed, nil
	}

	return false, nil
}

func (x *actorSystem) isLocalGrainOwner(grain *internalpb.Grain) bool {
	if grain == nil {
		return false
	}
	return grain.GetHost() == x.Host() && grain.GetPort() == int32(x.Port())
}

func (x *actorSystem) getGrainOwner(ctx context.Context, id *GrainIdentity) (*internalpb.Grain, error) {
	exists, err := x.getCluster().GrainExists(ctx, id.String())
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}

	owner, err := x.getCluster().GetGrain(ctx, id.String())
	if err != nil {
		if errors.Is(err, cluster.ErrGrainNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return owner, nil
}

func (x *actorSystem) tryClaimGrain(ctx context.Context, grain *internalpb.Grain) (bool, *internalpb.Grain, error) {
	if err := cluster.PutGrainIfAbsent(ctx, x.getCluster(), grain); err != nil {
		if errors.Is(err, cluster.ErrGrainAlreadyExists) {
			owner, err := x.getCluster().GetGrain(ctx, grain.GetGrainId().GetValue())
			if err != nil {
				if errors.Is(err, cluster.ErrGrainNotFound) {
					return false, nil, nil
				}
				return false, nil, err
			}
			return false, owner, nil
		}
		return false, nil, err
	}
	return true, grain, nil
}

// sendToGrainOwner forwards a message to the owning node using Ask/Tell semantics.
func (x *actorSystem) sendToGrainOwner(ctx context.Context, owner *internalpb.Grain, message any, timeout time.Duration, synchronous bool) (any, error) {
	if owner == nil {
		return nil, errors.New("grain owner is unknown")
	}

	if synchronous {
		return x.sendRemoteAskGrainRequest(ctx, owner, message, timeout)
	}

	return nil, x.sendRemoteTellGrainRequest(ctx, owner, message)
}

// sendRemoteAskGrainRequest sends a request to a known Grain endpoint and returns the decoded reply.
// It handles protobuf serialization, context propagation headers, and response decoding.
func (x *actorSystem) sendRemoteAskGrainRequest(ctx context.Context, grain *internalpb.Grain, message any, timeout time.Duration) (any, error) {
	serializer := x.remoting.Serializer(message)
	if serializer == nil {
		return nil, gerrors.NewErrInvalidMessage(fmt.Errorf("no serializer found for message type %T", message))
	}

	serialized, err := serializer.Serialize(message)
	if err != nil {
		return nil, err
	}

	client := x.remoting.NetClient(grain.GetHost(), int(grain.GetPort()))
	request := &internalpb.RemoteAskGrainRequest{
		Grain:          grain,
		Message:        serialized,
		RequestTimeout: durationpb.New(timeout),
	}

	resp, err := client.SendProto(ctx, request)
	if err != nil {
		return nil, err
	}

	// Check for proto errors
	if errResp, ok := resp.(*internalpb.Error); ok {
		return nil, fmt.Errorf("proto error: code=%s, msg=%s", errResp.GetCode(), errResp.GetMessage())
	}

	askResp, ok := resp.(*internalpb.RemoteAskGrainResponse)
	if !ok {
		return nil, fmt.Errorf("invalid response type")
	}

	return x.remoting.Serializer(nil).Deserialize(askResp.GetMessage())
}

// sendRemoteTellGrainRequest sends a fire-and-forget message to a known Grain endpoint.
// It handles protobuf serialization and context propagation headers.
func (x *actorSystem) sendRemoteTellGrainRequest(ctx context.Context, grain *internalpb.Grain, message any) error {
	serializer := x.remoting.Serializer(message)
	if serializer == nil {
		return gerrors.NewErrInvalidMessage(fmt.Errorf("no serializer found for message type %T", message))
	}

	serialized, err := serializer.Serialize(message)
	if err != nil {
		return err
	}

	client := x.remoting.NetClient(grain.GetHost(), int(grain.GetPort()))
	request := &internalpb.RemoteTellGrainRequest{
		Grain:   grain,
		Message: serialized,
	}

	resp, err := client.SendProto(ctx, request)
	if err != nil {
		return err
	}

	// Check for proto errors
	if errResp, ok := resp.(*internalpb.Error); ok {
		return fmt.Errorf("proto error: code=%s, msg=%s", errResp.GetCode(), errResp.GetMessage())
	}

	return nil
}

// recreateGrain recreates a serialized Grain.
//
// It instantiates the grain, activates it, registers it locally, and updates the cluster registry.
// Returns an error if any step fails.
func (x *actorSystem) recreateGrain(ctx context.Context, serializedGrain *internalpb.Grain) error {
	grainID := serializedGrain.GetGrainId().GetValue()
	_, err := x.runGrainActivation(grainID, func() (*grainPID, error) {
		return x.recreateGrainOnce(ctx, serializedGrain)
	})
	return err
}

func (x *actorSystem) recreateGrainOnce(ctx context.Context, serializedGrain *internalpb.Grain) (*grainPID, error) {
	logger := x.logger
	logger.Infof("Recreating Grain (%s)...", serializedGrain.GrainId.GetValue())

	// make sure the grain is not a system grain
	if isSystemName(serializedGrain.GrainId.GetValue()) {
		return nil, gerrors.NewErrReservedName(serializedGrain.GetGrainId().GetValue())
	}

	// Parse grain identity
	identity, err := toIdentity(serializedGrain.GetGrainId().GetValue())
	if err != nil {
		return nil, err
	}

	var (
		process *grainPID
		ok      bool
	)

	process, ok = x.grains.Get(identity.String())
	if !ok {
		if err := x.waitForGrainActivationBarrier(ctx); err != nil {
			return nil, err
		}

		grain, err := x.getReflection().instantiateGrain(identity.Kind())
		if err != nil {
			return nil, err
		}

		dependencies, err := x.getReflection().dependenciesFromProto(serializedGrain.GetDependencies()...)
		if err != nil {
			return nil, err
		}

		options := []GrainOption{
			WithGrainInitTimeout(serializedGrain.GetActivationTimeout().AsDuration()),
			WithGrainInitMaxRetries(int(serializedGrain.GetActivationRetries())),
			WithGrainDependencies(dependencies...),
		}

		if serializedGrain.MailboxCapacity != nil {
			capacity := serializedGrain.GetMailboxCapacity()
			options = append(options, WithGrainMailboxCapacity(capacity))
		}

		config := newGrainConfig(options...)
		if err := config.Validate(); err != nil {
			return nil, err
		}

		process = newGrainPID(identity, grain, x, config)
		if err := process.activate(ctx); err != nil {
			return nil, err
		}

		// Register locally
		x.getGrains().Set(identity.String(), process)

		// Register in the cluster
		if err := x.putGrainOnCluster(process); err != nil {
			return nil, err
		}
		return process, nil
	}

	if !x.registry.Exists(process.getGrain()) {
		x.registry.Register(process.getGrain())
	}

	if !process.isActive() {
		if err := x.waitForGrainActivationBarrier(ctx); err != nil {
			return nil, err
		}

		if err := process.activate(ctx); err != nil {
			return nil, err
		}
	}

	// Register in the cluster
	if err := x.putGrainOnCluster(process); err != nil {
		return nil, err
	}
	return process, nil
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
			client := x.remoting.NetClient(peer.Host, peer.RemotingPort)
			addr := peer.RemotingAddress()
			request := &internalpb.GetNodeMetricRequest{NodeAddress: addr}

			resp, err := client.SendProto(egCtx, request)
			if err != nil {
				return fmt.Errorf("failed to fetch node metric from %s: %w", addr, err)
			}

			// Check for proto errors
			if errResp, ok := resp.(*internalpb.Error); ok {
				return fmt.Errorf("proto error from %s: code=%s, msg=%s", addr, errResp.GetCode(), errResp.GetMessage())
			}

			metricResp, ok := resp.(*internalpb.GetNodeMetricResponse)
			if !ok {
				return fmt.Errorf("invalid response type from %s", addr)
			}

			metrics[index] = nodeMetric{Peer: peer, Load: metricResp.GetLoad()}
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

func (x *actorSystem) setupGrainActivationBarrier(ctx context.Context) {
	if !x.clusterEnabled.Load() || x.clusterConfig == nil || !x.clusterConfig.grainActivationBarrierEnabled() {
		return
	}

	barrier := newGrainActivationBarrier(
		x.clusterConfig.minimumPeersQuorum,
		x.clusterConfig.grainActivationBarrierTimeout(),
	)
	x.grainBarrier = barrier

	if barrier.minPeers <= 1 {
		barrier.open()
		return
	}

	x.tryOpenGrainActivationBarrier(ctx)
}

func (x *actorSystem) tryOpenGrainActivationBarrier(ctx context.Context) {
	barrier := x.grainBarrier
	if barrier == nil {
		return
	}

	select {
	case <-barrier.ready:
		return
	default:
	}

	if x.cluster == nil {
		return
	}

	timeout := time.Second
	if x.clusterConfig != nil && x.clusterConfig.readTimeout > 0 {
		timeout = x.clusterConfig.readTimeout
	}

	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	peers, err := x.cluster.Members(checkCtx)
	if err != nil {
		return
	}

	if uint32(len(peers)) >= barrier.minPeers {
		barrier.open()
	}
}

func (x *actorSystem) waitForGrainActivationBarrier(ctx context.Context) error {
	barrier := x.grainBarrier
	if barrier == nil {
		return nil
	}
	return barrier.wait(ctx)
}
