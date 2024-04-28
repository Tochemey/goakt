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
	"strings"
	"sync"
)

// registry represents reflection registry for dynamic loading and creation of
// actors at run-time
type registry interface {
	// Register an object
	Register(v any)
	// RegisterWithKey registers with an alias/key
	RegisterWithKey(key string, v any)
	// GetType returns the type of object,
	GetType(v any) (reflect.Type, bool)
	// GetTypeOf returns the type of an object name
	GetTypeOf(name string) (reflect.Type, bool)
	// GetTypeByKey returns the type of an object by key
	GetTypeByKey(key string) (reflect.Type, bool)
	// Deregister removes the registered object from the registry
	Deregister(v any)
	// DeregisterWithKey removes the registered object from the registry
	DeregisterWithKey(key string)
	// Exists return true when a given object is in the registry
	Exists(v any) bool
	// List returns the list of registered at any point in time
	List() map[string]reflect.Type
}

// registryImpl implements Registry
type registryImpl struct {
	typesMap map[string]reflect.Type
	mu       sync.Mutex
}

// enforce compilation error
var _ registry = (*registryImpl)(nil)

// newRegistry creates an instance of Registry
func newRegistry() registry {
	l := &registryImpl{
		typesMap: make(map[string]reflect.Type),
		mu:       sync.Mutex{},
	}
	return l
}

// Register an object with its fully qualified path
func (r *registryImpl) Register(v any) {
	var rtype reflect.Type
	switch _type := v.(type) {
	case reflect.Type:
		rtype = _type
	default:
		rtype = reflect.TypeOf(v).Elem()
	}

	if _, exist := r.GetType(v); !exist {
		r.mu.Lock()
		r.typesMap[strings.ToLower(rtype.Name())] = rtype
		r.mu.Unlock()
	}
}

// Deregister removes the registered object from the registry
func (r *registryImpl) Deregister(v any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.typesMap, nameOf(v))
}

// GetType returns the type of object
func (r *registryImpl) GetType(v any) (reflect.Type, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.typesMap[nameOf(v)]
	return t, ok
}

// GetTypeOf implements registry.
func (r *registryImpl) GetTypeOf(name string) (reflect.Type, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.typesMap[strings.ToLower(name)]
	return t, ok
}

// Exists implements Registry.
func (r *registryImpl) Exists(v any) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.typesMap[nameOf(v)]
	return ok
}

func nameOf(v any) string {
	var rtype reflect.Type
	switch _type := v.(type) {
	case reflect.Type:
		rtype = _type
	default:
		rtype = reflect.TypeOf(v).Elem()
	}

	return strings.ToLower(rtype.Name())
}

// List returns the list of registered at any point in time
func (r *registryImpl) List() map[string]reflect.Type {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]reflect.Type, len(r.typesMap))
	for key, val := range r.typesMap {
		out[key] = val
	}
	return out
}

// DeregisterWithKey implements registry.
func (r *registryImpl) DeregisterWithKey(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.typesMap, strings.ToLower(key))
}

// RegisterWithKey implements registry.
func (r *registryImpl) RegisterWithKey(key string, v any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var rtype reflect.Type
	switch _type := v.(type) {
	case reflect.Type:
		rtype = _type
	default:
		rtype = reflect.TypeOf(v).Elem()
	}
	r.typesMap[strings.ToLower(key)] = rtype
}

// GetTypeByKey returns the type of an object by key
func (r *registryImpl) GetTypeByKey(key string) (reflect.Type, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.typesMap[strings.ToLower(key)]
	return t, ok
}
