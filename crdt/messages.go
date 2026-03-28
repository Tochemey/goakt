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

package crdt

// Update is sent to the Replicator to create or update a CRDT key.
//
// The update is always applied locally first and the delta is published
// to the key's topic via TopicActor. If WriteTo is set, the Replicator
// also sends the delta directly to peers and waits for acknowledgments
// before returning the response.
//
// The Modify function is called by the Replicator and must be a pure
// function that only uses the data parameter and stable fields from
// enclosing scope.
type Update[T ReplicatedData] struct {
	Key     Key[T]
	Initial T
	Modify  func(current T) T
	WriteTo Coordination
}

// UpdateResponse is the response to an Ask-based Update.
type UpdateResponse struct{}

// Get is sent to the Replicator to read a CRDT key.
//
// The local value is always returned. If ReadFrom is set, the Replicator
// also queries peers, merges their values with the local value, and
// returns the merged result.
type Get[T ReplicatedData] struct {
	Key      Key[T]
	ReadFrom Coordination
}

// GetResponse is the typed response to a Get request.
type GetResponse[T ReplicatedData] struct {
	Key  Key[T]
	Data T
}

// Subscribe registers the sender for change notifications on a key.
// The subscriber will receive Changed messages whenever the key's value
// is updated, either by a local mutation or a peer delta.
// The subscriber is automatically unsubscribed if it terminates.
type Subscribe[T ReplicatedData] struct {
	Key Key[T]
}

// Unsubscribe removes the sender from change notifications on a key.
type Unsubscribe[T ReplicatedData] struct {
	Key Key[T]
}

// Changed is sent to watchers when a CRDT key's value changes.
// This message is delivered for both local mutations and remote delta merges.
type Changed[T ReplicatedData] struct {
	Key  Key[T]
	Data T
}

// Delete is sent to the Replicator to remove a CRDT key.
// Deletion publishes a tombstone to the key's topic. Tombstones are
// retained for the configured TombstoneTTL before pruning.
type Delete[T ReplicatedData] struct {
	Key     Key[T]
	WriteTo Coordination
}

// DeleteResponse is the response to an Ask-based Delete.
type DeleteResponse struct{}

// KeyID returns the key's string identifier for the replicator.
func (u *Update[T]) KeyID() string { return u.Key.ID() }

// CRDTDataType returns the CRDT data type for this update.
func (u *Update[T]) CRDTDataType() DataType { return u.Key.Type() }

// InitialValue returns the initial CRDT value for a new key.
func (u *Update[T]) InitialValue() ReplicatedData { return u.Initial }

// Apply applies the mutation to the current value.
// If current is nil, the mutation is applied to the Initial value.
// If current is not of type T, it falls back to the Initial value
// to prevent a panic from a type mismatch.
func (u *Update[T]) Apply(current ReplicatedData) ReplicatedData {
	if current == nil {
		return u.Modify(u.Initial)
	}
	typed, ok := current.(T)
	if !ok {
		return u.Modify(u.Initial)
	}
	return u.Modify(typed)
}

// KeyID returns the key's string identifier for the replicator.
func (g *Get[T]) KeyID() string { return g.Key.ID() }

// Response builds a typed GetResponse from the raw ReplicatedData.
// If the stored data is not of type T (type mismatch), Data is returned
// as the zero value of T.
func (g *Get[T]) Response(data ReplicatedData) any {
	var typed T
	if data != nil {
		typed, _ = data.(T)
	}
	return &GetResponse[T]{Key: g.Key, Data: typed}
}

// KeyID returns the key's string identifier for the replicator.
func (s *Subscribe[T]) KeyID() string { return s.Key.ID() }

// IsSubscribe is a marker method to distinguish Subscribe from other commands.
func (s *Subscribe[T]) IsSubscribe() {}

// KeyID returns the key's string identifier for the replicator.
func (u *Unsubscribe[T]) KeyID() string { return u.Key.ID() }

// IsUnsubscribe is a marker method to distinguish Unsubscribe from other commands.
func (u *Unsubscribe[T]) IsUnsubscribe() {}

// KeyID returns the key's string identifier for the replicator.
func (d *Delete[T]) KeyID() string { return d.Key.ID() }

// IsDelete is a marker method to distinguish Delete from other commands.
func (d *Delete[T]) IsDelete() {}
