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

import (
	"fmt"
	"reflect"
	"sync"
)

// Registry represents reflection registry for dynamic loading and creation of
// actors at run-time
type Registry interface {
	// Register an object with its fully qualified name
	Register(name string, v any)
	// GetType returns the type of object,
	GetType(v any) (reflect.Type, bool)
	// GetNamedType returns the type of object given its name
	GetNamedType(name string) (reflect.Type, bool)
}

// registry implements Registry
type registry struct {
	names map[string]reflect.Type
	types map[string]reflect.Type
	mu    sync.Mutex
}

// enforce compilation error
var _ Registry = &registry{}

// NewRegistry creates an instance of Registry
func NewRegistry() Registry {
	l := &registry{
		names: make(map[string]reflect.Type),
		types: make(map[string]reflect.Type),
		mu:    sync.Mutex{},
	}
	return l
}

// Register an object with its fully qualified path
func (r *registry) Register(name string, v any) {
	// define a variable to hold the object type
	var vType reflect.Type
	// pattern match on the object type
	switch _type := v.(type) {
	case reflect.Type:
		vType = _type
	default:
		vType = reflect.TypeOf(v).Elem()
	}

	// construct the type package path
	path := fmt.Sprintf("%s.%s", vType.PkgPath(), vType.Name())
	// set name to path if name is not set
	if len(name) == 0 {
		name = path
	}
	// only register the type when it is not set registered
	if _, exist := r.GetType(v); !exist {
		// acquire the lock
		r.mu.Lock()
		r.types[path] = vType
		r.mu.Unlock()
	}
	if _, exist := r.GetNamedType(name); !exist {
		r.mu.Lock()
		r.names[name] = vType
		r.mu.Unlock()
	}
}

// GetType returns the type of object
func (r *registry) GetType(v any) (reflect.Type, bool) {
	// acquire the lock
	r.mu.Lock()
	// release the lock
	defer r.mu.Unlock()

	// grab the object type
	elem := reflect.TypeOf(v).Elem()
	// construct the qualified name
	path := fmt.Sprintf("%s.%s", elem.PkgPath(), elem.Name())
	// lookup the type in the typesLoader registry
	t, ok := r.types[path]
	// if ok return it
	return t, ok
}

// GetNamedType returns the type of object given the name
func (r *registry) GetNamedType(name string) (reflect.Type, bool) {
	// acquire the lock
	r.mu.Lock()
	// release the lock
	defer r.mu.Unlock()

	// grab the type from the existing names
	t, ok := r.names[name]
	// if ok return it
	return t, ok
}
