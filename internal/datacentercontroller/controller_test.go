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

package datacentercontroller

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/datacenter"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/log"
)

func newTestController(t *testing.T, cp ControlPlane, mutate func(*Config)) *Controller {
	t.Helper()

	endpoints := []string{"127.0.0.1:1"}
	config := &Config{
		Logger:               log.DiscardLogger,
		ControlPlane:         cp,
		DataCenter:           DataCenter{Name: "dc-1"},
		HeartbeatInterval:    time.Hour,
		CacheRefreshInterval: time.Hour,
		MaxCacheStaleness:    time.Second,
		JitterRatio:          0.1,
		MaxBackoff:           time.Second,
		WatchEnabled:         false,
		RequestTimeout:       time.Second,
	}

	if mutate != nil {
		mutate(config)
	}

	manager, err := NewController(config, endpoints)
	require.NoError(t, err)
	return manager
}

func TestControllerNewManagerNilConfig(t *testing.T) {
	manager, err := NewController(nil, nil)
	require.Error(t, err)
	require.Nil(t, manager)
}

func TestControllerNewManagerNilEndpoints(t *testing.T) {
	manager, err := NewController(new(Config), nil)
	require.Error(t, err)
	require.Nil(t, manager)
}

func TestControllerNewManagerWithIinvalidConfig(t *testing.T) {
	manager, err := NewController(new(Config), []string{"127.0.0.1:1"})
	require.Error(t, err)
	require.Nil(t, manager)
}

func TestControllerStartStopHappyPath(t *testing.T) {
	states := make([]DataCenterState, 0, 3)
	cp := &MockControlPlane{
		registerFn: func(_ context.Context, _ DataCenterRecord) (string, uint64, error) {
			return "dc-1", 1, nil
		},
		setStateFn: func(_ context.Context, _ string, state DataCenterState, version uint64) (uint64, error) {
			states = append(states, state)
			return version + 1, nil
		},
		listActiveFn: func(context.Context) ([]DataCenterRecord, error) {
			return []DataCenterRecord{{ID: "dc-1", State: datacenter.DataCenterActive, Version: 1}}, nil
		},
	}

	manager := newTestController(t, cp, func(config *Config) {
		config.WatchEnabled = false
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, manager.Start(ctx))
	require.NoError(t, manager.Start(ctx))
	require.NoError(t, manager.Stop(context.Background()))
	require.Equal(t, []DataCenterState{datacenter.DataCenterActive, datacenter.DataCenterDraining, datacenter.DataCenterInactive}, states)
}

func TestControllerStartRegisterError(t *testing.T) {
	cp := &MockControlPlane{
		registerFn: func(context.Context, DataCenterRecord) (string, uint64, error) {
			return "", 0, errors.New("register failed")
		},
	}
	manager := newTestController(t, cp, nil)

	err := manager.Start(context.Background())
	require.Error(t, err)
	require.False(t, manager.started.Load())
}

func TestControllerStartRefreshError(t *testing.T) {
	cp := &MockControlPlane{
		registerFn: func(context.Context, DataCenterRecord) (string, uint64, error) {
			return "dc-1", 1, nil
		},
		setStateFn: func(context.Context, string, DataCenterState, uint64) (uint64, error) {
			return 2, nil
		},
		listActiveFn: func(context.Context) ([]DataCenterRecord, error) {
			return nil, errors.New("refresh failed")
		},
	}

	manager := newTestController(t, cp, func(config *Config) {
		config.WatchEnabled = false
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, manager.Start(ctx))
	require.True(t, manager.started.Load())
	require.NoError(t, manager.Stop(context.Background()))
}

func TestControllerStartWithWatchEnabled(t *testing.T) {
	cp := &MockControlPlane{
		registerFn: func(context.Context, DataCenterRecord) (string, uint64, error) {
			return "dc-1", 1, nil
		},
		setStateFn: func(context.Context, string, DataCenterState, uint64) (uint64, error) {
			return 2, nil
		},
		listActiveFn: func(context.Context) ([]DataCenterRecord, error) {
			return nil, nil
		},
		watchFn: func(context.Context) (<-chan ControlPlaneEvent, error) {
			return nil, gerrors.ErrWatchNotSupported
		},
	}

	manager := newTestController(t, cp, func(config *Config) {
		config.WatchEnabled = true
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, manager.Start(ctx))
	require.NoError(t, manager.Stop(context.Background()))
}

func TestControllerStopEmptyRecordID(t *testing.T) {
	setStateCalled := false
	cp := &MockControlPlane{
		setStateFn: func(context.Context, string, DataCenterState, uint64) (uint64, error) {
			setStateCalled = true
			return 0, nil
		},
	}
	manager := newTestController(t, cp, nil)
	manager.started.Store(true)
	manager.recordID = ""

	require.NoError(t, manager.Stop(context.Background()))
	require.False(t, setStateCalled)
}

func TestControllerStopDrainingError(t *testing.T) {
	cp := &MockControlPlane{
		setStateFn: func(context.Context, string, DataCenterState, uint64) (uint64, error) {
			return 0, errors.New("draining failed")
		},
	}
	manager := newTestController(t, cp, nil)
	manager.started.Store(true)
	manager.recordID = "dc-1"

	err := manager.Stop(context.Background())
	require.Error(t, err)
}

func TestControllerStopInactiveError(t *testing.T) {
	cp := &MockControlPlane{
		setStateFn: func(_ context.Context, _ string, state DataCenterState, version uint64) (uint64, error) {
			if state == datacenter.DataCenterInactive {
				return version + 1, errors.New("inactive failed")
			}
			return version + 1, nil
		},
	}

	manager := newTestController(t, cp, nil)
	manager.started.Store(true)
	manager.recordID = "dc-1"

	err := manager.Stop(context.Background())
	require.Error(t, err)
}

func TestControllerStopInactiveNotFound(t *testing.T) {
	call := 0
	cp := &MockControlPlane{
		setStateFn: func(context.Context, string, DataCenterState, uint64) (uint64, error) {
			call++
			if call == 1 {
				return 2, nil
			}
			return 0, gerrors.ErrDataCenterRecordNotFound
		},
	}
	manager := newTestController(t, cp, nil)
	manager.started.Store(true)
	manager.recordID = "dc-1"

	require.NoError(t, manager.Stop(context.Background()))
}

func TestControllerHeartbeatOnce(t *testing.T) {
	t.Run("empty record", func(t *testing.T) {
		called := false
		cp := &MockControlPlane{
			heartbeatFn: func(context.Context, string, uint64) (uint64, time.Time, error) {
				called = true
				return 0, time.Now(), nil
			},
		}
		manager := newTestController(t, cp, nil)
		manager.recordID = ""
		require.NoError(t, manager.heartbeatOnce(context.Background()))
		require.False(t, called)
	})

	t.Run("success", func(t *testing.T) {
		cp := &MockControlPlane{
			heartbeatFn: func(context.Context, string, uint64) (uint64, time.Time, error) {
				return 7, time.Unix(10, 0), nil
			},
		}
		manager := newTestController(t, cp, nil)
		manager.recordID = "dc-1"
		manager.recordVer = 3

		require.NoError(t, manager.heartbeatOnce(context.Background()))
		require.Equal(t, uint64(7), manager.recordVer)
		require.Equal(t, time.Unix(10, 0), manager.leaseExpiry)
	})

	t.Run("not found triggers register", func(t *testing.T) {
		registered := false
		cp := &MockControlPlane{
			heartbeatFn: func(context.Context, string, uint64) (uint64, time.Time, error) {
				return 0, time.Time{}, gerrors.ErrDataCenterRecordNotFound
			},
			registerFn: func(context.Context, DataCenterRecord) (string, uint64, error) {
				registered = true
				return "dc-2", 1, nil
			},
			setStateFn: func(context.Context, string, DataCenterState, uint64) (uint64, error) {
				return 2, nil
			},
		}
		manager := newTestController(t, cp, nil)
		manager.recordID = "dc-1"

		require.NoError(t, manager.heartbeatOnce(context.Background()))
		require.True(t, registered)
		require.Equal(t, "dc-2", manager.recordID)
		require.Equal(t, uint64(2), manager.recordVer)
	})

	t.Run("other error", func(t *testing.T) {
		cp := &MockControlPlane{
			heartbeatFn: func(context.Context, string, uint64) (uint64, time.Time, error) {
				return 0, time.Time{}, errors.New("boom")
			},
		}
		manager := newTestController(t, cp, nil)
		manager.recordID = "dc-1"
		require.Error(t, manager.heartbeatOnce(context.Background()))
	})
}

func TestControllerRefreshCache(t *testing.T) {
	cp := &MockControlPlane{
		listActiveFn: func(context.Context) ([]DataCenterRecord, error) {
			return []DataCenterRecord{
				{ID: "dc-b", State: datacenter.DataCenterActive, Version: 2},
			}, nil
		},
	}
	manager := newTestController(t, cp, nil)

	manager.cache.replace([]DataCenterRecord{{ID: "dc-a", State: datacenter.DataCenterActive, Version: 1}})
	manager.watchSupported.Store(false)
	require.NoError(t, manager.refreshCache(context.Background()))
	records, _ := manager.cache.snapshot()
	require.Len(t, records, 1)
	require.Equal(t, "dc-b", records[0].ID)

	manager.cache.replace([]DataCenterRecord{{ID: "dc-a", State: datacenter.DataCenterActive, Version: 1}})
	manager.watchSupported.Store(true)
	require.NoError(t, manager.refreshCache(context.Background()))
	records, _ = manager.cache.snapshot()
	require.Len(t, records, 2)
}

func TestControllerRecordUsesExistingID(t *testing.T) {
	manager := newTestController(t, &MockControlPlane{}, nil)
	manager.recordID = "dc-custom"

	record := manager.record()
	require.Equal(t, "dc-custom", record.ID)
}

func TestControllerRecordUsesDataCenterID(t *testing.T) {
	manager := newTestController(t, &MockControlPlane{}, func(config *Config) {
		config.DataCenter = DataCenter{Name: "dc-1", Region: "r", Zone: "z"}
	})

	record := manager.record()
	require.Equal(t, "zrdc-1", record.ID)
}

func TestControllerActiveRecords(t *testing.T) {
	manager := newTestController(t, &MockControlPlane{}, nil)

	records, stale := manager.ActiveRecords()
	require.Len(t, records, 0)
	require.True(t, stale)

	manager.cache.replace([]DataCenterRecord{
		{ID: "dc-1", State: datacenter.DataCenterActive, Version: 1},
	})
	records, stale = manager.ActiveRecords()
	require.Len(t, records, 1)
	require.False(t, stale)

	manager.config.MaxCacheStaleness = time.Nanosecond
	time.Sleep(2 * time.Nanosecond)
	records, stale = manager.ActiveRecords()
	require.Len(t, records, 1)
	require.True(t, stale)
}

func TestControllerReady(t *testing.T) {
	t.Run("not ready when not started", func(t *testing.T) {
		manager := newTestController(t, &MockControlPlane{}, nil)
		require.False(t, manager.Ready())
	})

	t.Run("not ready when started but cache never refreshed", func(t *testing.T) {
		manager := newTestController(t, &MockControlPlane{}, nil)
		manager.started.Store(true)
		// Cache has never been refreshed (refreshedAt is zero)
		require.False(t, manager.Ready())
	})

	t.Run("ready when started and cache refreshed", func(t *testing.T) {
		manager := newTestController(t, &MockControlPlane{}, nil)
		manager.started.Store(true)
		manager.cache.replace([]DataCenterRecord{
			{ID: "dc-1", State: datacenter.DataCenterActive, Version: 1},
		})
		require.True(t, manager.Ready())
	})

	t.Run("ready with empty cache after refresh", func(t *testing.T) {
		manager := newTestController(t, &MockControlPlane{}, nil)
		manager.started.Store(true)
		// Replace with empty list still sets refreshedAt
		manager.cache.replace([]DataCenterRecord{})
		require.True(t, manager.Ready())
	})

	t.Run("not ready after stop", func(t *testing.T) {
		cp := &MockControlPlane{
			registerFn: func(context.Context, DataCenterRecord) (string, uint64, error) {
				return "dc-1", 1, nil
			},
			setStateFn: func(context.Context, string, DataCenterState, uint64) (uint64, error) {
				return 2, nil
			},
			listActiveFn: func(context.Context) ([]DataCenterRecord, error) {
				return []DataCenterRecord{}, nil
			},
		}
		manager := newTestController(t, cp, nil)

		require.NoError(t, manager.Start(context.Background()))
		require.True(t, manager.Ready())

		require.NoError(t, manager.Stop(context.Background()))
		require.False(t, manager.Ready())
	})
}

func TestControllerLastRefresh(t *testing.T) {
	t.Run("zero when cache never refreshed", func(t *testing.T) {
		manager := newTestController(t, &MockControlPlane{}, nil)
		require.True(t, manager.LastRefresh().IsZero())
	})

	t.Run("non-zero after cache refresh", func(t *testing.T) {
		manager := newTestController(t, &MockControlPlane{}, nil)
		before := time.Now()
		manager.cache.replace([]DataCenterRecord{
			{ID: "dc-1", State: datacenter.DataCenterActive, Version: 1},
		})
		after := time.Now()

		lastRefresh := manager.LastRefresh()
		require.False(t, lastRefresh.IsZero())
		require.True(t, lastRefresh.After(before) || lastRefresh.Equal(before))
		require.True(t, lastRefresh.Before(after) || lastRefresh.Equal(after))
	})

	t.Run("updates on subsequent refreshes", func(t *testing.T) {
		manager := newTestController(t, &MockControlPlane{}, nil)
		manager.cache.replace([]DataCenterRecord{
			{ID: "dc-1", State: datacenter.DataCenterActive, Version: 1},
		})
		firstRefresh := manager.LastRefresh()

		time.Sleep(time.Millisecond)

		manager.cache.replace([]DataCenterRecord{
			{ID: "dc-1", State: datacenter.DataCenterActive, Version: 2},
		})
		secondRefresh := manager.LastRefresh()

		require.True(t, secondRefresh.After(firstRefresh))
	})

	t.Run("zero after reset", func(t *testing.T) {
		manager := newTestController(t, &MockControlPlane{}, nil)
		manager.cache.replace([]DataCenterRecord{
			{ID: "dc-1", State: datacenter.DataCenterActive, Version: 1},
		})
		require.False(t, manager.LastRefresh().IsZero())

		manager.cache.reset()
		require.True(t, manager.LastRefresh().IsZero())
	})
}

func TestControllerWatchLoopUnsupported(t *testing.T) {
	cp := &MockControlPlane{
		watchFn: func(context.Context) (<-chan ControlPlaneEvent, error) {
			return nil, gerrors.ErrWatchNotSupported
		},
	}
	manager := newTestController(t, cp, func(config *Config) {
		config.CacheRefreshInterval = time.Millisecond
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.ctx = ctx

	done := make(chan struct{})
	manager.wg.Add(1)
	go func() {
		manager.watchLoop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watch loop did not exit")
	}
	require.False(t, manager.watchSupported.Load())
}

func TestControllerWatchLoopErrorCancel(t *testing.T) {
	called := make(chan struct{}, 1)
	cp := &MockControlPlane{
		watchFn: func(context.Context) (<-chan ControlPlaneEvent, error) {
			select {
			case called <- struct{}{}:
			default:
			}
			return nil, errors.New("watch failed")
		},
	}
	manager := newTestController(t, cp, func(config *Config) {
		config.CacheRefreshInterval = 100 * time.Millisecond
	})
	manager.config.JitterRatio = 0

	ctx, cancel := context.WithCancel(context.Background())
	manager.ctx = ctx

	done := make(chan struct{})
	manager.wg.Add(1)
	go func() {
		manager.watchLoop()
		close(done)
	}()

	<-called
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watch loop did not exit")
	}
	require.False(t, manager.watchSupported.Load())
}

func TestControllerWatchLoopEvents(t *testing.T) {
	events := make(chan ControlPlaneEvent, 2)
	cp := &MockControlPlane{
		watchFn: func(context.Context) (<-chan ControlPlaneEvent, error) {
			return events, nil
		},
	}
	manager := newTestController(t, cp, func(config *Config) {
		config.CacheRefreshInterval = time.Millisecond
	})
	manager.config.JitterRatio = 0

	ctx, cancel := context.WithCancel(context.Background())
	manager.ctx = ctx

	done := make(chan struct{})
	manager.wg.Add(1)
	go func() {
		manager.watchLoop()
		close(done)
	}()

	events <- ControlPlaneEvent{Type: datacenter.ControlPlaneEventUpsert, Record: DataCenterRecord{}}
	events <- ControlPlaneEvent{Type: datacenter.ControlPlaneEventUpsert, Record: DataCenterRecord{ID: "dc-1", State: datacenter.DataCenterActive, Version: 1}}
	close(events)

	require.Eventually(t, func() bool {
		records, _ := manager.cache.snapshot()
		return len(records) == 1 && records[0].ID == "dc-1"
	}, time.Second, 5*time.Millisecond)

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watch loop did not exit")
	}

	records, _ := manager.cache.snapshot()
	require.Len(t, records, 1)
	require.Equal(t, "dc-1", records[0].ID)
}

func TestControllerRunLoopAndBackoff(t *testing.T) {
	manager := newTestController(t, &MockControlPlane{}, func(config *Config) {
		config.JitterRatio = 0.1
		config.MaxBackoff = 5 * time.Second
	})

	ctx, cancel := context.WithCancel(context.Background())
	manager.ctx = ctx

	var calls atomic.Int32
	fn := func(_ context.Context) error {
		count := calls.Add(1)
		if count == 1 {
			return errors.New("boom")
		}
		if count >= 2 {
			cancel()
		}
		return nil
	}

	done := make(chan struct{})
	go func() {
		manager.runLoop("test", time.Millisecond, fn)
		close(done)
	}()

	require.Eventually(t, func() bool {
		return calls.Load() >= 2
	}, time.Second, 5*time.Millisecond)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("run loop did not exit")
	}

	require.Equal(t, time.Second, manager.backoffDelay(time.Second, 1))
	require.Equal(t, 4*time.Second, manager.backoffDelay(time.Second, 3))
	manager.config.MaxBackoff = 2 * time.Second
	require.Equal(t, 2*time.Second, manager.backoffDelay(time.Second, 4))
	require.Equal(t, time.Duration(0), manager.backoffDelay(0, 1))

	manager.config.JitterRatio = 0
	manager.config.MaxBackoff = 10 * time.Second
	require.Equal(t, 2*time.Second, manager.nextDelay(2*time.Second, 0))
	require.Equal(t, 5*time.Second, manager.nextDelay(5*time.Second, 1))
	require.Equal(t, 5*time.Second, jitterDuration(5*time.Second, 0))
	require.Equal(t, time.Duration(0), jitterDuration(0, 0.1))

	cancelledCtx, cancelled := context.WithCancel(context.Background())
	cancelled()
	manager.ctx = cancelledCtx
	require.False(t, manager.sleepBackoff(time.Millisecond, 1))

	manager.ctx = context.Background()
	manager.config.JitterRatio = 0
	require.True(t, manager.sleepBackoff(time.Millisecond, 1))
}

func TestRecordCacheSemantics(t *testing.T) {
	cache := newRecordCache()
	cache.replace([]DataCenterRecord{
		{ID: "active", State: datacenter.DataCenterActive, Version: 1},
		{ID: "inactive", State: datacenter.DataCenterInactive, Version: 1},
	})

	records, _ := cache.snapshot()
	require.Len(t, records, 1)
	require.Equal(t, "active", records[0].ID)

	cache.merge([]DataCenterRecord{
		{ID: "merge", State: datacenter.DataCenterActive, Version: 2},
	})
	records, _ = cache.snapshot()
	require.Len(t, records, 2)

	cache.apply(ControlPlaneEvent{
		Type:   datacenter.ControlPlaneEventDelete,
		Record: DataCenterRecord{ID: "active", Version: 3},
	})
	records, _ = cache.snapshot()
	require.Len(t, records, 1)

	cache.apply(ControlPlaneEvent{
		Type:   datacenter.ControlPlaneEventUpsert,
		Record: DataCenterRecord{ID: "active", State: datacenter.DataCenterActive, Version: 2},
	})
	records, _ = cache.snapshot()
	require.Len(t, records, 1)

	cache.apply(ControlPlaneEvent{
		Type:   datacenter.ControlPlaneEventUpsert,
		Record: DataCenterRecord{ID: "merge", State: datacenter.DataCenterDraining, Version: 4},
	})
	records, _ = cache.snapshot()
	require.Len(t, records, 0)

	cache.replace([]DataCenterRecord{
		{ID: "kept", State: datacenter.DataCenterActive, Version: 2},
	})
	cache.replace([]DataCenterRecord{
		{ID: "kept", State: datacenter.DataCenterActive, Version: 0},
	})
	records, _ = cache.snapshot()
	require.Len(t, records, 0)

	cache.replace([]DataCenterRecord{
		{ID: "versioned", State: datacenter.DataCenterActive, Version: 0},
	})
	records, _ = cache.snapshot()
	require.Len(t, records, 1)

	cache.apply(ControlPlaneEvent{
		Type:   datacenter.ControlPlaneEventUpsert,
		Record: DataCenterRecord{ID: "", State: datacenter.DataCenterActive, Version: 10},
	})

	cache.apply(ControlPlaneEvent{
		Type:   datacenter.ControlPlaneEventDelete,
		Record: DataCenterRecord{ID: "versioned", Version: 0},
	})

	cache.reset()
	records, refreshedAt := cache.snapshot()
	require.Len(t, records, 0)
	require.True(t, refreshedAt.IsZero())
}

func TestRecordCacheMergeHonorsVersions(t *testing.T) {
	cache := newRecordCache()
	cache.apply(ControlPlaneEvent{
		Type:   datacenter.ControlPlaneEventDelete,
		Record: DataCenterRecord{ID: "dc-1", Version: 5},
	})
	cache.merge([]DataCenterRecord{
		{ID: "dc-1", State: datacenter.DataCenterActive, Version: 4},
	})

	records, _ := cache.snapshot()
	require.Len(t, records, 0)
}

// MockControlPlane is a test double for ControlPlane with overridable hooks.
type MockControlPlane struct {
	registerFn   func(context.Context, DataCenterRecord) (string, uint64, error)
	heartbeatFn  func(context.Context, string, uint64) (uint64, time.Time, error)
	setStateFn   func(context.Context, string, DataCenterState, uint64) (uint64, error)
	listActiveFn func(context.Context) ([]DataCenterRecord, error)
	watchFn      func(context.Context) (<-chan ControlPlaneEvent, error)
}

func (m *MockControlPlane) Register(ctx context.Context, record DataCenterRecord) (string, uint64, error) {
	if m.registerFn != nil {
		return m.registerFn(ctx, record)
	}
	id := record.ID
	if id == "" {
		id = "dc-1"
	}
	return id, 1, nil
}

func (m *MockControlPlane) Heartbeat(ctx context.Context, id string, version uint64) (uint64, time.Time, error) {
	if m.heartbeatFn != nil {
		return m.heartbeatFn(ctx, id, version)
	}
	return version + 1, time.Now(), nil
}

func (m *MockControlPlane) SetState(ctx context.Context, id string, state DataCenterState, version uint64) (uint64, error) {
	if m.setStateFn != nil {
		return m.setStateFn(ctx, id, state, version)
	}
	return version + 1, nil
}

func (m *MockControlPlane) ListActive(ctx context.Context) ([]DataCenterRecord, error) {
	if m.listActiveFn != nil {
		return m.listActiveFn(ctx)
	}
	return nil, nil
}

func (m *MockControlPlane) Watch(ctx context.Context) (<-chan ControlPlaneEvent, error) {
	if m.watchFn != nil {
		return m.watchFn(ctx)
	}
	return nil, gerrors.ErrWatchNotSupported
}

func (m *MockControlPlane) Deregister(_ context.Context, _ string) error {
	return nil
}

func TestControllerEndpoints(t *testing.T) {
	endpoints := []string{"127.0.0.1:8080", "127.0.0.2:8080"}
	config := &Config{
		Logger:               log.DiscardLogger,
		ControlPlane:         &MockControlPlane{},
		DataCenter:           DataCenter{Name: "dc-1"},
		HeartbeatInterval:    time.Hour,
		CacheRefreshInterval: time.Hour,
		MaxCacheStaleness:    time.Second,
		JitterRatio:          0.1,
		MaxBackoff:           time.Second,
		RequestTimeout:       time.Second,
	}
	
	controller, err := NewController(config, endpoints)
	require.NoError(t, err)
	
	// Test that Endpoints returns a copy
	result := controller.Endpoints()
	require.Equal(t, endpoints, result)
	
	// Modify the returned slice; should not affect controller's internal state
	result[0] = "192.168.1.1:9000"
	require.Equal(t, endpoints, controller.Endpoints())
}

func TestControllerUpdateEndpointsNotStarted(t *testing.T) {
	cp := &MockControlPlane{}
	controller := newTestController(t, cp, nil)
	
	err := controller.UpdateEndpoints(context.Background(), []string{"127.0.0.1:9000"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "controller is not started")
}

func TestControllerUpdateEndpointsEmptyEndpoints(t *testing.T) {
	cp := &MockControlPlane{}
	controller := newTestController(t, cp, nil)
	require.NoError(t, controller.Start(context.Background()))
	defer func() { _ = controller.Stop(context.Background()) }()
	
	err := controller.UpdateEndpoints(context.Background(), []string{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "endpoints must not be empty")
}

func TestControllerUpdateEndpointsHappyPath(t *testing.T) {
	var registeredRecords []DataCenterRecord
	cp := &MockControlPlane{
		registerFn: func(_ context.Context, record DataCenterRecord) (string, uint64, error) {
			registeredRecords = append(registeredRecords, record)
			return "dc-1", uint64(len(registeredRecords)), nil
		},
		setStateFn: func(_ context.Context, _ string, _ DataCenterState, version uint64) (uint64, error) {
			return version + 1, nil
		},
		listActiveFn: func(context.Context) ([]DataCenterRecord, error) {
			return []DataCenterRecord{{ID: "dc-1", State: datacenter.DataCenterActive, Version: 1}}, nil
		},
	}
	
	initialEndpoints := []string{"127.0.0.1:8080"}
	config := &Config{
		Logger:               log.DiscardLogger,
		ControlPlane:         cp,
		DataCenter:           DataCenter{Name: "dc-1"},
		HeartbeatInterval:    time.Hour,
		CacheRefreshInterval: time.Hour,
		MaxCacheStaleness:    time.Second,
		JitterRatio:          0.1,
		MaxBackoff:           time.Second,
		WatchEnabled:         false,
		RequestTimeout:       time.Second,
	}
	
	controller, err := NewController(config, initialEndpoints)
	require.NoError(t, err)
	
	require.NoError(t, controller.Start(context.Background()))
	defer func() { _ = controller.Stop(context.Background()) }()
	
	// Initial registration
	require.Len(t, registeredRecords, 1)
	require.Equal(t, initialEndpoints, registeredRecords[0].Endpoints)
	
	// Update endpoints
	newEndpoints := []string{"127.0.0.1:8080", "127.0.0.2:8080", "127.0.0.3:8080"}
	err = controller.UpdateEndpoints(context.Background(), newEndpoints)
	require.NoError(t, err)
	
	// Verify re-registration happened
	require.Len(t, registeredRecords, 2)
	require.Equal(t, newEndpoints, registeredRecords[1].Endpoints)
	require.Equal(t, datacenter.DataCenterActive, registeredRecords[1].State)
	
	// Verify controller's stored endpoints are updated
	require.Equal(t, newEndpoints, controller.Endpoints())
}

func TestControllerUpdateEndpointsRegisterError(t *testing.T) {
	registerErr := errors.New("control plane error")
	var registerCallCount int
	cp := &MockControlPlane{
		registerFn: func(_ context.Context, record DataCenterRecord) (string, uint64, error) {
			registerCallCount++
			// First call (initial registration) succeeds
			if registerCallCount == 1 {
				return "dc-1", 1, nil
			}
			// Second call (update) fails
			return "", 0, registerErr
		},
		setStateFn: func(_ context.Context, _ string, _ DataCenterState, version uint64) (uint64, error) {
			return version + 1, nil
		},
		listActiveFn: func(context.Context) ([]DataCenterRecord, error) {
			return []DataCenterRecord{{ID: "dc-1", State: datacenter.DataCenterActive, Version: 1}}, nil
		},
	}
	
	initialEndpoints := []string{"127.0.0.1:8080"}
	config := &Config{
		Logger:               log.DiscardLogger,
		ControlPlane:         cp,
		DataCenter:           DataCenter{Name: "dc-1"},
		HeartbeatInterval:    time.Hour,
		CacheRefreshInterval: time.Hour,
		MaxCacheStaleness:    time.Second,
		JitterRatio:          0.1,
		MaxBackoff:           time.Second,
		WatchEnabled:         false,
		RequestTimeout:       time.Second,
	}
	
	controller, err := NewController(config, initialEndpoints)
	require.NoError(t, err)
	
	require.NoError(t, controller.Start(context.Background()))
	defer func() { _ = controller.Stop(context.Background()) }()
	
	// Update endpoints should fail
	newEndpoints := []string{"127.0.0.1:8080", "127.0.0.2:8080"}
	err = controller.UpdateEndpoints(context.Background(), newEndpoints)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to update endpoints")
	
	// Original endpoints should be unchanged
	require.Equal(t, initialEndpoints, controller.Endpoints())
}
