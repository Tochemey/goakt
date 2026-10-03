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

package actor

import (
	"context"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"sort"
	"sync/atomic"
	"time"
	"unsafe"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/hash"
	"github.com/tochemey/goakt/v4/internal/retry"
	"github.com/tochemey/goakt/v4/internal/ticker"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/supervisor"
)

type routerKind int

const (
	standardRouter routerKind = iota
	scatterGatherFirstRouter
	tailChoppingRouter
	defaultVirtualNodes = 150
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
	routingStrategy RoutingStrategy
	poolSize        int
	// routees holds the live routees in a stable order: spawn order, with a
	// routee that is re-inserted after a restart or a resume appended at the
	// end. Round-robin indexes this slice, so the rotation is deterministic.
	routees []*PID
	// routeesMap indexes the same set as routees by routee ID for lookups.
	routeesMap map[string]*PID
	// nextRouteeIndex is the index of the next routee name. It only grows, so a
	// routee spawned after a removal never reuses the name of a live routee.
	nextRouteeIndex       int
	routeesKind           reflect.Type
	supervisorDirective   routeeSupervisorDirective
	restartRouteeAttempts uint32
	restartRouteeWithin   time.Duration
	roundRobinNext        uint32
	logger                log.Logger

	// these fields are only used for tail chopping routing strategy and scatter-gather
	within   time.Duration
	interval time.Duration

	// consistent-hash routing fields
	routingKeyExtractor MessageRoutingKeyExtractor
	ring                *consistentHashRing
	virtualNodes        int
	hasher              hash.Hasher

	kind routerKind
	name string
}

var _ Actor = (*router)(nil)

// askResult is the outcome of one Ask issued to a routee by a reply-based strategy.
type askResult struct {
	resp any
	err  error
}

// gather owns one reply-based request (scatter-gather-first or tail-chopping)
// once the router's turn has returned. It asks the routees from its own
// goroutine and delivers the first successful reply, or a StatusFailure carrying
// the original message, to the Broadcast sender. The router therefore never
// waits for routees and keeps handling other messages meanwhile.
type gather struct {
	// router is the owning router; it supplies the time budget, the probing
	// interval and the logger.
	router *router
	// self is the router's PID. Replies are sent through it so the Broadcast
	// sender sees the router as the sender of the outcome.
	self *PID
	// sender is the original Broadcast sender that receives the outcome.
	sender *PID
	// noSender issues the asks so routees do not observe the router as sender.
	noSender *PID
	// message is the unwrapped payload asked of the routees.
	message any
	// ctx is the non-cancellable base context of the router's turn. The asks
	// derive their deadline from it, so the goroutine lives at most `within`.
	ctx context.Context
}

// newRouter creates an instance of router giving the routing strategy and poolSize
// The poolSize specifies the number of routees to spawn by the router
func newRouter(poolSize int, routeesKind Actor, logger log.Logger, opts ...RouterOption) *router {
	router := &router{
		routingStrategy:     FanOutRouting,
		poolSize:            poolSize,
		routees:             make([]*PID, 0, poolSize),
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
	if x.logger.Enabled(log.InfoLevel) {
		x.logger.Infof("starting router=%s", x.name)
	}
	return x.validate()
}

// Receive handles messages sent to the router
func (x *router) Receive(ctx *ReceiveContext) {
	message := ctx.Message()
	switch message.(type) {
	case *PostStart:
		x.postStart(ctx)
	default:
		ctx.Unhandled()
	}
}

// PostStop is executed when the actor is shutting down.
func (x *router) PostStop(*Context) error {
	if x.logger.Enabled(log.InfoLevel) {
		x.logger.Infof("router=%s stopped", x.name)
	}
	return nil
}

// postStart spawns routeesMap
func (x *router) postStart(ctx *ReceiveContext) {
	if x.logger.Enabled(log.InfoLevel) {
		x.logger.Infof("router=%s started", x.name)
		x.logger.Debugf("router=%s spawning routees=%d", x.name, x.poolSize)
	}
	x.spawnRoutees(ctx, x.poolSize)
	x.rebuildHashRing()
	ctx.Become(x.broadcast)
}

// broadcast send message to all the routeesMap
func (x *router) broadcast(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *Broadcast:
		x.handleBroadcast(ctx)
	case *PanicSignal:
		x.handlePanicSignal(ctx)
	case *GetRoutees:
		x.handleGetRoutees(ctx)
	case *AdjustRouterPoolSize:
		x.handleAdjustRouterPoolSize(ctx)
	default:
		ctx.Unhandled()
	}
}

func (x *router) handleAdjustRouterPoolSize(ctx *ReceiveContext) {
	message := ctx.Message().(*AdjustRouterPoolSize)
	delta := int(message.PoolSize())
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

// scaleUp spawns delta new routees. Their names continue from nextRouteeIndex,
// never from the current pool size, so they cannot collide with a live routee
// left behind by an earlier removal.
func (x *router) scaleUp(ctx *ReceiveContext, delta int) {
	currentSize := len(x.routees)
	targetSize := currentSize + delta
	if x.logger.Enabled(log.InfoLevel) {
		x.logger.Debugf("router=%s scaling up pool=%d to %d", x.name, currentSize, targetSize)
	}
	x.poolSize = targetSize
	x.spawnRoutees(ctx, delta)
	x.rebuildHashRing()
}

// scaleDown stops the delta oldest live routees and removes them from the pool.
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
	if x.logger.Enabled(log.InfoLevel) {
		x.logger.Debugf("router=%s scaling down pool=%d to %d", x.name, currentSize, targetSize)
	}
	x.poolSize = targetSize

	for i := 0; i < delta; i++ {
		routee := routees[i]
		if x.logger.Enabled(log.InfoLevel) {
			x.logger.Debugf("stopping routee=%s", routee.ID())
		}
		ctx.Stop(routee)
		x.removeRoutee(routee.ID())
	}
	x.rebuildHashRing()
}

// spawnRoutees spawns count routees as children of the router and adds them to
// the pool. Each takes the next routee name index.
func (x *router) spawnRoutees(ctx *ReceiveContext, count int) {
	for range count {
		routeeName := routeeName(x.nextRouteeIndex, x.name)
		x.nextRouteeIndex++
		actor := reflect.New(x.routeesKind).Interface().(Actor)
		routee := ctx.Spawn(routeeName, actor,
			asSystem(),
			WithRelocationDisabled(),
			WithLongLived(),
			WithSupervisor(
				supervisor.NewSupervisor(supervisor.WithAnyErrorDirective(supervisor.EscalateDirective)),
			))
		x.addRoutee(routee)
	}
}

// addRoutee inserts a routee into the pool, at the end of the routing order.
// A routee already in the pool is left where it is.
func (x *router) addRoutee(routee *PID) {
	if _, ok := x.routeesMap[routee.ID()]; ok {
		return
	}

	x.routeesMap[routee.ID()] = routee
	x.routees = append(x.routees, routee)
}

// removeRoutee takes the routee with the given ID out of the pool, keeping the
// order of the remaining routees.
func (x *router) removeRoutee(id string) {
	if _, ok := x.routeesMap[id]; !ok {
		return
	}

	delete(x.routeesMap, id)
	x.routees = slices.DeleteFunc(x.routees, func(routee *PID) bool {
		return routee.ID() == id
	})
}

// handleGetRoutees replies with the names of the live routees.
func (x *router) handleGetRoutees(ctx *ReceiveContext) {
	routees, _ := x.availableRoutees()
	names := make([]string, 0, len(routees))
	for _, routee := range routees {
		names = append(names, routee.Name())
	}
	ctx.Response(NewRoutees(names))
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
	if x.logger.Enabled(log.DebugLevel) {
		x.logger.Debugf("restarting routee (%s)...", sender.ID())
	}

	var err error

	switch {
	case x.restartRouteeAttempts == 0 || x.restartRouteeWithin <= 0:
		err = sender.Restart(goCtx)
	default:
		retrier := retry.NewRetrier(int(x.restartRouteeAttempts), x.restartRouteeWithin, x.restartRouteeWithin)
		err = retrier.RunContext(goCtx, sender.Restart)
	}

	if err != nil {
		if x.logger.Enabled(log.ErrorLevel) {
			x.logger.Errorf("failed to restart routee (%s): %v", sender.ID(), err)
		}
		x.handleStopRoutee(ctx)
		return
	}

	if x.logger.Enabled(log.DebugLevel) {
		x.logger.Debugf("routee=%s restarted", sender.ID())
	}
	x.addRoutee(sender)
	x.rebuildHashRing()
}

// handleResumeRoutee reinstates the failing routee and puts it back into the
// pool, since a broadcast handled while it was suspended has removed it.
func (x *router) handleResumeRoutee(ctx *ReceiveContext) {
	sender := ctx.Sender()
	if x.logger.Enabled(log.DebugLevel) {
		x.logger.Debugf("resuming routee (%s)...", sender.ID())
	}
	ctx.Reinstate(sender)
	x.addRoutee(sender)
	x.rebuildHashRing()
}

// handleStopRoutee stops the failing routee and removes it from the pool.
func (x *router) handleStopRoutee(ctx *ReceiveContext) {
	sender := ctx.Sender()
	if x.logger.Enabled(log.DebugLevel) {
		x.logger.Debugf("stopping routee=%s", sender.ID())
	}
	ctx.Stop(sender)
	x.removeRoutee(sender.ID())
	x.rebuildHashRing()
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

	message := ctx.Message().(*Broadcast)
	x.dispatchToRoutees(ctx, message.Message(), routees)
}

func (x *router) handleNoRoutees(ctx *ReceiveContext) {
	if x.logger.Enabled(log.WarningLevel) {
		x.logger.Warn("no routees available. stopping.... Bye")
	}
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
// The method keeps the router non-blocking: every Tell only enqueues into the routee's
// mailbox, and the reply-based strategies wait for their routees on a goroutine of
// their own, so Receive returns at once.
func (x *router) dispatchToRoutees(ctx *ReceiveContext, msg any, routees []*PID) {
	switch x.kind {
	case tailChoppingRouter:
		x.tailChopping(ctx, msg, routees)
	case scatterGatherFirstRouter:
		x.scatterGatherFirst(ctx, msg, routees)
	default:
		x.routeByStrategy(ctx, msg, routees)
	}
}

func (x *router) routeByStrategy(ctx *ReceiveContext, msg any, routees []*PID) {
	switch x.routingStrategy {
	case RoundRobinRouting:
		n := atomic.AddUint32(&x.roundRobinNext, 1)
		routee := routees[(int(n)-1)%len(routees)]
		ctx.Tell(routee, msg)
	case RandomRouting:
		routee := routees[rand.IntN(len(routees))] //nolint:gosec
		ctx.Tell(routee, msg)
	case ConsistentHashRouting:
		x.routeByConsistentHash(ctx, msg, routees)
	default:
		// Tell every routee from the router's turn: each Tell only enqueues into the
		// routee's mailbox, and sending in turn order is what keeps consecutive
		// broadcasts in order at every routee.
		sender := ctx.Self()
		sendCtx := ctx.withoutCancel()
		for _, routee := range routees {
			if err := sender.Tell(sendCtx, routee, msg); err != nil {
				if x.logger.Enabled(log.WarningLevel) {
					x.logger.Warn(err)
				}
			}
		}
	}
}

func (x *router) routeByConsistentHash(ctx *ReceiveContext, msg any, routees []*PID) {
	key := x.routingKeyExtractor(msg)
	if key == "" {
		routee := routees[rand.IntN(len(routees))] //nolint:gosec
		ctx.Tell(routee, msg)
		return
	}

	id := x.ring.lookup(key)
	if routee, ok := x.routeesMap[id]; ok && routee.IsRunning() {
		ctx.Tell(routee, msg)
		return
	}

	routee := routees[rand.IntN(len(routees))] //nolint:gosec
	ctx.Tell(routee, msg)
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
// The method never blocks the router actor: the asks and the wait for their outcome run on a
// goroutine owned by a gather, and the outcome is pushed asynchronously back to the sender,
// preserving the router's fire-and-forget contract.
func (x *router) scatterGatherFirst(ctx *ReceiveContext, msg any, routees []*PID) {
	gather := x.newGather(ctx, msg)
	go gather.scatterGatherFirst(routees)
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
// This keeps router behavior asynchronous: the probing and the wait run on a goroutine owned
// by a gather, so the router never blocks, and replies arrive to the sender as ordinary
// messages rather than Ask responses.
func (x *router) tailChopping(ctx *ReceiveContext, msg any, routees []*PID) {
	gather := x.newGather(ctx, msg)
	go gather.tailChopping(routees)
}

// newGather captures, from the router's turn, everything a reply-based request
// needs once the turn has returned: the Broadcast sender, the router's own PID,
// the system's NoSender and a context that outlives the turn. The receive
// context itself is pooled and must not be touched after Receive returns.
func (x *router) newGather(ctx *ReceiveContext, msg any) *gather {
	return &gather{
		router:   x,
		self:     ctx.Self(),
		sender:   ctx.Sender(),
		noSender: ctx.ActorSystem().NoSender(),
		message:  msg,
		ctx:      ctx.withoutCancel(),
	}
}

// scatterGatherFirst asks every routee at once and relays the first successful
// reply; it fails the request when every ask failed or the time budget elapsed.
func (g *gather) scatterGatherFirst(routees []*PID) {
	within := g.router.within
	deadlineCtx, cancel := context.WithTimeout(g.ctx, within)
	defer cancel()

	results := make(chan askResult, len(routees))

	for _, routee := range routees {
		go func(to *PID) {
			resp, err := g.noSender.Ask(deadlineCtx, to, g.message, within)
			results <- askResult{resp: resp, err: err}
		}(routee)
	}

	pending := len(routees)

	for {
		select {
		case <-deadlineCtx.Done():
			// no need to drain: channel is buffered
			g.fail()
			return

		case r := <-results:
			pending--
			if r.err == nil {
				// first success wins
				cancel()
				g.reply(r.resp)
				return
			}

			if g.router.logger.Enabled(log.WarningLevel) {
				g.router.logger.Warnf("scatter-gather-first: attempt failed: %v", r.err)
			}

			if pending == 0 {
				g.fail()
				return
			}
		}
	}
}

// tailChopping asks the routees one at a time in random order, moving to the
// next one every interval, and relays the first successful reply; it fails the
// request when every attempt failed or the time budget elapsed.
func (g *gather) tailChopping(routees []*PID) {
	interval := g.router.interval
	within := g.router.within
	shuffled := reshuffleRoutees(routees)

	deadlineCtx, cancel := context.WithTimeout(g.ctx, within)
	defer cancel()

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

		go func(to *PID, timeout time.Duration) {
			resp, err := g.noSender.Ask(deadlineCtx, to, g.message, timeout)
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
			g.fail()
			return

		case r := <-results:
			pending--
			if r.err == nil {
				// first success wins
				cancel()
				g.reply(r.resp)
				return
			}

			if g.router.logger.Enabled(log.WarningLevel) {
				g.router.logger.Warnf("tail-chopping: attempt failed: %v", r.err)
			}

			if pending == 0 && next >= len(shuffled) {
				g.fail()
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

// reply delivers the winning response to the Broadcast sender as a message from
// the router. A sender that has gone meanwhile is logged, not retried.
func (g *gather) reply(resp any) {
	if err := g.self.Tell(g.ctx, g.sender, resp); err != nil {
		if g.router.logger.Enabled(log.WarningLevel) {
			g.router.logger.Warnf("router=%s failed to deliver reply to %s: %v", g.router.name, g.sender.ID(), err)
		}
	}
}

// fail tells the Broadcast sender that no routee answered in time, with a
// StatusFailure that carries the original message.
func (g *gather) fail() {
	g.reply(NewStatusFailure(gerrors.ErrRequestTimeout.Error(), g.message))
}

// rebuildHashRing populates the consistent hash ring from the current routee
// set. It is a no-op when the router does not use ConsistentHashRouting.
func (x *router) rebuildHashRing() {
	if x.routingStrategy != ConsistentHashRouting {
		return
	}

	if x.ring == nil {
		x.ring = newConsistentHashRing(x.hasher, x.virtualNodes)
	}

	members := make([]string, 0, len(x.routees))
	for _, routee := range x.routees {
		members = append(members, routee.ID())
	}
	x.ring.set(members)
}

// routeeName returns the routee name
func routeeName(index int, routerName string) string {
	return fmt.Sprintf("%s%s%d", routerName, routeeNamePrefix, index)
}

// availableRoutees returns a snapshot of the live routees in routing order and
// whether there is at least one. A routee that is no longer running is removed
// from the pool and is not returned, so no message is routed to it.
func (x *router) availableRoutees() ([]*PID, bool) {
	live := make([]*PID, 0, len(x.routees))
	removed := false

	for _, routee := range x.routees {
		if routee.IsRunning() {
			live = append(live, routee)
			continue
		}

		removed = true
	}

	if removed {
		x.routees = slices.Clone(live)
		x.routeesMap = make(map[string]*PID, len(live))

		for _, routee := range live {
			x.routeesMap[routee.ID()] = routee
		}

		x.rebuildHashRing()
	}

	return live, len(live) > 0
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

	if x.routingStrategy == ConsistentHashRouting && x.routingKeyExtractor == nil {
		return gerrors.ErrConsistentHashRouterMisconfigured
	}

	return nil
}

func reshuffleRoutees(routees []*PID) []*PID {
	n := len(routees)
	shuffled := make([]*PID, n)
	copy(shuffled, routees)

	rand.Shuffle(n, func(i, j int) { //nolint:gosec // routee order needs no cryptographic randomness
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	return shuffled
}

// consistentHashRing implements a consistent hash ring with virtual nodes.
//
// Each member is placed at multiple points (virtual nodes) on a uint64 ring,
// spreading load more evenly than a single point per member. Lookup is O(log N)
// where N is the total number of virtual nodes.
//
// The ring is not safe for concurrent use; the router actor processes messages
// sequentially, so no lock is required.
type consistentHashRing struct {
	hasher       hash.Hasher
	virtualNodes int
	keys         []uint64
	ring         map[uint64]string
}

func newConsistentHashRing(hasher hash.Hasher, virtualNodes int) *consistentHashRing {
	if hasher == nil {
		hasher = hash.DefaultHasher()
	}
	if virtualNodes <= 0 {
		virtualNodes = defaultVirtualNodes
	}
	return &consistentHashRing{
		hasher:       hasher,
		virtualNodes: virtualNodes,
		ring:         make(map[uint64]string),
	}
}

// set rebuilds the ring with the given set of member IDs, replacing all
// previous members. Passing an empty slice clears the ring.
func (r *consistentHashRing) set(members []string) {
	r.ring = make(map[uint64]string, len(members)*r.virtualNodes)
	r.keys = make([]uint64, 0, len(members)*r.virtualNodes)

	for _, member := range members {
		for i := range r.virtualNodes {
			h := r.hashVNode(member, i)
			r.ring[h] = member
			r.keys = append(r.keys, h)
		}
	}

	slices.Sort(r.keys)
}

// lookup returns the member responsible for the given key.
// Returns an empty string when the ring is empty.
func (r *consistentHashRing) lookup(key string) string {
	if len(r.keys) == 0 {
		return ""
	}

	h := r.hasher.HashCode(stringToBytes(key))

	idx := sort.Search(len(r.keys), func(i int) bool {
		return r.keys[i] >= h
	})

	if idx >= len(r.keys) {
		idx = 0
	}

	return r.ring[r.keys[idx]]
}

func (r *consistentHashRing) len() int {
	return len(r.keys)
}

func (r *consistentHashRing) hashVNode(member string, index int) uint64 {
	vkey := fmt.Sprintf("%s#%d", member, index)
	return r.hasher.HashCode(stringToBytes(vkey))
}

// stringToBytes converts a string to a byte slice without allocation.
func stringToBytes(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}
