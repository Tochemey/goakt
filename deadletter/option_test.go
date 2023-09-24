package deadletter

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/telemetry"
)

func TestOptions(t *testing.T) {
	tel := telemetry.New()
	testCases := []struct {
		name     string
		option   Option
		expected Stream
	}{
		{
			name:     "WithLogger",
			option:   WithLogger(log.DefaultLogger),
			expected: Stream{logger: log.DefaultLogger},
		},
		{
			name:     "WithTelemetry",
			option:   WithTelemetry(tel),
			expected: Stream{telemetry: tel},
		},
		{
			name:     "WithCapacity",
			option:   WithCapacity(10),
			expected: Stream{capacity: 10},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg Stream
			tc.option.Apply(&cfg)
			assert.Equal(t, tc.expected, cfg)
		})
	}
}
