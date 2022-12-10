package actors

import "sync"

type Unit struct{}

type pidMap struct {
	mu   sync.Mutex
	pids map[Address]*PID
}

func newPIDMap(cap int) *pidMap {
	return &pidMap{
		mu:   sync.Mutex{},
		pids: make(map[Address]*PID, cap),
	}
}

// Len returns the number of PIDs
func (m *pidMap) Len() int {
	return len(m.pids)
}

// Get retrieves a PID by its address
func (m *pidMap) Get(addr Address) (pid *PID, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pid, ok = m.pids[addr]
	return
}

// Set sets a PID in the map
func (m *pidMap) Set(child *PID) {
	m.mu.Lock()
	m.pids[child.addr] = child
	m.mu.Unlock()
}

// Delete removes a PID from the map
func (m *pidMap) Delete(addr Address) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pids, addr)
}

// All returns all actors as a slice
func (m *pidMap) All() []*PID {
	out := make([]*PID, 0, len(m.pids))
	for _, actor := range m.pids {
		out = append(out, actor)
	}
	return out
}
