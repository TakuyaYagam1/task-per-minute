package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	tournamentpause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/pause"
)

func (r *TournamentAdminLifecyclePostgres) GetTournament(
	ctx context.Context,
	id uuid.UUID,
) (*tournamentpause.PauseTournamentRecord, error) {
	if !validTournamentAdminLifecycleRepository(ctx, r) || id == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).GetTournamentSummary(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tournamentpause.PauseErrTournamentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminLifecyclePostgres - get tournament: %w", err)
	}
	record, err := tournamentPauseSummaryRecord(row)
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminLifecyclePostgres - get tournament - map: %w", err)
	}
	return record, nil
}

func (r *TournamentAdminLifecyclePostgres) GetTournamentTechnicalPause(
	ctx context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*tournamentpause.TournamentTechnicalPauseRecord, error) {
	if !validTournamentAdminLifecycleRepository(ctx, r) || tournamentID == uuid.Nil || commandID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).FindTournamentLifecycleCommand(
		ctx,
		sqlc.FindTournamentLifecycleCommandParams{TournamentID: tournamentID, CommandID: commandID},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tournamentpause.PauseErrTournamentNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminLifecyclePostgres - get technical pause: %w", err)
	}
	command, err := tournamentAdminLifecycleCommand(row)
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminLifecyclePostgres - get technical pause - map command: %w", err)
	}
	if command.Action != tournamentadmin.TournamentActionPause || command.PauseID == uuid.Nil ||
		command.Result.State != domain.TournamentStateTechnicalPause || command.Result.PausedFromState == nil {
		return nil, domain.ErrConflict
	}
	return &tournamentpause.TournamentTechnicalPauseRecord{
		Tournament: tournamentpause.PauseTournamentRecord{
			ID: command.Result.ID, State: command.Result.State,
			PausedFromState: lifecycleTournamentStatePointer(command.Result.PausedFromState),
			Revision:        command.Result.Revision, UpdatedAt: command.Result.UpdatedAt,
		},
		CommandID: command.CommandID, PauseID: command.PauseID, ActorID: command.Operator.ActorID,
		Reason: command.Reason, PausedAt: command.ExecutedAt,
	}, nil
}

func (r *TournamentAdminLifecyclePostgres) InspectTournamentPauseAdmission(
	ctx context.Context,
	tournamentID uuid.UUID,
) (*tournamentpause.TournamentPauseAdmission, error) {
	if !validTournamentAdminLifecycleRepository(ctx, r) || tournamentID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	authority, err := r.LockLifecycleAuthority(ctx, tournamentID)
	if err != nil {
		return nil, err
	}
	snapshot, err := r.LockExecutionSnapshot(ctx, authority)
	if err != nil {
		return nil, err
	}
	return &tournamentpause.TournamentPauseAdmission{
		TournamentID: tournamentID, GraphRevision: snapshot.GraphRevision,
		ExpectedChildren: snapshot.ExpectedChildren, ObservedChildren: snapshot.ObservedChildren,
		ActiveGolden: snapshot.ActiveGolden,
		Complete:     snapshot.IncompleteChildren == 0 && snapshot.ExpectedChildren == snapshot.ObservedChildren,
	}, nil
}

func (r *TournamentAdminLifecyclePostgres) EnterTournamentTechnicalPause(
	ctx context.Context,
	input tournamentpause.TournamentTechnicalPauseInput,
) (*tournamentpause.TournamentTechnicalPauseRecord, bool, error) {
	if !validTournamentAdminLifecycleRepository(ctx, r) || !validTournamentTechnicalPauseInput(input) {
		return nil, false, domain.ErrValidation
	}
	var record *tournamentpause.TournamentTechnicalPauseRecord
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		record, err = r.enterTournamentTechnicalPause(txCtx, input)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return record, true, nil
}

func (r *TournamentAdminLifecyclePostgres) enterTournamentTechnicalPause(
	ctx context.Context,
	input tournamentpause.TournamentTechnicalPauseInput,
) (*tournamentpause.TournamentTechnicalPauseRecord, error) {
	authority, snapshot, err := r.prepareTournamentTechnicalPause(ctx, input)
	if err != nil {
		return nil, err
	}
	pauseRevisionID := tournamentPauseRevisionID(input.PauseID)
	querier := r.tx.Querier(ctx)
	if err := createTournamentPause(ctx, querier, input, authority, snapshot, pauseRevisionID); err != nil {
		return nil, err
	}
	if err := createTournamentPauseRevision(ctx, querier, input, authority, pauseRevisionID); err != nil {
		return nil, err
	}
	return transitionTournamentToPause(ctx, querier, input, authority, snapshot, pauseRevisionID)
}

func (r *TournamentAdminLifecyclePostgres) prepareTournamentTechnicalPause(
	ctx context.Context,
	input tournamentpause.TournamentTechnicalPauseInput,
) (tournamentadmin.LifecycleAuthority, tournamentadmin.LifecycleExecutionSnapshot, error) {
	authority, err := r.LockLifecycleAuthority(ctx, input.TournamentID)
	if err != nil {
		return tournamentadmin.LifecycleAuthority{}, tournamentadmin.LifecycleExecutionSnapshot{}, err
	}
	if authority.Tournament.Revision != input.ExpectedRevision || authority.Tournament.State != input.ExpectedState ||
		authority.ProjectionRevision != input.GraphRevision {
		return tournamentadmin.LifecycleAuthority{}, tournamentadmin.LifecycleExecutionSnapshot{}, domain.ErrConflict
	}
	snapshot, err := r.LockExecutionSnapshot(ctx, authority)
	if err != nil {
		return tournamentadmin.LifecycleAuthority{}, tournamentadmin.LifecycleExecutionSnapshot{}, err
	}
	if snapshot.GraphRevision != input.GraphRevision || snapshot.ExpectedChildren != snapshot.ObservedChildren ||
		snapshot.IncompleteChildren != 0 || snapshot.ActiveGolden {
		return tournamentadmin.LifecycleAuthority{}, tournamentadmin.LifecycleExecutionSnapshot{}, domain.ErrConflict
	}
	return authority, snapshot, nil
}

func createTournamentPause(
	ctx context.Context,
	querier *sqlc.Queries,
	input tournamentpause.TournamentTechnicalPauseInput,
	authority tournamentadmin.LifecycleAuthority,
	snapshot tournamentadmin.LifecycleExecutionSnapshot,
	pauseRevisionID uuid.UUID,
) error {
	pauseID, err := querier.CreateTournamentTechnicalPause(
		ctx,
		sqlc.CreateTournamentTechnicalPauseParams{
			PauseID: input.PauseID, RosterID: authority.Tournament.RosterID,
			PauseRevisionID: pauseRevisionID, PausedAt: tstz(input.PausedAt),
			TournamentID: input.TournamentID, ExpectedTournamentRevision: input.ExpectedRevision,
			ExpectedTournamentState:    string(input.ExpectedState),
			ExpectedProjectionRevision: input.GraphRevision, ExecutionSnapshot: snapshot.Document,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return lifecycleWriteError("create technical pause", err)
	}
	if pauseID != input.PauseID {
		return domain.ErrInternal
	}
	return nil
}

func createTournamentPauseRevision(
	ctx context.Context,
	querier *sqlc.Queries,
	input tournamentpause.TournamentTechnicalPauseInput,
	authority tournamentadmin.LifecycleAuthority,
	pauseRevisionID uuid.UUID,
) error {
	revisionPauseID, err := querier.CreateTournamentTechnicalPauseRevision(
		ctx,
		sqlc.CreateTournamentTechnicalPauseRevisionParams{
			PauseRevisionID: pauseRevisionID, PausedAt: tstz(input.PausedAt),
			PauseID: input.PauseID, TournamentID: input.TournamentID,
			RosterID: authority.Tournament.RosterID,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return lifecycleWriteError("create technical pause revision", err)
	}
	if revisionPauseID != input.PauseID {
		return domain.ErrInternal
	}
	return nil
}

func transitionTournamentToPause(
	ctx context.Context,
	querier *sqlc.Queries,
	input tournamentpause.TournamentTechnicalPauseInput,
	authority tournamentadmin.LifecycleAuthority,
	snapshot tournamentadmin.LifecycleExecutionSnapshot,
	pauseRevisionID uuid.UUID,
) (*tournamentpause.TournamentTechnicalPauseRecord, error) {
	updated, err := querier.EnterTournamentTechnicalPause(
		ctx,
		sqlc.EnterTournamentTechnicalPauseParams{
			PausedAt: tstz(input.PausedAt), ExpectedTournamentRevision: input.ExpectedRevision,
			ExpectedTournamentState: string(input.ExpectedState), PauseID: input.PauseID,
			PauseRevisionID: pauseRevisionID, TournamentID: input.TournamentID,
			RosterID: authority.Tournament.RosterID, ExpectedProjectionRevision: input.GraphRevision,
			ExecutionSnapshot: snapshot.Document,
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrConflict
	}
	if err != nil {
		return nil, lifecycleWriteError("enter technical pause", err)
	}
	return tournamentTechnicalPauseRecord(updated, input)
}

func tournamentPauseSummaryRecord(row sqlc.GetTournamentSummaryRow) (*tournamentpause.PauseTournamentRecord, error) {
	updatedAt, ok := lifecycleRequiredTime(row.UpdatedAt.Valid, row.UpdatedAt.Time)
	state := domain.TournamentState(row.State)
	pausedFromState := lifecycleTournamentState(row.PausedFromState)
	if !ok || row.ID == uuid.Nil || row.Revision < 1 ||
		(domain.Tournament{State: state, PausedFromState: pausedFromState}).Validate() != nil {
		return nil, domain.ErrInternal
	}
	return &tournamentpause.PauseTournamentRecord{
		ID: row.ID, State: state, PausedFromState: pausedFromState,
		Revision: row.Revision, UpdatedAt: updatedAt,
	}, nil
}

func tournamentTechnicalPauseRecord(
	row sqlc.EnterTournamentTechnicalPauseRow,
	input tournamentpause.TournamentTechnicalPauseInput,
) (*tournamentpause.TournamentTechnicalPauseRecord, error) {
	updatedAt, ok := lifecycleRequiredTime(row.UpdatedAt.Valid, row.UpdatedAt.Time)
	state := domain.TournamentState(row.State)
	pausedFromState := lifecycleTournamentState(row.PausedFromState)
	if !ok || row.ID != input.TournamentID || state != domain.TournamentStateTechnicalPause ||
		pausedFromState == nil || *pausedFromState != input.ExpectedState || row.Revision != input.ExpectedRevision+1 ||
		!updatedAt.Equal(input.PausedAt) {
		return nil, domain.ErrInternal
	}
	return &tournamentpause.TournamentTechnicalPauseRecord{
		Tournament: tournamentpause.PauseTournamentRecord{
			ID: row.ID, State: state, PausedFromState: pausedFromState,
			Revision: row.Revision, UpdatedAt: updatedAt,
		},
		CommandID: input.CommandID, PauseID: input.PauseID, ActorID: input.ActorID,
		Reason: input.Reason, PausedAt: input.PausedAt,
	}, nil
}

func validTournamentTechnicalPauseInput(input tournamentpause.TournamentTechnicalPauseInput) bool {
	reason := strings.TrimSpace(input.Reason)
	state := domain.Tournament{State: input.ExpectedState}
	return input.TournamentID != uuid.Nil && input.ExpectedRevision >= 1 && input.GraphRevision >= 1 &&
		input.CommandID != uuid.Nil && input.PauseID != uuid.Nil && input.ActorID != uuid.Nil &&
		reason != "" && reason == input.Reason && len(reason) <= maxTournamentLifecycleReasonLength &&
		domain.IsValidServerTime(input.PausedAt) && state.CanTransitionTo(domain.TournamentStateTechnicalPause)
}

func tournamentPauseRevisionID(pauseID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(pauseID, []byte("tournament-pause:revision:1"))
}

func lifecycleTournamentStatePointer(value *domain.TournamentState) *domain.TournamentState {
	if value == nil {
		return nil
	}
	state := *value
	return &state
}

var _ tournamentpause.TournamentPauseRepository = (*TournamentAdminLifecyclePostgres)(nil)
