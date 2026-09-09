package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	tournamentparticipant "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant"
)

type ParticipantForfeitRepository struct {
	tx      *TxManager
	results *ResultPostgres
}

func NewParticipantForfeitRepository(
	tx *TxManager,
	results *ResultPostgres,
) *ParticipantForfeitRepository {
	return &ParticipantForfeitRepository{tx: tx, results: results}
}

func (r *ParticipantForfeitRepository) LoadForfeitAuthority(
	ctx context.Context,
	scope gameusecase.Scope,
) (gameusecase.ForfeitAuthority, error) {
	if ctx == nil || r == nil || r.tx == nil || !scope.IsValid() {
		return gameusecase.ForfeitAuthority{}, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	rosterID, err := querier.GetParticipantSeriesRoster(
		ctx,
		sqlc.GetParticipantSeriesRosterParams{
			SeriesID:     scope.SeriesID,
			TournamentID: scope.TournamentID,
		},
	)
	if err != nil {
		return gameusecase.ForfeitAuthority{}, participantForfeitLookupError("roster", err)
	}
	rows, err := querier.GetParticipantSeriesExecution(
		ctx,
		sqlc.GetParticipantSeriesExecutionParams{
			SeriesID: scope.SeriesID, TournamentID: scope.TournamentID, RosterID: rosterID,
		},
	)
	if err != nil {
		return gameusecase.ForfeitAuthority{}, fmt.Errorf("ParticipantForfeitRepository - load Series: %w", err)
	}
	series, metadata, err := participantSeriesExecution(rows)
	if err != nil {
		return gameusecase.ForfeitAuthority{}, err
	}
	presence, err := querier.ListParticipantForfeitPresence(
		ctx,
		sqlc.ListParticipantForfeitPresenceParams{
			TournamentID: scope.TournamentID, RosterID: rosterID, SeriesID: scope.SeriesID,
		},
	)
	if err != nil {
		return gameusecase.ForfeitAuthority{}, fmt.Errorf("ParticipantForfeitRepository - load presence: %w", err)
	}
	revisionRows, err := querier.ListParticipantGameResultRevisionIDs(
		ctx,
		sqlc.ListParticipantGameResultRevisionIDsParams{SeriesID: scope.SeriesID, RosterID: rosterID},
	)
	if err != nil {
		return gameusecase.ForfeitAuthority{}, fmt.Errorf("ParticipantForfeitRepository - load Game revisions: %w", err)
	}
	gameRevisions := make([]domain.OfficialResultRevisionID, len(revisionRows))
	for index, revisionID := range revisionRows {
		gameRevisions[index] = domain.OfficialResultRevisionID(revisionID)
	}
	return gameusecase.ForfeitAuthority{
		Scope: scope, Revision: metadata.SeriesRevision, Series: series,
		ConnectedParticipantIDs:      append([]uuid.UUID(nil), presence...),
		CurrentOrdinal:               int(metadata.ScoreRevision - 1),
		CurrentSeriesResultOrdinal:   int(metadata.SeriesResultRevision),
		CurrentProjectionRevision:    metadata.ProjectionRevision,
		CurrentGameResultRevisionIDs: gameRevisions,
	}, nil
}

func (r *ParticipantForfeitRepository) CommitForfeitResolution(
	ctx context.Context,
	resolution gameusecase.ForfeitResolution,
) (*gameusecase.ForfeitResolution, bool, error) {
	if ctx == nil || r == nil || r.tx == nil || r.results == nil || resolution.Validate() != nil ||
		resolution.Game == nil || resolution.GameRevision == nil || resolution.ExpectedGame == nil {
		return nil, false, domain.ErrValidation
	}

	var committed *gameusecase.ForfeitResolution
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		committed, changed, err = r.commitParticipantForfeit(txCtx, resolution)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	if committed == nil {
		return nil, false, domain.ErrInternal
	}
	return committed, changed, nil
}

func (r *ParticipantForfeitRepository) commitParticipantForfeit(
	ctx context.Context,
	resolution gameusecase.ForfeitResolution,
) (*gameusecase.ForfeitResolution, bool, error) {
	querier := r.tx.Querier(ctx)
	rosterID, err := querier.GetParticipantSeriesRoster(
		ctx,
		sqlc.GetParticipantSeriesRosterParams{
			SeriesID:     resolution.Scope.SeriesID,
			TournamentID: resolution.Scope.TournamentID,
		},
	)
	if err != nil {
		return nil, false, participantForfeitLookupError("commit roster", err)
	}
	metadata, err := querier.GetParticipantSurrenderCommitMetadata(
		ctx,
		sqlc.GetParticipantSurrenderCommitMetadataParams{
			AttemptID: resolution.Game.ID, SeriesID: resolution.Scope.SeriesID,
			TournamentID: resolution.Scope.TournamentID, RosterID: rosterID,
		},
	)
	if err != nil {
		return nil, false, participantForfeitLookupError("commit metadata", err)
	}
	if metadata.AttemptRevision < 1 || metadata.SeriesRevision != resolution.ExpectedAuthorityRevision ||
		domain.GameState(metadata.AttemptState) != resolution.ExpectedGame.State ||
		domain.SeriesState(metadata.SeriesState).IsTerminal() {
		return nil, false, domain.ErrConflict
	}
	record, inserted, err := r.results.Settle(
		ctx,
		participantForfeitSettlementInput(resolution, rosterID, metadata),
	)
	if err != nil {
		return nil, false, err
	}
	stored, err := participantStoredForfeitResolution(resolution, record)
	if err != nil {
		return nil, false, err
	}
	return &stored, inserted, nil
}

func participantForfeitSettlementInput(
	resolution gameusecase.ForfeitResolution,
	rosterID uuid.UUID,
	metadata sqlc.GetParticipantSurrenderCommitMetadataRow,
) ResultSettlementInput {
	winnerID := resolution.GameRevision.WinnerID
	return ResultSettlementInput{
		IDs: ResultSettlementIDs{
			CommitID:                  participantCommandID(resolution.CommandID, "result-commit"),
			ResultEventID:             participantCommandID(resolution.CommandID, "result-event"),
			ResultEventIdempotencyKey: participantCommandID(resolution.CommandID, "result-event-idempotency"),
			GameResultRevisionID:      resolution.GameRevision.ID.UUID(),
			SeriesScoreRevisionID:     resolution.ScoreRevision.ID.UUID(),
			SeriesResultRevisionID:    resolution.SeriesRevision.ID.UUID(),
			AuditEventID:              resolution.Evidence.AuditEventID,
			OutboxEventID:             resolution.Evidence.OutboxEventID,
			OutboxIdempotencyKey:      participantCommandID(resolution.CommandID, "outbox-idempotency"),
			ProjectionEvidenceID:      resolution.Evidence.ProjectionRevisionID,
			CommitIdempotencyKey:      resolution.CommandID,
		},
		Scope: ResultScope{
			TournamentID: resolution.Scope.TournamentID, RosterID: rosterID,
			SeriesID: resolution.Scope.SeriesID, AttemptID: resolution.Game.ID,
		},
		GameState: resolution.Game.State, GameReason: resolution.Game.ResultReason,
		GameWinnerID: &winnerID, Score: resolution.ScoreRevision.ScoreAfter,
		NextSeriesState:    resolution.Series.Series.State,
		SeriesResultReason: string(domain.SeriesResultReasonScoreComplete),
		SeriesWinnerID:     resolution.Series.Series.WinnerID,
		ActorKind:          resultActorServer,
		ProjectionArtifactKinds: []domain.ArtifactKind{
			domain.ArtifactKindGameResult,
			domain.ArtifactKindSeriesScore,
			domain.ArtifactKindSeriesResult,
			domain.ArtifactKindStandings,
			domain.ArtifactKindBracket,
		},
		ProjectionPayloadDigest: participantSurrenderDigest(resolution),
		SettledAt:               resolution.ResolvedAt,
		ExpectedAttemptRevision: metadata.AttemptRevision,
		ExpectedAttemptState:    domain.GameState(metadata.AttemptState),
		ExpectedSeriesRevision:  metadata.SeriesRevision,
		ExpectedSeriesState:     domain.SeriesState(metadata.SeriesState),
	}
}

type ParticipantSurrenderWorkflow struct {
	repository *ParticipantForfeitRepository
	usecase    *gameusecase.ForfeitUseCase
}

func NewParticipantSurrenderWorkflow(
	repository *ParticipantForfeitRepository,
	clock gameusecase.ForfeitClock,
) *ParticipantSurrenderWorkflow {
	return &ParticipantSurrenderWorkflow{
		repository: repository,
		usecase:    gameusecase.ForfeitNewUseCase(repository, clock),
	}
}

func (w *ParticipantSurrenderWorkflow) Surrender(
	ctx context.Context,
	resolved tournamentparticipant.ResolvedSurrender,
) (usecase.OfficialResultView, bool, error) {
	if ctx == nil || w == nil || w.repository == nil || w.usecase == nil {
		return usecase.OfficialResultView{}, false, domain.ErrValidation
	}
	existing, err := w.repository.findSurrenderResult(ctx, resolved)
	if err != nil {
		return usecase.OfficialResultView{}, false, err
	}
	if existing != nil {
		return *existing, false, nil
	}
	resolution, changed, err := w.usecase.Surrender(ctx, resolved.Command)
	if err != nil {
		return usecase.OfficialResultView{}, false, err
	}
	if resolution == nil {
		return usecase.OfficialResultView{}, false, domain.ErrInternal
	}
	view := participantSurrenderView(*resolution, resolved.Authority.ProjectionRevisionID)
	return view, changed, nil
}

func (r *ParticipantForfeitRepository) findSurrenderResult(
	ctx context.Context,
	resolved tournamentparticipant.ResolvedSurrender,
) (*usecase.OfficialResultView, error) {
	commit, err := r.tx.Querier(ctx).GetResultCommitByIdempotencyKey(ctx, resolved.Command.CommandID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("ParticipantForfeitRepository - find surrender: %w", err)
	}
	if commit.TournamentID != resolved.Authority.TournamentID ||
		commit.RosterID != resolved.Authority.RosterID || commit.SeriesID != resolved.Command.Scope.SeriesID {
		return nil, gameusecase.ErrForfeitCommandReuse
	}
	record, err := loadResultCommit(ctx, r.tx.Querier(ctx), commit)
	if err != nil {
		return nil, err
	}
	view, err := participantSurrenderViewFromCommit(record, resolved)
	if err != nil {
		return nil, err
	}
	return &view, nil
}

func participantStoredForfeitResolution(
	proposed gameusecase.ForfeitResolution,
	record *ResultCommitRecord,
) (gameusecase.ForfeitResolution, error) {
	if record == nil || record.SeriesRevision == nil ||
		record.Commit.IdempotencyKey != proposed.CommandID ||
		record.Commit.GameResultRevisionID != proposed.GameRevision.ID.UUID() ||
		record.Commit.SeriesScoreRevisionID != proposed.ScoreRevision.ID.UUID() ||
		!record.Commit.SeriesResultRevisionID.Valid ||
		record.Commit.SeriesResultRevisionID.UUID != proposed.SeriesRevision.ID.UUID() ||
		record.Event.ResultReason != string(domain.GameResultReasonSurrender) {
		return gameusecase.ForfeitResolution{}, gameusecase.ErrForfeitCommandReuse
	}
	stored := proposed
	stored.ResolvedAt = record.Event.OccurredAt.Time.Round(0).UTC()
	stored.GameRevision.RecordedAt = stored.ResolvedAt
	stored.ScoreRevision.RecordedAt = stored.ResolvedAt
	stored.SeriesRevision.RecordedAt = stored.ResolvedAt
	stored.Evidence.RecordedAt = stored.ResolvedAt
	if stored.Validate() != nil {
		return gameusecase.ForfeitResolution{}, domain.ErrInternal
	}
	return stored, nil
}

func participantSurrenderView(
	resolution gameusecase.ForfeitResolution,
	sourceProjectionRevisionID uuid.UUID,
) usecase.OfficialResultView {
	return usecase.OfficialResultView{
		ID:                 resolution.SeriesRevision.ID,
		PreviousRevisionID: cloneParticipantResultRevisionPointer(resolution.SeriesRevision.PreviousRevisionID),
		Ordinal:            resolution.SeriesRevision.Ordinal, CommandID: resolution.CommandID,
		TournamentID: resolution.Scope.TournamentID, SeriesID: resolution.Scope.SeriesID,
		Actor:                      domain.ResultActor{Kind: domain.ResultActorServer},
		WinnerID:                   cloneParticipantUUIDPointer(resolution.SeriesRevision.WinnerID),
		ScoreRevisionID:            resolution.SeriesRevision.ScoreRevisionID,
		SourceProjectionRevisionID: sourceProjectionRevisionID,
		SeriesState:                resolution.SeriesRevision.State,
		SeriesReason:               domain.SeriesResultReasonScoreComplete,
		RecordedAt:                 resolution.SeriesRevision.RecordedAt,
	}
}

func participantSurrenderViewFromCommit(
	record *ResultCommitRecord,
	resolved tournamentparticipant.ResolvedSurrender,
) (usecase.OfficialResultView, error) {
	if record == nil || record.SeriesRevision == nil ||
		record.Event.ResultReason != string(domain.GameResultReasonSurrender) ||
		record.Event.WinnerID.UUID == resolved.Authority.ParticipantID ||
		record.SeriesRevision.ResultState != string(domain.SeriesStateCompleted) ||
		record.SeriesRevision.ResultReason != string(domain.SeriesResultReasonScoreComplete) ||
		!record.SeriesRevision.WinnerID.Valid ||
		record.SeriesRevision.WinnerID.UUID != record.Event.WinnerID.UUID {
		return usecase.OfficialResultView{}, gameusecase.ErrForfeitCommandReuse
	}
	view := usecase.OfficialResultView{
		ID:           domain.OfficialResultRevisionID(record.SeriesRevision.ID),
		Ordinal:      int(record.SeriesRevision.RevisionNumber),
		CommandID:    record.Commit.IdempotencyKey,
		TournamentID: record.Commit.TournamentID, SeriesID: record.Commit.SeriesID,
		Actor:                      domain.ResultActor{Kind: domain.ResultActorServer},
		ScoreRevisionID:            domain.SeriesScoreRevisionID(record.Commit.SeriesScoreRevisionID),
		SourceProjectionRevisionID: resolved.Authority.ProjectionRevisionID,
		SeriesState:                domain.SeriesState(record.SeriesRevision.ResultState),
		SeriesReason:               domain.SeriesResultReason(record.SeriesRevision.ResultReason),
		RecordedAt:                 record.SeriesRevision.CreatedAt.Time.Round(0).UTC(),
	}
	winnerID := record.SeriesRevision.WinnerID.UUID
	view.WinnerID = &winnerID
	if record.SeriesRevision.PreviousRevisionID.Valid {
		previous := domain.OfficialResultRevisionID(record.SeriesRevision.PreviousRevisionID.UUID)
		view.PreviousRevisionID = &previous
	}
	return view, nil
}

func participantSurrenderDigest(resolution gameusecase.ForfeitResolution) [sha256.Size]byte {
	payload := fmt.Sprintf(
		"%s:%s:%s:%s:%d:%d",
		resolution.Scope.TournamentID,
		resolution.Scope.SeriesID,
		resolution.GameRevision.ID.UUID(),
		resolution.SeriesRevision.ID.UUID(),
		resolution.ScoreRevision.ScoreAfter.FirstParticipantWins,
		resolution.ScoreRevision.ScoreAfter.SecondParticipantWins,
	)
	return sha256.Sum256([]byte(payload))
}

func participantForfeitLookupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return fmt.Errorf("ParticipantForfeitRepository - %s: %w", operation, err)
}

func cloneParticipantResultRevisionPointer(
	value *domain.OfficialResultRevisionID,
) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

var (
	_ gameusecase.ForfeitRepository           = (*ParticipantForfeitRepository)(nil)
	_ tournamentparticipant.SurrenderWorkflow = (*ParticipantSurrenderWorkflow)(nil)
)
