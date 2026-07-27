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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/internal/commands"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestGrainReplyNilReceiver(t *testing.T) {
	var reply *GrainReply

	require.NotPanics(t, func() {
		reply.Response(new(testpb.TestReply))
		reply.Err(errors.New("ignored"))
		reply.NoErr()
	})
}

func TestGrainReplyCompletesOnce(t *testing.T) {
	system := newRequestTestSystem(t)

	slot := system.pendingAsks.Register("corr")
	reply := &GrainReply{system: system, correlationID: "corr"}

	reply.Response(&testpb.Reply{Content: "first"})
	reply.Response(&testpb.Reply{Content: "second"})
	reply.Err(errors.New("late failure"))
	reply.NoErr()

	response := <-slot
	payload, ok := response.Message.(*testpb.Reply)
	require.True(t, ok)
	require.Equal(t, "first", payload.GetContent())
	require.Zero(t, system.pendingAsks.Len())
}

func TestGrainReplyErrAndNoErr(t *testing.T) {
	system := newRequestTestSystem(t)

	slot := system.pendingAsks.Register("failing")
	failing := &GrainReply{system: system, correlationID: "failing"}
	failing.Err(errors.New("boom"))

	response := <-slot
	require.Equal(t, "boom", response.Error)
	require.Nil(t, response.Message)

	slot = system.pendingAsks.Register("empty")
	empty := &GrainReply{system: system, correlationID: "empty"}
	empty.NoErr()

	response = <-slot
	require.Empty(t, response.Error)
	require.Nil(t, response.Message)
}

func TestGrainReplyDeliveryFailureIsSwallowed(t *testing.T) {
	system := newRequestTestSystem(t)

	// A malformed grain target cannot be routed; the failure is logged at
	// debug and never escapes the handle.
	reply := &GrainReply{
		system:        system,
		replyTo:       &commands.AsyncReplyTo{Kind: commands.ReplyToGrain, Grain: "no-separator"},
		correlationID: "corr",
	}

	require.NotPanics(t, func() {
		reply.Response(new(testpb.TestReply))
	})
}
