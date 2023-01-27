package cluster

import (
	"time"

	"github.com/tochemey/goakt/log"
)

// NodeConfig specifies a cluster node configuration
type NodeConfig struct {
	// ID specifies the node unique ID
	// If the node name is not set it default to the combination of the host name
	// with an uuid
	ID string
	// BindHost is the address to bind to
	BindHost string
	// BindPort is the port to bind to
	BindPort int
	// GossipPort is used for both UDP and TCP gossip
	GossipPort int
	// JoinAddr is used by the node to join the cluster
	JoinAddr string
	// LeaveTimeout specifies the timeout for the given to leave the cluster
	// when shutting down
	LeaveTimeout time.Duration
	// Logger specifies the logger to use
	Logger log.Logger
}
