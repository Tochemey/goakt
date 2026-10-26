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

package net

import (
	"encoding/binary"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTellBatch(t *testing.T) {
	const (
		sender   = "goakt://client@127.0.0.1:1/user/sender"
		receiver = "goakt://server@127.0.0.1:2/user/receiver"
	)

	t.Run("round trips table refs, inline literals and metadata", func(t *testing.T) {
		conn := tellBatchConn(t, sender, receiver)
		body := encodeTellBatch(
			TellBatchMessage{SenderID: 1, Sender: sender, ReceiverID: 2, Receiver: receiver, Payload: []byte("one")},
			TellBatchMessage{Sender: "goakt://other@h:3/user/a", Receiver: "goakt://server@h:4/user/b", Metadata: map[string]string{"trace": "abc", "k": ""}, Payload: []byte("two")},
			TellBatchMessage{ReceiverID: 2, Receiver: receiver},
		)

		batch, err := conn.decodeTellBatch(body)
		require.NoError(t, err)
		require.Equal(t, 3, batch.Len())

		got := readAll(batch)
		require.Len(t, got, 3)

		assert.Equal(t, sender, got[0].Sender)
		assert.Equal(t, "handle:"+sender, got[0].SenderHandle, "a sender table hit fills the handle")
		assert.Equal(t, receiver, got[0].Receiver)
		assert.Nil(t, got[0].Metadata)
		assert.Equal(t, []byte("one"), got[0].Payload)

		assert.Equal(t, "goakt://other@h:3/user/a", got[1].Sender)
		assert.Nil(t, got[1].SenderHandle, "an inline sender has no cached handle")
		assert.Equal(t, "goakt://server@h:4/user/b", got[1].Receiver)
		assert.Equal(t, map[string]string{"trace": "abc", "k": ""}, got[1].Metadata)
		assert.Equal(t, []byte("two"), got[1].Payload)

		assert.Empty(t, got[2].Sender)
		assert.Equal(t, receiver, got[2].Receiver)
		assert.Empty(t, got[2].Payload)

		var msg TellBatchMessage
		assert.False(t, batch.Next(&msg), "an exhausted batch reports no more messages")
	})

	t.Run("a message at the metadata limit", func(t *testing.T) {
		metadata := make(map[string]string, maxTellBatchMetadataEntries)
		for i := range maxTellBatchMetadataEntries {
			metadata[strconv.Itoa(i)] = ""
		}

		batch, err := tellBatchConn(t).decodeTellBatch(encodeTellBatch(TellBatchMessage{Receiver: receiver, Metadata: metadata}))
		require.NoError(t, err)

		got := readAll(batch)
		require.Len(t, got, 1)
		assert.Len(t, got[0].Metadata, maxTellBatchMetadataEntries)
	})

	t.Run("an empty batch", func(t *testing.T) {
		batch, err := tellBatchConn(t).decodeTellBatch(encodeTellBatch())
		require.NoError(t, err)
		assert.Zero(t, batch.Len())
		assert.Empty(t, readAll(batch))
	})

	t.Run("a table ref the connection never installed", func(t *testing.T) {
		body := encodeTellBatch(TellBatchMessage{ReceiverID: 9, Receiver: receiver})

		_, err := tellBatchConn(t, sender).decodeTellBatch(body)
		require.ErrorIs(t, err, ErrUnknownTableRef)
	})

	t.Run("a table ref on a connection without tables", func(t *testing.T) {
		body := encodeTellBatch(TellBatchMessage{SenderID: 1, Sender: sender})

		_, err := (&duplexConn{}).decodeTellBatch(body)
		require.ErrorIs(t, err, ErrTableRefUnsupported)
	})

	t.Run("malformed bodies", func(t *testing.T) {
		valid := encodeTellBatch(TellBatchMessage{Sender: sender, Receiver: receiver, Metadata: map[string]string{"k": "v"}, Payload: []byte("payload")})

		moreThanSent := append([]byte(nil), valid...)
		binary.BigEndian.PutUint32(moreThanSent, 2)

		hostileMetadata := encodeTellBatch()
		binary.BigEndian.PutUint32(hostileMetadata, 1)
		hostileMetadata = append(hostileMetadata, 0, 0, 0, 0)
		hostileMetadata = binary.AppendUvarint(hostileMetadata, 1<<40)

		// A message of empty metadata pairs: every pair is present, so only
		// the count limit stops it.
		emptyPairs := func(entries uint64) []byte {
			body := binary.BigEndian.AppendUint32(nil, 1)
			body = append(body, 0, 0, 0, 0)
			body = binary.AppendUvarint(body, entries)
			body = append(body, make([]byte, 2*entries)...)
			return append(body, 0)
		}

		cases := map[string][]byte{
			"metadata count over the limit": emptyPairs(maxTellBatchMetadataEntries + 1),
			"metadata count that overflows": emptyPairs(1 << 63),
			"missing count":                 {0, 0, 1},
			"truncated sender":              {0, 0, 0, 1},
			"truncated receiver":            {0, 0, 0, 1, 0, 0},
			"truncated metadata count":      {0, 0, 0, 1, 0, 0, 0, 0},
			"count beyond the messages":     moreThanSent,
			"trailing bytes":                append(append([]byte(nil), valid...), 0),
			"truncated payload":             valid[:len(valid)-1],
			"hostile metadata count":        hostileMetadata,
		}

		for name, body := range cases {
			t.Run(name, func(t *testing.T) {
				_, err := tellBatchConn(t).decodeTellBatch(body)
				require.Error(t, err)
				assert.NotErrorIs(t, err, ErrUnknownTableRef)
				assert.NotErrorIs(t, err, ErrTableRefUnsupported)
			})
		}
	})

	t.Run("read rejects a malformed message", func(t *testing.T) {
		valid := AppendTellBatchMessage(nil, &TellBatchMessage{Metadata: map[string]string{"k": "v"}, Payload: []byte("p")})

		cases := map[string][]byte{
			"truncated sender":              {},
			"unknown receiver ref":          {0, 0, 9},
			"truncated metadata count":      {0, 0, 0, 0},
			"hostile metadata count":        binary.AppendUvarint([]byte{0, 0, 0, 0}, 1<<40),
			"metadata count over the limit": append(binary.AppendUvarint([]byte{0, 0, 0, 0}, maxTellBatchMetadataEntries+1), make([]byte, 2*maxTellBatchMetadataEntries+3)...),
			"truncated metadata key":        {0, 0, 0, 0, 1, 5},
			"truncated metadata value":      {0, 0, 0, 0, 1, 1, 'k', 5},
			"truncated payload length":      {0, 0, 0, 0, 0},
			"truncated payload":             valid[:len(valid)-1],
		}

		for name, src := range cases {
			t.Run(name, func(t *testing.T) {
				var msg TellBatchMessage
				_, err := readTellBatchMessage(src, newReceiverTable(1), nil, &msg)
				require.Error(t, err)
			})
		}
	})

	t.Run("next stops on a body that does not parse", func(t *testing.T) {
		batch := &TellBatch{body: []byte{0}, count: 1}

		var msg TellBatchMessage
		assert.False(t, batch.Next(&msg))
		assert.False(t, batch.Next(&msg), "a failed read ends the batch")
	})
}
