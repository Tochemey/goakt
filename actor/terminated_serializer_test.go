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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/address"
)

func TestTerminatedSerializer_Serialize(t *testing.T) {
	s := new(terminatedSerializer)
	addr := address.New("a", "TestSys", "h1", 1)
	stamp := time.Unix(1700000000, 12345).UTC()

	t.Run("Terminated produces magic + length + path + timestamp", func(t *testing.T) {
		msg := &Terminated{actorPath: newPath(addr), terminatedAt: stamp}

		data, err := s.Serialize(msg)
		require.NoError(t, err)

		pathStr := addr.String()
		expectedLen := terminatedMagicLen + terminatedPathLenSize + len(pathStr) + terminatedTimestampSize
		require.Len(t, data, expectedLen)
		require.Equal(t, terminatedMagic[:], data[:terminatedMagicLen])

		gotPathLen := binary.BigEndian.Uint32(data[terminatedMagicLen : terminatedMagicLen+terminatedPathLenSize])
		require.Equal(t, uint32(len(pathStr)), gotPathLen)

		pathStart := terminatedMagicLen + terminatedPathLenSize
		require.Equal(t, pathStr, string(data[pathStart:pathStart+len(pathStr)]))

		gotNanos := int64(binary.BigEndian.Uint64(data[pathStart+len(pathStr):]))
		require.Equal(t, stamp.UnixNano(), gotNanos)
	})

	t.Run("nil actorPath emits zero-length path", func(t *testing.T) {
		msg := &Terminated{terminatedAt: stamp}

		data, err := s.Serialize(msg)
		require.NoError(t, err)
		require.Len(t, data, terminatedMinFrameSize)

		gotPathLen := binary.BigEndian.Uint32(data[terminatedMagicLen : terminatedMagicLen+terminatedPathLenSize])
		require.Zero(t, gotPathLen)
	})

	t.Run("non-Terminated message returns errNotTerminatedFrame", func(t *testing.T) {
		data, err := s.Serialize(new(PoisonPill))
		require.ErrorIs(t, err, errNotTerminatedFrame)
		require.Nil(t, data)
	})

	t.Run("nil interface returns errNotTerminatedFrame", func(t *testing.T) {
		data, err := s.Serialize(nil)
		require.ErrorIs(t, err, errNotTerminatedFrame)
		require.Nil(t, data)
	})

	t.Run("typed-nil *Terminated returns errNotTerminatedFrame", func(t *testing.T) {
		var msg *Terminated
		data, err := s.Serialize(msg)
		require.ErrorIs(t, err, errNotTerminatedFrame)
		require.Nil(t, data)
	})
}

func TestTerminatedSerializer_Deserialize(t *testing.T) {
	s := new(terminatedSerializer)
	addr := address.New("a", "TestSys", "h1", 1)
	stamp := time.Unix(1700000000, 12345).UTC()

	t.Run("frame produced by Serialize round-trips", func(t *testing.T) {
		original := &Terminated{actorPath: newPath(addr), terminatedAt: stamp}

		data, err := s.Serialize(original)
		require.NoError(t, err)

		decoded, err := s.Deserialize(data)
		require.NoError(t, err)

		got, ok := decoded.(*Terminated)
		require.True(t, ok)
		require.NotNil(t, got.actorPath)
		require.Equal(t, addr.String(), got.actorPath.String())
		require.True(t, got.terminatedAt.Equal(stamp))
	})

	t.Run("zero-length path round-trips with nil actorPath", func(t *testing.T) {
		original := &Terminated{terminatedAt: stamp}

		data, err := s.Serialize(original)
		require.NoError(t, err)

		decoded, err := s.Deserialize(data)
		require.NoError(t, err)

		got := decoded.(*Terminated)
		require.Nil(t, got.actorPath)
		require.True(t, got.terminatedAt.Equal(stamp))
	})

	t.Run("empty slice returns errNotTerminatedFrame", func(t *testing.T) {
		msg, err := s.Deserialize(nil)
		require.ErrorIs(t, err, errNotTerminatedFrame)
		require.Nil(t, msg)
	})

	t.Run("slice shorter than the minimum frame returns errNotTerminatedFrame", func(t *testing.T) {
		short := make([]byte, terminatedMinFrameSize-1)
		copy(short, terminatedMagic[:])
		msg, err := s.Deserialize(short)
		require.ErrorIs(t, err, errNotTerminatedFrame)
		require.Nil(t, msg)
	})

	t.Run("wrong magic returns errNotTerminatedFrame", func(t *testing.T) {
		data := make([]byte, terminatedMinFrameSize)
		msg, err := s.Deserialize(data)
		require.ErrorIs(t, err, errNotTerminatedFrame)
		require.Nil(t, msg)
	})

	t.Run("length mismatch returns errNotTerminatedFrame", func(t *testing.T) {
		// Magic + pathLen claiming N bytes + only zero bytes of path + timestamp.
		data := make([]byte, terminatedMagicLen+terminatedPathLenSize+terminatedTimestampSize+4)
		copy(data, terminatedMagic[:])
		binary.BigEndian.PutUint32(data[terminatedMagicLen:], 8) // claim 8 bytes but only 4 follow before timestamp

		msg, err := s.Deserialize(data)
		require.ErrorIs(t, err, errNotTerminatedFrame)
		require.Nil(t, msg)
	})

	t.Run("malformed path string returns errNotTerminatedFrame", func(t *testing.T) {
		bogus := "not-a-valid-address"
		data := make([]byte, terminatedMagicLen+terminatedPathLenSize+len(bogus)+terminatedTimestampSize)
		copy(data, terminatedMagic[:])
		binary.BigEndian.PutUint32(data[terminatedMagicLen:], uint32(len(bogus)))
		copy(data[terminatedMagicLen+terminatedPathLenSize:], bogus)
		binary.BigEndian.PutUint64(data[len(data)-terminatedTimestampSize:], uint64(stamp.UnixNano()))

		msg, err := s.Deserialize(data)
		require.ErrorIs(t, err, errNotTerminatedFrame)
		require.Nil(t, msg)
	})
}

func TestTerminatedSerializer_RoundTripIdempotent(t *testing.T) {
	s := new(terminatedSerializer)
	addr := address.New("a", "TestSys", "h1", 1)
	stamp := time.Unix(1700000000, 12345).UTC()
	msg := &Terminated{actorPath: newPath(addr), terminatedAt: stamp}

	first, err := s.Serialize(msg)
	require.NoError(t, err)

	second, err := s.Serialize(msg)
	require.NoError(t, err)

	require.Equal(t, first, second)
}
