# Profiling Guide for GoAkt Benchmarks

This guide explains how to use `go tool pprof` to profile shutdown, rebalancing, and query operations in GoAkt.

## Prerequisites

- Go toolchain installed
- `go tool pprof` available (comes with Go)

## Profiling Shutdown Operations

### Using the Profiling Helper

The `bench` package provides helper functions for profiling. To profile shutdown operations:

1. Set the environment variable:

   ```bash
   export GOAKT_PROFILE_SHUTDOWN=1
   ```

2. Run the shutdown benchmark:

   ```bash
   go test -bench=BenchmarkGCAllocationsDuringShutdown -run=^$ ./bench
   ```

3. This will generate:
   - `shutdown_cpu.prof` - CPU profile
   - `shutdown_mem.prof` - Memory profile

### Analyzing Profiles

#### CPU Profile

```bash
go tool pprof shutdown_cpu.prof
```

In the pprof interactive mode:

- `top` - Show top functions by CPU time
- `top10` - Show top 10 functions
- `list <function>` - Show annotated source code
- `web` - Generate SVG visualization (requires Graphviz)
- `png` - Generate PNG visualization

#### Memory Profile

```bash
go tool pprof shutdown_mem.prof
```

In the pprof interactive mode:

- `top` - Show top allocations
- `top10` - Show top 10 allocations
- `list <function>` - Show annotated source code
- `web` - Generate SVG visualization

## Profiling Rebalancing Operations

1. Set the environment variable:

   ```bash
   export GOAKT_PROFILE_REBALANCING=1
   ```

2. Run the rebalancing benchmark:

   ```bash
   go test -bench=BenchmarkMemoryFootprintDuringRebalancing -run=^$ ./bench
   ```

3. Analyze the generated profiles as described above.

## Profiling Query Operations

1. Set the environment variable:

   ```bash
   export GOAKT_PROFILE_QUERY=1
   ```

2. Run query-related benchmarks or tests.

3. Analyze the generated profiles as described above.

## Example: Analyzing Memory Leaks

To identify memory leaks during shutdown:

```bash
# Generate profiles
export GOAKT_PROFILE_SHUTDOWN=1
go test -bench=BenchmarkGCAllocationsDuringShutdown_1000Actors -run=^$ ./bench

# Analyze memory profile
go tool pprof shutdown_mem.prof

# In pprof:
(pprof) top10
(pprof) list <function-name>
(pprof) web  # Generate visualization
```

## Example: CPU Bottleneck Analysis

To find CPU bottlenecks during rebalancing:

```bash
# Generate profiles
export GOAKT_PROFILE_REBALANCING=1
go test -bench=BenchmarkMemoryFootprintDuringRebalancing -run=^$ ./bench

# Analyze CPU profile
go tool pprof rebalancing_cpu.prof

# In pprof:
(pprof) top10
(pprof) list <function-name>
(pprof) png > cpu_analysis.png
```

## Tips

1. **Compare Before/After**: Generate profiles before and after optimizations to measure improvements.

2. **Focus on Hot Paths**: Use `top` to identify functions consuming the most CPU/memory.

3. **Source Code Analysis**: Use `list <function>` to see exactly where allocations/CPU time is spent.

4. **Visualization**: Use `web` or `png` to generate visual representations of the call graph.

5. **Multiple Runs**: Run benchmarks multiple times to get consistent results.

## Success Criteria

Based on `PHASE_4_PLAN.md`:

- **GC Allocations**: < 1MB during shutdown for typical scenarios
- **Memory Leaks**: No consistent growth in heap size over multiple operations
- **CPU Usage**: Shutdown should complete in < 100ms for 1,000 actors

## Troubleshooting

### Profile files not generated

- Ensure environment variables are set correctly
- Check file permissions in the current directory
- Verify the benchmark actually runs (not skipped)

### pprof command not found

- Ensure Go is installed and `$GOPATH/bin` or `$HOME/go/bin` is in your PATH
- Or use `go tool pprof` directly

### Visualization not working

- Install Graphviz: `brew install graphviz` (macOS) or `apt-get install graphviz` (Linux)
- Or use `png` instead of `web` for PNG output
