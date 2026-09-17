package roster

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/roster"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

func TestMissingTournamentPreflightRuntimeFailsClosed(t *testing.T) {
	t.Parallel()
	evaluatedAt := time.Date(2026, time.September, 6, 12, 0, 0, 0, time.UTC)
	authority := rosterusecase.RosterAuthority{
		Roster:               rosterusecase.RosterView{TournamentID: uuid.New()},
		TournamentPreset:     domain.TournamentPresetV1,
		ProjectionRevisionID: uuid.New(), ProjectionRevision: 8,
	}

	runtime := missingTournamentPreflightRuntime(authority, evaluatedAt, 4)

	require.False(t, runtime.Configuration.Valid)
	require.False(t, runtime.Health.Submission.Healthy)
	require.False(t, runtime.Health.TaskDelivery.Healthy)
	require.False(t, runtime.Health.Realtime.Healthy)
	require.Nil(t, runtime.Capacity)
	require.True(t, runtime.Clock.ReferenceAt.IsZero())
	require.True(t, runtime.Schedule.StartsAt.IsZero())
	require.True(t, runtime.Schedule.MustFinishBy.IsZero())
	require.Len(t, runtime.Dependencies, 3)
	require.True(t, runtime.Dependencies[0].Healthy)
	require.False(t, runtime.Dependencies[1].Healthy)
	require.False(t, runtime.Dependencies[2].Healthy)
}

func missingTournamentPreflightRuntime(
	authority rosterusecase.RosterAuthority,
	evaluatedAt time.Time,
	rosterSize int,
) tournamentpreflight.RuntimeInput {
	storageRevision := "postgres:" + authority.ProjectionRevisionID.String()
	return tournamentpreflight.RuntimeInput{
		TournamentID: authority.Roster.TournamentID,
		Preset:       authority.TournamentPreset,
		RosterSize:   rosterSize,
		Health: tournamentpreflight.Health{
			AuthoritativeStorage: tournamentpreflight.ComponentHealth{Healthy: true, Revision: storageRevision},
			Submission:           tournamentpreflight.ComponentHealth{Revision: "unavailable"},
			TaskDelivery:         tournamentpreflight.ComponentHealth{Revision: "unavailable"},
			Realtime:             tournamentpreflight.ComponentHealth{Revision: "unavailable"},
		},
		Dependencies: []tournamentpreflight.DependencyHealth{
			{Name: tournamentpreflight.DependencyPostgres, Healthy: true, Revision: storageRevision},
			{Name: tournamentpreflight.DependencyObjectStorage, Revision: "unavailable"},
			{Name: tournamentpreflight.DependencyRedis, Revision: "unavailable"},
		},
		Clock: tournamentpreflight.ClockHealth{ObservedAt: evaluatedAt},
	}
}

func TestTournamentAdminRosterMutationMapsConstraintConflicts(t *testing.T) {
	t.Parallel()

	for _, code := range []string{pgUniqueViolation, pgForeignKeyViolation, pgRestrictViolation} {
		err := tournamentAdminRosterMutationError("replace", &pgconn.PgError{Code: code})
		require.ErrorIs(t, err, domain.ErrConflict)
	}
}

func TestTournamentRosterParticipantIDIsStableAndScoped(t *testing.T) {
	t.Parallel()
	rosterID := uuid.New()
	playerID := uuid.New()

	first := tournamentRosterParticipantID(rosterID, playerID)
	second := tournamentRosterParticipantID(rosterID, playerID)
	otherRoster := tournamentRosterParticipantID(uuid.New(), playerID)

	require.Equal(t, first, second)
	require.NotEqual(t, first, otherRoster)
}

func TestSameTournamentAdminIDsIgnoresOrderButNotMultiplicity(t *testing.T) {
	t.Parallel()
	first := uuid.New()
	second := uuid.New()

	require.True(t, sameTournamentAdminIDs([]uuid.UUID{first, second}, []uuid.UUID{second, first}))
	require.False(t, sameTournamentAdminIDs([]uuid.UUID{first, first}, []uuid.UUID{first, second}))
}

func TestTournamentAdminRosterOperationMapsExplicitPreflightEvidence(t *testing.T) {
	t.Parallel()
	preflightID := uuid.New()
	playerIDs := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	firstPlayerID := playerIDs[0]
	digest := make([]byte, 32)
	digest[0] = 1
	document := []byte(`{"revision":5}`)
	executedAt := time.Date(2026, time.September, 6, 13, 0, 0, 0, time.UTC)
	row := sqlc.TournamentRosterOperation{
		CommandID: uuid.New(), TournamentID: uuid.New(), RosterID: uuid.New(), ActorID: uuid.New(),
		Action:                     string(rosterusecase.RosterOperationLock),
		PreflightRevisionID:        uuid.NullUUID{UUID: preflightID, Valid: true},
		SourceProjectionRevisionID: uuid.New(), SourceProjectionRevision: 7,
		SourceTournamentRevision: 3, SourceTournamentState: string(domain.TournamentStateRegistration),
		ResultingTournamentRevision: 4, ResultingTournamentState: string(domain.TournamentStateRosterLocked),
		SourceRosterRevision: 4, ResultingRosterRevision: 5,
		RequestDigest: digest, CheckedInPlayerIds: playerIDs, ResultDocument: document,
		ExecutedAt: pgtype.Timestamptz{Time: executedAt, Valid: true},
	}

	record, err := tournamentAdminRosterOperation(row)
	require.NoError(t, err)
	require.Equal(t, preflightID, record.PreflightRevisionID)
	require.Equal(t, playerIDs, record.CheckedInPlayerIDs)
	require.Equal(t, [32]byte{1}, record.RequestDigest)
	require.Equal(t, executedAt, record.ExecutedAt)

	row.CheckedInPlayerIds[0] = uuid.New()
	row.ResultDocument[0] = '['
	require.Equal(t, firstPlayerID, record.CheckedInPlayerIDs[0])
	require.JSONEq(t, `{"revision":5}`, string(record.ResultDocument))
}
