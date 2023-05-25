package cluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/coreos/etcd/pkg/types"
	goset "github.com/deckarep/golang-set/v2"
	"github.com/flowchartsman/retry"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/discovery"
	goaktpb "github.com/tochemey/goakt/internal/goakt/v1"
	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/pkg/etcd/embed"
	"github.com/tochemey/goakt/pkg/etcd/host"
	"github.com/tochemey/goakt/pkg/etcd/kvstore"
	"github.com/tochemey/goakt/pkg/telemetry"
	"go.etcd.io/etcd/api/v3/etcdserverpb"
	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/raft/v3"
	"golang.org/x/exp/slices"
)

const (
	clientPortName     = "clients-port"
	peersPortName      = "peers-port"
	memberAddThreshold = 5
)

// hostNode represents a cluster node hostNode
type hostNode struct {
	name       string
	host       string
	peerURLs   []string
	clientURLs []string
}

// Cluster represents the Cluster
type Cluster struct {
	logger            log.Logger
	disco             discovery.Discovery
	store             *kvstore.KVStore
	etcdNode          *embed.Node
	dataDir           string
	peersListenerChan chan struct{}
	nodeHost          string
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
		// variable holding
		err       error
		hostNode  *hostNode
		isJoining bool
	)

	// let us delay the start for sometime to make sure we have discovered all nodes
	duration := time.Second
	delay := duration - time.Duration(duration.Nanoseconds())%time.Second
	time.Sleep(delay)

	// discover the nodes
	discoNodes = c.discoverNodes(ctx)

	// add some logging
	c.logger.Debugf("%s has discovered %d nodes", c.disco.ID(), len(discoNodes))

	// get the host info
	hostNode = c.whoami(discoNodes)

	// we have more than one host.
	if len(discoNodes) > 1 {
		// first let us check whether there is already a running cluster
		var existingEndpoints []string
		// exclude this node from the list of endpoints to build
		for _, discoNode := range discoNodes {
			if !(hostNode.host == discoNode.Host) {
				// build the peer URL
				_, clientsURL := nodeURLs(discoNode)
				// set the endpoints
				existingEndpoints = append(existingEndpoints, clientsURL)
			}
		}

		// let us check whether the cluster is healthy
		isJoining, err = isHostNodeJoining(ctx, existingEndpoints)
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
			// create a retrier that will try a maximum of times to add a member, with
			// an initial delay of 100 ms and a maximum delay of 1 second
			retrier := retry.NewRetrier(memberAddThreshold, 100*time.Millisecond, time.Second)
			// run the retry
			err = retrier.RunContext(ctx, func(ctx context.Context) error {
				// adding the new member to the existing cluster
				mresp, err := client.MemberAdd(ctx, hostNode.peerURLs)
				// handle the error
				if err != nil {
					switch err {
					case rpctypes.ErrGRPCMemberNotEnoughStarted, context.DeadlineExceeded, context.Canceled:
						// retry again when the error message contain `re-configuration failed due to not enough started members`
						// continue till the threshold is reached
						return err
					default:
						err = errors.Wrapf(err, "failed to add node=%s to existing cluster", hostNode.name)
						c.logger.Error(err)
						return retry.Stop(err)
					}
				}

				// set the is joining the cluster
				isJoining = true
				// add some logging information
				c.logger.Infof("Node=%s Host=%s is successfully added as a new member=%s to existing cluster", hostNode.name, mresp.Member.GetName())
				return nil
			})

			// handle the error
			if err != nil {
				// re-configuration failed due to not enough started members
				c.logger.Error(err)
				return err
			}
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
		hostNode.name,
		types.MustNewURLs(hostNode.clientURLs),
		types.MustNewURLs(hostNode.peerURLs),
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

	// set the nodeHost
	c.nodeHost = hostNode.host

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

// NodeHost returns the cluster node Host
func (c *Cluster) NodeHost() string {
	return c.nodeHost
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
	base64ActorStr := string(resp.Kvs[0].Value)
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
				// make sure the nodes are defined
				current := x.Current
				latest := x.Node
				if current != nil && latest != nil {
					// add some debug logging
					c.logger.Debugf("updating peer=%s", current.Name)
					// let us get the list of members for the given node
					members, err := c.etcdNode.Members()
					// handle the error
					if err != nil {
						c.logger.Error(errors.Wrapf(err, "failed to fetch the member list for etcd node=%s", c.etcdNode.ID()))
					} else {
						// let us get the member information
						member := locateMember(members, current)
						// when matching member is found
						if member != nil {
							// grab the latest URLs
							peerURL, clientURL := nodeURLs(latest)
							// create an instance of member and set the various URLs
							m := &etcdserverpb.Member{
								ID:         member.GetID(),
								Name:       latest.Name,
								PeerURLs:   []string{peerURL},
								ClientURLs: []string{clientURL},
							}
							// send a member updated request
							if err := c.etcdNode.UpdateMember(m); err != nil {
								c.logger.Error(errors.Wrapf(err, "failed to update the member=%s list for etcd node=%s", member.GetName(), c.etcdNode.ID()))
								break
							}
						}
					}
				}
			case *discovery.NodeRemoved:
				// handle this when node is not nil
				if x.Node != nil {
					c.logger.Debugf("removing peer=%s", x.Node.Name)
					// let us attempt removing the peer
					members, err := c.etcdNode.Members()
					// handle the error and return
					if err != nil {
						c.logger.Error(errors.Wrapf(err, "failed to fetch the member list for etcd node=%s", c.etcdNode.ID()))
					} else {
						// let us get the member information
						member := locateMember(members, x.Node)
						// when matching member is found
						if member != nil {
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

// whoami returns the host node information
func (c *Cluster) whoami(discoNodes []*discovery.Node) *hostNode {
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
			peerURLs := goset.NewSet[string]()
			clientURLs := goset.NewSet[string]()

			// iterate the host addresses and construct the advertised urls
			for _, addr := range addresses {
				peerURLs.Add(fmt.Sprintf("http://%s:%d", addr, peersPort))
				clientURLs.Add(fmt.Sprintf("http://%s:%d", addr, clientsPort))
			}

			return &hostNode{
				name:       discoNode.Name,
				host:       discoNode.Host,
				peerURLs:   peerURLs.ToSlice(),
				clientURLs: clientURLs.ToSlice(),
			}
		}
	}
	return nil
}

// discoverNodes uses the discovery provider to find nodes in the cluster
// this function run with some delay mechanism to make sure the discovery provider finds enough nodes
func (c *Cluster) discoverNodes(ctx context.Context) []*discovery.Node {
	var (
		// create a ticker to run every 10 milliseconds for a duration of a second
		ticker = time.NewTicker(10 * time.Millisecond)
		timer  = time.After(time.Second)
		// create the ticker stop signal
		tickerStopSig = make(chan struct{})
		nodes         []*discovery.Node
		// variable to help remove duplicate nodes discovered
		seen = make(map[string]bool)
	)

	// start ticking
	go func() {
		for {
			select {
			case <-ticker.C:
				// let us discover the nodes
				// let us grab the existing nodes in the cluster
				discoNodes, err := c.disco.Nodes(ctx)
				// handle the error
				if err != nil {
					c.logger.Error(errors.Wrap(err, "failed to fetch existing nodes in the cluster"))
					tickerStopSig <- struct{}{}
					return
				}

				// remove duplicate
				for _, discoNode := range discoNodes {
					// let us get the node URL
					peersURL, _ := nodeURLs(discoNode)
					// check whether the Cluster has been already discovered and ignore it
					if _, ok := seen[peersURL]; ok {
						continue
					}
					// mark the Cluster as seen
					seen[peersURL] = true
					// add it to the list of nodes
					nodes = append(nodes, discoNode)
				}

			case <-timer:
				// tell the ticker to stop when timer is up
				tickerStopSig <- struct{}{}
				return
			}
		}
	}()
	// listen to ticker stop signal
	<-tickerStopSig
	// stop the ticker
	ticker.Stop()
	// return discovered nodes
	return nodes
}

// nodeURLs returns the actual node URLs
func nodeURLs(node *discovery.Node) (peersURL string, clientURL string) {
	return fmt.Sprintf("http://%s:%d", node.Host, node.Ports[peersPortName]),
		fmt.Sprintf("http://%s:%d", node.Host, node.Ports[clientPortName])
}

// locateMember helps find a given member using its peerURL
func locateMember(members []*etcdserverpb.Member, node *discovery.Node) *etcdserverpb.Member {
	// grab the given node URL
	peerURL, _ := nodeURLs(node)
	for _, member := range members {
		if slices.Contains(member.GetPeerURLs(), peerURL) && member.GetName() == node.Name {
			return member
		}
	}
	return nil
}

// getClient starts the etcd getClient and connects the Node instance to the cluster.
func getClient(ctx context.Context, endpoints []string) (*clientv3.Client, error) {
	// create the getClient config
	clientConfig := clientv3.Config{
		Endpoints:        endpoints,
		AutoSyncInterval: 30 * time.Second, // Update list of endpoints ever 30s.
		DialTimeout:      5 * time.Second,
		Context:          ctx,
	}

	// create an instance of the getClient
	client, err := clientv3.New(clientConfig)
	// return the eventual error
	if err != nil {
		return nil, err
	}

	// Immediately sync and update your list of endpoints
	if err := client.Sync(client.Ctx()); err != nil {
		return nil, err
	}
	return client, nil
}

// isHostNodeJoining checks whether the existing cluster is healthy and that the given node can be added to it as a member
func isHostNodeJoining(ctx context.Context, endpoints []string) (bool, error) {
	// keep the incoming ctx into a variable
	// so that for each iteration we can get
	// a fresh cancellation context
	mainCtx := ctx
	// iterate through the endpoints
	for index, ep := range endpoints {
		// create a cancellation context
		ctx, cancel := context.WithTimeout(mainCtx, 5*time.Second)
		// defer cancel
		defer cancel()
		// spawn a client connection
		client, err := clientv3.New(clientv3.Config{
			Endpoints:   endpoints,
			DialTimeout: 5 * time.Second,
			Context:     ctx,
		})

		// handle the error
		if err != nil {
			return false, err
		}

		// close the client connection
		defer client.Close()
		// check the node status
		resp, err := client.Status(client.Ctx(), ep)
		// handle the error
		if err != nil {
			switch err {
			case context.DeadlineExceeded, rpctypes.ErrMemberNotFound:
				// we have reached the number of endpoints to lookup
				if len(endpoints) == index {
					// this is a startup call which means that none of the nodes are not running yet
					return false, nil
				}
				// we continue scanning the endpoints till we exhaust the list
				continue
			default:
				// pass
			}
			return false, err
		}

		// we have found a peer
		output := resp.Header.GetMemberId() != raft.None &&
			(resp.Header.ClusterId != raft.None || resp.Leader == resp.Header.GetMemberId())
		// return the output
		return output, nil
	}

	return false, nil
}
