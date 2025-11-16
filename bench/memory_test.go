package bench

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tochemey/goakt/v3/actor"
	"github.com/tochemey/goakt/v3/log"
	"github.com/tochemey/goakt/v3/test/data/testpb"
)

type noopActor struct{}

func (x *noopActor) PreStart(*actor.Context) error { return nil }
func (x *noopActor) Receive(ctx *actor.ReceiveContext) {
	switch ctx.Message().(type) {
	case *testpb.TestSend:
	default:
		ctx.Unhandled()
	}
}
func (x *noopActor) PostStop(*actor.Context) error { return nil }

func BenchmarkActorMemoryFootprint(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping memory benchmark in short mode")
	}

	ctx := context.Background()
	system, err := actor.NewActorSystem("mem-bench",
		actor.WithLogger(log.DiscardLogger),
		actor.WithActorInitMaxRetries(1))
	require.NoError(b, err)
	require.NoError(b, system.Start(ctx))
	defer func() { _ = system.Stop(ctx) }()

	const actorCount = 2_000

	pids := make([]*actor.PID, 0, actorCount)
	for i := range actorCount {
		pid, err := system.Spawn(ctx, benchActorName(i), new(noopActor))
		require.NoError(b, err)
		pids = append(pids, pid)
	}

	runtime.GC()

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	bytesPerActor := float64(mem.Alloc) / float64(actorCount)
	b.ReportMetric(bytesPerActor, "bytes/actor")

	fmt.Printf("\nActors=%d Alloc=%s bytes/actor=%.0f (~%.2f KB)\n", actorCount, humanReadableBytes(mem.Alloc), bytesPerActor, bytesPerActor/1024)

	for _, pid := range pids {
		_ = pid.Shutdown(ctx)
	}
}

func benchActorName(i int) string {
	return "bench-noop-" + strconv.Itoa(i)
}

func humanReadableBytes(b uint64) string {
	const (
		kilobyte = 1024
		megabyte = 1024 * kilobyte
		gigabyte = 1024 * megabyte
	)

	switch {
	case b >= gigabyte:
		return fmt.Sprintf("%.2f GB", float64(b)/float64(gigabyte))
	case b >= megabyte:
		return fmt.Sprintf("%.2f MB", float64(b)/float64(megabyte))
	case b >= kilobyte:
		return fmt.Sprintf("%.2f KB", float64(b)/float64(kilobyte))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
