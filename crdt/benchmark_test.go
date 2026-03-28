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

package crdt

import (
	"fmt"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// GCounter benchmarks
// ---------------------------------------------------------------------------

func benchmarkGCounterMergeConverged(b *testing.B, nodes int) {
	c1 := NewGCounter()
	c2 := NewGCounter()
	for i := range nodes {
		nodeID := "node-" + string(rune('A'+i))
		c1 = c1.Increment(nodeID, uint64(i+1))
		c2 = c2.Increment(nodeID, uint64(i+1))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c1.Merge(c2)
	}
}

func BenchmarkGCounterMergeConverged_3Nodes(b *testing.B)  { benchmarkGCounterMergeConverged(b, 3) }
func BenchmarkGCounterMergeConverged_10Nodes(b *testing.B) { benchmarkGCounterMergeConverged(b, 10) }
func BenchmarkGCounterMergeConverged_50Nodes(b *testing.B) { benchmarkGCounterMergeConverged(b, 50) }

func benchmarkGCounterMergeDiverged(b *testing.B, nodes int) {
	c1 := NewGCounter()
	c2 := NewGCounter()
	for i := range nodes {
		nodeID := "node-" + string(rune('A'+i))
		c1 = c1.Increment(nodeID, uint64(i+1))
		c2 = c2.Increment(nodeID, uint64(i+2))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c1.Merge(c2)
	}
}

func BenchmarkGCounterMergeDiverged_3Nodes(b *testing.B)  { benchmarkGCounterMergeDiverged(b, 3) }
func BenchmarkGCounterMergeDiverged_10Nodes(b *testing.B) { benchmarkGCounterMergeDiverged(b, 10) }
func BenchmarkGCounterMergeDiverged_50Nodes(b *testing.B) { benchmarkGCounterMergeDiverged(b, 50) }

func BenchmarkGCounterIncrement(b *testing.B) {
	c := NewGCounter()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c = c.Increment("node-1", 1)
	}
}

func BenchmarkGCounterClone(b *testing.B) {
	c := NewGCounter()
	for i := range 10 {
		c = c.Increment(fmt.Sprintf("node-%d", i), uint64(i+1))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c.Clone()
	}
}

func BenchmarkGCounterDelta(b *testing.B) {
	c := NewGCounter().Increment("node-1", 100)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c.Delta()
	}
}

func BenchmarkGCounterValue(b *testing.B) {
	c := NewGCounter()
	for i := range 50 {
		c = c.Increment(fmt.Sprintf("node-%d", i), uint64(i+1))
	}
	b.ResetTimer()
	for range b.N {
		c.Value()
	}
}

// ---------------------------------------------------------------------------
// PNCounter benchmarks
// ---------------------------------------------------------------------------

func benchmarkPNCounterMergeConverged(b *testing.B, nodes int) {
	c1 := NewPNCounter()
	c2 := NewPNCounter()
	for i := range nodes {
		nodeID := "node-" + string(rune('A'+i))
		c1 = c1.Increment(nodeID, uint64(i+1))
		c2 = c2.Increment(nodeID, uint64(i+1))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c1.Merge(c2)
	}
}

func BenchmarkPNCounterMergeConverged_3Nodes(b *testing.B) {
	benchmarkPNCounterMergeConverged(b, 3)
}
func BenchmarkPNCounterMergeConverged_10Nodes(b *testing.B) {
	benchmarkPNCounterMergeConverged(b, 10)
}

func BenchmarkPNCounterIncrement(b *testing.B) {
	c := NewPNCounter()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c = c.Increment("node-1", 1)
	}
}

func BenchmarkPNCounterDecrement(b *testing.B) {
	c := NewPNCounter()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c = c.Decrement("node-1", 1)
	}
}

func BenchmarkPNCounterClone(b *testing.B) {
	c := NewPNCounter()
	for i := range 10 {
		id := fmt.Sprintf("node-%d", i)
		c = c.Increment(id, uint64(i+1))
		c = c.Decrement(id, uint64(i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c.Clone()
	}
}

func BenchmarkPNCounterDelta(b *testing.B) {
	c := NewPNCounter().Increment("node-1", 100).Decrement("node-1", 10)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c.Delta()
	}
}

func BenchmarkPNCounterValue(b *testing.B) {
	c := NewPNCounter()
	for i := range 50 {
		id := fmt.Sprintf("node-%d", i)
		c = c.Increment(id, uint64(i+1))
		c = c.Decrement(id, uint64(i))
	}
	b.ResetTimer()
	for range b.N {
		c.Value()
	}
}

// ---------------------------------------------------------------------------
// LWWRegister benchmarks
// ---------------------------------------------------------------------------

func BenchmarkLWWRegisterMerge(b *testing.B) {
	now := time.Now()
	r1 := NewLWWRegister[string]().Set("v1", now, "node-1")
	r2 := NewLWWRegister[string]().Set("v2", now.Add(time.Second), "node-2")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r1.Merge(r2)
	}
}

func BenchmarkLWWRegisterMergeConverged(b *testing.B) {
	now := time.Now()
	r1 := NewLWWRegister[string]().Set("v1", now, "node-1")
	r2 := NewLWWRegister[string]().Set("v1", now, "node-1")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r1.Merge(r2)
	}
}

func BenchmarkLWWRegisterSet(b *testing.B) {
	r := NewLWWRegister[string]()
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r.Set("value", now, "node-1")
	}
}

// ---------------------------------------------------------------------------
// Flag benchmarks
// ---------------------------------------------------------------------------

func BenchmarkFlagMerge(b *testing.B) {
	f1 := NewFlag().Enable()
	f2 := NewFlag()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		f1.Merge(f2)
	}
}

func BenchmarkFlagMergeConverged(b *testing.B) {
	f1 := NewFlag().Enable()
	f2 := NewFlag().Enable()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		f1.Merge(f2)
	}
}

func BenchmarkFlagClone(b *testing.B) {
	f := NewFlag().Enable()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		f.Clone()
	}
}

func BenchmarkFlagDelta(b *testing.B) {
	f := NewFlag().Enable()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		f.Delta()
	}
}

// ---------------------------------------------------------------------------
// ORSet benchmarks
// ---------------------------------------------------------------------------

func benchmarkORSetMergeConverged(b *testing.B, elements int) {
	s1 := NewORSet[int]()
	s2 := NewORSet[int]()
	for i := range elements {
		s1 = s1.Add("node-1", i)
		s2 = s2.Add("node-1", i)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		s1.Merge(s2)
	}
}

func BenchmarkORSetMergeConverged_10(b *testing.B)   { benchmarkORSetMergeConverged(b, 10) }
func BenchmarkORSetMergeConverged_100(b *testing.B)  { benchmarkORSetMergeConverged(b, 100) }
func BenchmarkORSetMergeConverged_1000(b *testing.B) { benchmarkORSetMergeConverged(b, 1000) }

func benchmarkORSetMergeDiverged(b *testing.B, elements int) {
	s1 := NewORSet[int]()
	s2 := NewORSet[int]()
	for i := range elements {
		s1 = s1.Add("node-1", i)
		s2 = s2.Add("node-2", i+elements)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		s1.Merge(s2)
	}
}

func BenchmarkORSetMergeDiverged_10(b *testing.B)   { benchmarkORSetMergeDiverged(b, 10) }
func BenchmarkORSetMergeDiverged_100(b *testing.B)  { benchmarkORSetMergeDiverged(b, 100) }
func BenchmarkORSetMergeDiverged_1000(b *testing.B) { benchmarkORSetMergeDiverged(b, 1000) }

func BenchmarkORSetAdd(b *testing.B) {
	s := NewORSet[int]()
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		s = s.Add("node-1", i)
	}
}

func BenchmarkORSetContains(b *testing.B) {
	s := NewORSet[string]()
	for i := range 1000 {
		s = s.Add("node-1", fmt.Sprintf("elem-%d", i))
	}
	b.ResetTimer()
	for range b.N {
		s.Contains("elem-500")
	}
}

func benchmarkORSetClone(b *testing.B, size int) {
	s := NewORSet[string]()
	for i := range size {
		s = s.Add("node-1", fmt.Sprintf("elem-%d", i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		s.Clone()
	}
}

func BenchmarkORSetClone_100(b *testing.B)  { benchmarkORSetClone(b, 100) }
func BenchmarkORSetClone_1000(b *testing.B) { benchmarkORSetClone(b, 1000) }

func benchmarkORSetCompact(b *testing.B, size int) {
	s := NewORSet[string]()
	for i := range size {
		s = s.Add("node-1", fmt.Sprintf("elem-%d", i))
		s = s.Add("node-2", fmt.Sprintf("elem-%d", i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		s.Compact()
	}
}

func BenchmarkORSetCompact_100(b *testing.B)  { benchmarkORSetCompact(b, 100) }
func BenchmarkORSetCompact_1000(b *testing.B) { benchmarkORSetCompact(b, 1000) }

func BenchmarkORSetDelta(b *testing.B) {
	s := NewORSet[string]()
	for i := range 100 {
		s = s.Add("node-1", fmt.Sprintf("elem-%d", i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		s.Delta()
	}
}

func benchmarkORSetElements(b *testing.B, size int) {
	s := NewORSet[string]()
	for i := range size {
		s = s.Add("node-1", fmt.Sprintf("elem-%d", i))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		s.Elements()
	}
}

func BenchmarkORSetElements_100(b *testing.B)  { benchmarkORSetElements(b, 100) }
func BenchmarkORSetElements_1000(b *testing.B) { benchmarkORSetElements(b, 1000) }

// ---------------------------------------------------------------------------
// MVRegister benchmarks
// ---------------------------------------------------------------------------

func BenchmarkMVRegisterSet(b *testing.B) {
	r := NewMVRegister[string]()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r = r.Set("node-1", "value")
	}
}

func BenchmarkMVRegisterMergeConverged(b *testing.B) {
	r := NewMVRegister[string]().Set("node-1", "hello")
	r.ResetDelta()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r.Merge(r)
	}
}

func BenchmarkMVRegisterMergeConcurrent(b *testing.B) {
	r1 := NewMVRegister[string]().Set("node-1", "a")
	r2 := NewMVRegister[string]().Set("node-2", "b")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r1.Merge(r2)
	}
}

func BenchmarkMVRegisterClone(b *testing.B) {
	r := NewMVRegister[string]().Set("node-1", "a").Set("node-2", "b")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r.Clone()
	}
}

func BenchmarkMVRegisterDelta(b *testing.B) {
	r := NewMVRegister[string]().Set("node-1", "hello")
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		r.Delta()
	}
}

func BenchmarkMVRegisterValues(b *testing.B) {
	r := NewMVRegister[string]()
	r = r.Set("node-1", "a")
	merged := r.Merge(NewMVRegister[string]().Set("node-2", "b")).(*MVRegister[string])
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		merged.Values()
	}
}

// ---------------------------------------------------------------------------
// ORMap benchmarks
// ---------------------------------------------------------------------------

func BenchmarkORMapSet(b *testing.B) {
	m := NewORMap[string, *GCounter]()
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		m = m.Set("node-1", fmt.Sprintf("key-%d", i), NewGCounter().Increment("node-1", 1))
	}
}

func BenchmarkORMapMergeConverged(b *testing.B) {
	m := NewORMap[string, *GCounter]()
	for i := range 10 {
		key := "key-" + string(rune('a'+i))
		m = m.Set("node-1", key, NewGCounter().Increment("node-1", uint64(i+1)))
	}
	m.ResetDelta()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.Merge(m)
	}
}

func BenchmarkORMapMergeDiverged(b *testing.B) {
	m1 := NewORMap[string, *GCounter]()
	m2 := NewORMap[string, *GCounter]()
	for i := range 10 {
		key := "key-" + string(rune('a'+i))
		m1 = m1.Set("node-1", key, NewGCounter().Increment("node-1", uint64(i+1)))
		m2 = m2.Set("node-2", key, NewGCounter().Increment("node-2", uint64(i+2)))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m1.Merge(m2)
	}
}

func benchmarkORMapMergeConvergedN(b *testing.B, size int) {
	m := NewORMap[string, *GCounter]()
	for i := range size {
		m = m.Set("node-1", fmt.Sprintf("key-%d", i), NewGCounter().Increment("node-1", uint64(i)))
	}
	m.ResetDelta()
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.Merge(m)
	}
}

func BenchmarkORMapMergeConverged_100(b *testing.B) { benchmarkORMapMergeConvergedN(b, 100) }

func benchmarkORMapMergeDivergedN(b *testing.B, size int) {
	m1 := NewORMap[string, *GCounter]()
	m2 := NewORMap[string, *GCounter]()
	for i := range size {
		key := fmt.Sprintf("key-%d", i)
		m1 = m1.Set("node-1", key, NewGCounter().Increment("node-1", uint64(i+1)))
		m2 = m2.Set("node-2", key, NewGCounter().Increment("node-2", uint64(i+2)))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m1.Merge(m2)
	}
}

func BenchmarkORMapMergeDivergent_100(b *testing.B) { benchmarkORMapMergeDivergedN(b, 100) }

func BenchmarkORMapGet(b *testing.B) {
	m := NewORMap[string, *GCounter]()
	for i := range 100 {
		m = m.Set("node-1", fmt.Sprintf("key-%d", i), NewGCounter().Increment("node-1", uint64(i)))
	}
	b.ResetTimer()
	for range b.N {
		m.Get("key-50")
	}
}

func BenchmarkORMapClone(b *testing.B) {
	m := NewORMap[string, *GCounter]()
	for i := range 100 {
		m = m.Set("node-1", fmt.Sprintf("key-%d", i), NewGCounter().Increment("node-1", uint64(i)))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.Clone()
	}
}

func BenchmarkORMapCompact(b *testing.B) {
	m := NewORMap[string, *GCounter]()
	for i := range 100 {
		m = m.Set("node-1", fmt.Sprintf("key-%d", i), NewGCounter().Increment("node-1", uint64(i)))
		m = m.Set("node-2", fmt.Sprintf("key-%d", i), NewGCounter().Increment("node-2", uint64(i)))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		m.Compact()
	}
}
