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
	"net"
	"slices"
	"strconv"
	"sync"

	goset "github.com/deckarep/golang-set/v2"
	"github.com/hashicorp/memberlist"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/log"
)

type delegate struct {
	sync.RWMutex
	meta         *internalpb.PeerMeta
	localState   *internalpb.LocalState
	remoteStates *internalpb.RemoteStates
	logger       log.Logger
	nodeAddress  string
}

// enforce compilation error
var _ memberlist.Delegate = (*delegate)(nil)

func newDelegate(meta *internalpb.PeerMeta, peerState *internalpb.PeerState, logger log.Logger) *delegate {
	return &delegate{
		meta: meta,
		localState: &internalpb.LocalState{
			PeerState: peerState,
			JobKeys:   []string{},
		},
		remoteStates: &internalpb.RemoteStates{
			States: make(map[string]*internalpb.LocalState, 20),
		},
		logger:      logger,
		nodeAddress: net.JoinHostPort(peerState.GetHost(), strconv.Itoa(int(peerState.GetPeersPort()))),
	}
}

// NodeMeta is used to retrieve meta-data about the current node
// when broadcasting an alive message. It's length is limited to
// the given byte size. This metadata is available in the Node structure.
// nolint
func (x *delegate) NodeMeta(limit int) []byte {
	x.Lock()
	// no need to check the error
	bytea, _ := proto.Marshal(x.meta)
	x.Unlock()
	return bytea
}

// NotifyMsg is called when a user-data message is received.
// Care should be taken that this method does not block, since doing
// so would block the entire UDP packet receive loop. Additionally, the byte
// slice may be modified after the call returns, so it should be copied if needed
// nolint
func (x *delegate) NotifyMsg(bytes []byte) {
}

// GetBroadcasts is called when user data messages can be broadcast.
// It can return a list of buffers to send. Each buffer should assume an
// overhead as provided with a limit on the total byte size allowed.
// The total byte size of the resulting data to send must not exceed
// the limit. Care should be taken that this method does not block,
// since doing so would block the entire UDP packet receive loop.
// nolint
func (x *delegate) GetBroadcasts(overhead, limit int) [][]byte {
	return nil
}

// LocalState is used for a TCP Push/Pull. This is sent to
// the remote side in addition to the membership information. Any
// data can be sent here. See MergeRemoteState as well. The `join`
// boolean indicates this is for a join instead of a push/pull.
// nolint
func (x *delegate) LocalState(join bool) []byte {
	x.Lock()
	bytea, _ := proto.Marshal(x.localState)
	x.Unlock()
	return bytea
}

// MergeRemoteState is invoked after a TCP Push/Pull. This is the
// delegate received from the remote side and is the result of the
// remote side's LocalState call. The 'join'
// boolean indicates this is for a join instead of a push/pull.
// nolint
func (x *delegate) MergeRemoteState(buf []byte, join bool) {
	x.Lock()
	localState := new(internalpb.LocalState)
	_ = proto.Unmarshal(buf, localState)
	id := net.JoinHostPort(localState.GetPeerState().GetHost(), strconv.Itoa(int(localState.GetPeerState().GetPeersPort())))
	x.logger.Debugf("%s merging remote state from=%s", x.nodeAddress, id)
	x.remoteStates.GetStates()[id] = localState
	x.logger.Debugf("%s remote state from=%s successfully merged", x.nodeAddress, id)
	x.Unlock()
}

// PutActor puts an actor to the localState that will be replicated to the rest of the cluster
func (x *delegate) PutActor(actor *internalpb.ActorRef) {
	x.Lock()
	peerState := x.localState.GetPeerState()
	actors := peerState.GetActors()

	// make sure we don't duplicate actors
	for _, xactor := range actors {
		if proto.Equal(xactor, actor) {
			x.Unlock()
			return
		}
	}

	peerState.Actors = append(peerState.GetActors(), actor)
	x.localState.PeerState = peerState
	x.Unlock()
}

// PutJobKey puts a job key in the localState that will be replicated to the rest of the cluster
func (x *delegate) PutJobKey(key string) {
	x.Lock()
	keys := x.localState.GetJobKeys()
	set := goset.NewSet(keys...)
	set.Add(key)
	x.localState.JobKeys = set.ToSlice()
	x.Unlock()
}

// GetActor retrieves an actor either from the localState or from the remote states
// Returns the actor or an actor not found error
func (x *delegate) GetActor(actorName, actorKind string) (*internalpb.ActorRef, string, error) {
	x.Lock()
	owner := net.JoinHostPort(x.localState.GetPeerState().GetHost(), strconv.Itoa(int(x.localState.GetPeerState().GetPeersPort())))
	actors := x.localState.GetPeerState().GetActors()
	for _, actor := range actors {
		name := actor.GetActorAddress().GetName()
		kind := actor.GetActorType()
		if actorName == name && actorKind == kind {
			x.Unlock()
			return actor, owner, nil
		}
	}

	for _, localState := range x.remoteStates.GetStates() {
		owner := net.JoinHostPort(localState.GetPeerState().GetHost(), strconv.Itoa(int(localState.GetPeerState().GetPeersPort())))
		actors := localState.GetPeerState().GetActors()
		for _, actor := range actors {
			name := actor.GetActorAddress().GetName()
			kind := actor.GetActorType()
			if actorName == name && actorKind == kind {
				x.Unlock()
				return actor, owner, nil
			}
		}
	}
	x.Unlock()
	return nil, "", ErrActorNotFound
}

// GetJobKey retrieves a job key from the localState or from the remote states.
// Returns the key reference or a key not found error
func (x *delegate) GetJobKey(key string) (*string, string, error) {
	x.Lock()
	owner := net.JoinHostPort(x.localState.GetPeerState().GetHost(), strconv.Itoa(int(x.localState.GetPeerState().GetPeersPort())))
	keys := x.localState.GetJobKeys()
	if slices.Contains(keys, key) {
		x.Unlock()
		return &key, owner, nil
	}

	for _, localState := range x.remoteStates.GetStates() {
		owner := net.JoinHostPort(localState.GetPeerState().GetHost(), strconv.Itoa(int(localState.GetPeerState().GetPeersPort())))
		keys := localState.GetJobKeys()
		if slices.Contains(keys, key) {
			x.Unlock()
			return &key, owner, nil
		}
	}
	x.Unlock()
	return nil, "", ErrKeyNotFound
}

// DeleteActor removes an actor from the localState
func (x *delegate) DeleteActor(actorName, actorKind string) {
	x.Lock()
	for index, actor := range x.localState.GetPeerState().GetActors() {
		kind := actor.GetActorType()
		name := actor.GetActorAddress().GetName()
		if kind == actorKind && name == actorName {
			x.localState.GetPeerState().Actors = append(x.localState.GetPeerState().GetActors()[:index], x.localState.GetPeerState().GetActors()[index+1:]...)
			x.Unlock()
			return
		}
	}
	x.Unlock()
}

// DeleteJobKey removes a job key from the localState
func (x *delegate) DeleteJobKey(key string) error {
	x.Lock()
	keys := x.localState.GetJobKeys()
	set := goset.NewSet(keys...)
	if !set.Contains(key) {
		return ErrKeyNotFound
	}
	set.Remove(key)
	x.localState.JobKeys = set.ToSlice()
	x.Unlock()
	return nil
}

// GetState returns the peer state
func (x *delegate) GetState(peerAddress string) (*internalpb.PeerState, error) {
	x.Lock()
	peerState := x.localState.GetPeerState()
	id := net.JoinHostPort(peerState.GetHost(), strconv.Itoa(int(peerState.GetPeersPort())))
	if id == peerAddress {
		x.Unlock()
		return peerState, nil
	}

	for _, localState := range x.remoteStates.GetStates() {
		peerState := localState.GetPeerState()
		id := net.JoinHostPort(peerState.GetHost(), strconv.Itoa(int(peerState.GetPeersPort())))
		if id == peerAddress {
			x.Unlock()
			return peerState, nil
		}
	}

	x.Unlock()
	return nil, ErrPeerSyncNotFound
}
