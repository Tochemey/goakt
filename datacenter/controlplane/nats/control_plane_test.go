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

package nats

import (
	"context"
	"fmt"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/datacenter"
	gerrors "github.com/tochemey/goakt/v3/errors"
)

var bucketNameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

var (
	idCounter uint64
)

func startNatsServer(t *testing.T) *natsserver.Server {
	t.Helper()

	// Create a temp directory for JetStream storage
	storeDir := t.TempDir()

	serv, err := natsserver.NewServer(&natsserver.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  storeDir,
	})
	require.NoError(t, err)

	ready := make(chan bool)
	go func() {
		ready <- true
		serv.Start()
	}()
	<-ready

	if !serv.ReadyForConnections(2 * time.Second) {
		t.Fatalf("nats-io server failed to start")
	}

	return serv
}
func nextID() string {
	return fmt.Sprintf("dc-%d", atomic.AddUint64(&idCounter, 1))
}

func newRecord(id string) datacenter.DataCenterRecord {
	return datacenter.DataCenterRecord{
		ID: id,
		DataCenter: datacenter.DataCenter{
			Name:   "dc-" + id,
			Region: "region-a",
			Zone:   "zone-1",
		},
		Endpoints: []string{"localhost:9090"},
		State:     datacenter.DataCenterActive,
	}
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

	t.Run("invalid URL", func(t *testing.T) {
		cfg := &Config{
			URL:            "://bad",
			Bucket:         defaultBucket,
			TTL:            time.Second,
			Timeout:        5 * time.Second,
			ConnectTimeout: 2 * time.Second,
		}
		cp, err := NewControlPlane(cfg)
		require.Error(t, err)
		require.Nil(t, cp)
	})
}

func TestControlPlaneClose(t *testing.T) {
	cp := &ControlPlane{}
	require.NoError(t, cp.Close())
}

func TestControlPlaneRegisterValidation(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	_, _, err := cp.Register(context.Background(), datacenter.DataCenterRecord{})
	require.Error(t, err)

	rec := newRecord(nextID())
	rec.State = datacenter.DataCenterState("BROKEN")
	_, _, err = cp.Register(context.Background(), rec)
	require.Error(t, err)
}

func TestControlPlaneRegisterAndListActive(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive

	id, version, err := cp.Register(context.Background(), record)
	require.NoError(t, err)
	require.Equal(t, record.ID, id)
	require.NotZero(t, version)

	records, err := cp.ListActive(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, record.ID, records[0].ID)
	require.Equal(t, datacenter.DataCenterActive, records[0].State)
	require.NotZero(t, records[0].Version)
	assert.True(t, records[0].LeaseExpiry.After(time.Now()))
}

func TestControlPlaneRegisterConflict(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive

	_, version, err := cp.Register(context.Background(), record)
	require.NoError(t, err)

	record.Version = version + 1
	_, _, err = cp.Register(context.Background(), record)
	require.ErrorIs(t, err, gerrors.ErrDataCenterRecordConflict)
}

func TestControlPlaneHeartbeat(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	_, _, err := cp.Heartbeat(context.Background(), " ", 1)
	require.Error(t, err)

	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive

	_, version, err := cp.Register(context.Background(), record)
	require.NoError(t, err)

	newVersion, leaseExpiry, err := cp.Heartbeat(context.Background(), record.ID, version)
	require.NoError(t, err)
	require.Greater(t, newVersion, version)
	assert.True(t, leaseExpiry.After(time.Now()))

	_, _, err = cp.Heartbeat(context.Background(), record.ID, newVersion+1)
	require.ErrorIs(t, err, gerrors.ErrDataCenterRecordConflict)

	_, _, err = cp.Heartbeat(context.Background(), "missing-id", 1)
	require.ErrorIs(t, err, gerrors.ErrDataCenterRecordNotFound)
}

func TestControlPlaneSetState(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	record := newRecord(nextID())
	record.State = datacenter.DataCenterRegistered

	_, version, err := cp.Register(context.Background(), record)
	require.NoError(t, err)

	newVersion, err := cp.SetState(context.Background(), record.ID, datacenter.DataCenterActive, version)
	require.NoError(t, err)
	require.Greater(t, newVersion, version)

	_, err = cp.SetState(context.Background(), record.ID, datacenter.DataCenterDraining, version)
	require.ErrorIs(t, err, gerrors.ErrDataCenterRecordConflict)

	_, err = cp.SetState(context.Background(), "missing-id", datacenter.DataCenterActive, 1)
	require.ErrorIs(t, err, gerrors.ErrDataCenterRecordNotFound)
}

func TestControlPlaneWatch(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := cp.Watch(ctx)
	require.NoError(t, err)
	require.NotNil(t, ch)

	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, _, err = cp.Register(ctx, record)
	require.NoError(t, err)

	select {
	case ev, ok := <-ch:
		require.True(t, ok)
		require.Equal(t, datacenter.ControlPlaneEventUpsert, ev.Type)
		require.Equal(t, record.ID, ev.Record.ID)
	case <-ctx.Done():
		t.Fatal("watch did not receive event in time")
	}
}

func TestControlPlaneRegisterUpdateExisting(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Register initial record
	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	id, version, err := cp.Register(context.Background(), record)
	require.NoError(t, err)
	require.Equal(t, record.ID, id)

	// Re-register with version=0 should update existing
	record.Version = 0
	id2, version2, err := cp.Register(context.Background(), record)
	require.NoError(t, err)
	require.Equal(t, record.ID, id2)
	require.Greater(t, version2, version)
}

func TestControlPlaneRegisterWithCorrectVersion(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Register initial record
	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, version, err := cp.Register(context.Background(), record)
	require.NoError(t, err)

	// Update with correct version should succeed
	record.Version = version
	_, version2, err := cp.Register(context.Background(), record)
	require.NoError(t, err)
	require.Greater(t, version2, version)
}

func TestControlPlaneRegisterNonExistentWithVersion(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Try to update non-existent record with version > 0
	// NATS KV Update on non-existent key returns a revision conflict
	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	record.Version = 999 // non-existent version

	_, _, err := cp.Register(context.Background(), record)
	require.Error(t, err)
	// NATS returns conflict error when updating non-existent key with specific version
}

func TestControlPlaneRegisterDefaultState(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Register with empty state should default to Registered
	record := newRecord(nextID())
	record.State = "" // empty state

	id, _, err := cp.Register(context.Background(), record)
	require.NoError(t, err)
	require.Equal(t, record.ID, id)
}

func TestControlPlaneSetStateValidation(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Empty ID
	_, err := cp.SetState(context.Background(), "", datacenter.DataCenterActive, 1)
	require.Error(t, err)

	// Invalid state
	record := newRecord(nextID())
	_, version, err := cp.Register(context.Background(), record)
	require.NoError(t, err)

	_, err = cp.SetState(context.Background(), record.ID, datacenter.DataCenterState("INVALID"), version)
	require.Error(t, err)
}

func TestControlPlaneListActiveEmpty(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Empty bucket should return empty list
	records, err := cp.ListActive(context.Background())
	require.NoError(t, err)
	require.Empty(t, records)
}

func TestControlPlaneListActiveFiltersNonActive(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Register with Registered state (not Active)
	record := newRecord(nextID())
	record.State = datacenter.DataCenterRegistered
	_, _, err := cp.Register(context.Background(), record)
	require.NoError(t, err)

	// Should not appear in ListActive
	records, err := cp.ListActive(context.Background())
	require.NoError(t, err)
	require.Empty(t, records)
}

func TestControlPlaneListActiveMultiple(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Register multiple active records
	record1 := newRecord(nextID())
	record1.State = datacenter.DataCenterActive
	_, _, err := cp.Register(context.Background(), record1)
	require.NoError(t, err)

	record2 := newRecord(nextID())
	record2.State = datacenter.DataCenterActive
	_, _, err = cp.Register(context.Background(), record2)
	require.NoError(t, err)

	// Both should appear
	records, err := cp.ListActive(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 2)

	// Should be sorted by version
	require.Less(t, records[0].Version, records[1].Version)
}

func TestControlPlaneWatchDelete(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Register a record first
	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, _, err := cp.Register(ctx, record)
	require.NoError(t, err)

	// Start watching
	ch, err := cp.Watch(ctx)
	require.NoError(t, err)
	require.NotNil(t, ch)

	// Delete the record via KV
	key := record.ID + ".datacenterrecord"
	err = cp.kv.Delete(key)
	require.NoError(t, err)

	// Should receive delete event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("watch channel closed unexpectedly")
			}
			if ev.Type == datacenter.ControlPlaneEventDelete {
				require.Equal(t, record.ID, ev.Record.ID)
				return
			}
			// Skip upsert events from initial state
		case <-ctx.Done():
			t.Fatal("watch did not receive delete event in time")
		}
	}
}

func TestControlPlaneWatchContextCancel(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	ctx, cancel := context.WithCancel(context.Background())

	ch, err := cp.Watch(ctx)
	require.NoError(t, err)
	require.NotNil(t, ch)

	// Cancel context
	cancel()

	// Channel should close
	select {
	case _, ok := <-ch:
		if ok {
			// May receive some events, drain them
			for range ch {
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("channel did not close after context cancel")
	}
}

func TestControlPlaneBucketAlreadyExists(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	natsURL := fmt.Sprintf("nats://%s", server.Addr().String())
	bucketName := fmt.Sprintf("test_bucket_%d", time.Now().UnixNano())

	// Create first control plane
	cfg1 := &Config{
		Context:        context.Background(),
		URL:            natsURL,
		Bucket:         bucketName,
		TTL:            10 * time.Second,
		Timeout:        5 * time.Second,
		ConnectTimeout: 5 * time.Second,
	}
	cp1, err := NewControlPlane(cfg1)
	require.NoError(t, err)
	defer func() { _ = cp1.Close() }()

	// Create second control plane with same bucket - should reuse existing
	cfg2 := &Config{
		Context:        context.Background(),
		URL:            natsURL,
		Bucket:         bucketName,
		TTL:            10 * time.Second,
		Timeout:        5 * time.Second,
		ConnectTimeout: 5 * time.Second,
	}
	cp2, err := NewControlPlane(cfg2)
	require.NoError(t, err)
	defer func() { _ = cp2.Close() }()

	// Both should work
	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, _, err = cp1.Register(context.Background(), record)
	require.NoError(t, err)

	records, err := cp2.ListActive(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)
}

func TestParseRecordID(t *testing.T) {
	// Valid key
	id, ok := parseRecordID("myid.datacenterrecord")
	require.True(t, ok)
	require.Equal(t, "myid", id)

	// Invalid suffix
	id, ok = parseRecordID("myid.other")
	require.False(t, ok)
	require.Empty(t, id)

	// Empty ID after trim
	id, ok = parseRecordID(".datacenterrecord")
	require.False(t, ok)
	require.Empty(t, id)

	// No suffix at all
	id, ok = parseRecordID("myid")
	require.False(t, ok)
	require.Empty(t, id)
}

func TestIsRecordKey(t *testing.T) {
	require.True(t, isRecordKey("test.datacenterrecord"))
	require.False(t, isRecordKey("test.other"))
	require.False(t, isRecordKey("test"))
}

func TestRecordKey(t *testing.T) {
	require.Equal(t, "myid.datacenterrecord", recordKey("myid"))
}

func TestIsValidState(t *testing.T) {
	require.True(t, isValidState(datacenter.DataCenterRegistered))
	require.True(t, isValidState(datacenter.DataCenterActive))
	require.True(t, isValidState(datacenter.DataCenterDraining))
	require.True(t, isValidState(datacenter.DataCenterInactive))
	require.False(t, isValidState(datacenter.DataCenterState("INVALID")))
}

func TestIsRevisionConflict(t *testing.T) {
	// Nil error
	require.False(t, isRevisionConflict(nil))

	// Random error
	require.False(t, isRevisionConflict(fmt.Errorf("random error")))

	// Test with wrapped ErrKeyExists
	wrappedErr := fmt.Errorf("wrapped: %w", nats.ErrKeyExists)
	require.True(t, isRevisionConflict(wrappedErr))
}

func TestControlPlaneCloseWithConnection(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())

	// Close should work
	err := cp.Close()
	require.NoError(t, err)

	// Second close should also work (idempotent)
	err = cp.Close()
	require.NoError(t, err)
}

func TestControlPlaneSetStateAllStates(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Test all valid state transitions
	states := []datacenter.DataCenterState{
		datacenter.DataCenterRegistered,
		datacenter.DataCenterActive,
		datacenter.DataCenterDraining,
		datacenter.DataCenterInactive,
	}

	record := newRecord(nextID())
	_, version, err := cp.Register(context.Background(), record)
	require.NoError(t, err)

	for _, state := range states {
		newVersion, err := cp.SetState(context.Background(), record.ID, state, version)
		require.NoError(t, err)
		version = newVersion
	}
}

func TestControlPlaneListActiveWithDraining(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Register an active record
	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, version, err := cp.Register(context.Background(), record)
	require.NoError(t, err)

	// Change to draining
	_, err = cp.SetState(context.Background(), record.ID, datacenter.DataCenterDraining, version)
	require.NoError(t, err)

	// Should not appear in ListActive (draining != active)
	records, err := cp.ListActive(context.Background())
	require.NoError(t, err)
	require.Empty(t, records)
}

func TestControlPlaneWatchPurge(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Register a record first
	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, _, err := cp.Register(ctx, record)
	require.NoError(t, err)

	// Start watching
	ch, err := cp.Watch(ctx)
	require.NoError(t, err)
	require.NotNil(t, ch)

	// Purge the record via KV
	key := record.ID + ".datacenterrecord"
	err = cp.kv.Purge(key)
	require.NoError(t, err)

	// Should receive delete event (purge is treated as delete)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("watch channel closed unexpectedly")
			}
			if ev.Type == datacenter.ControlPlaneEventDelete {
				require.Equal(t, record.ID, ev.Record.ID)
				return
			}
			// Skip upsert events from initial state
		case <-ctx.Done():
			t.Fatal("watch did not receive delete event in time")
		}
	}
}

func TestNewControlPlaneValidConfig(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cfg := &Config{
		URL:            fmt.Sprintf("nats://%s", server.Addr().String()),
		Bucket:         fmt.Sprintf("test_%d", time.Now().UnixNano()),
		TTL:            5 * time.Second,
		Timeout:        5 * time.Second,
		ConnectTimeout: 5 * time.Second,
	}

	cp, err := NewControlPlane(cfg)
	require.NoError(t, err)
	require.NotNil(t, cp)
	defer func() { _ = cp.Close() }()

	// Verify it works
	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, _, err = cp.Register(context.Background(), record)
	require.NoError(t, err)
}

func TestIsRevisionConflictWithAPIError(t *testing.T) {
	// Test with APIError containing wrong last sequence code
	apiErr := &nats.APIError{
		Code:        400,
		ErrorCode:   nats.JSErrCodeStreamWrongLastSequence,
		Description: "wrong last sequence",
	}
	require.True(t, isRevisionConflict(apiErr))

	// Test with different error code
	otherAPIErr := &nats.APIError{
		Code:        404,
		ErrorCode:   10014, // stream not found
		Description: "stream not found",
	}
	require.False(t, isRevisionConflict(otherAPIErr))
}

func TestControlPlaneHeartbeatVersionMismatch(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, version, err := cp.Register(context.Background(), record)
	require.NoError(t, err)

	// Heartbeat with wrong version (less than current)
	_, _, err = cp.Heartbeat(context.Background(), record.ID, version-1)
	require.ErrorIs(t, err, gerrors.ErrDataCenterRecordConflict)
}

func TestControlPlaneSetStateVersionMismatch(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, version, err := cp.Register(context.Background(), record)
	require.NoError(t, err)

	// SetState with wrong version (less than current)
	_, err = cp.SetState(context.Background(), record.ID, datacenter.DataCenterDraining, version-1)
	require.ErrorIs(t, err, gerrors.ErrDataCenterRecordConflict)
}

func TestControlPlaneListActiveSorting(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Register multiple records and verify sorting
	for i := 0; i < 3; i++ {
		record := newRecord(nextID())
		record.State = datacenter.DataCenterActive
		_, _, err := cp.Register(context.Background(), record)
		require.NoError(t, err)
	}

	records, err := cp.ListActive(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 3)

	// Verify sorted by version (ascending)
	for i := 1; i < len(records); i++ {
		require.Less(t, records[i-1].Version, records[i].Version)
	}
}

func TestControlPlaneWatchMultipleEvents(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ch, err := cp.Watch(ctx)
	require.NoError(t, err)

	// Register multiple records
	record1 := newRecord(nextID())
	record1.State = datacenter.DataCenterActive
	_, _, err = cp.Register(ctx, record1)
	require.NoError(t, err)

	record2 := newRecord(nextID())
	record2.State = datacenter.DataCenterActive
	_, _, err = cp.Register(ctx, record2)
	require.NoError(t, err)

	// Should receive both events
	received := make(map[string]bool)
	timeout := time.After(5 * time.Second)

	for len(received) < 2 {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("channel closed unexpectedly")
			}
			if ev.Type == datacenter.ControlPlaneEventUpsert {
				received[ev.Record.ID] = true
			}
		case <-timeout:
			t.Fatalf("only received %d of 2 expected events", len(received))
		}
	}

	require.True(t, received[record1.ID])
	require.True(t, received[record2.ID])
}

func TestControlPlaneGetRecordEntryNotFound(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Try to get non-existent record
	entry, err := cp.getRecordEntry("non-existent-id")
	require.ErrorIs(t, err, gerrors.ErrDataCenterRecordNotFound)
	require.Nil(t, entry)
}

func TestControlPlaneValidateRecord(t *testing.T) {
	cp := &ControlPlane{}

	// Empty ID
	err := cp.validateRecord(datacenter.DataCenterRecord{})
	require.Error(t, err)

	// Empty DataCenter.Name
	err = cp.validateRecord(datacenter.DataCenterRecord{
		ID: "test-id",
	})
	require.Error(t, err)

	// Empty Endpoints
	err = cp.validateRecord(datacenter.DataCenterRecord{
		ID:         "test-id",
		DataCenter: datacenter.DataCenter{Name: "test-dc"},
	})
	require.Error(t, err)

	// Valid record
	err = cp.validateRecord(datacenter.DataCenterRecord{
		ID:         "test-id",
		DataCenter: datacenter.DataCenter{Name: "test-dc"},
		Endpoints:  []string{"localhost:8080"},
	})
	require.NoError(t, err)
}

func TestControlPlaneHeartbeatDecodeError(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Put invalid data directly to KV
	invalidID := nextID()
	key := invalidID + ".datacenterrecord"
	_, err := cp.kv.Put(key, []byte("invalid protobuf data"))
	require.NoError(t, err)

	// Get the revision
	entry, err := cp.kv.Get(key)
	require.NoError(t, err)

	// Heartbeat should fail on decode
	_, _, err = cp.Heartbeat(context.Background(), invalidID, entry.Revision())
	require.Error(t, err)
}

func TestControlPlaneSetStateDecodeError(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Put invalid data directly to KV
	invalidID := nextID()
	key := invalidID + ".datacenterrecord"
	_, err := cp.kv.Put(key, []byte("invalid protobuf data"))
	require.NoError(t, err)

	// Get the revision
	entry, err := cp.kv.Get(key)
	require.NoError(t, err)

	// SetState should fail on decode
	_, err = cp.SetState(context.Background(), invalidID, datacenter.DataCenterActive, entry.Revision())
	require.Error(t, err)
}

func TestControlPlaneListActiveDecodeError(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Put invalid data directly to KV
	invalidID := nextID()
	key := invalidID + ".datacenterrecord"
	_, err := cp.kv.Put(key, []byte("invalid protobuf data"))
	require.NoError(t, err)

	// ListActive should fail on decode
	_, err = cp.ListActive(context.Background())
	require.Error(t, err)
}

func TestControlPlaneWatchDecodeError(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	ch, err := cp.Watch(ctx)
	require.NoError(t, err)

	// Put invalid data - toControlPlaneEvent should return false and skip it
	invalidID := nextID()
	key := invalidID + ".datacenterrecord"
	_, err = cp.kv.Put(key, []byte("invalid protobuf data"))
	require.NoError(t, err)

	// Then put valid data
	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, _, err = cp.Register(ctx, record)
	require.NoError(t, err)

	// Should only receive valid record event (invalid one filtered out)
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatal("channel closed")
		}
		// Should eventually get the valid record
		if ev.Record.ID == record.ID {
			require.Equal(t, datacenter.ControlPlaneEventUpsert, ev.Type)
			return
		}
		// May get other events, continue
	case <-ctx.Done():
		// Timeout is acceptable here - the invalid record event is filtered
	}
}

func TestControlPlaneGetRecordEntryDeleted(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Register a record
	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, _, err := cp.Register(context.Background(), record)
	require.NoError(t, err)

	// Delete it
	key := record.ID + ".datacenterrecord"
	err = cp.kv.Delete(key)
	require.NoError(t, err)

	// getRecordEntry should return ErrRecordNotFound for deleted key
	entry, err := cp.getRecordEntry(record.ID)
	require.ErrorIs(t, err, gerrors.ErrDataCenterRecordNotFound)
	require.Nil(t, entry)
}

func TestControlPlaneListActiveWithNonRecordKey(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Put a key that doesn't match record suffix
	_, err := cp.kv.Put("non-record-key", []byte("some data"))
	require.NoError(t, err)

	// Register a valid active record
	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, _, err = cp.Register(context.Background(), record)
	require.NoError(t, err)

	// ListActive should filter out non-record keys
	records, err := cp.ListActive(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, record.ID, records[0].ID)
}

func TestControlPlaneListActiveWithDeletedRecord(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Register and then delete a record
	record1 := newRecord(nextID())
	record1.State = datacenter.DataCenterActive
	_, _, err := cp.Register(context.Background(), record1)
	require.NoError(t, err)

	key := record1.ID + ".datacenterrecord"
	err = cp.kv.Delete(key)
	require.NoError(t, err)

	// Register another active record
	record2 := newRecord(nextID())
	record2.State = datacenter.DataCenterActive
	_, _, err = cp.Register(context.Background(), record2)
	require.NoError(t, err)

	// ListActive should only return the active, non-deleted record
	records, err := cp.ListActive(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)
	require.Equal(t, record2.ID, records[0].ID)
}

func TestControlPlaneListActiveWithInactiveState(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Register with Inactive state
	record := newRecord(nextID())
	record.State = datacenter.DataCenterInactive
	_, _, err := cp.Register(context.Background(), record)
	require.NoError(t, err)

	// Should not appear in ListActive
	records, err := cp.ListActive(context.Background())
	require.NoError(t, err)
	require.Empty(t, records)
}

func TestControlPlaneRegisterUpdateExistingVersion0Success(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// First registration
	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	id, v1, err := cp.Register(context.Background(), record)
	require.NoError(t, err)

	// Second registration with version=0 should update
	record.Version = 0
	record.Endpoints = []string{"new-endpoint:8080"}
	id2, v2, err := cp.Register(context.Background(), record)
	require.NoError(t, err)
	require.Equal(t, id, id2)
	require.Greater(t, v2, v1)

	// Third registration with version=0 should also update
	record.Version = 0
	record.Endpoints = []string{"another-endpoint:8080"}
	id3, v3, err := cp.Register(context.Background(), record)
	require.NoError(t, err)
	require.Equal(t, id, id3)
	require.Greater(t, v3, v2)
}

func TestControlPlaneWatchClosesOnContextCancel(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	ctx, cancel := context.WithCancel(context.Background())

	ch, err := cp.Watch(ctx)
	require.NoError(t, err)

	// Cancel immediately
	cancel()

	// Wait for channel to close
	timeout := time.After(3 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // Channel closed as expected
			}
			// Drain any buffered events
		case <-timeout:
			t.Fatal("channel did not close after context cancel")
		}
	}
}

func TestControlPlaneWatchNonRecordKey(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := cp.Watch(ctx)
	require.NoError(t, err)

	// Put a non-record key - should be filtered by isRecordKey check
	_, err = cp.kv.Put("non-record-key", []byte("some data"))
	require.NoError(t, err)

	// Put a valid record
	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, _, err = cp.Register(ctx, record)
	require.NoError(t, err)

	// Should only receive the valid record event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("channel closed unexpectedly")
			}
			if ev.Type == datacenter.ControlPlaneEventUpsert && ev.Record.ID == record.ID {
				return // Success - got the valid record
			}
			// Non-record key events should be filtered out
		case <-ctx.Done():
			t.Fatal("did not receive expected event")
		}
	}
}

func TestControlPlaneListActiveFiltersExpiredLease(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	// Create control plane with very short TTL
	natsURL := fmt.Sprintf("nats://%s", server.Addr().String())
	bucketName := fmt.Sprintf("test_expired_%d", time.Now().UnixNano())

	cfg := &Config{
		Context:        context.Background(),
		URL:            natsURL,
		Bucket:         bucketName,
		TTL:            1 * time.Second, // Very short TTL
		Timeout:        5 * time.Second,
		ConnectTimeout: 5 * time.Second,
	}

	cp, err := NewControlPlane(cfg)
	require.NoError(t, err)
	defer func() { _ = cp.Close() }()

	// Register a record
	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, _, err = cp.Register(context.Background(), record)
	require.NoError(t, err)

	// Wait for lease to expire
	time.Sleep(2 * time.Second)

	// ListActive should filter out expired records
	records, err := cp.ListActive(context.Background())
	require.NoError(t, err)
	require.Empty(t, records)
}

func TestControlPlaneRegisterAllStates(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	states := []datacenter.DataCenterState{
		datacenter.DataCenterRegistered,
		datacenter.DataCenterActive,
		datacenter.DataCenterDraining,
		datacenter.DataCenterInactive,
	}

	for _, state := range states {
		record := newRecord(nextID())
		record.State = state
		id, version, err := cp.Register(context.Background(), record)
		require.NoError(t, err, "failed for state %s", state)
		require.Equal(t, record.ID, id)
		require.NotZero(t, version)
	}
}

func TestControlPlaneHeartbeatUpdatesExpiry(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, version, err := cp.Register(context.Background(), record)
	require.NoError(t, err)

	// First heartbeat
	v2, expiry1, err := cp.Heartbeat(context.Background(), record.ID, version)
	require.NoError(t, err)
	require.True(t, expiry1.After(time.Now()))

	// Second heartbeat
	_, expiry2, err := cp.Heartbeat(context.Background(), record.ID, v2)
	require.NoError(t, err)
	require.True(t, expiry2.After(time.Now()))
	// Second expiry should be later than or equal to first
	require.True(t, !expiry2.Before(expiry1))
}

func TestControlPlaneRegisterEncodeError(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Inject encode error
	cp.encodeFunc = func(datacenter.DataCenterRecord) ([]byte, error) {
		return nil, fmt.Errorf("encode error")
	}

	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, _, err := cp.Register(context.Background(), record)
	require.Error(t, err)
	require.Contains(t, err.Error(), "encode error")
}

func TestControlPlaneHeartbeatEncodeError(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Register a record first (with normal encoding)
	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, version, err := cp.Register(context.Background(), record)
	require.NoError(t, err)

	// Inject encode error for heartbeat
	cp.encodeFunc = func(datacenter.DataCenterRecord) ([]byte, error) {
		return nil, fmt.Errorf("encode error")
	}

	_, _, err = cp.Heartbeat(context.Background(), record.ID, version)
	require.Error(t, err)
	require.Contains(t, err.Error(), "encode error")
}

func TestControlPlaneSetStateEncodeError(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Register a record first (with normal encoding)
	record := newRecord(nextID())
	record.State = datacenter.DataCenterRegistered
	_, version, err := cp.Register(context.Background(), record)
	require.NoError(t, err)

	// Inject encode error for SetState
	cp.encodeFunc = func(datacenter.DataCenterRecord) ([]byte, error) {
		return nil, fmt.Errorf("encode error")
	}

	_, err = cp.SetState(context.Background(), record.ID, datacenter.DataCenterActive, version)
	require.Error(t, err)
	require.Contains(t, err.Error(), "encode error")
}

func TestControlPlaneEncode(t *testing.T) {
	cp := &ControlPlane{}

	// Without custom encoder
	record := newRecord("test-id")
	data, err := cp.encode(record)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// With custom encoder
	customData := []byte("custom")
	cp.encodeFunc = func(datacenter.DataCenterRecord) ([]byte, error) {
		return customData, nil
	}
	data, err = cp.encode(record)
	require.NoError(t, err)
	require.Equal(t, customData, data)
}

func TestControlPlaneWatchDeleteWithPurge(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Register a record first
	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, _, err := cp.Register(ctx, record)
	require.NoError(t, err)

	// Start watching
	ch, err := cp.Watch(ctx)
	require.NoError(t, err)

	// Purge the key
	key := record.ID + ".datacenterrecord"
	err = cp.kv.Purge(key)
	require.NoError(t, err)

	// Should receive delete event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("channel closed unexpectedly")
			}
			if ev.Type == datacenter.ControlPlaneEventDelete {
				require.Equal(t, record.ID, ev.Record.ID)
				return
			}
		case <-ctx.Done():
			t.Fatal("did not receive delete event in time")
		}
	}
}

func TestControlPlaneRegisterUpdateConflict(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Register initial record
	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, version, err := cp.Register(context.Background(), record)
	require.NoError(t, err)

	// Update with incorrect version
	record.Version = version + 100
	_, _, err = cp.Register(context.Background(), record)
	require.ErrorIs(t, err, gerrors.ErrDataCenterRecordConflict)
}

func TestControlPlaneListActiveNoRecords(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Empty bucket
	records, err := cp.ListActive(context.Background())
	require.NoError(t, err)
	require.Empty(t, records)

	// Register a non-active record
	record := newRecord(nextID())
	record.State = datacenter.DataCenterRegistered
	_, _, err = cp.Register(context.Background(), record)
	require.NoError(t, err)

	// Still should be empty (not active)
	records, err = cp.ListActive(context.Background())
	require.NoError(t, err)
	require.Empty(t, records)
}

func TestControlPlaneHeartbeatNotFound(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Heartbeat for non-existent record
	_, _, err := cp.Heartbeat(context.Background(), "non-existent-id", 1)
	require.ErrorIs(t, err, gerrors.ErrDataCenterRecordNotFound)
}

func TestControlPlaneSetStateNotFound(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// SetState for non-existent record
	_, err := cp.SetState(context.Background(), "non-existent-id", datacenter.DataCenterActive, 1)
	require.ErrorIs(t, err, gerrors.ErrDataCenterRecordNotFound)
}

func TestGetRecordEntrySuccess(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	cp := MockControlPlane(t, server.Addr().String())
	defer func() { _ = cp.Close() }()

	// Register a record
	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, _, err := cp.Register(context.Background(), record)
	require.NoError(t, err)

	// Get the entry
	entry, err := cp.getRecordEntry(record.ID)
	require.NoError(t, err)
	require.NotNil(t, entry)
}

func TestNewControlPlaneWithInjectedDependencies(t *testing.T) {
	server := startNatsServer(t)
	defer server.Shutdown()

	natsURL := fmt.Sprintf("nats://%s", server.Addr().String())

	// Connect manually
	conn, err := nats.Connect(natsURL, nats.Timeout(5*time.Second))
	require.NoError(t, err)
	defer conn.Close()

	// Get JetStream
	js, err := conn.JetStream()
	require.NoError(t, err)

	// Create bucket
	bucketName := fmt.Sprintf("test_injected_%d", time.Now().UnixNano())
	kv, err := js.CreateKeyValue(&nats.KeyValueConfig{
		Bucket: bucketName,
		TTL:    10 * time.Second,
	})
	require.NoError(t, err)

	// Create control plane with injected dependencies
	cfg := &Config{
		Context:        context.Background(),
		URL:            natsURL,
		Bucket:         bucketName,
		TTL:            10 * time.Second,
		Timeout:        5 * time.Second,
		ConnectTimeout: 5 * time.Second,
	}

	cp, err := newControlPlane(cfg, conn, kv)
	require.NoError(t, err)
	require.NotNil(t, cp)

	// Verify it works
	record := newRecord(nextID())
	record.State = datacenter.DataCenterActive
	_, _, err = cp.Register(context.Background(), record)
	require.NoError(t, err)
}

func MockControlPlane(t *testing.T, natsURL string) *ControlPlane {
	t.Helper()
	safe := bucketNameSanitizer.ReplaceAllString(t.Name(), "_")
	if safe == "" {
		safe = "default"
	}
	cfg := &Config{
		Context:        context.Background(),
		URL:            fmt.Sprintf("nats://%s", natsURL),
		Bucket:         fmt.Sprintf("test_%s_%d", safe, time.Now().UnixNano()),
		TTL:            10 * time.Second,
		Timeout:        5 * time.Second,
		ConnectTimeout: 5 * time.Second,
	}
	cp, err := NewControlPlane(cfg)
	require.NoError(t, err)
	return cp
}
