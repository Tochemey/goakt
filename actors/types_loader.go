package actors

import (
	"fmt"
	"reflect"
	"sync"
)

// TypesLoader represents reflection typesLoader for dynamic loading and creation of
// actors at run-time
type TypesLoader interface {
	// Register an object with its fully qualified name
	Register(name string, v any)
	// Type returns the type of object,
	Type(v any) (reflect.Type, bool)
	// TypeByName returns the type of object given its name
	TypeByName(name string) (reflect.Type, bool)
}

// typesLoader implements TypesLoader
type typesLoader struct {
	names map[string]reflect.Type
	types map[string]reflect.Type
	mu    sync.Mutex
}

// enforce compilation error
var _ TypesLoader = &typesLoader{}

// NewTypesLoader creates an instance of TypesLoader
func NewTypesLoader() TypesLoader {
	l := &typesLoader{
		names: make(map[string]reflect.Type),
		types: make(map[string]reflect.Type),
		mu:    sync.Mutex{},
	}
	return l
}

// Register an object with its fully qualified path
func (l *typesLoader) Register(name string, v any) {
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
		// acquire the lock
		l.mu.Lock()
		l.types[path] = vType
		l.mu.Unlock()
	}
	if _, exist := l.TypeByName(name); !exist {
		l.mu.Lock()
		l.names[name] = vType
		l.mu.Unlock()
	}
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
	// if ok return it
	return t, ok
}
