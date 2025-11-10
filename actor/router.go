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
	"sync/atomic"
	"time"

	"github.com/flowchartsman/retry"
	"google.golang.org/protobuf/proto"

	gerrors "github.com/tochemey/goakt/v3/errors"
	"github.com/tochemey/goakt/v3/goaktpb"
	"github.com/tochemey/goakt/v3/internal/ticker"
	"github.com/tochemey/goakt/v3/log"
)

type routerKind int

const (
	standardRouter routerKind = iota
	scatterGatherFirstRouter
	tailChoppingRouter
)

type routeeSupervisorDirective int

const (
	restartRoutee routeeSupervisorDirective = iota
	stopRoutee
	resumeRoutee
)

// router is an actor that depending upon the routing
// strategy route message to its routees.
type router struct {
	routingStrategy       RoutingStrategy
	poolSize              int
	routeesMap            map[string]*PID
	routeesKind           reflect.Type
	supervisorDirective   routeeSupervisorDirective
	restartRouteeAttempts uint32
	restartRouteeWithin   time.Duration
	roundRobinNext        uint32
	logger                log.Logger

	// these fields are only used for tail chopping routing strategy and scatter-gather
	within   time.Duration
	interval time.Duration

	kind routerKind
	name string
}

var _ Actor = (*router)(nil)

// newRouter creates an instance of router giving the routing strategy and poolSize
// The poolSize specifies the number of routees to spawn by the router
func newRouter(poolSize int, routeesKind Actor, logger log.Logger, opts ...RouterOption) *router {
	router := &router{
		routingStrategy:     FanOutRouting,
		poolSize:            poolSize,
		routeesMap:          make(map[string]*PID, poolSize),
		routeesKind:         reflect.TypeOf(routeesKind).Elem(),
		logger:              logger,
		kind:                standardRouter,
		supervisorDirective: stopRoutee,

		// TODO: revisit these defaults
		restartRouteeAttempts: 3,
		restartRouteeWithin:   time.Second,
	}

	// apply the various options
	for _, opt := range opts {
		opt.Apply(router)
	}
	return router
}

// PreStart pre-starts the actor.
func (x *router) PreStart(ctx *Context) error {
	x.name = ctx.ActorName()
	x.logger.Infof("starting the router (%s)...", x.name)
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
	x.logger.Infof("router (%s) stopped", x.name)
	return nil
}

// postStart spawns routeesMap
func (x *router) postStart(ctx *ReceiveContext) {
	x.logger.Infof("router (%s) successfully started", x.name)
	x.logger.Infof("router (%s) spawning (%d) routees...", x.name, x.poolSize)
	x.spawnRoutees(ctx, 0, x.poolSize)
	ctx.Become(x.broadcast)
}

// broadcast send message to all the routeesMap
func (x *router) broadcast(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *goaktpb.Broadcast:
		x.handleBroadcast(ctx)
	case *goaktpb.PanicSignal:
		x.handlePanicSignal(ctx)
	case *goaktpb.GetRoutees:
		x.handleGetRoutees(ctx)
	case *goaktpb.AdjustRouterPoolSize:
		x.handleAjustRouterPoolSize(ctx)
	default:
		ctx.Unhandled()
	}
}

func (x *router) handleAjustRouterPoolSize(ctx *ReceiveContext) {
	message := ctx.Message().(*goaktpb.AdjustRouterPoolSize)
	delta := int(message.GetPoolSize())
	if delta == 0 {
		// nothing to do
		return
	}

	if delta > 0 {
		x.scaleUp(ctx, delta)
		return
	}

	x.scaleDown(ctx, -delta)
}

func (x *router) scaleUp(ctx *ReceiveContext, delta int) {
	currentSize := len(x.routeesMap)
	targetSize := currentSize + delta
	x.logger.Infof("scaling up router (%s) pool size from %d to %d", x.name, currentSize, targetSize)
	x.poolSize = targetSize
	x.spawnRoutees(ctx, currentSize, targetSize)
}

func (x *router) scaleDown(ctx *ReceiveContext, delta int) {
	routees, ok := x.availableRoutees()
	if !ok {
		return
	}

	currentSize := len(routees)
	if delta > currentSize {
		delta = currentSize
	}

	targetSize := currentSize - delta
	x.logger.Infof("scaling down router (%s) pool size from %d to %d", x.name, currentSize, targetSize)
	x.poolSize = targetSize

	for i := 0; i < delta; i++ {
		routee := routees[i]
		x.logger.Infof("stopping routee (%s)...", routee.ID())
		ctx.Stop(routee)
		delete(x.routeesMap, routee.ID())
	}
}

func (x *router) spawnRoutees(ctx *ReceiveContext, start, size int) {
	for i := start; i < size; i++ {
		routeeName := routeeName(i, x.name)
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
}

func (x *router) handleGetRoutees(ctx *ReceiveContext) {
	routees, _ := x.availableRoutees()
	names := make([]string, 0, len(routees))
	for _, routee := range routees {
		names = append(names, routee.Name())
	}
	ctx.Response(&goaktpb.Routees{
		Names: names,
	})
}

func (x *router) handlePanicSignal(ctx *ReceiveContext) {
	switch x.supervisorDirective {
	case restartRoutee:
		x.handleRestartRoutee(ctx)
	case stopRoutee:
		x.handleStopRoutee(ctx)
	case resumeRoutee:
		x.handleResumeRoutee(ctx)
	default:
		x.handleStopRoutee(ctx)
	}
}

func (x *router) handleRestartRoutee(ctx *ReceiveContext) {
	goCtx := ctx.withoutCancel()
	sender := ctx.Sender()
	x.logger.Infof("restarting routee (%s)...", sender.ID())

	var err error

	switch {
	case x.restartRouteeAttempts == 0 || x.restartRouteeWithin <= 0:
		err = sender.Restart(goCtx)
	default:
		retrier := retry.NewRetrier(int(x.restartRouteeAttempts), x.restartRouteeWithin, x.restartRouteeWithin)
		err = retrier.RunContext(goCtx, sender.Restart)
	}

	if err != nil {
		x.logger.Errorf("failed to restart routee (%s): %v", sender.ID(), err)
		x.handleStopRoutee(ctx)
		return
	}

	x.logger.Infof("routee (%s) restarted successfully", sender.ID())
	x.routeesMap[sender.ID()] = sender
}

func (x *router) handleResumeRoutee(ctx *ReceiveContext) {
	sender := ctx.Sender()
	x.logger.Infof("resuming routee (%s)...", sender.ID())
	ctx.Reinstate(sender)
}

func (x *router) handleStopRoutee(ctx *ReceiveContext) {
	sender := ctx.Sender()
	x.logger.Infof("stopping routee (%s)...", sender.ID())
	ctx.Stop(sender)
	delete(x.routeesMap, sender.ID())
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
	switch x.routingStrategy {
	case RoundRobinRouting:
		n := atomic.AddUint32(&x.roundRobinNext, 1)
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

	askRoutee := func(routee *PID) {
		remaining := time.Until(time.Now().Add(0))
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
	askRoutee(shuffled[0])

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

			x.logger.Warnf("tail-chopping: attempt failed: %v", r.err)
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
				askRoutee(shuffled[next])
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
func routeeName(index int, routerName string) string {
	return fmt.Sprintf("%s%s%d", routerName, routeeNamePrefix, index)
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
