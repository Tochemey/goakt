package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestOptions(t *testing.T) {
	tracerProvider := sdktrace.NewTracerProvider()
	meterProvider := metric.NewNoopMeterProvider()

	testCases := []struct {
		name           string
		option         Option
		expectedConfig Telemetry
	}{
		{
			name:           "WithTracerProvider",
			option:         WithTracerProvider(tracerProvider),
			expectedConfig: Telemetry{TracerProvider: tracerProvider},
		},
		{
			name:           "WithMeterProvider",
			option:         WithMeterProvider(meterProvider),
			expectedConfig: Telemetry{MeterProvider: meterProvider},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg Telemetry
			tc.option.Apply(&cfg)
			assert.Equal(t, tc.expectedConfig, cfg)
		})
	}
}
