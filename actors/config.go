package actors

import (
	"errors"
	"net"
	"strconv"
	"time"

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
	// Specifies how long the sender of a message should wait to receive a reply
	// when using SendReply. The default value is 5s
	replyTimeout time.Duration
	// Specifies the maximum of retries to attempt when the actor
	// initialization fails. The default value is 5
	actorInitMaxRetries int
}

// NewConfig creates an instance of Config
func NewConfig(name, nodeHostAndPort string, options ...Option) (*Config, error) {
	// check whether the name is set or not
	if name == "" {
		return nil, ErrNameRequired
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
		expireActorAfter:    5 * time.Second,
		replyTimeout:        5 * time.Second,
		actorInitMaxRetries: 5,
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

// Logger returns the logger
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

// WithLogger sets the actor system custom logger
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
