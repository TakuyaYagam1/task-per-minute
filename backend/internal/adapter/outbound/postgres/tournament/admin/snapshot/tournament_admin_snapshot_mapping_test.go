package snapshot

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/snapshot"
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

func TestTournamentAdminSnapshotRecoveryControlsEmptyProjection(t *testing.T) {
	t.Parallel()

	controls, err := tournamentAdminSnapshotRecoveryControls(nil, nil)
	require.NoError(t, err)
	require.NotNil(t, controls)
	require.Empty(t, controls)
}

func TestTournamentAdminSnapshotRecoveryControlsMapsLegalReplay(t *testing.T) {
	t.Parallel()

	assignmentID, seriesID, slotID := uuid.New(), uuid.New(), uuid.New()
	oldWaveID, closureID := uuid.New(), uuid.New()
	attemptID, resultRevisionID := uuid.New(), uuid.New()
	reason := string(domain.GameResultReasonNoSolve)
	rows := []sqlc.ListTournamentAdminRecoveryControlsRow{{
		ControlKind:                     string(tournamentadmin.RecoveryControlReplay),
		AssignmentID:                    assignmentID,
		SeriesID:                        seriesID,
		SlotID:                          slotID,
		Category:                        string(domain.CategoryWeb),
		ExpectedAuthorityRevision:       7,
		OldWaveID:                       oldWaveID,
		Reason:                          reason,
		ReplayExpectedClosureRevisionID: closureID,
		AttemptID:                       attemptID,
		AttemptSlotID:                   slotID,
		AttemptNumber:                   1,
		AttemptState:                    string(domain.GameStateVoid),
		AttemptResultReason:             &reason,
		AttemptResultRevisionID:         uuid.NullUUID{UUID: resultRevisionID, Valid: true},
	}}

	controls, err := tournamentAdminSnapshotRecoveryControls(rows, nil)
	require.NoError(t, err)
	require.Len(t, controls, 1)
	require.Equal(t, tournamentadmin.RecoveryControlReplay, controls[0].Kind)
	require.Equal(t, assignmentID, controls[0].AssignmentID)
	require.Equal(t, closureID, controls[0].Replay.ExpectedClosureRevisionID)
	require.Len(t, controls[0].Attempts, 1)
	require.Equal(t, domain.GameResultReasonNoSolve, controls[0].Attempts[0].ResultReason)
}

func TestTournamentAdminSnapshotRecoveryControlsMapsReserveCandidates(t *testing.T) {
	t.Parallel()

	assignmentID, seriesID, slotID := uuid.New(), uuid.New(), uuid.New()
	oldWaveID, closureID := uuid.New(), uuid.New()
	attemptID, resultRevisionID := uuid.New(), uuid.New()
	exhaustionID, currentSnapshotID := uuid.New(), uuid.New()
	candidateTaskID := uuid.New()
	reason := string(domain.GameResultReasonTaskFailure)
	revision := func(value int64) *int64 { return &value }
	identifier := func(value uuid.UUID) uuid.NullUUID { return uuid.NullUUID{UUID: value, Valid: true} }
	rows := []sqlc.ListTournamentAdminRecoveryControlsRow{{
		ControlKind:                     string(tournamentadmin.RecoveryControlReserveExhausted),
		AssignmentID:                    assignmentID,
		SeriesID:                        seriesID,
		SlotID:                          slotID,
		Category:                        string(domain.CategoryCrypto),
		ExpectedAuthorityRevision:       8,
		OldWaveID:                       oldWaveID,
		Reason:                          "replay reserves exhausted",
		ReplayExpectedClosureRevisionID: closureID,
		ExhaustionCommandID:             identifier(exhaustionID),
		CurrentSnapshotID:               identifier(currentSnapshotID),
		ExpectedAssignmentRevision:      revision(3),
		ExpectedPoolRevision:            revision(4),
		ExpectedPoolRevisionID:          identifier(uuid.New()),
		ExpectedHistoryRevision:         revision(5),
		ExpectedHistoryRevisionID:       identifier(uuid.New()),
		ExpectedArtifactRevision:        revision(6),
		ExpectedArtifactRevisionID:      identifier(uuid.New()),
		ExpectedReservationRevision:     revision(7),
		ExpectedReservationRevisionID:   identifier(uuid.New()),
		ExpectedCategoryRevision:        revision(8),
		ExpectedCategoryRevisionID:      identifier(uuid.New()),
		ExpectedSnapshotID:              identifier(currentSnapshotID),
		AttemptID:                       attemptID,
		AttemptSlotID:                   slotID,
		AttemptNumber:                   1,
		AttemptState:                    string(domain.GameStateVoid),
		AttemptResultReason:             &reason,
		AttemptResultRevisionID:         identifier(resultRevisionID),
	}}
	candidates := []sqlc.ListTournamentAdminRecoveryReserveCandidatesRow{{
		ExhaustionCommandID: exhaustionID, TaskID: candidateTaskID, TaskVersion: 3,
	}}

	controls, err := tournamentAdminSnapshotRecoveryControls(rows, candidates)
	require.NoError(t, err)
	require.Len(t, controls, 1)
	require.Equal(t, tournamentadmin.RecoveryControlReserveExhausted, controls[0].Kind)
	require.Equal(t, int64(4), controls[0].ReserveExhausted.ExpectedPoolRevision)
	require.Len(t, controls[0].ReserveExhausted.Candidates, 1)
	require.Equal(t, candidateTaskID, controls[0].ReserveExhausted.Candidates[0].TaskID)
	require.Equal(t, 3, controls[0].ReserveExhausted.Candidates[0].Version)
}

func TestTournamentAdminSnapshotRecoveryControlsRejectsAmbiguousEvidence(t *testing.T) {
	t.Parallel()

	assignmentID, seriesID, slotID := uuid.New(), uuid.New(), uuid.New()
	oldWaveID, closureID := uuid.New(), uuid.New()
	attemptID, resultRevisionID := uuid.New(), uuid.New()
	reason := string(domain.GameResultReasonNoSolve)
	row := sqlc.ListTournamentAdminRecoveryControlsRow{
		ControlKind:                     string(tournamentadmin.RecoveryControlReplay),
		AssignmentID:                    assignmentID,
		SeriesID:                        seriesID,
		SlotID:                          slotID,
		Category:                        string(domain.CategoryWeb),
		ExpectedAuthorityRevision:       7,
		OldWaveID:                       oldWaveID,
		Reason:                          reason,
		ReplayExpectedClosureRevisionID: closureID,
		AttemptID:                       attemptID,
		AttemptSlotID:                   slotID,
		AttemptNumber:                   1,
		AttemptState:                    string(domain.GameStateVoid),
		AttemptResultReason:             &reason,
		AttemptResultRevisionID:         uuid.NullUUID{UUID: resultRevisionID, Valid: true},
	}

	duplicateCandidate := sqlc.ListTournamentAdminRecoveryReserveCandidatesRow{
		ExhaustionCommandID: uuid.New(), TaskID: uuid.New(), TaskVersion: 2,
	}
	reserveRow := row
	reserveRow.ControlKind = string(tournamentadmin.RecoveryControlReserveExhausted)
	reserveRow.Reason = "replay reserves exhausted"
	reserveRow.ExhaustionCommandID = uuid.NullUUID{UUID: uuid.New(), Valid: true}
	reserveRow.CurrentSnapshotID = uuid.NullUUID{UUID: uuid.New(), Valid: true}
	reserveRow.ExpectedSnapshotID = reserveRow.CurrentSnapshotID
	reserveRow.ExpectedAssignmentRevision = int64Ptr(1)
	reserveRow.ExpectedPoolRevision = int64Ptr(1)
	reserveRow.ExpectedHistoryRevision = int64Ptr(1)
	reserveRow.ExpectedArtifactRevision = int64Ptr(1)
	reserveRow.ExpectedReservationRevision = int64Ptr(1)
	reserveRow.ExpectedCategoryRevision = int64Ptr(1)
	reserveRow.ExpectedPoolRevisionID = uuid.NullUUID{UUID: uuid.New(), Valid: true}
	reserveRow.ExpectedHistoryRevisionID = uuid.NullUUID{UUID: uuid.New(), Valid: true}
	reserveRow.ExpectedArtifactRevisionID = uuid.NullUUID{UUID: uuid.New(), Valid: true}
	reserveRow.ExpectedReservationRevisionID = uuid.NullUUID{UUID: uuid.New(), Valid: true}
	reserveRow.ExpectedCategoryRevisionID = uuid.NullUUID{UUID: uuid.New(), Valid: true}

	reserveCommandID := reserveRow.ExhaustionCommandID.UUID
	_, err := tournamentAdminSnapshotRecoveryControls(
		[]sqlc.ListTournamentAdminRecoveryControlsRow{reserveRow},
		[]sqlc.ListTournamentAdminRecoveryReserveCandidatesRow{duplicateCandidateFor(reserveCommandID, duplicateCandidate), duplicateCandidateFor(reserveCommandID, duplicateCandidate)},
	)
	require.ErrorIs(t, err, domain.ErrInternal)

	_, err = tournamentAdminSnapshotRecoveryControls(
		[]sqlc.ListTournamentAdminRecoveryControlsRow{row},
		[]sqlc.ListTournamentAdminRecoveryReserveCandidatesRow{{ExhaustionCommandID: uuid.New(), TaskID: uuid.New(), TaskVersion: 2}},
	)
	require.ErrorIs(t, err, domain.ErrInternal)

	inconsistent := row
	inconsistent.AttemptID = uuid.New()
	inconsistent.AttemptNumber = 2
	inconsistent.Reason = "different persisted evidence"
	_, err = tournamentAdminSnapshotRecoveryControls(
		[]sqlc.ListTournamentAdminRecoveryControlsRow{row, inconsistent}, nil,
	)
	require.ErrorIs(t, err, domain.ErrInternal)
}

func duplicateCandidateFor(commandID uuid.UUID, row sqlc.ListTournamentAdminRecoveryReserveCandidatesRow) sqlc.ListTournamentAdminRecoveryReserveCandidatesRow {
	row.ExhaustionCommandID = commandID
	return row
}

func int64Ptr(value int64) *int64 {
	return &value
}

func tournamentAdminSnapshotTestTime(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}
