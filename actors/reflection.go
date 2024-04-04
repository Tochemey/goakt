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

import "reflect"

// reflection helps create an instance dynamically
type reflection interface {
	// ActorOf creates a new instance of Actor from its concrete type
	ActorOf(rtype reflect.Type) (actor Actor, err error)
	// ActorFrom creates a new instance of Actor from its FQN
	ActorFrom(key string) (actor Actor, err error)
}

// reflectionImpl implements Reflection
type reflectionImpl struct {
	registry registry
}

// enforce compilation error
var _ reflection = &reflectionImpl{}

// newReflection creates an instance of Reflection
func newReflection(registry registry) reflection {
	return &reflectionImpl{registry: registry}
}

// ActorOf creates a new instance of an Actor
func (r *reflectionImpl) ActorOf(rtype reflect.Type) (actor Actor, err error) {
	iface := reflect.TypeOf((*Actor)(nil)).Elem()
	isActor := rtype.Implements(iface) || reflect.PointerTo(rtype).Implements(iface)

	if !isActor {
		return nil, ErrInstanceNotAnActor
	}
	typVal := reflect.New(rtype)

	if !typVal.IsValid() {
		return nil, ErrInvalidInstance
	}
	return typVal.Interface().(Actor), nil
}

// ActorFrom creates a new instance of Actor from its FQN
func (r *reflectionImpl) ActorFrom(key string) (actor Actor, err error) {
	rtype, ok := r.registry.GetTypeOf(key)
	if !ok {
		return nil, ErrTypeNotRegistered
	}
	return r.ActorOf(rtype)
}
