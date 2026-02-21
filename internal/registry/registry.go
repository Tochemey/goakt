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

package registry

import (
	"reflect"
	"strings"

	"github.com/tochemey/goakt/v3/internal/xsync"
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
	m *xsync.Map[string, reflect.Type]
}

var _ Registry = (*registry)(nil)

// NewRegistry creates a new types registry
func NewRegistry() Registry {
	return &registry{
		m: xsync.NewMap[string, reflect.Type](),
	}
}

// Deregister removes the registered object from the registry
func (x *registry) Deregister(v any) {
	x.m.Delete(Name(v))
}

// Exists return true when a given object is in the registry
func (x *registry) Exists(v any) bool {
	_, ok := x.m.Get(Name(v))
	return ok
}

// TypesMap returns the list of registered at any point in time
func (x *registry) TypesMap() map[string]reflect.Type {
	out := make(map[string]reflect.Type)
	x.m.Range(func(s string, r reflect.Type) {
		out[s] = r
	})
	return out
}

// Register an object
func (x *registry) Register(v any) {
	rtype := reflectType(v)
	name := Name(v)
	x.m.Set(name, rtype)
}

// Type returns the type of object
func (x *registry) Type(v any) (reflect.Type, bool) {
	return x.m.Get(Name(v))
}

// TypeOf returns the type of object name
func (x *registry) TypeOf(name string) (reflect.Type, bool) {
	return x.m.Get(lowTrim(name))
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

// Name returns the type name of a given object
func Name(v any) string {
	return lowTrim(reflectType(v).String())
}

// lowTrim trim any space and lower the string value
func lowTrim(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}
