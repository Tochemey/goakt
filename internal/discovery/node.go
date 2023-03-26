package discovery

// Node represents a discovered node
type Node struct {
	// name specifies the discovered node's name
	name string
	// host specifies teh discovered node's host
	host string
	// port specifies the discovered node's port
	port int32
	// timestamp specifies the time the node was created
	// The timestamp is expressed in UNIX time
	timestamp int64
	// Specifies additional meta info about the node
	// This is optional but can be useful when one want to pass a node information to other nodes
	metadata map[string]string
}

// NewNode creates an instance of Node
func NewNode(name, host string, port int32, timestamp int64, metadata map[string]string) *Node {
	return &Node{
		name:      name,
		host:      host,
		port:      port,
		timestamp: timestamp,
		metadata:  metadata,
	}
}

// Name returns the node name
func (n Node) Name() string {
	return n.name
}

// Host returns the node host
func (n Node) Host() string {
	return n.host
}

// Port returns the node port
func (n Node) Port() int32 {
	return n.port
}

// Timestamp returns the node created time
// The timestamp is expressed in UNIX time
func (n Node) Timestamp() int64 {
	return n.timestamp
}

// Metadata returns the node's metadata
func (n Node) Metadata() map[string]string {
	return n.metadata
}
