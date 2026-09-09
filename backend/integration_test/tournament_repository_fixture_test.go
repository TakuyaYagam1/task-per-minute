//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog/mocks"
)

func newTournamentClock(t *testing.T, now time.Time) *tournamentmocks.MockClock {
	t.Helper()
	clock := tournamentmocks.NewMockClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}

func createRepositoryTournament(
	ctx context.Context, tb testing.TB,
	fixture *repositoryFixture,
	createdAt time.Time,
) (*postgres.TournamentRecord, *postgres.RosterRecord) {
	tb.Helper()
	tournament, roster, err := fixture.tournaments.Create(ctx, uuid.New(), uuid.New(), createdAt)
	require.NoError(tb, err)
	require.Equal(tb, domain.TournamentStateDraft, tournament.State)
	require.EqualValues(tb, 1, tournament.Revision)
	require.EqualValues(tb, 1, roster.Revision)
	return tournament, roster
}

func addRepositoryParticipant(
	ctx context.Context, tb testing.TB,
	fixture *repositoryFixture,
	rosterID uuid.UUID,
	playerID uuid.UUID,
	seed int32,
	attendance domain.AttendanceState,
	createdAt time.Time,
) *postgres.ParticipantRecord {
	tb.Helper()
	participant, changed, err := fixture.tournaments.AddParticipant(ctx, postgres.ParticipantInput{
		ID:         uuid.New(),
		RosterID:   rosterID,
		PlayerID:   playerID,
		Seed:       seed,
		Attendance: attendance,
		CreatedAt:  createdAt,
	})
	require.NoError(tb, err)
	require.True(tb, changed)
	return participant
}

func transitionRepositoryTournament(
	ctx context.Context, tb testing.TB,
	fixture *repositoryFixture,
	current *postgres.TournamentRecord,
	next domain.TournamentState,
	at time.Time,
) *postgres.TournamentRecord {
	tb.Helper()
	input := postgres.TournamentTransitionInput{
		ID:               current.ID,
		ExpectedRevision: current.Revision,
		ExpectedState:    current.State,
		NextState:        next,
		UpdatedAt:        at,
		StartedAt:        current.StartedAt,
		FinishedAt:       current.FinishedAt,
	}
	if next == domain.TournamentStateSwiss && input.StartedAt == nil {
		input.StartedAt = &at
	}
	updated, changed, err := fixture.tournaments.Transition(ctx, input)
	require.NoError(tb, err)
	require.True(tb, changed)
	return updated
}
