package postgres

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func validateParticipantInput(in ParticipantInput) error {
	if in.ID == uuid.Nil || in.RosterID == uuid.Nil || in.PlayerID == uuid.Nil || in.Seed < 1 ||
		!in.Attendance.IsValid() || !validServerTime(in.CreatedAt) {
		return domain.ErrValidation
	}
	return nil
}

func validServerTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

func tournamentRecord(row sqlc.Tournament) *TournamentRecord {
	out := &TournamentRecord{
		ID:     row.ID,
		Preset: row.Preset,
		Name:   row.Name, PublicID: row.PublicID, PlannedRosterSize: int(row.PlannedRosterSize),
		ContentRevision: row.ContentRevision,
		State:           domain.TournamentState(row.State),
		Revision:        row.Revision,
		CreatedAt:       row.CreatedAt.Time,
		UpdatedAt:       row.UpdatedAt.Time,
		StartedAt:       nullableTime(row.StartedAt),
		FinishedAt:      nullableTime(row.FinishedAt),
	}
	if row.PausedFromState != nil {
		state := domain.TournamentState(*row.PausedFromState)
		out.PausedFromState = &state
	}
	return out
}

func rosterRecord(row sqlc.Roster) *RosterRecord {
	return &RosterRecord{
		ID:                 row.ID,
		TournamentID:       row.TournamentID,
		Revision:           row.Revision,
		LockedAt:           nullableTime(row.LockedAt),
		ExecutionStartedAt: nullableTime(row.ExecutionStartedAt),
		CreatedAt:          row.CreatedAt.Time,
		UpdatedAt:          row.UpdatedAt.Time,
	}
}
