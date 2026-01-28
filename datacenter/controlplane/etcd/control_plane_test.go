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

package etcd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	testcontainer "github.com/testcontainers/testcontainers-go/modules/etcd"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/tochemey/goakt/v3/datacenter"
	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/codec"
)

var (
	etcdContainer *testcontainer.EtcdContainer
	etcdEndpoints []string
	idCounter     uint64
)

func TestMain(m *testing.M) {
	ctx := context.Background()
	container, err := testcontainer.Run(
		ctx,
		"gcr.io/etcd-development/etcd:v3.5.14",
		testcontainer.WithNodes("etcd-1", "etcd-2", "etcd-3"),
	)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	endpoints, err := container.ClientEndpoints(ctx)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		_ = testcontainers.TerminateContainer(container)
		os.Exit(1)
	}

	etcdContainer = container
	etcdEndpoints = endpoints

	code := m.Run()
	_ = testcontainers.TerminateContainer(container)
	os.Exit(code)
}

func TestNewControlPlane(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		cp, err := NewControlPlane(nil)
		require.Error(t, err)
		require.Nil(t, cp)
	})

	t.Run("invalid config", func(t *testing.T) {
		cp, err := NewControlPlane(&Config{})
		require.Error(t, err)
		require.Nil(t, cp)
	})

	t.Run("default namespace", func(t *testing.T) {
		config := &Config{
			Endpoints:   etcdEndpoints,
			TTL:         5 * time.Second,
			DialTimeout: 5 * time.Second,
			Timeout:     5 * time.Second,
		}

		cp, err := NewControlPlane(config)
		require.NoError(t, err)
		require.Equal(t, defaultNamespace, config.Namespace)
		require.NoError(t, cp.Close())
	})

	t.Run("context defaults", func(t *testing.T) {
		config := &Config{
			Context:     nil,
			Endpoints:   etcdEndpoints,
			Namespace:   " ",
			TTL:         5 * time.Second,
			DialTimeout: 5 * time.Second,
			Timeout:     5 * time.Second,
		}

		cp, err := NewControlPlane(config)
		require.NoError(t, err)
		require.NotNil(t, config.Context)
		require.Equal(t, defaultNamespace, config.Namespace)
		require.NoError(t, cp.Close())
	})

	t.Run("invalid endpoints", func(t *testing.T) {
		config := &Config{
			Context:     t.Context(),
			Endpoints:   []string{"http://127.0.0.1:1"},
			Namespace:   defaultNamespace,
			TTL:         2 * time.Second,
			DialTimeout: 500 * time.Millisecond,
			Timeout:     500 * time.Millisecond,
		}

		cp, err := NewControlPlane(config)
		require.Error(t, err)
		require.Nil(t, cp)
	})

	t.Run("defaults for nil client functions", func(t *testing.T) {
		config := &Config{
			Context:     t.Context(),
			Endpoints:   etcdEndpoints,
			Namespace:   defaultNamespace,
			TTL:         5 * time.Second,
			DialTimeout: 5 * time.Second,
			Timeout:     5 * time.Second,
		}

		cp, err := newControlPlane(config, nil, nil)
		require.NoError(t, err)
		require.NoError(t, cp.Close())
	})
}

func TestNewControlPlaneClientErrors(t *testing.T) {
	t.Run("new client error", func(t *testing.T) {
		config := &Config{
			Context:     t.Context(),
			Endpoints:   []string{"http://127.0.0.1:2379"},
			Namespace:   defaultNamespace,
			TTL:         5 * time.Second,
			DialTimeout: 5 * time.Second,
			Timeout:     5 * time.Second,
		}

		cp, err := newControlPlane(config, func(clientv3.Config) (*clientv3.Client, error) {
			return nil, errors.New("boom")
		}, nil)
		require.Error(t, err)
		require.Nil(t, cp)
	})

	t.Run("close error on status failure", func(t *testing.T) {
		config := &Config{
			Context:     t.Context(),
			Endpoints:   []string{"http://127.0.0.1:1"},
			Namespace:   defaultNamespace,
			TTL:         2 * time.Second,
			DialTimeout: 500 * time.Millisecond,
			Timeout:     500 * time.Millisecond,
		}

		cp, err := newControlPlane(config, nil, func(*clientv3.Client) error {
			return errors.New("close failed")
		})
		require.Error(t, err)
		require.Nil(t, cp)
		require.Contains(t, err.Error(), "failed to close etcd client")
	})
}

func TestControlPlaneClose(t *testing.T) {
	cp := &ControlPlane{}
	require.NoError(t, cp.Close())
}

func TestControlPlaneCloseBranches(t *testing.T) {
	client := clientv3.NewCtxClient(context.Background())
	cp := &ControlPlane{client: client}
	require.ErrorIs(t, cp.Close(), context.Canceled)

	client = clientv3.NewCtxClient(context.Background())
	cp = &ControlPlane{
		client: client,
		closeFunc: func(*clientv3.Client) error {
			return nil
		},
	}
	require.NoError(t, cp.Close())
}

func TestControlPlaneRegisterAndListActive(t *testing.T) {
	cp := newTestControlPlane(t)

	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive

	id, version, err := cp.Register(t.Context(), record)
	require.NoError(t, err)
	require.Equal(t, record.ID, id)
	require.NotZero(t, version)

	records, err := cp.ListActive(t.Context())
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, record.ID, records[0].ID)
	require.Equal(t, datacenter.DataCenterActive, records[0].State)
	require.NotZero(t, records[0].Version)
	require.True(t, records[0].LeaseExpiry.After(time.Now()))
}

func TestControlPlaneRegisterConflict(t *testing.T) {
	cp := newTestControlPlane(t)

	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive

	_, version, err := cp.Register(t.Context(), record)
	require.NoError(t, err)

	record.Version = version + 1
	_, _, err = cp.Register(t.Context(), record)
	require.ErrorIs(t, err, gerrors.ErrDataCenterRecordConflict)
}

func TestControlPlaneRegisterValidation(t *testing.T) {
	cp := newTestControlPlane(t)

	_, _, err := cp.Register(t.Context(), datacenter.DataCenterRecord{})
	require.Error(t, err)

	record := newRecord(nextID())
	record.State = datacenter.DataCenterState("BROKEN")
	_, _, err = cp.Register(t.Context(), record)
	require.Error(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err = cp.Register(ctx, newRecord(nextID()))
	require.Error(t, err)
}

func TestControlPlaneRegisterErrorPaths(t *testing.T) {
	record := newRecord("dc-register-errors")
	record.State = datacenter.DataCenterActive

	t.Run("lease grant error", func(t *testing.T) {
		cp := &ControlPlane{
			config: &Config{Timeout: time.Second},
			lease:  &fakeLease{grantErr: errors.New("grant error")},
		}
		_, _, err := cp.Register(context.Background(), record)
		require.Error(t, err)
	})

	t.Run("register compare error", func(t *testing.T) {
		cp := &ControlPlane{
			config: &Config{Timeout: time.Second},
			lease:  &fakeLease{grantResp: &clientv3.LeaseGrantResponse{ID: 1, TTL: 5}},
			kv:     &fakeKV{getErr: errors.New("get error")},
		}
		_, _, err := cp.Register(context.Background(), record)
		require.Error(t, err)
	})

	t.Run("txn error", func(t *testing.T) {
		cp := &ControlPlane{
			config: &Config{Timeout: time.Second},
			lease:  &fakeLease{grantResp: &clientv3.LeaseGrantResponse{ID: 2, TTL: 5}},
			kv: &fakeKV{
				getResp: &clientv3.GetResponse{},
				txn:     &fakeTxn{err: errors.New("txn error")},
			},
		}
		_, _, err := cp.Register(context.Background(), record)
		require.Error(t, err)
	})

	t.Run("txn conflict", func(t *testing.T) {
		cp := &ControlPlane{
			config: &Config{Timeout: time.Second},
			lease:  &fakeLease{grantResp: &clientv3.LeaseGrantResponse{ID: 3, TTL: 5}},
			kv: &fakeKV{
				getResp: &clientv3.GetResponse{},
				txn: &fakeTxn{
					resp: &clientv3.TxnResponse{
						Succeeded: false,
						Header:    &etcdserverpb.ResponseHeader{Revision: 10},
					},
				},
			},
		}
		_, _, err := cp.Register(context.Background(), record)
		require.ErrorIs(t, err, gerrors.ErrDataCenterRecordConflict)
	})

	t.Run("encode error", func(t *testing.T) {
		cp := &ControlPlane{
			config: &Config{Timeout: time.Second},
			lease:  &fakeLease{grantResp: &clientv3.LeaseGrantResponse{ID: 4, TTL: 5}},
			kv:     &fakeKV{},
			encodeRecordFunc: func(datacenter.DataCenterRecord) ([]byte, error) {
				return nil, errors.New("encode error")
			},
		}
		_, _, err := cp.Register(context.Background(), record)
		require.Error(t, err)
	})
}

func TestControlPlaneRegisterDefaultsState(t *testing.T) {
	cp := newTestControlPlane(t)

	record := newRecord(nextID())
	record.State = ""

	_, version, err := cp.Register(t.Context(), record)
	require.NoError(t, err)

	kv, err := cp.getRecordKV(t.Context(), record.ID)
	require.NoError(t, err)
	require.Equal(t, int64(version), kv.ModRevision)

	decoded, err := codec.DecodeDataCenterRecord(kv.Value)
	require.NoError(t, err)
	require.Equal(t, datacenter.DataCenterRegistered, decoded.State)
}

func TestControlPlaneHeartbeat(t *testing.T) {
	cp := newTestControlPlane(t)

	_, _, err := cp.Heartbeat(t.Context(), " ", 1)
	require.Error(t, err)

	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive

	_, version, err := cp.Register(t.Context(), record)
	require.NoError(t, err)

	newVersion, leaseExpiry, err := cp.Heartbeat(t.Context(), record.ID, version)
	require.NoError(t, err)
	require.Greater(t, newVersion, version)
	require.True(t, leaseExpiry.After(time.Now()))

	_, _, err = cp.Heartbeat(t.Context(), record.ID, newVersion+1)
	require.ErrorIs(t, err, gerrors.ErrDataCenterRecordConflict)

	_, _, err = cp.Heartbeat(t.Context(), "missing", 1)
	require.ErrorIs(t, err, gerrors.ErrDataCenterRecordNotFound)

	record = newRecord(nextID())
	payload, err := codec.EncodeDataCenterRecord(record)
	require.NoError(t, err)
	_, err = cp.kv.Put(t.Context(), recordKey(record.ID), string(payload))
	require.NoError(t, err)

	kv, err := cp.getRecordKV(t.Context(), record.ID)
	require.NoError(t, err)

	_, _, err = cp.Heartbeat(t.Context(), record.ID, uint64(kv.ModRevision))
	require.Error(t, err)
	require.Contains(t, err.Error(), "record has no lease")

	record = newRecord(nextID())
	invalidPayload := []byte("invalid-payload")
	leaseResp, err := cp.lease.Grant(t.Context(), cp.leaseTTLSeconds())
	require.NoError(t, err)
	_, err = cp.kv.Put(t.Context(), recordKey(record.ID), string(invalidPayload), clientv3.WithLease(leaseResp.ID))
	require.NoError(t, err)

	kv, err = cp.getRecordKV(t.Context(), record.ID)
	require.NoError(t, err)

	_, _, err = cp.Heartbeat(t.Context(), record.ID, uint64(kv.ModRevision))
	require.Error(t, err)
}

func TestControlPlaneHeartbeatErrorPaths(t *testing.T) {
	record := newRecord("dc-heartbeat-errors")
	record.State = datacenter.DataCenterActive
	payload, err := codec.EncodeDataCenterRecord(record)
	require.NoError(t, err)

	t.Run("get record error", func(t *testing.T) {
		cp := &ControlPlane{
			config: &Config{Timeout: time.Second},
			kv:     &fakeKV{getErr: errors.New("get error")},
		}
		_, _, err := cp.Heartbeat(context.Background(), record.ID, 1)
		require.Error(t, err)
	})

	t.Run("keepalive error", func(t *testing.T) {
		cp := &ControlPlane{
			config: &Config{Timeout: time.Second},
			kv: &fakeKV{
				getResp: &clientv3.GetResponse{
					Kvs: []*mvccpb.KeyValue{{Value: payload, ModRevision: 1, Lease: 2}},
				},
			},
			lease: &fakeLease{keepAliveOnceErr: errors.New("keepalive error")},
		}
		_, _, err := cp.Heartbeat(context.Background(), record.ID, 1)
		require.Error(t, err)
	})

	t.Run("txn error", func(t *testing.T) {
		cp := &ControlPlane{
			config: &Config{Timeout: time.Second},
			kv: &fakeKV{
				getResp: &clientv3.GetResponse{
					Kvs: []*mvccpb.KeyValue{{Value: payload, ModRevision: 2, Lease: 3}},
				},
				txn: &fakeTxn{err: errors.New("txn error")},
			},
			lease: &fakeLease{keepAliveOnceResp: &clientv3.LeaseKeepAliveResponse{TTL: 5}},
		}
		_, _, err := cp.Heartbeat(context.Background(), record.ID, 2)
		require.Error(t, err)
	})

	t.Run("txn conflict", func(t *testing.T) {
		cp := &ControlPlane{
			config: &Config{Timeout: time.Second},
			kv: &fakeKV{
				getResp: &clientv3.GetResponse{
					Kvs: []*mvccpb.KeyValue{{Value: payload, ModRevision: 3, Lease: 4}},
				},
				txn: &fakeTxn{
					resp: &clientv3.TxnResponse{
						Succeeded: false,
						Header:    &etcdserverpb.ResponseHeader{Revision: 12},
					},
				},
			},
			lease: &fakeLease{keepAliveOnceResp: &clientv3.LeaseKeepAliveResponse{TTL: 5}},
		}
		_, _, err := cp.Heartbeat(context.Background(), record.ID, 3)
		require.ErrorIs(t, err, gerrors.ErrDataCenterRecordConflict)
	})

	t.Run("encode error", func(t *testing.T) {
		cp := &ControlPlane{
			config: &Config{Timeout: time.Second},
			kv: &fakeKV{
				getResp: &clientv3.GetResponse{
					Kvs: []*mvccpb.KeyValue{{Value: payload, ModRevision: 4, Lease: 4}},
				},
			},
			lease: &fakeLease{keepAliveOnceResp: &clientv3.LeaseKeepAliveResponse{TTL: 5}},
			encodeRecordFunc: func(datacenter.DataCenterRecord) ([]byte, error) {
				return nil, errors.New("encode error")
			},
		}
		_, _, err := cp.Heartbeat(context.Background(), record.ID, 4)
		require.Error(t, err)
	})
}

func TestControlPlaneSetState(t *testing.T) {
	cp := newTestControlPlane(t)

	_, err := cp.SetState(t.Context(), " ", datacenter.DataCenterActive, 1)
	require.Error(t, err)

	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive

	_, version, err := cp.Register(t.Context(), record)
	require.NoError(t, err)

	newVersion, err := cp.SetState(t.Context(), record.ID, datacenter.DataCenterDraining, version)
	require.NoError(t, err)
	require.Greater(t, newVersion, version)

	_, err = cp.SetState(t.Context(), record.ID, datacenter.DataCenterState("BAD"), newVersion)
	require.Error(t, err)

	_, err = cp.SetState(t.Context(), record.ID, datacenter.DataCenterActive, newVersion+1)
	require.ErrorIs(t, err, gerrors.ErrDataCenterRecordConflict)

	_, err = cp.SetState(t.Context(), "missing", datacenter.DataCenterActive, 1)
	require.ErrorIs(t, err, gerrors.ErrDataCenterRecordNotFound)

	record = newRecord(nextID())
	payload, err := codec.EncodeDataCenterRecord(record)
	require.NoError(t, err)
	_, err = cp.kv.Put(t.Context(), recordKey(record.ID), string(payload))
	require.NoError(t, err)

	kv, err := cp.getRecordKV(t.Context(), record.ID)
	require.NoError(t, err)

	_, err = cp.SetState(t.Context(), record.ID, datacenter.DataCenterActive, uint64(kv.ModRevision))
	require.NoError(t, err)

	invalidID := nextID()
	_, err = cp.kv.Put(t.Context(), recordKey(invalidID), "invalid")
	require.NoError(t, err)

	kv, err = cp.getRecordKV(t.Context(), invalidID)
	require.NoError(t, err)

	_, err = cp.SetState(t.Context(), invalidID, datacenter.DataCenterActive, uint64(kv.ModRevision))
	require.Error(t, err)
}

func TestControlPlaneSetStateErrorPaths(t *testing.T) {
	record := newRecord("dc-setstate-errors")
	record.State = datacenter.DataCenterActive
	payload, err := codec.EncodeDataCenterRecord(record)
	require.NoError(t, err)

	t.Run("get record error", func(t *testing.T) {
		cp := &ControlPlane{
			config: &Config{Timeout: time.Second},
			kv:     &fakeKV{getErr: errors.New("get error")},
		}
		_, err := cp.SetState(context.Background(), record.ID, datacenter.DataCenterActive, 1)
		require.Error(t, err)
	})

	t.Run("txn error", func(t *testing.T) {
		cp := &ControlPlane{
			config: &Config{Timeout: time.Second},
			kv: &fakeKV{
				getResp: &clientv3.GetResponse{
					Kvs: []*mvccpb.KeyValue{{Value: payload, ModRevision: 2, Lease: 1}},
				},
				txn: &fakeTxn{err: errors.New("txn error")},
			},
		}
		_, err := cp.SetState(context.Background(), record.ID, datacenter.DataCenterActive, 2)
		require.Error(t, err)
	})

	t.Run("txn conflict", func(t *testing.T) {
		cp := &ControlPlane{
			config: &Config{Timeout: time.Second},
			kv: &fakeKV{
				getResp: &clientv3.GetResponse{
					Kvs: []*mvccpb.KeyValue{{Value: payload, ModRevision: 3, Lease: 1}},
				},
				txn: &fakeTxn{
					resp: &clientv3.TxnResponse{
						Succeeded: false,
						Header:    &etcdserverpb.ResponseHeader{Revision: 15},
					},
				},
			},
		}
		_, err := cp.SetState(context.Background(), record.ID, datacenter.DataCenterActive, 3)
		require.ErrorIs(t, err, gerrors.ErrDataCenterRecordConflict)
	})

	t.Run("encode error", func(t *testing.T) {
		cp := &ControlPlane{
			config: &Config{Timeout: time.Second},
			kv: &fakeKV{
				getResp: &clientv3.GetResponse{
					Kvs: []*mvccpb.KeyValue{{Value: payload, ModRevision: 4, Lease: 1}},
				},
			},
			encodeRecordFunc: func(datacenter.DataCenterRecord) ([]byte, error) {
				return nil, errors.New("encode error")
			},
		}
		_, err := cp.SetState(context.Background(), record.ID, datacenter.DataCenterActive, 4)
		require.Error(t, err)
	})
}

func TestControlPlaneListActiveSkipsInactive(t *testing.T) {
	cp := newTestControlPlane(t)

	active := newRecord(nextID())
	active.State = datacenter.DataCenterActive
	_, _, err := cp.Register(t.Context(), active)
	require.NoError(t, err)

	activeTwo := newRecord(nextID())
	activeTwo.State = datacenter.DataCenterActive
	_, _, err = cp.Register(t.Context(), activeTwo)
	require.NoError(t, err)

	inactive := newRecord(nextID())
	inactive.State = datacenter.DataCenterRegistered
	_, _, err = cp.Register(t.Context(), inactive)
	require.NoError(t, err)

	_, err = cp.kv.Put(t.Context(), "junk", "value")
	require.NoError(t, err)

	records, err := cp.ListActive(t.Context())
	require.NoError(t, err)
	require.Len(t, records, 2)
	require.ElementsMatch(t, []string{active.ID, activeTwo.ID}, []string{records[0].ID, records[1].ID})
}

func TestControlPlaneListActiveErrors(t *testing.T) {
	cp := newTestControlPlane(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := cp.ListActive(ctx)
	require.Error(t, err)

	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	payload, err := codec.EncodeDataCenterRecord(record)
	require.NoError(t, err)

	_, err = cp.kv.Put(t.Context(), recordKey(record.ID), string(payload))
	require.NoError(t, err)

	_, err = cp.kv.Put(t.Context(), recordKey(nextID()), "invalid")
	require.NoError(t, err)

	_, err = cp.ListActive(t.Context())
	require.Error(t, err)
}

func TestControlPlaneLeaseExpiry(t *testing.T) {
	cp := &ControlPlane{
		config: &Config{Timeout: time.Second},
		lease: &fakeLease{
			timeToLiveResp: &clientv3.LeaseTimeToLiveResponse{TTL: 5},
		},
	}

	expiry, active := cp.leaseExpiry(context.Background(), 1)
	require.True(t, active)
	require.True(t, expiry.After(time.Now()))

	cp.lease = &fakeLease{
		timeToLiveResp: &clientv3.LeaseTimeToLiveResponse{TTL: 0},
	}
	expiry, active = cp.leaseExpiry(context.Background(), 1)
	require.False(t, active)
	require.True(t, expiry.IsZero())

	cp.lease = &fakeLease{timeToLiveErr: errors.New("ttl error")}
	expiry, active = cp.leaseExpiry(context.Background(), 1)
	require.False(t, active)
	require.True(t, expiry.IsZero())
}

func TestControlPlaneWatch(t *testing.T) {
	cp := newTestControlPlane(t)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	events, err := cp.Watch(ctx)
	require.NoError(t, err)

	_, err = cp.kv.Put(t.Context(), "junk", "value")
	require.NoError(t, err)

	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive

	_, _, err = cp.Register(t.Context(), record)
	require.NoError(t, err)

	waitForEvent(t, events, func(event datacenter.ControlPlaneEvent) bool {
		return event.Type == datacenter.ControlPlaneEventUpsert && event.Record.ID == record.ID
	})

	_, err = cp.kv.Delete(t.Context(), recordKey(record.ID))
	require.NoError(t, err)

	waitForEvent(t, events, func(event datacenter.ControlPlaneEvent) bool {
		return event.Type == datacenter.ControlPlaneEventDelete && event.Record.ID == record.ID
	})
}

func TestControlPlaneWatchError(t *testing.T) {
	respCh := make(chan clientv3.WatchResponse, 1)
	respCh <- clientv3.WatchResponse{CompactRevision: 1}
	close(respCh)

	cp := &ControlPlane{
		config:  &Config{},
		watcher: &fakeWatcher{ch: respCh},
	}

	events, err := cp.Watch(t.Context())
	require.NoError(t, err)

	_, ok := <-events
	require.False(t, ok)
}

func TestControlPlaneWatchBranches(t *testing.T) {
	respCh := make(chan clientv3.WatchResponse, 2)
	respCh <- clientv3.WatchResponse{
		Events: []*clientv3.Event{
			{
				Type: clientv3.EventTypePut,
				Kv: &mvccpb.KeyValue{
					Key:   []byte(recordKey("dc-watch")),
					Value: []byte("invalid"),
				},
			},
		},
	}
	close(respCh)

	cp := &ControlPlane{
		config:  &Config{},
		watcher: &fakeWatcher{ch: respCh},
	}

	events, err := cp.Watch(context.TODO())
	require.NoError(t, err)
	_, ok := <-events
	require.False(t, ok)
}

func TestControlPlaneWatchContextCancel(t *testing.T) {
	record := newRecord("dc-watch-cancel")
	record.State = datacenter.DataCenterActive
	payload, err := codec.EncodeDataCenterRecord(record)
	require.NoError(t, err)

	watchCh := make(chan clientv3.WatchResponse)
	cp := &ControlPlane{
		config:  &Config{},
		watcher: &fakeWatcher{ch: watchCh},
	}

	ctx, cancel := context.WithCancel(context.Background())
	events, err := cp.Watch(ctx)
	require.NoError(t, err)
	cancel()

	go func() {
		watchCh <- clientv3.WatchResponse{
			Events: []*clientv3.Event{
				{
					Type: clientv3.EventTypePut,
					Kv: &mvccpb.KeyValue{
						Key:   []byte(recordKey(record.ID)),
						Value: payload,
					},
				},
			},
		}
		close(watchCh)
	}()

	require.Eventually(t, func() bool {
		select {
		case _, ok := <-events:
			return !ok
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
}

func TestControlPlaneHelpers(t *testing.T) {
	cp := newTestControlPlane(t)

	assert.Equal(t, defaultNamespace+"/", normalizeNamespace(""))
	assert.Equal(t, "/foo/", normalizeNamespace("/foo"))
	assert.Equal(t, "/foo/", normalizeNamespace("/foo/"))

	key := recordKey("dc-1")
	assert.True(t, isRecordKey(key))
	assert.False(t, isRecordKey("dc-1"))

	id, ok := parseRecordID(key)
	require.True(t, ok)
	require.Equal(t, "dc-1", id)

	_, ok = parseRecordID("bad-key")
	require.False(t, ok)

	_, ok = parseRecordID(recordSuffix)
	require.False(t, ok)

	assert.True(t, isValidState(datacenter.DataCenterActive))
	assert.False(t, isValidState("INVALID"))

	assert.Equal(t, int64(1), (&ControlPlane{config: &Config{TTL: 500 * time.Millisecond}}).leaseTTLSeconds())
	assert.Equal(t, int64(5), (&ControlPlane{config: &Config{TTL: 5 * time.Second}}).leaseTTLSeconds())

	ctx, cancel := cp.withTimeout(context.TODO())
	cancel()
	_, ok = ctx.Deadline()
	require.True(t, ok)

	ctx, cancel = (&ControlPlane{config: &Config{Timeout: time.Second}}).withTimeout(context.TODO())
	cancel()
	_, ok = ctx.Deadline()
	require.True(t, ok)

	cmp, err := cp.registerCompare(t.Context(), recordKey(nextID()), 0)
	require.NoError(t, err)
	require.NotNil(t, cmp)

	cmp, err = cp.registerCompare(t.Context(), recordKey(nextID()), 10)
	require.NoError(t, err)
	require.NotNil(t, cmp)

	cancelCtx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = cp.registerCompare(cancelCtx, recordKey(nextID()), 0)
	require.Error(t, err)

	fakeCompareCP := &ControlPlane{
		config: &Config{Timeout: time.Second},
		kv: &fakeKV{
			getResp: &clientv3.GetResponse{
				Kvs: []*mvccpb.KeyValue{{ModRevision: 2}},
			},
		},
	}
	_, err = fakeCompareCP.registerCompare(context.Background(), recordKey(nextID()), 0)
	require.NoError(t, err)

	_, err = cp.getRecordKV(cancelCtx, "missing")
	require.Error(t, err)

	_, ok = parseRecordID("")
	require.False(t, ok)

	expiry, active := cp.leaseExpiry(t.Context(), 0)
	require.False(t, active)
	require.True(t, expiry.IsZero())
}

func TestControlPlaneProtoHelpers(t *testing.T) {
	record := newRecord("dc-proto")
	record.State = datacenter.DataCenterActive
	record.LeaseExpiry = time.Now().Add(1 * time.Minute)

	payload, err := codec.EncodeDataCenterRecord(record)
	require.NoError(t, err)

	decoded, err := codec.DecodeDataCenterRecord(payload)
	require.NoError(t, err)
	require.Equal(t, record.ID, decoded.ID)
	require.Equal(t, record.DataCenter.Name, decoded.DataCenter.Name)
	require.Equal(t, record.State, decoded.State)

	record.LeaseExpiry = time.Time{}

	_, err = codec.EncodeDataCenterRecord(datacenter.DataCenterRecord{State: "INVALID"})
	require.Error(t, err)

	_, err = codec.DecodeDataCenterRecord([]byte("invalid"))
	require.Error(t, err)
}

func TestControlPlaneEventConversion(t *testing.T) {
	cp := newTestControlPlane(t)

	record := newRecord("dc-event")
	record.State = datacenter.DataCenterActive
	payload, err := codec.EncodeDataCenterRecord(record)
	require.NoError(t, err)

	putEvent := &clientv3.Event{
		Type: clientv3.EventTypePut,
		Kv: &mvccpb.KeyValue{
			Key:         []byte(recordKey(record.ID)),
			Value:       payload,
			ModRevision: 10,
			Lease:       0,
		},
	}

	event, ok := cp.toControlPlaneEvent(t.Context(), putEvent)
	require.True(t, ok)
	require.Equal(t, datacenter.ControlPlaneEventUpsert, event.Type)

	deleteEvent := &clientv3.Event{
		Type: clientv3.EventTypeDelete,
		Kv: &mvccpb.KeyValue{
			Key: []byte(recordKey(record.ID)),
		},
	}

	event, ok = cp.toControlPlaneEvent(t.Context(), deleteEvent)
	require.True(t, ok)
	require.Equal(t, datacenter.ControlPlaneEventDelete, event.Type)
	require.Equal(t, record.ID, event.Record.ID)

	deleteEvent.PrevKv = &mvccpb.KeyValue{
		Value:       payload,
		ModRevision: 12,
	}
	event, ok = cp.toControlPlaneEvent(t.Context(), deleteEvent)
	require.True(t, ok)
	require.Equal(t, record.ID, event.Record.ID)

	deleteEvent.PrevKv = &mvccpb.KeyValue{
		Value: []byte("invalid"),
	}
	_, ok = cp.toControlPlaneEvent(t.Context(), deleteEvent)
	require.False(t, ok)

	invalidEvent := &clientv3.Event{
		Type: clientv3.EventTypePut,
		Kv: &mvccpb.KeyValue{
			Value: []byte("bad"),
		},
	}

	_, ok = cp.toControlPlaneEvent(t.Context(), invalidEvent)
	require.False(t, ok)

	unknownEvent := &clientv3.Event{
		Type: mvccpb.Event_EventType(99),
	}

	_, ok = cp.toControlPlaneEvent(t.Context(), unknownEvent)
	require.True(t, ok)
}

func TestControlPlaneErrors(t *testing.T) {
	err := errors.New("base")
	assert.ErrorIs(t, errors.Join(err, gerrors.ErrDataCenterRecordConflict), gerrors.ErrDataCenterRecordConflict)
}

func newTestControlPlane(t *testing.T) *ControlPlane {
	t.Helper()
	config := &Config{
		Context:     t.Context(),
		Endpoints:   etcdEndpoints,
		Namespace:   testNamespace(t),
		TTL:         5 * time.Second,
		DialTimeout: 5 * time.Second,
		Timeout:     5 * time.Second,
	}
	cp, err := NewControlPlane(config)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = cp.Close()
	})
	return cp
}

func newRecord(id string) datacenter.DataCenterRecord {
	return datacenter.DataCenterRecord{
		ID: id,
		DataCenter: datacenter.DataCenter{
			Name:   id,
			Region: "us-east-1",
			Zone:   "us-east-1a",
			Labels: map[string]string{"tier": "prod"},
		},
		Endpoints: []string{"127.0.0.1:9000"},
	}
}

func nextID() string {
	return fmt.Sprintf("dc-%d", atomic.AddUint64(&idCounter, 1))
}

func testNamespace(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s/%s", defaultNamespace, strings.ReplaceAll(t.Name(), "/", "-"))
}

func waitForEvent(t *testing.T, events <-chan datacenter.ControlPlaneEvent, match func(datacenter.ControlPlaneEvent) bool) {
	t.Helper()

	timeout := time.After(5 * time.Second)
	for {
		select {
		case event, ok := <-events:
			require.True(t, ok, "watch channel closed")
			if match(event) {
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for control plane event")
		}
	}
}

type fakeKV struct {
	getResp *clientv3.GetResponse
	getErr  error
	txn     clientv3.Txn
}

func (f *fakeKV) Put(context.Context, string, string, ...clientv3.OpOption) (*clientv3.PutResponse, error) {
	return &clientv3.PutResponse{}, nil
}

func (f *fakeKV) Get(context.Context, string, ...clientv3.OpOption) (*clientv3.GetResponse, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.getResp == nil {
		return &clientv3.GetResponse{}, nil
	}
	return f.getResp, nil
}

func (f *fakeKV) Delete(context.Context, string, ...clientv3.OpOption) (*clientv3.DeleteResponse, error) {
	return &clientv3.DeleteResponse{}, nil
}

func (f *fakeKV) Compact(context.Context, int64, ...clientv3.CompactOption) (*clientv3.CompactResponse, error) {
	return &clientv3.CompactResponse{}, nil
}

func (f *fakeKV) Do(context.Context, clientv3.Op) (clientv3.OpResponse, error) {
	return clientv3.OpResponse{}, nil
}

func (f *fakeKV) Txn(context.Context) clientv3.Txn {
	if f.txn == nil {
		return &fakeTxn{}
	}
	return f.txn
}

type fakeTxn struct {
	resp *clientv3.TxnResponse
	err  error
}

func (f *fakeTxn) If(...clientv3.Cmp) clientv3.Txn {
	return f
}

func (f *fakeTxn) Then(...clientv3.Op) clientv3.Txn {
	return f
}

func (f *fakeTxn) Else(...clientv3.Op) clientv3.Txn {
	return f
}

func (f *fakeTxn) Commit() (*clientv3.TxnResponse, error) {
	if f.resp == nil {
		return &clientv3.TxnResponse{}, f.err
	}
	return f.resp, f.err
}

type fakeLease struct {
	grantResp         *clientv3.LeaseGrantResponse
	grantErr          error
	keepAliveOnceResp *clientv3.LeaseKeepAliveResponse
	keepAliveOnceErr  error
	timeToLiveResp    *clientv3.LeaseTimeToLiveResponse
	timeToLiveErr     error
}

func (f *fakeLease) Grant(context.Context, int64) (*clientv3.LeaseGrantResponse, error) {
	if f.grantResp == nil {
		return &clientv3.LeaseGrantResponse{ID: 1, TTL: 1}, f.grantErr
	}
	return f.grantResp, f.grantErr
}

func (f *fakeLease) Revoke(context.Context, clientv3.LeaseID) (*clientv3.LeaseRevokeResponse, error) {
	return &clientv3.LeaseRevokeResponse{}, nil
}

func (f *fakeLease) TimeToLive(context.Context, clientv3.LeaseID, ...clientv3.LeaseOption) (*clientv3.LeaseTimeToLiveResponse, error) {
	if f.timeToLiveResp == nil {
		return &clientv3.LeaseTimeToLiveResponse{}, f.timeToLiveErr
	}
	return f.timeToLiveResp, f.timeToLiveErr
}

func (f *fakeLease) Leases(context.Context) (*clientv3.LeaseLeasesResponse, error) {
	return &clientv3.LeaseLeasesResponse{}, nil
}

func (f *fakeLease) KeepAlive(context.Context, clientv3.LeaseID) (<-chan *clientv3.LeaseKeepAliveResponse, error) {
	return make(chan *clientv3.LeaseKeepAliveResponse), nil
}

func (f *fakeLease) KeepAliveOnce(context.Context, clientv3.LeaseID) (*clientv3.LeaseKeepAliveResponse, error) {
	if f.keepAliveOnceResp == nil {
		return &clientv3.LeaseKeepAliveResponse{TTL: 1}, f.keepAliveOnceErr
	}
	return f.keepAliveOnceResp, f.keepAliveOnceErr
}

func (f *fakeLease) Close() error {
	return nil
}

type fakeWatcher struct {
	ch clientv3.WatchChan
}

func (f *fakeWatcher) Watch(context.Context, string, ...clientv3.OpOption) clientv3.WatchChan {
	return f.ch
}

func (f *fakeWatcher) RequestProgress(context.Context) error {
	return nil
}

func (f *fakeWatcher) Close() error {
	return nil
}
