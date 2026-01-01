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

	"github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

func TestTestNode(t *testing.T) {
	ctx := context.Background()
	multi := NewMultiNodes(t, log.DiscardLogger, []actor.Actor{&pinger{}}, nil)
	multi.Start()
	t.Cleanup(multi.Stop)

	node := multi.StartNode(ctx, "node-1")

	t.Run("NodeName", func(t *testing.T) {
		require.Equal(t, "node-1", node.NodeName())
	})

	t.Run("Spawn", func(t *testing.T) {
		name := "spawn-actor"
		node.Spawn(ctx, name, &pinger{})

		require.Eventually(t, func() bool {
			pid, err := node.actorSystem.LocalActor(name)
			return err == nil && pid != nil
		}, time.Second, 10*time.Millisecond)
	})

	t.Run("SpawnSingleton", func(t *testing.T) {
		name := "singleton-actor"
		node.SpawnSingleton(ctx, name, &pinger{})

		require.Eventually(t, func() bool {
			pid, err := node.actorSystem.LocalActor(name)
			return err == nil && pid != nil
		}, 2*time.Second, 10*time.Millisecond)
	})

	t.Run("SpawnProbe", func(t *testing.T) {
		target := "probe-target"
		node.Spawn(ctx, target, &pinger{})

		probe := node.SpawnProbe(ctx)
		probe.Send(target, new(testpb.TestPing))
		probe.ExpectMessage(new(testpb.TestPong))
		probe.ExpectNoMessage()
		probe.Stop()
	})

	t.Run("SpawnGrainProbe", func(t *testing.T) {
		target := node.GrainIdentity(ctx, "grain-probe-target", func(ctx context.Context) (actor.Grain, error) {
			return &grain{}, nil
		})

		probe := node.SpawnGrainProbe(ctx)
		// send a message to the grain to be tested
		probe.Send(target, new(testpb.TestPing))

		// here we expect no response from the grain
		probe.ExpectNoResponse()
	})

	t.Run("Subscribe", func(t *testing.T) {
		subscriber := node.Subscribe()
		require.NotNil(t, subscriber)
	})

	t.Run("Kill", func(t *testing.T) {
		name := "kill-actor"
		node.Spawn(ctx, name, &pinger{})
		node.Kill(ctx, name)

		require.Eventually(t, func() bool {
			_, err := node.actorSystem.LocalActor(name)
			return err != nil
		}, 2*time.Second, 10*time.Millisecond)
	})
}
