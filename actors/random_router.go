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

	"github.com/tochemey/goakt/v2/goaktpb"
)

// randomRouter defines a randomRouter router.
// The router selects a routee at random when a message is sent through the router.
type randomRouter struct {
	// list of routees
	routeesMap map[string]PID
	routees    []Actor
}

// enforce compilation error
var _ Actor = (*randomRouter)(nil)

// newRandomRouter creates an instance of randomRouter router
func newRandomRouter(routees ...Actor) *randomRouter {
	// create the router instance
	router := &randomRouter{
		routeesMap: make(map[string]PID, len(routees)),
		routees:    routees,
	}
	return router
}

// PreStart pre-starts the actor.
func (x *randomRouter) PreStart(context.Context) error {
	return nil
}

// Receive handles messages sent to the randomRouter router
func (x *randomRouter) Receive(ctx ReceiveContext) {
	message := ctx.Message()
	switch message.(type) {
	case *goaktpb.PostStart:
		x.postStart(ctx)
	default:
		ctx.Unhandled()
	}
}

// PostStop is executed when the actor is shutting down.
func (x *randomRouter) PostStop(context.Context) error {
	return nil
}

// postStart spawns routeeRefs
func (x *randomRouter) postStart(ctx ReceiveContext) {
	for index, routee := range x.routees {
		name := fmt.Sprintf("%s-%s-%d", goakRouteeNamePrefix, ctx.Self().Name(), index)
		routee := ctx.Spawn(name, routee)
		x.routeesMap[routee.ID()] = routee
	}
	ctx.Become(x.broadcast)
}

// broadcast send message to all the routeeRefs
func (x *randomRouter) broadcast(ctx ReceiveContext) {
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

	routee := routees[rand.IntN(len(routees))]
	ctx.Tell(routee, msg)
}
