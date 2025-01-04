/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
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

package peers

import (
	"sync"

	"github.com/hashicorp/memberlist"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/internal/internalpb"
)

type delegate struct {
	sync.RWMutex
	meta *internalpb.PeerMeta
}

var (
	// enforce compilation error
	_ memberlist.Delegate = (*delegate)(nil)
)

func newDelegate(meta *internalpb.PeerMeta) *delegate {
	return &delegate{
		meta: meta,
	}
}

// NodeMeta is used to retrieve peerMeta-data about the current node
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
func (x *delegate) NotifyMsg(bytes []byte) {}

// GetBroadcasts is called when user data messages can be broadcast.
// It can return a list of buffers to send. Each buffer should assume an
// overhead as provided with a limit on the total byte size allowed.
// The total byte size of the resulting data to send must not exceed
// the limit. Care should be taken that this method does not block,
// since doing so would block the entire UDP packet receive loop.
// nolint
func (x *delegate) GetBroadcasts(overhead, limit int) [][]byte { return nil }

// LocalState is used for a TCP Push/Pull. This is sent to
// the remote side in addition to the membership information. Any
// data can be sent here. See MergeRemoteState as well. The `join`
// boolean indicates this is for a join instead of a push/pull.
// nolint
func (x *delegate) LocalState(join bool) []byte { return nil }

// MergeRemoteState is invoked after a TCP Push/Pull. This is the
// delegate received from the remote side and is the result of the
// remote side's LocalState call. The 'join'
// boolean indicates this is for a join instead of a push/pull.
// nolint
func (x *delegate) MergeRemoteState(buf []byte, join bool) {}
