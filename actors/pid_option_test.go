package actors

import (
	"testing"
	"time"

	"github.com/tochemey/goakt/pkg/stream"

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
	eventsStream := stream.NewBroker()

	testCases := []struct {
		name     string
		option   pidOption
		expected *pid
	}{
		{
			name:     "WithPassivationAfter",
			option:   withPassivationAfter(time.Second),
			expected: &pid{passivateAfter: atomicDuration},
		},
		{
			name:     "WithSendReplyTimeout",
			option:   withSendReplyTimeout(time.Second),
			expected: &pid{sendReplyTimeout: atomicDuration},
		},
		{
			name:     "WithInitMaxRetries",
			option:   withInitMaxRetries(5),
			expected: &pid{initMaxRetries: atomicInt},
		},
		{
			name:     "WithLogger",
			option:   withCustomLogger(log.DefaultLogger),
			expected: &pid{logger: log.DefaultLogger},
		},
		{
			name:     "WithSupervisorStrategy",
			option:   withSupervisorStrategy(RestartDirective),
			expected: &pid{supervisorStrategy: RestartDirective},
		},
		{
			name:     "WithShutdownTimeout",
			option:   withShutdownTimeout(time.Second),
			expected: &pid{shutdownTimeout: atomicDuration},
		},
		{
			name:     "WithPassivationDisabled",
			option:   withPassivationDisabled(),
			expected: &pid{passivateAfter: negativeDuration},
		},
		{
			name:     "WithMailboxSize",
			option:   withMailboxSize(10),
			expected: &pid{mailboxSize: 10},
		},
		{
			name:     "WithMailbox",
			option:   withMailbox(mailbox),
			expected: &pid{mailbox: mailbox},
		},
		{
			name:     "WithStash",
			option:   withStash(10),
			expected: &pid{stashCapacity: atomicUint64},
		},
		{
			name:     "withEventsStream",
			option:   withEventsStream(eventsStream),
			expected: &pid{eventsStream: eventsStream},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pid := &pid{}
			tc.option(pid)
			assert.Equal(t, tc.expected, pid)
		})
	}
}
