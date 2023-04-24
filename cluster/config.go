package cluster

import "fmt"

// Config define the cluster config
type Config struct {
	NodeID   uint64
	NodeHost string
	NodePort int
}

// GetURL returns the config address
func (c Config) GetURL() string {
	return fmt.Sprintf("https://%s:%d", c.NodeHost, c.NodePort)
}
