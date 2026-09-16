//go:build integration

package integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	draftrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestDraftRepositoryPersistsOneImmutableRevisionChain(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	tournamentID := createMigrationTournament(ctx, t)
	rosterID := createMigrationRoster(ctx, t, tournamentID)
	players := createMigrationPlayers(ctx, t, 2)
	participants := createSwissMigrationParticipants(ctx, t, rosterID, players)
	seriesID := createMigrationSeries(ctx, t, tournamentID, rosterID, participants, "bo1")
	baseTime := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	repository := draftrepo.NewDraftPostgres(postgres.NewTxManager(sharedPool))

	draftID := uuid.New()
	decision, err := domain.NewDecisionEvidence(
		uuid.New(), domain.DecisionPurposeDraftOrder, domain.DecisionAlgorithmV1,
		[]string{participants[0].String(), participants[1].String()}, draftID, baseTime,
	)
	require.NoError(t, err)
	serviceEpoch := uuid.New()
	initialRevisionID := uuid.New()
	draft, err := repository.Create(ctx, draftrepo.DraftCreateInput{
		ID: draftID, SeriesID: seriesID, RosterID: rosterID, CategoryRevisionID: uuid.New(),
		CategoryRevision: 1, SourcePoolRevision: uuid.New(), FirstParticipantID: participants[0],
		SecondParticipantID: participants[1], Format: domain.SeriesFormatBO1,
		Pool:              []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryPwn},
		InitialRevisionID: initialRevisionID, CommandID: uuid.New(), ServiceEpoch: serviceEpoch,
		AbsoluteDeadline: baseTime.Add(15 * time.Second), DecisionEvidence: decision, CreatedAt: baseTime,
	})
	require.NoError(t, err)
	require.Len(t, draft.Revisions, 1)
	require.EqualValues(t, 1, draft.Revisions[0].Revision)

	expected := draftrepo.DraftRevisionExpectation{ID: initialRevisionID, Revision: 1, ServiceEpoch: serviceEpoch}
	type appendResult struct {
		record  *draftrepo.DraftRevisionRecord
		changed bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan appendResult, 2)
	var workers sync.WaitGroup
	for index, category := range []domain.Category{domain.CategoryWeb, domain.CategoryCrypto} {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			deadline := baseTime.Add(30 * time.Second)
			record, changed, appendErr := repository.AppendRevision(ctx, draftID, expected, draftrepo.DraftRevisionInput{
				ID: uuid.New(), CommandID: uuid.New(), ServiceEpoch: serviceEpoch,
				State: draftrepo.DraftPersistenceStateActive, TurnNumber: 2,
				CurrentActorID: &participants[1], CurrentAction: draftActionPointer(domain.DraftActionBan),
				AbsoluteDeadline: &deadline, CreatedAt: baseTime.Add(time.Duration(index+1) * time.Second),
				Action: &draftrepo.DraftActionInput{
					ID: uuid.New(), TurnNumber: 1, ActorID: participants[0], Action: domain.DraftActionBan,
					Category: category, ScheduledDeadline: baseTime.Add(15 * time.Second),
					OccurredAt: baseTime.Add(time.Second),
				},
			})
			results <- appendResult{record: record, changed: changed, err: appendErr}
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	var winningRevision *draftrepo.DraftRevisionRecord
	winners := 0
	for item := range results {
		require.NoError(t, item.err)
		if item.changed {
			winners++
			winningRevision = item.record
		}
	}
	require.Equal(t, 1, winners)
	require.NotNil(t, winningRevision)

	loaded, err := repository.Get(ctx, draftID)
	require.NoError(t, err)
	require.Len(t, loaded.Revisions, 2)
	require.Len(t, loaded.Actions, 1)
	require.Equal(t, 1, loaded.Actions[0].TurnNumber)
	require.Equal(t, participants[0], loaded.Actions[0].ActorID)
	require.Equal(t, participants[1], *loaded.Revisions[1].CurrentActorID)

	selected := domain.CategoryPwn
	if loaded.Actions[0].Category == selected {
		selected = domain.CategoryWeb
	}
	completed, changed, err := repository.AppendRevision(
		ctx,
		draftID,
		draftrepo.DraftRevisionExpectation{
			ID: winningRevision.ID, Revision: winningRevision.Revision, ServiceEpoch: serviceEpoch,
		},
		draftrepo.DraftRevisionInput{
			ID: uuid.New(), CommandID: uuid.New(), ServiceEpoch: serviceEpoch,
			State: draftrepo.DraftPersistenceStateCompleted, TurnNumber: 2,
			SelectedCategories: []domain.Category{selected}, CreatedAt: baseTime.Add(3 * time.Second),
			Action: &draftrepo.DraftActionInput{
				ID: uuid.New(), TurnNumber: 2, ActorID: participants[1], Action: domain.DraftActionBan,
				Category:          otherDraftBanCategory(loaded.Actions[0].Category, selected),
				ScheduledDeadline: baseTime.Add(30 * time.Second), OccurredAt: baseTime.Add(2 * time.Second),
			},
		},
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.EqualValues(t, 3, completed.Revision)

	_, changed, err = repository.AppendRevision(ctx, draftID, expected, draftrepo.DraftRevisionInput{
		ID: uuid.New(), CommandID: uuid.New(), ServiceEpoch: serviceEpoch,
		State: draftrepo.DraftPersistenceStateCompleted, TurnNumber: 2,
		SelectedCategories: []domain.Category{selected}, CreatedAt: baseTime.Add(4 * time.Second),
	})
	require.NoError(t, err)
	require.False(t, changed, "stale revision must not append a competing history")

	loaded, err = repository.Get(ctx, draftID)
	require.NoError(t, err)
	require.Len(t, loaded.Revisions, 3)
	require.Len(t, loaded.Actions, 2)
	require.Equal(t, draftrepo.DraftPersistenceStateCompleted, loaded.Revisions[2].State)
	require.Equal(t, []domain.Category{selected}, loaded.Revisions[2].SelectedCategories)
}

func draftActionPointer(value domain.DraftActionType) *domain.DraftActionType {
	return &value
}

func otherDraftBanCategory(first, selected domain.Category) domain.Category {
	for _, category := range []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryPwn} {
		if category != first && category != selected {
			return category
		}
	}
	return ""
}

func TestDraftRepositorySeparatesActorOrderFromSeriesIdentity(t *testing.T) {
	ctx := context.Background()
	truncateRoundProofTables(ctx, t)
	t.Cleanup(func() { truncateRoundProofTables(ctx, t) })
	tournamentID := createMigrationTournament(ctx, t)
	rosterID := createMigrationRoster(ctx, t, tournamentID)
	players := createMigrationPlayers(ctx, t, 3)
	participants := createSwissMigrationParticipants(ctx, t, rosterID, players)
	seriesID := createMigrationSeries(ctx, t, tournamentID, rosterID, participants[:2], "bo1")
	tx := postgres.NewTxManager(sharedPool)
	for _, name := range []string{"reversed", "outsider", "duplicate", "format"} {
		t.Run(name, func(t *testing.T) {
			first, second := participants[1], participants[0]
			format := domain.SeriesFormatBO1
			pool := []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryPwn}
			switch name {
			case "outsider":
				second = participants[2]
			case "duplicate":
				second = first
			case "format":
				format = domain.SeriesFormatBO3
				pool = append(pool, domain.CategoryReverse, domain.CategoryForensics)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			draftID := uuid.New()
			decision, err := domain.NewDecisionEvidence(uuid.New(), domain.DecisionPurposeDraftOrder, domain.DecisionAlgorithmV1,
				[]string{participants[0].String(), participants[1].String()}, draftID, now)
			require.NoError(t, err)
			rollback := errors.New("rollback accepted draft fixture")
			err = tx.Do(ctx, func(txCtx context.Context) error {
				created, err := draftrepo.NewDraftPostgres(tx).Create(txCtx, draftrepo.DraftCreateInput{
					ID: draftID, SeriesID: seriesID, RosterID: rosterID, CategoryRevisionID: uuid.New(), CategoryRevision: 1,
					SourcePoolRevision: uuid.New(), FirstParticipantID: first, SecondParticipantID: second, Format: format, Pool: pool,
					InitialRevisionID: uuid.New(), CommandID: uuid.New(), ServiceEpoch: uuid.New(), AbsoluteDeadline: now.Add(15 * time.Second), DecisionEvidence: decision, CreatedAt: now,
				})
				if err != nil {
					return err
				}
				require.Equal(t, first, created.Draft.FirstParticipantID)
				require.Equal(t, second, created.Draft.SecondParticipantID)
				require.Equal(t, first, *created.Revisions[0].CurrentActorID)
				return rollback
			})
			if name == "reversed" {
				require.ErrorIs(t, err, rollback)
			} else {
				require.Error(t, err)
				require.NotErrorIs(t, err, rollback)
			}
		})
	}
}
