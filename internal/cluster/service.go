package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
	"github.com/shaj13/raft"
	goaktpb "github.com/tochemey/goakt/internal/goaktpb/v1"
	"github.com/tochemey/goakt/internal/grpc"
	"github.com/tochemey/goakt/log"
	ggrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Service implements the cluster service
type Service struct {
	server grpc.Server
	logger log.Logger
	node   *node
	config *Config

	peersListenerChan chan struct{}
}

// enforces compilation error
var _ goaktpb.ClusterServiceServer = &Service{}

// NewService creates an instance of cluster node service
func NewService(config *Config) *Service {
	// let us create an instance of the node
	node := newNode(fmt.Sprintf("%s:%d", config.Host, config.Port), config.StateDir, config.Discovery, config.Logger)
	return &Service{
		logger:            config.Logger,
		node:              node,
		config:            config,
		peersListenerChan: make(chan struct{}, 1),
	}
}

// Start starts the cluster node service
func (s *Service) Start(ctx context.Context) error {
	// TODO add traces
	// TODO grab the context logger
	// build the grpc server
	config := &grpc.Config{
		ServiceName:      s.config.Name,
		GrpcHost:         s.config.Host,
		GrpcPort:         s.config.Port,
		TraceEnabled:     false,
		TraceURL:         "",
		EnableReflection: false,
		Logger:           s.config.Logger,
	}

	// build the grpc service
	server, err := grpc.
		GetServerBuilder(config).
		WithService(s).
		Build()

	// handle the error
	if err != nil {
		s.logger.Error(errors.Wrap(err, "failed to start cluster server"))
		return err
	}
	// set the service server
	s.server = server
	// start the server
	s.server.Start(ctx)
	// start the underlying node
	if err := s.node.Start(ctx); err != nil {
		s.logger.Error(errors.Wrap(err, "failed to start cluster node"))
		return err
	}

	// let u start listening to the discovery event
	discoEvents, err := s.config.Discovery.Watch(ctx)
	// handle the error
	if err != nil {
		s.logger.Error(errors.Wrap(err, "failed to listen to discovery node lifecycle"))
		return err
	}

	// handle the discovery node events
	go s.handleClusterEvents(discoEvents)

	// Ahoy we are successful
	return nil
}

// Stop stops the cluster node service
func (s *Service) Stop(ctx context.Context) error {
	// TODO add traces
	// stop the underlying grpc server
	s.server.Stop(ctx)
	// close the events listener channel
	close(s.peersListenerChan)
	// stop the raft node
	if err := s.node.Stop(); err != nil {
		s.logger.Error(errors.Wrap(err, "failed to stop underlying raft node"))
		return err
	}
	return nil
}

// GetPeers fetches all the peers of a given node
func (s *Service) GetPeers(ctx context.Context, request *goaktpb.GetPeersRequest) (*goaktpb.GetPeersResponse, error) {
	// TODO add traces
	// TODO grab the context logger
	// clone the incoming request
	req := proto.Clone(request).(*goaktpb.GetPeersRequest)
	// check whether the request is nil
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	// fetch the node peers
	peers := s.node.Peers()
	return &goaktpb.GetPeersResponse{Peers: peers}, nil
}

// PutActor adds an actor meta to the cluster
func (s *Service) PutActor(ctx context.Context, request *goaktpb.PutActorRequest) (*goaktpb.PutActorResponse, error) {
	// TODO add traces
	// TODO grab the context logger
	// clone the incoming request
	req := proto.Clone(request).(*goaktpb.PutActorRequest)
	// check whether the request is nil
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	// grab the actor meta
	actor := req.GetActor()
	// let us marshal it
	bytea, err := proto.Marshal(actor)
	// handle the marshaling error
	if err != nil {
		// add a logging to the stderr
		s.logger.Error(errors.Wrapf(err, "failed to persist actor=%s data in the cluster", actor.GetActorName()))
		// here we cancel the request
		return nil, status.Error(codes.Canceled, err.Error())
	}
	// TODO add the persisting timeout in an option or a config
	ctx, cancelFn := context.WithTimeout(ctx, time.Second)
	defer cancelFn()
	// let us replicate the data across the cluster
	if err := s.node.raftNode.Replicate(ctx, bytea); err != nil {
		// add a logging to the stderr
		s.logger.Error(errors.Wrapf(err, "failed to persist actor=%s data in the cluster", actor.GetActorName()))
		// here we cancel the request
		return nil, status.Error(codes.Canceled, err.Error())
	}
	// Ahoy we are successful
	return &goaktpb.PutActorResponse{}, nil
}

// GetActor reads an actor meta from the cluster
func (s *Service) GetActor(ctx context.Context, request *goaktpb.GetActorRequest) (*goaktpb.GetActorResponse, error) {
	// TODO add traces
	// TODO grab the context logger
	// clone the incoming request
	req := proto.Clone(request).(*goaktpb.GetActorRequest)
	// check whether the request is nil
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is nil")
	}
	// let us grab the actor name
	actorName := req.GetActorName()
	// TODO add the fetching timeout in an option or a config
	ctx, cancelFn := context.WithTimeout(ctx, time.Second)
	defer cancelFn()

	// make sure we can read data from the cluster
	if err := s.node.raftNode.LinearizableRead(ctx); err != nil {
		// add a logging to the stderr
		s.logger.Error(errors.Wrapf(err, "failed to fetch actor=%s data", actorName))
		// here we cancel the request
		return nil, status.Error(codes.Canceled, err.Error())
	}

	// fetch the data from the fsm
	actor := s.node.fsm.Read(actorName)
	// return the response
	return &goaktpb.GetActorResponse{Actor: actor}, nil
}

// RegisterService register the service
func (s *Service) RegisterService(srv *ggrpc.Server) {
	goaktpb.RegisterClusterServiceServer(srv, s)
}

// handleClusterEvents handles the cluster node events
func (s *Service) handleClusterEvents(events <-chan *goaktpb.Event) {
	for {
		select {
		case <-s.peersListenerChan:
			return
		case event := <-events:
			switch x := event.GetType().(type) {
			case *goaktpb.Event_Added:
			// TBD
			case *goaktpb.Event_Modified:
				// let us grab the event
				evt := x.Modified
				func() {
					// let us check whether the given node is a leader or not
					if s.node.raftNode.Leader() == raft.None {
						return
					}
					// create a context that can be canceled
					ctx := context.Background()
					s.logger.Debugf("updating peer=%d", evt.GetOldNode().GetName())
					// TODO add the removal of timeout in an option or a config
					ctx, cancelFn := context.WithTimeout(ctx, time.Second)
					defer cancelFn()
					// let us attempt removing the peer
					peers := s.node.Peers()
					nodeAddr := fmt.Sprintf("%s:%d", evt.GetOldNode().GetHost(), evt.GetOldNode().GetPort())
					var peer *goaktpb.Peer
					for _, p := range peers {
						if p.HostAndPort == nodeAddr {
							peer = p
							break
						}
					}
					// update the peer
					member := &raft.RawMember{
						ID:      peer.GetNodeId(),
						Address: fmt.Sprintf("%s:%d", evt.GetNewNode().GetHost(), evt.GetNewNode().GetPort()),
					}
					// update the member
					if err := s.node.raftNode.UpdateMember(ctx, member); err != nil {
						// add a logging to the stderr
						s.logger.Error(errors.Wrapf(err, "failed to update node's peer=%d", peer.GetNodeId()))
					}
				}()
			case *goaktpb.Event_Removed:
				// let us grab the removal event
				evt := x.Removed
				func() {
					// let us check whether the given node is a leader or not
					if s.node.raftNode.Leader() == raft.None {
						return
					}
					// create a context that can be canceled
					ctx := context.Background()
					s.logger.Debugf("removing peer=%d", evt.GetNode().GetName())
					// TODO add the removal of timeout in an option or a config
					ctx, cancelFn := context.WithTimeout(ctx, time.Second)
					defer cancelFn()
					// let us attempt removing the peer
					peers := s.node.Peers()
					nodeAddr := fmt.Sprintf("%s:%d", evt.GetNode().GetHost(), evt.GetNode().GetPort())
					var peer *goaktpb.Peer
					for _, p := range peers {
						if p.HostAndPort == nodeAddr {
							peer = p
							break
						}
					}
					// remove the peer
					if err := s.node.raftNode.RemoveMember(ctx, peer.GetNodeId()); err != nil {
						// add a logging to the stderr
						s.logger.Error(errors.Wrapf(err, "failed to remove node's peer=%d", peer.GetNodeId()))
					}
				}()
			default:
				// pass
			}
		}
	}
}
