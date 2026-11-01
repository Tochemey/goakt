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

// Command table renders saved benchmark results as Markdown tables, one per
// figure: Throughput, Time per operation, Bytes allocated per operation,
// Allocations per operation, Live heap per idle actor and Live heap per idle
// grain.
//
// Each argument is label=path, where path is the output of go test -bench of
// one run of ./benchmark. The first label is the baseline the other columns
// are compared with. -match and -skip keep or drop benchmarks whose name
// matches a regular expression:
//
//	go run ./benchmark/cmd/table -skip '^Grain' "v4.6.6=before.txt" "branch=after.txt"
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// metric is rendered as its own table. A benchmark row uses the first of the
// metric's units it reports. A benchmark that reports an exclusive metric's
// unit appears in that table only: its other figures describe the setup, not
// the quantity measured.
type metric struct {
	units        []string
	title        string
	higherBetter bool
	exclusive    bool
	format       func(value float64, unit string) string
}

// samples holds every value recorded for a benchmark, keyed by run label and
// then by unit.
type samples map[string]map[string][]float64

// results is everything read from the result files: the run labels in
// argument order, the benchmark names in order of first appearance, their
// samples, and the cpu line of the first file that had one.
type results struct {
	labels     []string
	benchmarks []string
	samples    map[string]samples
	cpu        string
}

var metrics = []metric{
	{units: []string{"msgs/s", "actors/s", "grains/s"}, title: "Throughput", higherBetter: true, format: formatRate},
	{units: []string{"ns/op"}, title: "Time per operation", format: formatDuration},
	{units: []string{"B/op"}, title: "Bytes allocated per operation", format: func(v float64, _ string) string { return fmt.Sprintf("%.0f B", v) }},
	{units: []string{"allocs/op"}, title: "Allocations per operation", format: func(v float64, _ string) string { return fmt.Sprintf("%.0f", v) }},
	{units: []string{"B/actor"}, title: "Live heap per idle actor", exclusive: true, format: func(v float64, _ string) string { return fmt.Sprintf("%.0f B", v) }},
	{units: []string{"B/grain"}, title: "Live heap per idle grain", exclusive: true, format: func(v float64, _ string) string { return fmt.Sprintf("%.0f B", v) }},
}

// errUsage reports arguments the command cannot use.
var errUsage = errors.New("usage: table [-match regexp] [-skip regexp] label=path [label=path...]")

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, errUsage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}

// run parses args, reads the result files they name and writes the tables to
// out.
func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("table", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	match := flags.String("match", "", "only render benchmarks whose name matches this regular expression")
	skip := flags.String("skip", "", "leave out benchmarks whose name matches this regular expression")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}

	if flags.NArg() < 1 {
		return errUsage
	}

	keep, err := filter(*match, *skip)
	if err != nil {
		return fmt.Errorf("%w: %w", errUsage, err)
	}

	read := &results{samples: make(map[string]samples)}
	for _, arg := range flags.Args() {
		label, path, ok := strings.Cut(arg, "=")
		if !ok {
			return fmt.Errorf("%w: invalid argument %q: want label=path", errUsage, arg)
		}

		read.labels = append(read.labels, label)
		if err := read.readFile(label, path, keep); err != nil {
			return err
		}
	}

	render(out, read)
	return nil
}

// readFile reads the go test -bench output at path into x under label, keeping
// the benchmarks keep accepts. A result line is a benchmark name, the
// iteration count and value-unit pairs; the cpu line of the output's header
// names the machine. Every other line, such as a failed run, is skipped.
func (x *results) readFile(label, path string, keep func(name string) bool) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if x.cpu == "" {
			if cpu, ok := strings.CutPrefix(line, "cpu:"); ok {
				x.cpu = strings.TrimSpace(cpu)
				continue
			}
		}

		name, values, ok := parseResult(line)
		if !ok || !keep(name) {
			continue
		}

		if _, ok := x.samples[name]; !ok {
			x.samples[name] = make(samples)
			x.benchmarks = append(x.benchmarks, name)
		}

		if _, ok := x.samples[name][label]; !ok {
			x.samples[name][label] = make(map[string][]float64)
		}

		for unit, v := range values {
			x.samples[name][label][unit] = append(x.samples[name][label][unit], v)
		}
	}

	return scanner.Err()
}

// parseResult parses one result line of go test -bench, such as
//
//	BenchmarkTell-8   36158917   74.97 ns/op   13337905 msgs/s   0 B/op   0 allocs/op
//
// into the benchmark name, without the Benchmark prefix as the published
// tables print it, and its values by unit. It reports false for any other
// line.
func parseResult(line string) (string, map[string]float64, bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 || !strings.HasPrefix(fields[0], "Benchmark") || len(fields)%2 != 0 {
		return "", nil, false
	}

	if _, err := strconv.Atoi(fields[1]); err != nil {
		return "", nil, false
	}

	values := make(map[string]float64, (len(fields)-2)/2)
	for i := 2; i < len(fields); i += 2 {
		v, err := strconv.ParseFloat(fields[i], 64)
		if err != nil {
			return "", nil, false
		}

		values[fields[i+1]] = v
	}

	return strings.TrimPrefix(fields[0], "Benchmark"), values, true
}

// render writes one table per metric for the benchmarks that reported it.
func render(out io.Writer, read *results) {
	if read.cpu != "" {
		fmt.Fprintf(out, "Measured on %s. Each cell is the median of all runs, the best value in a row is in bold, and the factor in parentheses is relative to %s.\n\n", read.cpu, read.labels[0])
	}

	for _, m := range metrics {
		var rows []string
		for _, name := range read.benchmarks {
			if row, ok := renderRow(m, name, read); ok {
				rows = append(rows, row)
			}
		}

		if len(rows) == 0 {
			continue
		}

		fmt.Fprintf(out, "### %s\n\n", m.title)
		if m.higherBetter {
			fmt.Fprint(out, "Higher is better.\n\n")
		} else {
			fmt.Fprint(out, "Lower is better.\n\n")
		}

		fmt.Fprintf(out, "| Benchmark | %s |\n", strings.Join(read.labels, " | "))
		fmt.Fprintf(out, "|---|%s\n", strings.Repeat("---:|", len(read.labels)))
		fmt.Fprint(out, strings.Join(rows, ""))
		fmt.Fprintln(out)
	}
}

// renderRow renders the row of benchmark name in the table of m, and reports
// false when the benchmark does not belong in that table.
func renderRow(m metric, name string, read *results) (string, bool) {
	byLabel := read.samples[name]
	if m.exclusive {
		if !slices.ContainsFunc(m.units, func(unit string) bool { return hasUnit(byLabel, unit) }) {
			return "", false
		}
	} else if hasExclusiveUnit(byLabel) {
		return "", false
	}

	unit := m.units[0]
	for _, candidate := range m.units {
		if hasUnit(byLabel, candidate) {
			unit = candidate
			break
		}
	}

	medians := make([]float64, len(read.labels))
	found := make([]bool, len(read.labels))
	for i, label := range read.labels {
		if values := byLabel[label][unit]; len(values) > 0 {
			medians[i], found[i] = median(values), true
		}
	}

	best, hasBest := 0.0, false
	for i := range read.labels {
		if !found[i] {
			continue
		}

		if !hasBest || (m.higherBetter && medians[i] > best) || (!m.higherBetter && medians[i] < best) {
			best, hasBest = medians[i], true
		}
	}

	cells := make([]string, len(read.labels))
	for i := range read.labels {
		if !found[i] {
			cells[i] = "n/a"
			continue
		}

		cell := m.format(medians[i], unit)
		if i > 0 && found[0] && medians[0] > 0 {
			cell += fmt.Sprintf(" (%.2f×)", medians[i]/medians[0])
		}

		if medians[i] == best {
			cell = "**" + cell + "**"
		}

		cells[i] = cell
	}

	return fmt.Sprintf("| %s | %s |\n", name, strings.Join(cells, " | ")), true
}

// filter returns a function reporting whether a benchmark is rendered: its name
// matches match, when set, and does not match skip, when set.
func filter(match, skip string) (func(name string) bool, error) {
	var matchRe, skipRe *regexp.Regexp
	var err error
	if match != "" {
		if matchRe, err = regexp.Compile(match); err != nil {
			return nil, fmt.Errorf("invalid -match: %w", err)
		}
	}

	if skip != "" {
		if skipRe, err = regexp.Compile(skip); err != nil {
			return nil, fmt.Errorf("invalid -skip: %w", err)
		}
	}

	return func(name string) bool {
		return (matchRe == nil || matchRe.MatchString(name)) && (skipRe == nil || !skipRe.MatchString(name))
	}, nil
}

// hasExclusiveUnit reports whether a benchmark recorded the unit of an
// exclusive metric.
func hasExclusiveUnit(byLabel samples) bool {
	for _, m := range metrics {
		if !m.exclusive {
			continue
		}

		for _, unit := range m.units {
			if hasUnit(byLabel, unit) {
				return true
			}
		}
	}

	return false
}

// hasUnit reports whether any run recorded unit for a benchmark.
func hasUnit(byLabel samples, unit string) bool {
	for _, units := range byLabel {
		if len(units[unit]) > 0 {
			return true
		}
	}

	return false
}

// median returns the median of values.
func median(values []float64) float64 {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 0 {
		return (sorted[mid-1] + sorted[mid]) / 2
	}

	return sorted[mid]
}

// formatDuration formats a time per operation given in nanoseconds.
func formatDuration(ns float64, _ string) string {
	switch {
	case ns >= 1e6:
		return fmt.Sprintf("%.2f ms", ns/1e6)
	case ns >= 1e3:
		return fmt.Sprintf("%.2f µs", ns/1e3)
	default:
		return fmt.Sprintf("%.1f ns", ns)
	}
}

// formatRate formats a per-second rate with its unit.
func formatRate(rate float64, unit string) string {
	switch {
	case rate >= 1e6:
		return fmt.Sprintf("%.2fM %s", rate/1e6, unit)
	case rate >= 1e3:
		return fmt.Sprintf("%.0fK %s", rate/1e3, unit)
	default:
		return fmt.Sprintf("%.0f %s", rate, unit)
	}
}
