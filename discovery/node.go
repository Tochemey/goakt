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
	// Port specifies the discovered node's Port
	Port int32
	// Specifies the start time
	StartTime int64
}

// GetURL returns the config address
func (n Node) GetURL() string {
	return fmt.Sprintf("https://%s:%d", n.Host, n.Port)
}
