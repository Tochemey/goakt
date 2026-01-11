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

package multidatacentermanager

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/multidatacenter"
)

func newTestManager(t *testing.T, cp ControlPlane, mutate func(*Config)) *Manager {
	t.Helper()

	config := &Config{
		Logger:               log.DiscardLogger,
		ControlPlane:         cp,
		DataCenter:           DataCenter{Name: "dc-1"},
		Endpoints:            []string{"127.0.0.1:1"},
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

	manager, err := NewManager(config)
	require.NoError(t, err)
	return manager
}

func TestManagerNewManagerNilConfig(t *testing.T) {
	manager, err := NewManager(nil)
	require.Error(t, err)
	require.Nil(t, manager)
}

func TestManagerStartStopHappyPath(t *testing.T) {
	states := make([]DataCenterState, 0, 3)
	cp := &MockControlPlane{
		registerFn: func(ctx context.Context, record DataCenterRecord) (string, uint64, error) {
			return "dc-1", 1, nil
		},
		setStateFn: func(ctx context.Context, id string, state DataCenterState, version uint64) (uint64, error) {
			states = append(states, state)
			return version + 1, nil
		},
		listActiveFn: func(context.Context) ([]DataCenterRecord, error) {
			return []DataCenterRecord{{ID: "dc-1", State: multidatacenter.DataCenterActive, Version: 1}}, nil
		},
	}

	manager := newTestManager(t, cp, func(config *Config) {
		config.WatchEnabled = false
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, manager.Start(ctx))
	require.NoError(t, manager.Start(ctx))
	require.NoError(t, manager.Stop(context.Background()))
	require.Equal(t, []DataCenterState{multidatacenter.DataCenterActive, multidatacenter.DataCenterDraining, multidatacenter.DataCenterInactive}, states)
}

func TestManagerStartRegisterError(t *testing.T) {
	cp := &MockControlPlane{
		registerFn: func(context.Context, DataCenterRecord) (string, uint64, error) {
			return "", 0, errors.New("register failed")
		},
	}
	manager := newTestManager(t, cp, nil)

	err := manager.Start(context.Background())
	require.Error(t, err)
	require.False(t, manager.started.Load())
}

func TestManagerStartRefreshError(t *testing.T) {
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

	manager := newTestManager(t, cp, func(config *Config) {
		config.WatchEnabled = false
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, manager.Start(ctx))
	require.True(t, manager.started.Load())
	require.NoError(t, manager.Stop(context.Background()))
}

func TestManagerStartWithWatchEnabled(t *testing.T) {
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

	manager := newTestManager(t, cp, func(config *Config) {
		config.WatchEnabled = true
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	require.NoError(t, manager.Start(ctx))
	require.NoError(t, manager.Stop(context.Background()))
}

func TestManagerStopEmptyRecordID(t *testing.T) {
	setStateCalled := false
	cp := &MockControlPlane{
		setStateFn: func(context.Context, string, DataCenterState, uint64) (uint64, error) {
			setStateCalled = true
			return 0, nil
		},
	}
	manager := newTestManager(t, cp, nil)
	manager.started.Store(true)
	manager.recordID = ""

	require.NoError(t, manager.Stop(context.Background()))
	require.False(t, setStateCalled)
}

func TestManagerStopDrainingError(t *testing.T) {
	cp := &MockControlPlane{
		setStateFn: func(context.Context, string, DataCenterState, uint64) (uint64, error) {
			return 0, errors.New("draining failed")
		},
	}
	manager := newTestManager(t, cp, nil)
	manager.started.Store(true)
	manager.recordID = "dc-1"

	err := manager.Stop(context.Background())
	require.Error(t, err)
}

func TestManagerStopInactiveError(t *testing.T) {
	cp := &MockControlPlane{
		setStateFn: func(ctx context.Context, id string, state DataCenterState, version uint64) (uint64, error) {
			if state == multidatacenter.DataCenterInactive {
				return version + 1, errors.New("inactive failed")
			}
			return version + 1, nil
		},
	}

	manager := newTestManager(t, cp, nil)
	manager.started.Store(true)
	manager.recordID = "dc-1"

	err := manager.Stop(context.Background())
	require.Error(t, err)
}

func TestManagerStopInactiveNotFound(t *testing.T) {
	call := 0
	cp := &MockControlPlane{
		setStateFn: func(context.Context, string, DataCenterState, uint64) (uint64, error) {
			call++
			if call == 1 {
				return 2, nil
			}
			return 0, gerrors.ErrRecordNotFound
		},
	}
	manager := newTestManager(t, cp, nil)
	manager.started.Store(true)
	manager.recordID = "dc-1"

	require.NoError(t, manager.Stop(context.Background()))
}

func TestManagerHeartbeatOnce(t *testing.T) {
	t.Run("empty record", func(t *testing.T) {
		called := false
		cp := &MockControlPlane{
			heartbeatFn: func(context.Context, string, uint64) (uint64, time.Time, error) {
				called = true
				return 0, time.Now(), nil
			},
		}
		manager := newTestManager(t, cp, nil)
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
		manager := newTestManager(t, cp, nil)
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
				return 0, time.Time{}, gerrors.ErrRecordNotFound
			},
			registerFn: func(context.Context, DataCenterRecord) (string, uint64, error) {
				registered = true
				return "dc-2", 1, nil
			},
			setStateFn: func(context.Context, string, DataCenterState, uint64) (uint64, error) {
				return 2, nil
			},
		}
		manager := newTestManager(t, cp, nil)
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
		manager := newTestManager(t, cp, nil)
		manager.recordID = "dc-1"
		require.Error(t, manager.heartbeatOnce(context.Background()))
	})
}

func TestManagerRefreshCache(t *testing.T) {
	cp := &MockControlPlane{
		listActiveFn: func(context.Context) ([]DataCenterRecord, error) {
			return []DataCenterRecord{
				{ID: "dc-b", State: multidatacenter.DataCenterActive, Version: 2},
			}, nil
		},
	}
	manager := newTestManager(t, cp, nil)

	manager.cache.replace([]DataCenterRecord{{ID: "dc-a", State: multidatacenter.DataCenterActive, Version: 1}})
	manager.watchSupported.Store(false)
	require.NoError(t, manager.refreshCache(context.Background()))
	records, _ := manager.cache.snapshot()
	require.Len(t, records, 1)
	require.Equal(t, "dc-b", records[0].ID)

	manager.cache.replace([]DataCenterRecord{{ID: "dc-a", State: multidatacenter.DataCenterActive, Version: 1}})
	manager.watchSupported.Store(true)
	require.NoError(t, manager.refreshCache(context.Background()))
	records, _ = manager.cache.snapshot()
	require.Len(t, records, 2)
}

func TestManagerRecordUsesExistingID(t *testing.T) {
	manager := newTestManager(t, &MockControlPlane{}, nil)
	manager.recordID = "dc-custom"

	record := manager.record()
	require.Equal(t, "dc-custom", record.ID)
}

func TestManagerRecordUsesDataCenterID(t *testing.T) {
	manager := newTestManager(t, &MockControlPlane{}, func(config *Config) {
		config.DataCenter = DataCenter{Name: "dc-1", Region: "r", Zone: "z"}
	})

	record := manager.record()
	require.Equal(t, "zrdc-1", record.ID)
}

func TestManagerActiveRecords(t *testing.T) {
	manager := newTestManager(t, &MockControlPlane{}, nil)

	records, stale := manager.ActiveRecords()
	require.Len(t, records, 0)
	require.True(t, stale)

	manager.cache.replace([]DataCenterRecord{
		{ID: "dc-1", State: multidatacenter.DataCenterActive, Version: 1},
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

func TestManagerWatchLoopUnsupported(t *testing.T) {
	cp := &MockControlPlane{
		watchFn: func(context.Context) (<-chan ControlPlaneEvent, error) {
			return nil, gerrors.ErrWatchNotSupported
		},
	}
	manager := newTestManager(t, cp, func(config *Config) {
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

func TestManagerWatchLoopErrorCancel(t *testing.T) {
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
	manager := newTestManager(t, cp, func(config *Config) {
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

func TestManagerWatchLoopEvents(t *testing.T) {
	events := make(chan ControlPlaneEvent, 2)
	cp := &MockControlPlane{
		watchFn: func(context.Context) (<-chan ControlPlaneEvent, error) {
			return events, nil
		},
	}
	manager := newTestManager(t, cp, func(config *Config) {
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

	events <- ControlPlaneEvent{Type: multidatacenter.ControlPlaneEventUpsert, Record: DataCenterRecord{}}
	events <- ControlPlaneEvent{Type: multidatacenter.ControlPlaneEventUpsert, Record: DataCenterRecord{ID: "dc-1", State: multidatacenter.DataCenterActive, Version: 1}}
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

func TestManagerRunLoopAndBackoff(t *testing.T) {
	manager := newTestManager(t, &MockControlPlane{}, func(config *Config) {
		config.JitterRatio = 0.1
		config.MaxBackoff = 5 * time.Second
	})

	ctx, cancel := context.WithCancel(context.Background())
	manager.ctx = ctx

	var calls atomic.Int32
	fn := func(ctx context.Context) error {
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
		{ID: "active", State: multidatacenter.DataCenterActive, Version: 1},
		{ID: "inactive", State: multidatacenter.DataCenterInactive, Version: 1},
	})

	records, _ := cache.snapshot()
	require.Len(t, records, 1)
	require.Equal(t, "active", records[0].ID)

	cache.merge([]DataCenterRecord{
		{ID: "merge", State: multidatacenter.DataCenterActive, Version: 2},
	})
	records, _ = cache.snapshot()
	require.Len(t, records, 2)

	cache.apply(ControlPlaneEvent{
		Type:   multidatacenter.ControlPlaneEventDelete,
		Record: DataCenterRecord{ID: "active", Version: 3},
	})
	records, _ = cache.snapshot()
	require.Len(t, records, 1)

	cache.apply(ControlPlaneEvent{
		Type:   multidatacenter.ControlPlaneEventUpsert,
		Record: DataCenterRecord{ID: "active", State: multidatacenter.DataCenterActive, Version: 2},
	})
	records, _ = cache.snapshot()
	require.Len(t, records, 1)

	cache.apply(ControlPlaneEvent{
		Type:   multidatacenter.ControlPlaneEventUpsert,
		Record: DataCenterRecord{ID: "merge", State: multidatacenter.DataCenterDraining, Version: 4},
	})
	records, _ = cache.snapshot()
	require.Len(t, records, 0)

	cache.replace([]DataCenterRecord{
		{ID: "kept", State: multidatacenter.DataCenterActive, Version: 2},
	})
	cache.replace([]DataCenterRecord{
		{ID: "kept", State: multidatacenter.DataCenterActive, Version: 0},
	})
	records, _ = cache.snapshot()
	require.Len(t, records, 0)

	cache.replace([]DataCenterRecord{
		{ID: "versioned", State: multidatacenter.DataCenterActive, Version: 0},
	})
	records, _ = cache.snapshot()
	require.Len(t, records, 1)

	cache.apply(ControlPlaneEvent{
		Type:   multidatacenter.ControlPlaneEventUpsert,
		Record: DataCenterRecord{ID: "", State: multidatacenter.DataCenterActive, Version: 10},
	})

	cache.apply(ControlPlaneEvent{
		Type:   multidatacenter.ControlPlaneEventDelete,
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
		Type:   multidatacenter.ControlPlaneEventDelete,
		Record: DataCenterRecord{ID: "dc-1", Version: 5},
	})
	cache.merge([]DataCenterRecord{
		{ID: "dc-1", State: multidatacenter.DataCenterActive, Version: 4},
	})

	records, _ := cache.snapshot()
	require.Len(t, records, 0)
}
