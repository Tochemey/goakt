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

package actor

import (
	"reflect"

	"github.com/tochemey/goakt/v3/extension"
	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/types"
)

// reflection helps create an instance dynamically
type reflection struct {
	registry types.Registry
}

// newReflection creates an instance of Reflection
func newReflection(registry types.Registry) *reflection {
	return &reflection{registry: registry}
}

// NewActor creates a new instance of Actor from its FQN
func (r *reflection) NewActor(typeName string) (actor Actor, err error) {
	rtype, ok := r.registry.TypeOf(typeName)
	if !ok {
		return nil, ErrTypeNotRegistered
	}

	elem := reflect.TypeOf((*Actor)(nil)).Elem()
	if ok := rtype.Implements(elem) || reflect.PointerTo(rtype).Implements(elem); !ok {
		return nil, ErrInstanceNotAnActor
	}

	instance := reflect.New(rtype)
	if !instance.IsValid() {
		return nil, ErrInvalidInstance
	}
	return instance.Interface().(Actor), nil
}

// NewDependency creates a new instance of Dependency from its type name and bytes array
func (r *reflection) NewDependency(typeName string, bytea []byte) (extension.Dependency, error) {
	dept, ok := r.registry.TypeOf(typeName)
	if !ok {
		return nil, ErrDependencyTypeNotRegistered
	}

	elem := reflect.TypeOf((*extension.Dependency)(nil)).Elem()
	if ok := dept.Implements(elem) || reflect.PointerTo(dept).Implements(elem); !ok {
		return nil, ErrInstanceNotDependency
	}

	instance := reflect.New(dept)
	if !instance.IsValid() {
		return nil, ErrInvalidInstance
	}

	if dependency, ok := instance.Interface().(extension.Dependency); ok {
		if err := dependency.UnmarshalBinary(bytea); err != nil {
			return nil, err
		}
		return dependency, nil
	}
	return nil, ErrInvalidInstance
}

// DependenciesFromProtobuf reflects the dependencies defined in the protobuf
func (r *reflection) DependenciesFromProtobuf(dependencies ...*internalpb.Dependency) ([]extension.Dependency, error) {
	deps := make([]extension.Dependency, 0, len(dependencies))
	for _, dep := range dependencies {
		dependency, err := r.NewDependency(dep.GetTypeName(), dep.GetBytea())
		if err != nil {
			return nil, err
		}
		deps = append(deps, dependency)
	}
	return deps, nil
}

// NewGrain creates a new instance of Grain from its FQN
func (r *reflection) NewGrain(kind string) (grain Grain, err error) {
	rtype, ok := r.registry.TypeOf(kind)
	if !ok {
		return nil, ErrTypeNotRegistered
	}

	elem := reflect.TypeOf((*Grain)(nil)).Elem()
	if ok := rtype.Implements(elem) || reflect.PointerTo(rtype).Implements(elem); !ok {
		return nil, ErrInstanceNotAnActor
	}

	instance := reflect.New(rtype)
	if !instance.IsValid() {
		return nil, ErrInvalidInstance
	}
	return instance.Interface().(Grain), nil
}
