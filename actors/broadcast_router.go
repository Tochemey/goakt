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

	"github.com/tochemey/goakt/v2/goaktpb"
)

// broadcastRouter defines a broadcast router. This is a pool router
// When the router receives a message to broadcast, every routee is checked whether alive or not.
// When a routee is not alive the router removes it from its set of routeesMap.
// When the last routee stops the router itself stops.
type broadcastRouter struct {
	// list of routees
	routeesMap map[string]PID
	routees    []Actor
}

// enforce compilation error
var _ Actor = (*broadcastRouter)(nil)

// newBroadcastRouter creates an instance of broadcastRouter router
// routeeRefs can be of different types as long as they can handle the router broadcast message
func newBroadcastRouter(routees ...Actor) *broadcastRouter {
	// create the router instance
	router := &broadcastRouter{
		routeesMap: make(map[string]PID, len(routees)),
		routees:    routees,
	}
	return router
}

// PreStart pre-starts the actor.
func (x *broadcastRouter) PreStart(context.Context) error {
	return nil
}

// Receive handles messages sent to the broadcastRouter router
func (x *broadcastRouter) Receive(ctx ReceiveContext) {
	message := ctx.Message()
	switch message.(type) {
	case *goaktpb.PostStart:
		x.postStart(ctx)
	default:
		ctx.Unhandled()
	}
}

// PostStop is executed when the actor is shutting down.
func (x *broadcastRouter) PostStop(context.Context) error {
	return nil
}

// postStart spawns routeesMap
func (x *broadcastRouter) postStart(ctx ReceiveContext) {
	for index, routee := range x.routees {
		name := fmt.Sprintf("%s-%s-%d", goakRouteeNamePrefix, ctx.Self().Name(), index)
		routee := ctx.Spawn(name, routee)
		x.routeesMap[routee.ID()] = routee
	}

	ctx.Become(x.broadcast)
}

// broadcast send message to all the routeesMap
func (x *broadcastRouter) broadcast(ctx ReceiveContext) {
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

	if !x.canProceed() {
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

	for _, routee := range x.routeesMap {
		routee := routee
		go func(pid PID) {
			ctx.Tell(pid, msg)
		}(routee)
	}
}

// canProceed check whether there are available routeesMap to proceed
func (x *broadcastRouter) canProceed() bool {
	for _, routee := range x.routeesMap {
		if !routee.IsRunning() {
			delete(x.routeesMap, routee.ID())
		}
	}
	return len(x.routeesMap) > 0
}
