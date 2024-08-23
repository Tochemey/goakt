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

package actors

import (
	"context"
	"fmt"
	"math/rand/v2"
	"reflect"
	"sync/atomic"

	"github.com/tochemey/goakt/v2/goaktpb"
	"github.com/tochemey/goakt/v2/log"
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

// WithRoutingStrategy sets the routing strategy
func WithRoutingStrategy(strategy RoutingStrategy) RouterOption {
	return RouterOptionFunc(func(r *router) {
		r.strategy = strategy
	})
}

// RoutingStrategy defines the routing strategy to use when
// defining routers
type RoutingStrategy int

const (
	RoundRobinRouting RoutingStrategy = iota
	RandomRouting
	FanOutRouting
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
	}

	// apply the various options
	for _, opt := range opts {
		opt.Apply(router)
	}
	return router
}

// PreStart pre-starts the actor.
func (x *router) PreStart(context.Context) error {
	x.logger.Info("starting the router...")
	return nil
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
func (x *router) PostStop(context.Context) error {
	x.logger.Info("router stopped")
	return nil
}

// postStart spawns routeesMap
func (x *router) postStart(ctx *ReceiveContext) {
	x.logger.Info("router successfully started")
	for i := 0; i < x.poolSize; i++ {
		routeeName := routeeName(ctx.Self().Name(), i)
		actor := reflect.New(x.routeesKind).Interface().(Actor)
		routee := ctx.Spawn(routeeName, actor)
		x.routeesMap[routee.ID()] = routee
	}
	ctx.Become(x.broadcast)
}

// broadcast send message to all the routeesMap
func (x *router) broadcast(ctx *ReceiveContext) {
	var message *goaktpb.Broadcast
	switch msg := ctx.Message().(type) {
	case *goaktpb.Broadcast:
		message = msg
	case *goaktpb.Terminated:
		delete(x.routeesMap, msg.GetActorId())
		return
	default:
		ctx.Unhandled()
		return
	}

	routees, proceed := x.availableRoutees()
	if !proceed {
		x.logger.Warn("no routees available. stopping.... Bye")
		// push message to deadletters
		ctx.Unhandled()
		// shutdown
		ctx.Shutdown()
		return
	}

	msg, err := message.GetMessage().UnmarshalNew()
	if err != nil {
		ctx.Err(err)
		return
	}

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
			routee := routee
			go func(pid *PID) {
				ctx.Tell(pid, msg)
			}(routee)
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
