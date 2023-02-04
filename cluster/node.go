package cluster

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/memberlist"
	cmp "github.com/orcaman/concurrent-map/v2"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/log"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"github.com/tochemey/goakt/pkg/grpc"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// Node is a gRPC serving node of the cluster
type Node struct {
	// host and node ports for gossiping
	bindAddr string
	// bind port
	bindPort int
	// grpcPort
	grpcPort int
	// node id
	id string

	mu           sync.Mutex
	memberlist   *memberlist.Memberlist
	broadcasts   *memberlist.TransmitLimitedQueue
	memberConfig *memberlist.Config
	leaveTimeout time.Duration

	// node internal state - this is the actual nodeState being gossiped
	nodeState *pb.NodeState
	// useful to share node details with other nodes
	metadata map[string]string

	grpcServer grpc.Server
	logger     log.Logger
	peers      cmp.ConcurrentMap[string, *pb.Peer]
}

// enforce compilation error
var _ pb.GossipServiceServer = &Node{}

// NewNode creates new gRPC serving node but does not start serving
func NewNode(config *NodeConfig) (*Node, error) {
	// create a member list config
	conf := memberlist.DefaultLocalConfig()

	// define a nodeName variable and set it to the incoming config ID
	// and assert whether it is empty or not
	nodeName := config.ID
	// check whether the node id is set or not
	if len(config.ID) == 0 {
		// grab the machine host name and ignore the error
		hostname, err := os.Hostname()
		// handle the error
		if err != nil {
			return nil, errors.Wrap(err, "node ID is not set")
		}
		// set the configuration name
		nodeName = fmt.Sprintf("%s-%s", hostname, uuid.NewString())
	}

	// append the configuration with the name, address and ports
	conf.Name = nodeName
	conf.BindAddr = config.BindHost
	conf.BindPort = config.BindPort
	conf.AdvertisePort = config.BindPort
	conf.AdvertiseAddr = config.BindHost

	// create the metadata to pass along to other node
	md := make(map[string]string, 1)
	md["bindPort"] = strconv.Itoa(config.BindPort)

	// create the Node instance
	node := &Node{
		bindAddr:     config.BindHost,
		bindPort:     config.BindPort,
		grpcPort:     config.GRPCPort,
		mu:           sync.Mutex{},
		memberlist:   nil,
		broadcasts:   nil,
		memberConfig: conf,
		leaveTimeout: 0,
		nodeState:    &pb.NodeState{},
		metadata:     nil,
		grpcServer:   nil,
		logger:       config.Logger,
		peers:        cmp.New[*pb.Peer](),
		id:           nodeName,
	}

	// let us add the Peers
	for _, peer := range config.Peers {
		node.peers.Set(peer.GetId(), peer)
	}

	// set the various delegates
	conf.Events = node
	conf.Delegate = node

	return node, nil
}

// Start async runs gRPC server and joins cluster
func (n *Node) Start(ctx context.Context) error {
	// start the service
	if err := n.serve(ctx); err != nil {
		return err
	}
	// join the cluster
	if err := n.joinCluster(ctx); err != nil {
		return err
	}
	return nil
}

// Shutdown stops gRPC server and leaves cluster
func (n *Node) Shutdown(ctx context.Context) error {
	// stop gracefully the grpc service
	n.grpcServer.Stop(ctx)
	// leave the cluster
	if err := n.memberlist.Leave(n.leaveTimeout); err != nil {
		return err
	}
	// shutdown the
	if err := n.memberlist.Shutdown(); err != nil {
		return err
	}
	return nil
}

// RegisterService register the node as a grpc service
func (n *Node) RegisterService(server *ggrpc.Server) {
	pb.RegisterGossipServiceServer(server, n)
}

// Put adds actor meta to the local store
func (n *Node) Put(ctx context.Context, request *pb.PutRequest) (*pb.PutResponse, error) {
	// make a copy of the incoming request
	cloned := proto.Clone(request).(*pb.PutRequest)
	// update the node state
	n.PutActorMeta(cloned.GetNodeId(), cloned.GetActorMeta())
	// return the response
	return &pb.PutResponse{
		NodeId:    cloned.GetNodeId(),
		ActorMeta: cloned.GetActorMeta(),
	}, nil
}

// Get fetches actor meta from the local store
func (n *Node) Get(ctx context.Context, request *pb.GetRequest) (*pb.GetResponse, error) {
	// make a copy of the incoming request
	cloned := proto.Clone(request).(*pb.GetRequest)
	// query the node state
	actorMeta := n.GetActorMeta(cloned.GetNodeId())
	return &pb.GetResponse{
		NodeId:    cloned.GetNodeId(),
		ActorMeta: actorMeta,
	}, nil
}

// Address returns the Node address
func (n *Node) Address() string {
	return n.memberlist.LocalNode().FullAddress().Addr
}

// ID returns the Node id
func (n *Node) ID() string {
	return n.memberlist.LocalNode().Name
}

// NodeMeta is used to retrieve meta-data about the current node
// when broadcasting an alive message. Its length is limited to
// the given byte size. This metadata is available in the Node structure.
func (n *Node) NodeMeta(limit int) []byte {
	n.mu.Lock()
	defer n.mu.Unlock()

	var network bytes.Buffer
	encoder := gob.NewEncoder(&network)
	err := encoder.Encode(n.metadata)
	if err != nil {
		n.logger.Fatal("failed to encode metadata", err)
	}
	return network.Bytes()
}

// NotifyMsg is called when a user-data message is received.
// Care should be taken that this method does not block, since doing
// so would block the entire UDP packet receive loop. Additionally, the byte
// slice may be modified after the call returns, so it should be copied if needed
func (n *Node) NotifyMsg(b []byte) {
	// not expecting messages - push/pull sync should suffice
}

// GetBroadcasts is called when user data messages can be broadcast.
// It can return a list of buffers to send. Each buffer should assume an
// overhead as provided with a limit on the total byte size allowed.
// The total byte size of the resulting data to send must not exceed
// the limit. Care should be taken that this method does not block,
// since doing so would block the entire UDP packet receive loop.
func (n *Node) GetBroadcasts(overhead, limit int) [][]byte {
	return n.broadcasts.GetBroadcasts(overhead, limit)
}

// LocalState is used for a TCP Push/Pull. This is sent to
// the remote side in addition to the membership information. Any
// data can be sent here. See MergeRemoteState as well. The `join`
// boolean indicates this is for a join instead of a push/pull.
func (n *Node) LocalState(join bool) []byte {
	n.mu.Lock()
	defer n.mu.Unlock()

	// let us marshal the node state
	bytes, err := proto.Marshal(n.nodeState)
	if err != nil {
		n.logger.Fatal("failed to encode local state", err)
	}
	return bytes
}

// MergeRemoteState is invoked after a TCP Push/Pull. This is the
// state received from the remote side and is the result of the
// remote side's LocalState call. The 'join'
// boolean indicates this is for a join instead of a push/pull.
func (n *Node) MergeRemoteState(buf []byte, join bool) {
	n.mu.Lock()
	defer n.mu.Unlock()

	// unmarshal the bytes array
	nodeState := new(pb.NodeState)
	if err := proto.Unmarshal(buf, nodeState); err != nil {
		n.logger.Fatal("failed to decode remote state", err)
	}

	// let us update the local node state
	for key, meta := range nodeState.GetStates() {
		if !proto.Equal(n.nodeState.GetStates()[key], meta) {
			n.nodeState.States[key] = meta
		}
	}
	n.logger.Debug("successfully merged remote state.")
}

// NotifyJoin is invoked when a node is detected to have joined.
// The Node argument must not be modified.
func (n *Node) NotifyJoin(node *memberlist.Node) {
	// add it to the peers map
	n.peers.SetIfAbsent(node.Name, &pb.Peer{
		Id:          node.Name,
		HostAndPort: fmt.Sprintf("%s:%d", node.Addr.To4().String(), node.Port),
	})
}

// NotifyLeave is invoked when a node is detected to have left.
// The Node argument must not be modified.
func (n *Node) NotifyLeave(node *memberlist.Node) {
	// remove the node from the peers map
	n.peers.Remove(node.Name)
}

// NotifyUpdate is invoked when a node is detected to have
// updated, usually involving the metadata. The Node argument
// must not be modified.
func (n *Node) NotifyUpdate(node *memberlist.Node) {
	// do nothing for now
}

// serve starts the grpc serve
func (n *Node) serve(ctx context.Context) error {
	// build the grpc server
	config := &grpc.Config{
		ServiceName:      n.id,
		GrpcPort:         n.grpcPort,
		GrpcHost:         n.bindAddr,
		TraceEnabled:     false, // TODO
		TraceURL:         "",    // TODO
		EnableReflection: false,
	}

	// build the grpc service
	svr, err := grpc.
		GetServerBuilder(config).
		WithService(n).
		Build()

	// handle the error
	if err != nil {
		n.logger.Error("failed to listen on %s: %v", n.bindAddr, err)
		return err
	}

	// set the server
	n.grpcServer = svr
	// start the server
	n.grpcServer.Start(ctx)
	return nil
}

// joinCluster help join the memberlist
func (n *Node) joinCluster(ctx context.Context) error {
	// variable holding error
	var err error
	// create the memberlist
	n.memberlist, err = memberlist.Create(n.memberConfig)
	// handle the error
	if err != nil {
		// log the error
		n.logger.Error("failed to init memberlist", err)
		return err
	}

	// add members to cluster
	if n.peers.Count() > 0 {
		addresses := make([]string, 0, n.peers.Count())
		for _, peer := range n.peers.Items() {
			addresses = append(addresses, peer.GetHostAndPort())
		}
		// join the cluster
		_, err := n.memberlist.Join(addresses)
		// handle the join error
		if err != nil {
			n.logger.Error(errors.Wrap(err, "failed to join cluster"))
			return err
		}
	}

	// create the broadcast list
	br := &memberlist.TransmitLimitedQueue{
		NumNodes: func() int {
			return n.memberlist.NumMembers()
		},
		RetransmitMult: 3,
	}

	// set the broadcasts
	n.broadcasts = br
	return nil
}

// PutActorMeta adds config property to config store
func (n *Node) PutActorMeta(key string, value *pb.ActorMeta) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nodeState.States[key] = value
}

// GetActorMeta returns a property value
func (n *Node) GetActorMeta(key string) *pb.ActorMeta {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.nodeState.GetStates()[key]
}
