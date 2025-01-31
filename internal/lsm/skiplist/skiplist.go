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

package skiplist

import (
	"math/rand"
	"time"
	"unsafe"

	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/lsm/types"
)

const _head = "HEAD"

type SkipList struct {
	maxLevel    int
	probability float64
	level       int
	rand        *rand.Rand
	size        int
	head        *Element
}

type Element struct {
	*internalpb.Entry
	next []*Element
}

func New(maxLevel int, probability float64) *SkipList {
	return &SkipList{
		maxLevel:    maxLevel,
		probability: probability,
		level:       1,
		rand:        rand.New(rand.NewSource(time.Now().UnixNano())),
		size:        0,
		head: &Element{
			Entry: &internalpb.Entry{
				Key:       _head,
				Value:     nil,
				Tombstone: false,
			},
			next: make([]*Element, maxLevel),
		},
	}
}

func (s *SkipList) Reset() *SkipList {
	return New(s.maxLevel, s.probability)
}

func (s *SkipList) Size() int {
	return s.size
}

func (s *SkipList) Set(entry *internalpb.Entry) {
	curr := s.head
	update := make([]*Element, s.maxLevel)

	for i := s.maxLevel - 1; i >= 0; i-- {
		for curr.next[i] != nil && curr.next[i].GetKey() < entry.GetKey() {
			curr = curr.next[i]
		}
		update[i] = curr
	}

	// update entry
	if curr.next[0] != nil && curr.next[0].GetKey() == entry.GetKey() {
		s.size += len(entry.GetValue()) - len(curr.next[0].GetValue())

		// update value and tombstone
		curr.next[0].Value = entry.GetValue()
		curr.next[0].Tombstone = entry.GetTombstone()
		return
	}

	// add entry
	level := s.randomLevel()

	if level > s.level {
		for i := s.level; i < level; i++ {
			update[i] = s.head
		}
		s.level = level
	}

	e := &Element{
		Entry: &internalpb.Entry{
			Key:       entry.GetKey(),
			Value:     entry.GetValue(),
			Tombstone: entry.GetTombstone(),
		},
		next: make([]*Element, level),
	}

	for i := range level {
		e.next[i] = update[i].next[i]
		update[i].next[i] = e
	}
	s.size += len(entry.GetKey()) + len(entry.GetValue()) + int(unsafe.Sizeof(entry.GetTombstone())) + len(e.next)*int(unsafe.Sizeof((*Element)(nil)))
}

func (s *SkipList) Get(key types.Key) (*internalpb.Entry, bool) {
	curr := s.head

	for i := s.maxLevel - 1; i >= 0; i-- {
		for curr.next[i] != nil && curr.next[i].GetKey() < key {
			curr = curr.next[i]
		}
	}

	curr = curr.next[0]

	if curr != nil && curr.GetKey() == key {
		return &internalpb.Entry{
			Key:       curr.GetKey(),
			Value:     curr.GetValue(),
			Tombstone: curr.GetTombstone(),
		}, true
	}
	return nil, false
}

// Scan [start, end)
func (s *SkipList) Scan(start, end types.Key) []*internalpb.Entry {
	var res []*internalpb.Entry
	curr := s.head

	for i := s.maxLevel - 1; i >= 0; i-- {
		for curr.next[i] != nil && curr.next[i].GetKey() < start {
			curr = curr.next[i]
		}
	}

	curr = curr.next[0]

	for curr != nil && curr.GetKey() < end {
		res = append(res, &internalpb.Entry{
			Key:       curr.GetKey(),
			Value:     curr.GetValue(),
			Tombstone: curr.GetTombstone(),
		})
		curr = curr.next[0]
	}

	return res
}

func (s *SkipList) All() []*internalpb.Entry {
	var all []*internalpb.Entry

	for curr := s.head.next[0]; curr != nil; curr = curr.next[0] {
		all = append(all, &internalpb.Entry{
			Key:       curr.GetKey(),
			Value:     curr.GetValue(),
			Tombstone: curr.GetTombstone(),
		})
	}

	return all
}

// Delete won't be used, use tombstone in set instead
func (s *SkipList) Delete(key types.Key) bool {
	curr := s.head
	update := make([]*Element, s.maxLevel)

	for i := s.maxLevel - 1; i >= 0; i-- {
		for curr.next[i] != nil && curr.next[i].GetKey() < key {
			curr = curr.next[i]
		}
		update[i] = curr
	}

	curr = curr.next[0]

	if curr != nil && curr.GetKey() == key {
		for i := range s.level {
			if update[i].next[i] != curr {
				break
			}
			update[i].next[i] = curr.next[i]
		}
		s.size -= len(curr.GetKey()) + len(curr.GetValue()) + int(unsafe.Sizeof(curr.GetTombstone())) + len(curr.next)*int(unsafe.Sizeof((*Element)(nil)))

		for s.level > 1 && s.head.next[s.level-1] == nil {
			s.level--
		}
		return true
	}
	return false
}

// n < MaxLevel, return level == n has probability P^n
func (s *SkipList) randomLevel() int {
	level := 1
	for s.rand.Float64() < s.probability && level < s.maxLevel {
		level++
	}
	return level
}
