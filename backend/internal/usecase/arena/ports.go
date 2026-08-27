package arena

import (
	"context"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type Clock interface {
	Now() time.Time
}

type TransactionManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

type GameRepository interface {
	Get(ctx context.Context, scope GameScope) (*domain.ArenaGame, error)
	Update(
		ctx context.Context,
		scope GameScope,
		expected domain.ArenaGameState,
		next domain.ArenaGame,
	) (*domain.ArenaGame, bool, error)
}

// TaskSnapshotter derives a snapshot without writing task or game state.
type TaskSnapshotter interface {
	Snapshot(ctx context.Context, scope GameScope, input TaskSnapshotInput) (domain.ArenaTaskSnapshot, error)
}

// FlagValidator checks a submitted flag without settling the game.
type FlagValidator interface {
	Validate(ctx context.Context, scope GameScope, input FlagValidationInput) (bool, error)
}

// SubmissionOrderer orders values without recording or settling them.
type SubmissionOrderer interface {
	Order(ctx context.Context, scope GameScope, submissions []Submission) ([]Submission, error)
}
