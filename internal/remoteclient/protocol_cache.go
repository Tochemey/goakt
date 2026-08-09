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

package remoteclient

import (
	"errors"
	"time"
)

// errPreferLegacy is an internal sentinel returned when auto mode should retry
// on the legacy SendProto path after a handshake failure that indicates the
// peer is legacy-only (EOF or connection reset before HELLO_ACK).
var errPreferLegacy = errors.New("remote: prefer legacy protocol")

// errLaneClosedDuringDial is an internal sentinel returned when ClosePeer or
// shutdown reset the peer's lane set while a lane dial was in flight; the
// freshly dialed session is discarded rather than installed on the reset peer.
var errLaneClosedDuringDial = errors.New("duplex lane closed during dial")

// peerLegacyReprobeInterval is how long auto mode trusts a legacy
// classification before clearing the cache and probing duplex again. This
// enables switchover when a peer is upgraded without requiring a process
// restart on the dialer.
const peerLegacyReprobeInterval = 30 * time.Second

// peerProtocol records the cached wire protocol for a remote peer endpoint.
type peerProtocol int

const (
	// peerProtocolUnknown means the dialer has not yet classified the peer.
	// The next outbound call probes according to the configured ProtocolPin.
	peerProtocolUnknown peerProtocol = iota

	// peerProtocolDuplex means traffic uses the multiplexed duplex protocol
	// over a long-lived [inet.DuplexSession].
	peerProtocolDuplex

	// peerProtocolLegacy means traffic uses the legacy unary SendProto path
	// (and the coalescer when enabled). Cleared on reconnect or after
	// [peerLegacyReprobeInterval] so auto mode can re-probe.
	peerProtocolLegacy
)

// protocolCache holds the cached protocol classification for one peer
// endpoint (host:port). It is not synchronized itself; the owning [peer]
// mutex serializes reads and writes.
type protocolCache struct {
	// value is the last observed protocol for this peer.
	value peerProtocol
	// markedAt records when value was last set; used for legacy re-probe.
	markedAt time.Time
}

// get returns the cached protocol. Zero value is [peerProtocolUnknown].
func (x *protocolCache) get() peerProtocol {
	return x.value
}

// set stores the peer protocol classification after a successful dial or a
// definitive legacy handshake failure.
func (x *protocolCache) set(protocol peerProtocol) {
	x.value = protocol
	x.markedAt = time.Now()
}

// clear resets the cache so the next dial re-probes in auto mode. Called when
// a duplex session is torn down after a transport failure or when a legacy
// classification expires.
func (x *protocolCache) clear() {
	x.value = peerProtocolUnknown
	x.markedAt = time.Time{}
}

// isLegacy reports whether the cached protocol is legacy-only.
func (x *protocolCache) isLegacy() bool {
	return x.value == peerProtocolLegacy
}

// isDuplex reports whether the cached protocol is the multiplexed duplex path.
func (x *protocolCache) isDuplex() bool {
	return x.value == peerProtocolDuplex
}

// legacyExpired reports whether a legacy classification should be cleared so
// auto mode can probe duplex again.
func (x *protocolCache) legacyExpired(now time.Time) bool {
	return x.value == peerProtocolLegacy && !x.markedAt.IsZero() && now.Sub(x.markedAt) >= peerLegacyReprobeInterval
}
