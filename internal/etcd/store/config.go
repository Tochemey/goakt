package store

import (
	"github.com/coreos/etcd/pkg/types"
	"github.com/tochemey/goakt/internal/etcd/embed"
	"github.com/tochemey/goakt/log"
)

// Config defines the distributed store config
type Config struct {
	ClientURLs []string
	PeerURLs   []string
	EndPoints  []string
	Logger     log.Logger
	Name       string
	endPoints  []string
}

// NewDefaultConfig create a config that will use the default values
// Refer to the embed.Config
func NewDefaultConfig() *Config {
	return new(Config)
}

// NewConfig creates an instance of Config
func NewConfig(name string, logger log.Logger, endpoints, clientURLs, peerURLs []string) *Config {
	return &Config{
		ClientURLs: clientURLs,
		PeerURLs:   peerURLs,
		Logger:     logger,
		Name:       name,
		EndPoints:  endpoints,
	}
}

// GetEmbedConfig returns an instance of embed config from the given config
func (c *Config) GetEmbedConfig() *embed.Config {
	// let us define the various embed config options
	var opts []embed.Option
	// check whether the endpoints are set
	if len(c.EndPoints) > 0 {
		opts = append(opts, embed.WithEndPoints(types.MustNewURLs(c.EndPoints)))
	}
	// check whether the client URLs are set or not
	if len(c.ClientURLs) > 0 {
		opts = append(opts, embed.WithClientURLs(types.MustNewURLs(c.ClientURLs)))
	}
	// check whether the peer urls are set or not
	if len(c.PeerURLs) > 0 {
		opts = append(opts, embed.WithPeerURLs(types.MustNewURLs(c.PeerURLs)))
	}

	// check whether the logger is set
	if c.Logger != nil {
		opts = append(opts, embed.WithLogger(c.Logger))
	}

	// return an instance of embed config with options set
	if len(opts) > 0 {
		return embed.NewConfig(c.Name, opts...)
	}

	// return an instance of embed config with the default value
	return embed.NewConfig(c.Name)
}
