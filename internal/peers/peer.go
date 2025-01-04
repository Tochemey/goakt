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
	"fmt"
	"net"
	"strconv"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/internal/internalpb"
)

// Peer specifies the cluster member
type Peer struct {
	Name          string
	Host          string
	Port          uint32
	DiscoveryPort uint32
	RemotingPort  uint32
	CreatedAt     time.Time
}

// PeerAddress returns address the node's peers will use to connect to
func (x *Peer) PeerAddress() string {
	return net.JoinHostPort(x.Host, strconv.Itoa(int(x.Port)))
}

// DiscoveryAddress returns the member discoveryAddress
func (x *Peer) DiscoveryAddress() string {
	return net.JoinHostPort(x.Host, strconv.Itoa(int(x.DiscoveryPort)))
}

// String returns the printable representation of Peer
func (x *Peer) String() string {
	return fmt.Sprintf("[host=%s gossip=%d peers=%d remoting=%d]", x.Host, x.DiscoveryPort, x.Port, x.RemotingPort)
}

// peerFromMeta returns a Peer record from
// a node metadata
func peerFromMeta(meta []byte) (*Peer, error) {
	nodeMeta := new(internalpb.PeerMeta)
	if err := proto.Unmarshal(meta, nodeMeta); err != nil {
		return nil, err
	}
	return &Peer{
		Name:          nodeMeta.GetName(),
		Host:          nodeMeta.GetHost(),
		Port:          nodeMeta.GetPort(),
		DiscoveryPort: nodeMeta.GetDiscoveryPort(),
		RemotingPort:  nodeMeta.GetRemotingPort(),
		CreatedAt:     nodeMeta.GetCreationTime().AsTime(),
	}, nil
}
