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

package metric

import "go.opentelemetry.io/otel/metric"

// ClusterMetric groups the OpenTelemetry instruments that describe cluster
// membership churn as observed by the local node.
//
// Both totals only grow for the lifetime of the node, so they are observable
// counters fed by the membership event loop and read at scrape time.
//
// Instruments:
//   - cluster.members.joined.count (Int64ObservableCounter)
//   - cluster.members.left.count   (Int64ObservableCounter)
type ClusterMetric struct {
	membersJoinedCount metric.Int64ObservableCounter
	membersLeftCount   metric.Int64ObservableCounter
}

// NewClusterMetric creates the cluster membership instruments using the
// provided Meter. It returns an error if any instrument cannot be created so
// telemetry initialization failures are surfaced early.
func NewClusterMetric(meter metric.Meter) (*ClusterMetric, error) {
	var instruments ClusterMetric
	var err error

	if instruments.membersJoinedCount, err = meter.Int64ObservableCounter(
		"cluster.members.joined.count",
		metric.WithDescription("Total number of cluster members the local node has seen join"),
	); err != nil {
		return nil, err
	}

	if instruments.membersLeftCount, err = meter.Int64ObservableCounter(
		"cluster.members.left.count",
		metric.WithDescription("Total number of cluster members the local node has seen leave"),
	); err != nil {
		return nil, err
	}

	return &instruments, nil
}

// MembersJoinedCount returns the observable counter that tracks how many
// cluster members the local node has seen join.
//
// Use with Meter.RegisterCallback to observe the current value periodically.
func (x *ClusterMetric) MembersJoinedCount() metric.Int64ObservableCounter {
	return x.membersJoinedCount
}

// MembersLeftCount returns the observable counter that tracks how many cluster
// members the local node has seen leave.
//
// Use with Meter.RegisterCallback to observe the current value periodically.
func (x *ClusterMetric) MembersLeftCount() metric.Int64ObservableCounter {
	return x.membersLeftCount
}
