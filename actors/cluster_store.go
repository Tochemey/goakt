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

package actors

import (
	"net"
	"strconv"

	"google.golang.org/protobuf/proto"

	"github.com/tochemey/goakt/v2/internal/internalpb"
	"github.com/tochemey/goakt/v2/internal/logsm"
	"github.com/tochemey/goakt/v2/internal/size"
	"github.com/tochemey/goakt/v2/log"
)

type clusterStore struct {
	store  *logsm.LogSM
	logger log.Logger
}

// newClusterStore creates an instance of clusterStore
// TODO: add custom options. At the moment the default should be enough
func newClusterStore(dir string, logger log.Logger) (*clusterStore, error) {
	store, err := logsm.Open(dir,
		logsm.WithLogger(logger),
		logsm.WithL0TargetNum(5),
		logsm.WithLevelRatio(10),
		logsm.WithProbability(5),
		logsm.WithMemTableSizeThreshold(20*size.MB), // After 20MB of data in memory it will be persisted to disk
		logsm.WithDataBlockByteThreshold(4*size.KB),
		logsm.WithSkipListMaxLevel(9),
	)

	if err != nil {
		return nil, err
	}

	return &clusterStore{
		store:  store,
		logger: logger,
	}, nil
}

// set adds a peer to the cache
func (s *clusterStore) set(peer *internalpb.PeerState) error {
	peerAddress := net.JoinHostPort(peer.GetHost(), strconv.Itoa(int(peer.GetPeersPort())))
	value, _ := proto.Marshal(peer)
	return s.store.Set(peerAddress, value)
}

// get retrieve a peer from the cache
func (s *clusterStore) get(peerAddress string) (*internalpb.PeerState, bool) {
	value, ok := s.store.Get(peerAddress)
	if !ok {
		return nil, false
	}

	peer := new(internalpb.PeerState)
	// this scenario may never occur unless the logsm store is corrupt,
	// but it is good to cater for it as good programming pattern
	if err := proto.Unmarshal(value, peer); err != nil {
		s.logger.Errorf("failed to unmarshal peer state: %v", err)
		return nil, false
	}

	return peer, ok
}

// remove deletes a peer from the cache
func (s *clusterStore) remove(peerAddress string) error {
	return s.store.Delete(peerAddress)
}

// close resets the cache
func (s *clusterStore) close() {
	s.store.Close()
}
