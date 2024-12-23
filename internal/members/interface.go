/*
 * MIT License
 *
 * Copyright (c) 2022-2024  Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package members

import (
	"context"

	"github.com/tochemey/goakt/v2/internal/internalpb"
)

// IServer defines the members server interface
type IServer interface {
	// Start starts the cluster engine
	Start(ctx context.Context) error
	// Stop stops the cluster engine
	Stop(ctx context.Context) error
	// PutActor sets the given actor on the localState of the node and let the replicator
	// pushes to the rest of the cluster
	PutActor(actor *internalpb.ActorRef) error
	// GetActor fetches an actor from the cluster
	GetActor(actorName, actorKind string) (*internalpb.ActorRef, error)
	// SetSchedulerJobKey sets a given key to the cluster
	SetSchedulerJobKey(key string) error
	// SchedulerJobKeyExists checks the existence of a given key
	SchedulerJobKeyExists(key string) bool
	// UnsetSchedulerJobKey unsets the already set given key in the cluster
	UnsetSchedulerJobKey(ctx context.Context, key string) error
	// DeleteActor removes a given actor from the cluster.
	// An actor is removed from the cluster when this actor has been passivated.
	DeleteActor(ctx context.Context, actorName, actorKind string) error
	// Peers returns a channel containing the list of peers at a given time
	Peers() ([]*Member, error)
	// Leader returns the eldest member of the cluster
	// Leadership here is based upon node time creation
	Leader() (*Member, error)
	// Whoami returns the node member data
	Whoami() *Member
	// GetState fetches a given peer state
	GetState(peerAddress string) (*internalpb.PeerState, error)
	// IsLeader states whether the given cluster node is a leader or not at a given
	// point in time in the cluster
	IsLeader() (bool, error)
	// Events returns a channel where cluster events are published
	Events() <-chan *Event
	// Address returns the node host:port address
	Address() string
}
