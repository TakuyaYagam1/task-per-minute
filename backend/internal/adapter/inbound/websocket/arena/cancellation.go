package arena

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const CancellationTerminalState = "cancelled"

const (
	CancellationRoleParticipant CancellationRole = "participant"
	CancellationRolePublic      CancellationRole = "public"
	CancellationRoleOperator    CancellationRole = "operator"
)

const (
	CancellationDeliverySnapshot CancellationDeliveryKind = "snapshot"
	CancellationDeliveryTerminal CancellationDeliveryKind = "terminal"
)

var (
	ErrCancellationInvalidScope              = errors.New("invalid Arena cancellation scope")
	ErrCancellationInvalidSubscription       = errors.New("invalid Arena cancellation subscription")
	ErrCancellationInvalidCommit             = errors.New("invalid committed Arena cancellation")
	ErrCancellationCommitConflict            = errors.New("arena cancellation already committed")
	ErrCancellationParticipantActionRejected = errors.New("participant action rejected after Arena cancellation")
)

type CancellationRole string

type CancellationDeliveryKind string

type CancellationSubscriptionRequest struct {
	Role          CancellationRole
	Authenticated bool
	TournamentID  uuid.UUID
	ParticipantID uuid.UUID
}

type CancellationParticipantAction struct {
	TournamentID  uuid.UUID
	ParticipantID uuid.UUID
}

type CancellationCommitInput struct {
	TournamentID         uuid.UUID
	CancellationID       uuid.UUID
	TerminalMetadata     RealtimeEnvelopeMetadata
	ParticipantSnapshots map[uuid.UUID]RealtimeEnvelope
	PublicSnapshot       RealtimeEnvelope
	OperatorSnapshot     RealtimeEnvelope
	OperatorReason       string
}

type CancellationParticipantTerminal struct {
	TournamentID  uuid.UUID `json:"tournament_id"`
	ParticipantID uuid.UUID `json:"participant_id"`
	State         string    `json:"state"`
}

type CancellationPublicTerminal struct {
	TournamentID uuid.UUID `json:"tournament_id"`
	State        string    `json:"state"`
}

type CancellationOperatorTerminal struct {
	TournamentID   uuid.UUID `json:"tournament_id"`
	CancellationID uuid.UUID `json:"cancellation_id"`
	State          string    `json:"state"`
	Reason         string    `json:"reason"`
}

type CancellationDelivery struct {
	Kind               CancellationDeliveryKind         `json:"kind"`
	SchemaVersion      int                              `json:"schema_version"`
	TournamentID       uuid.UUID                        `json:"tournament_id"`
	Sequence           int64                            `json:"sequence"`
	EventID            uuid.UUID                        `json:"event_id"`
	OccurredAt         time.Time                        `json:"occurred_at"`
	ProjectionRevision int64                            `json:"projection_revision"`
	Snapshot           *RealtimeEnvelope                `json:"snapshot,omitempty"`
	Participant        *CancellationParticipantTerminal `json:"participant,omitempty"`
	Public             *CancellationPublicTerminal      `json:"public,omitempty"`
	Operator           *CancellationOperatorTerminal    `json:"operator,omitempty"`
}

type CancellationCoordinator struct {
	mu            sync.Mutex
	tournamentID  uuid.UUID
	subscriptions map[*CancellationSubscription]CancellationSubscriptionRequest
	committed     *cancellationCommitted
}

type CancellationSubscription struct {
	coordinator *CancellationCoordinator
	deliveries  chan CancellationDelivery
	closed      bool
}

type cancellationCommitted struct {
	tournamentID         uuid.UUID
	cancellationID       uuid.UUID
	terminalMetadata     RealtimeEnvelopeMetadata
	participantSnapshots map[uuid.UUID]RealtimeEnvelope
	publicSnapshot       RealtimeEnvelope
	operatorSnapshot     RealtimeEnvelope
	operatorReason       string
}

func NewCancellationCoordinator(tournamentID uuid.UUID) (*CancellationCoordinator, error) {
	if tournamentID == uuid.Nil {
		return nil, ErrCancellationInvalidScope
	}
	return &CancellationCoordinator{
		tournamentID:  tournamentID,
		subscriptions: make(map[*CancellationSubscription]CancellationSubscriptionRequest),
	}, nil
}

func (coordinator *CancellationCoordinator) CancellationSubscribe(request CancellationSubscriptionRequest) (*CancellationSubscription, error) {
	if coordinator == nil || !cancellationSubscriptionValid(request, coordinator.tournamentID) {
		return nil, ErrCancellationInvalidSubscription
	}

	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()

	subscription := &CancellationSubscription{
		coordinator: coordinator,
		deliveries:  make(chan CancellationDelivery, 2),
	}
	if coordinator.committed == nil {
		coordinator.subscriptions[subscription] = request
		return subscription, nil
	}

	deliveries, err := coordinator.committed.cancellationDeliveries(request)
	if err != nil {
		return nil, err
	}
	for _, delivery := range deliveries {
		subscription.deliveries <- delivery
	}
	close(subscription.deliveries)
	subscription.closed = true
	return subscription, nil
}

func (subscription *CancellationSubscription) CancellationDeliveries() <-chan CancellationDelivery {
	if subscription == nil {
		return nil
	}
	return subscription.deliveries
}

func (subscription *CancellationSubscription) CancellationClose() {
	if subscription == nil || subscription.coordinator == nil {
		return
	}
	coordinator := subscription.coordinator
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if subscription.closed {
		return
	}
	delete(coordinator.subscriptions, subscription)
	close(subscription.deliveries)
	subscription.closed = true
}

func (coordinator *CancellationCoordinator) CancellationAuthorizeParticipantAction(action CancellationParticipantAction) error {
	if coordinator == nil || action.TournamentID == uuid.Nil || action.TournamentID != coordinator.tournamentID || action.ParticipantID == uuid.Nil {
		return ErrCancellationInvalidScope
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.committed != nil {
		return ErrCancellationParticipantActionRejected
	}
	return nil
}

func (coordinator *CancellationCoordinator) CancellationCommit(input CancellationCommitInput) error {
	if coordinator == nil || input.TournamentID != coordinator.tournamentID {
		return ErrCancellationInvalidScope
	}
	committed, err := cancellationValidatedCommit(input)
	if err != nil {
		return err
	}

	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.committed != nil {
		if coordinator.committed.cancellationID == committed.cancellationID {
			return nil
		}
		return ErrCancellationCommitConflict
	}

	pending := make(map[*CancellationSubscription][2]CancellationDelivery, len(coordinator.subscriptions))
	for subscription, request := range coordinator.subscriptions {
		deliveries, deliveryErr := committed.cancellationDeliveries(request)
		if deliveryErr != nil {
			return deliveryErr
		}
		pending[subscription] = [2]CancellationDelivery{deliveries[0], deliveries[1]}
	}

	coordinator.committed = committed
	for subscription, deliveries := range pending {
		subscription.deliveries <- deliveries[0]
		subscription.deliveries <- deliveries[1]
		close(subscription.deliveries)
		subscription.closed = true
		delete(coordinator.subscriptions, subscription)
	}
	return nil
}

func cancellationSubscriptionValid(request CancellationSubscriptionRequest, tournamentID uuid.UUID) bool {
	if request.TournamentID == uuid.Nil || request.TournamentID != tournamentID {
		return false
	}
	switch request.Role {
	case CancellationRoleParticipant:
		return request.Authenticated && request.ParticipantID != uuid.Nil
	case CancellationRolePublic:
		return request.ParticipantID == uuid.Nil
	case CancellationRoleOperator:
		return request.Authenticated && request.ParticipantID == uuid.Nil
	default:
		return false
	}
}

//nolint:gocyclo // One commit boundary validates terminal identity and all role snapshots before publication.
func cancellationValidatedCommit(input CancellationCommitInput) (*cancellationCommitted, error) {
	metadata := input.TerminalMetadata
	if input.TournamentID == uuid.Nil || input.CancellationID == uuid.Nil || metadata.SchemaVersion != ArenaRealtimeSchemaVersion ||
		metadata.TournamentID != input.TournamentID || metadata.Sequence < 2 || metadata.EventID == uuid.Nil ||
		metadata.ProjectionRevision < 2 || !isServerUTC(metadata.OccurredAt) || strings.TrimSpace(input.OperatorReason) == "" ||
		input.ParticipantSnapshots == nil {
		return nil, ErrCancellationInvalidCommit
	}

	publicSnapshot, err := cancellationSnapshotClone(input.PublicSnapshot, metadata, CancellationRolePublic, uuid.Nil)
	if err != nil {
		return nil, err
	}
	operatorSnapshot, err := cancellationSnapshotClone(input.OperatorSnapshot, metadata, CancellationRoleOperator, uuid.Nil)
	if err != nil {
		return nil, err
	}
	participantSnapshots := make(map[uuid.UUID]RealtimeEnvelope, len(input.ParticipantSnapshots))
	for participantID, snapshot := range input.ParticipantSnapshots {
		if participantID == uuid.Nil {
			return nil, ErrCancellationInvalidCommit
		}
		clone, cloneErr := cancellationSnapshotClone(snapshot, metadata, CancellationRoleParticipant, participantID)
		if cloneErr != nil {
			return nil, cloneErr
		}
		participantSnapshots[participantID] = clone
	}

	return &cancellationCommitted{
		tournamentID:         input.TournamentID,
		cancellationID:       input.CancellationID,
		terminalMetadata:     metadata,
		participantSnapshots: participantSnapshots,
		publicSnapshot:       publicSnapshot,
		operatorSnapshot:     operatorSnapshot,
		operatorReason:       strings.TrimSpace(input.OperatorReason),
	}, nil
}

//nolint:gocyclo // Role-exclusive snapshot validation is intentionally kept at one delivery boundary.
func cancellationSnapshotClone(
	snapshot RealtimeEnvelope,
	terminal RealtimeEnvelopeMetadata,
	role CancellationRole,
	participantID uuid.UUID,
) (RealtimeEnvelope, error) {
	if snapshot.TournamentID != terminal.TournamentID || snapshot.Sequence != terminal.Sequence-1 ||
		snapshot.ProjectionRevision >= terminal.ProjectionRevision || snapshot.OccurredAt.After(terminal.OccurredAt) {
		return RealtimeEnvelope{}, ErrCancellationInvalidCommit
	}
	switch role {
	case CancellationRoleParticipant:
		if snapshot.Participant == nil || snapshot.Public != nil || snapshot.Operator != nil || snapshot.Participant.PlayerID != participantID {
			return RealtimeEnvelope{}, ErrCancellationInvalidCommit
		}
	case CancellationRolePublic:
		if snapshot.Public == nil || snapshot.Participant != nil || snapshot.Operator != nil {
			return RealtimeEnvelope{}, ErrCancellationInvalidCommit
		}
	case CancellationRoleOperator:
		if snapshot.Operator == nil || snapshot.Participant != nil || snapshot.Public != nil {
			return RealtimeEnvelope{}, ErrCancellationInvalidCommit
		}
	default:
		return RealtimeEnvelope{}, ErrCancellationInvalidCommit
	}
	clone, err := cloneResumeEnvelope(snapshot)
	if err != nil {
		return RealtimeEnvelope{}, fmt.Errorf("%w: role snapshot: %w", ErrCancellationInvalidCommit, err)
	}
	return clone, nil
}

func (committed *cancellationCommitted) cancellationDeliveries(request CancellationSubscriptionRequest) ([]CancellationDelivery, error) {
	var snapshot RealtimeEnvelope
	switch request.Role {
	case CancellationRoleParticipant:
		participantSnapshot, ok := committed.participantSnapshots[request.ParticipantID]
		if !ok {
			return nil, ErrCancellationInvalidSubscription
		}
		snapshot = participantSnapshot
	case CancellationRolePublic:
		snapshot = committed.publicSnapshot
	case CancellationRoleOperator:
		snapshot = committed.operatorSnapshot
	default:
		return nil, ErrCancellationInvalidSubscription
	}

	snapshotClone, err := cloneResumeEnvelope(snapshot)
	if err != nil {
		return nil, ErrCancellationInvalidCommit
	}
	snapshotDelivery := cancellationDeliveryMetadata(CancellationDeliverySnapshot, RealtimeEnvelopeMetadata{
		SchemaVersion:      snapshotClone.SchemaVersion,
		TournamentID:       snapshotClone.TournamentID,
		Sequence:           snapshotClone.Sequence,
		EventID:            snapshotClone.EventID,
		OccurredAt:         snapshotClone.OccurredAt,
		ProjectionRevision: snapshotClone.ProjectionRevision,
	})
	snapshotDelivery.Snapshot = &snapshotClone
	terminalDelivery := cancellationDeliveryMetadata(CancellationDeliveryTerminal, committed.terminalMetadata)
	switch request.Role {
	case CancellationRoleParticipant:
		terminalDelivery.Participant = &CancellationParticipantTerminal{
			TournamentID:  committed.tournamentID,
			ParticipantID: request.ParticipantID,
			State:         CancellationTerminalState,
		}
	case CancellationRolePublic:
		terminalDelivery.Public = &CancellationPublicTerminal{
			TournamentID: committed.tournamentID,
			State:        CancellationTerminalState,
		}
	case CancellationRoleOperator:
		terminalDelivery.Operator = &CancellationOperatorTerminal{
			TournamentID:   committed.tournamentID,
			CancellationID: committed.cancellationID,
			State:          CancellationTerminalState,
			Reason:         committed.operatorReason,
		}
	}
	return []CancellationDelivery{snapshotDelivery, terminalDelivery}, nil
}

func cancellationDeliveryMetadata(kind CancellationDeliveryKind, metadata RealtimeEnvelopeMetadata) CancellationDelivery {
	return CancellationDelivery{
		Kind:               kind,
		SchemaVersion:      metadata.SchemaVersion,
		TournamentID:       metadata.TournamentID,
		Sequence:           metadata.Sequence,
		EventID:            metadata.EventID,
		OccurredAt:         metadata.OccurredAt,
		ProjectionRevision: metadata.ProjectionRevision,
	}
}
