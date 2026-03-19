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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStageMetrics_Snapshot(t *testing.T) {
	var m stageMetrics
	m.elementsIn.Add(10)
	m.elementsOut.Add(8)
	m.droppedElements.Add(2)
	m.errors.Add(1)
	m.bpWaitNs.Add(int64(50 * time.Millisecond))

	s := m.snapshot()
	assert.Equal(t, uint64(10), s.ElementsIn)
	assert.Equal(t, uint64(8), s.ElementsOut)
	assert.Equal(t, uint64(2), s.DroppedElements)
	assert.Equal(t, uint64(1), s.Errors)
	assert.InDelta(t, 50.0, s.BackpressureMs, 0.01)
}

func TestAggregateMetrics(t *testing.T) {
	parts := []StreamMetrics{
		{ElementsIn: 5, ElementsOut: 4, DroppedElements: 1, Errors: 0, BackpressureMs: 10.0},
		{ElementsIn: 3, ElementsOut: 3, DroppedElements: 0, Errors: 1, BackpressureMs: 5.0},
	}
	agg := aggregateMetrics(parts)
	assert.Equal(t, uint64(8), agg.ElementsIn)
	assert.Equal(t, uint64(7), agg.ElementsOut)
	assert.Equal(t, uint64(1), agg.DroppedElements)
	assert.Equal(t, uint64(1), agg.Errors)
	assert.InDelta(t, 15.0, agg.BackpressureMs, 0.01)
}

func TestAggregateMetrics_Empty(t *testing.T) {
	agg := aggregateMetrics(nil)
	assert.Equal(t, StreamMetrics{}, agg)
}
