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
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/tochemey/goakt/v4/datacenter"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/codec"
	"github.com/tochemey/goakt/v4/internal/validation"
)

const recordSuffix = ".datacenterrecord"

// ControlPlane is a NATS JetStream KeyValue-backed implementation of datacenter.ControlPlane.
//
// It uses the legacy NATS KeyValue API (nc.JetStream().CreateKeyValue) and stores
// datacenter records in a JetStream KV bucket. Bucket TTL is used for liveness;
// each Put or Update resets the key's age. Version is the KV revision for OCC.
type ControlPlane struct {
	config *Config
	conn   *nats.Conn
	kv     nats.KeyValue

	// encodeFunc allows overriding encoding for testing error paths
	encodeFunc func(datacenter.DataCenterRecord) ([]byte, error)
}

// Ensure ControlPlane implements datacenter.ControlPlane.
var _ datacenter.ControlPlane = (*ControlPlane)(nil)

// NewControlPlane creates a new ControlPlane backed by NATS JetStream KeyValue.
//
// It validates the config, connects to the NATS server, and ensures the KV bucket
// exists with the configured TTL.
func NewControlPlane(config *Config) (*ControlPlane, error) {
	return newControlPlane(config, nil, nil)
}

// newControlPlane is the internal constructor that accepts optional overrides for testing.
func newControlPlane(config *Config, conn *nats.Conn, kv nats.KeyValue) (*ControlPlane, error) {
	if config == nil {
		return nil, errors.New("controlplane/nats: config is nil")
	}

	config.Sanitize()
	if err := config.Validate(); err != nil {
		return nil, err
	}

	// If connection and KV are provided, use them directly (for testing)
	if conn != nil && kv != nil {
		return &ControlPlane{
			config: config,
			conn:   conn,
			kv:     kv,
		}, nil
	}

	var err error
	conn, err = nats.Connect(config.URL, nats.Timeout(config.ConnectTimeout))
	if err != nil {
		return nil, fmt.Errorf("controlplane/nats: connect: %w", err)
	}

	js, err := conn.JetStream()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("controlplane/nats: jetstream: %w", err)
	}

	// Try to get existing bucket first, then create if it doesn't exist.
	// This avoids errors when reconnecting or when multiple instances start concurrently.
	kv, err = js.KeyValue(config.Bucket)
	if err != nil {
		// Bucket doesn't exist, create it
		kvCfg := &nats.KeyValueConfig{
			Bucket: config.Bucket,
			TTL:    config.TTL,
		}

		kv, err = js.CreateKeyValue(kvCfg)
		if err != nil {
			// Handle race condition: another instance may have created the bucket
			if errors.Is(err, nats.ErrStreamNameAlreadyInUse) {
				kv, err = js.KeyValue(config.Bucket)
			}

			if err != nil {
				conn.Close()
				return nil, fmt.Errorf("controlplane/nats: create bucket: %w", err)
			}
		}
	}

	return &ControlPlane{
		config: config,
		conn:   conn,
		kv:     kv,
	}, nil
}

// Close releases the NATS connection. Close is idempotent.
func (c *ControlPlane) Close() error {
	if c.conn == nil {
		return nil
	}
	c.conn.Close()
	c.conn = nil
	return nil
}

// Register creates or updates a datacenter record and returns its id and version.
//
// If record.Version is 0, Register behaves like create-or-update with OCC against the
// current stored revision. If record.Version > 0, Update is used for OCC; on mismatch,
// gerrors.ErrRecordConflict is returned.
func (c *ControlPlane) Register(_ context.Context, record datacenter.DataCenterRecord) (string, uint64, error) {
	if err := c.validateRecord(record); err != nil {
		return "", 0, err
	}

	if record.State == "" {
		record.State = datacenter.DataCenterRegistered
	}

	if !isValidState(record.State) {
		return "", 0, fmt.Errorf("controlplane/nats: invalid state %q", record.State)
	}

	record.LeaseExpiry = time.Now().Add(c.config.TTL)
	payload, err := c.encode(record)
	if err != nil {
		return "", 0, err
	}

	key := recordKey(record.ID)

	if record.Version == 0 {
		existing, err := c.kv.Get(key)
		if err != nil && !errors.Is(err, nats.ErrKeyNotFound) && !errors.Is(err, nats.ErrKeyDeleted) {
			return "", 0, fmt.Errorf("controlplane/nats: get: %w", err)
		}

		if err != nil || existing == nil {
			rev, putErr := c.kv.Put(key, payload)
			if putErr != nil {
				if errors.Is(putErr, nats.ErrKeyExists) {
					return "", 0, gerrors.ErrDataCenterRecordConflict
				}
				return "", 0, fmt.Errorf("controlplane/nats: put: %w", putErr)
			}
			return record.ID, rev, nil
		}

		rev, err := c.kv.Update(key, payload, existing.Revision())
		if err != nil {
			if isRevisionConflict(err) {
				return "", 0, gerrors.ErrDataCenterRecordConflict
			}
			return "", 0, fmt.Errorf("controlplane/nats: update: %w", err)
		}
		return record.ID, rev, nil
	}

	rev, err := c.kv.Update(key, payload, record.Version)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) {
			return "", 0, gerrors.ErrDataCenterRecordNotFound
		}

		if isRevisionConflict(err) {
			return "", 0, gerrors.ErrDataCenterRecordConflict
		}

		return "", 0, fmt.Errorf("controlplane/nats: update: %w", err)
	}
	return record.ID, rev, nil
}

// Heartbeat refreshes the record's TTL and returns the new version and lease expiry.
func (c *ControlPlane) Heartbeat(_ context.Context, id string, version uint64) (uint64, time.Time, error) {
	if strings.TrimSpace(id) == "" {
		return 0, time.Time{}, fmt.Errorf("controlplane/nats: id is required")
	}

	entry, err := c.getRecordEntry(id)
	if err != nil {
		return 0, time.Time{}, err
	}

	if entry.Revision() != version {
		return 0, time.Time{}, gerrors.ErrDataCenterRecordConflict
	}

	record, err := codec.DecodeDataCenterRecord(entry.Value())
	if err != nil {
		return 0, time.Time{}, err
	}

	record.LeaseExpiry = time.Now().Add(c.config.TTL)
	payload, err := c.encode(record)
	if err != nil {
		return 0, time.Time{}, err
	}

	key := recordKey(id)
	rev, err := c.kv.Update(key, payload, version)
	if err != nil {
		if isRevisionConflict(err) {
			return 0, time.Time{}, gerrors.ErrDataCenterRecordConflict
		}
		return 0, time.Time{}, fmt.Errorf("controlplane/nats: heartbeat update: %w", err)
	}
	return rev, record.LeaseExpiry, nil
}

// SetState updates the record state at the given version.
func (c *ControlPlane) SetState(_ context.Context, id string, state datacenter.DataCenterState, version uint64) (uint64, error) {
	if strings.TrimSpace(id) == "" {
		return 0, fmt.Errorf("controlplane/nats: id is required")
	}

	if !isValidState(state) {
		return 0, fmt.Errorf("controlplane/nats: invalid state %q", state)
	}

	entry, err := c.getRecordEntry(id)
	if err != nil {
		return 0, err
	}

	if entry.Revision() != version {
		return 0, gerrors.ErrDataCenterRecordConflict
	}

	record, err := codec.DecodeDataCenterRecord(entry.Value())
	if err != nil {
		return 0, err
	}

	record.State = state
	payload, err := c.encode(record)
	if err != nil {
		return 0, err
	}

	key := recordKey(id)
	rev, err := c.kv.Update(key, payload, version)
	if err != nil {
		if isRevisionConflict(err) {
			return 0, gerrors.ErrDataCenterRecordConflict
		}
		return 0, fmt.Errorf("controlplane/nats: set state: %w", err)
	}
	return rev, nil
}

// ListActive returns records that are DataCenterActive and whose LeaseExpiry is in the future.
func (c *ControlPlane) ListActive(context.Context) ([]datacenter.DataCenterRecord, error) {
	lister, err := c.kv.ListKeys()
	if err != nil {
		return nil, fmt.Errorf("controlplane/nats: list keys: %w", err)
	}

	defer func() { _ = lister.Stop() }()

	var records []datacenter.DataCenterRecord
	now := time.Now()
	for key := range lister.Keys() {
		if !isRecordKey(key) {
			continue
		}

		entry, err := c.kv.Get(key)
		if err != nil {
			if errors.Is(err, nats.ErrKeyNotFound) || errors.Is(err, nats.ErrKeyDeleted) {
				continue
			}
			return nil, err
		}

		if entry.Operation() != nats.KeyValuePut {
			continue
		}

		record, err := codec.DecodeDataCenterRecord(entry.Value())
		if err != nil {
			return nil, err
		}

		if record.State != datacenter.DataCenterActive {
			continue
		}

		if !record.LeaseExpiry.After(now) {
			continue
		}

		record.Version = entry.Revision()
		records = append(records, record)
	}

	for err := range lister.Error() {
		if err != nil {
			return nil, err
		}
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].Version < records[j].Version
	})
	return records, nil
}

// Watch returns a channel of control-plane events (UPSERT/DELETE) until ctx is done.
func (c *ControlPlane) Watch(ctx context.Context) (<-chan datacenter.ControlPlaneEvent, error) {
	opts := []nats.WatchOpt{nats.Context(ctx)}
	watcher, err := c.kv.Watch(nats.AllKeys, opts...)
	if err != nil {
		return nil, fmt.Errorf("controlplane/nats: watch: %w", err)
	}

	events := make(chan datacenter.ControlPlaneEvent)
	go func() {
		defer close(events)
		defer func() { _ = watcher.Stop() }()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			entry, ok := <-watcher.Updates()
			if !ok {
				return
			}
			if entry == nil {
				continue
			}
			if !isRecordKey(entry.Key()) {
				continue
			}
			ev, ok := c.toControlPlaneEvent(entry)
			if !ok {
				continue
			}
			select {
			case events <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, nil
}

// Deregister explicitly removes the datacenter record from the control plane.
//
// This method deletes the record from the NATS KV bucket immediately rather than
// waiting for TTL expiry. If the record does not exist, nil is returned (idempotent).
func (c *ControlPlane) Deregister(_ context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("controlplane/nats: id is required")
	}

	key := recordKey(id)
	err := c.kv.Delete(key)
	if err != nil {
		// Treat not found as success (idempotent)
		if errors.Is(err, nats.ErrKeyNotFound) || errors.Is(err, nats.ErrKeyDeleted) {
			return nil
		}
		return fmt.Errorf("controlplane/nats: failed to deregister record: %w", err)
	}

	return nil
}

func (*ControlPlane) toControlPlaneEvent(entry nats.KeyValueEntry) (datacenter.ControlPlaneEvent, bool) {
	switch entry.Operation() {
	case nats.KeyValuePut:
		record, err := codec.DecodeDataCenterRecord(entry.Value())
		if err != nil {
			return datacenter.ControlPlaneEvent{}, false
		}
		record.Version = entry.Revision()
		return datacenter.ControlPlaneEvent{
			Type:   datacenter.ControlPlaneEventUpsert,
			Record: record,
		}, true
	case nats.KeyValueDelete, nats.KeyValuePurge:
		id, _ := parseRecordID(entry.Key())
		r := datacenter.DataCenterRecord{ID: id}
		if v := entry.Value(); len(v) > 0 {
			if dec, err := codec.DecodeDataCenterRecord(v); err == nil {
				r = dec
				r.Version = entry.Revision()
			}
		}
		return datacenter.ControlPlaneEvent{
			Type:   datacenter.ControlPlaneEventDelete,
			Record: r,
		}, true
	default:
		return datacenter.ControlPlaneEvent{}, false
	}
}

func (*ControlPlane) validateRecord(record datacenter.DataCenterRecord) error {
	return validation.New(validation.FailFast()).
		AddValidator(validation.NewEmptyStringValidator("ID", record.ID)).
		AddValidator(validation.NewEmptyStringValidator("DataCenter.Name", record.DataCenter.Name)).
		AddAssertion(len(record.Endpoints) > 0, "Endpoints must not be empty").
		Validate()
}

func (c *ControlPlane) encode(record datacenter.DataCenterRecord) ([]byte, error) {
	if c.encodeFunc != nil {
		return c.encodeFunc(record)
	}
	return codec.EncodeDataCenterRecord(record)
}

func (c *ControlPlane) getRecordEntry(id string) (nats.KeyValueEntry, error) {
	key := recordKey(id)
	entry, err := c.kv.Get(key)
	if err != nil {
		if errors.Is(err, nats.ErrKeyNotFound) || errors.Is(err, nats.ErrKeyDeleted) {
			return nil, gerrors.ErrDataCenterRecordNotFound
		}
		return nil, fmt.Errorf("controlplane/nats: get: %w", err)
	}
	return entry, nil
}

func recordKey(id string) string {
	return id + recordSuffix
}

func isRecordKey(key string) bool {
	return strings.HasSuffix(key, recordSuffix)
}

func parseRecordID(key string) (string, bool) {
	if !strings.HasSuffix(key, recordSuffix) {
		return "", false
	}
	id := strings.TrimSuffix(key, recordSuffix)
	if id == "" {
		return "", false
	}
	return id, true
}

func isValidState(state datacenter.DataCenterState) bool {
	switch state {
	case datacenter.DataCenterRegistered, datacenter.DataCenterActive, datacenter.DataCenterDraining, datacenter.DataCenterInactive:
		return true
	default:
		return false
	}
}

// isRevisionConflict returns true when the NATS error indicates a revision/sequence mismatch (OCC failure).
func isRevisionConflict(err error) bool {
	if errors.Is(err, nats.ErrKeyExists) {
		return true
	}
	var apiErr *nats.APIError
	if errors.As(err, &apiErr) && apiErr != nil && apiErr.ErrorCode == nats.JSErrCodeStreamWrongLastSequence {
		return true
	}
	return false
}
