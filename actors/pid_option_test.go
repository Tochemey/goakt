package actors

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
)

func TestPIDOptions(t *testing.T) {
	testCases := []struct {
		name           string
		option         pidOption
		expectedConfig *pid
	}{
		{
			name:           "WithPassivationAfter",
			option:         withPassivationAfter(time.Second),
			expectedConfig: &pid{passivateAfter: atomic.NewDuration(time.Second)},
		},
		{
			name:           "WithSendReplyTimeout",
			option:         withSendReplyTimeout(time.Second),
			expectedConfig: &pid{sendReplyTimeout: atomic.NewDuration(time.Second)},
		},
		{
			name:           "WithInitMaxRetries",
			option:         withInitMaxRetries(5),
			expectedConfig: &pid{initMaxRetries: atomic.NewInt32(5)},
		},
		{
			name:           "WithLogger",
			option:         withCustomLogger(log.DefaultLogger),
			expectedConfig: &pid{logger: log.DefaultLogger},
		},
		{
			name:           "WithSupervisorStrategy",
			option:         withSupervisorStrategy(RestartDirective),
			expectedConfig: &pid{supervisorStrategy: RestartDirective},
		},
		{
			name:           "WithShutdownTimeout",
			option:         withShutdownTimeout(time.Second),
			expectedConfig: &pid{shutdownTimeout: atomic.NewDuration(time.Second)},
		},
		{
			name:           "WithPassivationDisabled",
			option:         withPassivationDisabled(),
			expectedConfig: &pid{passivateAfter: atomic.NewDuration(-1)},
		},
		{
			name:           "WithMailboxSize",
			option:         withMailboxSize(10),
			expectedConfig: &pid{mailboxSize: 10},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pid := &pid{}
			tc.option(pid)
			assert.Equal(t, tc.expectedConfig, pid)
		})
	}
}
