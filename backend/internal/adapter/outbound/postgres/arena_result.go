package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	arenaSubmissionStatusAccepted = "accepted"
	arenaSubmissionStatusRejected = "rejected"
	arenaResultActorServer        = "server"
	arenaResultActorOperator      = "operator"
	arenaResultOutboxTopic        = "arena.result.committed"
)

var ErrArenaResultNotFound = errors.New("arena result repository: result not found")

type ArenaResultPostgres struct {
	tx *TxManager
}

type ArenaResultScope struct {
	TournamentID uuid.UUID
	RosterID     uuid.UUID
	SeriesID     uuid.UUID
	AttemptID    uuid.UUID
}

type ArenaSubmissionInput struct {
	ID                      uuid.UUID
	Scope                   ArenaResultScope
	AssignmentID            uuid.UUID
	ParticipantID           uuid.UUID
	ServerSequence          int64
	IdempotencyKey          uuid.UUID
	Status                  string
	DecisionReason          string
	PayloadDigest           [32]byte
	SubmittedAt             time.Time
	ReceivedAt              time.Time
	CreatedAt               time.Time
	ExpectedAttemptRevision int64
	ExpectedAttemptState    domain.ArenaGameState
}

type ArenaSubmissionRecord struct {
	ID             uuid.UUID
	Scope          ArenaResultScope
	AssignmentID   uuid.UUID
	ParticipantID  uuid.UUID
	ServerSequence int64
	IdempotencyKey uuid.UUID
	Status         string
	DecisionReason string
	PayloadDigest  [32]byte
	SubmittedAt    time.Time
	ReceivedAt     time.Time
	CreatedAt      time.Time
}

type ArenaResultSettlementIDs struct {
	CommitID                  uuid.UUID
	ResultEventID             uuid.UUID
	ResultEventIdempotencyKey uuid.UUID
	GameResultRevisionID      uuid.UUID
	SeriesScoreRevisionID     uuid.UUID
	SeriesResultRevisionID    uuid.UUID
	AuditEventID              uuid.UUID
	OutboxEventID             uuid.UUID
	OutboxIdempotencyKey      uuid.UUID
	ProjectionEvidenceID      uuid.UUID
	CommitIdempotencyKey      uuid.UUID
}

type ArenaResultSettlementInput struct {
	IDs                     ArenaResultSettlementIDs
	Scope                   ArenaResultScope
	SubmissionEventID       uuid.UUID
	ResultServerSequence    int64
	GameState               domain.ArenaGameState
	GameReason              domain.ArenaGameResultReason
	GameWinnerID            *uuid.UUID
	Score                   domain.ArenaSeriesScore
	NextSeriesState         domain.ArenaSeriesState
	SeriesResultReason      string
	SeriesWinnerID          *uuid.UUID
	ActorKind               string
	ActorID                 *uuid.UUID
	ProjectionArtifactKinds []domain.ArenaArtifactKind
	ProjectionPayloadDigest [32]byte
	SettledAt               time.Time
	ExpectedAttemptRevision int64
	ExpectedAttemptState    domain.ArenaGameState
	ExpectedSeriesRevision  int64
	ExpectedSeriesState     domain.ArenaSeriesState
}

type ArenaResultCommitRecord struct {
	Commit             sqlc.ArenaResultCommit
	Event              sqlc.ArenaResultEvent
	GameRevision       sqlc.ArenaOfficialResultRevision
	ScoreRevision      sqlc.ArenaSeriesScoreRevision
	SeriesRevision     *sqlc.ArenaOfficialResultRevision
	Audit              sqlc.ArenaAuditEvent
	Outbox             sqlc.ArenaOutboxEvent
	ProjectionEvidence sqlc.ArenaResultProjectionEvidence
}

type ArenaResultHistoryRecord struct {
	CommitID               uuid.UUID
	Scope                  ArenaResultScope
	ResultEventID          uuid.UUID
	ServerSequence         int64
	GameState              domain.ArenaGameState
	GameReason             domain.ArenaGameResultReason
	WinnerID               *uuid.UUID
	GameRevisionID         uuid.UUID
	GameRevisionNumber     int64
	ScoreRevisionID        uuid.UUID
	ScoreRevisionNumber    int64
	Score                  domain.ArenaSeriesScore
	SeriesResultRevisionID *uuid.UUID
	CreatedAt              time.Time
}

func NewArenaResultPostgres(tx *TxManager) *ArenaResultPostgres {
	return &ArenaResultPostgres{tx: tx}
}

func (r *ArenaResultPostgres) RecordSubmission(
	ctx context.Context,
	in ArenaSubmissionInput,
) (*ArenaSubmissionRecord, bool, error) {
	if r == nil || r.tx == nil || !validArenaSubmissionInput(in) {
		return nil, false, domain.ErrValidation
	}

	var record *ArenaSubmissionRecord
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		attempt, err := querier.LockArenaResultAttempt(txCtx, arenaResultAttemptParams(in.Scope))
		if err != nil {
			return arenaResultLookupError("RecordSubmission - lock attempt", err)
		}

		existing, err := querier.GetArenaSubmissionEventByIdempotencyKey(txCtx, in.IdempotencyKey)
		if err == nil {
			if !arenaSubmissionMatchesInput(existing, in) {
				return domain.ErrConflict
			}
			record = arenaSubmissionRecord(existing)
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("ArenaResultPostgres - RecordSubmission - lookup idempotency: %w", err)
		}
		if attempt.Revision != in.ExpectedAttemptRevision || attempt.State != string(in.ExpectedAttemptState) ||
			domain.ArenaGameState(attempt.State).IsTerminal() {
			return domain.ErrConflict
		}

		row, err := querier.CreateArenaSubmissionEvent(txCtx, sqlc.CreateArenaSubmissionEventParams{
			ID:             in.ID,
			TournamentID:   in.Scope.TournamentID,
			RosterID:       in.Scope.RosterID,
			SeriesID:       in.Scope.SeriesID,
			AttemptID:      in.Scope.AttemptID,
			AssignmentID:   in.AssignmentID,
			ParticipantID:  in.ParticipantID,
			ServerSequence: in.ServerSequence,
			IdempotencyKey: in.IdempotencyKey,
			Status:         in.Status,
			DecisionReason: optionalTrimmedString(in.DecisionReason),
			PayloadDigest:  append([]byte(nil), in.PayloadDigest[:]...),
			SubmittedAt:    tstz(in.SubmittedAt),
			ReceivedAt:     tstz(in.ReceivedAt),
			CreatedAt:      tstz(in.CreatedAt),
		})
		if err != nil {
			return mapArenaRepositoryWriteError("ArenaResultPostgres - RecordSubmission", err)
		}
		record = arenaSubmissionRecord(row)
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return record, changed, nil
}

func (r *ArenaResultPostgres) Settle(
	ctx context.Context,
	in ArenaResultSettlementInput,
) (*ArenaResultCommitRecord, bool, error) {
	if r == nil || r.tx == nil || !validArenaResultSettlementInput(in) {
		return nil, false, domain.ErrValidation
	}

	var record *ArenaResultCommitRecord
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		attempt, err := querier.LockArenaResultAttempt(txCtx, arenaResultAttemptParams(in.Scope))
		if err != nil {
			return arenaResultLookupError("Settle - lock attempt", err)
		}

		if domain.ArenaGameState(attempt.State).IsTerminal() {
			current, currentErr := querier.GetCurrentArenaResultCommitForAttempt(
				txCtx,
				sqlc.GetCurrentArenaResultCommitForAttemptParams{
					AttemptID: in.Scope.AttemptID, TournamentID: in.Scope.TournamentID,
					RosterID: in.Scope.RosterID, SeriesID: in.Scope.SeriesID,
				},
			)
			if currentErr != nil {
				return arenaResultLookupError("Settle - load current commit", currentErr)
			}
			record, currentErr = loadArenaResultCommit(txCtx, querier, current)
			return currentErr
		}
		if attempt.Revision != in.ExpectedAttemptRevision || attempt.State != string(in.ExpectedAttemptState) {
			return domain.ErrConflict
		}

		series, err := querier.LockArenaResultSeries(txCtx, sqlc.LockArenaResultSeriesParams{
			SeriesID: in.Scope.SeriesID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		})
		if err != nil {
			return arenaResultLookupError("Settle - lock series", err)
		}
		if err := validateArenaSettlementAgainstRows(in, attempt, series); err != nil {
			return err
		}

		if err := createArenaResultEvidence(txCtx, querier, in, series); err != nil {
			return err
		}
		commit, err := querier.GetArenaResultCommitByID(txCtx, in.IDs.CommitID)
		if err != nil {
			return fmt.Errorf("ArenaResultPostgres - Settle - reload commit: %w", err)
		}
		record, err = loadArenaResultCommit(txCtx, querier, commit)
		if err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return record, changed, nil
}

func (r *ArenaResultPostgres) Current(
	ctx context.Context,
	scope ArenaResultScope,
) (*ArenaResultCommitRecord, error) {
	if r == nil || r.tx == nil || !validArenaResultScope(scope) {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	commit, err := querier.GetCurrentArenaResultCommitForAttempt(
		ctx,
		sqlc.GetCurrentArenaResultCommitForAttemptParams{
			AttemptID: scope.AttemptID, TournamentID: scope.TournamentID,
			RosterID: scope.RosterID, SeriesID: scope.SeriesID,
		},
	)
	if err != nil {
		return nil, arenaResultLookupError("Current", err)
	}
	return loadArenaResultCommit(ctx, querier, commit)
}

func (r *ArenaResultPostgres) History(
	ctx context.Context,
	scope ArenaResultScope,
) ([]ArenaResultHistoryRecord, error) {
	if r == nil || r.tx == nil || !validArenaResultScope(scope) {
		return nil, domain.ErrValidation
	}
	rows, err := r.tx.Querier(ctx).ListArenaResultHistory(ctx, sqlc.ListArenaResultHistoryParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		SeriesID: scope.SeriesID, AttemptID: scope.AttemptID,
	})
	if err != nil {
		return nil, fmt.Errorf("ArenaResultPostgres - History: %w", err)
	}
	history := make([]ArenaResultHistoryRecord, 0, len(rows))
	for _, row := range rows {
		history = append(history, arenaResultHistoryRecord(row))
	}
	return history, nil
}

func createArenaResultEvidence(
	ctx context.Context,
	querier *sqlc.Queries,
	in ArenaResultSettlementInput,
	series sqlc.LockArenaResultSeriesRow,
) error {
	resultEvent, err := querier.CreateArenaResultEvent(ctx, sqlc.CreateArenaResultEventParams{
		ID: in.IDs.ResultEventID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, AttemptID: in.Scope.AttemptID,
		SubmissionEventID: nullableUUIDValue(in.SubmissionEventID), ServerSequence: in.ResultServerSequence,
		IdempotencyKey: in.IDs.ResultEventIdempotencyKey, ResultState: string(in.GameState),
		ResultReason: string(in.GameReason), WinnerID: nullableUUID(in.GameWinnerID),
		OccurredAt: tstz(in.SettledAt), CreatedAt: tstz(in.SettledAt),
	})
	if err != nil {
		return mapArenaRepositoryWriteError("ArenaResultPostgres - Settle - create result event", err)
	}

	_, err = querier.CreateArenaOfficialResultRevision(ctx, sqlc.CreateArenaOfficialResultRevisionParams{
		ID: in.IDs.GameResultRevisionID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		EntityKind: "game_attempt", EntityID: in.Scope.AttemptID, SeriesID: in.Scope.SeriesID,
		GameAttemptID: nullableUUIDValue(in.Scope.AttemptID), ResultEventID: resultEvent.ID,
		RevisionNumber: 1, ResultState: string(in.GameState), ResultReason: string(in.GameReason),
		WinnerID: nullableUUID(in.GameWinnerID), CreatedAt: tstz(in.SettledAt),
	})
	if err != nil {
		return mapArenaRepositoryWriteError("ArenaResultPostgres - Settle - create Game revision", err)
	}

	_, err = querier.CreateArenaSeriesScoreRevision(ctx, sqlc.CreateArenaSeriesScoreRevisionParams{
		ID: in.IDs.SeriesScoreRevisionID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, ResultEventID: nullableUUIDValue(resultEvent.ID),
		PreviousRevisionID:    nullableUUIDValue(series.ScoreHeadRevisionID),
		RevisionNumber:        series.ScoreHeadRevision + 1,
		FirstParticipantWins:  int16(in.Score.FirstParticipantWins),  //nolint:gosec // Arena score validation bounds BO1 and BO3 scores to 0..2.
		SecondParticipantWins: int16(in.Score.SecondParticipantWins), //nolint:gosec // Arena score validation bounds BO1 and BO3 scores to 0..2.
		CreatedAt:             tstz(in.SettledAt),
	})
	if err != nil {
		return mapArenaRepositoryWriteError("ArenaResultPostgres - Settle - create score revision", err)
	}

	seriesResultID := uuid.NullUUID{}
	if in.IDs.SeriesResultRevisionID != uuid.Nil {
		seriesResultID = nullableUUIDValue(in.IDs.SeriesResultRevisionID)
		_, err = querier.CreateArenaOfficialResultRevision(ctx, sqlc.CreateArenaOfficialResultRevisionParams{
			ID: in.IDs.SeriesResultRevisionID, TournamentID: in.Scope.TournamentID,
			RosterID: in.Scope.RosterID, EntityKind: "series", EntityID: in.Scope.SeriesID,
			SeriesID: in.Scope.SeriesID, ResultEventID: resultEvent.ID, RevisionNumber: 1,
			ResultState: string(in.NextSeriesState), ResultReason: in.SeriesResultReason,
			WinnerID: nullableUUID(in.SeriesWinnerID), CreatedAt: tstz(in.SettledAt),
		})
		if err != nil {
			return mapArenaRepositoryWriteError("ArenaResultPostgres - Settle - create Series revision", err)
		}
	}

	if err := createArenaResultSideEvidence(ctx, querier, in, resultEvent.ID, seriesResultID); err != nil {
		return err
	}
	return advanceArenaResultHeads(ctx, querier, in, series, seriesResultID)
}

func createArenaResultSideEvidence(
	ctx context.Context,
	querier *sqlc.Queries,
	in ArenaResultSettlementInput,
	resultEventID uuid.UUID,
	seriesResultID uuid.NullUUID,
) error {
	auditPayload := mustJSON(map[string]any{
		"attempt_id": in.Scope.AttemptID.String(),
		"reason":     string(in.GameReason),
		"state":      string(in.GameState),
	})
	outboxPayload := mustJSON(map[string]any{
		"result_event_id": resultEventID.String(),
		"series_id":       in.Scope.SeriesID.String(),
		"tournament_id":   in.Scope.TournamentID.String(),
	})
	artifactKinds := make([]string, len(in.ProjectionArtifactKinds))
	for i, kind := range in.ProjectionArtifactKinds {
		artifactKinds[i] = string(kind)
	}

	_, err := querier.CreateArenaAuditEvent(ctx, sqlc.CreateArenaAuditEventParams{
		ID: in.IDs.AuditEventID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, ResultEventID: resultEventID, ActorKind: in.ActorKind,
		ActorID: nullableUUID(in.ActorID), Action: arenaResultOutboxTopic,
		Payload: auditPayload, OccurredAt: tstz(in.SettledAt), CreatedAt: tstz(in.SettledAt),
	})
	if err != nil {
		return mapArenaRepositoryWriteError("ArenaResultPostgres - Settle - create audit", err)
	}
	_, err = querier.CreateArenaResultProjectionEvidence(ctx, sqlc.CreateArenaResultProjectionEvidenceParams{
		ID: in.IDs.ProjectionEvidenceID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, ResultEventID: resultEventID,
		ArtifactKinds: mustJSON(artifactKinds), PayloadDigest: append([]byte(nil), in.ProjectionPayloadDigest[:]...),
		CreatedAt: tstz(in.SettledAt),
	})
	if err != nil {
		return mapArenaRepositoryWriteError("ArenaResultPostgres - Settle - create projection evidence", err)
	}
	_, err = querier.CreateArenaOutboxEvent(ctx, sqlc.CreateArenaOutboxEventParams{
		ID: in.IDs.OutboxEventID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, ResultEventID: resultEventID,
		IdempotencyKey: in.IDs.OutboxIdempotencyKey, Topic: arenaResultOutboxTopic,
		Payload: outboxPayload, CreatedAt: tstz(in.SettledAt),
	})
	if err != nil {
		return mapArenaRepositoryWriteError("ArenaResultPostgres - Settle - create outbox", err)
	}
	_, err = querier.CreateArenaResultCommit(ctx, sqlc.CreateArenaResultCommitParams{
		ID: in.IDs.CommitID, TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		SeriesID: in.Scope.SeriesID, AttemptID: in.Scope.AttemptID, ResultEventID: resultEventID,
		GameResultRevisionID:  in.IDs.GameResultRevisionID,
		SeriesScoreRevisionID: in.IDs.SeriesScoreRevisionID, SeriesResultRevisionID: seriesResultID,
		AuditEventID: in.IDs.AuditEventID, OutboxEventID: in.IDs.OutboxEventID,
		ProjectionEvidenceID: in.IDs.ProjectionEvidenceID,
		IdempotencyKey:       in.IDs.CommitIdempotencyKey, CreatedAt: tstz(in.SettledAt),
	})
	if err != nil {
		return mapArenaRepositoryWriteError("ArenaResultPostgres - Settle - create commit", err)
	}
	return nil
}

func advanceArenaResultHeads(
	ctx context.Context,
	querier *sqlc.Queries,
	in ArenaResultSettlementInput,
	series sqlc.LockArenaResultSeriesRow,
	seriesResultID uuid.NullUUID,
) error {
	_, err := querier.CreateArenaOfficialResultHead(ctx, sqlc.CreateArenaOfficialResultHeadParams{
		EntityKind: "game_attempt", EntityID: in.Scope.AttemptID, SeriesID: in.Scope.SeriesID,
		RosterID: in.Scope.RosterID, GameAttemptID: nullableUUIDValue(in.Scope.AttemptID),
		CurrentRevisionID: in.IDs.GameResultRevisionID, UpdatedAt: tstz(in.SettledAt),
	})
	if err != nil {
		return mapArenaRepositoryWriteError("ArenaResultPostgres - Settle - create Game head", err)
	}
	if seriesResultID.Valid {
		_, err = querier.CreateArenaOfficialResultHead(ctx, sqlc.CreateArenaOfficialResultHeadParams{
			EntityKind: "series", EntityID: in.Scope.SeriesID, SeriesID: in.Scope.SeriesID,
			RosterID: in.Scope.RosterID, CurrentRevisionID: seriesResultID.UUID,
			UpdatedAt: tstz(in.SettledAt),
		})
		if err != nil {
			return mapArenaRepositoryWriteError("ArenaResultPostgres - Settle - create Series head", err)
		}
	}
	_, err = querier.AdvanceArenaSeriesScoreHeadCAS(ctx, sqlc.AdvanceArenaSeriesScoreHeadCASParams{
		CurrentRevisionID: in.IDs.SeriesScoreRevisionID, UpdatedAt: tstz(in.SettledAt),
		SeriesID: in.Scope.SeriesID, RosterID: in.Scope.RosterID,
		ExpectedRevisionID: series.ScoreHeadRevisionID, ExpectedRevision: series.ScoreHeadRevision,
	})
	if err != nil {
		return arenaResultCASWriteError("advance score head", err)
	}
	_, err = querier.SettleArenaGameAttemptCAS(ctx, sqlc.SettleArenaGameAttemptCASParams{
		ResultState: string(in.GameState), ResultReason: optionalTrimmedString(string(in.GameReason)),
		WinnerID: nullableUUID(in.GameWinnerID), ResultRevisionID: nullableUUIDValue(in.IDs.GameResultRevisionID),
		SettledAt: tstz(in.SettledAt), AttemptID: in.Scope.AttemptID, SeriesID: in.Scope.SeriesID,
		RosterID: in.Scope.RosterID, TournamentID: in.Scope.TournamentID,
		ExpectedRevision: in.ExpectedAttemptRevision, ExpectedState: string(in.ExpectedAttemptState),
	})
	if err != nil {
		return arenaResultCASWriteError("settle Game", err)
	}
	_, err = querier.SettleArenaSeriesCAS(ctx, sqlc.SettleArenaSeriesCASParams{
		NextState:             string(in.NextSeriesState),
		FirstParticipantWins:  int16(in.Score.FirstParticipantWins),  //nolint:gosec // Arena score validation bounds BO1 and BO3 scores to 0..2.
		SecondParticipantWins: int16(in.Score.SecondParticipantWins), //nolint:gosec // Arena score validation bounds BO1 and BO3 scores to 0..2.
		WinnerID:              nullableUUID(in.SeriesWinnerID),
		ScoreRevisionID:       nullableUUIDValue(in.IDs.SeriesScoreRevisionID), ResultRevisionID: seriesResultID,
		SettledAt: tstz(in.SettledAt), SeriesID: in.Scope.SeriesID,
		TournamentID: in.Scope.TournamentID, RosterID: in.Scope.RosterID,
		ExpectedRevision: in.ExpectedSeriesRevision, ExpectedState: string(in.ExpectedSeriesState),
		ExpectedScoreRevisionID: nullableUUIDValue(series.ScoreHeadRevisionID),
	})
	if err != nil {
		return arenaResultCASWriteError("settle Series", err)
	}
	return nil
}

func loadArenaResultCommit(
	ctx context.Context,
	querier *sqlc.Queries,
	commit sqlc.ArenaResultCommit,
) (*ArenaResultCommitRecord, error) {
	event, err := querier.GetArenaResultEventByID(ctx, commit.ResultEventID)
	if err != nil {
		return nil, fmt.Errorf("ArenaResultPostgres - load commit - event: %w", err)
	}
	gameRevision, err := querier.GetArenaOfficialResultRevisionByID(ctx, commit.GameResultRevisionID)
	if err != nil {
		return nil, fmt.Errorf("ArenaResultPostgres - load commit - Game revision: %w", err)
	}
	scoreRevision, err := querier.GetArenaSeriesScoreRevisionByID(ctx, commit.SeriesScoreRevisionID)
	if err != nil {
		return nil, fmt.Errorf("ArenaResultPostgres - load commit - score revision: %w", err)
	}
	var seriesRevision *sqlc.ArenaOfficialResultRevision
	if commit.SeriesResultRevisionID.Valid {
		row, loadErr := querier.GetArenaOfficialResultRevisionByID(ctx, commit.SeriesResultRevisionID.UUID)
		if loadErr != nil {
			return nil, fmt.Errorf("ArenaResultPostgres - load commit - Series revision: %w", loadErr)
		}
		seriesRevision = &row
	}
	audit, err := querier.GetArenaAuditEventByID(ctx, commit.AuditEventID)
	if err != nil {
		return nil, fmt.Errorf("ArenaResultPostgres - load commit - audit: %w", err)
	}
	outbox, err := querier.GetArenaOutboxEventByID(ctx, commit.OutboxEventID)
	if err != nil {
		return nil, fmt.Errorf("ArenaResultPostgres - load commit - outbox: %w", err)
	}
	projection, err := querier.GetArenaResultProjectionEvidenceByID(ctx, commit.ProjectionEvidenceID)
	if err != nil {
		return nil, fmt.Errorf("ArenaResultPostgres - load commit - projection: %w", err)
	}
	return &ArenaResultCommitRecord{
		Commit: commit, Event: event, GameRevision: gameRevision, ScoreRevision: scoreRevision,
		SeriesRevision: seriesRevision, Audit: audit, Outbox: outbox, ProjectionEvidence: projection,
	}, nil
}

func validateArenaSettlementAgainstRows(
	in ArenaResultSettlementInput,
	attempt sqlc.ArenaGameAttempt,
	series sqlc.LockArenaResultSeriesRow,
) error {
	if series.Revision != in.ExpectedSeriesRevision || series.State != string(in.ExpectedSeriesState) ||
		!series.CurrentScoreRevisionID.Valid || series.CurrentScoreRevisionID.UUID != series.ScoreHeadRevisionID {
		return domain.ErrConflict
	}
	if err := in.Score.Validate(domain.ArenaSeriesFormat(series.Format)); err != nil {
		return domain.WrapError(err, domain.ErrValidation)
	}
	game := domain.ArenaGame{
		ID: attempt.ID, SlotID: attempt.SlotID, AttemptNo: int(attempt.AttemptNumber),
		State: in.GameState, ResultReason: in.GameReason, WinnerID: in.GameWinnerID,
	}
	resultRevisionID := domain.ArenaOfficialResultRevisionID(in.IDs.GameResultRevisionID)
	game.ResultRevisionID = &resultRevisionID
	if err := game.Validate(); err != nil {
		return domain.WrapError(err, domain.ErrValidation)
	}
	if in.GameWinnerID != nil && *in.GameWinnerID != series.FirstParticipantID &&
		*in.GameWinnerID != series.SecondParticipantID {
		return domain.ErrValidation
	}
	return validateArenaSeriesSettlement(in, series)
}

func validateArenaSeriesSettlement(in ArenaResultSettlementInput, series sqlc.LockArenaResultSeriesRow) error {
	if in.NextSeriesState.IsTerminal() {
		if in.IDs.SeriesResultRevisionID == uuid.Nil || !validArenaSeriesResultReason(in.SeriesResultReason) {
			return domain.ErrValidation
		}
		if in.NextSeriesState == domain.ArenaSeriesStateCompleted {
			expected := in.Score.Winner(series.FirstParticipantID, series.SecondParticipantID, domain.ArenaSeriesFormat(series.Format))
			if expected == nil || in.SeriesWinnerID == nil || *expected != *in.SeriesWinnerID {
				return domain.ErrValidation
			}
		} else if in.SeriesWinnerID != nil && *in.SeriesWinnerID != series.FirstParticipantID &&
			*in.SeriesWinnerID != series.SecondParticipantID {
			return domain.ErrValidation
		}
		return nil
	}
	if in.IDs.SeriesResultRevisionID != uuid.Nil || in.SeriesResultReason != "" || in.SeriesWinnerID != nil {
		return domain.ErrValidation
	}
	return nil
}

func validArenaResultSettlementInput(in ArenaResultSettlementInput) bool {
	if !validArenaResultSettlementIDs(in.IDs) || !validArenaResultSettlementMetadata(in) {
		return false
	}
	if in.GameReason == domain.ArenaGameResultReasonSolved && in.SubmissionEventID == uuid.Nil {
		return false
	}
	if in.ActorKind == arenaResultActorServer {
		return in.ActorID == nil
	}
	return in.ActorKind == arenaResultActorOperator && in.ActorID != nil && *in.ActorID != uuid.Nil
}

func validArenaResultSettlementIDs(value ArenaResultSettlementIDs) bool {
	required := []uuid.UUID{
		value.CommitID, value.ResultEventID, value.ResultEventIdempotencyKey,
		value.GameResultRevisionID, value.SeriesScoreRevisionID, value.AuditEventID,
		value.OutboxEventID, value.OutboxIdempotencyKey, value.ProjectionEvidenceID,
		value.CommitIdempotencyKey,
	}
	for _, id := range required {
		if id == uuid.Nil {
			return false
		}
	}
	return true
}

func validArenaResultSettlementMetadata(in ArenaResultSettlementInput) bool {
	if !validArenaResultScope(in.Scope) || in.ResultServerSequence < 1 || !validServerTime(in.SettledAt) ||
		in.ExpectedAttemptRevision < 1 || in.ExpectedSeriesRevision < 1 ||
		!in.ExpectedAttemptState.IsValid() || in.ExpectedAttemptState.IsTerminal() ||
		!in.ExpectedSeriesState.IsValid() || in.ExpectedSeriesState.IsTerminal() ||
		!in.GameState.IsTerminal() || !in.NextSeriesState.IsValid() ||
		zeroDigest(in.ProjectionPayloadDigest[:]) || !validArenaArtifactKinds(in.ProjectionArtifactKinds) {
		return false
	}
	return true
}

func validArenaSubmissionInput(in ArenaSubmissionInput) bool {
	if !validArenaSubmissionMetadata(in) {
		return false
	}
	switch in.Status {
	case arenaSubmissionStatusAccepted:
		return in.DecisionReason == ""
	case arenaSubmissionStatusRejected:
		return validTrimmedText(in.DecisionReason)
	default:
		return false
	}
}

func validArenaSubmissionMetadata(in ArenaSubmissionInput) bool {
	return validArenaResultScope(in.Scope) && in.ID != uuid.Nil && in.AssignmentID != uuid.Nil &&
		in.ParticipantID != uuid.Nil && in.ServerSequence >= 1 && in.IdempotencyKey != uuid.Nil &&
		in.ExpectedAttemptRevision >= 1 && in.ExpectedAttemptState.IsValid() &&
		!in.ExpectedAttemptState.IsTerminal() && !zeroDigest(in.PayloadDigest[:]) &&
		validServerTime(in.SubmittedAt) && validServerTime(in.ReceivedAt) && validServerTime(in.CreatedAt) &&
		!in.SubmittedAt.After(in.ReceivedAt) && !in.ReceivedAt.After(in.CreatedAt)
}

func validArenaResultScope(scope ArenaResultScope) bool {
	return scope.TournamentID != uuid.Nil && scope.RosterID != uuid.Nil &&
		scope.SeriesID != uuid.Nil && scope.AttemptID != uuid.Nil
}

func validArenaArtifactKinds(kinds []domain.ArenaArtifactKind) bool {
	if len(kinds) == 0 {
		return false
	}
	seen := make(map[domain.ArenaArtifactKind]struct{}, len(kinds))
	for _, kind := range kinds {
		if !kind.IsValid() {
			return false
		}
		if _, exists := seen[kind]; exists {
			return false
		}
		seen[kind] = struct{}{}
	}
	return true
}

func validArenaSeriesResultReason(reason string) bool {
	switch reason {
	case "score_complete", "operator_correction", "series_cancelled", "tournament_cancelled":
		return true
	default:
		return false
	}
}

func arenaResultAttemptParams(scope ArenaResultScope) sqlc.LockArenaResultAttemptParams {
	return sqlc.LockArenaResultAttemptParams{
		AttemptID: scope.AttemptID, SeriesID: scope.SeriesID,
		RosterID: scope.RosterID, TournamentID: scope.TournamentID,
	}
}

func arenaSubmissionMatchesInput(row sqlc.ArenaSubmissionEvent, in ArenaSubmissionInput) bool {
	return row.ID == in.ID && row.TournamentID == in.Scope.TournamentID && row.RosterID == in.Scope.RosterID &&
		row.SeriesID == in.Scope.SeriesID && row.AttemptID == in.Scope.AttemptID &&
		row.AssignmentID == in.AssignmentID && row.ParticipantID == in.ParticipantID &&
		row.ServerSequence == in.ServerSequence && row.Status == in.Status &&
		stringValue(row.DecisionReason) == in.DecisionReason && bytes.Equal(row.PayloadDigest, in.PayloadDigest[:]) &&
		row.SubmittedAt.Time.Equal(in.SubmittedAt) && row.ReceivedAt.Time.Equal(in.ReceivedAt) &&
		row.CreatedAt.Time.Equal(in.CreatedAt)
}

func arenaSubmissionRecord(row sqlc.ArenaSubmissionEvent) *ArenaSubmissionRecord {
	record := &ArenaSubmissionRecord{
		ID: row.ID,
		Scope: ArenaResultScope{
			TournamentID: row.TournamentID, RosterID: row.RosterID,
			SeriesID: row.SeriesID, AttemptID: row.AttemptID,
		},
		AssignmentID: row.AssignmentID, ParticipantID: row.ParticipantID,
		ServerSequence: row.ServerSequence, IdempotencyKey: row.IdempotencyKey,
		Status: row.Status, DecisionReason: stringValue(row.DecisionReason),
		SubmittedAt: row.SubmittedAt.Time, ReceivedAt: row.ReceivedAt.Time, CreatedAt: row.CreatedAt.Time,
	}
	copy(record.PayloadDigest[:], row.PayloadDigest)
	return record
}

func arenaResultHistoryRecord(row sqlc.ListArenaResultHistoryRow) ArenaResultHistoryRecord {
	record := ArenaResultHistoryRecord{
		CommitID: row.CommitID,
		Scope: ArenaResultScope{
			TournamentID: row.TournamentID, RosterID: row.RosterID,
			SeriesID: row.SeriesID, AttemptID: row.AttemptID,
		},
		ResultEventID: row.ResultEventID, ServerSequence: row.ServerSequence,
		GameState: domain.ArenaGameState(row.ResultState), GameReason: domain.ArenaGameResultReason(row.ResultReason),
		GameRevisionID: row.GameRevisionID, GameRevisionNumber: row.GameRevisionNumber,
		ScoreRevisionID: row.ScoreRevisionID, ScoreRevisionNumber: row.ScoreRevisionNumber,
		Score: domain.ArenaSeriesScore{
			FirstParticipantWins:  int(row.FirstParticipantWins),
			SecondParticipantWins: int(row.SecondParticipantWins),
		},
		CreatedAt: row.CreatedAt.Time,
	}
	if row.WinnerID.Valid {
		winnerID := row.WinnerID.UUID
		record.WinnerID = &winnerID
	}
	if row.SeriesResultRevisionID.Valid {
		resultID := row.SeriesResultRevisionID.UUID
		record.SeriesResultRevisionID = &resultID
	}
	return record
}

func arenaResultLookupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrArenaResultNotFound
	}
	return fmt.Errorf("ArenaResultPostgres - %s: %w", operation, err)
}

func arenaResultCASWriteError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return mapArenaRepositoryWriteError("ArenaResultPostgres - Settle - "+operation, err)
}

func optionalTrimmedString(value string) *string {
	if value == "" {
		return nil
	}
	trimmed := strings.TrimSpace(value)
	return &trimmed
}

func zeroDigest(value []byte) bool {
	if len(value) != 32 {
		return true
	}
	return bytes.Equal(value, make([]byte, 32))
}
