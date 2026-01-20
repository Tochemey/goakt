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
	"fmt"
	"time"
)

// DataCenterState is the lifecycle state of a DataCenterRecord as seen by the control plane.
//
// Providers and clients should treat these values as stable wire-level strings.
type DataCenterState string

const (
	// DataCenterRegistered indicates the record exists in the control plane but is not yet
	// eligible for routing.
	DataCenterRegistered DataCenterState = "REGISTERED"

	// DataCenterActive indicates the record is eligible for routing as long as its lease is valid.
	DataCenterActive DataCenterState = "ACTIVE"

	// DataCenterDraining indicates the record should avoid new placements; existing traffic may
	// continue depending on routing policy.
	DataCenterDraining DataCenterState = "DRAINING"

	// DataCenterInactive indicates the record is not eligible for routing.
	DataCenterInactive DataCenterState = "INACTIVE"
)

// DataCenter carries datacenter metadata for multi-DC routing and health.
type DataCenter struct {
	// Name is the datacenter identifier (required when multi-DC is enabled).
	Name string
	// Region optionally groups datacenters into a higher-level region.
	Region string
	// Zone optionally specifies an availability zone.
	Zone string
	// Labels provides optional routing metadata for placement and policy decisions.
	Labels map[string]string
}

func (dc DataCenter) ID() string {
	return fmt.Sprintf("%s%s%s", dc.Zone, dc.Region, dc.Name)
}

// DataCenterRecord is the control plane representation of a single datacenter.
//
// Records are expected to be leased: when LeaseExpiry is in the past, the record should be
// treated as expired/inactive unless renewed via Heartbeat.
//
// Version is a monotonic revision used for optimistic concurrency. Providers should reject
// conflicting updates (stale Version) and return an error.
type DataCenterRecord struct {
	// ID is the stable, immutable identifier for the record.
	ID string
	// DataCenter holds the datacenter metadata.
	DataCenter DataCenter
	// Endpoints is the list of advertised addresses for routing.
	Endpoints []string
	// State is the current lifecycle state of the record.
	State DataCenterState
	// LeaseExpiry is the time at which the record expires if not renewed.
	LeaseExpiry time.Time
	// Version is a monotonic revision for conflict-free updates.
	Version uint64
}
