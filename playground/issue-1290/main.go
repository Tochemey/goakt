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

// Package main is a living sample for github.com/Tochemey/goakt/issues/1290:
// grain request scheduling (reentrancy). A checkout pipeline fans out three
// concurrent downstream calls from one order grain without ever blocking a
// handler: an inventory reserve (grain to grain), a fraud screen with a
// per-call timeout and a manual-review fallback (grain to actor), and a
// payment charge (grain to actor). The external AskGrain is answered through
// a deferred reply completed by whichever continuation lands last.
//
// Along the way the sample exercises every facet of the feature: AllowAll
// keeping the order grain responsive to status reads mid-checkout,
// StashNonReentrant enabled at runtime by the inventory grain only for the
// one case that needs it (a supplier restock) and disabled again once stock
// is healthy, the stash pause holding an audit ask until stock is consistent,
// a supplier completing its deferred reply from a grain timer, and error
// identity surviving the request path (errors.Is on ErrRequestTimeout).
//
// Reading order for newcomers: OrderGrain is the core pattern (defer the
// reply, fan out requests from one turn, join in continuations),
// InventoryGrain is the runtime toggles and stash mode, SupplierGrain is a
// deferred reply completed from a later turn.
package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"context"

	"github.com/tochemey/goakt/v4/actor"
	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/pause"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/reentrancy"
)

const (
	espressoUnitPrice    = 3.50
	fraudReviewThreshold = 20.00
	fraudBudget          = 600 * time.Millisecond
	shippingDelay        = 900 * time.Millisecond
	initialStock         = 5
	restockLot           = 20

	paymentsActorName = "payments"
	fraudActorName    = "fraud-screen"
)

// order statuses
const (
	statusIdle       = "idle"
	statusProcessing = "processing"
	statusCompleted  = "completed"
	statusFailed     = "failed"
)

type (
	// Checkout opens the order pipeline; the reply is a Receipt.
	Checkout struct {
		Item     string
		Quantity int
	}
	// Receipt is the deferred answer to a Checkout.
	Receipt struct {
		Item        string
		Quantity    int
		Total       float64
		PaymentRef  string
		FraudNote   string
		Backordered bool
	}
	// GetStatus asks the order grain for a live progress snapshot.
	GetStatus struct{}
	// Status is the answer to GetStatus, served even mid-checkout.
	Status struct {
		State        string
		StepsPending int
	}

	// Reserve asks the inventory grain to set stock aside.
	Reserve struct {
		Quantity int
	}
	// Reserved answers a Reserve; Backordered reports whether the stock had
	// to be restocked from the supplier first.
	Reserved struct {
		Backordered bool
	}
	// AuditStock asks the inventory grain for its current stock level.
	AuditStock struct{}

	// Restock asks the supplier grain for more units.
	Restock struct {
		Quantity int
	}
	// Restocked confirms a delivered shipment.
	Restocked struct {
		Quantity int
	}
	// shipmentArrived is the supplier's internal delivery timer tick.
	shipmentArrived struct{}

	// ScreenOrder asks the fraud actor to vet an order.
	ScreenOrder struct {
		Amount float64
	}
	// ScreenPassed is the fraud actor's approval.
	ScreenPassed struct{}
	// Charge asks the payments actor to capture the amount.
	Charge struct {
		Amount float64
	}
	// PaymentApproved carries the capture reference back.
	PaymentApproved struct {
		Reference string
	}
)

// The grain identities are shared here for sample brevity; a real service
// would hand them to the grains through WithGrainDependencies.
var (
	inventoryID *actor.GrainIdentity
	supplierID  *actor.GrainIdentity
)

// checkoutFlow is the in-flight state of one checkout: the deferred reply to
// the external ask plus the join counter for the three concurrent steps.
// Continuations run on the grain's own turn, so this state needs no locking.
type checkoutFlow struct {
	reply       *actor.GrainReply
	item        string
	quantity    int
	steps       int
	paymentRef  string
	fraudNote   string
	backordered bool
	failure     error
}

// OrderGrain drives the checkout pipeline. It is activated with AllowAll
// reentrancy, so it keeps serving GetStatus reads while a checkout's three
// downstream requests are still in flight.
type OrderGrain struct {
	status string
	flow   *checkoutFlow
}

var _ actor.Grain = (*OrderGrain)(nil)

func (g *OrderGrain) OnActivate(context.Context, *actor.GrainProps) error {
	g.status = statusIdle
	return nil
}

func (g *OrderGrain) OnReceive(ctx *actor.GrainContext) {
	name := ctx.Self().Name()

	switch msg := ctx.Message().(type) {
	case *Checkout:
		total := float64(msg.Quantity) * espressoUnitPrice

		// Take ownership of the reply and fan out. All three requests are
		// issued from this one turn; the handler returns immediately and the
		// grain goes back to its mailbox. Requests must be issued from a
		// handler turn like this one: the continuations below never touch
		// ctx, which is recycled once the turn ends; they only record flow
		// state and complete the reply handle. One checkout is in flight at
		// a time by construction of the sample.
		flow := &checkoutFlow{reply: ctx.DeferResponse(), item: msg.Item, quantity: msg.Quantity, steps: 3}
		g.flow = flow
		g.status = statusProcessing
		fmt.Printf("[%s] checkout: %d x %s ($%.2f), fanning out reserve, fraud screen, and charge\n", name, msg.Quantity, msg.Item, total)

		// grain to grain: reserve the stock. The typed response tells the
		// order whether the goods had to be backordered.
		ctx.RequestGrain(inventoryID, &Reserve{Quantity: msg.Quantity}).Then(func(result any, err error) {
			if err != nil {
				flow.failure = err
			} else {
				flow.backordered = result.(*Reserved).Backordered
			}
			g.finishStep(name, "reserve", flow)
		})

		// grain to actor with a per-call timeout: an unresponsive fraud
		// service degrades to manual review instead of failing the checkout
		ctx.RequestActor(fraudActorName, &ScreenOrder{Amount: total}, actor.WithRequestTimeout(fraudBudget)).Then(func(_ any, err error) {
			switch {
			case err == nil:
				flow.fraudNote = "fraud screen passed"
			case errors.Is(err, gerrors.ErrRequestTimeout):
				flow.fraudNote = "fraud screen timed out, flagged for manual review"
			default:
				flow.failure = err
			}
			g.finishStep(name, "fraud", flow)
		})

		// grain to actor: capture the payment
		ctx.RequestActor(paymentsActorName, &Charge{Amount: total}).Then(func(result any, err error) {
			if err != nil {
				flow.failure = err
			} else {
				flow.paymentRef = result.(*PaymentApproved).Reference
			}
			g.finishStep(name, "charge", flow)
		})

	case *GetStatus:
		// AllowAll: answered immediately, even while a checkout is in flight.
		pending := 0

		if g.flow != nil {
			pending = g.flow.steps
			fmt.Printf("[%s] handling a status read while %d checkout steps are still pending\n", name, pending)
		}

		ctx.Response(&Status{State: g.status, StepsPending: pending})

	default:
		ctx.Unhandled()
	}
}

// finishStep joins the fan-out: the continuation that lands last completes
// the deferred reply. It always runs on the grain's turn.
func (g *OrderGrain) finishStep(name, step string, flow *checkoutFlow) {
	flow.steps--
	fmt.Printf("[%s] step %q done, %d remaining\n", name, step, flow.steps)

	if flow.steps > 0 {
		return
	}
	g.flow = nil

	if flow.failure != nil {
		g.status = statusFailed
		flow.reply.Err(flow.failure)
		return
	}

	g.status = statusCompleted
	flow.reply.Response(&Receipt{
		Item:        flow.item,
		Quantity:    flow.quantity,
		Total:       float64(flow.quantity) * espressoUnitPrice,
		PaymentRef:  flow.paymentRef,
		FraudNote:   flow.fraudNote,
		Backordered: flow.backordered,
	})
}

func (g *OrderGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

// InventoryGrain holds the stock for one product. It is activated WITHOUT
// reentrancy: in-stock reserves are plain request/reply. Only when a reserve
// needs a supplier round trip does it enable StashNonReentrant at runtime, so
// every other message waits until stock is consistent again, and it disables
// the capability once stock is healthy.
type InventoryGrain struct {
	stock            int
	runtimeReentrant bool
}

var _ actor.Grain = (*InventoryGrain)(nil)

func (g *InventoryGrain) OnActivate(context.Context, *actor.GrainProps) error {
	g.stock = initialStock
	return nil
}

func (g *InventoryGrain) OnReceive(ctx *actor.GrainContext) {
	name := ctx.Self().Name()

	switch msg := ctx.Message().(type) {
	case *Reserve:
		if msg.Quantity <= g.stock {
			g.stock -= msg.Quantity

			if g.runtimeReentrant {
				// The supplier case is over and stock is healthy: the
				// capability was needed only for that case, so turn it off.
				ctx.DisableReentrancy()
				g.runtimeReentrant = false
				fmt.Printf("[%s] stock healthy again, reentrancy disabled\n", name)
			}

			fmt.Printf("[%s] reserved %d, stock=%d\n", name, msg.Quantity, g.stock)
			ctx.Response(&Reserved{})
			return
		}

		// Shortfall: enable reentrancy at runtime for this one case. Stash
		// mode pauses the mailbox, so no other message can observe the
		// half-updated stock while the supplier round trip is in flight.
		if err := ctx.EnableReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.StashNonReentrant))); err != nil {
			ctx.Err(err)
			return
		}
		g.runtimeReentrant = true
		fmt.Printf("[%s] stock low (have %d, need %d): reentrancy enabled, pausing for a supplier restock\n", name, g.stock, msg.Quantity)

		reply := ctx.DeferResponse()
		ctx.RequestGrain(supplierID, &Restock{Quantity: restockLot}).Then(func(result any, err error) {
			if err != nil {
				reply.Err(err)
				return
			}

			g.stock += result.(*Restocked).Quantity
			g.stock -= msg.Quantity
			fmt.Printf("[%s] restock landed, reserve completed, stock=%d\n", name, g.stock)
			reply.Response(&Reserved{Backordered: true})
		})

	case *AuditStock:
		// While the grain is paused this answer waits in the mailbox, so an
		// auditor can never see stock mid-restock.
		ctx.Response(g.stock)

	default:
		ctx.Unhandled()
	}
}

func (g *InventoryGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

// SupplierGrain ships asynchronously: it defers the restock reply and
// completes it from a one-shot grain timer once the shipment "arrives".
type SupplierGrain struct {
	pending  *actor.GrainReply
	quantity int
}

var _ actor.Grain = (*SupplierGrain)(nil)

func (g *SupplierGrain) OnActivate(context.Context, *actor.GrainProps) error { return nil }

func (g *SupplierGrain) OnReceive(ctx *actor.GrainContext) {
	name := ctx.Self().Name()

	switch msg := ctx.Message().(type) {
	case *Restock:
		reply := ctx.DeferResponse()

		if _, err := ctx.ScheduleOnce(&shipmentArrived{}, shippingDelay); err != nil {
			reply.Err(err)
			return
		}

		g.pending = reply
		g.quantity = msg.Quantity
		fmt.Printf("[%s] shipping %d units, ETA %v\n", name, msg.Quantity, shippingDelay)

	case *shipmentArrived:
		fmt.Printf("[%s] shipment arrived, confirming restock\n", name)
		g.pending.Response(&Restocked{Quantity: g.quantity})
		g.pending = nil
		ctx.NoErr()

	default:
		ctx.Unhandled()
	}
}

func (g *SupplierGrain) OnDeactivate(context.Context, *actor.GrainProps) error { return nil }

// PaymentsActor approves charges instantly. Its reply travels back to the
// requesting grain as an async response envelope.
type PaymentsActor struct {
	sequence int
}

var _ actor.Actor = (*PaymentsActor)(nil)

func (a *PaymentsActor) PreStart(*actor.Context) error { return nil }

func (a *PaymentsActor) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *Charge:
		a.sequence++
		reference := fmt.Sprintf("pay-%03d", a.sequence)
		fmt.Printf("[%s] captured $%.2f (%s)\n", paymentsActorName, msg.Amount, reference)
		rctx.Response(&PaymentApproved{Reference: reference})
	default:
		rctx.Unhandled()
	}
}

func (a *PaymentsActor) PostStop(*actor.Context) error { return nil }

// FraudActor passes small orders instantly and starts a deep analysis for
// large ones that never answers within the caller's budget, driving the
// order grain's timeout fallback.
type FraudActor struct{}

var _ actor.Actor = (*FraudActor)(nil)

func (a *FraudActor) PreStart(*actor.Context) error { return nil }

func (a *FraudActor) Receive(rctx *actor.ReceiveContext) {
	switch msg := rctx.Message().(type) {
	case *ScreenOrder:
		if msg.Amount < fraudReviewThreshold {
			fmt.Printf("[%s] $%.2f is below the review threshold, passed\n", fraudActorName, msg.Amount)
			rctx.Response(&ScreenPassed{})
			return
		}
		// Deep analysis: deliberately no reply, the caller's timeout fires.
		fmt.Printf("[%s] $%.2f needs deep analysis, this will exceed the caller's budget\n", fraudActorName, msg.Amount)
	default:
		rctx.Unhandled()
	}
}

func (a *FraudActor) PostStop(*actor.Context) error { return nil }

func main() {
	ctx := context.Background()

	system, err := actor.NewActorSystem("checkout", actor.WithLogger(log.DiscardLogger))
	must(err)
	must(system.Start(ctx))

	supplierID, err = actor.GrainOf[*SupplierGrain](ctx, system, "supplier-acme")
	must(err)
	inventoryID, err = actor.GrainOf[*InventoryGrain](ctx, system, "inventory-espresso")
	must(err)
	orderID, err := actor.GrainOf[*OrderGrain](ctx, system, "order-1001",
		actor.WithGrainReentrancy(reentrancy.New(reentrancy.WithMode(reentrancy.AllowAll))))
	must(err)

	_, err = system.Spawn(ctx, paymentsActorName, &PaymentsActor{})
	must(err)
	_, err = system.Spawn(ctx, fraudActorName, &FraudActor{})
	must(err)

	fmt.Println("== checkout 1: 8 espressos ($28.00), needs a supplier restock and a fraud review ==")
	receipts := make(chan *Receipt, 1)

	go func() {
		response, err := system.AskGrain(ctx, orderID, &Checkout{Item: "espresso", Quantity: 8}, 5*time.Second)
		must(err)
		receipts <- response.(*Receipt)
	}()

	// While the inventory grain is paused waiting for the supplier, this audit
	// waits in its mailbox and can only ever observe consistent stock.
	audits := make(chan int, 1)

	go func() {
		pause.For(250 * time.Millisecond)
		response, err := system.AskGrain(ctx, inventoryID, &AuditStock{}, 5*time.Second)
		must(err)
		audits <- response.(int)
	}()

	// AllowAll: the order grain answers status reads while the checkout's
	// three downstream requests are still pending.
	observeInFlightStatus(ctx, system, orderID)

	receipt := <-receipts
	assertEqual("checkout 1 total", 28.00, receipt.Total)
	assertEqual("checkout 1 fraud note", "fraud screen timed out, flagged for manual review", receipt.FraudNote)
	assertEqual("checkout 1 payment ref", "pay-001", receipt.PaymentRef)
	assertEqual("checkout 1 backordered", true, receipt.Backordered)
	fmt.Printf("== receipt 1: $%.2f, %s, backordered=%v (%s) ==\n", receipt.Total, receipt.PaymentRef, receipt.Backordered, receipt.FraudNote)

	// initial 5 + restock 20 - reserved 8 = 17. Any other value would mean the
	// audit slipped through the stash pause and saw intermediate stock.
	assertEqual("audited stock during the restock pause", 17, <-audits)
	fmt.Println("== audit answered only after the restock settled: stock=17 ==")

	fmt.Println("== checkout 2: 2 espressos ($7.00), in stock and below the fraud threshold ==")
	response, err := system.AskGrain(ctx, orderID, &Checkout{Item: "espresso", Quantity: 2}, 5*time.Second)
	must(err)

	receipt = response.(*Receipt)
	assertEqual("checkout 2 total", 7.00, receipt.Total)
	assertEqual("checkout 2 fraud note", "fraud screen passed", receipt.FraudNote)
	assertEqual("checkout 2 payment ref", "pay-002", receipt.PaymentRef)
	assertEqual("checkout 2 backordered", false, receipt.Backordered)
	fmt.Printf("== receipt 2: $%.2f, %s, backordered=%v (%s) ==\n", receipt.Total, receipt.PaymentRef, receipt.Backordered, receipt.FraudNote)

	// The inventory grain disabled reentrancy during checkout 2; this final
	// audit travels the plain ask path and still adds up: 17 - 2 = 15.
	response, err = system.AskGrain(ctx, inventoryID, &AuditStock{}, time.Second)
	must(err)
	assertEqual("final stock", 15, response.(int))
	fmt.Println("== final audit through the plain ask path: stock=15 ==")

	must(system.Stop(ctx))
	fmt.Println("OK")
}

// observeInFlightStatus polls GetStatus until it catches the checkout with
// steps still pending, proving the AllowAll grain never stopped serving.
func observeInFlightStatus(ctx context.Context, system actor.ActorSystem, orderID *actor.GrainIdentity) {
	deadline := time.Now().Add(3 * time.Second)

	for time.Now().Before(deadline) {
		response, err := system.AskGrain(ctx, orderID, &GetStatus{}, time.Second)
		must(err)

		status := response.(*Status)
		if status.State == statusProcessing && status.StepsPending > 0 {
			fmt.Printf("== mid-checkout status read: %s with %d steps pending (grain not blocked) ==\n", status.State, status.StepsPending)
			return
		}
		pause.For(50 * time.Millisecond)
	}

	fail("never observed the checkout in flight through GetStatus")
}

func assertEqual[T comparable](what string, expected, actual T) {
	if expected != actual {
		fail("expected %s to be %v, got %v", what, expected, actual)
	}
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
