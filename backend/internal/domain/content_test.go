package domain_test

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestContentConfigurationRevision(t *testing.T) {
	t.Parallel()

	t.Run("keeps distinct normalized pools and stage defaults", func(t *testing.T) {
		t.Parallel()

		input := task020ContentInput()
		configuration, err := domain.CreateContentConfiguration(input)
		if err != nil {
			t.Fatalf("CreateContentConfiguration() error = %v", err)
		}
		if configuration.Revision != 1 {
			t.Fatalf("revision = %d, want 1", configuration.Revision)
		}
		if configuration.NormalPool.Kind != domain.AssignmentTaskKindNormal ||
			configuration.GoldenPool.Kind != domain.AssignmentTaskKindGolden ||
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
		current, err := domain.CreateContentConfiguration(input)
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

		next, changed, err := domain.ReviseContentConfiguration(current, input)
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
		current, err := domain.CreateContentConfiguration(input)
		if err != nil {
			t.Fatalf("CreateContentConfiguration() error = %v", err)
		}
		current, err = current.WithDerivedRevisions(task020ID(90), task020ID(91))
		if err != nil {
			t.Fatalf("WithDerivedRevisions() error = %v", err)
		}
		input.NormalPool.ID = task020ID(32)
		input.NormalPool.Revision++
		input.NormalPool.Versions = append(input.NormalPool.Versions, domain.TaskVersionRef{
			TaskID: task020ID(13), Version: 1,
		})

		next, changed, err := domain.ReviseContentConfiguration(current, input)
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
		current, err := domain.CreateContentConfiguration(base)
		if err != nil {
			t.Fatalf("CreateContentConfiguration() error = %v", err)
		}

		wrongShape := task020ContentInput()
		wrongShape.CategoryPools[0].Categories = wrongShape.CategoryPools[0].Categories[:2]
		overlap := task020ContentInput()
		overlap.GoldenPool.Versions[0].TaskID = overlap.NormalPool.Versions[0].TaskID
		wrongFinal := task020ContentInput()
		for i := range wrongFinal.StageDefaults {
			if wrongFinal.StageDefaults[i].Stage == domain.TournamentStageFinal {
				wrongFinal.StageDefaults[i].Format = domain.SeriesFormatBO1
				wrongFinal.StageDefaults[i].CategoryPoolRevisionID = task020ID(21)
			}
		}
		for name, input := range map[string]domain.ContentConfigurationInput{
			"category shape": wrongShape,
			"pool overlap":   overlap,
			"final format":   wrongFinal,
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				if _, createErr := domain.CreateContentConfiguration(input); !errors.Is(createErr, domain.ErrInvalidContentConfiguration) {
					t.Fatalf("CreateContentConfiguration() error = %v, want ErrInvalidContentConfiguration", createErr)
				}
			})
		}

		stale := task020ContentInput()
		stale.NormalPool.ID = task020ID(32)
		stale.NormalPool.Versions = append(stale.NormalPool.Versions, domain.TaskVersionRef{
			TaskID: task020ID(13), Version: 1,
		})
		if _, changed, reviseErr := domain.ReviseContentConfiguration(current, stale); !errors.Is(reviseErr, domain.ErrInvalidContentConfiguration) || changed {
			t.Fatalf("ReviseContentConfiguration(stale) error = %v, changed = %v", reviseErr, changed)
		}
	})
}

func TestContentConfigurationStageDefaultModes(t *testing.T) {
	t.Parallel()

	stages := []struct {
		name  string
		stage domain.TournamentStage
	}{
		{name: "swiss", stage: domain.TournamentStageSwiss},
		{name: "Golden", stage: domain.TournamentStageGolden},
		{name: "semifinal", stage: domain.TournamentStageSemifinal},
		{name: "final", stage: domain.TournamentStageFinal},
	}
	modes := []domain.CategoryMode{
		domain.CategoryModeRandom,
		domain.CategoryModeAdmin,
		domain.CategoryModeDraft,
	}
	for _, stage := range stages {

		for _, mode := range modes {

			t.Run(stage.name+"/"+string(mode), func(t *testing.T) {
				t.Parallel()

				input := task020ContentInput()
				for i := range input.StageDefaults {
					if input.StageDefaults[i].Stage == stage.stage {
						input.StageDefaults[i].CategoryMode = mode
					}
				}
				if _, err := domain.CreateContentConfiguration(input); err != nil {
					t.Fatalf("CreateContentConfiguration() error = %v", err)
				}
			})
		}
	}
}

func TestContentConfigurationStageFormatMatrix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                   string
		stage                  domain.TournamentStage
		format                 domain.SeriesFormat
		categoryPoolRevisionID uuid.UUID
	}{
		{
			name:                   "swiss BO3",
			stage:                  domain.TournamentStageSwiss,
			format:                 domain.SeriesFormatBO3,
			categoryPoolRevisionID: task020ID(22),
		},
		{
			name:                   "Golden BO3",
			stage:                  domain.TournamentStageGolden,
			format:                 domain.SeriesFormatBO3,
			categoryPoolRevisionID: task020ID(22),
		},
		{
			name:                   "semifinal BO3",
			stage:                  domain.TournamentStageSemifinal,
			format:                 domain.SeriesFormatBO3,
			categoryPoolRevisionID: task020ID(22),
		},
		{
			name:                   "final BO1",
			stage:                  domain.TournamentStageFinal,
			format:                 domain.SeriesFormatBO1,
			categoryPoolRevisionID: task020ID(21),
		},
	}
	for _, tc := range cases {

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			input := task020ContentInput()
			for i := range input.StageDefaults {
				if input.StageDefaults[i].Stage == tc.stage {
					input.StageDefaults[i].Format = tc.format
					input.StageDefaults[i].CategoryPoolRevisionID = tc.categoryPoolRevisionID
				}
			}
			if _, err := domain.CreateContentConfiguration(input); !errors.Is(err, domain.ErrInvalidContentConfiguration) {
				t.Fatalf("CreateContentConfiguration() error = %v, want ErrInvalidContentConfiguration", err)
			}
		})
	}
}

func task020ContentInput() domain.ContentConfigurationInput {
	bo1PoolID := task020ID(21)
	bo3PoolID := task020ID(22)
	return domain.ContentConfigurationInput{
		TournamentID: task020ID(1),
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
			ID: task020ID(30), Revision: 4, Kind: domain.AssignmentTaskKindNormal,
			Versions: []domain.TaskVersionRef{
				{TaskID: task020ID(11), Version: 3},
				{TaskID: task020ID(12), Version: 2},
			},
		},
		GoldenPool: domain.TaskPoolRevision{
			ID: task020ID(31), Revision: 2, Kind: domain.AssignmentTaskKindGolden,
			Versions: []domain.TaskVersionRef{{TaskID: task020ID(14), Version: 5}},
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

func task020ID(suffix int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("20000000-0000-0000-0000-%012d", suffix))
}
