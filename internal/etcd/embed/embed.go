package embed

import (
	"os"
	"path"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/tochemey/goakt/log"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
	"go.etcd.io/etcd/server/v3/embed"
)

// Embed embeds a etcd server
type Embed struct {
	// define the underlying etcd server
	server *embed.Etcd
	// define the etcd server config
	embedConfig *embed.Config
	// define the etcd client
	client *clientv3.Client

	session  *concurrency.Session
	election *concurrency.Election

	config        *Config
	isStopped     bool
	hasStarted    bool
	stopWatchChan chan struct{}
	watchers      sync.WaitGroup

	mu sync.Mutex

	logger log.Logger
}

// NewEmbed creates an instance of Embed
func NewEmbed(config *Config) (*Embed, error) {
	// create an instance of the server
	srv := new(Embed)
	srv.config = config
	srv.stopWatchChan = make(chan struct{}, 1)
	srv.logger = config.logger

	// If no endpoints are given or if the default endpoint is set, assume that there is no existing server
	if len(srv.config.EndPoints()) == 0 || isDefaultEndPointURL(srv.config.EndPoints()) {
		// add some logging
		srv.logger.Info("starting a server")
		// start the server
		if err := srv.startServer(""); err != nil {
			// log the error
			srv.logger.Error(err)
			return nil, err
		}

		// update the endpoints to the advertised client URLs of the embedded server
		srv.config.endPoints = srv.server.Config().AdvertiseClientUrls
		// set the started
		srv.hasStarted = true
	}

	// let us connect to cluster as client
	if err := srv.startClient(); err != nil {
		// log the error
		srv.logger.Error(errors.Wrap(err, "failed to start as a client"))
		// let us shutdown the server
		if err := srv.Shutdown(); err != nil {
			// log the error
			srv.logger.Error(errors.Wrap(err, "failed to shutdown the embed server"))
			return nil, err
		}
	}

	if srv.hasStarted {
		// Add yourself to the nominee list, avoids nominating yourself again when you become the leader
		if err := srv.addToNominees(srv.embedConfig.Name, srv.server.Config().AdvertisePeerUrls); err != nil {
			return nil, err
		}
	}

	// let us volunteer self to start watching for nomination
	if err := srv.volunteerSelf(); err != nil {
		// log the error
		srv.logger.Error(errors.Wrap(err, "failed to volunteer self"))
		// let us shutdown the server
		if err := srv.Shutdown(); err != nil {
			// log the error
			srv.logger.Error(errors.Wrap(err, "failed to shutdown the embed server when failing to volunteer self"))
			return nil, err
		}
	}

	// start election campaign
	srv.startCampaign()
	return srv, nil
}

// Shutdown stops the Embed instance
func (es *Embed) Shutdown() error {
	// acquire the lock
	es.mu.Lock()
	// release the lock once done
	defer es.mu.Unlock()

	// stop the client
	if err := es.stopClient(); err != nil {
		return err
	}

	// stop the underlying server
	if err := es.stopServer(); err != nil {
		return err
	}
	return nil
}

// Client returns the etcd client of ElasticEtcd
func (es *Embed) Client() *clientv3.Client {
	// acquire the lock
	es.mu.Lock()
	// release the lock once done
	defer es.mu.Unlock()
	return es.client
}

// Session returns the etcd session used by ElasticEtcd
func (es *Embed) Session() *concurrency.Session {
	// acquire the lock
	es.mu.Lock()
	// release the lock once done
	defer es.mu.Unlock()
	return es.session
}

// startServer starts the underlying etcd server
func (es *Embed) startServer(initialCluster string) error {
	// acquire the lock
	es.mu.Lock()
	// release the lock once done
	defer es.mu.Unlock()

	// check whether the server has started or not
	if es.hasStarted {
		return nil
	}

	// create the embed config
	es.embedConfig = embed.NewConfig()
	es.embedConfig.Name = es.config.Name()
	es.embedConfig.Dir = path.Join(es.config.DataDir(), "etcd.data")

	es.embedConfig.ListenClientUrls = es.config.ClientURLs()
	es.embedConfig.AdvertiseClientUrls = es.config.ClientURLs()
	es.embedConfig.ListenPeerUrls = es.config.PeerURLs()
	es.embedConfig.AdvertisePeerUrls = es.config.PeerURLs()

	es.embedConfig.Logger = "zap"
	es.embedConfig.LogLevel = "info"
	es.embedConfig.InitialCluster = es.embedConfig.InitialClusterFromName(es.config.Name())

	// here we are joining an existing cluster
	if initialCluster != "" {
		// override the default behavior and set the cluster existing flag state
		es.embedConfig.InitialCluster = initialCluster
		es.embedConfig.ClusterState = embed.ClusterStateFlagExisting
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

	// let us start the underlying server
	etcd, err := embed.StartEtcd(es.embedConfig)
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
		es.logger.Info("server ready..:)")
		// set the server field
		es.server = etcd
		// return
		return nil
	case <-time.After(es.config.startTimeout):
		es.logger.Info("starting embedded server timeout")
		// trigger a shutdown
		etcd.Server.Stop() // trigger a shutdown
		return errors.New("etcd embedded server took too long to start")
	case err := <-etcd.Err():
		// log the error
		es.logger.Error(errors.Wrap(err, "failed to start embedded server"))
		return err
	}
}

// stopServer stops the embedded etcd server.
func (es *Embed) stopServer() error {
	if !es.hasStarted || es.server == nil {
		return errors.New("etcd server not running")
	}

	// close the server
	es.server.Close()
	// set to nil
	es.server = nil
	es.hasStarted = false
	es.isStopped = true

	return nil
}

// startClient starts the etcd client and connects the Embed instance to the cluster.
func (es *Embed) startClient() error {
	// check whether the client is set or not
	if es.client != nil {
		return errors.New("client already exists")
	}

	// create the client config
	clientConfig := clientv3.Config{
		Endpoints:        es.config.EndPoints().StringSlice(),
		AutoSyncInterval: 30 * time.Second, // Update list of endpoints ever 30s.
		DialTimeout:      5 * time.Second,
		RejectOldCluster: true,
	}

	// create an instance of the client
	client, err := clientv3.New(clientConfig)
	// return the eventual error
	if err != nil {
		return err
	}

	// set the client
	es.client = client
	// Immediately sync and update your list of endpoints
	if err := es.client.Sync(es.client.Ctx()); err != nil {
		return err
	}

	// start a new session, which is needed for the watchers
	session, err := concurrency.NewSession(es.client)
	if err != nil {
		// try closing the client
		if err := es.client.Close(); err != nil {
			return err
		}
		return err
	}

	// set the session
	es.session = session

	return nil
}

// stopClient stops the etcd client
func (es *Embed) stopClient() error {
	// make sure we do have a client set
	if es.client == nil {
		return errors.New("no client present")
	}

	// First stop all the watchers
	close(es.stopWatchChan)
	es.watchers.Wait()

	// Then close the session
	if err := es.session.Close(); err != nil {
		return err
	}

	// Then close the etcd client
	if err := es.client.Close(); err != nil {
		return err
	}

	// set the client to NULL
	es.client = nil
	return nil
}

// watch watches for changes the given key and runs the handler when changes happen.
// watch also waits on the stop watch channel and stops watching when notified.
// All watchers in Embed must use this instead of starting their own etcd watchers.
func (es *Embed) watch(key string, handler func(clientv3.WatchResponse), opts ...clientv3.OpOption) {
	es.watchers.Add(1)
	go func() {
		defer es.watchers.Done()

		wch := es.client.Watch(es.client.Ctx(), key, opts...)
		for {
			select {
			case resp := <-wch:
				if resp.Canceled {
					return
				}
				handler(resp)
			case <-es.stopWatchChan:
				return
			}
		}
	}()
}
