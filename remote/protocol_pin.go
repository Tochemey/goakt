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
//
// The dual-protocol listener discriminates on the first byte after TLS.
// Legacy brotli compression emits no magic byte and can collide with the
// duplex discriminator, so deployments that use brotli on the legacy path
// must pin [ProtocolPinLegacy] or [ProtocolPinDuplex] instead of
// [ProtocolPinAuto].
type ProtocolPin int

const (
	// ProtocolPinAuto enables dual-protocol compatibility. Inbound connections
	// are classified by the first byte after TLS: duplex when the peer speaks
	// the duplex protocol, otherwise legacy. Dialers prefer duplex and fall
	// back to legacy when the peer does not speak it.
	ProtocolPinAuto ProtocolPin = iota

	// ProtocolPinLegacy restricts remoting to the legacy unary
	// protobuf-over-TCP protocol on both dial and accept paths.
	ProtocolPinLegacy

	// ProtocolPinDuplex restricts remoting to the multiplexed duplex protocol
	// on both dial and accept paths, with no legacy fallback.
	ProtocolPinDuplex
)

// String returns the stable config name for the pin.
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
