package preflight_test

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
)

func preflightContentInput() domain.ContentConfigurationInput {
	bo1PoolID := preflightContentID(21)
	bo3PoolID := preflightContentID(22)
	return domain.ContentConfigurationInput{
		TournamentID: preflightContentID(1),
		CategoryPools: []domain.CategoryPoolRevision{
			{
				ID: bo1PoolID, Revision: 1, Format: domain.SeriesFormatBO1,
				Categories: []domain.Category{domain.CategoryReverse, domain.CategoryWeb, domain.CategoryCrypto},
			},
			{
				ID: bo3PoolID, Revision: 1, Format: domain.SeriesFormatBO3,
				Categories: []domain.Category{
					domain.CategoryPwn,
					domain.CategoryWeb,
					domain.CategoryForensics,
					domain.CategoryReverse,
					domain.CategoryCrypto,
				},
			},
		},
		NormalPool: domain.TaskPoolRevision{
			ID: preflightContentID(30), Revision: 4, Kind: domain.AssignmentTaskKindNormal,
			Versions: []domain.TaskVersionRef{
				{TaskID: preflightContentID(11), Version: 3},
				{TaskID: preflightContentID(12), Version: 2},
			},
		},
		GoldenPool: domain.TaskPoolRevision{
			ID: preflightContentID(31), Revision: 2, Kind: domain.AssignmentTaskKindGolden,
			Versions: []domain.TaskVersionRef{{TaskID: preflightContentID(14), Version: 5}},
		},
		StageDefaults: []domain.StageContentDefault{
			{
				Stage: domain.TournamentStageSwiss, Format: domain.SeriesFormatBO1,
				CategoryMode: domain.CategoryModeRandom, CategoryPoolRevisionID: bo1PoolID,
				TaskPoolKind: domain.AssignmentTaskKindNormal,
			},
			{
				Stage: domain.TournamentStageGolden, Format: domain.SeriesFormatBO1,
				CategoryMode: domain.CategoryModeRandom, CategoryPoolRevisionID: bo1PoolID,
				TaskPoolKind: domain.AssignmentTaskKindGolden,
			},
			{
				Stage: domain.TournamentStageSemifinal, Format: domain.SeriesFormatBO1,
				CategoryMode: domain.CategoryModeDraft, CategoryPoolRevisionID: bo1PoolID,
				TaskPoolKind: domain.AssignmentTaskKindNormal,
			},
			{
				Stage: domain.TournamentStageFinal, Format: domain.SeriesFormatBO3,
				CategoryMode: domain.CategoryModeDraft, CategoryPoolRevisionID: bo3PoolID,
				TaskPoolKind: domain.AssignmentTaskKindNormal,
			},
		},
	}
}

func preflightNormalCapacityInput(rosterSize int) capacity.NormalInput {
	participants := make([]uuid.UUID, rosterSize)
	for index := range participants {
		participants[index] = preflightCapacityID(100 + index)
	}

	bo1PoolID := preflightCapacityID(10)
	bo3PoolID := preflightCapacityID(11)
	normalPoolID := preflightCapacityID(20)
	categoryPools := []domain.CategoryPoolRevision{
		{
			ID: bo1PoolID, Revision: 2, Format: domain.SeriesFormatBO1,
			Categories: []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryReverse},
		},
		{
			ID: bo3PoolID, Revision: 3, Format: domain.SeriesFormatBO3,
			Categories: []domain.Category{
				domain.CategoryWeb,
				domain.CategoryCrypto,
				domain.CategoryReverse,
				domain.CategoryPwn,
				domain.CategoryForensics,
			},
		},
	}

	swissRounds, err := domain.TournamentPresetV1.SwissRounds(rosterSize)
	if err != nil {
		panic(err)
	}
	chainSize := domain.AssignmentReserveCount + 1
	sharedCategoryCount := (rosterSize/2*swissRounds + 3) * chainSize
	versions := make([]capacity.TaskVersion, 0, sharedCategoryCount*3+6)
	for _, category := range []struct {
		category domain.Category
		count    int
	}{
		{domain.CategoryWeb, sharedCategoryCount},
		{domain.CategoryCrypto, sharedCategoryCount},
		{domain.CategoryReverse, sharedCategoryCount},
		{domain.CategoryPwn, chainSize},
		{domain.CategoryForensics, chainSize},
	} {
		for range category.count {
			versions = append(versions, capacity.TaskVersion{
				TaskVersionRef: domain.TaskVersionRef{
					TaskID: preflightCapacityID(1000 + len(versions)), Version: 1,
				},
				PoolRevisionID: normalPoolID,
				PoolKind:       domain.AssignmentTaskKindNormal,
				Category:       category.category,
			})
		}
	}
	poolRefs := make([]domain.TaskVersionRef, len(versions))
	for index, version := range versions {
		poolRefs[index] = version.TaskVersionRef
	}

	return capacity.NormalInput{
		Preset:         domain.TournamentPresetV1,
		ParticipantIDs: participants,
		CategoryPools:  categoryPools,
		NormalPool: domain.TaskPoolRevision{
			ID: normalPoolID, Revision: 7, Kind: domain.AssignmentTaskKindNormal, Versions: poolRefs,
		},
		Versions: versions,
	}
}

func preflightGoldenCapacityInput(rosterSize int) capacity.GoldenInput {
	participants := make([]uuid.UUID, rosterSize)
	for index := range participants {
		participants[index] = preflightCapacityID(200 + index)
	}
	goldenPoolID := preflightCapacityID(30)
	required := rosterSize / 2 * (domain.AssignmentReserveCount + 1)
	versions := make([]capacity.TaskVersion, required)
	refs := make([]domain.TaskVersionRef, required)
	for index := range versions {
		ref := domain.TaskVersionRef{TaskID: preflightCapacityID(2000 + index), Version: 2}
		refs[index] = ref
		versions[index] = capacity.TaskVersion{
			TaskVersionRef: ref,
			PoolRevisionID: goldenPoolID,
			PoolKind:       domain.AssignmentTaskKindGolden,
			Category:       domain.CategoryMisc,
		}
	}
	return capacity.GoldenInput{
		Preset:         domain.TournamentPresetV1,
		ParticipantIDs: participants,
		NormalPool: domain.TaskPoolRevision{
			ID: preflightCapacityID(20), Revision: 7, Kind: domain.AssignmentTaskKindNormal,
			Versions: []domain.TaskVersionRef{{TaskID: preflightCapacityID(3000), Version: 1}},
		},
		GoldenPool: domain.TaskPoolRevision{
			ID: goldenPoolID, Revision: 5, Kind: domain.AssignmentTaskKindGolden, Versions: refs,
		},
		Versions: versions,
	}
}

func preflightContentID(suffix int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("20000000-0000-0000-0000-%012d", suffix))
}

func preflightCapacityID(suffix int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("21000000-0000-0000-0000-%012d", suffix))
}
