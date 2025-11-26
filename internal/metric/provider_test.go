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
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

func TestNewInstrumentationUsesGlobalProvider(t *testing.T) {
	prevProvider := otel.GetMeterProvider()
	baseProvider := noop.NewMeterProvider()
	baseMeter := baseProvider.Meter("base")

	recorder := &recorderMeterProvider{
		MeterProvider: baseProvider,
		meter:         baseMeter,
	}

	otel.SetMeterProvider(recorder)
	t.Cleanup(func() {
		otel.SetMeterProvider(prevProvider)
	})

	telemetry := New()
	require.NotNil(t, telemetry)
	require.NotNil(t, telemetry.Meter())
	require.Equal(t, recorder, telemetry.meterProvider)
	require.Equal(t, baseMeter, telemetry.meter)
	require.Equal(t, []string{instrumentationName}, recorder.called)
}

func TestWithMeterProviderOverridesDefault(t *testing.T) {
	customProvider := &recorderMeterProvider{
		MeterProvider: noop.NewMeterProvider(),
		meter:         noop.NewMeterProvider().Meter("custom"),
	}

	telemetry := New(WithMeterProvider(customProvider))

	require.Equal(t, customProvider, telemetry.meterProvider)
	require.Equal(t, customProvider.meter, telemetry.meter)
	require.Equal(t, []string{instrumentationName}, customProvider.called)
}

func TestWithMeterProviderIgnoresNil(t *testing.T) {
	prevProvider := otel.GetMeterProvider()
	baseProvider := noop.NewMeterProvider()

	recorder := &recorderMeterProvider{
		MeterProvider: baseProvider,
	}

	otel.SetMeterProvider(recorder)
	t.Cleanup(func() {
		otel.SetMeterProvider(prevProvider)
	})

	telemetry := New(WithMeterProvider(nil))

	require.Equal(t, recorder, telemetry.meterProvider)
	require.NotNil(t, telemetry.meter)
	require.Equal(t, []string{instrumentationName}, recorder.called)
}

type recorderMeterProvider struct {
	metric.MeterProvider
	called []string
	meter  metric.Meter
}

func (p *recorderMeterProvider) Meter(name string, opts ...metric.MeterOption) metric.Meter {
	p.called = append(p.called, name)
	if p.meter != nil {
		return p.meter
	}
	return p.MeterProvider.Meter(name, opts...)
}
