/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
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

// Registry defines the types registry interface
type Registry interface {
	// Register an object
	Register(v any)
	// Deregister removes the registered object from the registry
	Deregister(v any)
	// Exists return true when a given object is in the registry
	Exists(v any) bool
	// TypesMap returns the list of registered at any point in time
	TypesMap() map[string]reflect.Type
	// Type returns the type of object
	Type(v any) (reflect.Type, bool)
	// TypeOf returns the type of object name
	TypeOf(name string) (reflect.Type, bool)
}

type registry struct {
	mu       *sync.RWMutex
	typesMap map[string]reflect.Type
}

var _ Registry = (*registry)(nil)

// NewRegistry creates a new types registry
func NewRegistry() Registry {
	return &registry{
		mu:       &sync.RWMutex{},
		typesMap: make(map[string]reflect.Type),
	}
}

// Deregister removes the registered object from the registry
func (r *registry) Deregister(v any) {
	r.mu.Lock()
	delete(r.typesMap, TypeName(v))
	r.mu.Unlock()
}

// Exists return true when a given object is in the registry
func (r *registry) Exists(v any) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.typesMap[TypeName(v)]
	return ok
}

// TypesMap returns the list of registered at any point in time
func (r *registry) TypesMap() map[string]reflect.Type {
	r.mu.Lock()
	out := r.typesMap
	r.mu.Unlock()
	return out
}

// Register an object
func (r *registry) Register(v any) {
	rtype := reflectType(v)
	name := TypeName(v)
	r.mu.Lock()
	r.typesMap[name] = rtype
	r.mu.Unlock()
}

// Type returns the type of object
func (r *registry) Type(v any) (reflect.Type, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out, ok := r.typesMap[TypeName(v)]
	return out, ok
}

// TypeOf returns the type of object name
func (r *registry) TypeOf(name string) (reflect.Type, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out, ok := r.typesMap[lowTrim(name)]
	return out, ok
}

// reflectType returns the runtime type of object
func reflectType(v any) reflect.Type {
	var rtype reflect.Type
	switch _type := v.(type) {
	case reflect.Type:
		rtype = _type
	default:
		rtype = reflect.TypeOf(v).Elem()
	}
	return rtype
}

// TypeName returns the name of a given object
func TypeName(v any) string {
	return lowTrim(reflectType(v).String())
}

// lowTrim trim any space and lower the string value
func lowTrim(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}
