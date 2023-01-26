package cluster

import (
	"bytes"
	"encoding/gob"
	"sync"

	"github.com/hashicorp/memberlist"
	"github.com/tochemey/goakt/log"
	pb "github.com/tochemey/goakt/pb/goakt/v1"
	"google.golang.org/protobuf/proto"
)

// Storage is the Delegate invoked by memberlist during gossiping to sync config across members
type Storage struct {
	mu         sync.Mutex
	broadcasts *memberlist.TransmitLimitedQueue
	// node internal state - this is the actual nodeState being gossiped
	nodeState *pb.NodeState
	// useful to share node details with other nodes
	metadata map[string]string
	logger   log.Logger
}

var _ memberlist.Delegate = &Storage{}

// NewStorage creates an instance of Storage
func NewStorage(md map[string]string, logger log.Logger) *Storage {
	return &Storage{
		mu:         sync.Mutex{},
		broadcasts: nil, // TODO come back to this
		nodeState:  &pb.NodeState{},
		metadata:   md,
		logger:     logger,
	}
}

// NodeMeta is used to retrieve meta-data about the current node
// when broadcasting an alive message. Its length is limited to
// the given byte size. This metadata is available in the Node structure.
func (s *Storage) NodeMeta(limit int) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	var network bytes.Buffer
	encoder := gob.NewEncoder(&network)
	err := encoder.Encode(s.metadata)
	if err != nil {
		s.logger.Fatal("failed to encode metadata", err)
	}
	return network.Bytes()
}

// NotifyMsg is called when a user-data message is received.
// Care should be taken that this method does not block, since doing
// so would block the entire UDP packet receive loop. Additionally, the byte
// slice may be modified after the call returns, so it should be copied if needed
func (s *Storage) NotifyMsg(b []byte) {
	// not expecting messages - push/pull sync should suffice
}

// GetBroadcasts is called when user data messages can be broadcast.
// It can return a list of buffers to send. Each buffer should assume an
// overhead as provided with a limit on the total byte size allowed.
// The total byte size of the resulting data to send must not exceed
// the limit. Care should be taken that this method does not block,
// since doing so would block the entire UDP packet receive loop.
func (s *Storage) GetBroadcasts(overhead, limit int) [][]byte {
	return s.broadcasts.GetBroadcasts(overhead, limit)
}

// LocalState is used for a TCP Push/Pull. This is sent to
// the remote side in addition to the membership information. Any
// data can be sent here. See MergeRemoteState as well. The `join`
// boolean indicates this is for a join instead of a push/pull.
func (s *Storage) LocalState(join bool) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()

	// let us marshal the node state
	bytes, err := proto.Marshal(s.nodeState)
	if err != nil {
		s.logger.Fatal("failed to encode local state", err)
	}
	return bytes
}

// MergeRemoteState is invoked after a TCP Push/Pull. This is the
// state received from the remote side and is the result of the
// remote side's LocalState call. The 'join'
// boolean indicates this is for a join instead of a push/pull.
func (s *Storage) MergeRemoteState(buf []byte, join bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// unmarshal the bytes array
	nodeState := new(pb.NodeState)
	if err := proto.Unmarshal(buf, nodeState); err != nil {
		s.logger.Fatal("failed to decode remote state", err)
	}

	// let us update the local node state
	for key, meta := range nodeState.GetStates() {
		if !proto.Equal(s.nodeState.GetStates()[key], meta) {
			s.nodeState.States[key] = meta
		}
	}
	s.logger.Debug("successfully merged remote state.")
}

// Put adds config property to config store
func (s *Storage) Put(key string, value *pb.ActorMeta) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.nodeState.States[key] = value
}

// Get returns a property value
func (s *Storage) Get(key string) *pb.ActorMeta {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.nodeState.GetStates()[key]
}
