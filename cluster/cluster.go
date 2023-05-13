package cluster

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/pkg/errors"
	"github.com/tochemey/goakt/discovery"
	"github.com/tochemey/goakt/internal/etcd/store"
	goaktpb "github.com/tochemey/goakt/internal/goakt/v1"
	"github.com/tochemey/goakt/internal/telemetry"
	"github.com/tochemey/goakt/log"
	"google.golang.org/protobuf/proto"
)

// Cluster represents the Cluster
type Cluster struct {
	logger log.Logger
	disco  discovery.Discovery
	store  *store.Store
	name   string
}

// New creates an instance of Cluster
func New(name string, disco discovery.Discovery, logger log.Logger) *Cluster {
	return &Cluster{
		logger: logger,
		disco:  disco,
		name:   name,
	}
}

// Start starts the Cluster. When the join address is not set a brand-new cluster is started.
// However, when the join address is set the given Cluster joins an existing cluster at the joinAddr.
func (n *Cluster) Start(ctx context.Context) error {
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
			// check whether the Cluster has been already discovered and ignore it
			if _, ok := seen[discoNode.Host]; ok {
				continue
			}
			// mark the Cluster as seen
			seen[discoNode.Host] = true
			// add it to the list of nodes
			discoNodes = append(discoNodes, discoNode)
		}

		// wait a bit for before proceeding to the next round
		time.Sleep(delay)
	}

	// add some logging
	n.logger.Debugf("%s has discovered %d nodes", n.disco.ID(), len(discoNodes))

	var (
		peerURLs   = make([]string, len(discoNodes))
		endpoints  = make([]string, len(discoNodes))
		clientURLs = make([]string, len(discoNodes))
	)

	// let us build the various from the discovered nodes
	for i, discoNode := range discoNodes {
		peerURLs[i] = discoNode.JoinAddr()
		endpoints[i] = ""  // FIXME: enhance discoNode to get this values
		clientURLs[i] = "" // FIXME: enhance discoNode to get this values
	}

	// create an instance of the distributed store
	config := &store.Config{
		Logger: n.logger,
		Name:   n.name,
	}
	// use the default config if the given Cluster is the only discovered
	if len(discoNodes) > 1 {
		config = &store.Config{
			Endpoints:  endpoints,
			ClientURLs: clientURLs,
			PeerURLs:   peerURLs,
			Logger:     n.logger,
			Name:       n.name,
		}
	}

	// create the instance of the distributed store and set it
	n.store, err = store.New(config)
	// handle the error
	if err != nil {
		// log the error and return
		n.logger.Error(errors.Wrap(err, "failed to start the Cluster"))
	}

	return nil
}

// Stop stops the Cluster gracefully
func (n *Cluster) Stop() error {
	return n.store.Shutdown()
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
