package embed

import (
	"fmt"
	"net"
	"path"
	"time"

	"github.com/coreos/etcd/pkg/types"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/log"
)

var (
	defaultClientURLs types.URLs
	defaultPeerURLs   types.URLs
)

// init helps set the default URLs
func init() {
	// grab all the IP interfaces on the host machine
	addresses, err := net.InterfaceAddrs()
	// handle the error
	if err != nil {
		// panic because we need to set the default URLs
		log.Panic(errors.Wrap(err, "failed to get the assigned ip addresses of the host"))
	}

	var (
		clientURLs []string
		peerURLs   []string
	)

	// iterate the assigned addresses
	for _, address := range addresses {
		// let us grab the CIDR
		// no need to handle the error because the address is return by golang which
		// automatically a valid address
		ip, _, _ := net.ParseCIDR(address.String())
		// let us ignore loopback ip address
		if ip.IsLoopback() {
			continue
		}

		// grab the ip string representation
		repr := ip.String()
		// check whether it is an IPv4 or IPv6
		if ip.To4() == nil {
			// Enclose IPv6 addresses with '[]' or the formed URLs will fail parsing
			repr = fmt.Sprintf("[%s]", ip.String())
		}
		// set the various URLs
		clientURLs = append(clientURLs, fmt.Sprintf("http://%s:2379", repr))
		peerURLs = append(peerURLs, fmt.Sprintf("http://%s:2380", repr))
	}

	// let us finally set the default URLs
	defaultClientURLs = types.MustNewURLs(clientURLs)
	defaultPeerURLs = types.MustNewURLs(peerURLs)
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
	defaultDIR := "."
	// create a config instance
	cfg := &Config{
		name:          name,
		dataDir:       defaultDIR,
		peerURLs:      defaultPeerURLs,
		clientURLs:    defaultClientURLs,
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
