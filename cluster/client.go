package cluster

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"github.com/tochemey/goakt/pkg/grpc"
)

// Client helps interact with the cluster
type Client struct {
	// node address
	nodeAddr string
	// node port
	nodePort int
	// specifies the underlying grpc service client
	stateReplicationService pb.GossipServiceClient
}

// NewClient creates an instance of Client
func NewClient(ctx context.Context, nodeAddr string, nodePort int) (*Client, error) {
	// create a client connection
	clientConn, err := grpc.GetClientConn(ctx, fmt.Sprintf("%s:%d", nodeAddr, nodePort))
	// handle the error
	if err != nil {
		return nil, errors.Wrap(err, "failed to create the cluster client")
	}
	// create an instance of client and return it
	return &Client{
		nodeAddr:                nodeAddr,
		nodePort:                nodePort,
		stateReplicationService: pb.NewGossipServiceClient(clientConn),
	}, nil
}
