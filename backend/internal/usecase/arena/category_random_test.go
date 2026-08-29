package arena_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestRandomCategorySelection(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.August, 29, 12, 0, 0, 0, time.UTC)
	revision := task028CategoryRevision(t, arena.ArenaStageSwiss, task028ID(201), createdAt)
	command := arena.RandomCategorySelectionCommand{
		LockID: revision.ID, EvidenceID: task028ID(203),
		LockedAt: createdAt.Add(time.Minute),
	}

	t.Run("stores reproducible server decision evidence", func(t *testing.T) {
		t.Parallel()

		lock, changed, err := arena.LockRandomSeriesCategory(nil, revision, command)
		if err != nil || !changed {
			t.Fatalf("LockRandomSeriesCategory() error = %v, changed = %v", err, changed)
		}
		if err := lock.Validate(revision); err != nil {
			t.Fatalf("lock Validate() error = %v", err)
		}
		if lock.Mode != domain.ArenaCategoryModeRandom || len(lock.SelectedCategories) != 1 {
			t.Fatalf("random lock = %+v", lock)
		}
		if lock.SelectorActorID != nil || lock.SelectionReason != "" {
			t.Fatalf("random lock contains admin evidence: %+v", lock)
		}
		if lock.DecisionEvidence == nil || lock.DecisionEvidence.Purpose != domain.ArenaDecisionPurposeCategory ||
			lock.DecisionEvidence.AlgorithmVersion != domain.ArenaDecisionAlgorithmV1 ||
			lock.DecisionEvidence.OwnerID != command.LockID || lock.DecisionEvidence.DecidedAt != command.LockedAt {
			t.Fatalf("decision evidence = %+v", lock.DecisionEvidence)
		}
		wantInputs := []string{"crypto", "reverse", "web"}
		if !slices.Equal(lock.DecisionEvidence.NormalizedInputs, wantInputs) {
			t.Fatalf("normalized inputs = %v, want %v", lock.DecisionEvidence.NormalizedInputs, wantInputs)
		}
		if lock.DecisionEvidence.Seed == ([domain.ArenaDecisionSeedSize]byte{}) || lock.ProofHash == "" {
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

	t.Run("returns the stored decision for an exact retry", func(t *testing.T) {
		t.Parallel()

		first, _, err := arena.LockRandomSeriesCategory(nil, revision, command)
		if err != nil {
			t.Fatalf("LockRandomSeriesCategory(first) error = %v", err)
		}
		retried, changed, err := arena.LockRandomSeriesCategory(&first, revision, command)
		if err != nil || changed || !reflect.DeepEqual(retried, first) {
			t.Fatalf("retry error = %v, changed = %v, lock = %+v", err, changed, retried)
		}

		other := command
		other.EvidenceID = task028ID(206)
		other.LockedAt = command.LockedAt.Add(time.Minute)
		if _, changed, err := arena.LockRandomSeriesCategory(&first, revision, other); !errors.Is(err, arena.ErrSeriesCategoryLocked) || changed {
			t.Fatalf("second lock error = %v, changed = %v", err, changed)
		}
	})

	t.Run("rejects wrong mode and tampered lock evidence", func(t *testing.T) {
		t.Parallel()

		foreignIdentity := command
		foreignIdentity.LockID = task028ID(205)
		if _, changed, err := arena.LockRandomSeriesCategory(nil, revision, foreignIdentity); !errors.Is(err, arena.ErrInvalidRandomCategorySelection) || changed {
			t.Fatalf("foreign lock identity error = %v, changed = %v", err, changed)
		}

		adminRevision := task028CategoryRevision(t, arena.ArenaStageSemifinal, task028ID(207), createdAt)
		if _, changed, err := arena.LockRandomSeriesCategory(nil, adminRevision, command); !errors.Is(err, arena.ErrInvalidRandomCategorySelection) || changed {
			t.Fatalf("admin-mode random lock error = %v, changed = %v", err, changed)
		}

		lock, _, err := arena.LockRandomSeriesCategory(nil, revision, command)
		if err != nil {
			t.Fatalf("LockRandomSeriesCategory() error = %v", err)
		}
		lock.SelectedCategories[0] = domain.CategoryMisc
		if err := lock.Validate(revision); !errors.Is(err, arena.ErrInvalidSeriesCategoryLock) {
			t.Fatalf("tampered lock Validate() error = %v", err)
		}
	})
}

func task028CategoryRevision(
	t *testing.T,
	stage arena.ArenaStage,
	id uuid.UUID,
	createdAt time.Time,
) arena.SeriesCategoryRevision {
	t.Helper()

	command := task028CategoryRevisionCommand(task028Configuration(t), stage, id, createdAt)
	revision, changed, err := arena.DeriveSeriesCategoryRevision(nil, command)
	if err != nil || !changed {
		t.Fatalf("DeriveSeriesCategoryRevision() error = %v, changed = %v", err, changed)
	}
	return revision
}
