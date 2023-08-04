package actors

import (
	"errors"
	"time"

	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/pkg/telemetry"
)

var (
	ErrNameRequired = errors.New("actor system is required")
)

// Option is the interface that applies a configuration option.
type Option interface {
	// Apply sets the Option value of a config.
	Apply(sys *actorSystem)
}

var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(*actorSystem)

func (f OptionFunc) Apply(c *actorSystem) {
	f(c)
}

// WithExpireActorAfter sets the actor expiry duration.
// After such duration an idle actor will be expired and removed from the actor system
func WithExpireActorAfter(duration time.Duration) Option {
	return OptionFunc(func(sys *actorSystem) {
		sys.expireActorAfter = duration
	})
}

// WithLogger sets the actor system custom log
func WithLogger(logger log.Logger) Option {
	return OptionFunc(func(sys *actorSystem) {
		sys.logger = logger
	})
}

// WithReplyTimeout sets how long in seconds an actor should reply a command
// in a receive-reply pattern
func WithReplyTimeout(timeout time.Duration) Option {
	return OptionFunc(func(sys *actorSystem) {
		sys.replyTimeout = timeout
	})
}

// WithActorInitMaxRetries sets the number of times to retry an actor init process
func WithActorInitMaxRetries(max int) Option {
	return OptionFunc(func(sys *actorSystem) {
		sys.actorInitMaxRetries = max
	})
}

// WithPassivationDisabled disable the passivation mode
func WithPassivationDisabled() Option {
	return OptionFunc(func(sys *actorSystem) {
		sys.expireActorAfter = -1
	})
}

// WithSupervisorStrategy sets the supervisor strategy
func WithSupervisorStrategy(strategy StrategyDirective) Option {
	return OptionFunc(func(sys *actorSystem) {
		sys.supervisorStrategy = strategy
	})
}

// WithTelemetry sets the custom telemetry
func WithTelemetry(telemetry *telemetry.Telemetry) Option {
	return OptionFunc(func(sys *actorSystem) {
		sys.telemetry = telemetry
	})
}

// WithRemoting enables remoting on the actor system
func WithRemoting(host string, port int32) Option {
	return OptionFunc(func(sys *actorSystem) {
		sys.remotingEnabled = true
		sys.remotingPort = port
		sys.remotingHost = host
	})
}

// WithClustering enables clustering on the actor system. This enables remoting on the actor system as well
// and set the remotingHost to the cluster node host when the cluster is fully enabled.
func WithClustering(serviceDiscovery *discovery.ServiceDiscovery, remotingPort int32, partitionCount uint64) Option {
	return OptionFunc(func(sys *actorSystem) {
		sys.clusterEnabled = true
		sys.remotingEnabled = true
		sys.remotingPort = remotingPort
		sys.partitionsCount = partitionCount
		sys.serviceDiscovery = serviceDiscovery
	})
}
