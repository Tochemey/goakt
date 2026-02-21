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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/datacenter"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/datacentercontroller"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/internal/ticker"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	mockscluster "github.com/tochemey/goakt/v4/mocks/cluster"
	mocksremote "github.com/tochemey/goakt/v4/mocks/remote"
)

func TestDataCenterReady(t *testing.T) {
	t.Run("returns true when multi-DC is not enabled", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger

		actorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		require.NoError(t, err)

		err = actorSystem.Start(ctx)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, actorSystem.Stop(ctx)) })

		// Multi-DC not configured, should return true (nothing to wait for)
		assert.True(t, actorSystem.DataCenterReady())
	})

	t.Run("returns true when actor system not started and multi-DC not enabled", func(t *testing.T) {
		logger := log.DiscardLogger

		actorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		require.NoError(t, err)

		// Multi-DC not configured, should return true even before start
		assert.True(t, actorSystem.DataCenterReady())
	})

	t.Run("returns false when multi-DC enabled but controller is nil", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		sys := MockReplicationTestSystem(clusterMock)
		sys.clusterConfig = NewClusterConfig().WithDataCenter(datacenter.NewConfig())
		sys.dataCenterController = nil

		// Multi-DC enabled but no controller yet
		assert.False(t, sys.DataCenterReady())
	})

	t.Run("returns true when multi-DC enabled and controller is ready", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{{ID: "dc-1", State: datacenter.DataCenterActive}}, nil
		}, remotingMock)

		pause.For(25 * time.Millisecond) // allow cache refresh
		assert.True(t, sys.DataCenterReady())
	})

	t.Run("returns false when multi-DC enabled and controller not yet ready", func(t *testing.T) {
		dcConfig := datacenter.NewConfig()
		dcConfig.ControlPlane = &MockControlPlane{listActive: func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return nil, nil
		}}
		dcConfig.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}

		controller, err := datacentercontroller.NewController(dcConfig, []string{"127.0.0.1:8080"})
		require.NoError(t, err)
		// Do NOT start the controller - Ready() will be false

		clusterMock := mockscluster.NewCluster(t)
		sys := MockReplicationTestSystem(clusterMock)
		sys.clusterConfig = NewClusterConfig().WithDataCenter(dcConfig)
		sys.dataCenterController = controller

		assert.False(t, sys.DataCenterReady())
	})
}

func TestDataCenterLastRefresh(t *testing.T) {
	t.Run("returns zero time when multi-DC is not enabled", func(t *testing.T) {
		ctx := context.TODO()
		logger := log.DiscardLogger

		actorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		require.NoError(t, err)

		err = actorSystem.Start(ctx)
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, actorSystem.Stop(ctx)) })

		// Multi-DC not configured, should return zero time
		assert.True(t, actorSystem.DataCenterLastRefresh().IsZero())
	})

	t.Run("returns zero time when actor system not started", func(t *testing.T) {
		logger := log.DiscardLogger

		actorSystem, err := NewActorSystem(
			"test",
			WithLogger(logger),
		)
		require.NoError(t, err)

		// Multi-DC not configured, should return zero time
		assert.True(t, actorSystem.DataCenterLastRefresh().IsZero())
	})

	t.Run("returns zero time when multi-DC enabled but controller is nil", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		sys := MockReplicationTestSystem(clusterMock)
		sys.clusterConfig = NewClusterConfig().WithDataCenter(datacenter.NewConfig())
		sys.dataCenterController = nil

		assert.True(t, sys.DataCenterLastRefresh().IsZero())
	})

	t.Run("returns non-zero when multi-DC enabled and controller has refreshed", func(t *testing.T) {
		remotingMock := mocksremote.NewRemoting(t)
		sys := MockDatacenterSystem(t, func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return []datacenter.DataCenterRecord{{ID: "dc-1", State: datacenter.DataCenterActive}}, nil
		}, remotingMock)

		pause.For(25 * time.Millisecond)
		assert.False(t, sys.DataCenterLastRefresh().IsZero())
	})
}

func TestStopDataCenterController(t *testing.T) {
	t.Run("returns nil when controller is nil", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		sys := MockReplicationTestSystem(clusterMock)
		sys.dataCenterController = nil

		err := sys.stopDataCenterController(context.Background())
		require.NoError(t, err)
	})

	t.Run("stops controller successfully", func(t *testing.T) {
		dcConfig := datacenter.NewConfig()
		dcConfig.ControlPlane = &MockControlPlane{listActive: func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return nil, nil
		}}
		dcConfig.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}
		dcConfig.CacheRefreshInterval = 10 * time.Millisecond

		controller, err := datacentercontroller.NewController(dcConfig, []string{"127.0.0.1:8080"})
		require.NoError(t, err)
		startCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = controller.Start(startCtx)
		cancel()
		require.NoError(t, err)

		clusterMock := mockscluster.NewCluster(t)
		sys := MockReplicationTestSystem(clusterMock)
		sys.dataCenterController = controller
		sys.shutdownTimeout = 5 * time.Second

		err = sys.stopDataCenterController(context.Background())
		require.NoError(t, err)
	})

	t.Run("returns error when controller stop fails", func(t *testing.T) {
		dcConfig := datacenter.NewConfig()
		dcConfig.ControlPlane = &MockFailingSetStateControlPlane{}
		dcConfig.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}

		controller, err := datacentercontroller.NewController(dcConfig, []string{"127.0.0.1:8080"})
		require.NoError(t, err)
		startCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = controller.Start(startCtx)
		cancel()
		require.NoError(t, err)

		clusterMock := mockscluster.NewCluster(t)
		sys := MockReplicationTestSystem(clusterMock)
		sys.dataCenterController = controller

		err = sys.stopDataCenterController(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "set state failed")
	})
}

func TestStartDataCenterController(t *testing.T) {
	validDCConfig := func() *datacenter.Config {
		cfg := datacenter.NewConfig()
		cfg.ControlPlane = &MockControlPlane{listActive: func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return nil, nil
		}}
		cfg.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}
		cfg.CacheRefreshInterval = 10 * time.Millisecond
		cfg.LeaderCheckInterval = 50 * time.Millisecond
		return cfg
	}

	t.Run("returns nil when stopping", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		sys := MockReplicationTestSystem(clusterMock)
		sys.shuttingDown.Store(true)
		sys.clusterConfig = NewClusterConfig().WithDataCenter(validDCConfig())
		sys.cluster = clusterMock

		err := sys.startDataCenterController(context.Background())
		require.NoError(t, err)
	})

	t.Run("returns nil when reconcile already in flight", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		sys := MockReplicationTestSystem(clusterMock)
		sys.dataCenterReconcileInFlight.Store(true)
		sys.clusterConfig = NewClusterConfig().WithDataCenter(validDCConfig())
		sys.cluster = clusterMock

		err := sys.startDataCenterController(context.Background())
		require.NoError(t, err)
	})

	t.Run("returns nil when data center not enabled", func(t *testing.T) {
		sys := MockReplicationTestSystem(mockscluster.NewCluster(t))
		sys.clusterConfig = nil
		sys.cluster = nil
		sys.clusterEnabled.Store(false)

		err := sys.startDataCenterController(context.Background())
		require.NoError(t, err)
	})

	t.Run("stops controller when not leader", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		clusterMock.EXPECT().IsLeader(mock.Anything).Return(false)

		dcConfig := validDCConfig()
		controller, err := datacentercontroller.NewController(dcConfig, []string{"127.0.0.1:8080"})
		require.NoError(t, err)
		startCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = controller.Start(startCtx)
		cancel()
		require.NoError(t, err)
		t.Cleanup(func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
			_ = controller.Stop(stopCtx)
			stopCancel()
		})

		sys := MockReplicationTestSystem(clusterMock)
		sys.clusterConfig = NewClusterConfig().WithDataCenter(dcConfig)
		sys.dataCenterController = controller
		sys.shutdownTimeout = 5 * time.Second

		err = sys.startDataCenterController(context.Background())
		require.NoError(t, err)
		assert.Nil(t, sys.dataCenterController)
	})

	t.Run("returns nil when controller already exists", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		clusterMock.EXPECT().IsLeader(mock.Anything).Return(true)
		// Expect Members() call for endpoint check (returns same endpoints as controller has)
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{
			{Host: "127.0.0.1", RemotingPort: 8080},
		}, nil)

		dcConfig := validDCConfig()
		controller, err := datacentercontroller.NewController(dcConfig, []string{"127.0.0.1:8080"})
		require.NoError(t, err)
		startCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err = controller.Start(startCtx)
		cancel()
		require.NoError(t, err)
		t.Cleanup(func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
			_ = controller.Stop(stopCtx)
			stopCancel()
		})

		sys := MockReplicationTestSystem(clusterMock)
		sys.clusterConfig = NewClusterConfig().WithDataCenter(dcConfig)
		sys.dataCenterController = controller

		err = sys.startDataCenterController(context.Background())
		require.NoError(t, err)
	})

	t.Run("returns error when NewController fails", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		clusterMock.EXPECT().IsLeader(mock.Anything).Return(true)
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{}, nil) // empty endpoints -> NewController fails

		invalidConfig := datacenter.NewConfig()
		invalidConfig.ControlPlane = &MockControlPlane{}
		invalidConfig.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}

		sys := MockReplicationTestSystem(clusterMock)
		sys.clusterConfig = NewClusterConfig().WithDataCenter(invalidConfig)
		sys.dataCenterController = nil

		err := sys.startDataCenterController(context.Background())
		require.Error(t, err)
	})

	t.Run("returns error when controller Start fails", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		clusterMock.EXPECT().IsLeader(mock.Anything).Return(true)
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{{Host: "127.0.0.1", RemotingPort: 8080}}, nil)

		cfg := datacenter.NewConfig()
		cfg.ControlPlane = &MockFailingRegisterControlPlane{}
		cfg.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}

		sys := MockReplicationTestSystem(clusterMock)
		sys.clusterConfig = NewClusterConfig().WithDataCenter(cfg)
		sys.dataCenterController = nil

		err := sys.startDataCenterController(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "register failed")
	})

	t.Run("starts controller successfully when leader", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		clusterMock.EXPECT().IsLeader(mock.Anything).Return(true)
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{{Host: "127.0.0.1", RemotingPort: 8080}}, nil)

		dcConfig := validDCConfig()
		sys := MockReplicationTestSystem(clusterMock)
		sys.clusterConfig = NewClusterConfig().WithDataCenter(dcConfig)
		sys.dataCenterController = nil
		sys.shutdownTimeout = 5 * time.Second

		err := sys.startDataCenterController(context.Background())
		require.NoError(t, err)
		require.NotNil(t, sys.dataCenterController)
		t.Cleanup(func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
			_ = sys.stopDataCenterController(stopCtx)
			stopCancel()
		})
	})
}

func TestTriggerDataCentersReconciliation(t *testing.T) {
	t.Run("returns early when stopping", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		sys := MockReplicationTestSystem(clusterMock)
		sys.shuttingDown.Store(true)
		sys.clusterConfig = NewClusterConfig().WithDataCenter(datacenter.NewConfig())

		sys.triggerDataCentersReconciliation()
		// No panic, no hang - early return
	})

	t.Run("returns early when reconcile in flight", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		sys := MockReplicationTestSystem(clusterMock)
		sys.dataCenterReconcileInFlight.Store(true)

		sys.triggerDataCentersReconciliation()
	})

	t.Run("spawns goroutine and reconciles", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{{Host: "127.0.0.1", RemotingPort: 8080}}, nil).Maybe()

		dcConfig := datacenter.NewConfig()
		dcConfig.ControlPlane = &MockControlPlane{listActive: func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return nil, nil
		}}
		dcConfig.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}
		dcConfig.CacheRefreshInterval = 10 * time.Millisecond
		dcConfig.LeaderCheckInterval = 50 * time.Millisecond

		sys := MockReplicationTestSystem(clusterMock)
		sys.clusterConfig = NewClusterConfig().WithDataCenter(dcConfig)
		sys.shutdownTimeout = 5 * time.Second

		sys.triggerDataCentersReconciliation()
		pause.For(100 * time.Millisecond) // allow goroutine to run
	})

	t.Run("logs error when reconcile fails", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{}, nil).Maybe() // empty -> NewController fails

		invalidConfig := datacenter.NewConfig()
		invalidConfig.ControlPlane = &MockControlPlane{}
		invalidConfig.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}

		sys := MockReplicationTestSystem(clusterMock)
		sys.clusterConfig = NewClusterConfig().WithDataCenter(invalidConfig)
		sys.shutdownTimeout = 5 * time.Second

		sys.triggerDataCentersReconciliation()
		pause.For(100 * time.Millisecond) // allow goroutine to run and log error
	})
}

func TestStartDataCenterLeaderWatch(t *testing.T) {
	validDCConfig := func() *datacenter.Config {
		cfg := datacenter.NewConfig()
		cfg.ControlPlane = &MockControlPlane{}
		cfg.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}
		cfg.LeaderCheckInterval = 100 * time.Millisecond
		return cfg
	}

	t.Run("returns nil when data center not enabled", func(t *testing.T) {
		sys := MockReplicationTestSystem(mockscluster.NewCluster(t))
		sys.clusterConfig = nil

		err := sys.startDataCenterLeaderWatch(context.Background())
		require.NoError(t, err)
	})

	t.Run("returns nil when ticker already exists", func(t *testing.T) {
		sys := MockReplicationTestSystem(mockscluster.NewCluster(t))
		sys.clusterConfig = NewClusterConfig().WithDataCenter(validDCConfig())

		err := sys.startDataCenterLeaderWatch(context.Background())
		require.NoError(t, err)

		err = sys.startDataCenterLeaderWatch(context.Background())
		require.NoError(t, err)

		sys.stopDataCenterLeaderWatch()
	})

	t.Run("starts leader watch successfully", func(t *testing.T) {
		sys := MockReplicationTestSystem(mockscluster.NewCluster(t))
		sys.clusterConfig = NewClusterConfig().WithDataCenter(validDCConfig())

		err := sys.startDataCenterLeaderWatch(context.Background())
		require.NoError(t, err)
		require.NotNil(t, sys.dataCenterLeaderTicker)

		sys.stopDataCenterLeaderWatch()
	})
}

func TestStopDataCenterLeaderWatch(t *testing.T) {
	t.Run("returns early when ticker is nil", func(t *testing.T) {
		sys := MockReplicationTestSystem(mockscluster.NewCluster(t))
		sys.dataCenterLeaderTicker = nil
		sys.dataCenterLeaderStopWatch = nil

		sys.stopDataCenterLeaderWatch()
	})

	t.Run("stops ticker and sends stop signal", func(t *testing.T) {
		cfg := datacenter.NewConfig()
		cfg.ControlPlane = &MockControlPlane{}
		cfg.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}
		cfg.LeaderCheckInterval = 50 * time.Millisecond

		sys := MockReplicationTestSystem(mockscluster.NewCluster(t))
		sys.clusterConfig = NewClusterConfig().WithDataCenter(cfg)
		require.NoError(t, sys.startDataCenterLeaderWatch(context.Background()))

		sys.stopDataCenterLeaderWatch()
		assert.Nil(t, sys.dataCenterLeaderTicker)
		assert.Nil(t, sys.dataCenterLeaderStopWatch)
	})

	t.Run("handles full stop channel gracefully", func(t *testing.T) {
		sys := MockReplicationTestSystem(mockscluster.NewCluster(t))
		clock := ticker.New(50 * time.Millisecond)
		stopSig := make(chan types.Unit, 1)
		stopSig <- types.Unit{} // pre-fill so next send would block

		sys.dataCenterLeaderTicker = clock
		sys.dataCenterLeaderStopWatch = stopSig

		clock.Start()
		sys.stopDataCenterLeaderWatch() // exercises default branch when channel is full
		assert.Nil(t, sys.dataCenterLeaderTicker)
		assert.Nil(t, sys.dataCenterLeaderStopWatch)
	})
}

func TestDataCenterLeaderWatchLoop(t *testing.T) {
	t.Run("exits on stop signal", func(t *testing.T) {
		cfg := datacenter.NewConfig()
		cfg.LeaderCheckInterval = 10 * time.Millisecond
		clock := ticker.New(10 * time.Millisecond)
		stopSig := make(chan types.Unit, 1)

		sys := MockReplicationTestSystem(mockscluster.NewCluster(t))
		sys.clusterConfig = NewClusterConfig().WithDataCenter(cfg)
		sys.shutdownTimeout = 5 * time.Second

		ctx := context.Background()
		clock.Start()
		go sys.dataCenterLeaderWatchLoop(ctx, clock, stopSig)

		stopSig <- types.Unit{}
		pause.For(50 * time.Millisecond)
	})

	t.Run("exits on context cancel", func(t *testing.T) {
		clock := ticker.New(10 * time.Millisecond)
		stopSig := make(chan types.Unit, 1)

		sys := MockReplicationTestSystem(mockscluster.NewCluster(t))
		sys.clusterConfig = NewClusterConfig().WithDataCenter(datacenter.NewConfig())
		sys.shutdownTimeout = 5 * time.Second

		ctx, cancel := context.WithCancel(context.Background())
		clock.Start()
		go sys.dataCenterLeaderWatchLoop(ctx, clock, stopSig)

		cancel()
		pause.For(50 * time.Millisecond)
	})

	t.Run("triggers reconciliation on tick", func(t *testing.T) {
		clusterMock := mockscluster.NewCluster(t)
		clusterMock.EXPECT().IsLeader(mock.Anything).Return(true).Maybe()
		clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{{Host: "127.0.0.1", RemotingPort: 8080}}, nil).Maybe()

		cfg := datacenter.NewConfig()
		cfg.ControlPlane = &MockControlPlane{listActive: func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return nil, nil
		}}
		cfg.DataCenter = datacenter.DataCenter{Name: "local", Region: "r", Zone: "z"}
		cfg.LeaderCheckInterval = 5 * time.Millisecond
		cfg.CacheRefreshInterval = 10 * time.Millisecond

		clock := ticker.New(5 * time.Millisecond)
		stopSig := make(chan types.Unit, 1)

		sys := MockReplicationTestSystem(clusterMock)
		sys.clusterConfig = NewClusterConfig().WithDataCenter(cfg)
		sys.shutdownTimeout = 5 * time.Second

		ctx := context.Background()
		clock.Start()
		go sys.dataCenterLeaderWatchLoop(ctx, clock, stopSig)

		pause.For(50 * time.Millisecond)
		stopSig <- types.Unit{}
	})
}

func TestMaybeUpdateEndpointsNoController(t *testing.T) {
	clusterMock := mockscluster.NewCluster(t)
	sys := MockReplicationTestSystem(clusterMock)
	sys.dataCenterController = nil

	err := sys.maybeUpdateEndpoints(context.Background())
	require.NoError(t, err)
}

func TestMaybeUpdateEndpointsMembersFetchError(t *testing.T) {
	clusterMock := mockscluster.NewCluster(t)
	clusterMock.EXPECT().Members(mock.Anything).Return(nil, assert.AnError)

	cfg := datacenter.NewConfig()
	cfg.ControlPlane = &MockControlPlane{}
	cfg.DataCenter = datacenter.DataCenter{Name: "dc1"}

	sys := MockReplicationTestSystem(clusterMock)
	sys.clusterConfig = NewClusterConfig().WithDataCenter(cfg)

	// Create a controller
	controller, err := datacentercontroller.NewController(cfg, []string{"127.0.0.1:8080"})
	require.NoError(t, err)
	sys.dataCenterController = controller

	err = sys.maybeUpdateEndpoints(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to fetch cluster members")
}

func TestMaybeUpdateEndpointsNoMembers(t *testing.T) {
	clusterMock := mockscluster.NewCluster(t)
	clusterMock.EXPECT().Members(mock.Anything).Return([]*cluster.Peer{}, nil)

	cfg := datacenter.NewConfig()
	cfg.ControlPlane = &MockControlPlane{}
	cfg.DataCenter = datacenter.DataCenter{Name: "dc1"}

	sys := MockReplicationTestSystem(clusterMock)
	sys.clusterConfig = NewClusterConfig().WithDataCenter(cfg)

	// Create a controller
	controller, err := datacentercontroller.NewController(cfg, []string{"127.0.0.1:8080"})
	require.NoError(t, err)
	sys.dataCenterController = controller

	// Should not fail with empty members
	err = sys.maybeUpdateEndpoints(context.Background())
	require.NoError(t, err)
}

func TestMaybeUpdateEndpointsUnchanged(t *testing.T) {
	members := []*cluster.Peer{
		{Host: "127.0.0.1", RemotingPort: 8080},
		{Host: "127.0.0.2", RemotingPort: 8080},
	}
	clusterMock := mockscluster.NewCluster(t)
	clusterMock.EXPECT().Members(mock.Anything).Return(members, nil)

	cfg := datacenter.NewConfig()
	cfg.ControlPlane = &MockControlPlane{}
	cfg.DataCenter = datacenter.DataCenter{Name: "dc1"}

	initialEndpoints := []string{"127.0.0.1:8080", "127.0.0.2:8080"}

	sys := MockReplicationTestSystem(clusterMock)
	sys.clusterConfig = NewClusterConfig().WithDataCenter(cfg)

	// Create a controller with the same endpoints as current members
	controller, err := datacentercontroller.NewController(cfg, initialEndpoints)
	require.NoError(t, err)
	sys.dataCenterController = controller

	err = sys.maybeUpdateEndpoints(context.Background())
	require.NoError(t, err)

	// Endpoints should remain unchanged
	require.Equal(t, initialEndpoints, controller.Endpoints())
}

func TestMaybeUpdateEndpointsChanged(t *testing.T) {
	// Start with 2 members, then add a 3rd
	newMembers := []*cluster.Peer{
		{Host: "127.0.0.1", RemotingPort: 8080},
		{Host: "127.0.0.2", RemotingPort: 8080},
		{Host: "127.0.0.3", RemotingPort: 8080},
	}
	clusterMock := mockscluster.NewCluster(t)
	clusterMock.EXPECT().Members(mock.Anything).Return(newMembers, nil)

	var registeredRecords []datacenter.DataCenterRecord
	cfg := datacenter.NewConfig()
	cfg.ControlPlane = &testControlPlane{
		registerFn: func(_ context.Context, record datacenter.DataCenterRecord) (string, uint64, error) {
			registeredRecords = append(registeredRecords, record)
			return "dc1", uint64(len(registeredRecords)), nil
		},
		setStateFn: func(_ context.Context, _ string, _ datacenter.DataCenterState, version uint64) (uint64, error) {
			return version + 1, nil
		},
		listActiveFn: func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return nil, nil
		},
	}
	cfg.DataCenter = datacenter.DataCenter{Name: "dc1"}

	initialEndpoints := []string{"127.0.0.1:8080", "127.0.0.2:8080"}

	sys := MockReplicationTestSystem(clusterMock)
	sys.clusterConfig = NewClusterConfig().WithDataCenter(cfg)

	// Create and start controller with initial endpoints
	controller, err := datacentercontroller.NewController(cfg, initialEndpoints)
	require.NoError(t, err)
	require.NoError(t, controller.Start(context.Background()))
	defer func() { _ = controller.Stop(context.Background()) }()

	sys.dataCenterController = controller

	// Trigger update
	err = sys.maybeUpdateEndpoints(context.Background())
	require.NoError(t, err)

	// Verify endpoints were updated
	expectedEndpoints := []string{"127.0.0.1:8080", "127.0.0.2:8080", "127.0.0.3:8080"}
	require.Equal(t, expectedEndpoints, controller.Endpoints())

	// Verify re-registration happened (initial register + setState to ACTIVE + update)
	require.GreaterOrEqual(t, len(registeredRecords), 2)
	lastRecord := registeredRecords[len(registeredRecords)-1]
	require.Equal(t, expectedEndpoints, lastRecord.Endpoints)
}

func TestMaybeUpdateEndpointsUpdateFails(t *testing.T) {
	// Scenario: endpoints differ, but UpdateEndpoints fails
	newMembers := []*cluster.Peer{
		{Host: "127.0.0.1", RemotingPort: 8080},
		{Host: "127.0.0.2", RemotingPort: 8080},
		{Host: "127.0.0.3", RemotingPort: 8080},
	}
	clusterMock := mockscluster.NewCluster(t)
	clusterMock.EXPECT().Members(mock.Anything).Return(newMembers, nil)

	updateErr := errors.New("control plane update failed")
	var registerCallCount int
	cfg := datacenter.NewConfig()
	cfg.ControlPlane = &testControlPlane{
		registerFn: func(_ context.Context, record datacenter.DataCenterRecord) (string, uint64, error) {
			registerCallCount++
			// First call (initial registration) succeeds
			if registerCallCount == 1 {
				return "dc1", 1, nil
			}
			// Second call (update) fails
			return "", 0, updateErr
		},
		setStateFn: func(_ context.Context, _ string, _ datacenter.DataCenterState, version uint64) (uint64, error) {
			return version + 1, nil
		},
		listActiveFn: func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return nil, nil
		},
	}
	cfg.DataCenter = datacenter.DataCenter{Name: "dc1"}

	initialEndpoints := []string{"127.0.0.1:8080", "127.0.0.2:8080"}

	sys := MockReplicationTestSystem(clusterMock)
	sys.clusterConfig = NewClusterConfig().WithDataCenter(cfg)

	// Create and start controller with initial endpoints
	controller, err := datacentercontroller.NewController(cfg, initialEndpoints)
	require.NoError(t, err)
	require.NoError(t, controller.Start(context.Background()))
	defer func() { _ = controller.Stop(context.Background()) }()

	sys.dataCenterController = controller

	// Trigger update - should fail
	err = sys.maybeUpdateEndpoints(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to update data center endpoints")

	// Original endpoints should be unchanged since update failed
	require.Equal(t, initialEndpoints, controller.Endpoints())
}

func TestMaybeUpdateEndpointsOrderMatters(t *testing.T) {
	// slices.Equal is order-sensitive; different order = different endpoints
	// Controller has [A, B], cluster has [B, A] → should trigger update
	clusterMembers := []*cluster.Peer{
		{Host: "127.0.0.2", RemotingPort: 8080},
		{Host: "127.0.0.1", RemotingPort: 8080},
	}
	clusterMock := mockscluster.NewCluster(t)
	clusterMock.EXPECT().Members(mock.Anything).Return(clusterMembers, nil)

	var registeredRecords []datacenter.DataCenterRecord
	cfg := datacenter.NewConfig()
	cfg.ControlPlane = &testControlPlane{
		registerFn: func(_ context.Context, record datacenter.DataCenterRecord) (string, uint64, error) {
			registeredRecords = append(registeredRecords, record)
			return "dc1", uint64(len(registeredRecords)), nil
		},
		setStateFn: func(_ context.Context, _ string, _ datacenter.DataCenterState, version uint64) (uint64, error) {
			return version + 1, nil
		},
		listActiveFn: func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return nil, nil
		},
	}
	cfg.DataCenter = datacenter.DataCenter{Name: "dc1"}

	// Initial endpoints in different order than cluster will report
	initialEndpoints := []string{"127.0.0.1:8080", "127.0.0.2:8080"}

	sys := MockReplicationTestSystem(clusterMock)
	sys.clusterConfig = NewClusterConfig().WithDataCenter(cfg)

	// Create and start controller
	controller, err := datacentercontroller.NewController(cfg, initialEndpoints)
	require.NoError(t, err)
	require.NoError(t, controller.Start(context.Background()))
	defer func() { _ = controller.Stop(context.Background()) }()

	sys.dataCenterController = controller

	// Trigger update
	err = sys.maybeUpdateEndpoints(context.Background())
	require.NoError(t, err)

	// Endpoints should be updated to match cluster order
	expectedEndpoints := []string{"127.0.0.2:8080", "127.0.0.1:8080"}
	require.Equal(t, expectedEndpoints, controller.Endpoints())
}

func TestMaybeUpdateEndpointsSingleMember(t *testing.T) {
	// Edge case: cluster with only one member
	singleMember := []*cluster.Peer{
		{Host: "127.0.0.1", RemotingPort: 8080},
	}
	clusterMock := mockscluster.NewCluster(t)
	clusterMock.EXPECT().Members(mock.Anything).Return(singleMember, nil)

	cfg := datacenter.NewConfig()
	cfg.ControlPlane = &testControlPlane{}
	cfg.DataCenter = datacenter.DataCenter{Name: "dc1"}

	initialEndpoints := []string{"127.0.0.1:8080"}

	sys := MockReplicationTestSystem(clusterMock)
	sys.clusterConfig = NewClusterConfig().WithDataCenter(cfg)

	controller, err := datacentercontroller.NewController(cfg, initialEndpoints)
	require.NoError(t, err)
	sys.dataCenterController = controller

	// Should not update (same single endpoint)
	err = sys.maybeUpdateEndpoints(context.Background())
	require.NoError(t, err)
	require.Equal(t, initialEndpoints, controller.Endpoints())
}

func TestMaybeUpdateEndpointsDifferentPorts(t *testing.T) {
	// Members with different remoting ports
	members := []*cluster.Peer{
		{Host: "127.0.0.1", RemotingPort: 8080},
		{Host: "127.0.0.1", RemotingPort: 8081},
		{Host: "127.0.0.1", RemotingPort: 8082},
	}
	clusterMock := mockscluster.NewCluster(t)
	clusterMock.EXPECT().Members(mock.Anything).Return(members, nil)

	var registeredRecords []datacenter.DataCenterRecord
	cfg := datacenter.NewConfig()
	cfg.ControlPlane = &testControlPlane{
		registerFn: func(_ context.Context, record datacenter.DataCenterRecord) (string, uint64, error) {
			registeredRecords = append(registeredRecords, record)
			return "dc1", uint64(len(registeredRecords)), nil
		},
		setStateFn: func(_ context.Context, _ string, _ datacenter.DataCenterState, version uint64) (uint64, error) {
			return version + 1, nil
		},
		listActiveFn: func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return nil, nil
		},
	}
	cfg.DataCenter = datacenter.DataCenter{Name: "dc1"}

	initialEndpoints := []string{"127.0.0.1:8080"}

	sys := MockReplicationTestSystem(clusterMock)
	sys.clusterConfig = NewClusterConfig().WithDataCenter(cfg)

	controller, err := datacentercontroller.NewController(cfg, initialEndpoints)
	require.NoError(t, err)
	require.NoError(t, controller.Start(context.Background()))
	defer func() { _ = controller.Stop(context.Background()) }()

	sys.dataCenterController = controller

	// Trigger update
	err = sys.maybeUpdateEndpoints(context.Background())
	require.NoError(t, err)

	// Should include all three ports
	expectedEndpoints := []string{"127.0.0.1:8080", "127.0.0.1:8081", "127.0.0.1:8082"}
	require.Equal(t, expectedEndpoints, controller.Endpoints())
}

func TestMaybeUpdateEndpointsMemberRemoved(t *testing.T) {
	// Scenario: member removed from cluster (scale down)
	remainingMembers := []*cluster.Peer{
		{Host: "127.0.0.1", RemotingPort: 8080},
	}
	clusterMock := mockscluster.NewCluster(t)
	clusterMock.EXPECT().Members(mock.Anything).Return(remainingMembers, nil)

	var registeredRecords []datacenter.DataCenterRecord
	cfg := datacenter.NewConfig()
	cfg.ControlPlane = &testControlPlane{
		registerFn: func(_ context.Context, record datacenter.DataCenterRecord) (string, uint64, error) {
			registeredRecords = append(registeredRecords, record)
			return "dc1", uint64(len(registeredRecords)), nil
		},
		setStateFn: func(_ context.Context, _ string, _ datacenter.DataCenterState, version uint64) (uint64, error) {
			return version + 1, nil
		},
		listActiveFn: func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return nil, nil
		},
	}
	cfg.DataCenter = datacenter.DataCenter{Name: "dc1"}

	// Initial endpoints include a member that will be removed
	initialEndpoints := []string{"127.0.0.1:8080", "127.0.0.2:8080", "127.0.0.3:8080"}

	sys := MockReplicationTestSystem(clusterMock)
	sys.clusterConfig = NewClusterConfig().WithDataCenter(cfg)

	controller, err := datacentercontroller.NewController(cfg, initialEndpoints)
	require.NoError(t, err)
	require.NoError(t, controller.Start(context.Background()))
	defer func() { _ = controller.Stop(context.Background()) }()

	sys.dataCenterController = controller

	// Trigger update
	err = sys.maybeUpdateEndpoints(context.Background())
	require.NoError(t, err)

	// Should only have the remaining member
	expectedEndpoints := []string{"127.0.0.1:8080"}
	require.Equal(t, expectedEndpoints, controller.Endpoints())

	// Verify update was registered
	require.GreaterOrEqual(t, len(registeredRecords), 2)
	lastRecord := registeredRecords[len(registeredRecords)-1]
	require.Equal(t, expectedEndpoints, lastRecord.Endpoints)
}

func TestStartDataCenterControllerUpdatesEndpoints(t *testing.T) {
	// Scenario: controller exists, membership changes, reconciliation is triggered
	newMembers := []*cluster.Peer{
		{Host: "127.0.0.1", RemotingPort: 8080},
		{Host: "127.0.0.2", RemotingPort: 8080},
		{Host: "127.0.0.3", RemotingPort: 8080},
	}

	clusterMock := mockscluster.NewCluster(t)
	clusterMock.EXPECT().IsLeader(mock.Anything).Return(true)
	clusterMock.EXPECT().Members(mock.Anything).Return(newMembers, nil)

	var registeredRecords []datacenter.DataCenterRecord
	cfg := datacenter.NewConfig()
	cfg.ControlPlane = &testControlPlane{
		registerFn: func(_ context.Context, record datacenter.DataCenterRecord) (string, uint64, error) {
			registeredRecords = append(registeredRecords, record)
			return "dc1", uint64(len(registeredRecords)), nil
		},
		setStateFn: func(_ context.Context, _ string, _ datacenter.DataCenterState, version uint64) (uint64, error) {
			return version + 1, nil
		},
		listActiveFn: func(_ context.Context) ([]datacenter.DataCenterRecord, error) {
			return nil, nil
		},
	}
	cfg.DataCenter = datacenter.DataCenter{Name: "dc1"}

	initialEndpoints := []string{"127.0.0.1:8080", "127.0.0.2:8080"}

	sys := MockReplicationTestSystem(clusterMock)
	sys.clusterConfig = NewClusterConfig().WithDataCenter(cfg)
	sys.shutdownTimeout = 5 * time.Second

	// Create and start controller with initial endpoints
	controller, err := datacentercontroller.NewController(cfg, initialEndpoints)
	require.NoError(t, err)
	require.NoError(t, controller.Start(context.Background()))
	defer func() { _ = controller.Stop(context.Background()) }()

	sys.dataCenterController = controller
	sys.clusterEnabled.Store(true)

	ctx := context.Background()

	// Trigger reconciliation (simulates cluster event or periodic tick)
	err = sys.startDataCenterController(ctx)
	require.NoError(t, err)

	// Verify endpoints were updated to include the new member
	expectedEndpoints := []string{"127.0.0.1:8080", "127.0.0.2:8080", "127.0.0.3:8080"}
	require.Equal(t, expectedEndpoints, controller.Endpoints())
}

// testControlPlane is a flexible mock control plane for testing endpoint updates.
type testControlPlane struct {
	registerFn   func(context.Context, datacenter.DataCenterRecord) (string, uint64, error)
	heartbeatFn  func(context.Context, string, uint64) (uint64, time.Time, error)
	setStateFn   func(context.Context, string, datacenter.DataCenterState, uint64) (uint64, error)
	listActiveFn func(context.Context) ([]datacenter.DataCenterRecord, error)
}

func (m *testControlPlane) Register(ctx context.Context, record datacenter.DataCenterRecord) (string, uint64, error) {
	if m.registerFn != nil {
		return m.registerFn(ctx, record)
	}
	return record.ID, 1, nil
}

func (m *testControlPlane) Heartbeat(ctx context.Context, id string, version uint64) (uint64, time.Time, error) {
	if m.heartbeatFn != nil {
		return m.heartbeatFn(ctx, id, version)
	}
	return version + 1, time.Now().Add(time.Hour), nil
}

func (m *testControlPlane) SetState(ctx context.Context, id string, state datacenter.DataCenterState, version uint64) (uint64, error) {
	if m.setStateFn != nil {
		return m.setStateFn(ctx, id, state, version)
	}
	return version + 1, nil
}

func (m *testControlPlane) ListActive(ctx context.Context) ([]datacenter.DataCenterRecord, error) {
	if m.listActiveFn != nil {
		return m.listActiveFn(ctx)
	}
	return nil, nil
}

func (*testControlPlane) Watch(_ context.Context) (<-chan datacenter.ControlPlaneEvent, error) {
	return nil, nil
}

func (*testControlPlane) Deregister(_ context.Context, _ string) error {
	return nil
}

// MockFailingSetStateControlPlane makes Controller.Stop fail by returning error from SetState
// when transitioning to DRAINING/INACTIVE. SetState succeeds for ACTIVE (during register).
type MockFailingSetStateControlPlane struct{}

func (*MockFailingSetStateControlPlane) Register(_ context.Context, record datacenter.DataCenterRecord) (string, uint64, error) {
	return record.ID, 1, nil
}

func (*MockFailingSetStateControlPlane) Heartbeat(_ context.Context, _ string, version uint64) (uint64, time.Time, error) {
	return version + 1, time.Now().Add(time.Hour), nil
}

func (*MockFailingSetStateControlPlane) SetState(_ context.Context, _ string, state datacenter.DataCenterState, version uint64) (uint64, error) {
	if state == datacenter.DataCenterActive {
		return version + 1, nil
	}
	return 0, errors.New("set state failed")
}

func (*MockFailingSetStateControlPlane) ListActive(_ context.Context) ([]datacenter.DataCenterRecord, error) {
	return nil, nil
}

func (*MockFailingSetStateControlPlane) Watch(_ context.Context) (<-chan datacenter.ControlPlaneEvent, error) {
	return nil, nil
}

func (*MockFailingSetStateControlPlane) Deregister(_ context.Context, _ string) error {
	return nil
}

// MockFailingRegisterControlPlane makes Controller.Start fail by returning error from Register.
type MockFailingRegisterControlPlane struct{}

func (*MockFailingRegisterControlPlane) Register(_ context.Context, _ datacenter.DataCenterRecord) (string, uint64, error) {
	return "", 0, errors.New("register failed")
}

func (*MockFailingRegisterControlPlane) Heartbeat(_ context.Context, _ string, version uint64) (uint64, time.Time, error) {
	return version + 1, time.Now().Add(time.Hour), nil
}

func (*MockFailingRegisterControlPlane) SetState(_ context.Context, _ string, _ datacenter.DataCenterState, version uint64) (uint64, error) {
	return version + 1, nil
}

func (*MockFailingRegisterControlPlane) ListActive(_ context.Context) ([]datacenter.DataCenterRecord, error) {
	return nil, nil
}

func (*MockFailingRegisterControlPlane) Watch(_ context.Context) (<-chan datacenter.ControlPlaneEvent, error) {
	return nil, nil
}

func (*MockFailingRegisterControlPlane) Deregister(_ context.Context, _ string) error {
	return nil
}
