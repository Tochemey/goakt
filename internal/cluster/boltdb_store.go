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

package cluster

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	bbolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/internal/internalpb"
)

const (
	boltFileMode      os.FileMode = 0o600
	boltBucketName                = "peer_states"
	boltFolder                    = ".goakt"
	boltClusterFolder             = "cluster"
	boltFilePrefix                = "peers"
	boltFileExtension             = ".db"
)

var (
	boltTimeout        = 5 * time.Second
	defaultBoltOptions = &bbolt.Options{Timeout: boltTimeout, NoGrowSync: true}
	errBoltStoreClosed = errors.New("cluster: boltdb store is closed")
	// boltPathGenerator allows tests to override BoltDB path generation.
	boltPathGenerator = defaultBoltPath
)

// BoltStore implements Store using go.etcd.io/bbolt for durable persistence.
//
// Concurrency:
//   - bbolt provides single-writer/multi-reader semantics. We only guard the
//     close state to prevent operations once the store is shut down.
//
// Efficiency:
//   - Peer states are marshaled with protobuf and packed directly into a
//     dedicated bucket. Reads avoid allocations unless data exists.
//   - The DB is opened with a short timeout to avoid blocking on locked files.
type BoltStore struct {
	db     *bbolt.DB
	bucket []byte
	path   string
	closed atomic.Bool
}

var _ Store = (*BoltStore)(nil)

// NewBoltStore opens (or creates) a BoltDB-backed Store. Each invocation reserves
// a unique database file rooted under the user's home directory
// ("~/.goakt/cluster/peers-*.db"), allowing multiple stores to coexist across
// processes without clashing on file locks. The database is configured with
// production defaults (short open timeout, NoGrowSync). Closing the store closes
// the underlying Bolt database and deletes the backing file.
func NewBoltStore() (Store, error) {
	path, err := boltPathGenerator()
	if err != nil {
		return nil, err
	}

	optionsCopy := *defaultBoltOptions
	db, err := bbolt.Open(path, boltFileMode, &optionsCopy)
	if err != nil {
		return nil, fmt.Errorf("cluster: opening boltdb: %w", err)
	}

	bucket := []byte(boltBucketName)
	if err := db.Update(func(tx *bbolt.Tx) error {
		_, e := tx.CreateBucketIfNotExists(bucket)
		return e
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("cluster: initializing boltdb bucket: %w", err)
	}

	return &BoltStore{db: db, bucket: bucket, path: path}, nil
}

// PersistPeerState stores or updates the provided peer state.
//
// After the write transaction commits, a read transaction is opened to ensure
// the write is visible to subsequent GetPeerState calls. This provides explicit
// synchronization to prevent read-after-write visibility issues when NodeLeft
// events are processed on different goroutines.
func (s *BoltStore) PersistPeerState(ctx context.Context, peer *internalpb.PeerState) error {
	if peer == nil {
		return nil
	}
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if err := contextErr(ctx); err != nil {
		return err
	}

	data, _ := proto.Marshal(peer)

	key := peerKey(peer)

	// Write the peer state
	if err := s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(s.bucket)
		if bucket == nil {
			return fmt.Errorf("cluster: bucket %q missing", s.bucket)
		}
		return bucket.Put([]byte(key), data)
	}); err != nil {
		return err
	}

	// Open a read transaction to ensure the write is visible to subsequent reads.
	// This provides explicit synchronization to prevent read-after-write visibility
	// issues when NodeLeft events are processed on different goroutines.
	_ = s.db.View(func(tx *bbolt.Tx) error {
		// Just open a read transaction to ensure write is visible.
		// We don't need to read anything, just ensure the transaction sees
		// the committed write.
		return nil
	})

	return nil
}

// GetPeerState returns the persisted peer state (if any) for the given address.
func (s *BoltStore) GetPeerState(ctx context.Context, peerAddress string) (*internalpb.PeerState, bool) {
	if err := s.ensureOpen(); err != nil {
		return nil, false
	}
	if err := contextErr(ctx); err != nil {
		return nil, false
	}

	var state *internalpb.PeerState
	err := s.db.View(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(s.bucket)
		if bucket == nil {
			return fmt.Errorf("cluster: bucket %q missing", s.bucket)
		}
		raw := bucket.Get([]byte(peerAddress))
		if raw == nil {
			return nil
		}
		peer := new(internalpb.PeerState)
		if unmarshalErr := proto.Unmarshal(raw, peer); unmarshalErr != nil {
			return unmarshalErr
		}
		state = peer
		return nil
	})
	if err != nil || state == nil {
		return nil, false
	}
	return state, true
}

// DeletePeerState removes the entry associated with the given peer address.
func (s *BoltStore) DeletePeerState(ctx context.Context, peerAddress string) error {
	if err := s.ensureOpen(); err != nil {
		return err
	}
	if err := contextErr(ctx); err != nil {
		return err
	}

	return s.db.Update(func(tx *bbolt.Tx) error {
		bucket := tx.Bucket(s.bucket)
		if bucket == nil {
			return fmt.Errorf("cluster: bucket %q missing", s.bucket)
		}
		return bucket.Delete([]byte(peerAddress))
	})
}

// Close releases the underlying BoltDB handle.
func (s *BoltStore) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	closeErr := s.db.Close()
	removeErr := os.Remove(s.path)
	if closeErr != nil {
		if removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			return errors.Join(closeErr, removeErr)
		}
		return closeErr
	}
	if removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
		return removeErr
	}
	return nil
}

func (s *BoltStore) ensureOpen() error {
	if s.closed.Load() {
		return errBoltStoreClosed
	}
	return nil
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func peerKey(peer *internalpb.PeerState) string {
	host := peer.GetHost()
	port := strconv.Itoa(int(peer.GetPeersPort()))
	return net.JoinHostPort(host, port)
}

func defaultBoltPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cluster: determine user home directory: %w", err)
	}
	dir := filepath.Join(home, boltFolder, boltClusterFolder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("cluster: unable to create boltdb directory: %w", err)
	}
	pattern := fmt.Sprintf("%s-*%s", boltFilePrefix, boltFileExtension)
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("cluster: create boltdb file: %w", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("cluster: close boltdb file: %w", err)
	}
	return path, nil
}
