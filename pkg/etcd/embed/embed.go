package embed

import (
	"os"
	"path"
	"sync"
	"time"

	"github.com/coreos/etcd/pkg/types"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/log"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"
)

// Embed embeds a etcd server
type Embed struct {
	// define the underlying etcd server
	server *embed.Etcd
	// define the etcd client
	client *clientv3.Client

	config    *Config
	isStopped bool
	isReady   bool
	isLeader  bool

	mu sync.Mutex

	logger log.Logger
}

// NewEmbed creates an instance of Embed
func NewEmbed(config *Config) *Embed {
	// create the instance of the embed
	return &Embed{
		config:    config,
		isStopped: false,
		isReady:   false,
		isLeader:  false,
		mu:        sync.Mutex{},
		logger:    config.Logger(),
	}
}

// Start starts the underlying etcd server
func (es *Embed) Start() error {
	// acquire the lock
	es.mu.Lock()
	// release the lock once done
	defer es.mu.Unlock()

	// check whether the server has started or not
	if es.server != nil {
		return nil
	}

	// add some debug logging
	if es.config.InitialCluster() != "" {
		es.logger.Debugf("Starting the embed etcd server with initial cluster=[%s]", es.config.InitialCluster())
	} else {
		es.logger.Debug("Starting the embed etcd server...")
	}

	// create the embed config
	embedConfig := embed.NewConfig()
	embedConfig.Name = es.config.Name()
	embedConfig.Dir = path.Join(es.config.DataDir(), "etcd.data", es.config.Name())

	// set the various URLs
	embedConfig.ListenClientUrls = es.config.ClientURLs()
	embedConfig.AdvertiseClientUrls = es.config.ClientURLs()
	embedConfig.ListenPeerUrls = es.config.PeerURLs()
	embedConfig.AdvertisePeerUrls = es.config.PeerURLs()

	// set the logger
	embedConfig.Logger = "zap"
	embedConfig.LogLevel = es.config.logger.LogLevel().String()
	embedConfig.InitialCluster = embedConfig.InitialClusterFromName(es.config.Name())

	// generate a unique cluster token
	embedConfig.InitialClusterToken = "goakt-cluster"

	// override the initial cluster and the cluster state when
	if es.config.InitialCluster() != "" {
		embedConfig.InitialCluster = es.config.InitialCluster()
		// also let us make sure to clear the data dir
		// If starting with non-empty initial cluster, delete the datadir if it
		// exists. The etcd server will be brought up as a new server and old data
		// being present will prevent it.
		// Starting with an empty initial cluster implies that we are in a single
		// node cluster, so we need to keep the etcd data.
		if err := os.RemoveAll(es.config.DataDir()); err != nil {
			es.logger.Panic(errors.Wrap(err, "failed to remove the data dir"))
		}
	}

	es.logger.Debugf("Embed etcd server with initial cluster=[%s]", embedConfig.InitialCluster)

	// let us start the underlying server
	etcd, err := embed.StartEtcd(embedConfig)
	// handle the error
	if err != nil {
		es.logger.Error(errors.Wrap(err, "failed to start the etcd embedded server"))
		return err
	}

	// The returned embed.Etcd.Server instance is not guaranteed to have
	// joined the cluster yet. Wait on the embed.Etcd.Server.ReadyNotify()
	// channel to know when it's ready for use. Stop waiting after the start timeout
	select {
	case <-etcd.Server.ReadyNotify():
		// add logging information
		es.logger.Info("embed etcd server started..:)")
		// set the server field
		es.server = etcd
		es.isReady = true
		// return
		return nil
	case <-time.After(es.config.StartTimeout()):
		es.logger.Info("starting embedded server timeout")
		// trigger a shutdown
		etcd.Server.Stop() // trigger a shutdown
		return errors.New("embed etcd server took too long to start")
	case err := <-etcd.Err():
		// log the error
		es.logger.Error(errors.Wrap(err, "failed to start embed etcd server"))
		return err
	}
}

// Stop stops the Embed instance
func (es *Embed) Stop() error {
	// acquire the lock
	es.mu.Lock()
	// release the lock once done
	defer es.mu.Unlock()

	if es.server == nil {
		return errors.New("etcd server not running")
	}

	// close the server
	es.server.Close()
	// set to nil
	es.server = nil
	es.isStopped = true
	es.isReady = false
	// let us remove the data dir
	if err := os.RemoveAll(es.config.DataDir()); err != nil {
		es.logger.Panic(errors.Wrap(err, "failed to remove the data dir"))
	}
	return nil
}

// ClientURLs returns the list of client endpoint
func (es *Embed) ClientURLs() types.URLs {
	// acquire the lock
	es.mu.Lock()
	// release the lock once done
	defer es.mu.Unlock()
	return es.config.ClientURLs()
}

// IsLeader states whether the given node is a leader or not
func (es *Embed) IsLeader() bool {
	return es.server.Server.Leader().String() == es.server.Server.ID().String() && es.isLeader
}

// LeaderID returns the leader id
func (es *Embed) LeaderID() string {
	return es.server.Server.Leader().String()
}
