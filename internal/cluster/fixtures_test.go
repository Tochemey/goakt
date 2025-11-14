/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
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
	"github.com/tochemey/goakt/v3/log"
)

type MockClient struct {
	newDMapErr   error
	newPubSubErr error
	membersErr   error
}

// nolint
func (f *MockClient) NewDMap(name string, options ...olric.DMapOption) (olric.DMap, error) {
	if f.newDMapErr != nil {
		return nil, f.newDMapErr
	}
	return &MockDMap{}, nil
}

// nolint
func (f *MockClient) NewPubSub(options ...olric.PubSubOption) (*olric.PubSub, error) {
	if f.newPubSubErr != nil {
		return nil, f.newPubSubErr
	}
	panic("unexpected call to NewPubSub without error")
}

// nolint
func (f *MockClient) Stats(ctx context.Context, address string, options ...olric.StatsOption) (stats.Stats, error) {
	return stats.Stats{}, nil
}

// nolint
func (f *MockClient) Ping(ctx context.Context, address, message string) (string, error) {
	return "", nil
}

// nolint
func (f *MockClient) RoutingTable(ctx context.Context) (olric.RoutingTable, error) {
	return nil, nil
}

// nolint
func (f *MockClient) Members(ctx context.Context) ([]olric.Member, error) {
	if f.membersErr != nil {
		return nil, f.membersErr
	}
	panic("unexpected call to Members without error")
}

// nolint
func (f *MockClient) RefreshMetadata(ctx context.Context) error {
	return nil
}

// nolint
func (f *MockClient) Close(ctx context.Context) error {
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

func (f *MockDMap) Name() string { return "fake-dmap" }

// nolint
func (f *MockDMap) Put(ctx context.Context, key string, value any, options ...olric.PutOption) error {
	if f.putFn != nil {
		return f.putFn(ctx, key, value, options...)
	}
	if f.putErr != nil {
		return f.putErr
	}
	return nil
}

// nolint
func (f *MockDMap) Get(ctx context.Context, key string) (*olric.GetResponse, error) {
	if f.getFn != nil {
		return f.getFn(ctx, key)
	}
	panic("unexpected call to Get")
}

// nolint
func (f *MockDMap) Delete(ctx context.Context, keys ...string) (int, error) {
	if f.deleteFn != nil {
		return f.deleteFn(ctx, keys...)
	}
	panic("unexpected call to Delete")
}

// nolint
func (f *MockDMap) Incr(ctx context.Context, key string, delta int) (int, error) {
	if f.incrFn != nil {
		return f.incrFn(ctx, key, delta)
	}
	if f.incrErr != nil {
		return 0, f.incrErr
	}
	panic("unexpected call to Incr")
}

// nolint
func (f *MockDMap) Decr(ctx context.Context, key string, delta int) (int, error) {
	panic("unexpected call to Decr")
}

// nolint
func (f *MockDMap) GetPut(ctx context.Context, key string, value any) (*olric.GetResponse, error) {
	panic("unexpected call to GetPut")
}

// nolint
func (f *MockDMap) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	panic("unexpected call to IncrByFloat")
}

// nolint
func (f *MockDMap) Expire(ctx context.Context, key string, timeout time.Duration) error {
	panic("unexpected call to Expire")
}

// nolint
func (f *MockDMap) Lock(ctx context.Context, key string, deadline time.Duration) (olric.LockContext, error) {
	panic("unexpected call to Lock")
}

// nolint
func (f *MockDMap) LockWithTimeout(ctx context.Context, key string, timeout, deadline time.Duration) (olric.LockContext, error) {
	panic("unexpected call to LockWithTimeout")
}

// nolint
func (f *MockDMap) Scan(ctx context.Context, options ...olric.ScanOption) (olric.Iterator, error) {
	if f.scanFn != nil {
		return f.scanFn(ctx, options...)
	}
	panic("unexpected call to Scan")
}

// nolint
func (f *MockDMap) Destroy(ctx context.Context) error {
	panic("unexpected call to Destroy")
}

// nolint
func (f *MockDMap) Pipeline(opts ...olric.PipelineOption) (*olric.DMapPipeline, error) {
	panic("unexpected call to Pipeline")
}

func newEventTestCluster(host string, port int) *cluster {
	return &cluster{
		node:                   &discovery.Node{Host: host, PeersPort: port},
		events:                 make(chan *Event, defaultEventsBufSize),
		nodeJoinedEventsFilter: goset.NewSet[string](),
		nodeLeftEventsFilter:   goset.NewSet[string](),
		logger:                 log.DiscardLogger,
	}
}

type fakeClient struct {
	*MockClient
	members []olric.Member
}

// nolint
func (f *fakeClient) Members(ctx context.Context) ([]olric.Member, error) {
	if f.MockClient != nil && f.MockClient.membersErr != nil {
		return nil, f.MockClient.membersErr
	}
	return f.members, nil
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
