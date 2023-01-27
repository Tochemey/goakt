package cluster

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/hashicorp/memberlist"
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

	// addr:port of any node in the memberlist to join to; empty if it's the first node
	joinNodeAddr string

	mu           sync.Mutex
	memberlist   *memberlist.Memberlist
	broadcasts   *memberlist.TransmitLimitedQueue
	memberConfig *memberlist.Config

	// node internal state - this is the actual nodeState being gossiped
	nodeState *pb.NodeState
	// useful to share node details with other nodes
	metadata map[string]string

	grpcServer grpc.Server
	logger     log.Logger
}

// enforce compilation error
var _ pb.NodeStateReplicationServiceServer = &Node{}

// NewNode creates new gRPC serving node but does not start serving
func NewNode(name string, bindAddr string, bindPort, gossipPort int, joinNodeAddr string, logger log.Logger) *Node {
	// create a member list config
	config := memberlist.DefaultLocalConfig()

	// append the configuration with the name, address and ports
	config.Name = name
	config.BindAddr = bindAddr
	config.BindPort = gossipPort
	config.AdvertisePort = config.BindPort
	config.Events = nil // TODO may be useful

	// create the metadata to pass along to other node
	md := make(map[string]string, 1)
	md["bindPort"] = strconv.Itoa(bindPort)

	// create the Node instance
	node := &Node{
		bindAddr:     bindAddr,
		bindPort:     bindPort,
		joinNodeAddr: joinNodeAddr,
		nodeState:    &pb.NodeState{},
		memberConfig: config,
		mu:           sync.Mutex{},
		logger:       logger,
	}

	config.Delegate = node

	return node
}

// Start async runs gRPC server and joins cluster
func (s *Node) Start(ctx context.Context) chan error {
	errChan := make(chan error)
	go s.serve(ctx, errChan)
	go s.joinCluster(errChan)
	return errChan
}

// Shutdown stops gRPC server and leaves cluster
func (s *Node) Shutdown(ctx context.Context) {
	s.grpcServer.Stop(ctx)
	// TODO handle the errors
	_ = s.memberlist.Leave(15 * time.Second)
	_ = s.memberlist.Shutdown()
}

// RegisterService register the node as a grpc service
func (s *Node) RegisterService(server *ggrpc.Server) {
	pb.RegisterNodeStateReplicationServiceServer(server, s)
}

// PutActorMeta adds actor meta to the local store
func (s *Node) PutActorMeta(ctx context.Context, request *pb.PutActorMetaRequest) (*pb.PutActorMetaResponse, error) {
	// make a copy of the incoming request
	cloned := proto.Clone(request).(*pb.PutActorMetaRequest)
	// update the node state
	s.putActorMeta(cloned.GetNodeId(), cloned.GetActorMeta())
	// return the response
	return &pb.PutActorMetaResponse{
		NodeId:    cloned.GetNodeId(),
		ActorMeta: cloned.GetActorMeta(),
	}, nil
}

// GetActorMeta fetches actor meta from the local store
func (s *Node) GetActorMeta(ctx context.Context, request *pb.GetActorMetaRequest) (*pb.GetActorMetaResponse, error) {
	// make a copy of the incoming request
	cloned := proto.Clone(request).(*pb.GetActorMetaRequest)
	// query the node state
	actorMeta := s.getActorMeta(cloned.GetNodeId())
	return &pb.GetActorMetaResponse{
		NodeId:    cloned.GetNodeId(),
		ActorMeta: actorMeta,
	}, nil
}

// Address returns the Node address
func (s *Node) Address() string {
	return s.memberlist.LocalNode().FullAddress().Addr
}

// NodeMeta is used to retrieve meta-data about the current node
// when broadcasting an alive message. Its length is limited to
// the given byte size. This metadata is available in the Node structure.
func (s *Node) NodeMeta(limit int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	var network bytes.Buffer
	encoder := gob.NewEncoder(&network)
	err := encoder.Encode(s.metadata)
	if err != nil {
		s.logger.Fatal("failed to encode metadata", err)
	}
	return network.Bytes()
}

// NotifyMsg is called when a user-data message is received.
// Care should be taken that this method does not block, since doing
// so would block the entire UDP packet receive loop. Additionally, the byte
// slice may be modified after the call returns, so it should be copied if needed
func (s *Node) NotifyMsg(b []byte) {
	// not expecting messages - push/pull sync should suffice
}

// GetBroadcasts is called when user data messages can be broadcast.
// It can return a list of buffers to send. Each buffer should assume an
// overhead as provided with a limit on the total byte size allowed.
// The total byte size of the resulting data to send must not exceed
// the limit. Care should be taken that this method does not block,
// since doing so would block the entire UDP packet receive loop.
func (s *Node) GetBroadcasts(overhead, limit int) [][]byte {
	return s.broadcasts.GetBroadcasts(overhead, limit)
}

// LocalState is used for a TCP Push/Pull. This is sent to
// the remote side in addition to the membership information. Any
// data can be sent here. See MergeRemoteState as well. The `join`
// boolean indicates this is for a join instead of a push/pull.
func (s *Node) LocalState(join bool) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	// let us marshal the node state
	bytes, err := proto.Marshal(s.nodeState)
	if err != nil {
		s.logger.Fatal("failed to encode local state", err)
	}
	return bytes
}

// MergeRemoteState is invoked after a TCP Push/Pull. This is the
// state received from the remote side and is the result of the
// remote side's LocalState call. The 'join'
// boolean indicates this is for a join instead of a push/pull.
func (s *Node) MergeRemoteState(buf []byte, join bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// unmarshal the bytes array
	nodeState := new(pb.NodeState)
	if err := proto.Unmarshal(buf, nodeState); err != nil {
		s.logger.Fatal("failed to decode remote state", err)
	}

	// let us update the local node state
	for key, meta := range nodeState.GetStates() {
		if !proto.Equal(s.nodeState.GetStates()[key], meta) {
			s.nodeState.States[key] = meta
		}
	}
	s.logger.Debug("successfully merged remote state.")
}

// serve starts the grpc serve
func (s *Node) serve(ctx context.Context, errChan chan error) {
	// build the grpc server
	config := &grpc.Config{
		ServiceName:      "",
		GrpcPort:         s.bindPort,
		GrpcHost:         s.bindAddr,
		TraceEnabled:     false, // TODO set tracer later
		TraceURL:         "",
		EnableReflection: false,
	}

	// build the grpc service
	svr, err := grpc.
		GetServerBuilder(config).
		WithService(s).
		Build()

	// handle the error
	if err != nil {
		s.logger.Error("failed to listen on %s: %v", s.bindAddr, err)
		errChan <- err
		return
	}

	// set the server
	s.grpcServer = svr
	// start the server
	s.grpcServer.Start(ctx)
}

// joinCluster help join the memberlist
func (s *Node) joinCluster(errChan chan error) {
	// variable holding error
	var err error
	// create the memberlist
	s.memberlist, err = memberlist.Create(s.memberConfig)
	// handle the error
	if err != nil {
		// log the error
		s.logger.Error("failed to init memberlist", err)
		errChan <- err
	}

	var nodeAddr string
	if s.joinNodeAddr != "" {
		s.logger.Debugf("not the first node, joining %s...", s.joinNodeAddr)
		nodeAddr = s.joinNodeAddr
	} else {
		s.logger.Debug("first node of the cluster...")
		nodeAddr = fmt.Sprintf("%s:%d", s.bindAddr, s.memberConfig.BindPort)
	}

	// join the cluster
	_, err = s.memberlist.Join([]string{nodeAddr})
	// handle the join error
	if err != nil {
		s.logger.Error(errors.Wrap(err, "failed to join cluster"))
		errChan <- err
		return
	}

	// create the broadcast list
	br := &memberlist.TransmitLimitedQueue{
		NumNodes: func() int {
			return s.memberlist.NumMembers()
		},
		RetransmitMult: 3,
	}

	// set the broadcasts
	s.broadcasts = br

	s.logger.Infof("successfully joined cluster via %s", nodeAddr)
}

// putActorMeta adds config property to config store
func (s *Node) putActorMeta(key string, value *pb.ActorMeta) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nodeState.States[key] = value
}

// getActorMeta returns a property value
func (s *Node) getActorMeta(key string) *pb.ActorMeta {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.nodeState.GetStates()[key]
}
