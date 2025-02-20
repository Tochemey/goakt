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

package actors

import (
	"github.com/tochemey/goakt/v3/address"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/types"
)

// ActorRef defines the information about a given actor.
// The following information is captured by the ActorRef:
//
//   - Name: The given actor name which is unique both locally
//     and in a cluster environment. Actor's names only word characters
//     that is:[a-zA-Z0-9] plus non-leading '-' or '_'.
//
//   - Kind: The actor kind returns the reflected type of the underlying Actor
//     backing the given actor reference.
//
//   - Address: The actor address. One can use the address with Remoting to
//     interact with the actor by sending messages.
type ActorRef struct {
	// name defines the actor Name
	name string
	// kind defines the actor kind
	kind string
	// address defines the actor address
	address *address.Address
}

// Name represents the actor given name
func (x ActorRef) Name() string {
	return x.name
}

// Kind represents the actor kind
func (x ActorRef) Kind() string {
	return x.kind
}

// Address represents the actor address
func (x ActorRef) Address() *address.Address {
	return x.address
}

// Equals is a convenient method to compare two ActorRef
func (x ActorRef) Equals(actor ActorRef) bool {
	return x.address.Equals(actor.address)
}

func fromActorRef(actorRef *internalpb.ActorRef) ActorRef {
	return ActorRef{
		name:    actorRef.GetActorAddress().GetName(),
		kind:    actorRef.GetActorType(),
		address: address.From(actorRef.GetActorAddress()),
	}
}

func fromPID(pid *PID) ActorRef {
	return ActorRef{
		name:    pid.Name(),
		kind:    types.Name(pid.Actor()),
		address: pid.Address(),
	}
}
