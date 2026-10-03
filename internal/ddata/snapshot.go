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
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v4/internal/internalpb"
)

const (
	bucketName                 = "crdt_snapshots"
	metaBucketName             = "crdt_snapshot_meta"
	lastContactKey             = "last_contact"
	sinceKey                   = "since"
	fileMode       os.FileMode = 0o600
	fileName                   = "crdt-snapshot.db"
)

var (
	timeout        = 5 * time.Second
	defaultOptions = &bbolt.Options{Timeout: timeout, NoGrowSync: true}

	// ErrStoreClosed is returned when an operation is attempted on a closed store.
	ErrStoreClosed = errors.New("crdt: snapshot store is closed")
)

// Store persists CRDT state to BoltDB for durable recovery.
// The store is a raw byte-storage layer: it accepts and returns pre-encoded
// CRDTSnapshotEntry protobuf messages. All CRDT encoding/decoding is the
// responsibility of the caller (the replicator).
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
		if _, e := tx.CreateBucketIfNotExists(bucket); e != nil {
			return e
		}

		_, e := tx.CreateBucketIfNotExists([]byte(metaBucketName))
		return e
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("crdt: initializing snapshot bucket: %w", err)
	}

	return &Store{db: db, bucket: bucket, path: path}, nil
}

// Save persists the pre-encoded CRDT snapshot entries to BoltDB in one
// transaction with two times of the node: the last time it heard from a peer
// Replicator, and the time from which it knows every deletion. Each entry is
// keyed by its CRDT key ID. A node restored from the snapshot compares the
// last contact with the tombstone TTL to know whether the deletions made
// while it was away may have expired. A zero time is saved as none.
func (s *Store) Save(entries map[string]*internalpb.CRDTSnapshotEntry, lastContact, since time.Time) error {
	if s.closed.Load() {
		return ErrStoreClosed
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(s.bucket)
		if bucket == nil {
			return fmt.Errorf("crdt: snapshot bucket %q missing", bucketName)
		}

		meta := tx.Bucket([]byte(metaBucketName))
		if meta == nil {
			return fmt.Errorf("crdt: snapshot bucket %q missing", metaBucketName)
		}

		if err := meta.Put([]byte(lastContactKey), encodeTime(lastContact)); err != nil {
			return err
		}

		if err := meta.Put([]byte(sinceKey), encodeTime(since)); err != nil {
			return err
		}

		// clear existing entries
		cursor := bucket.Cursor()
		for k, _ := cursor.First(); k != nil; k, _ = cursor.Next() {
			if err := cursor.Delete(); err != nil {
				return err
			}
		}

		// write current state
		for keyID, entry := range entries {
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

// Load restores the pre-encoded CRDT snapshot entries from BoltDB.
// Returns an empty map if no snapshot exists.
func (s *Store) Load() (map[string]*internalpb.CRDTSnapshotEntry, error) {
	if s.closed.Load() {
		return nil, ErrStoreClosed
	}

	entries := make(map[string]*internalpb.CRDTSnapshotEntry)

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
			entries[string(k)] = entry
			return nil
		})
	})

	if err != nil {
		return nil, err
	}

	return entries, nil
}

// Contact returns the last contact and the since saved by Save. A time that
// was zero, or that was not saved, as in a snapshot written by a version that
// predates it, is returned as the zero time.
func (s *Store) Contact() (lastContact, since time.Time, err error) {
	if s.closed.Load() {
		return time.Time{}, time.Time{}, ErrStoreClosed
	}

	err = s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket([]byte(metaBucketName))
		if bucket == nil {
			return nil
		}

		if lastContact, err = decodeTime(bucket.Get([]byte(lastContactKey))); err != nil {
			return fmt.Errorf("crdt: last contact: %w", err)
		}

		if since, err = decodeTime(bucket.Get([]byte(sinceKey))); err != nil {
			return fmt.Errorf("crdt: since: %w", err)
		}

		return nil
	})

	return lastContact, since, err
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

// encodeTime encodes a time as its Unix nanoseconds in eight big-endian
// bytes. The zero time, which has no Unix nanoseconds, is encoded as zero.
func encodeTime(t time.Time) []byte {
	var nanos int64
	if !t.IsZero() {
		nanos = t.UnixNano()
	}

	raw := make([]byte, 8)
	binary.BigEndian.PutUint64(raw, uint64(nanos))
	return raw
}

// decodeTime decodes a time encoded by encodeTime. Nil and zero decode as the
// zero time; any length other than eight bytes is malformed.
func decodeTime(raw []byte) (time.Time, error) {
	if raw == nil {
		return time.Time{}, nil
	}

	if len(raw) != 8 {
		return time.Time{}, fmt.Errorf("malformed time of %d bytes", len(raw))
	}

	nanos := int64(binary.BigEndian.Uint64(raw))
	if nanos == 0 {
		return time.Time{}, nil
	}

	return time.Unix(0, nanos), nil
}
