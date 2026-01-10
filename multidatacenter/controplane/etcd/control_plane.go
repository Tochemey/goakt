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
	"sort"
	"strings"
	"time"

	"go.etcd.io/etcd/api/v3/mvccpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/namespace"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/validation"
	"github.com/tochemey/goakt/v3/multidatacenter"
)

const recordSuffix = "/record"

// ControlPlane is an etcd-backed implementation of multidc.ControlPlane.
//
// It stores datacenter records under the configured namespace, using an etcd
// lease to model liveness. Each successful write returns an etcd revision that
// is used as the record "version" for optimistic concurrency control (OCC).
//
// Unless otherwise stated by the called method, any provided context is wrapped
// with the configured per-operation timeout.
type ControlPlane struct {
	config           *Config
	client           *clientv3.Client
	kv               clientv3.KV
	lease            clientv3.Lease
	watcher          clientv3.Watcher
	clientFunc       func(clientv3.Config) (*clientv3.Client, error)
	closeFunc        func(*clientv3.Client) error
	encodeRecordFunc func(multidatacenter.DataCenterRecord) ([]byte, error)
}

// Ensure ControlPlane implements multidc.ControlPlane.
var _ multidatacenter.ControlPlane = (*ControlPlane)(nil)

// NewControlPlane creates a new ControlPlane backed by etcd.
//
// It validates the provided configuration, connects to the first configured
// endpoint, and applies the configured namespace to all keys.
func NewControlPlane(config *Config) (*ControlPlane, error) {
	return newControlPlane(config, clientv3.New, func(client *clientv3.Client) error { return client.Close() })
}

func newControlPlane(config *Config, clientFunc func(clientv3.Config) (*clientv3.Client, error), closeFunc func(*clientv3.Client) error) (*ControlPlane, error) {
	if config == nil {
		return nil, errors.New("multidc/etcd: config is nil")
	}

	config.Sanitize()
	if err := config.Validate(); err != nil {
		return nil, err
	}

	if clientFunc == nil {
		clientFunc = clientv3.New
	}

	if closeFunc == nil {
		closeFunc = func(client *clientv3.Client) error { return client.Close() }
	}

	client, err := clientFunc(clientv3.Config{
		Endpoints:   config.Endpoints,
		DialTimeout: config.DialTimeout,
		TLS:         config.TLS,
		Username:    config.Username,
		Password:    config.Password,
		Context:     config.Context,
	})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(config.Context, config.DialTimeout)
	defer cancel()

	if _, err = client.Status(ctx, config.Endpoints[0]); err != nil {
		if cerr := closeFunc(client); cerr != nil {
			return nil, errors.Join(err, fmt.Errorf("failed to close etcd client: %w", cerr))
		}
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}

	namespacePrefix := normalizeNamespace(config.Namespace)
	return &ControlPlane{
		config:           config,
		client:           client,
		kv:               namespace.NewKV(client.KV, namespacePrefix),
		lease:            namespace.NewLease(client.Lease, namespacePrefix),
		watcher:          namespace.NewWatcher(client.Watcher, namespacePrefix),
		clientFunc:       clientFunc,
		closeFunc:        closeFunc,
		encodeRecordFunc: encodeRecord,
	}, nil
}

// Close releases resources held by the ControlPlane, including the underlying
// etcd client. Close is idempotent.
func (c *ControlPlane) Close() error {
	if c.client == nil {
		return nil
	}
	if c.closeFunc != nil {
		return c.closeFunc(c.client)
	}
	return c.client.Close()
}

// Register writes (or conditionally updates) a datacenter record and returns its
// record ID and new version.
//
// Register attaches an etcd lease to the record to represent liveness. The
// returned version is the etcd revision of the successful write.
//
// Concurrency semantics:
//   - If record.Version is 0, Register behaves like a "create-or-update" with OCC
//     against the current stored revision.
//   - If record.Version is > 0, Register succeeds only if the stored record's
//     ModRevision matches record.Version.
//   - On version mismatch, gerrors.ErrRecordConflict is returned.
func (c *ControlPlane) Register(ctx context.Context, record multidatacenter.DataCenterRecord) (string, uint64, error) {
	if err := c.validateRecord(record); err != nil {
		return "", 0, err
	}

	if record.State == "" {
		record.State = multidatacenter.DataCenterRegistered
	}

	if !isValidState(record.State) {
		return "", 0, fmt.Errorf("multidc/etcd: invalid state %q", record.State)
	}

	opCtx, cancel := c.withTimeout(ctx)
	defer cancel()

	leaseResp, err := c.lease.Grant(opCtx, c.leaseTTLSeconds())
	if err != nil {
		return "", 0, fmt.Errorf("multidc/etcd: failed to create lease: %w", err)
	}

	record.LeaseExpiry = time.Now().Add(time.Duration(leaseResp.TTL) * time.Second)
	payload, err := c.encode(record)
	if err != nil {
		return "", 0, err
	}

	key := recordKey(record.ID)
	cmp, err := c.registerCompare(opCtx, key, record.Version)
	if err != nil {
		return "", 0, err
	}

	txnResp, err := c.kv.Txn(opCtx).
		If(cmp).
		Then(clientv3.OpPut(key, string(payload), clientv3.WithLease(leaseResp.ID))).
		Commit()
	if err != nil {
		return "", 0, fmt.Errorf("multidc/etcd: failed to register record: %w", err)
	}

	if !txnResp.Succeeded {
		return "", 0, gerrors.ErrRecordConflict
	}

	return record.ID, uint64(txnResp.Header.Revision), nil
}

// Heartbeat renews the lease for the given record and returns the new version
// and the computed lease expiry time.
//
// Heartbeat enforces OCC using the supplied version. If the stored record's
// ModRevision does not match version, gerrors.ErrRecordConflict is returned.
// If the record has no associated lease, an error is returned.
func (c *ControlPlane) Heartbeat(ctx context.Context, id string, version uint64) (uint64, time.Time, error) {
	if strings.TrimSpace(id) == "" {
		return 0, time.Time{}, fmt.Errorf("multidc/etcd: id is required")
	}

	opCtx, cancel := c.withTimeout(ctx)
	defer cancel()

	kv, err := c.getRecordKV(opCtx, id)
	if err != nil {
		return 0, time.Time{}, err
	}

	if uint64(kv.ModRevision) != version {
		return 0, time.Time{}, gerrors.ErrRecordConflict
	}

	if kv.Lease == 0 {
		return 0, time.Time{}, fmt.Errorf("multidc/etcd: record has no lease")
	}

	leaseResp, err := c.lease.KeepAliveOnce(opCtx, clientv3.LeaseID(kv.Lease))
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("multidc/etcd: failed to renew lease: %w", err)
	}

	record, err := decodeRecord(kv.Value)
	if err != nil {
		return 0, time.Time{}, err
	}

	record.LeaseExpiry = time.Now().Add(time.Duration(leaseResp.TTL) * time.Second)
	payload, err := c.encode(record)
	if err != nil {
		return 0, time.Time{}, err
	}

	key := recordKey(id)
	txnResp, err := c.kv.Txn(opCtx).
		If(clientv3.Compare(clientv3.ModRevision(key), "=", kv.ModRevision)).
		Then(clientv3.OpPut(key, string(payload), clientv3.WithLease(clientv3.LeaseID(kv.Lease)))).
		Commit()
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("multidc/etcd: failed to update lease expiry: %w", err)
	}

	if !txnResp.Succeeded {
		return 0, time.Time{}, gerrors.ErrRecordConflict
	}

	return uint64(txnResp.Header.Revision), record.LeaseExpiry, nil
}

// SetState updates the record state and returns the new version.
//
// SetState enforces OCC using the supplied version. If the stored record's
// ModRevision does not match version, gerrors.ErrRecordConflict is returned.
// If the record currently has a lease, it is preserved.
func (c *ControlPlane) SetState(ctx context.Context, id string, state multidatacenter.DataCenterState, version uint64) (uint64, error) {
	if strings.TrimSpace(id) == "" {
		return 0, fmt.Errorf("multidc/etcd: id is required")
	}

	if !isValidState(state) {
		return 0, fmt.Errorf("multidc/etcd: invalid state %q", state)
	}

	opCtx, cancel := c.withTimeout(ctx)
	defer cancel()

	kv, err := c.getRecordKV(opCtx, id)
	if err != nil {
		return 0, err
	}

	if uint64(kv.ModRevision) != version {
		return 0, gerrors.ErrRecordConflict
	}

	record, err := decodeRecord(kv.Value)
	if err != nil {
		return 0, err
	}

	record.State = state
	payload, err := c.encode(record)
	if err != nil {
		return 0, err
	}

	key := recordKey(id)
	putOp := clientv3.OpPut(key, string(payload))
	if kv.Lease != 0 {
		putOp = clientv3.OpPut(key, string(payload), clientv3.WithLease(clientv3.LeaseID(kv.Lease)))
	}

	txnResp, err := c.kv.Txn(opCtx).
		If(clientv3.Compare(clientv3.ModRevision(key), "=", kv.ModRevision)).
		Then(putOp).
		Commit()
	if err != nil {
		return 0, fmt.Errorf("multidc/etcd: failed to update state: %w", err)
	}

	if !txnResp.Succeeded {
		return 0, gerrors.ErrRecordConflict
	}

	return uint64(txnResp.Header.Revision), nil
}

// ListActive returns all records whose state is DataCenterActive and whose lease
// is still valid.
//
// Records are fetched by prefix scan within the configured namespace. For each
// candidate record, ListActive checks the lease TTL; records without a lease or
// with an expired lease are excluded. The returned slice is sorted by version
// (ascending).
func (c *ControlPlane) ListActive(ctx context.Context) ([]multidatacenter.DataCenterRecord, error) {
	opCtx, cancel := c.withTimeout(ctx)
	defer cancel()

	resp, err := c.kv.Get(opCtx, "", clientv3.WithPrefix())
	if err != nil {
		return nil, fmt.Errorf("multidc/etcd: failed to list records: %w", err)
	}

	records := make([]multidatacenter.DataCenterRecord, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		if !isRecordKey(string(kv.Key)) {
			continue
		}

		record, err := decodeRecord(kv.Value)
		if err != nil {
			return nil, err
		}

		if record.State != multidatacenter.DataCenterActive {
			continue
		}

		record.Version = uint64(kv.ModRevision)
		leaseExpiry, active := c.leaseExpiry(ctx, kv.Lease)
		if !active {
			continue
		}

		record.LeaseExpiry = leaseExpiry
		records = append(records, record)
	}

	sort.Slice(records, func(i, j int) bool {
		return records[i].Version < records[j].Version
	})

	return records, nil
}

// Watch streams control-plane events (upserts and deletes) for datacenter records.
//
// The returned channel is closed when the watch terminates or when ctx is done.
// Only keys that represent datacenter records are translated into events.
//
// Note: Watch uses etcd's watch API with WithPrevKV enabled to provide the last
// known record value on deletes when available.
func (c *ControlPlane) Watch(ctx context.Context) (<-chan multidatacenter.ControlPlaneEvent, error) {
	if ctx == nil {
		ctx = c.config.Context
	}

	events := make(chan multidatacenter.ControlPlaneEvent)
	watchChan := c.watcher.Watch(ctx, "", clientv3.WithPrefix(), clientv3.WithPrevKV())

	go func() {
		defer close(events)
		for resp := range watchChan {
			if resp.Err() != nil {
				return
			}
			for _, ev := range resp.Events {
				if !isRecordKey(string(ev.Kv.Key)) {
					continue
				}
				event, ok := c.toControlPlaneEvent(ctx, ev)
				if !ok {
					continue
				}
				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return events, nil
}

func (c *ControlPlane) toControlPlaneEvent(ctx context.Context, ev *clientv3.Event) (multidatacenter.ControlPlaneEvent, bool) {
	switch ev.Type {
	case clientv3.EventTypePut:
		record, err := decodeRecord(ev.Kv.Value)
		if err != nil {
			return multidatacenter.ControlPlaneEvent{}, false
		}
		record.Version = uint64(ev.Kv.ModRevision)
		record.LeaseExpiry, _ = c.leaseExpiry(ctx, ev.Kv.Lease)
		return multidatacenter.ControlPlaneEvent{
			Type:   multidatacenter.ControlPlaneEventUpsert,
			Record: record,
		}, true
	case clientv3.EventTypeDelete:
		record := multidatacenter.DataCenterRecord{}
		if ev.PrevKv != nil {
			decoded, err := decodeRecord(ev.PrevKv.Value)
			if err != nil {
				return multidatacenter.ControlPlaneEvent{}, false
			}
			record = decoded
			record.Version = uint64(ev.PrevKv.ModRevision)
		} else {
			if id, ok := parseRecordID(string(ev.Kv.Key)); ok {
				record.ID = id
			}
		}
		return multidatacenter.ControlPlaneEvent{
			Type:   multidatacenter.ControlPlaneEventDelete,
			Record: record,
		}, true
	default:
		return multidatacenter.ControlPlaneEvent{}, true
	}
}

func (c *ControlPlane) validateRecord(record multidatacenter.DataCenterRecord) error {
	return validation.New(validation.FailFast()).
		AddValidator(validation.NewEmptyStringValidator("ID", record.ID)).
		AddValidator(validation.NewEmptyStringValidator("DataCenter.Name", record.DataCenter.Name)).
		AddAssertion(len(record.Endpoints) > 0, "Endpoints must not be empty").
		Validate()
}

func (c *ControlPlane) leaseTTLSeconds() int64 {
	seconds := int64(c.config.TTL.Seconds())
	if seconds < 1 {
		return 1
	}
	return seconds
}

func (c *ControlPlane) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = c.config.Context
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, c.config.Timeout)
}

func (c *ControlPlane) registerCompare(ctx context.Context, key string, version uint64) (clientv3.Cmp, error) {
	if version > 0 {
		return clientv3.Compare(clientv3.ModRevision(key), "=", int64(version)), nil
	}

	resp, err := c.kv.Get(ctx, key)
	if err != nil {
		return clientv3.Cmp{}, fmt.Errorf("multidc/etcd: failed to check existing record: %w", err)
	}
	if len(resp.Kvs) == 0 {
		return clientv3.Compare(clientv3.CreateRevision(key), "=", 0), nil
	}
	return clientv3.Compare(clientv3.ModRevision(key), "=", resp.Kvs[0].ModRevision), nil
}

func (c *ControlPlane) getRecordKV(ctx context.Context, id string) (*mvccpb.KeyValue, error) {
	key := recordKey(id)
	resp, err := c.kv.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("multidc/etcd: failed to load record: %w", err)
	}
	if len(resp.Kvs) == 0 {
		return nil, gerrors.ErrRecordNotFound
	}
	return resp.Kvs[0], nil
}

func (c *ControlPlane) leaseExpiry(ctx context.Context, leaseID int64) (time.Time, bool) {
	if leaseID == 0 {
		return time.Time{}, false
	}

	opCtx, cancel := c.withTimeout(ctx)
	defer cancel()

	resp, err := c.lease.TimeToLive(opCtx, clientv3.LeaseID(leaseID))
	if err != nil || resp.TTL <= 0 {
		return time.Time{}, false
	}
	return time.Now().Add(time.Duration(resp.TTL) * time.Second), true
}

func (c *ControlPlane) encode(record multidatacenter.DataCenterRecord) ([]byte, error) {
	if c.encodeRecordFunc != nil {
		return c.encodeRecordFunc(record)
	}
	return encodeRecord(record)
}

func normalizeNamespace(namespaceValue string) string {
	trimmed := strings.TrimSpace(namespaceValue)
	if trimmed == "" {
		return defaultNamespace + "/"
	}
	if strings.HasSuffix(trimmed, "/") {
		return trimmed
	}
	return trimmed + "/"
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

func isValidState(state multidatacenter.DataCenterState) bool {
	switch state {
	case multidatacenter.DataCenterRegistered, multidatacenter.DataCenterActive, multidatacenter.DataCenterDraining, multidatacenter.DataCenterInactive:
		return true
	default:
		return false
	}
}

func encodeRecord(record multidatacenter.DataCenterRecord) ([]byte, error) {
	pbRecord, err := toProto(record)
	if err != nil {
		return nil, err
	}
	return proto.Marshal(pbRecord)
}

func decodeRecord(payload []byte) (multidatacenter.DataCenterRecord, error) {
	pbRecord := &internalpb.DataCenterRecord{}
	if err := proto.Unmarshal(payload, pbRecord); err != nil {
		return multidatacenter.DataCenterRecord{}, fmt.Errorf("multidc/etcd: failed to decode record: %w", err)
	}
	return fromProto(pbRecord), nil
}

func toProto(record multidatacenter.DataCenterRecord) (*internalpb.DataCenterRecord, error) {
	state, err := toProtoState(record.State)
	if err != nil {
		return nil, err
	}

	pbRecord := &internalpb.DataCenterRecord{
		Id: record.ID,
		DataCenter: &internalpb.DataCenter{
			Name:   record.DataCenter.Name,
			Region: record.DataCenter.Region,
			Zone:   record.DataCenter.Zone,
			Labels: record.DataCenter.Labels,
		},
		Endpoints: record.Endpoints,
		State:     state,
		Version:   record.Version,
	}

	if !record.LeaseExpiry.IsZero() {
		pbRecord.LeaseExpiry = timestamppb.New(record.LeaseExpiry)
	}

	return pbRecord, nil
}

func fromProto(record *internalpb.DataCenterRecord) multidatacenter.DataCenterRecord {
	var dataCenter multidatacenter.DataCenter
	if record.GetDataCenter() != nil {
		dataCenter = multidatacenter.DataCenter{
			Name:   record.GetDataCenter().GetName(),
			Region: record.GetDataCenter().GetRegion(),
			Zone:   record.GetDataCenter().GetZone(),
			Labels: record.GetDataCenter().GetLabels(),
		}
	}

	mapped := multidatacenter.DataCenterRecord{
		ID:         record.GetId(),
		DataCenter: dataCenter,
		Endpoints:  record.GetEndpoints(),
		State:      fromProtoState(record.GetState()),
		Version:    record.GetVersion(),
	}

	if record.LeaseExpiry != nil {
		mapped.LeaseExpiry = record.LeaseExpiry.AsTime()
	}

	return mapped
}

func toProtoState(state multidatacenter.DataCenterState) (internalpb.DataCenterState, error) {
	switch state {
	case multidatacenter.DataCenterRegistered:
		return internalpb.DataCenterState_DATA_CENTER_STATE_REGISTERED, nil
	case multidatacenter.DataCenterActive:
		return internalpb.DataCenterState_DATA_CENTER_STATE_ACTIVE, nil
	case multidatacenter.DataCenterDraining:
		return internalpb.DataCenterState_DATA_CENTER_STATE_DRAINING, nil
	case multidatacenter.DataCenterInactive:
		return internalpb.DataCenterState_DATA_CENTER_STATE_INACTIVE, nil
	case "":
		return internalpb.DataCenterState_DATA_CENTER_STATE_UNSPECIFIED, nil
	default:
		return internalpb.DataCenterState_DATA_CENTER_STATE_UNSPECIFIED, fmt.Errorf("multidc/etcd: unsupported state %q", state)
	}
}

func fromProtoState(state internalpb.DataCenterState) multidatacenter.DataCenterState {
	switch state {
	case internalpb.DataCenterState_DATA_CENTER_STATE_REGISTERED:
		return multidatacenter.DataCenterRegistered
	case internalpb.DataCenterState_DATA_CENTER_STATE_ACTIVE:
		return multidatacenter.DataCenterActive
	case internalpb.DataCenterState_DATA_CENTER_STATE_DRAINING:
		return multidatacenter.DataCenterDraining
	case internalpb.DataCenterState_DATA_CENTER_STATE_INACTIVE:
		return multidatacenter.DataCenterInactive
	default:
		return ""
	}
}
