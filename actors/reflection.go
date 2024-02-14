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
 *
 */

package actors

import "reflect"

// Reflection helps create an instance dynamically
type Reflection interface {
	// ActorOf creates a new instance of Actor from its concrete type
	ActorOf(rtype reflect.Type) (actor Actor, err error)
	// ActorFrom creates a new instance of Actor from its FQN
	ActorFrom(name string) (actor Actor, err error)
}

// reflection implements Reflection
type reflection struct {
	registry Registry
}

// enforce compilation error
var _ Reflection = &reflection{}

// NewReflection creates an instance of Reflection
func NewReflection(registry Registry) Reflection {
	return &reflection{registry: registry}
}

// ActorOf creates a new instance of an Actor
func (r *reflection) ActorOf(rtype reflect.Type) (actor Actor, err error) {
	// grab the Actor interface type
	iface := reflect.TypeOf((*Actor)(nil)).Elem()
	// make sure the type implements Actor interface
	isActor := rtype.Implements(iface) || reflect.PtrTo(rtype).Implements(iface)
	// reject the creation of the instance
	if !isActor {
		return nil, ErrInstanceNotAnActor
	}
	// get the type value of the object type
	typVal := reflect.New(rtype)
	// validate the typVal
	if !typVal.IsValid() {
		return nil, ErrInvalidInstance
	}
	return typVal.Interface().(Actor), nil
}

// ActorFrom creates a new instance of Actor from its FQN
func (r *reflection) ActorFrom(name string) (actor Actor, err error) {
	// grab the type from the typesLoader
	rtype, ok := r.registry.GetNamedType(name)
	if !ok {
		return nil, ErrTypeNotFound(name)
	}
	return r.ActorOf(rtype)
}
