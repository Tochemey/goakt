package cluster

import (
	"context"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
)

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

// canJoin checks whether the existing cluster is healthy and that the given node can be added to it as a member
func canJoin(ctx context.Context, endpoints []string) (bool, error) {
	// keep the incoming ctx into a variable
	// so that for each iteration we can get
	// a fresh cancellation context
	mainCtx := ctx
	// iterate through the endpoints
	for _, ep := range endpoints {
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
			case context.DeadlineExceeded:
				// this is a startup call which means that none of the nodes are not running yet
				return false, nil
			default:
				// pass
			}
			return false, err
		}

		// we just locate the leader
		if resp.Leader == resp.Header.GetMemberId() {
			return true, nil
		}
	}

	return false, nil
}
