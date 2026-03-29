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

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"
)

func TestNewReplicatorMetric(t *testing.T) {
	t.Run("creates all instruments successfully", func(t *testing.T) {
		meter := noop.NewMeterProvider().Meter("test")
		m, err := NewReplicatorMetric(meter)
		require.NoError(t, err)
		require.NotNil(t, m)
	})

	t.Run("accessors return non-nil instruments", func(t *testing.T) {
		meter := noop.NewMeterProvider().Meter("test")
		m, err := NewReplicatorMetric(meter)
		require.NoError(t, err)

		assert.NotNil(t, m.StoreSize())
		assert.NotNil(t, m.MergeCount())
		assert.NotNil(t, m.DeltaPublishCount())
		assert.NotNil(t, m.DeltaReceiveCount())
		assert.NotNil(t, m.CoordinatedWriteCount())
		assert.NotNil(t, m.CoordinatedReadCount())
		assert.NotNil(t, m.AntiEntropyCount())
		assert.NotNil(t, m.TombstoneCount())
		assert.NotNil(t, m.CrossDCSendCount())
		assert.NotNil(t, m.CrossDCReceiveCount())
		assert.NotNil(t, m.CrossDCReplicationLag())
		assert.NotNil(t, m.CrossDCStaleSkipCount())
	})
}

func TestNewReplicatorMetricErrors(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("boom")
	baseMeter := noop.NewMeterProvider().Meter("test")

	testCases := []struct {
		name    string
		failKey string
	}{
		{name: "store size gauge", failKey: "crdt.replicator.store.size"},
		{name: "merge count counter", failKey: "crdt.replicator.merge.count"},
		{name: "delta publish counter", failKey: "crdt.replicator.delta.publish.count"},
		{name: "delta receive counter", failKey: "crdt.replicator.delta.receive.count"},
		{name: "coordinated write counter", failKey: "crdt.replicator.coordinated.write.count"},
		{name: "coordinated read counter", failKey: "crdt.replicator.coordinated.read.count"},
		{name: "anti-entropy counter", failKey: "crdt.replicator.antientropy.count"},
		{name: "tombstone gauge", failKey: "crdt.replicator.tombstone.count"},
		{name: "cross-DC send counter", failKey: "crdt.replicator.crossdc.send.count"},
		{name: "cross-DC receive counter", failKey: "crdt.replicator.crossdc.receive.count"},
		{name: "cross-DC replication lag gauge", failKey: "crdt.replicator.crossdc.replication.lag"},
		{name: "cross-DC stale skip counter", failKey: "crdt.replicator.crossdc.stale.skip.count"},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			meter := instrumentFailingMeter{
				Meter: baseMeter,
				failures: map[string]error{
					tt.failKey: errBoom,
				},
			}

			instruments, err := NewReplicatorMetric(meter)
			require.Error(t, err)
			require.Nil(t, instruments)
		})
	}
}
