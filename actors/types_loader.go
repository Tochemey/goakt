package actors

import (
	"fmt"
	"reflect"
	"sync"
)

// TypesLoader represents reflection typesLoader for dynamic loading and creation
type TypesLoader interface {
	// Register an object with its fully qualified name
	Register(name string, v any)
	// Type returns the type of object,
	Type(v any) (reflect.Type, bool)
	// TypeByName returns the type of object given its name
	TypeByName(name string) (reflect.Type, bool)
	// Parent represents the parent typesLoader
	Parent() TypesLoader
}

// typesLoader implements TypesLoader
type typesLoader struct {
	parent TypesLoader
	names  map[string]reflect.Type
	types  map[string]reflect.Type
	mu     sync.Mutex
}

// enforce compilation error
var _ TypesLoader = &typesLoader{}

// NewTypesLoader creates an instance of TypesLoader
func NewTypesLoader(parent TypesLoader) TypesLoader {
	l := &typesLoader{
		parent: nil,
		names:  make(map[string]reflect.Type),
		types:  make(map[string]reflect.Type),
		mu:     sync.Mutex{},
	}
	l.SetParent(parent)
	return l
}

// Register an object with its fully qualified path
func (l *typesLoader) Register(name string, v any) {
	// acquire the lock
	l.mu.Lock()
	// release the lock
	defer l.mu.Unlock()

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
	if _, exist := l.Type(v); !exist {
		l.types[path] = vType
	}
	if _, exist := l.TypeByName(name); !exist {
		l.names[name] = vType
	}
	return
}

// Type returns the type of object
func (l *typesLoader) Type(v any) (reflect.Type, bool) {
	// acquire the lock
	l.mu.Lock()
	// release the lock
	defer l.mu.Unlock()

	// grab the object type
	vType := reflect.TypeOf(v).Elem()
	// construct the qualified name
	path := fmt.Sprintf("%s.%s", vType.PkgPath(), vType.Name())
	// lookup the type in the typesLoader registry
	t, ok := l.types[path]
	// if not found check whether the parent has it
	if !ok {
		// if the parent is not nil lookup for the type
		if l.parent != nil {
			return l.parent.Type(v)
		}
	}
	// if ok return it
	return t, ok
}

// TypeByName returns the type of object given the name
func (l *typesLoader) TypeByName(name string) (reflect.Type, bool) {
	// acquire the lock
	l.mu.Lock()
	// release the lock
	defer l.mu.Unlock()

	// grab the type from the existing names
	t, ok := l.names[name]
	// if not found check with the parent typesLoader
	if !ok {
		// if the parent is not nil lookup for the type
		if l.parent != nil {
			return l.parent.TypeByName(name)
		}
	}
	// if ok return it
	return t, ok
}

// Parent returns the typesLoader parent
func (l *typesLoader) Parent() TypesLoader {
	return l.parent
}

// SetParent sets the typesLoader
func (l *typesLoader) SetParent(parent TypesLoader) {
	// check whether the parent is nil
	if parent == nil {
		return
	}

	// if the parent is the same as the given typesLoader return
	if parent == l {
		return
	}

	// only set when the parent is not nil
	if l.parent == nil {
		l.parent = parent
		return
	}

	// return when there is already a parent
	return
}
