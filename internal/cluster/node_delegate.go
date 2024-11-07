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

package cluster

import (
	"encoding/json"
	"net"
	"slices"
	"strconv"
	"sync"

	"github.com/hashicorp/memberlist"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/internal/internalpb"
)

// nodeDelegate implements memberlist Delegate
type nodeDelegate struct {
	sync.RWMutex
	// node metadata shared in the cluster
	// for instance the IP discoveryAddress of the node, the name of the node
	// relevant information that can be known by the other peers in the cluster
	meta []byte

	// localState holds the node local state
	// this will be replicated on other peer nodes
	// via the gossip protocol
	localState *internalpb.LocalState

	// remoteStates holds all the peers state
	// this will be used when merging other node state
	// during merge each node will unmarshal the incoming bytes array into
	// internalpb.RemoteStates and try to find out whether the given entry exists in its peer
	// state and add it.
	remoteStates *internalpb.RemoteStates
}

// enforce compilation error
var _ memberlist.Delegate = (*nodeDelegate)(nil)

// newNodeDelegate creates an instance of nodeDelegate
func (n *Node) newNodeDelegate() (*nodeDelegate, error) {
	node := n.node
	bytea, err := json.Marshal(node)
	if err != nil {
		return nil, err
	}

	return &nodeDelegate{
		RWMutex: sync.RWMutex{},
		localState: &internalpb.LocalState{
			PeerState: n.peerState,
			JobKeys:   []*internalpb.ScheduleJobKey{},
		},
		meta: bytea,
		remoteStates: &internalpb.RemoteStates{
			RemoteStates: make(map[string]*internalpb.LocalState, 20),
		},
	}, nil
}

// NodeMeta is used to retrieve meta-data about the current node
// when broadcasting an alive message. It's length is limited to
// the given byte size. This metadata is available in the Node structure.
// nolint
func (d *nodeDelegate) NodeMeta(limit int) []byte {
	return d.meta
}

// NotifyMsg is called when a user-data message is received.
// Care should be taken that this method does not block, since doing
// so would block the entire UDP packet receive loop. Additionally, the byte
// slice may be modified after the call returns, so it should be copied if needed
// nolint
func (d *nodeDelegate) NotifyMsg(bytes []byte) {
}

// GetBroadcasts is called when user data messages can be broadcast.
// It can return a list of buffers to send. Each buffer should assume an
// overhead as provided with a limit on the total byte size allowed.
// The total byte size of the resulting data to send must not exceed
// the limit. Care should be taken that this method does not block,
// since doing so would block the entire UDP packet receive loop.
// nolint
func (d *nodeDelegate) GetBroadcasts(overhead, limit int) [][]byte {
	return nil
}

// LocalState is used for a TCP Push/Pull. This is sent to
// the remote side in addition to the membership information. Any
// data can be sent here. See MergeRemoteState as well. The `join`
// boolean indicates this is for a join instead of a push/pull.
// nolint
func (d *nodeDelegate) LocalState(join bool) []byte {
	d.Lock()
	bytea, _ := proto.Marshal(d.localState)
	d.Unlock()
	return bytea
}

// MergeRemoteState is invoked after a TCP Push/Pull. This is the
// delegate received from the remote side and is the result of the
// remote side's LocalState call. The 'join'
// boolean indicates this is for a join instead of a push/pull.
// nolint
func (d *nodeDelegate) MergeRemoteState(buf []byte, join bool) {
	d.Lock()
	incomingState := new(internalpb.LocalState)
	_ = proto.Unmarshal(buf, incomingState)
	remotePeerState := incomingState.GetPeerState()
	remotePeerAddress := net.JoinHostPort(remotePeerState.GetHost(), strconv.Itoa(int(remotePeerState.GetPeersPort())))

	// override the existing peer state if already exists
	d.remoteStates.GetRemoteStates()[remotePeerAddress] = incomingState
	d.Unlock()
}

// PutActor adds an actor to the local state that will be replicated to
// the other nodes during push/pull
func (d *nodeDelegate) PutActor(actor *internalpb.ActorRef) {
	d.Lock()
	peerState := d.localState.GetPeerState()
	actors := append(peerState.GetActors(), actor)
	compacted := slices.CompactFunc(actors, func(ref *internalpb.ActorRef, ref2 *internalpb.ActorRef) bool {
		return proto.Equal(ref, ref2)
	})

	peerState.Actors = compacted
	d.localState.PeerState = peerState
	d.Unlock()
}

// GetActor returns the value of the given actor
// This can return a false negative meaning that the actor may exist but at the time of checking it
// is having yet to be replicated in the cluster
func (d *nodeDelegate) GetActor(actorName string) (*internalpb.ActorRef, error) {
	d.RLock()
	// first let us lookup our local state and see whether the given is in there
	// if exists return it otherwise check the peer state maybe a node has it
	for _, actorRef := range d.localState.GetPeerState().GetActors() {
		if actorRef.ActorAddress.GetName() == actorName {
			d.RUnlock()
			return actorRef, nil
		}
	}

	// second when not found locally check the remote peer states
	for _, peerState := range d.remoteStates.GetRemoteStates() {
		for _, actorRef := range peerState.GetPeerState().GetActors() {
			if actorRef.ActorAddress.GetName() == actorName {
				d.RUnlock()
				return actorRef, nil
			}
		}
	}

	d.RUnlock()
	return nil, ErrActorNotFound
}

// DeleteActor deletes the given actor from the cluster
// One can only delete the actor if the given node is the owner
func (d *nodeDelegate) DeleteActor(actorName string) {
	d.Lock()
	localState := d.localState
	actors := localState.GetPeerState().GetActors()
	actors = slices.DeleteFunc(
		actors,
		func(actorRef *internalpb.ActorRef) bool {
			return actorRef.GetActorAddress().GetName() == actorName
		})

	localState.GetPeerState().Actors = actors
	d.Unlock()
}

// PutJobKey adds a job key to the local state that will be replicated to
// the other nodes during push/pull
func (d *nodeDelegate) PutJobKey(key string) {
	d.Lock()
	jobKeys := d.localState.GetJobKeys()
	jobKeys = append(jobKeys, &internalpb.ScheduleJobKey{
		JobKey: key,
	})
	compacted := slices.CompactFunc(jobKeys, func(key1 *internalpb.ScheduleJobKey, key2 *internalpb.ScheduleJobKey) bool {
		return proto.Equal(key1, key2)
	})
	d.localState.JobKeys = compacted
	d.Unlock()
}

// JobKeyExists checks whether a given exists
// This can return a false negative meaning that the key may exist but at the time of checking it
// is having yet to be replicated in the cluster
func (d *nodeDelegate) JobKeyExists(key string) bool {
	d.RLock()
	for _, jobKey := range d.localState.GetJobKeys() {
		if jobKey.GetJobKey() == key {
			d.RUnlock()
			return true
		}
	}

	// second when not found locally check the remote peer states
	for _, peerState := range d.remoteStates.GetRemoteStates() {
		for _, jobKey := range peerState.GetJobKeys() {
			if jobKey.GetJobKey() == key {
				d.RUnlock()
				return true
			}
		}
	}

	d.RUnlock()
	return false
}

// DeleteJobKey deletes the given job key from the cluster
// One can only delete the job key if the given node is the owner
func (d *nodeDelegate) DeleteJobKey(key string) {
	d.Lock()
	keys := d.localState.GetJobKeys()
	keys = slices.DeleteFunc(keys, func(jobKey *internalpb.ScheduleJobKey) bool {
		return jobKey.GetJobKey() == key
	})
	d.localState.JobKeys = keys
	d.Unlock()
}

// GetPeerState returns a peer state given its address
func (d *nodeDelegate) GetPeerState(address string) (*internalpb.PeerState, error) {
	d.Lock()
	me := net.JoinHostPort(d.localState.GetPeerState().GetHost(), strconv.Itoa(int(d.localState.GetPeerState().GetPeersPort())))
	if me == address {
		d.Unlock()
		return d.localState.GetPeerState(), nil
	}

	for _, remoteState := range d.remoteStates.GetRemoteStates() {
		peerState := remoteState.GetPeerState()
		remoteAddr := net.JoinHostPort(peerState.GetHost(), strconv.Itoa(int(peerState.GetPeersPort())))
		if remoteAddr == address {
			d.Unlock()
			return peerState, nil
		}
	}

	d.Unlock()
	return nil, ErrPeerSyncNotFound
}
