package draft_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecasedraft "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func TestAdminCategorySelection(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.August, 29, 13, 0, 0, 0, time.UTC)
	revision := task028CategoryRevision(t, domain.TournamentStageSemifinal, task028ID(301), createdAt)
	selected := domain.CategoryWeb
	command := usecasedraft.AdminSelectionCommand{
		LockID: revision.ID, ActorID: task028ID(303),
		ExpectedCategoryRevisionID: revision.ID, ExpectedCategoryRevision: revision.Revision,
		SelectedCategory: &selected, Reason: " selected by match operator ", LockedAt: createdAt.Add(time.Minute),
	}

	t.Run("locks one eligible category for every expected Game", func(t *testing.T) {
		t.Parallel()

		lock, changed, err := usecasedraft.LockAdmin(nil, revision, command)
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
		if lock.Mode != domain.CategoryModeAdmin || lock.SelectorActorID == nil ||
			*lock.SelectorActorID != command.ActorID ||
			lock.SelectionReason != "selected by match operator" || lock.DecisionEvidence != nil || lock.ProofHash == "" {
			t.Fatalf("admin lock evidence = %+v", lock)
		}
	})

	t.Run("locks three ordered categories for a BO3 revision", func(t *testing.T) {
		t.Parallel()

		adminMode := domain.CategoryModeAdmin
		categoryCommand := task028CategoryRevisionCommand(task028Configuration(t), domain.TournamentStageFinal, task028ID(306), createdAt)
		categoryCommand.ModeOverride = &adminMode
		revision, changed, err := usecasedraft.DeriveCategoryRevision(nil, categoryCommand)
		if err != nil || !changed {
			t.Fatalf("DeriveSeriesCategoryRevision(BO3 admin) error = %v, changed = %v", err, changed)
		}
		selectedCategories := []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryReverse}
		command := usecasedraft.AdminSelectionCommand{
			LockID: revision.ID, ActorID: task028ID(307),
			ExpectedCategoryRevisionID: revision.ID, ExpectedCategoryRevision: revision.Revision,
			SelectedCategories: selectedCategories, Reason: " selected by match operator ", LockedAt: createdAt.Add(time.Minute),
		}
		lock, changed, err := usecasedraft.LockAdmin(nil, revision, command)
		if err != nil || !changed {
			t.Fatalf("LockAdminSeriesCategory(BO3) error = %v, changed = %v", err, changed)
		}
		if err := lock.Validate(revision); err != nil {
			t.Fatalf("BO3 lock Validate() error = %v", err)
		}
		if !slices.Equal(lock.SelectedCategories, selectedCategories) {
			t.Fatalf("selected categories = %v, want %v", lock.SelectedCategories, selectedCategories)
		}
	})

	t.Run("rejects duplicate, insufficient, and ineligible BO3 selections", func(t *testing.T) {
		t.Parallel()

		adminMode := domain.CategoryModeAdmin
		categoryCommand := task028CategoryRevisionCommand(task028Configuration(t), domain.TournamentStageFinal, task028ID(308), createdAt)
		categoryCommand.ModeOverride = &adminMode
		revision, changed, err := usecasedraft.DeriveCategoryRevision(nil, categoryCommand)
		if err != nil || !changed {
			t.Fatalf("DeriveSeriesCategoryRevision(BO3 admin) error = %v, changed = %v", err, changed)
		}
		base := usecasedraft.AdminSelectionCommand{
			LockID: revision.ID, ActorID: task028ID(309),
			ExpectedCategoryRevisionID: revision.ID, ExpectedCategoryRevision: revision.Revision,
			SelectedCategories: []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryReverse},
			Reason:             "operator choice", LockedAt: createdAt.Add(time.Minute),
		}

		duplicate := base
		duplicate.SelectedCategories = []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryWeb}
		if _, changed, err := usecasedraft.LockAdmin(nil, revision, duplicate); !errors.Is(err, usecasedraft.ErrInvalidAdminSelection) || changed {
			t.Fatalf("duplicate selection error = %v, changed = %v", err, changed)
		}

		insufficient := base
		insufficient.SelectedCategories = []domain.Category{domain.CategoryWeb, domain.CategoryCrypto}
		if _, changed, err := usecasedraft.LockAdmin(nil, revision, insufficient); !errors.Is(err, usecasedraft.ErrInvalidAdminSelection) || changed {
			t.Fatalf("insufficient selection error = %v, changed = %v", err, changed)
		}

		ineligible := base
		ineligible.SelectedCategories = []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryMisc}
		if _, changed, err := usecasedraft.LockAdmin(nil, revision, ineligible); !errors.Is(err, usecasedraft.ErrInvalidAdminSelection) || changed {
			t.Fatalf("ineligible selection error = %v, changed = %v", err, changed)
		}
	})

	t.Run("rejects missing, ineligible, and stale selections", func(t *testing.T) {
		t.Parallel()

		missing := command
		missing.SelectedCategory = nil
		if _, changed, err := usecasedraft.LockAdmin(nil, revision, missing); !errors.Is(err, usecasedraft.ErrInvalidAdminSelection) || changed {
			t.Fatalf("missing selection error = %v, changed = %v", err, changed)
		}

		blankReason := command
		blankReason.Reason = "  "
		if _, changed, err := usecasedraft.LockAdmin(nil, revision, blankReason); !errors.Is(err, usecasedraft.ErrInvalidAdminSelection) || changed {
			t.Fatalf("blank reason error = %v, changed = %v", err, changed)
		}

		ineligibleCategory := domain.CategoryMisc
		ineligible := command
		ineligible.SelectedCategory = &ineligibleCategory
		if _, changed, err := usecasedraft.LockAdmin(nil, revision, ineligible); !errors.Is(err, usecasedraft.ErrInvalidAdminSelection) || changed {
			t.Fatalf("ineligible selection error = %v, changed = %v", err, changed)
		}

		stale := command
		stale.ExpectedCategoryRevisionID = task028ID(399)
		if _, changed, err := usecasedraft.LockAdmin(nil, revision, stale); !errors.Is(err, usecasedraft.ErrInvalidAdminSelection) || changed {
			t.Fatalf("stale selection error = %v, changed = %v", err, changed)
		}

		foreignIdentity := command
		foreignIdentity.LockID = task028ID(398)
		if _, changed, err := usecasedraft.LockAdmin(nil, revision, foreignIdentity); !errors.Is(err, usecasedraft.ErrInvalidAdminSelection) || changed {
			t.Fatalf("foreign lock identity error = %v, changed = %v", err, changed)
		}
	})

	t.Run("keeps the exact retry and rejects every locked change", func(t *testing.T) {
		t.Parallel()

		first, _, err := usecasedraft.LockAdmin(nil, revision, command)
		if err != nil {
			t.Fatalf("LockAdminSeriesCategory(first) error = %v", err)
		}
		retried, changed, err := usecasedraft.LockAdmin(&first, revision, command)
		if err != nil || changed || !reflect.DeepEqual(retried, first) {
			t.Fatalf("retry error = %v, changed = %v, lock = %+v", err, changed, retried)
		}

		otherCategory := domain.CategoryCrypto
		changedCommand := command
		changedCommand.SelectedCategory = &otherCategory
		changedCommand.LockedAt = command.LockedAt.Add(time.Minute)
		if _, changed, err := usecasedraft.LockAdmin(&first, revision, changedCommand); !errors.Is(err, usecasedraft.ErrCategoryLocked) || changed {
			t.Fatalf("locked change error = %v, changed = %v", err, changed)
		}
	})

	t.Run("binds every BO3 category in an exact retry", func(t *testing.T) {
		t.Parallel()

		adminMode := domain.CategoryModeAdmin
		categoryCommand := task028CategoryRevisionCommand(task028Configuration(t), domain.TournamentStageFinal, task028ID(310), createdAt)
		categoryCommand.ModeOverride = &adminMode
		revision, changed, err := usecasedraft.DeriveCategoryRevision(nil, categoryCommand)
		if err != nil || !changed {
			t.Fatalf("DeriveSeriesCategoryRevision(BO3 admin) error = %v, changed = %v", err, changed)
		}
		command := usecasedraft.AdminSelectionCommand{
			LockID: revision.ID, ActorID: task028ID(311),
			ExpectedCategoryRevisionID: revision.ID, ExpectedCategoryRevision: revision.Revision,
			SelectedCategories: []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryReverse},
			Reason:             "operator choice", LockedAt: createdAt.Add(time.Minute),
		}
		first, changed, err := usecasedraft.LockAdmin(nil, revision, command)
		if err != nil || !changed {
			t.Fatalf("LockAdminSeriesCategory(first BO3) error = %v, changed = %v", err, changed)
		}
		retried, changed, err := usecasedraft.LockAdmin(&first, revision, command)
		if err != nil || changed || !reflect.DeepEqual(retried, first) {
			t.Fatalf("BO3 retry error = %v, changed = %v, lock = %+v", err, changed, retried)
		}
		changedCommand := command
		changedCommand.SelectedCategories = []domain.Category{domain.CategoryWeb, domain.CategoryReverse, domain.CategoryCrypto}
		if _, changed, err := usecasedraft.LockAdmin(&first, revision, changedCommand); !errors.Is(err, usecasedraft.ErrCategoryLocked) || changed {
			t.Fatalf("BO3 reordered change error = %v, changed = %v", err, changed)
		}
	})

	t.Run("rejects non-admin Series revisions", func(t *testing.T) {
		t.Parallel()

		randomRevision := task028CategoryRevision(t, domain.TournamentStageSwiss, task028ID(305), createdAt)
		wrongMode := command
		wrongMode.ExpectedCategoryRevisionID = randomRevision.ID
		wrongMode.ExpectedCategoryRevision = randomRevision.Revision
		if _, changed, err := usecasedraft.LockAdmin(nil, randomRevision, wrongMode); !errors.Is(err, usecasedraft.ErrInvalidAdminSelection) || changed {
			t.Fatalf("random-mode admin lock error = %v, changed = %v", err, changed)
		}
	})
}
