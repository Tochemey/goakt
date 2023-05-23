package actors

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tochemey/goakt/log"
)

func TestConfig(t *testing.T) {
	t.Run("WithValidConfig", func(t *testing.T) {
		cfg, err := NewConfig("testSys")
		require.NoError(t, err)
		assert.NotNil(t, cfg)
		assert.EqualValues(t, 100*time.Millisecond, cfg.ReplyTimeout())
		assert.EqualValues(t, 2*time.Second, cfg.ExpireActorAfter())
		assert.EqualValues(t, 5, cfg.ActorInitMaxRetries())
		assert.Equal(t, log.DefaultLogger, cfg.Logger())
		assert.Equal(t, "testSys", cfg.Name())
	})
	t.Run("WithEmptyName", func(t *testing.T) {
		cfg, err := NewConfig("")
		require.Error(t, err)
		assert.EqualError(t, err, ErrNameRequired.Error())
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
			option:         WithSupervisorStrategy(RestartDirective),
			expectedConfig: Config{supervisorStrategy: RestartDirective},
		},
		{
			name:           "WithRemoting",
			option:         WithRemoting("localhost", 3100),
			expectedConfig: Config{remotingEnabled: true, remotingPort: 3100, remotingHost: "localhost"},
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
