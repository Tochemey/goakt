package raft

import (
	"log"
	"sync"

	goaktpb "github.com/tochemey/goakt/internal/goakt/v1"
	goaktlog "github.com/tochemey/goakt/log"
	"go.etcd.io/etcd/raft/v3/raftpb"
	"go.etcd.io/etcd/server/v3/etcdserver/api/snap"
	"google.golang.org/protobuf/proto"
)

type commit struct {
	data       [][]byte
	applyDoneC chan<- struct{}
}

// WireActorsStore backed by raft
type WireActorsStore struct {
	proposeC    chan<- []byte // channel for proposing updates
	mu          sync.RWMutex
	kvStore     *goaktpb.WireActors // current committed key-value pairs
	snapshotter *snap.Snapshotter
	log         *goaktlog.Log
}

// NewWireActorsStore creates an instance of WireActorsStore
func NewWireActorsStore(snapshotter *snap.Snapshotter, proposeC chan<- []byte, commitC <-chan *commit, errorC <-chan error, log *goaktlog.Log) *WireActorsStore {
	// create an instance of the WireActorsStore
	s := &WireActorsStore{
		proposeC:    proposeC,
		kvStore:     new(goaktpb.WireActors),
		snapshotter: snapshotter,
		log:         log,
	}
	snapshot, err := s.loadSnapshot()
	if err != nil {
		s.log.Panic(err)
	}
	if snapshot != nil {
		s.log.Infof("loading snapshot at term %d and index %d", snapshot.Metadata.Term, snapshot.Metadata.Index)
		if err := s.recoverFromSnapshot(snapshot.Data); err != nil {
			s.log.Panic(err)
		}
	}
	// read commits from raft into kvStore map until error
	go s.readCommits(commitC, errorC)
	return s
}

// Lookup looks for a given actor in the cluster storage
func (s *WireActorsStore) Lookup(actorName string) (*goaktpb.WireActor, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.kvStore.GetActors()[actorName]
	return v, ok
}

// Propose persists a given actor on the storage
func (s *WireActorsStore) Propose(actor *goaktpb.WireActor) {
	// get the byte array
	bytea, err := proto.Marshal(actor)
	// handle the error
	if err != nil {
		log.Fatal(err)
	}
	s.proposeC <- bytea
}

func (s *WireActorsStore) readCommits(commitC <-chan *commit, errorC <-chan error) {
	for commit := range commitC {
		if commit == nil {
			// signaled to load snapshot
			snapshot, err := s.loadSnapshot()
			if err != nil {
				log.Panic(err)
			}
			if snapshot != nil {
				s.log.Infof("loading snapshot at term %d and index %d", snapshot.Metadata.Term, snapshot.Metadata.Index)
				if err := s.recoverFromSnapshot(snapshot.Data); err != nil {
					log.Panic(err)
				}
			}
			continue
		}

		for _, data := range commit.data {
			dataKv := new(goaktpb.WireActor)
			if err := proto.Unmarshal(data, dataKv); err != nil {
				s.log.Fatalf("failed to decode message (%v)", err)
			}
			s.mu.Lock()
			s.kvStore.GetActors()[dataKv.GetActorName()] = dataKv
			s.mu.Unlock()
		}
		close(commit.applyDoneC)
	}
	if err, ok := <-errorC; ok {
		s.log.Fatal(err)
	}
}

func (s *WireActorsStore) GetSnapshot() ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return proto.Marshal(s.kvStore)
}

func (s *WireActorsStore) loadSnapshot() (*raftpb.Snapshot, error) {
	snapshot, err := s.snapshotter.Load()
	if err == snap.ErrNoSnapshot {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s *WireActorsStore) recoverFromSnapshot(snapshot []byte) error {
	store := new(goaktpb.WireActors)
	if err := proto.Unmarshal(snapshot, store); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kvStore = store
	return nil
}
