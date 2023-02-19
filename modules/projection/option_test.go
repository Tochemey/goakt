package projection

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
)

func TestOption(t *testing.T) {
	testCases := []struct {
		name           string
		option         Option
		expectedConfig *RecoverySetting
	}{
		{
			name:           "WithRetries",
			option:         WithRetries(5),
			expectedConfig: &RecoverySetting{retries: 5},
		},
		{
			name:           "WithRetryDelay",
			option:         WithRetryDelay(time.Second),
			expectedConfig: &RecoverySetting{retryDelay: time.Second},
		},
		{
			name:           "WithRecoveryStrategy",
			option:         WithRecoveryStrategy(pb.ProjectionRecoveryStrategy_SKIP),
			expectedConfig: &RecoverySetting{strategy: pb.ProjectionRecoveryStrategy_SKIP},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			recovery := &RecoverySetting{}
			tc.option.Apply(recovery)
			assert.Equal(t, tc.expectedConfig, recovery)
		})
	}
}
