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

package actor

import (
	"errors"

	"github.com/tochemey/goakt/v4/remote"
)

// poisonPillMagic is the fixed 8-byte sentinel written on the wire for every
// *PoisonPill message. The value is intentionally distinct from any valid
// framing used by other serializers so that the remoting layer can dispatch
// it unambiguously.
var poisonPillMagic = [8]byte{0xDE, 0xAD, 0xBE, 0xEF, 0xCA, 0xFE, 0xBA, 0xBE}

// errNotPoisonPillFrame is returned when a frame is not a valid PoisonPill sentinel.
var errNotPoisonPillFrame = errors.New("remote: not a PoisonPill frame")

// poisonPillSerializer is an unexported [remote.Serializer] implementation for
// the [PoisonPill] message. Because PoisonPill carries no payload, its entire
// wire representation is the 8-byte [poisonPillMagic] sentinel — Serialize is
// a single copy and Deserialize is a single 8-byte comparison.
//
// This serializer is registered automatically inside [actorSystem.setupRemoting]
// and is never exposed to application code.
type poisonPillSerializer struct{}

// enforce the remote.Serializer interface at compile time.
var _ remote.Serializer = (*poisonPillSerializer)(nil)

// Serialize encodes a *[PoisonPill] as the fixed 8-byte sentinel.
// Returns [errNotPoisonPillFrame] if message is not *PoisonPill.
func (x *poisonPillSerializer) Serialize(message any) ([]byte, error) {
	if _, ok := message.(*PoisonPill); !ok {
		return nil, errNotPoisonPillFrame
	}
	out := make([]byte, 8)
	copy(out, poisonPillMagic[:])
	return out, nil
}

// Deserialize decodes the 8-byte sentinel back to a *[PoisonPill].
// Returns [errNotPoisonPillFrame] for any frame that is not the exact sentinel.
func (x *poisonPillSerializer) Deserialize(data []byte) (any, error) {
	if len(data) != 8 || [8]byte(data) != poisonPillMagic {
		return nil, errNotPoisonPillFrame
	}
	return new(PoisonPill), nil
}
