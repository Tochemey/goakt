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

package bench

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"time"

	"github.com/tochemey/goakt/v3/actor"
)

// ProfileShutdown profiles memory and CPU during shutdown
// Usage: Set GOAKT_PROFILE_SHUTDOWN=1 and run benchmarks
func ProfileShutdown(ctx context.Context, system actor.ActorSystem, profileName string) error {
	if os.Getenv("GOAKT_PROFILE_SHUTDOWN") == "" {
		return nil // Profiling disabled
	}

	// Create CPU profile
	cpuFile, err := os.Create(fmt.Sprintf("%s_cpu.prof", profileName))
	if err != nil {
		return fmt.Errorf("failed to create CPU profile: %w", err)
	}
	defer cpuFile.Close()

	if err := pprof.StartCPUProfile(cpuFile); err != nil {
		return fmt.Errorf("failed to start CPU profile: %w", err)
	}
	defer pprof.StopCPUProfile()

	// Create memory profile
	memFile, err := os.Create(fmt.Sprintf("%s_mem.prof", profileName))
	if err != nil {
		return fmt.Errorf("failed to create memory profile: %w", err)
	}
	defer memFile.Close()

	// Perform shutdown
	start := time.Now()
	err = system.Stop(ctx)
	shutdownDuration := time.Since(start)

	if err != nil {
		return fmt.Errorf("shutdown failed: %w", err)
	}

	// Write memory profile
	runtime.GC()
	if err := pprof.WriteHeapProfile(memFile); err != nil {
		return fmt.Errorf("failed to write memory profile: %w", err)
	}

	fmt.Printf("Shutdown profiled: duration=%v, profiles saved to %s_cpu.prof and %s_mem.prof\n",
		shutdownDuration, profileName, profileName)

	return nil
}

// ProfileRebalancing profiles memory and CPU during rebalancing
// Usage: Set GOAKT_PROFILE_REBALANCING=1 and run benchmarks
func ProfileRebalancing(profileName string, rebalancingFunc func() error) error {
	if os.Getenv("GOAKT_PROFILE_REBALANCING") == "" {
		return rebalancingFunc() // Profiling disabled, just run the function
	}

	// Create CPU profile
	cpuFile, err := os.Create(fmt.Sprintf("%s_cpu.prof", profileName))
	if err != nil {
		return fmt.Errorf("failed to create CPU profile: %w", err)
	}
	defer cpuFile.Close()

	if err := pprof.StartCPUProfile(cpuFile); err != nil {
		return fmt.Errorf("failed to start CPU profile: %w", err)
	}
	defer pprof.StopCPUProfile()

	// Create memory profile
	memFile, err := os.Create(fmt.Sprintf("%s_mem.prof", profileName))
	if err != nil {
		return fmt.Errorf("failed to create memory profile: %w", err)
	}
	defer memFile.Close()

	// Perform rebalancing
	start := time.Now()
	err = rebalancingFunc()
	rebalancingDuration := time.Since(start)

	if err != nil {
		return fmt.Errorf("rebalancing failed: %w", err)
	}

	// Write memory profile
	runtime.GC()
	if err := pprof.WriteHeapProfile(memFile); err != nil {
		return fmt.Errorf("failed to write memory profile: %w", err)
	}

	fmt.Printf("Rebalancing profiled: duration=%v, profiles saved to %s_cpu.prof and %s_mem.prof\n",
		rebalancingDuration, profileName, profileName)

	return nil
}

// ProfileQuery profiles memory and CPU during query operations
// Usage: Set GOAKT_PROFILE_QUERY=1 and run benchmarks
func ProfileQuery(profileName string, queryFunc func() error) error {
	if os.Getenv("GOAKT_PROFILE_QUERY") == "" {
		return queryFunc() // Profiling disabled, just run the function
	}

	// Create CPU profile
	cpuFile, err := os.Create(fmt.Sprintf("%s_cpu.prof", profileName))
	if err != nil {
		return fmt.Errorf("failed to create CPU profile: %w", err)
	}
	defer cpuFile.Close()

	if err := pprof.StartCPUProfile(cpuFile); err != nil {
		return fmt.Errorf("failed to start CPU profile: %w", err)
	}
	defer pprof.StopCPUProfile()

	// Create memory profile
	memFile, err := os.Create(fmt.Sprintf("%s_mem.prof", profileName))
	if err != nil {
		return fmt.Errorf("failed to create memory profile: %w", err)
	}
	defer memFile.Close()

	// Perform query
	start := time.Now()
	err = queryFunc()
	queryDuration := time.Since(start)

	if err != nil {
		return fmt.Errorf("query failed: %w", err)
	}

	// Write memory profile
	runtime.GC()
	if err := pprof.WriteHeapProfile(memFile); err != nil {
		return fmt.Errorf("failed to write memory profile: %w", err)
	}

	fmt.Printf("Query profiled: duration=%v, profiles saved to %s_cpu.prof and %s_mem.prof\n",
		queryDuration, profileName, profileName)

	return nil
}
