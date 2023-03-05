package cluster

import (
	"context"
	"net"
	"time"

	"github.com/pkg/errors"
	"github.com/shaj13/raft"
	"github.com/shaj13/raft/transport"
	"github.com/shaj13/raft/transport/raftgrpc"
	goaktpb "github.com/tochemey/goakt/internal/goaktpb/v1"
	"github.com/tochemey/goakt/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Node represents the raft node
type Node struct {
	raftNode *raft.Node
	fsm      *FSM

	// specifies the raft server address
	raftAddr string
	// specifies the cluster join address
	joinAddr string
	// specifies the WAL state directory
	stateDIR string
	// specifies the start options
	startOpts []raft.StartOption
	// specifies the options
	opts []raft.Option

	raftServer *grpc.Server
	logger     log.Logger
}

// NewNode creates an instance of Node
func NewNode(raftAddr string, stateDIR string, logger log.Logger) *Node {
	// create the options
	opts := []raft.Option{
		raft.WithStateDIR(stateDIR),
		raft.WithLinearizableReadSafe(),
	}
	// create an instance of FSM
	fsm := NewFSM(logger)
	// create an instance of the node
	node := raft.NewNode(fsm, transport.GRPC, opts...)
	// create the initial start options
	startOpts := []raft.StartOption{
		raft.WithAddress(raftAddr),
	}

	// register the grpc service for the raft server
	raftgrpc.Register(
		raftgrpc.WithDialOptions(grpc.WithTransportCredentials(insecure.NewCredentials())),
	)

	// create an instance of Node
	return &Node{
		raftNode:   node,
		fsm:        fsm,
		raftAddr:   raftAddr,
		joinAddr:   "",
		stateDIR:   stateDIR,
		startOpts:  startOpts,
		opts:       opts,
		logger:     logger,
		raftServer: grpc.NewServer(),
	}
}

// Start starts the node. When the join address is not set a brand-new cluster is started.
// However, when the join address is set the given node joins an existing cluster at the joinAddr.
func (n *Node) Start(joinAddr string) error {
	// when the join address is set, it means this node is joining an existing cluster
	if joinAddr != "" {
		// set the join address
		n.joinAddr = joinAddr
		// joining an existing cluster
		n.startOpts = append(n.startOpts, raft.WithFallback(
			raft.WithJoin(joinAddr, time.Second),
			raft.WithRestart(),
		))
	} else {
		// starting a brand-new cluster
		n.startOpts = append(n.startOpts, raft.WithFallback(
			raft.WithInitCluster(),
			raft.WithRestart(),
		))
	}
	// start the raft server
	go n.startRaftServer()
	// start the underlying node
	if err := n.raftNode.Start(n.startOpts...); err != nil && err != raft.ErrNodeStopped {
		return err
	}
	return nil
}

// Stop stops the node gracefully
func (n *Node) Stop() error {
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
func (n *Node) Peers() []*goaktpb.Peer {
	// create an empty list of peers
	var peers []*goaktpb.Peer
	// get the members of this node
	members := n.raftNode.Members()
	// iterate the members list
	// TODO augment the Peer data type to add Peer Type and more
	for _, member := range members {
		peers = append(peers, &goaktpb.Peer{
			NodeId:      member.ID(),
			HostAndPort: member.Address(),
		})
	}

	return peers
}

// startRaftServer start the raft server.
// This facilitates the communication between raft nodes
func (n *Node) startRaftServer() {
	// create an instance of the TCP listener
	listener, err := net.Listen("tcp", n.raftAddr)
	// return the error when there is an error
	if err != nil {
		panic(errors.Wrap(err, "failed to create a TCP listener for the raft server"))
	}
	// start the server
	if err := n.raftServer.Serve(listener); err != nil {
		panic(errors.Wrap(err, "failed to start the raft server"))
	}
}
