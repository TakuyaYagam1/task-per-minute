package arena_test

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestContentConfigurationRevision(t *testing.T) {
	t.Parallel()

	t.Run("keeps distinct normalized pools and stage defaults", func(t *testing.T) {
		t.Parallel()

		input := task020ContentInput()
		configuration, err := arena.CreateContentConfiguration(input)
		if err != nil {
			t.Fatalf("CreateContentConfiguration() error = %v", err)
		}
		if configuration.Revision != 1 {
			t.Fatalf("revision = %d, want 1", configuration.Revision)
		}
		if configuration.NormalPool.Kind != domain.ArenaTaskKindNormal ||
			configuration.GoldenPool.Kind != domain.ArenaTaskKindGolden ||
			configuration.NormalPool.ID == configuration.GoldenPool.ID {
			t.Fatalf("task pools are not distinct: normal = %+v, Golden = %+v", configuration.NormalPool, configuration.GoldenPool)
		}
		if got := len(configuration.StageDefaults); got != 4 {
			t.Fatalf("stage defaults = %d, want 4", got)
		}
		if err := configuration.Validate(); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}

		input.CategoryPools[0].Categories[0] = domain.CategoryMisc
		input.NormalPool.Versions[0].Version = 99
		if configuration.CategoryPools[0].Categories[0] == domain.CategoryMisc ||
			configuration.NormalPool.Versions[0].Version == 99 {
			t.Fatal("configuration retained caller-owned slices")
		}
	})

	t.Run("normalizes equivalent input without invalidation", func(t *testing.T) {
		t.Parallel()

		input := task020ContentInput()
		current, err := arena.CreateContentConfiguration(input)
		if err != nil {
			t.Fatalf("CreateContentConfiguration() error = %v", err)
		}
		current, err = current.WithDerivedRevisions(task020ID(90), task020ID(91))
		if err != nil {
			t.Fatalf("WithDerivedRevisions() error = %v", err)
		}
		for i := range input.CategoryPools {
			slices.Reverse(input.CategoryPools[i].Categories)
		}
		slices.Reverse(input.CategoryPools)
		slices.Reverse(input.NormalPool.Versions)
		slices.Reverse(input.StageDefaults)

		next, changed, err := arena.ReviseContentConfiguration(current, input)
		if err != nil {
			t.Fatalf("ReviseContentConfiguration() error = %v", err)
		}
		if changed {
			t.Fatal("equivalent normalized input changed the configuration")
		}
		if !reflect.DeepEqual(next, current) {
			t.Fatalf("idempotent revision = %+v, want %+v", next, current)
		}
	})

	t.Run("invalidates derived revisions after pool change", func(t *testing.T) {
		t.Parallel()

		input := task020ContentInput()
		current, err := arena.CreateContentConfiguration(input)
		if err != nil {
			t.Fatalf("CreateContentConfiguration() error = %v", err)
		}
		current, err = current.WithDerivedRevisions(task020ID(90), task020ID(91))
		if err != nil {
			t.Fatalf("WithDerivedRevisions() error = %v", err)
		}
		input.NormalPool.ID = task020ID(32)
		input.NormalPool.Revision++
		input.NormalPool.Versions = append(input.NormalPool.Versions, arena.TaskVersionRef{
			TaskID: task020ID(13), Version: 1,
		})

		next, changed, err := arena.ReviseContentConfiguration(current, input)
		if err != nil {
			t.Fatalf("ReviseContentConfiguration() error = %v", err)
		}
		if !changed || next.Revision != current.Revision+1 {
			t.Fatalf("changed = %v, revision = %d, want true and %d", changed, next.Revision, current.Revision+1)
		}
		if next.PlanRevisionID != uuid.Nil || next.PreflightRevisionID != uuid.Nil {
			t.Fatalf("derived revisions survived content change: plan = %s, preflight = %s", next.PlanRevisionID, next.PreflightRevisionID)
		}
		if next.GoldenPool.ID != current.GoldenPool.ID || next.GoldenPool.Revision != current.GoldenPool.Revision {
			t.Fatal("normal pool revision changed the Golden pool")
		}
	})

	t.Run("rejects invalid shapes and revision reuse", func(t *testing.T) {
		t.Parallel()

		base := task020ContentInput()
		current, err := arena.CreateContentConfiguration(base)
		if err != nil {
			t.Fatalf("CreateContentConfiguration() error = %v", err)
		}

		wrongShape := task020ContentInput()
		wrongShape.CategoryPools[0].Categories = wrongShape.CategoryPools[0].Categories[:2]
		overlap := task020ContentInput()
		overlap.GoldenPool.Versions[0].TaskID = overlap.NormalPool.Versions[0].TaskID
		wrongFinal := task020ContentInput()
		for i := range wrongFinal.StageDefaults {
			if wrongFinal.StageDefaults[i].Stage == arena.ArenaStageFinal {
				wrongFinal.StageDefaults[i].CategoryMode = domain.ArenaCategoryModeRandom
			}
		}
		for name, input := range map[string]arena.ContentConfigurationInput{
			"category shape": wrongShape,
			"pool overlap":   overlap,
			"final mode":     wrongFinal,
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				if _, createErr := arena.CreateContentConfiguration(input); !errors.Is(createErr, arena.ErrInvalidContentConfiguration) {
					t.Fatalf("CreateContentConfiguration() error = %v, want ErrInvalidContentConfiguration", createErr)
				}
			})
		}

		stale := task020ContentInput()
		stale.NormalPool.ID = task020ID(32)
		stale.NormalPool.Versions = append(stale.NormalPool.Versions, arena.TaskVersionRef{
			TaskID: task020ID(13), Version: 1,
		})
		if _, changed, reviseErr := arena.ReviseContentConfiguration(current, stale); !errors.Is(reviseErr, arena.ErrInvalidContentConfiguration) || changed {
			t.Fatalf("ReviseContentConfiguration(stale) error = %v, changed = %v", reviseErr, changed)
		}
	})
}

func task020ContentInput() arena.ContentConfigurationInput {
	bo1PoolID := task020ID(21)
	bo3PoolID := task020ID(22)
	return arena.ContentConfigurationInput{
		TournamentID: task020ID(1),
		CategoryPools: []arena.CategoryPoolRevision{
			{
				ID: bo1PoolID, Revision: 1, Format: domain.ArenaSeriesFormatBO1,
				Categories: []domain.Category{domain.CategoryReverse, domain.CategoryWeb, domain.CategoryCrypto},
			},
			{
				ID: bo3PoolID, Revision: 1, Format: domain.ArenaSeriesFormatBO3,
				Categories: []domain.Category{
					domain.CategoryPwn,
					domain.CategoryWeb,
					domain.CategoryForensics,
					domain.CategoryReverse,
					domain.CategoryCrypto,
				},
			},
		},
		NormalPool: arena.TaskPoolRevision{
			ID: task020ID(30), Revision: 4, Kind: domain.ArenaTaskKindNormal,
			Versions: []arena.TaskVersionRef{
				{TaskID: task020ID(11), Version: 3},
				{TaskID: task020ID(12), Version: 2},
			},
		},
		GoldenPool: arena.TaskPoolRevision{
			ID: task020ID(31), Revision: 2, Kind: domain.ArenaTaskKindGolden,
			Versions: []arena.TaskVersionRef{{TaskID: task020ID(14), Version: 5}},
		},
		StageDefaults: []arena.StageContentDefault{
			{
				Stage: arena.ArenaStageSwiss, Format: domain.ArenaSeriesFormatBO1,
				CategoryMode: domain.ArenaCategoryModeRandom, CategoryPoolRevisionID: bo1PoolID,
				TaskPoolKind: domain.ArenaTaskKindNormal,
			},
			{
				Stage: arena.ArenaStageGolden, Format: domain.ArenaSeriesFormatBO1,
				CategoryMode: domain.ArenaCategoryModeRandom, CategoryPoolRevisionID: bo1PoolID,
				TaskPoolKind: domain.ArenaTaskKindGolden,
			},
			{
				Stage: arena.ArenaStageSemifinal, Format: domain.ArenaSeriesFormatBO1,
				CategoryMode: domain.ArenaCategoryModeDraft, CategoryPoolRevisionID: bo1PoolID,
				TaskPoolKind: domain.ArenaTaskKindNormal,
			},
			{
				Stage: arena.ArenaStageFinal, Format: domain.ArenaSeriesFormatBO3,
				CategoryMode: domain.ArenaCategoryModeDraft, CategoryPoolRevisionID: bo3PoolID,
				TaskPoolKind: domain.ArenaTaskKindNormal,
			},
		},
	}
}

func task020ID(suffix int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("20000000-0000-0000-0000-%012d", suffix))
}
