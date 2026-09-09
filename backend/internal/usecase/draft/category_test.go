package draft_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecasedraft "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func TestSeriesCategoryMode(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.August, 29, 11, 0, 0, 0, time.UTC)

	t.Run("applies a normalized pre-final default", func(t *testing.T) {
		t.Parallel()

		configuration := task028Configuration(t)
		command := task028CategoryRevisionCommand(configuration, domain.TournamentStageSwiss, task028ID(101), createdAt)
		revision, changed, err := usecasedraft.DeriveCategoryRevision(nil, command)
		if err != nil || !changed {
			t.Fatalf("DeriveSeriesCategoryRevision() error = %v, changed = %v", err, changed)
		}
		if revision.Revision != 1 || revision.SupersedesRevisionID != uuid.Nil || revision.RosterID != task028ID(3) {
			t.Fatalf("revision identity = %+v", revision)
		}
		if revision.Format != domain.SeriesFormatBO1 || revision.Mode != domain.CategoryModeRandom {
			t.Fatalf("pre-final mode = %s/%s, want bo1/random", revision.Format, revision.Mode)
		}
		if revision.CategoryPool.ID != task028ID(21) || len(revision.CategoryPool.Categories) != 3 {
			t.Fatalf("category pool = %+v", revision.CategoryPool)
		}
		if err := revision.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}

		configuration.CategoryPools[0].Categories[0] = domain.CategoryMisc
		if revision.CategoryPool.Categories[0] == domain.CategoryMisc {
			t.Fatal("revision retained the caller-owned category pool")
		}
	})

	t.Run("supersedes an unstarted override without mutating the current revision", func(t *testing.T) {
		t.Parallel()

		configuration := task028Configuration(t)
		firstCommand := task028CategoryRevisionCommand(configuration, domain.TournamentStageSwiss, task028ID(102), createdAt)
		first, _, err := usecasedraft.DeriveCategoryRevision(nil, firstCommand)
		if err != nil {
			t.Fatalf("DeriveSeriesCategoryRevision(first) error = %v", err)
		}
		retried, retryChanged, err := usecasedraft.DeriveCategoryRevision(&first, firstCommand)
		if err != nil || retryChanged || !reflect.DeepEqual(retried, first) {
			t.Fatalf("exact retry error = %v, changed = %v, revision = %+v", err, retryChanged, retried)
		}

		adminMode := domain.CategoryModeAdmin
		override := firstCommand
		override.ID = task028ID(103)
		override.CreatedAt = createdAt.Add(time.Minute)
		override.ModeOverride = &adminMode
		second, changed, err := usecasedraft.DeriveCategoryRevision(&first, override)
		if err != nil || !changed {
			t.Fatalf("DeriveSeriesCategoryRevision(override) error = %v, changed = %v", err, changed)
		}
		if second.Revision != 2 || second.SupersedesRevisionID != first.ID || second.Mode != adminMode {
			t.Fatalf("override revision = %+v", second)
		}
		if first.Mode != domain.CategoryModeRandom || first.Revision != 1 {
			t.Fatalf("current revision was mutated: %+v", first)
		}

		noOp := override
		noOp.ID = task028ID(104)
		noOp.CreatedAt = createdAt.Add(2 * time.Minute)
		repeated, repeatedChange, err := usecasedraft.DeriveCategoryRevision(&second, noOp)
		if err != nil || repeatedChange || !reflect.DeepEqual(repeated, second) {
			t.Fatalf("equivalent override error = %v, changed = %v, revision = %+v", err, repeatedChange, repeated)
		}
	})

	t.Run("forces the final to BO3 draft", func(t *testing.T) {
		t.Parallel()

		configuration := task028Configuration(t)
		command := task028CategoryRevisionCommand(configuration, domain.TournamentStageFinal, task028ID(105), createdAt)
		revision, _, err := usecasedraft.DeriveCategoryRevision(nil, command)
		if err != nil {
			t.Fatalf("DeriveSeriesCategoryRevision(final) error = %v", err)
		}
		if revision.Format != domain.SeriesFormatBO3 || revision.Mode != domain.CategoryModeDraft {
			t.Fatalf("final mode = %s/%s, want bo3/draft", revision.Format, revision.Mode)
		}

		adminMode := domain.CategoryModeAdmin
		command.ID = task028ID(106)
		command.ModeOverride = &adminMode
		if _, changed, err := usecasedraft.DeriveCategoryRevision(nil, command); !errors.Is(err, usecasedraft.ErrInvalidCategoryRevision) || changed {
			t.Fatalf("final override error = %v, changed = %v", err, changed)
		}
	})

	t.Run("rejects edits at the category lock or Series start", func(t *testing.T) {
		t.Parallel()

		configuration := task028Configuration(t)
		command := task028CategoryRevisionCommand(configuration, domain.TournamentStageSwiss, task028ID(107), createdAt)
		current, _, err := usecasedraft.DeriveCategoryRevision(nil, command)
		if err != nil {
			t.Fatalf("DeriveSeriesCategoryRevision(current) error = %v", err)
		}
		adminMode := domain.CategoryModeAdmin
		command.ID = task028ID(108)
		command.ModeOverride = &adminMode
		command.CreatedAt = createdAt.Add(time.Minute)
		command.CategoryLocked = true
		if _, changed, err := usecasedraft.DeriveCategoryRevision(&current, command); !errors.Is(err, usecasedraft.ErrCategoryNotEditable) || changed {
			t.Fatalf("locked edit error = %v, changed = %v", err, changed)
		}

		command.CategoryLocked = false
		command.SeriesState = domain.SeriesStateLocked
		if _, changed, err := usecasedraft.DeriveCategoryRevision(&current, command); !errors.Is(err, usecasedraft.ErrCategoryNotEditable) || changed {
			t.Fatalf("started edit error = %v, changed = %v", err, changed)
		}
	})
}

func task028Configuration(t *testing.T) domain.ContentConfiguration {
	t.Helper()

	bo1PoolID := task028ID(21)
	bo3PoolID := task028ID(22)
	configuration, err := domain.CreateContentConfiguration(domain.ContentConfigurationInput{
		TournamentID: task028ID(1),
		CategoryPools: []domain.CategoryPoolRevision{
			{
				ID: bo3PoolID, Revision: 3, Format: domain.SeriesFormatBO3,
				Categories: []domain.Category{
					domain.CategoryPwn,
					domain.CategoryWeb,
					domain.CategoryForensics,
					domain.CategoryReverse,
					domain.CategoryCrypto,
				},
			},
			{
				ID: bo1PoolID, Revision: 2, Format: domain.SeriesFormatBO1,
				Categories: []domain.Category{domain.CategoryReverse, domain.CategoryWeb, domain.CategoryCrypto},
			},
		},
		NormalPool: domain.TaskPoolRevision{
			ID: task028ID(31), Revision: 4, Kind: domain.AssignmentTaskKindNormal,
			Versions: []domain.TaskVersionRef{{TaskID: task028ID(41), Version: 3}},
		},
		GoldenPool: domain.TaskPoolRevision{
			ID: task028ID(32), Revision: 2, Kind: domain.AssignmentTaskKindGolden,
			Versions: []domain.TaskVersionRef{{TaskID: task028ID(42), Version: 5}},
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
				CategoryMode: domain.CategoryModeAdmin, CategoryPoolRevisionID: bo1PoolID,
				TaskPoolKind: domain.AssignmentTaskKindNormal,
			},
			{
				Stage: domain.TournamentStageFinal, Format: domain.SeriesFormatBO3,
				CategoryMode: domain.CategoryModeDraft, CategoryPoolRevisionID: bo3PoolID,
				TaskPoolKind: domain.AssignmentTaskKindNormal,
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateContentConfiguration() error = %v", err)
	}
	return configuration
}

func task028CategoryRevisionCommand(
	configuration domain.ContentConfiguration,
	stage domain.TournamentStage,
	id uuid.UUID,
	createdAt time.Time,
) usecasedraft.CategoryRevisionCommand {
	return usecasedraft.CategoryRevisionCommand{
		ID: id, SeriesID: task028ID(2), RosterID: task028ID(3), SeriesState: domain.SeriesStatePlanned,
		Stage: stage, Configuration: configuration, CreatedAt: createdAt,
	}
}

func task028ID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("28000000-0000-0000-0000-%012d", number))
}
