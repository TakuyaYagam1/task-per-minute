package progression

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func TestPlayoffSemifinalCategoryRevisionParamsCarryRandomDecisionEvidence(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)
	configuration := swissMaterializationContentConfiguration(t)
	seriesID := uuid.New()
	revision, changed, err := draftusecase.DeriveCategoryRevision(nil, draftusecase.CategoryRevisionCommand{
		ID: uuid.New(), SeriesID: seriesID, RosterID: uuid.New(), SeriesState: domain.SeriesStatePlanned,
		Stage: domain.TournamentStageSemifinal, Configuration: configuration,
		ModeOverride: categoryModePtr(domain.CategoryModeRandom), CreatedAt: createdAt,
	})
	require.NoError(t, err)
	require.True(t, changed)
	lock, changed, err := draftusecase.LockRandom(nil, revision, draftusecase.RandomSelectionCommand{
		LockID: revision.ID, EvidenceID: uuid.New(), LockedAt: createdAt,
	})
	require.NoError(t, err)
	require.True(t, changed)

	params, err := playoffSemifinalCategoryRevisionParams(revision, lock, configuration.NormalPool.ID)
	require.NoError(t, err)
	require.Equal(t, revision.ID, params.ID)
	require.Equal(t, revision.SeriesID, params.SeriesID)
	require.Equal(t, string(domain.CategoryModeRandom), params.Mode)
	require.Equal(t, lock.SelectedCategories, decodeCategoryJSON(t, params.SelectedCategories))
	require.True(t, params.DecisionEvidenceID.Valid)
	require.Equal(t, lock.DecisionEvidence.ID, params.DecisionEvidenceID.UUID)
	require.Equal(t, lock.ID, params.DecisionOwnerID.UUID)
	require.Len(t, params.DecisionSeed, domain.DecisionSeedSize)
	require.Len(t, params.DecisionReplayDigest, domain.DecisionSeedSize)
}

func TestPlayoffSemifinalExactNormalCommandScopesEveryIdentity(t *testing.T) {
	t.Parallel()

	command := tournamentProgressionCommandFixture()
	match := playoff.SemifinalMatch{Series: domain.Series{
		ID: uuid.New(), TournamentID: command.TournamentID,
		FirstParticipantID: uuid.New(), SecondParticipantID: uuid.New(),
		Format: domain.SeriesFormatBO1, State: domain.SeriesStateLocked,
	}}
	categoryRevisionID, slotID, planID := uuid.New(), uuid.New(), uuid.New()
	exact := playoffSemifinalExactNormalCommand(command, match, categoryRevisionID, slotID, planID,
		domain.AssignmentReserveCount, time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC), 1)

	require.Equal(t, command.TournamentID, exact.Scope.TournamentID)
	require.Equal(t, command.RosterID, exact.Scope.RosterID)
	require.Equal(t, match.Series.ID, exact.Scope.SeriesID)
	require.Equal(t, slotID, exact.Scope.SlotID)
	require.Equal(t, categoryRevisionID, exact.Scope.CategoryLockID)
	require.Equal(t, planID, exact.PlanID)
	require.NotEqual(t, exact.PlanID, exact.PlanRevisionID)
	require.NotEqual(t, exact.PlanID, exact.BranchID)
	require.NotEqual(t, exact.PlanID, exact.DecisionEvidenceID)
	for index := range exact.EdgeIDs {
		require.NotEqual(t, uuid.Nil, exact.EdgeIDs[index])
		require.NotEqual(t, exact.EdgeIDs[index], exact.ReservationIDs[index])
		require.NotEqual(t, exact.EdgeIDs[index], exact.SnapshotIDs[index])
	}
}

func TestPlayoffSemifinalMaterializationSQLIsIdempotentAndExecutable(t *testing.T) {
	t.Parallel()

	query, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", "..", "db", "queries", "tournament_progression.sql"))
	require.NoError(t, err)
	contents := string(query)
	for _, fragment := range []string{
		"-- name: CreatePlayoffSemifinalCategoryRevision :one",
		"-- name: LockPlayoffSemifinalSeriesForMaterialization :one",
		"-- name: CreatePlayoffSemifinalWave :exec",
		"-- name: ReadyPlayoffSemifinalGameForMaterialization :one",
		"-- name: ReadyPlayoffSemifinalSeriesForMaterialization :one",
		"ON CONFLICT (command_id, tournament_id) DO NOTHING",
		"ON CONFLICT DO NOTHING",
		"state = 'ready'",
		"state = 'locked'",
	} {
		require.Contains(t, contents, fragment)
	}
}

func TestPlayoffSemifinalReadyAtAdvancesPostgresTimestampPrecision(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.September, 11, 12, 0, 0, 123456789, time.UTC)
	readyAt, err := playoffSemifinalReadyAt(createdAt)
	require.NoError(t, err)
	require.Equal(t, createdAt.Truncate(time.Microsecond).Add(time.Microsecond), readyAt)
	require.True(t, readyAt.After(createdAt.Truncate(time.Microsecond)))

	_, err = playoffSemifinalReadyAt(time.Time{})
	require.ErrorIs(t, err, domain.ErrValidation)
	_, err = playoffSemifinalReadyAt(createdAt.In(time.FixedZone("offset", 3*60*60)))
	require.ErrorIs(t, err, domain.ErrValidation)
}

func categoryModePtr(mode domain.CategoryMode) *domain.CategoryMode {
	return &mode
}

func decodeCategoryJSON(t *testing.T, encoded []byte) []domain.Category {
	t.Helper()
	var result []domain.Category
	require.NoError(t, json.Unmarshal(encoded, &result))
	return result
}

func tournamentProgressionCommandFixture() tournamentprogression.Command {
	return tournamentprogression.Command{
		TournamentID: uuid.New(), RosterID: uuid.New(), CommandID: uuid.New(),
	}
}
