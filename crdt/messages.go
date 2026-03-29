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
// to the shared goakt.crdt.deltas topic via TopicActor (the key is
// carried inside the delta payload). If WriteTo is set, the Replicator
// also sends the delta directly to peers and waits for acknowledgments
// before returning the response.
//
// The Modify function is called by the Replicator and must be a pure
// function that only uses the data parameter and stable fields from
// enclosing scope.
type Update struct {
	Key     Key
	Initial ReplicatedData
	Modify  func(current ReplicatedData) ReplicatedData
	WriteTo Coordination
}

// UpdateResponse is the response to an Ask-based Update.
type UpdateResponse struct{}

// Get is sent to the Replicator to read a CRDT key.
//
// The local value is always returned. If ReadFrom is set, the Replicator
// also queries peers, merges their values with the local value, and
// returns the merged result.
type Get struct {
	Key      Key
	ReadFrom Coordination
}

// GetResponse is the response to a Get request.
type GetResponse struct {
	Key  Key
	Data ReplicatedData
}

// Subscribe registers the sender for change notifications on a key.
// The subscriber will receive Changed messages whenever the key's value
// is updated, either by a local mutation or a peer delta.
// The subscriber is automatically unsubscribed if it terminates.
type Subscribe struct {
	Key Key
}

// Unsubscribe removes the sender from change notifications on a key.
type Unsubscribe struct {
	Key Key
}

// Changed is sent to watchers when a CRDT key's value changes.
// This message is delivered for both local mutations and remote delta merges.
type Changed struct {
	Key  Key
	Data ReplicatedData
}

// Delete is sent to the Replicator to remove a CRDT key.
// Deletion publishes a tombstone to the shared goakt.crdt.deltas topic.
// Tombstones are retained for the configured TombstoneTTL before pruning.
type Delete struct {
	Key     Key
	WriteTo Coordination
}

// DeleteResponse is the response to an Ask-based Delete.
type DeleteResponse struct{}

// KeyID returns the key's string identifier for the replicator.
func (u *Update) KeyID() string { return u.Key.ID() }

// CRDTDataType returns the CRDT data type for this update.
func (u *Update) CRDTDataType() DataType { return u.Key.Type() }

// InitialValue returns the initial CRDT value for a new key.
func (u *Update) InitialValue() ReplicatedData { return u.Initial }

// Apply applies the mutation to the current value.
// If current is nil, the mutation is applied to the Initial value.
func (u *Update) Apply(current ReplicatedData) ReplicatedData {
	if current == nil {
		return u.Modify(u.Initial)
	}
	return u.Modify(current)
}

// WriteCoordination returns the coordination level for this update.
func (u *Update) WriteCoordination() Coordination { return u.WriteTo }

// KeyID returns the key's string identifier for the replicator.
func (g *Get) KeyID() string { return g.Key.ID() }

// ReadCoordination returns the coordination level for this get.
func (g *Get) ReadCoordination() Coordination { return g.ReadFrom }

// Response builds a GetResponse from the raw ReplicatedData.
func (g *Get) Response(data ReplicatedData) any {
	return &GetResponse{Key: g.Key, Data: data}
}

// KeyID returns the key's string identifier for the replicator.
func (s *Subscribe) KeyID() string { return s.Key.ID() }

// IsSubscribe is a marker method to distinguish Subscribe from other commands.
func (s *Subscribe) IsSubscribe() {}

// KeyID returns the key's string identifier for the replicator.
func (u *Unsubscribe) KeyID() string { return u.Key.ID() }

// IsUnsubscribe is a marker method to distinguish Unsubscribe from other commands.
func (u *Unsubscribe) IsUnsubscribe() {}

// KeyID returns the key's string identifier for the replicator.
func (d *Delete) KeyID() string { return d.Key.ID() }

// IsDelete is a marker method to distinguish Delete from other commands.
func (d *Delete) IsDelete() {}

// WriteCoordination returns the coordination level for this delete.
func (d *Delete) WriteCoordination() Coordination { return d.WriteTo }
