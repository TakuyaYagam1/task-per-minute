package eventdelivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	AudienceAll         Audience = "all"
	AudiencePublic      Audience = "public"
	AudienceParticipant Audience = "participant"
	AudienceOperator    Audience = "operator"
)

var (
	ErrInvalidConfig   = errors.New("event delivery: invalid configuration")
	ErrInvalidEvent    = errors.New("event delivery: invalid event")
	ErrInvalidReceipt  = errors.New("event delivery: invalid receipt")
	ErrClaimLost       = errors.New("event delivery: claim lost")
	ErrDeliveryFailed  = errors.New("event delivery: sink failed")
	ErrRepositoryState = errors.New("event delivery: invalid repository state")
	ErrWorkerRunning   = errors.New("event delivery: worker already running")
	ErrSubscriberScope = errors.New("event delivery: subscriber scope mismatch")
)

const (
	DeliveryPending      DeliveryOutcome = "pending"
	DeliveryWritten      DeliveryOutcome = "written"
	DeliveryDisconnected DeliveryOutcome = "disconnected"
)

type Audience string

func (a Audience) Valid() bool {
	switch a {
	case AudienceAll, AudiencePublic, AudienceParticipant, AudienceOperator:
		return true
	default:
		return false
	}
}

type Event struct {
	ID                   uuid.UUID
	CorrelationID        uuid.UUID
	TournamentID         uuid.UUID
	ProjectionRevisionID uuid.UUID
	Sequence             int64
	ProjectionRevision   int64
	ProjectionOrdinal    int32
	Terminal             bool
	Audience             Audience
	PrincipalID          uuid.UUID
	Topic                string
	Payload              json.RawMessage
	OccurredAt           time.Time
	AttemptCount         int32
}

func (e Event) Validate() error {
	if !validEventIdentity(e) || !validEventDelivery(e) || !validEventContent(e) {
		return ErrInvalidEvent
	}
	if (e.Audience == AudienceAll || e.Audience == AudiencePublic) && payloadHasPrivateField(e.Payload) {
		return fmt.Errorf("%w: public payload contains a private field", ErrInvalidEvent)
	}
	return nil
}

func validEventIdentity(event Event) bool {
	if event.ID == uuid.Nil || event.CorrelationID == uuid.Nil || event.TournamentID == uuid.Nil || event.Sequence < 1 {
		return false
	}
	return event.ProjectionRevisionID != uuid.Nil && event.ProjectionRevision >= 1 &&
		event.ProjectionOrdinal >= 1
}

func (e Event) HasProjection() bool {
	return e.ProjectionRevisionID != uuid.Nil && e.ProjectionRevision >= 1 && e.ProjectionOrdinal >= 1
}

func validEventDelivery(event Event) bool {
	return event.Audience.Valid() && validAudiencePrincipal(event.Audience, event.PrincipalID) &&
		validServerTime(event.OccurredAt) && event.AttemptCount >= 1
}

func validEventContent(event Event) bool {
	return validText(event.Topic, 128) && validPayload(event.Payload)
}

func (e Event) Clone() Event {
	e.Payload = append(json.RawMessage(nil), e.Payload...)
	return e
}

func (e Event) Matches(subscriber Subscriber) bool {
	return e.Validate() == nil && subscriber.Validate() == nil && audienceMatches(e, subscriber)
}

type ClaimRequest struct {
	WorkerID  uuid.UUID
	Token     uuid.UUID
	Limit     int32
	ClaimedAt time.Time
	LeaseEnds time.Time
}

func (r ClaimRequest) Validate() error {
	if r.WorkerID == uuid.Nil || r.Token == uuid.Nil || r.Limit < 1 ||
		!validServerTime(r.ClaimedAt) || !validServerTime(r.LeaseEnds) ||
		!r.LeaseEnds.After(r.ClaimedAt) {
		return ErrInvalidReceipt
	}
	return nil
}

type Acknowledgement struct {
	EventID        uuid.UUID
	WorkerID       uuid.UUID
	ClaimToken     uuid.UUID
	AcknowledgedAt time.Time
}

func (a Acknowledgement) Validate() error {
	if a.EventID == uuid.Nil || a.WorkerID == uuid.Nil || a.ClaimToken == uuid.Nil ||
		!validServerTime(a.AcknowledgedAt) {
		return ErrInvalidReceipt
	}
	return nil
}

type Retry struct {
	EventID     uuid.UUID
	WorkerID    uuid.UUID
	ClaimToken  uuid.UUID
	AvailableAt time.Time
	Reason      string
}

func (r Retry) Validate() error {
	if r.EventID == uuid.Nil || r.WorkerID == uuid.Nil || r.ClaimToken == uuid.Nil ||
		!validServerTime(r.AvailableAt) || !validText(r.Reason, 96) {
		return ErrInvalidReceipt
	}
	return nil
}

type Store interface {
	Claim(ctx context.Context, request ClaimRequest) ([]Event, error)
	Acknowledge(ctx context.Context, acknowledgement Acknowledgement) (bool, error)
	Retry(ctx context.Context, retry Retry) (bool, error)
	ReleaseClaims(ctx context.Context, workerID uuid.UUID) error
}

// ReceiptRetentionRequest bounds one maintenance transaction. CutoffAt is
// computed by the application from the configured retention duration; the
// durable store must preserve every active, resumable, or newer receipt.
type ReceiptRetentionRequest struct {
	CutoffAt  time.Time
	BatchSize int32
}

func (r ReceiptRetentionRequest) Validate() error {
	if !validServerTime(r.CutoffAt) || r.BatchSize < 1 || r.BatchSize > maximumBatchSize {
		return ErrInvalidReceipt
	}
	return nil
}

type ReceiptRetentionResult struct {
	DeletedReceipts    int64
	DeletedSubscribers int64
}

func (r ReceiptRetentionResult) Validate() error {
	if r.DeletedReceipts < 0 || r.DeletedSubscribers < 0 {
		return ErrRepositoryState
	}
	return nil
}

// ReceiptRetentionStore is the durable, bounded cleanup boundary. It may
// delete only terminal receipts that belong to sufficiently old closed
// subscribers, then closed subscribers with no remaining receipt evidence.
type ReceiptRetentionStore interface {
	PruneClosedSubscribers(ctx context.Context, request ReceiptRetentionRequest) (ReceiptRetentionResult, error)
}

type ReplayRequest struct {
	TournamentID  uuid.UUID
	AfterSequence int64
	Limit         int32
	Audience      Audience
	PrincipalID   uuid.UUID
}

func (r ReplayRequest) Validate() error {
	if r.TournamentID == uuid.Nil || r.AfterSequence < 0 || r.Limit < 1 || r.Limit > maximumBatchSize ||
		r.Audience == AudienceAll || !validAudiencePrincipal(r.Audience, r.PrincipalID) {
		return ErrInvalidReceipt
	}
	return nil
}

type ReplayStore interface {
	ListAfter(ctx context.Context, request ReplayRequest) ([]Event, error)
	Cursor(ctx context.Context, tournamentID uuid.UUID) (int64, error)
}

type Sink interface {
	Deliver(ctx context.Context, event Event) error
}

type Subscriber struct {
	ID                   uuid.UUID
	InstanceID           uuid.UUID
	ConnectionID         uuid.UUID
	ConnectionGeneration int64
	TournamentID         uuid.UUID
	Audience             Audience
	PrincipalID          uuid.UUID
	AfterSequence        int64
	ConnectedAt          time.Time
}

func (s Subscriber) Validate() error {
	if s.ID == uuid.Nil || s.InstanceID == uuid.Nil || s.ConnectionID == uuid.Nil ||
		s.ConnectionGeneration < 1 || s.TournamentID == uuid.Nil ||
		!s.Audience.Valid() || s.Audience == AudienceAll ||
		!validAudiencePrincipal(s.Audience, s.PrincipalID) || s.AfterSequence < 0 ||
		!validServerTime(s.ConnectedAt) {
		return ErrInvalidReceipt
	}
	return nil
}

// SubscriptionOpenRequest creates a new logical subscriber when ResumeID is
// empty, or resumes the existing opaque subscriber when it is present. The
// store owns issuance of a new ID and must bind a resumed ID to this exact
// tournament, audience, and principal. ConnectionID identifies only the
// current socket fence and must never be used as a resume credential.
type SubscriptionOpenRequest struct {
	ResumeID      uuid.UUID
	ConnectionID  uuid.UUID
	InstanceID    uuid.UUID
	TournamentID  uuid.UUID
	Audience      Audience
	PrincipalID   uuid.UUID
	AfterSequence int64
	OpenedAt      time.Time
}

func (r SubscriptionOpenRequest) Validate() error {
	if r.ConnectionID == uuid.Nil || r.InstanceID == uuid.Nil || r.TournamentID == uuid.Nil ||
		!r.Audience.Valid() || r.Audience == AudienceAll ||
		!validAudiencePrincipal(r.Audience, r.PrincipalID) || r.AfterSequence < 0 ||
		!validServerTime(r.OpenedAt) {
		return ErrInvalidReceipt
	}
	return nil
}

// Subscription is the durable scope binding returned by a store. A pending
// terminal is deliberately separate from the replay cursor: it must be sent
// after the initial snapshot even when its sequence is at or before the
// snapshot watermark.
type Subscription struct {
	Subscriber      Subscriber
	TerminalState   TerminalReceiptState
	PendingTerminal *Event
}

func (s Subscription) Validate() error {
	if s.Subscriber.Validate() != nil {
		return ErrInvalidReceipt
	}
	switch s.TerminalState {
	case TerminalReceiptNone:
		if s.PendingTerminal != nil {
			return ErrInvalidReceipt
		}
		return nil
	case TerminalReceiptWritten:
		if s.PendingTerminal != nil {
			return ErrInvalidReceipt
		}
		return nil
	case TerminalReceiptPending:
		if s.PendingTerminal == nil {
			return ErrInvalidReceipt
		}
	default:
		return ErrInvalidReceipt
	}
	terminal := s.PendingTerminal.Clone()
	if terminal.Validate() != nil || !terminal.Terminal || !audienceMatches(terminal, s.Subscriber) {
		return ErrInvalidReceipt
	}
	return nil
}

type TerminalReceiptState string

const (
	TerminalReceiptNone    TerminalReceiptState = "none"
	TerminalReceiptPending TerminalReceiptState = "pending"
	TerminalReceiptWritten TerminalReceiptState = "written"
)

// SubscriptionStore is the durable identity boundary for websocket resume.
// Implementations must atomically create or scope-check the opaque subscriber
// ID and return only a terminal event whose receipt is still pending for that
// subscriber. The adapter always revalidates browser authentication before it
// invokes this port.
type SubscriptionStore interface {
	OpenSubscription(ctx context.Context, request SubscriptionOpenRequest) (Subscription, error)
}

// SubscriptionRepository is the mandatory durable boundary for realtime
// delivery. Implementations provide outbox replay, delivery receipts, and
// atomically scope-bound subscriber resume in the same durable authority.
type SubscriptionRepository interface {
	Repository
	SubscriptionStore
}

type SubscriberClose struct {
	SubscriberID         uuid.UUID
	InstanceID           uuid.UUID
	ConnectionID         uuid.UUID
	ConnectionGeneration int64
	ClosedAt             time.Time
	Reason               string
}

func (c SubscriberClose) Validate() error {
	if c.SubscriberID == uuid.Nil || c.InstanceID == uuid.Nil || c.ConnectionID == uuid.Nil ||
		c.ConnectionGeneration < 1 ||
		!validServerTime(c.ClosedAt) || !validText(c.Reason, 64) {
		return ErrInvalidReceipt
	}
	return nil
}

type DeliveryClaim struct {
	Subscriber Subscriber
	Event      Event
	WorkerID   uuid.UUID
	ClaimToken uuid.UUID
	ClaimedAt  time.Time
	LeaseEnds  time.Time
}

func (c DeliveryClaim) Validate() error {
	if c.Subscriber.Validate() != nil || c.Event.Validate() != nil ||
		c.Event.TournamentID != c.Subscriber.TournamentID ||
		(!c.Event.Terminal && c.Event.Sequence <= c.Subscriber.AfterSequence) || c.WorkerID == uuid.Nil ||
		c.ClaimToken == uuid.Nil || !validServerTime(c.ClaimedAt) ||
		!validServerTime(c.LeaseEnds) || !c.LeaseEnds.After(c.ClaimedAt) ||
		!audienceMatches(c.Event, c.Subscriber) {
		return ErrInvalidReceipt
	}
	return nil
}

type DeliveryAcknowledgement struct {
	SubscriberID         uuid.UUID
	InstanceID           uuid.UUID
	ConnectionID         uuid.UUID
	ConnectionGeneration int64
	EventID              uuid.UUID
	WorkerID             uuid.UUID
	ClaimToken           uuid.UUID
	Sequence             int64
	AcknowledgedAt       time.Time
}

func (a DeliveryAcknowledgement) Validate() error {
	if a.SubscriberID == uuid.Nil || a.InstanceID == uuid.Nil || a.ConnectionID == uuid.Nil || a.ConnectionGeneration < 1 ||
		a.EventID == uuid.Nil || a.WorkerID == uuid.Nil ||
		a.ClaimToken == uuid.Nil || a.Sequence < 1 || !validServerTime(a.AcknowledgedAt) {
		return ErrInvalidReceipt
	}
	return nil
}

type DeliveryRetry struct {
	SubscriberID         uuid.UUID
	InstanceID           uuid.UUID
	ConnectionID         uuid.UUID
	ConnectionGeneration int64
	EventID              uuid.UUID
	WorkerID             uuid.UUID
	ClaimToken           uuid.UUID
	AvailableAt          time.Time
	Reason               string
}

func (r DeliveryRetry) Validate() error {
	if r.SubscriberID == uuid.Nil || r.InstanceID == uuid.Nil || r.ConnectionID == uuid.Nil || r.ConnectionGeneration < 1 ||
		r.EventID == uuid.Nil || r.WorkerID == uuid.Nil ||
		r.ClaimToken == uuid.Nil || !validServerTime(r.AvailableAt) || !validText(r.Reason, 96) {
		return ErrInvalidReceipt
	}
	return nil
}

type DeliveryOutcome string

func (o DeliveryOutcome) Valid() bool {
	switch o {
	case DeliveryPending, DeliveryWritten, DeliveryDisconnected:
		return true
	default:
		return false
	}
}

type DeliveryReceipt struct {
	SubscriberID uuid.UUID
	EventID      uuid.UUID
	Sequence     int64
	Outcome      DeliveryOutcome
	TerminalAt   *time.Time
}

func (r DeliveryReceipt) Validate() error {
	if r.SubscriberID == uuid.Nil || r.EventID == uuid.Nil || r.Sequence < 1 || !r.Outcome.Valid() {
		return ErrInvalidReceipt
	}
	if r.Outcome == DeliveryPending {
		if r.TerminalAt != nil {
			return ErrInvalidReceipt
		}
		return nil
	}
	if r.TerminalAt == nil || !validServerTime(*r.TerminalAt) {
		return ErrInvalidReceipt
	}
	return nil
}

type DeliveryJournal interface {
	CloseSubscriber(ctx context.Context, request SubscriberClose) error
	ClaimDelivery(ctx context.Context, claim DeliveryClaim) (bool, error)
	AcknowledgeDelivery(ctx context.Context, acknowledgement DeliveryAcknowledgement) (bool, error)
	RetryDelivery(ctx context.Context, retry DeliveryRetry) (bool, error)
	DeliveryReceipt(ctx context.Context, subscriber Subscriber, eventID uuid.UUID) (DeliveryReceipt, error)
	SubscriberCursor(ctx context.Context, subscriber Subscriber) (int64, error)
}

type Repository interface {
	Store
	ReplayStore
	DeliveryJournal
}

type HealthSnapshot struct {
	Started             bool
	Running             bool
	Stale               bool
	StartedAt           *time.Time
	LastAttemptAt       *time.Time
	LastSuccessAt       *time.Time
	LastFailureAt       *time.Time
	ConsecutiveFailures int64
}

type HealthSource interface {
	Health(now time.Time) HealthSnapshot
}

const (
	WorkerOutcomeSuccess = "success"
	WorkerOutcomeRetry   = "retry"
	WorkerOutcomeFailure = "failure"
)

// WorkerEvent is the bounded, payload-free outcome of one claimed outbox event.
type WorkerEvent struct {
	EventID            uuid.UUID
	CorrelationID      uuid.UUID
	TournamentID       uuid.UUID
	ProjectionRevision int64
	Sequence           int64
	Outcome            string
	Transition         string
	ReasonCode         string
	Duration           time.Duration
}

type WorkerObserver interface {
	ObserveEventDelivery(ctx context.Context, event WorkerEvent)
}

// BacklogSnapshot describes durable, cross-replica delivery pressure.
type BacklogSnapshot struct {
	PendingCount    int64
	OldestPendingAt *time.Time
}

func (snapshot BacklogSnapshot) Validate() error {
	if snapshot.PendingCount < 0 ||
		(snapshot.PendingCount == 0) != (snapshot.OldestPendingAt == nil) {
		return ErrRepositoryState
	}
	for _, value := range []*time.Time{snapshot.OldestPendingAt} {
		if value != nil && !validServerTime(*value) {
			return ErrRepositoryState
		}
	}
	return nil
}

type BacklogSource interface {
	Backlog(ctx context.Context) (BacklogSnapshot, error)
}

func audienceMatches(event Event, subscriber Subscriber) bool {
	if event.TournamentID != subscriber.TournamentID {
		return false
	}
	switch event.Audience {
	case AudienceAll:
		return true
	case AudiencePublic:
		return subscriber.Audience == AudiencePublic
	case AudienceParticipant:
		return subscriber.Audience == AudienceParticipant && subscriber.PrincipalID == event.PrincipalID
	case AudienceOperator:
		return subscriber.Audience == AudienceOperator && subscriber.PrincipalID == event.PrincipalID
	default:
		return false
	}
}

func validAudiencePrincipal(audience Audience, principalID uuid.UUID) bool {
	switch audience {
	case AudienceAll, AudiencePublic:
		return principalID == uuid.Nil
	case AudienceParticipant, AudienceOperator:
		return principalID != uuid.Nil
	default:
		return false
	}
}

func validText(value string, limit int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= limit
}

func validServerTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

func validPayload(payload json.RawMessage) bool {
	trimmed := bytes.TrimSpace(payload)
	return len(trimmed) >= 2 && trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}' &&
		!bytes.Equal(trimmed, []byte("{}")) && json.Valid(trimmed)
}

func payloadHasPrivateField(payload json.RawMessage) bool {
	var value any
	if json.Unmarshal(payload, &value) != nil {
		return true
	}
	return containsPrivateField(value)
}

func containsPrivateField(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if privatePayloadKey(strings.ToLower(key)) || containsPrivateField(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if containsPrivateField(nested) {
				return true
			}
		}
	}
	return false
}

func privatePayloadKey(key string) bool {
	switch key {
	case "flag", "submitted_flag", "expected_flag", "secret", "token", "access_token",
		"refresh_token", "session_token", "authorization", "credential", "cookie", "api_key",
		"private_key", "task", "task_snapshot", "assignment", "participant_id", "operator_id",
		"source_url", "task_url", "task_source_url", "presigned_url", "download_url", "payload_digest":
		return true
	default:
		return false
	}
}
