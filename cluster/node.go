package cluster

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/pkg/errors"
	"github.com/shaj13/raft"
	"github.com/shaj13/raft/transport"
	"github.com/shaj13/raft/transport/raftgrpc"
	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Peer holds the node peer
type Peer struct {
	PeerID  uint64
	Address string
}

// node represents the raft node
type node struct {
	raftNode *raft.Node
	fsm      *FSM

	// specifies the WAL state directory
	stateDIR string
	// specifies the options
	opts   []raft.Option
	logger log.Logger

	// list of peers mapping their addr and node id
	peersMap       map[string]uint64
	disco          discovery.Discovery
	nodeURL        string
	raftServerPort int
	raftServer     *grpc.Server
}

// newNode creates an instance of node
func newNode(port int, disco discovery.Discovery, logger log.Logger) *node {
	//// grab any available port to start the node
	//port, _ = availablePort()
	// let us set the node URL
	nodeURL := fmt.Sprintf(":%d", port)
	// add debug logging
	logger.Debugf("raft node URL=%s", nodeURL)
	// create the wal dir
	stateDIR := fmt.Sprintf("node-%d-state", port)
	// create the options
	opts := []raft.Option{
		raft.WithStateDIR(stateDIR),
		raft.WithLinearizableReadSafe(),
	}
	// create an instance of FSM
	fsm := NewFSM(logger)
	// create an instance of the node
	raftNode := raft.NewNode(fsm, transport.GRPC, opts...)
	// create the raft server
	raftServer := grpc.NewServer()
	// register the grpc service for the raft server
	raftgrpc.Register(
		raftgrpc.WithDialOptions(grpc.WithTransportCredentials(insecure.NewCredentials())),
	)
	// raft register the raft server
	raftgrpc.RegisterHandler(raftServer, raftNode.Handler())
	// create an instance of node
	return &node{
		raftNode:   raftNode,
		fsm:        fsm,
		stateDIR:   stateDIR,
		opts:       opts,
		logger:     logger,
		disco:      disco,
		nodeURL:    nodeURL,
		raftServer: raftServer,
	}
}

// Start starts the node. When the join address is not set a brand-new cluster is started.
// However, when the join address is set the given node joins an existing cluster at the joinAddr.
func (n *node) Start(ctx context.Context) error {
	var (
		// create a variable to hold the discovered nodes
		discoNodes []*discovery.Node
		// variable to hold error
		err error
	)
	// let us delay for sometime to make sure we have discovered all nodes
	// FIXME: this is an approximation
	time.Sleep(time.Second)

	// let us grab the existing nodes in the cluster
	discoNodes, err = n.disco.Nodes(ctx)
	// handle the error
	if err != nil {
		n.logger.Error(errors.Wrap(err, "failed to fetch existing nodes in the cluster"))
		return err
	}
	// add some logging
	n.logger.Debugf("%s has discovered %d nodes", n.disco.ID(), len(discoNodes))

	// let us filter the discovered nodes by excluding the current node
	//filtered := make([]*discovery.Node, 0, len(discoNodes))
	//// iterate the discovered nodes
	//for _, discoNode := range discoNodes {
	//	// exclude the given node from the list
	//	if discoNode.GetURL() == n.nodeURL {
	//		continue
	//	}
	//	filtered = append(filtered, discoNode)
	//}

	// let us define the raft members
	var members []raft.RawMember
	for _, discoNode := range discoNodes {
		members = append(members, raft.RawMember{
			Address: discoNode.GetURL(),
		})
	}

	// add to the members this current node
	members = append(members, raft.RawMember{Address: n.nodeURL})
	// let us define the start options
	opts := []raft.StartOption{
		raft.WithInitCluster(),
		raft.WithMembers(members...),
	}

	// start the raft server
	go func() {
		lis, err := net.Listen("tcp", n.nodeURL)
		if err != nil {
			n.logger.Fatal(err)
		}

		err = n.raftServer.Serve(lis)
		if err != nil {
			n.logger.Fatal(err)
		}
	}()

	// start the underlying node
	if err := n.raftNode.Start(opts...); err != nil && err != raft.ErrNodeStopped {
		return err
	}
	return nil
}

// Stop stops the node gracefully
func (n *node) Stop() error {
	// stop the raft server
	n.raftServer.GracefulStop()
	// stop the underlying raft node
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	// shutdown the raft node
	if err := n.raftNode.Shutdown(ctx); err != nil {
		panic(errors.Wrap(err, "failed to shutdown the underlying raft node"))
	}
	return nil
}

// Peers returns the list of node Peers
func (n *node) Peers() []*Peer {
	// create an empty list of peers
	var peers []*Peer
	// get the members of this node
	members := n.raftNode.Members()
	// iterate the members list
	// TODO augment the Peer data type to add Peer Type and more
	for _, member := range members {
		peers = append(peers, &Peer{
			PeerID:  member.ID(),
			Address: member.Address(),
		})
	}

	return peers
}
