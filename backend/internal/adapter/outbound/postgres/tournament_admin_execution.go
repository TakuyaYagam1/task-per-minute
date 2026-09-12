package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

type TournamentAdminExecutionPostgres struct {
	tx    *TxManager
	swiss *SwissPostgres
	waves *WavePostgres
}

func NewTournamentAdminExecutionPostgres(tx *TxManager) *TournamentAdminExecutionPostgres {
	return &TournamentAdminExecutionPostgres{
		tx: tx, swiss: NewSwissPostgres(tx), waves: NewWavePostgres(tx),
	}
}

func (r *TournamentAdminExecutionPostgres) LockPairingAuthority(
	ctx context.Context,
	tournamentID uuid.UUID,
) (tournamentadmin.PairingAuthority, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) || tournamentID == uuid.Nil {
		return tournamentadmin.PairingAuthority{}, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	header, err := querier.LockTournamentPairingAuthority(ctx, tournamentID)
	if err != nil {
		return tournamentadmin.PairingAuthority{}, r.pairingAuthorityError(ctx, tournamentID, err)
	}
	participants, err := querier.LockTournamentPairingParticipants(ctx, header.RosterID)
	if err != nil {
		return tournamentadmin.PairingAuthority{}, fmt.Errorf("lock pairing participants: %w", err)
	}
	history, err := querier.LockTournamentPairingHistory(ctx, header.RosterID)
	if err != nil {
		return tournamentadmin.PairingAuthority{}, fmt.Errorf("lock pairing history: %w", err)
	}
	byes, err := querier.LockTournamentPairingByes(ctx, header.RosterID)
	if err != nil {
		return tournamentadmin.PairingAuthority{}, fmt.Errorf("lock pairing byes: %w", err)
	}
	rounds, err := querier.LockTournamentPairingRounds(ctx, header.RosterID)
	if err != nil {
		return tournamentadmin.PairingAuthority{}, fmt.Errorf("lock pairing rounds: %w", err)
	}
	waves, err := querier.LockTournamentPairingWaves(ctx, header.RosterID)
	if err != nil {
		return tournamentadmin.PairingAuthority{}, fmt.Errorf("lock pairing Waves: %w", err)
	}
	authority, err := tournamentAdminPairingAuthority(header, participants, history, byes, rounds, waves)
	if err != nil {
		return tournamentadmin.PairingAuthority{}, fmt.Errorf("map pairing authority: %w", err)
	}
	return authority, nil
}

func (r *TournamentAdminExecutionPostgres) FindPairingCommand(
	ctx context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*tournamentadmin.PairingCommandRecord, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) || tournamentID == uuid.Nil || commandID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).FindSwissPairingCommand(
		ctx, sqlc.FindSwissPairingCommandParams{TournamentID: tournamentID, CommandID: commandID},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminExecutionPostgres - FindPairingCommand: %w", err)
	}
	return tournamentAdminPairingCommand(row)
}

func (r *TournamentAdminExecutionPostgres) ReadExecutionTime(ctx context.Context) (time.Time, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) {
		return time.Time{}, domain.ErrValidation
	}
	observed, err := r.tx.Querier(ctx).ReadTournamentExecutionTime(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("TournamentAdminExecutionPostgres - ReadExecutionTime: %w", err)
	}
	if !observed.Valid || !validServerTime(observed.Time.UTC()) {
		return time.Time{}, domain.ErrInternal
	}
	return observed.Time.UTC(), nil
}

//nolint:gocyclo // Pairing mode dispatch and its transactional persistence intentionally share one boundary.
func (r *TournamentAdminExecutionPostgres) CommitPairing(
	ctx context.Context,
	plan tournamentadmin.PairingPlan,
) (tournamentadmin.SwissRoundView, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) {
		return tournamentadmin.SwissRoundView{}, domain.ErrValidation
	}
	if len(plan.Pairs) == 0 || len(plan.PairingIDs) != len(plan.Pairs) ||
		len(plan.SeriesIDs) != len(plan.Pairs) || len(plan.InitialScoreRevisionIDs) != len(plan.Pairs) {
		return tournamentadmin.SwissRoundView{}, domain.ErrValidation
	}
	meta, err := tournamentAdminSwissRoundMeta(plan)
	if err != nil {
		return tournamentadmin.SwissRoundView{}, err
	}
	var saved *SwissRoundRecord
	switch plan.Command.PairingMode {
	case tournamentadmin.PairingModeAutomatic:
		if plan.Automatic == nil {
			return tournamentadmin.SwissRoundView{}, domain.ErrValidation
		}
		saved, err = r.swiss.SaveAutomaticRound(ctx, AutomaticSwissRoundInput{
			Meta: meta, Pairing: *plan.Automatic,
		}, nil)
	case tournamentadmin.PairingModeManual:
		manual := swissusecase.ManualRound{
			ID: plan.RoundID, Pairings: append([]swissusecase.Pair(nil), plan.Pairs...),
			ByeParticipantID: tournamentAdminByeParticipantID(plan.Bye),
		}
		saved, err = r.swiss.SaveManualRound(ctx, ManualSwissRoundInput{
			Meta: meta, Round: manual, PairingInputs: tournamentAdminManualPairingInputs(plan.Pairs),
			Override: plan.Override,
		}, nil)
	default:
		return tournamentadmin.SwissRoundView{}, domain.ErrValidation
	}
	if err != nil {
		return tournamentadmin.SwissRoundView{}, err
	}
	series := make([]WaveSeriesInput, len(plan.Pairs))
	for index, pair := range plan.Pairs {
		series[index] = WaveSeriesInput{
			ID: plan.SeriesIDs[index], FirstParticipantID: pair.FirstParticipantID,
			SecondParticipantID: pair.SecondParticipantID, Format: domain.SeriesFormatBO1,
			InitialScoreRevisionID: plan.InitialScoreRevisionIDs[index],
		}
	}
	if _, err := r.waves.Create(ctx, WaveCreateInput{
		ID: plan.WaveID, TournamentID: plan.Command.TournamentID, RosterID: plan.Authority.RosterID,
		RevisionID: plan.WaveRevisionID, ParticipantIDs: pairingAuthorityParticipantIDs(plan.Authority),
		CommandID: plan.Command.CommandID, SourceProjectionRevisionID: plan.Authority.ProjectionRevisionID,
		SourceProjectionRevision: plan.Authority.ProjectionRevision,
		Series:                   series, CreatedAt: plan.DecidedAt,
	}); err != nil {
		return tournamentadmin.SwissRoundView{}, err
	}
	linkID, err := r.tx.Querier(ctx).CreateSwissWaveLink(ctx, sqlc.CreateSwissWaveLinkParams{
		WaveID: plan.WaveID, TournamentID: plan.Command.TournamentID, RosterID: plan.Authority.RosterID,
		RoundID: plan.RoundID, ByeParticipantID: nullableSwissByeParticipant(plan.Bye),
		ByeRevisionID: nullableSwissByeRevision(plan), CreatedAt: tstz(plan.DecidedAt),
	})
	if err != nil {
		return tournamentadmin.SwissRoundView{}, executionWriteError("link Swiss Wave", err)
	}
	if linkID != plan.WaveID {
		return tournamentadmin.SwissRoundView{}, domain.ErrInternal
	}
	if plan.Command.CategoryMode == domain.CategoryModeRandom {
		if err := r.materializeSwissRandomBO1(ctx, plan); err != nil {
			return tournamentadmin.SwissRoundView{}, err
		}
	}
	return tournamentAdminSwissRoundView(plan, saved)
}

func (r *TournamentAdminExecutionPostgres) SavePairingCommand(
	ctx context.Context,
	record tournamentadmin.PairingCommandRecord,
) error {
	if !validTournamentAdminExecutionRepository(ctx, r) ||
		record.RoundNumber < 1 || record.RoundNumber > math.MaxInt16 {
		return domain.ErrValidation
	}
	var view tournamentadmin.SwissRoundView
	//nolint:musttag // ResultDocument is validated as an application-owned SwissRoundView below.
	if err := json.Unmarshal(record.ResultDocument, &view); err != nil || view.ID == uuid.Nil {
		return domain.ErrValidation
	}
	categories, err := json.Marshal(record.Categories)
	if err != nil {
		return fmt.Errorf("encode pairing categories: %w", err)
	}
	roundNumber := int16(record.RoundNumber)
	created, err := r.tx.Querier(ctx).CreateSwissPairingCommand(
		ctx,
		sqlc.CreateSwissPairingCommandParams{
			CommandID: record.CommandID, TournamentID: record.TournamentID, RosterID: record.RosterID,
			RoundID: view.ID, ActorID: record.Operator.ActorID, RoundNumber: roundNumber,
			PairingMode: string(record.Mode), CategoryMode: string(record.CategoryMode),
			Categories: categories, SourceProjectionRevisionID: record.SourceProjectionRevisionID,
			SourceProjectionRevision: record.SourceProjectionRevision,
			SourceTournamentRevision: record.SourceTournamentRevision,
			SourceRosterRevision:     record.SourceRosterRevision, SourceHistoryRevision: record.SourceHistoryRevision,
			RequestDigest:  append([]byte(nil), record.RequestDigest[:]...),
			ResultDocument: append([]byte(nil), record.ResultDocument...), ExecutedAt: tstz(record.ExecutedAt),
		},
	)
	if err != nil {
		return executionWriteError("save pairing command", err)
	}
	if created != record.CommandID {
		return domain.ErrInternal
	}
	return nil
}

func (r *TournamentAdminExecutionPostgres) LockWaveAuthority(
	ctx context.Context,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
) (tournamentadmin.WaveAuthority, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) || tournamentID == uuid.Nil || waveID == uuid.Nil {
		return tournamentadmin.WaveAuthority{}, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	if err := lockTournamentResultScope(ctx, querier, tournamentID, uuid.Nil); err != nil {
		return tournamentadmin.WaveAuthority{}, r.waveAuthorityError(ctx, tournamentID, err)
	}
	header, err := querier.LockTournamentAdminWaveAuthority(
		ctx, sqlc.LockTournamentAdminWaveAuthorityParams{TournamentID: tournamentID, WaveID: waveID},
	)
	if err != nil {
		return tournamentadmin.WaveAuthority{}, r.waveAuthorityError(ctx, tournamentID, err)
	}
	members, err := querier.LockTournamentAdminWaveMembers(ctx, waveID)
	if err != nil {
		return tournamentadmin.WaveAuthority{}, fmt.Errorf("lock Wave members: %w", err)
	}
	series, err := querier.LockTournamentAdminWaveSeries(ctx, waveID)
	if err != nil {
		return tournamentadmin.WaveAuthority{}, fmt.Errorf("lock Wave Series: %w", err)
	}
	games, err := querier.LockTournamentAdminWaveGames(ctx, waveID)
	if err != nil {
		return tournamentadmin.WaveAuthority{}, fmt.Errorf("lock Wave Games: %w", err)
	}
	assignments, err := querier.LockTournamentAdminWaveAssignments(ctx, waveID)
	if err != nil {
		return tournamentadmin.WaveAuthority{}, fmt.Errorf("lock Wave assignments: %w", err)
	}
	deliveries, err := querier.LockTournamentAdminWaveDeliveries(ctx, waveID)
	if err != nil {
		return tournamentadmin.WaveAuthority{}, fmt.Errorf("lock Wave deliveries: %w", err)
	}
	authority, err := tournamentAdminWaveAuthority(header, members, series, games, assignments, deliveries)
	if err != nil {
		return tournamentadmin.WaveAuthority{}, fmt.Errorf("map Wave authority: %w", err)
	}
	return authority, nil
}

func (r *TournamentAdminExecutionPostgres) FindWaveCommand(
	ctx context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*tournamentadmin.WaveCommandRecord, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) || tournamentID == uuid.Nil || commandID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).FindWaveControlCommand(
		ctx, sqlc.FindWaveControlCommandParams{TournamentID: tournamentID, CommandID: commandID},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminExecutionPostgres - FindWaveCommand: %w", err)
	}
	return tournamentAdminWaveCommand(row)
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func (r *TournamentAdminExecutionPostgres) CommitWave(
	ctx context.Context,
	mutation tournamentadmin.WaveMutation,
) (tournamentadmin.WaveView, error) {
	if !validTournamentAdminExecutionRepository(ctx, r) || mutation.Next.Validate() != nil ||
		(mutation.Command.Action == tournamentadmin.WaveActionResume &&
			(mutation.ExecutionAuthority.Validate() != nil ||
				mutation.ExecutionAuthority.TournamentID != mutation.Command.TournamentID)) {
		return tournamentadmin.WaveView{}, fmt.Errorf("validate Wave mutation: %w", domain.ErrValidation)
	}
	var err error
	switch mutation.Command.Action {
	case tournamentadmin.WaveActionOpenReadyWindow:
		err = r.openTournamentAdminWave(ctx, mutation)
	case tournamentadmin.WaveActionStart:
		err = r.startTournamentAdminWave(ctx, mutation)
	case tournamentadmin.WaveActionPause:
		err = r.pauseTournamentAdminWave(ctx, mutation)
	case tournamentadmin.WaveActionResume:
		err = r.resumeTournamentAdminWave(ctx, mutation)
	case tournamentadmin.WaveActionComplete:
		err = r.completeTournamentAdminWave(ctx, mutation)
	case tournamentadmin.WaveActionCancel:
		err = r.cancelTournamentAdminWave(ctx, mutation)
	default:
		return tournamentadmin.WaveView{}, domain.ErrValidation
	}
	if err != nil {
		return tournamentadmin.WaveView{}, err
	}
	current, err := r.LockWaveAuthority(ctx, mutation.Command.TournamentID, mutation.Command.WaveID)
	if err != nil {
		return tournamentadmin.WaveView{}, err
	}
	if current.View.Revision != mutation.Authority.View.Revision+1 ||
		current.View.Wave.State != mutation.Next.State {
		return tournamentadmin.WaveView{}, fmt.Errorf("validate persisted Wave mutation: %w", domain.ErrInternal)
	}
	return current.View, nil
}

func (r *TournamentAdminExecutionPostgres) SaveWaveCommand(
	ctx context.Context,
	record tournamentadmin.WaveCommandRecord,
) error {
	if !validTournamentAdminExecutionRepository(ctx, r) {
		return domain.ErrValidation
	}
	//nolint:musttag // SourceRevisions is an application-owned command evidence document.
	sourceRevisions, err := json.Marshal(record.SourceRevisions)
	if err != nil {
		return fmt.Errorf("encode Wave source revisions: %w", err)
	}
	//nolint:musttag // SourceGraph is an application-owned command evidence document.
	sourceGraph, err := json.Marshal(record.SourceGraph)
	if err != nil {
		return fmt.Errorf("encode Wave source graph: %w", err)
	}
	created, err := r.tx.Querier(ctx).CreateWaveControlCommand(ctx, sqlc.CreateWaveControlCommandParams{
		CommandID: record.CommandID, TournamentID: record.TournamentID, RosterID: record.RosterID,
		WaveID: record.WaveID, ActorID: record.Operator.ActorID, Action: string(record.Action),
		SourceProjectionRevisionID: record.SourceProjectionRevisionID,
		SourceProjectionRevision:   record.SourceProjectionRevision,
		SourceTournamentRevision:   record.SourceTournamentRevision,
		SourceRosterRevision:       record.SourceRosterRevision, SourceWaveRevision: record.SourceWaveRevision,
		ResultingWaveRevision: record.ResultingWaveRevision, SourceRevisions: sourceRevisions,
		SourceGraph: sourceGraph, RequestDigest: append([]byte(nil), record.RequestDigest[:]...),
		Reason: optionalExecutionReason(record.Reason), ResultDocument: append([]byte(nil), record.ResultDocument...),
		ExecutedAt: tstz(record.ExecutedAt),
	})
	if err != nil {
		return executionWriteError("save Wave command", err)
	}
	if created != record.CommandID {
		return domain.ErrInternal
	}
	return nil
}

func (r *TournamentAdminExecutionPostgres) pairingAuthorityError(
	ctx context.Context,
	tournamentID uuid.UUID,
	err error,
) error {
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("TournamentAdminExecutionPostgres - lock pairing authority: %w", err)
	}
	if _, lookupErr := r.tx.Querier(ctx).GetTournament(ctx, tournamentID); errors.Is(lookupErr, pgx.ErrNoRows) {
		return domain.ErrTournamentNotFound
	} else if lookupErr != nil {
		return fmt.Errorf("resolve missing pairing authority: %w", lookupErr)
	}
	return domain.ErrTournamentProjectionNotFound
}

func (r *TournamentAdminExecutionPostgres) waveAuthorityError(
	ctx context.Context,
	tournamentID uuid.UUID,
	err error,
) error {
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("TournamentAdminExecutionPostgres - lock Wave authority: %w", err)
	}
	if _, lookupErr := r.tx.Querier(ctx).GetTournament(ctx, tournamentID); errors.Is(lookupErr, pgx.ErrNoRows) {
		return domain.ErrTournamentNotFound
	} else if lookupErr != nil {
		return fmt.Errorf("resolve missing Wave authority: %w", lookupErr)
	}
	return ErrWaveNotFound
}

func validTournamentAdminExecutionRepository(
	ctx context.Context,
	repository *TournamentAdminExecutionPostgres,
) bool {
	return ctx != nil && repository != nil && repository.tx != nil && repository.swiss != nil &&
		repository.waves != nil
}

func tournamentAdminSwissRoundMeta(plan tournamentadmin.PairingPlan) (SwissRoundMeta, error) {
	if plan.RoundID == uuid.Nil || plan.Authority.RosterID == uuid.Nil || len(plan.Pairs) == 0 ||
		len(plan.PairingIDs) != len(plan.Pairs) || len(plan.SeriesIDs) != len(plan.Pairs) ||
		plan.Command.RoundNumber < 1 || plan.Command.RoundNumber > math.MaxInt16 ||
		!validServerTime(plan.DecidedAt) {
		return SwissRoundMeta{}, domain.ErrValidation
	}
	counts := make([]int16, len(plan.Pairs))
	for index, pair := range plan.Pairs {
		count := plan.Authority.PriorMeetingCounts[swissusecase.NewPairKey(
			pair.FirstParticipantID, pair.SecondParticipantID,
		)]
		if count < 0 || count > math.MaxInt16 {
			return SwissRoundMeta{}, domain.ErrValidation
		}
		counts[index] = int16(count)
	}
	return SwissRoundMeta{
		ID: plan.RoundID, RosterID: plan.Authority.RosterID, RoundNumber: int16(plan.Command.RoundNumber),
		SourceRosterRevision:  plan.Authority.RosterRevision,
		SourceHistoryRevision: plan.Authority.HistoryRevision,
		PairingIDs:            append([]uuid.UUID(nil), plan.PairingIDs...), PriorMeetingCounts: counts,
		Bye: plan.Bye, GeneratedAt: plan.DecidedAt, RecordedAt: plan.DecidedAt,
	}, nil
}

func executionWriteError(operation string, err error) error {
	return mapRepositoryWriteError("TournamentAdminExecutionPostgres - "+operation, err)
}

var _ tournamentadmin.ExecutionWorkflowRepository = (*TournamentAdminExecutionPostgres)(nil)
