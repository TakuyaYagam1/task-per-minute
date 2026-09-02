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

	arenaws "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket/arena"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	duelusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/duel"
)

const (
	EventQueueJoined          = "queue_joined"
	EventQueueLeft            = "queue_left"
	EventMatchFound           = "match_found"
	EventTaskAssigned         = "task_assigned"
	EventFlagResult           = "flag_result"
	EventHintUnlocked         = "hint_unlocked"
	EventDuelExpired          = "duel_expired"
	EventDuelFinished         = "duel_finished"
	EventOpponentSolved       = "opponent_solved"
	EventOpponentDisconnected = "opponent_disconnected"
	EventOpponentReconnected  = "opponent_reconnected"
	EventDuelResume           = "duel_resume"
	EventPong                 = "pong"
	EventError                = "error"

	EventJoinQueue  = "join_queue"
	EventLeaveQueue = "leave_queue"
	EventFlagSubmit = "flag_submit"
	EventSurrender  = "surrender"
	EventPing       = "ping"
)

const (
	EventArenaParticipant = "arena_participant"
	EventArenaPublic      = "arena_public"
	EventArenaOperator    = "arena_operator"
	EventArenaTerminal    = "arena_terminal"
	EventArenaRejected    = "arena_rejected"
	EventArenaConnect     = "arena_connect"
	EventArenaResume      = "arena_resume"
)

const (
	ErrorUnknownEvent    = "unknown_event"
	ErrorInvalidJSON     = "invalid_json"
	ErrorInvalidPayload  = "invalid_payload"
	ErrorServerShutdown  = "server_shutdown"
	ErrorInternal        = "internal"
	ErrorDuelPaused      = "duel.paused"
	ErrorStaleConnection = "stale_connection"
)

var (
	ErrArenaInvalidJSON      = errors.New("invalid Arena JSON")
	ErrArenaInvalidPayload   = errors.New("invalid Arena payload")
	ErrArenaUnknownEvent     = errors.New("unknown Arena event")
	ErrArenaRoleMismatch     = errors.New("arena role does not match connection")
	ErrArenaCommandForbidden = errors.New("arena command is forbidden")
	ErrArenaConnectionClosed = errors.New("arena connection is closed")
)

type ArenaRole string

const (
	ArenaRoleParticipant ArenaRole = "participant"
	ArenaRolePublic      ArenaRole = "public"
	ArenaRoleOperator    ArenaRole = "operator"
)

type ArenaRejectionCode string

const (
	ArenaRejectionInvalidJSON    ArenaRejectionCode = "arena_invalid_json"
	ArenaRejectionInvalidPayload ArenaRejectionCode = "arena_invalid_payload"
	ArenaRejectionUnknownEvent   ArenaRejectionCode = "arena_unknown_event"
	ArenaRejectionRoleMismatch   ArenaRejectionCode = "arena_role_mismatch"
	ArenaRejectionForbidden      ArenaRejectionCode = "arena_forbidden"
	ArenaRejectionClosed         ArenaRejectionCode = "arena_closed"
)

type IncomingEvent struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type Event struct {
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type ArenaResumePayload struct {
	Role   ArenaRole              `json:"role"`
	Cursor arenaws.RealtimeCursor `json:"cursor"`
}

type ArenaConnectPayload struct {
	Role         ArenaRole `json:"role"`
	TournamentID uuid.UUID `json:"tournament_id"`
}

type ArenaCommand struct {
	Type    string
	Connect *ArenaConnectPayload
	Resume  *ArenaResumePayload
}

type ArenaParticipantPayload struct {
	UsesSnapshot bool                                  `json:"uses_snapshot"`
	Envelopes    []arenaws.ParticipantRealtimeEnvelope `json:"envelopes"`
}

type ArenaPublicPayload struct {
	UsesSnapshot bool                  `json:"uses_snapshot"`
	Envelopes    []ArenaPublicEnvelope `json:"envelopes"`
}

type ArenaOperatorPayload struct {
	UsesSnapshot bool                    `json:"uses_snapshot"`
	Envelopes    []ArenaOperatorEnvelope `json:"envelopes"`
}

type ArenaPublicEnvelope struct {
	SchemaVersion      int                    `json:"schema_version"`
	TournamentID       uuid.UUID              `json:"tournament_id"`
	Sequence           int64                  `json:"sequence"`
	EventID            uuid.UUID              `json:"event_id"`
	OccurredAt         time.Time              `json:"occurred_at"`
	ProjectionRevision int64                  `json:"projection_revision"`
	Public             arenaws.PublicSnapshot `json:"public"`
}

type ArenaOperatorEnvelope struct {
	SchemaVersion      int                      `json:"schema_version"`
	TournamentID       uuid.UUID                `json:"tournament_id"`
	Sequence           int64                    `json:"sequence"`
	EventID            uuid.UUID                `json:"event_id"`
	OccurredAt         time.Time                `json:"occurred_at"`
	ProjectionRevision int64                    `json:"projection_revision"`
	Operator           arenaws.OperatorSnapshot `json:"operator"`
}

type ArenaParticipantTerminalPayload struct {
	TournamentID  uuid.UUID `json:"tournament_id"`
	ParticipantID uuid.UUID `json:"participant_id"`
	State         string    `json:"state"`
}

type ArenaPublicTerminalPayload struct {
	TournamentID uuid.UUID `json:"tournament_id"`
	State        string    `json:"state"`
}

type ArenaOperatorTerminalPayload struct {
	TournamentID   uuid.UUID `json:"tournament_id"`
	CancellationID uuid.UUID `json:"cancellation_id"`
	State          string    `json:"state"`
	Reason         string    `json:"reason"`
}

type ArenaRejection struct {
	Code    ArenaRejectionCode `json:"code"`
	Message string             `json:"message"`
}

type ArenaParticipantMessage struct {
	Type        string
	Participant *ArenaParticipantPayload
	Terminal    *ArenaParticipantTerminalPayload
	Rejected    *ArenaRejection
}

type ArenaPublicMessage struct {
	Type     string
	Public   *ArenaPublicPayload
	Terminal *ArenaPublicTerminalPayload
	Rejected *ArenaRejection
}

type ArenaOperatorMessage struct {
	Type     string
	Operator *ArenaOperatorPayload
	Terminal *ArenaOperatorTerminalPayload
	Rejected *ArenaRejection
}

type arenaPayloadEvent[T any] struct {
	Type    string `json:"type"`
	Payload T      `json:"payload"`
}

type arenaRejectedEvent struct {
	Type    string             `json:"type"`
	Code    ArenaRejectionCode `json:"code"`
	Message string             `json:"message"`
}

func DecodeArenaCommand(expectedRole ArenaRole, data []byte) (ArenaCommand, error) {
	if !validArenaRole(expectedRole) {
		return ArenaCommand{}, ErrArenaRoleMismatch
	}
	eventType, err := arenaEventType(data)
	if err != nil {
		return ArenaCommand{}, err
	}

	switch eventType {
	case EventArenaConnect:
		var frame arenaPayloadEvent[ArenaConnectPayload]
		if err := decodeArenaStrict(data, &frame); err != nil {
			return ArenaCommand{}, err
		}
		if frame.Payload.Role != expectedRole {
			return ArenaCommand{}, ErrArenaRoleMismatch
		}
		if frame.Payload.TournamentID == uuid.Nil {
			return ArenaCommand{}, ErrArenaInvalidPayload
		}
		payload := frame.Payload
		return ArenaCommand{Type: eventType, Connect: &payload}, nil
	case EventArenaResume:
		var frame arenaPayloadEvent[ArenaResumePayload]
		if err := decodeArenaStrict(data, &frame); err != nil {
			return ArenaCommand{}, err
		}
		if frame.Payload.Role != expectedRole {
			return ArenaCommand{}, ErrArenaRoleMismatch
		}
		if !validArenaCursor(frame.Payload.Cursor) {
			return ArenaCommand{}, ErrArenaInvalidPayload
		}
		payload := frame.Payload
		return ArenaCommand{Type: eventType, Resume: &payload}, nil
	default:
		if strings.HasPrefix(eventType, "arena_") {
			return ArenaCommand{}, ErrArenaCommandForbidden
		}
		return ArenaCommand{}, ErrArenaUnknownEvent
	}
}

func NewArenaParticipantPayload(result arenaws.ParticipantRealtimeResult) (ArenaParticipantPayload, error) {
	payload := ArenaParticipantPayload{
		UsesSnapshot: result.UsesSnapshot,
		Envelopes:    append([]arenaws.ParticipantRealtimeEnvelope(nil), result.Envelopes...),
	}
	if payload.Envelopes == nil {
		payload.Envelopes = []arenaws.ParticipantRealtimeEnvelope{}
	}
	if err := validateArenaParticipantPayload(payload); err != nil {
		return ArenaParticipantPayload{}, err
	}
	return payload, nil
}

func NewArenaPublicPayload(usesSnapshot bool, envelopes []arenaws.RealtimeEnvelope) (ArenaPublicPayload, error) {
	payload := ArenaPublicPayload{UsesSnapshot: usesSnapshot, Envelopes: make([]ArenaPublicEnvelope, len(envelopes))}
	for index, envelope := range envelopes {
		if envelope.Public == nil || envelope.Participant != nil || envelope.Operator != nil || envelope.Validate() != nil {
			return ArenaPublicPayload{}, fmt.Errorf("%w: public envelope %d", ErrArenaInvalidPayload, index)
		}
		payload.Envelopes[index] = ArenaPublicEnvelope{
			SchemaVersion:      envelope.SchemaVersion,
			TournamentID:       envelope.TournamentID,
			Sequence:           envelope.Sequence,
			EventID:            envelope.EventID,
			OccurredAt:         envelope.OccurredAt,
			ProjectionRevision: envelope.ProjectionRevision,
			Public:             *envelope.Public,
		}
	}
	return payload, nil
}

func NewArenaOperatorPayload(result arenaws.OperatorRealtimeResult) (ArenaOperatorPayload, error) {
	payload := ArenaOperatorPayload{UsesSnapshot: result.UsesSnapshot, Envelopes: make([]ArenaOperatorEnvelope, len(result.Envelopes))}
	for index, envelope := range result.Envelopes {
		if envelope.Operator == nil || envelope.Participant != nil || envelope.Public != nil || envelope.Validate() != nil {
			return ArenaOperatorPayload{}, fmt.Errorf("%w: operator envelope %d", ErrArenaInvalidPayload, index)
		}
		payload.Envelopes[index] = ArenaOperatorEnvelope{
			SchemaVersion:      envelope.SchemaVersion,
			TournamentID:       envelope.TournamentID,
			Sequence:           envelope.Sequence,
			EventID:            envelope.EventID,
			OccurredAt:         envelope.OccurredAt,
			ProjectionRevision: envelope.ProjectionRevision,
			Operator:           *envelope.Operator,
		}
	}
	return payload, nil
}

func MarshalArenaParticipant(payload ArenaParticipantPayload) ([]byte, error) {
	if err := validateArenaParticipantPayload(payload); err != nil {
		return nil, err
	}
	return json.Marshal(arenaPayloadEvent[ArenaParticipantPayload]{Type: EventArenaParticipant, Payload: payload})
}

func MarshalArenaPublic(payload ArenaPublicPayload) ([]byte, error) {
	if err := validateArenaPublicPayload(payload); err != nil {
		return nil, err
	}
	return json.Marshal(arenaPayloadEvent[ArenaPublicPayload]{Type: EventArenaPublic, Payload: payload})
}

func MarshalArenaOperator(payload ArenaOperatorPayload) ([]byte, error) {
	if err := validateArenaOperatorPayload(payload); err != nil {
		return nil, err
	}
	return json.Marshal(arenaPayloadEvent[ArenaOperatorPayload]{Type: EventArenaOperator, Payload: payload})
}

func MarshalArenaParticipantTerminal(payload ArenaParticipantTerminalPayload) ([]byte, error) {
	if err := validateArenaParticipantTerminal(payload); err != nil {
		return nil, err
	}
	return json.Marshal(arenaPayloadEvent[ArenaParticipantTerminalPayload]{Type: EventArenaTerminal, Payload: payload})
}

func MarshalArenaPublicTerminal(payload ArenaPublicTerminalPayload) ([]byte, error) {
	if err := validateArenaPublicTerminal(payload); err != nil {
		return nil, err
	}
	return json.Marshal(arenaPayloadEvent[ArenaPublicTerminalPayload]{Type: EventArenaTerminal, Payload: payload})
}

func MarshalArenaOperatorTerminal(payload ArenaOperatorTerminalPayload) ([]byte, error) {
	if err := validateArenaOperatorTerminal(payload); err != nil {
		return nil, err
	}
	return json.Marshal(arenaPayloadEvent[ArenaOperatorTerminalPayload]{Type: EventArenaTerminal, Payload: payload})
}

func MarshalArenaRejected(rejection ArenaRejection) ([]byte, error) {
	if err := validateArenaRejection(rejection); err != nil {
		return nil, err
	}
	return json.Marshal(arenaRejectedEvent{
		Type: EventArenaRejected, Code: rejection.Code, Message: rejection.Message,
	})
}

func DecodeArenaParticipantMessage(data []byte) (ArenaParticipantMessage, error) {
	eventType, err := arenaEventType(data)
	if err != nil {
		return ArenaParticipantMessage{}, err
	}
	switch eventType {
	case EventArenaParticipant:
		var frame arenaPayloadEvent[ArenaParticipantPayload]
		if err := decodeArenaStrict(data, &frame); err != nil {
			return ArenaParticipantMessage{}, err
		}
		if err := validateArenaParticipantPayload(frame.Payload); err != nil {
			return ArenaParticipantMessage{}, err
		}
		payload := frame.Payload
		return ArenaParticipantMessage{Type: eventType, Participant: &payload}, nil
	case EventArenaTerminal:
		if err := requireArenaTerminalRole(data, ArenaRoleParticipant); err != nil {
			return ArenaParticipantMessage{}, err
		}
		var frame arenaPayloadEvent[ArenaParticipantTerminalPayload]
		if err := decodeArenaStrict(data, &frame); err != nil {
			return ArenaParticipantMessage{}, err
		}
		if err := validateArenaParticipantTerminal(frame.Payload); err != nil {
			return ArenaParticipantMessage{}, err
		}
		payload := frame.Payload
		return ArenaParticipantMessage{Type: eventType, Terminal: &payload}, nil
	case EventArenaRejected:
		rejection, err := decodeArenaRejection(data)
		if err != nil {
			return ArenaParticipantMessage{}, err
		}
		return ArenaParticipantMessage{Type: eventType, Rejected: &rejection}, nil
	case EventArenaPublic, EventArenaOperator:
		return ArenaParticipantMessage{}, ErrArenaRoleMismatch
	default:
		return ArenaParticipantMessage{}, ErrArenaUnknownEvent
	}
}

func DecodeArenaPublicMessage(data []byte) (ArenaPublicMessage, error) {
	eventType, err := arenaEventType(data)
	if err != nil {
		return ArenaPublicMessage{}, err
	}
	switch eventType {
	case EventArenaPublic:
		var frame arenaPayloadEvent[ArenaPublicPayload]
		if err := decodeArenaStrict(data, &frame); err != nil {
			return ArenaPublicMessage{}, err
		}
		if err := validateArenaPublicPayload(frame.Payload); err != nil {
			return ArenaPublicMessage{}, err
		}
		payload := frame.Payload
		return ArenaPublicMessage{Type: eventType, Public: &payload}, nil
	case EventArenaTerminal:
		if err := requireArenaTerminalRole(data, ArenaRolePublic); err != nil {
			return ArenaPublicMessage{}, err
		}
		var frame arenaPayloadEvent[ArenaPublicTerminalPayload]
		if err := decodeArenaStrict(data, &frame); err != nil {
			return ArenaPublicMessage{}, err
		}
		if err := validateArenaPublicTerminal(frame.Payload); err != nil {
			return ArenaPublicMessage{}, err
		}
		payload := frame.Payload
		return ArenaPublicMessage{Type: eventType, Terminal: &payload}, nil
	case EventArenaRejected:
		rejection, err := decodeArenaRejection(data)
		if err != nil {
			return ArenaPublicMessage{}, err
		}
		return ArenaPublicMessage{Type: eventType, Rejected: &rejection}, nil
	case EventArenaParticipant, EventArenaOperator:
		return ArenaPublicMessage{}, ErrArenaRoleMismatch
	default:
		return ArenaPublicMessage{}, ErrArenaUnknownEvent
	}
}

func DecodeArenaOperatorMessage(data []byte) (ArenaOperatorMessage, error) {
	eventType, err := arenaEventType(data)
	if err != nil {
		return ArenaOperatorMessage{}, err
	}
	switch eventType {
	case EventArenaOperator:
		var frame arenaPayloadEvent[ArenaOperatorPayload]
		if err := decodeArenaStrict(data, &frame); err != nil {
			return ArenaOperatorMessage{}, err
		}
		if err := validateArenaOperatorPayload(frame.Payload); err != nil {
			return ArenaOperatorMessage{}, err
		}
		payload := frame.Payload
		return ArenaOperatorMessage{Type: eventType, Operator: &payload}, nil
	case EventArenaTerminal:
		if err := requireArenaTerminalRole(data, ArenaRoleOperator); err != nil {
			return ArenaOperatorMessage{}, err
		}
		var frame arenaPayloadEvent[ArenaOperatorTerminalPayload]
		if err := decodeArenaStrict(data, &frame); err != nil {
			return ArenaOperatorMessage{}, err
		}
		if err := validateArenaOperatorTerminal(frame.Payload); err != nil {
			return ArenaOperatorMessage{}, err
		}
		payload := frame.Payload
		return ArenaOperatorMessage{Type: eventType, Terminal: &payload}, nil
	case EventArenaRejected:
		rejection, err := decodeArenaRejection(data)
		if err != nil {
			return ArenaOperatorMessage{}, err
		}
		return ArenaOperatorMessage{Type: eventType, Rejected: &rejection}, nil
	case EventArenaParticipant, EventArenaPublic:
		return ArenaOperatorMessage{}, ErrArenaRoleMismatch
	default:
		return ArenaOperatorMessage{}, ErrArenaUnknownEvent
	}
}

func ArenaRejectionFor(err error) ArenaRejection {
	switch {
	case errors.Is(err, ErrArenaInvalidJSON):
		return ArenaRejection{Code: ArenaRejectionInvalidJSON, Message: "Arena command is malformed"}
	case errors.Is(err, ErrArenaUnknownEvent):
		return ArenaRejection{Code: ArenaRejectionUnknownEvent, Message: "Arena event is unknown"}
	case errors.Is(err, ErrArenaRoleMismatch):
		return ArenaRejection{Code: ArenaRejectionRoleMismatch, Message: "Arena role does not match connection"}
	case errors.Is(err, ErrArenaCommandForbidden):
		return ArenaRejection{Code: ArenaRejectionForbidden, Message: "Arena command is forbidden"}
	case errors.Is(err, ErrArenaConnectionClosed):
		return ArenaRejection{Code: ArenaRejectionClosed, Message: "Arena connection is closed"}
	default:
		return ArenaRejection{Code: ArenaRejectionInvalidPayload, Message: "Arena command payload is invalid"}
	}
}

type DuelPayload struct {
	ID         uuid.UUID  `json:"id"`
	Player1ID  uuid.UUID  `json:"player1_id"`
	Player2ID  uuid.UUID  `json:"player2_id"`
	Status     string     `json:"status"`
	WinnerID   *uuid.UUID `json:"winner_id,omitempty"`
	Deadline   time.Time  `json:"deadline"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

type TaskPayload struct {
	ID            uuid.UUID           `json:"id"`
	Title         string              `json:"title"`
	Description   string              `json:"description"`
	Category      string              `json:"category"`
	Difficulty    string              `json:"difficulty"`
	TimeLimit     int                 `json:"time_limit"`
	TimeLimitSec  int                 `json:"time_limit_seconds"`
	TaskURL       *string             `json:"task_url,omitempty"`
	SourceURL     *string             `json:"source_url,omitempty"`
	SourceFileURL *string             `json:"source_file_url,omitempty"`
	HintSchedule  []HintScheduleEntry `json:"hint_schedule,omitempty"`
	UnlockedHints []UnlockedHint      `json:"unlocked_hints,omitempty"`
}

type MatchFoundPayload struct {
	DuelID           uuid.UUID   `json:"duel_id"`
	OpponentUsername string      `json:"opponent_username"`
	Duel             DuelPayload `json:"duel"`
}

type TaskAssignedPayload struct {
	DuelID           uuid.UUID   `json:"duel_id"`
	Deadline         time.Time   `json:"deadline"`
	TimeLimitSeconds int         `json:"time_limit_seconds"`
	Task             TaskPayload `json:"task"`
}

type FlagResultPayload struct {
	DuelID  uuid.UUID `json:"duel_id"`
	Correct bool      `json:"correct"`
	Message string    `json:"message,omitempty"`
}

type DuelFinishedPayload struct {
	DuelID         uuid.UUID   `json:"duel_id"`
	WinnerID       *uuid.UUID  `json:"winner_id,omitempty"`
	WinnerUsername *string     `json:"winner_username,omitempty"`
	YourSolved     bool        `json:"your_solved"`
	OpponentSolved bool        `json:"opponent_solved"`
	Duel           DuelPayload `json:"duel"`
}

type DuelExpiredPayload struct {
	DuelID uuid.UUID `json:"duel_id"`
}

type HintScheduleEntry struct {
	HintIndex int       `json:"hint_index"`
	UnlockAt  time.Time `json:"unlock_at"`
}

type UnlockedHint struct {
	HintIndex  int       `json:"hint_index"`
	Hint       string    `json:"hint"`
	UnlockedAt time.Time `json:"unlocked_at"`
}

type HintUnlockedPayload struct {
	DuelID     uuid.UUID `json:"duel_id"`
	TaskID     uuid.UUID `json:"task_id"`
	HintIndex  int       `json:"hint_index"`
	Hint       string    `json:"hint"`
	UnlockedAt time.Time `json:"unlocked_at"`
}

type OpponentSolvedPayload struct {
	DuelID   uuid.UUID `json:"duel_id"`
	PlayerID uuid.UUID `json:"player_id"`
}

type OpponentDisconnectedPayload struct {
	DuelID            uuid.UUID `json:"duel_id"`
	PlayerID          uuid.UUID `json:"player_id"`
	ReconnectDeadline time.Time `json:"reconnect_deadline"`
}

type OpponentReconnectedPayload struct {
	DuelID   uuid.UUID `json:"duel_id"`
	PlayerID uuid.UUID `json:"player_id"`
	Deadline time.Time `json:"deadline"`
}

type DuelResumePayload struct {
	DuelID                    uuid.UUID    `json:"duel_id"`
	OpponentID                uuid.UUID    `json:"opponent_id"`
	OpponentUsername          string       `json:"opponent_username,omitempty"`
	Deadline                  time.Time    `json:"deadline"`
	OpponentDisconnected      bool         `json:"opponent_disconnected,omitempty"`
	OpponentReconnectDeadline *time.Time   `json:"opponent_reconnect_deadline,omitempty"`
	Task                      *TaskPayload `json:"task,omitempty"`
}

func marshalEvent(typ string, payload any) ([]byte, error) {
	return json.Marshal(Event{Type: typ, Payload: payload})
}

func marshalError(code, message string) ([]byte, error) {
	return json.Marshal(Event{Type: EventError, Code: code, Message: message})
}

func arenaEventType(data []byte) (string, error) {
	if !json.Valid(data) {
		return "", ErrArenaInvalidJSON
	}
	var header struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &header); err != nil || strings.TrimSpace(header.Type) == "" {
		return "", ErrArenaInvalidPayload
	}
	return header.Type, nil
}

func decodeArenaStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%w: %w", ErrArenaInvalidPayload, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrArenaInvalidJSON
	}
	return nil
}

func validArenaRole(role ArenaRole) bool {
	return role == ArenaRoleParticipant || role == ArenaRolePublic || role == ArenaRoleOperator
}

func validArenaCursor(cursor arenaws.RealtimeCursor) bool {
	return cursor.SchemaVersion == arenaws.ArenaRealtimeSchemaVersion &&
		cursor.TournamentID != uuid.Nil && cursor.LastSequence >= 1 && cursor.ProjectionRevision >= 1
}

func validateArenaParticipantPayload(payload ArenaParticipantPayload) error {
	if payload.Envelopes == nil {
		return ErrArenaInvalidPayload
	}
	for index := range payload.Envelopes {
		envelope := payload.Envelopes[index]
		participant := envelope.Participant
		root := arenaws.RealtimeEnvelope{
			SchemaVersion:      envelope.SchemaVersion,
			TournamentID:       envelope.TournamentID,
			Sequence:           envelope.Sequence,
			EventID:            envelope.EventID,
			OccurredAt:         envelope.OccurredAt,
			ProjectionRevision: envelope.ProjectionRevision,
			Participant:        &participant,
		}
		if err := root.Validate(); err != nil {
			return fmt.Errorf("%w: participant envelope %d", ErrArenaInvalidPayload, index)
		}
	}
	return nil
}

func validateArenaPublicPayload(payload ArenaPublicPayload) error {
	if payload.Envelopes == nil {
		return ErrArenaInvalidPayload
	}
	for index := range payload.Envelopes {
		envelope := payload.Envelopes[index]
		public := envelope.Public
		root := arenaws.RealtimeEnvelope{
			SchemaVersion:      envelope.SchemaVersion,
			TournamentID:       envelope.TournamentID,
			Sequence:           envelope.Sequence,
			EventID:            envelope.EventID,
			OccurredAt:         envelope.OccurredAt,
			ProjectionRevision: envelope.ProjectionRevision,
			Public:             &public,
		}
		if root.Validate() != nil {
			return fmt.Errorf("%w: public envelope %d", ErrArenaInvalidPayload, index)
		}
	}
	return nil
}

func validateArenaOperatorPayload(payload ArenaOperatorPayload) error {
	if payload.Envelopes == nil {
		return ErrArenaInvalidPayload
	}
	for index := range payload.Envelopes {
		envelope := payload.Envelopes[index]
		operator := envelope.Operator
		root := arenaws.RealtimeEnvelope{
			SchemaVersion:      envelope.SchemaVersion,
			TournamentID:       envelope.TournamentID,
			Sequence:           envelope.Sequence,
			EventID:            envelope.EventID,
			OccurredAt:         envelope.OccurredAt,
			ProjectionRevision: envelope.ProjectionRevision,
			Operator:           &operator,
		}
		if root.Validate() != nil {
			return fmt.Errorf("%w: operator envelope %d", ErrArenaInvalidPayload, index)
		}
	}
	return nil
}

func validateArenaParticipantTerminal(payload ArenaParticipantTerminalPayload) error {
	if payload.TournamentID == uuid.Nil || payload.ParticipantID == uuid.Nil || strings.TrimSpace(payload.State) == "" {
		return ErrArenaInvalidPayload
	}
	return nil
}

func validateArenaPublicTerminal(payload ArenaPublicTerminalPayload) error {
	if payload.TournamentID == uuid.Nil || strings.TrimSpace(payload.State) == "" {
		return ErrArenaInvalidPayload
	}
	return nil
}

func validateArenaOperatorTerminal(payload ArenaOperatorTerminalPayload) error {
	if payload.TournamentID == uuid.Nil || payload.CancellationID == uuid.Nil ||
		strings.TrimSpace(payload.State) == "" || strings.TrimSpace(payload.Reason) == "" {
		return ErrArenaInvalidPayload
	}
	return nil
}

func validateArenaRejection(rejection ArenaRejection) error {
	switch rejection.Code {
	case ArenaRejectionInvalidJSON, ArenaRejectionInvalidPayload, ArenaRejectionUnknownEvent,
		ArenaRejectionRoleMismatch, ArenaRejectionForbidden, ArenaRejectionClosed:
	default:
		return ErrArenaInvalidPayload
	}
	if strings.TrimSpace(rejection.Message) == "" {
		return ErrArenaInvalidPayload
	}
	return nil
}

func decodeArenaRejection(data []byte) (ArenaRejection, error) {
	var frame arenaRejectedEvent
	if err := decodeArenaStrict(data, &frame); err != nil {
		return ArenaRejection{}, err
	}
	rejection := ArenaRejection{Code: frame.Code, Message: frame.Message}
	if err := validateArenaRejection(rejection); err != nil {
		return ArenaRejection{}, err
	}
	return rejection, nil
}

func requireArenaTerminalRole(data []byte, expected ArenaRole) error {
	var frame arenaPayloadEvent[json.RawMessage]
	if err := decodeArenaStrict(data, &frame); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(frame.Payload, &fields); err != nil || fields == nil {
		return ErrArenaInvalidPayload
	}
	_, participant := fields["participant_id"]
	_, cancellation := fields["cancellation_id"]
	_, reason := fields["reason"]
	if participant && (cancellation || reason) {
		return ErrArenaInvalidPayload
	}
	role := ArenaRolePublic
	if participant {
		role = ArenaRoleParticipant
	} else if cancellation || reason {
		role = ArenaRoleOperator
	}
	if role != expected {
		return ErrArenaRoleMismatch
	}
	return nil
}

func duelPayload(duel *domain.Duel) DuelPayload {
	return DuelPayload{
		ID:         duel.ID,
		Player1ID:  duel.Player1ID,
		Player2ID:  duel.Player2ID,
		Status:     duel.Status.String(),
		WinnerID:   duel.WinnerID,
		Deadline:   duel.Deadline,
		StartedAt:  duel.StartedAt,
		FinishedAt: duel.FinishedAt,
	}
}

func taskPayload(task *domain.Task, hints duelusecase.HintSnapshot) TaskPayload {
	return TaskPayload{
		ID:            task.ID,
		Title:         task.Title,
		Description:   task.Description,
		Category:      task.Category.String(),
		Difficulty:    task.Difficulty.String(),
		TimeLimit:     task.TimeLimit,
		TimeLimitSec:  task.TimeLimit,
		TaskURL:       task.TaskURL,
		SourceURL:     task.SourceFileURL,
		SourceFileURL: task.SourceFileURL,
		HintSchedule:  hintSchedulePayload(hints.Schedule),
		UnlockedHints: unlockedHintsPayload(hints.Unlocked),
	}
}

func taskVisibleHintSchedule(startedAt time.Time, task *domain.Task) []domain.HintScheduleEntry {
	if task == nil {
		return nil
	}
	schedule := domain.BuildHintSchedule(startedAt, task.TimeLimit)
	out := make([]domain.HintScheduleEntry, 0, len(schedule))
	for idx, entry := range schedule {
		if _, ok := domain.TaskHintText(task.Hints, idx); ok {
			out = append(out, entry)
		}
	}
	return out
}

func duelFinishedPayload(
	duel *domain.Duel,
	recipientID uuid.UUID,
	solvedPlayerID *uuid.UUID,
	winnerUsername *string,
) DuelFinishedPayload {
	opponent, _ := duelOpponentID(duel, recipientID)
	yourSolved := solvedPlayerID != nil && *solvedPlayerID == recipientID
	opponentSolved := solvedPlayerID != nil && *solvedPlayerID == opponent
	return DuelFinishedPayload{
		DuelID:         duel.ID,
		WinnerID:       duel.WinnerID,
		WinnerUsername: winnerUsername,
		YourSolved:     yourSolved,
		OpponentSolved: opponentSolved,
		Duel:           duelPayload(duel),
	}
}

func duelOpponentID(duel *domain.Duel, playerID uuid.UUID) (uuid.UUID, bool) {
	if duel == nil {
		return uuid.Nil, false
	}
	switch playerID {
	case duel.Player1ID:
		return duel.Player2ID, true
	case duel.Player2ID:
		return duel.Player1ID, true
	default:
		return uuid.Nil, false
	}
}

func hintSchedulePayload(schedule []domain.HintScheduleEntry) []HintScheduleEntry {
	out := make([]HintScheduleEntry, 0, len(schedule))
	for _, entry := range schedule {
		out = append(out, HintScheduleEntry{
			HintIndex: entry.Index,
			UnlockAt:  entry.UnlockAt,
		})
	}
	return out
}

func unlockedHintsPayload(hints []domain.UnlockedHint) []UnlockedHint {
	out := make([]UnlockedHint, 0, len(hints))
	for _, hint := range hints {
		out = append(out, UnlockedHint{
			HintIndex:  hint.Index,
			Hint:       hint.Text,
			UnlockedAt: hint.UnlockedAt,
		})
	}
	return out
}

func hintUnlockedPayload(event duelusecase.HintUnlocked) HintUnlockedPayload {
	return HintUnlockedPayload{
		DuelID:     event.DuelID,
		TaskID:     event.TaskID,
		HintIndex:  event.Hint.Index,
		Hint:       event.Hint.Text,
		UnlockedAt: event.Hint.UnlockedAt,
	}
}
