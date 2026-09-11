package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	tournamentpause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/pause"
)

func TestTournamentAdminLifecycleAuthorityMapsFullView(t *testing.T) {
	t.Parallel()
	createdAt := lifecycleTestTime()
	updatedAt := createdAt.Add(2 * time.Minute)
	startedAt := createdAt.Add(time.Minute)
	origin := string(domain.TournamentStateSwiss)
	row := sqlc.LockTournamentLifecycleAuthorityRow{
		ID: uuid.New(), RosterID: uuid.New(), Preset: string(domain.TournamentPresetV1),
		Name: "Lifecycle Tournament", PublicID: "lifecycle-tournament", PlannedRosterSize: 8, ContentRevision: 3,
		State: string(domain.TournamentStateTechnicalPause), PausedFromState: &origin,
		TournamentRevision: 7, TournamentCreatedAt: lifecycleTestTSTZ(createdAt),
		TournamentUpdatedAt: lifecycleTestTSTZ(updatedAt), TournamentStartedAt: lifecycleTestTSTZ(startedAt),
		RosterSize: 8, ProjectionRevisionID: uuid.New(), ProjectionRevision: 11,
	}

	authority, err := tournamentAdminLifecycleAuthority(row)
	require.NoError(t, err)
	require.Equal(t, row.ID, authority.Tournament.ID)
	require.Equal(t, row.RosterID, authority.Tournament.RosterID)
	require.Equal(t, row.Name, authority.Tournament.Name)
	require.Equal(t, row.PublicID, authority.Tournament.PublicID)
	require.Equal(t, int(row.PlannedRosterSize), authority.Tournament.PlannedRosterSize)
	require.Equal(t, row.ContentRevision, authority.Tournament.ContentRevision)
	require.Equal(t, domain.TournamentStateTechnicalPause, authority.Tournament.State)
	require.NotNil(t, authority.Tournament.PausedFromState)
	require.Equal(t, domain.TournamentStateSwiss, *authority.Tournament.PausedFromState)
	require.Equal(t, startedAt, *authority.Tournament.StartedAt)
	require.Nil(t, authority.Tournament.FinishedAt)
	require.Equal(t, row.ProjectionRevisionID, authority.ProjectionRevisionID)
	require.Equal(t, row.ProjectionRevision, authority.ProjectionRevision)
}

func TestLifecycleRosterReadyForSwissFailsClosed(t *testing.T) {
	t.Parallel()

	roster := sqlc.Roster{
		ID: uuid.New(), TournamentID: uuid.New(), Revision: 3,
		LockedAt: lifecycleTestTSTZ(lifecycleTestTime()),
	}
	participants := make([]sqlc.ListTournamentAdminRosterParticipantsRow, domain.TournamentMinParticipants)
	reservations := make([]sqlc.ParticipantReservation, domain.TournamentMinParticipants)
	for index := range participants {
		playerID := uuid.New()
		participants[index] = sqlc.ListTournamentAdminRosterParticipantsRow{
			ID: uuid.New(), RosterID: roster.ID, TournamentID: roster.TournamentID,
			PlayerID: playerID, Attendance: string(domain.AttendanceStateCheckedIn),
		}
		reservations[index] = sqlc.ParticipantReservation{
			PlayerID: playerID, ReservationID: uuid.New(), TournamentID: roster.TournamentID, Revision: 1,
		}
	}
	require.True(t, lifecycleRosterReadyForSwiss(roster, participants, reservations))

	tests := map[string]func(*sqlc.Roster, []sqlc.ListTournamentAdminRosterParticipantsRow, *[]sqlc.ParticipantReservation){
		"unlocked roster": func(value *sqlc.Roster, _ []sqlc.ListTournamentAdminRosterParticipantsRow, _ *[]sqlc.ParticipantReservation) {
			value.LockedAt = pgtype.Timestamptz{}
		},
		"participant not checked in": func(_ *sqlc.Roster, value []sqlc.ListTournamentAdminRosterParticipantsRow, _ *[]sqlc.ParticipantReservation) {
			value[0].Attendance = string(domain.AttendanceStateRegistered)
		},
		"missing reservation": func(_ *sqlc.Roster, _ []sqlc.ListTournamentAdminRosterParticipantsRow, value *[]sqlc.ParticipantReservation) {
			*value = (*value)[:len(*value)-1]
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidateRoster := roster
			candidateParticipants := append([]sqlc.ListTournamentAdminRosterParticipantsRow(nil), participants...)
			candidateReservations := append([]sqlc.ParticipantReservation(nil), reservations...)
			mutate(&candidateRoster, candidateParticipants, &candidateReservations)
			require.False(t, lifecycleRosterReadyForSwiss(candidateRoster, candidateParticipants, candidateReservations))
		})
	}
}

func TestTournamentExecutionSnapshotRequiresCompleteShape(t *testing.T) {
	t.Parallel()
	document := []byte(`{
		"waves":[["wave",1]],"series":[],"games":[],"drafts":[],"ready_windows":[],
		"readiness":[],"assignments":[],"child_pauses":[],"reconnect":[],"golden":[],
		"incomplete_count":1,"active_golden_count":0
	}`)

	snapshot, err := mapTournamentExecutionSnapshot(document, 12)
	require.NoError(t, err)
	require.Equal(t, int64(12), snapshot.GraphRevision)
	require.Equal(t, 1, snapshot.ExpectedChildren)
	require.Equal(t, 1, snapshot.ObservedChildren)
	require.Equal(t, 1, snapshot.IncompleteChildren)
	require.False(t, snapshot.ActiveGolden)

	document[0] = '['
	require.JSONEq(t, `{
		"waves":[["wave",1]],"series":[],"games":[],"drafts":[],"ready_windows":[],
		"readiness":[],"assignments":[],"child_pauses":[],"reconnect":[],"golden":[],
		"incomplete_count":1,"active_golden_count":0
	}`, string(snapshot.Document))
}

func TestTournamentExecutionSnapshotRejectsMissingOrInconsistentEvidence(t *testing.T) {
	t.Parallel()
	_, err := mapTournamentExecutionSnapshot([]byte(`{"waves":[],"incomplete_count":0}`), 2)
	require.ErrorIs(t, err, domain.ErrInternal)

	_, err = mapTournamentExecutionSnapshot([]byte(`{
		"waves":[],"series":[],"games":[],"drafts":[],"ready_windows":[],"readiness":[],
		"assignments":[],"child_pauses":[],"reconnect":[],"golden":[],
		"incomplete_count":1,"active_golden_count":0
	}`), 2)
	require.ErrorIs(t, err, domain.ErrInternal)
}

func TestTournamentAdminLifecycleCommandRoundTripPreservesPauseEvidence(t *testing.T) {
	t.Parallel()
	createdAt := lifecycleTestTime()
	startedAt := createdAt.Add(time.Minute)
	pausedAt := createdAt.Add(2 * time.Minute)
	reason := "operator intervention"
	pauseID := uuid.New()
	row := sqlc.TournamentLifecycleCommand{
		CommandID: uuid.New(), TournamentID: uuid.New(), RosterID: uuid.New(), ActorID: uuid.New(),
		Action: string(tournamentadmin.TournamentActionPause), SourceProjectionRevisionID: uuid.New(),
		SourceProjectionRevision: 9, SourceTournamentRevision: 4,
		SourceTournamentState: string(domain.TournamentStateSwiss), ResultingTournamentRevision: 5,
		ResultingTournamentState: string(domain.TournamentStateTechnicalPause), Reason: &reason,
		PauseID: uuid.NullUUID{UUID: pauseID, Valid: true}, ExecutionSnapshot: []byte(`{"waves":[]}`),
		Preset: string(domain.TournamentPresetV1), RosterSize: 8,
		TournamentCreatedAt: lifecycleTestTSTZ(createdAt), TournamentUpdatedAt: lifecycleTestTSTZ(pausedAt),
		TournamentStartedAt: lifecycleTestTSTZ(startedAt), ExecutedAt: lifecycleTestTSTZ(pausedAt),
		CreatedAt: lifecycleTestTSTZ(pausedAt),
	}
	record, err := tournamentAdminLifecycleCommand(row)
	require.NoError(t, err)
	require.Equal(t, pauseID, record.PauseID)
	require.Equal(t, reason, record.Reason)
	require.Equal(t, row.ExecutionSnapshot, []byte(record.ExecutionSnapshot))
	require.NotNil(t, record.Result.PausedFromState)
	require.Equal(t, domain.TournamentStateSwiss, *record.Result.PausedFromState)

	params := tournamentAdminLifecycleCommandParams(*record)
	require.Equal(t, row.CommandID, params.CommandID)
	require.Equal(t, pauseID, params.PauseID.UUID)
	require.True(t, params.PauseID.Valid)
	require.Equal(t, row.ResultingTournamentState, params.ResultingTournamentState)
	require.Equal(t, row.ExecutionSnapshot, params.ExecutionSnapshot)
}

func TestTournamentTechnicalPauseRecordRequiresExactCASResult(t *testing.T) {
	t.Parallel()
	pausedAt := lifecycleTestTime()
	input := tournamentpause.TournamentTechnicalPauseInput{
		TournamentID: uuid.New(), ExpectedRevision: 4, ExpectedState: domain.TournamentStateSwiss,
		GraphRevision: 9, CommandID: uuid.New(), PauseID: uuid.New(), ActorID: uuid.New(),
		Reason: "operator intervention", PausedAt: pausedAt,
	}
	origin := string(input.ExpectedState)
	row := sqlc.EnterTournamentTechnicalPauseRow{
		ID: input.TournamentID, State: string(domain.TournamentStateTechnicalPause),
		PausedFromState: &origin, Revision: input.ExpectedRevision + 1, UpdatedAt: lifecycleTestTSTZ(pausedAt),
	}

	record, err := tournamentTechnicalPauseRecord(row, input)
	require.NoError(t, err)
	require.Equal(t, input.CommandID, record.CommandID)
	require.Equal(t, input.PauseID, record.PauseID)
	require.Equal(t, domain.TournamentStateTechnicalPause, record.Tournament.State)

	row.Revision++
	_, err = tournamentTechnicalPauseRecord(row, input)
	require.ErrorIs(t, err, domain.ErrInternal)
}

func lifecycleTestTime() time.Time {
	return time.Date(2026, time.September, 6, 10, 0, 0, 0, time.UTC)
}

func lifecycleTestTSTZ(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}
