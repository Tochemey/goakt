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
	"github.com/tochemey/goakt/v3/internal/internalpb"
)

type Element struct {
	*internalpb.Entry
	// sorted sequence
	SortedSeq int
}

// Heap min heap
type Heap []Element

func (h *Heap) Len() int {
	return len(*h)
}

func (h *Heap) Less(i, j int) bool {
	return (*h)[i].GetKey() < (*h)[j].GetKey() &&
		(*h)[i].SortedSeq == (*h)[j].SortedSeq ||
		(*h)[i].GetKey() == (*h)[j].GetKey() &&
			(*h)[i].SortedSeq < (*h)[j].SortedSeq
}

func (h *Heap) Swap(i, j int) {
	(*h)[i], (*h)[j] = (*h)[j], (*h)[i]
}

func (h *Heap) Push(x any) {
	*h = append(*h, x.(Element))
}

// Pop the minimum element in heap
// 1. move the minimum element to the end of slice
// 2. pop it (what this method does)
// 3. heapify
func (h *Heap) Pop() any {
	curr := *h
	n := len(curr)
	e := curr[n-1]
	*h = curr[0 : n-1]
	return e
}
