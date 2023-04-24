package cluster

import (
	"context"

	"github.com/bufbuild/connect-go"
	"github.com/tochemey/goakt/discovery"
	goaktpb "github.com/tochemey/goakt/internal/goakt/v1"
	"github.com/tochemey/goakt/internal/goakt/v1/goaktv1connect"
	"github.com/tochemey/goakt/internal/http2"
	"github.com/tochemey/goakt/internal/raft"
	"github.com/tochemey/goakt/log"
	"go.etcd.io/etcd/raft/v3/raftpb"
)

// Cluster represents the cluster
type Cluster struct {
	config            *Config
	logger            *log.Log
	disco             discovery.Discovery
	raftProposeC      chan []byte
	raftConfChangeC   chan raftpb.ConfChange
	raftServiceClient goaktv1connect.RaftServiceClient
}

// New creates an instance of Cluster
func New(config *Config, logger *log.Log, disco discovery.Discovery) *Cluster {
	// create the instance
	return &Cluster{
		logger: logger,
		disco:  disco,
		config: config,
	}
}

// Start the cluster given the node config
func (c *Cluster) Start(ctx context.Context) error {
	// check the existence of nodes
	nodes, err := c.disco.Nodes(ctx)
	// handle the error
	if err != nil {
		return err
	}

	// join variables
	var (
		joinAddrs = []string{c.config.GetURL()}
		join      bool
	)

	// there are no nodes available aside the given node
	if len(nodes) > 1 {
		// iterate the list of nodes and get their URLs
		for _, peer := range nodes {
			joinAddrs = append(joinAddrs, peer.GetURL())
		}
		// set join to true
		join = true
	}

	// create a channel for cluster proposition
	proposeChan := make(chan []byte)
	// create a channel for cluster change
	confChangeChan := make(chan raftpb.ConfChange)
	// set the cluster fields
	c.raftProposeC = proposeChan
	c.raftConfChangeC = confChangeChan
	// define a store
	var store *raft.WireActorsStore
	// create an anonymous function to grab snapshot
	getSnapshot := func() ([]byte, error) { return store.GetSnapshot() }
	// create an instance of raft node
	commitC, errorC, snapshotterReady := raft.NewNode(int(c.config.NodeID), joinAddrs, join, getSnapshot, c.raftProposeC, c.raftConfChangeC, c.logger)
	// set the store instance
	store = raft.NewWireActorsStore(<-snapshotterReady, c.raftProposeC, commitC, errorC, c.logger)
	// create an instance of the raft service
	raftService := raft.NewService(store, c.config.NodePort, c.raftConfChangeC, c.logger)
	// create the service client
	c.raftServiceClient = goaktv1connect.NewRaftServiceClient(
		http2.GetClient(),
		http2.GetURL(c.config.NodeHost, c.config.NodePort),
		connect.WithGRPC())

	// start the raft service
	go raftService.ListenAndServe(errorC)
	return nil
}

// Stop stops the cluster
func (c *Cluster) Stop(ctx context.Context) error {
	// close the various channels
	close(c.raftProposeC)
	close(c.raftConfChangeC)
	// notify the service to remove the node
	if _, err := c.raftServiceClient.RemoveNode(ctx, connect.NewRequest(&goaktpb.RemoveNodeRequest{
		NodeId:  c.config.NodeID,
		Address: c.config.GetURL(),
		// handle the error
	})); err != nil {
		return err
	}
	return nil
}
