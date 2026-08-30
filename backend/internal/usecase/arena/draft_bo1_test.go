package arena_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestBO1Draft(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC)
	revision := task029CategoryRevision(t, arena.ArenaStageSwiss, true, task029ID(1), createdAt)
	firstID := task029ID(2)
	secondID := task029ID(3)
	start := arena.DraftStartCommand{
		DraftID: task029ID(4), FirstParticipantID: firstID, SecondParticipantID: secondID,
		FirstDeadline: createdAt.Add(time.Minute),
	}

	t.Run("completes every legal two-ban remainder", func(t *testing.T) {
		t.Parallel()

		pool := []domain.Category{domain.CategoryWeb, domain.CategoryCrypto, domain.CategoryReverse}
		for _, remaining := range pool {
			t.Run(remaining.String(), func(t *testing.T) {
				t.Parallel()

				bans := categoriesExcept(pool, remaining)
				draft, err := arena.NewBO1Draft(revision, start)
				if err != nil {
					t.Fatalf("NewBO1Draft() error = %v", err)
				}
				initial := draft
				draft, err = arena.ApplyBO1DraftAction(draft, arena.DraftActionCommand{
					ExpectedTurn: 1, ActorID: firstID, Action: domain.ArenaDraftActionBan,
					Category: bans[0], OccurredAt: createdAt.Add(10 * time.Second),
					NextDeadline: createdAt.Add(2 * time.Minute),
				})
				if err != nil {
					t.Fatalf("ApplyBO1DraftAction(first ban) error = %v", err)
				}
				if initial.Turn != 1 || len(initial.Actions) != 0 || initial.State != domain.ArenaDraftStateActive {
					t.Fatalf("input draft mutated: %+v", initial)
				}
				draft, err = arena.ApplyBO1DraftAction(draft, arena.DraftActionCommand{
					ExpectedTurn: 2, ActorID: secondID, Action: domain.ArenaDraftActionBan,
					Category: bans[1], OccurredAt: createdAt.Add(70 * time.Second),
				})
				if err != nil {
					t.Fatalf("ApplyBO1DraftAction(second ban) error = %v", err)
				}

				category, err := arena.BO1DraftGameCategory(draft)
				if err != nil {
					t.Fatalf("BO1DraftGameCategory() error = %v", err)
				}
				if category != remaining || draft.State != domain.ArenaDraftStateCompleted {
					t.Fatalf("completed draft = %+v, category = %s, want %s", draft, category, remaining)
				}
			})
		}
	})

	t.Run("rejects invalid turn actor action and category", func(t *testing.T) {
		t.Parallel()

		draft, err := arena.NewBO1Draft(revision, start)
		if err != nil {
			t.Fatalf("NewBO1Draft() error = %v", err)
		}
		cases := []struct {
			name    string
			command arena.DraftActionCommand
			want    error
		}{
			{
				name: "stale turn",
				command: arena.DraftActionCommand{
					ExpectedTurn: 2, ActorID: firstID, Action: domain.ArenaDraftActionBan,
					Category: domain.CategoryWeb, OccurredAt: createdAt.Add(10 * time.Second),
					NextDeadline: createdAt.Add(2 * time.Minute),
				},
				want: domain.ErrArenaDraftStaleTurn,
			},
			{
				name: "wrong actor",
				command: arena.DraftActionCommand{
					ExpectedTurn: 1, ActorID: secondID, Action: domain.ArenaDraftActionBan,
					Category: domain.CategoryWeb, OccurredAt: createdAt.Add(10 * time.Second),
					NextDeadline: createdAt.Add(2 * time.Minute),
				},
				want: domain.ErrArenaDraftIllegalAction,
			},
			{
				name: "wrong action",
				command: arena.DraftActionCommand{
					ExpectedTurn: 1, ActorID: firstID, Action: domain.ArenaDraftActionPick,
					Category: domain.CategoryWeb, OccurredAt: createdAt.Add(10 * time.Second),
					NextDeadline: createdAt.Add(2 * time.Minute),
				},
				want: domain.ErrArenaDraftIllegalAction,
			},
			{
				name: "outside category",
				command: arena.DraftActionCommand{
					ExpectedTurn: 1, ActorID: firstID, Action: domain.ArenaDraftActionBan,
					Category: domain.CategoryPwn, OccurredAt: createdAt.Add(10 * time.Second),
					NextDeadline: createdAt.Add(2 * time.Minute),
				},
				want: domain.ErrArenaDraftIllegalAction,
			},
		}
		for _, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				t.Parallel()

				got, applyErr := arena.ApplyBO1DraftAction(draft, testCase.command)
				if !errors.Is(applyErr, testCase.want) {
					t.Fatalf("ApplyBO1DraftAction() error = %v, want %v", applyErr, testCase.want)
				}
				if !reflect.DeepEqual(got, domain.ArenaDraft{}) || len(draft.Actions) != 0 {
					t.Fatalf("failed action changed draft: got = %+v, input = %+v", got, draft)
				}
			})
		}

		first, err := arena.ApplyBO1DraftAction(draft, arena.DraftActionCommand{
			ExpectedTurn: 1, ActorID: firstID, Action: domain.ArenaDraftActionBan,
			Category: domain.CategoryWeb, OccurredAt: createdAt.Add(10 * time.Second),
			NextDeadline: createdAt.Add(2 * time.Minute),
		})
		if err != nil {
			t.Fatalf("ApplyBO1DraftAction(first ban) error = %v", err)
		}
		_, err = arena.ApplyBO1DraftAction(first, arena.DraftActionCommand{
			ExpectedTurn: 2, ActorID: secondID, Action: domain.ArenaDraftActionBan,
			Category: domain.CategoryWeb, OccurredAt: createdAt.Add(70 * time.Second),
		})
		if !errors.Is(err, domain.ErrArenaDraftCategoryUsed) {
			t.Fatalf("duplicate category error = %v", err)
		}
		if _, err := arena.BO1DraftGameCategory(first); !errors.Is(err, arena.ErrInvalidBO1Draft) {
			t.Fatalf("incomplete result error = %v", err)
		}
	})

	t.Run("requires draft mode and the exact BO1 pool", func(t *testing.T) {
		t.Parallel()

		randomRevision := task029CategoryRevision(t, arena.ArenaStageSwiss, false, task029ID(5), createdAt)
		if _, err := arena.NewBO1Draft(randomRevision, start); !errors.Is(err, arena.ErrInvalidBO1Draft) {
			t.Fatalf("random revision error = %v", err)
		}

		wrongPool := revision
		wrongPool.CategoryPool.Categories = []domain.Category{
			domain.CategoryCrypto,
			domain.CategoryPwn,
			domain.CategoryWeb,
		}
		if _, err := arena.NewBO1Draft(wrongPool, start); !errors.Is(err, arena.ErrInvalidBO1Draft) {
			t.Fatalf("wrong pool error = %v", err)
		}

		callerRevision := revision
		callerRevision.CategoryPool.Categories = append(
			[]domain.Category(nil),
			revision.CategoryPool.Categories...,
		)
		draft, err := arena.NewBO1Draft(callerRevision, start)
		if err != nil {
			t.Fatalf("NewBO1Draft() error = %v", err)
		}
		callerRevision.CategoryPool.Categories[0] = domain.CategoryMisc
		if draft.Pool[0] == domain.CategoryMisc {
			t.Fatal("draft retained caller category slice")
		}
	})
}

func task029CategoryRevision(
	t *testing.T,
	stage arena.ArenaStage,
	draftMode bool,
	id uuid.UUID,
	createdAt time.Time,
) arena.SeriesCategoryRevision {
	t.Helper()

	command := task028CategoryRevisionCommand(task028Configuration(t), stage, id, createdAt)
	command.SeriesID = task029ID(100)
	command.RosterID = task029ID(101)
	if draftMode && stage != arena.ArenaStageFinal {
		mode := domain.ArenaCategoryModeDraft
		command.ModeOverride = &mode
	}
	revision, changed, err := arena.DeriveSeriesCategoryRevision(nil, command)
	if err != nil || !changed {
		t.Fatalf("DeriveSeriesCategoryRevision() error = %v, changed = %v", err, changed)
	}
	return revision
}

func categoriesExcept(categories []domain.Category, excluded ...domain.Category) []domain.Category {
	blocked := make(map[domain.Category]struct{}, len(excluded))
	for _, category := range excluded {
		blocked[category] = struct{}{}
	}
	result := make([]domain.Category, 0, len(categories)-len(excluded))
	for _, category := range categories {
		if _, skip := blocked[category]; !skip {
			result = append(result, category)
		}
	}
	return result
}

func task029ID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("29000000-0000-0000-0000-%012d", number))
}
