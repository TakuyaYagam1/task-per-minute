package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

// goldenRuntimeCommandSpec is the transport-independent identity of one
// mutation. The digest deliberately includes authenticated actor, scope,
// target, expected fence, ready window, and the canonical request payload.
type goldenRuntimeCommandSpec struct {
	CommandID               uuid.UUID
	TournamentID            uuid.UUID
	RosterID                uuid.UUID
	ActorKind               string
	ActorID                 uuid.UUID
	Scope                   string
	Kind                    string
	AttemptID               uuid.UUID
	ParticipantID           uuid.UUID
	ExpectedRuntimeRevision int64
	ExpectedReadyWindowID   uuid.UUID
	Payload                 any
}

type goldenRuntimeCommandDigestInput struct {
	TournamentID            string `json:"tournament_id"`
	RosterID                string `json:"roster_id"`
	ActorKind               string `json:"actor_kind"`
	ActorID                 string `json:"actor_id"`
	Scope                   string `json:"scope"`
	Kind                    string `json:"kind"`
	AttemptID               string `json:"attempt_id,omitempty"`
	ParticipantID           string `json:"participant_id,omitempty"`
	ExpectedRuntimeRevision int64  `json:"expected_runtime_revision"`
	ExpectedReadyWindowID   string `json:"expected_ready_window_id,omitempty"`
	Payload                 any    `json:"payload"`
}

func (spec goldenRuntimeCommandSpec) digest() [sha256.Size]byte {
	payload, err := json.Marshal(goldenRuntimeCommandDigestInput{
		TournamentID:            spec.TournamentID.String(),
		RosterID:                spec.RosterID.String(),
		ActorKind:               spec.ActorKind,
		ActorID:                 spec.ActorID.String(),
		Scope:                   spec.Scope,
		Kind:                    spec.Kind,
		AttemptID:               goldenRuntimeOptionalUUID(spec.AttemptID),
		ParticipantID:           goldenRuntimeOptionalUUID(spec.ParticipantID),
		ExpectedRuntimeRevision: spec.ExpectedRuntimeRevision,
		ExpectedReadyWindowID:   goldenRuntimeOptionalUUID(spec.ExpectedReadyWindowID),
		Payload:                 spec.Payload,
	})
	if err != nil {
		return sha256.Sum256([]byte("invalid-golden-runtime-command"))
	}
	return sha256.Sum256(payload)
}

func goldenRuntimeOptionalUUID(value uuid.UUID) string {
	if value == uuid.Nil {
		return ""
	}
	return value.String()
}

func goldenRuntimeNullUUID(value uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: value, Valid: value != uuid.Nil}
}

func goldenRuntimeCommandMatches(row sqlc.GoldenRuntimeCommand, spec goldenRuntimeCommandSpec) bool {
	digest := spec.digest()
	return row.CommandID == spec.CommandID &&
		row.TournamentID == spec.TournamentID && row.RosterID == spec.RosterID &&
		row.ActorKind == spec.ActorKind && row.ActorID == goldenRuntimeNullUUID(spec.ActorID) &&
		row.CommandScope == spec.Scope && row.CommandKind == spec.Kind &&
		row.AttemptID == goldenRuntimeNullUUID(spec.AttemptID) &&
		row.ParticipantID == goldenRuntimeNullUUID(spec.ParticipantID) &&
		row.ExpectedRuntimeRevision == spec.ExpectedRuntimeRevision &&
		row.ExpectedReadyWindowID == goldenRuntimeNullUUID(spec.ExpectedReadyWindowID) &&
		bytes.Equal(row.CommandDigest, digest[:])
}

func goldenRuntimeReplay(
	ctx context.Context,
	q *sqlc.Queries,
	spec goldenRuntimeCommandSpec,
) (sqlc.GoldenRuntimeCommand, bool, error) {
	row, err := q.GetGoldenRuntimeCommand(ctx, spec.CommandID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.GoldenRuntimeCommand{}, false, nil
	}
	if err != nil {
		return sqlc.GoldenRuntimeCommand{}, false, goldenRuntimeReadError("load Golden command replay", err)
	}
	if !goldenRuntimeCommandMatches(row, spec) {
		return sqlc.GoldenRuntimeCommand{}, false, &usecase.GoldenCommandReuseConflictError{CommandID: spec.CommandID}
	}
	return row, true, nil
}

// goldenRuntimeRecoverySpec derives server-owned command identity only from
// persisted attempt facts. It intentionally excludes the wall clock and the
// current runtime revision so a worker restart addresses the same boundary.
func goldenRuntimeRecoverySpec(
	attempt sqlc.ListGoldenRuntimeRecoveryAttemptsRow,
	boundary string,
	headerRevision int64,
) goldenRuntimeCommandSpec {
	return goldenRuntimeCommandSpec{
		CommandID:    goldenRuntimeRecoveryCommandID(attempt, boundary),
		TournamentID: attempt.TournamentID, RosterID: attempt.RosterID,
		ActorKind: "server", Scope: "recovery", Kind: boundary,
		AttemptID: attempt.AttemptID, ExpectedRuntimeRevision: headerRevision,
		ExpectedReadyWindowID: attempt.ReadyWindowID,
		Payload: struct {
			Boundary            string    `json:"boundary"`
			GroupRevisionID     uuid.UUID `json:"group_revision_id"`
			EdgePosition        int16     `json:"edge_position"`
			ReadyWindowDeadline string    `json:"ready_window_deadline,omitempty"`
			StartedAt           string    `json:"started_at,omitempty"`
			Deadline            string    `json:"deadline,omitempty"`
		}{
			Boundary: boundary, GroupRevisionID: attempt.GroupRevisionID,
			EdgePosition:        attempt.EdgePosition,
			ReadyWindowDeadline: goldenRuntimeRecoveryTimestamp(attempt.ReadyWindowDeadline),
			StartedAt:           goldenRuntimeRecoveryTimestamp(attempt.StartedAt),
			Deadline:            goldenRuntimeRecoveryTimestamp(attempt.Deadline),
		},
	}
}

func goldenRuntimeRecoveryCommandID(
	attempt sqlc.ListGoldenRuntimeRecoveryAttemptsRow,
	boundary string,
) uuid.UUID {
	seed := fmt.Sprintf(
		"golden-runtime-recovery:v1|%s|%s|%s|%s|%s|%d|%s|%s|%s|%s|%s",
		attempt.TournamentID, attempt.RosterID, attempt.GroupRevisionID,
		attempt.AttemptID, attempt.ReadyWindowID, attempt.EdgePosition, boundary,
		goldenRuntimeRecoveryTimestamp(attempt.ReadyWindowDeadline),
		goldenRuntimeRecoveryTimestamp(attempt.StartedAt),
		goldenRuntimeRecoveryTimestamp(attempt.Deadline),
		attempt.State,
	)
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(seed))
}

func goldenRuntimeRecoveryTimestamp(value pgtype.Timestamptz) string {
	if !value.Valid {
		return ""
	}
	return value.Time.UTC().Format(time.RFC3339Nano)
}

// Recovery retries normally observe the post-transition state and therefore
// classify as a no-op. This helper also supports a retry at the same boundary
// before the caller has refreshed its head, while retaining the persisted
// expected revision in the receipt as the source of truth.
func goldenRuntimeRecoveryReplay(
	ctx context.Context,
	q *sqlc.Queries,
	spec goldenRuntimeCommandSpec,
) (sqlc.GoldenRuntimeCommand, bool, error) {
	row, err := q.GetGoldenRuntimeCommand(ctx, spec.CommandID)
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlc.GoldenRuntimeCommand{}, false, nil
	}
	if err != nil {
		return sqlc.GoldenRuntimeCommand{}, false, goldenRuntimeReadError("load Golden recovery replay", err)
	}
	persisted := spec
	persisted.ExpectedRuntimeRevision = row.ExpectedRuntimeRevision
	if !goldenRuntimeCommandMatches(row, persisted) {
		return sqlc.GoldenRuntimeCommand{}, false, &usecase.GoldenCommandReuseConflictError{CommandID: spec.CommandID}
	}
	return row, true, nil
}

func goldenRuntimeRecoveryResultPayload(
	attempt sqlc.ListGoldenRuntimeRecoveryAttemptsRow,
	boundary, resultingState string,
	resultingRevision int64,
) ([]byte, error) {
	return json.Marshal(map[string]any{
		"schema":                     "golden-runtime-recovery-result-v1",
		"boundary":                   boundary,
		"attempt_id":                 attempt.AttemptID,
		"group_revision_id":          attempt.GroupRevisionID,
		"resulting_state":            resultingState,
		"resulting_runtime_revision": resultingRevision,
	})
}

func goldenRuntimeAuthorityConflict(
	expectedRevision, currentRevision int64,
	expectedWindow, currentWindow uuid.UUID,
) error {
	if expectedRevision != currentRevision ||
		(expectedWindow != uuid.Nil && expectedWindow != currentWindow) {
		return &usecase.GoldenAuthorityConflictError{
			ExpectedRevision: expectedRevision,
			CurrentRevision:  currentRevision,
			ExpectedWindow:   expectedWindow,
			CurrentWindow:    currentWindow,
		}
	}
	return nil
}

func goldenRuntimeAuthorityConflictForTarget(
	expectedRevision, currentRevision int64,
	expectedWindow, currentWindow uuid.UUID,
	expectedAttempt, currentAttempt uuid.UUID,
) error {
	if expectedAttempt != uuid.Nil && expectedAttempt != currentAttempt {
		return &usecase.GoldenAuthorityConflictError{
			ExpectedRevision: expectedRevision,
			CurrentRevision:  currentRevision,
			ExpectedWindow:   expectedWindow,
			CurrentWindow:    currentWindow,
			ExpectedAttempt:  expectedAttempt,
			CurrentAttempt:   currentAttempt,
		}
	}
	if err := goldenRuntimeAuthorityConflict(expectedRevision, currentRevision, expectedWindow, currentWindow); err != nil {
		authorityErr := &usecase.GoldenAuthorityConflictError{}
		if errors.As(err, &authorityErr) {
			authorityErr.ExpectedAttempt = expectedAttempt
			authorityErr.CurrentAttempt = currentAttempt
		}
		return err
	}
	return nil
}

func goldenRuntimeAuditPayload(spec goldenRuntimeCommandSpec, resultingRevision int64) ([]byte, error) {
	payload := map[string]any{
		"schema":                     "golden-runtime-audit-v1",
		"command_id":                 spec.CommandID,
		"command_scope":              spec.Scope,
		"command_kind":               spec.Kind,
		"expected_runtime_revision":  spec.ExpectedRuntimeRevision,
		"resulting_runtime_revision": resultingRevision,
	}
	if spec.AttemptID != uuid.Nil {
		payload["attempt_id"] = spec.AttemptID
	}
	if spec.ParticipantID != uuid.Nil {
		payload["participant_id"] = spec.ParticipantID
	}
	if spec.ExpectedReadyWindowID != uuid.Nil {
		payload["expected_ready_window_id"] = spec.ExpectedReadyWindowID
	}
	return json.Marshal(payload)
}

func goldenRuntimeEventPayload(spec goldenRuntimeCommandSpec, resultingRevision int64) ([]byte, error) {
	return json.Marshal(map[string]any{
		"schema":           "golden-runtime-event-v1",
		"command_id":       spec.CommandID,
		"command_kind":     spec.Kind,
		"runtime_revision": resultingRevision,
		"source":           "golden-runtime",
	})
}

func goldenRuntimeResultPayload(value any) ([]byte, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal Golden command result: %w", err)
	}
	return payload, nil
}

func goldenRuntimeDecodeResult[T any](payload []byte) (T, error) {
	var value T
	if err := json.Unmarshal(payload, &value); err != nil {
		return value, fmt.Errorf("decode Golden command result: %w", err)
	}
	return value, nil
}

func (repository *GoldenRuntimePostgres) appendGoldenRuntimeEvidence(
	ctx context.Context,
	q *sqlc.Queries,
	spec goldenRuntimeCommandSpec,
	head sqlc.GoldenRuntimeHead,
	resultKind string,
	resultPayload []byte,
	now time.Time,
) error {
	digest := spec.digest()
	if _, err := q.CreateGoldenRuntimeCommand(ctx, sqlc.CreateGoldenRuntimeCommandParams{
		CommandID: spec.CommandID, TournamentID: spec.TournamentID, RosterID: spec.RosterID,
		ActorKind: spec.ActorKind, ActorID: goldenRuntimeNullUUID(spec.ActorID),
		CommandScope: spec.Scope, CommandKind: spec.Kind,
		AttemptID: goldenRuntimeNullUUID(spec.AttemptID), ParticipantID: goldenRuntimeNullUUID(spec.ParticipantID),
		ExpectedRuntimeRevision: spec.ExpectedRuntimeRevision,
		ExpectedReadyWindowID:   goldenRuntimeNullUUID(spec.ExpectedReadyWindowID),
		CommandDigest:           digest[:], ResultingRuntimeRevision: head.Revision,
		ResultKind: resultKind, ResultPayload: resultPayload,
		OccurredAt: tstz(now), CreatedAt: tstz(now),
	}); err != nil {
		return goldenRuntimeWriteError("record Golden command", err)
	}
	auditPayload, err := goldenRuntimeAuditPayload(spec, head.Revision)
	if err != nil {
		return err
	}
	if _, err = q.CreateGoldenRuntimeAuditEvent(ctx, sqlc.CreateGoldenRuntimeAuditEventParams{
		ID: uuid.New(), TournamentID: spec.TournamentID, RosterID: spec.RosterID,
		ActorKind: spec.ActorKind, ActorID: goldenRuntimeNullUUID(spec.ActorID),
		Action: "golden.runtime." + spec.Kind, Payload: auditPayload,
		OccurredAt: tstz(now), CreatedAt: tstz(now),
	}); err != nil {
		return goldenRuntimeWriteError("record Golden audit evidence", err)
	}
	sequence, err := q.AllocateGoldenRuntimeOutboxSequence(ctx, sqlc.AllocateGoldenRuntimeOutboxSequenceParams{
		TournamentID: spec.TournamentID, UpdatedAt: tstz(now),
	})
	if err != nil {
		return goldenRuntimeWriteError("allocate Golden outbox sequence", err)
	}
	ordinal, err := q.AllocateGoldenRuntimeProjectionOrdinal(ctx, sqlc.AllocateGoldenRuntimeProjectionOrdinalParams{
		ProjectionRevisionID: head.SourceProjectionRevisionID, TournamentID: spec.TournamentID,
		RosterID: head.RosterID, UpdatedAt: tstz(now),
	})
	if err != nil {
		return goldenRuntimeWriteError("allocate Golden outbox ordinal", err)
	}
	if ordinal < 1 || ordinal > math.MaxInt16 {
		return domain.ErrConflict
	}
	projectionOrdinal := int16(ordinal)
	eventPayload, err := goldenRuntimeEventPayload(spec, head.Revision)
	if err != nil {
		return err
	}
	event, err := q.CreateGoldenRuntimeOutboxEvent(ctx, sqlc.CreateGoldenRuntimeOutboxEventParams{
		ID: uuid.New(), TournamentID: spec.TournamentID, RosterID: head.RosterID,
		ProjectionRevisionID: head.SourceProjectionRevisionID,
		ProjectionRevision:   head.SourceProjectionRevision, Sequence: int64(sequence),
		ProjectionOrdinal: projectionOrdinal, CommandID: spec.CommandID,
		Payload: eventPayload, CreatedAt: tstz(now),
	})
	if err != nil {
		return goldenRuntimeWriteError("record Golden outbox event", err)
	}
	if _, err = q.CreateGoldenRuntimeOutboxSource(ctx, sqlc.CreateGoldenRuntimeOutboxSourceParams{
		OutboxEventID: event.ID, TournamentID: spec.TournamentID, RosterID: head.RosterID,
		CommandID: spec.CommandID, RuntimeRevision: head.Revision,
		ProjectionRevisionID: head.SourceProjectionRevisionID,
		ProjectionRevision:   head.SourceProjectionRevision,
		ProjectionOrdinal:    projectionOrdinal, CreatedAt: tstz(now),
	}); err != nil {
		return goldenRuntimeWriteError("bind Golden outbox source", err)
	}
	return nil
}

func goldenRuntimeCommandActorKind(operator bool) string {
	if operator {
		return "operator"
	}
	return "participant"
}
