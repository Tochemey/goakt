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
	"testing"

	"github.com/stretchr/testify/require"
)

// encodeTellBatch builds a batch body from msgs the way the client does:
// a reserved count, the messages, then the count written in place.
func encodeTellBatch(msgs ...TellBatchMessage) []byte {
	body := make([]byte, TellBatchCountSize)
	for i := range msgs {
		body = AppendTellBatchMessage(body, &msgs[i])
	}

	PutTellBatchCount(body, uint32(len(msgs)))
	return body
}

// tellBatchConn returns a duplexConn carrying only what decodeTellBatch
// reads: the inbound path table, holding literals under IDs 1, 2, ..., and
// a sender resolver that wraps the literal.
func tellBatchConn(t *testing.T, literals ...string) *duplexConn {
	t.Helper()

	paths := newReceiverTable(8)
	for i, literal := range literals {
		require.Empty(t, paths.install(uint64(i+1), literal).HardError)
	}

	return &duplexConn{
		pathReceiver:   paths,
		senderResolver: func(path string) any { return "handle:" + path },
	}
}

// readAll drains batch into a slice.
func readAll(batch *TellBatch) []TellBatchMessage {
	var out []TellBatchMessage

	var msg TellBatchMessage
	for batch.Next(&msg) {
		out = append(out, msg)
	}

	return out
}
