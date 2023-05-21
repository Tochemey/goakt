package cluster

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOptions(t *testing.T) {
	testCases := []struct {
		name     string
		option   Option
		expected Cluster
	}{
		{
			name:     "WithDataDir",
			option:   WithDataDir("etcd/data"),
			expected: Cluster{dataDir: "etcd/data"},
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
