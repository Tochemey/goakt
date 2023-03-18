package cluster

import (
	"context"
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

// node represents the raft node
type node struct {
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
	opts   []raft.Option
	logger log.Logger
}

// newNode creates an instance of node
func newNode(raftAddr string, stateDIR string, logger log.Logger) *node {
	// create the options
	opts := []raft.Option{
		raft.WithStateDIR(stateDIR),
		raft.WithLinearizableReadSafe(),
	}
	// create an instance of FSM
	fsm := NewFSM(logger)
	// create an instance of the node
	raftNode := raft.NewNode(fsm, transport.GRPC, opts...)
	// create the initial start options
	startOpts := []raft.StartOption{
		raft.WithAddress(raftAddr),
	}

	// register the grpc service for the raft server
	raftgrpc.Register(
		raftgrpc.WithDialOptions(grpc.WithTransportCredentials(insecure.NewCredentials())),
	)

	// create an instance of node
	return &node{
		raftNode:  raftNode,
		fsm:       fsm,
		raftAddr:  raftAddr,
		joinAddr:  "",
		stateDIR:  stateDIR,
		startOpts: startOpts,
		opts:      opts,
		logger:    logger,
	}
}

// Start starts the node. When the join address is not set a brand-new cluster is started.
// However, when the join address is set the given node joins an existing cluster at the joinAddr.
func (n *node) Start(joinAddr *string) error {
	// when the join address is set, it means this node is joining an existing cluster
	if joinAddr != nil {
		// set the join address
		n.joinAddr = *joinAddr
		// joining an existing cluster
		n.startOpts = append(n.startOpts, raft.WithFallback(
			raft.WithJoin(*joinAddr, time.Second),
			raft.WithRestart(),
		))
	} else {
		// starting a brand-new cluster
		n.startOpts = append(n.startOpts, raft.WithFallback(
			raft.WithInitCluster(),
			raft.WithRestart(),
		))
	}
	// start the underlying node
	if err := n.raftNode.Start(n.startOpts...); err != nil && err != raft.ErrNodeStopped {
		return err
	}
	return nil
}

// Stop stops the node gracefully
func (n *node) Stop() error {
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
func (n *node) Peers() []*goaktpb.Peer {
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

// RaftAddr returns the raft server address
func (n *node) RaftAddr() string {
	return n.raftAddr
}
