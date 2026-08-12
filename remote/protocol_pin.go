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

package remote

// ProtocolPin selects which remoting wire protocol a node dials and accepts.
// Set it with [WithProtocolPin]; read it back with [Config.ProtocolPin].
//
// Remoting supports a multiplexed duplex protocol and a legacy unary
// protobuf-over-TCP protocol. The pin decides whether both remain available
// for mixed-version rollouts, or whether the node is locked to one path.
//
// The dual-protocol listener (used under [ProtocolPinAuto]) discriminates on
// the first byte after TLS: 0x02 is the duplex frame version and routes to the
// new path; any other byte is replayed into the legacy path. Legacy brotli
// compression emits no magic byte and can collide with that discriminator, so
// deployments that still use brotli on the legacy path must pin
// [ProtocolPinLegacy] or [ProtocolPinDuplex] instead of [ProtocolPinAuto].
type ProtocolPin int

const (
	// ProtocolPinAuto enables dual-protocol compatibility (the default).
	// Inbound connections are classified by the first byte after TLS: duplex
	// when the peer speaks the duplex protocol, otherwise legacy. Dialers
	// prefer duplex and fall back to legacy when the peer closes or resets
	// before HELLO_ACK, caching that peer and re-probing duplex after 30
	// seconds so an upgraded peer can switch over without a dialer restart.
	ProtocolPinAuto ProtocolPin = iota

	// ProtocolPinLegacy restricts remoting to the legacy unary
	// protobuf-over-TCP protocol on both dial and accept. The listener does
	// not sniff for duplex; there is no duplex dial or fallback.
	ProtocolPinLegacy

	// ProtocolPinDuplex restricts remoting to the multiplexed duplex protocol
	// on both dial and accept. The listener refuses non-duplex first bytes;
	// dialers do not fall back to legacy.
	ProtocolPinDuplex
)

// String returns the stable config name for the pin ("auto", "legacy", or
// "duplex"). Unrecognized values return "unknown".
func (x ProtocolPin) String() string {
	switch x {
	case ProtocolPinAuto:
		return "auto"
	case ProtocolPinLegacy:
		return "legacy"
	case ProtocolPinDuplex:
		return "duplex"
	default:
		return "unknown"
	}
}

// Valid reports whether x is a recognized pin value.
func (x ProtocolPin) Valid() bool {
	switch x {
	case ProtocolPinAuto, ProtocolPinLegacy, ProtocolPinDuplex:
		return true
	default:
		return false
	}
}
