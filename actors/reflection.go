package actors

import (
	"reflect"
)

// Reflection helps create an instance dynamically
type Reflection interface {
	// ActorOf creates a new instance of Actor from its concrete type
	ActorOf(rtype reflect.Type) (actor Actor, err error)
	// ActorFrom creates a new instance of Actor from its FQN
	ActorFrom(name string) (actor Actor, err error)
}

// reflection implements Reflection
type reflection struct {
	typesLoader TypesLoader
}

// enforce compilation error
var _ Reflection = &reflection{}

// New creates an instance of Reflection
func New(loader TypesLoader) Reflection {
	return &reflection{typesLoader: loader}
}

// ActorOf creates a new instance of an Actor
func (r *reflection) ActorOf(rtype reflect.Type) (actor Actor, err error) {
	// make sure the type implements Actor interface
	isActor := rtype.Implements(reflect.TypeOf((*Actor)(nil)).Elem())
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
	rtype, ok := r.typesLoader.TypeByName(name)
	if !ok {
		return nil, ErrTypeNotFound(name)
	}
	return r.ActorOf(rtype)
}
