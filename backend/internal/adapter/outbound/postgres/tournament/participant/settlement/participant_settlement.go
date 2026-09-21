package settlement

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	resultpostgres "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/settlement"
)

const participantSettlementProjectionReason = "participant_submission"

// ResultRepository is the narrow result persistence boundary needed by the
// participant settlement workflow.
type ResultRepository interface {
	Settle(
		ctx context.Context,
		input resultpostgres.ResultSettlementInput,
	) (*resultpostgres.ResultCommitRecord, bool, error)
}

var _ ResultRepository = (*resultpostgres.ResultPostgres)(nil)

type ParticipantSettlementRepository struct {
	tx        *db.TxManager
	results   ResultRepository
	finalizer resultpostgres.ProjectionFinalizer
}

func NewParticipantSettlementRepository(
	tx *db.TxManager,
	results *resultpostgres.ResultPostgres,
) *ParticipantSettlementRepository {
	return NewParticipantSettlementRepositoryWithResultRepository(tx, results, nil)
}

func NewParticipantSettlementRepositoryWithFinalizer(
	tx *db.TxManager,
	results *resultpostgres.ResultPostgres,
	finalizer resultpostgres.ProjectionFinalizer,
) *ParticipantSettlementRepository {
	return NewParticipantSettlementRepositoryWithResultRepository(tx, results, finalizer)
}

func NewParticipantSettlementRepositoryWithResultRepository(
	tx *db.TxManager,
	results ResultRepository,
	finalizer resultpostgres.ProjectionFinalizer,
) *ParticipantSettlementRepository {
	return &ParticipantSettlementRepository{tx: tx, results: results, finalizer: finalizer}
}

func (r *ParticipantSettlementRepository) LoadConcurrentWinnerAuthority(
	ctx context.Context,
	scope gamedomain.SubmissionScope,
) (gameusecase.SettlementAuthority, error) {
	if ctx == nil || r == nil || r.tx == nil || !scope.IsValid() {
		return gameusecase.SettlementAuthority{}, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	attempt, series, metadata, err := loadParticipantSubmissionSeries(ctx, querier, scope)
	if err != nil {
		return gameusecase.SettlementAuthority{}, err
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
		return gameusecase.SettlementAuthority{}, participantSettlementLookupError("submission binding", err)
	}
	startedAt, ok := participantSubmissionStartedAt(series, scope, binding)
	if !ok {
		return gameusecase.SettlementAuthority{}, domain.ErrConflict
	}
	history, err := loadParticipantSubmissionHistory(ctx, querier, scope, attempt.RosterID, binding)
	if err != nil {
		return gameusecase.SettlementAuthority{}, err
	}
	revisionIDs, err := querier.ListParticipantGameResultRevisionIDs(
		ctx,
		sqlc.ListParticipantGameResultRevisionIDsParams{
			SeriesID: scope.Game.SeriesID,
			RosterID: attempt.RosterID,
		},
	)
	if err != nil {
		return gameusecase.SettlementAuthority{}, fmt.Errorf("ParticipantSettlementRepository - Game revisions: %w", err)
	}
	gameRevisionIDs := make([]domain.OfficialResultRevisionID, len(revisionIDs))
	for index, revisionID := range revisionIDs {
		gameRevisionIDs[index] = domain.OfficialResultRevisionID(revisionID)
	}
	started := gamedomain.Started{
		Scope: scope.Game,
		ParticipantIDs: [2]uuid.UUID{
			series.Series.FirstParticipantID,
			series.Series.SecondParticipantID,
		},
		Series: series, AssignmentID: scope.AssignmentID,
		AssignmentRevision: binding.AssignmentRevision, PlanRevisionID: binding.PlanRevisionID,
		SnapshotID: binding.SnapshotID, ContentDigest: participantBindingDigest(binding),
		DeadlineSeconds: int(domain.TournamentTaskDuration / time.Second), StartedAt: startedAt,
		Deadline: startedAt.Add(domain.TournamentTaskDuration), DeliveryEnabled: true,
	}
	if gamedomain.ValidateStarted(scope, started) != nil ||
		metadata.AttemptRevisions[scope.Game.GameID] != binding.AttemptRevision {
		return gameusecase.SettlementAuthority{}, domain.ErrInternal
	}
	return gameusecase.SettlementAuthority{
		Scope:                        scope,
		Revision:                     binding.AttemptRevision,
		StartedGame:                  started,
		Submissions:                  history,
		CurrentScoreOrdinal:          int(metadata.ScoreRevision - 1),
		CurrentGameResultRevisionIDs: gameRevisionIDs,
		CurrentProjectionRevision:    metadata.ProjectionRevision,
	}, nil
}

func (r *ParticipantSettlementRepository) CommitConcurrentWinnerSettlement(
	ctx context.Context,
	proposed gameusecase.SettlementRecord,
) (*gameusecase.SettlementRecord, bool, error) {
	if ctx == nil || r == nil || r.tx == nil || r.results == nil || proposed.Validate() != nil {
		return nil, false, domain.ErrValidation
	}
	var committed *gameusecase.SettlementRecord
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		committed, changed, err = r.commitConcurrentWinnerSettlement(txCtx, proposed)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	if committed == nil || committed.Validate() != nil {
		return nil, false, domain.ErrInternal
	}
	return committed, changed, nil
}

func (r *ParticipantSettlementRepository) commitConcurrentWinnerSettlement(
	ctx context.Context,
	proposed gameusecase.SettlementRecord,
) (*gameusecase.SettlementRecord, bool, error) {
	querier := r.tx.Querier(ctx)
	rosterID, err := querier.GetParticipantSeriesRoster(
		ctx,
		sqlc.GetParticipantSeriesRosterParams{
			SeriesID:     proposed.Scope.Game.SeriesID,
			TournamentID: proposed.Scope.Game.TournamentID,
		},
	)
	if err != nil {
		return nil, false, participantSettlementLookupError("roster", err)
	}
	metadata, err := querier.GetParticipantSurrenderCommitMetadata(
		ctx,
		sqlc.GetParticipantSurrenderCommitMetadataParams{
			AttemptID: proposed.Scope.Game.GameID, SeriesID: proposed.Scope.Game.SeriesID,
			TournamentID: proposed.Scope.Game.TournamentID, RosterID: rosterID,
		},
	)
	if err != nil {
		return nil, false, participantSettlementLookupError("commit metadata", err)
	}
	if metadata.AttemptRevision != proposed.ExpectedAuthorityRevision ||
		metadata.AttemptState != string(domain.GameStateActive) ||
		metadata.SeriesState != string(domain.SeriesStateActive) {
		return nil, false, domain.ErrConflict
	}
	result, changed, err := r.results.Settle(
		ctx,
		participantSettlementResultInput(proposed, rosterID, metadata),
	)
	if err != nil {
		return nil, false, err
	}
	committed, err := participantStoredSettlement(proposed, result)
	if err != nil {
		return nil, false, err
	}
	if !changed {
		return &committed, false, nil
	}
	if err := r.appendParticipantSettlementSwissLedger(ctx, rosterID, committed, result); err != nil {
		return nil, false, err
	}
	if err := r.publishParticipantSettlementProjection(ctx, rosterID, committed, result); err != nil {
		return nil, false, err
	}
	return &committed, true, nil
}

func participantSettlementResultInput(
	record gameusecase.SettlementRecord,
	rosterID uuid.UUID,
	metadata sqlc.GetParticipantSurrenderCommitMetadataRow,
) resultpostgres.ResultSettlementInput {
	seriesResultRevisionID := uuid.Nil
	seriesReason := ""
	if record.Series.CurrentResultRevisionID != nil {
		seriesResultRevisionID = record.Series.CurrentResultRevisionID.UUID()
		seriesReason = string(domain.SeriesResultReasonScoreComplete)
	}
	winnerID := record.WinningSubmission.ParticipantID
	kinds := []domain.ArtifactKind{
		domain.ArtifactKindGameResult,
		domain.ArtifactKindSeriesScore,
		domain.ArtifactKindStandings,
	}
	if seriesResultRevisionID != uuid.Nil {
		kinds = append(kinds, domain.ArtifactKindSeriesResult)
	}
	return resultpostgres.ResultSettlementInput{
		IDs: resultpostgres.ResultSettlementIDs{
			CommitID:                  participantCommandID(record.CommandID, "result-commit"),
			ResultEventID:             participantCommandID(record.CommandID, "result-event"),
			ResultEventIdempotencyKey: participantCommandID(record.CommandID, "result-event-idempotency"),
			GameResultRevisionID:      record.SettlementGameResultRevision.ID.UUID(),
			SeriesScoreRevisionID:     record.ScoreRevision.ID.UUID(),
			SeriesResultRevisionID:    seriesResultRevisionID,
			AuditEventID:              record.Evidence.AuditEventID,
			OutboxEventID:             record.Evidence.OutboxEventID,
			OutboxIdempotencyKey:      participantCommandID(record.CommandID, "outbox-idempotency"),
			ProjectionEvidenceID:      record.Evidence.ProjectionRevisionID,
			CommitIdempotencyKey:      record.CommandID,
		},
		Scope: resultpostgres.ResultScope{
			TournamentID: record.Scope.Game.TournamentID, RosterID: rosterID,
			SeriesID: record.Scope.Game.SeriesID, AttemptID: record.Scope.Game.GameID,
		},
		ProjectionPublication:   resultpostgres.ResultProjectionPublicationCallerOwned,
		SubmissionEventID:       participantCommandID(record.WinningSubmission.CommandID, "submission-event"),
		GameState:               record.Game.State,
		GameReason:              record.Game.ResultReason,
		GameWinnerID:            &winnerID,
		Score:                   record.ScoreRevision.ScoreAfter,
		NextSeriesState:         record.Series.State,
		SeriesResultReason:      seriesReason,
		SeriesWinnerID:          cloneParticipantUUIDPointer(record.Series.WinnerID),
		ActorKind:               "server",
		ProjectionArtifactKinds: kinds,
		ProjectionPayloadDigest: participantSettlementDigest(record),
		SettledAt:               record.SettledAt,
		ExpectedAttemptRevision: metadata.AttemptRevision,
		ExpectedAttemptState:    domain.GameState(metadata.AttemptState),
		ExpectedSeriesRevision:  metadata.SeriesRevision,
		ExpectedSeriesState:     domain.SeriesState(metadata.SeriesState),
	}
}

func participantStoredSettlement(
	proposed gameusecase.SettlementRecord,
	stored *resultpostgres.ResultCommitRecord,
) (gameusecase.SettlementRecord, error) {
	if stored == nil || stored.Commit.IdempotencyKey != proposed.CommandID ||
		stored.Commit.GameResultRevisionID != proposed.SettlementGameResultRevision.ID.UUID() ||
		stored.Commit.SeriesScoreRevisionID != proposed.ScoreRevision.ID.UUID() ||
		stored.Event.ResultReason != string(domain.GameResultReasonSolved) ||
		stored.Event.SubmissionEventID.UUID != participantCommandID(proposed.WinningSubmission.CommandID, "submission-event") {
		return gameusecase.SettlementRecord{}, domain.ErrConflict
	}
	wantSeriesResult := proposed.Series.CurrentResultRevisionID
	if wantSeriesResult == nil {
		if stored.Commit.SeriesResultRevisionID.Valid || stored.SeriesRevision != nil {
			return gameusecase.SettlementRecord{}, domain.ErrConflict
		}
	} else if !stored.Commit.SeriesResultRevisionID.Valid ||
		stored.Commit.SeriesResultRevisionID.UUID != wantSeriesResult.UUID() || stored.SeriesRevision == nil {
		return gameusecase.SettlementRecord{}, domain.ErrConflict
	}
	committed := proposed
	committedAt := stored.Event.OccurredAt.Time.Round(0).UTC()
	committed.SettledAt = committedAt
	committed.SettlementGameResultRevision.RecordedAt = committedAt
	committed.ScoreRevision.RecordedAt = committedAt
	committed.Evidence.RecordedAt = committedAt
	if committed.Validate() != nil {
		return gameusecase.SettlementRecord{}, domain.ErrInternal
	}
	return committed, nil
}

func (r *ParticipantSettlementRepository) appendParticipantSettlementSwissLedger(
	ctx context.Context,
	rosterID uuid.UUID,
	record gameusecase.SettlementRecord,
	result *resultpostgres.ResultCommitRecord,
) error {
	if record.Series.CurrentResultRevisionID == nil || result == nil || !result.Commit.SeriesResultRevisionID.Valid {
		return nil
	}
	querier := r.tx.Querier(ctx)
	rows, err := querier.ListParticipantSettlementSwissSeriesLedgerSources(
		ctx,
		sqlc.ListParticipantSettlementSwissSeriesLedgerSourcesParams{
			SeriesID: record.Scope.Game.SeriesID, TournamentID: record.Scope.Game.TournamentID,
			RosterID: rosterID, SeriesResultRevisionID: result.Commit.SeriesResultRevisionID,
		},
	)
	if err != nil {
		return fmt.Errorf("ParticipantSettlementRepository - Swiss ledger sources: %w", err)
	}
	for _, row := range rows {
		params := sqlc.CreateSwissPointLedgerEntryParams{
			ID:           participantSettlementID(record.CommandID, "swiss-ledger:"+row.ParticipantID.String()),
			TournamentID: record.Scope.Game.TournamentID, RosterID: rosterID,
			RoundID: row.RoundID, RoundNumber: row.RoundNumber,
			SourceKind: "series", SourceSeriesID: nullableUUIDValue(record.Scope.Game.SeriesID),
			SeriesResultRevisionID: nullableUUIDValue(result.Commit.SeriesResultRevisionID.UUID),
			ResultLabel:            optionalTrimmedString("played"), ParticipantID: row.ParticipantID,
			//nolint:gosec // Domain validation bounds this value before the storage conversion.
			OpponentID: nullableUUIDValue(row.OpponentID), Points: int16(row.Points),
			EffectiveTimeNs: record.EffectiveSolveTime.Nanoseconds(), StableSeed: row.StableSeed,
			CreatedAt: tstz(record.SettledAt),
		}
		if row.ParticipantID == record.WinningSubmission.ParticipantID {
			accepted := record.EffectiveSolveTime.Nanoseconds()
			params.AcceptedSolveTimeNs = &accepted
		}
		if _, err = querier.CreateSwissPointLedgerEntry(ctx, params); err != nil {
			return mapRepositoryWriteError("ParticipantSettlementRepository - append Swiss ledger", err)
		}
	}
	return nil
}

func participantSettlementDigest(record gameusecase.SettlementRecord) [sha256.Size]byte {
	return sha256.Sum256([]byte(fmt.Sprintf(
		"%s:%s:%s:%s:%d:%d",
		record.Scope.Game.TournamentID, record.Scope.Game.SeriesID,
		record.SettlementGameResultRevision.ID.UUID(), record.ScoreRevision.ID.UUID(),
		record.ScoreRevision.ScoreAfter.FirstParticipantWins,
		record.ScoreRevision.ScoreAfter.SecondParticipantWins,
	)))
}

func participantBindingDigest(binding sqlc.GetParticipantSubmissionBindingRow) [sha256.Size]byte {
	var digest [sha256.Size]byte
	copy(digest[:], binding.ContentDigest)
	return digest
}

func participantSettlementID(commandID uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(commandID, []byte("participant-settlement:"+role))
}

func participantSettlementLookupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return fmt.Errorf("ParticipantSettlementRepository - %s: %w", operation, err)
}

var _ gameusecase.SettlementRepository = (*ParticipantSettlementRepository)(nil)
