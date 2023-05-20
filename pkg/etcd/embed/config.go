package embed

import (
	"path"
	"time"

	"github.com/coreos/etcd/pkg/types"
	"github.com/tochemey/goakt/log"
)

// Config defines the embed etcd
type Config struct {
	name           string     // name defines the system name
	dataDir        string     // dataDir defines the data directory
	initialCluster string     // initialCluster defines the etcd server endpoint
	peerURLs       types.URLs // peerURLs defines the peers URL
	clientURLs     types.URLs // clientURLs defines the clients URL
	endPoints      types.URLs // endPoints defines the etcd servers' endpoint
	enableLogging  bool       // enableLogging states whether to enable logging
	logDir         string     // logDir specifies the log directory

	logger       log.Logger
	startTimeout time.Duration
	join         bool
}

// NewConfig creates an instance of Config
func NewConfig(name string, clientURLs, peerURLs, endpoints types.URLs, opts ...Option) *Config {
	// create the default dir
	defaultDIR := "/var/goakt/"
	// create a config instance
	cfg := &Config{
		name:          name,
		dataDir:       defaultDIR,
		enableLogging: false,
		logDir:        path.Join(defaultDIR, "logs"),
		logger:        log.DefaultLogger,
		startTimeout:  time.Minute,
		clientURLs:    clientURLs,
		peerURLs:      peerURLs,
		endPoints:     endpoints,
	}

	// apply the various options
	for _, opt := range opts {
		opt.Apply(cfg)
	}

	return cfg
}

// Name returns the name
func (c *Config) Name() string {
	return c.name
}

// DataDir returns the data directory name
func (c *Config) DataDir() string {
	return c.dataDir
}

// InitialCluster returns the endpoints
func (c *Config) InitialCluster() string {
	return c.initialCluster
}

// PeerURLs returns the peer URLs
func (c *Config) PeerURLs() types.URLs {
	return c.peerURLs
}

// ClientURLs returns the client URLs
func (c *Config) ClientURLs() types.URLs {
	return c.clientURLs
}

func (c *Config) EnableLogging() bool {
	return c.enableLogging
}

// LogDir returns the log directory
func (c *Config) LogDir() string {
	return c.logDir
}

// Logger returns the configured logger
func (c *Config) Logger() log.Logger {
	return c.logger
}

// StartTimeout returns the start timeout
func (c *Config) StartTimeout() time.Duration {
	return c.startTimeout
}

// EndPoints returns the endpoints
func (c *Config) EndPoints() types.URLs {
	return c.endPoints
}
