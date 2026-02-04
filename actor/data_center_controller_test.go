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

	"github.com/tochemey/goakt/v3/datacenter"
	"github.com/tochemey/goakt/v3/internal/cluster"
	"github.com/tochemey/goakt/v3/internal/datacentercontroller"
	"github.com/tochemey/goakt/v3/internal/pause"
	"github.com/tochemey/goakt/v3/internal/ticker"
	"github.com/tochemey/goakt/v3/internal/types"
	"github.com/tochemey/goakt/v3/log"
	mockscluster "github.com/tochemey/goakt/v3/mocks/cluster"
	mocksremote "github.com/tochemey/goakt/v3/mocks/remote"
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
