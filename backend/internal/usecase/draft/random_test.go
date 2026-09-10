package draft_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecasedraft "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func TestRandomCategorySelection(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.August, 29, 12, 0, 0, 0, time.UTC)
	revision := task028CategoryRevision(t, domain.TournamentStageSwiss, task028ID(201), createdAt)
	command := usecasedraft.RandomSelectionCommand{
		LockID: revision.ID, EvidenceID: task028ID(203),
		LockedAt: createdAt.Add(time.Minute),
	}

	t.Run("stores reproducible server decision evidence", func(t *testing.T) {
		t.Parallel()

		lock, changed, err := usecasedraft.LockRandom(nil, revision, command)
		if err != nil || !changed {
			t.Fatalf("LockRandomSeriesCategory() error = %v, changed = %v", err, changed)
		}
		if err := lock.Validate(revision); err != nil {
			t.Fatalf("lock Validate() error = %v", err)
		}
		if lock.Mode != domain.CategoryModeRandom || len(lock.SelectedCategories) != 1 {
			t.Fatalf("random lock = %+v", lock)
		}
		if lock.SelectorActorID != nil || lock.SelectionReason != "" {
			t.Fatalf("random lock contains admin evidence: %+v", lock)
		}
		if lock.DecisionEvidence == nil || lock.DecisionEvidence.Purpose != domain.DecisionPurposeCategory ||
			lock.DecisionEvidence.AlgorithmVersion != domain.DecisionAlgorithmV1 ||
			lock.DecisionEvidence.OwnerID != command.LockID || lock.DecisionEvidence.DecidedAt != command.LockedAt {
			t.Fatalf("decision evidence = %+v", lock.DecisionEvidence)
		}
		wantInputs := []string{"crypto", "reverse", "web"}
		if !slices.Equal(lock.DecisionEvidence.NormalizedInputs, wantInputs) {
			t.Fatalf("normalized inputs = %v, want %v", lock.DecisionEvidence.NormalizedInputs, wantInputs)
		}
		if lock.DecisionEvidence.Seed == ([domain.DecisionSeedSize]byte{}) || lock.ProofHash == "" {
			t.Fatal("random lock omitted seed or immutable proof")
		}
		replayed, err := lock.DecisionEvidence.Replay()
		if err != nil {
			t.Fatalf("Replay() error = %v", err)
		}
		if domain.Category(replayed[0]) != lock.SelectedCategories[0] {
			t.Fatalf("selected category = %s, replay first = %s", lock.SelectedCategories[0], replayed[0])
		}
	})

	t.Run("selects three ordered categories for a BO3 revision", func(t *testing.T) {
		t.Parallel()

		randomMode := domain.CategoryModeRandom
		bo3Command := task028CategoryRevisionCommand(task028Configuration(t), domain.TournamentStageFinal, task028ID(208), createdAt)
		bo3Command.ModeOverride = &randomMode
		bo3Revision, changed, err := usecasedraft.DeriveCategoryRevision(nil, bo3Command)
		if err != nil || !changed {
			t.Fatalf("DeriveSeriesCategoryRevision(BO3 random) error = %v, changed = %v", err, changed)
		}
		command := usecasedraft.RandomSelectionCommand{
			LockID: bo3Revision.ID, EvidenceID: task028ID(209), LockedAt: createdAt.Add(time.Minute),
		}
		lock, changed, err := usecasedraft.LockRandom(nil, bo3Revision, command)
		if err != nil || !changed {
			t.Fatalf("LockRandomSeriesCategory(BO3) error = %v, changed = %v", err, changed)
		}
		if err := lock.Validate(bo3Revision); err != nil {
			t.Fatalf("BO3 lock Validate() error = %v", err)
		}
		if len(lock.SelectedCategories) != 3 || lock.DecisionEvidence == nil || len(lock.DecisionEvidence.Result) != 5 {
			t.Fatalf("BO3 random lock = %+v", lock)
		}
		for index, category := range lock.SelectedCategories {
			if string(category) != lock.DecisionEvidence.Result[index] {
				t.Fatalf("selected category %d = %s, evidence result = %s", index, category, lock.DecisionEvidence.Result[index])
			}
			if slices.Contains(lock.SelectedCategories[index+1:], category) {
				t.Fatalf("selected categories contain duplicate %s: %v", category, lock.SelectedCategories)
			}
		}
	})

	t.Run("returns the stored decision for an exact retry", func(t *testing.T) {
		t.Parallel()

		first, _, err := usecasedraft.LockRandom(nil, revision, command)
		if err != nil {
			t.Fatalf("LockRandomSeriesCategory(first) error = %v", err)
		}
		retried, changed, err := usecasedraft.LockRandom(&first, revision, command)
		if err != nil || changed || !reflect.DeepEqual(retried, first) {
			t.Fatalf("retry error = %v, changed = %v, lock = %+v", err, changed, retried)
		}

		other := command
		other.EvidenceID = task028ID(206)
		other.LockedAt = command.LockedAt.Add(time.Minute)
		if _, changed, err := usecasedraft.LockRandom(&first, revision, other); !errors.Is(err, usecasedraft.ErrCategoryLocked) || changed {
			t.Fatalf("second lock error = %v, changed = %v", err, changed)
		}
	})

	t.Run("rejects wrong mode and tampered lock evidence", func(t *testing.T) {
		t.Parallel()

		foreignIdentity := command
		foreignIdentity.LockID = task028ID(205)
		if _, changed, err := usecasedraft.LockRandom(nil, revision, foreignIdentity); !errors.Is(err, usecasedraft.ErrInvalidRandomSelection) || changed {
			t.Fatalf("foreign lock identity error = %v, changed = %v", err, changed)
		}

		adminRevision := task028CategoryRevision(t, domain.TournamentStageSemifinal, task028ID(207), createdAt)
		if _, changed, err := usecasedraft.LockRandom(nil, adminRevision, command); !errors.Is(err, usecasedraft.ErrInvalidRandomSelection) || changed {
			t.Fatalf("admin-mode random lock error = %v, changed = %v", err, changed)
		}

		lock, _, err := usecasedraft.LockRandom(nil, revision, command)
		if err != nil {
			t.Fatalf("LockRandomSeriesCategory() error = %v", err)
		}
		lock.SelectedCategories[0] = domain.CategoryMisc
		if err := lock.Validate(revision); !errors.Is(err, usecasedraft.ErrInvalidCategoryLock) {
			t.Fatalf("tampered lock Validate() error = %v", err)
		}
	})
}

func task028CategoryRevision(
	t *testing.T,
	stage domain.TournamentStage,
	id uuid.UUID,
	createdAt time.Time,
) usecasedraft.CategoryRevision {
	t.Helper()

	command := task028CategoryRevisionCommand(task028Configuration(t), stage, id, createdAt)
	revision, changed, err := usecasedraft.DeriveCategoryRevision(nil, command)
	if err != nil || !changed {
		t.Fatalf("DeriveSeriesCategoryRevision() error = %v, changed = %v", err, changed)
	}
	return revision
}
