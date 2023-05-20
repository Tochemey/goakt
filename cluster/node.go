package cluster

import (
	"fmt"

	"github.com/tochemey/goakt/discovery"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"golang.org/x/exp/slices"
)

// nodeURLs returns the actual node URLs
func nodeURLs(node *discovery.Node) (peersURL string, clientURL string) {
	return fmt.Sprintf("http://%s:%d", node.Host, node.Ports[peersPortName]),
		fmt.Sprintf("http://%s:%d", node.Host, node.Ports[clientPortName])
}

// locateMember helps find a given member using its peerURL
func locateMember(members []*etcdserverpb.Member, node *discovery.Node) *etcdserverpb.Member {
	// grab the given node URL
	peerURL, _ := nodeURLs(node)
	for _, member := range members {
		if slices.Contains(member.GetPeerURLs(), peerURL) && member.GetName() == node.Name {
			return member
		}
	}
	return nil
}
