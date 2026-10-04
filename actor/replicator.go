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
	"net"
	"slices"
	"strconv"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"

	"github.com/tochemey/goakt/v4/crdt"
	"github.com/tochemey/goakt/v4/datacenter"
	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/cluster"
	"github.com/tochemey/goakt/v4/internal/codec"
	"github.com/tochemey/goakt/v4/internal/ddata"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/metric"
	"github.com/tochemey/goakt/v4/internal/remoteclient"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/log"
	"github.com/tochemey/goakt/v4/remote"
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
	antiEntropyScheduleRef     = "goakt.crdt.anti-entropy"
	pruneScheduleRef           = "goakt.crdt.prune"
	snapshotScheduleRef        = "goakt.crdt.snapshot"
	dataCenterFlushScheduleRef = "goakt.crdt.dc.flush"
	dataCenterAntiEntropyRef   = "goakt.crdt.dc.anti-entropy"
)

// internal message types for scheduled tasks.
type (
	antiEntropyTick           struct{}
	pruneTick                 struct{}
	snapshotTick              struct{}
	dataCenterFlushTick       struct{}
	dataCenterAntiEntropyTick struct{}
	dataCenterDigestRequest   struct{}
)

// tombstone records that a key was deleted and when.
type tombstone struct {
	keyID     string
	dataType  crdt.DataType
	deletedAt time.Time
	deletedBy string
}

// pendingDelta is the one change of a key waiting for the remote datacenters.
type pendingDelta struct {
	*crdtDelta
	// seq is the pending sequence number of the last write to this entry.
	// A datacenter whose accepted mark is below it has not received the
	// entry in its current form.
	seq uint64
}

// pendingTombstone is the one deletion of a key waiting for the remote
// datacenters.
type pendingTombstone struct {
	// tombstone is the deletion as it is sent.
	tombstone *internalpb.CRDTTombstone
	// seq is the pending sequence number given to the deletion when it was
	// buffered. A datacenter whose accepted mark is below it has not
	// received the deletion.
	seq uint64
}

// crdtConfigExtension holds the CRDT configuration as an actor system extension.
// This ensures the config survives supervisor restarts — the Replicator reads
// it from the extension registry in PreStart rather than from a constructor argument.
type crdtConfigExtension struct {
	config *crdt.Config
	dc     datacenter.DataCenter
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
	pid           *PID
	topicActor    *PID
	logger        log.Logger
	config        *crdt.Config
	nodeID        string
	store         map[string]crdt.ReplicatedData
	keyTypes      map[string]crdt.DataType
	subscriptions map[string]types.Unit
	watchers      map[string][]*PID
	tombstones    map[string]*tombstone
	versions      map[string]uint64
	// hashes caches the canonical content hash of stored values, by key ID.
	// An entry is valid for the value currently in store: every write to
	// store goes through setValue, which drops it, and contentHash computes
	// it again on demand. A key that has not changed since the last
	// anti-entropy round therefore costs a map lookup in the next one.
	hashes map[string]uint64
	// lastContact is the last time a peer Replicator reached this node with
	// a delta, a tombstone, a digest, an anti-entropy answer or a coordinated
	// read. It is saved with the snapshot. A gap since then longer than the
	// tombstone TTL means the tombstones of the deletions made meanwhile may
	// have expired on every node, so the keys this node held before the gap
	// are stale until a peer resolves them: see markStaleKeys.
	lastContact time.Time
	// since is the time from which this node knows every deletion made in
	// the cluster: its start, or the earlier time of a peer whose state it
	// merged. It travels in digests and anti-entropy answers, so a node back
	// from a gap can tell whether the sender saw the whole gap. It is zero
	// while this node is itself back from a gap, and then nothing is sent.
	since time.Time
	// changedAt holds the time of the last local update of each key. A key
	// updated after the last contact is this node's own write, not a copy a
	// deletion may have missed, so it is never stale.
	changedAt map[string]time.Time
	// staleKeys holds, while this node is back from a gap longer than the
	// tombstone TTL, the keys it held before the gap and has not updated or
	// received from a peer since. They are kept from peers until they are
	// resolved. It is nil when this node has no stale keys.
	staleKeys map[string]types.Unit
	// gapStart is the last contact before the gap, while staleKeys is set.
	// A peer whose since is at or before it saw every deletion of the gap.
	gapStart time.Time
	// heardWhileStale holds the peers, by host:port, whose digest arrived
	// while staleKeys is set. Once it covers every current peer and none of
	// them saw the whole gap, no node knows more than this one and its stale
	// keys are kept.
	heardWhileStale       map[string]types.Unit
	msgSeq                atomic.Uint64
	actorSystem           ActorSystem
	clusterRef            cluster.Cluster
	remoting              remoteclient.Client
	serializer            remote.Serializer
	snapshotStore         *ddata.Store
	storeSize             atomic.Int64
	tombstoneSize         atomic.Int64
	mergeCount            atomic.Uint64
	deltaPublishCount     atomic.Uint64
	deltaReceiveCount     atomic.Uint64
	coordinatedWriteCount atomic.Uint64
	coordinatedReadCount  atomic.Uint64
	antiEntropyCount      atomic.Uint64

	// cross-datacenter replication state
	dc            datacenter.DataCenter
	originDCProto *internalpb.DataCenter
	// pendingDeltas holds the local changes not yet accepted by every
	// remote datacenter, one entry per key: a new delta of a key is merged
	// into the pending one, so an entry is cumulative since it entered the
	// buffer. Its size is bounded by the number of distinct keys changed
	// since every remote datacenter on record last caught up, so by the
	// number of keys in the store. Every node buffers, leader or not, so
	// that a node that becomes the leader has its own latest changes to
	// send.
	pendingDeltas map[string]*pendingDelta
	// pendingTombstones holds the local deletions not yet accepted by
	// every remote datacenter, one per key, with the same bound as
	// pendingDeltas. A tombstone removes the pending delta of its key. A
	// key can only be updated again once its local tombstone has expired;
	// if its tombstone is still pending then, both are sent and the
	// receiver applies the tombstone before the delta, which leaves it
	// with the new incarnation of the key.
	pendingTombstones map[string]*pendingTombstone
	// pendingSeq numbers the writes to the two pending buffers. Every
	// write stamps its entry with the next value, so the entries a
	// datacenter still needs are those stamped above its accepted mark.
	pendingSeq uint64
	// dataCenterAccepted holds, for each remote datacenter, the highest
	// pending sequence number of a batch it accepted from this node. It
	// is keyed by DataCenter.ID of the control-plane record, the identity
	// the record itself is registered under. Only the leader fills it; a
	// node that is not the leader keeps it empty. A datacenter without a
	// mark has accepted nothing.
	dataCenterAccepted    map[string]uint64
	crossDCSendCount      atomic.Uint64
	crossDCReceiveCount   atomic.Uint64
	crossDCStaleSkipCount atomic.Uint64
	// lastReplicationLag holds the delay of the last received cross-DC batch in
	// nanoseconds. It is stored at nanosecond resolution and converted to
	// milliseconds when observed by the replication lag gauge.
	lastReplicationLag atomic.Int64
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
	crdtExt := ext.(*crdtConfigExtension)
	r.config = crdtExt.Config()
	r.dc = crdtExt.dc
	dc := &internalpb.DataCenter{}
	dc.SetName(r.dc.Name)
	dc.SetRegion(r.dc.Region)
	dc.SetZone(r.dc.Zone)
	dc.SetLabels(r.dc.Labels)
	r.originDCProto = dc
	r.store = make(map[string]crdt.ReplicatedData)
	r.keyTypes = make(map[string]crdt.DataType)
	r.subscriptions = make(map[string]types.Unit)
	r.watchers = make(map[string][]*PID)
	r.tombstones = make(map[string]*tombstone)
	r.versions = make(map[string]uint64)
	r.hashes = make(map[string]uint64)
	r.changedAt = make(map[string]time.Time)
	r.lastContact = time.Time{}
	r.since = time.Now()
	r.staleKeys = nil
	r.gapStart = time.Time{}
	r.heardWhileStale = nil

	// what is still owed to the remote datacenters survives a restart
	if r.pendingDeltas == nil {
		r.pendingDeltas = make(map[string]*pendingDelta)
		r.pendingTombstones = make(map[string]*pendingTombstone)
		r.dataCenterAccepted = make(map[string]uint64)
	}

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
	r.storeSize.Store(int64(len(r.store)))
	r.tombstoneSize.Store(int64(len(r.tombstones)))
}

// PostStop is called when the actor is shutting down.
func (r *replicatorActor) PostStop(ctx *Context) error {
	_ = ctx.ActorSystem().CancelSchedule(antiEntropyScheduleRef)
	_ = ctx.ActorSystem().CancelSchedule(pruneScheduleRef)
	_ = ctx.ActorSystem().CancelSchedule(snapshotScheduleRef)
	_ = ctx.ActorSystem().CancelSchedule(dataCenterFlushScheduleRef)
	_ = ctx.ActorSystem().CancelSchedule(dataCenterAntiEntropyRef)

	// persist final snapshot before shutdown
	if r.snapshotStore != nil {
		entries, err := r.buildSnapshotEntries()
		if err != nil {
			return fmt.Errorf("failed to encode final CRDT snapshot: %w", err)
		}

		lastContact, since := r.snapshotContact()
		if err := r.snapshotStore.Save(entries, lastContact, since); err != nil {
			return fmt.Errorf("failed to save final CRDT snapshot: %w", err)
		}

		if err := r.snapshotStore.Close(); err != nil {
			return fmt.Errorf("failed to close CRDT snapshot store: %w", err)
		}
	}

	r.logger.Debugf("actor=%s stopped successfully", ctx.ActorName())
	return nil
}

// handlePostStart completes initialization that requires a running actor context.
// The PID, TopicActor reference, and topic subscription require the actor to be started.
func (r *replicatorActor) handlePostStart(ctx *ReceiveContext) {
	actorSystem := ctx.ActorSystem()
	r.pid = ctx.Self()
	r.topicActor = actorSystem.TopicActor()
	r.nodeID = r.pid.ID()
	scheduleCtx := context.WithoutCancel(ctx.Context())

	r.clusterRef = actorSystem.getCluster()
	r.remoting = actorSystem.getRemoting()
	r.serializer = ddata.NewCRDTValueSerializer()

	// subscribe to the CRDT delta topic so this Replicator receives
	// deltas from all peer Replicators in the cluster.
	if r.topicActor != nil {
		ctx.Tell(r.topicActor, NewSubscribe(crdtTopic))
	}

	// start anti-entropy schedule
	if r.config.AntiEntropyInterval() > 0 {
		if err := actorSystem.Schedule(
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
		if err := actorSystem.Schedule(
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
		if err := actorSystem.Schedule(
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

	// start cross-datacenter flush schedule if configured
	if r.config.DataCenterEnabled() && r.config.DataCenterReplicationInterval() > 0 {
		if err := actorSystem.Schedule(
			scheduleCtx,
			&dataCenterFlushTick{},
			r.pid,
			r.config.DataCenterReplicationInterval(),
			WithReference(dataCenterFlushScheduleRef),
		); err != nil {
			r.logger.Errorf("failed to schedule cross-DC flush: %v", err)
			ctx.Err(err)
			return
		}
	}

	// start cross-datacenter anti-entropy schedule if configured
	if r.config.DataCenterEnabled() && r.config.DataCenterAntiEntropy() && r.config.DataCenterAntiEntropyInterval() > 0 {
		if err := actorSystem.Schedule(
			scheduleCtx,
			&dataCenterAntiEntropyTick{},
			r.pid,
			r.config.DataCenterAntiEntropyInterval(),
			WithReference(dataCenterAntiEntropyRef),
		); err != nil {
			r.logger.Errorf("failed to schedule cross-DC anti-entropy: %v", err)
			ctx.Err(err)
			return
		}
	}

	r.logger.Debugf("actor=%s started successfully", r.pid.Name())
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
		r.handleProtoTombstone(ctx, msg)
	case *internalpb.CRDTReadRequest:
		r.handleReadRequest(ctx, msg)
	case *internalpb.CRDTDigest:
		r.handleDigest(ctx, msg)
	case *internalpb.CRDTFullState:
		r.handleFullState(ctx, msg)
	case *internalpb.CRDTDeltaBatch:
		r.handleIncomingBatch(ctx, msg)
	case *dataCenterDigestRequest:
		r.handleDataCenterDigestRequest(ctx)
	case *dataCenterFlushTick:
		r.handleDataCenterFlush(ctx)
	case *dataCenterAntiEntropyTick:
		r.handleDataCenterAntiEntropy(ctx)
	case *antiEntropyTick:
		r.handleAntiEntropy(ctx)
	case *pruneTick:
		r.handlePrune()
	case *snapshotTick:
		r.handleSnapshot()
	case *SubscribeAck:
		// the TopicActor confirms the subscription made in handlePostStart;
		// nothing depends on the confirmation
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
	r.setValue(keyID, updated)
	r.versions[keyID]++
	r.changedAt[keyID] = time.Now()
	delete(r.staleKeys, keyID)

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
// A coordinated read keeps the merged value in the local store. When the key
// was unknown locally, its data type is recorded from the merged value so the
// key is snapshotted and advertised with its real type like any other key.
func (r *replicatorActor) handleGet(ctx *ReceiveContext, msg getCommand) {
	keyID := msg.KeyID()
	data := r.store[keyID]

	coordination := msg.ReadCoordination()
	if coordination != 0 {
		merged := r.coordinatedRead(ctx, keyID, data, coordination)
		if merged != nil {
			if _, tracked := r.keyTypes[keyID]; !tracked {
				r.trackKey(keyID, crdtDataTypeOf(merged))
			}

			r.setValue(keyID, merged)
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
				n := len(watchers) - 1
				watchers[i] = watchers[n]
				watchers[n] = nil
				r.watchers[keyID] = watchers[:n]
				break
			}
		}
		if len(r.watchers[keyID]) == 0 {
			delete(r.watchers, keyID)
		}
	}
}

// handleDelete removes a CRDT key from the local store and publishes a tombstone.
// When it removes a value, the key's watchers are sent Deleted.
func (r *replicatorActor) handleDelete(ctx *ReceiveContext, msg deleteCommand) {
	keyID := msg.KeyID()

	dataType, hasType := r.keyTypes[keyID]
	if r.removeValue(keyID) {
		r.notifyDeleted(ctx, keyID)
	}

	now := time.Now()
	r.tombstones[keyID] = &tombstone{
		keyID:     keyID,
		dataType:  dataType,
		deletedAt: now,
		deletedBy: r.nodeID,
	}

	if hasType {
		pb := &internalpb.CRDTTombstone{}
		pb.SetKey(codec.EncodeCRDTKey(keyID, dataType))
		pb.SetDeletedAtNanos(now.UnixNano())
		pb.SetDeletedByNode(r.nodeID)

		coordination := msg.WriteCoordination()
		if coordination != 0 {
			r.coordinatedTombstone(ctx, pb, coordination)
		}

		// always publish tombstone to peers via TopicActor
		if r.topicActor != nil {
			msgID := r.nodeID + ":del:" + strconv.FormatUint(r.msgSeq.Add(1), 10)
			ctx.Tell(r.topicActor, NewPublish(msgID, crdtTopic, pb))
		}

		// buffer for cross-DC forwarding if enabled
		if r.config.DataCenterEnabled() {
			r.bufferTombstone(keyID, pb)
		}
	}

	if ctx.Sender() != nil {
		ctx.Response(&crdt.DeleteResponse{})
	}
}

// handleProtoTombstone processes a tombstone received from a peer via TopicActor
// or in a batch from another datacenter. One this node issued is ignored: the
// TopicActor delivers a publication to its publisher too. Any other is a
// contact with a peer: see observeContact.
func (r *replicatorActor) handleProtoTombstone(ctx *ReceiveContext, msg *internalpb.CRDTTombstone) {
	if msg.GetDeletedByNode() == r.nodeID {
		return
	}

	r.observeContact()
	r.applyTombstone(ctx, msg)
}

// applyTombstone applies a deletion made on another node, or one this node
// made and has since lost. It is the one rule for a tombstone, whichever way
// it arrives: the topic, a batch from another datacenter, or an anti-entropy
// exchange.
//
// The deletion wins over whatever value is stored, whenever that value was
// written: the key is removed, and so is a change of it still waiting for
// the remote datacenters. When this node held a value for the key, its
// watchers are sent Deleted, however old the tombstone; a tombstone for a key
// this node does not hold sends nothing, so a deletion is announced once.
//
// The tombstone is then kept to reject the deltas of the deleted key that
// arrive late, for the tombstone TTL counted from the deletion. One that
// arrives after that time still deletes the key but is not kept: it would be
// pruned on the next tick, and until then it would reject the deltas of a key
// its sender has created again since, which a tombstone of that age allows.
//
// A tombstone this node already holds with the same or a later deletion time
// is left alone, so a tombstone that comes back on every anti-entropy round is
// applied once, and two nodes that deleted the same key on their own settle
// on the later deletion.
func (r *replicatorActor) applyTombstone(ctx *ReceiveContext, msg *internalpb.CRDTTombstone) {
	keyID, dataType, err := codec.DecodeCRDTKey(msg.GetKey())
	if err != nil {
		r.logger.Warnf("tombstone: failed to decode key: %v", err)
		return
	}

	deletedAt := time.Unix(0, msg.GetDeletedAtNanos())
	if held, ok := r.tombstones[keyID]; ok && !held.deletedAt.Before(deletedAt) {
		return
	}

	if r.removeValue(keyID) {
		r.notifyDeleted(ctx, keyID)
	}

	delete(r.pendingDeltas, keyID)

	if time.Since(deletedAt) > r.config.TombstoneTTL() {
		return
	}

	r.tombstones[keyID] = &tombstone{
		keyID:     keyID,
		dataType:  dataType,
		deletedAt: deletedAt,
		deletedBy: msg.GetDeletedByNode(),
	}
}

// encodeLiveTombstone returns the wire form of a tombstone this node retains,
// for an anti-entropy exchange. It reports false for one older than the
// tombstone TTL: it is about to be pruned and no longer deletes anything.
func (r *replicatorActor) encodeLiveTombstone(ts *tombstone) (*internalpb.CRDTTombstone, bool) {
	if time.Since(ts.deletedAt) > r.config.TombstoneTTL() {
		return nil, false
	}

	pb := &internalpb.CRDTTombstone{}
	pb.SetKey(codec.EncodeCRDTKey(ts.keyID, ts.dataType))
	pb.SetDeletedAtNanos(ts.deletedAt.UnixNano())
	pb.SetDeletedByNode(ts.deletedBy)
	return pb, true
}

// handleProtoDelta decodes a protobuf delta received from a peer replicator
// via TopicActor and delegates to handleDelta.
func (r *replicatorActor) handleProtoDelta(ctx *ReceiveContext, msg *internalpb.CRDTDelta) {
	d, err := r.decodeDelta(msg)
	if err != nil {
		r.logger.Warnf("failed to decode CRDT delta: %v", err)
		return
	}
	r.handleDelta(ctx, d)
}

// handleDelta merges a delta received from a peer replicator via TopicActor.
// A delta that leaves the stored value unchanged, such as a duplicate
// delivery, neither advances the key's version nor notifies its watchers.
// A delta from a peer is a contact with it (see observeContact), and the key
// it carries is no longer stale.
func (r *replicatorActor) handleDelta(ctx *ReceiveContext, msg *crdtDelta) {
	if msg.Origin == r.nodeID {
		return
	}

	r.deltaReceiveCount.Add(1)
	r.observeContact()
	keyID := msg.KeyID

	// reject deltas for tombstoned keys
	if _, ok := r.tombstones[keyID]; ok {
		return
	}

	// a peer has the key, so it was not deleted
	delete(r.staleKeys, keyID)

	current, exists := r.store[keyID]
	if !exists {
		r.setValue(keyID, msg.Delta)
		r.trackKey(keyID, msg.DataType)
		r.versions[keyID]++
		r.notifyChanged(ctx, keyID, msg.Delta)
		return
	}

	merged, changed := r.mergeValue(keyID, current, msg.Delta)
	r.mergeCount.Add(1)

	if changed {
		r.versions[keyID]++
		r.notifyChanged(ctx, keyID, merged)
	}
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

	// no current peer saw the whole gap: this node knows as much as any
	if r.hasStaleKeys() && r.heardFromEveryPeer(peers) {
		r.logger.Infof("crdt: no peer saw the whole gap; keeping %d stale keys", len(r.staleKeys))
		r.keepStaleKeys(time.Now())
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

// handleDigest processes an anti-entropy digest from a peer and answers with
// the full state of every key the peer needs from this node.
//
// The digest is a contact with a peer: see observeContact. While this node
// has stale keys, the digest may resolve them (see resolveStaleKeys), and a
// key that is still stale is not sent. The answer carries this node's since;
// a digest whose since is later than it is answered even when nothing else
// is to be sent, so two nodes that hold the same state still pass the
// earlier since on, and a node restarted while another was away can resolve
// that node's stale keys.
//
// A key is sent when the peer does not have it. For a key both nodes have,
// the content hashes decide: the key is sent when the hashes differ and left
// alone when they are equal, whatever the versions say. Versions count local
// events and are not comparable across nodes, so two nodes can hold different
// values at the same version; the hash sees that, the version does not. An
// entry without a hash comes from a node that predates the field, and for it
// the version rule applies: the key is sent when the local version is higher.
// A tombstoned key is not in the store, so it is never sent.
//
// Deletions travel in both directions of the exchange, for as long as a node
// retains the tombstone. The digest carries the tombstones its sender
// retains; each one is applied here first, by the rule of applyTombstone, so
// a key the sender deleted is removed before the store is compared and is
// not sent back. For a key the digest lists and this node has deleted, the
// answer carries this node's tombstone, and the sender applies it by the same
// rule. A tombstone for a key the digest does not list needs no answer. A
// digest without tombstones comes from a node that predates the field and is
// handled as it always was; such a node also ignores the tombstones of an
// answer.
//
// The exchange is a pull by the node that sent the digest: it receives this
// node's state and merges it, and this node learns nothing from the round.
// That is enough to converge. Every node sends its own digest to a random
// peer on every anti-entropy tick, so for any two nodes each one eventually
// pulls from the other; after A pulled from B and B pulled from A both hold
// the merge of the two states, their hashes are equal and the rounds between
// them go quiet. Neither direction can starve, because neither depends on the
// other node's choice of peer. When only one side is behind, the node that is
// ahead still receives the older state; merging it changes nothing, and a
// merge that changes nothing is not counted or announced.
func (r *replicatorActor) handleDigest(ctx *ReceiveContext, msg *internalpb.CRDTDigest) {
	var (
		entries    []*internalpb.CRDTFullStateEntry
		tombstones []*internalpb.CRDTTombstone
	)

	r.observeContact()

	for _, ts := range msg.GetTombstones() {
		r.applyTombstone(ctx, ts)
	}

	peerEntries := make(map[string]*internalpb.CRDTDigestEntry, len(msg.GetEntries()))
	for _, e := range msg.GetEntries() {
		keyID, _, err := codec.DecodeCRDTKey(e.GetKey())
		if err != nil {
			r.logger.Warnf("anti-entropy digest: failed to decode key: %v", err)
			continue
		}
		peerEntries[keyID] = e

		// the peer holds a key this node has deleted
		if ts, deleted := r.tombstones[keyID]; deleted {
			if pb, live := r.encodeLiveTombstone(ts); live {
				tombstones = append(tombstones, pb)
			}
		}
	}

	if r.hasStaleKeys() {
		r.resolveStaleKeys(ctx, msg, peerEntries)
	}

	for keyID, data := range r.store {
		if peerEntry, peerHas := peerEntries[keyID]; peerHas && !r.differsFromPeer(keyID, peerEntry) {
			continue
		}

		if _, stale := r.staleKeys[keyID]; stale {
			continue
		}

		dataType := r.keyTypes[keyID]
		pbData, err := ddata.EncodeCRDT(data, r.serializer)
		if err != nil {
			r.logger.Warnf("anti-entropy: failed to encode state for key=%s: %v", keyID, err)
			continue
		}
		crdtfse := &internalpb.CRDTFullStateEntry{}
		crdtfse.SetKey(codec.EncodeCRDTKey(keyID, dataType))
		crdtfse.SetData(pbData)
		entries = append(entries, crdtfse)
	}

	// a peer that knows every deletion only from a later time than this node
	// takes this node's since from the answer, even when nothing else differs
	sinceIsNews := !r.since.IsZero() && msg.HasSinceNanos() && time.Unix(0, msg.GetSinceNanos()).After(r.since)

	if (len(entries) > 0 || len(tombstones) > 0 || sinceIsNews) && ctx.Sender() != nil {
		fullState := &internalpb.CRDTFullState{}
		fullState.SetEntries(entries)
		fullState.SetTombstones(tombstones)

		if !r.since.IsZero() {
			fullState.SetSinceNanos(r.since.UnixNano())
		}

		ctx.Tell(ctx.Sender(), fullState)
	}
}

// differsFromPeer reports whether the peer needs this node's state of a key
// both nodes hold, given the peer's digest entry for it. With a content hash
// on both sides the hashes are compared; without one, the peer being a node
// that predates the hash, the peer needs the state when the local version is
// the higher one.
func (r *replicatorActor) differsFromPeer(keyID string, peerEntry *internalpb.CRDTDigestEntry) bool {
	if peerEntry.HasStateHash() {
		if localHash, ok := r.contentHash(keyID); ok {
			return localHash != peerEntry.GetStateHash()
		}
	}

	return r.versions[keyID] > peerEntry.GetVersion()
}

// observeContact records that a peer Replicator reached this node. When the
// previous contact is older than the tombstone TTL (see gapBeyondTTL), this
// node first marks its stale keys, with the gap starting at that previous
// contact.
func (r *replicatorActor) observeContact() {
	if !r.hasStaleKeys() && r.gapBeyondTTL() {
		r.markStaleKeys()
	}

	r.lastContact = time.Now()
}

// gapBeyondTTL reports whether the last contact is older than the tombstone
// TTL. A node that has never had a contact has no gap. Without anti-entropy
// there is no gap either: no digest would ever resolve the stale keys, and no
// anti-entropy round would spread them.
func (r *replicatorActor) gapBeyondTTL() bool {
	if r.config.AntiEntropyInterval() <= 0 || r.lastContact.IsZero() {
		return false
	}

	return time.Since(r.lastContact) > r.config.TombstoneTTL()
}

// hasStaleKeys reports whether this node is back from a gap longer than the
// tombstone TTL and its stale keys are not resolved yet.
func (r *replicatorActor) hasStaleKeys() bool {
	return r.staleKeys != nil
}

// markStaleKeys marks the keys this node held before its gap, and has not
// updated since, as stale. Any of them may have been deleted during the gap
// with a tombstone that has expired since, which no peer can send any more.
// A stale key is not sent to peers, not in an anti-entropy answer and not in
// a coordinated read, and the deltas waiting for the remote datacenters wait
// too, until the stale keys are resolved (see resolveStaleKeys and
// handleAntiEntropy). A key a peer turns out to hold, through a delta or an
// anti-entropy answer, is no longer stale. This node's own reads still see
// the stale keys.
//
// This node also stops advertising its since: it missed the deletions of the
// gap, so it cannot resolve another node's stale keys.
func (r *replicatorActor) markStaleKeys() {
	r.gapStart = r.lastContact
	r.staleKeys = make(map[string]types.Unit)
	r.heardWhileStale = make(map[string]types.Unit)
	r.since = time.Time{}

	for keyID := range r.store {
		if changedAt, ok := r.changedAt[keyID]; ok && changedAt.After(r.gapStart) {
			continue
		}

		r.staleKeys[keyID] = types.Unit{}
	}

	r.logger.Infof("crdt: back after %s without a peer, longer than the tombstone TTL; %d keys are stale", time.Since(r.gapStart).Round(time.Second), len(r.staleKeys))
}

// resolveStaleKeys applies the digest of a peer to the stale keys. The peer
// is recorded as heard. A peer whose since is at or before the start of the
// gap saw every deletion of the gap, and its digest lists every key it holds:
// a stale key it lists is kept, one it does not list was deleted during the
// gap and is removed, as a tombstone would remove it, and its watchers are
// sent Deleted. This node then takes the peer's since. A digest without a
// since, from a node that has stale keys itself or that predates the field,
// or with a later since, resolves nothing.
func (r *replicatorActor) resolveStaleKeys(ctx *ReceiveContext, msg *internalpb.CRDTDigest, peerEntries map[string]*internalpb.CRDTDigestEntry) {
	if ctx.Sender() != nil {
		r.heardWhileStale[ctx.Sender().Path().HostPort()] = types.Unit{}
	}

	if !msg.HasSinceNanos() {
		return
	}

	peerSince := time.Unix(0, msg.GetSinceNanos())
	if peerSince.After(r.gapStart) {
		return
	}

	removed := 0
	for keyID := range r.staleKeys {
		if _, listed := peerEntries[keyID]; listed {
			continue
		}

		r.removeValue(keyID)
		delete(r.pendingDeltas, keyID)
		r.notifyDeleted(ctx, keyID)
		removed++
	}

	r.logger.Infof("crdt: a peer that saw the whole gap resolved the stale keys; removed %d keys deleted during the gap", removed)
	r.keepStaleKeys(peerSince)
}

// heardFromEveryPeer reports whether the digest of every given peer that runs
// a Replicator arrived while this node has stale keys. When distributed data
// is restricted to a role, a peer without the role runs none and is skipped.
func (r *replicatorActor) heardFromEveryPeer(peers []*cluster.Peer) bool {
	role := r.config.Role()
	for _, peer := range peers {
		if role != "" && !peer.HasRole(role) {
			continue
		}

		if _, heard := r.heardWhileStale[peer.Host+":"+strconv.Itoa(peer.RemotingPort)]; !heard {
			return false
		}
	}

	return true
}

// keepStaleKeys keeps the keys that are still stale as ordinary keys, and
// records that this node knows every deletion from the given time on.
func (r *replicatorActor) keepStaleKeys(since time.Time) {
	r.staleKeys = nil
	r.heardWhileStale = nil
	r.gapStart = time.Time{}
	r.since = since
}

// handleFullState processes a full state response from a peer during anti-entropy.
// A state that leaves the stored value unchanged, which is what a node that is
// ahead of its peer receives, neither advances the key's version nor notifies
// its watchers.
//
// The answer may also carry the peer's tombstones for keys this node listed
// in its digest. They are applied first, by the rule of applyTombstone, so a
// key the peer has deleted is removed here.
//
// The answer is a contact with a peer: see observeContact. A key it carries
// is held by the peer, so it is no longer stale. Once merged, this node
// knows every deletion the peer knows, so it takes the peer's since when that
// is earlier than its own; a node with stale keys takes it from
// resolveStaleKeys instead.
func (r *replicatorActor) handleFullState(ctx *ReceiveContext, msg *internalpb.CRDTFullState) {
	r.observeContact()

	for _, ts := range msg.GetTombstones() {
		r.applyTombstone(ctx, ts)
	}

	if !r.hasStaleKeys() && msg.HasSinceNanos() {
		if peerSince := time.Unix(0, msg.GetSinceNanos()); peerSince.Before(r.since) {
			r.since = peerSince
		}
	}

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

		// the peer has the key, so it was not deleted
		delete(r.staleKeys, keyID)

		data, err := ddata.DecodeCRDT(entry.GetData(), r.serializer)
		if err != nil {
			r.logger.Warnf("anti-entropy: failed to decode state for key=%s: %v", keyID, err)
			continue
		}

		current, exists := r.store[keyID]
		if !exists {
			r.setValue(keyID, data)
			r.trackKey(keyID, dataType)
			r.versions[keyID]++
			r.notifyChanged(ctx, keyID, data)
			continue
		}

		merged, changed := r.mergeValue(keyID, current, data)
		r.mergeCount.Add(1)

		if changed {
			r.versions[keyID]++
			r.notifyChanged(ctx, keyID, merged)
		}
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
			r.setValue(keyID, c.CompactData())
		}
	}
}

// buildDigest creates an anti-entropy digest from the local store. Each entry
// carries the key's local version and the content hash of its value; the hash
// is what a peer compares, the version is kept for peers that predate it.
// The digest also carries every tombstone this node retains that is within
// its TTL, so a peer that still holds a deleted key deletes it, and this
// node's since unless it has stale keys.
// Pre-allocates contiguous slices to minimize heap allocations.
func (r *replicatorActor) buildDigest() *internalpb.CRDTDigest {
	n := len(r.store)
	entries := make([]*internalpb.CRDTDigestEntry, 0, n)
	entryBuf := make([]internalpb.CRDTDigestEntry, n)
	keyBuf := make([]internalpb.CRDTKey, n)

	i := 0
	for keyID := range r.store {
		dataType := r.keyTypes[keyID]
		keyBuf[i].SetId(keyID)
		keyBuf[i].SetDataType(internalpb.CRDTDataType(dataType + 1))
		entryBuf[i].SetKey(&keyBuf[i])
		entryBuf[i].SetVersion(r.versions[keyID])

		if hash, ok := r.contentHash(keyID); ok {
			entryBuf[i].SetStateHash(hash)
		}

		entries = append(entries, &entryBuf[i])
		i++
	}
	var tombstones []*internalpb.CRDTTombstone

	for _, ts := range r.tombstones {
		if pb, live := r.encodeLiveTombstone(ts); live {
			tombstones = append(tombstones, pb)
		}
	}

	crdtd := &internalpb.CRDTDigest{}
	crdtd.SetEntries(entries)
	crdtd.SetTombstones(tombstones)

	if !r.since.IsZero() {
		crdtd.SetSinceNanos(r.since.UnixNano())
	}

	return crdtd
}

// trackKey records that a CRDT key exists in the local store.
func (r *replicatorActor) trackKey(keyID string, dataType crdt.DataType) {
	r.subscriptions[keyID] = types.Unit{}
	r.keyTypes[keyID] = dataType
}

// setValue stores the value of a key. Every write to the store goes through
// it, because it also drops the key's cached content hash, which described
// the previous value.
func (r *replicatorActor) setValue(keyID string, data crdt.ReplicatedData) {
	r.store[keyID] = data
	delete(r.hashes, keyID)
}

// removeValue forgets the value of a deleted key together with its version
// and its cached content hash. It reports whether there was a value to
// remove, which is when the deletion is news to the key's watchers.
func (r *replicatorActor) removeValue(keyID string) bool {
	_, exists := r.store[keyID]
	delete(r.store, keyID)
	delete(r.versions, keyID)
	delete(r.hashes, keyID)
	delete(r.changedAt, keyID)
	delete(r.staleKeys, keyID)
	return exists
}

// contentHash returns the canonical content hash of the value stored under a
// key, computing it only when the value changed since it was last asked for.
// It reports false for a key that is not stored and for a value whose type
// has no content hash.
func (r *replicatorActor) contentHash(keyID string) (uint64, bool) {
	if hash, ok := r.hashes[keyID]; ok {
		return hash, true
	}

	hasher, ok := r.store[keyID].(crdt.StateHasher)
	if !ok {
		return 0, false
	}

	hash := hasher.StateHash()
	r.hashes[keyID] = hash
	return hash, true
}

// mergeValue merges incoming into the stored value of a key, stores the
// result and reports whether the replicated state changed, judged by the
// content hash before and after. A value whose type has no content hash is
// always reported as changed.
func (r *replicatorActor) mergeValue(keyID string, current, incoming crdt.ReplicatedData) (crdt.ReplicatedData, bool) {
	before, hashed := r.contentHash(keyID)
	merged := current.Merge(incoming)
	r.setValue(keyID, merged)
	after, _ := r.contentHash(keyID)
	return merged, !hashed || before != after
}

// bufferDelta records a local change for the remote datacenters. The buffer
// holds one delta per key: a further delta of the same key is merged into
// the pending one, which gives the receiver the same value as the two deltas
// applied one after the other. Either way the entry takes the next pending
// sequence number, so a datacenter that accepted an earlier form of it is
// sent the merged form.
func (r *replicatorActor) bufferDelta(keyID string, dataType crdt.DataType, delta crdt.ReplicatedData) {
	r.pendingSeq++

	if pending, ok := r.pendingDeltas[keyID]; ok {
		pending.Delta = pending.Delta.Merge(delta)
		pending.seq = r.pendingSeq
		return
	}

	r.pendingDeltas[keyID] = &pendingDelta{
		crdtDelta: &crdtDelta{
			KeyID:    keyID,
			DataType: dataType,
			Delta:    delta,
			Origin:   r.nodeID,
		},
		seq: r.pendingSeq,
	}
}

// bufferTombstone records a local deletion for the remote datacenters under
// the next pending sequence number. It replaces the pending delta of the
// key: the receiver deletes the key, so the change that preceded the
// deletion is no longer worth sending.
func (r *replicatorActor) bufferTombstone(keyID string, pb *internalpb.CRDTTombstone) {
	r.pendingSeq++
	delete(r.pendingDeltas, keyID)
	r.pendingTombstones[keyID] = &pendingTombstone{tombstone: pb, seq: r.pendingSeq}
}

// publishDelta publishes a CRDT delta to the well-known CRDT topic via the TopicActor.
// All peer Replicators subscribed to this topic will receive the delta.
// The delta is encoded as a protobuf message for wire serialization.
func (r *replicatorActor) publishDelta(ctx *ReceiveContext, keyID string, dataType crdt.DataType, delta crdt.ReplicatedData) {
	if r.topicActor == nil {
		return
	}

	pb, err := r.encodeDelta(&crdtDelta{
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

	// buffer for cross-DC forwarding if enabled
	if r.config.DataCenterEnabled() {
		r.bufferDelta(keyID, dataType, delta)
	}
}

// notifyChanged sends a Changed message to all local watchers of a key.
// The message names the key, so an actor watching several keys can tell
// which one changed.
func (r *replicatorActor) notifyChanged(ctx *ReceiveContext, keyID string, data crdt.ReplicatedData) {
	r.notifyWatchers(ctx, keyID, func(key crdt.Key) any {
		return &crdt.Changed{Key: key, Data: data}
	})
}

// notifyDeleted sends a Deleted message to all local watchers of a key whose
// value this node has just removed. The watchers stay subscribed, so they
// hear of the key again if it is created once its tombstone has expired.
func (r *replicatorActor) notifyDeleted(ctx *ReceiveContext, keyID string) {
	r.notifyWatchers(ctx, keyID, func(key crdt.Key) any {
		return &crdt.Deleted{Key: key}
	})
}

// notifyWatchers sends every running watcher of a key its own message, built
// by newMessage from the key. Dead watchers are pruned from the list to
// prevent unbounded growth.
func (r *replicatorActor) notifyWatchers(ctx *ReceiveContext, keyID string, newMessage func(key crdt.Key) any) {
	watchers, ok := r.watchers[keyID]
	if !ok {
		return
	}

	key := crdtKeyOf(keyID, r.keyTypes[keyID])
	alive := watchers[:0]
	for _, watcher := range watchers {
		if watcher.IsRunning() {
			ctx.Tell(watcher, newMessage(key))
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
			n := len(watchers) - 1
			watchers[i] = watchers[n]
			watchers[n] = nil
			r.watchers[keyID] = watchers[:n]
			return
		}
	}
}

// handleSnapshot saves the current CRDT store to BoltDB, with the contact
// times returned by snapshotContact.
func (r *replicatorActor) handleSnapshot() {
	if r.snapshotStore == nil {
		return
	}
	entries, err := r.buildSnapshotEntries()
	if err != nil {
		r.logger.Errorf("failed to encode CRDT snapshot: %v", err)
		return
	}
	lastContact, since := r.snapshotContact()
	if err := r.snapshotStore.Save(entries, lastContact, since); err != nil {
		r.logger.Errorf("failed to save CRDT snapshot: %v", err)
	}
}

// snapshotContact returns the last contact and the since to save with a
// snapshot. While this node has stale keys it saves the start of the gap as
// its last contact, and no since, so a restart before the stale keys are
// resolved finds the gap again.
func (r *replicatorActor) snapshotContact() (time.Time, time.Time) {
	if r.hasStaleKeys() {
		return r.gapStart, time.Time{}
	}

	return r.lastContact, r.since
}

// buildSnapshotEntries encodes the in-memory CRDT store into protobuf snapshot entries.
func (r *replicatorActor) buildSnapshotEntries() (map[string]*internalpb.CRDTSnapshotEntry, error) {
	entries := make(map[string]*internalpb.CRDTSnapshotEntry, len(r.store))
	for keyID, data := range r.store {
		dataType, ok := r.keyTypes[keyID]
		if !ok {
			return nil, fmt.Errorf("missing data type for key=%s", keyID)
		}
		version := r.versions[keyID]
		pbData, err := ddata.EncodeCRDT(data, r.serializer)
		if err != nil {
			return nil, fmt.Errorf("encode snapshot for key=%s: %w", keyID, err)
		}
		crdtse := &internalpb.CRDTSnapshotEntry{}
		crdtse.SetKey(codec.EncodeCRDTKey(keyID, dataType))
		crdtse.SetData(pbData)
		crdtse.SetVersion(version)

		if changedAt, ok := r.changedAt[keyID]; ok {
			crdtse.SetChangedAtNanos(changedAt.UnixNano())
		}

		entries[keyID] = crdtse
	}
	return entries, nil
}

// handleReadRequest processes a coordinated read request from a peer Replicator.
// It returns the local value for the requested key.
func (r *replicatorActor) handleReadRequest(ctx *ReceiveContext, msg *internalpb.CRDTReadRequest) {
	keyID, dataType, err := codec.DecodeCRDTKey(msg.GetKey())
	if err != nil {
		r.logger.Warnf("coordinated read: failed to decode key: %v", err)
		return
	}

	r.observeContact()

	// a stale key is answered as absent
	data := r.store[keyID]
	if _, stale := r.staleKeys[keyID]; stale {
		data = nil
	}

	var pbData *internalpb.CRDTData
	if data != nil {
		encoded, err := ddata.EncodeCRDT(data, r.serializer)
		if err != nil {
			r.logger.Warnf("coordinated read: failed to encode data for key=%s: %v", keyID, err)
			return
		}
		pbData = encoded
	}

	resp := &internalpb.CRDTReadResponse{}
	resp.SetKey(codec.EncodeCRDTKey(keyID, dataType))
	resp.SetData(pbData)
	resp.SetFromNode(r.nodeID)

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

	pb, err := r.encodeDelta(&crdtDelta{
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
	req := &internalpb.CRDTReadRequest{}
	req.SetKey(codec.EncodeCRDTKey(keyID, dataType))
	req.SetFromNode(r.nodeID)

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
		peerData, decodeErr := ddata.DecodeCRDT(readResp.GetData(), r.serializer)
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

// updateCommand is implemented by crdt.Update.
// It keeps the replicator's handling independent of the concrete message type.
type updateCommand interface {
	KeyID() string
	CRDTDataType() crdt.DataType
	InitialValue() crdt.ReplicatedData
	Apply(current crdt.ReplicatedData) crdt.ReplicatedData
	WriteCoordination() crdt.Coordination
}

// getCommand is implemented by crdt.Get.
type getCommand interface {
	KeyID() string
	Response(data crdt.ReplicatedData) any
	ReadCoordination() crdt.Coordination
}

// subscribeCommand is implemented by crdt.Subscribe.
type subscribeCommand interface {
	KeyID() string
	IsSubscribe()
}

// unsubscribeCommand is implemented by crdt.Unsubscribe.
type unsubscribeCommand interface {
	KeyID() string
	IsUnsubscribe()
}

// deleteCommand is implemented by crdt.Delete.
type deleteCommand interface {
	KeyID() string
	IsDelete()
	WriteCoordination() crdt.Coordination
}

// encodeDelta converts a crdtDelta to the protobuf wire format.
func (r *replicatorActor) encodeDelta(d *crdtDelta) (*internalpb.CRDTDelta, error) {
	data, err := ddata.EncodeCRDT(d.Delta, r.serializer)
	if err != nil {
		return nil, err
	}
	cRDTDelta := &internalpb.CRDTDelta{}
	cRDTDelta.SetKey(codec.EncodeCRDTKey(d.KeyID, d.DataType))
	cRDTDelta.SetOriginNode(d.Origin)
	cRDTDelta.SetData(data)
	return cRDTDelta, nil
}

// decodeDelta converts a protobuf CRDTDelta back to a crdtDelta.
func (r *replicatorActor) decodeDelta(pb *internalpb.CRDTDelta) (*crdtDelta, error) {
	data, err := ddata.DecodeCRDT(pb.GetData(), r.serializer)
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

// crdtKeyOf rebuilds the public key of a stored CRDT from its identifier and
// data type, which is how the replicator tracks keys internally.
func crdtKeyOf(keyID string, dataType crdt.DataType) crdt.Key {
	switch dataType {
	case crdt.PNCounterType:
		return crdt.PNCounterKey(keyID)
	case crdt.LWWRegisterType:
		return crdt.LWWRegisterKey(keyID)
	case crdt.ORSetType:
		return crdt.ORSetKey(keyID)
	case crdt.ORMapType:
		return crdt.ORMapKey(keyID)
	case crdt.FlagType:
		return crdt.FlagKey(keyID)
	case crdt.MVRegisterType:
		return crdt.MVRegisterKey(keyID)
	default:
		return crdt.GCounterKey(keyID)
	}
}

// crdtDataTypeOf returns the data type of a CRDT value. The replicator uses it
// when a value reaches the store without its key, which is the case of a
// coordinated read of a key that only peers hold.
func crdtDataTypeOf(data crdt.ReplicatedData) crdt.DataType {
	switch data.(type) {
	case *crdt.PNCounter:
		return crdt.PNCounterType
	case *crdt.LWWRegister:
		return crdt.LWWRegisterType
	case *crdt.ORSet:
		return crdt.ORSetType
	case *crdt.ORMap:
		return crdt.ORMapType
	case *crdt.Flag:
		return crdt.FlagType
	case *crdt.MVRegister:
		return crdt.MVRegisterType
	default:
		return crdt.GCounterType
	}
}

// spawnReplicator creates the CRDT Replicator system actor.
// It is only spawned when cluster mode is enabled and CRDT is configured
// via ClusterConfig.WithCRDT.
func (x *actorSystem) spawnReplicator(ctx context.Context) error {
	if !x.clusterEnabled.Load() || x.clusterConfig.crdtConfig == nil {
		return nil
	}

	if requiredRole := x.clusterConfig.crdtConfig.Role(); requiredRole != "" {
		if !slices.Contains(x.clusterConfig.roles, requiredRole) {
			return nil
		}
	}

	// register the CRDT config extension so the Replicator can read it
	// from PreStart on initial start and on supervisor restarts.
	ext := &crdtConfigExtension{
		config: x.clusterConfig.crdtConfig,
	}

	if x.clusterConfig.dataCenterConfig != nil {
		ext.dc = x.clusterConfig.dataCenterConfig.DataCenter
	}

	x.extensions.Set(crdtConfigExtensionID, ext)

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

	registration, err := meter.RegisterCallback(func(_ context.Context, observer otelmetric.Observer) error {
		observer.ObserveInt64(metrics.StoreSize(), replActor.storeSize.Load(), observeOptions...)
		observer.ObserveInt64(metrics.MergeCount(), int64(replActor.mergeCount.Load()), observeOptions...)
		observer.ObserveInt64(metrics.DeltaPublishCount(), int64(replActor.deltaPublishCount.Load()), observeOptions...)
		observer.ObserveInt64(metrics.DeltaReceiveCount(), int64(replActor.deltaReceiveCount.Load()), observeOptions...)
		observer.ObserveInt64(metrics.CoordinatedWriteCount(), int64(replActor.coordinatedWriteCount.Load()), observeOptions...)
		observer.ObserveInt64(metrics.CoordinatedReadCount(), int64(replActor.coordinatedReadCount.Load()), observeOptions...)
		observer.ObserveInt64(metrics.AntiEntropyCount(), int64(replActor.antiEntropyCount.Load()), observeOptions...)
		observer.ObserveInt64(metrics.TombstoneCount(), replActor.tombstoneSize.Load(), observeOptions...)
		observer.ObserveInt64(metrics.CrossDCSendCount(), int64(replActor.crossDCSendCount.Load()), observeOptions...)
		observer.ObserveInt64(metrics.CrossDCReceiveCount(), int64(replActor.crossDCReceiveCount.Load()), observeOptions...)
		observer.ObserveInt64(metrics.CrossDCReplicationLag(), replActor.lastReplicationLag.Load()/int64(time.Millisecond), observeOptions...)
		observer.ObserveInt64(metrics.CrossDCStaleSkipCount(), int64(replActor.crossDCStaleSkipCount.Load()), observeOptions...)
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
		metrics.CrossDCSendCount(),
		metrics.CrossDCReceiveCount(),
		metrics.CrossDCReplicationLag(),
		metrics.CrossDCStaleSkipCount(),
	)

	return x.keepMetricRegistration(registration, err)
}

// restoreFromSnapshot opens the snapshot store and restores persisted CRDT state,
// then the contact times saved with it (see restoreContact).
// This is a no-op when snapshot persistence is not configured.
// Note: the serializer is not yet available during PreStart (it is set in
// handlePostStart), so restoreFromSnapshot decodes with its own
// CRDTValueSerializer, the same serializer type handlePostStart installs and
// the snapshot was encoded with.
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

	lastContact, since, err := store.Contact()
	if err != nil {
		r.logger.Warnf("failed to load CRDT snapshot contact times: %v", err)
	}

	entries, err := store.Load()
	if err != nil {
		r.logger.Warnf("failed to load CRDT snapshot: %v", err)
		return nil
	}

	// Snapshot restore happens before the serializer is set (PreStart runs
	// before PostStart). Use the dedicated CRDT value serializer for decoding.
	serializer := ddata.NewCRDTValueSerializer()

	for keyID, entry := range entries {
		entryKeyID, dataType, keyErr := codec.DecodeCRDTKey(entry.GetKey())
		if keyErr != nil {
			r.logger.Warnf("failed to decode snapshot key for %s: %v", keyID, keyErr)
			continue
		}
		data, decErr := ddata.DecodeCRDT(entry.GetData(), serializer)
		if decErr != nil {
			r.logger.Warnf("failed to decode snapshot data for key=%s: %v", entryKeyID, decErr)
			continue
		}

		r.store[entryKeyID] = data
		r.keyTypes[entryKeyID] = dataType
		r.versions[entryKeyID] = entry.GetVersion()
		r.subscriptions[entryKeyID] = types.Unit{}

		if changedAt := entry.GetChangedAtNanos(); changedAt != 0 {
			r.changedAt[entryKeyID] = time.Unix(0, changedAt)
		}
	}

	r.restoreContact(lastContact, since)
	r.logger.Debugf("restored %d CRDT keys from snapshot", len(entries))
	return nil
}

// restoreContact applies the contact times saved with the snapshot, once its
// keys are restored. A node restored after a gap longer than the tombstone
// TTL marks its stale keys at once, before any peer reaches it, so it neither
// advertises the since it had before the gap nor serves its old keys. A node
// restored within the TTL keeps the since it had, when that is earlier than
// its start: it saw every deletion until it stopped, and the tombstones of
// those made since are still live.
func (r *replicatorActor) restoreContact(lastContact, since time.Time) {
	r.lastContact = lastContact
	if r.gapBeyondTTL() {
		r.markStaleKeys()
		return
	}

	if !since.IsZero() && since.Before(r.since) {
		r.since = since
	}
}

// handleDataCenterFlush sends the buffered deltas and tombstones to the
// remote datacenters. Only the cluster leader performs the flush; a node that
// is not the leader skips it and keeps buffering.
//
// There is one buffer for all datacenters and one accepted mark per
// datacenter. Each remote datacenter is sent a CRDTDeltaBatch of the entries
// stamped above its mark, and nothing when there is none. A datacenter that
// takes its batch has its mark raised to the highest sequence number in that
// batch; one that does not keeps its mark and is sent the entries again on
// the next tick, while the datacenters that took theirs are not. An entry is
// dropped once the mark of every remote datacenter on record has reached it.
//
// The flush runs inside one turn of the actor, sends included, so no entry
// is written while it runs; the mark is still taken from the batch that was
// sent and not from the current sequence number, which keeps the rule true
// whatever the send path does.
//
// A datacenter that leaves the records no longer holds entries back and its
// mark is forgotten. One that appears has no mark, so it is sent everything
// still pending. With no remote datacenter on record nothing is dropped. A
// node that becomes the leader has entries and no marks: it sends everything
// pending to every datacenter, which is a set of idempotent merges and
// deletions for those that had received it from the previous leader. A node
// that stops being the leader forgets its marks on its next tick, because the
// datacenters and what they hold may change while another node leads.
func (r *replicatorActor) handleDataCenterFlush(ctx *ReceiveContext) {
	if len(r.pendingDeltas) == 0 && len(r.pendingTombstones) == 0 {
		return
	}

	// a pending delta may be of a stale key; it waits until they are resolved
	if r.hasStaleKeys() {
		return
	}

	if r.clusterRef == nil || r.remoting == nil {
		return
	}

	// only the leader flushes cross-DC
	cctx := context.WithoutCancel(ctx.Context())
	if !r.clusterRef.IsLeader(cctx) {
		clear(r.dataCenterAccepted)
		return
	}

	controller := r.actorSystem.getDataCenterController()
	if controller == nil {
		return
	}

	records, stale := controller.ActiveRecords()
	if stale && controller.FailOnStaleCache() {
		r.crossDCStaleSkipCount.Add(1)
		r.logger.Warnf("cross-DC flush: skipping due to stale DC cache")
		return
	}

	r.sendPendingToRemoteDataCenters(ctx, records)
	r.dropAcceptedPending(records)
}

// sendPendingToRemoteDataCenters sends each remote datacenter the pending
// entries it has not accepted yet and raises the accepted mark of those that
// took their batch. Every pending delta is encoded once, whatever the number
// of datacenters; one that cannot be encoded can never be sent and is removed
// from the buffer.
func (r *replicatorActor) sendPendingToRemoteDataCenters(ctx *ReceiveContext, records []datacenter.DataCenterRecord) {
	encoded := make(map[string]*internalpb.CRDTDelta, len(r.pendingDeltas))

	for keyID, pending := range r.pendingDeltas {
		pb, err := r.encodeDelta(pending.crdtDelta)
		if err != nil {
			r.logger.Errorf("cross-DC flush: failed to encode delta for key=%s: %v", keyID, err)
			delete(r.pendingDeltas, keyID)
			continue
		}

		encoded[keyID] = pb
	}

	from := pathToAddress(r.pid.Path())
	actorName := reservedName(replicatorType)
	me := r.dc.ID()

	for _, record := range records {
		id := record.DataCenter.ID()
		if id == me || len(record.Endpoints) == 0 {
			continue
		}

		batch, highest := r.buildPendingBatch(encoded, r.dataCenterAccepted[id])
		if highest == 0 {
			continue
		}

		if r.sendToDataCenter(ctx, record, from, actorName, batch) {
			r.dataCenterAccepted[id] = highest
		}
	}
}

// buildPendingBatch builds the batch of the pending entries stamped above
// after, the accepted mark of the datacenter it is meant for, from the
// already encoded deltas. It returns the batch and the highest sequence
// number in it, which is zero when the datacenter needs nothing.
func (r *replicatorActor) buildPendingBatch(encoded map[string]*internalpb.CRDTDelta, after uint64) (*internalpb.CRDTDeltaBatch, uint64) {
	var (
		deltas     []*internalpb.CRDTDelta
		tombstones []*internalpb.CRDTTombstone
		highest    uint64
	)

	for keyID, pending := range r.pendingDeltas {
		if pending.seq > after {
			deltas = append(deltas, encoded[keyID])
			highest = max(highest, pending.seq)
		}
	}

	for _, pending := range r.pendingTombstones {
		if pending.seq > after {
			tombstones = append(tombstones, pending.tombstone)
			highest = max(highest, pending.seq)
		}
	}

	batch := &internalpb.CRDTDeltaBatch{}
	batch.SetDeltas(deltas)
	batch.SetTombstones(tombstones)
	batch.SetOriginDc(r.originDCProto)
	batch.SetSentAtNanos(time.Now().UnixNano())
	return batch, highest
}

// dropAcceptedPending removes the pending entries every remote datacenter on
// record has accepted, and forgets the accepted mark of a datacenter that is
// not on record. A datacenter on record without a mark, or without an
// endpoint to send to, has accepted nothing and holds every entry back. With
// no remote datacenter on record nothing is removed.
func (r *replicatorActor) dropAcceptedPending(records []datacenter.DataCenterRecord) {
	me := r.dc.ID()
	onRecord := make(map[string]types.Unit, len(records))

	for _, record := range records {
		if id := record.DataCenter.ID(); id != me {
			onRecord[id] = types.Unit{}
		}
	}

	for id := range r.dataCenterAccepted {
		if _, ok := onRecord[id]; !ok {
			delete(r.dataCenterAccepted, id)
		}
	}

	if len(onRecord) == 0 {
		return
	}

	lowest := r.pendingSeq
	for id := range onRecord {
		lowest = min(lowest, r.dataCenterAccepted[id])
	}

	for keyID, pending := range r.pendingDeltas {
		if pending.seq <= lowest {
			delete(r.pendingDeltas, keyID)
		}
	}

	for keyID, pending := range r.pendingTombstones {
		if pending.seq <= lowest {
			delete(r.pendingTombstones, keyID)
		}
	}
}

// handleIncomingBatch processes a CRDTDeltaBatch received from a remote
// datacenter's replicator and merges each delta and tombstone locally.
//
// Tombstones are applied before deltas. A batch holds a tombstone and a delta
// of the same key only when the sender deleted the key and created it again
// after its tombstone expired, so the deletion comes first: it removes the
// incarnation held here, and the delta then creates the new one, which leaves
// this node with the value the sender holds. The tombstone of such a key is
// older than the tombstone TTL and is therefore not kept, so it does not
// reject the delta that follows it. A tombstone still within its TTL is kept
// and rejects a delta of its key in the same batch, as it rejects any other.
func (r *replicatorActor) handleIncomingBatch(ctx *ReceiveContext, batch *internalpb.CRDTDeltaBatch) {
	originDC := batch.GetOriginDc()
	if originDC != nil && originDC.GetName() == r.dc.Name &&
		originDC.GetRegion() == r.dc.Region &&
		originDC.GetZone() == r.dc.Zone {
		return
	}

	r.lastReplicationLag.Store(time.Now().UnixNano() - batch.GetSentAtNanos())
	r.crossDCReceiveCount.Add(1)

	for _, ts := range batch.GetTombstones() {
		r.handleProtoTombstone(ctx, ts)
	}

	for _, delta := range batch.GetDeltas() {
		r.handleProtoDelta(ctx, delta)
	}
}

// handleDataCenterDigestRequest responds with the local digest so that
// a remote datacenter's replicator can perform cross-DC anti-entropy.
func (r *replicatorActor) handleDataCenterDigestRequest(ctx *ReceiveContext) {
	if ctx.Sender() != nil {
		ctx.Response(r.buildDigest())
	}
}

// handleDataCenterAntiEntropy runs one round of cross-datacenter anti-entropy
// by exchanging a digest with a random remote datacenter's replicator.
// Only the cluster leader performs this; non-leaders skip silently.
func (r *replicatorActor) handleDataCenterAntiEntropy(ctx *ReceiveContext) {
	if r.clusterRef == nil || r.remoting == nil {
		return
	}

	cctx := context.WithoutCancel(ctx.Context())
	if !r.clusterRef.IsLeader(cctx) {
		return
	}

	controller := r.actorSystem.getDataCenterController()
	if controller == nil {
		return
	}

	records, stale := controller.ActiveRecords()
	if stale && controller.FailOnStaleCache() {
		return
	}

	me := r.dc.ID()

	remoteCount := 0
	for _, record := range records {
		if record.DataCenter.ID() != me && len(record.Endpoints) > 0 {
			remoteCount++
		}
	}

	if remoteCount == 0 {
		return
	}

	target := rand.IntN(remoteCount) //nolint:gosec
	var record datacenter.DataCenterRecord
	idx := 0
	for _, rec := range records {
		if rec.DataCenter.ID() != me && len(rec.Endpoints) > 0 {
			if idx == target {
				record = rec
				break
			}
			idx++
		}
	}

	endpoint := record.Endpoints[rand.IntN(len(record.Endpoints))] //nolint:gosec

	host, portStr, err := net.SplitHostPort(endpoint)
	if err != nil {
		r.logger.Debugf("cross-DC anti-entropy: invalid endpoint %s: %v", endpoint, err)
		return
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		r.logger.Debugf("cross-DC anti-entropy: invalid port in %s: %v", endpoint, err)
		return
	}

	sendCtx, cancel := context.WithTimeout(cctx, r.config.DataCenterSendTimeout())
	defer cancel()

	actorName := reservedName(replicatorType)
	to, err := r.remoting.RemoteLookup(sendCtx, host, port, actorName)
	if err != nil {
		r.logger.Warnf("cross-DC anti-entropy: failed to lookup replicator on %s: %v", endpoint, err)
		return
	}

	digest := r.buildDigest()
	from := pathToAddress(r.pid.Path())
	if err := r.remoting.RemoteTell(sendCtx, from, to, digest); err != nil {
		r.logger.Warnf("cross-DC anti-entropy: failed to send digest to %s: %v", endpoint, err)
	}
}

// sendToDataCenter attempts to deliver a batch to a single remote DC.
// It shuffles the endpoint list and tries each one until a send succeeds
// or all endpoints are exhausted. It reports whether an endpoint took the
// batch.
func (r *replicatorActor) sendToDataCenter(ctx *ReceiveContext, record datacenter.DataCenterRecord, from *address.Address, actorName string, batch *internalpb.CRDTDeltaBatch) bool {
	endpoints := make([]string, len(record.Endpoints))
	copy(endpoints, record.Endpoints)
	rand.Shuffle(len(endpoints), func(i, j int) { //nolint:gosec
		endpoints[i], endpoints[j] = endpoints[j], endpoints[i]
	})

	timeout := r.config.DataCenterSendTimeout()
	dcID := record.DataCenter.ID()

	for _, endpoint := range endpoints {
		host, portStr, err := net.SplitHostPort(endpoint)
		if err != nil {
			r.logger.Debugf("cross-DC flush: invalid endpoint %s: %v", endpoint, err)
			continue
		}

		port, err := strconv.Atoi(portStr)
		if err != nil {
			r.logger.Debugf("cross-DC flush: invalid port in %s: %v", endpoint, err)
			continue
		}

		sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx.Context()), timeout)

		to, err := r.remoting.RemoteLookup(sendCtx, host, port, actorName)
		if err != nil {
			cancel()
			r.logger.Warnf("cross-DC flush: failed to lookup replicator on %s: %v", endpoint, err)
			continue
		}

		if err := r.remoting.RemoteTell(sendCtx, from, to, batch); err != nil {
			cancel()
			r.logger.Warnf("cross-DC flush: failed to send batch to %s: %v", endpoint, err)
			continue
		}

		cancel()
		r.crossDCSendCount.Add(1)
		return true
	}

	r.logger.Warnf("cross-DC flush: all endpoints exhausted for DC %s", dcID)
	return false
}
