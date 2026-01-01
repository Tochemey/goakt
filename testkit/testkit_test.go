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
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

func newTestKit(t *testing.T) (*TestKit, context.Context) {
	ctx := context.Background()
	kit := New(ctx, t)
	t.Cleanup(func() {
		if kit.started.Load() {
			kit.Shutdown(ctx)
		}
	})
	return kit, ctx
}

func TestTestKit(t *testing.T) {
	t.Run("ActorSystem", func(t *testing.T) {
		kit, _ := newTestKit(t)

		sys := kit.ActorSystem()
		require.NotNil(t, sys)
		require.True(t, sys.Running())
	})

	t.Run("Spawn", func(t *testing.T) {
		kit, ctx := newTestKit(t)

		kit.Spawn(ctx, "spawn-actor", &pinger{})
		pid, err := kit.ActorSystem().LocalActor("spawn-actor")
		require.NoError(t, err)
		require.NotNil(t, pid)
	})

	t.Run("SpawnChild", func(t *testing.T) {
		kit, ctx := newTestKit(t)

		kit.Spawn(ctx, "parent-actor", &pinger{})
		kit.SpawnChild(ctx, "child-actor", "parent-actor", &pinger{})

		pid, err := kit.ActorSystem().LocalActor("child-actor")
		require.NoError(t, err)
		require.NotNil(t, pid)
	})

	t.Run("NewProbe", func(t *testing.T) {
		kit, ctx := newTestKit(t)

		kit.Spawn(ctx, "probe-target", &pinger{})
		probe := kit.NewProbe(ctx)
		probe.Send("probe-target", new(testpb.TestPing))
		probe.ExpectMessage(new(testpb.TestPong))
		probe.ExpectNoMessage()
		probe.Stop()
	})

	t.Run("NewGrainProbe", func(t *testing.T) {
		kit, ctx := newTestKit(t)

		identity := kit.GrainIdentity(ctx, "probe-grain", func(ctx context.Context) (actor.Grain, error) {
			return &grain{}, nil
		})

		probe := kit.NewGrainProbe(ctx)
		probe.SendSync(identity, new(testpb.TestReply), time.Second)
		probe.ExpectResponse(new(testpb.Reply))
		probe.ExpectNoResponse()
	})

	t.Run("Subscribe", func(t *testing.T) {
		kit, _ := newTestKit(t)

		subscriber := kit.Subscribe()
		require.NotNil(t, subscriber)
	})

	t.Run("Kill", func(t *testing.T) {
		kit, ctx := newTestKit(t)

		kit.Spawn(ctx, "kill-actor", &pinger{})
		kit.Kill(ctx, "kill-actor")

		require.Eventually(t, func() bool {
			_, err := kit.ActorSystem().LocalActor("kill-actor")
			return err != nil
		}, 2*time.Second, 10*time.Millisecond)
	})

	t.Run("GrainIdentity", func(t *testing.T) {
		kit, ctx := newTestKit(t)

		identity := kit.GrainIdentity(ctx, "grain-id", func(ctx context.Context) (actor.Grain, error) {
			return &grain{}, nil
		})
		require.NotNil(t, identity)
		require.Equal(t, "grain-id", identity.Name())
		require.NoError(t, identity.Validate())
	})

	t.Run("Shutdown", func(t *testing.T) {
		kit, ctx := newTestKit(t)

		kit.Shutdown(ctx)
		require.False(t, kit.started.Load())
		require.False(t, kit.ActorSystem().Running())
	})
}
