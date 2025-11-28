/*
 * MIT License
 *
 * Copyright (c) 2022-2025 Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package metric

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

func TestActorMetric(t *testing.T) {
	meter := noop.NewMeterProvider().Meter("test")
	instruments, err := NewActorMetric(meter)
	require.NoError(t, err)
	require.NotNil(t, instruments)

	require.NotNil(t, instruments.ChildrenCount())
	require.NotNil(t, instruments.StashSize())
	require.NotNil(t, instruments.DeadlettersCount())
	require.NotNil(t, instruments.RestartCount())
	require.NotNil(t, instruments.LastReceivedDuration())
	require.NotNil(t, instruments.ProcessedCount())
	require.NotNil(t, instruments.Uptime())
	require.NotNil(t, instruments.FailureCount())
	require.NotNil(t, instruments.ReinstateCount())
}

func TestActorMetricErrors(t *testing.T) {
	t.Parallel()

	errBoom := errors.New("boom")
	baseMeter := noop.NewMeterProvider().Meter("test")

	testCases := []struct {
		name    string
		failKey string
	}{
		{name: "children counter", failKey: "actor.children.count"},
		{name: "stash size counter", failKey: "actor.stash.size"},
		{name: "deadletters counter", failKey: "actor.deadletters.count"},
		{name: "restart counter", failKey: "actor.restart.count"},
		{name: "last received duration histogram", failKey: "actor.last.received.duration"},
		{name: "processed counter", failKey: "actor.processed.count"},
		{name: "uptime histogram", failKey: "actor.uptime"},
		{name: "failure counter", failKey: "actor.failure.count"},
		{name: "reinstate counter", failKey: "actor.reinstate.count"},
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

			instruments, err := NewActorMetric(meter)
			require.Error(t, err)
			require.Nil(t, instruments)
		})
	}
}

type instrumentFailingMeter struct {
	metric.Meter
	failures map[string]error
}

func (m instrumentFailingMeter) Int64ObservableCounter(
	name string,
	options ...metric.Int64ObservableCounterOption,
) (metric.Int64ObservableCounter, error) {
	if err, ok := m.failures[name]; ok {
		return nil, err
	}
	return m.Meter.Int64ObservableCounter(name, options...)
}

func (m instrumentFailingMeter) Float64Histogram(
	name string,
	options ...metric.Float64HistogramOption,
) (metric.Float64Histogram, error) {
	if err, ok := m.failures[name]; ok {
		return nil, err
	}
	return m.Meter.Float64Histogram(name, options...)
}
