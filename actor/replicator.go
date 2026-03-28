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
	"strconv"
	"sync/atomic"

	"github.com/tochemey/goakt/v4/crdt"
	"github.com/tochemey/goakt/v4/internal/codec"
	"github.com/tochemey/goakt/v4/internal/internalpb"
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
// subscribe to the same CRDT key topics via the TopicActor. When any
// replicator updates a key, it publishes the delta to that key's topic.
// Because every replicator is subscribed to the same topic, they all
// receive the delta automatically.
type replicatorActor struct {
	pid           *PID
	topicActor    *PID
	logger        log.Logger
	config        *crdt.Config
	nodeID        string
	store         map[string]crdt.ReplicatedData
	subscriptions map[string]types.Unit
	watchers      map[string][]*PID
	msgSeq        atomic.Uint64
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
func (r *replicatorActor) PreStart(ctx *Context) error {
	ext := ctx.Extension(crdtConfigExtensionID)
	if ext == nil {
		return fmt.Errorf("crdt config extension not found")
	}
	r.config = ext.(*crdtConfigExtension).Config()
	r.store = make(map[string]crdt.ReplicatedData)
	r.subscriptions = make(map[string]types.Unit)
	r.watchers = make(map[string][]*PID)
	r.logger = ctx.ActorSystem().Logger()
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
	r.logger.Infof("actor=%s stopped successfully", ctx.ActorName())
	r.store = nil
	r.subscriptions = nil
	r.watchers = nil
	return nil
}

// handlePostStart completes initialization that requires a running actor context.
// The PID, TopicActor reference, and topic subscription require the actor to be started.
func (r *replicatorActor) handlePostStart(ctx *ReceiveContext) {
	r.pid = ctx.Self()
	r.topicActor = ctx.ActorSystem().TopicActor()
	r.nodeID = r.pid.ID()

	// subscribe to the CRDT delta topic so this Replicator receives
	// deltas from all peer Replicators in the cluster.
	if r.topicActor != nil {
		ctx.Tell(r.topicActor, NewSubscribe(crdtTopic))
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
	default:
		ctx.Unhandled()
	}
}

// handleUpdate applies a local CRDT mutation and publishes the delta.
func (r *replicatorActor) handleUpdate(ctx *ReceiveContext, msg updateCommand) {
	keyID := msg.KeyID()

	current, exists := r.store[keyID]
	if !exists {
		current = msg.InitialValue()
		r.trackKey(keyID)
	}

	updated := msg.Apply(current)
	delta := updated.Delta()
	updated.ResetDelta()
	r.store[keyID] = updated

	if delta != nil {
		r.publishDelta(ctx, keyID, msg.CRDTDataType(), delta)
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

// handleDelete removes a CRDT key from the local store.
func (r *replicatorActor) handleDelete(ctx *ReceiveContext, msg deleteCommand) {
	keyID := msg.KeyID()
	delete(r.store, keyID)

	if ctx.Sender() != nil {
		ctx.Response(&crdt.DeleteResponse{})
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

	keyID := msg.KeyID

	current, exists := r.store[keyID]
	if !exists {
		r.store[keyID] = msg.Delta
		r.trackKey(keyID)
		r.notifyChanged(ctx, keyID, msg.Delta)
		return
	}

	merged := current.Merge(msg.Delta)
	r.store[keyID] = merged
	r.notifyChanged(ctx, keyID, merged)
}

// trackKey records that a CRDT key exists in the local store.
func (r *replicatorActor) trackKey(keyID string) {
	r.subscriptions[keyID] = types.Unit{}
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
		r.logger.Warnf("failed to encode CRDT delta for key=%s: %v", keyID, err)
		return
	}
	msgID := r.nodeID + ":" + strconv.FormatUint(r.msgSeq.Add(1), 10)
	ctx.Tell(r.topicActor, NewPublish(msgID, crdtTopic, pb))
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
}

// getCommand is implemented by crdt.Get[T] for any T.
type getCommand interface {
	KeyID() string
	Response(data crdt.ReplicatedData) any
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
	keyID, dataType := codec.DecodeCRDTKey(pb.GetKey())
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

	var err error
	x.replicator, err = x.configPID(ctx,
		actorName,
		newReplicatorActor(),
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

	// the replicator is a child actor of the system guardian
	return x.actors.addNode(x.systemGuardian, x.replicator)
}
