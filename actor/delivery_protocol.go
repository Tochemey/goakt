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
	"bytes"
	"errors"
	"slices"
	"time"

	gerrors "github.com/tochemey/goakt/v4/errors"
	"github.com/tochemey/goakt/v4/internal/internalpb"
	"github.com/tochemey/goakt/v4/internal/types"
)

// MaxFlowControlWindow is the highest demand window a consumer may grant per
// request; it also bounds the consumer controller's receive buffer.
const MaxFlowControlWindow = 10_000

const (
	// reliableControllerRoleUnknown is the zero value for an unspecified role.
	reliableControllerRoleUnknown ReliableControllerRole = iota
	// ReliableControllerRoleProducer identifies a producer controller.
	ReliableControllerRoleProducer
	// ReliableControllerRoleConsumer identifies a consumer controller.
	ReliableControllerRoleConsumer
)

const (
	// reliableDeliveryStageUnknown is the zero value for an unspecified stage.
	reliableDeliveryStageUnknown ReliableDeliveryStage = iota
	// ReliableDeliveryStageLoad identifies durable-state loading.
	ReliableDeliveryStageLoad
	// ReliableDeliveryStageStore identifies durable message storage.
	ReliableDeliveryStageStore
	// ReliableDeliveryStageAccept identifies producer acceptance.
	ReliableDeliveryStageAccept
	// ReliableDeliveryStageConfirm identifies durable confirmation.
	ReliableDeliveryStageConfirm
	// ReliableDeliveryStageProtocol identifies a protocol violation.
	ReliableDeliveryStageProtocol
)

// ReliablePayload is an immutable serialized message snapshot. The serializer
// frame contains the information needed to restore the concrete message type.
type ReliablePayload struct {
	// The bytes hold the serialized message snapshot.
	bytes []byte
}

// NewReliablePayload creates a reliable payload. The byte slice is copied.
func NewReliablePayload(data []byte) (ReliablePayload, error) {
	return newReliablePayload(bytes.Clone(data))
}

// newReliablePayload creates a reliable payload that owns data.
func newReliablePayload(data []byte) (ReliablePayload, error) {
	if len(data) == 0 {
		return ReliablePayload{}, gerrors.NewErrInvalidMessage(errors.New("serialized payload is required"))
	}

	return ReliablePayload{
		bytes: data,
	}, nil
}

// Bytes returns a copy of the serialized message.
func (x ReliablePayload) Bytes() []byte {
	return bytes.Clone(x.bytes)
}

// Equal reports whether both payloads contain the same serialized frame.
func (x ReliablePayload) Equal(other ReliablePayload) bool {
	return bytes.Equal(x.bytes, other.bytes)
}

// rawBytes returns the serialized message without copying it. Callers must not
// retain or modify the returned slice.
func (x ReliablePayload) rawBytes() []byte {
	return x.bytes
}

// UnconfirmedMessage is a stored message awaiting consumer confirmation.
type UnconfirmedMessage struct {
	// messageID is the idempotency key supplied by the producer.
	messageID string
	// seq is the message's ordered position in the producer stream.
	seq int64
	// payload is the immutable serialized application message.
	payload ReliablePayload
}

// NewUnconfirmedMessage creates an immutable stored-message snapshot.
func NewUnconfirmedMessage(messageID string, seq int64, payload ReliablePayload) (UnconfirmedMessage, error) {
	if types.IsBlank(messageID) {
		return UnconfirmedMessage{}, gerrors.NewErrInvalidMessage(errors.New("message ID is required"))
	}

	if seq <= 0 {
		return UnconfirmedMessage{}, gerrors.NewErrInvalidMessage(errors.New("sequence must be greater than zero"))
	}

	if len(payload.rawBytes()) == 0 {
		return UnconfirmedMessage{}, gerrors.NewErrInvalidMessage(errors.New("payload is required"))
	}

	return UnconfirmedMessage{
		messageID: messageID,
		seq:       seq,
		payload:   payload,
	}, nil
}

// MessageID returns the producer's idempotency key.
func (x UnconfirmedMessage) MessageID() string {
	return x.messageID
}

// Seq returns the message's ordered position in the producer stream.
func (x UnconfirmedMessage) Seq() int64 {
	return x.seq
}

// Payload returns the immutable serialized application message.
func (x UnconfirmedMessage) Payload() ReliablePayload {
	return x.payload
}

// StoreRequest describes one message the producer wants to append.
type StoreRequest struct {
	// messageID is the idempotency key supplied by the producer.
	messageID string
	// proposedSeq must be the queue's current sequence plus one for a new ID.
	proposedSeq int64
	// payload is the serialized message snapshot proposed for the first write.
	payload ReliablePayload
}

// NewStoreRequest creates an immutable append request.
func NewStoreRequest(messageID string, proposedSeq int64, payload ReliablePayload) (StoreRequest, error) {
	if types.IsBlank(messageID) {
		return StoreRequest{}, gerrors.NewErrInvalidMessage(errors.New("message ID is required"))
	}

	if proposedSeq <= 0 {
		return StoreRequest{}, gerrors.NewErrInvalidMessage(errors.New("proposed sequence must be greater than zero"))
	}

	if len(payload.rawBytes()) == 0 {
		return StoreRequest{}, gerrors.NewErrInvalidMessage(errors.New("payload is required"))
	}

	return StoreRequest{
		messageID:   messageID,
		proposedSeq: proposedSeq,
		payload:     payload,
	}, nil
}

// MessageID returns the producer's idempotency key.
func (x StoreRequest) MessageID() string {
	return x.messageID
}

// ProposedSeq returns the ordered position proposed for a new message.
func (x StoreRequest) ProposedSeq() int64 {
	return x.proposedSeq
}

// Payload returns the immutable proposed serialized message.
func (x StoreRequest) Payload() ReliablePayload {
	return x.payload
}

// StoreResult reports the authoritative first write for a message ID.
type StoreResult struct {
	// seq is the sequence assigned by the first successful Store.
	seq int64
	// alreadyStored reports whether Store found an existing message ID.
	alreadyStored bool
	// payload is the first-write snapshot retained by the queue.
	payload ReliablePayload
}

// NewStoreResult creates an immutable Store result.
func NewStoreResult(seq int64, alreadyStored bool, payload ReliablePayload) (StoreResult, error) {
	if seq <= 0 {
		return StoreResult{}, gerrors.NewErrInvalidMessage(errors.New("sequence must be greater than zero"))
	}

	if len(payload.rawBytes()) == 0 {
		return StoreResult{}, gerrors.NewErrInvalidMessage(errors.New("payload is required"))
	}

	return StoreResult{
		seq:           seq,
		alreadyStored: alreadyStored,
		payload:       payload,
	}, nil
}

// Seq returns the sequence assigned by the first successful Store.
func (x StoreResult) Seq() int64 {
	return x.seq
}

// AlreadyStored reports whether Store found an existing message ID.
func (x StoreResult) AlreadyStored() bool {
	return x.alreadyStored
}

// Payload returns the queue's immutable authoritative first-write snapshot.
func (x StoreResult) Payload() ReliablePayload {
	return x.payload
}

// DurableQueueState contains the sequence watermark and messages needed to
// resume a producer stream. Unconfirmed messages cover every sequence after
// confirmedSeq through currentSeq without gaps.
type DurableQueueState struct {
	// currentSeq is the highest sequence assigned by Store.
	currentSeq int64
	// confirmedSeq is the highest contiguous consumer-confirmed sequence.
	confirmedSeq int64
	// unconfirmed contains every message after confirmedSeq through currentSeq.
	unconfirmed []UnconfirmedMessage
}

// NewDurableQueueState creates and validates an immutable queue snapshot.
func NewDurableQueueState(currentSeq, confirmedSeq int64, unconfirmed []UnconfirmedMessage) (DurableQueueState, error) {
	if currentSeq < 0 {
		return DurableQueueState{}, gerrors.NewErrInvalidMessage(errors.New("current sequence must not be negative"))
	}

	if confirmedSeq < 0 {
		return DurableQueueState{}, gerrors.NewErrInvalidMessage(errors.New("confirmed sequence must not be negative"))
	}

	if confirmedSeq > currentSeq {
		return DurableQueueState{}, gerrors.NewErrInvalidMessage(errors.New("confirmed sequence must not exceed current sequence"))
	}

	if int64(len(unconfirmed)) != currentSeq-confirmedSeq {
		return DurableQueueState{}, gerrors.NewErrInvalidMessage(errors.New("unconfirmed messages must cover every sequence after confirmation"))
	}

	messageIDs := make(map[string]struct{}, len(unconfirmed))

	for index, message := range unconfirmed {
		expectedSeq := confirmedSeq + int64(index) + 1
		if message.seq != expectedSeq {
			return DurableQueueState{}, gerrors.NewErrInvalidMessage(errors.New("unconfirmed messages must be ordered without gaps"))
		}

		if types.IsBlank(message.messageID) {
			return DurableQueueState{}, gerrors.NewErrInvalidMessage(errors.New("unconfirmed message ID is required"))
		}

		if len(message.payload.rawBytes()) == 0 {
			return DurableQueueState{}, gerrors.NewErrInvalidMessage(errors.New("unconfirmed payload is required"))
		}

		if _, exists := messageIDs[message.messageID]; exists {
			return DurableQueueState{}, gerrors.NewErrInvalidMessage(errors.New("unconfirmed message IDs must be unique"))
		}

		messageIDs[message.messageID] = struct{}{}
	}

	return DurableQueueState{
		currentSeq:   currentSeq,
		confirmedSeq: confirmedSeq,
		unconfirmed:  cloneUnconfirmedMessages(unconfirmed),
	}, nil
}

// CurrentSeq returns the highest sequence assigned by Store.
func (x DurableQueueState) CurrentSeq() int64 {
	return x.currentSeq
}

// ConfirmedSeq returns the highest contiguous confirmed sequence.
func (x DurableQueueState) ConfirmedSeq() int64 {
	return x.confirmedSeq
}

// Unconfirmed returns a copy of messages awaiting confirmation. Their payloads
// remain immutable and copy bytes only when Bytes is called.
func (x DurableQueueState) Unconfirmed() []UnconfirmedMessage {
	return cloneUnconfirmedMessages(x.unconfirmed)
}

// RequestNext grants a producer permission to submit exactly one message. A
// retried request keeps the same session and token.
type RequestNext struct {
	// The session ID identifies the ProducerController incarnation that issued the request.
	sessionID string
	// The token identifies this one-message permission and correlates its response.
	token string
	// The endpoint is the producer allowed to act on this request.
	endpoint *PID
	// The controller is the ProducerController that issued the request.
	controller *PID
}

// newRequestNext grants one producer permission to submit one message.
func newRequestNext(sessionID, token string, endpoint, controller *PID) (*RequestNext, error) {
	if err := validateSessionToken(sessionID, token); err != nil {
		return nil, err
	}

	if err := validateLocalOwnership(endpoint, controller); err != nil {
		return nil, err
	}

	return &RequestNext{
		sessionID:  sessionID,
		token:      token,
		endpoint:   endpoint,
		controller: controller,
	}, nil
}

// SessionID returns the producer-controller session.
func (x RequestNext) SessionID() string {
	return x.sessionID
}

// Token returns the identifier that correlates the request and its response.
func (x RequestNext) Token() string {
	return x.token
}

// IsAuthorizedFor reports whether endpoint received the request from its
// controller.
func (x RequestNext) IsAuthorizedFor(endpoint, sender *PID) bool {
	return isAuthorizedFor(x.endpoint, x.controller, endpoint, sender)
}

// Produced submits one application message in response to RequestNext. The
// producer must not mutate Payload after construction.
type Produced struct {
	// The session ID is copied from the RequestNext being answered.
	sessionID string
	// The token is copied from the RequestNext being answered.
	token string
	// The message ID remains stable when the same application message is retried.
	messageID string
	// The payload is the application message awaiting serialization.
	payload any
}

// NewProduced creates a response correlated with request.
func NewProduced(request *RequestNext, messageID string, payload any) (*Produced, error) {
	if request == nil {
		return nil, gerrors.NewErrInvalidMessage(errors.New("request is required"))
	}

	if err := validateSessionToken(request.sessionID, request.token); err != nil {
		return nil, err
	}

	if err := validateLocalOwnership(request.endpoint, request.controller); err != nil {
		return nil, err
	}

	if types.IsBlank(messageID) {
		return nil, gerrors.NewErrInvalidMessage(errors.New("message ID is required"))
	}

	if types.IsNil(payload) {
		return nil, gerrors.NewErrInvalidMessage(errors.New("payload is required"))
	}

	return &Produced{
		sessionID: request.sessionID,
		token:     request.token,
		messageID: messageID,
		payload:   payload,
	}, nil
}

// SessionID returns the producer-controller session.
func (x Produced) SessionID() string {
	return x.sessionID
}

// Token returns the identifier copied from the corresponding RequestNext.
func (x Produced) Token() string {
	return x.token
}

// MessageID returns the stable application message identifier.
func (x Produced) MessageID() string {
	return x.messageID
}

// Payload returns the application message. The caller must treat it as
// immutable.
func (x Produced) Payload() any {
	return x.payload
}

// Stored reports the sequence assigned to a produced message. The producer
// acknowledges it with NewStoredAck before another RequestNext can be issued.
type Stored struct {
	// The session ID identifies the ProducerController incarnation that stored the message.
	sessionID string
	// The token identifies the RequestNext fulfilled by the stored message.
	token string
	// The message ID identifies the application message assigned to seq.
	messageID string
	// The sequence number is the message's ordered position in the producer stream.
	seq int64
	// The endpoint is the producer allowed to acknowledge this message.
	endpoint *PID
	// The controller is the ProducerController that stored the message.
	controller *PID
}

// newStoredFromState creates a storage result from controller state after the
// original Produced reference, and with it the application payload, has been
// released.
func newStoredFromState(sessionID, token, messageID string, seq int64, endpoint, controller *PID) (*Stored, error) {
	if err := validateSessionToken(sessionID, token); err != nil {
		return nil, err
	}

	if types.IsBlank(messageID) {
		return nil, gerrors.NewErrInvalidMessage(errors.New("message ID is required"))
	}

	if seq <= 0 {
		return nil, gerrors.NewErrInvalidMessage(errors.New("sequence must be greater than zero"))
	}

	if err := validateLocalOwnership(endpoint, controller); err != nil {
		return nil, err
	}

	return &Stored{
		sessionID:  sessionID,
		token:      token,
		messageID:  messageID,
		seq:        seq,
		endpoint:   endpoint,
		controller: controller,
	}, nil
}

// newStored creates a storage result bound to one producer and its controller.
func newStored(produced *Produced, seq int64, endpoint, controller *PID) (*Stored, error) {
	if produced == nil {
		return nil, gerrors.NewErrInvalidMessage(errors.New("produced message is required"))
	}

	if err := validateSessionToken(produced.sessionID, produced.token); err != nil {
		return nil, err
	}

	if types.IsBlank(produced.messageID) {
		return nil, gerrors.NewErrInvalidMessage(errors.New("message ID is required"))
	}

	if types.IsNil(produced.payload) {
		return nil, gerrors.NewErrInvalidMessage(errors.New("payload is required"))
	}

	if seq <= 0 {
		return nil, gerrors.NewErrInvalidMessage(errors.New("sequence must be greater than zero"))
	}

	if err := validateLocalOwnership(endpoint, controller); err != nil {
		return nil, err
	}

	return &Stored{
		sessionID:  produced.sessionID,
		token:      produced.token,
		messageID:  produced.messageID,
		seq:        seq,
		endpoint:   endpoint,
		controller: controller,
	}, nil
}

// SessionID returns the producer-controller session.
func (x Stored) SessionID() string {
	return x.sessionID
}

// Token returns the identifier copied from the corresponding RequestNext.
func (x Stored) Token() string {
	return x.token
}

// MessageID returns the stable application message identifier.
func (x Stored) MessageID() string {
	return x.messageID
}

// Seq returns the assigned sequence number.
func (x Stored) Seq() int64 {
	return x.seq
}

// IsAuthorizedFor reports whether endpoint received the message from its
// controller.
func (x Stored) IsAuthorizedFor(endpoint, sender *PID) bool {
	return isAuthorizedFor(x.endpoint, x.controller, endpoint, sender)
}

// StoredAck tells the ProducerController that the producer accepted a Stored
// result. Its correlation fields are copied from that result.
type StoredAck struct {
	// The session ID is copied from the Stored message being acknowledged.
	sessionID string
	// The token is copied from the Stored message being acknowledged.
	token string
	// The message ID is copied from the Stored message being acknowledged.
	messageID string
}

// NewStoredAck creates an acknowledgement correlated with stored.
func NewStoredAck(stored *Stored) (*StoredAck, error) {
	if stored == nil {
		return nil, gerrors.NewErrInvalidMessage(errors.New("stored message is required"))
	}

	if err := validateSessionToken(stored.sessionID, stored.token); err != nil {
		return nil, err
	}

	if types.IsBlank(stored.messageID) {
		return nil, gerrors.NewErrInvalidMessage(errors.New("message ID is required"))
	}

	if stored.seq <= 0 {
		return nil, gerrors.NewErrInvalidMessage(errors.New("sequence must be greater than zero"))
	}

	if err := validateLocalOwnership(stored.endpoint, stored.controller); err != nil {
		return nil, err
	}

	return &StoredAck{
		sessionID: stored.sessionID,
		token:     stored.token,
		messageID: stored.messageID,
	}, nil
}

// SessionID returns the producer-controller session.
func (x StoredAck) SessionID() string {
	return x.sessionID
}

// Token returns the identifier copied from the corresponding Stored message.
func (x StoredAck) Token() string {
	return x.token
}

// MessageID returns the stable application message identifier.
func (x StoredAck) MessageID() string {
	return x.messageID
}

// Delivery presents one sequenced message to the consumer. It may be delivered
// again until the consumer responds with Confirmed.
type Delivery struct {
	// The session ID identifies the ProducerController incarnation that sequenced the message.
	sessionID string
	// The message ID identifies the application message across retries and sessions.
	messageID string
	// The sequence number is the message's ordered position in the producer stream.
	seq int64
	// The payload is a freshly decoded application message.
	payload any
	// The endpoint is the consumer allowed to process this delivery.
	endpoint *PID
	// The controller is the ConsumerController that issued the delivery.
	controller *PID
}

// newDelivery creates a delivery bound to one consumer and its controller.
func newDelivery(sessionID, messageID string, seq int64, payload any, endpoint, controller *PID) (*Delivery, error) {
	if types.IsBlank(sessionID) {
		return nil, gerrors.NewErrInvalidMessage(errors.New("session ID is required"))
	}

	if types.IsBlank(messageID) {
		return nil, gerrors.NewErrInvalidMessage(errors.New("message ID is required"))
	}

	if seq <= 0 {
		return nil, gerrors.NewErrInvalidMessage(errors.New("sequence must be greater than zero"))
	}

	if types.IsNil(payload) {
		return nil, gerrors.NewErrInvalidMessage(errors.New("payload is required"))
	}

	if err := validateLocalOwnership(endpoint, controller); err != nil {
		return nil, err
	}

	return &Delivery{
		sessionID:  sessionID,
		messageID:  messageID,
		seq:        seq,
		payload:    payload,
		endpoint:   endpoint,
		controller: controller,
	}, nil
}

// SessionID returns the producer-controller session.
func (x Delivery) SessionID() string {
	return x.sessionID
}

// MessageID returns the stable application message identifier.
func (x Delivery) MessageID() string {
	return x.messageID
}

// Seq returns the delivery sequence number.
func (x Delivery) Seq() int64 {
	return x.seq
}

// Payload returns the decoded application message. The caller must treat it as
// immutable because the same Delivery may be retried.
func (x Delivery) Payload() any {
	return x.payload
}

// IsAuthorizedFor reports whether endpoint received the delivery from its
// controller.
func (x Delivery) IsAuthorizedFor(endpoint, sender *PID) bool {
	return isAuthorizedFor(x.endpoint, x.controller, endpoint, sender)
}

// Confirmed acknowledges successful business processing of a Delivery.
type Confirmed struct {
	// The session ID is copied from the Delivery being confirmed.
	sessionID string
	// The message ID is copied from the Delivery being confirmed.
	messageID string
	// The sequence number is copied from the Delivery being confirmed.
	seq int64
}

// NewConfirmed creates a confirmation correlated with delivery.
func NewConfirmed(delivery *Delivery) (*Confirmed, error) {
	if delivery == nil {
		return nil, gerrors.NewErrInvalidMessage(errors.New("delivery is required"))
	}

	if types.IsBlank(delivery.sessionID) {
		return nil, gerrors.NewErrInvalidMessage(errors.New("session ID is required"))
	}

	if types.IsBlank(delivery.messageID) {
		return nil, gerrors.NewErrInvalidMessage(errors.New("message ID is required"))
	}

	if delivery.seq <= 0 {
		return nil, gerrors.NewErrInvalidMessage(errors.New("sequence must be greater than zero"))
	}

	if types.IsNil(delivery.payload) {
		return nil, gerrors.NewErrInvalidMessage(errors.New("payload is required"))
	}

	if err := validateLocalOwnership(delivery.endpoint, delivery.controller); err != nil {
		return nil, err
	}

	return &Confirmed{
		sessionID: delivery.sessionID,
		messageID: delivery.messageID,
		seq:       delivery.seq,
	}, nil
}

// SessionID returns the producer-controller session.
func (x Confirmed) SessionID() string {
	return x.sessionID
}

// MessageID returns the stable application message identifier.
func (x Confirmed) MessageID() string {
	return x.messageID
}

// Seq returns the confirmed sequence number.
func (x Confirmed) Seq() int64 {
	return x.seq
}

// ReliableControllerRole identifies a reliable-delivery controller.
type ReliableControllerRole uint8

// ReliableDeliveryStage identifies the operation that failed.
type ReliableDeliveryStage uint8

// String returns the stage name.
func (x ReliableDeliveryStage) String() string {
	switch x {
	case ReliableDeliveryStageLoad:
		return "load"
	case ReliableDeliveryStageStore:
		return "store"
	case ReliableDeliveryStageAccept:
		return "accept"
	case ReliableDeliveryStageConfirm:
		return "confirm"
	case ReliableDeliveryStageProtocol:
		return "protocol"
	default:
		return "unknown"
	}
}

// ReliableDeliveryFailed reports why a reliable-delivery controller stopped.
// The endpoint remains identified by its user-visible actor name.
type ReliableDeliveryFailed struct {
	// The endpoint name identifies the user actor whose controller failed.
	endpointName string
	// The controller role identifies which side of the flow failed.
	controllerRole ReliableControllerRole
	// The stage identifies the operation that could not continue.
	stage ReliableDeliveryStage
	// The error is the terminal failure reported by the controller.
	err error
	// The timestamp records when the controller reported the failure.
	timestamp time.Time
}

// newReliableDeliveryFailed creates a terminal failure event for publication.
func newReliableDeliveryFailed(endpointName string, controllerRole ReliableControllerRole, stage ReliableDeliveryStage, err error) (*ReliableDeliveryFailed, error) {
	if types.IsBlank(endpointName) {
		return nil, gerrors.NewErrInvalidMessage(errors.New("endpoint name is required"))
	}

	if !controllerRole.valid() {
		return nil, gerrors.NewErrInvalidMessage(errors.New("controller role is invalid"))
	}

	if !stage.valid() {
		return nil, gerrors.NewErrInvalidMessage(errors.New("delivery stage is invalid"))
	}

	if err == nil {
		return nil, gerrors.NewErrInvalidMessage(errors.New("failure is required"))
	}

	return &ReliableDeliveryFailed{
		endpointName:   endpointName,
		controllerRole: controllerRole,
		stage:          stage,
		err:            err,
		timestamp:      time.Now().UTC(),
	}, nil
}

// EndpointName returns the user-visible endpoint name.
func (x ReliableDeliveryFailed) EndpointName() string {
	return x.endpointName
}

// ControllerRole returns the failed controller's role.
func (x ReliableDeliveryFailed) ControllerRole() ReliableControllerRole {
	return x.controllerRole
}

// Stage returns the operation that failed.
func (x ReliableDeliveryFailed) Stage() ReliableDeliveryStage {
	return x.stage
}

// Err returns the underlying failure.
func (x ReliableDeliveryFailed) Err() error {
	return x.err
}

// Timestamp returns when the failure was reported.
func (x ReliableDeliveryFailed) Timestamp() time.Time {
	return x.timestamp
}

// String returns the role name.
func (x ReliableControllerRole) String() string {
	switch x {
	case ReliableControllerRoleProducer:
		return "producer"
	case ReliableControllerRoleConsumer:
		return "consumer"
	default:
		return "unknown"
	}
}

// valid reports whether x identifies a supported controller role.
func (x ReliableControllerRole) valid() bool {
	return x == ReliableControllerRoleProducer || x == ReliableControllerRoleConsumer
}

// toProto converts the role to its wire enum.
func (x ReliableControllerRole) toProto() internalpb.ReliableControllerRole {
	switch x {
	case ReliableControllerRoleProducer:
		return internalpb.ReliableControllerRole_RELIABLE_CONTROLLER_ROLE_PRODUCER
	case ReliableControllerRoleConsumer:
		return internalpb.ReliableControllerRole_RELIABLE_CONTROLLER_ROLE_CONSUMER
	default:
		return internalpb.ReliableControllerRole_RELIABLE_CONTROLLER_ROLE_UNSPECIFIED
	}
}

// reliableControllerRoleFromProto restores the role from its wire enum. An
// unspecified or unknown value maps to the invalid zero role, which every
// validation path rejects.
func reliableControllerRoleFromProto(role internalpb.ReliableControllerRole) ReliableControllerRole {
	switch role {
	case internalpb.ReliableControllerRole_RELIABLE_CONTROLLER_ROLE_PRODUCER:
		return ReliableControllerRoleProducer
	case internalpb.ReliableControllerRole_RELIABLE_CONTROLLER_ROLE_CONSUMER:
		return ReliableControllerRoleConsumer
	default:
		return reliableControllerRoleUnknown
	}
}

// valid reports whether x identifies a supported delivery stage.
func (x ReliableDeliveryStage) valid() bool {
	return x >= ReliableDeliveryStageLoad && x <= ReliableDeliveryStageProtocol
}

// cloneUnconfirmedMessages creates an independent slice and payload snapshot.
func cloneUnconfirmedMessages(messages []UnconfirmedMessage) []UnconfirmedMessage {
	return slices.Clone(messages)
}

// validateSessionToken checks the correlation fields used by the producer
// handshake.
func validateSessionToken(sessionID, token string) error {
	if types.IsBlank(sessionID) {
		return gerrors.NewErrInvalidMessage(errors.New("session ID is required"))
	}

	if types.IsBlank(token) {
		return gerrors.NewErrInvalidMessage(errors.New("token is required"))
	}

	return nil
}

// validateLocalOwnership checks that a controller-to-endpoint message which
// never crosses nodes is bound to an endpoint and controller on this node.
func validateLocalOwnership(endpoint, controller *PID) error {
	if endpoint == nil {
		return gerrors.NewErrInvalidMessage(errors.New("endpoint is required"))
	}

	if controller == nil {
		return gerrors.NewErrInvalidMessage(errors.New("controller is required"))
	}

	if !endpoint.IsLocal() {
		return gerrors.NewErrInvalidMessage(errors.New("endpoint must be local"))
	}

	if !controller.IsLocal() {
		return gerrors.NewErrInvalidMessage(errors.New("controller must be local"))
	}

	return nil
}

// isAuthorizedFor reports whether the supplied endpoint and sender match the
// actors recorded in RequestNext, Stored, or Delivery.
func isAuthorizedFor(expectedEndpoint, expectedSender, endpoint, sender *PID) bool {
	if expectedEndpoint == nil || expectedSender == nil || endpoint == nil || sender == nil {
		return false
	}

	return expectedEndpoint.Equals(endpoint) && expectedSender.Equals(sender)
}
