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
	"strconv"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/internal/internalpb"
)

// Member specifies the cluster member
type Member struct {
	Name          string
	Host          string
	Port          uint32
	DiscoveryPort uint32
	RemotingPort  uint32
	CreatedAt     time.Time
}

// PeerAddress returns address the node's peers will use to connect to
func (m *Member) PeerAddress() string {
	return net.JoinHostPort(m.Host, strconv.Itoa(int(m.Port)))
}

// DiscoveryAddress returns the member discoveryAddress
func (m *Member) DiscoveryAddress() string {
	return net.JoinHostPort(m.Host, strconv.Itoa(int(m.DiscoveryPort)))
}

// memberFromMeta returns a Member record from
// a node metadata
func memberFromMeta(meta []byte) (*Member, error) {
	nodeMeta := new(internalpb.PeerMeta)
	if err := proto.Unmarshal(meta, nodeMeta); err != nil {
		return nil, err
	}
	return &Member{
		Name:          nodeMeta.GetName(),
		Host:          nodeMeta.GetHost(),
		Port:          nodeMeta.GetPort(),
		DiscoveryPort: nodeMeta.GetDiscoveryPort(),
		RemotingPort:  nodeMeta.GetRemotingPort(),
		CreatedAt:     nodeMeta.GetCreationTime().AsTime(),
	}, nil
}
