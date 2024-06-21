/*
 * MIT License
 *
 * Copyright (c) 2022-2024 Tochemey
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package client

import (
	"context"
	"net"
	"strconv"
	"sync"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/actors"
	"github.com/tochemey/goakt/v2/internal/http"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/internalpb/internalpbconnect"
	"github.com/tochemey/goakt/v2/internal/types"
	"github.com/tochemey/goakt/v2/internal/validation"
)

// Client connects to af Go-Akt nodes.
// This client can only be used when remoting is enabled on the various nodes.
// The client is only used against a Go-Akt cluster
type Client struct {
	nodes           []*Node
	locker          *sync.Mutex
	strategy        BalancerStrategy
	balancer        Balancer
	closeSignal     chan types.Unit
	refreshInterval time.Duration
}

// New creates an instance of Client. The provided nodes are the cluster nodes.
// A node is the form of host:port where host and port represents the remoting host
// and remoting port of the nodes. The nodes list will be load balanced based upon the load-balancing
// strategy defined by default round-robin will be used.
// An instance of the Client can be reused and it is thread safe.
func New(ctx context.Context, nodes []string, opts ...Option) (*Client, error) {
	chain := validation.
		New(validation.FailFast()).
		AddAssertion(len(nodes) != 0, "nodes are required")
	for _, host := range nodes {
		chain = chain.AddValidator(validation.NewTCPAddressValidator(host))
	}

	if err := chain.Validate(); err != nil {
		return nil, err
	}

	cl := &Client{
		locker:          &sync.Mutex{},
		strategy:        RoundRobinStrategy,
		refreshInterval: -1,
	}

	// apply the various options
	for _, opt := range opts {
		opt.Apply(cl)
	}

	var (
		verifiedNodes []*Node
	)

	for _, node := range nodes {
		weight, ok, err := nodeMetric(ctx, node)
		if err != nil {
			return nil, err
		}

		if !ok {
			continue
		}

		verifiedNodes = append(verifiedNodes, NewNode(node, weight))
	}

	switch cl.strategy {
	case RoundRobinStrategy:
		cl.balancer = NewRoundRobin()
	case RandomStrategy:
		cl.balancer = NewRandom()
	case LeastLoadStrategy:
		cl.balancer = NewLeastLoad()
	default:
		// TODO: add more balancer strategy
	}

	cl.nodes = verifiedNodes
	cl.balancer.Set(cl.nodes...)

	// only refresh nodes when refresh interval is set
	if cl.refreshInterval > 0 {
		cl.closeSignal = make(chan types.Unit, 1)
		go cl.refreshNodesLoop()
	}

	return cl, nil
}

// Close closes the Client connection
func (x *Client) Close() {
	x.locker.Lock()
	x.nodes = make([]*Node, 0)
	if x.refreshInterval > 0 {
		close(x.closeSignal)
	}
	x.locker.Unlock()
}

// Kinds returns the list of all the Client kinds registered
func (x *Client) Kinds(ctx context.Context) ([]string, error) {
	x.locker.Lock()
	defer x.locker.Unlock()
	interceptor, err := otelconnect.NewInterceptor()
	if err != nil {
		return nil, err
	}

	host, port := x.getNextRemotingHostAndPort()
	service := internalpbconnect.NewClusterServiceClient(
		http.NewClient(),
		http.URL(host, port),
		connect.WithGRPC(),
		connect.WithInterceptors(interceptor))

	response, err := service.GetKinds(ctx, connect.NewRequest(new(internalpb.GetKindsRequest)))
	if err != nil {
		return nil, err
	}

	return response.Msg.GetKinds(), nil
}

// Spawn creates an actor provided the actor name.
// The actor name will be generated and returned when the request is successful
func (x *Client) Spawn(ctx context.Context, actor *Actor) (err error) {
	x.locker.Lock()
	defer x.locker.Unlock()
	host, port := x.getNextRemotingHostAndPort()
	return actors.RemoteSpawn(ctx, host, port, actor.Name(), actor.Kind())
}

// Tell sends a message to a given actor provided the actor name.
// If the given actor does not exist it will be created automatically when
// Client mode is enabled
func (x *Client) Tell(ctx context.Context, actor *Actor, message proto.Message) error {
	x.locker.Lock()
	defer x.locker.Unlock()
	host, port := x.getNextRemotingHostAndPort()
	address, err := actors.RemoteLookup(ctx, host, port, actor.Name())
	if err != nil {
		return err
	}
	return actors.RemoteTell(ctx, address, message)
}

// Ask sends a message to a given actor provided the actor name and expects a response.
// If the given actor does not exist it will be created automatically when
// Client mode is enabled. This will block until a response is received or timed out.
func (x *Client) Ask(ctx context.Context, actor *Actor, message proto.Message) (reply proto.Message, err error) {
	x.locker.Lock()
	defer x.locker.Unlock()
	host, port := x.getNextRemotingHostAndPort()
	address, err := actors.RemoteLookup(ctx, host, port, actor.Name())
	if err != nil {
		return nil, err
	}
	return actors.RemoteAsk(ctx, address, message)
}

// Kill kills a given actor in the Client
func (x *Client) Kill(ctx context.Context, actor *Actor) error {
	x.locker.Lock()
	defer x.locker.Unlock()
	host, port := x.getNextRemotingHostAndPort()
	return actors.RemoteStop(ctx, host, port, actor.Name())
}

// getNextRemotingHostAndPort returns the next node host and port
func (x *Client) getNextRemotingHostAndPort() (host string, port int) {
	node := x.balancer.Next()
	host, p, _ := net.SplitHostPort(node.Address())
	port, _ = strconv.Atoi(p)
	return
}

// updateNodes updates the list of nodes availables in the pool
// the old nodes pool is completely replaced by the new nodes pool
func (x *Client) updateNodes(ctx context.Context) error {
	x.locker.Unlock()
	defer x.locker.Lock()

	for _, node := range x.nodes {
		weight, ok, err := nodeMetric(ctx, node.Address())
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		node.SetWeight(float64(weight))
	}
	return nil
}

// refreshNodesLoop refreshes the nodes
func (x *Client) refreshNodesLoop() {
	ticker := time.NewTicker(10 * time.Second)
	tickerStopSig := make(chan types.Unit, 1)
	go func() {
		for {
			select {
			case <-ticker.C:
				if err := x.updateNodes(context.Background()); err != nil {
					// TODO: is it good to panic?
					panic(err)
				}
			case <-tickerStopSig:
				tickerStopSig <- types.Unit{}
				return
			}
		}
	}()
	<-tickerStopSig
	ticker.Stop()
}

// nodeMetric pings a given node and get the node metric info and
func nodeMetric(ctx context.Context, node string) (int, bool, error) {
	host, p, _ := net.SplitHostPort(node)
	port, _ := strconv.Atoi(p)
	service := internalpbconnect.NewClusterServiceClient(
		http.NewClient(),
		http.URL(host, port),
		connect.WithGRPC())

	response, err := service.GetNodeMetric(ctx, connect.NewRequest(&internalpb.GetNodeMetricRequest{NodeAddress: node}))
	if err != nil {
		code := connect.CodeOf(err)

		// here node may not be available
		if code == connect.CodeUnavailable ||
			code == connect.CodeCanceled ||
			code == connect.CodeDeadlineExceeded {
			return 0, false, nil
		}

		return 0, false, err
	}
	return int(response.Msg.GetActorsCount()), true, nil
}
