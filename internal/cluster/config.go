package cluster

import "github.com/tochemey/goakt/log"

// Config represents the cluster configuration
type Config struct {
	// Logger specifies the logger to use
	Logger log.Logger
	// Specifies the host address
	Host string
	// Specifies the port
	Port int
	// Specifies the cluster state directory
	StateDir string
	// Specifies the cluster name
	Name string
}
