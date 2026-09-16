//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	catalogrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/catalog"
	rosterrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/roster"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
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
) (*catalogusecase.CatalogTournamentRecord, *rosterrepo.RosterRecord) {
	tb.Helper()
	tournamentID := uuid.New()
	contentRevision := ensureTaskPoolPublicationRevision(ctx, tb)
	tournament, rosterSummary, err := fixture.tournaments.CreateTournamentDraft(ctx, catalogusecase.TournamentCreateCommand{
		TournamentID: tournamentID, RosterID: uuid.New(), Name: "Repository Tournament", PublicID: tournamentID.String(),
		PlannedRosterSize: 4, ContentRevision: contentRevision,
	}, createdAt)
	require.NoError(tb, err)
	roster, err := fixture.roster.GetRoster(ctx, rosterSummary.ID)
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
) *rosterrepo.ParticipantRecord {
	tb.Helper()
	participant, changed, err := fixture.roster.AddParticipant(ctx, rosterrepo.ParticipantInput{
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
	current *catalogusecase.CatalogTournamentRecord,
	next domain.TournamentState,
	at time.Time,
) *catalogusecase.CatalogTournamentRecord {
	tb.Helper()
	input := catalogrepo.TournamentTransitionInput{
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
	_, changed, err := fixture.tournaments.Transition(ctx, input)
	require.NoError(tb, err)
	require.True(tb, changed)
	updated, err := fixture.tournaments.GetTournament(ctx, current.ID)
	require.NoError(tb, err)
	return updated
}
