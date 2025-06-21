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
func (r *reflection) NewActor(typeName string) (Actor, error) {
	rtype, ok := r.registry.TypeOf(typeName)
	if !ok {
		return nil, ErrTypeNotRegistered
	}

	actorType := reflect.TypeOf((*Actor)(nil)).Elem()
	if !rtype.Implements(actorType) && !reflect.PointerTo(rtype).Implements(actorType) {
		return nil, ErrInstanceNotAnActor
	}

	instance := reflect.New(rtype)
	actor, ok := instance.Interface().(Actor)
	if !ok {
		return nil, ErrInvalidInstance
	}
	return actor, nil
}

// NewDependency creates a new instance of Dependency from its type name and bytes array
func (r *reflection) NewDependency(typeName string, bytea []byte) (extension.Dependency, error) {
	rtype, ok := r.registry.TypeOf(typeName)
	if !ok {
		return nil, ErrDependencyTypeNotRegistered
	}

	depType := reflect.TypeOf((*extension.Dependency)(nil)).Elem()
	if !rtype.Implements(depType) && !reflect.PointerTo(rtype).Implements(depType) {
		return nil, ErrInstanceNotDependency
	}

	instance := reflect.New(rtype)
	dependency, ok := instance.Interface().(extension.Dependency)
	if !ok {
		return nil, ErrInvalidInstance
	}
	if err := dependency.UnmarshalBinary(bytea); err != nil {
		return nil, err
	}
	return dependency, nil
}

// NewDependencies reflects the dependencies defined in the protobuf
func (r *reflection) NewDependencies(dependencies ...*internalpb.Dependency) ([]extension.Dependency, error) {
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
func (r *reflection) NewGrain(kind string) (Grain, error) {
	rtype, ok := r.registry.TypeOf(kind)
	if !ok {
		return nil, ErrGrainNotRegistered
	}

	grainType := reflect.TypeOf((*Grain)(nil)).Elem()
	if !rtype.Implements(grainType) && !reflect.PointerTo(rtype).Implements(grainType) {
		return nil, ErrInstanceNotAnGrain
	}

	instance := reflect.New(rtype)
	grain, ok := instance.Interface().(Grain)
	if !ok {
		return nil, ErrInvalidInstance
	}
	return grain, nil
}
