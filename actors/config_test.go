package actors

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	actorspb "github.com/tochemey/goakt/actorpb/actors/v1"
	"github.com/tochemey/goakt/log"
)

func TestConfig(t *testing.T) {
	t.Run("WithValidConfig", func(t *testing.T) {
		cfg, err := NewConfig("testSys", "localhost:0")
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
		cfg, err := NewConfig("", "localhost:0")
		require.Error(t, err)
		assert.EqualError(t, err, ErrNameRequired.Error())
		assert.Nil(t, cfg)
	})
	t.Run("WithInvalidNodeAddr", func(t *testing.T) {
		cfg, err := NewConfig("Sys", "localhost")
		require.Error(t, err)
		assert.Nil(t, cfg)
	})
}

func TestOptions(t *testing.T) {
	testCases := []struct {
		name           string
		option         Option
		expectedConfig Config
	}{
		{
			name:           "WithExpireActorAfter",
			option:         WithExpireActorAfter(2 * time.Second),
			expectedConfig: Config{expireActorAfter: 2. * time.Second},
		},
		{
			name:           "WithReplyTimeout",
			option:         WithReplyTimeout(2 * time.Second),
			expectedConfig: Config{replyTimeout: 2. * time.Second},
		},
		{
			name:           "WithActorInitMaxRetries",
			option:         WithActorInitMaxRetries(2),
			expectedConfig: Config{actorInitMaxRetries: 2},
		},
		{
			name:           "WithLogger",
			option:         WithLogger(log.DefaultLogger),
			expectedConfig: Config{logger: log.DefaultLogger},
		},
		{
			name:           "WithPassivationDisabled",
			option:         WithPassivationDisabled(),
			expectedConfig: Config{expireActorAfter: -1},
		},
		{
			name:           "WithSupervisorStrategy",
			option:         WithSupervisorStrategy(actorspb.Strategy_RESTART),
			expectedConfig: Config{supervisorStrategy: actorspb.Strategy_RESTART},
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
