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
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/tochemey/goakt/v4/internal/internalpb"
)

// tablesHello returns a HELLO advertising compression tables (revision 3).
func tablesHello() *internalpb.Hello {
	return internalpb.Hello_builder{
		Revision:                    CapabilityRevisionTables,
		MaxFrameSize:                defaultMaxFrameSize,
		MaxMessageSize:              DefaultMaxMessageSize,
		MaxConcurrentLargeTransfers: 4,
	}.Build()
}

// chunkingHello returns a HELLO advertising chunking only (revision 2).
func chunkingHello() *internalpb.Hello {
	return internalpb.Hello_builder{
		Revision:                    CapabilityRevisionChunking,
		MaxFrameSize:                defaultMaxFrameSize,
		MaxMessageSize:              DefaultMaxMessageSize,
		MaxConcurrentLargeTransfers: 4,
	}.Build()
}

func TestPrepareRefEmitsTableBeforeData(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), int64(defaultMaxFrameSize),
		withDuplexNegotiated(tablesHello()),
	)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), int64(defaultMaxFrameSize),
		withDuplexNegotiated(tablesHello()),
	)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sender := "goakt://sys@127.0.0.1:1/user/s"
	receiver := "goakt://sys@127.0.0.1:1/user/r"
	typeName := "test.Type"

	senderID, err := left.PrepareRef(TableKindActorPath, sender)
	require.NoError(t, err)
	require.Equal(t, uint64(1), senderID)

	receiverID, err := left.PrepareRef(TableKindActorPath, receiver)
	require.NoError(t, err)
	require.Equal(t, uint64(2), receiverID)

	typeID, err := left.PrepareRef(TableKindTypeName, typeName)
	require.NoError(t, err)
	require.Equal(t, uint64(1), typeID)

	encoded, err := EncodeDataEnvelopeWithTables(DataEnvelope{
		Sender:       sender,
		Receiver:     receiver,
		TypeName:     typeName,
		SerializerID: SerializerIDInternalProto,
		Payload:      []byte("hi"),
	}, senderID, receiverID, typeID)
	require.NoError(t, err)

	require.NoError(t, left.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Lane:    left.Lane(),
		Payload: encoded,
	}))

	frame, err := right.Recv(ctx)
	require.NoError(t, err)
	require.Equal(t, FrameTypeData, frame.Type)

	env, err := right.DecodeDataEnvelope(frame.Payload, frame.HasMetadata())
	require.NoError(t, err)
	assert.Equal(t, sender, env.Sender)
	assert.Equal(t, receiver, env.Receiver)
	assert.Equal(t, typeName, env.TypeName)
	assert.Equal(t, []byte("hi"), env.Payload)

	// Steady-state envelope is smaller than the three inline string refs.
	inline, err := EncodeDataEnvelope(DataEnvelope{
		Sender:       sender,
		Receiver:     receiver,
		TypeName:     typeName,
		SerializerID: SerializerIDInternalProto,
		Payload:      []byte("hi"),
	})
	require.NoError(t, err)
	assert.Less(t, len(encoded), len(inline))
	assert.Less(t, len(encoded), 32)
}

func TestPrepareRefConcurrentFirstUseSingleTable(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), int64(defaultMaxFrameSize),
		withDuplexNegotiated(tablesHello()),
	)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), int64(defaultMaxFrameSize),
		withDuplexNegotiated(tablesHello()),
	)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	const workers = 16
	literal := "goakt://sys@127.0.0.1:1/user/shared"
	var wg sync.WaitGroup
	ids := make([]uint64, workers)

	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(idx int) {
			defer wg.Done()

			id, err := left.PrepareRef(TableKindActorPath, literal)
			require.NoError(t, err)
			ids[idx] = id
		}(i)
	}

	wg.Wait()

	for _, id := range ids {
		assert.Equal(t, uint64(1), id)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	encoded, err := EncodeDataEnvelopeWithTables(DataEnvelope{
		Sender:       literal,
		Receiver:     literal,
		TypeName:     "t",
		SerializerID: SerializerIDInternalProto,
		Payload:      []byte{1},
	}, 1, 1, 0)
	require.NoError(t, err)
	require.NoError(t, left.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Lane:    left.Lane(),
		Payload: encoded,
	}))

	frame, err := right.Recv(ctx)
	require.NoError(t, err)
	env, err := right.DecodeDataEnvelope(frame.Payload, false)
	require.NoError(t, err)
	assert.Equal(t, literal, env.Sender)
}

func TestRevisionTwoRejectsInboundTable(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), int64(defaultMaxFrameSize),
		withDuplexChunkSize(1024),
		withDuplexNegotiated(tablesHello()),
	)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), int64(defaultMaxFrameSize),
		withDuplexChunkSize(1024),
		withDuplexNegotiated(chunkingHello()),
	)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	_, err := left.PrepareRef(TableKindActorPath, "goakt://sys@127.0.0.1:1/user/a")
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return right.IsClosed()
	}, 2*time.Second, 10*time.Millisecond)
}

func TestPrepareRefNoopBelowRevisionThree(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), int64(defaultMaxFrameSize),
		withDuplexNegotiated(chunkingHello()),
	)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), int64(defaultMaxFrameSize),
		withDuplexNegotiated(chunkingHello()),
	)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	id, err := left.PrepareRef(TableKindActorPath, "goakt://sys@127.0.0.1:1/user/a")
	require.NoError(t, err)
	assert.Equal(t, uint64(0), id)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	encoded, err := EncodeDataEnvelope(DataEnvelope{
		Sender:       "a",
		Receiver:     "b",
		TypeName:     "t",
		SerializerID: SerializerIDInternalProto,
		Payload:      []byte("x"),
	})
	require.NoError(t, err)
	require.NoError(t, left.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Lane:    left.Lane(),
		Payload: encoded,
	}))

	frame, err := right.Recv(ctx)
	require.NoError(t, err)
	assert.Equal(t, FrameTypeData, frame.Type)
}

func TestChunkedSendEmitsTableBeforeFirstChunk(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), int64(defaultMaxFrameSize),
		withDuplexChunkSize(128),
		withDuplexNegotiated(tablesHello()),
	)
	defer func() { _ = left.Close() }()

	// The right side reads raw frames off the wire so the on-wire order is
	// asserted directly instead of inferred from a successful decode.
	capture := newTCPFramedConn(c2, defaultMaxFrameSize)
	require.NoError(t, c2.SetReadDeadline(time.Now().Add(2*time.Second)))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sender := "goakt://sys@127.0.0.1:1/user/s"
	receiver := "goakt://sys@127.0.0.1:1/user/r"
	typeName := "test.Type"

	senderID, err := left.PrepareRef(TableKindActorPath, sender)
	require.NoError(t, err)
	receiverID, err := left.PrepareRef(TableKindActorPath, receiver)
	require.NoError(t, err)
	typeID, err := left.PrepareRef(TableKindTypeName, typeName)
	require.NoError(t, err)

	encoded, err := EncodeDataEnvelopeWithTables(DataEnvelope{
		Sender:       sender,
		Receiver:     receiver,
		TypeName:     typeName,
		SerializerID: SerializerIDInternalProto,
		Payload:      make([]byte, 512),
	}, senderID, receiverID, typeID)
	require.NoError(t, err)

	require.NoError(t, left.Tell(ctx, Frame{
		Type:    FrameTypeData,
		Lane:    left.Lane(),
		Payload: encoded,
	}))

	// Every TABLE must precede the first CHUNK on the wire.
	var kinds []byte

	for {
		frame, readErr := capture.ReadFrame()
		require.NoError(t, readErr)
		kinds = append(kinds, frame.Type)

		if frame.Type == FrameTypeChunk && frame.IsLastChunk() {
			break
		}
	}

	require.GreaterOrEqual(t, len(kinds), 5, "expected 3 TABLE frames plus at least 2 CHUNK frames, got %v", kinds)
	assert.Equal(t, []byte{FrameTypeTable, FrameTypeTable, FrameTypeTable}, kinds[:3])

	for _, kind := range kinds[3:] {
		assert.Equal(t, FrameTypeChunk, kind)
	}
}

func TestSenderHandleLazyResolve(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	var resolves atomic.Int32
	handle := &struct{ name string }{name: "pid"}

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), int64(defaultMaxFrameSize),
		withDuplexNegotiated(tablesHello()),
	)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), int64(defaultMaxFrameSize),
		withDuplexNegotiated(tablesHello()),
		withDuplexSenderResolver(func(path string) any {
			resolves.Add(1)
			return handle
		}),
	)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	sender := "goakt://sys@127.0.0.1:1/user/s"
	senderID, err := left.PrepareRef(TableKindActorPath, sender)
	require.NoError(t, err)

	encoded, err := EncodeDataEnvelopeWithTables(DataEnvelope{
		Sender:       sender,
		Receiver:     "r",
		TypeName:     "t",
		SerializerID: SerializerIDInternalProto,
		Payload:      []byte("1"),
	}, senderID, 0, 0)
	require.NoError(t, err)

	for i := 0; i < 3; i++ {
		require.NoError(t, left.Tell(ctx, Frame{
			Type:    FrameTypeData,
			Lane:    left.Lane(),
			Payload: encoded,
		}))

		frame, recvErr := right.Recv(ctx)
		require.NoError(t, recvErr)

		env, decErr := right.DecodeDataEnvelope(frame.Payload, false)
		require.NoError(t, decErr)
		assert.Same(t, handle, env.SenderHandle)
	}

	assert.Equal(t, int32(1), resolves.Load())
}

func TestEncodeReplyEnvelopeRegistersType(t *testing.T) {
	defer goleak.VerifyNone(t)

	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		_ = c1.Close()
		_ = c2.Close()
	})

	left := newDuplexConn(newTCPFramedConn(c1, defaultMaxFrameSize), int64(defaultMaxFrameSize),
		withDuplexNegotiated(tablesHello()),
	)
	right := newDuplexConn(newTCPFramedConn(c2, defaultMaxFrameSize), int64(defaultMaxFrameSize),
		withDuplexNegotiated(tablesHello()),
	)
	defer func() {
		_ = left.Close()
		_ = right.Close()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	wait := right.pending.register(1)

	payload, err := left.EncodeReplyEnvelope(ReplyEnvelope{
		TypeName:     "reply.Type",
		SerializerID: SerializerIDInternalProto,
		Payload:      []byte("ok"),
	})
	require.NoError(t, err)

	require.NoError(t, left.Submit(ctx, Frame{
		Type:        FrameTypeReply,
		Lane:        left.Lane(),
		Correlation: 1,
		Payload:     payload,
	}))

	select {
	case reply := <-wait:
		putPendingWaiter(wait)
		env, decErr := right.DecodeReplyEnvelope(reply.Payload, reply.HasMetadata())
		require.NoError(t, decErr)
		assert.Equal(t, "reply.Type", env.TypeName)
		assert.Equal(t, []byte("ok"), env.Payload)
	case <-ctx.Done():
		t.Fatal("timed out waiting for REPLY")
	}
}
