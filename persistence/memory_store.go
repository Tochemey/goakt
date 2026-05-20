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

package persistence

import (
	"context"
	"slices"
	"sync"
)

// MemoryEventsStore is a mutex-protected, in-memory implementation of
// [EventsStore]. It is provided for convenience in unit tests and should
// not be used in production — all state is lost when the process exits.
type MemoryEventsStore struct {
	mu     sync.RWMutex
	events map[string][]*PersistedEvent
}

var _ EventsStore = (*MemoryEventsStore)(nil)

// NewMemoryEventsStore returns a ready-to-use [MemoryEventsStore].
func NewMemoryEventsStore() *MemoryEventsStore {
	return &MemoryEventsStore{
		events: make(map[string][]*PersistedEvent),
	}
}

// WriteEvents appends events to the in-memory store.
func (s *MemoryEventsStore) WriteEvents(_ context.Context, events []*PersistedEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, e := range events {
		s.events[e.PersistenceID] = append(s.events[e.PersistenceID], e)
	}

	return nil
}

// ReplayEvents returns events whose sequence numbers fall in [from, to].
// to=0 means no upper bound. limit=0 means no page limit.
func (s *MemoryEventsStore) ReplayEvents(_ context.Context, persistenceID string, from, to, limit uint64) ([]*PersistedEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []*PersistedEvent

	for _, e := range s.events[persistenceID] {
		if e.SequenceNumber < from {
			continue
		}

		if to > 0 && e.SequenceNumber > to {
			break
		}

		out = append(out, e)

		if limit > 0 && uint64(len(out)) >= limit {
			break
		}
	}

	return out, nil
}

// GetLatestEvent returns the most recently written event for persistenceID,
// or nil if none exist.
func (s *MemoryEventsStore) GetLatestEvent(_ context.Context, persistenceID string) (*PersistedEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	events := s.events[persistenceID]
	if len(events) == 0 {
		return nil, nil
	}

	return events[len(events)-1], nil
}

// DeleteEvents removes all events with sequence number ≤ toSequenceNumber.
func (s *MemoryEventsStore) DeleteEvents(_ context.Context, persistenceID string, toSequenceNumber uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.events[persistenceID] = slices.DeleteFunc(s.events[persistenceID], func(e *PersistedEvent) bool {
		return e.SequenceNumber <= toSequenceNumber
	})

	return nil
}

// MemorySnapshotStore is a mutex-protected, in-memory implementation of
// [SnapshotStore]. It is provided for convenience in unit tests and should
// not be used in production — all state is lost when the process exits.
type MemorySnapshotStore struct {
	mu        sync.RWMutex
	snapshots map[string]*PersistedSnapshot
}

var _ SnapshotStore = (*MemorySnapshotStore)(nil)

// NewMemorySnapshotStore returns a ready-to-use [MemorySnapshotStore].
func NewMemorySnapshotStore() *MemorySnapshotStore {
	return &MemorySnapshotStore{
		snapshots: make(map[string]*PersistedSnapshot),
	}
}

// WriteSnapshot overwrites the stored snapshot for the given persistence ID.
func (s *MemorySnapshotStore) WriteSnapshot(_ context.Context, snapshot *PersistedSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.snapshots[snapshot.PersistenceID] = snapshot

	return nil
}

// GetLatestSnapshot returns the stored snapshot for persistenceID, or nil if none exists.
func (s *MemorySnapshotStore) GetLatestSnapshot(_ context.Context, persistenceID string) (*PersistedSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.snapshots[persistenceID], nil
}

// DeleteSnapshots removes the snapshot if its sequence number is ≤ toSequenceNumber.
func (s *MemorySnapshotStore) DeleteSnapshots(_ context.Context, persistenceID string, toSequenceNumber uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if snap := s.snapshots[persistenceID]; snap != nil && snap.SequenceNumber <= toSequenceNumber {
		delete(s.snapshots, persistenceID)
	}

	return nil
}
