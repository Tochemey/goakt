package cluster

import (
	"context"
	"strings"

	"github.com/coreos/etcd/pkg/types"
	"github.com/pkg/errors"
	"github.com/tochemey/goakt/log"
	"github.com/tochemey/goakt/pkg/etcd/embed"
	"go.uber.org/multierr"
)

// listenURLs specifies an etcd server listen URLs
type listenURLs struct {
	nodeName   string
	clientURLs []string
	peerURLs   []string
}

// startResult holds the result of embed etcd startup
type startResult struct {
	serverID string
	embed    *embed.Embed
	err      error
}

// engine represents the cluster engine
type engine struct {
	// endpoints specifies the endpoints list
	endpoints []string
	// clientURLs specifies the client URLs
	advertiseURLs []listenURLs
	// servers specifies the list of servers
	servers []*embed.Embed
	// startResultChan specifies the channel that capture the server starts
	startResultChan chan startResult
	// logger specifies the logger
	logger log.Logger
	// dataDir specifies the data dir
	dataDir string
}

// newEngine creates an instance of engine
func newEngine(dataDir string, logger log.Logger, endpoints []string, advertiseURLs []listenURLs) *engine {
	return &engine{
		endpoints:       endpoints,
		advertiseURLs:   advertiseURLs,
		startResultChan: make(chan startResult, 1),
		logger:          logger,
		dataDir:         dataDir,
	}
}

// start starts the cluster engine
func (e *engine) start(ctx context.Context) error {
	// let us build the initial cluster
	initialCluster := strings.Join(e.endpoints, ",")
	// add some logging information
	e.logger.Infof("Bootstrapping the GoAkt cluster with initial=[%s]", initialCluster)
	// create a list of embed etcd server
	e.servers = make([]*embed.Embed, len(e.advertiseURLs))
	// iterate over the list of endpoints and build the embed etcd server
	for _, entry := range e.advertiseURLs {
		entry := entry
		// make the urls
		clientsURLs := types.MustNewURLs(entry.clientURLs)
		peerURLs := types.MustNewURLs(entry.peerURLs)
		// create the embed config
		config := embed.NewConfig(
			entry.nodeName,
			clientsURLs,
			peerURLs,
			embed.WithLogger(e.logger),
			embed.WithInitialCluster(initialCluster),
			embed.WithDataDir(e.dataDir),
		)
		// start the server
		go func() {
			// create an instance of embed
			embed := embed.NewEmbed(config)
			// start the server
			err := embed.Start()
			// pass the embed server and the eventual error to the channel
			e.startResultChan <- startResult{
				serverID: entry.nodeName,
				embed:    embed,
				err:      err,
			}
		}()
	}

	// create a variable to combine startup errors
	var err error
	// iterate the listenURLs
	for range e.advertiseURLs {
		// grab the start result
		startResult := <-e.startResultChan
		// set the embed etcd server to the list of servers
		e.servers = append(e.servers, startResult.embed)
		// handle the error
		if startResult.err != nil {
			err = multierr.Append(err, startResult.err)
			e.logger.Error(errors.Wrap(startResult.err, "failed to start embed etcd server"))
		}
	}

	// handle the final error
	if err != nil {
		// let us stop the engine
		if e := e.stop(); e != nil {
			return multierr.Append(err, e)
		}
		return err
	}
	return nil
}

// stop stops the engine
func (e *engine) stop() error {
	// create an error to combine all stop errors
	var combinedErr error
	// iterate the embed servers and stop them
	for _, server := range e.servers {
		// stop the server when it is defined
		if server != nil {
			// handle the error when stopping the server
			if err := server.Stop(); err != nil {
				combinedErr = multierr.Append(combinedErr, err)
			}
		}
	}
	// return the error
	return combinedErr
}
