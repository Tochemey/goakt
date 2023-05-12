package store

import (
	"github.com/coreos/etcd/pkg/types"
	"github.com/tochemey/goakt/internal/etcd/embed"
	"github.com/tochemey/goakt/log"
)

// Config defines the distributed store config
type Config struct {
	Endpoints  []string
	ClientURLs []string
	PeerURLs   []string
	Logger     log.Logger
	Name       string
}

// GetEmbedConfig returns an instance of embed config from the given config
func (c Config) GetEmbedConfig() *embed.Config {
	// let us define the various embed config options
	var opts []embed.Option
	// check whether the endpoints are set
	if len(c.Endpoints) > 0 {
		opts = append(opts, embed.WithEndPoints(types.MustNewURLs(c.Endpoints)))
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
