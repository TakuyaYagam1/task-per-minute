package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

var ErrArenaGameNotFound = errors.New("arena game repository: game not found")

type ArenaGamePostgres struct {
	tx *TxManager
}

type ArenaGameSlotInput struct {
	Slot      domain.ArenaGameSlot
	RosterID  uuid.UUID
	CreatedAt time.Time
}

type ArenaGameAttemptInput struct {
	Game       domain.ArenaGame
	SeriesID   uuid.UUID
	RosterID   uuid.UUID
	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

type ArenaGameAttemptRecord struct {
	Game       domain.ArenaGame
	SeriesID   uuid.UUID
	RosterID   uuid.UUID
	Revision   int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

type ArenaGameSlotRecord struct {
	Slot      domain.ArenaGameSlot
	RosterID  uuid.UUID
	Revision  int64
	CreatedAt time.Time
	UpdatedAt time.Time
	Attempts  []ArenaGameAttemptRecord
}

var _ arena.GameRepository = (*ArenaGamePostgres)(nil)

func NewArenaGamePostgres(tx *TxManager) *ArenaGamePostgres {
	return &ArenaGamePostgres{tx: tx}
}

func (r *ArenaGamePostgres) CreateSlot(
	ctx context.Context,
	in ArenaGameSlotInput,
) (*ArenaGameSlotRecord, error) {
	if r == nil || r.tx == nil || in.RosterID == uuid.Nil || !validServerTime(in.CreatedAt) ||
		len(in.Slot.Attempts) != 0 || in.Slot.Position > math.MaxInt16 {
		return nil, domain.ErrValidation
	}
	if err := in.Slot.Validate(); err != nil {
		return nil, domain.WrapError(err, domain.ErrValidation)
	}
	row, err := r.tx.Querier(ctx).CreateArenaGameSlot(ctx, sqlc.CreateArenaGameSlotParams{
		ID:                          in.Slot.ID,
		SeriesID:                    in.Slot.SeriesID,
		RosterID:                    in.RosterID,
		SlotNumber:                  int16(in.Slot.Position), //nolint:gosec // validated against the PostgreSQL int2 range below.
		Category:                    string(in.Slot.Category),
		FirstParticipantWinsBefore:  int16(in.Slot.ScoreBefore.FirstParticipantWins),  //nolint:gosec // Arena score validation bounds this value to 0..2.
		SecondParticipantWinsBefore: int16(in.Slot.ScoreBefore.SecondParticipantWins), //nolint:gosec // Arena score validation bounds this value to 0..2.
		CreatedAt:                   tstz(in.CreatedAt),
	})
	if err != nil {
		return nil, mapArenaRepositoryWriteError("ArenaGamePostgres - CreateSlot", err)
	}
	return arenaGameSlotRecord(row, nil), nil
}

func (r *ArenaGamePostgres) AppendAttempt(
	ctx context.Context,
	in ArenaGameAttemptInput,
) (*ArenaGameAttemptRecord, error) {
	if err := validateArenaGameAttemptInput(in); err != nil {
		return nil, err
	}
	row, err := r.tx.Querier(ctx).CreateArenaGameAttempt(ctx, sqlc.CreateArenaGameAttemptParams{
		ID:               in.Game.ID,
		SlotID:           in.Game.SlotID,
		SeriesID:         in.SeriesID,
		RosterID:         in.RosterID,
		AttemptNumber:    int32(in.Game.AttemptNo), //nolint:gosec // validateArenaGameAttemptInput bounds the PostgreSQL int4 value.
		State:            string(in.Game.State),
		ResultReason:     arenaGameResultReason(in.Game.ResultReason),
		WinnerID:         nullableUUID(in.Game.WinnerID),
		ResultRevisionID: nullableArenaResultRevision(in.Game.ResultRevisionID),
		CreatedAt:        tstz(in.CreatedAt),
		StartedAt:        nullableTSTZ(in.StartedAt),
		FinishedAt:       nullableTSTZ(in.FinishedAt),
	})
	if err != nil {
		return nil, mapArenaRepositoryWriteError("ArenaGamePostgres - AppendAttempt", err)
	}
	record, err := arenaGameAttemptRecord(row)
	if err != nil {
		return nil, fmt.Errorf("ArenaGamePostgres - AppendAttempt - map row: %w", err)
	}
	return record, nil
}

func (r *ArenaGamePostgres) Get(
	ctx context.Context,
	scope arena.GameScope,
) (*domain.ArenaGame, error) {
	record, err := r.GetAttemptRecord(ctx, scope)
	if err != nil {
		return nil, err
	}
	game := record.Game
	return &game, nil
}

func (r *ArenaGamePostgres) GetAttemptRecord(
	ctx context.Context,
	scope arena.GameScope,
) (*ArenaGameAttemptRecord, error) {
	if !validArenaGameScope(scope) {
		return nil, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).GetArenaGameAttemptScoped(ctx, sqlc.GetArenaGameAttemptScopedParams{
		ID:           scope.GameID,
		SlotID:       scope.SlotID,
		SeriesID:     scope.SeriesID,
		TournamentID: scope.TournamentID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrArenaGameNotFound
		}
		return nil, fmt.Errorf("ArenaGamePostgres - GetAttemptRecord: %w", err)
	}
	record, err := arenaGameAttemptRecord(row)
	if err != nil {
		return nil, fmt.Errorf("ArenaGamePostgres - GetAttemptRecord - map row: %w", err)
	}
	return record, nil
}

func (r *ArenaGamePostgres) Update(
	ctx context.Context,
	scope arena.GameScope,
	expected domain.ArenaGameState,
	next domain.ArenaGame,
) (*domain.ArenaGame, bool, error) {
	current, err := r.GetAttemptRecord(ctx, scope)
	if err != nil {
		return nil, false, err
	}
	if current.Game.State != expected {
		return nil, false, nil
	}
	updated, changed, err := r.Transition(
		ctx,
		scope,
		current.Revision,
		expected,
		next,
		time.Now().UTC(),
	)
	if err != nil || !changed {
		return nil, changed, err
	}
	game := updated.Game
	return &game, true, nil
}

func (r *ArenaGamePostgres) Transition(
	ctx context.Context,
	scope arena.GameScope,
	expectedRevision int64,
	expectedState domain.ArenaGameState,
	next domain.ArenaGame,
	updatedAt time.Time,
) (*ArenaGameAttemptRecord, bool, error) {
	if !validArenaGameScope(scope) || expectedRevision < 1 || !expectedState.IsValid() ||
		!validServerTime(updatedAt) || next.ID != scope.GameID || next.SlotID != scope.SlotID {
		return nil, false, domain.ErrValidation
	}
	if err := next.Validate(); err != nil {
		return nil, false, domain.WrapError(err, domain.ErrValidation)
	}
	row, err := r.tx.Querier(ctx).UpdateArenaGameAttemptCAS(ctx, sqlc.UpdateArenaGameAttemptCASParams{
		NextState:        string(next.State),
		ResultReason:     arenaGameResultReason(next.ResultReason),
		WinnerID:         nullableUUID(next.WinnerID),
		ResultRevisionID: nullableArenaResultRevision(next.ResultRevisionID),
		UpdatedAt:        tstz(updatedAt),
		ID:               scope.GameID,
		SlotID:           scope.SlotID,
		SeriesID:         scope.SeriesID,
		TournamentID:     scope.TournamentID,
		ExpectedRevision: expectedRevision,
		ExpectedState:    string(expectedState),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, mapArenaRepositoryWriteError("ArenaGamePostgres - Transition", err)
	}
	record, err := arenaGameAttemptRecord(row)
	if err != nil {
		return nil, false, fmt.Errorf("ArenaGamePostgres - Transition - map row: %w", err)
	}
	return record, true, nil
}

func (r *ArenaGamePostgres) GetSlot(
	ctx context.Context,
	tournamentID uuid.UUID,
	seriesID uuid.UUID,
	slotID uuid.UUID,
) (*ArenaGameSlotRecord, error) {
	if tournamentID == uuid.Nil || seriesID == uuid.Nil || slotID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	slot, err := querier.GetArenaGameSlot(ctx, sqlc.GetArenaGameSlotParams{
		ID:           slotID,
		SeriesID:     seriesID,
		TournamentID: tournamentID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrArenaGameNotFound
		}
		return nil, fmt.Errorf("ArenaGamePostgres - GetSlot: %w", err)
	}
	attemptRows, err := querier.ListArenaGameAttempts(ctx, slotID)
	if err != nil {
		return nil, fmt.Errorf("ArenaGamePostgres - GetSlot - list attempts: %w", err)
	}
	attempts := make([]ArenaGameAttemptRecord, 0, len(attemptRows))
	domainAttempts := make([]domain.ArenaGame, 0, len(attemptRows))
	for _, row := range attemptRows {
		record, mapErr := arenaGameAttemptRecord(row)
		if mapErr != nil {
			return nil, fmt.Errorf("ArenaGamePostgres - GetSlot - map attempt: %w", mapErr)
		}
		attempts = append(attempts, *record)
		domainAttempts = append(domainAttempts, record.Game)
	}
	record := arenaGameSlotRecord(slot, attempts)
	record.Slot.Attempts = domainAttempts
	if err := record.Slot.Validate(); err != nil {
		return nil, fmt.Errorf("ArenaGamePostgres - GetSlot - invalid snapshot: %w", err)
	}
	return record, nil
}

func validateArenaGameAttemptInput(in ArenaGameAttemptInput) error {
	if in.SeriesID == uuid.Nil || in.RosterID == uuid.Nil || !validServerTime(in.CreatedAt) {
		return domain.ErrValidation
	}
	if err := in.Game.Validate(); err != nil {
		return domain.WrapError(err, domain.ErrValidation)
	}
	if in.Game.AttemptNo > math.MaxInt32 {
		return domain.ErrValidation
	}
	if in.StartedAt != nil && !validServerTime(*in.StartedAt) {
		return domain.ErrValidation
	}
	if in.FinishedAt != nil && !validServerTime(*in.FinishedAt) {
		return domain.ErrValidation
	}
	if in.Game.State.IsTerminal() != (in.FinishedAt != nil) {
		return domain.ErrValidation
	}
	return nil
}

func validArenaGameScope(scope arena.GameScope) bool {
	return scope.TournamentID != uuid.Nil && scope.SeriesID != uuid.Nil && scope.SlotID != uuid.Nil &&
		scope.GameID != uuid.Nil
}

func arenaGameAttemptRecord(row sqlc.ArenaGameAttempt) (*ArenaGameAttemptRecord, error) {
	game := domain.ArenaGame{
		ID:           row.ID,
		SlotID:       row.SlotID,
		AttemptNo:    int(row.AttemptNumber),
		State:        domain.ArenaGameState(row.State),
		ResultReason: domain.ArenaGameResultReason(stringValue(row.ResultReason)),
	}
	if row.WinnerID.Valid {
		winnerID := row.WinnerID.UUID
		game.WinnerID = &winnerID
	}
	if row.ResultRevisionID.Valid {
		revisionID := domain.ArenaOfficialResultRevisionID(row.ResultRevisionID.UUID)
		game.ResultRevisionID = &revisionID
	}
	if err := game.Validate(); err != nil {
		return nil, err
	}
	return &ArenaGameAttemptRecord{
		Game:       game,
		SeriesID:   row.SeriesID,
		RosterID:   row.RosterID,
		Revision:   row.Revision,
		CreatedAt:  row.CreatedAt.Time,
		UpdatedAt:  row.UpdatedAt.Time,
		StartedAt:  nullableTime(row.StartedAt),
		FinishedAt: nullableTime(row.FinishedAt),
	}, nil
}

func arenaGameSlotRecord(row sqlc.ArenaGameSlot, attempts []ArenaGameAttemptRecord) *ArenaGameSlotRecord {
	return &ArenaGameSlotRecord{
		Slot: domain.ArenaGameSlot{
			ID:       row.ID,
			SeriesID: row.SeriesID,
			Position: int(row.SlotNumber),
			Category: domain.Category(row.Category),
			ScoreBefore: domain.ArenaSeriesScore{
				FirstParticipantWins:  int(row.FirstParticipantWinsBefore),
				SecondParticipantWins: int(row.SecondParticipantWinsBefore),
			},
		},
		RosterID:  row.RosterID,
		Revision:  row.Revision,
		CreatedAt: row.CreatedAt.Time,
		UpdatedAt: row.UpdatedAt.Time,
		Attempts:  attempts,
	}
}

func arenaGameResultReason(reason domain.ArenaGameResultReason) *string {
	if reason == "" {
		return nil
	}
	value := string(reason)
	return &value
}

func nullableArenaResultRevision(id *domain.ArenaOfficialResultRevisionID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: id.UUID(), Valid: true}
}

func mapArenaRepositoryWriteError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgUniqueViolation, pgForeignKeyViolation, "23514", "40001":
			return domain.WrapError(err, domain.ErrConflict)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
