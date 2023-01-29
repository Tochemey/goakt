package actors

import (
	"errors"
	"net"
	"strconv"
	"time"

	"github.com/tochemey/goakt/log"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"github.com/tochemey/goakt/telemetry"
)

var (
	ErrNameRequired     = errors.New("actor system is required")
	ErrNodeAddrRequired = errors.New("actor system node address is required")
)

// Setting represents the actor system configuration
type Setting struct {
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
	supervisorStrategy pb.StrategyDirective
	// Specifies the telemetry setting
	telemetry *telemetry.Telemetry
	// Specifies cluster Peers
	peers []*pb.Peer
	// Specifies whether cluster is enabled or not
	clusterEnabled bool
}

// NewSetting creates an instance of Setting
func NewSetting(name, nodeHostAndPort string, options ...Option) (*Setting, error) {
	// check whether the name is set or not
	if name == "" {
		return nil, ErrNameRequired
	}
	// check whether the node host and port is set and valid
	if err := validateHostAndPort(nodeHostAndPort); err != nil {
		return nil, err
	}
	// create an instance of config
	setting := &Setting{
		name:                name,
		nodeHostAndPort:     nodeHostAndPort,
		logger:              log.DefaultLogger,
		expireActorAfter:    2 * time.Second,
		replyTimeout:        100 * time.Millisecond,
		actorInitMaxRetries: 5,
		supervisorStrategy:  pb.StrategyDirective_STOP_DIRECTIVE,
		telemetry:           telemetry.New(),
		clusterEnabled:      false,
		peers:               nil,
	}
	// apply the various options
	for _, opt := range options {
		opt.Apply(setting)
	}

	return setting, nil
}

// Name returns the actor system name
func (c Setting) Name() string {
	return c.name
}

// NodeHostAndPort returns the node host and port
func (c Setting) NodeHostAndPort() string {
	return c.nodeHostAndPort
}

// Logger returns the logger
func (c Setting) Logger() log.Logger {
	return c.logger
}

// ExpireActorAfter returns the expireActorAfter
func (c Setting) ExpireActorAfter() time.Duration {
	return c.expireActorAfter
}

// ReplyTimeout returns the reply timeout
func (c Setting) ReplyTimeout() time.Duration {
	return c.replyTimeout
}

// ActorInitMaxRetries returns the actor init max retries
func (c Setting) ActorInitMaxRetries() int {
	return c.actorInitMaxRetries
}

// Peers returns the list of Peers
func (c Setting) Peers() []*pb.Peer {
	return c.peers
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
	Apply(config *Setting)
}

var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(*Setting)

func (f OptionFunc) Apply(c *Setting) {
	f(c)
}

// WithExpireActorAfter sets the actor expiry duration.
// After such duration an idle actor will be expired and removed from the actor system
func WithExpireActorAfter(duration time.Duration) Option {
	return OptionFunc(func(config *Setting) {
		config.expireActorAfter = duration
	})
}

// WithLogger sets the actor system custom logger
func WithLogger(logger log.Logger) Option {
	return OptionFunc(func(config *Setting) {
		config.logger = logger
	})
}

// WithReplyTimeout sets how long in seconds an actor should reply a command
// in a receive-reply pattern
func WithReplyTimeout(timeout time.Duration) Option {
	return OptionFunc(func(config *Setting) {
		config.replyTimeout = timeout
	})
}

// WithActorInitMaxRetries sets the number of times to retry an actor init process
func WithActorInitMaxRetries(max int) Option {
	return OptionFunc(func(config *Setting) {
		config.actorInitMaxRetries = max
	})
}

// WithPassivationDisabled disable the passivation mode
func WithPassivationDisabled() Option {
	return OptionFunc(func(config *Setting) {
		config.expireActorAfter = -1
	})
}

// WithSupervisorStrategy sets the supervisor strategy
func WithSupervisorStrategy(strategy pb.StrategyDirective) Option {
	return OptionFunc(func(config *Setting) {
		config.supervisorStrategy = strategy
	})
}

// WithTelemetry sets the custom telemetry
func WithTelemetry(telemetry *telemetry.Telemetry) Option {
	return OptionFunc(func(config *Setting) {
		config.telemetry = telemetry
	})
}

// WithPeers sets the cluster peers
func WithPeers(peers []*pb.Peer) Option {
	return OptionFunc(func(setting *Setting) {
		setting.peers = peers
	})
}

// WithCluster enables clustering on the actor system
// by making the actor system node a cluster aware node
func WithCluster() Option {
	return OptionFunc(func(setting *Setting) {
		setting.clusterEnabled = true
	})
}
