/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package actor

import (
	"context"
	"fmt"
	"math/rand/v2"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/ticker"
	"github.com/tochemey/goakt/v3/log"
)

// RouterOption is the interface that applies a configuration option.
type RouterOption interface {
	// Apply sets the Option value of a config.
	Apply(cl *router)
}

// enforce compilation error
var _ RouterOption = RouterOptionFunc(nil)

// RouterOptionFunc implements the Option interface.
type RouterOptionFunc func(router *router)

func (f RouterOptionFunc) Apply(c *router) {
	f(c)
}

// WithRoutingStrategy sets the routing strategy to use by the router
// to forward messages to its routees. By default, FanOutRouting is used.
// ⚠️ Note: Fan-out, round-robin, and random strategies are fire-and-forget; they never wait for replies.
// When you need the router to observe replies consider Scatter-Gather First or Tail-Chopping,
// but remember that routers always operate asynchronously—the outcome is delivered back to the sender
// as a new message rather than through Ask.
func WithRoutingStrategy(strategy RoutingStrategy) RouterOption {
	return RouterOptionFunc(func(r *router) {
		r.strategy = strategy
	})
}

// AsTailChopping configures the router to use the Tail-Chopping routing strategy.
//
// Tail-Chopping prioritizes predictable latency over full concurrency by probing one
// routee at a time (in random order) and only moving on when the current attempt does
// not respond fast enough. This avoids thundering herds while still racing to find
// any healthy worker within a bounded deadline. Replies are forwarded asynchronously
// back to the Broadcast sender; the router itself never blocks waiting for results.
//
// Sequence (conceptual):
//  1. Shuffle live routees to avoid bias.
//  2. Send the message to the first routee.
//  3. If no response arrives before interval, send to the next routee.
//  4. Repeat until:
//     - A routee replies (first success wins and the rest are cancelled).
//     - All routees have been attempted.
//     - The cumulative time exceeds within (global deadline).
//  5. If the deadline expires without success, the sender receives a StatusFailure.
//
// Parameters:
//
//	within   - Total time budget allowed for obtaining any successful response.
//	           Must be > 0. Starts counting when the first routee receives the message.
//	interval - Delay between successive attempts. Must be > 0 and < within for multiple routees
//	           to be attempted. If interval >= within only the first routee is tried.
//
// Recommended tuning guidelines:
//   - interval ≈ p95 of expected fast response time so that healthy actors reply before the next probe fires.
//   - within  ≈ (number_of_routees * interval) or a business SLO bound to cap worst-case latency.
//   - Ensure interval << within to allow multiple attempts; otherwise only the first probe runs.
//
// Example:
//
//	// Create a tail-chopping router that will try for up to 2s,
//	// issuing a new attempt every 200ms until a reply is obtained.
//	r := newRouter(5, &MyWorker{}, logger, AsTailChopping(2*time.Second, 200*time.Millisecond))
//
// Use Scatter-Gather when you need the absolute fastest responder; use Tail-Chopping
// when you prefer controlled sequential probing with upper-bounded fan-out.
//
// Returns a RouterOption that sets internal state used by the router.
func AsTailChopping(within, interval time.Duration) RouterOption {
	return RouterOptionFunc(func(r *router) {
		r.kind = tailChoppingRouter
		r.within = within
		r.interval = interval
	})
}

// AsScatterGatherFirst configures the router to use the Scatter‑Gather First routing strategy.
//
// Pattern summary:
//   - Sends the same request to all currently live routees concurrently.
//   - Returns the first successful reply within the time budget (within).
//   - Late replies are ignored once the first result is returned or the deadline elapses.
//   - Replies arrive asynchronously to the Broadcast sender; the router never blocks.
//
// Behavioral contract (first-response-wins):
//   - Every routee may work on the request (duplicate work is expected).
//   - The first successful reply is forwarded to the original sender; remaining replies are dropped.
//   - If no routee replies before within, the sender receives a StatusFailure.
//   - If no live routees exist at send time, fail fast to avoid wasting time.
//
// Parameters:
//
//	within: Total time budget for obtaining any successful response.
//	        Must be > 0. Choose based on SLOs or empirical latency.
//
// Tuning guidelines:
//   - within ≈ p95/p99 of expected fast response or a business SLO so healthy routees usually win.
//   - For large pools, consider sampling a subset (e.g., nearest N) or capping concurrency to avoid storms.
//   - Add a small safety margin for serialization and scheduling overheads before declaring a timeout.
//
// Example:
//
//	// Create a scatter‑gather router that waits up to 500ms for any reply.
//	r := newRouter(10, &MyWorker{}, logger, AsScatterGatherFirst(500*time.Millisecond))
//
//	payload, _ := anypb.New(&MyRequest{})
//	ctx.Tell(rPID, &goaktpb.Broadcast{Message: payload})
//
//	// Later, handle the first successful reply (or StatusFailure) asynchronously
//	// in the sender's Receive.
//
// Errors and edge cases:
//   - within <= 0 should be validated by the caller before passing.
//   - Expect a timeout error after within elapses without any reply.
//   - Decide what constitutes “successful reply” (e.g., non‑error response type).
//
// Returns a RouterOption that sets internal state used by the router.
func AsScatterGatherFirst(within time.Duration) RouterOption {
	return RouterOptionFunc(func(r *router) {
		r.kind = scatterGatherFirstRouter
		r.within = within
	})
}

// RoutingStrategy defines how a router actor forwards incoming messages to its routees.
//
// Available strategies:
//   - RoundRobinRouting:
//     Distributes messages one at a time to each routee in sequence.
//     Useful for balancing uniform, stateless workloads.
//     Example:
//     // creates a router that round-robins messages across 5 workers
//     r := newRouter(5, &MyWorker{}, logger, WithRoutingStrategy(RoundRobinRouting))
//   - RandomRouting:
//     Chooses a routee at random for every message.
//     Useful when uneven load patterns are acceptable or desired.
//     Example:
//     r := newRouter(10, &MyWorker{}, logger, WithRoutingStrategy(RandomRouting))
//   - FanOutRouting:
//     Broadcasts each message to all active routees concurrently.
//     Useful for pub/sub, cache invalidation, or multi-sink processing.
//     Example:
//     r := newRouter(3, &EventConsumer{}, logger, WithRoutingStrategy(FanOutRouting))
//
// Note: If a routee stops, it is removed from the internal map and no longer receives messages.
type RoutingStrategy int

const (
	// RoundRobinRouting sends each incoming message to the next routee in order,
	// cycling back to the first after the last. Provides even distribution.
	RoundRobinRouting RoutingStrategy = iota
	// RandomRouting selects a routee uniformly at random for each message.
	RandomRouting
	// FanOutRouting broadcasts every message to all currently available routees.
	FanOutRouting
)

type routerKind int

const (
	simpleRouter routerKind = iota
	scatterGatherFirstRouter
	tailChoppingRouter
)

// router is an actor that depending upon the routing
// strategy route message to its routees.
type router struct {
	strategy RoutingStrategy
	poolSize int
	// list of routees
	routeesMap  map[string]*PID
	next        uint32
	routeesKind reflect.Type
	logger      log.Logger

	// these fields are only used for tail chopping routing strategy and scatter-gather
	within   time.Duration
	interval time.Duration
	kind     routerKind
}

var _ Actor = (*router)(nil)

// newRouter creates an instance of router giving the routing strategy and poolSize
// The poolSize specifies the number of routees to spawn by the router
func newRouter(poolSize int, routeesKind Actor, loggger log.Logger, opts ...RouterOption) *router {
	router := &router{
		strategy:    FanOutRouting,
		poolSize:    poolSize,
		routeesMap:  make(map[string]*PID, poolSize),
		routeesKind: reflect.TypeOf(routeesKind).Elem(),
		logger:      loggger,
		kind:        simpleRouter,
	}

	// apply the various options
	for _, opt := range opts {
		opt.Apply(router)
	}
	return router
}

// PreStart pre-starts the actor.
func (x *router) PreStart(*Context) error {
	x.logger.Info("starting the router...")
	return x.validate()
}

// Receive handles messages sent to the router
func (x *router) Receive(ctx *ReceiveContext) {
	message := ctx.Message()
	switch message.(type) {
	case *goaktpb.PostStart:
		x.postStart(ctx)
	default:
		ctx.Unhandled()
	}
}

// PostStop is executed when the actor is shutting down.
func (x *router) PostStop(*Context) error {
	x.logger.Info("router stopped")
	return nil
}

// postStart spawns routeesMap
func (x *router) postStart(ctx *ReceiveContext) {
	x.logger.Info("router successfully started")
	x.logger.Infof("spawning %d routees...", x.poolSize)
	for i := 0; i < x.poolSize; i++ {
		routeeName := routeeName(ctx.Self().Name(), i)
		actor := reflect.New(x.routeesKind).Interface().(Actor)
		routee := ctx.Spawn(routeeName, actor,
			asSystem(),
			WithRelocationDisabled(),
			WithLongLived(),
			WithSupervisor(
				NewSupervisor(WithAnyErrorDirective(EscalateDirective)),
			))
		x.routeesMap[routee.ID()] = routee
	}
	ctx.Become(x.broadcast)
}

// broadcast send message to all the routeesMap
func (x *router) broadcast(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.Broadcast:
		x.handleBroadcast(ctx)
	case *goaktpb.PanicSignal:
		ctx.Stop(ctx.Sender())
		delete(x.routeesMap, ctx.Sender().ID())
	default:
		ctx.Unhandled()
	}
}

func (x *router) handleBroadcast(ctx *ReceiveContext) {
	sender := ctx.Sender()
	if sender == nil {
		// push message to deadletter
		ctx.Unhandled()
		return
	}

	routees, ok := x.availableRoutees()
	if !ok {
		x.handleNoRoutees(ctx)
		return
	}

	message := ctx.Message().(*goaktpb.Broadcast)
	payload, err := message.GetMessage().UnmarshalNew()
	if err != nil {
		ctx.Err(err)
		return
	}

	x.dispatchToRoutees(ctx, payload, routees)
}

func (x *router) handleNoRoutees(ctx *ReceiveContext) {
	x.logger.Warn("no routees available. stopping.... Bye")
	// push message to deadletter
	ctx.Unhandled()
	// shutdown
	ctx.Shutdown()
}

// dispatchToRoutees selects the appropriate routing algorithm for the current message
// and forwards it to the configured pool of routees.
//
// Flow:
//  1. Specialized router kinds (scatter-gather-first or tail-chopping) take precedence
//     because they encode bespoke behaviors that ignore the generic strategy field.
//  2. If the router is a plain one, the configured RoutingStrategy determines how the
//     message is fanned out (round-robin, random, or fan-out).
//
// The method keeps the router non-blocking: every Tell happens asynchronously and
// specialized strategies offload long-running work into goroutines.
func (x *router) dispatchToRoutees(ctx *ReceiveContext, msg proto.Message, routees []*PID) {
	switch x.kind {
	case tailChoppingRouter:
		x.tailChopping(ctx, msg, routees)
	case scatterGatherFirstRouter:
		x.scatterGatherFirst(ctx, msg, routees)
	default:
		x.routeByStrategy(ctx, msg, routees)
	}
}

func (x *router) routeByStrategy(ctx *ReceiveContext, msg proto.Message, routees []*PID) {
	switch x.strategy {
	case RoundRobinRouting:
		n := atomic.AddUint32(&x.next, 1)
		routee := routees[(int(n)-1)%len(routees)]
		ctx.Tell(routee, msg)
	case RandomRouting:
		routee := routees[rand.IntN(len(routees))] //nolint:gosec
		ctx.Tell(routee, msg)
	default:
		for _, routee := range routees {
			go func(pid *PID) {
				ctx.Tell(pid, msg)
			}(routee)
		}
	}
}

// scatterGatherFirst fans a single request out to every currently live routee and relays
// the earliest successful reply back to the original sender.
//
// Algorithm overview:
//  1. Clone the payload (when possible) and concurrently Ask every routee using the router's
//     system-level noSender PID so the target actors do not observe the router itself as sender.
//  2. All outstanding asks share the same deadline context bounded by r.within; late responses
//     automatically error with ErrRequestTimeout.
//  3. The first ask that completes without error wins: its reply is forwarded via Tell to whoever
//     initiated the Broadcast, and the deadline context is canceled to short-circuit slower asks.
//  4. Each failure is logged; if every routee errors or the deadline elapses, the router replays a
//     StatusFailure back to the sender so the workflow can decide how to recover.
//
// The method never blocks the router actor: all IO happens in goroutines and outcomes are pushed
// asynchronously back to the sender, preserving the router's fire-and-forget contract.
func (x *router) scatterGatherFirst(ctx *ReceiveContext, msg proto.Message, routees []*PID) {
	logger := ctx.Logger()
	sender := ctx.Sender()
	broadcast := ctx.Message().(*goaktpb.Broadcast)
	within := x.within

	sendTimeout := func() {
		ctx.Tell(sender, &goaktpb.StatusFailure{
			Error:   gerrors.ErrRequestTimeout.Error(),
			Message: broadcast.GetMessage(),
		})
	}

	noSender := ctx.ActorSystem().NoSender()

	deadlineCtx, cancel := context.WithTimeout(ctx.Context(), within)
	defer cancel()
	type askResult struct {
		resp proto.Message
		err  error
	}

	results := make(chan askResult, len(routees))

	for _, routee := range routees {
		payload := proto.Clone(msg)
		go func(to *PID, payload proto.Message) {
			resp, err := noSender.Ask(deadlineCtx, to, payload, within)
			results <- askResult{resp: resp, err: err}
		}(routee, payload)
	}

	pending := len(routees)

	for {
		select {
		case <-deadlineCtx.Done():
			// no need to drain: channel is buffered
			sendTimeout()
			return

		case r := <-results:
			pending--
			if r.err == nil {
				// first success wins
				cancel()
				ctx.Tell(sender, r.resp)
				return
			}

			logger.Warnf("scatter-gather-first: attempt failed: %v", r.err)
			if pending == 0 {
				sendTimeout()
				return
			}
		}
	}
}

// tailChopping implements the Tail-Chopping routing pattern.
//
// Algorithm outline:
//  1. Shuffle the live routees to avoid bias and launch an Ask to the first one immediately.
//  2. Track the overall deadline (within) using a context; every Ask shares that context so any
//     response past the global deadline turns into ErrRequestTimeout.
//  3. Use a ticking clock with the configured interval to decide when to send the next Ask. Each
//     new attempt reuses the remaining time until the global deadline as its per-request timeout.
//  4. At most one Ask is outstanding per routee; pending count keeps back-pressure so failures
//     can trigger subsequent attempts.
//  5. The first Ask that succeeds immediately tells the original sender and cancels the deadline
//     context, shutting down the remaining goroutines. If all routees fail or the deadline expires
//     before any success, a StatusFailure is reported back.
//
// This keeps router behavior asynchronous: the router never blocks, and replies arrive to the
// sender as ordinary messages rather than Ask responses.
func (x *router) tailChopping(ctx *ReceiveContext, msg proto.Message, routees []*PID) {
	logger := ctx.Logger()
	sender := ctx.Sender()
	broadcast := ctx.Message().(*goaktpb.Broadcast)
	interval := x.interval
	within := x.within

	sendTimeout := func() {
		ctx.Tell(sender, &goaktpb.StatusFailure{
			Error:   gerrors.ErrRequestTimeout.Error(),
			Message: broadcast.GetMessage(),
		})
	}

	shuffled := reshuffleRoutees(routees)
	noSender := ctx.ActorSystem().NoSender()

	deadlineCtx, cancel := context.WithTimeout(ctx.Context(), within)
	defer cancel()

	type askResult struct {
		resp proto.Message
		err  error
	}

	results := make(chan askResult, len(shuffled))

	launchAsk := func(routee *PID) {
		remaining := time.Until(time.Now().Add(0)) // placeholder; replaced below
		if deadline, ok := deadlineCtx.Deadline(); ok {
			remaining = time.Until(deadline)
		}

		if remaining <= 0 {
			results <- askResult{err: gerrors.ErrRequestTimeout}
			return
		}

		payload := proto.Clone(msg)
		go func(to *PID, timeout time.Duration) {
			resp, err := noSender.Ask(deadlineCtx, to, payload, timeout)
			results <- askResult{resp: resp, err: err}
		}(routee, remaining)
	}

	// always launch first
	pending := 1
	next := 1
	launchAsk(shuffled[0])

	var clock *ticker.Ticker
	if len(shuffled) > 1 && interval > 0 && interval < within {
		clock = ticker.New(interval)
		clock.Start()
		defer clock.Stop()
	}

	for {
		select {
		case <-deadlineCtx.Done():
			// no need to drain: channel is buffered
			sendTimeout()
			return

		case r := <-results:
			pending--
			if r.err == nil {
				// first success wins
				cancel()
				ctx.Tell(sender, r.resp)
				return
			}

			logger.Warnf("tail-chopping: attempt failed: %v", r.err)
			if pending == 0 && next >= len(shuffled) {
				sendTimeout()
				return
			}

		case <-func() <-chan time.Time {
			if clock == nil {
				return nil
			}
			return clock.Ticks
		}():
			if next < len(shuffled) {
				launchAsk(shuffled[next])
				pending++
				next++
				if next >= len(shuffled) && clock != nil {
					clock.Stop()
				}
			}
		}
	}
}

// routeeName returns the routee name
func routeeName(routerName string, routeeIndex int) string {
	return fmt.Sprintf("%s-%s-%d", routeeNamePrefix, routerName, routeeIndex)
}

func (x *router) availableRoutees() ([]*PID, bool) {
	routees := make([]*PID, 0, x.poolSize)
	for _, routee := range x.routeesMap {
		if !routee.IsRunning() {
			delete(x.routeesMap, routee.ID())
		}
		routees = append(routees, routee)
	}
	return routees, len(routees) > 0
}

// validate checks if the router is properly configured
func (x *router) validate() error {
	if x.poolSize <= 0 {
		return gerrors.ErrInvalidRouterPoolSize
	}

	if x.kind == tailChoppingRouter {
		if x.interval <= 0 || x.within <= 0 {
			return gerrors.ErrTailChopingRouterMisconfigured
		}
	}

	if x.kind == scatterGatherFirstRouter {
		if x.within <= 0 {
			return gerrors.ErrScatterGatherFirstRouterMisconfigured
		}
	}

	return nil
}

func reshuffleRoutees(routees []*PID) []*PID {
	n := len(routees)
	shuffled := make([]*PID, n)
	copy(shuffled, routees)

	rand.Shuffle(n, func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	return shuffled
}

func isRouter(name string) bool {
	return strings.EqualFold(name, reservedNames[routerType])
}
