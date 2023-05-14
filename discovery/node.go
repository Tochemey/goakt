package discovery

import "fmt"

const (
	advertisePeerPortNumber   = 2380 // TODO: revisit this port
	advertiseClientPortNumber = 2379 //  TODO: revisit this port
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
}

// NodeURL return the node URL
func (n *Node) NodeURL() string {
	var url string
	for _, portNumber := range n.Ports {
		if portNumber == advertisePeerPortNumber {
			url = fmt.Sprintf("http://%s:%d", n.Host, portNumber)
			break
		}
	}
	return url
}

// ClientURL return the node URL
func (n *Node) ClientURL() string {
	var url string
	for _, portNumber := range n.Ports {
		if portNumber == advertiseClientPortNumber {
			url = fmt.Sprintf("http://%s:%d", n.Host, portNumber)
			break
		}
	}
	return url
}
