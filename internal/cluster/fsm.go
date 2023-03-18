package cluster

import (
	"bytes"
	"io"
	"sync"

	"github.com/pkg/errors"
	goaktpb "github.com/tochemey/goakt/internal/goaktpb/v1"
	"github.com/tochemey/goakt/log"
	"google.golang.org/protobuf/proto"
)

// FSM represents the Finite State Machine of the multi-raft cluster
type FSM struct {
	mu     sync.Mutex
	state  *goaktpb.FsmState
	logger log.Logger
}

// NewFSM creates a new instance of the FSM
func NewFSM(logger log.Logger) *FSM {
	return &FSM{
		state: &goaktpb.FsmState{
			Actors: make(map[string]*goaktpb.WireActor),
		},
		logger: logger,
	}
}

// Apply committed raft log entry.
func (s *FSM) Apply(data []byte) {
	// create an instance of proto message
	wireActor := new(goaktpb.WireActor)
	// let us unpack the byte array
	if err := proto.Unmarshal(data, wireActor); err != nil {
		// log the error and return
		s.logger.Error(errors.Wrap(err, "failed to unmarshal fsm command"))
		return
	}
	// let us acquire the lock to set the FSM state and release the lock once done
	s.mu.Lock()
	actors := s.state.GetActors()
	actors[wireActor.GetActorName()] = wireActor
	s.state.Actors = actors
	s.mu.Unlock()
}

// Snapshot is used to write the current state to a snapshot file,
// on stable storage and compacting the raft logs.
func (s *FSM) Snapshot() (io.ReadCloser, error) {
	// acquire the lock
	s.mu.Lock()
	// release of the lock once done
	defer s.mu.Unlock()
	// let us marshal the state
	bytea, err := proto.Marshal(s.state)
	// return the error
	if err != nil {
		return nil, errors.Wrap(err, "failed to marshal the FSM state")
	}
	// return the marshaled FSM state
	return io.NopCloser(bytes.NewReader(bytea)), nil
}

// Restore is used to restore state machine from a snapshot.
func (s *FSM) Restore(r io.ReadCloser) error {
	// acquire the lock
	s.mu.Lock()
	// release of the lock once done
	defer s.mu.Unlock()
	// let us read the bytes data
	bytea, err := io.ReadAll(r)
	// return the error in case there is one
	if err != nil {
		return errors.Wrap(err, "failed to read FSM snapshot state")
	}
	// create an instance of the FsmState
	state := new(goaktpb.FsmState)
	// let us unmarshal the bytes array
	if err := proto.Unmarshal(bytea, state); err != nil {
		return errors.Wrap(err, "failed to unmarshal the FSM snapshot state")
	}
	// set the state
	s.state = state
	return nil
}

// Read returns the state entry given the key which here is the actor  name
func (s *FSM) Read(actorName string) *goaktpb.WireActor {
	// acquire the lock
	s.mu.Lock()
	actors := s.state.GetActors()
	wireActor := actors[actorName]
	// release the lock
	s.mu.Unlock()
	return wireActor
}
