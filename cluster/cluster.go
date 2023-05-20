package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/coreos/etcd/pkg/types"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/discovery"
	goaktpb "github.com/tochemey/goakt/internal/goakt/v1"
	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/pkg/etcd/embed"
	"github.com/tochemey/goakt/pkg/etcd/host"
	"github.com/tochemey/goakt/pkg/etcd/kvstore"
	"github.com/tochemey/goakt/pkg/telemetry"
	"golang.org/x/exp/slices"
)

const (
	clientPortName            = "clients-port"
	peersPortName             = "peers-port"
	minimumInitialMembersSize = 3
)

// Cluster represents the Cluster
type Cluster struct {
	logger            log.Logger
	disco             discovery.Discovery
	store             *kvstore.KVStore
	etcdNode          *embed.Node
	dataDir           string
	peersListenerChan chan struct{}
}

// New creates an instance of Cluster
func New(disco discovery.Discovery, logger log.Logger, opts ...Option) *Cluster {
	cl := &Cluster{
		logger:            logger,
		disco:             disco,
		dataDir:           "/var/goakt/",
		peersListenerChan: make(chan struct{}, 1),
	}
	// apply the various options
	for _, opt := range opts {
		opt.Apply(cl)
	}

	return cl
}

// Start starts the Cluster. When the join address is not set a brand-new cluster is started.
// However, when the join address is set the given Cluster joins an existing cluster at the joinAddr.
func (c *Cluster) Start(ctx context.Context) error {
	// add some logging information
	c.logger.Info("Starting GoAkt cluster....")

	var (
		// create a variable to hold the discovered nodes
		discoNodes []*discovery.Node
		// variable to help remove duplicate nodes discovered
		seen = make(map[string]bool)
		// variable holding the discovery loop count
		count = 3 // TODO revisit this after QA
		// variable holding
		err            error
		hostPeerURLs   []string
		hostClientURLs []string
		hostNodeName   string
		isJoining      bool
	)

	// handle the error
	if err != nil {
		c.logger.Error(errors.Wrap(err, "failed to grab node advertise URLs"))
		return err
	}

	// let us delay for sometime to make sure we have discovered all nodes
	// FIXME: this is an approximation
	duration := time.Second
	delay := duration - time.Duration(duration.Nanoseconds())%time.Second

	// let us loop three times to attempt discovering all available nodes
	// This a poor man mechanism to attempt discovering all possible Cluster on before starting the cluster
	// This is an approximation
	// TODO: revisit this flow 😉
	for i := 0; i < count; i++ {
		// let us grab the existing nodes in the cluster
		nodes, err := c.disco.Nodes(ctx)
		// handle the error
		if err != nil {
			c.logger.Error(errors.Wrap(err, "failed to fetch existing nodes in the cluster"))
			return err
		}

		// remove duplicate
		for _, discoNode := range nodes {
			// let us get the node URL
			peersURL, _ := nodeURLs(discoNode)
			// check whether the Cluster has been already discovered and ignore it
			if _, ok := seen[peersURL]; ok {
				continue
			}
			// mark the Cluster as seen
			seen[peersURL] = true
			// add it to the list of nodes
			discoNodes = append(discoNodes, discoNode)
		}

		// wait a bit for before proceeding to the next round
		time.Sleep(delay)
	}

	// add some logging
	c.logger.Debugf("%s has discovered %d nodes", c.disco.ID(), len(discoNodes))

	// get the host addresses
	addresses, _ := host.Addresses()

	// iterate the discovered nodes to find the host node
	for _, discoNode := range discoNodes {
		// here the host node is found
		if slices.Contains(addresses, discoNode.Host) {
			// get the peer port
			peersPort := discoNode.Ports[peersPortName]
			// get the clients port
			clientsPort := discoNode.Ports[clientPortName]
			// let us build the host peer URLs and getClient URLs
			for _, addr := range addresses {
				hostPeerURLs = append(hostPeerURLs, fmt.Sprintf("http://%s:%d", addr, peersPort))
				hostClientURLs = append(hostClientURLs, fmt.Sprintf("http://%s:%d", addr, clientsPort))
			}
			// set the host node name
			hostNodeName = discoNode.Name
			break
		}
	}

	// we have more than one host.
	if len(discoNodes) > 1 {
		// first let us check whether there is already a running cluster
		var existingEndpoints []string
		// exclude this node from the list of endpoints to build
		for _, discoNode := range discoNodes {
			if !slices.Contains(addresses, discoNode.Host) {
				// build the peer URL
				_, clientsURL := nodeURLs(discoNode)
				// set the endpoints
				existingEndpoints = append(existingEndpoints, clientsURL)
			}
		}

		// let us check whether the cluster is healthy
		isJoining, err = canJoin(ctx, existingEndpoints)
		// handle the error
		if err != nil {
			c.logger.Error(errors.Wrap(err, "failed check the status of the possible existing cluster"))
			return err
		}

		// check whether the lead node is found or not
		// we assume here once we find a lead node then there is an existing cluster
		if isJoining {
			// here we will add the given node the existing cluster before starting it
			client, err := getClient(ctx, existingEndpoints)
			// handle the error
			if err != nil {
				c.logger.Error(errors.Wrap(err, "failed to create a client connection for an existing cluster"))
				return err
			}

			// adding the new member to the existing cluster
			mresp, err := client.MemberAdd(ctx, hostPeerURLs)
			// handle the error
			if err != nil {
				c.logger.Error(errors.Wrapf(err, "failed to add node=%s to existing cluster", hostNodeName))
				return err
			}
			// set the is joining the cluster
			isJoining = true
			// add some logging information
			c.logger.Infof("Node=%s Host=%s is successfully added as a new member=%s to existing cluster", hostNodeName, mresp.Member.GetName())
		}
	}

	// iterate the list of discovered nodes to build the endpoints and the various URLs
	initialPeerURLs := make([]string, len(discoNodes))
	endpoints := make([]string, len(discoNodes))
	for i, discoNode := range discoNodes {
		// build the peer URL
		peersURL, clientsURL := nodeURLs(discoNode)
		// build the initial cluster builder
		initialPeerURLs[i] = fmt.Sprintf("%s=%s", discoNode.Name, peersURL)
		// set the endpoints
		endpoints[i] = clientsURL
	}

	// create the embed config
	config := embed.NewConfig(
		hostNodeName,
		types.MustNewURLs(hostClientURLs),
		types.MustNewURLs(hostPeerURLs),
		types.MustNewURLs(endpoints),
		embed.WithLogger(c.logger),
		embed.WithInitialCluster(strings.Join(initialPeerURLs, ",")),
		embed.WithDataDir(c.dataDir),
		embed.WithJoin(isJoining),
	)

	// create an instance of embed
	c.etcdNode = embed.NewNode(config)

	// start the node server
	if err := c.etcdNode.Start(); err != nil {
		return err
	}

	// promote the node to a voter

	// let us build the KV store connection endpoints
	// create the instance of the distributed store and set it
	c.store, err = kvstore.New(kvstore.NewConfig(c.logger, c.etcdNode.Client()))
	// handle the error
	if err != nil {
		// log the error and return
		c.logger.Error(errors.Wrap(err, "failed to start the Cluster"))
		return c.Stop()
	}

	// add some logging information
	c.logger.Info("GoAkt cluster successfully started.🎉")

	// let u start listening to the discovery event
	discoEvents, err := c.disco.Watch(ctx)
	// handle the error
	if err != nil {
		// log the error and return
		c.logger.Error(errors.Wrap(err, "failed to start the Cluster"))
		return c.Stop()
	}

	// start listening to the discovery events
	go c.handleClusterEvents(discoEvents)

	return nil
}

// Stop stops the Cluster gracefully
func (c *Cluster) Stop() error {
	// add some logging information
	c.logger.Info("Stopping GoAkt cluster....")
	// close the events listener channel
	close(c.peersListenerChan)

	// let us stop the store
	if err := c.store.Shutdown(); err != nil {
		return errors.Wrap(err, "failed to Stop  GoAkt cluster...😣")
	}
	// stop the engine
	if err := c.etcdNode.Stop(); err != nil {
		return errors.Wrap(err, "failed to Stop  GoAkt cluster...😣")
	}
	c.logger.Info("GoAkt cluster successfully stopped.🎉")
	return nil
}

// PutActor replicates onto the cluster the metadata of an actor
func (c *Cluster) PutActor(ctx context.Context, actor *goaktpb.WireActor) error {
	// create a cancellation context of 1 second timeout
	ctx, cancelFn := context.WithTimeout(ctx, time.Second) // TODO make this configurable
	defer cancelFn()

	// add a span to trace this call
	ctx, span := telemetry.SpanContext(ctx, "PutActor")
	defer span.End()

	// let us marshal it
	data, err := encode(actor)
	// handle the marshaling error
	if err != nil {
		// add a logging to the stderr
		c.logger.Error(errors.Wrapf(err, "failed to persist actor=%s data in the cluster", actor.GetActorName()))
		// here we cancel the request
		return errors.Wrapf(err, "failed to persist actor=%s data in the cluster", actor.GetActorName())
	}

	// send the record into the cluster
	_, err = c.store.SetValue(ctx, actor.GetActorName(), data)
	// handle the error
	if err != nil {
		// log the error
		c.logger.Error(errors.Wrapf(err, "failed to replicate actor=%s record", actor.GetActorName()))
		return err
	}

	// Ahoy we are successful
	return nil
}

// GetActor fetches an actor from the cluster
func (c *Cluster) GetActor(ctx context.Context, actorName string) (*goaktpb.WireActor, error) {
	// create a cancellation context of 1 second timeout
	ctx, cancelFn := context.WithTimeout(ctx, time.Second) // TODO make this configurable
	defer cancelFn()

	// add a span to trace this call
	ctx, span := telemetry.SpanContext(ctx, "GetActor")
	defer span.End()

	// grab the record from the distributed store
	resp, err := c.store.GetValue(ctx, actorName)
	// handle the error
	if err != nil {
		// log the error
		c.logger.Error(errors.Wrapf(err, "failed to get actor=%s record", actorName))
		return nil, err
	}

	// grab the base64 representation of the wire actor
	base64ActorStr := resp.Kvs[0].String()
	// decode it
	actor, err := decode(base64ActorStr)
	// let us unpack the byte array
	if err != nil {
		// log the error and return
		c.logger.Error(errors.Wrapf(err, "failed to decode actor=%s record", actorName))
		return nil, err
	}
	// return the response
	return actor, nil
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
				// pass
			case *discovery.NodeRemoved:
				c.logger.Debugf("removing peer=%s", x.Node.Name)
				// let us attempt removing the peer
				members, err := c.etcdNode.Members()
				// handle the error and return
				if err != nil {
					c.logger.Error(errors.Wrapf(err, "failed to fetch the member list for etcd node=%s", c.etcdNode.ID()))
				} else {
					// grab the given node URL
					peerURL, _ := nodeURLs(x.Node)
					// iterate the list of members
					for _, member := range members {
						if slices.Contains(member.GetPeerURLs(), peerURL) && member.GetName() == x.Node.Name {
							// send a member removal request
							if err := c.etcdNode.RemoveMember(member); err != nil {
								c.logger.Error(errors.Wrapf(err, "failed to remove the member=%s list for etcd node=%s", member.GetName(), c.etcdNode.ID()))
								break
							}
						}
					}
				}
			default:
				// pass
			}
		}
	}
}
