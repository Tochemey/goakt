package store

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/pkg/errors"
	clientv3 "go.etcd.io/etcd/client/v3"
)

const (
	// livenessKeyPrefix is the prefix in store where peers publish
	// their liveness information.
	livenessKeyPrefix = "alive/"
)

// IsPeerRunning checks whether the given peer is up running
func (s *Store) IsPeerRunning(ctx context.Context, peerID string, timeout time.Duration) (int, bool) {
	// create a cancellation context
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// build the lookup key
	key := fmt.Sprintf("%s%s", livenessKeyPrefix, peerID)
	// retrieve the value associated with the key
	resp, err := s.Get(ctx, key)
	// handle the error
	if err != nil {
		s.logger.Error(errors.Wrapf(err, "failed to get Peer=%s", peerID))
		return 0, false
	}

	// we only expect a single result given the peer id
	if resp.Count == 1 {
		// parse the peer id
		pid, err := strconv.Atoi(string(resp.Kvs[0].Value))
		// handle the parsing error
		if err != nil {
			s.logger.Error(errors.Wrap(err, "failed to parse peer id"))
			return 0, false
		}
		return pid, true
	}
	return 0, false
}

// broadcastLiveness inform the cluster about this node liveness
func (s *Store) broadcastLiveness(ctx context.Context, timeout time.Duration) error {
	// publish liveness of this instance into the store
	key := fmt.Sprintf("%s%s", livenessKeyPrefix, s.name)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err := s.Put(ctx, key, strconv.Itoa(os.Getpid()), clientv3.WithLease(s.Session.Lease()))
	return err
}

// revokeLiveness informs the cluster that the given node is no longer running
// this function is invoked during graceful shutdown
func (s *Store) revokeLiveness(ctx context.Context, timeout time.Duration) error {
	// publish liveness of this instance into the store
	key := fmt.Sprintf("%s%s", livenessKeyPrefix, s.name)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err := s.Delete(ctx, key)
	return err
}
