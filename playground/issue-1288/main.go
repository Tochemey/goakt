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

// Package main is a living sample for github.com/Tochemey/goakt/issues/1288:
// grain timers. Two order grains are placed; one pays in time and its payment
// timeout is cancelled, the other misses the deadline and its timeout fires.
// Along the way the sample exercises every facet of the feature: an interval
// timer started from OnActivate, a one-shot timeout with an explicit reference,
// cancellation from a message handler, a self-cancelling poll timer, ticks not
// preventing passivation, and a reactivated grain starting fresh with no timers.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
)

const (
	paymentDeadline = 1500 * time.Millisecond
	pollInterval    = 300 * time.Millisecond
	auditInterval   = 400 * time.Millisecond
	passivateAfter  = 1500 * time.Millisecond
)

// order statuses
const (
	statusNew             = "new"
	statusAwaitingPayment = "awaiting-payment"
	statusPaid            = "paid"
	statusCancelled       = "cancelled"
	statusDelivered       = "delivered"
)

// timer references
const (
	paymentTimeoutRef = "payment-timeout"
	shipmentPollRef   = "shipment-poll"
)

type (
	// PlaceOrder opens the order and arms the payment deadline.
	PlaceOrder struct{}
	// PaymentReceived settles the payment and starts shipment polling.
	PaymentReceived struct{}
	// GetStatus asks the grain for its current status.
	GetStatus struct{}

	// paymentTimeout is the one-shot tick fired when the deadline is missed.
	paymentTimeout struct{}
	// pollShipment is the interval tick driving the shipment status checks.
	pollShipment struct{}
	// auditTick is the activation-long heartbeat started from OnActivate.
	auditTick struct{}
)

// OrderGrain walks an order through awaiting-payment, paid, and delivered (or
// cancelled when the payment deadline passes). All of its periodic behavior is
// driven by grain timers, so everything stops the moment the grain passivates.
type OrderGrain struct {
	status string
	polls  int
}

var _ actor.Grain = (*OrderGrain)(nil)

func (g *OrderGrain) OnActivate(_ context.Context, props *actor.GrainProps) error {
	g.status = statusNew

	// The canonical pattern: periodic behavior starts in OnActivate. The audit
	// tick runs for as long as this activation lives. It is not registered with
	// WithTimerKeepAlive, so it does not stop the grain from passivating.
	_, err := props.Schedule(&auditTick{}, auditInterval)
	return err
}

func (g *OrderGrain) OnReceive(ctx *actor.GrainContext) {
	name := ctx.Self().Name()

	switch ctx.Message().(type) {
	case *PlaceOrder:
		g.status = statusAwaitingPayment

		// a one-shot with an explicit reference so it can be cancelled when
		// the payment arrives in time
		_, err := ctx.ScheduleOnce(&paymentTimeout{}, paymentDeadline, actor.WithTimerReference(paymentTimeoutRef))
		if err != nil {
			ctx.Err(err)
			return
		}

		fmt.Printf("[%s] placed, payment due within %v\n", name, paymentDeadline)
		ctx.NoErr()

	case *PaymentReceived:
		// the confirmation arrived first: disarm the deadline
		_ = ctx.CancelSchedule(paymentTimeoutRef)
		g.status = statusPaid

		_, err := ctx.Schedule(&pollShipment{}, pollInterval, actor.WithTimerReference(shipmentPollRef))
		if err != nil {
			ctx.Err(err)
			return
		}

		fmt.Printf("[%s] paid, polling shipment every %v\n", name, pollInterval)
		ctx.NoErr()

	case *paymentTimeout:
		g.status = statusCancelled
		fmt.Printf("[%s] payment deadline missed, order cancelled\n", name)
		ctx.NoErr()

	case *pollShipment:
		g.polls++
		fmt.Printf("[%s] shipment poll #%d\n", name, g.polls)

		if g.polls >= 3 {
			// a timer cancelling itself from its own tick handler
			_ = ctx.CancelSchedule(shipmentPollRef)
			g.status = statusDelivered
			fmt.Printf("[%s] delivered\n", name)
		}
		ctx.NoErr()

	case *auditTick:
		fmt.Printf("[%s] audit: status=%s\n", name, g.status)
		ctx.NoErr()

	case *GetStatus:
		ctx.Response(g.status)

	default:
		ctx.Unhandled()
	}
}

func (g *OrderGrain) OnDeactivate(_ context.Context, props *actor.GrainProps) error {
	// no timer cleanup needed: the registry is already stopped at this point
	fmt.Printf("[%s] deactivated, all timers cancelled automatically\n", props.Identity().Name())
	return nil
}

func main() {
	ctx := context.Background()

	system, err := actor.NewActorSystem("orders", actor.WithLogger(log.DiscardLogger))
	must(err)
	must(system.Start(ctx))

	paid, err := actor.GrainOf[*OrderGrain](ctx, system, "order-42", actor.WithGrainDeactivateAfter(passivateAfter))
	must(err)
	unpaid, err := actor.GrainOf[*OrderGrain](ctx, system, "order-13", actor.WithGrainDeactivateAfter(passivateAfter))
	must(err)

	fmt.Println("== placing two orders: order-42 pays in time, order-13 never pays ==")
	must(system.TellGrain(ctx, paid, &PlaceOrder{}))
	must(system.TellGrain(ctx, unpaid, &PlaceOrder{}))

	pause.For(500 * time.Millisecond)
	must(system.TellGrain(ctx, paid, &PaymentReceived{}))

	waitForStatus(ctx, system, paid, statusDelivered)
	waitForStatus(ctx, system, unpaid, statusCancelled)

	fmt.Println("== both orders settled; going idle so passivation reclaims the grains ==")
	waitUntil(func() bool {
		return len(system.Grains(ctx, time.Second)) == 0
	}, 10*time.Second, "grains to passivate while their audit timers keep ticking")

	// A passivated grain reactivates fresh: volatile timers and in-memory state
	// are gone, and OnActivate runs again. Real applications reload state from
	// storage there.
	if status := askStatus(ctx, system, paid); status != statusNew {
		fail("expected order-42 to reactivate with status %q, got %q", statusNew, status)
	}

	fmt.Println("== order-42 reactivated fresh with status \"new\": timers and state are activation-scoped ==")

	must(system.Stop(ctx))
	fmt.Println("OK")
}

// askStatus fetches the grain's current status.
func askStatus(ctx context.Context, system actor.ActorSystem, id *actor.GrainIdentity) string {
	response, err := system.AskGrain(ctx, id, &GetStatus{}, time.Second)
	must(err)
	return response.(string)
}

// waitForStatus polls the grain until it reports the wanted status.
func waitForStatus(ctx context.Context, system actor.ActorSystem, id *actor.GrainIdentity, wanted string) {
	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		if askStatus(ctx, system, id) == wanted {
			return
		}
		pause.For(100 * time.Millisecond)
	}

	fail("timed out waiting for %s to reach status %q", id.Name(), wanted)
}

// waitUntil polls condition until it holds.
func waitUntil(condition func() bool, timeout time.Duration, what string) {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		pause.For(100 * time.Millisecond)
	}

	fail("timed out waiting for %s", what)
}

func must(err error) {
	if err != nil {
		fail("%v", err)
	}
}

func fail(format string, args ...any) {
	fmt.Printf("FAILED: "+format+"\n", args...)
	os.Exit(1)
}
