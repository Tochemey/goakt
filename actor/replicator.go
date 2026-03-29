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

package actor

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strconv"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"

	"github.com/tochemey/goakt/v4/crdt"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/codec"
	"github.com/tochemey/goakt/v4/internal/ddata"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/metric"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	sup "github.com/tochemey/goakt/v4/supervisor"
)

// crdtConfigExtensionID is the unique identifier for the CRDT config extension.
const crdtConfigExtensionID = "goakt.crdt.config"

// crdtTopic is the well-known topic all Replicators subscribe to.
// Every delta is published to this single topic so that all peer
// Replicators receive it regardless of which keys they know about.
const crdtTopic = "goakt.crdt.deltas"

// schedule references for cancellation on stop.
const (
	antiEntropyScheduleRef = "goakt.crdt.anti-entropy"
	pruneScheduleRef       = "goakt.crdt.prune"
	snapshotScheduleRef    = "goakt.crdt.snapshot"
)

// internal message types for scheduled tasks.
type (
	antiEntropyTick struct{}
	pruneTick       struct{}
	snapshotTick    struct{}
)

// tombstone records that a key was deleted and when.
type tombstone struct {
	keyID     string
	dataType  crdt.DataType
	deletedAt time.Time
	deletedBy string
}

// crdtConfigExtension holds the CRDT configuration as an actor system extension.
// This ensures the config survives supervisor restarts — the Replicator reads
// it from the extension registry in PreStart rather than from a constructor argument.
type crdtConfigExtension struct {
	config *crdt.Config
}

// ID returns the unique extension identifier.
func (e *crdtConfigExtension) ID() string {
	return crdtConfigExtensionID
}

// Config returns the CRDT configuration.
func (e *crdtConfigExtension) Config() *crdt.Config {
	return e.config
}

// replicatorActor is a system actor that manages the local CRDT store
// and replicates state across the cluster via TopicActor pub/sub.
//
// Each node in the cluster runs its own replicatorActor. All replicators
// subscribe to a single shared topic (goakt.crdt.deltas) via the TopicActor.
// When any replicator updates a key, it publishes the delta (which carries
// the key inside the payload) to this shared topic. Because every replicator
// is subscribed to the same topic, they all receive the delta automatically.
type replicatorActor struct {
	pid                   *PID
	topicActor            *PID
	logger                log.Logger
	config                *crdt.Config
	nodeID                string
	store                 map[string]crdt.ReplicatedData
	keyTypes              map[string]crdt.DataType
	subscriptions         map[string]types.Unit
	watchers              map[string][]*PID
	tombstones            map[string]*tombstone
	versions              map[string]uint64
	msgSeq                atomic.Uint64
	actorSystem           ActorSystem
	clusterRef            cluster.Cluster
	remoting              remoteclient.Client
	snapshotStore         *ddata.Store
	mergeCount            atomic.Uint64
	deltaPublishCount     atomic.Uint64
	deltaReceiveCount     atomic.Uint64
	coordinatedWriteCount atomic.Uint64
	coordinatedReadCount  atomic.Uint64
	antiEntropyCount      atomic.Uint64
}

// enforce compilation error
var _ Actor = (*replicatorActor)(nil)

// newReplicatorActor creates a new replicatorActor instance.
// The actor reads its configuration from the crdtConfigExtension during PreStart.
func newReplicatorActor() *replicatorActor {
	return &replicatorActor{}
}

// PreStart initializes the replicator before message processing begins.
// All state is set up here so that supervisor restarts reinitialize correctly.
// The CRDT config is read from the crdtConfigExtension registered on the actor system.
// If snapshot persistence is configured, the store is restored from BoltDB.
func (r *replicatorActor) PreStart(ctx *Context) error {
	ext := ctx.Extension(crdtConfigExtensionID)
	if ext == nil {
		return fmt.Errorf("crdt config extension not found")
	}
	r.config = ext.(*crdtConfigExtension).Config()
	r.store = make(map[string]crdt.ReplicatedData)
	r.keyTypes = make(map[string]crdt.DataType)
	r.subscriptions = make(map[string]types.Unit)
	r.watchers = make(map[string][]*PID)
	r.tombstones = make(map[string]*tombstone)
	r.versions = make(map[string]uint64)
	r.logger = ctx.ActorSystem().Logger()
	r.actorSystem = ctx.ActorSystem()

	if err := r.restoreFromSnapshot(); err != nil {
		return err
	}

	return nil
}

// Receive handles messages sent to the replicator.
func (r *replicatorActor) Receive(ctx *ReceiveContext) {
	switch ctx.Message().(type) {
	case *PostStart:
		r.handlePostStart(ctx)
	default:
		r.handleMessage(ctx)
	}
}

// PostStop is called when the actor is shutting down.
func (r *replicatorActor) PostStop(ctx *Context) error {
	_ = ctx.ActorSystem().CancelSchedule(antiEntropyScheduleRef)
	_ = ctx.ActorSystem().CancelSchedule(pruneScheduleRef)
	_ = ctx.ActorSystem().CancelSchedule(snapshotScheduleRef)

	// persist final snapshot before shutdown
	if r.snapshotStore != nil {
		if err := r.snapshotStore.Save(r.store, r.keyTypes, r.versions); err != nil {
			return fmt.Errorf("failed to save final CRDT snapshot: %w", err)
		}

		if err := r.snapshotStore.Close(); err != nil {
			return fmt.Errorf("failed to close CRDT snapshot store: %w", err)
		}
	}

	r.logger.Infof("actor=%s stopped successfully", ctx.ActorName())
	return nil
}

// handlePostStart completes initialization that requires a running actor context.
// The PID, TopicActor reference, and topic subscription require the actor to be started.
func (r *replicatorActor) handlePostStart(ctx *ReceiveContext) {
	r.pid = ctx.Self()
	r.topicActor = ctx.ActorSystem().TopicActor()
	r.nodeID = r.pid.ID()
	scheduleCtx := context.WithoutCancel(ctx.Context())

	sys := ctx.ActorSystem().(*actorSystem)
	r.clusterRef = sys.getCluster()
	r.remoting = sys.getRemoting()

	// subscribe to the CRDT delta topic so this Replicator receives
	// deltas from all peer Replicators in the cluster.
	if r.topicActor != nil {
		ctx.Tell(r.topicActor, NewSubscribe(crdtTopic))
	}

	// start anti-entropy schedule
	if r.config.AntiEntropyInterval() > 0 {
		if err := ctx.ActorSystem().Schedule(
			scheduleCtx,
			&antiEntropyTick{},
			r.pid,
			r.config.AntiEntropyInterval(),
			WithReference(antiEntropyScheduleRef),
		); err != nil {
			r.logger.Errorf("failed to schedule anti-entropy: %v", err)
			ctx.Err(err)
			return
		}
	}

	// start prune schedule for tombstone and departed node cleanup
	if r.config.PruneInterval() > 0 {
		if err := ctx.ActorSystem().Schedule(
			scheduleCtx,
			&pruneTick{},
			r.pid,
			r.config.PruneInterval(),
			WithReference(pruneScheduleRef),
		); err != nil {
			r.logger.Errorf("failed to schedule prune: %v", err)
			ctx.Err(err)
			return
		}
	}

	// start snapshot schedule if configured
	if r.snapshotStore != nil && r.config.SnapshotInterval() > 0 {
		if err := ctx.ActorSystem().Schedule(
			scheduleCtx,
			&snapshotTick{},
			r.pid,
			r.config.SnapshotInterval(),
			WithReference(snapshotScheduleRef),
		); err != nil {
			r.logger.Errorf("failed to schedule snapshot: %v", err)
			ctx.Err(err)
			return
		}
	}

	r.logger.Infof("actor=%s started successfully", r.pid.Name())
}

// handleMessage dispatches CRDT commands to the appropriate handler.
func (r *replicatorActor) handleMessage(ctx *ReceiveContext) {
	switch msg := ctx.Message().(type) {
	case updateCommand:
		r.handleUpdate(ctx, msg)
	case getCommand:
		r.handleGet(ctx, msg)
	case subscribeCommand:
		r.handleSubscribe(ctx, msg)
	case unsubscribeCommand:
		r.handleUnsubscribe(ctx, msg)
	case deleteCommand:
		r.handleDelete(ctx, msg)
	case *Terminated:
		r.handleTerminated(msg)
	case *internalpb.CRDTDelta:
		r.handleProtoDelta(ctx, msg)
	case *crdtDelta:
		r.handleDelta(ctx, msg)
	case *internalpb.CRDTTombstone:
		r.handleProtoTombstone(msg)
	case *internalpb.CRDTReadRequest:
		r.handleReadRequest(ctx, msg)
	case *internalpb.CRDTDigest:
		r.handleDigest(ctx, msg)
	case *internalpb.CRDTFullState:
		r.handleFullState(ctx, msg)
	case *antiEntropyTick:
		r.handleAntiEntropy(ctx)
	case *pruneTick:
		r.handlePrune()
	case *snapshotTick:
		r.handleSnapshot()
	default:
		ctx.Unhandled()
	}
}

// handleUpdate applies a local CRDT mutation and publishes the delta.
func (r *replicatorActor) handleUpdate(ctx *ReceiveContext, msg updateCommand) {
	keyID := msg.KeyID()

	// reject updates to tombstoned keys
	if _, ok := r.tombstones[keyID]; ok {
		if ctx.Sender() != nil {
			ctx.Response(&crdt.UpdateResponse{})
		}
		return
	}

	current, exists := r.store[keyID]
	if !exists {
		current = msg.InitialValue()
		r.trackKey(keyID, msg.CRDTDataType())
	}

	updated := msg.Apply(current)
	delta := updated.Delta()
	updated.ResetDelta()
	r.store[keyID] = updated
	r.versions[keyID]++

	coordination := msg.WriteCoordination()
	if delta != nil {
		if coordination == 0 {
			r.publishDelta(ctx, keyID, msg.CRDTDataType(), delta)
		} else {
			r.coordinatedWrite(ctx, keyID, msg.CRDTDataType(), delta, coordination)
		}
	}

	r.notifyChanged(ctx, keyID, updated)

	if ctx.Sender() != nil {
		ctx.Response(&crdt.UpdateResponse{})
	}
}

// handleGet reads the current value of a CRDT key.
func (r *replicatorActor) handleGet(ctx *ReceiveContext, msg getCommand) {
	keyID := msg.KeyID()
	data := r.store[keyID]

	coordination := msg.ReadCoordination()
	if coordination != 0 {
		merged := r.coordinatedRead(ctx, keyID, data, coordination)
		if merged != nil {
			r.store[keyID] = merged
			data = merged
		}
	}

	ctx.Response(msg.Response(data))
}

// handleSubscribe registers an actor for change notifications on a key.
func (r *replicatorActor) handleSubscribe(ctx *ReceiveContext, msg subscribeCommand) {
	keyID := msg.KeyID()
	sender := ctx.Sender()
	if sender == nil {
		return
	}
	r.watchers[keyID] = append(r.watchers[keyID], sender)
	ctx.Watch(sender)
}

// handleUnsubscribe removes an actor from change notifications on a key.
func (r *replicatorActor) handleUnsubscribe(ctx *ReceiveContext, msg unsubscribeCommand) {
	keyID := msg.KeyID()
	sender := ctx.Sender()
	if sender == nil {
		return
	}
	r.removeWatcher(keyID, sender)
}

// handleTerminated removes a terminated actor from all watcher lists.
// This is triggered by the death watch set up in handleSubscribe.
func (r *replicatorActor) handleTerminated(msg *Terminated) {
	actorID := msg.ActorPath().String()
	for keyID, watchers := range r.watchers {
		for i, w := range watchers {
			if w.ID() == actorID {
				r.watchers[keyID] = append(watchers[:i], watchers[i+1:]...)
				break
			}
		}
		if len(r.watchers[keyID]) == 0 {
			delete(r.watchers, keyID)
		}
	}
}

// handleDelete removes a CRDT key from the local store and publishes a tombstone.
func (r *replicatorActor) handleDelete(ctx *ReceiveContext, msg deleteCommand) {
	keyID := msg.KeyID()

	dataType, hasType := r.keyTypes[keyID]
	delete(r.store, keyID)
	delete(r.versions, keyID)

	now := time.Now()
	r.tombstones[keyID] = &tombstone{
		keyID:     keyID,
		dataType:  dataType,
		deletedAt: now,
		deletedBy: r.nodeID,
	}

	if hasType {
		pb := &internalpb.CRDTTombstone{
			Key:            codec.EncodeCRDTKey(keyID, dataType),
			DeletedAtNanos: now.UnixNano(),
			DeletedByNode:  r.nodeID,
		}

		coordination := msg.WriteCoordination()
		if coordination != 0 {
			r.coordinatedTombstone(ctx, pb, coordination)
		}

		// always publish tombstone to peers via TopicActor
		if r.topicActor != nil {
			msgID := r.nodeID + ":del:" + strconv.FormatUint(r.msgSeq.Add(1), 10)
			ctx.Tell(r.topicActor, NewPublish(msgID, crdtTopic, pb))
		}
	}

	if ctx.Sender() != nil {
		ctx.Response(&crdt.DeleteResponse{})
	}
}

// handleProtoTombstone processes a tombstone received from a peer via TopicActor.
func (r *replicatorActor) handleProtoTombstone(msg *internalpb.CRDTTombstone) {
	if msg.GetDeletedByNode() == r.nodeID {
		return
	}

	keyID, dataType, err := codec.DecodeCRDTKey(msg.GetKey())
	if err != nil {
		r.logger.Warnf("tombstone: failed to decode key: %v", err)
		return
	}
	delete(r.store, keyID)
	delete(r.versions, keyID)

	r.tombstones[keyID] = &tombstone{
		keyID:     keyID,
		dataType:  dataType,
		deletedAt: time.Unix(0, msg.GetDeletedAtNanos()),
		deletedBy: msg.GetDeletedByNode(),
	}
}

// handleProtoDelta decodes a protobuf delta received from a peer replicator
// via TopicActor and delegates to handleDelta.
func (r *replicatorActor) handleProtoDelta(ctx *ReceiveContext, msg *internalpb.CRDTDelta) {
	d, err := decodeCRDTDelta(msg)
	if err != nil {
		r.logger.Warnf("failed to decode CRDT delta: %v", err)
		return
	}
	r.handleDelta(ctx, d)
}

// handleDelta merges a delta received from a peer replicator via TopicActor.
func (r *replicatorActor) handleDelta(ctx *ReceiveContext, msg *crdtDelta) {
	if msg.Origin == r.nodeID {
		return
	}

	r.deltaReceiveCount.Add(1)
	keyID := msg.KeyID

	// reject deltas for tombstoned keys
	if _, ok := r.tombstones[keyID]; ok {
		return
	}

	current, exists := r.store[keyID]
	if !exists {
		r.store[keyID] = msg.Delta
		r.trackKey(keyID, msg.DataType)
		r.versions[keyID]++
		r.notifyChanged(ctx, keyID, msg.Delta)
		return
	}

	merged := current.Merge(msg.Delta)
	r.store[keyID] = merged
	r.versions[keyID]++
	r.mergeCount.Add(1)
	r.notifyChanged(ctx, keyID, merged)
}

// handleAntiEntropy runs one round of anti-entropy by exchanging digests
// with a random peer Replicator.
func (r *replicatorActor) handleAntiEntropy(ctx *ReceiveContext) {
	if r.clusterRef == nil || r.remoting == nil {
		return
	}

	cctx := context.WithoutCancel(ctx.Context())
	peers, err := r.clusterRef.Peers(cctx)
	if err != nil {
		r.logger.Debugf("anti-entropy: failed to get cluster peers: %v", err)
		return
	}

	if len(peers) == 0 {
		return
	}

	// select a random peer
	peer := peers[rand.IntN(len(peers))] //nolint:gosec // cryptographic randomness not needed for peer selection
	actorName := reservedName(replicatorType)

	to, err := r.remoting.RemoteLookup(cctx, peer.Host, peer.RemotingPort, actorName)
	if err != nil {
		r.logger.Debugf("anti-entropy: failed to lookup peer replicator on %s:%d: %v", peer.Host, peer.RemotingPort, err)
		return
	}

	// build digest from local store
	digest := r.buildDigest()
	from := pathToAddress(r.pid.Path())
	if err := r.remoting.RemoteTell(cctx, from, to, digest); err != nil {
		r.logger.Debugf("anti-entropy: failed to send digest to %s:%d: %v", peer.Host, peer.RemotingPort, err)
	}
	r.antiEntropyCount.Add(1)
}

// handleDigest processes an anti-entropy digest from a peer.
// For keys where the peer is behind, we send our full state.
// For keys where we are behind, we request the peer's state.
func (r *replicatorActor) handleDigest(ctx *ReceiveContext, msg *internalpb.CRDTDigest) {
	// Build response: full state for keys where we are ahead or peer is missing.
	var entries []*internalpb.CRDTFullStateEntry

	peerVersions := make(map[string]uint64, len(msg.GetEntries()))
	for _, e := range msg.GetEntries() {
		keyID, _, err := codec.DecodeCRDTKey(e.GetKey())
		if err != nil {
			r.logger.Warnf("anti-entropy digest: failed to decode key: %v", err)
			continue
		}
		peerVersions[keyID] = e.GetVersion()
	}

	// Send state for keys where local version > peer version, or peer doesn't have the key.
	for keyID, data := range r.store {
		localVersion := r.versions[keyID]
		peerVersion, peerHas := peerVersions[keyID]
		if !peerHas || localVersion > peerVersion {
			dataType := r.keyTypes[keyID]
			pbData, err := codec.EncodeCRDTData(data)
			if err != nil {
				// TODO: decide what to do on encoding error. If we skip the key, the peer will never get it until the next anti-entropy round. For now we log and skip, but we may want to consider sending a tombstone or some other marker to trigger a retry on the next round.
				r.logger.Warnf("anti-entropy: failed to encode state for key=%s: %v", keyID, err)
				continue
			}
			entries = append(entries, &internalpb.CRDTFullStateEntry{
				Key:  codec.EncodeCRDTKey(keyID, dataType),
				Data: pbData,
			})
		}
	}

	if len(entries) > 0 && ctx.Sender() != nil {
		fullState := &internalpb.CRDTFullState{Entries: entries}
		ctx.Tell(ctx.Sender(), fullState)
	}
}

// handleFullState processes a full state response from a peer during anti-entropy.
func (r *replicatorActor) handleFullState(ctx *ReceiveContext, msg *internalpb.CRDTFullState) {
	for _, entry := range msg.GetEntries() {
		keyID, dataType, err := codec.DecodeCRDTKey(entry.GetKey())
		if err != nil {
			r.logger.Warnf("anti-entropy full state: failed to decode key: %v", err)
			continue
		}

		// skip tombstoned keys
		if _, ok := r.tombstones[keyID]; ok {
			continue
		}

		data, err := codec.DecodeCRDTData(entry.GetData())
		if err != nil {
			r.logger.Warnf("anti-entropy: failed to decode state for key=%s: %v", keyID, err)
			continue
		}

		current, exists := r.store[keyID]
		if !exists {
			r.store[keyID] = data
			r.trackKey(keyID, dataType)
			r.versions[keyID]++
			r.notifyChanged(ctx, keyID, data)
			continue
		}

		merged := current.Merge(data)
		r.store[keyID] = merged
		r.versions[keyID]++
		r.mergeCount.Add(1)
		r.notifyChanged(ctx, keyID, merged)
	}
}

// handlePrune cleans up expired tombstones, prunes departed node slots,
// and compacts CRDTs that implement Compactable.
func (r *replicatorActor) handlePrune() {
	now := time.Now()
	ttl := r.config.TombstoneTTL()

	// prune expired tombstones
	for keyID, ts := range r.tombstones {
		if now.Sub(ts.deletedAt) > ttl {
			delete(r.tombstones, keyID)
		}
	}

	// compact CRDTs that support it
	for keyID, data := range r.store {
		if c, ok := data.(crdt.Compactable); ok {
			r.store[keyID] = c.CompactData()
		}
	}
}

// buildDigest creates an anti-entropy digest from the local store.
// Pre-allocates contiguous slices to minimize heap allocations.
func (r *replicatorActor) buildDigest() *internalpb.CRDTDigest {
	n := len(r.store)
	entries := make([]*internalpb.CRDTDigestEntry, 0, n)
	entryBuf := make([]internalpb.CRDTDigestEntry, n)
	keyBuf := make([]internalpb.CRDTKey, n)

	i := 0
	for keyID := range r.store {
		dataType := r.keyTypes[keyID]
		keyBuf[i] = internalpb.CRDTKey{
			Id:       keyID,
			DataType: internalpb.CRDTDataType(dataType + 1),
		}
		entryBuf[i] = internalpb.CRDTDigestEntry{
			Key:     &keyBuf[i],
			Version: r.versions[keyID],
		}
		entries = append(entries, &entryBuf[i])
		i++
	}
	return &internalpb.CRDTDigest{Entries: entries}
}

// trackKey records that a CRDT key exists in the local store.
func (r *replicatorActor) trackKey(keyID string, dataType crdt.DataType) {
	r.subscriptions[keyID] = types.Unit{}
	r.keyTypes[keyID] = dataType
}

// publishDelta publishes a CRDT delta to the well-known CRDT topic via the TopicActor.
// All peer Replicators subscribed to this topic will receive the delta.
// The delta is encoded as a protobuf message for wire serialization.
func (r *replicatorActor) publishDelta(ctx *ReceiveContext, keyID string, dataType crdt.DataType, delta crdt.ReplicatedData) {
	if r.topicActor == nil {
		return
	}

	pb, err := encodeCRDTDelta(&crdtDelta{
		KeyID:    keyID,
		DataType: dataType,
		Delta:    delta,
		Origin:   r.nodeID,
	})
	if err != nil {
		r.logger.Errorf("failed to encode CRDT delta for key=%s: %v", keyID, err)
		ctx.Err(err)
		return
	}
	msgID := r.nodeID + ":" + strconv.FormatUint(r.msgSeq.Add(1), 10)
	ctx.Tell(r.topicActor, NewPublish(msgID, crdtTopic, pb))
	r.deltaPublishCount.Add(1)
}

// notifyChanged sends a Changed message to all local watchers of a key.
// Dead watchers are pruned from the list to prevent unbounded growth.
func (r *replicatorActor) notifyChanged(ctx *ReceiveContext, keyID string, data crdt.ReplicatedData) {
	watchers, ok := r.watchers[keyID]
	if !ok {
		return
	}

	alive := watchers[:0]
	for _, watcher := range watchers {
		if watcher.IsRunning() {
			ctx.Tell(watcher, &crdt.Changed[crdt.ReplicatedData]{Data: data})
			alive = append(alive, watcher)
		}
	}

	if len(alive) == 0 {
		delete(r.watchers, keyID)
		return
	}

	r.watchers[keyID] = alive
}

// removeWatcher removes a specific watcher PID from a key's watcher list.
func (r *replicatorActor) removeWatcher(keyID string, pid *PID) {
	watchers, ok := r.watchers[keyID]
	if !ok {
		return
	}
	for i, w := range watchers {
		if w.ID() == pid.ID() {
			r.watchers[keyID] = append(watchers[:i], watchers[i+1:]...)
			return
		}
	}
}

// handleSnapshot saves the current CRDT store to BoltDB.
func (r *replicatorActor) handleSnapshot() {
	if r.snapshotStore == nil {
		return
	}
	if err := r.snapshotStore.Save(r.store, r.keyTypes, r.versions); err != nil {
		r.logger.Errorf("failed to save CRDT snapshot: %v", err)
	}
}

// handleReadRequest processes a coordinated read request from a peer Replicator.
// It returns the local value for the requested key.
func (r *replicatorActor) handleReadRequest(ctx *ReceiveContext, msg *internalpb.CRDTReadRequest) {
	keyID, dataType, err := codec.DecodeCRDTKey(msg.GetKey())
	if err != nil {
		r.logger.Warnf("coordinated read: failed to decode key: %v", err)
		return
	}
	data := r.store[keyID]

	var pbData *internalpb.CRDTData
	if data != nil {
		encoded, err := codec.EncodeCRDTData(data)
		if err != nil {
			r.logger.Warnf("coordinated read: failed to encode data for key=%s: %v", keyID, err)
			return
		}
		pbData = encoded
	}

	resp := &internalpb.CRDTReadResponse{
		Key:      codec.EncodeCRDTKey(keyID, dataType),
		Data:     pbData,
		FromNode: r.nodeID,
	}

	if ctx.Sender() != nil {
		ctx.Response(resp)
	}
}

// coordinatedWrite sends a delta directly to peers for acknowledgment
// and also publishes via TopicActor for remaining peers.
func (r *replicatorActor) coordinatedWrite(ctx *ReceiveContext, keyID string, dataType crdt.DataType, delta crdt.ReplicatedData, level crdt.Coordination) {
	r.coordinatedWriteCount.Add(1)
	cctx := context.WithoutCancel(ctx.Context())
	peers, err := r.clusterRef.Peers(cctx)
	if err != nil || len(peers) == 0 {
		// fall back to TopicActor
		r.publishDelta(ctx, keyID, dataType, delta)
		return
	}

	selected := r.selectPeers(peers, r.targetCount(len(peers), level))

	pb, err := encodeCRDTDelta(&crdtDelta{
		KeyID:    keyID,
		DataType: dataType,
		Delta:    delta,
		Origin:   r.nodeID,
	})
	if err != nil {
		r.logger.Errorf("coordinated write: failed to encode delta for key=%s: %v", keyID, err)
		r.publishDelta(ctx, keyID, dataType, delta)
		return
	}

	actorName := reservedName(replicatorType)
	from := pathToAddress(r.pid.Path())
	for _, peer := range selected {
		to, lookupErr := r.remoting.RemoteLookup(cctx, peer.Host, peer.RemotingPort, actorName)
		if lookupErr != nil {
			r.logger.Debugf("coordinated write: failed to lookup peer %s:%d: %v", peer.Host, peer.RemotingPort, lookupErr)
			continue
		}
		if tellErr := r.remoting.RemoteTell(cctx, from, to, pb); tellErr != nil {
			r.logger.Debugf("coordinated write: failed to send to %s:%d: %v", peer.Host, peer.RemotingPort, tellErr)
		}
	}

	// always also publish via TopicActor for remaining peers
	r.publishDelta(ctx, keyID, dataType, delta)
}

// coordinatedRead queries peers for their local value and merges all results.
func (r *replicatorActor) coordinatedRead(ctx *ReceiveContext, keyID string, local crdt.ReplicatedData, level crdt.Coordination) crdt.ReplicatedData {
	r.coordinatedReadCount.Add(1)
	cctx := context.WithoutCancel(ctx.Context())
	peers, err := r.clusterRef.Peers(cctx)
	if err != nil || len(peers) == 0 {
		return local
	}

	selected := r.selectPeers(peers, r.targetCount(len(peers), level))

	dataType := r.keyTypes[keyID]
	req := &internalpb.CRDTReadRequest{
		Key:      codec.EncodeCRDTKey(keyID, dataType),
		FromNode: r.nodeID,
	}

	actorName := reservedName(replicatorType)
	from := pathToAddress(r.pid.Path())
	timeout := r.config.CoordinationTimeout()

	merged := local
	for _, peer := range selected {
		to, lookupErr := r.remoting.RemoteLookup(cctx, peer.Host, peer.RemotingPort, actorName)
		if lookupErr != nil {
			continue
		}
		resp, askErr := r.remoting.RemoteAsk(cctx, from, to, req, timeout)
		if askErr != nil {
			r.logger.Debugf("coordinated read: failed to ask %s:%d: %v", peer.Host, peer.RemotingPort, askErr)
			continue
		}
		readResp, ok := resp.(*internalpb.CRDTReadResponse)
		if !ok || readResp.GetData() == nil {
			continue
		}
		peerData, decodeErr := codec.DecodeCRDTData(readResp.GetData())
		if decodeErr != nil {
			continue
		}
		if merged == nil {
			merged = peerData
		} else {
			merged = merged.Merge(peerData)
		}
	}
	return merged
}

// coordinatedTombstone sends a tombstone directly to peers during coordinated delete.
func (r *replicatorActor) coordinatedTombstone(ctx *ReceiveContext, pb *internalpb.CRDTTombstone, level crdt.Coordination) {
	cctx := context.WithoutCancel(ctx.Context())
	peers, err := r.clusterRef.Peers(cctx)
	if err != nil || len(peers) == 0 {
		return
	}

	selected := r.selectPeers(peers, r.targetCount(len(peers), level))
	actorName := reservedName(replicatorType)
	from := pathToAddress(r.pid.Path())

	for _, peer := range selected {
		to, lookupErr := r.remoting.RemoteLookup(cctx, peer.Host, peer.RemotingPort, actorName)
		if lookupErr != nil {
			continue
		}
		if tellErr := r.remoting.RemoteTell(cctx, from, to, pb); tellErr != nil {
			r.logger.Debugf("coordinated delete: failed to send tombstone to %s:%d: %v", peer.Host, peer.RemotingPort, tellErr)
		}
	}
}

// targetCount computes the number of peers to contact for a coordination level.
func (r *replicatorActor) targetCount(peerCount int, level crdt.Coordination) int {
	switch level {
	case crdt.Majority:
		count := peerCount/2 + 1
		if count > peerCount {
			return peerCount
		}
		return count
	case crdt.All:
		return peerCount
	default:
		return 0
	}
}

// selectPeers returns a subset of peers for coordinated operations.
// For count >= len(peers), returns the full slice without copying.
func (r *replicatorActor) selectPeers(peers []*cluster.Peer, count int) []*cluster.Peer {
	if count >= len(peers) {
		return peers
	}
	if count <= 0 {
		return nil
	}
	// Fisher-Yates partial shuffle: swap random elements into the first `count` positions.
	shuffled := make([]*cluster.Peer, len(peers))
	copy(shuffled, peers)
	for i := range count {
		j := i + rand.IntN(len(shuffled)-i) //nolint:gosec // cryptographic randomness not needed for peer selection
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}
	return shuffled[:count]
}

// crdtDelta is an internal message carrying a CRDT delta between replicators
// via the TopicActor pub/sub system.
type crdtDelta struct {
	KeyID    string
	DataType crdt.DataType
	Delta    crdt.ReplicatedData
	Origin   string
}

// updateCommand is implemented by crdt.Update[T] for any T.
// It bridges the generic typed message to the replicator's untyped handling.
type updateCommand interface {
	KeyID() string
	CRDTDataType() crdt.DataType
	InitialValue() crdt.ReplicatedData
	Apply(current crdt.ReplicatedData) crdt.ReplicatedData
	WriteCoordination() crdt.Coordination
}

// getCommand is implemented by crdt.Get[T] for any T.
type getCommand interface {
	KeyID() string
	Response(data crdt.ReplicatedData) any
	ReadCoordination() crdt.Coordination
}

// subscribeCommand is implemented by crdt.Subscribe[T] for any T.
type subscribeCommand interface {
	KeyID() string
	IsSubscribe()
}

// unsubscribeCommand is implemented by crdt.Unsubscribe[T] for any T.
type unsubscribeCommand interface {
	KeyID() string
	IsUnsubscribe()
}

// deleteCommand is implemented by crdt.Delete[T] for any T.
type deleteCommand interface {
	KeyID() string
	IsDelete()
	WriteCoordination() crdt.Coordination
}

// encodeCRDTDelta converts a crdtDelta to the protobuf wire format.
func encodeCRDTDelta(d *crdtDelta) (*internalpb.CRDTDelta, error) {
	data, err := codec.EncodeCRDTData(d.Delta)
	if err != nil {
		return nil, err
	}
	return &internalpb.CRDTDelta{
		Key:        codec.EncodeCRDTKey(d.KeyID, d.DataType),
		OriginNode: d.Origin,
		Data:       data,
	}, nil
}

// decodeCRDTDelta converts a protobuf CRDTDelta back to a crdtDelta.
func decodeCRDTDelta(pb *internalpb.CRDTDelta) (*crdtDelta, error) {
	data, err := codec.DecodeCRDTData(pb.GetData())
	if err != nil {
		return nil, err
	}
	keyID, dataType, err := codec.DecodeCRDTKey(pb.GetKey())
	if err != nil {
		return nil, err
	}
	return &crdtDelta{
		KeyID:    keyID,
		DataType: dataType,
		Delta:    data,
		Origin:   pb.GetOriginNode(),
	}, nil
}

// spawnReplicator creates the CRDT Replicator system actor.
// It is only spawned when cluster mode is enabled and CRDT is configured
// via ClusterConfig.WithCRDT.
func (x *actorSystem) spawnReplicator(ctx context.Context) error {
	if !x.clusterEnabled.Load() || x.clusterConfig.crdtConfig == nil {
		return nil
	}

	// register the CRDT config extension so the Replicator can read it
	// from PreStart on initial start and on supervisor restarts.
	x.extensions.Set(crdtConfigExtensionID, &crdtConfigExtension{
		config: x.clusterConfig.crdtConfig,
	})

	actorName := reservedName(replicatorType)
	replActor := newReplicatorActor()

	var err error
	x.replicator, err = x.configPID(ctx,
		actorName,
		replActor,
		asSystem(),
		WithLongLived(),
		WithSupervisor(
			sup.NewSupervisor(
				sup.WithStrategy(sup.OneForOneStrategy),
				sup.WithAnyErrorDirective(sup.RestartDirective),
			),
		),
	)
	if err != nil {
		return err
	}

	// register OTel metrics for the replicator
	if x.metricProvider != nil && x.metricProvider.Meter() != nil {
		if metricsErr := x.registerReplicatorMetrics(replActor); metricsErr != nil {
			x.logger.Warnf("failed to register replicator metrics: %v", metricsErr)
		}
	}

	// the replicator is a child actor of the system guardian
	return x.actors.addNode(x.systemGuardian, x.replicator)
}

// registerReplicatorMetrics registers OpenTelemetry observable counters for the Replicator.
func (x *actorSystem) registerReplicatorMetrics(replActor *replicatorActor) error {
	meter := x.metricProvider.Meter()
	metrics, err := metric.NewReplicatorMetric(meter)
	if err != nil {
		return err
	}

	observeOptions := []otelmetric.ObserveOption{
		otelmetric.WithAttributes(attribute.String("actor.system", x.Name())),
	}

	_, err = meter.RegisterCallback(func(_ context.Context, observer otelmetric.Observer) error {
		observer.ObserveInt64(metrics.StoreSize(), int64(len(replActor.store)), observeOptions...)
		observer.ObserveInt64(metrics.MergeCount(), int64(replActor.mergeCount.Load()), observeOptions...)
		observer.ObserveInt64(metrics.DeltaPublishCount(), int64(replActor.deltaPublishCount.Load()), observeOptions...)
		observer.ObserveInt64(metrics.DeltaReceiveCount(), int64(replActor.deltaReceiveCount.Load()), observeOptions...)
		observer.ObserveInt64(metrics.CoordinatedWriteCount(), int64(replActor.coordinatedWriteCount.Load()), observeOptions...)
		observer.ObserveInt64(metrics.CoordinatedReadCount(), int64(replActor.coordinatedReadCount.Load()), observeOptions...)
		observer.ObserveInt64(metrics.AntiEntropyCount(), int64(replActor.antiEntropyCount.Load()), observeOptions...)
		observer.ObserveInt64(metrics.TombstoneCount(), int64(len(replActor.tombstones)), observeOptions...)
		return nil
	},
		metrics.StoreSize(),
		metrics.MergeCount(),
		metrics.DeltaPublishCount(),
		metrics.DeltaReceiveCount(),
		metrics.CoordinatedWriteCount(),
		metrics.CoordinatedReadCount(),
		metrics.AntiEntropyCount(),
		metrics.TombstoneCount(),
	)

	return err
}

// restoreFromSnapshot opens the snapshot store and restores persisted CRDT state.
// This is a no-op when snapshot persistence is not configured.
func (r *replicatorActor) restoreFromSnapshot() error {
	if r.config.SnapshotInterval() <= 0 || r.config.SnapshotDir() == "" {
		return nil
	}

	store, err := ddata.NewStore(r.config.SnapshotDir())
	if err != nil {
		r.logger.Warnf("failed to open CRDT snapshot store: %v", err)
		return nil
	}
	r.snapshotStore = store

	data, keyTypes, versions, err := store.Load()
	if err != nil {
		r.logger.Warnf("failed to load CRDT snapshot: %v", err)
		return nil
	}

	if len(data) == 0 {
		return nil
	}

	// apply restored state
	r.store = data
	r.keyTypes = keyTypes
	r.versions = versions
	for keyID := range data {
		r.subscriptions[keyID] = types.Unit{}
	}

	r.logger.Infof("restored %d CRDT keys from snapshot", len(data))
	return nil
}
