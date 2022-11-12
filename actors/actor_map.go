package actors

import "sync"

type actorMap struct {
	mu     sync.Mutex
	actors map[Address]*ActorRef
}

// newActorMap instantiates a new actor map
func newActorMap(initialCapacity int) *actorMap {
	return &actorMap{
		actors: make(map[Address]*ActorRef, initialCapacity),
		mu:     sync.Mutex{},
	}
}

// Len returns the number of actors
func (m *actorMap) Len() int {
	return len(m.actors)
}

// Get retrieves an actor by Address
func (m *actorMap) Get(addr Address) (value *ActorRef, exists bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, exists = m.actors[addr]
	return value, exists
}

// Set sets an actor in the map
func (m *actorMap) Set(addr Address, actorRef *ActorRef) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.actors[addr] = actorRef
}

// Delete removes an actor from the map
func (m *actorMap) Delete(addr Address) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.actors, addr)
}

// GetAll returns all actors as a slice
func (m *actorMap) GetAll() []*ActorRef {
	out := make([]*ActorRef, 0, len(m.actors))
	for _, actor := range m.actors {
		out = append(out, actor)
	}
	return out
}
