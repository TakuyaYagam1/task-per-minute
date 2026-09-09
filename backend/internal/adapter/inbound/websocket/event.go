package websocket

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	tournamentws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/tournament"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/wirelimits"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
)

const (
	EventTournamentParticipant = "tournament.participant"
	EventTournamentPublic      = "tournament.public"
	EventTournamentOperator    = "tournament.operator"
	EventTournamentTerminal    = "tournament.terminal"
	EventTournamentRejected    = "tournament.rejected"
)

var (
	ErrTournamentInvalidPayload = errors.New("invalid tournament payload")
	ErrTournamentUnknownEvent   = errors.New("unknown tournament event")
	ErrTournamentRoleMismatch   = errors.New("tournament role does not match connection")
	ErrTournamentInboundFrame   = errors.New("invalid tournament inbound frame")
	ErrTournamentInboundRate    = errors.New("tournament inbound rate exceeded")
)

type TournamentRole string

const (
	TournamentRoleParticipant TournamentRole = "participant"
	TournamentRolePublic      TournamentRole = "public"
	TournamentRoleOperator    TournamentRole = "operator"
)

type TournamentRejectionCode string

const (
	TournamentRejectionUnauthenticated TournamentRejectionCode = "tournament.unauthenticated"
	TournamentRejectionForbidden       TournamentRejectionCode = "tournament.forbidden"
	TournamentRejectionUnavailable     TournamentRejectionCode = "tournament.unavailable"
	TournamentRejectionCapacity        TournamentRejectionCode = "tournament.capacity"
	TournamentRejectionInvalidFrame    TournamentRejectionCode = "tournament.invalid_frame"
	TournamentRejectionRateLimited     TournamentRejectionCode = "tournament.rate_limited"
)

type TournamentParticipantPayload struct {
	Envelope tournamentws.ParticipantRealtimeEnvelope `json:"envelope"`
}

type TournamentPublicPayload struct {
	Envelope TournamentPublicEnvelope `json:"envelope"`
}

type TournamentOperatorPayload struct {
	Envelope TournamentOperatorEnvelope `json:"envelope"`
}

type TournamentTerminalPayload struct {
	SchemaVersion int                    `json:"schema_version"`
	TournamentID  uuid.UUID              `json:"tournament_id"`
	Sequence      int64                  `json:"sequence"`
	EventID       uuid.UUID              `json:"event_id"`
	OccurredAt    time.Time              `json:"occurred_at"`
	State         domain.TournamentState `json:"state"`
}

type TournamentPublicEnvelope struct {
	SchemaVersion      int                         `json:"schema_version"`
	TournamentID       uuid.UUID                   `json:"tournament_id"`
	Sequence           int64                       `json:"sequence"`
	EventID            uuid.UUID                   `json:"event_id"`
	OccurredAt         time.Time                   `json:"occurred_at"`
	ProjectionRevision int64                       `json:"projection_revision"`
	ResumeID           *uuid.UUID                  `json:"resume_id,omitempty"`
	Public             tournamentws.PublicSnapshot `json:"public"`
}

type TournamentOperatorEnvelope struct {
	SchemaVersion      int                           `json:"schema_version"`
	TournamentID       uuid.UUID                     `json:"tournament_id"`
	Sequence           int64                         `json:"sequence"`
	EventID            uuid.UUID                     `json:"event_id"`
	OccurredAt         time.Time                     `json:"occurred_at"`
	ProjectionRevision int64                         `json:"projection_revision"`
	ResumeID           *uuid.UUID                    `json:"resume_id,omitempty"`
	Operator           tournamentws.OperatorSnapshot `json:"operator"`
}

type TournamentRejection struct {
	Code    TournamentRejectionCode `json:"code"`
	Message string                  `json:"message"`
}

type TournamentParticipantMessage struct {
	Type        string
	Participant *TournamentParticipantPayload
	Terminal    *TournamentTerminalPayload
	Rejected    *TournamentRejection
}

type TournamentPublicMessage struct {
	Type     string
	Public   *TournamentPublicPayload
	Terminal *TournamentTerminalPayload
	Rejected *TournamentRejection
}

type TournamentOperatorMessage struct {
	Type     string
	Operator *TournamentOperatorPayload
	Terminal *TournamentTerminalPayload
	Rejected *TournamentRejection
}

type tournamentPayloadEvent[T any] struct {
	Type    string `json:"type"`
	Payload T      `json:"payload"`
}

type tournamentRejectedEvent struct {
	Type    string                  `json:"type"`
	Code    TournamentRejectionCode `json:"code"`
	Message string                  `json:"message"`
}

func cloneTournamentResumeID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func NewTournamentParticipantPayload(
	envelope tournamentws.ParticipantRealtimeEnvelope,
) (TournamentParticipantPayload, error) {
	cloned, err := cloneTournamentParticipantEnvelope(envelope)
	if err != nil {
		return TournamentParticipantPayload{}, ErrTournamentInvalidPayload
	}
	payload := TournamentParticipantPayload{Envelope: cloned}
	if err := validateTournamentParticipantPayload(payload); err != nil {
		return TournamentParticipantPayload{}, err
	}
	return payload, nil
}

func NewTournamentPublicPayload(
	envelope tournamentws.RealtimeEnvelope,
) (TournamentPublicPayload, error) {
	if envelope.Public == nil || envelope.Participant != nil || envelope.Operator != nil || envelope.Validate() != nil {
		return TournamentPublicPayload{}, ErrTournamentInvalidPayload
	}
	cloned, err := cloneTournamentRealtimeEnvelope(envelope)
	if err != nil || cloned.Public == nil {
		return TournamentPublicPayload{}, ErrTournamentInvalidPayload
	}
	payload := TournamentPublicPayload{Envelope: TournamentPublicEnvelope{
		SchemaVersion:      cloned.SchemaVersion,
		TournamentID:       cloned.TournamentID,
		Sequence:           cloned.Sequence,
		EventID:            cloned.EventID,
		OccurredAt:         cloned.OccurredAt,
		ProjectionRevision: cloned.ProjectionRevision,
		ResumeID:           cloneTournamentResumeID(cloned.ResumeID),
		Public:             *cloned.Public,
	}}
	if err := validateTournamentPublicPayload(payload); err != nil {
		return TournamentPublicPayload{}, err
	}
	return payload, nil
}

func NewTournamentOperatorPayload(
	envelope tournamentws.RealtimeEnvelope,
) (TournamentOperatorPayload, error) {
	if envelope.Operator == nil || envelope.Participant != nil || envelope.Public != nil || envelope.Validate() != nil {
		return TournamentOperatorPayload{}, ErrTournamentInvalidPayload
	}
	cloned, err := cloneTournamentRealtimeEnvelope(envelope)
	if err != nil || cloned.Operator == nil {
		return TournamentOperatorPayload{}, ErrTournamentInvalidPayload
	}
	payload := TournamentOperatorPayload{Envelope: TournamentOperatorEnvelope{
		SchemaVersion:      cloned.SchemaVersion,
		TournamentID:       cloned.TournamentID,
		Sequence:           cloned.Sequence,
		EventID:            cloned.EventID,
		OccurredAt:         cloned.OccurredAt,
		ProjectionRevision: cloned.ProjectionRevision,
		ResumeID:           cloneTournamentResumeID(cloned.ResumeID),
		Operator:           *cloned.Operator,
	}}
	if err := validateTournamentOperatorPayload(payload); err != nil {
		return TournamentOperatorPayload{}, err
	}
	return payload, nil
}

func NewTournamentTerminalPayload(
	tournamentID uuid.UUID,
	sequence int64,
	eventID uuid.UUID,
	occurredAt time.Time,
	state domain.TournamentState,
) (TournamentTerminalPayload, error) {
	payload := TournamentTerminalPayload{
		SchemaVersion: tournamentws.TournamentRealtimeSchemaVersion,
		TournamentID:  tournamentID,
		Sequence:      sequence,
		EventID:       eventID,
		OccurredAt:    occurredAt,
		State:         state,
	}
	if err := validateTournamentTerminalPayload(payload); err != nil {
		return TournamentTerminalPayload{}, err
	}
	return payload, nil
}

func cloneTournamentParticipantEnvelope(
	envelope tournamentws.ParticipantRealtimeEnvelope,
) (tournamentws.ParticipantRealtimeEnvelope, error) {
	cloned, err := cloneTournamentRealtimeEnvelope(tournamentws.RealtimeEnvelope{
		SchemaVersion:      envelope.SchemaVersion,
		TournamentID:       envelope.TournamentID,
		Sequence:           envelope.Sequence,
		EventID:            envelope.EventID,
		OccurredAt:         envelope.OccurredAt,
		ProjectionRevision: envelope.ProjectionRevision,
		ResumeID:           cloneTournamentResumeID(envelope.ResumeID),
		Participant:        &envelope.Participant,
	})
	if err != nil || cloned.Participant == nil {
		return tournamentws.ParticipantRealtimeEnvelope{}, ErrTournamentInvalidPayload
	}
	return tournamentws.ParticipantRealtimeEnvelope{
		SchemaVersion:      cloned.SchemaVersion,
		TournamentID:       cloned.TournamentID,
		Sequence:           cloned.Sequence,
		EventID:            cloned.EventID,
		OccurredAt:         cloned.OccurredAt,
		ProjectionRevision: cloned.ProjectionRevision,
		ResumeID:           cloneTournamentResumeID(cloned.ResumeID),
		Participant:        *cloned.Participant,
	}, nil
}

func cloneTournamentRealtimeEnvelope(envelope tournamentws.RealtimeEnvelope) (tournamentws.RealtimeEnvelope, error) {
	if err := envelope.Validate(); err != nil {
		return tournamentws.RealtimeEnvelope{}, err
	}
	metadata := tournamentws.RealtimeEnvelopeMetadata{
		SchemaVersion:      envelope.SchemaVersion,
		TournamentID:       envelope.TournamentID,
		Sequence:           envelope.Sequence,
		EventID:            envelope.EventID,
		OccurredAt:         envelope.OccurredAt,
		ProjectionRevision: envelope.ProjectionRevision,
	}
	if envelope.ResumeID != nil {
		metadata.ResumeID = *envelope.ResumeID
	}
	switch {
	case envelope.Participant != nil:
		return tournamentws.NewRealtimeEnvelope(metadata, envelope.Participant)
	case envelope.Public != nil:
		return tournamentws.NewRealtimeEnvelope(metadata, envelope.Public)
	case envelope.Operator != nil:
		return tournamentws.NewRealtimeEnvelope(metadata, envelope.Operator)
	default:
		return tournamentws.RealtimeEnvelope{}, ErrTournamentInvalidPayload
	}
}

func MarshalTournamentParticipant(payload TournamentParticipantPayload) ([]byte, error) {
	if err := validateTournamentParticipantPayload(payload); err != nil {
		return nil, err
	}
	return marshalTournamentFrame(tournamentPayloadEvent[TournamentParticipantPayload]{Type: EventTournamentParticipant, Payload: payload})
}

func MarshalTournamentPublic(payload TournamentPublicPayload) ([]byte, error) {
	if err := validateTournamentPublicPayload(payload); err != nil {
		return nil, err
	}
	return marshalTournamentFrame(tournamentPayloadEvent[TournamentPublicPayload]{Type: EventTournamentPublic, Payload: payload})
}

func MarshalTournamentOperator(payload TournamentOperatorPayload) ([]byte, error) {
	if err := validateTournamentOperatorPayload(payload); err != nil {
		return nil, err
	}
	return marshalTournamentFrame(tournamentPayloadEvent[TournamentOperatorPayload]{Type: EventTournamentOperator, Payload: payload})
}

func MarshalTournamentTerminal(payload TournamentTerminalPayload) ([]byte, error) {
	if err := validateTournamentTerminalPayload(payload); err != nil {
		return nil, err
	}
	return marshalTournamentFrame(tournamentPayloadEvent[TournamentTerminalPayload]{
		Type: EventTournamentTerminal, Payload: payload,
	})
}

func MarshalTournamentRejected(rejection TournamentRejection) ([]byte, error) {
	if err := validateTournamentRejection(rejection); err != nil {
		return nil, err
	}
	return marshalTournamentFrame(tournamentRejectedEvent{Type: EventTournamentRejected, Code: rejection.Code, Message: rejection.Message})
}

func TournamentRejectionFor(err error) TournamentRejection {
	switch {
	case errors.Is(err, tournamentws.ErrParticipantRealtimeUnauthenticated),
		errors.Is(err, tournamentws.ErrOperatorRealtimeAuthentication):
		return TournamentRejection{Code: TournamentRejectionUnauthenticated, Message: "authentication required"}
	case errors.Is(err, tournamentws.ErrParticipantRealtimeTournamentScope),
		errors.Is(err, tournamentws.ErrParticipantRealtimePlayerScope),
		errors.Is(err, tournamentws.ErrOperatorRealtimeRole),
		errors.Is(err, tournamentws.ErrOperatorRealtimeScope),
		errors.Is(err, eventdelivery.ErrSubscriberScope):
		return TournamentRejection{Code: TournamentRejectionForbidden, Message: "tournament scope forbidden"}
	case errors.Is(err, tournamentws.ErrPublicRealtimeConnectionLimit):
		return TournamentRejection{Code: TournamentRejectionCapacity, Message: "realtime capacity reached"}
	case errors.Is(err, ErrTournamentInboundFrame):
		return TournamentRejection{Code: TournamentRejectionInvalidFrame, Message: "invalid realtime frame"}
	case errors.Is(err, ErrTournamentInboundRate):
		return TournamentRejection{Code: TournamentRejectionRateLimited, Message: "realtime command rate exceeded"}
	default:
		return TournamentRejection{Code: TournamentRejectionUnavailable, Message: "tournament realtime unavailable"}
	}
}

func DecodeTournamentParticipantMessage(data []byte) (TournamentParticipantMessage, error) {
	eventType, err := tournamentEventType(data)
	if err != nil {
		return TournamentParticipantMessage{}, err
	}
	switch eventType {
	case EventTournamentParticipant:
		var frame tournamentPayloadEvent[TournamentParticipantPayload]
		if err := decodeTournamentStrict(data, &frame); err != nil {
			return TournamentParticipantMessage{}, err
		}
		if err := validateTournamentParticipantPayload(frame.Payload); err != nil {
			return TournamentParticipantMessage{}, err
		}
		payload := frame.Payload
		return TournamentParticipantMessage{Type: eventType, Participant: &payload}, nil
	case EventTournamentTerminal:
		terminal, err := decodeTournamentTerminal(data)
		return TournamentParticipantMessage{Type: eventType, Terminal: &terminal}, err
	case EventTournamentRejected:
		rejection, err := decodeTournamentRejection(data)
		return TournamentParticipantMessage{Type: eventType, Rejected: &rejection}, err
	case EventTournamentPublic, EventTournamentOperator:
		return TournamentParticipantMessage{}, ErrTournamentRoleMismatch
	default:
		return TournamentParticipantMessage{}, ErrTournamentUnknownEvent
	}
}

func DecodeTournamentPublicMessage(data []byte) (TournamentPublicMessage, error) {
	eventType, err := tournamentEventType(data)
	if err != nil {
		return TournamentPublicMessage{}, err
	}
	switch eventType {
	case EventTournamentPublic:
		var frame tournamentPayloadEvent[TournamentPublicPayload]
		if err := decodeTournamentStrict(data, &frame); err != nil {
			return TournamentPublicMessage{}, err
		}
		if err := validateTournamentPublicPayload(frame.Payload); err != nil {
			return TournamentPublicMessage{}, err
		}
		payload := frame.Payload
		return TournamentPublicMessage{Type: eventType, Public: &payload}, nil
	case EventTournamentTerminal:
		terminal, err := decodeTournamentTerminal(data)
		return TournamentPublicMessage{Type: eventType, Terminal: &terminal}, err
	case EventTournamentRejected:
		rejection, err := decodeTournamentRejection(data)
		return TournamentPublicMessage{Type: eventType, Rejected: &rejection}, err
	case EventTournamentParticipant, EventTournamentOperator:
		return TournamentPublicMessage{}, ErrTournamentRoleMismatch
	default:
		return TournamentPublicMessage{}, ErrTournamentUnknownEvent
	}
}

func DecodeTournamentOperatorMessage(data []byte) (TournamentOperatorMessage, error) {
	eventType, err := tournamentEventType(data)
	if err != nil {
		return TournamentOperatorMessage{}, err
	}
	switch eventType {
	case EventTournamentOperator:
		var frame tournamentPayloadEvent[TournamentOperatorPayload]
		if err := decodeTournamentStrict(data, &frame); err != nil {
			return TournamentOperatorMessage{}, err
		}
		if err := validateTournamentOperatorPayload(frame.Payload); err != nil {
			return TournamentOperatorMessage{}, err
		}
		payload := frame.Payload
		return TournamentOperatorMessage{Type: eventType, Operator: &payload}, nil
	case EventTournamentTerminal:
		terminal, err := decodeTournamentTerminal(data)
		return TournamentOperatorMessage{Type: eventType, Terminal: &terminal}, err
	case EventTournamentRejected:
		rejection, err := decodeTournamentRejection(data)
		return TournamentOperatorMessage{Type: eventType, Rejected: &rejection}, err
	case EventTournamentParticipant, EventTournamentPublic:
		return TournamentOperatorMessage{}, ErrTournamentRoleMismatch
	default:
		return TournamentOperatorMessage{}, ErrTournamentUnknownEvent
	}
}

func tournamentEventType(data []byte) (string, error) {
	if wirelimits.ValidateJSON(data) != nil {
		return "", ErrTournamentInvalidPayload
	}
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &header); err != nil || strings.TrimSpace(header.Type) == "" {
		return "", ErrTournamentInvalidPayload
	}
	return header.Type, nil
}

func marshalTournamentFrame(frame any) ([]byte, error) {
	encoded, err := json.Marshal(frame)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTournamentInvalidPayload, err)
	}
	if err := wirelimits.ValidateJSON(encoded); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTournamentInvalidPayload, err)
	}
	return encoded, nil
}

func decodeTournamentStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%w: %w", ErrTournamentInvalidPayload, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrTournamentInvalidPayload
	}
	return nil
}

func validateTournamentParticipantPayload(payload TournamentParticipantPayload) error {
	envelope := payload.Envelope
	participant := envelope.Participant
	root := tournamentws.RealtimeEnvelope{
		SchemaVersion:      envelope.SchemaVersion,
		TournamentID:       envelope.TournamentID,
		Sequence:           envelope.Sequence,
		EventID:            envelope.EventID,
		OccurredAt:         envelope.OccurredAt,
		ProjectionRevision: envelope.ProjectionRevision,
		ResumeID:           cloneTournamentResumeID(envelope.ResumeID),
		Participant:        &participant,
	}
	if root.Validate() != nil {
		return ErrTournamentInvalidPayload
	}
	return nil
}

func validateTournamentPublicPayload(payload TournamentPublicPayload) error {
	envelope := payload.Envelope
	public := envelope.Public
	root := tournamentws.RealtimeEnvelope{
		SchemaVersion:      envelope.SchemaVersion,
		TournamentID:       envelope.TournamentID,
		Sequence:           envelope.Sequence,
		EventID:            envelope.EventID,
		OccurredAt:         envelope.OccurredAt,
		ProjectionRevision: envelope.ProjectionRevision,
		ResumeID:           cloneTournamentResumeID(envelope.ResumeID),
		Public:             &public,
	}
	if root.Validate() != nil {
		return ErrTournamentInvalidPayload
	}
	return nil
}

func validateTournamentOperatorPayload(payload TournamentOperatorPayload) error {
	envelope := payload.Envelope
	operator := envelope.Operator
	root := tournamentws.RealtimeEnvelope{
		SchemaVersion:      envelope.SchemaVersion,
		TournamentID:       envelope.TournamentID,
		Sequence:           envelope.Sequence,
		EventID:            envelope.EventID,
		OccurredAt:         envelope.OccurredAt,
		ProjectionRevision: envelope.ProjectionRevision,
		ResumeID:           cloneTournamentResumeID(envelope.ResumeID),
		Operator:           &operator,
	}
	if root.Validate() != nil {
		return ErrTournamentInvalidPayload
	}
	return nil
}

func validateTournamentTerminalPayload(payload TournamentTerminalPayload) error {
	if payload.SchemaVersion != tournamentws.TournamentRealtimeSchemaVersion ||
		payload.TournamentID == uuid.Nil || payload.Sequence < 1 || payload.EventID == uuid.Nil ||
		payload.OccurredAt.IsZero() || payload.OccurredAt.Location() != time.UTC || !payload.State.IsTerminal() {
		return ErrTournamentInvalidPayload
	}
	return nil
}

func validateTournamentRejection(rejection TournamentRejection) error {
	switch rejection.Code {
	case TournamentRejectionUnauthenticated, TournamentRejectionForbidden,
		TournamentRejectionUnavailable, TournamentRejectionCapacity,
		TournamentRejectionInvalidFrame, TournamentRejectionRateLimited:
	default:
		return ErrTournamentInvalidPayload
	}
	if strings.TrimSpace(rejection.Message) == "" || !wirelimits.StringWithinLimit(rejection.Message) {
		return ErrTournamentInvalidPayload
	}
	return nil
}

func decodeTournamentRejection(data []byte) (TournamentRejection, error) {
	var frame tournamentRejectedEvent
	if err := decodeTournamentStrict(data, &frame); err != nil {
		return TournamentRejection{}, err
	}
	rejection := TournamentRejection{Code: frame.Code, Message: frame.Message}
	if err := validateTournamentRejection(rejection); err != nil {
		return TournamentRejection{}, err
	}
	return rejection, nil
}

func decodeTournamentTerminal(data []byte) (TournamentTerminalPayload, error) {
	var frame tournamentPayloadEvent[TournamentTerminalPayload]
	if err := decodeTournamentStrict(data, &frame); err != nil {
		return TournamentTerminalPayload{}, err
	}
	if frame.Type != EventTournamentTerminal || validateTournamentTerminalPayload(frame.Payload) != nil {
		return TournamentTerminalPayload{}, ErrTournamentInvalidPayload
	}
	return frame.Payload, nil
}
