// MIT License
//
// Copyright (c) 2022-2026 GoAkt Team
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

// Package main is the clustered companion of the issue-1296 sample:
// point-to-point reliable delivery between two actors hosted on different
// nodes of one GoAkt cluster. The endpoint actors and their handshake are the
// same as in the single-node sample. What changes is the topology: the
// publisher runs on one node, the processor on another, the controllers find
// each other through the cluster registry, and every sequenced message
// crosses the network through remoting. The payload is a protobuf message, so
// no serializer registration is needed.
package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/discovery/nats"
	dynaport "github.com/tochemey/goakt/v4/internal/net"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

const (
	publisherName = "order-publisher"
	processorName = "order-processor"

	// retryInterval keeps the controllers' recovery ticks snappy so the
	// sample converges quickly; production flows keep the defaults.
	retryInterval = 200 * time.Millisecond
)

type (
	// PublishOrder commands the publisher to submit one order to the
	// reliable flow.
	PublishOrder struct {
		OrderID string
	}

	// GetProcessed asks the processor for a snapshot of the order IDs it has
	// processed, in processing order.
	GetProcessed struct{}
)

// OrderPublisher is the producer endpoint. It queues submissions and answers
// the controller handshake: it holds the current RequestNext grant until a
// submission exists, spends the grant with one Produced, idempotently
// re-answers a retried grant with the same Produced, and acknowledges Stored.
type OrderPublisher struct {
	controller   *actor.PID
	grant        *actor.RequestNext
	pending      []*PublishOrder
	lastToken    string
	lastProduced *actor.Produced
}

var _ actor.Actor = (*OrderPublisher)(nil)

// PreStart implements actor.Actor.
func (x *OrderPublisher) PreStart(*actor.Context) error { return nil }

// PostStop implements actor.Actor.
func (x *OrderPublisher) PostStop(*actor.Context) error { return nil }

// Receive handles business ingress and the producer side of the handshake.
func (x *OrderPublisher) Receive(ctx *actor.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *actor.PostStart:
	case *actor.RequestNext:
		// only the endpoint's own controller may grant credit
		if !msg.IsAuthorizedFor(ctx.Self(), ctx.Sender()) {
			return
		}

		x.controller = ctx.Sender()

		// a retried grant of an already answered token must receive the
		// same Produced again, never consume a second submission
		if msg.Token() == x.lastToken && x.lastProduced != nil {
			ctx.Tell(x.controller, x.lastProduced)
			return
		}

		x.grant = msg
		x.flush(ctx)
	case *actor.Stored:
		ack, err := actor.NewStoredAck(msg)
		if err != nil {
			ctx.Err(err)
			return
		}

		fmt.Printf("[%s] order %s stored as seq %d\n", publisherName, msg.MessageID(), msg.Seq())
		ctx.Tell(ctx.Sender(), ack)
	case *PublishOrder:
		x.pending = append(x.pending, msg)
		x.flush(ctx)
	default:
		ctx.Unhandled()
	}
}

// flush spends the held grant on the oldest queued submission.
func (x *OrderPublisher) flush(ctx *actor.ReceiveContext) {
	if x.grant == nil || len(x.pending) == 0 {
		return
	}

	order := x.pending[0]
	produced, err := actor.NewProduced(x.grant, order.OrderID, &testpb.Reply{Content: order.OrderID})
	if err != nil {
		ctx.Err(err)
		return
	}

	x.pending = x.pending[1:]
	x.lastToken = x.grant.Token()
	x.lastProduced = produced
	x.grant = nil
	fmt.Printf("[%s] handing order %s to the controller\n", publisherName, order.OrderID)
	ctx.Tell(x.controller, produced)
}

// OrderProcessor is the consumer endpoint. It processes each Delivery
// idempotently, deduplicating by MessageID because a lost confirmation or a
// controller restart legitimately redelivers, and replies Confirmed once the
// order is safely processed.
type OrderProcessor struct {
	seen      map[string]bool
	processed []string
}

var _ actor.Actor = (*OrderProcessor)(nil)

// PreStart implements actor.Actor.
func (x *OrderProcessor) PreStart(*actor.Context) error {
	x.seen = make(map[string]bool)
	return nil
}

// PostStop implements actor.Actor.
func (x *OrderProcessor) PostStop(*actor.Context) error { return nil }

// Receive handles the consumer side of the handshake and snapshot requests.
func (x *OrderProcessor) Receive(ctx *actor.ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case *actor.PostStart:
	case *actor.Delivery:
		// only the endpoint's own controller may deliver
		if !msg.IsAuthorizedFor(ctx.Self(), ctx.Sender()) {
			return
		}

		if !x.seen[msg.MessageID()] {
			x.seen[msg.MessageID()] = true
			order := msg.Payload().(*testpb.Reply)
			x.processed = append(x.processed, order.GetContent())
			fmt.Printf("[%s] processed order %s (seq %d)\n", processorName, order.GetContent(), msg.Seq())
		}

		confirmed, err := actor.NewConfirmed(msg)
		if err != nil {
			ctx.Err(err)
			return
		}

		ctx.Tell(ctx.Sender(), confirmed)
	case *GetProcessed:
		ctx.Response(append([]string(nil), x.processed...))
	default:
		ctx.Unhandled()
	}
}

// startNode starts one cluster member with remoting and NATS discovery on
// dynamically allocated ports. Both endpoint kinds are registered so a
// surviving node could reconstruct a relocated endpoint.
func startNode(ctx context.Context, natsAddress string) (actor.ActorSystem, error) {
	ports := dynaport.Get(3)
	discoveryPort := ports[0]
	peersPort := ports[1]
	remotingPort := ports[2]

	discovery := nats.NewDiscovery(&nats.Config{
		NatsServer:    "nats://" + natsAddress,
		NatsSubject:   "issue-1296",
		Host:          "localhost",
		DiscoveryPort: discoveryPort,
	})

	// the registry backup replica is the operational requirement for
	// relocatable reliable endpoints: with a single copy, registry state
	// owned by a lost node disappears with it
	clusterConfig := actor.
		NewClusterConfig().
		WithDiscovery(discovery).
		WithDiscoveryPort(discoveryPort).
		WithPeersPort(peersPort).
		WithMinimumPeersQuorum(1).
		WithReplicaCount(2).
		WithKinds(new(OrderPublisher), new(OrderProcessor))

	system, err := actor.NewActorSystem("orders",
		actor.WithRemote(remote.NewConfig("localhost", remotingPort)),
		actor.WithLogger(log.DiscardLogger),
		actor.WithCluster(clusterConfig),
	)
	if err != nil {
		return nil, err
	}

	if err := system.Start(ctx); err != nil {
		return nil, err
	}

	return system, nil
}

// newNatsServer starts an embedded NATS server for cluster discovery.
func newNatsServer() *natsserver.Server {
	server, err := natsserver.NewServer(&natsserver.Options{
		Host: "localhost",
		Port: -1,
	})
	if err != nil {
		fail(fmt.Sprintf("creating NATS server: %v", err))
	}

	ready := make(chan bool)

	go func() {
		ready <- true
		server.Start()
	}()

	<-ready

	if !server.ReadyForConnections(2 * time.Second) {
		fail("NATS server is not ready for connections")
	}

	return server
}

func main() {
	server := newNatsServer()
	defer server.Shutdown()

	ctx := context.Background()

	producerNode, err := startNode(ctx, server.Addr().String())
	must(err)

	consumerNode, err := startNode(ctx, server.Addr().String())
	must(err)

	// each endpoint is spawned on its own node; the spawn options are
	// identical to the single-node sample, and the controllers resolve each
	// other through the cluster registry
	publisher, err := producerNode.Spawn(ctx, publisherName, &OrderPublisher{},
		actor.AsReliableProducer(processorName, actor.WithReliableRetryInterval(retryInterval)))
	must(err)

	processor, err := consumerNode.Spawn(ctx, processorName, &OrderProcessor{},
		actor.AsReliableConsumer(publisherName, actor.WithReliableResendInterval(retryInterval)))
	must(err)

	fmt.Printf("publisher on %s:%d, processor on %s:%d\n",
		producerNode.Host(), producerNode.Port(), consumerNode.Host(), consumerNode.Port())

	for i := 1; i <= 5; i++ {
		must(actor.Tell(ctx, publisher, &PublishOrder{OrderID: fmt.Sprintf("ord-%d", i)}))
	}

	must(await("five processed orders on the consumer node", func() bool {
		return len(processedOrders(ctx, processor)) == 5
	}))

	expected := []string{"ord-1", "ord-2", "ord-3", "ord-4", "ord-5"}
	if processed := processedOrders(ctx, processor); !slices.Equal(processed, expected) {
		fail(fmt.Sprintf("processed %v, want %v", processed, expected))
	}

	must(consumerNode.Stop(ctx))
	must(producerNode.Stop(ctx))
	fmt.Println("OK")
}

// processedOrders asks the processor for its snapshot; an empty snapshot is
// returned while the ask cannot be served.
func processedOrders(ctx context.Context, processor *actor.PID) []string {
	response, err := actor.Ask(ctx, processor, &GetProcessed{}, time.Second)
	if err != nil {
		return nil
	}

	processed, _ := response.([]string)
	return processed
}

// await polls the condition until it holds or the sample's deadline expires.
func await(description string, condition func() bool) error {
	deadline := time.Now().Add(30 * time.Second)

	for time.Now().Before(deadline) {
		if condition() {
			return nil
		}

		pause.For(50 * time.Millisecond)
	}

	return fmt.Errorf("timed out waiting for %s", description)
}

func must(err error) {
	if err != nil {
		fail(err.Error())
	}
}

func fail(message string) {
	fmt.Println("FAILED:", message)
	os.Exit(1)
}
