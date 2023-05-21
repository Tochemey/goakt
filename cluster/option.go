package cluster

// Option is the interface that applies a configuration option.
type Option interface {
	// Apply sets the Option value of a config.
	Apply(cluster *Cluster)
}

var _ Option = OptionFunc(nil)

// OptionFunc implements the Option interface.
type OptionFunc func(cluster *Cluster)

func (f OptionFunc) Apply(c *Cluster) {
	f(c)
}

// WithDataDir sets the data dir
func WithDataDir(datadir string) Option {
	return OptionFunc(func(cl *Cluster) {
		cl.dataDir = datadir
	})
}
