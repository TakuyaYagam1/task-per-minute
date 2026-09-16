package lifecycle

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

type TournamentAdminLifecyclePostgres struct {
	tx *db.TxManager
}

func NewTournamentAdminLifecyclePostgres(tx *db.TxManager) *TournamentAdminLifecyclePostgres {
	return &TournamentAdminLifecyclePostgres{tx: tx}
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func (r *TournamentAdminLifecyclePostgres) LockLifecycleAuthority(
	ctx context.Context,
	tournamentID uuid.UUID,
) (tournamentadmin.LifecycleAuthority, error) {
	if !validTournamentAdminLifecycleRepository(ctx, r) || tournamentID == uuid.Nil {
		return tournamentadmin.LifecycleAuthority{}, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	rosterID, err := querier.LockTournamentLifecycleScope(ctx, tournamentID)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, lookupErr := querier.GetTournament(ctx, tournamentID); errors.Is(lookupErr, pgx.ErrNoRows) {
			return tournamentadmin.LifecycleAuthority{}, domain.ErrTournamentNotFound
		} else if lookupErr != nil {
			return tournamentadmin.LifecycleAuthority{}, fmt.Errorf(
				"TournamentAdminLifecyclePostgres - lock scope - find tournament: %w",
				lookupErr,
			)
		}
		return tournamentadmin.LifecycleAuthority{}, fmt.Errorf(
			"TournamentAdminLifecyclePostgres - lock scope - missing roster: %w",
			domain.ErrInternal,
		)
	}
	if err != nil {
		return tournamentadmin.LifecycleAuthority{}, fmt.Errorf(
			"TournamentAdminLifecyclePostgres - lock scope: %w",
			err,
		)
	}
	if rosterID == uuid.Nil {
		return tournamentadmin.LifecycleAuthority{}, domain.ErrInternal
	}
	row, err := querier.LockTournamentLifecycleAuthority(ctx, tournamentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return tournamentadmin.LifecycleAuthority{}, domain.ErrTournamentProjectionNotFound
	}
	if err != nil {
		return tournamentadmin.LifecycleAuthority{}, fmt.Errorf(
			"TournamentAdminLifecyclePostgres - lock authority: %w",
			err,
		)
	}
	if row.RosterID != rosterID {
		return tournamentadmin.LifecycleAuthority{}, domain.ErrInternal
	}
	authority, err := tournamentAdminLifecycleAuthority(row)
	if err != nil {
		return tournamentadmin.LifecycleAuthority{}, fmt.Errorf(
			"TournamentAdminLifecyclePostgres - lock authority - map: %w",
			err,
		)
	}
	roster, err := querier.GetTournamentAdminRoster(ctx, tournamentID)
	if err != nil {
		return tournamentadmin.LifecycleAuthority{}, fmt.Errorf(
			"TournamentAdminLifecyclePostgres - lock authority - load roster: %w",
			err,
		)
	}
	if roster.ID != rosterID || roster.TournamentID != tournamentID {
		return tournamentadmin.LifecycleAuthority{}, domain.ErrInternal
	}
	participants, err := querier.ListTournamentAdminRosterParticipants(ctx, tournamentID)
	if err != nil {
		return tournamentadmin.LifecycleAuthority{}, fmt.Errorf(
			"TournamentAdminLifecyclePostgres - lock authority - list participants: %w",
			err,
		)
	}
	reservations, err := querier.ListTournamentReservations(ctx, tournamentID)
	if err != nil {
		return tournamentadmin.LifecycleAuthority{}, fmt.Errorf(
			"TournamentAdminLifecyclePostgres - lock authority - list reservations: %w",
			err,
		)
	}
	authority.RosterLocked = roster.LockedAt.Valid
	authority.RosterReadyForSwiss = lifecycleRosterReadyForSwiss(roster, participants, reservations)
	return authority, nil
}

func (r *TournamentAdminLifecyclePostgres) FindLifecycleCommand(
	ctx context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*tournamentadmin.LifecycleCommandRecord, error) {
	if !validTournamentAdminLifecycleRepository(ctx, r) || tournamentID == uuid.Nil || commandID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).FindTournamentLifecycleCommand(
		ctx,
		sqlc.FindTournamentLifecycleCommandParams{TournamentID: tournamentID, CommandID: commandID},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminLifecyclePostgres - find command: %w", err)
	}
	record, err := tournamentAdminLifecycleCommand(row)
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminLifecyclePostgres - find command - map: %w", err)
	}
	return record, nil
}

func (r *TournamentAdminLifecyclePostgres) ResumeTechnicalPause(
	ctx context.Context,
	input tournamentadmin.LifecycleResumeInput,
) (tournamentadmin.LifecycleResumeResult, error) {
	if !validTournamentAdminLifecycleRepository(ctx, r) || !validLifecycleResumeInput(input) {
		return tournamentadmin.LifecycleResumeResult{}, domain.ErrValidation
	}

	var result tournamentadmin.LifecycleResumeResult
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		resumed, err := querier.ResumeTournamentTechnicalPause(
			txCtx,
			sqlc.ResumeTournamentTechnicalPauseParams{
				TournamentID: input.TournamentID, RosterID: input.RosterID,
				ExpectedTournamentRevision: input.ExpectedTournamentRevision,
				ExpectedProjectionRevision: input.ExpectedProjectionRevision,
				ExecutionSnapshot:          input.ExecutionSnapshot, PauseRevisionID: input.PauseRevisionID,
				Reason: lifecycleReasonPointer(input.Reason), ResumedAt: tstz(input.ResumedAt),
			},
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrConflict
		}
		if err != nil {
			return lifecycleWriteError("resume technical pause", err)
		}
		if resumed.ID != input.TournamentID || resumed.PauseID == uuid.Nil || resumed.SourcePauseCommandID == uuid.Nil {
			return domain.ErrInternal
		}
		summary, err := querier.GetTournamentSummary(txCtx, input.TournamentID)
		if err != nil {
			return fmt.Errorf("TournamentAdminLifecyclePostgres - resume technical pause - load result: %w", err)
		}
		tournament, err := tournamentLifecycleSummaryRecord(summary)
		if err != nil {
			return fmt.Errorf("TournamentAdminLifecyclePostgres - resume technical pause - map result: %w", err)
		}
		if tournament.ID != resumed.ID || tournament.State != domain.TournamentState(resumed.State) ||
			tournament.Revision != resumed.Revision || !tournament.UpdatedAt.Equal(resumed.UpdatedAt.Time.UTC()) {
			return domain.ErrInternal
		}
		result = tournamentadmin.LifecycleResumeResult{
			Tournament: *tournament, PauseID: resumed.PauseID,
			SourcePauseCommandID: resumed.SourcePauseCommandID,
		}
		return nil
	})
	if err != nil {
		return tournamentadmin.LifecycleResumeResult{}, err
	}
	return result, nil
}

func (r *TournamentAdminLifecyclePostgres) CancelTechnicalPause(
	ctx context.Context,
	input tournamentadmin.LifecyclePauseCancellationInput,
) error {
	if !validTournamentAdminLifecycleRepository(ctx, r) || !validLifecyclePauseCancellationInput(input) {
		return domain.ErrValidation
	}
	id, err := r.tx.Querier(ctx).CancelTournamentTechnicalPause(
		ctx,
		sqlc.CancelTournamentTechnicalPauseParams{
			PauseRevisionID: input.PauseRevisionID, CancelledAt: tstz(input.CancelledAt),
			TournamentID: input.TournamentID, RosterID: input.RosterID,
			ResultingTournamentRevision: input.ResultingTournamentRevision,
			Reason:                      lifecycleReasonPointer(input.Reason),
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return lifecycleWriteError("cancel technical pause", err)
	}
	if id == uuid.Nil {
		return domain.ErrInternal
	}
	return nil
}

func (r *TournamentAdminLifecyclePostgres) SaveLifecycleCommand(
	ctx context.Context,
	record tournamentadmin.LifecycleCommandRecord,
) error {
	if !validTournamentAdminLifecycleRepository(ctx, r) || !validLifecycleCommandRecord(record) {
		return domain.ErrValidation
	}
	id, err := r.tx.Querier(ctx).CreateTournamentLifecycleCommand(
		ctx,
		tournamentAdminLifecycleCommandParams(record),
	)
	if err != nil {
		return lifecycleWriteError("save command", err)
	}
	if id != record.CommandID {
		return domain.ErrInternal
	}
	return nil
}

func validTournamentAdminLifecycleRepository(
	ctx context.Context,
	repository *TournamentAdminLifecyclePostgres,
) bool {
	return ctx != nil && repository != nil && repository.tx != nil
}

func lifecycleWriteError(operation string, err error) error {
	return mapRepositoryWriteError("TournamentAdminLifecyclePostgres - "+operation, err)
}

var _ tournamentadmin.LifecycleWorkflowRepository = (*TournamentAdminLifecyclePostgres)(nil)
