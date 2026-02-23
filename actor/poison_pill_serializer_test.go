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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPoisonPillSerializer_Serialize(t *testing.T) {
	s := new(poisonPillSerializer)

	t.Run("PoisonPill produces the 8-byte magic sentinel", func(t *testing.T) {
		data, err := s.Serialize(new(PoisonPill))
		require.NoError(t, err)
		require.Len(t, data, 8)
		require.Equal(t, poisonPillMagic[:], data)
	})

	t.Run("non-PoisonPill message returns errNotPoisonPillFrame", func(t *testing.T) {
		data, err := s.Serialize(new(PostStart))
		require.ErrorIs(t, err, errNotPoisonPillFrame)
		require.Nil(t, data)
	})

	t.Run("nil message returns errNotPoisonPillFrame", func(t *testing.T) {
		data, err := s.Serialize(nil)
		require.ErrorIs(t, err, errNotPoisonPillFrame)
		require.Nil(t, data)
	})
}

func TestPoisonPillSerializer_Deserialize(t *testing.T) {
	s := new(poisonPillSerializer)

	t.Run("exact magic sentinel produces *PoisonPill", func(t *testing.T) {
		msg, err := s.Deserialize(poisonPillMagic[:])
		require.NoError(t, err)
		require.IsType(t, new(PoisonPill), msg)
	})

	t.Run("empty slice returns errNotPoisonPillFrame", func(t *testing.T) {
		msg, err := s.Deserialize([]byte{})
		require.ErrorIs(t, err, errNotPoisonPillFrame)
		require.Nil(t, msg)
	})

	t.Run("slice shorter than 8 bytes returns errNotPoisonPillFrame", func(t *testing.T) {
		msg, err := s.Deserialize(poisonPillMagic[:4])
		require.ErrorIs(t, err, errNotPoisonPillFrame)
		require.Nil(t, msg)
	})

	t.Run("slice longer than 8 bytes returns errNotPoisonPillFrame", func(t *testing.T) {
		data := append(poisonPillMagic[:], 0x00)
		msg, err := s.Deserialize(data)
		require.ErrorIs(t, err, errNotPoisonPillFrame)
		require.Nil(t, msg)
	})

	t.Run("correct length but wrong bytes returns errNotPoisonPillFrame", func(t *testing.T) {
		wrong := make([]byte, 8)
		msg, err := s.Deserialize(wrong)
		require.ErrorIs(t, err, errNotPoisonPillFrame)
		require.Nil(t, msg)
	})

	t.Run("single byte flip returns errNotPoisonPillFrame", func(t *testing.T) {
		corrupted := make([]byte, 8)
		copy(corrupted, poisonPillMagic[:])
		corrupted[3] ^= 0xFF
		msg, err := s.Deserialize(corrupted)
		require.ErrorIs(t, err, errNotPoisonPillFrame)
		require.Nil(t, msg)
	})
}

func TestPoisonPillSerializer_RoundTrip(t *testing.T) {
	s := new(poisonPillSerializer)

	data, err := s.Serialize(new(PoisonPill))
	require.NoError(t, err)

	msg, err := s.Deserialize(data)
	require.NoError(t, err)
	require.IsType(t, new(PoisonPill), msg)
}

func TestPoisonPillSerializer_SerializeIsIdempotent(t *testing.T) {
	s := new(poisonPillSerializer)

	first, err := s.Serialize(new(PoisonPill))
	require.NoError(t, err)

	second, err := s.Serialize(new(PoisonPill))
	require.NoError(t, err)

	require.Equal(t, first, second)
}

func TestPoisonPillSerializer_SerializeOutputIsIndependent(t *testing.T) {
	s := new(poisonPillSerializer)

	first, err := s.Serialize(new(PoisonPill))
	require.NoError(t, err)

	// mutating the first output must not affect a subsequent serialization
	first[0] ^= 0xFF

	second, err := s.Serialize(new(PoisonPill))
	require.NoError(t, err)
	require.Equal(t, poisonPillMagic[:], second)
}
