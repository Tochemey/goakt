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

package memtable

import (
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"sync"

	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/logsm/skiplist"
	"github.com/tochemey/goakt/v3/internal/logsm/types"
	"github.com/tochemey/goakt/v3/internal/logsm/wal"
)

type MemTable struct {
	mu       sync.RWMutex
	skiplist *skiplist.SkipList
	wal      *wal.WAL
	dir      string
	readOnly bool
}

// Wal returns the memtable WAL
func (mt *MemTable) Wal() *wal.WAL {
	return mt.wal
}

func New(dir string, maxLevel int, probability float64) (*MemTable, error) {
	l, err := wal.Create(dir)
	if err != nil {
		return nil, err
	}
	return &MemTable{
		skiplist: skiplist.New(maxLevel, probability),
		wal:      l,
		dir:      dir,
		readOnly: false,
	}, nil
}

func (mt *MemTable) Recover() error {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	files, err := os.ReadDir(mt.dir)
	if err != nil {
		return fmt.Errorf("failed to read dir=(%s) :%w", mt.dir, err)
	}

	var walFiles []string
	for _, file := range files {
		if !file.IsDir() && path.Ext(file.Name()) == ".log" && wal.CompareVersion(wal.ParseVersion(file.Name()), mt.wal.Version()) < 0 {
			walFiles = append(walFiles, path.Join(mt.dir, file.Name()))
		}
	}

	if len(walFiles) == 0 {
		return nil
	}

	slices.Sort(walFiles)

	// merge wal files
	for _, file := range walFiles {
		l, err := wal.Open(file)
		if err != nil {
			return fmt.Errorf("failed to open wal file=(%s) :%w", file, err)
		}

		entries, err := l.Read()
		if err != nil {
			return fmt.Errorf("failed to read wal file=(%s) :%w", file, err)
		}

		for _, entry := range entries {
			mt.skiplist.Set(entry)
			if err = mt.wal.Write(entry); err != nil {
				return fmt.Errorf("failed to write wal file=(%s) :%w", file, err)
			}
		}

		if err = l.Delete(); err != nil {
			return fmt.Errorf("failed to delete wal file=(%s) :%w", file, err)
		}
	}
	return nil
}

func (mt *MemTable) Set(entry *internalpb.Entry) error {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	if mt.readOnly {
		return errors.New("memtable is read-only")
	}

	mt.skiplist.Set(entry)
	if err := mt.wal.Write(entry); err != nil {
		return fmt.Errorf("failed to write wal file :%w", err)
	}
	return nil
}

func (mt *MemTable) Get(key types.Key) (*internalpb.Entry, bool) {
	mt.mu.RLock()
	defer mt.mu.RUnlock()
	return mt.skiplist.Get(key)
}

func (mt *MemTable) Scan(start, end types.Key) []*internalpb.Entry {
	mt.mu.RLock()
	defer mt.mu.RUnlock()
	return mt.skiplist.Scan(start, end)
}

func (mt *MemTable) All() []*internalpb.Entry {
	mt.mu.RLock()
	defer mt.mu.RUnlock()
	return mt.skiplist.All()
}

func (mt *MemTable) Size() int {
	mt.mu.RLock()
	defer mt.mu.RUnlock()
	return mt.skiplist.Size()
}

func (mt *MemTable) Freeze() {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	if err := mt.wal.Close(); err != nil {
		panic(err)
	}
	mt.readOnly = true
}

func (mt *MemTable) Reset() *MemTable {
	mt.mu.Lock()
	defer mt.mu.Unlock()

	l, err := mt.wal.Reset()
	if err != nil {
		panic(err)
	}
	return &MemTable{
		skiplist: mt.skiplist.Reset(),
		wal:      l,
		dir:      mt.dir,
		readOnly: false,
	}
}
