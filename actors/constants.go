package actors

import (
	"time"

	addresspb "github.com/tochemey/goakt/pb/address/v1"
)

const (
	// protocol defines the Go-Akt addressing protocol
	protocol = "goakt"
	// RestartDirective defines the restart strategy when handling actors failure
	RestartDirective StrategyDirective = iota
	// StopDirective defines the stop strategy when handling actors failure
	StopDirective

	// DefaultPassivationTimeout defines the default passivation timeout
	DefaultPassivationTimeout = 2 * time.Minute
	// DefaultReplyTimeout defines the default send/reply timeout
	DefaultReplyTimeout = 100 * time.Millisecond
	// DefaultInitMaxRetries defines the default value for retrying actor initialization
	DefaultInitMaxRetries = 5
	// DefaultSupervisoryStrategy defines the default supervisory strategy
	DefaultSupervisoryStrategy = StopDirective
	// DefaultShutdownTimeout defines the default shutdown timeout
	DefaultShutdownTimeout = 2 * time.Second

	defaultMailboxSize = 4096
)

// Unit type
type Unit struct{}

// NoSender means that there is no sender
var NoSender PID

// RemoteNoSender means that there is no sender
var RemoteNoSender = new(addresspb.Address)
