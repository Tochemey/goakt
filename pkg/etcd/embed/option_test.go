package embed

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestOptions(t *testing.T) {
	testCases := []struct {
		name           string
		option         Option
		expectedConfig Config
	}{
		{
			name:           "WithLoggingEnable",
			option:         WithLoggingEnable(),
			expectedConfig: Config{enableLogging: true},
		},
		{
			name:           "WithInitialCluster",
			option:         WithInitialCluster("test=http://0.0.0.0:2380"),
			expectedConfig: Config{initialCluster: "test=http://0.0.0.0:2380"},
		},
		{
			name:           "WithStartTimeout",
			option:         WithStartTimeout(time.Second),
			expectedConfig: Config{startTimeout: time.Second},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg Config
			tc.option.Apply(&cfg)
			assert.Equal(t, tc.expectedConfig, cfg)
		})
	}
}
