package arena_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestAdminCategorySelection(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.August, 29, 13, 0, 0, 0, time.UTC)
	revision := task028CategoryRevision(t, arena.ArenaStageSemifinal, task028ID(301), createdAt)
	selected := domain.CategoryWeb
	command := arena.AdminCategorySelectionCommand{
		LockID: revision.ID, ActorID: task028ID(303),
		ExpectedCategoryRevisionID: revision.ID, ExpectedCategoryRevision: revision.Revision,
		SelectedCategory: &selected, Reason: " selected by match operator ", LockedAt: createdAt.Add(time.Minute),
	}

	t.Run("locks one eligible category for every expected Game", func(t *testing.T) {
		t.Parallel()

		lock, changed, err := arena.LockAdminSeriesCategory(nil, revision, command)
		if err != nil || !changed {
			t.Fatalf("LockAdminSeriesCategory() error = %v, changed = %v", err, changed)
		}
		if err := lock.Validate(revision); err != nil {
			t.Fatalf("lock Validate() error = %v", err)
		}
		wantGames := revision.Format.WinsRequired()*2 - 1
		if len(lock.SelectedCategories) != wantGames || lock.SelectedCategories[0] != selected {
			t.Fatalf("selected categories = %v, want %d Game category %s", lock.SelectedCategories, wantGames, selected)
		}
		if lock.Mode != domain.ArenaCategoryModeAdmin || lock.SelectorActorID == nil ||
			*lock.SelectorActorID != command.ActorID ||
			lock.SelectionReason != "selected by match operator" || lock.DecisionEvidence != nil || lock.ProofHash == "" {
			t.Fatalf("admin lock evidence = %+v", lock)
		}
	})

	t.Run("rejects missing, ineligible, and stale selections", func(t *testing.T) {
		t.Parallel()

		missing := command
		missing.SelectedCategory = nil
		if _, changed, err := arena.LockAdminSeriesCategory(nil, revision, missing); !errors.Is(err, arena.ErrInvalidAdminCategorySelection) || changed {
			t.Fatalf("missing selection error = %v, changed = %v", err, changed)
		}

		blankReason := command
		blankReason.Reason = "  "
		if _, changed, err := arena.LockAdminSeriesCategory(nil, revision, blankReason); !errors.Is(err, arena.ErrInvalidAdminCategorySelection) || changed {
			t.Fatalf("blank reason error = %v, changed = %v", err, changed)
		}

		ineligibleCategory := domain.CategoryMisc
		ineligible := command
		ineligible.SelectedCategory = &ineligibleCategory
		if _, changed, err := arena.LockAdminSeriesCategory(nil, revision, ineligible); !errors.Is(err, arena.ErrInvalidAdminCategorySelection) || changed {
			t.Fatalf("ineligible selection error = %v, changed = %v", err, changed)
		}

		stale := command
		stale.ExpectedCategoryRevisionID = task028ID(399)
		if _, changed, err := arena.LockAdminSeriesCategory(nil, revision, stale); !errors.Is(err, arena.ErrInvalidAdminCategorySelection) || changed {
			t.Fatalf("stale selection error = %v, changed = %v", err, changed)
		}

		foreignIdentity := command
		foreignIdentity.LockID = task028ID(398)
		if _, changed, err := arena.LockAdminSeriesCategory(nil, revision, foreignIdentity); !errors.Is(err, arena.ErrInvalidAdminCategorySelection) || changed {
			t.Fatalf("foreign lock identity error = %v, changed = %v", err, changed)
		}
	})

	t.Run("keeps the exact retry and rejects every locked change", func(t *testing.T) {
		t.Parallel()

		first, _, err := arena.LockAdminSeriesCategory(nil, revision, command)
		if err != nil {
			t.Fatalf("LockAdminSeriesCategory(first) error = %v", err)
		}
		retried, changed, err := arena.LockAdminSeriesCategory(&first, revision, command)
		if err != nil || changed || !reflect.DeepEqual(retried, first) {
			t.Fatalf("retry error = %v, changed = %v, lock = %+v", err, changed, retried)
		}

		otherCategory := domain.CategoryCrypto
		changedCommand := command
		changedCommand.SelectedCategory = &otherCategory
		changedCommand.LockedAt = command.LockedAt.Add(time.Minute)
		if _, changed, err := arena.LockAdminSeriesCategory(&first, revision, changedCommand); !errors.Is(err, arena.ErrSeriesCategoryLocked) || changed {
			t.Fatalf("locked change error = %v, changed = %v", err, changed)
		}
	})

	t.Run("rejects non-admin Series revisions", func(t *testing.T) {
		t.Parallel()

		randomRevision := task028CategoryRevision(t, arena.ArenaStageSwiss, task028ID(305), createdAt)
		wrongMode := command
		wrongMode.ExpectedCategoryRevisionID = randomRevision.ID
		wrongMode.ExpectedCategoryRevision = randomRevision.Revision
		if _, changed, err := arena.LockAdminSeriesCategory(nil, randomRevision, wrongMode); !errors.Is(err, arena.ErrInvalidAdminCategorySelection) || changed {
			t.Fatalf("random-mode admin lock error = %v, changed = %v", err, changed)
		}
	})
}
