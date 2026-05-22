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
	"encoding/binary"
	"errors"
	"time"

	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/remote"
)

const (
	// terminatedMagicLen is the byte length of the leading sentinel that
	// identifies a [Terminated] frame on the wire.
	terminatedMagicLen = 8
	// terminatedPathLenSize is the byte length of the big-endian uint32
	// holding the encoded actor-path length.
	terminatedPathLenSize = 4
	// terminatedTimestampSize is the byte length of the big-endian int64
	// holding the terminatedAt timestamp encoded as Unix nanoseconds.
	terminatedTimestampSize = 8
	// terminatedMinFrameSize is the minimum valid encoded frame length
	// (magic + zero-length path + timestamp).
	terminatedMinFrameSize = terminatedMagicLen + terminatedPathLenSize + terminatedTimestampSize
)

var (
	// terminatedMagic is the fixed 8-byte sentinel written on the wire for
	// every *[Terminated] message. The value is intentionally distinct from
	// any other internal serializer's magic so that the remoting layer can
	// dispatch it unambiguously.
	terminatedMagic = [terminatedMagicLen]byte{0xDE, 0xAD, 0xAC, 0x70, 0x52, 0xBE, 0xEF, 0xED}
	// errNotTerminatedFrame is returned when a frame is not a valid Terminated frame.
	errNotTerminatedFrame = errors.New("remote: not a Terminated frame")
)

// terminatedSerializer is an unexported [remote.Serializer] implementation for
// the [Terminated] message. The wire layout is, in order:
//
//   - terminatedMagic (8 bytes): identifies the frame as a Terminated frame.
//   - actor path length (4 bytes, big-endian uint32).
//   - actor path (N bytes, UTF-8 encoding of [Path.String]).
//   - terminatedAt (8 bytes, big-endian int64, Unix nanoseconds).
//
// The frame is self-describing via the leading magic so the receive-side
// dispatcher can identify it without out-of-band type information.
//
// This serializer is registered automatically inside [actorSystem.setupRemoting]
// and is never exposed to application code.
type terminatedSerializer struct{}

// enforce the remote.Serializer interface at compile time.
var _ remote.Serializer = (*terminatedSerializer)(nil)

// Serialize encodes a *[Terminated] using the layout documented on
// [terminatedSerializer]. Returns [errNotTerminatedFrame] when message is
// not *Terminated or is a typed-nil *Terminated.
func (x *terminatedSerializer) Serialize(message any) ([]byte, error) {
	msg, ok := message.(*Terminated)
	if !ok || msg == nil {
		return nil, errNotTerminatedFrame
	}

	var pathStr string
	if msg.actorPath != nil {
		pathStr = msg.actorPath.String()
	}

	pathBytes := []byte(pathStr)
	out := make([]byte, 0, terminatedMagicLen+terminatedPathLenSize+len(pathBytes)+terminatedTimestampSize)
	out = append(out, terminatedMagic[:]...)
	out = binary.BigEndian.AppendUint32(out, uint32(len(pathBytes)))
	out = append(out, pathBytes...)
	out = binary.BigEndian.AppendUint64(out, uint64(msg.terminatedAt.UnixNano()))
	return out, nil
}

// Deserialize decodes a wire frame produced by [terminatedSerializer.Serialize]
// back to a *[Terminated]. Returns [errNotTerminatedFrame] for any frame that
// does not carry the magic sentinel or whose declared path length does not
// match the remaining payload.
func (x *terminatedSerializer) Deserialize(data []byte) (any, error) {
	if len(data) < terminatedMinFrameSize || [terminatedMagicLen]byte(data[:terminatedMagicLen]) != terminatedMagic {
		return nil, errNotTerminatedFrame
	}

	pathLenOffset := terminatedMagicLen
	pathOffset := pathLenOffset + terminatedPathLenSize
	pathLen := binary.BigEndian.Uint32(data[pathLenOffset:pathOffset])

	if uint64(pathOffset)+uint64(pathLen)+terminatedTimestampSize != uint64(len(data)) {
		return nil, errNotTerminatedFrame
	}

	pathBytes := data[pathOffset : pathOffset+int(pathLen)]
	timestampOffset := pathOffset + int(pathLen)
	timestampNanos := int64(binary.BigEndian.Uint64(data[timestampOffset:]))

	var actorPath Path
	if len(pathBytes) > 0 {
		addr, err := address.Parse(string(pathBytes))
		if err != nil {
			return nil, errNotTerminatedFrame
		}
		actorPath = newPath(addr)
	}

	return &Terminated{
		actorPath:    actorPath,
		terminatedAt: time.Unix(0, timestampNanos).UTC(),
	}, nil
}
