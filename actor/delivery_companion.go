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
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/tochemey/goakt/v4/internal/types"
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
// validate. A missing endpoint, a missing companion, or a mixed pair is
// reported as errReliableCompanionUnavailable and never falls back to an
// older record; callers retry. Cluster-backed resolution of remote endpoints
// arrives with companion publication.
func (x *actorSystem) resolveReliableCompanion(endpointName string, role ReliableControllerRole) (*PID, error) {
	if !role.valid() {
		return nil, errors.New("reliable controller role is not supported")
	}

	node, ok := x.actors.nodeByName(endpointName)
	if !ok {
		return nil, fmt.Errorf("%w: endpoint=%s has no local record", errReliableCompanionUnavailable, endpointName)
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
