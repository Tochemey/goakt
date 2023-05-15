package embed

import (
	"path"
	"time"

	"github.com/coreos/etcd/pkg/types"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/internal/etcd/urls"
	"github.com/tochemey/goakt/log"
)

var (
	defaultClientURLs   types.URLs
	defaultPeerURLs     types.URLs
	defaultEndPointURLs types.URLs
)

// init helps set the default URLs
func init() {
	// grab all the IP interfaces on the host machine
	peerURLs, clientURLs, err := urls.GetNodeAdvertiseURLs()
	// handle the error
	if err != nil {
		// panic because we need to set the default URLs
		log.Panic(errors.Wrap(err, "failed to get the assigned ip addresses of the host"))
	}

	// let us finally set the default URLs
	defaultClientURLs = types.MustNewURLs(clientURLs)
	defaultPeerURLs = types.MustNewURLs(peerURLs)
	defaultEndPointURLs = defaultClientURLs
}

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
	defaultDIR := "/var/goakt/"
	// create a config instance
	cfg := &Config{
		name:          name,
		dataDir:       defaultDIR,
		peerURLs:      defaultPeerURLs,
		clientURLs:    defaultClientURLs,
		endPoints:     defaultEndPointURLs,
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
func (c *Config) Name() string {
	return c.name
}

// DataDir returns the data directory name
func (c *Config) DataDir() string {
	return c.dataDir
}

// EndPoints returns the endpoints
func (c *Config) EndPoints() types.URLs {
	return c.endPoints
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

// Size returns the cluster size
func (c *Config) Size() int {
	return c.size
}

// LogDir returns the log directory
func (c *Config) LogDir() string {
	return c.logDir
}
