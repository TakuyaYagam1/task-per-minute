package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

func TestArenaDraftRulesAcceptCategoryModes(t *testing.T) {
	for _, mode := range []domain.ArenaCategoryMode{
		domain.ArenaCategoryModeRandom,
		domain.ArenaCategoryModeAdmin,
		domain.ArenaCategoryModeDraft,
	} {
		if !mode.IsValid() {
			t.Fatalf("valid mode rejected: %q", mode)
		}
	}
	if domain.ArenaCategoryMode("client").IsValid() {
		t.Fatal("unknown mode accepted")
	}
}

func TestArenaDraftRulesBO1UsesTwoBansAndRemainingCategory(t *testing.T) {
	draft, firstID, secondID, now := newArenaDraft(t, domain.ArenaSeriesFormatBO1, []domain.Category{
		domain.CategoryWeb,
		domain.CategoryCrypto,
		domain.CategoryReverse,
	})

	applyDraftAction(t, &draft, 1, firstID, domain.ArenaDraftActionBan, domain.CategoryWeb, now, now.Add(2*time.Minute))
	applyDraftAction(t, &draft, 2, secondID, domain.ArenaDraftActionBan, domain.CategoryCrypto, now.Add(time.Minute), time.Time{})

	if draft.State != domain.ArenaDraftStateCompleted {
		t.Fatalf("draft state = %s", draft.State)
	}
	assertCategories(t, draft.SelectedCategories, []domain.Category{domain.CategoryReverse})
	if err := draft.Validate(); err != nil {
		t.Fatalf("validate draft: %v", err)
	}
}

func TestArenaDraftRulesBO3UsesTwoBansTwoPicksAndRemainingCategory(t *testing.T) {
	draft, firstID, secondID, now := newArenaDraft(t, domain.ArenaSeriesFormatBO3, []domain.Category{
		domain.CategoryWeb,
		domain.CategoryCrypto,
		domain.CategoryReverse,
		domain.CategoryPwn,
		domain.CategoryForensics,
	})

	applyDraftAction(t, &draft, 1, firstID, domain.ArenaDraftActionBan, domain.CategoryWeb, now, now.Add(2*time.Minute))
	applyDraftAction(t, &draft, 2, secondID, domain.ArenaDraftActionBan, domain.CategoryCrypto, now.Add(time.Minute), now.Add(3*time.Minute))
	applyDraftAction(t, &draft, 3, firstID, domain.ArenaDraftActionPick, domain.CategoryReverse, now.Add(2*time.Minute), now.Add(4*time.Minute))
	applyDraftAction(t, &draft, 4, secondID, domain.ArenaDraftActionPick, domain.CategoryPwn, now.Add(3*time.Minute), time.Time{})

	assertCategories(t, draft.SelectedCategories, []domain.Category{
		domain.CategoryReverse,
		domain.CategoryPwn,
		domain.CategoryForensics,
	})
	if err := draft.Validate(); err != nil {
		t.Fatalf("validate draft: %v", err)
	}
}

func TestArenaDraftRulesRejectStaleDuplicateAndIllegalActions(t *testing.T) {
	draft, firstID, secondID, now := newArenaDraft(t, domain.ArenaSeriesFormatBO1, []domain.Category{
		domain.CategoryWeb,
		domain.CategoryCrypto,
		domain.CategoryReverse,
	})

	if err := draft.ApplyAction(2, firstID, domain.ArenaDraftActionBan, domain.CategoryWeb, now, now.Add(2*time.Minute)); !errors.Is(err, domain.ErrArenaDraftStaleTurn) {
		t.Fatalf("stale turn: %v", err)
	}
	if err := draft.ApplyAction(1, secondID, domain.ArenaDraftActionBan, domain.CategoryWeb, now, now.Add(2*time.Minute)); !errors.Is(err, domain.ErrArenaDraftIllegalAction) {
		t.Fatalf("wrong actor: %v", err)
	}
	if err := draft.ApplyAction(1, firstID, domain.ArenaDraftActionPick, domain.CategoryWeb, now, now.Add(2*time.Minute)); !errors.Is(err, domain.ErrArenaDraftIllegalAction) {
		t.Fatalf("wrong action: %v", err)
	}
	applyDraftAction(t, &draft, 1, firstID, domain.ArenaDraftActionBan, domain.CategoryWeb, now, now.Add(2*time.Minute))
	if err := draft.ApplyAction(2, secondID, domain.ArenaDraftActionBan, domain.CategoryWeb, now.Add(time.Minute), time.Time{}); !errors.Is(err, domain.ErrArenaDraftCategoryUsed) {
		t.Fatalf("duplicate category: %v", err)
	}
}

func TestArenaDraftRulesEnforceServerDeadlines(t *testing.T) {
	draft, firstID, _, now := newArenaDraft(t, domain.ArenaSeriesFormatBO1, []domain.Category{
		domain.CategoryWeb,
		domain.CategoryCrypto,
		domain.CategoryReverse,
	})

	if err := draft.ApplyAction(1, firstID, domain.ArenaDraftActionBan, domain.CategoryWeb, now.Add(time.Nanosecond), now.Add(2*time.Minute)); !errors.Is(err, domain.ErrArenaDraftDeadline) {
		t.Fatalf("late action: %v", err)
	}
	if err := draft.ApplyAction(1, firstID, domain.ArenaDraftActionBan, domain.CategoryWeb, now, now); !errors.Is(err, domain.ErrInvalidArenaDraft) {
		t.Fatalf("non-advancing deadline: %v", err)
	}
	applyDraftAction(t, &draft, 1, firstID, domain.ArenaDraftActionBan, domain.CategoryWeb, now, now.Add(time.Minute))
}

func TestArenaDraftRulesRejectWrongPoolSizesAndDuplicates(t *testing.T) {
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	for name, pool := range map[string][]domain.Category{
		"wrong size": {domain.CategoryWeb, domain.CategoryCrypto},
		"duplicate":  {domain.CategoryWeb, domain.CategoryWeb, domain.CategoryCrypto},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := domain.NewArenaDraft(uuid.New(), uuid.New(), domain.ArenaSeriesFormatBO1, uuid.New(), uuid.New(), pool, now)
			if !errors.Is(err, domain.ErrInvalidArenaDraft) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func newArenaDraft(t *testing.T, format domain.ArenaSeriesFormat, pool []domain.Category) (domain.ArenaDraft, uuid.UUID, uuid.UUID, time.Time) {
	t.Helper()
	firstID := uuid.New()
	secondID := uuid.New()
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	draft, err := domain.NewArenaDraft(uuid.New(), uuid.New(), format, firstID, secondID, pool, now)
	if err != nil {
		t.Fatalf("new draft: %v", err)
	}
	return draft, firstID, secondID, now
}

func applyDraftAction(
	t *testing.T,
	draft *domain.ArenaDraft,
	turn int,
	actorID uuid.UUID,
	action domain.ArenaDraftActionType,
	category domain.Category,
	at time.Time,
	nextDeadline time.Time,
) {
	t.Helper()
	if err := draft.ApplyAction(turn, actorID, action, category, at, nextDeadline); err != nil {
		t.Fatalf("apply turn %d: %v", turn, err)
	}
}

func assertCategories(t *testing.T, got, want []domain.Category) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("category count = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("category %d = %s, want %s", i, got[i], want[i])
		}
	}
}
