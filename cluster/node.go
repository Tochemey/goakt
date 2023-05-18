package cluster

import (
	"fmt"

	"github.com/tochemey/goakt/discovery"
)

// nodeURLs returns the actual node URLs
func nodeURLs(node *discovery.Node) (peersURL string, clientURL string) {
	return fmt.Sprintf("http://%s:%d", node.Host, node.Ports[peersPortName]),
		fmt.Sprintf("http://%s:%d", node.Host, node.Ports[clientPortName])
}
