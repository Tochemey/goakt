package cluster

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
	"github.com/shaj13/raft"
	"github.com/tochemey/goakt/internal/discovery"
	goaktpb "github.com/tochemey/goakt/internal/goaktpb/v1"
	"github.com/tochemey/goakt/log"
	"google.golang.org/protobuf/proto"
)

// Cluster defines the cluster contract
type Cluster interface {
	// Start starts the cluster node
	Start(ctx context.Context) error
	// Stop stops the cluster node
	Stop(ctx context.Context) error
	// GetPeers fetches all the peers of a given node
	GetPeers(ctx context.Context) ([]*Peer, error)
	// PutActor adds an actor meta to the cluster
	PutActor(ctx context.Context, actor *goaktpb.WireActor) error
	// GetActor reads an actor meta from the cluster
	GetActor(ctx context.Context, actorName string) (*goaktpb.WireActor, error)
}

// cluster implements the cluster service
type cluster struct {
	logger log.Logger
	node   *node
	config *Config

	peersListenerChan chan struct{}
}

// enforce compilation error
var _ Cluster = &cluster{}

// New creates an instance of cluster node service
func New(config *Config) Cluster {
	// let us create an instance of the node
	node := newNode(fmt.Sprintf("%s:%d", config.Host, config.Port), config.StateDir, config.Discovery, config.Logger)
	return &cluster{
		logger:            config.Logger,
		node:              node,
		config:            config,
		peersListenerChan: make(chan struct{}, 1),
	}
}

// Start starts the cluster node
func (s *cluster) Start(ctx context.Context) error {
	// TODO add traces
	// TODO grab the context logger
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

// Stop stops the cluster node
func (s *cluster) Stop(ctx context.Context) error {
	// TODO add traces
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
func (s *cluster) GetPeers(ctx context.Context) ([]*Peer, error) {
	// TODO add traces
	// TODO grab the context logger
	// fetch the node peers
	return s.node.Peers(), nil
}

// PutActor adds an actor meta to the cluster
func (s *cluster) PutActor(ctx context.Context, actor *goaktpb.WireActor) error {
	// TODO add traces
	// TODO grab the context logger

	// let us marshal it
	bytea, err := proto.Marshal(actor)
	// handle the marshaling error
	if err != nil {
		// add a logging to the stderr
		s.logger.Error(errors.Wrapf(err, "failed to persist actor=%s data in the cluster", actor.GetActorName()))
		// here we cancel the request
		return errors.Wrapf(err, "failed to persist actor=%s data in the cluster", actor.GetActorName())
	}
	// TODO add the persisting timeout in an option or a config
	ctx, cancelFn := context.WithTimeout(ctx, time.Second)
	defer cancelFn()
	// let us replicate the data across the cluster
	if err := s.node.raftNode.Replicate(ctx, bytea); err != nil {
		// add a logging to the stderr
		s.logger.Error(errors.Wrapf(err, "failed to persist actor=%s data in the cluster", actor.GetActorName()))
		// here we cancel the request
		return errors.Wrapf(err, "failed to persist actor=%s data in the cluster", actor.GetActorName())
	}
	// Ahoy we are successful
	return nil
}

// GetActor reads an actor meta from the cluster
func (s *cluster) GetActor(ctx context.Context, actorName string) (*goaktpb.WireActor, error) {
	// TODO add traces
	// TODO grab the context logger
	// TODO add the fetching timeout in an option or a config
	ctx, cancelFn := context.WithTimeout(ctx, time.Second)
	defer cancelFn()

	// make sure we can read data from the cluster
	if err := s.node.raftNode.LinearizableRead(ctx); err != nil {
		// add a logging to the stderr
		s.logger.Error(errors.Wrapf(err, "failed to fetch actor=%s data", actorName))
		// here we cancel the request
		return nil, err
	}

	// fetch the data from the fsm
	actor := s.node.fsm.Read(actorName)
	// return the response
	return actor, nil
}

// handleClusterEvents handles the cluster node events
func (s *cluster) handleClusterEvents(events <-chan discovery.Event) {
	for {
		select {
		case <-s.peersListenerChan:
			return
		case event := <-events:
			switch evt := event.(type) {
			case *discovery.NodeAdded:
			// pass. No need to handle this since every use the join method to join the cluster
			case *discovery.NodeModified:
				func() {
					// let us check whether the given node is a leader or not
					if s.node.raftNode.Leader() == raft.None {
						return
					}
					// create a context that can be canceled
					ctx := context.Background()
					s.logger.Debugf("updating peer=%d", evt.Current.Name())
					// TODO add the removal of timeout in an option or a config
					ctx, cancelFn := context.WithTimeout(ctx, time.Second)
					defer cancelFn()
					// let us attempt removing the peer
					peers := s.node.Peers()
					nodeAddr := fmt.Sprintf("%s:%d", evt.Current.Host(), evt.Current.Port())
					var peer *Peer
					for _, p := range peers {
						if p.Address == nodeAddr {
							peer = p
							break
						}
					}
					// update the peer
					member := &raft.RawMember{
						ID:      peer.PeerID,
						Address: fmt.Sprintf("%s:%d", evt.Node.Host(), evt.Node.Port()),
					}
					// update the member
					if err := s.node.raftNode.UpdateMember(ctx, member); err != nil {
						// add a logging to the stderr
						s.logger.Error(errors.Wrapf(err, "failed to update node's peer=%d", peer.PeerID))
					}
				}()
			case *discovery.NodeRemoved:
				func() {
					// let us check whether the given node is a leader or not
					if s.node.raftNode.Leader() == raft.None {
						return
					}
					// create a context that can be canceled
					ctx := context.Background()
					s.logger.Debugf("removing peer=%d", evt.Node.Name())
					// TODO add the removal of timeout in an option or a config
					ctx, cancelFn := context.WithTimeout(ctx, time.Second)
					defer cancelFn()
					// let us attempt removing the peer
					peers := s.node.Peers()
					nodeAddr := fmt.Sprintf("%s:%d", evt.Node.Host(), evt.Node.Port())
					var peer *Peer
					for _, p := range peers {
						if p.Address == nodeAddr {
							peer = p
							break
						}
					}
					// remove the peer
					if err := s.node.raftNode.RemoveMember(ctx, peer.PeerID); err != nil {
						// add a logging to the stderr
						s.logger.Error(errors.Wrapf(err, "failed to remove node's peer=%d", peer.PeerID))
					}
				}()
			default:
				// pass
			}
		}
	}
}
