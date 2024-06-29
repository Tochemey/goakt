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
	"reflect"

	"github.com/tochemey/goakt/v2/internal/types"
)

// reflection helps create an instance dynamically
type reflection struct {
	registry types.Registry
}

// newReflection creates an instance of Reflection
func newReflection(registry types.Registry) *reflection {
	return &reflection{registry: registry}
}

// ActorFrom creates a new instance of Actor from its FQN
func (r *reflection) ActorFrom(key string) (actor Actor, err error) {
	rtype, ok := r.registry.TypeOf(key)
	if !ok {
		return nil, ErrTypeNotRegistered
	}

	iActor := reflect.TypeOf((*Actor)(nil)).Elem()
	isActor := rtype.Implements(iActor) || reflect.PointerTo(rtype).Implements(iActor)

	if !isActor {
		return nil, ErrInstanceNotAnActor
	}

	instance := reflect.New(rtype)
	if !instance.IsValid() {
		return nil, ErrInvalidInstance
	}
	return instance.Interface().(Actor), nil
}
