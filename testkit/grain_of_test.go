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

package testkit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v4/actor"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/test/data/testpb"
)

func TestGrainOf(t *testing.T) {
	// create a test context
	ctx := context.TODO()
	// create a test kit
	testkit := New(ctx, t, WithLogging(log.ErrorLevel))

	// get the grain identity without a factory
	id := GrainOf[*grain](ctx, testkit, "grainof-kit")
	require.NotNil(t, id)

	// create the test probe
	probe := testkit.NewGrainProbe(ctx)
	// send a message to the grain to be tested
	probe.SendSync(id, new(testpb.TestReply), time.Second)

	// here we expect a response from the grain
	probe.ExpectResponse(new(testpb.Reply))
	probe.ExpectNoResponse()

	testkit.Shutdown(ctx)
}

func TestNodeGrainOf(t *testing.T) {
	ctx := context.Background()
	multi := NewMultiNodes(t, log.DiscardLogger, []actor.Actor{&pinger{}}, nil)
	multi.Start()
	t.Cleanup(multi.Stop)

	node := multi.StartNode(ctx, "node-grainof")

	// get the grain identity without a factory
	target := NodeGrainOf[*grain](ctx, node, "grainof-node")
	require.NotNil(t, target)

	probe := node.SpawnGrainProbe(ctx)
	// send a message to the grain to be tested
	probe.Send(target, new(testpb.TestPing))

	// here we expect no response from the grain
	probe.ExpectNoResponse()
}
