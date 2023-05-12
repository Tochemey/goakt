package embed

import (
	"path"
	"time"

	"github.com/coreos/etcd/pkg/types"
	"github.com/tochemey/goakt/log"
)

const (
	defaultEndpoint  = "http://0.0.0.0:2379"
	defaultClientURL = "http://0.0.0.0:2379"
	defaultPeerURL   = "http://0.0.0.0:2380"
)

// Config defines distro configuration
type Config struct {
	name          string     // name defines the system name
	dataDir       string     // dataDir defines the data directory
	endPoints     types.URLs // endPoints defines the etcd server endpoint
	peerURLs      types.URLs // peerURLS defines the peers URL
	clientURLs    types.URLs // clientURLs defines the clients URL
	enableLogging bool       // enableLogging states whether to enable logging
	size          int        // size defines the size
	logDir        string     // logDir specifies the log directory

	logger       log.Logger
	startTimeout time.Duration
}

// NewConfig creates an instance of Config
func NewConfig(name string, opts ...Option) *Config {
	// create the default dir
	defaultDIR := "."
	// create a config instance
	cfg := &Config{
		name:          name,
		dataDir:       defaultDIR,
		endPoints:     types.MustNewURLs([]string{"http://0.0.0.0:2379"}),
		peerURLs:      types.MustNewURLs([]string{"http://0.0.0.0:2380"}),
		clientURLs:    types.MustNewURLs([]string{"http://0.0.0.0:2379"}),
		enableLogging: false,
		size:          3,
		logDir:        path.Join(defaultDIR, "logs"),
		logger:        log.DefaultLogger,
		startTimeout:  time.Minute,
	}

	// apply the various options
	for _, opt := range opts {
		opt.Apply(cfg)
	}

	return cfg
}

// Name returns the name
func (c Config) Name() string {
	return c.name
}

// DataDir returns the data directory name
func (c Config) DataDir() string {
	return c.dataDir
}

// EndPoints returns the endpoints
func (c Config) EndPoints() types.URLs {
	return c.endPoints
}

// PeerURLs returns the peer URLs
func (c Config) PeerURLs() types.URLs {
	return c.peerURLs
}

// ClientURLs returns the client URLs
func (c Config) ClientURLs() types.URLs {
	return c.clientURLs
}

func (c Config) EnableLogging() bool {
	return c.enableLogging
}

// Size returns the cluster size
func (c Config) Size() int {
	return c.size
}

// LogDir returns the log directory
func (c Config) LogDir() string {
	return c.logDir
}
