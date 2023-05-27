package actors

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tochemey/goakt/log"
)

func TestOptions(t *testing.T) {
	testCases := []struct {
		name     string
		option   Option
		expected actorSystem
	}{
		{
			name:     "WithExpireActorAfter",
			option:   WithExpireActorAfter(2 * time.Second),
			expected: actorSystem{expireActorAfter: 2. * time.Second},
		},
		{
			name:     "WithReplyTimeout",
			option:   WithReplyTimeout(2 * time.Second),
			expected: actorSystem{replyTimeout: 2. * time.Second},
		},
		{
			name:     "WithActorInitMaxRetries",
			option:   WithActorInitMaxRetries(2),
			expected: actorSystem{actorInitMaxRetries: 2},
		},
		{
			name:     "WithLogger",
			option:   WithLogger(log.DefaultLogger),
			expected: actorSystem{logger: log.DefaultLogger},
		},
		{
			name:     "WithPassivationDisabled",
			option:   WithPassivationDisabled(),
			expected: actorSystem{expireActorAfter: -1},
		},
		{
			name:     "WithSupervisorStrategy",
			option:   WithSupervisorStrategy(RestartDirective),
			expected: actorSystem{supervisorStrategy: RestartDirective},
		},
		{
			name:     "WithRemoting",
			option:   WithRemoting("localhost", 3100),
			expected: actorSystem{remotingEnabled: true, remotingPort: 3100, remotingHost: "localhost"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg actorSystem
			tc.option.Apply(&cfg)
			assert.Equal(t, tc.expected, cfg)
		})
	}
}
