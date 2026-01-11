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

package cluster

import (
	"context"
	"reflect"
	"time"
	"unsafe"

	goset "github.com/deckarep/golang-set/v2"
	"github.com/tochemey/olric"
	"github.com/tochemey/olric/pkg/storage"
	"github.com/tochemey/olric/stats"

	"github.com/tochemey/goakt/v3/discovery"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/log"
)

type MockFailingGetDMap struct {
	olric.DMap
	key string
}

func (x MockFailingGetDMap) Get(ctx context.Context, key string) (*olric.GetResponse, error) {
	if key == x.key {
		return nil, olric.ErrKeyNotFound
	}
	return x.DMap.Get(ctx, key)
}

type MockClient struct {
	newDMapErr   error
	newPubSubErr error
	membersErr   error
}

// nolint
func (x *MockClient) NewDMap(name string, options ...olric.DMapOption) (olric.DMap, error) {
	if x.newDMapErr != nil {
		return nil, x.newDMapErr
	}
	return &MockDMap{}, nil
}

// nolint
func (x *MockClient) NewPubSub(options ...olric.PubSubOption) (*olric.PubSub, error) {
	if x.newPubSubErr != nil {
		return nil, x.newPubSubErr
	}
	panic("unexpected call to NewPubSub without error")
}

// nolint
func (x *MockClient) Stats(ctx context.Context, address string, options ...olric.StatsOption) (stats.Stats, error) {
	return stats.Stats{}, nil
}

// nolint
func (x *MockClient) Ping(ctx context.Context, address, message string) (string, error) {
	return "", nil
}

// nolint
func (x *MockClient) RoutingTable(ctx context.Context) (olric.RoutingTable, error) {
	return nil, nil
}

// nolint
func (x *MockClient) Members(ctx context.Context) ([]olric.Member, error) {
	if x.membersErr != nil {
		return nil, x.membersErr
	}
	panic("unexpected call to Members without error")
}

// nolint
func (x *MockClient) RefreshMetadata(ctx context.Context) error {
	return nil
}

// nolint
func (x *MockClient) Close(ctx context.Context) error {
	return nil
}

type MockDMap struct {
	putErr   error
	putFn    func(ctx context.Context, key string, value any, options ...olric.PutOption) error
	getFn    func(ctx context.Context, key string) (*olric.GetResponse, error)
	scanFn   func(ctx context.Context, options ...olric.ScanOption) (olric.Iterator, error)
	deleteFn func(ctx context.Context, keys ...string) (int, error)
	incrFn   func(ctx context.Context, key string, delta int) (int, error)
	incrErr  error
}

func (x *MockDMap) Name() string { return "fake-dmap" }

// nolint
func (x *MockDMap) Put(ctx context.Context, key string, value any, options ...olric.PutOption) error {
	if x.putFn != nil {
		return x.putFn(ctx, key, value, options...)
	}
	if x.putErr != nil {
		return x.putErr
	}
	return nil
}

// nolint
func (x *MockDMap) Get(ctx context.Context, key string) (*olric.GetResponse, error) {
	if x.getFn != nil {
		return x.getFn(ctx, key)
	}
	panic("unexpected call to Get")
}

// nolint
func (x *MockDMap) Delete(ctx context.Context, keys ...string) (int, error) {
	if x.deleteFn != nil {
		return x.deleteFn(ctx, keys...)
	}
	panic("unexpected call to Delete")
}

// nolint
func (x *MockDMap) Incr(ctx context.Context, key string, delta int) (int, error) {
	if x.incrFn != nil {
		return x.incrFn(ctx, key, delta)
	}
	if x.incrErr != nil {
		return 0, x.incrErr
	}
	panic("unexpected call to Incr")
}

// nolint
func (x *MockDMap) Decr(ctx context.Context, key string, delta int) (int, error) {
	panic("unexpected call to Decr")
}

// nolint
func (x *MockDMap) GetPut(ctx context.Context, key string, value any) (*olric.GetResponse, error) {
	panic("unexpected call to GetPut")
}

// nolint
func (x *MockDMap) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	panic("unexpected call to IncrByFloat")
}

// nolint
func (x *MockDMap) Expire(ctx context.Context, key string, timeout time.Duration) error {
	panic("unexpected call to Expire")
}

// nolint
func (x *MockDMap) Lock(ctx context.Context, key string, deadline time.Duration) (olric.LockContext, error) {
	panic("unexpected call to Lock")
}

// nolint
func (x *MockDMap) LockWithTimeout(ctx context.Context, key string, timeout, deadline time.Duration) (olric.LockContext, error) {
	panic("unexpected call to LockWithTimeout")
}

// nolint
func (x *MockDMap) Scan(ctx context.Context, options ...olric.ScanOption) (olric.Iterator, error) {
	if x.scanFn != nil {
		return x.scanFn(ctx, options...)
	}
	panic("unexpected call to Scan")
}

// nolint
func (x *MockDMap) Destroy(ctx context.Context) error {
	panic("unexpected call to Destroy")
}

// nolint
func (x *MockDMap) Pipeline(opts ...olric.PipelineOption) (*olric.DMapPipeline, error) {
	panic("unexpected call to Pipeline")
}

type MockCluster struct {
	grainExistsFn    func(context.Context, string) (bool, error)
	putGrainFn       func(context.Context, *internalpb.Grain) error
	putKindFn        func(context.Context, string) error
	lookupKindFn     func(context.Context, string) (string, error)
	grainExistsCalls int
	putGrainCalls    int
	putKindCalls     int
	lookupKindCalls  int
}

func (x *MockCluster) Start(context.Context) error { panic("unexpected call") }
func (x *MockCluster) Stop(context.Context) error  { panic("unexpected call") }
func (x *MockCluster) PutActor(context.Context, *internalpb.Actor) error {
	panic("unexpected call")
}
func (x *MockCluster) GetActor(context.Context, string) (*internalpb.Actor, error) {
	panic("unexpected call")
}
func (x *MockCluster) RemoveActor(context.Context, string) error { panic("unexpected call") }
func (x *MockCluster) ActorExists(context.Context, string) (bool, error) {
	panic("unexpected call")
}
func (x *MockCluster) Actors(context.Context, time.Duration) ([]*internalpb.Actor, error) {
	panic("unexpected call")
}
func (x *MockCluster) PutGrain(ctx context.Context, grain *internalpb.Grain) error {
	x.putGrainCalls++
	if x.putGrainFn != nil {
		return x.putGrainFn(ctx, grain)
	}
	return nil
}

func (x *MockCluster) PutKind(ctx context.Context, kind string) error {
	x.putKindCalls++
	if x.putKindFn != nil {
		return x.putKindFn(ctx, kind)
	}
	return nil
}

func (x *MockCluster) LookupKind(ctx context.Context, kind string) (string, error) {
	x.lookupKindCalls++
	if x.lookupKindFn != nil {
		return x.lookupKindFn(ctx, kind)
	}
	return "", nil
}

func (x *MockCluster) GetGrain(context.Context, string) (*internalpb.Grain, error) {
	panic("unexpected call")
}
func (x *MockCluster) RemoveGrain(context.Context, string) error { panic("unexpected call") }
func (x *MockCluster) GrainExists(ctx context.Context, identity string) (bool, error) {
	x.grainExistsCalls++
	if x.grainExistsFn != nil {
		return x.grainExistsFn(ctx, identity)
	}
	return false, nil
}
func (x *MockCluster) Grains(context.Context, time.Duration) ([]*internalpb.Grain, error) {
	panic("unexpected call")
}

func (x *MockCluster) RemoveKind(context.Context, string) error        { panic("unexpected call") }
func (x *MockCluster) Events() <-chan *Event                           { panic("unexpected call") }
func (x *MockCluster) Peers(context.Context) ([]*Peer, error)          { panic("unexpected call") }
func (x *MockCluster) IsLeader(context.Context) bool                   { panic("unexpected call") }
func (x *MockCluster) GetPartition(string) uint64                      { panic("unexpected call") }
func (x *MockCluster) IsRunning() bool                                 { panic("unexpected call") }
func (x *MockCluster) PutJobKey(context.Context, string, []byte) error { panic("unexpected call") }
func (x *MockCluster) DeleteJobKey(context.Context, string) error      { panic("unexpected call") }
func (x *MockCluster) JobKey(context.Context, string) ([]byte, error)  { panic("unexpected call") }
func (x *MockCluster) Members(context.Context) ([]*Peer, error)        { panic("unexpected call") }
func (x *MockCluster) NextRoundRobinValue(context.Context, string) (int, error) {
	panic("unexpected call")
}

func newEventTestCluster(host string, port int) *cluster {
	return &cluster{
		node:                    &discovery.Node{Host: host, PeersPort: port},
		events:                  make(chan *Event, defaultEventsBufSize),
		nodeJoinedEventsFilter:  goset.NewSet[string](),
		nodeLeftEventsFilter:    goset.NewSet[string](),
		nodeJoinTimestamps:      make(map[string]int64),
		nodeLeftTimestamps:      make(map[string]int64),
		rebalanceJoinNodeEpochs: make(map[string]uint64),
		rebalanceLeftNodeEpochs: make(map[string]uint64),
		rebalanceStartSeen:      make(map[uint64]struct{}),
		rebalanceCompleteSeen:   make(map[uint64]struct{}),
		logger:                  log.DiscardLogger,
	}
}

type MockOlricClient struct {
	*MockClient
	members []olric.Member
}

// nolint
func (x *MockOlricClient) Members(ctx context.Context) ([]olric.Member, error) {
	if x.MockClient != nil && x.MockClient.membersErr != nil {
		return nil, x.MockClient.membersErr
	}
	return x.members, nil
}

type iteratorStub struct {
	keys    []string
	index   int
	closeFn func()
}

func (i *iteratorStub) Next() bool {
	if i.index < len(i.keys) {
		i.index++
		return true
	}
	return false
}

func (i *iteratorStub) Key() string {
	if i.index == 0 || i.index > len(i.keys) {
		return ""
	}
	return i.keys[i.index-1]
}

func (i *iteratorStub) Close() {
	if i.closeFn != nil {
		i.closeFn()
	}
}

type testEntry struct {
	key        string
	value      []byte
	ttl        int64
	timestamp  int64
	lastAccess int64
}

func (e *testEntry) SetKey(key string) { e.key = key }

func (e *testEntry) Key() string { return e.key }

func (e *testEntry) SetValue(value []byte) { e.value = append([]byte(nil), value...) }

func (e *testEntry) Value() []byte { return append([]byte(nil), e.value...) }

func (e *testEntry) SetTTL(ttl int64) { e.ttl = ttl }

func (e *testEntry) TTL() int64 { return e.ttl }

func (e *testEntry) SetTimestamp(ts int64) { e.timestamp = ts }

func (e *testEntry) Timestamp() int64 { return e.timestamp }

func (e *testEntry) SetLastAccess(ts int64) { e.lastAccess = ts }

func (e *testEntry) LastAccess() int64 { return e.lastAccess }

func (e *testEntry) Encode() []byte { return e.Value() }

func (e *testEntry) Decode(data []byte) { e.SetValue(data) }

func newGetResponseWithValue(value []byte) *olric.GetResponse {
	entry := &testEntry{}
	entry.SetValue(value)
	return newGetResponse(entry, 0)
}

func newGetResponse(entry storage.Entry, partition uint64) *olric.GetResponse {
	resp := &olric.GetResponse{}
	rv := reflect.ValueOf(resp).Elem()

	entryField := rv.FieldByName("entry")
	reflect.NewAt(entryField.Type(), unsafe.Pointer(entryField.UnsafeAddr())).Elem().Set(reflect.ValueOf(entry))

	partitionField := rv.FieldByName("partition")
	reflect.NewAt(partitionField.Type(), unsafe.Pointer(partitionField.UnsafeAddr())).Elem().SetUint(partition)

	return resp
}
