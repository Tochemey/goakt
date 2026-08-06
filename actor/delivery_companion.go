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
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"

	"github.com/tochemey/goakt/v4/internal/address"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/types"
	"github.com/tochemey/goakt/v4/supervisor"
)

// errReliableCompanionUnavailable reports that an endpoint's reliable-delivery
// companion pair is missing, mixed across incarnations, or not yet live. The
// condition is transient: controllers keep the current state and retry on
// their next tick.
var errReliableCompanionUnavailable = errors.New("reliable delivery companion is unavailable")

// reliableCompanionSpec is the runtime-companion metadata that marks a PID as
// the endpoint-owned reliable-delivery controller of one endpoint incarnation.
// Its presence on a PID is the runtime-companion kind; ordinary actors never
// carry it.
type reliableCompanionSpec struct {
	// role identifies which controller this companion runs for its endpoint.
	role ReliableControllerRole
	// endpointName is the user-visible name of the owning endpoint and plays
	// the ParentName part of ownership validation.
	endpointName string
	// endpointIncarnationID pins the companion to one endpoint incarnation so
	// a stale companion can never be adopted by a newer endpoint.
	endpointIncarnationID string
}

// toProto converts the runtime-companion metadata to its wire form. A nil
// spec serializes as absent so ordinary actor records stay unchanged.
func (x *reliableCompanionSpec) toProto() *internalpb.ReliableCompanionSpec {
	if x == nil {
		return nil
	}

	return &internalpb.ReliableCompanionSpec{
		Role:                  x.role.toProto(),
		EndpointName:          x.endpointName,
		EndpointIncarnationId: x.endpointIncarnationID,
	}
}

// reliableCompanionSpecFromProto validates and restores runtime-companion
// metadata from its wire form through the same constructor local spawns use,
// so a malformed record can never produce a spec that resolution would trust.
func reliableCompanionSpecFromProto(proto *internalpb.ReliableCompanionSpec) (*reliableCompanionSpec, error) {
	if proto == nil {
		return nil, errors.New("reliable companion specification is required")
	}

	return newReliableCompanionSpec(reliableControllerRoleFromProto(proto.GetRole()), proto.GetEndpointName(), proto.GetEndpointIncarnationId())
}

// newReliableCompanionSpec validates and builds runtime-companion metadata.
func newReliableCompanionSpec(role ReliableControllerRole, endpointName, endpointIncarnationID string) (*reliableCompanionSpec, error) {
	if !role.valid() {
		return nil, errors.New("reliable controller role is not supported")
	}

	if types.IsBlank(endpointName) {
		return nil, errors.New("endpoint name is required")
	}

	if err := uuid.Validate(endpointIncarnationID); err != nil {
		return nil, fmt.Errorf("endpoint incarnation ID is invalid: %w", err)
	}

	return &reliableCompanionSpec{
		role:                  role,
		endpointName:          endpointName,
		endpointIncarnationID: endpointIncarnationID,
	}, nil
}

// reliableCompanionName derives the reserved controller identity owned by one
// endpoint incarnation and role. It returns an empty string for an
// unsupported role.
func reliableCompanionName(role ReliableControllerRole, endpointIncarnationID string) string {
	switch role {
	case ReliableControllerRoleProducer:
		return reliableProducerControllerNamePrefix + endpointIncarnationID
	case ReliableControllerRoleConsumer:
		return reliableConsumerControllerNamePrefix + endpointIncarnationID
	default:
		return ""
	}
}

// resolveReliableCompanion returns the live controller companion bound to the
// named endpoint's current incarnation using local-first resolution: the
// endpoint is looked up in the local actor tree, its incarnation selects the
// role-specific companion identity, and the companion is returned only when
// its runtime kind, role, owning endpoint name, and endpoint incarnation all
// validate. Only when no local endpoint record exists at all does resolution
// fall to the cluster registry; a present-but-invalid local pair never does,
// because a mixed local activation means a spawn or restart is in flight and
// an older registry record must not win over it. A missing endpoint, a
// missing companion, or a mixed pair is reported as
// errReliableCompanionUnavailable; callers retry on their next tick.
func (x *actorSystem) resolveReliableCompanion(ctx context.Context, endpointName string, role ReliableControllerRole) (*PID, error) {
	if !role.valid() {
		return nil, errors.New("reliable controller role is not supported")
	}

	node, ok := x.actors.nodeByName(endpointName)
	if !ok {
		return x.resolveRemoteReliableCompanion(ctx, endpointName, role)
	}

	endpoint := node.value()
	if !endpoint.IsRunning() {
		return nil, fmt.Errorf("%w: endpoint=%s is not running", errReliableCompanionUnavailable, endpointName)
	}

	companionNode, ok := x.actors.nodeByName(reliableCompanionName(role, endpoint.IncarnationID()))
	if !ok {
		return nil, fmt.Errorf("%w: endpoint=%s has no %s controller for incarnation=%s", errReliableCompanionUnavailable, endpointName, role, endpoint.IncarnationID())
	}

	companion := companionNode.value()
	if err := validateReliableCompanion(endpoint, companion, role); err != nil {
		return nil, err
	}

	return companion, nil
}

// resolveRemoteReliableCompanion resolves the controller companion of an
// endpoint that has no local record through the cluster registry: the
// endpoint record's incarnation selects the companion identity, the companion
// record must carry a runtime-companion spec whose role, owning endpoint, and
// incarnation match, and the pair must live together on one remote node. Any
// missing or mismatching record is the transient mixed-pair condition, which
// covers both the publication window where the endpoint is visible before its
// companion and a stale record left by an older activation. A record pointing
// back at this node is also transient: the local tree is authoritative here,
// so a registry self-reference only exists while local cleanup is in flight.
func (x *actorSystem) resolveRemoteReliableCompanion(ctx context.Context, endpointName string, role ReliableControllerRole) (*PID, error) {
	if !x.clusterEnabled.Load() {
		return nil, fmt.Errorf("%w: endpoint=%s has no local record", errReliableCompanionUnavailable, endpointName)
	}

	registry := x.getCluster()

	endpoint, err := registry.GetActor(ctx, endpointName)
	if err != nil {
		return nil, fmt.Errorf("%w: endpoint=%s has no registry record: %v", errReliableCompanionUnavailable, endpointName, err)
	}

	name := reliableCompanionName(role, endpoint.GetIncarnationId())
	record, err := registry.GetActor(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("%w: endpoint=%s has no published %s controller for incarnation=%s: %v", errReliableCompanionUnavailable, endpointName, role, endpoint.GetIncarnationId(), err)
	}

	spec, specErr := reliableCompanionSpecFromProto(record.GetReliableCompanion())
	endpointAddress, endpointErr := address.Parse(endpoint.GetAddress())
	companionAddress, companionErr := address.Parse(record.GetAddress())

	switch {
	case specErr != nil:
		return nil, fmt.Errorf("%w: record=%s is not a runtime companion: %v", errReliableCompanionUnavailable, name, specErr)
	case spec.role != role:
		return nil, fmt.Errorf("%w: companion=%s runs role=%s, want role=%s", errReliableCompanionUnavailable, name, spec.role, role)
	case spec.endpointName != endpointName:
		return nil, fmt.Errorf("%w: companion=%s is owned by endpoint=%s, want endpoint=%s", errReliableCompanionUnavailable, name, spec.endpointName, endpointName)
	case spec.endpointIncarnationID != endpoint.GetIncarnationId():
		return nil, fmt.Errorf("%w: companion=%s is bound to incarnation=%s, want incarnation=%s", errReliableCompanionUnavailable, name, spec.endpointIncarnationID, endpoint.GetIncarnationId())
	case endpointErr != nil || companionErr != nil:
		return nil, fmt.Errorf("%w: registry records for endpoint=%s carry an invalid address", errReliableCompanionUnavailable, endpointName)
	case endpointAddress.HostPort() != companionAddress.HostPort():
		return nil, fmt.Errorf("%w: endpoint=%s and companion=%s records live on different nodes", errReliableCompanionUnavailable, endpointName, name)
	case companionAddress.HostPort() == x.actorReference(name).HostPort():
		return nil, fmt.Errorf("%w: registry records for endpoint=%s point at this node but no local pair exists", errReliableCompanionUnavailable, endpointName)
	default:
		return newRemotePID(companionAddress, x.getRemoting()), nil
	}
}

// reliableTickReference derives the scheduler reference of one controller
// incarnation's recurring tick from its name and generation. Deriving the
// reference instead of storing it on the controller removes the shared field
// that an external shutdown's PostStop could otherwise read while PostStart
// is still writing it, and the generation keeps every restart's reference
// unique so a schedule leaked by that same overlap can never collide with the
// next incarnation's.
func reliableTickReference(name string, generation uint64) string {
	return name + "-tick-" + strconv.FormatUint(generation, 10)
}

// reliableCompanionSupervisor returns the supervisor attached to every
// reliable-delivery controller. Any processing error restarts the controller,
// because the failure classification only lets transient conditions surface
// as errors: durable queue retry exhaustion reaches the supervisor through
// ctx.Err so a restart reloads durable state and resumes, while deterministic
// failures never get here because the controller publishes
// ReliableDeliveryFailed and stops itself.
func reliableCompanionSupervisor() *supervisor.Supervisor {
	return supervisor.NewSupervisor(supervisor.WithAnyErrorDirective(supervisor.RestartDirective))
}

// ensureReliableCompanion creates the endpoint-owned controller companion of a
// reliable endpoint when its current incarnation does not already have a live
// one. It is the second half of the endpoint spawn transaction and the
// recovery step of ActorSystem.ReSpawn: a live companion is left untouched so
// recovery can never duplicate a controller, a companion still tearing down is
// reported as an error the caller may retry, and otherwise the role-specific
// controller is constructed from the endpoint's retained settings and attached
// to the tree as a child of the endpoint, so endpoint shutdown and restart
// carry the controller with them. The caller owns endpoint rollback when
// creation fails.
func (x *actorSystem) ensureReliableCompanion(ctx context.Context, endpoint *PID) error {
	role := endpoint.reliableDelivery.role()

	name := reliableCompanionName(role, endpoint.IncarnationID())
	if node, ok := x.actors.nodeByName(name); ok {
		if companion := node.value(); companion != nil && companion.IsRunning() {
			return nil
		}

		return fmt.Errorf("%s controller for endpoint=%s is still terminating", role, endpoint.Name())
	}

	spec, err := newReliableCompanionSpec(role, endpoint.Name(), endpoint.IncarnationID())
	if err != nil {
		return err
	}

	pid, err := x.configPID(ctx, name, newReliableController(endpoint), asSystem(), asReliableCompanion(spec), WithSupervisor(reliableCompanionSupervisor()))
	if err != nil {
		return err
	}

	_, err = x.attachAndPublish(ctx, endpoint, pid)
	return err
}

// rollbackReliableSpawn undoes a failed reliable endpoint spawn transaction:
// it stops the endpoint subtree, which takes any attached companion with it,
// then removes both published registry records so a failed spawn leaves
// nothing behind cluster-wide. The companion record is removed by its
// incarnation-scoped name, which no other activation can own; the endpoint
// record only when it still carries this activation's incarnation. Runs
// detached from the caller's ctx because rollback must proceed even when the
// failure was a cancellation.
func (x *actorSystem) rollbackReliableSpawn(ctx context.Context, endpoint *PID) {
	x.rollbackSpawn(ctx, endpoint, "failed reliable controller creation")

	if !x.clusterEnabled.Load() {
		return
	}

	ctx = context.WithoutCancel(ctx)
	name := reliableCompanionName(endpoint.reliableDelivery.role(), endpoint.IncarnationID())

	if err := x.getCluster().RemoveActor(ctx, name); err != nil {
		x.logger.Errorf("failed to remove registry record for reliable controller of endpoint=%s during rollback: %v", endpoint.Name(), err)
	}

	x.removeActorIfIncarnation(ctx, endpoint.Name(), endpoint.IncarnationID())
}

// releaseDepartedReliableCompanion removes the registry record of the
// controller companion owned by a departed reliable endpoint activation. The
// companion identity is derived from the departed record's role and
// incarnation, and the removal reuses the releaseDepartedEntry ownership
// comparison, so it can only ever delete a record still pointing at the
// departed node and can never touch the fresh controller a relocation
// respawn creates under a new incarnation. Best effort: the stale record is
// incarnation-scoped and can never be adopted, so a failed release is logged
// rather than failing the relocation that triggered it.
func (x *actorSystem) releaseDepartedReliableCompanion(ctx context.Context, props *internalpb.Actor, departedNode string) {
	// the role only depends on which endpoint side the wire configuration
	// sets, mirroring reliableDeliveryConfig.role, so the release never
	// decodes settings it does not use
	role := ReliableControllerRoleConsumer
	if props.GetReliableDelivery().GetProducer() != nil {
		role = ReliableControllerRoleProducer
	}

	name := reliableCompanionName(role, props.GetIncarnationId())
	if _, err := x.releaseDepartedEntry(ctx, name, departedNode); err != nil {
		x.logger.Errorf("failed to release registry record of the departed reliable controller=%s: %v", name, err)
	}
}

// newReliableController builds the role-specific controller of a reliable
// endpoint from its retained settings: the producer side carries the durable
// queue instance and retry policy (or the work-pulling multiplexer when that
// pattern is selected), the consumer side carries flow control.
func newReliableController(endpoint *PID) Actor {
	config := endpoint.reliableDelivery

	if producerConfig := config.producer; producerConfig != nil {
		if producerConfig.workPulling {
			return newWorkPullingProducerController(endpoint, producerConfig)
		}

		return newProducerController(endpoint, producerConfig, endpoint.durableQueue)
	}

	consumerConfig := config.consumer
	return newConsumerController(endpoint, consumerConfig.producerName, consumerConfig.flowControlWindow, consumerConfig.resendInterval)
}

// authenticateWorkPullingWorker verifies that sender is the live consumer
// companion of an endpoint whose reliable-consumer configuration names
// producerName. Companion names are self-describing, so fencing starts from
// the sender rather than a preconfigured peer list: local senders are checked
// against the local tree, and remote senders against the cluster registry.
// Success returns the verified companion and the worker endpoint name.
func (x *actorSystem) authenticateWorkPullingWorker(ctx context.Context, sender *PID, producerName string) (*PID, string, error) {
	if sender == nil {
		return nil, "", errors.New("registration sender is required")
	}

	if types.IsBlank(producerName) {
		return nil, "", errors.New("producer endpoint name is required")
	}

	if sender.IsLocal() {
		return x.authenticateLocalWorkPullingWorker(sender, producerName)
	}

	return x.authenticateRemoteWorkPullingWorker(ctx, sender, producerName)
}

// authenticateLocalWorkPullingWorker fences a local registration sender against
// the local actor tree and the owning endpoint's consumer configuration.
func (x *actorSystem) authenticateLocalWorkPullingWorker(sender *PID, producerName string) (*PID, string, error) {
	spec := sender.reliableCompanion
	if spec == nil || spec.role != ReliableControllerRoleConsumer {
		return nil, "", fmt.Errorf("%w: sender=%s is not a consumer companion", errReliableCompanionUnavailable, sender.Name())
	}

	node, ok := x.actors.nodeByName(spec.endpointName)
	if !ok {
		return nil, "", fmt.Errorf("%w: worker endpoint=%s has no local record", errReliableCompanionUnavailable, spec.endpointName)
	}

	endpoint := node.value()
	if !endpoint.IsRunning() {
		return nil, "", fmt.Errorf("%w: worker endpoint=%s is not running", errReliableCompanionUnavailable, spec.endpointName)
	}

	if err := validateReliableCompanion(endpoint, sender, ReliableControllerRoleConsumer); err != nil {
		return nil, "", err
	}

	consumer := endpoint.reliableDelivery
	if consumer == nil || consumer.consumer == nil || consumer.consumer.producerName != producerName {
		return nil, "", fmt.Errorf("%w: worker endpoint=%s does not name producer=%s", errReliableCompanionUnavailable, spec.endpointName, producerName)
	}

	return sender, spec.endpointName, nil
}

// authenticateRemoteWorkPullingWorker fences a remote registration sender
// through the cluster registry: the sender's companion record must validate,
// live with its owning endpoint on one node, and that endpoint's consumer
// configuration must name producerName.
func (x *actorSystem) authenticateRemoteWorkPullingWorker(ctx context.Context, sender *PID, producerName string) (*PID, string, error) {
	if !x.clusterEnabled.Load() {
		return nil, "", fmt.Errorf("%w: remote worker registration requires cluster mode", errReliableCompanionUnavailable)
	}

	registry := x.getCluster()
	record, err := registry.GetActor(ctx, sender.Name())
	if err != nil {
		return nil, "", fmt.Errorf("%w: companion=%s has no registry record: %v", errReliableCompanionUnavailable, sender.Name(), err)
	}

	spec, specErr := reliableCompanionSpecFromProto(record.GetReliableCompanion())
	companionAddress, companionErr := address.Parse(record.GetAddress())

	switch {
	case specErr != nil:
		return nil, "", fmt.Errorf("%w: record=%s is not a runtime companion: %v", errReliableCompanionUnavailable, sender.Name(), specErr)
	case spec.role != ReliableControllerRoleConsumer:
		return nil, "", fmt.Errorf("%w: companion=%s runs role=%s, want role=%s", errReliableCompanionUnavailable, sender.Name(), spec.role, ReliableControllerRoleConsumer)
	case companionErr != nil:
		return nil, "", fmt.Errorf("%w: companion=%s carries an invalid address", errReliableCompanionUnavailable, sender.Name())
	case !companionAddress.Equals(pathToAddress(sender.Path())):
		return nil, "", fmt.Errorf("%w: companion=%s address does not match the registration sender", errReliableCompanionUnavailable, sender.Name())
	}

	endpoint, err := registry.GetActor(ctx, spec.endpointName)
	if err != nil {
		return nil, "", fmt.Errorf("%w: worker endpoint=%s has no registry record: %v", errReliableCompanionUnavailable, spec.endpointName, err)
	}

	endpointAddress, endpointErr := address.Parse(endpoint.GetAddress())
	delivery, deliveryErr := reliableDeliveryConfigFromProto(endpoint.GetReliableDelivery())

	switch {
	case endpointErr != nil:
		return nil, "", fmt.Errorf("%w: worker endpoint=%s carries an invalid address", errReliableCompanionUnavailable, spec.endpointName)
	case endpointAddress.HostPort() != companionAddress.HostPort():
		return nil, "", fmt.Errorf("%w: worker endpoint=%s and companion=%s records live on different nodes", errReliableCompanionUnavailable, spec.endpointName, sender.Name())
	case spec.endpointIncarnationID != endpoint.GetIncarnationId():
		return nil, "", fmt.Errorf("%w: companion=%s is bound to incarnation=%s, want incarnation=%s", errReliableCompanionUnavailable, sender.Name(), spec.endpointIncarnationID, endpoint.GetIncarnationId())
	case deliveryErr != nil || delivery == nil || delivery.consumer == nil:
		return nil, "", fmt.Errorf("%w: worker endpoint=%s has no consumer configuration", errReliableCompanionUnavailable, spec.endpointName)
	case delivery.consumer.producerName != producerName:
		return nil, "", fmt.Errorf("%w: worker endpoint=%s does not name producer=%s", errReliableCompanionUnavailable, spec.endpointName, producerName)
	default:
		return sender, spec.endpointName, nil
	}
}

// validateReliableCompanion enforces local-tree ownership of a companion:
// runtime-companion kind, controller role, owning endpoint name, endpoint
// incarnation, and liveness must all match the endpoint. Any mismatch is the
// transient mixed-pair condition.
func validateReliableCompanion(endpoint, companion *PID, role ReliableControllerRole) error {
	spec := companion.reliableCompanion

	switch {
	case spec == nil:
		return fmt.Errorf("%w: actor=%s is not a runtime companion", errReliableCompanionUnavailable, companion.Name())
	case spec.role != role:
		return fmt.Errorf("%w: companion=%s runs role=%s, want role=%s", errReliableCompanionUnavailable, companion.Name(), spec.role, role)
	case spec.endpointName != endpoint.Name():
		return fmt.Errorf("%w: companion=%s is owned by endpoint=%s, want endpoint=%s", errReliableCompanionUnavailable, companion.Name(), spec.endpointName, endpoint.Name())
	case spec.endpointIncarnationID != endpoint.IncarnationID():
		return fmt.Errorf("%w: companion=%s is bound to incarnation=%s, want incarnation=%s", errReliableCompanionUnavailable, companion.Name(), spec.endpointIncarnationID, endpoint.IncarnationID())
	case !companion.IsRunning():
		return fmt.Errorf("%w: companion=%s is not running", errReliableCompanionUnavailable, companion.Name())
	default:
		return nil
	}
}
