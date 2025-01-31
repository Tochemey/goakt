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

package levelstore

import (
	"container/list"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"sync"

	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/logsm/bloom"
	"github.com/tochemey/goakt/v2/internal/logsm/merger"
	"github.com/tochemey/goakt/v2/internal/logsm/sstable"
	"github.com/tochemey/goakt/v2/internal/logsm/types"
	"github.com/tochemey/goakt/v2/internal/logsm/util"
	"github.com/tochemey/goakt/v2/log"
)

type Table struct {
	// list index of table within a level
	levelIndex int
	// bloom filter
	filter *bloom.Filter
	// index of data blocks in this sstable
	dataBlockIndex sstable.Index
}

type Store struct {
	mu            *sync.Mutex
	dir           string
	l0TargetNum   int
	ratio         int
	dataBlockSize int
	// list.Element: table
	levels []*list.List

	logger log.Logger
}

func New(dir string, l0TargetNum, ratio, blockSize int, logger log.Logger) *Store {
	return &Store{
		dir:           dir,
		l0TargetNum:   l0TargetNum,
		ratio:         ratio,
		dataBlockSize: blockSize,
		logger:        logger,
		mu:            new(sync.Mutex),
	}
}

func (x *Store) Recover() error {
	x.mu.Lock()
	defer x.mu.Unlock()

	files, err := os.ReadDir(x.dir)
	if err != nil {
		return fmt.Errorf("failed to read dir: %w", err)
	}

	var dbFiles []string
	for _, file := range files {
		if !file.IsDir() && path.Ext(file.Name()) == ".db" {
			dbFiles = append(dbFiles, file.Name())
		}
	}

	if len(dbFiles) == 0 {
		return nil
	}

	slices.Sort(dbFiles)

	for _, file := range dbFiles {
		level, idx, err := parseFileName(file)
		if err != nil {
			return fmt.Errorf("failed to parse file name: %w", err)
		}

		fd, err := os.Open(path.Join(x.dir, file))
		if err != nil {
			return fmt.Errorf("failed to open file %s: %w", file, err)
		}

		// read and decode footer
		_, err = fd.Seek(-40, io.SeekEnd)
		if err != nil {
			return fmt.Errorf("failed to seek file %s: %w", file, err)
		}

		footerBytes := make([]byte, 40)
		_, err = fd.Read(footerBytes)
		if err != nil {
			return fmt.Errorf("failed to read footer: %w", err)
		}

		var footer sstable.Footer
		if err = footer.Decode(footerBytes); err != nil {
			return fmt.Errorf("failed to decode footer: %w", err)
		}

		// read and decode index block
		_, err = fd.Seek(int64(footer.IndexBlock.Offset), io.SeekStart)
		if err != nil {
			return fmt.Errorf("failed to seek footer: %w", err)
		}

		indexBytes := make([]byte, footer.IndexBlock.Length)
		_, err = fd.Read(indexBytes)
		if err != nil {
			return fmt.Errorf("failed to read index: %w", err)
		}

		var index sstable.Index
		if err = index.Decode(indexBytes); err != nil {
			return fmt.Errorf("failed to decode index: %w", err)
		}

		// read and decode data blocks
		_, err = fd.Seek(int64(index.DataBlock.Offset), io.SeekStart)
		if err != nil {
			return fmt.Errorf("failed to seek data block: %w", err)
		}

		dataBlockBytes := make([]byte, index.DataBlock.Length)
		_, err = fd.Read(dataBlockBytes)
		if err != nil {
			return fmt.Errorf("failed to read data block: %w", err)
		}

		var dataBlock sstable.Data
		if err = dataBlock.Decode(dataBlockBytes); err != nil {
			return fmt.Errorf("failed to decode data block: %w", err)
		}

		bf := bloom.New(len(dataBlock.Entries), 0.01)

		for len(x.levels) <= level {
			x.levels = append(x.levels, list.New())
		}

		th := Table{
			levelIndex:     idx,
			filter:         bf,
			dataBlockIndex: index,
		}

		x.levels[level].PushBack(th)
	}
	return nil
}

func (x *Store) Search(key types.Key) (*internalpb.Entry, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()

	if len(x.levels) == 0 {
		return nil, false
	}

	for level, tables := range x.levels {
		for e := tables.Front(); e != nil; e = e.Next() {
			th := e.Value.(Table)

			// search bloom filter
			if !th.filter.Contains(key) {
				// not in this sstable, search next one
				continue
			}

			// determine which data block the key is in
			dataBlockHandle, ok := th.dataBlockIndex.Search(key)
			if !ok {
				// not in this sstable, search next one
				continue
			}

			// in this sstable, search according to data block
			entry, ok := x.fetchAndSearch(key, level, th.levelIndex, dataBlockHandle)
			if ok {
				return entry, true
			}
		}
	}

	return nil, false
}

func (x *Store) Scan(start, end types.Key) []*internalpb.Entry {
	x.mu.Lock()
	defer x.mu.Unlock()

	if len(x.levels) == 0 {
		return nil
	}

	var entriesList [][]*internalpb.Entry
	// scan L0 - LN
	// sort and merge result
	for level, tables := range x.levels {
		// tables in same level
		var levelList [][]*internalpb.Entry
		for e := tables.Front(); e != nil; e = e.Next() {
			th := e.Value.(Table)

			// search the data blocks where the range in
			dataBlockHandles := th.dataBlockIndex.Scan(start, end)

			for _, handle := range dataBlockHandles {
				entries := x.fetchAndScan(start, end, level, th.levelIndex, handle)
				levelList = append(levelList, entries)
			}
		}
		slices.Reverse(levelList)
		entriesList = append(entriesList, levelList...)
	}

	slices.Reverse(entriesList)
	return merger.Merge(entriesList...)
}

func (x *Store) FlushToL0(kvs []*internalpb.Entry) error {
	x.mu.Lock()
	defer x.mu.Unlock()

	// new and build bloom filter
	bf := bloom.Build(kvs)
	// build sstable
	dataBlockIndex, tableBytes, err := sstable.Build(kvs, x.dataBlockSize, 0)
	if err != nil {
		return err
	}

	// lazy init
	if len(x.levels) == 0 {
		x.levels = append(x.levels, list.New())
	}

	// table handle
	th := Table{
		levelIndex:     x.maxLevelIdx(0) + 1,
		filter:         bf,
		dataBlockIndex: dataBlockIndex,
	}

	// l0 list
	x.levels[0].PushBack(th)

	// file name format: level-idx.db
	fd, err := os.OpenFile(x.fileName(0, th.levelIndex), os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}

	defer func() {
		if err = fd.Close(); err != nil {
			x.logger.Fatalf("failed to close file %s: %w", x.fileName(0, th.levelIndex), err)
		}
	}()

	// write sstable
	_, err = fd.Write(tableBytes)
	return err
}

func (x *Store) CheckAndCompact() error {
	x.mu.Lock()
	defer x.mu.Unlock()

	for i, tables := range x.levels {
		if tables.Len() > x.l0TargetNum*util.Pow(x.ratio, i) {
			if i == 0 {
				if err := x.compactL0(); err != nil {
					return err
				}
				continue
			}

			if err := x.compactLN(i); err != nil {
				return err
			}
		}
	}
	return nil
}

func (x *Store) fetch(level, idx int, handle sstable.Block) (sstable.Data, error) {
	filename := x.fileName(level, idx)
	fd, err := os.Open(filename)
	if err != nil {
		return sstable.Data{}, fmt.Errorf("failed to open sstable %s: %w", filename, err)
	}

	defer func() {
		if err = fd.Close(); err != nil {
			x.logger.Fatalf("failed to close sstable %s: %w", filename, err)
		}
	}()

	_, err = fd.Seek(int64(handle.Offset), io.SeekStart)
	if err != nil {
		return sstable.Data{}, fmt.Errorf("failed to seek sstable %s: %w", filename, err)
	}

	data := make([]byte, handle.Length)
	_, err = fd.Read(data)
	if err != nil {
		return sstable.Data{}, fmt.Errorf("failed to read sstable %s: %w", filename, err)
	}

	var dataBlock sstable.Data
	if err = dataBlock.Decode(data); err != nil {
		return sstable.Data{}, fmt.Errorf("failed to decode sstable data block %s: %w", filename, err)
	}

	return dataBlock, nil
}

func (x *Store) fetchAndSearch(key types.Key, level, idx int, handle sstable.Block) (*internalpb.Entry, bool) {
	dataBlock, err := x.fetch(level, idx, handle)
	if err != nil {
		x.logger.Errorf("failed to fetch and search %s: %w", x.fileName(level, idx), err)
		return nil, false
	}
	return dataBlock.Search(key)
}

func (x *Store) fetchAndScan(start, end types.Key, level, idx int, handle sstable.Block) []*internalpb.Entry {
	dataBlock, err := x.fetch(level, idx, handle)
	if err != nil {
		x.logger.Errorf("failed to fetch and scan %s: %w", x.fileName(level, idx), err)
		return nil
	}
	return dataBlock.Scan(start, end)
}

// L0 -> L1
func (x *Store) compactL0() error {
	// lazy init
	if len(x.levels)-1 < 1 {
		x.levels = append(x.levels, list.New())
	}

	// len(overlaps) >= 1
	// overlap sstables in level 0
	l0Tables := x.overlapL0()

	// boundary from first table to last table in l0Tables
	start, end := boundary(l0Tables...)

	// overlap sstables in L1
	l1Tables := x.overlapLN(1, start, end)

	// old -> new (append L1 first)
	var dataBlockList [][]*internalpb.Entry
	// L1 data block entries
	for _, tb := range l1Tables {
		th := tb.Value.(Table)
		dataBlock, err := x.fetch(1, th.levelIndex, th.dataBlockIndex.DataBlock)
		if err != nil {
			return err
		}
		dataBlockList = append(dataBlockList, dataBlock.Entries)
	}
	// L0 data block entries
	for _, tb := range l0Tables {
		th := tb.Value.(Table)
		dataBlock, err := x.fetch(0, th.levelIndex, th.dataBlockIndex.DataBlock)
		if err != nil {
			return err
		}
		dataBlockList = append(dataBlockList, dataBlock.Entries)
	}

	// merge sstables
	mergedEntries := merger.Merge(dataBlockList...)

	// build new bloom filter
	bf := bloom.Build(mergedEntries)
	// build new sstable
	dataBlockIndex, tableBytes, err := sstable.Build(mergedEntries, x.dataBlockSize, 1)
	if err != nil {
		return err
	}

	// table handle
	th := Table{
		levelIndex:     x.maxLevelIdx(1) + 1,
		filter:         bf,
		dataBlockIndex: dataBlockIndex,
	}

	// update index
	// add new index to L1
	x.levels[1].PushBack(th)

	// remove old sstable index from L0
	for _, e := range l0Tables {
		x.levels[0].Remove(e)
	}
	// remove old sstable index from L1
	for _, e := range l1Tables {
		x.levels[1].Remove(e)
	}

	// delete old sstables from L0
	for _, e := range l0Tables {
		if err := os.Remove(x.fileName(0, e.Value.(Table).levelIndex)); err != nil {
			return fmt.Errorf("failed to remove %w", err)
		}
	}

	// delete old sstables from L1
	for _, e := range l1Tables {
		if err := os.Remove(x.fileName(1, e.Value.(Table).levelIndex)); err != nil {
			return fmt.Errorf("failed to remove %w", err)
		}
	}

	// write new sstable
	fd, err := os.OpenFile(x.fileName(1, th.levelIndex), os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open sstable %w", err)
	}

	defer func() {
		if err = fd.Close(); err != nil {
			x.logger.Fatal(err)
		}
	}()

	_, err = fd.Write(tableBytes)
	return err
}

// LN -> LN+1
func (x *Store) compactLN(n int) error {
	// lazy init
	if len(x.levels)-1 < n+1 {
		x.levels = append(x.levels, list.New())
	}

	lnTable := x.levels[n].Front()
	start, end := boundary(lnTable)

	// overlap sstables in LN+1
	ln1Tables := x.overlapLN(n+1, start, end)

	// old -> new (append LN+1 first)
	var dataBlockList [][]*internalpb.Entry
	// LN+1 data block entries
	for _, tb := range ln1Tables {
		th := tb.Value.(Table)
		dataBlockLN1, err := x.fetch(n+1, th.levelIndex, th.dataBlockIndex.DataBlock)
		if err != nil {
			return err
		}
		dataBlockList = append(dataBlockList, dataBlockLN1.Entries)
	}

	// LN data block entries
	dataBlockLN, err := x.fetch(n, lnTable.Value.(Table).levelIndex, lnTable.Value.(Table).dataBlockIndex.DataBlock)
	if err != nil {
		return err
	}
	dataBlockList = append(dataBlockList, dataBlockLN.Entries)

	// merge sstables
	mergedEntries := merger.Merge(dataBlockList...)

	// build new bloom filter
	bf := bloom.Build(mergedEntries)
	// build new sstable
	dataBlockIndex, tableBytes, err := sstable.Build(mergedEntries, x.dataBlockSize, n+1)
	if err != nil {
		return err
	}

	// table handle
	th := Table{
		levelIndex:     x.maxLevelIdx(n+1) + 1,
		filter:         bf,
		dataBlockIndex: dataBlockIndex,
	}

	// update index
	// add new index to LN+1
	x.levels[n+1].PushBack(th)

	// remove old sstable index from LN
	x.levels[n].Remove(lnTable)
	// remove old sstable index from LN+1
	for _, e := range ln1Tables {
		x.levels[n+1].Remove(e)
	}

	// delete old sstables from LN
	if err := os.Remove(x.fileName(n, lnTable.Value.(Table).levelIndex)); err != nil {
		return fmt.Errorf("failed to remove old sstable %w", err)
	}

	// delete old sstables from LN+1
	for _, e := range ln1Tables {
		if err := os.Remove(x.fileName(n+1, e.Value.(Table).levelIndex)); err != nil {
			return fmt.Errorf("failed to remove old sstable %w", err)
		}
	}

	// write new sstable
	fd, err := os.OpenFile(x.fileName(n+1, th.levelIndex), os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open sstable %w", err)
	}

	defer func() {
		if err = fd.Close(); err != nil {
			x.logger.Fatal(fmt.Errorf("failed to close sstable %w", err))
		}
	}()

	_, err = fd.Write(tableBytes)
	return err
}

func (x *Store) overlapL0() []*list.Element {
	frontIndex := x.levels[0].Front().Value.(Table).dataBlockIndex

	startKey := frontIndex.Entries[0].StartKey
	endKey := frontIndex.Entries[len(frontIndex.Entries)-1].EndKey

	return x.overlapLN(0, startKey, endKey)
}

func (x *Store) overlapLN(level int, start, end string) []*list.Element {
	// check if LN+1 is not initialized
	if x.levels[level].Len() == 0 {
		return nil
	}

	ln := x.levels[level]

	var overlaps []*list.Element
	for e := ln.Front(); e != nil; e = e.Next() {
		index := e.Value.(Table).dataBlockIndex
		if index.Entries[0].StartKey <= end && index.Entries[len(index.Entries)-1].EndKey >= start {
			overlaps = append(overlaps, e)
		}
	}

	return overlaps
}

func (x *Store) fileName(level, idx int) string {
	return path.Join(x.dir, fmt.Sprintf("%d-%d.db", level, idx))
}

// if no elements in this level, return -1
// else return max level idx
func (x *Store) maxLevelIdx(level int) int {
	res := -1
	for e := x.levels[level].Front(); e != nil; e = e.Next() {
		levelIdx := e.Value.(Table).levelIndex
		if levelIdx > res {
			res = levelIdx
		}
	}
	return res
}

func parseFileName(name string) (int, int, error) {
	var level, idx int
	_, err := fmt.Sscanf(name, "%d-%d.db", &level, &idx)
	if err != nil {
		return 0, 0, err
	}
	return level, idx, nil
}

func boundary(list ...*list.Element) (string, string) {
	entries := list[0].Value.(Table).dataBlockIndex.Entries
	start := entries[0].StartKey
	end := entries[len(entries)-1].EndKey

	for _, e := range list {
		index := e.Value.(Table).dataBlockIndex
		currStart := index.Entries[0].StartKey
		currEnd := index.Entries[len(index.Entries)-1].EndKey

		if currStart < start {
			start = currStart
		}
		if currEnd > end {
			end = currEnd
		}
	}
	return start, end
}
