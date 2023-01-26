package cluster

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/hashicorp/memberlist"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/log"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"google.golang.org/grpc"
)

// Node is a gRPC serving node of the cluster
type Node struct {
	pb.NodeStateReplicationServiceServer

	// host and node ports for gossiping
	gossipAddr string
	// grpc port
	grpcPort int

	// addr:port of any node in the cluster to join to; empty if it's the first node
	joinNodeAddr string

	mtx          sync.RWMutex
	cluster      *memberlist.Memberlist
	broadcasts   *memberlist.TransmitLimitedQueue
	memberConfig *memberlist.Config

	// Holds the node data state; it's also the Delegate used by memberlist to gossip state
	stateStore *Storage

	grpcServer *grpc.Server
	logger     log.Logger
}

// NewNode creates new gRPC serving node but does not start serving
func NewNode(name string, addr string, grpcPort, gossipPort int, joinNodeAddr string, logger log.Logger) *Node {
	// create a member list config
	config := memberlist.DefaultLocalConfig()
	config.Name = name
	config.BindAddr = addr
	config.BindPort = gossipPort
	config.AdvertisePort = config.BindPort

	md := make(map[string]string, 1)
	md["grpcPort"] = strconv.Itoa(grpcPort)
	store := NewStorage(md, logger)
	config.Delegate = store

	return &Node{
		gossipAddr:   addr,
		grpcPort:     grpcPort,
		joinNodeAddr: joinNodeAddr,
		stateStore:   store,
		memberConfig: config,
	}
}

// Start async runs gRPC server and joins cluster
func (n *Node) Start() chan error {
	errChan := make(chan error)
	go n.serve(errChan)
	go n.joinCluster(errChan)
	return errChan
}

// Shutdown stops gRPC server and leaves cluster
func (n *Node) Shutdown() {
	n.grpcServer.GracefulStop()
	// TODO handle the errors
	_ = n.cluster.Leave(15 * time.Second)
	_ = n.cluster.Shutdown()
}

func (n *Node) serve(errChan chan error) {
	lis, err := net.Listen("tcp", fmt.Sprintf("%s:%d", n.gossipAddr, n.grpcPort))
	if err != nil {
		n.logger.Error("failed to listen on %s: %v", n.gossipAddr, err)
		errChan <- err
	}
	n.logger.Infof("grpc api serving on %s:%d", n.gossipAddr, n.grpcPort)

	n.grpcServer = grpc.NewServer()
	pb.RegisterNodeStateReplicationServiceServer(n.grpcServer, n)
	if err := n.grpcServer.Serve(lis); err != nil {
		n.logger.Error("failed to serve", err)
		errChan <- err
	}
}

func (n *Node) joinCluster(errChan chan error) {
	var err error
	n.cluster, err = memberlist.Create(n.memberConfig)
	if err != nil {
		n.logger.Error("failed to init memberlist", err)
		errChan <- err
	}

	var nodeAddr string
	if n.joinNodeAddr != "" {
		n.logger.Debugf("not the first node, joining %s...", n.joinNodeAddr)
		nodeAddr = n.joinNodeAddr
	} else {
		n.logger.Debug("first node of the cluster...")
		nodeAddr = fmt.Sprintf("%s:%d", n.gossipAddr, n.memberConfig.BindPort)
	}
	_, err = n.cluster.Join([]string{nodeAddr})
	if err != nil {
		n.logger.Error(errors.Wrap(err, "failed to join cluster"))
		errChan <- err
	}

	n.logger.Infof("successfully joined cluster via %s", nodeAddr)
}
