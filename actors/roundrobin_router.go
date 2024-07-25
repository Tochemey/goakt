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
	"sync/atomic"

	"github.com/tochemey/goakt/v2/goaktpb"
)

// roundRobinRouter defines a Random router.
// It rotates over the set of routeesMap making sure that if there are n routeesMap,
// then for n messages sent through the router, each actor is forwarded one message.
type roundRobinRouter struct {
	// list of routees
	routeesMap map[string]PID
	routees    []Actor
	next       uint32
}

// enforce compilation error
var _ Actor = (*roundRobinRouter)(nil)

// newRoundRobinRouter creates an instance of roundRobinRouter router
func newRoundRobinRouter(routees ...Actor) *roundRobinRouter {
	// create the router instance
	router := &roundRobinRouter{
		routeesMap: make(map[string]PID, len(routees)),
		routees:    routees,
	}
	return router
}

// PreStart pre-starts the actor.
func (x *roundRobinRouter) PreStart(context.Context) error {
	return nil
}

// Receive handles messages sent to the Random router
func (x *roundRobinRouter) Receive(ctx ReceiveContext) {
	message := ctx.Message()
	switch message.(type) {
	case *goaktpb.PostStart:
		x.postStart(ctx)
	default:
		ctx.Unhandled()
	}
}

// PostStop is executed when the actor is shutting down.
func (x *roundRobinRouter) PostStop(context.Context) error {
	return nil
}

// postStart spawns routees
func (x *roundRobinRouter) postStart(ctx ReceiveContext) {
	for index, routee := range x.routees {
		name := fmt.Sprintf("%s-%s-%d", goakRouteeNamePrefix, ctx.Self().Name(), index)
		routee := ctx.Spawn(name, routee)
		x.routeesMap[routee.ID()] = routee
	}
	ctx.Become(x.broadcast)
}

// broadcast send message to all the routees
func (x *roundRobinRouter) broadcast(ctx ReceiveContext) {
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

	routees := make([]PID, 0, len(x.routeesMap))
	for _, routee := range x.routeesMap {
		if routee.IsRunning() {
			routees = append(routees, routee)
		}
	}

	if len(routees) == 0 {
		// push message to deadletters
		ctx.Unhandled()
		return
	}

	msg, err := message.GetMessage().UnmarshalNew()
	if err != nil {
		ctx.Err(err)
	}
	n := atomic.AddUint32(&x.next, 1)
	routee := routees[(int(n)-1)%len(routees)]
	ctx.Tell(routee, msg)
}
