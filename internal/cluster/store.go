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

package cluster

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"time"

	"github.com/dgraph-io/badger/v4"
	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v3/internal/internalpb"
	"github.com/tochemey/goakt/v3/internal/registry"
	"github.com/tochemey/goakt/v3/internal/ticker"
	"github.com/tochemey/goakt/v3/log"
)

type Store struct {
	db       *badger.DB
	logger   log.Logger
	stopSig  chan registry.Unit
	inmemory bool
}

// NewStore creates an instance of Store
func NewStore(logger log.Logger, dir *string) (*Store, error) {
	dbOpts := badger.
		DefaultOptions("").
		WithInMemory(true).
		WithLogger(nil)

	if dir != nil {
		dbOpts = badger.
			DefaultOptions(*dir).
			WithLogger(nil)
	}

	// open the database
	db, err := badger.Open(dbOpts)
	if err != nil {
		return nil, err
	}

	// create the store instance
	s := &Store{
		db:       db,
		logger:   logger,
		stopSig:  make(chan registry.Unit, 1),
		inmemory: dir == nil,
	}

	// run the garbage collector when not running in memory
	if !s.inmemory {
		s.runGC()
	}

	return s, nil
}

// PersistPeerState adds a peer to the cache
func (s *Store) PersistPeerState(peer *internalpb.PeerState) error {
	peerAddress := net.JoinHostPort(peer.GetHost(), strconv.Itoa(int(peer.GetPeersPort())))
	value, _ := proto.Marshal(peer)

	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set([]byte(peerAddress), value)
	})
}

// GetPeerState retrieve a peer from the cache
func (s *Store) GetPeerState(peerAddress string) (*internalpb.PeerState, bool) {
	var value []byte

	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(peerAddress))
		if err != nil {
			if errors.Is(err, badger.ErrKeyNotFound) {
				s.logger.Warn(fmt.Sprintf("peer state not found for peer=(%s): %v", peerAddress, err))
				return err
			}
			s.logger.Error(fmt.Errorf("failed to get peer=(%s) state: %w", peerAddress, err))
			return err
		}

		return item.Value(func(val []byte) error {
			value = slices.Clone(val)
			return nil
		})
	})

	// no need to log error here because the error is already logged in the view function
	if err != nil {
		if errors.Is(err, badger.ErrKeyNotFound) {
			return nil, false
		}
		return nil, false
	}

	peer := new(internalpb.PeerState)
	if err := proto.Unmarshal(value, peer); err != nil {
		s.logger.Errorf("failed to unmarshal peer state for peer=(%s): %v", peerAddress, err)
		return nil, false
	}

	return peer, true
}

// DeletePeerState deletes a peer from the cache
func (s *Store) DeletePeerState(peerAddress string) error {
	return s.db.Update(func(txn *badger.Txn) error {
		err := txn.Delete([]byte(peerAddress))
		if err != nil {
			return err
		}
		return nil
	})
}

// Close resets the cache
func (s *Store) Close() error {
	close(s.stopSig)
	return s.db.Close()
}

// runGC runs the garbage collector
func (s *Store) runGC() {
	go func() {
		ticker := ticker.New(5 * time.Minute)
		ticker.Start()
		defer ticker.Stop()

		for {
			select {
			case <-ticker.Ticks:
				for {
					if err := s.db.RunValueLogGC(0.7); err != nil {
						if errors.Is(err, badger.ErrNoRewrite) {
							break
						}

						s.logger.Error(fmt.Errorf("failed to run value log GC: %w", err))
						break
					}
				}
			case <-s.stopSig:
				return
			}
		}
	}()
}
