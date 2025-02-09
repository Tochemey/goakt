/*
 * MIT License
 *
 * Copyright (c) 2022-2025  Arsene Tochemey Gandote
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package merger

import (
	"cmp"
	"container/heap"
	"slices"

	"github.com/tochemey/goakt/v3/internal/internalpb"
)

func Merge(lists ...[]*internalpb.Entry) []*internalpb.Entry {
	h := &Heap{}
	heap.Init(h)

	// push first element of each list
	for i, list := range lists {
		if len(list) > 0 {
			heap.Push(h, Element{
				Entry:     list[0],
				SortedSeq: i,
			})
			lists[i] = list[1:]
		}
	}

	latest := make(map[string]*internalpb.Entry)

	for h.Len() > 0 {
		// pop minimum element
		e := heap.Pop(h).(Element)
		latest[e.GetKey()] = e.Entry
		// push next element
		if len(lists[e.SortedSeq]) > 0 {
			heap.Push(h, Element{
				Entry:     lists[e.SortedSeq][0],
				SortedSeq: e.SortedSeq,
			})
			lists[e.SortedSeq] = lists[e.SortedSeq][1:]
		}
	}

	var merged []*internalpb.Entry

	for _, entry := range latest {
		if entry.Tombstone {
			continue
		}
		merged = append(merged, entry)
	}

	slices.SortFunc(merged, func(a, b *internalpb.Entry) int {
		return cmp.Compare(a.GetKey(), b.GetKey())
	})

	return merged
}
