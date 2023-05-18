package cluster

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/coreos/etcd/pkg/types"
	"github.com/tochemey/goakt/pkg/etcd/embed"

	"github.com/pkg/errors"
	"github.com/tochemey/goakt/discovery"
	goaktpb "github.com/tochemey/goakt/internal/goakt/v1"
	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/pkg/etcd/host"
	"github.com/tochemey/goakt/pkg/etcd/kvstore"
	"github.com/tochemey/goakt/pkg/telemetry"
	"golang.org/x/exp/slices"
	"google.golang.org/protobuf/proto"
)

const (
	clientPortName = "clients-port"
	peersPortName  = "peers-port"
)

// Cluster represents the Cluster
type Cluster struct {
	logger  log.Logger
	disco   discovery.Discovery
	store   *kvstore.KVStore
	eng     *embed.Embed
	dataDir string
}

// New creates an instance of Cluster
func New(disco discovery.Discovery, logger log.Logger, opts ...Option) *Cluster {
	cl := &Cluster{
		logger:  logger,
		disco:   disco,
		dataDir: "/var/goakt/",
	}
	// apply the various options
	for _, opt := range opts {
		opt.Apply(cl)
	}

	return cl
}

// Start starts the Cluster. When the join address is not set a brand-new cluster is started.
// However, when the join address is set the given Cluster joins an existing cluster at the joinAddr.
func (n *Cluster) Start(ctx context.Context) error {
	// add some logging information
	n.logger.Info("Starting GoAkt cluster....")

	var (
		// create a variable to hold the discovered nodes
		discoNodes []*discovery.Node
		// variable to help remove duplicate nodes discovered
		seen = make(map[string]bool)
		// variable holding the discovery loop count
		count = 3 // TODO revisit this after QA
		// variable holding
		err error
	)

	// handle the error
	if err != nil {
		n.logger.Error(errors.Wrap(err, "failed to grab node advertise URLs"))
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
		nodes, err := n.disco.Nodes(ctx)
		// handle the error
		if err != nil {
			n.logger.Error(errors.Wrap(err, "failed to fetch existing nodes in the cluster"))
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
	n.logger.Debugf("%s has discovered %d nodes", n.disco.ID(), len(discoNodes))

	// get the host addresses
	addresses, _ := host.HostAddresses()

	// let us get the host from the discovered nodes
	var hostPeerURLs []string
	var hostClientURLs []string
	var hostNodeName string
	// iterate the discovered nodes to find the host node
	for _, discoNode := range discoNodes {
		// here the host node is found
		if slices.Contains(addresses, discoNode.Host) {
			// get the peer port
			peersPort := discoNode.Ports[peersPortName]
			// get the clients port
			clientsPort := discoNode.Ports[clientPortName]
			// let us build the host peer URLs and client URLs
			for _, addr := range addresses {
				hostPeerURLs = append(hostPeerURLs, fmt.Sprintf("http://%s:%d", addr, peersPort))
				hostClientURLs = append(hostClientURLs, fmt.Sprintf("http://%s:%d", addr, clientsPort))
			}
			// set the host node name
			hostNodeName = discoNode.Name
			break
		}
	}

	// variables to hold endpoints, clientURLs and peerURLs
	endpoints := make([]string, len(discoNodes))
	kvStoreEndpoints := make([]string, len(discoNodes))
	// iterate the list of discovered nodes to build the endpoints and the various URLs
	for i, discoNode := range discoNodes {
		// build the peer URL
		peersURL, clientsURL := nodeURLs(discoNode)
		// build the endpoints
		endpoints[i] = fmt.Sprintf("%s=%s", discoNode.Name, peersURL)
		// set the KV store
		kvStoreEndpoints[i] = clientsURL
	}

	// make the urls
	clientsURLs := types.MustNewURLs(hostClientURLs)
	peerURLs := types.MustNewURLs(hostPeerURLs)
	// let us build the initial cluster
	initialCluster := strings.Join(endpoints, ",")
	// create the embed config
	config := embed.NewConfig(
		hostNodeName,
		clientsURLs,
		peerURLs,
		embed.WithLogger(n.logger),
		embed.WithInitialCluster(initialCluster),
		embed.WithDataDir(n.dataDir),
	)
	// create an instance of embed
	n.eng = embed.NewEmbed(config)

	// start the engine
	if err := n.eng.Start(); err != nil {
		return err
	}

	// let us build the KV store connection endpoints
	// create the instance of the distributed store and set it
	n.store, err = kvstore.New(kvstore.NewConfig(n.logger, kvStoreEndpoints))
	// handle the error
	if err != nil {
		// log the error and return
		n.logger.Error(errors.Wrap(err, "failed to start the Cluster"))
	}

	// add some logging information
	n.logger.Info("GoAkt cluster successfully started.🎉")
	return nil
}

// Stop stops the Cluster gracefully
func (n *Cluster) Stop() error {
	// add some logging information
	n.logger.Info("Stopping GoAkt cluster....")
	// let us stop the store
	if err := n.store.Shutdown(); err != nil {
		return errors.Wrap(err, "failed to Stop  GoAkt cluster...😣")
	}
	// stop the engine
	if err := n.eng.Stop(); err != nil {
		return errors.Wrap(err, "failed to Stop  GoAkt cluster...😣")
	}
	n.logger.Info("GoAkt cluster successfully stopped.🎉")
	return nil
}

// PutActor replicates onto the cluster the metadata of an actor
func (n *Cluster) PutActor(ctx context.Context, actor *goaktpb.WireActor) error {
	// create a cancellation context of 1 second timeout
	ctx, cancelFn := context.WithTimeout(ctx, time.Second) // TODO make this configurable
	defer cancelFn()

	// add a span to trace this call
	ctx, span := telemetry.SpanContext(ctx, "PutActor")
	defer span.End()

	// let us marshal it
	bytea, err := proto.Marshal(actor)
	// handle the marshaling error
	if err != nil {
		// add a logging to the stderr
		n.logger.Error(errors.Wrapf(err, "failed to persist actor=%s data in the cluster", actor.GetActorName()))
		// here we cancel the request
		return errors.Wrapf(err, "failed to persist actor=%s data in the cluster", actor.GetActorName())
	}

	// let us base64 encode the bytea before sending it into the cluster
	data := make([]byte, base64.StdEncoding.EncodedLen(len(bytea)))
	base64.StdEncoding.Encode(data, bytea)

	// send the record into the cluster
	_, err = n.store.SetValue(ctx, actor.GetActorName(), string(data))
	// handle the error
	if err != nil {
		// log the error
		n.logger.Error(errors.Wrapf(err, "failed to replicate actor=%s record", actor.GetActorName()))
		return err
	}

	// Ahoy we are successful
	return nil
}

// GetActor fetches an actor from the cluster
func (n *Cluster) GetActor(ctx context.Context, actorName string) (*goaktpb.WireActor, error) {
	// create a cancellation context of 1 second timeout
	ctx, cancelFn := context.WithTimeout(ctx, time.Second) // TODO make this configurable
	defer cancelFn()

	// add a span to trace this call
	ctx, span := telemetry.SpanContext(ctx, "GetActor")
	defer span.End()

	// grab the record from the distributed store
	resp, err := n.store.GetValue(ctx, actorName)
	// handle the error
	if err != nil {
		// log the error
		n.logger.Error(errors.Wrapf(err, "failed to get actor=%s record", actorName))
		return nil, err
	}

	// let us grab the actor response
	bytea := make([]byte, base64.StdEncoding.DecodedLen(len(string(resp.Kvs[0].Value))))
	// let base64 decode the data before parsing it
	_, err = base64.StdEncoding.Decode(bytea, resp.Kvs[0].Value)
	// handle the error
	if err != nil {
		// log the error
		n.logger.Error(errors.Wrapf(err, "failed to decode actor=%s record", actorName))
		return nil, err
	}

	// create an instance of proto message
	actor := new(goaktpb.WireActor)
	// let us unpack the byte array
	if err := proto.Unmarshal(bytea, actor); err != nil {
		// log the error and return
		n.logger.Error(errors.Wrapf(err, "failed to decode actor=%s record", actorName))
		return nil, err
	}
	// return the response
	return actor, nil
}
