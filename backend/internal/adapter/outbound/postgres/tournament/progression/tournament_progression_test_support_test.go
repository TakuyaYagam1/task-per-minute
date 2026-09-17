package progression

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func stringPointer(value string) *string {
	return &value
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
