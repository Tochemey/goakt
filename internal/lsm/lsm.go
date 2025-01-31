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

package lsm

import (
	"container/list"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/tochemey/goakt/v2/internal/lsm/compaction"
	"github.com/tochemey/goakt/v2/internal/lsm/memtable"
	"github.com/tochemey/goakt/v2/internal/size"
	"github.com/tochemey/goakt/v2/internal/types"
	"github.com/tochemey/goakt/v2/log"
)

type State uint32

const (
	_ State = iota
	StateInitialized
	StateOpened
	StateClosed
)

// LSMTree defines the Log-Structured Merge Tree
type LSMTree struct {
	// SkipList settings
	maxLevel    int
	probability float64

	// MemTable settings
	memTableSizeThreshold int
	immutableBuffer       int

	// SSTable setting
	datablockByteThreshold int

	// Levels setting
	l0TargetNum int
	levelRatio  int

	logger log.Logger
	dir    string
	state  uint32

	memtable  *memtable.MemTable
	immutable *list.List
	flushC    chan *memtable.MemTable

	compaction *compaction.Compaction
	closed     chan types.Unit
	closeC     chan types.Unit
}

// Open the log
func Open(dir string, opts ...Option) (*LSMTree, error) {
	lsm := &LSMTree{
		maxLevel:               9,
		probability:            5,
		memTableSizeThreshold:  20 * size.MB,
		immutableBuffer:        10,
		datablockByteThreshold: 4 * size.KB,
		l0TargetNum:            5,
		levelRatio:             10,
		dir:                    dir,
		immutable:              list.New(),
		logger:                 log.New(log.ErrorLevel, os.Stderr),
		flushC:                 make(chan *memtable.MemTable, 10),
		closeC:                 make(chan types.Unit, 1),
		closed:                 make(chan types.Unit, 1),
	}

	// apply the options
	for _, opt := range opts {
		opt.Apply(lsm)
	}

	// create the directory
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create directory=(%s): %w", dir, err)
	}

	atomic.StoreUint32(&lsm.state, uint32(StateInitialized))

	// recover from exist wal
	mt := memtable.New(dir, lsm.maxLevel, lsm.probability)
	mt.Recover()

	// recover from exist log
	compaction := compaction.New(dir, lsm.l0TargetNum, lsm.levelRatio, lsm.datablockByteThreshold)
	compaction.Recover()

	lsm.compaction = compaction
	lsm.memtable = mt

	// TODO: add the run

	return lsm, nil
}
