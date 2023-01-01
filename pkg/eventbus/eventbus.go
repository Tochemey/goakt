package eventbus

import (
	"fmt"
	"reflect"
	"sync"
)

// Subscriber defines subscription-related bus behavior
type Subscriber interface {
	// Subscribe subscribes to a topic.
	// Returns error if `fn` is not a function.
	Subscribe(topic string, fn any) error
	// SubscribeAsync subscribes to a topic with an asynchronous callback
	// Transactional determines whether subsequent callbacks for a topic are
	// run serially (true) or concurrently (false)
	// Returns error if `fn` is not a function.
	SubscribeAsync(topic string, fn any, transactional bool) error
	// SubscribeOnce subscribes to a topic once. Handler will be removed after executing.
	// Returns error if `fn` is not a function.
	SubscribeOnce(topic string, fn any) error
	// SubscribeOnceAsync subscribes to a topic once with an asynchronous callback
	// Handler will be removed after executing.
	// Returns error if `fn` is not a function.
	SubscribeOnceAsync(topic string, fn any) error
	// Unsubscribe removes callback defined for a topic.
	// Returns error if there are no callbacks subscribed to the topic.
	Unsubscribe(topic string, handler any) error
}

// Publisher defines publishing-related bus behavior
type Publisher interface {
	// Publish executes callback defined for a topic. Any additional argument will be transferred to the callback.
	Publish(topic string, args ...any)
}

// Controller defines bus control behavior (checking handler's presence, synchronization)
type Controller interface {
	// HasCallback returns true if exists any callback subscribed to the topic.
	HasCallback(topic string) bool
	// WaitAsync waits for all async callbacks to complete
	WaitAsync()
}

// EventBus (subscribe, publish, control) bus behavior
type EventBus interface {
	Controller
	Subscriber
	Publisher
}

// eventBus - box for handlers and callbacks.
type eventBus struct {
	handlers map[string][]*eventHandler
	lock     sync.Mutex // a lock for the map
	wg       sync.WaitGroup
}

type eventHandler struct {
	callBack      reflect.Value
	once          *sync.Once
	async         bool
	transactional bool
	sync.Mutex    // lock for an event handler - useful for running async callbacks serially
}

// New returns new eventBus with empty handlers.
func New() EventBus {
	b := &eventBus{
		make(map[string][]*eventHandler),
		sync.Mutex{},
		sync.WaitGroup{},
	}
	return EventBus(b)
}

// Subscribe subscribes to a topic.
// Returns error if `fn` is not a function.
func (bus *eventBus) Subscribe(topic string, fn any) error {
	return bus.doSubscribe(topic, fn, &eventHandler{
		reflect.ValueOf(fn), nil, false, false, sync.Mutex{},
	})
}

// SubscribeAsync subscribes to a topic with an asynchronous callback
// Transactional determines whether subsequent callbacks for a topic are
// run serially (true) or concurrently (false)
// Returns error if `fn` is not a function.
func (bus *eventBus) SubscribeAsync(topic string, fn any, transactional bool) error {
	return bus.doSubscribe(topic, fn, &eventHandler{
		reflect.ValueOf(fn), nil, true, transactional, sync.Mutex{},
	})
}

// SubscribeOnce subscribes to a topic once. Handler will be removed after executing.
// Returns error if `fn` is not a function.
func (bus *eventBus) SubscribeOnce(topic string, fn any) error {
	return bus.doSubscribe(topic, fn, &eventHandler{
		reflect.ValueOf(fn), new(sync.Once), false, false, sync.Mutex{},
	})
}

// SubscribeOnceAsync subscribes to a topic once with an asynchronous callback
// Handler will be removed after executing.
// Returns error if `fn` is not a function.
func (bus *eventBus) SubscribeOnceAsync(topic string, fn any) error {
	return bus.doSubscribe(topic, fn, &eventHandler{
		reflect.ValueOf(fn), new(sync.Once), true, false, sync.Mutex{},
	})
}

// HasCallback returns true if exists any callback subscribed to the topic.
func (bus *eventBus) HasCallback(topic string) bool {
	bus.lock.Lock()
	defer bus.lock.Unlock()
	_, ok := bus.handlers[topic]
	if ok {
		return len(bus.handlers[topic]) > 0
	}
	return false
}

// Unsubscribe removes callback defined for a topic.
// Returns error if there are no callbacks subscribed to the topic.
func (bus *eventBus) Unsubscribe(topic string, handler any) error {
	bus.lock.Lock()
	defer bus.lock.Unlock()
	if _, ok := bus.handlers[topic]; ok && len(bus.handlers[topic]) > 0 {
		bus.removeHandler(topic, bus.findHandlerIdx(topic, reflect.ValueOf(handler)))
		return nil
	}
	return fmt.Errorf("topic %s doesn't exist", topic)
}

// Publish executes callback defined for a topic. Any additional argument will be transferred to the callback.
func (bus *eventBus) Publish(topic string, args ...any) {
	// Handlers slice may be changed by removeHandler and Unsubscribe during iteration,
	// so make a copy and iterate the copied slice.
	bus.lock.Lock()
	handlers := bus.handlers[topic]
	copyHandlers := make([]*eventHandler, len(handlers))
	copy(copyHandlers, handlers)
	bus.lock.Unlock()
	for _, handler := range copyHandlers {
		if !handler.async {
			bus.doPublish(handler, topic, args...)
		} else {
			bus.wg.Add(1)
			if handler.transactional {
				handler.Lock()
			}
			go bus.doPublishAsync(handler, topic, args...)
		}
	}
}

// WaitAsync waits for all async callbacks to complete
func (bus *eventBus) WaitAsync() {
	bus.wg.Wait()
}

// doSubscribe handles the subscription logic and is utilized by the public Subscribe functions
func (bus *eventBus) doSubscribe(topic string, fn any, handler *eventHandler) error {
	bus.lock.Lock()
	defer bus.lock.Unlock()
	if !(reflect.TypeOf(fn).Kind() == reflect.Func) {
		return fmt.Errorf("%s is not of type reflect.Func", reflect.TypeOf(fn).Kind())
	}
	bus.handlers[topic] = append(bus.handlers[topic], handler)
	return nil
}

// doPublish handles the publication logic
func (bus *eventBus) doPublish(handler *eventHandler, topic string, args ...any) {
	passedArguments := bus.setUpPublish(handler, args...)
	if handler.once == nil {
		handler.callBack.Call(passedArguments)
	} else {
		handler.once.Do(func() {
			bus.lock.Lock()
			for idx, h := range bus.handlers[topic] {
				// compare pointers since pointers are unique for all members of slice
				if h.once == handler.once {
					bus.removeHandler(topic, idx)
					break
				}
			}
			bus.lock.Unlock()
			handler.callBack.Call(passedArguments)
		})
	}
}

func (bus *eventBus) doPublishAsync(handler *eventHandler, topic string, args ...any) {
	defer bus.wg.Done()
	if handler.transactional {
		defer handler.Unlock()
	}
	bus.doPublish(handler, topic, args...)
}

func (bus *eventBus) removeHandler(topic string, idx int) {
	if _, ok := bus.handlers[topic]; !ok {
		return
	}
	l := len(bus.handlers[topic])

	if !(0 <= idx && idx < l) {
		return
	}

	copy(bus.handlers[topic][idx:], bus.handlers[topic][idx+1:])
	bus.handlers[topic][l-1] = nil // or the zero value of T
	bus.handlers[topic] = bus.handlers[topic][:l-1]
}

func (bus *eventBus) findHandlerIdx(topic string, callback reflect.Value) int {
	if _, ok := bus.handlers[topic]; ok {
		for idx, handler := range bus.handlers[topic] {
			if handler.callBack.Type() == callback.Type() &&
				handler.callBack.Pointer() == callback.Pointer() {
				return idx
			}
		}
	}
	return -1
}

func (bus *eventBus) setUpPublish(callback *eventHandler, args ...any) []reflect.Value {
	funcType := callback.callBack.Type()
	passedArguments := make([]reflect.Value, len(args))
	for i, v := range args {
		if v == nil {
			passedArguments[i] = reflect.New(funcType.In(i)).Elem()
		} else {
			passedArguments[i] = reflect.ValueOf(v)
		}
	}

	return passedArguments
}
