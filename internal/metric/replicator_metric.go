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

// ReplicatorMetric groups OpenTelemetry instruments for the CRDT Replicator actor.
//
// Instruments:
//   - crdt.replicator.store.size              (Int64ObservableGauge)
//   - crdt.replicator.merge.count             (Int64ObservableCounter)
//   - crdt.replicator.delta.publish.count     (Int64ObservableCounter)
//   - crdt.replicator.delta.receive.count     (Int64ObservableCounter)
//   - crdt.replicator.coordinated.write.count (Int64ObservableCounter)
//   - crdt.replicator.coordinated.read.count  (Int64ObservableCounter)
//   - crdt.replicator.antientropy.count       (Int64ObservableCounter)
//   - crdt.replicator.tombstone.count         (Int64ObservableGauge)
type ReplicatorMetric struct {
	storeSize             metric.Int64ObservableGauge
	mergeCount            metric.Int64ObservableCounter
	deltaPublishCount     metric.Int64ObservableCounter
	deltaReceiveCount     metric.Int64ObservableCounter
	coordinatedWriteCount metric.Int64ObservableCounter
	coordinatedReadCount  metric.Int64ObservableCounter
	antiEntropyCount      metric.Int64ObservableCounter
	tombstoneCount        metric.Int64ObservableGauge
}

// NewReplicatorMetric creates the CRDT Replicator instruments using the provided Meter.
// Returns an error if any instrument cannot be created.
func NewReplicatorMetric(meter metric.Meter) (*ReplicatorMetric, error) {
	var m ReplicatorMetric
	var err error

	if m.storeSize, err = meter.Int64ObservableGauge(
		"crdt.replicator.store.size",
		metric.WithDescription("Number of CRDT keys in the local store"),
	); err != nil {
		return nil, err
	}

	if m.mergeCount, err = meter.Int64ObservableCounter(
		"crdt.replicator.merge.count",
		metric.WithDescription("Total number of CRDT merges performed"),
	); err != nil {
		return nil, err
	}

	if m.deltaPublishCount, err = meter.Int64ObservableCounter(
		"crdt.replicator.delta.publish.count",
		metric.WithDescription("Total number of deltas published via TopicActor"),
	); err != nil {
		return nil, err
	}

	if m.deltaReceiveCount, err = meter.Int64ObservableCounter(
		"crdt.replicator.delta.receive.count",
		metric.WithDescription("Total number of deltas received from peers"),
	); err != nil {
		return nil, err
	}

	if m.coordinatedWriteCount, err = meter.Int64ObservableCounter(
		"crdt.replicator.coordinated.write.count",
		metric.WithDescription("Total number of coordinated write operations"),
	); err != nil {
		return nil, err
	}

	if m.coordinatedReadCount, err = meter.Int64ObservableCounter(
		"crdt.replicator.coordinated.read.count",
		metric.WithDescription("Total number of coordinated read operations"),
	); err != nil {
		return nil, err
	}

	if m.antiEntropyCount, err = meter.Int64ObservableCounter(
		"crdt.replicator.antientropy.count",
		metric.WithDescription("Total number of anti-entropy rounds completed"),
	); err != nil {
		return nil, err
	}

	if m.tombstoneCount, err = meter.Int64ObservableGauge(
		"crdt.replicator.tombstone.count",
		metric.WithDescription("Number of active tombstones in the replicator"),
	); err != nil {
		return nil, err
	}

	return &m, nil
}

// StoreSize returns the observable gauge for CRDT store size.
func (m *ReplicatorMetric) StoreSize() metric.Int64ObservableGauge {
	return m.storeSize
}

// MergeCount returns the observable counter for merge operations.
func (m *ReplicatorMetric) MergeCount() metric.Int64ObservableCounter {
	return m.mergeCount
}

// DeltaPublishCount returns the observable counter for published deltas.
func (m *ReplicatorMetric) DeltaPublishCount() metric.Int64ObservableCounter {
	return m.deltaPublishCount
}

// DeltaReceiveCount returns the observable counter for received deltas.
func (m *ReplicatorMetric) DeltaReceiveCount() metric.Int64ObservableCounter {
	return m.deltaReceiveCount
}

// CoordinatedWriteCount returns the observable counter for coordinated writes.
func (m *ReplicatorMetric) CoordinatedWriteCount() metric.Int64ObservableCounter {
	return m.coordinatedWriteCount
}

// CoordinatedReadCount returns the observable counter for coordinated reads.
func (m *ReplicatorMetric) CoordinatedReadCount() metric.Int64ObservableCounter {
	return m.coordinatedReadCount
}

// AntiEntropyCount returns the observable counter for anti-entropy rounds.
func (m *ReplicatorMetric) AntiEntropyCount() metric.Int64ObservableCounter {
	return m.antiEntropyCount
}

// TombstoneCount returns the observable gauge for active tombstones.
func (m *ReplicatorMetric) TombstoneCount() metric.Int64ObservableGauge {
	return m.tombstoneCount
}
