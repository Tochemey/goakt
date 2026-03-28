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

package ddata

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/crdt"
	"github.com/tochemey/goakt/v4/internal/codec"
	"github.com/tochemey/goakt/v4/internal/internalpb"
)

const (
	bucketName             = "crdt_snapshots"
	fileMode   os.FileMode = 0o600
	fileName               = "crdt-snapshot.db"
)

var (
	timeout        = 5 * time.Second
	defaultOptions = &bbolt.Options{Timeout: timeout, NoGrowSync: true}

	// ErrStoreClosed is returned when an operation is attempted on a closed store.
	ErrStoreClosed = errors.New("crdt: snapshot store is closed")
)

// Store persists CRDT state to BoltDB for durable recovery.
type Store struct {
	db     *bbolt.DB
	bucket []byte
	path   string
	closed atomic.Bool
}

// NewStore opens or creates a BoltDB-backed snapshot store
// in the given directory.
func NewStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("crdt: create snapshot directory: %w", err)
	}

	path := filepath.Join(dir, fileName)
	optionsCopy := *defaultOptions
	db, err := bbolt.Open(path, fileMode, &optionsCopy)
	if err != nil {
		return nil, fmt.Errorf("crdt: opening snapshot db: %w", err)
	}

	bucket := []byte(bucketName)
	if err := db.Update(func(tx *bbolt.Tx) error {
		_, e := tx.CreateBucketIfNotExists(bucket)
		return e
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("crdt: initializing snapshot bucket: %w", err)
	}

	return &Store{db: db, bucket: bucket, path: path}, nil
}

// Save persists the entire CRDT store to BoltDB.
// Each key is stored as a serialized CRDTSnapshotEntry.
func (s *Store) Save(store map[string]crdt.ReplicatedData, keyTypes map[string]crdt.DataType, versions map[string]uint64) error {
	if s.closed.Load() {
		return ErrStoreClosed
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(s.bucket)
		if bucket == nil {
			return fmt.Errorf("crdt: snapshot bucket %q missing", s.bucket)
		}

		// clear existing entries
		cursor := bucket.Cursor()
		for k, _ := cursor.First(); k != nil; k, _ = cursor.Next() {
			if err := bucket.Delete(k); err != nil {
				return err
			}
		}

		// write current state
		for keyID, data := range store {
			dataType, ok := keyTypes[keyID]
			if !ok {
				return fmt.Errorf("crdt: missing data type for key=%s", keyID)
			}
			version, ok := versions[keyID]
			if !ok {
				return fmt.Errorf("crdt: missing version for key=%s", keyID)
			}

			pbData, err := codec.EncodeCRDTData(data)
			if err != nil {
				return fmt.Errorf("crdt: encode snapshot for key=%s: %w", keyID, err)
			}

			entry := &internalpb.CRDTSnapshotEntry{
				Key:     codec.EncodeCRDTKey(keyID, dataType),
				Data:    pbData,
				Version: version,
			}

			raw, err := proto.Marshal(entry)
			if err != nil {
				return fmt.Errorf("crdt: marshal snapshot entry for key=%s: %w", keyID, err)
			}

			if err := bucket.Put([]byte(keyID), raw); err != nil {
				return err
			}
		}

		return nil
	})
}

// Load restores the CRDT store from BoltDB.
// Returns empty maps if no snapshot exists.
func (s *Store) Load() (map[string]crdt.ReplicatedData, map[string]crdt.DataType, map[string]uint64, error) {
	if s.closed.Load() {
		return nil, nil, nil, ErrStoreClosed
	}

	store := make(map[string]crdt.ReplicatedData)
	keyTypes := make(map[string]crdt.DataType)
	versions := make(map[string]uint64)

	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(s.bucket)
		if bucket == nil {
			return nil
		}

		return bucket.ForEach(func(k, v []byte) error {
			entry := new(internalpb.CRDTSnapshotEntry)
			if err := proto.Unmarshal(v, entry); err != nil {
				return fmt.Errorf("crdt: unmarshal snapshot entry: %w", err)
			}

			keyID, dataType, err := codec.DecodeCRDTKey(entry.GetKey())
			if err != nil {
				return fmt.Errorf("crdt: decode snapshot key: %w", err)
			}
			data, err := codec.DecodeCRDTData(entry.GetData())
			if err != nil {
				return fmt.Errorf("crdt: decode snapshot data for key=%s: %w", keyID, err)
			}

			store[keyID] = data
			keyTypes[keyID] = dataType
			versions[keyID] = entry.GetVersion()
			return nil
		})
	})

	if err != nil {
		return nil, nil, nil, err
	}

	return store, keyTypes, versions, nil
}

// Close releases the underlying BoltDB handle without removing the snapshot file.
// Use Remove to delete the snapshot file when it is no longer needed.
func (s *Store) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	return s.db.Close()
}

// Remove deletes the snapshot file from disk.
// The store must be closed before calling Remove.
func (s *Store) Remove() error {
	if !s.closed.Load() {
		return fmt.Errorf("crdt: cannot remove snapshot file: store is still open")
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// EnsureOpen returns an error if the store has been closed.
func (s *Store) EnsureOpen() error {
	if s.closed.Load() {
		return ErrStoreClosed
	}
	return nil
}
