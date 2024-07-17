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

package router

import (
	"context"
	"fmt"
	"math/rand/v2"

	"github.com/tochemey/goakt/v2/actors"
	"github.com/tochemey/goakt/v2/goaktpb"
)

// Random defines a Random router.
// The router selects a routee at random when a message is sent through the router.
type Random struct {
	// list of routees
	routeesMap map[string]actors.PID
	routees    []actors.Actor
}

// enforce compilation error
var _ actors.Actor = (*Random)(nil)

// NewRandom creates an instance of Random router
func NewRandom(routees ...actors.Actor) *Random {
	// create the router instance
	router := &Random{
		routeesMap: make(map[string]actors.PID, len(routees)),
		routees:    routees,
	}
	return router
}

// PreStart pre-starts the actor.
func (x *Random) PreStart(context.Context) error {
	return nil
}

// Receive handles messages sent to the Random router
func (x *Random) Receive(ctx actors.ReceiveContext) {
	message := ctx.Message()
	switch message.(type) {
	case *goaktpb.PostStart:
		x.postStart(ctx)
	default:
		ctx.Unhandled()
	}
}

// PostStop is executed when the actor is shutting down.
func (x *Random) PostStop(context.Context) error {
	return nil
}

// postStart spawns routeeRefs
func (x *Random) postStart(ctx actors.ReceiveContext) {
	for index, routee := range x.routees {
		name := fmt.Sprintf("routee-%s-%d", ctx.Self().Name(), index)
		routee := ctx.Spawn(name, routee)
		x.routeesMap[routee.ID()] = routee
	}
	ctx.Become(x.broadcast)
}

// broadcast send message to all the routeeRefs
func (x *Random) broadcast(ctx actors.ReceiveContext) {
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

	routees := make([]actors.PID, 0, len(x.routeesMap))
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
