package actors

import (
	"sync"

	pb "github.com/tochemey/goakt/pb/v1"
)

// Unit type
type Unit struct{}

// NoSender means that there is no sender
var NoSender PID

// RemoteNoSender means that there is no sender
var RemoteNoSender = new(pb.Address)

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
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pids)
}

// Get retrieves a pid by its address
func (m *pidMap) Get(path *Path) (pid PID, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pid, ok = m.pids[path.String()]
	return
}

// Set sets a pid in the map
func (m *pidMap) Set(pid PID) {
	m.mu.Lock()
	m.pids[pid.ActorPath().String()] = pid
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
