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

package actor

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	otelmetric "go.opentelemetry.io/otel/metric"
	noopmetric "go.opentelemetry.io/otel/metric/noop"

	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
)

// callbackCapturingMeter records the metric callbacks a metrics-enabled system
// registers so a test can drive a full scrape by invoking them directly.
type callbackCapturingMeter struct {
	otelmetric.Meter
	callbacks []otelmetric.Callback
}

// RegisterCallback captures the callback and returns a no-op registration.
func (m *callbackCapturingMeter) RegisterCallback(cb otelmetric.Callback, _ ...otelmetric.Observable) (otelmetric.Registration, error) {
	m.callbacks = append(m.callbacks, cb)
	return noopmetric.Registration{}, nil
}

// callbackCapturingMeterProvider hands out its callbackCapturingMeter regardless
// of the requested meter name.
type callbackCapturingMeterProvider struct {
	otelmetric.MeterProvider
	meter *callbackCapturingMeter
}

// Meter implements otelmetric.MeterProvider.
func (p *callbackCapturingMeterProvider) Meter(string, ...otelmetric.MeterOption) otelmetric.Meter {
	return p.meter
}

// TestRegisterMetricsAsksDeadletterOncePerScrape guards the fix for issue #1322:
// the per-actor metrics callback must ask the deadletter actor for its counts
// once per scrape, not once per running actor. The deadletter actor increments
// its processed message count before it replies, so the delta across a single
// scrape over a whole population is the number of asks the scrape issued.
func TestRegisterMetricsAsksDeadletterOncePerScrape(t *testing.T) {
	ctx := context.TODO()

	delegate := noopmetric.NewMeterProvider()
	meter := &callbackCapturingMeter{Meter: delegate.Meter("capture")}
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(&callbackCapturingMeterProvider{MeterProvider: delegate, meter: meter})
	defer otel.SetMeterProvider(previous)

	sys, err := NewActorSystem("testSys", WithLogger(log.DiscardLogger), WithMetrics())
	require.NoError(t, err)
	require.NoError(t, sys.Start(ctx))

	// spawn a population large enough that a per-actor ask would be obvious as
	// a delta far greater than one.
	const population = 20
	for i := range population {
		_, err := sys.Spawn(ctx, fmt.Sprintf("worker-%d", i), &MockActor{}, WithLongLived())
		require.NoError(t, err)
	}

	// let every actor process its PostStart so the per-actor callback observes
	// them and reaches the deadletter lookup.
	pause.For(time.Second)

	system, ok := sys.(*actorSystem)
	require.True(t, ok)
	deadletter := system.getDeadletter()
	require.NotNil(t, deadletter)

	// two callbacks are registered for a non-cluster system: the system-level
	// one and the per-actor one. Only the per-actor callback asks the
	// deadletter actor.
	require.Len(t, meter.callbacks, 2)

	observer := noopmetric.Observer{}
	before := deadletter.ProcessedCount()

	for _, callback := range meter.callbacks {
		require.NoError(t, callback(ctx, observer))
	}

	after := deadletter.ProcessedCount()
	require.EqualValues(t, 1, after-before, "one scrape over %d actors must ask the deadletter actor once", population)

	require.NoError(t, sys.Stop(ctx))
}
