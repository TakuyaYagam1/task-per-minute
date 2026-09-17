package execution

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
	pairingusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/pairing"
)

func TestSwissRandomMaterializationPlanRequiresOneSeriesPerPair(t *testing.T) {
	t.Parallel()

	plan := pairingusecase.PairingPlan{
		Command: pairingusecase.PairingCommand{
			CommandScope: tournamentadmin.CommandScope{TournamentID: uuid.New()},
			CategoryMode: domain.CategoryModeRandom,
		},
		Authority: pairingusecase.PairingAuthority{RosterID: uuid.New()},
		Pairs:     []swissusecase.Pair{{FirstParticipantID: uuid.New(), SecondParticipantID: uuid.New()}},
		SeriesIDs: []uuid.UUID{uuid.New()}, DecidedAt: time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC),
	}
	require.True(t, validSwissRandomMaterializationPlan(plan))

	plan.SeriesIDs = nil
	require.False(t, validSwissRandomMaterializationPlan(plan))
}

func TestSwissAdminMaterializationPlanRequiresOneCategoryAndActor(t *testing.T) {
	t.Parallel()

	plan := pairingusecase.PairingPlan{
		Command: pairingusecase.PairingCommand{
			CommandScope: tournamentadmin.CommandScope{
				Operator:     tournamentadmin.OperatorIdentity{ActorID: uuid.New()},
				TournamentID: uuid.New(),
			},
			CategoryMode: domain.CategoryModeAdmin,
			Categories:   []domain.Category{domain.CategoryWeb},
		},
		Authority: pairingusecase.PairingAuthority{RosterID: uuid.New()},
		Pairs:     []swissusecase.Pair{{FirstParticipantID: uuid.New(), SecondParticipantID: uuid.New()}},
		SeriesIDs: []uuid.UUID{uuid.New()}, DecidedAt: time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC),
	}
	require.True(t, validSwissAdminMaterializationPlan(plan))

	plan.Command.Categories = []domain.Category{domain.CategoryWeb, domain.CategoryCrypto}
	require.False(t, validSwissAdminMaterializationPlan(plan))
	plan.Command.Categories = []domain.Category{domain.CategoryWeb}
	plan.Command.Operator.ActorID = uuid.Nil
	require.False(t, validSwissAdminMaterializationPlan(plan))
}

func TestSwissCategoryRevisionParamsRejectsUnprovenLock(t *testing.T) {
	t.Parallel()

	params, err := swissCategoryRevisionParams(
		draftusecase.CategoryRevision{}, draftusecase.CategoryLock{},
		uuid.New(),
	)
	require.ErrorIs(t, err, domain.ErrValidation)
	require.Equal(t, sqlc.CreateSwissCategoryRevisionParams{}, params)
}

func TestSwissCategoryRevisionParamsCarriesRandomDecisionEvidence(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	configuration := swissMaterializationContentConfiguration(t)
	revision, changed, err := draftusecase.DeriveCategoryRevision(nil, draftusecase.CategoryRevisionCommand{
		ID: uuid.New(), SeriesID: uuid.New(), RosterID: uuid.New(), SeriesState: domain.SeriesStatePlanned,
		Stage: domain.TournamentStageSwiss, Configuration: configuration, CreatedAt: createdAt,
	})
	require.NoError(t, err)
	require.True(t, changed)
	lock, changed, err := draftusecase.LockRandom(nil, revision, draftusecase.RandomSelectionCommand{
		LockID: revision.ID, EvidenceID: uuid.New(), LockedAt: createdAt,
	})
	require.NoError(t, err)
	require.True(t, changed)

	params, err := swissCategoryRevisionParams(revision, lock, configuration.NormalPool.ID)
	require.NoError(t, err)
	require.Equal(t, revision.ID, params.ID)
	require.Equal(t, revision.SeriesID, params.SeriesID)
	require.Equal(t, configuration.NormalPool.ID, params.SourcePoolRevisionID)
	require.Equal(t, string(domain.CategoryModeRandom), params.Mode)
	require.True(t, params.DecisionEvidenceID.Valid)
	require.Equal(t, lock.DecisionEvidence.ID, params.DecisionEvidenceID.UUID)
	require.Equal(t, lock.ID, params.DecisionOwnerID.UUID)
	require.Len(t, params.DecisionSeed, domain.DecisionSeedSize)
	require.Len(t, params.DecisionReplayDigest, domain.DecisionSeedSize)
}

func TestSwissCategoryRevisionParamsCarriesAdminSelectionEvidence(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	configuration := swissMaterializationContentConfiguration(t)
	actorID := uuid.New()
	revision, changed, err := draftusecase.DeriveCategoryRevision(nil, draftusecase.CategoryRevisionCommand{
		ID: uuid.New(), SeriesID: uuid.New(), RosterID: uuid.New(), SeriesState: domain.SeriesStatePlanned,
		Stage: domain.TournamentStageSwiss, Configuration: configuration,
		ModeOverride: func() *domain.CategoryMode { mode := domain.CategoryModeAdmin; return &mode }(), CreatedAt: createdAt,
	})
	require.NoError(t, err)
	require.True(t, changed)
	selected := domain.CategoryWeb
	lock, changed, err := draftusecase.LockAdmin(nil, revision, draftusecase.AdminSelectionCommand{
		LockID: revision.ID, ActorID: actorID, ExpectedCategoryRevisionID: revision.ID,
		ExpectedCategoryRevision: revision.Revision, SelectedCategory: &selected,
		Reason: "operator selection", LockedAt: createdAt,
	})
	require.NoError(t, err)
	require.True(t, changed)

	params, err := swissCategoryRevisionParams(revision, lock, configuration.NormalPool.ID)
	require.NoError(t, err)
	require.Equal(t, string(domain.CategoryModeAdmin), params.Mode)
	require.Equal(t, actorID, params.SelectorActorID.UUID)
	require.True(t, params.SelectorActorID.Valid)
	require.NotNil(t, params.SelectionReason)
	require.Equal(t, "operator selection", *params.SelectionReason)
	require.True(t, params.DecidedAt.Valid)
	require.Equal(t, lock.LockedAt, params.DecidedAt.Time)
	require.False(t, params.DecisionEvidenceID.Valid)
	require.Nil(t, params.DecisionAlgorithmVersion)
}

func TestSwissMaterializationSQLContractKeepsGraphAtomic(t *testing.T) {
	t.Parallel()

	query, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "..", "db", "queries", "tournament_admin_materialization.sql"))
	require.NoError(t, err)
	contents := string(query)
	for _, fragment := range []string{
		"-- name: CreateSwissCategoryRevision :one",
		"-- name: LockSwissSeriesForMaterialization :one",
		"-- name: ReadySwissGameForMaterialization :one",
		"-- name: ReadySwissSeriesForMaterialization :one",
		"state = 'locked'",
		"state = 'ready'",
		"decision_replay_digest",
	} {
		require.Contains(t, contents, fragment)
	}
}

func swissMaterializationContentConfiguration(t *testing.T) domain.ContentConfiguration {
	t.Helper()
	bo1PoolID := uuid.New()
	bo3PoolID := uuid.New()
	configuration, err := domain.CreateContentConfiguration(domain.ContentConfigurationInput{
		TournamentID: uuid.New(),
		CategoryPools: []domain.CategoryPoolRevision{
			{ID: bo1PoolID, Revision: 1, Format: domain.SeriesFormatBO1,
				Categories: []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryForensics}},
			{ID: bo3PoolID, Revision: 1, Format: domain.SeriesFormatBO3,
				Categories: []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryForensics, domain.CategoryReverse, domain.CategoryPwn}},
		},
		NormalPool: domain.TaskPoolRevision{
			ID: uuid.New(), Revision: 1, Kind: domain.AssignmentTaskKindNormal,
			Versions: []domain.TaskVersionRef{{TaskID: uuid.New(), Version: 1}, {TaskID: uuid.New(), Version: 1}, {TaskID: uuid.New(), Version: 1}},
		},
		GoldenPool: domain.TaskPoolRevision{
			ID: uuid.New(), Revision: 1, Kind: domain.AssignmentTaskKindGolden,
			Versions: []domain.TaskVersionRef{{TaskID: uuid.New(), Version: 1}},
		},
		StageDefaults: []domain.StageContentDefault{
			{Stage: domain.TournamentStageSwiss, Format: domain.SeriesFormatBO1, CategoryMode: domain.CategoryModeRandom, CategoryPoolRevisionID: bo1PoolID, TaskPoolKind: domain.AssignmentTaskKindNormal},
			{Stage: domain.TournamentStageGolden, Format: domain.SeriesFormatBO1, CategoryMode: domain.CategoryModeRandom, CategoryPoolRevisionID: bo1PoolID, TaskPoolKind: domain.AssignmentTaskKindGolden},
			{Stage: domain.TournamentStageSemifinal, Format: domain.SeriesFormatBO1, CategoryMode: domain.CategoryModeAdmin, CategoryPoolRevisionID: bo1PoolID, TaskPoolKind: domain.AssignmentTaskKindNormal},
			{Stage: domain.TournamentStageFinal, Format: domain.SeriesFormatBO3, CategoryMode: domain.CategoryModeDraft, CategoryPoolRevisionID: bo3PoolID, TaskPoolKind: domain.AssignmentTaskKindNormal},
		},
	})
	require.NoError(t, err)
	return configuration
}
