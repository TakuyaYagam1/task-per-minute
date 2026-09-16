package snapshot

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

func TestTournamentAdminSnapshotCursorRecoveryWatermark(t *testing.T) {
	t.Parallel()

	current := tournamentadmin.OperatorCursor{
		ProjectionRevision: 7,
		AuthorityRevision:  5,
		AuditSequence:      11,
	}
	tests := []struct {
		name      string
		requested *tournamentadmin.OperatorCursor
		conflict  bool
	}{
		{name: "absent"},
		{name: "equal", requested: &tournamentadmin.OperatorCursor{ProjectionRevision: 7, AuthorityRevision: 5, AuditSequence: 11}},
		{name: "older", requested: &tournamentadmin.OperatorCursor{ProjectionRevision: 1, AuthorityRevision: 1, AuditSequence: 0}},
		{name: "future projection", requested: &tournamentadmin.OperatorCursor{ProjectionRevision: 8, AuthorityRevision: 1, AuditSequence: 0}, conflict: true},
		{name: "future authority", requested: &tournamentadmin.OperatorCursor{ProjectionRevision: 1, AuthorityRevision: 6, AuditSequence: 0}, conflict: true},
		{name: "future audit", requested: &tournamentadmin.OperatorCursor{ProjectionRevision: 1, AuthorityRevision: 1, AuditSequence: 12}, conflict: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := tournamentAdminSnapshotCursorError(test.requested, current, domain.TournamentStateSwiss)
			if !test.conflict {
				require.NoError(t, err)
				return
			}
			var conflict *tournamentadmin.RevisionConflictError
			require.ErrorAs(t, err, &conflict)
			require.ErrorIs(t, err, domain.ErrConflict)
			require.Equal(t, current.ProjectionRevision, conflict.CurrentRevision)
		})
	}
}

func TestTournamentAdminSnapshotHeaderAuthorityFallback(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	rosterID := uuid.New()
	createdAt := time.Date(2026, time.September, 6, 9, 0, 0, 0, time.UTC)
	row := sqlc.GetTournamentAdminSnapshotHeaderRow{
		ID:                  tournamentID,
		RosterID:            uuid.NullUUID{UUID: rosterID, Valid: true},
		Preset:              string(domain.TournamentPresetV1),
		State:               string(domain.TournamentStateDraft),
		TournamentRevision:  3,
		TournamentCreatedAt: tournamentAdminSnapshotTestTime(createdAt),
		TournamentUpdatedAt: tournamentAdminSnapshotTestTime(createdAt),
		ProjectionRevision:  2,
		AuthorityRevision:   3,
		AuditSequence:       0,
	}

	header, err := tournamentAdminSnapshotHeader(row, tournamentID)
	require.NoError(t, err)
	require.Nil(t, header.authority)
	require.Equal(t, int64(3), header.cursor.AuthorityRevision)

	row.AuthorityRevision++
	_, err = tournamentAdminSnapshotHeader(row, tournamentID)
	require.ErrorIs(t, err, domain.ErrInternal)

	row.AuthorityRevision = 3
	row.ProjectionRevision = 0
	_, err = tournamentAdminSnapshotHeader(row, tournamentID)
	require.ErrorIs(t, err, domain.ErrTournamentProjectionNotFound)
}

func TestTournamentAdminSnapshotWavesPreservesSwissBye(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	rosterID := uuid.New()
	waveID := uuid.New()
	participants := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	seriesID := uuid.New()
	now := time.Date(2026, time.September, 6, 10, 0, 0, 0, time.UTC)
	roster := tournamentadmin.RosterView{ID: rosterID, TournamentID: tournamentID}
	for index, participantID := range participants {
		roster.Participants = append(roster.Participants, tournamentadmin.RosterParticipantView{
			ID: participantID, RosterID: rosterID, TournamentID: tournamentID,
			PlayerID: uuid.New(), Seed: index + 1, Attendance: domain.AttendanceStateCheckedIn,
			CreatedAt: now, UpdatedAt: now,
		})
	}
	rows := []sqlc.ListTournamentAdminSnapshotWavesRow{{
		ID: waveID, TournamentID: tournamentID, RevisionID: uuid.New(), Revision: 1,
		State: string(domain.WaveStatePlanned), CreatedAt: tournamentAdminSnapshotTestTime(now),
		UpdatedAt:        tournamentAdminSnapshotTestTime(now),
		ByeParticipantID: uuid.NullUUID{UUID: participants[2], Valid: true},
	}}
	ready := false
	revision := int64(1)
	members := []sqlc.ListTournamentAdminSnapshotWaveMembersRow{
		{WaveID: waveID, ParticipantID: participants[0], Ready: &ready, ReadinessRevision: &revision, SeriesID: seriesID},
		{WaveID: waveID, ParticipantID: participants[1], Ready: &ready, ReadinessRevision: &revision, SeriesID: seriesID},
		{WaveID: waveID, ParticipantID: participants[2], Ready: &ready, ReadinessRevision: &revision},
	}

	waves, err := tournamentAdminSnapshotWaves(rows, members, roster)
	require.NoError(t, err)
	require.Len(t, waves, 1)
	require.NotNil(t, waves[0].ByeParticipantID)
	require.Equal(t, participants[2], *waves[0].ByeParticipantID)
	require.NotContains(t, waves[0].SeriesIDs, participants[2])
}

func TestTournamentAdminSnapshotGameAttemptsMapsGeneratedQueryRows(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 6, 10, 0, 0, 0, time.UTC)
	attemptID := uuid.New()
	rows := []sqlc.ListTournamentAdminSnapshotGameAttemptsRow{{
		ID:            attemptID,
		SlotID:        uuid.New(),
		SeriesID:      uuid.New(),
		RosterID:      uuid.New(),
		AttemptNumber: 2,
		State:         string(domain.GameStateActive),
		ResultReason:  nil,
		Revision:      3,
		CreatedAt:     tournamentAdminSnapshotTestTime(now),
		UpdatedAt:     tournamentAdminSnapshotTestTime(now),
		StartedAt:     tournamentAdminSnapshotTestTime(now),
		FinishedAt:    pgtype.Timestamptz{},
	}}

	mapped := tournamentAdminSnapshotGameAttempts(rows)
	require.Len(t, mapped, 1)
	require.Equal(t, attemptID, mapped[0].ID)
	require.Equal(t, rows[0].SlotID, mapped[0].SlotID)
	require.Equal(t, rows[0].Revision, mapped[0].Revision)
	require.Equal(t, rows[0].StartedAt, mapped[0].StartedAt)
}

func tournamentAdminSnapshotTestTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}
