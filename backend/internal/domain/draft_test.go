package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

func TestDraftRulesAcceptCategoryModes(t *testing.T) {
	for _, mode := range []domain.CategoryMode{
		domain.CategoryModeRandom,
		domain.CategoryModeAdmin,
		domain.CategoryModeDraft,
	} {
		if !mode.IsValid() {
			t.Fatalf("valid mode rejected: %q", mode)
		}
	}
	if domain.CategoryMode("client").IsValid() {
		t.Fatal("unknown mode accepted")
	}
}

func TestDraftRulesBO1UsesTwoBansAndRemainingCategory(t *testing.T) {
	draft, firstID, secondID, now := newDraft(t, domain.SeriesFormatBO1, []domain.Category{
		domain.CategoryWeb,
		domain.CategoryCrypto,
		domain.CategoryReverse,
	})

	applyDraftAction(t, &draft, 1, firstID, domain.DraftActionBan, domain.CategoryWeb, now, now.Add(2*time.Minute))
	applyDraftAction(t, &draft, 2, secondID, domain.DraftActionBan, domain.CategoryCrypto, now.Add(time.Minute), time.Time{})

	if draft.State != domain.DraftStateCompleted {
		t.Fatalf("draft state = %s", draft.State)
	}
	assertCategories(t, draft.SelectedCategories, []domain.Category{domain.CategoryReverse})
	if err := draft.Validate(); err != nil {
		t.Fatalf("validate draft: %v", err)
	}
}

func TestDraftRulesBO3UsesTwoBansTwoPicksAndRemainingCategory(t *testing.T) {
	draft, firstID, secondID, now := newDraft(t, domain.SeriesFormatBO3, []domain.Category{
		domain.CategoryWeb,
		domain.CategoryCrypto,
		domain.CategoryReverse,
		domain.CategoryPwn,
		domain.CategoryForensics,
	})

	applyDraftAction(t, &draft, 1, firstID, domain.DraftActionBan, domain.CategoryWeb, now, now.Add(2*time.Minute))
	applyDraftAction(t, &draft, 2, secondID, domain.DraftActionBan, domain.CategoryCrypto, now.Add(time.Minute), now.Add(3*time.Minute))
	applyDraftAction(t, &draft, 3, firstID, domain.DraftActionPick, domain.CategoryReverse, now.Add(2*time.Minute), now.Add(4*time.Minute))
	applyDraftAction(t, &draft, 4, secondID, domain.DraftActionPick, domain.CategoryPwn, now.Add(3*time.Minute), time.Time{})

	assertCategories(t, draft.SelectedCategories, []domain.Category{
		domain.CategoryReverse,
		domain.CategoryPwn,
		domain.CategoryForensics,
	})
	if err := draft.Validate(); err != nil {
		t.Fatalf("validate draft: %v", err)
	}
}

func TestDraftRulesRejectStaleDuplicateAndIllegalActions(t *testing.T) {
	draft, firstID, secondID, now := newDraft(t, domain.SeriesFormatBO1, []domain.Category{
		domain.CategoryWeb,
		domain.CategoryCrypto,
		domain.CategoryReverse,
	})

	if err := draft.ApplyAction(2, firstID, domain.DraftActionBan, domain.CategoryWeb, now, now.Add(2*time.Minute)); !errors.Is(err, domain.ErrDraftStaleTurn) {
		t.Fatalf("stale turn: %v", err)
	}
	if err := draft.ApplyAction(1, secondID, domain.DraftActionBan, domain.CategoryWeb, now, now.Add(2*time.Minute)); !errors.Is(err, domain.ErrDraftIllegalAction) {
		t.Fatalf("wrong actor: %v", err)
	}
	if err := draft.ApplyAction(1, firstID, domain.DraftActionPick, domain.CategoryWeb, now, now.Add(2*time.Minute)); !errors.Is(err, domain.ErrDraftIllegalAction) {
		t.Fatalf("wrong action: %v", err)
	}
	applyDraftAction(t, &draft, 1, firstID, domain.DraftActionBan, domain.CategoryWeb, now, now.Add(2*time.Minute))
	if err := draft.ApplyAction(2, secondID, domain.DraftActionBan, domain.CategoryWeb, now.Add(time.Minute), time.Time{}); !errors.Is(err, domain.ErrDraftCategoryUsed) {
		t.Fatalf("duplicate category: %v", err)
	}
}

func TestDraftRulesEnforceServerDeadlines(t *testing.T) {
	draft, firstID, _, now := newDraft(t, domain.SeriesFormatBO1, []domain.Category{
		domain.CategoryWeb,
		domain.CategoryCrypto,
		domain.CategoryReverse,
	})

	if err := draft.ApplyAction(1, firstID, domain.DraftActionBan, domain.CategoryWeb, now.Add(time.Nanosecond), now.Add(2*time.Minute)); !errors.Is(err, domain.ErrDraftDeadline) {
		t.Fatalf("late action: %v", err)
	}
	if err := draft.ApplyAction(1, firstID, domain.DraftActionBan, domain.CategoryWeb, now, now); !errors.Is(err, domain.ErrInvalidDraft) {
		t.Fatalf("non-advancing deadline: %v", err)
	}
	applyDraftAction(t, &draft, 1, firstID, domain.DraftActionBan, domain.CategoryWeb, now, now.Add(time.Minute))
}

func TestDraftRulesRejectWrongPoolSizesAndDuplicates(t *testing.T) {
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	for name, pool := range map[string][]domain.Category{
		"wrong size": {domain.CategoryWeb, domain.CategoryCrypto},
		"duplicate":  {domain.CategoryWeb, domain.CategoryWeb, domain.CategoryCrypto},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := domain.NewDraft(uuid.New(), uuid.New(), domain.SeriesFormatBO1, uuid.New(), uuid.New(), pool, now)
			if !errors.Is(err, domain.ErrInvalidDraft) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func newDraft(t *testing.T, format domain.SeriesFormat, pool []domain.Category) (domain.Draft, uuid.UUID, uuid.UUID, time.Time) {
	t.Helper()
	firstID := uuid.New()
	secondID := uuid.New()
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	draft, err := domain.NewDraft(uuid.New(), uuid.New(), format, firstID, secondID, pool, now)
	if err != nil {
		t.Fatalf("new draft: %v", err)
	}
	return draft, firstID, secondID, now
}

func applyDraftAction(
	t *testing.T,
	draft *domain.Draft,
	turn int,
	actorID uuid.UUID,
	action domain.DraftActionType,
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
