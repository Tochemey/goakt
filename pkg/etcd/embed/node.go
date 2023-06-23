package embed

import (
	"os"
	"path"
	"sync"
	"time"

	"github.com/coreos/etcd/pkg/types"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/log"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/client/v3/concurrency"
	"go.etcd.io/etcd/server/v3/embed"
)

// Node embeds a etcd server
type Node struct {
	// define the underlying etcd server
	server *embed.Etcd
	// define the etcd client
	client *clientv3.Client

	session *concurrency.Session

	config    *Config
	isStopped bool
	isReady   bool

	mu sync.Mutex

	logger log.Logger
}

// NewNode creates an instance of Node
func NewNode(config *Config) *Node {
	// create the instance of the embed
	node := &Node{
		config:    config,
		isStopped: false,
		isReady:   false,
		mu:        sync.Mutex{},
		logger:    config.Logger(),
	}

	return node
}

// Start starts the underlying etcd server
func (n *Node) Start() error {
	// acquire the lock
	n.mu.Lock()
	// release the lock once done
	defer n.mu.Unlock()

	// check whether the server has started or not
	if n.server != nil {
		return nil
	}

	// add some debug logging
	if n.config.InitialCluster() != "" {
		n.logger.Debugf("Starting the embed etcd server with initial cluster=[%s]", n.config.InitialCluster())
	} else {
		n.logger.Debug("Starting the embed etcd server...")
	}

	// create the embed config
	embedConfig := embed.NewConfig()
	embedConfig.Name = n.config.Name()
	embedConfig.Dir = path.Join(n.config.DataDir(), "etcd.data", n.config.Name())

	// set the various URLs
	embedConfig.ListenClientUrls = n.config.ClientURLs()
	embedConfig.AdvertiseClientUrls = n.config.ClientURLs()
	embedConfig.ListenPeerUrls = n.config.PeerURLs()
	embedConfig.AdvertisePeerUrls = n.config.PeerURLs()

	// set the logger
	embedConfig.Logger = "zap"
	embedConfig.LogLevel = n.config.logger.LogLevel().String()
	embedConfig.InitialCluster = embedConfig.InitialClusterFromName(n.config.Name())

	// generate a unique cluster token
	embedConfig.InitialClusterToken = "goakt-cluster"

	// override the initial cluster and the cluster state when
	if n.config.InitialCluster() != "" {
		embedConfig.InitialCluster = n.config.InitialCluster()
		// here we are joining an existing cluster
		if n.config.join {
			embedConfig.ClusterState = embed.ClusterStateFlagExisting
		}
		// also let us make sure to clear the data dir
		// If starting with non-empty initial cluster, delete the datadir if it
		// exists. The etcd server will be brought up as a new server and old data
		// being present will prevent it.
		// Starting with an empty initial cluster implies that we are in a single
		// node cluster, so we need to keep the etcd data.
		if err := os.RemoveAll(n.config.DataDir()); err != nil {
			n.logger.Panic(errors.Wrap(err, "failed to remove the data dir"))
		}
	}

	n.logger.Debugf("Embed etcd server with initial cluster=[%s]", embedConfig.InitialCluster)

	// let us start the underlying server
	etcd, err := embed.StartEtcd(embedConfig)
	// handle the error
	if err != nil {
		n.logger.Error(errors.Wrap(err, "failed to start the etcd embedded server"))
		return err
	}

	// The returned embed.Etcd.Server instance is not guaranteed to have
	// joined the cluster yet. Wait on the embed.Etcd.Server.ReadyNotify()
	// channel to know when it's ready for use. Stop waiting after the start timeout
	select {
	case <-etcd.Server.ReadyNotify():
		// add logging information
		n.logger.Info("embed etcd server started..:)")
		// set the server field
		n.server = etcd
		n.isReady = true

		// connect to the cluster to handle membership
		if err := n.startClient(n.config.EndPoints().StringSlice()); err != nil {
			// log the error
			n.logger.Panic(errors.Wrap(err, "failed to start as a client"))
			// trigger a shutdown
			etcd.Server.Stop() // trigger a shutdown
			return err
		}

		// return
		return nil
	case <-time.After(n.config.StartTimeout()):
		n.logger.Info("starting embedded server timeout")
		// trigger a shutdown
		etcd.Server.Stop() // trigger a shutdown
		return errors.New("embed etcd server took too long to start")
	case err := <-etcd.Err():
		// log the error
		n.logger.Error(errors.Wrap(err, "failed to start embed etcd server"))
		return err
	}
}

// Stop stops the Node instance
func (n *Node) Stop() error {
	// acquire the lock
	n.mu.Lock()
	// release the lock once done
	defer n.mu.Unlock()

	if n.server == nil {
		return errors.New("etcd server not running")
	}

	// stop the client
	if err := n.stopClient(); err != nil {
		return err
	}

	// close the server
	n.server.Close()
	// set to nil
	n.server = nil
	n.isStopped = true
	n.isReady = false
	// let us remove the data dir
	if err := os.RemoveAll(n.config.DataDir()); err != nil {
		n.logger.Panic(errors.Wrap(err, "failed to remove the data dir"))
	}
	return nil
}

// ClientURLs returns the list of client endpoint
func (n *Node) ClientURLs() types.URLs {
	// acquire the lock
	n.mu.Lock()
	// release the lock once done
	defer n.mu.Unlock()
	return n.config.ClientURLs()
}

// IsLeader states whether the given node is a leader or not
func (n *Node) IsLeader() bool {
	// acquire the lock
	n.mu.Lock()
	// release the lock once done
	defer n.mu.Unlock()
	return n.server.Server.Leader().String() == n.server.Server.ID().String()
}

// LeaderID returns the leader id
func (n *Node) LeaderID() string {
	// acquire the lock
	n.mu.Lock()
	// release the lock once done
	defer n.mu.Unlock()
	return n.server.Server.Leader().String()
}

// ID returns the given node id
func (n *Node) ID() string {
	// acquire the lock
	n.mu.Lock()
	// release the lock once done
	defer n.mu.Unlock()
	return n.server.Server.ID().String()
}

// Members returns the given node members
func (n *Node) Members() ([]*etcdserverpb.Member, error) {
	// acquire the lock
	n.mu.Lock()
	// release the lock once done
	defer n.mu.Unlock()
	// create a context
	ctx := n.client.Ctx()
	// query for members
	resp, err := n.client.MemberList(ctx)
	// handle the error
	if err != nil {
		return nil, err
	}

	return resp.Members, nil
}

// RemoveMember removes a given member from the memberlist
func (n *Node) RemoveMember(member *etcdserverpb.Member) error {
	// acquire the lock
	n.mu.Lock()
	// release the lock once done
	defer n.mu.Unlock()
	// create a context
	ctx := n.client.Ctx()
	// execute the request to remove the member
	_, err := n.client.MemberRemove(ctx, member.GetID())
	// handle the error
	if err != nil {
		return err
	}
	return nil
}

// UpdateMember updates a given member
func (n *Node) UpdateMember(member *etcdserverpb.Member) error {
	// acquire the lock
	n.mu.Lock()
	// release the lock once done
	defer n.mu.Unlock()
	// create a context
	ctx := n.client.Ctx()
	// execute the update request
	_, err := n.client.MemberUpdate(ctx, member.GetID(), member.GetPeerURLs())
	// handle the error
	if err != nil {
		return err
	}
	return nil
}

// Client returns the etcd client of Embed
func (n *Node) Client() *clientv3.Client {
	// acquire the lock
	n.mu.Lock()
	// release the lock once done
	defer n.mu.Unlock()
	return n.client
}

// Session returns the etcd session used by Embed
func (n *Node) Session() *concurrency.Session {
	// acquire the lock
	n.mu.Lock()
	// release the lock once done
	defer n.mu.Unlock()
	return n.session
}

// startClient starts the etcd client and connects the Node instance to the cluster.
func (n *Node) startClient(endpoints []string) error {
	// check whether the client is set or not
	if n.client != nil {
		return errors.New("client already exists")
	}

	// create the client config
	clientConfig := clientv3.Config{
		Endpoints:        endpoints,
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
	n.client = client
	// Immediately sync and update your list of endpoints
	if err := n.client.Sync(n.client.Ctx()); err != nil {
		return err
	}

	// start a new session, which is needed for the watchers
	session, err := concurrency.NewSession(n.client)
	if err != nil {
		// try closing the client
		if err := n.client.Close(); err != nil {
			return err
		}
		return err
	}

	// set the session
	n.session = session

	return nil
}

// stopClient stops the etcd client
func (n *Node) stopClient() error {
	// make sure we do have a client set
	if n.client == nil {
		return errors.New("no client present")
	}

	// Then close the session
	if err := n.session.Close(); err != nil {
		return err
	}

	// Then close the etcd client
	if err := n.client.Close(); err != nil {
		return err
	}

	// set the client to NULL
	n.client = nil
	return nil
}
