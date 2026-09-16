package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

func (r *TournamentAdminRosterPostgres) ReplaceRosterParticipants(
	ctx context.Context,
	authority tournamentadmin.RosterAuthority,
	participants []tournamentadmin.RosterParticipantInput,
	updatedAt time.Time,
) (tournamentadmin.RosterView, error) {
	if !r.rosterWriteReady(ctx) || !validServerTime(updatedAt) {
		return tournamentadmin.RosterView{}, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	deleted, err := querier.DeleteTournamentAdminRosterParticipants(
		ctx,
		sqlc.DeleteTournamentAdminRosterParticipantsParams{
			RosterID: authority.Roster.ID, TournamentID: authority.Roster.TournamentID,
			ExpectedRosterRevision: authority.Roster.Revision,
		},
	)
	if err != nil {
		return tournamentadmin.RosterView{}, tournamentAdminRosterMutationError("ReplaceRoster - delete", err)
	}
	if deleted != int64(len(authority.Roster.Participants)) {
		return tournamentadmin.RosterView{}, domain.ErrConflict
	}
	ordered := append([]tournamentadmin.RosterParticipantInput(nil), participants...)
	slices.SortFunc(ordered, func(first, second tournamentadmin.RosterParticipantInput) int {
		return first.Seed - second.Seed
	})
	for _, participant := range ordered {
		_, err = querier.InsertTournamentAdminRosterParticipant(ctx, sqlc.InsertTournamentAdminRosterParticipantParams{
			ID:       tournamentRosterParticipantID(authority.Roster.ID, participant.PlayerID),
			PlayerID: participant.PlayerID, Seed: int32(participant.Seed), //nolint:gosec // Application validation caps seeds at 16.
			Attendance: string(participant.Attendance), CreatedAt: tstz(updatedAt),
			RosterID: authority.Roster.ID, TournamentID: authority.Roster.TournamentID,
			ExpectedRosterRevision: authority.Roster.Revision,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return tournamentadmin.RosterView{}, &tournamentadmin.RevisionConflictError{
					ExpectedRevision: authority.ProjectionRevision,
					CurrentRevision:  authority.ProjectionRevision,
					CurrentState:     authority.TournamentState,
				}
			}
			return tournamentadmin.RosterView{}, tournamentAdminRosterMutationError("ReplaceRoster - insert", err)
		}
	}
	if _, err = querier.AdvanceTournamentAdminRosterRevision(
		ctx,
		sqlc.AdvanceTournamentAdminRosterRevisionParams{
			UpdatedAt: tstz(updatedAt), RosterID: authority.Roster.ID,
			TournamentID:           authority.Roster.TournamentID,
			ExpectedRosterRevision: authority.Roster.Revision,
		},
	); err != nil {
		return tournamentadmin.RosterView{}, tournamentAdminRosterMutationError("ReplaceRoster - advance", err)
	}
	return r.GetRoster(ctx, authority.Roster.TournamentID)
}

func (r *TournamentAdminRosterPostgres) LockRosterWithPreflight(
	ctx context.Context,
	authority tournamentadmin.RosterAuthority,
	checkedInPlayerIDs []uuid.UUID,
	lockedAt time.Time,
) (tournamentadmin.RosterView, error) {
	if !r.rosterWriteReady(ctx) || !validServerTime(lockedAt) ||
		len(checkedInPlayerIDs) < domain.TournamentMinParticipants {
		return tournamentadmin.RosterView{}, domain.ErrValidation
	}
	current := tournamentAdminCheckedInPlayerIDs(authority.Roster.Participants)
	if !slices.Equal(current, checkedInPlayerIDs) {
		return tournamentadmin.RosterView{}, domain.ErrConflict
	}
	querier := r.tx.Querier(ctx)
	if err := lockRosterTaskExposure(ctx, querier, authority.Roster.TournamentID); err != nil {
		return tournamentadmin.RosterView{}, err
	}
	reserved, err := querier.ReserveCheckedInTournamentParticipants(
		ctx,
		sqlc.ReserveCheckedInTournamentParticipantsParams{
			AcquiredAt: tstz(lockedAt), RosterID: authority.Roster.ID,
		},
	)
	if err != nil {
		return tournamentadmin.RosterView{}, tournamentAdminRosterMutationError("LockRoster - reserve", err)
	}
	if !sameTournamentAdminIDs(reserved, checkedInPlayerIDs) {
		return tournamentadmin.RosterView{}, domain.ErrConflict
	}
	if _, err = querier.LockTournamentRosterCAS(ctx, sqlc.LockTournamentRosterCASParams{
		LockedAt: tstz(lockedAt), ID: authority.Roster.ID, ExpectedRevision: authority.Roster.Revision,
	}); err != nil {
		return tournamentadmin.RosterView{}, tournamentAdminRosterMutationError("LockRoster - roster", err)
	}
	if err := transitionTournamentForRoster(
		ctx, querier, authority, domain.TournamentStateRosterLocked, lockedAt,
	); err != nil {
		return tournamentadmin.RosterView{}, err
	}
	return r.GetRoster(ctx, authority.Roster.TournamentID)
}

// The saved preflight cannot authorize content disclosed after the report was
// recorded. Lock the exact bound versions before reading exposure in a fresh
// statement so a concurrent disclosure either precedes or follows roster lock.
func lockRosterTaskExposure(ctx context.Context, querier *sqlc.Queries, tournamentID uuid.UUID) error {
	content, err := querier.GetCurrentTournamentContentConfiguration(ctx, tournamentID)
	if err != nil {
		return tournamentAdminRosterMutationError("LockRoster - content", err)
	}
	pools := []uuid.UUID{content.NormalPoolRevisionID, content.GoldenPoolRevisionID}
	if err := querier.LockTaskPoolVersionsForExposureCheck(ctx, pools); err != nil {
		return tournamentAdminRosterMutationError("LockRoster - lock content", err)
	}
	health, err := querier.ListTaskPoolVersionHealth(ctx, pools)
	if err != nil {
		return tournamentAdminRosterMutationError("LockRoster - content exposure", err)
	}
	for _, version := range health {
		if version.TaskPubliclyExposed {
			return domain.ErrConflict
		}
	}
	return nil
}

func (r *TournamentAdminRosterPostgres) UnlockRoster(
	ctx context.Context,
	authority tournamentadmin.RosterAuthority,
	updatedAt time.Time,
) (tournamentadmin.RosterView, error) {
	if !r.rosterWriteReady(ctx) || !validServerTime(updatedAt) {
		return tournamentadmin.RosterView{}, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	if _, err := querier.UnlockTournamentRosterCAS(ctx, sqlc.UnlockTournamentRosterCASParams{
		UpdatedAt: tstz(updatedAt), ID: authority.Roster.ID, ExpectedRevision: authority.Roster.Revision,
	}); err != nil {
		return tournamentadmin.RosterView{}, tournamentAdminRosterMutationError("UnlockRoster - roster", err)
	}
	released, err := querier.ReleaseTournamentReservations(ctx, authority.Roster.TournamentID)
	if err != nil {
		return tournamentadmin.RosterView{}, tournamentAdminRosterMutationError("UnlockRoster - reservations", err)
	}
	if released != int64(len(tournamentAdminCheckedInPlayerIDs(authority.Roster.Participants))) {
		return tournamentadmin.RosterView{}, domain.ErrConflict
	}
	if err := transitionTournamentForRoster(
		ctx, querier, authority, domain.TournamentStateRegistration, updatedAt,
	); err != nil {
		return tournamentadmin.RosterView{}, err
	}
	return r.GetRoster(ctx, authority.Roster.TournamentID)
}

func transitionTournamentForRoster(
	ctx context.Context,
	querier *sqlc.Queries,
	authority tournamentadmin.RosterAuthority,
	next domain.TournamentState,
	updatedAt time.Time,
) error {
	id, err := querier.TransitionTournamentForRosterCAS(ctx, sqlc.TransitionTournamentForRosterCASParams{
		NextState: string(next), UpdatedAt: tstz(updatedAt), TournamentID: authority.Roster.TournamentID,
		ExpectedTournamentRevision: authority.TournamentRevision,
		ExpectedState:              string(authority.TournamentState),
	})
	if err != nil {
		return tournamentAdminRosterMutationError("transition tournament", err)
	}
	if id != authority.Roster.TournamentID {
		return domain.ErrInternal
	}
	return nil
}

func (r *TournamentAdminRosterPostgres) rosterWriteReady(ctx context.Context) bool {
	if ctx == nil || r == nil || r.tx == nil {
		return false
	}
	_, active := r.tx.Conn(ctx).(pgx.Tx)
	return active
}

func tournamentRosterParticipantID(rosterID, playerID uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(rosterID, playerID[:])
}

func tournamentAdminCheckedInPlayerIDs(participants []tournamentadmin.RosterParticipantView) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(participants))
	for _, participant := range participants {
		if participant.Attendance == domain.AttendanceStateCheckedIn {
			result = append(result, participant.PlayerID)
		}
	}
	slices.SortFunc(result, func(first, second uuid.UUID) int { return bytes.Compare(first[:], second[:]) })
	return result
}

func sameTournamentAdminIDs(first, second []uuid.UUID) bool {
	firstCopy := append([]uuid.UUID(nil), first...)
	secondCopy := append([]uuid.UUID(nil), second...)
	slices.SortFunc(firstCopy, func(left, right uuid.UUID) int { return bytes.Compare(left[:], right[:]) })
	slices.SortFunc(secondCopy, func(left, right uuid.UUID) int { return bytes.Compare(left[:], right[:]) })
	return slices.Equal(firstCopy, secondCopy)
}

func tournamentAdminRosterMutationError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) || tournamentAdminRosterConstraintConflict(err) {
		return domain.WrapError(err, domain.ErrConflict)
	}
	return fmt.Errorf("TournamentAdminRosterPostgres - %s: %w", operation, err)
}

func tournamentAdminRosterConstraintConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == pgUniqueViolation ||
		pgErr.Code == pgForeignKeyViolation || pgErr.Code == pgRestrictViolation)
}
