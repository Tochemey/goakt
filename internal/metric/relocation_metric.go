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
	"fmt"

	"go.opentelemetry.io/otel/metric"
)

// RelocationMetric groups the OpenTelemetry instruments that describe actor and
// grain relocation (the rebalance a leader performs after a node leaves the
// cluster).
//
// Unlike the system-level observable counters, these are synchronous
// instruments recorded by the relocation worker when a single node's rebalance
// completes:
//   - actorsystem.relocation.duration        (Int64Histogram, unit: ms)
//   - actorsystem.relocation.relocated.count  (Int64Counter)
//   - actorsystem.relocation.failed.count     (Int64Counter)
//   - actorsystem.relocation.buffered.count   (Int64Counter)
type RelocationMetric struct {
	duration  metric.Int64Histogram
	relocated metric.Int64Counter
	failed    metric.Int64Counter
	buffered  metric.Int64Counter
}

// NewRelocationMetric constructs the relocation instruments with the provided
// Meter. It returns an error if any instrument cannot be created so telemetry
// setup issues are surfaced early.
func NewRelocationMetric(meter metric.Meter) (*RelocationMetric, error) {
	var instruments RelocationMetric
	var err error

	if instruments.duration, err = meter.Int64Histogram(
		"actorsystem.relocation.duration",
		metric.WithDescription("Wall-clock duration of a single departed node's actor/grain relocation"),
		metric.WithUnit("ms"),
	); err != nil {
		return nil, fmt.Errorf("failed to create relocation duration instrument, %v", err)
	}

	if instruments.relocated, err = meter.Int64Counter(
		"actorsystem.relocation.relocated.count",
		metric.WithDescription("Total number of actors and grains successfully relocated"),
	); err != nil {
		return nil, fmt.Errorf("failed to create relocated count instrument, %v", err)
	}

	if instruments.failed, err = meter.Int64Counter(
		"actorsystem.relocation.failed.count",
		metric.WithDescription("Total number of actors and grains that failed to relocate"),
	); err != nil {
		return nil, fmt.Errorf("failed to create relocation failed count instrument, %v", err)
	}

	if instruments.buffered, err = meter.Int64Counter(
		"actorsystem.relocation.buffered.count",
		metric.WithDescription("Total number of name-based sends that were buffered and retried while their target was relocating"),
	); err != nil {
		return nil, fmt.Errorf("failed to create relocation buffered count instrument, %v", err)
	}

	return &instruments, nil
}

// Duration returns the histogram recording the wall-clock duration, in
// milliseconds, of a single departed node's relocation.
func (x *RelocationMetric) Duration() metric.Int64Histogram {
	return x.duration
}

// Relocated returns the counter tracking how many actors and grains were
// successfully relocated.
func (x *RelocationMetric) Relocated() metric.Int64Counter {
	return x.relocated
}

// Failed returns the counter tracking how many actors and grains failed to
// relocate.
func (x *RelocationMetric) Failed() metric.Int64Counter {
	return x.failed
}

// Buffered returns the counter tracking how many name-based sends were buffered
// and retried while their target was being relocated.
func (x *RelocationMetric) Buffered() metric.Int64Counter {
	return x.buffered
}
