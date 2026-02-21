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
	"slices"
	"time"

	"github.com/tochemey/goakt/v4/internal/datacentercontroller"
	"github.com/tochemey/goakt/v4/internal/ticker"
	"github.com/tochemey/goakt/v4/internal/types"
)

// DataCenterReady reports whether the multi-datacenter controller is operational.
func (x *actorSystem) DataCenterReady() bool {
	if !x.isDataCenterEnabled() {
		// Multi-DC not configured; nothing to wait for.
		return true
	}

	controller := x.getDataCenterController()
	if controller == nil {
		return false
	}

	return controller.Ready()
}

// DataCenterLastRefresh returns the time of the last successful datacenter cache refresh.
func (x *actorSystem) DataCenterLastRefresh() time.Time {
	if !x.isDataCenterEnabled() {
		return time.Time{}
	}

	controller := x.getDataCenterController()
	if controller == nil {
		return time.Time{}
	}

	return controller.LastRefresh()
}

// isDataCenterEnabled returns true if multi-data center support is configured and the cluster is ready.
func (x *actorSystem) isDataCenterEnabled() bool {
	return x.Running() &&
		x.clusterConfig != nil &&
		x.clusterConfig.dataCenterConfig != nil &&
		x.clusterEnabled.Load() &&
		x.cluster != nil
}

// stopControllerLocked stops the data center controller if running.
// Caller must hold dataCenterControllerMutex.
func (x *actorSystem) stopControllerLocked(ctx context.Context) error {
	if x.dataCenterController == nil {
		return nil
	}

	if err := x.dataCenterController.Stop(ctx); err != nil {
		x.logger.Errorf("failed to stop data center controller: %v", err)
		return err
	}

	x.dataCenterController = nil
	return nil
}

// startDataCenterController starts the multi-data center controller if the
// current node is the leader for its data center. If the node is not the leader,
// it stops any running controller instance.
func (x *actorSystem) startDataCenterController(ctx context.Context) error {
	if x.isStopping() {
		return nil
	}

	if !x.isDataCenterEnabled() {
		return nil
	}

	if !x.dataCenterReconcileInFlight.CompareAndSwap(false, true) {
		return nil
	}

	defer x.dataCenterReconcileInFlight.Store(false)

	isLeader := x.cluster.IsLeader(ctx)

	x.dataCenterControllerMutex.Lock()
	defer x.dataCenterControllerMutex.Unlock()

	// Only the DC leader should run the manager. Followers must stop it to
	// avoid multiple writers and conflicting heartbeats in the control plane.
	//
	// We still handle the follower stop path because leadership can change over
	// time (re-election, partition heal). A node that previously led the DC
	// must stop its manager when it loses leadership to prevent stale writers.
	if !isLeader {
		return x.stopControllerLocked(ctx)
	}

	// If controller already exists, check if endpoints need updating
	if x.dataCenterController != nil {
		return x.maybeUpdateEndpoints(ctx)
	}

	// let us fetch the endpoints from the cluster
	members, err := x.cluster.Members(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch cluster members: %w", err)
	}

	endpoints := make([]string, len(members))
	for i, member := range members {
		endpoints[i] = member.RemotingAddress()
	}

	// Leader is responsible for owning the controller lifecycle; start it once.
	controller, err := datacentercontroller.NewController(x.clusterConfig.dataCenterConfig, endpoints)
	if err != nil {
		return err
	}

	if err := controller.Start(ctx); err != nil {
		return err
	}

	x.dataCenterController = controller
	return nil
}

// maybeUpdateEndpoints checks if cluster membership has changed and updates
// the data center controller's endpoints if needed.
// Caller must hold dataCenterControllerMutex.
func (x *actorSystem) maybeUpdateEndpoints(ctx context.Context) error {
	if x.dataCenterController == nil {
		return nil
	}

	// Get current cluster members
	members, err := x.cluster.Members(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch cluster members: %w", err)
	}

	if len(members) == 0 {
		// No members available; keep existing endpoints
		return nil
	}

	// Build current endpoints from members
	currentEndpoints := make([]string, len(members))
	for i, member := range members {
		currentEndpoints[i] = member.RemotingAddress()
	}

	// Compare with registered endpoints
	registeredEndpoints := x.dataCenterController.Endpoints()
	if slices.Equal(currentEndpoints, registeredEndpoints) {
		// Endpoints unchanged; nothing to do
		return nil
	}

	// Endpoints changed; update the controller
	x.logger.Infof("Data center endpoints changed from %v to %v; updating controller",
		registeredEndpoints, currentEndpoints)

	if err := x.dataCenterController.UpdateEndpoints(ctx, currentEndpoints); err != nil {
		return fmt.Errorf("failed to update data center endpoints: %w", err)
	}

	x.logger.Infof("Data center endpoints updated successfully")
	return nil
}

func (x *actorSystem) triggerDataCentersReconciliation() {
	if x.isStopping() {
		return
	}

	if x.dataCenterReconcileInFlight.Load() {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), x.shutdownTimeout)
		defer cancel()
		if err := x.startDataCenterController(ctx); err != nil {
			x.logger.Errorf("failed to reconcile data centers: %v", err)
		}
	}()
}

func (x *actorSystem) startDataCenterLeaderWatch(ctx context.Context) error {
	if !x.isDataCenterEnabled() {
		return nil
	}

	x.dataCenterLeaderMutex.Lock()
	defer x.dataCenterLeaderMutex.Unlock()

	if x.dataCenterLeaderTicker != nil {
		return nil
	}

	interval := x.clusterConfig.dataCenterConfig.LeaderCheckInterval
	clock := ticker.New(interval)
	stopSig := make(chan types.Unit, 1)

	x.dataCenterLeaderTicker = clock
	x.dataCenterLeaderStopWatch = stopSig

	clock.Start()
	go x.dataCenterLeaderWatchLoop(ctx, clock, stopSig)
	return nil
}

func (x *actorSystem) dataCenterLeaderWatchLoop(ctx context.Context, clock *ticker.Ticker, stopSig <-chan types.Unit) {
	defer clock.Stop()

	for {
		select {
		case <-clock.Ticks:
			x.triggerDataCentersReconciliation()
		case <-stopSig:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (x *actorSystem) stopDataCenterController(ctx context.Context) error {
	x.dataCenterControllerMutex.Lock()
	defer x.dataCenterControllerMutex.Unlock()
	return x.stopControllerLocked(ctx)
}

func (x *actorSystem) stopDataCenterLeaderWatch() {
	x.dataCenterLeaderMutex.Lock()
	defer x.dataCenterLeaderMutex.Unlock()

	if x.dataCenterLeaderTicker == nil {
		return
	}

	x.dataCenterLeaderTicker.Stop()
	x.dataCenterLeaderTicker = nil

	if x.dataCenterLeaderStopWatch != nil {
		select {
		case x.dataCenterLeaderStopWatch <- types.Unit{}:
		default:
		}
		x.dataCenterLeaderStopWatch = nil
	}
}
