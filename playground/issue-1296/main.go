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

// Package main is a living sample for github.com/Tochemey/goakt/issues/1296:
// point-to-point reliable delivery on a single node. An order publisher
// streams orders to an order processor with effectively-once semantics: the
// actor system creates and owns a controller next to each endpoint, the
// controllers handle sequencing, flow control, deduplication, and resends,
// and the application code only answers a small handshake.
//
// The producer contract is three messages: hold the RequestNext grant until
// there is something to send, answer it with exactly one Produced, and
// acknowledge the Stored receipt with a StoredAck. A retried grant carrying a
// token that was already answered must be re-answered with the same Produced,
// never with a new message. The consumer contract is one exchange: process
// the Delivery idempotently and reply Confirmed.
//
// The sample runs three acts. Act one streams five orders end to end and
// verifies ordered, deduplicated processing. Act two hands the controller a
// payload type the serializer does not know; encoding is deterministic, so
// the producer controller declares the flow terminally failed, publishes one
// ReliableDeliveryFailed event, and stops. Act three is the operator
// remediation: ReSpawn of the producer endpoint recreates the controller,
// and the orders queued during the outage flow again.
package main

import (
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
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
	// reliable flow. The payload is the wire message; reliable payloads must
	// be protobuf messages or types with a registered serializer.
	PublishOrder struct {
		OrderID string
		Payload any
	}

	// GetProcessed asks the processor for a snapshot of the order IDs it has
	// processed, in processing order.
	GetProcessed struct{}

	// unserializableOrder is a payload type the serializer does not know. It
	// poisons the flow in act two: encoding is deterministic, so the
	// producer controller reports a terminal failure instead of retrying.
	unserializableOrder struct{}
)

// OrderPublisher is the producer endpoint. It queues submissions and answers
// the controller handshake: it holds the current RequestNext grant until a
// submission exists, spends the grant with one Produced, idempotently
// re-answers a retried grant with the same Produced, and acknowledges Stored.
// Reliability starts at the Produced handoff, so the pending queue survives a
// restart only because this actor keeps it; a production publisher would
// reload its outbox from its own store in PreStart.
type OrderPublisher struct {
	controller   *actor.PID
	request      *actor.RequestNext
	pending      []*PublishOrder
	lastToken    string
	lastProduced *actor.Produced
}

var _ actor.Actor = (*OrderPublisher)(nil)

func (x *OrderPublisher) PreStart(*actor.Context) error { return nil }
func (x *OrderPublisher) PostStop(*actor.Context) error { return nil }

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

		x.request = msg
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
	if x.request == nil || len(x.pending) == 0 {
		return
	}

	order := x.pending[0]
	produced, err := actor.NewProduced(x.request, order.OrderID, order.Payload)
	if err != nil {
		ctx.Err(err)
		return
	}

	x.pending = x.pending[1:]
	x.lastToken = x.request.Token()
	x.lastProduced = produced
	x.request = nil
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

func (x *OrderProcessor) PreStart(*actor.Context) error {
	x.seen = make(map[string]bool)
	return nil
}

func (x *OrderProcessor) PostStop(*actor.Context) error { return nil }

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

func main() {
	ctx := context.Background()

	system, err := actor.NewActorSystem("orders", actor.WithLogger(log.DiscardLogger))
	must(err)
	must(system.Start(ctx))

	// ReliableDeliveryFailed events surface terminal flow failures here
	subscriber, err := system.Subscribe()
	must(err)

	// the actor system creates and owns one controller per endpoint; the
	// options only name the peer endpoint and tune the retry cadence
	publisher, err := system.Spawn(ctx, publisherName, &OrderPublisher{},
		actor.AsReliableProducer(processorName, actor.WithLocalRetryInterval(retryInterval)))
	must(err)

	processor, err := system.Spawn(ctx, processorName, &OrderProcessor{},
		actor.AsReliableConsumer(publisherName, actor.WithResendInterval(retryInterval)))
	must(err)

	fmt.Println("== act 1: five orders flow end to end ==")

	for i := 1; i <= 5; i++ {
		orderID := fmt.Sprintf("ord-%d", i)
		must(actor.Tell(ctx, publisher, &PublishOrder{OrderID: orderID, Payload: &testpb.Reply{Content: orderID}}))
	}

	must(await("five processed orders", func() bool {
		return len(processedOrders(ctx, processor)) == 5
	}))

	fmt.Println("== act 2: an unserializable payload disables the flow ==")

	// the poisoned order is handed to the controller and lost with the
	// failed flow; remediation is the operator's job, not a silent retry
	must(actor.Tell(ctx, publisher, &PublishOrder{OrderID: "ord-6", Payload: &unserializableOrder{}}))

	var failure *actor.ReliableDeliveryFailed

	must(await("the ReliableDeliveryFailed event", func() bool {
		for message := range subscriber.Iterator() {
			if candidate, ok := message.Payload().(*actor.ReliableDeliveryFailed); ok {
				failure = candidate
			}
		}
		return failure != nil
	}))

	if failure.EndpointName() != publisherName ||
		failure.ControllerRole() != actor.ReliableControllerRoleProducer ||
		failure.Stage() != actor.ReliableDeliveryStageProtocol {
		fail(fmt.Sprintf("unexpected failure event: endpoint=%s role=%s", failure.EndpointName(), failure.ControllerRole()))
	}

	fmt.Printf("[event] reliable delivery failed: endpoint=%s role=%s cause=%v\n", failure.EndpointName(), failure.ControllerRole(), failure.Err())

	// orders submitted during the outage wait in the publisher's queue
	must(actor.Tell(ctx, publisher, &PublishOrder{OrderID: "ord-7", Payload: &testpb.Reply{Content: "ord-7"}}))

	fmt.Println("== act 3: ReSpawn recreates the controller and the flow resumes ==")

	_, err = system.ReSpawn(ctx, publisherName)
	must(err)

	must(actor.Tell(ctx, publisher, &PublishOrder{OrderID: "ord-8", Payload: &testpb.Reply{Content: "ord-8"}}))

	must(await("the queued and new orders after recovery", func() bool {
		return len(processedOrders(ctx, processor)) == 7
	}))

	expected := []string{"ord-1", "ord-2", "ord-3", "ord-4", "ord-5", "ord-7", "ord-8"}
	if processed := processedOrders(ctx, processor); !slices.Equal(processed, expected) {
		fail(fmt.Sprintf("processed %v, want %v", processed, expected))
	}

	must(system.Stop(ctx))
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
	deadline := time.Now().Add(10 * time.Second)

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
