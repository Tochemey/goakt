package actors

import (
	"time"

	"github.com/tochemey/goakt/logging"
)

// Config represents the actor system configuration
type Config struct {
	// Specifies the actor system name
	Name string
	// Specifies the Node Host and IP address
	// example: 127.0.0.1:8888
	NodeHostAndPort string
	// Specifies the logger to use in the system
	Logger logging.Logger
	// Specifies at what point in time to passivate the actor.
	// when the actor is passivated it is stopped which means it does not consume
	// any further resources like memory and cpu. The default value is 5s
	PassivateAfter time.Duration
	// Specifies how long the sender of a message should wait to receive a reply
	// when using SendReply. The default value is 5s
	SendRecvTimeout time.Duration
	// Specifies the maximum of retries to attempt when the actor
	// initialization fails. The default value is 5
	InitMaxRetries int
}
