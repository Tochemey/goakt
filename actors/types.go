package actors

import "sync"

type Unit struct{}

type pidMap struct {
	mu   sync.Mutex
	pids map[string]PID
}

func newPIDMap(cap int) *pidMap {
	return &pidMap{
		mu:   sync.Mutex{},
		pids: make(map[string]PID, cap),
	}
}

// Len returns the number of PIDs
func (m *pidMap) Len() int {
	return len(m.pids)
}

// Get retrieves a pid by its address
func (m *pidMap) Get(path string) (pid PID, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pid, ok = m.pids[path]
	return
}

// Set sets a pid in the map
func (m *pidMap) Set(child PID) {
	m.mu.Lock()
	m.pids[child.ActorPath().String()] = child
	m.mu.Unlock()
}

// Delete removes a pid from the map
func (m *pidMap) Delete(addr *Path) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pids, addr.String())
}

// List returns all actors as a slice
func (m *pidMap) List() []PID {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]PID, 0, len(m.pids))
	for _, actor := range m.pids {
		out = append(out, actor)
	}
	return out
}
