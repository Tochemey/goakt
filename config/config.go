package config

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

// New creates an instance of Config
func New(name, nodeHostAndPort string, options ...Option) (*Config, error) {
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
