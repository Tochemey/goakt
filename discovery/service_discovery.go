package discovery

// ServiceDiscovery defines the cluster service discovery
type ServiceDiscovery struct {
	// provider specifies the discovery provider
	provider Provider
	// config specifies the discovery config
	config Config
}

// NewServiceDiscovery creates an instance of ServiceDiscovery
func NewServiceDiscovery(provider Provider, config Config) *ServiceDiscovery {
	return &ServiceDiscovery{
		provider: provider,
		config:   config,
	}
}

// Provider returns the service discovery provider
func (s ServiceDiscovery) Provider() Provider {
	return s.provider
}

// Config returns the service discovery config
func (s ServiceDiscovery) Config() Config {
	return s.config
}
