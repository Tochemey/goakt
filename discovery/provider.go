package discovery

const (
	GossipPortName  = "gossip-port"
	ClusterPortName = "cluster-port"
)

// Provider helps discover other running actor system in a cloud environment
type Provider interface {
	// ID returns the discovery name
	ID() string
	// Initialize initializes the plugin: registers some internal data structures, clients etc.
	Initialize() error
	// Register registers this node to a service discovery directory.
	Register() error
	// Deregister removes this node from a service discovery directory.
	Deregister() error
	// SetConfig registers the underlying discovery options
	SetConfig(config Config) error
	// DiscoverPeers returns a list of known nodes.
	DiscoverPeers() ([]string, error)
}
