package cluster

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tochemey/goakt/log"
	testkit "github.com/tochemey/goakt/testkit/hash"
)

func TestOptions(t *testing.T) {
	mockHasher := new(testkit.Hasher)
	testCases := []struct {
		name     string
		option   Option
		expected Cluster
	}{
		{
			name:     "WithPartitionsCount",
			option:   WithPartitionsCount(2),
			expected: Cluster{partitionsCount: 2},
		},
		{
			name:     "WithLogger",
			option:   WithLogger(log.DefaultLogger),
			expected: Cluster{logger: log.DefaultLogger},
		},
		{
			name:     "WithWriteTimeout",
			option:   WithWriteTimeout(2 * time.Minute),
			expected: Cluster{writeTimeout: 2 * time.Minute},
		},
		{
			name:     "WithReadTimeout",
			option:   WithReadTimeout(2 * time.Minute),
			expected: Cluster{readTimeout: 2 * time.Minute},
		},
		{
			name:     "WithShutdownTimeout",
			option:   WithShutdownTimeout(2 * time.Minute),
			expected: Cluster{shutdownTimeout: 2 * time.Minute},
		},
		{
			name:     "WithHasher",
			option:   WithHasher(mockHasher),
			expected: Cluster{hasher: mockHasher},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var cl Cluster
			tc.option.Apply(&cl)
			assert.Equal(t, tc.expected, cl)
		})
	}
}
