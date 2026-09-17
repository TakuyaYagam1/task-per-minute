package roster

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	adminoperation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/operation"
	rostercapability "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
)

func rosterFromAuthority(row sqlc.LockTournamentRosterAuthorityRow) sqlc.Roster {
	return sqlc.Roster{
		ID: row.RosterID, TournamentID: row.TournamentID, Revision: row.RosterRevision,
		LockedAt: row.LockedAt, ExecutionStartedAt: row.ExecutionStartedAt,
		CreatedAt: row.RosterCreatedAt, UpdatedAt: row.RosterUpdatedAt,
	}
}

func tournamentAdminRosterView(
	header sqlc.Roster,
	rows []sqlc.ListTournamentAdminRosterParticipantsRow,
) (rostercapability.RosterView, error) {
	createdAt, ok := tournamentAdminRequiredTime(header.CreatedAt)
	if !ok {
		return rostercapability.RosterView{}, domain.ErrInternal
	}
	updatedAt, ok := tournamentAdminRequiredTime(header.UpdatedAt)
	if !ok || updatedAt.Before(createdAt) {
		return rostercapability.RosterView{}, domain.ErrInternal
	}
	participants := make([]rostercapability.RosterParticipantView, len(rows))
	for index, row := range rows {
		participant, err := tournamentAdminRosterParticipant(row)
		if err != nil || participant.RosterID != header.ID || participant.TournamentID != header.TournamentID {
			return rostercapability.RosterView{}, domain.ErrInternal
		}
		participants[index] = participant
	}
	lockedAt := utcNullableTime(header.LockedAt)
	executionStartedAt := utcNullableTime(header.ExecutionStartedAt)
	return rostercapability.RosterView{
		ID: header.ID, TournamentID: header.TournamentID, Revision: header.Revision,
		Participants: participants, Locked: lockedAt != nil, ExecutionStarted: executionStartedAt != nil,
		LockedAt: lockedAt, ExecutionStartedAt: executionStartedAt,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}, nil
}

func tournamentAdminRosterParticipant(
	row sqlc.ListTournamentAdminRosterParticipantsRow,
) (rostercapability.RosterParticipantView, error) {
	createdAt, ok := tournamentAdminRequiredTime(row.CreatedAt)
	if !ok {
		return rostercapability.RosterParticipantView{}, domain.ErrInternal
	}
	updatedAt, ok := tournamentAdminRequiredTime(row.UpdatedAt)
	attendance := domain.AttendanceState(row.Attendance)
	if !ok || updatedAt.Before(createdAt) || !attendance.IsValid() {
		return rostercapability.RosterParticipantView{}, domain.ErrInternal
	}
	return rostercapability.RosterParticipantView{
		ID: row.ID, RosterID: row.RosterID, TournamentID: row.TournamentID,
		PlayerID: row.PlayerID, Seed: int(row.Seed), Attendance: attendance,
		CreatedAt: createdAt, UpdatedAt: updatedAt,
	}, nil
}

func tournamentAdminRosterOperation(
	row sqlc.TournamentRosterOperation,
) (*rostercapability.RosterOperationRecord, error) {
	if len(row.RequestDigest) != 32 || !json.Valid(row.ResultDocument) || !row.ExecutedAt.Valid {
		return nil, domain.ErrInternal
	}
	var digest [32]byte
	copy(digest[:], row.RequestDigest)
	executedAt := row.ExecutedAt.Time.UTC()
	if !validServerTime(executedAt) {
		return nil, domain.ErrInternal
	}
	return &rostercapability.RosterOperationRecord{
		CommandScope: adminoperation.CommandScope{
			Operator:     adminoperation.OperatorIdentity{ActorID: row.ActorID},
			TournamentID: row.TournamentID, CommandID: row.CommandID,
		},
		RosterID: row.RosterID, Action: rostercapability.RosterOperationAction(row.Action),
		PreflightRevisionID:         row.PreflightRevisionID.UUID,
		SourceProjectionRevisionID:  row.SourceProjectionRevisionID,
		SourceProjectionRevision:    row.SourceProjectionRevision,
		SourceTournamentRevision:    row.SourceTournamentRevision,
		SourceTournamentState:       domain.TournamentState(row.SourceTournamentState),
		ResultingTournamentRevision: row.ResultingTournamentRevision,
		ResultingTournamentState:    domain.TournamentState(row.ResultingTournamentState),
		SourceRosterRevision:        row.SourceRosterRevision, ResultingRosterRevision: row.ResultingRosterRevision,
		RequestDigest: digest, CheckedInPlayerIDs: append([]uuid.UUID(nil), row.CheckedInPlayerIds...),
		ResultDocument: append(json.RawMessage(nil), row.ResultDocument...), ExecutedAt: executedAt,
	}, nil
}

func tournamentAdminRequiredTime(value pgtype.Timestamptz) (time.Time, bool) {
	if !value.Valid {
		return time.Time{}, false
	}
	result := value.Time.UTC()
	return result, validServerTime(result)
}
