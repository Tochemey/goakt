package store

import (
	"github.com/coreos/etcd/pkg/types"
	"github.com/tochemey/goakt/internal/etcd/embed"
	"github.com/tochemey/goakt/internal/etcd/urls"
	"github.com/tochemey/goakt/log"
)

// Config defines the distributed store config
type Config struct {
	logger      log.Logger
	name        string
	endPoints   []string
	clientsPort int32
	peersPort   int32
}

// NewDefaultConfig creates a store config with the default cluster ports
func NewDefaultConfig(name string, logger log.Logger) *Config {
	return &Config{
		logger:      logger,
		name:        name,
		clientsPort: urls.DefaultClientsPort,
		peersPort:   urls.DefaultPeersPort,
	}
}

// NewConfig creates an instance of Config
func NewConfig(name string, logger log.Logger, endpoints []string, clientsPort, peersPort int32) *Config {
	return &Config{
		logger:      logger,
		name:        name,
		endPoints:   endpoints,
		clientsPort: clientsPort,
		peersPort:   peersPort,
	}
}

// GetEmbedConfig returns an instance of embed config from the given config
func (c *Config) GetEmbedConfig() *embed.Config {
	// let us define the various embed config options
	var opts []embed.Option

	// check whether the endpoints are set
	if len(c.endPoints) > 0 {
		opts = append(opts, embed.WithEndPoints(types.MustNewURLs(c.endPoints)))
	}

	// check whether the logger is set
	if c.logger != nil {
		opts = append(opts, embed.WithLogger(c.logger))
	}

	// return an instance of embed config with options set
	if len(opts) > 0 {
		return embed.NewConfig(c.name, c.clientsPort, c.peersPort, opts...)
	}

	// return an instance of embed config with the default value
	return embed.NewConfig(c.name, c.clientsPort, c.peersPort)
}
