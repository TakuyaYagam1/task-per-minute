//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	catalogrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/catalog"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
	publiccatalog "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/publiccatalog"
)

func TestPublicTournamentCatalogPaginationFiltersAndDetail(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })
	prepareTournamentCreateReceiptContent(ctx, t)

	fixture := newRepositoryFixture()
	service := publiccatalog.NewService(catalogrepo.NewTournamentCatalogPostgres(fixture.tx))
	baseTime := time.Date(2026, 9, 26, 9, 0, 0, 0, time.UTC)

	draft, _ := createCatalogTestTournament(ctx, t, fixture, baseTime, "Draft private tournament", "draft-private")
	literal, _ := createCatalogTestTournament(ctx, t, fixture, baseTime, "Literal_%_Marker", "literal-marker")
	literalFinishedAt := baseTime.Add(time.Second)
	_, changed, err := fixture.tournaments.Transition(ctx, catalogrepo.TournamentTransitionInput{
		ID:               literal.ID,
		ExpectedRevision: literal.Revision,
		ExpectedState:    literal.State,
		NextState:        domain.TournamentStateCancelled,
		UpdatedAt:        literalFinishedAt,
		FinishedAt:       &literalFinishedAt,
	})
	require.NoError(t, err)
	require.True(t, changed)
	literal, err = fixture.tournaments.GetTournament(ctx, literal.ID)
	require.NoError(t, err)
	registration, _ := createCatalogTestTournament(ctx, t, fixture, baseTime.Add(time.Second), "Registration empty", "registration-empty")
	registration = transitionRepositoryTournament(ctx, t, fixture, registration, domain.TournamentStateRegistration, baseTime.Add(2*time.Second))

	pause, _ := createCatalogTestTournament(ctx, t, fixture, baseTime.Add(3*time.Second), "Paused Swiss", "paused-swiss")
	pause = transitionRepositoryTournament(ctx, t, fixture, pause, domain.TournamentStateRegistration, baseTime.Add(4*time.Second))
	pause = transitionRepositoryTournament(ctx, t, fixture, pause, domain.TournamentStateRosterLocked, baseTime.Add(5*time.Second))
	pause = transitionRepositoryTournament(ctx, t, fixture, pause, domain.TournamentStateSwiss, baseTime.Add(6*time.Second))
	pauseOrigin := domain.TournamentStateSwiss
	paused, changed, err := fixture.tournaments.Transition(ctx, catalogrepo.TournamentTransitionInput{
		ID:               pause.ID,
		ExpectedRevision: pause.Revision,
		ExpectedState:    pause.State,
		NextState:        domain.TournamentStateTechnicalPause,
		PausedFromState:  &pauseOrigin,
		UpdatedAt:        baseTime.Add(7 * time.Second),
		StartedAt:        pause.StartedAt,
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.TournamentStateTechnicalPause, domain.TournamentState(paused.State))

	for index := 0; index < 25; index++ {
		createdAt := baseTime.Add(time.Duration(index) * time.Minute)
		name := fmt.Sprintf("Cancelled %02d", index)
		publicID := fmt.Sprintf("cancelled-%02d", index)
		cancelled, _ := createCatalogTestTournament(ctx, t, fixture, createdAt, name, publicID)
		finishedAt := createdAt.Add(time.Minute)
		_, changed, err := fixture.tournaments.Transition(ctx, catalogrepo.TournamentTransitionInput{
			ID:               cancelled.ID,
			ExpectedRevision: cancelled.Revision,
			ExpectedState:    cancelled.State,
			NextState:        domain.TournamentStateCancelled,
			UpdatedAt:        finishedAt,
			FinishedAt:       &finishedAt,
		})
		require.NoError(t, err)
		require.True(t, changed)
	}

	var all []inbound.PublicTournamentCatalogView
	var cursor *inbound.PublicTournamentCatalogCursor
	for {
		page, err := service.ListPublicTournaments(ctx, inbound.PublicTournamentCatalogQuery{
			Group: inbound.PublicTournamentCatalogFilterAll,
			Sort:  inbound.PublicTournamentCatalogSortNewest,
			Limit: 7,
			After: cursor,
		})
		require.NoError(t, err)
		all = append(all, page.Items...)
		if page.Next == nil {
			break
		}
		cursor = page.Next
	}
	require.GreaterOrEqual(t, len(all), 25)
	seen := make(map[uuid.UUID]struct{}, len(all))
	for _, item := range all {
		_, duplicate := seen[item.TournamentID]
		require.False(t, duplicate, "catalog pagination repeated %s", item.TournamentID)
		seen[item.TournamentID] = struct{}{}
	}

	literalPage, err := service.ListPublicTournaments(ctx, inbound.PublicTournamentCatalogQuery{
		Search: "%_", Group: inbound.PublicTournamentCatalogFilterAll,
		Sort: inbound.PublicTournamentCatalogSortName, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, literalPage.Items, 1)
	require.Equal(t, literal.ID, literalPage.Items[0].TournamentID)

	completedPage, err := service.ListPublicTournaments(ctx, inbound.PublicTournamentCatalogQuery{
		Group: inbound.PublicTournamentCatalogFilterCompleted,
		Sort:  inbound.PublicTournamentCatalogSortActivity, Limit: 50,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(completedPage.Items), 25)
	for _, item := range completedPage.Items {
		require.Equal(t, inbound.PublicTournamentCatalogGroupCompleted, item.Group)
		require.Equal(t, domain.TournamentStateCancelled, item.State)
		require.Equal(t, domain.TournamentStateCancelled, item.Stage)
		require.NotNil(t, item.FinishedAt)
	}

	livePage, err := service.ListPublicTournaments(ctx, inbound.PublicTournamentCatalogQuery{
		Group: inbound.PublicTournamentCatalogFilterLive,
		Sort:  inbound.PublicTournamentCatalogSortActivity, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, livePage.Items, 1)
	require.Equal(t, pause.ID, livePage.Items[0].TournamentID)
	require.Equal(t, domain.TournamentStateTechnicalPause, livePage.Items[0].State)
	require.Equal(t, domain.TournamentStateSwiss, livePage.Items[0].Stage)

	upcomingPage, err := service.ListPublicTournaments(ctx, inbound.PublicTournamentCatalogQuery{
		Group: inbound.PublicTournamentCatalogFilterUpcoming,
		Sort:  inbound.PublicTournamentCatalogSortActivity, Limit: 20,
	})
	require.NoError(t, err)
	require.Len(t, upcomingPage.Items, 1)
	require.Equal(t, registration.ID, upcomingPage.Items[0].TournamentID)
	require.Zero(t, upcomingPage.Items[0].RosterSize)

	detail, err := service.GetPublicTournamentByPublicID(ctx, literal.PublicID)
	require.NoError(t, err)
	require.Equal(t, literal.ID, detail.TournamentID)
	require.Equal(t, literal.PublicID, detail.PublicID)

	_, err = service.GetPublicTournamentByPublicID(ctx, draft.PublicID)
	require.ErrorIs(t, err, domain.ErrTournamentNotFound)

	firstPage, err := service.ListPublicTournaments(ctx, inbound.PublicTournamentCatalogQuery{
		Group: inbound.PublicTournamentCatalogFilterAll,
		Sort:  inbound.PublicTournamentCatalogSortName, Limit: 1,
	})
	require.NoError(t, err)
	require.NotNil(t, firstPage.Next)
	_, err = service.ListPublicTournaments(ctx, inbound.PublicTournamentCatalogQuery{
		Group: inbound.PublicTournamentCatalogFilterCompleted,
		Sort:  inbound.PublicTournamentCatalogSortName, Limit: 1,
		After: firstPage.Next,
	})
	require.ErrorIs(t, err, domain.ErrValidation)
}

func TestPublicTournamentCatalogUsesStableTieKeys(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })
	prepareTournamentCreateReceiptContent(ctx, t)

	fixture := newRepositoryFixture()
	service := publiccatalog.NewService(catalogrepo.NewTournamentCatalogPostgres(fixture.tx))
	createdAt := time.Date(2026, 9, 26, 11, 0, 0, 0, time.UTC)
	for index := 0; index < 3; index++ {
		item, _ := createCatalogTestTournament(ctx, t, fixture, createdAt, "Same Name", fmt.Sprintf("same-name-%d", index))
		finishedAt := createdAt.Add(time.Second)
		_, changed, err := fixture.tournaments.Transition(ctx, catalogrepo.TournamentTransitionInput{
			ID: item.ID, ExpectedRevision: item.Revision, ExpectedState: item.State,
			NextState: domain.TournamentStateCancelled, UpdatedAt: finishedAt, FinishedAt: &finishedAt,
		})
		require.NoError(t, err)
		require.True(t, changed)
	}

	for _, sort := range []inbound.PublicTournamentCatalogSort{
		inbound.PublicTournamentCatalogSortName,
		inbound.PublicTournamentCatalogSortNewest,
	} {
		var cursor *inbound.PublicTournamentCatalogCursor
		seen := make(map[uuid.UUID]struct{})
		for {
			page, err := service.ListPublicTournaments(ctx, inbound.PublicTournamentCatalogQuery{
				Sort: sort, Group: inbound.PublicTournamentCatalogFilterCompleted, Limit: 1, After: cursor,
			})
			require.NoError(t, err)
			for _, item := range page.Items {
				_, duplicate := seen[item.TournamentID]
				require.False(t, duplicate)
				seen[item.TournamentID] = struct{}{}
			}
			if page.Next == nil {
				break
			}
			cursor = page.Next
		}
		require.Len(t, seen, 3)
	}
}

func createCatalogTestTournament(
	ctx context.Context,
	tb testing.TB,
	fixture *repositoryFixture,
	createdAt time.Time,
	name string,
	publicID string,
) (*catalogusecase.CatalogTournamentRecord, *catalogusecase.CatalogRosterRecord) {
	tb.Helper()
	contentRevision := ensureTaskPoolPublicationRevision(ctx, tb)
	tournament, roster, err := fixture.tournaments.CreateTournamentDraft(ctx, catalogusecase.TournamentCreateCommand{
		TournamentID: uuid.New(), RosterID: uuid.New(), Name: name, PublicID: publicID,
		PlannedRosterSize: 4, ContentRevision: contentRevision,
	}, createdAt)
	require.NoError(tb, err)
	require.Equal(tb, domain.TournamentStateDraft, tournament.State)
	return tournament, roster
}
