package result

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	submissionStatusAccepted = "accepted"
	submissionStatusRejected = "rejected"
	resultActorServer        = "server"
	resultActorOperator      = "operator"
	resultOutboxTopic        = "tournament.result.committed"
)

var ErrResultNotFound = errors.New("result repository: result not found")

// ProjectionFinalizer joins the result publication transaction to the
// tournament progression receipt boundary. The result package keeps this
// callback optional so it does not import the parent adapter.
type ProjectionFinalizer func(
	context.Context,
	*db.TxManager,
	uuid.UUID,
	uuid.UUID,
	uuid.UUID,
	time.Time,
) error

type ResultPostgres struct {
	tx                *db.TxManager
	publishProjection func(context.Context, *db.TxManager, ResultSettlementInput, sqlc.LockResultSourceProjectionRow) error
}

type ResultScope struct {
	TournamentID uuid.UUID
	RosterID     uuid.UUID
	SeriesID     uuid.UUID
	AttemptID    uuid.UUID
}

type SubmissionInput struct {
	ID                      uuid.UUID
	Scope                   ResultScope
	AssignmentID            uuid.UUID
	ParticipantID           uuid.UUID
	IdempotencyKey          uuid.UUID
	Status                  string
	DecisionReason          string
	PayloadDigest           [32]byte
	IntentDigest            [32]byte
	SubmittedAt             time.Time
	ReceivedAt              time.Time
	CreatedAt               time.Time
	ExpectedAttemptRevision int64
	ExpectedAttemptState    domain.GameState
}

type SubmissionRecord struct {
	ID             uuid.UUID
	Scope          ResultScope
	AssignmentID   uuid.UUID
	ParticipantID  uuid.UUID
	ServerSequence int64
	IdempotencyKey uuid.UUID
	Status         string
	DecisionReason string
	PayloadDigest  [32]byte
	IntentDigest   [32]byte
	SubmittedAt    time.Time
	ReceivedAt     time.Time
	CreatedAt      time.Time
}

type ResultSettlementIDs struct {
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

type ResultSettlementInput struct {
	IDs                     ResultSettlementIDs
	Scope                   ResultScope
	ProjectionPublication   ResultProjectionPublication
	SubmissionEventID       uuid.UUID
	GameState               domain.GameState
	GameReason              domain.GameResultReason
	GameWinnerID            *uuid.UUID
	Score                   domain.SeriesScore
	NextSeriesState         domain.SeriesState
	SeriesResultReason      string
	SeriesWinnerID          *uuid.UUID
	ActorKind               string
	ActorID                 *uuid.UUID
	ProjectionArtifactKinds []domain.ArtifactKind
	ProjectionPayloadDigest [32]byte
	SettledAt               time.Time
	ExpectedAttemptRevision int64
	ExpectedAttemptState    domain.GameState
	ExpectedSeriesRevision  int64
	ExpectedSeriesState     domain.SeriesState
}

// ResultProjectionPublication selects the owner of the immutable projection
// revision that is already bound to the result outbox event.
type ResultProjectionPublication uint8

const (
	ResultProjectionPublicationImmediate ResultProjectionPublication = iota
	ResultProjectionPublicationCallerOwned
	resultProjectionPublicationImmediate   = ResultProjectionPublicationImmediate
	resultProjectionPublicationCallerOwned = ResultProjectionPublicationCallerOwned
)

type ResultCommitRecord struct {
	Commit             sqlc.ResultCommit
	Event              sqlc.ResultEvent
	GameRevision       sqlc.OfficialResultRevision
	ScoreRevision      sqlc.SeriesScoreRevision
	SeriesRevision     *sqlc.OfficialResultRevision
	Audit              sqlc.AuditEvent
	Outbox             sqlc.OutboxEvent
	ProjectionEvidence sqlc.ResultProjectionEvidence
}

type ResultHistoryRecord struct {
	CommitID               uuid.UUID
	Scope                  ResultScope
	ResultEventID          uuid.UUID
	ServerSequence         int64
	GameState              domain.GameState
	GameReason             domain.GameResultReason
	WinnerID               *uuid.UUID
	GameRevisionID         uuid.UUID
	GameRevisionNumber     int64
	ScoreRevisionID        uuid.UUID
	ScoreRevisionNumber    int64
	Score                  domain.SeriesScore
	SeriesResultRevisionID *uuid.UUID
	CreatedAt              time.Time
}

func NewResultPostgres(tx *db.TxManager) *ResultPostgres {
	return NewResultPostgresWithFinalizer(tx, nil)
}

func NewResultPostgresWithFinalizer(tx *db.TxManager, finalizer ProjectionFinalizer) *ResultPostgres {
	return &ResultPostgres{
		tx: tx,
		publishProjection: func(
			ctx context.Context,
			tx *db.TxManager,
			in ResultSettlementInput,
			source sqlc.LockResultSourceProjectionRow,
		) error {
			return publishResultProjectionWithFinalizer(ctx, tx, in, source, finalizer)
		},
	}
}

func (r *ResultPostgres) RecordSubmission(
	ctx context.Context,
	in SubmissionInput,
) (*SubmissionRecord, bool, error) {
	if r == nil || r.tx == nil || !validSubmissionInput(in) {
		return nil, false, domain.ErrValidation
	}

	var record *SubmissionRecord
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		if err := lockTournamentResultScope(txCtx, querier, in.Scope.TournamentID, in.Scope.RosterID); err != nil {
			return resultLookupError("RecordSubmission - lock result scope", err)
		}
		attempt, err := querier.LockResultAttempt(txCtx, resultAttemptParams(in.Scope))
		if err != nil {
			return resultLookupError("RecordSubmission - lock attempt", err)
		}

		existing, err := querier.GetResultSubmissionEventByIdempotencyKey(txCtx, in.IdempotencyKey)
		if err == nil {
			if !submissionMatchesInput(existing, in) {
				return domain.ErrConflict
			}
			record = submissionRecord(existing)
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("ResultPostgres - RecordSubmission - lookup idempotency: %w", err)
		}
		if attempt.Revision != in.ExpectedAttemptRevision || attempt.State != string(in.ExpectedAttemptState) ||
			domain.GameState(attempt.State).IsTerminal() {
			return domain.ErrConflict
		}
		sequence, err := querier.AllocateSubmissionEventSequence(txCtx, submissionEventSequenceParams(in.Scope))
		if err != nil {
			return mapRepositoryWriteError("ResultPostgres - RecordSubmission - allocate sequence", err)
		}

		row, err := querier.CreateResultSubmissionEvent(txCtx, sqlc.CreateResultSubmissionEventParams{
			ID:             in.ID,
			TournamentID:   in.Scope.TournamentID,
			RosterID:       in.Scope.RosterID,
			SeriesID:       in.Scope.SeriesID,
			AttemptID:      in.Scope.AttemptID,
			AssignmentID:   in.AssignmentID,
			ParticipantID:  in.ParticipantID,
			ServerSequence: sequence,
			IdempotencyKey: in.IdempotencyKey,
			Status:         in.Status,
			DecisionReason: optionalTrimmedString(in.DecisionReason),
			PayloadDigest:  append([]byte(nil), in.PayloadDigest[:]...),
			IntentDigest:   append([]byte(nil), in.IntentDigest[:]...),
			SubmittedAt:    tstz(in.SubmittedAt),
			ReceivedAt:     tstz(in.ReceivedAt),
			CreatedAt:      tstz(in.CreatedAt),
		})
		if err != nil {
			return mapRepositoryWriteError("ResultPostgres - RecordSubmission", err)
		}
		record = submissionRecord(row)
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return record, changed, nil
}
