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
	"fmt"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/address"
)

// QualifiedName returns the name that identifies an actor within its actor
// system: the names of its ancestors from the root down, then its own name,
// joined by "/". It is the name ActorOf, ActorExists, Kill and ReSpawn resolve,
// locally and in a cluster, and the name Path.QualifiedName reports for a
// running actor.
//
// A single name is a top-level actor's qualified name unchanged. Every name
// must be one Spawn would accept: the first invalid name is reported with
// ErrInvalidActorName, and so is a call without names, and the first name
// reserved for system actors with ErrReservedName.
//
//	actor.QualifiedName("orders")                 // "orders"
//	actor.QualifiedName("orders", "cart", "item") // "orders/cart/item"
func QualifiedName(names ...string) (string, error) {
	if len(names) == 0 {
		return "", fmt.Errorf("%w: at least one name is required", gerrors.ErrInvalidActorName)
	}

	for _, name := range names {
		if err := address.ValidateName(name); err != nil {
			return "", gerrors.NewErrInvalidActorName(name, err)
		}

		if isSystemName(name) {
			return "", gerrors.NewErrReservedName(name)
		}
	}

	return address.JoinNames(names...), nil
}
