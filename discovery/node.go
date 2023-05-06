package discovery

import (
	"fmt"
)

// Node represents a discovered node
type Node struct {
	// Name specifies the discovered node's Name
	Name string
	// Host specifies the discovered node's Host
	Host string
	// JoinPort specifies the discovered node's JoinPort
	JoinPort int32
	// Specifies the start time
	StartTime int64
	// RemotingPort specifies the discovered node's remoting port
	// This is necessary for remoting messages
	RemotingPort int32
}

// JoinAddr returns the join address
func (n Node) JoinAddr() string {
	return fmt.Sprintf("%s:%d", n.Host, n.JoinPort)
}

// RemotingAddr returns the remoting address
func (n Node) RemotingAddr() string {
	return fmt.Sprintf("%s:%d", n.Host, n.RemotingPort)
}
