package actors

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/log"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
)

func TestConfig(t *testing.T) {
	t.Run("WithValidConfig", func(t *testing.T) {
		cfg, err := NewSetting("testSys", "localhost:0")
		require.NoError(t, err)
		assert.NotNil(t, cfg)
		assert.EqualValues(t, 100*time.Millisecond, cfg.ReplyTimeout())
		assert.EqualValues(t, 2*time.Second, cfg.ExpireActorAfter())
		assert.EqualValues(t, 5, cfg.ActorInitMaxRetries())
		assert.Equal(t, log.DefaultLogger, cfg.Logger())
		assert.Equal(t, "testSys", cfg.Name())
		assert.Equal(t, "localhost:0", cfg.NodeHostAndPort())
	})
	t.Run("WithEmptyName", func(t *testing.T) {
		cfg, err := NewSetting("", "localhost:0")
		require.Error(t, err)
		assert.EqualError(t, err, ErrNameRequired.Error())
		assert.Nil(t, cfg)
	})
	t.Run("WithInvalidNodeAddr", func(t *testing.T) {
		cfg, err := NewSetting("Sys", "localhost")
		require.Error(t, err)
		assert.Nil(t, cfg)
	})
}

func TestOptions(t *testing.T) {
	testCases := []struct {
		name           string
		option         Option
		expectedConfig Setting
	}{
		{
			name:           "WithExpireActorAfter",
			option:         WithExpireActorAfter(2 * time.Second),
			expectedConfig: Setting{expireActorAfter: 2. * time.Second},
		},
		{
			name:           "WithReplyTimeout",
			option:         WithReplyTimeout(2 * time.Second),
			expectedConfig: Setting{replyTimeout: 2. * time.Second},
		},
		{
			name:           "WithActorInitMaxRetries",
			option:         WithActorInitMaxRetries(2),
			expectedConfig: Setting{actorInitMaxRetries: 2},
		},
		{
			name:           "WithLogger",
			option:         WithLogger(log.DefaultLogger),
			expectedConfig: Setting{logger: log.DefaultLogger},
		},
		{
			name:           "WithPassivationDisabled",
			option:         WithPassivationDisabled(),
			expectedConfig: Setting{expireActorAfter: -1},
		},
		{
			name:           "WithSupervisorStrategy",
			option:         WithSupervisorStrategy(pb.StrategyDirective_RESTART_DIRECTIVE),
			expectedConfig: Setting{supervisorStrategy: pb.StrategyDirective_RESTART_DIRECTIVE},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg Setting
			tc.option.Apply(&cfg)
			assert.Equal(t, tc.expectedConfig, cfg)
		})
	}
}
