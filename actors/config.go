package actors

import (
	"errors"
	"net"
	"regexp"
	"strconv"
	"time"

	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/internal/telemetry"
	"github.com/tochemey/goakt/log"
)

var (
	ErrNameRequired     = errors.New("actor system is required")
	ErrNodeAddrRequired = errors.New("actor system node address is required")
)

// Config represents the actor system configuration
type Config struct {
	// Specifies the actor system name
	name string
	// Specifies the Node Host and IP address
	// example: 127.0.0.1:8888
	nodeHostAndPort string
	// Specifies the logger to use in the system
	logger log.Logger
	// Specifies at what point in time to passivate the actor.
	// when the actor is passivated it is stopped which means it does not consume
	// any further resources like memory and cpu. The default value is 5s
	expireActorAfter time.Duration
	// Specifies how long the sender of a receiveContext should wait to receive a reply
	// when using SendReply. The default value is 5s
	replyTimeout time.Duration
	// Specifies the maximum of retries to attempt when the actor
	// initialization fails. The default value is 5
	actorInitMaxRetries int
	// Specifies the supervisor strategy
	supervisorStrategy StrategyDirective
	// Specifies the telemetry config
	telemetry *telemetry.Telemetry
	// Specifies whether remoting is enabled.
	// This allows to handle remote messaging
	remotingEnabled bool
	// convenient field to check cluster setup
	clusterEnabled bool
	// cluster discovery method
	disco discovery.Discovery
}

// NewConfig creates an instance of Config
func NewConfig(name, nodeHostAndPort string, options ...Option) (*Config, error) {
	// check whether the name is set or not
	if name == "" {
		return nil, ErrNameRequired
	}
	if match, _ := regexp.MatchString("^[a-zA-Z0-9][a-zA-Z0-9-_]*$", name); !match {
		return nil, ErrInvalidActorSystemName
	}

	// check whether the node host and port is set and valid
	if err := validateHostAndPort(nodeHostAndPort); err != nil {
		return nil, err
	}
	// create an instance of config
	config := &Config{
		name:                name,
		nodeHostAndPort:     nodeHostAndPort,
		logger:              log.DefaultLogger,
		expireActorAfter:    2 * time.Second,
		replyTimeout:        100 * time.Millisecond,
		actorInitMaxRetries: 5,
		supervisorStrategy:  StopDirective,
		telemetry:           telemetry.New(),
		remotingEnabled:     false,
	}
	// apply the various options
	for _, opt := range options {
		opt.Apply(config)
	}

	return config, nil
}

// Name returns the actor system name
func (c Config) Name() string {
	return c.name
}

// NodeHostAndPort returns the node host and port
func (c Config) NodeHostAndPort() string {
	return c.nodeHostAndPort
}

// Logger returns the log
func (c Config) Logger() log.Logger {
	return c.logger
}

// ExpireActorAfter returns the expireActorAfter
func (c Config) ExpireActorAfter() time.Duration {
	return c.expireActorAfter
}

// ReplyTimeout returns the reply timeout
func (c Config) ReplyTimeout() time.Duration {
	return c.replyTimeout
}

// ActorInitMaxRetries returns the actor init max retries
func (c Config) ActorInitMaxRetries() int {
	return c.actorInitMaxRetries
}

// HostAndPort returns the host and the port
func (c Config) HostAndPort() (host string, port int) {
	// no need to check for the error because of the previous validation
	host, portStr, _ := net.SplitHostPort(c.nodeHostAndPort)
	port, _ = strconv.Atoi(portStr)
	return
}

// SupervisorStrategy specifies the supervisor strategy
func (c Config) SupervisorStrategy() StrategyDirective {
	return c.supervisorStrategy
}

// Telemetry specifies the telemetry config
func (c Config) Telemetry() *telemetry.Telemetry {
	return c.telemetry
}

// RemotingEnabled returns the remoting enabled
func (c Config) RemotingEnabled() bool {
	return c.remotingEnabled
}

// validateHostAndPort helps validate the host address and port of and address
func validateHostAndPort(hostAndPort string) error {
	if hostAndPort == "" {
		return ErrNodeAddrRequired
	}
	_, port, err := net.SplitHostPort(hostAndPort)
	if err != nil {
		return err
	}

	_, err = strconv.Atoi(port)
	if err != nil {
		return err
	}

	return nil
}

// Option is the interface that applies a configuration option.
type Option interface {
	// Apply sets the Option value of a config.
	Apply(config *Config)
}

var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(*Config)

func (f OptionFunc) Apply(c *Config) {
	f(c)
}

// WithExpireActorAfter sets the actor expiry duration.
// After such duration an idle actor will be expired and removed from the actor system
func WithExpireActorAfter(duration time.Duration) Option {
	return OptionFunc(func(config *Config) {
		config.expireActorAfter = duration
	})
}

// WithLogger sets the actor system custom log
func WithLogger(logger log.Logger) Option {
	return OptionFunc(func(config *Config) {
		config.logger = logger
	})
}

// WithReplyTimeout sets how long in seconds an actor should reply a command
// in a receive-reply pattern
func WithReplyTimeout(timeout time.Duration) Option {
	return OptionFunc(func(config *Config) {
		config.replyTimeout = timeout
	})
}

// WithActorInitMaxRetries sets the number of times to retry an actor init process
func WithActorInitMaxRetries(max int) Option {
	return OptionFunc(func(config *Config) {
		config.actorInitMaxRetries = max
	})
}

// WithPassivationDisabled disable the passivation mode
func WithPassivationDisabled() Option {
	return OptionFunc(func(config *Config) {
		config.expireActorAfter = -1
	})
}

// WithSupervisorStrategy sets the supervisor strategy
func WithSupervisorStrategy(strategy StrategyDirective) Option {
	return OptionFunc(func(config *Config) {
		config.supervisorStrategy = strategy
	})
}

// WithTelemetry sets the custom telemetry
func WithTelemetry(telemetry *telemetry.Telemetry) Option {
	return OptionFunc(func(config *Config) {
		config.telemetry = telemetry
	})
}

// WithRemoting enables remoting on the actor system
func WithRemoting() Option {
	return OptionFunc(func(config *Config) {
		config.remotingEnabled = true
	})
}

// WithClustering enables clustering on the actor system
func WithClustering(disco discovery.Discovery) Option {
	return OptionFunc(func(config *Config) {
		config.clusterEnabled = true
		config.disco = disco
	})
}
