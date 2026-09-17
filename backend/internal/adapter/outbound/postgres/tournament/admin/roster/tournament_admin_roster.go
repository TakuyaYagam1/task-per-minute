package roster

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
	rostercapability "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
)

type TournamentAdminRosterPostgres struct {
	tx *db.TxManager
}

func NewTournamentAdminRosterPostgres(tx *db.TxManager) *TournamentAdminRosterPostgres {
	return &TournamentAdminRosterPostgres{tx: tx}
}

func (r *TournamentAdminRosterPostgres) GetRoster(
	ctx context.Context,
	tournamentID uuid.UUID,
) (rostercapability.RosterView, error) {
	if ctx == nil || r == nil || r.tx == nil || tournamentID == uuid.Nil {
		return rostercapability.RosterView{}, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	header, err := querier.GetTournamentAdminRoster(ctx, tournamentID)
	if err != nil {
		return rostercapability.RosterView{}, tournamentAdminRosterLookupError("GetRoster", err)
	}
	participants, err := querier.ListTournamentAdminRosterParticipants(ctx, tournamentID)
	if err != nil {
		return rostercapability.RosterView{}, fmt.Errorf(
			"TournamentAdminRosterPostgres - GetRoster - list participants: %w", err,
		)
	}
	return tournamentAdminRosterView(header, participants)
}

func (r *TournamentAdminRosterPostgres) LockRosterAuthority(
	ctx context.Context,
	tournamentID uuid.UUID,
) (rostercapability.RosterAuthority, error) {
	if !r.rosterWriteReady(ctx) || tournamentID == uuid.Nil {
		return rostercapability.RosterAuthority{}, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	row, err := querier.LockTournamentRosterAuthority(ctx, tournamentID)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return rostercapability.RosterAuthority{}, fmt.Errorf(
				"TournamentAdminRosterPostgres - LockRosterAuthority: %w", err,
			)
		}
		if _, rosterErr := querier.GetTournamentAdminRoster(ctx, tournamentID); rosterErr == nil {
			return rostercapability.RosterAuthority{}, domain.ErrTournamentProjectionNotFound
		} else if !errors.Is(rosterErr, pgx.ErrNoRows) {
			return rostercapability.RosterAuthority{}, fmt.Errorf(
				"TournamentAdminRosterPostgres - LockRosterAuthority - resolve missing projection: %w", rosterErr,
			)
		}
		return rostercapability.RosterAuthority{}, domain.ErrTournamentNotFound
	}
	participants, err := querier.ListTournamentAdminRosterParticipants(ctx, tournamentID)
	if err != nil {
		return rostercapability.RosterAuthority{}, fmt.Errorf(
			"TournamentAdminRosterPostgres - LockRosterAuthority - list participants: %w", err,
		)
	}
	view, err := tournamentAdminRosterView(rosterFromAuthority(row), participants)
	if err != nil {
		return rostercapability.RosterAuthority{}, err
	}
	return rostercapability.RosterAuthority{
		Roster: view, TournamentPreset: domain.TournamentPreset(row.TournamentPreset),
		PlannedRosterSize:    int(row.PlannedRosterSize),
		ContentRevision:      row.ContentRevision,
		TournamentState:      domain.TournamentState(row.TournamentState),
		TournamentRevision:   row.TournamentRevision,
		ProjectionRevisionID: row.ProjectionRevisionID, ProjectionRevision: row.ProjectionRevision,
	}, nil
}

func (r *TournamentAdminRosterPostgres) FindRosterOperation(
	ctx context.Context,
	tournamentID uuid.UUID,
	commandID uuid.UUID,
) (*rostercapability.RosterOperationRecord, error) {
	if ctx == nil || r == nil || r.tx == nil || tournamentID == uuid.Nil || commandID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).FindTournamentRosterOperation(
		ctx, sqlc.FindTournamentRosterOperationParams{TournamentID: tournamentID, CommandID: commandID},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminRosterPostgres - FindRosterOperation: %w", err)
	}
	return tournamentAdminRosterOperation(row)
}

func (r *TournamentAdminRosterPostgres) ReadRosterTime(ctx context.Context) (time.Time, error) {
	if ctx == nil || r == nil || r.tx == nil {
		return time.Time{}, domain.ErrValidation
	}
	value, err := r.tx.Querier(ctx).ReadTournamentRosterTime(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("TournamentAdminRosterPostgres - ReadRosterTime: %w", err)
	}
	if !value.Valid || !validServerTime(value.Time.UTC()) {
		return time.Time{}, domain.ErrInternal
	}
	return value.Time.UTC(), nil
}

func (r *TournamentAdminRosterPostgres) SaveRosterOperation(
	ctx context.Context,
	record rostercapability.RosterOperationRecord,
) error {
	if !r.rosterWriteReady(ctx) {
		return domain.ErrValidation
	}
	created, err := r.tx.Querier(ctx).CreateTournamentRosterOperation(
		ctx,
		sqlc.CreateTournamentRosterOperationParams{
			CommandID: record.CommandID, TournamentID: record.TournamentID, RosterID: record.RosterID,
			ActorID: record.Operator.ActorID, Action: string(record.Action),
			PreflightRevisionID: uuid.NullUUID{
				UUID: record.PreflightRevisionID, Valid: record.PreflightRevisionID != uuid.Nil,
			},
			SourceProjectionRevisionID:  record.SourceProjectionRevisionID,
			SourceProjectionRevision:    record.SourceProjectionRevision,
			SourceTournamentRevision:    record.SourceTournamentRevision,
			SourceTournamentState:       string(record.SourceTournamentState),
			ResultingTournamentRevision: record.ResultingTournamentRevision,
			ResultingTournamentState:    string(record.ResultingTournamentState),
			SourceRosterRevision:        record.SourceRosterRevision,
			ResultingRosterRevision:     record.ResultingRosterRevision,
			RequestDigest:               append([]byte(nil), record.RequestDigest[:]...),
			CheckedInPlayerIds:          append([]uuid.UUID{}, record.CheckedInPlayerIDs...),
			ResultDocument:              append([]byte(nil), record.ResultDocument...), ExecutedAt: tstz(record.ExecutedAt),
		},
	)
	if err != nil {
		if isUniqueViolation(err, "tournament_roster_operations_pkey") {
			return domain.WrapError(err, domain.ErrConflict)
		}
		return fmt.Errorf("TournamentAdminRosterPostgres - SaveRosterOperation: %w", err)
	}
	if created != record.CommandID {
		return domain.ErrInternal
	}
	return nil
}

func tournamentAdminRosterLookupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrTournamentNotFound
	}
	return fmt.Errorf("TournamentAdminRosterPostgres - %s: %w", operation, err)
}

var _ rostercapability.RosterWorkflowRepository = (*TournamentAdminRosterPostgres)(nil)
