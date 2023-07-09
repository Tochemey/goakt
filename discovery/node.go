package discovery

import "fmt"

const (
	clientPortName = "clients-port"
	peersPortName  = "peers-port"
)

// Node represents a discovered node
type Node struct {
	// Name specifies the discovered node's Name
	Name string
	// Host specifies the discovered node's Host
	Host string
	// Specifies the start time
	StartTime int64
	// Ports specifies the list of Ports
	Ports map[string]int32
	// IsRunning specifies whether the node is up and running
	IsRunning bool
}

// IsValid checks whether the discovered node is a valid node discovered
func (n Node) IsValid() bool {
	// first let us make sure the various ports are set
	if _, ok := n.Ports[clientPortName]; !ok {
		return ok
	}
	// first let us make sure the various ports are set
	if _, ok := n.Ports[peersPortName]; !ok {
		return ok
	}
	return len(n.Host) != 0 && len(n.Name) != 0
}

// URLs returns the actual node URLs
func (n Node) URLs() (peersURL string, clientURL string) {
	return fmt.Sprintf("http://%s:%d", n.Host, n.Ports[peersPortName]),
		fmt.Sprintf("http://%s:%d", n.Host, n.Ports[clientPortName])
}

// PeersPort returns the node peer port
func (n Node) PeersPort() int32 {
	return n.Ports[peersPortName]
}

// ClientsPort returns the node clients ports
func (n Node) ClientsPort() int32 {
	return n.Ports[clientPortName]
}
