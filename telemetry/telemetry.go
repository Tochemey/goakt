/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
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

package telemetry

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const (
	instrumentationName = "github.com/Tochemey/goakt/v2"
)

// Telemetry encapsulates some settings for an actor
type Telemetry struct {
	tracerProvider trace.TracerProvider
	tracer         trace.Tracer

	meterProvider metric.MeterProvider
	meter         metric.Meter
}

// New creates an instance of Telemetry
func New(options ...Option) *Telemetry {
	// create a config instance
	telemetry := &Telemetry{
		tracerProvider: otel.GetTracerProvider(),
		meterProvider:  otel.GetMeterProvider(),
	}

	// apply the various options
	for _, opt := range options {
		opt.Apply(telemetry)
	}

	// set the tracer
	telemetry.tracer = telemetry.tracerProvider.Tracer(
		instrumentationName,
		trace.WithInstrumentationVersion(Version()),
	)

	// set the meter
	telemetry.meter = telemetry.meterProvider.Meter(
		instrumentationName,
		metric.WithInstrumentationVersion(Version()),
	)

	return telemetry
}

// TraceProvider returns the trace provider
func (t *Telemetry) TraceProvider() trace.TracerProvider {
	return t.tracerProvider
}

// MeterProvider returns the meter provider
func (t *Telemetry) MeterProvider() metric.MeterProvider {
	return t.meterProvider
}

// Meter returns the meter
func (t *Telemetry) Meter() metric.Meter {
	return t.meter
}

// Tracer returns the tracer
func (t *Telemetry) Tracer() trace.Tracer {
	return t.tracer
}
