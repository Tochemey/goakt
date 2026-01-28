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

package datacenter

import (
	"context"
	"time"
)

// ControlPlaneEventType describes the kind of change carried by a ControlPlaneEvent.
type ControlPlaneEventType string

const (
	// ControlPlaneEventUpsert indicates a record was created or updated.
	ControlPlaneEventUpsert ControlPlaneEventType = "UPSERT"
	// ControlPlaneEventDelete indicates a record was removed or expired.
	ControlPlaneEventDelete ControlPlaneEventType = "DELETE"
)

// ControlPlaneEvent is a change notification emitted by ControlPlane.Watch.
//
// For UPSERT, Record contains the latest view.
// For DELETE, Record identifies the deleted record (providers may populate partial fields).
type ControlPlaneEvent struct {
	// Type is the change type.
	Type ControlPlaneEventType
	// Record is the record affected by the event.
	Record DataCenterRecord
}

// ControlPlane is the provider interface for multi-datacenter coordination.
//
// Semantics:
//   - Records are leased. Heartbeat renews the lease; expired records should not be routed to.
//   - Updates are versioned. Mutating operations take the caller's current version and return
//     a new version on success.
//   - All methods must honor ctx for cancellation/timeouts.
//   - Implementations should be safe for concurrent use.
type ControlPlane interface {
	// Register creates (or registers) a datacenter record and returns its assigned ID and version.
	//
	// If the provider assigns IDs, record.ID may be ignored. On success, the returned version is
	// the initial record version to be used for subsequent updates.
	Register(ctx context.Context, record DataCenterRecord) (id string, version uint64, err error)

	// Heartbeat renews the record lease for the given id at the specified version.
	//
	// On success, it returns the new record version and the updated lease expiry time.
	// Providers should return an error if the version is stale or the record does not exist.
	Heartbeat(ctx context.Context, id string, version uint64) (newVersion uint64, leaseExpiry time.Time, err error)

	// SetState sets the lifecycle state of the record for the given id at the specified version.
	//
	// On success, it returns the new record version. Providers should return an error if the
	// version is stale or the record does not exist.
	SetState(ctx context.Context, id string, state DataCenterState, version uint64) (newVersion uint64, err error)

	// ListActive returns records that are currently eligible for routing.
	//
	// Implementations should only return records whose State is DataCenterActive and whose leases
	// have not expired at the time of evaluation.
	ListActive(ctx context.Context) ([]DataCenterRecord, error)

	// Watch returns a channel that streams ordered record updates until ctx is canceled or the
	// watch ends.
	//
	// Implementations should close the returned channel when the watch terminates. Providers that
	// do not support watch should return goakt/errors.ErrWatchNotSupported.
	Watch(ctx context.Context) (<-chan ControlPlaneEvent, error)
}
