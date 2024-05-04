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

package types

import (
	"reflect"
	"strings"
	"sync"
)

// Map defines mapping between a type name and its runtime type
type Map map[string]reflect.Type

// Registry defines the types registry interface
type Registry interface {
	// Register an object
	Register(v any)
	// Deregister removes the registered object from the registry
	Deregister(v any)
	// Exists return true when a given object is in the registry
	Exists(v any) bool
	// TypesMap returns the list of registered at any point in time
	TypesMap() Map
	// Type returns the type of object
	Type(v any) (reflect.Type, bool)
	// TypeOf returns the type of an object name
	TypeOf(name string) (reflect.Type, bool)
}

type registry struct {
	typesMap Map
	mu       *sync.RWMutex
}

var _ Registry = (*registry)(nil)

// NewRegistry creates a new types registry
func NewRegistry() Registry {
	return &registry{
		typesMap: Map{},
		mu:       new(sync.RWMutex),
	}
}

// Deregister removes the registered object from the registry
func (r *registry) Deregister(v any) {
	r.mu.Lock()
	name, _ := nameAndTypeOf(v)
	delete(r.typesMap, name)
	r.mu.Unlock()
}

// Exists return true when a given object is in the registry
func (r *registry) Exists(v any) bool {
	r.mu.RLock()
	name, _ := nameAndTypeOf(v)
	_, ok := r.typesMap[name]
	r.mu.RUnlock()
	return ok
}

// TypesMap returns the list of registered at any point in time
func (r *registry) TypesMap() Map {
	r.mu.Lock()
	out := r.typesMap
	r.mu.Unlock()
	return out
}

// Register an object
func (r *registry) Register(v any) {
	r.mu.Lock()
	name, rtype := nameAndTypeOf(v)
	r.typesMap[name] = rtype
	r.mu.Unlock()
}

// Type returns the type of object
func (r *registry) Type(v any) (reflect.Type, bool) {
	r.mu.RLock()
	name, _ := nameAndTypeOf(v)
	out, ok := r.typesMap[name]
	r.mu.RUnlock()
	return out, ok
}

// TypeOf returns the type of an object name
func (r *registry) TypeOf(name string) (reflect.Type, bool) {
	r.mu.RLock()
	out, ok := r.typesMap[strings.ToLower(name)]
	r.mu.RUnlock()
	return out, ok
}

func nameAndTypeOf(v any) (string, reflect.Type) {
	rtype := RuntimeTypeOf(v)
	return strings.ToLower(rtype.Name()), rtype
}

// RuntimeTypeOf returns the runtime type of an object
func RuntimeTypeOf(v any) reflect.Type {
	var rtype reflect.Type
	switch _type := v.(type) {
	case reflect.Type:
		rtype = _type
	default:
		rtype = reflect.TypeOf(v).Elem()
	}
	return rtype
}
