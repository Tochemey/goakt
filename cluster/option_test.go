package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tochemey/goakt/log"
)

func TestOptions(t *testing.T) {
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var cl Cluster
			tc.option.Apply(&cl)
			assert.Equal(t, tc.expected, cl)
		})
	}
}
