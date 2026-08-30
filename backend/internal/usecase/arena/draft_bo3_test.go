package arena_test

import (
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestBO3FinalDraft(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.August, 30, 11, 0, 0, 0, time.UTC)
	revision := task029CategoryRevision(t, arena.ArenaStageFinal, true, task029ID(201), createdAt)
	firstID := task029ID(202)
	secondID := task029ID(203)
	start := arena.DraftStartCommand{
		DraftID: task029ID(204), FirstParticipantID: firstID, SecondParticipantID: secondID,
		FirstDeadline: createdAt.Add(time.Minute),
	}

	t.Run("assigns Games for every legal action sequence", func(t *testing.T) {
		t.Parallel()

		pool := []domain.Category{
			domain.CategoryWeb,
			domain.CategoryCrypto,
			domain.CategoryReverse,
			domain.CategoryForensics,
			domain.CategoryPwn,
		}
		sequences := 0
		for _, firstBan := range pool {
			for _, secondBan := range categoriesExcept(pool, firstBan) {
				for _, firstPick := range categoriesExcept(pool, firstBan, secondBan) {
					for _, secondPick := range categoriesExcept(pool, firstBan, secondBan, firstPick) {
						remaining := categoriesExcept(pool, firstBan, secondBan, firstPick, secondPick)[0]
						draft, err := arena.NewBO3FinalDraft(revision, start)
						if err != nil {
							t.Fatalf("NewBO3FinalDraft() error = %v", err)
						}
						actions := []arena.DraftActionCommand{
							{
								ExpectedTurn: 1, ActorID: firstID, Action: domain.ArenaDraftActionBan,
								Category: firstBan, OccurredAt: createdAt.Add(10 * time.Second),
								NextDeadline: createdAt.Add(2 * time.Minute),
							},
							{
								ExpectedTurn: 2, ActorID: secondID, Action: domain.ArenaDraftActionBan,
								Category: secondBan, OccurredAt: createdAt.Add(70 * time.Second),
								NextDeadline: createdAt.Add(3 * time.Minute),
							},
							{
								ExpectedTurn: 3, ActorID: firstID, Action: domain.ArenaDraftActionPick,
								Category: firstPick, OccurredAt: createdAt.Add(130 * time.Second),
								NextDeadline: createdAt.Add(4 * time.Minute),
							},
							{
								ExpectedTurn: 4, ActorID: secondID, Action: domain.ArenaDraftActionPick,
								Category: secondPick, OccurredAt: createdAt.Add(190 * time.Second),
							},
						}
						for _, action := range actions {
							draft, err = arena.ApplyBO3FinalDraftAction(draft, action)
							if err != nil {
								t.Fatalf("ApplyBO3FinalDraftAction(turn %d) error = %v", action.ExpectedTurn, err)
							}
						}

						games, err := arena.BO3FinalDraftGameCategories(draft)
						if err != nil {
							t.Fatalf("BO3FinalDraftGameCategories() error = %v", err)
						}
						want := [3]domain.Category{firstPick, secondPick, remaining}
						if games != want || draft.State != domain.ArenaDraftStateCompleted {
							t.Fatalf("Game categories = %v, want %v; draft = %+v", games, want, draft)
						}
						sequences++
					}
				}
			}
		}
		if sequences != 120 {
			t.Fatalf("legal sequence count = %d, want 120", sequences)
		}
	})

	t.Run("enforces the exact actor and action order", func(t *testing.T) {
		t.Parallel()

		draft, err := arena.NewBO3FinalDraft(revision, start)
		if err != nil {
			t.Fatalf("NewBO3FinalDraft() error = %v", err)
		}
		wrongActor := arena.DraftActionCommand{
			ExpectedTurn: 1, ActorID: secondID, Action: domain.ArenaDraftActionBan,
			Category: domain.CategoryWeb, OccurredAt: createdAt.Add(10 * time.Second),
			NextDeadline: createdAt.Add(2 * time.Minute),
		}
		if _, err := arena.ApplyBO3FinalDraftAction(draft, wrongActor); !errors.Is(err, domain.ErrArenaDraftIllegalAction) {
			t.Fatalf("wrong actor error = %v", err)
		}

		wrongAction := wrongActor
		wrongAction.ActorID = firstID
		wrongAction.Action = domain.ArenaDraftActionPick
		if _, err := arena.ApplyBO3FinalDraftAction(draft, wrongAction); !errors.Is(err, domain.ErrArenaDraftIllegalAction) {
			t.Fatalf("wrong action error = %v", err)
		}

		if _, err := arena.BO3FinalDraftGameCategories(draft); !errors.Is(err, arena.ErrInvalidBO3FinalDraft) {
			t.Fatalf("incomplete result error = %v", err)
		}
	})

	t.Run("requires the final draft revision and exact five-category pool", func(t *testing.T) {
		t.Parallel()

		wrongMode := revision
		wrongMode.Mode = domain.ArenaCategoryModeRandom
		if _, err := arena.NewBO3FinalDraft(wrongMode, start); !errors.Is(err, arena.ErrInvalidBO3FinalDraft) {
			t.Fatalf("wrong mode error = %v", err)
		}

		wrongStage := revision
		wrongStage.Stage = arena.ArenaStageSemifinal
		if _, err := arena.NewBO3FinalDraft(wrongStage, start); !errors.Is(err, arena.ErrInvalidBO3FinalDraft) {
			t.Fatalf("wrong stage error = %v", err)
		}

		wrongPool := revision
		wrongPool.CategoryPool.Categories = []domain.Category{
			domain.CategoryCrypto,
			domain.CategoryForensics,
			domain.CategoryMisc,
			domain.CategoryPwn,
			domain.CategoryWeb,
		}
		if _, err := arena.NewBO3FinalDraft(wrongPool, start); !errors.Is(err, arena.ErrInvalidBO3FinalDraft) {
			t.Fatalf("wrong pool error = %v", err)
		}
	})
}
