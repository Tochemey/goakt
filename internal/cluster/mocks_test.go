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
	"time"

	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/olric"
	"github.com/tochemey/olric/stats"
)

// MockContext is pinned to the context.Context contract so a signature drift fails the build.
var (
	_ context.Context = (*MockContext)(nil)
)

// MockContext is an inert context that never carries a deadline, a cancellation or a value.
type MockContext struct{}

// Deadline reports that no deadline is set.
func (x *MockContext) Deadline() (deadline time.Time, ok bool) {
	return time.Time{}, false
}

// Done returns a nil channel, so the context never completes.
func (x *MockContext) Done() <-chan struct{} {
	return nil
}

// Err reports that the context is still live.
func (x *MockContext) Err() error {
	return nil
}

// Value never resolves a key.
func (x *MockContext) Value(key any) any {
	return nil
}

// MockInitialSyncer is an initialSyncer double that returns injected errors instead of running a real sync.
type MockInitialSyncer struct {
	syncErr     error // error returned by WaitForInitialSync
	shutdownErr error // error returned by Shutdown
	shutdowns   int   // number of Shutdown calls received
}

// WaitForInitialSync returns the injected sync error.
func (x *MockInitialSyncer) WaitForInitialSync(context.Context) error { return x.syncErr }

// Shutdown counts the call and returns the injected shutdown error.
func (x *MockInitialSyncer) Shutdown(context.Context) error {
	x.shutdowns++
	return x.shutdownErr
}

// MockFailingGetDMap is a DMap wrapper that reports one key as missing and delegates every other read.
type MockFailingGetDMap struct {
	olric.DMap
	key string // key whose lookup fails with olric.ErrKeyNotFound
}

// Get fails for the configured key and forwards any other key to the wrapped DMap.
func (x MockFailingGetDMap) Get(ctx context.Context, key string) (*olric.GetResponse, error) {
	if key == x.key {
		return nil, olric.ErrKeyNotFound
	}
	return x.DMap.Get(ctx, key)
}

// MockClient is an Olric client double that returns injected errors and panics on unexpected calls.
type MockClient struct {
	newDMapErr   error // error returned by NewDMap
	newPubSubErr error // error returned by NewPubSub
	membersErr   error // error returned by Members
}

// NewDMap returns the injected error, or an empty MockDMap when none is set.
func (x *MockClient) NewDMap(name string, options ...olric.DMapOption) (olric.DMap, error) {
	if x.newDMapErr != nil {
		return nil, x.newDMapErr
	}
	return &MockDMap{}, nil
}

// NewPubSub returns the injected error and panics when none is set.
func (x *MockClient) NewPubSub(options ...olric.PubSubOption) (*olric.PubSub, error) {
	if x.newPubSubErr != nil {
		return nil, x.newPubSubErr
	}
	panic("unexpected call to NewPubSub without error")
}

// Stats returns empty statistics.
func (x *MockClient) Stats(ctx context.Context, address string, options ...olric.StatsOption) (stats.Stats, error) {
	return stats.Stats{}, nil
}

// Ping returns an empty reply.
func (x *MockClient) Ping(ctx context.Context, address, message string) (string, error) {
	return "", nil
}

// RoutingTable returns no routing table.
func (x *MockClient) RoutingTable(ctx context.Context) (olric.RoutingTable, error) {
	return nil, nil
}

// Members returns the injected error and panics when none is set.
func (x *MockClient) Members(ctx context.Context) ([]olric.Member, error) {
	if x.membersErr != nil {
		return nil, x.membersErr
	}
	panic("unexpected call to Members without error")
}

// RefreshMetadata does nothing.
func (x *MockClient) RefreshMetadata(ctx context.Context) error {
	return nil
}

// Close does nothing.
func (x *MockClient) Close(ctx context.Context) error {
	return nil
}

// MockDMap is a distributed map double whose calls are served by injected hooks and errors, and panic otherwise.
type MockDMap struct {
	putErr   error                                                                                             // fallback error for Put
	putFn    func(ctx context.Context, key string, value any, options ...olric.PutOption) error                // hook serving Put
	getFn    func(ctx context.Context, key string) (*olric.GetResponse, error)                                 // hook serving Get
	scanFn   func(ctx context.Context, options ...olric.ScanOption) (olric.Iterator, error)                    // hook serving Scan
	deleteFn func(ctx context.Context, keys ...string) (int, error)                                            // hook serving Delete
	incrFn   func(ctx context.Context, key string, delta int) (int, error)                                     // hook serving Incr
	incrErr  error                                                                                             // fallback error for Incr
	lockFn   func(ctx context.Context, key string, timeout, deadline time.Duration) (olric.LockContext, error) // hook serving LockWithTimeout
}

// Name returns the fixed name of the map.
func (x *MockDMap) Name() string { return "fake-dmap" }

// Put calls putFn when set, otherwise returns the injected error.
func (x *MockDMap) Put(ctx context.Context, key string, value any, options ...olric.PutOption) error {
	if x.putFn != nil {
		return x.putFn(ctx, key, value, options...)
	}
	if x.putErr != nil {
		return x.putErr
	}
	return nil
}

// Get calls getFn and panics when it is unset.
func (x *MockDMap) Get(ctx context.Context, key string) (*olric.GetResponse, error) {
	if x.getFn != nil {
		return x.getFn(ctx, key)
	}
	panic("unexpected call to Get")
}

// Delete calls deleteFn and panics when it is unset.
func (x *MockDMap) Delete(ctx context.Context, keys ...string) (int, error) {
	if x.deleteFn != nil {
		return x.deleteFn(ctx, keys...)
	}
	panic("unexpected call to Delete")
}

// Incr calls incrFn when set, returns the injected error when it is not, and panics when neither is set.
func (x *MockDMap) Incr(ctx context.Context, key string, delta int) (int, error) {
	if x.incrFn != nil {
		return x.incrFn(ctx, key, delta)
	}
	if x.incrErr != nil {
		return 0, x.incrErr
	}
	panic("unexpected call to Incr")
}

// Decr panics because no test exercises it.
func (x *MockDMap) Decr(ctx context.Context, key string, delta int) (int, error) {
	panic("unexpected call to Decr")
}

// GetPut panics because no test exercises it.
func (x *MockDMap) GetPut(ctx context.Context, key string, value any) (*olric.GetResponse, error) {
	panic("unexpected call to GetPut")
}

// IncrByFloat panics because no test exercises it.
func (x *MockDMap) IncrByFloat(ctx context.Context, key string, delta float64) (float64, error) {
	panic("unexpected call to IncrByFloat")
}

// Expire panics because no test exercises it.
func (x *MockDMap) Expire(ctx context.Context, key string, timeout time.Duration) error {
	panic("unexpected call to Expire")
}

// Lock panics because no test exercises it.
func (x *MockDMap) Lock(ctx context.Context, key string, deadline time.Duration) (olric.LockContext, error) {
	panic("unexpected call to Lock")
}

// LockWithTimeout calls lockFn and panics when it is unset.
func (x *MockDMap) LockWithTimeout(ctx context.Context, key string, timeout, deadline time.Duration) (olric.LockContext, error) {
	if x.lockFn != nil {
		return x.lockFn(ctx, key, timeout, deadline)
	}
	panic("unexpected call to LockWithTimeout")
}

// MockLockContext is a LockContext double whose Unlock returns the injected error.
type MockLockContext struct {
	unlockErr error // error returned by Unlock
	unlocks   int   // number of Unlock calls received
}

// Unlock counts the call and returns the injected error.
func (x *MockLockContext) Unlock(context.Context) error {
	x.unlocks++
	return x.unlockErr
}

// Lease panics because no test exercises it.
func (x *MockLockContext) Lease(context.Context, time.Duration) error {
	panic("unexpected call to Lease")
}

// Scan calls scanFn and panics when it is unset.
func (x *MockDMap) Scan(ctx context.Context, options ...olric.ScanOption) (olric.Iterator, error) {
	if x.scanFn != nil {
		return x.scanFn(ctx, options...)
	}
	panic("unexpected call to Scan")
}

// Destroy panics because no test exercises it.
func (x *MockDMap) Destroy(ctx context.Context) error {
	panic("unexpected call to Destroy")
}

// Pipeline panics because no test exercises it.
func (x *MockDMap) Pipeline(opts ...olric.PipelineOption) (*olric.DMapPipeline, error) {
	panic("unexpected call to Pipeline")
}

// MockCluster is a Cluster double that serves the grain calls through injected hooks and panics on the rest.
type MockCluster struct {
	grainExistsFn    func(context.Context, string) (bool, error)    // hook serving GrainExists
	putGrainFn       func(context.Context, *internalpb.Grain) error // hook serving PutGrain
	grainExistsCalls int                                            // number of GrainExists calls received
	putGrainCalls    int                                            // number of PutGrain calls received
}

// Start panics because no test exercises it.
func (x *MockCluster) Start(context.Context) error { panic("unexpected call") }

// Stop panics because no test exercises it.
func (x *MockCluster) Stop(context.Context) error { panic("unexpected call") }

// PutActor panics because no test exercises it.
func (x *MockCluster) PutActor(context.Context, *internalpb.Actor) error {
	panic("unexpected call")
}

// PutActorIfAbsent panics because no test exercises it.
func (x *MockCluster) PutActorIfAbsent(context.Context, *internalpb.Actor) error {
	panic("unexpected call")
}

// GetActor panics because no test exercises it.
func (x *MockCluster) GetActor(context.Context, string) (*internalpb.Actor, error) {
	panic("unexpected call")
}

// RemoveActor panics because no test exercises it.
func (x *MockCluster) RemoveActor(context.Context, string) error { panic("unexpected call") }

// ActorExists panics because no test exercises it.
func (x *MockCluster) ActorExists(context.Context, string) (bool, error) {
	panic("unexpected call")
}

// Actors panics because no test exercises it.
func (x *MockCluster) Actors(context.Context, time.Duration) ([]*internalpb.Actor, error) {
	panic("unexpected call")
}

// ActorsByHost panics because no test exercises it.
func (x *MockCluster) ActorsByHost(context.Context, string, int, time.Duration) ([]*internalpb.Actor, error) {
	panic("unexpected call")
}

// CountActorsByHost panics because no test exercises it.
func (x *MockCluster) CountActorsByHost(context.Context, time.Duration) (map[string]int, error) {
	panic("unexpected call")
}

// PutGrain counts the call and delegates to putGrainFn when set.
func (x *MockCluster) PutGrain(ctx context.Context, grain *internalpb.Grain) error {
	x.putGrainCalls++
	if x.putGrainFn != nil {
		return x.putGrainFn(ctx, grain)
	}
	return nil
}

// GetGrain panics because no test exercises it.
func (x *MockCluster) GetGrain(context.Context, string) (*internalpb.Grain, error) {
	panic("unexpected call")
}

// ReleaseGrain panics because no test exercises it.
func (x *MockCluster) ReleaseGrain(context.Context, string, string) (*internalpb.Grain, error) {
	panic("unexpected call")
}

// GrainExists counts the call and delegates to grainExistsFn when set, otherwise reports the grain missing.
func (x *MockCluster) GrainExists(ctx context.Context, identity string) (bool, error) {
	x.grainExistsCalls++
	if x.grainExistsFn != nil {
		return x.grainExistsFn(ctx, identity)
	}
	return false, nil
}

// Grains panics because no test exercises it.
func (x *MockCluster) Grains(context.Context, time.Duration) ([]*internalpb.Grain, error) {
	panic("unexpected call")
}

// GrainsByHost panics because no test exercises it.
func (x *MockCluster) GrainsByHost(context.Context, string, int, time.Duration) ([]*internalpb.Grain, error) {
	panic("unexpected call")
}

// Events panics because no test exercises it.
func (x *MockCluster) Events() <-chan *Event { panic("unexpected call") }

// Peers panics because no test exercises it.
func (x *MockCluster) Peers(context.Context) ([]*Peer, error) { panic("unexpected call") }

// IsLeader panics because no test exercises it.
func (x *MockCluster) IsLeader(context.Context) bool { panic("unexpected call") }

// GetPartition panics because no test exercises it.
func (x *MockCluster) GetPartition(string) uint64 { panic("unexpected call") }

// IsRunning panics because no test exercises it.
func (x *MockCluster) IsRunning() bool { panic("unexpected call") }

// LastRebalanceEvent panics because no test exercises it.
func (x *MockCluster) LastRebalanceEvent() time.Time { panic("unexpected call") }

// ClaimScheduleFire panics because no test exercises it.
func (x *MockCluster) ClaimScheduleFire(context.Context, string, time.Duration) error {
	panic("unexpected call")
}

// PutJobKey panics because no test exercises it.
func (x *MockCluster) PutJobKey(context.Context, string, []byte) error { panic("unexpected call") }

// DeleteJobKey panics because no test exercises it.
func (x *MockCluster) DeleteJobKey(context.Context, string) error { panic("unexpected call") }

// JobKey panics because no test exercises it.
func (x *MockCluster) JobKey(context.Context, string) ([]byte, error) { panic("unexpected call") }

// Members panics because no test exercises it.
func (x *MockCluster) Members(context.Context) ([]*Peer, error) { panic("unexpected call") }

// NextRoundRobinValue panics because no test exercises it.
func (x *MockCluster) NextRoundRobinValue(context.Context, string) (int, error) {
	panic("unexpected call")
}

// MockMembersClient is a MockClient whose Members call answers from a fixed membership list.
type MockMembersClient struct {
	*MockClient
	members []olric.Member // membership returned when the embedded client has no injected error
}

// Members returns the embedded client's injected error when set, otherwise the fixed membership list.
func (x *MockMembersClient) Members(ctx context.Context) ([]olric.Member, error) {
	if x.MockClient != nil && x.MockClient.membersErr != nil {
		return nil, x.MockClient.membersErr
	}
	return x.members, nil
}

// MockIterator is a scan iterator double that walks a fixed list of keys.
type MockIterator struct {
	keys    []string
	index   int    // position just past the key Next stopped on
	closeFn func() // hook invoked by Close
}

// Next advances to the following key and reports whether one was left.
func (x *MockIterator) Next() bool {
	if x.index < len(x.keys) {
		x.index++
		return true
	}
	return false
}

// Key returns the key Next stopped on, or an empty string outside the list.
func (x *MockIterator) Key() string {
	if x.index == 0 || x.index > len(x.keys) {
		return ""
	}
	return x.keys[x.index-1]
}

// Close invokes closeFn when set.
func (x *MockIterator) Close() {
	if x.closeFn != nil {
		x.closeFn()
	}
}

// MockEntry is a storage entry double that holds the key, the value and the timestamps in memory.
type MockEntry struct {
	key        string
	value      []byte
	ttl        int64
	timestamp  int64
	lastAccess int64
}

// SetKey stores the key.
func (x *MockEntry) SetKey(key string) { x.key = key }

// Key returns the stored key.
func (x *MockEntry) Key() string { return x.key }

// SetValue stores a copy of the value.
func (x *MockEntry) SetValue(value []byte) { x.value = append([]byte(nil), value...) }

// Value returns a copy of the stored value.
func (x *MockEntry) Value() []byte { return append([]byte(nil), x.value...) }

// SetTTL stores the time to live.
func (x *MockEntry) SetTTL(ttl int64) { x.ttl = ttl }

// TTL returns the stored time to live.
func (x *MockEntry) TTL() int64 { return x.ttl }

// SetTimestamp stores the write timestamp.
func (x *MockEntry) SetTimestamp(ts int64) { x.timestamp = ts }

// Timestamp returns the stored write timestamp.
func (x *MockEntry) Timestamp() int64 { return x.timestamp }

// SetLastAccess stores the last access timestamp.
func (x *MockEntry) SetLastAccess(ts int64) { x.lastAccess = ts }

// LastAccess returns the stored last access timestamp.
func (x *MockEntry) LastAccess() int64 { return x.lastAccess }

// Encode returns a copy of the stored value.
func (x *MockEntry) Encode() []byte { return x.Value() }

// Decode stores a copy of the given bytes as the value.
func (x *MockEntry) Decode(data []byte) { x.SetValue(data) }
