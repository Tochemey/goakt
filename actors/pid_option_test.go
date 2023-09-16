package actors

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tochemey/goakt/log"
	"go.uber.org/atomic"
)

func TestPIDOptions(t *testing.T) {
	mailbox := newReceiveContextBuffer(10)
	var (
		atomicDuration   atomic.Duration
		atomicInt        atomic.Int32
		negativeDuration atomic.Duration
		atomicUint64     atomic.Uint64
	)
	negativeDuration.Store(-1)
	atomicInt.Store(5)
	atomicDuration.Store(time.Second)
	atomicUint64.Store(10)
	testCases := []struct {
		name           string
		option         pidOption
		expectedConfig *pid
	}{
		{
			name:           "WithPassivationAfter",
			option:         withPassivationAfter(time.Second),
			expectedConfig: &pid{passivateAfter: atomicDuration},
		},
		{
			name:           "WithSendReplyTimeout",
			option:         withSendReplyTimeout(time.Second),
			expectedConfig: &pid{sendReplyTimeout: atomicDuration},
		},
		{
			name:           "WithInitMaxRetries",
			option:         withInitMaxRetries(5),
			expectedConfig: &pid{initMaxRetries: atomicInt},
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
			expectedConfig: &pid{shutdownTimeout: atomicDuration},
		},
		{
			name:           "WithPassivationDisabled",
			option:         withPassivationDisabled(),
			expectedConfig: &pid{passivateAfter: negativeDuration},
		},
		{
			name:           "WithMailboxSize",
			option:         withMailboxSize(10),
			expectedConfig: &pid{mailboxSize: 10},
		},
		{
			name:           "WithMailbox",
			option:         withMailbox(mailbox),
			expectedConfig: &pid{mailbox: mailbox},
		},
		{
			name:           "WithStash",
			option:         withStash(10),
			expectedConfig: &pid{stashCapacity: atomicUint64},
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
