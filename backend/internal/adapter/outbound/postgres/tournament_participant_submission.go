package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/taskexec"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

const participantIncorrectFlagReason = "incorrect_flag"

type ParticipantSubmissionRepository struct {
	tx      *TxManager
	results *ResultPostgres
}

type participantSubmissionAttempt struct {
	RosterID uuid.UUID
}

func NewParticipantSubmissionRepository(
	tx *TxManager,
	results *ResultPostgres,
) *ParticipantSubmissionRepository {
	return &ParticipantSubmissionRepository{tx: tx, results: results}
}

func (r *ParticipantSubmissionRepository) LoadSubmissionAuthority(
	ctx context.Context,
	scope gamedomain.SubmissionScope,
) (gameusecase.SubmissionAuthority, error) {
	if ctx == nil || r == nil || r.tx == nil || !scope.IsValid() {
		return gameusecase.SubmissionAuthority{}, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	attempt, series, metadata, err := loadParticipantSubmissionSeries(ctx, querier, scope)
	if err != nil {
		return gameusecase.SubmissionAuthority{}, err
	}
	binding, err := querier.GetParticipantSubmissionBinding(
		ctx,
		sqlc.GetParticipantSubmissionBindingParams{
			WaveID: nullableUUIDValue(scope.WaveID), GameID: scope.Game.GameID,
			SlotID: scope.Game.SlotID, AssignmentID: scope.AssignmentID,
			SeriesID: scope.Game.SeriesID, TournamentID: scope.Game.TournamentID, RosterID: attempt.RosterID,
		},
	)
	if err != nil {
		return gameusecase.SubmissionAuthority{}, participantSubmissionLookupError("binding", err)
	}
	snapshot, digest, err := participantSubmissionSnapshotFromRow(binding)
	if err != nil {
		return gameusecase.SubmissionAuthority{}, err
	}
	startedAt, ok := participantSubmissionStartedAt(series, scope, binding)
	if !ok {
		return gameusecase.SubmissionAuthority{}, domain.ErrConflict
	}
	presence, err := loadParticipantSubmissionPresence(ctx, querier, scope, attempt.RosterID)
	if err != nil {
		return gameusecase.SubmissionAuthority{}, err
	}
	history, err := loadParticipantSubmissionHistory(ctx, querier, scope, attempt.RosterID, binding)
	if err != nil {
		return gameusecase.SubmissionAuthority{}, err
	}
	started := gamedomain.Started{
		Scope: scope.Game,
		ParticipantIDs: [2]uuid.UUID{
			series.Series.FirstParticipantID,
			series.Series.SecondParticipantID,
		},
		Series: series, AssignmentID: scope.AssignmentID,
		AssignmentRevision: binding.AssignmentRevision, PlanRevisionID: binding.PlanRevisionID,
		SnapshotID: binding.SnapshotID, ContentDigest: digest, DeadlineSeconds: int(binding.TimeLimit),
		StartedAt: startedAt, Deadline: startedAt.Add(time.Duration(binding.TimeLimit) * time.Second),
		DeliveryEnabled: true,
	}
	if gamedomain.ValidateStarted(scope, started) != nil || metadata.AttemptRevisions[scope.Game.GameID] != binding.AttemptRevision {
		return gameusecase.SubmissionAuthority{}, domain.ErrInternal
	}
	return gameusecase.SubmissionAuthority{
		Scope: scope, Revision: binding.AttemptRevision, StartedGame: started,
		Snapshot: participantSubmissionSnapshot{
			value: snapshot, digest: digest, participantIDs: started.ParticipantIDs,
		},
		ConnectedParticipantIDs: append([]uuid.UUID(nil), presence...),
		Paused:                  binding.Paused, Submissions: history,
	}, nil
}

func loadParticipantSubmissionSeries(
	ctx context.Context,
	querier *sqlc.Queries,
	scope gamedomain.SubmissionScope,
) (participantSubmissionAttempt, seriesdomain.Execution, participantSeriesAuthority, error) {
	row, err := querier.GetGameAttemptScoped(ctx, sqlc.GetGameAttemptScopedParams{
		ID: scope.Game.GameID, SlotID: scope.Game.SlotID,
		SeriesID: scope.Game.SeriesID, TournamentID: scope.Game.TournamentID,
	})
	if err != nil {
		return participantSubmissionAttempt{}, seriesdomain.Execution{}, participantSeriesAuthority{},
			participantSubmissionLookupError("attempt", err)
	}
	attempt, err := participantSubmissionAttemptFromRow(scope, row)
	if err != nil {
		return participantSubmissionAttempt{}, seriesdomain.Execution{}, participantSeriesAuthority{}, err
	}
	rows, err := querier.GetParticipantSeriesExecution(
		ctx,
		sqlc.GetParticipantSeriesExecutionParams{
			SeriesID: scope.Game.SeriesID, TournamentID: scope.Game.TournamentID, RosterID: attempt.RosterID,
		},
	)
	if err != nil {
		return participantSubmissionAttempt{}, seriesdomain.Execution{}, participantSeriesAuthority{}, fmt.Errorf(
			"ParticipantSubmissionRepository - load Series: %w",
			err,
		)
	}
	series, metadata, err := participantSeriesExecution(rows)
	return attempt, series, metadata, err
}

func participantSubmissionAttemptFromRow(
	scope gamedomain.SubmissionScope,
	row sqlc.GetGameAttemptScopedRow,
) (participantSubmissionAttempt, error) {
	if row.ID != scope.Game.GameID || row.SlotID != scope.Game.SlotID || row.SeriesID != scope.Game.SeriesID ||
		row.RosterID == uuid.Nil {
		return participantSubmissionAttempt{}, domain.ErrConflict
	}
	return participantSubmissionAttempt{RosterID: row.RosterID}, nil
}

func loadParticipantSubmissionPresence(
	ctx context.Context,
	querier *sqlc.Queries,
	scope gamedomain.SubmissionScope,
	rosterID uuid.UUID,
) ([]uuid.UUID, error) {
	presence, err := querier.ListParticipantSubmissionPresence(
		ctx,
		sqlc.ListParticipantSubmissionPresenceParams{
			TournamentID: scope.Game.TournamentID, RosterID: rosterID, SeriesID: scope.Game.SeriesID,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("ParticipantSubmissionRepository - load presence: %w", err)
	}
	return presence, nil
}

func loadParticipantSubmissionHistory(
	ctx context.Context,
	querier *sqlc.Queries,
	scope gamedomain.SubmissionScope,
	rosterID uuid.UUID,
	binding sqlc.GetParticipantSubmissionBindingRow,
) ([]gamedomain.Submission, error) {
	rows, err := querier.ListParticipantSubmissionHistory(
		ctx,
		sqlc.ListParticipantSubmissionHistoryParams{
			TournamentID: scope.Game.TournamentID, RosterID: rosterID,
			SeriesID: scope.Game.SeriesID, AttemptID: scope.Game.GameID, AssignmentID: scope.AssignmentID,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("ParticipantSubmissionRepository - load history: %w", err)
	}
	return participantSubmissionHistory(scope, binding.SnapshotID, binding.TaskID, rows)
}

func (r *ParticipantSubmissionRepository) CommitSubmission(
	ctx context.Context,
	commit gameusecase.SubmissionCommit,
) (*gamedomain.Submission, bool, error) {
	if ctx == nil || r == nil || r.tx == nil || r.results == nil ||
		!validParticipantSubmissionCommit(commit) {
		return nil, false, domain.ErrValidation
	}

	var result *gamedomain.Submission
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		result, changed, err = r.commitParticipantSubmission(txCtx, commit)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	if result == nil {
		return nil, false, domain.ErrInternal
	}
	return result, changed, nil
}

func (r *ParticipantSubmissionRepository) commitParticipantSubmission(
	ctx context.Context,
	commit gameusecase.SubmissionCommit,
) (*gamedomain.Submission, bool, error) {
	querier := r.tx.Querier(ctx)
	attemptRow, err := querier.GetGameAttemptScoped(ctx, sqlc.GetGameAttemptScopedParams{
		ID: commit.Scope.Game.GameID, SlotID: commit.Scope.Game.SlotID,
		SeriesID: commit.Scope.Game.SeriesID, TournamentID: commit.Scope.Game.TournamentID,
	})
	if err != nil {
		return nil, false, participantSubmissionLookupError("commit attempt", err)
	}
	attempt, err := participantSubmissionAttemptFromRow(commit.Scope, attemptRow)
	if err != nil {
		return nil, false, err
	}
	metadata, err := querier.GetParticipantSubmissionCommitMetadata(
		ctx,
		sqlc.GetParticipantSubmissionCommitMetadataParams{
			AttemptID: commit.Scope.Game.GameID,
			SeriesID:  commit.Scope.Game.SeriesID,
			RosterID:  attempt.RosterID,
		},
	)
	if err != nil {
		return nil, false, participantSubmissionLookupError("commit metadata", err)
	}
	committedAt := metadata.CommittedAt.Time.Round(0).UTC()
	if metadata.AttemptRevision != commit.ExpectedAuthorityRevision ||
		domain.GameState(metadata.AttemptState) != commit.ExpectedGameState {
		return nil, false, domain.ErrConflict
	}
	if !domain.IsValidServerTime(committedAt) || !committedAt.Before(commit.ExpectedDeadline) {
		return nil, false, gameusecase.ErrSubmissionNotOpen
	}
	status, decisionReason := participantSubmissionDecision(commit.Correct)
	record, inserted, err := r.results.RecordSubmission(ctx, SubmissionInput{
		ID: participantCommandID(commit.CommandID, "submission-event"),
		Scope: ResultScope{
			TournamentID: commit.Scope.Game.TournamentID,
			RosterID:     attempt.RosterID,
			SeriesID:     commit.Scope.Game.SeriesID,
			AttemptID:    commit.Scope.Game.GameID,
		},
		AssignmentID: commit.Scope.AssignmentID, ParticipantID: commit.ParticipantID,
		IdempotencyKey: commit.CommandID,
		Status:         status, DecisionReason: decisionReason,
		PayloadDigest: commit.ContentDigest, IntentDigest: commit.IntentDigest,
		SubmittedAt: committedAt, ReceivedAt: committedAt, CreatedAt: committedAt,
		ExpectedAttemptRevision: commit.ExpectedAuthorityRevision,
		ExpectedAttemptState:    commit.ExpectedGameState,
	})
	if err != nil {
		return nil, false, err
	}
	mapped, err := participantSubmissionFromRecord(commit, record)
	if err != nil {
		return nil, false, err
	}
	return &mapped, inserted, nil
}

func participantSubmissionDecision(correct bool) (string, string) {
	if correct {
		return submissionStatusAccepted, ""
	}
	return submissionStatusRejected, participantIncorrectFlagReason
}

type participantSubmissionSnapshot struct {
	value          domain.AssignmentTaskSnapshot
	digest         [sha256.Size]byte
	participantIDs [2]uuid.UUID
}

func (s participantSubmissionSnapshot) Snapshot() domain.AssignmentTaskSnapshot {
	value := s.value
	value.Hints = append([]string(nil), s.value.Hints...)
	value.TaskURL = cloneParticipantStringPointer(s.value.TaskURL)
	value.SourceFileURL = cloneParticipantStringPointer(s.value.SourceFileURL)
	return value
}

func (s participantSubmissionSnapshot) ContentDigest() [sha256.Size]byte {
	return s.digest
}

func (s participantSubmissionSnapshot) HasParticipant(participantID uuid.UUID) bool {
	return participantID != uuid.Nil &&
		(participantID == s.participantIDs[0] || participantID == s.participantIDs[1])
}

func participantSubmissionSnapshotFromRow(
	row sqlc.GetParticipantSubmissionBindingRow,
) (domain.AssignmentTaskSnapshot, [sha256.Size]byte, error) {
	var hints []string
	if err := json.Unmarshal(row.Hints, &hints); err != nil {
		return domain.AssignmentTaskSnapshot{}, [sha256.Size]byte{}, domain.ErrInternal
	}
	snapshot := domain.AssignmentTaskSnapshot{
		SnapshotID: row.SnapshotID, TaskID: row.TaskID, Version: int(row.TaskVersion),
		Kind: domain.AssignmentTaskKind(row.Kind), Title: row.Title, Description: row.Description,
		Category: domain.Category(row.Category), Difficulty: domain.Difficulty(row.Difficulty),
		TimeLimit: int(row.TimeLimit), Flag: row.Flag, Hints: hints,
		TaskURL:       cloneParticipantStringPointer(row.TaskUrl),
		SourceFileURL: cloneParticipantStringPointer(row.SourceFileUrl),
	}
	if snapshot.Validate() != nil || len(row.ContentDigest) != sha256.Size {
		return domain.AssignmentTaskSnapshot{}, [sha256.Size]byte{}, domain.ErrInternal
	}
	var digest [sha256.Size]byte
	copy(digest[:], row.ContentDigest)
	calculated, err := taskexec.SnapshotDigest(snapshot)
	if err != nil || calculated != digest {
		return domain.AssignmentTaskSnapshot{}, [sha256.Size]byte{}, domain.ErrInternal
	}
	return snapshot, digest, nil
}

func participantSubmissionStartedAt(
	series seriesdomain.Execution,
	scope gamedomain.SubmissionScope,
	binding sqlc.GetParticipantSubmissionBindingRow,
) (time.Time, bool) {
	game, _, found := gamedomain.FindAttempt(gamedomain.Started{Series: series}, scope.Game)
	if series.Validate() != nil || !found || !binding.StartedAt.Valid ||
		game.State != domain.GameState(binding.AttemptState) || game.State != domain.GameStateActive {
		return time.Time{}, false
	}
	startedAt := binding.StartedAt.Time.Round(0).UTC()
	return startedAt, domain.IsValidServerTime(startedAt) && scope.IsValid()
}

func participantSubmissionHistory(
	scope gamedomain.SubmissionScope,
	snapshotID uuid.UUID,
	taskID uuid.UUID,
	rows []sqlc.SubmissionEvent,
) ([]gamedomain.Submission, error) {
	records := make([]gamedomain.Submission, len(rows))
	for index, row := range rows {
		if row.TournamentID != scope.Game.TournamentID || row.SeriesID != scope.Game.SeriesID ||
			row.AttemptID != scope.Game.GameID || row.AssignmentID != scope.AssignmentID ||
			len(row.PayloadDigest) != sha256.Size || len(row.IntentDigest) != sha256.Size {
			return nil, domain.ErrInternal
		}
		correct := false
		switch row.Status {
		case submissionStatusAccepted:
			if row.DecisionReason != nil {
				return nil, domain.ErrInternal
			}
			correct = true
		case submissionStatusRejected:
			if row.DecisionReason == nil || *row.DecisionReason != participantIncorrectFlagReason {
				return nil, domain.ErrInternal
			}
		default:
			return nil, domain.ErrInternal
		}
		var digest [sha256.Size]byte
		copy(digest[:], row.PayloadDigest)
		records[index] = gamedomain.Submission{
			Scope: scope, CommandID: row.IdempotencyKey, ParticipantID: row.ParticipantID,
			Sequence: row.ServerSequence, CommittedAt: row.CreatedAt.Time.Round(0).UTC(), Correct: correct,
			SnapshotID: snapshotID, TaskID: taskID, ContentDigest: digest,
		}
		if gamedomain.ValidateSubmission(records[index]) != nil {
			return nil, domain.ErrInternal
		}
	}
	return records, nil
}

func validParticipantSubmissionCommit(commit gameusecase.SubmissionCommit) bool {
	return commit.Scope.IsValid() && commit.ExpectedAuthorityRevision >= 1 &&
		commit.ExpectedGameState.IsValid() && !commit.ExpectedGameState.IsTerminal() &&
		domain.IsValidServerTime(commit.ExpectedDeadline) && commit.CommandID != uuid.Nil &&
		commit.ParticipantID != uuid.Nil && commit.SnapshotID != uuid.Nil && commit.TaskID != uuid.Nil &&
		commit.ContentDigest != [sha256.Size]byte{} && commit.IntentDigest != [sha256.Size]byte{}
}

func participantSubmissionFromRecord(
	commit gameusecase.SubmissionCommit,
	record *SubmissionRecord,
) (gamedomain.Submission, error) {
	if record == nil || record.Scope.TournamentID != commit.Scope.Game.TournamentID ||
		record.Scope.SeriesID != commit.Scope.Game.SeriesID || record.Scope.AttemptID != commit.Scope.Game.GameID ||
		record.AssignmentID != commit.Scope.AssignmentID || record.ParticipantID != commit.ParticipantID ||
		record.IdempotencyKey != commit.CommandID || record.PayloadDigest != commit.ContentDigest ||
		record.IntentDigest != commit.IntentDigest {
		return gamedomain.Submission{}, domain.ErrInternal
	}
	correct := record.Status == submissionStatusAccepted
	if correct != commit.Correct {
		return gamedomain.Submission{}, domain.ErrInternal
	}
	result := gamedomain.Submission{
		Scope: commit.Scope, CommandID: commit.CommandID, ParticipantID: commit.ParticipantID,
		Sequence: record.ServerSequence, CommittedAt: record.CreatedAt.Round(0).UTC(), Correct: correct,
		SnapshotID: commit.SnapshotID, TaskID: commit.TaskID, ContentDigest: commit.ContentDigest,
	}
	if gamedomain.ValidateSubmission(result) != nil {
		return gamedomain.Submission{}, domain.ErrInternal
	}
	return result, nil
}

func participantSubmissionLookupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return fmt.Errorf("ParticipantSubmissionRepository - %s: %w", operation, err)
}

func cloneParticipantStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

var _ gameusecase.SubmissionRepository = (*ParticipantSubmissionRepository)(nil)
