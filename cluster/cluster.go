package cluster

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/pkg/errors"
	"github.com/shaj13/raft"
	"github.com/tochemey/goakt/discovery"
	goaktpb "github.com/tochemey/goakt/internal/goakt/v1"
	"github.com/tochemey/goakt/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Cluster represents the cluster
type Cluster struct {
	logger            log.Logger
	node              *node
	peersListenerChan chan struct{}
	disco             discovery.Discovery
	port              int
}

// New creates an instance of Cluster
func New(port int, logger *log.Log, disco discovery.Discovery) *Cluster {
	// create a node
	node := newNode(port, disco, logger)
	// create the instance
	return &Cluster{
		logger: logger,
		node:   node,
		disco:  disco,
		port:   port,
	}
}

// Start the cluster given the node config
func (c *Cluster) Start(ctx context.Context) error {
	// TODO add traces
	// TODO grab the context logger
	// start the underlying node
	if err := c.node.Start(ctx); err != nil {
		c.logger.Error(errors.Wrap(err, "failed to start cluster node"))
		return err
	}

	// let u start listening to the discovery event
	discoEvents, err := c.disco.Watch(ctx)
	// handle the error
	if err != nil {
		c.logger.Error(errors.Wrap(err, "failed to listen to discovery node lifecycle"))
		return err
	}

	// handle the discovery node events
	go c.handleClusterEvents(discoEvents)

	// Ahoy we are successful
	return nil
}

// Stop stops the cluster
func (c *Cluster) Stop(ctx context.Context) error {
	// TODO add traces
	// close the events listener channel
	close(c.peersListenerChan)
	// stop the raft node
	if err := c.node.Stop(); err != nil {
		c.logger.Error(errors.Wrap(err, "failed to stop underlying raft node"))
		return err
	}
	return nil
}

// Get fetches an actor from the cluster
func (c *Cluster) Get(ctx context.Context, actorName string) (*goaktpb.WireActor, error) {
	// TODO add traces
	// TODO grab the context logger
	// TODO add the fetching timeout in an option or a config
	ctx, cancelFn := context.WithTimeout(ctx, time.Second)
	defer cancelFn()

	// make sure we can read data from the cluster
	if err := c.node.raftNode.LinearizableRead(ctx); err != nil {
		// add a logging to the stderr
		c.logger.Error(errors.Wrapf(err, "failed to fetch actor=%s data", actorName))
		// here we cancel the request
		return nil, status.Error(codes.Canceled, err.Error())
	}

	// fetch the data from the fsm
	actor := c.node.fsm.Read(actorName)
	// return the response
	return actor, nil
}

// Replicate replicates onto the cluster the metadata of an actor
func (c *Cluster) Replicate(ctx context.Context, actor *goaktpb.WireActor) error {
	// TODO add traces
	// TODO grab the context logger

	// let us marshal it
	bytea, err := proto.Marshal(actor)
	// handle the marshaling error
	if err != nil {
		// add a logging to the stderr
		c.logger.Error(errors.Wrapf(err, "failed to persist actor=%s data in the cluster", actor.GetActorName()))
		// here we cancel the request
		return errors.Wrapf(err, "failed to persist actor=%s data in the cluster", actor.GetActorName())
	}
	// TODO add the persisting timeout in an option or a config
	ctx, cancelFn := context.WithTimeout(ctx, time.Second)
	defer cancelFn()
	// let us replicate the data across the cluster
	if err := c.node.raftNode.Replicate(ctx, bytea); err != nil {
		// add a logging to the stderr
		c.logger.Error(errors.Wrapf(err, "failed to persist actor=%s data in the cluster", actor.GetActorName()))
		// here we cancel the request
		return errors.Wrapf(err, "failed to persist actor=%s data in the cluster", actor.GetActorName())
	}
	// Ahoy we are successful
	return nil
}

// handleClusterEvents handles the cluster node events
func (c *Cluster) handleClusterEvents(events <-chan discovery.Event) {
	for {
		select {
		case <-c.peersListenerChan:
			return
		case event := <-events:
			switch x := event.(type) {
			case *discovery.NodeAdded:
			// pass. No need to handle this since every use the join method to join the cluster
			case *discovery.NodeModified:
				// let us grab the event
				evt := x
				func() {
					// let us check whether the given node is a leader or not
					if c.node.raftNode.Leader() == raft.None {
						return
					}
					// create a context that can be canceled
					ctx := context.Background()
					c.logger.Debugf("updating peer=%d", evt.Current.Name)
					// TODO add the removal of timeout in an option or a config
					ctx, cancelFn := context.WithTimeout(ctx, time.Second)
					defer cancelFn()
					// let us attempt removing the peer
					peers := c.node.Peers()
					nodeAddr := fmt.Sprintf("%s:%d", evt.Current.Host, evt.Current.JoinPort)
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
						Address: fmt.Sprintf("%s:%d", evt.Node.Host, evt.Node.JoinPort),
					}
					// update the member
					if err := c.node.raftNode.UpdateMember(ctx, member); err != nil {
						// add a logging to the stderr
						c.logger.Error(errors.Wrapf(err, "failed to update node's peer=%d", peer.PeerID))
					}
				}()
			case *discovery.NodeRemoved:
				// let us grab the removal event
				evt := x
				func() {
					// let us check whether the given node is a leader or not
					if c.node.raftNode.Leader() == raft.None {
						return
					}
					// create a context that can be canceled
					ctx := context.Background()
					c.logger.Debugf("removing peer=%d", evt.Node.Name)
					// TODO add the removal of timeout in an option or a config
					ctx, cancelFn := context.WithTimeout(ctx, time.Second)
					defer cancelFn()
					// let us attempt removing the peer
					peers := c.node.Peers()
					nodeAddr := fmt.Sprintf("%s:%d", evt.Node.Host, evt.Node.JoinPort)
					var peer *Peer
					for _, p := range peers {
						if p.Address == nodeAddr {
							peer = p
							break
						}
					}
					// remove the peer
					if err := c.node.raftNode.RemoveMember(ctx, peer.PeerID); err != nil {
						// add a logging to the stderr
						c.logger.Error(errors.Wrapf(err, "failed to remove node's peer=%d", peer.PeerID))
					}
				}()
			default:
				// pass
			}
		}
	}
}

// availablePort returns any available port to use
func availablePort() (int, error) {
	// let us get a port number for the node starting the cluster
	listener, err := net.Listen("tcp", ":0") // nolint
	// handle the error
	if err != nil {
		return 0, err
	}
	// grab the port
	port := listener.Addr().(*net.TCPAddr).Port
	return port, nil
}
