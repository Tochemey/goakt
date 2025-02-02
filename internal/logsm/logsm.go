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

package logsm

import (
	"container/list"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/tochemey/goakt/v2/internal/errorschain"
	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/logsm/levelstore"
	"github.com/tochemey/goakt/v2/internal/logsm/memtable"
	"github.com/tochemey/goakt/v2/internal/logsm/merger"
	stypes "github.com/tochemey/goakt/v2/internal/logsm/types"
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

// LogSM defines the Log-Structured Merge Tree
type LogSM struct {
	mu sync.RWMutex
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

	memtable   *memtable.MemTable
	immutables *list.List
	flushC     chan *memtable.MemTable

	levelsStorage *levelstore.Store
	closed        chan types.Unit
	closeC        chan types.Unit
}

// Open the log
func Open(dir string, opts ...Option) (*LogSM, error) {
	storage := &LogSM{
		maxLevel:               9,
		probability:            5,
		memTableSizeThreshold:  20 * size.MB,
		immutableBuffer:        10,
		datablockByteThreshold: 4 * size.KB,
		l0TargetNum:            5,
		levelRatio:             10,
		dir:                    dir,
		immutables:             list.New(),
		logger:                 log.New(log.ErrorLevel, os.Stderr),
		flushC:                 make(chan *memtable.MemTable, 10),
		closeC:                 make(chan types.Unit, 1),
		closed:                 make(chan types.Unit, 1),
	}

	// apply the options
	for _, opt := range opts {
		opt.Apply(storage)
	}

	var err error
	storage.memtable, err = memtable.New(dir, storage.maxLevel, storage.probability)
	if err != nil {
		return nil, err
	}

	storage.levelsStorage = levelstore.New(dir, storage.l0TargetNum, storage.levelRatio, storage.datablockByteThreshold, storage.logger)

	if err := errorschain.
		New(errorschain.ReturnFirst()).
		AddError(os.MkdirAll(dir, 0755)).
		AddError(storage.memtable.Recover()).
		AddError(storage.levelsStorage.Recover()).
		Error(); err != nil {
		return nil, err
	}

	atomic.StoreUint32(&storage.state, uint32(StateInitialized))
	go storage.run()
	return storage, nil
}

func (x *LogSM) Close() {
	defer atomic.StoreUint32(&x.state, uint32(StateClosed))
	x.closeC <- types.Unit{}

	mt := x.memtable
	mt.Freeze()

	switch {
	case mt.Size() > 0:
		if err := x.flushImmutable(mt); err != nil {
			x.logger.Panic(err)
		}
	default:
		if err := mt.Wal().Delete(); err != nil {
			x.logger.Panicf("failed to delete immutable wal file: %v", err)
		}
	}

	<-x.closed
}

func (x *LogSM) State() State {
	return State(atomic.LoadUint32(&x.state))
}

func (x *LogSM) Set(key string, value []byte) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.rawset(&internalpb.Entry{
		Key:       key,
		Value:     value,
		Tombstone: false,
	})
}

func (x *LogSM) Get(key string) ([]byte, bool) {
	x.mu.RLock()
	defer x.mu.RUnlock()

	// search memtable
	mtEntry, ok := x.memtable.Get(key)
	if ok {
		return value(mtEntry)
	}

	// search immutables
	for e := x.immutables.Back(); e != nil; e = e.Prev() {
		imt := e.Value.(*memtable.MemTable)
		imtEntry, ok := imt.Get(key)
		if ok {
			return value(imtEntry)
		}
	}

	// search sstables
	sstEntry, ok := x.levelsStorage.Search(key)
	if ok {
		return value(sstEntry)
	}
	return nil, false
}

func (x *LogSM) Delete(key string) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	return x.rawset(&internalpb.Entry{
		Key:       key,
		Value:     []byte{},
		Tombstone: true,
	})
}

// Scan [start, end)
func (x *LogSM) Scan(start, end string) []stypes.KV {
	x.mu.RLock()
	defer x.mu.RUnlock()

	var scan [][]*internalpb.Entry

	// scan memtable
	scan = append(scan, x.memtable.Scan(start, end))

	// scan immutables
	for e := x.immutables.Back(); e != nil; e = e.Prev() {
		imt := e.Value.(*memtable.MemTable)
		scan = append(scan, imt.Scan(start, end))
	}

	// scan sstables
	scan = append(scan, x.levelsStorage.Scan(start, end))

	slices.Reverse(scan)
	// merge result
	return kvs(merger.Merge(scan...))
}

func (x *LogSM) rawset(entry *internalpb.Entry) error {
	if err := x.memtable.Set(entry); err != nil {
		return err
	}

	if x.memtable.Size() >= x.memTableSizeThreshold {
		x.memtable.Freeze()
		imt := x.memtable

		x.flushC <- imt
		x.immutables.PushBack(imt)
		x.memtable = x.memtable.Reset()
	}

	return nil
}

func (x *LogSM) run() {
	atomic.StoreUint32(&x.state, uint32(StateOpened))
	var closed bool
LOOP:
	for {
		select {
		case imt := <-x.flushC:
			if err := errorschain.
				New(errorschain.ReturnFirst()).
				AddError(x.flushImmutable(imt)).AddError(x.levelsStorage.CheckAndCompact()).Error(); err != nil {
				x.logger.Panic(err)
				return
			}

			x.mu.Lock()
			x.immutables.Remove(x.immutables.Back())
			x.mu.Unlock()

			if closed && len(x.flushC) == 0 {
				break LOOP
			}
		case <-x.closeC:
			closed = true
			if len(x.flushC) > 0 {
				continue
			}
			break LOOP
		}
	}
	close(x.closed)
}

func (x *LogSM) flushImmutable(imt *memtable.MemTable) error {
	// flush immutable memtable to L0 and delete WAL file
	return errorschain.
		New(errorschain.ReturnFirst()).
		AddError(x.levelsStorage.FlushToL0(imt.All())).
		AddError(imt.Wal().Delete()).
		Error()
}

func value(entry *internalpb.Entry) ([]byte, bool) {
	if entry.Tombstone {
		return nil, false
	}
	return entry.Value, true
}

func kvs(entries []*internalpb.Entry) []stypes.KV {
	var res []stypes.KV
	for _, entry := range entries {
		if entry.Tombstone {
			continue
		}
		res = append(res, stypes.KV{
			Key:   entry.Key,
			Value: entry.Value,
		})
	}
	return res
}
