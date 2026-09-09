package draft_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecasedraft "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
)

func TestBO1Draft(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, time.August, 30, 10, 0, 0, 0, time.UTC)
	revision := task029CategoryRevision(t, domain.TournamentStageSwiss, true, task029ID(1), createdAt)
	firstID := task029ID(2)
	secondID := task029ID(3)
	start := usecasedraft.StartCommand{
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
				draft, err := usecasedraft.NewBO1(revision, start)
				if err != nil {
					t.Fatalf("NewBO1Draft() error = %v", err)
				}
				initial := draft
				draft, err = usecasedraft.ApplyBO1Action(draft, usecasedraft.ActionCommand{
					ExpectedTurn: 1, ActorID: firstID, Action: domain.DraftActionBan,
					Category: bans[0], OccurredAt: createdAt.Add(10 * time.Second),
					NextDeadline: createdAt.Add(2 * time.Minute),
				})
				if err != nil {
					t.Fatalf("ApplyBO1DraftAction(first ban) error = %v", err)
				}
				if initial.Turn != 1 || len(initial.Actions) != 0 || initial.State != domain.DraftStateActive {
					t.Fatalf("input draft mutated: %+v", initial)
				}
				draft, err = usecasedraft.ApplyBO1Action(draft, usecasedraft.ActionCommand{
					ExpectedTurn: 2, ActorID: secondID, Action: domain.DraftActionBan,
					Category: bans[1], OccurredAt: createdAt.Add(70 * time.Second),
				})
				if err != nil {
					t.Fatalf("ApplyBO1DraftAction(second ban) error = %v", err)
				}

				category, err := usecasedraft.BO1GameCategory(draft)
				if err != nil {
					t.Fatalf("BO1DraftGameCategory() error = %v", err)
				}
				if category != remaining || draft.State != domain.DraftStateCompleted {
					t.Fatalf("completed draft = %+v, category = %s, want %s", draft, category, remaining)
				}
			})
		}
	})

	t.Run("rejects invalid turn actor action and category", func(t *testing.T) {
		t.Parallel()

		draft, err := usecasedraft.NewBO1(revision, start)
		if err != nil {
			t.Fatalf("NewBO1Draft() error = %v", err)
		}
		cases := []struct {
			name    string
			command usecasedraft.ActionCommand
			want    error
		}{
			{
				name: "stale turn",
				command: usecasedraft.ActionCommand{
					ExpectedTurn: 2, ActorID: firstID, Action: domain.DraftActionBan,
					Category: domain.CategoryWeb, OccurredAt: createdAt.Add(10 * time.Second),
					NextDeadline: createdAt.Add(2 * time.Minute),
				},
				want: domain.ErrDraftStaleTurn,
			},
			{
				name: "wrong actor",
				command: usecasedraft.ActionCommand{
					ExpectedTurn: 1, ActorID: secondID, Action: domain.DraftActionBan,
					Category: domain.CategoryWeb, OccurredAt: createdAt.Add(10 * time.Second),
					NextDeadline: createdAt.Add(2 * time.Minute),
				},
				want: domain.ErrDraftIllegalAction,
			},
			{
				name: "wrong action",
				command: usecasedraft.ActionCommand{
					ExpectedTurn: 1, ActorID: firstID, Action: domain.DraftActionPick,
					Category: domain.CategoryWeb, OccurredAt: createdAt.Add(10 * time.Second),
					NextDeadline: createdAt.Add(2 * time.Minute),
				},
				want: domain.ErrDraftIllegalAction,
			},
			{
				name: "outside category",
				command: usecasedraft.ActionCommand{
					ExpectedTurn: 1, ActorID: firstID, Action: domain.DraftActionBan,
					Category: domain.CategoryPwn, OccurredAt: createdAt.Add(10 * time.Second),
					NextDeadline: createdAt.Add(2 * time.Minute),
				},
				want: domain.ErrDraftIllegalAction,
			},
		}
		for _, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				t.Parallel()

				got, applyErr := usecasedraft.ApplyBO1Action(draft, testCase.command)
				if !errors.Is(applyErr, testCase.want) {
					t.Fatalf("ApplyBO1DraftAction() error = %v, want %v", applyErr, testCase.want)
				}
				if !reflect.DeepEqual(got, domain.Draft{}) || len(draft.Actions) != 0 {
					t.Fatalf("failed action changed draft: got = %+v, input = %+v", got, draft)
				}
			})
		}

		first, err := usecasedraft.ApplyBO1Action(draft, usecasedraft.ActionCommand{
			ExpectedTurn: 1, ActorID: firstID, Action: domain.DraftActionBan,
			Category: domain.CategoryWeb, OccurredAt: createdAt.Add(10 * time.Second),
			NextDeadline: createdAt.Add(2 * time.Minute),
		})
		if err != nil {
			t.Fatalf("ApplyBO1DraftAction(first ban) error = %v", err)
		}
		_, err = usecasedraft.ApplyBO1Action(first, usecasedraft.ActionCommand{
			ExpectedTurn: 2, ActorID: secondID, Action: domain.DraftActionBan,
			Category: domain.CategoryWeb, OccurredAt: createdAt.Add(70 * time.Second),
		})
		if !errors.Is(err, domain.ErrDraftCategoryUsed) {
			t.Fatalf("duplicate category error = %v", err)
		}
		if _, err := usecasedraft.BO1GameCategory(first); !errors.Is(err, usecasedraft.ErrInvalidBO1) {
			t.Fatalf("incomplete result error = %v", err)
		}
	})

	t.Run("requires draft mode and the exact BO1 pool", func(t *testing.T) {
		t.Parallel()

		randomRevision := task029CategoryRevision(t, domain.TournamentStageSwiss, false, task029ID(5), createdAt)
		if _, err := usecasedraft.NewBO1(randomRevision, start); !errors.Is(err, usecasedraft.ErrInvalidBO1) {
			t.Fatalf("random revision error = %v", err)
		}

		wrongPool := revision
		wrongPool.CategoryPool.Categories = []domain.Category{
			domain.CategoryCrypto,
			domain.CategoryPwn,
			domain.CategoryWeb,
		}
		if _, err := usecasedraft.NewBO1(wrongPool, start); !errors.Is(err, usecasedraft.ErrInvalidBO1) {
			t.Fatalf("wrong pool error = %v", err)
		}

		callerRevision := revision
		callerRevision.CategoryPool.Categories = append(
			[]domain.Category(nil),
			revision.CategoryPool.Categories...,
		)
		draft, err := usecasedraft.NewBO1(callerRevision, start)
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
	stage domain.TournamentStage,
	draftMode bool,
	id uuid.UUID,
	createdAt time.Time,
) usecasedraft.CategoryRevision {
	t.Helper()

	command := task028CategoryRevisionCommand(task028Configuration(t), stage, id, createdAt)
	command.SeriesID = task029ID(100)
	command.RosterID = task029ID(101)
	if draftMode && stage != domain.TournamentStageFinal {
		mode := domain.CategoryModeDraft
		command.ModeOverride = &mode
	}
	revision, changed, err := usecasedraft.DeriveCategoryRevision(nil, command)
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
