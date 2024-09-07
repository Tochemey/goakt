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
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

func TestTelemetry(t *testing.T) {
	tel := New()
	assert.NotNil(t, tel)
	globalTracer := otel.GetTracerProvider()
	globalMeterProvider := otel.GetMeterProvider()

	actual := tel.TraceProvider()
	assert.NotNil(t, actual)
	assert.Equal(t, globalTracer, actual)
	assert.Equal(t, globalTracer.Tracer(instrumentationName,
		trace.WithInstrumentationVersion(Version())), tel.Tracer())

	actualmp := tel.MeterProvider()
	assert.NotNil(t, actualmp)
	assert.Equal(t, globalMeterProvider, actualmp)
	assert.Equal(t, globalMeterProvider.Meter(instrumentationName,
		metric.WithInstrumentationVersion(Version())), tel.Meter())
}
