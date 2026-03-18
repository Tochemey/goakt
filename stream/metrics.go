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

package stream

import (
	"sync/atomic"
	"time"
)

// StreamMetrics captures live observability data for a running stream.
// All counters are updated atomically by stage actors; reads are eventually consistent.
type StreamMetrics struct {
	// ElementsIn is the total number of elements that entered the pipeline at the source.
	ElementsIn uint64
	// ElementsOut is the total number of elements emitted by the final sink.
	ElementsOut uint64
	// DroppedElements is the count of elements discarded due to overflow or error-resume.
	DroppedElements uint64
	// Errors is the count of element-level processing errors encountered.
	Errors uint64
	// BackpressureMs is the cumulative milliseconds the source spent waiting for demand.
	BackpressureMs float64
}

// stageMetrics is the mutable, atomically-updated metrics state for a single stage.
// A snapshot is copied into StreamMetrics on each Metrics() call.
type stageMetrics struct {
	elementsIn      atomic.Uint64
	elementsOut     atomic.Uint64
	droppedElements atomic.Uint64
	errors          atomic.Uint64
	bpWaitNs        atomic.Int64 // nanoseconds of backpressure wait
}

func (m *stageMetrics) snapshot() StreamMetrics {
	bp := m.bpWaitNs.Load()
	return StreamMetrics{
		ElementsIn:      m.elementsIn.Load(),
		ElementsOut:     m.elementsOut.Load(),
		DroppedElements: m.droppedElements.Load(),
		Errors:          m.errors.Load(),
		BackpressureMs:  float64(bp) / float64(time.Millisecond),
	}
}

// aggregateMetrics sums multiple stageMetrics snapshots into one StreamMetrics.
func aggregateMetrics(parts []StreamMetrics) StreamMetrics {
	var agg StreamMetrics
	for _, p := range parts {
		agg.ElementsIn += p.ElementsIn
		agg.ElementsOut += p.ElementsOut
		agg.DroppedElements += p.DroppedElements
		agg.Errors += p.Errors
		agg.BackpressureMs += p.BackpressureMs
	}
	return agg
}
