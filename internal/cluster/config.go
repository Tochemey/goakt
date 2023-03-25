package cluster

import (
	"github.com/tochemey/goakt/internal/discovery"
	"github.com/tochemey/goakt/log"
)

// Config represents the cluster configuration
type Config struct {
	// Logger specifies the logger to use
	Logger log.Logger
	// Host specifies the host address
	Host string
	// Port specifies the port
	Port int32
	// StateDir specifies the cluster state directory
	StateDir string
	// Name specifies the cluster name
	Name string
	// Discovery specifies the discovery engine
	Discovery discovery.Discovery
}
