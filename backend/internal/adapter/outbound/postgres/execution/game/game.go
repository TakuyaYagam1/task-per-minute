package game

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
)

var ErrGameNotFound = errors.New("game repository: game not found")

type GamePostgres struct {
	tx *db.TxManager
}

type GameSlotInput struct {
	Slot      domain.GameSlot
	RosterID  uuid.UUID
	CreatedAt time.Time
}

type GameAttemptInput struct {
	Game       domain.Game
	SeriesID   uuid.UUID
	RosterID   uuid.UUID
	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

type GameAttemptRecord struct {
	Game       domain.Game
	SeriesID   uuid.UUID
	RosterID   uuid.UUID
	Revision   int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

type GameSlotRecord struct {
	Slot      domain.GameSlot
	RosterID  uuid.UUID
	Revision  int64
	CreatedAt time.Time
	UpdatedAt time.Time
	Attempts  []GameAttemptRecord
}

func NewGamePostgres(tx *db.TxManager) *GamePostgres {
	return &GamePostgres{tx: tx}
}

func (r *GamePostgres) CreateSlot(
	ctx context.Context,
	in GameSlotInput,
) (*GameSlotRecord, error) {
	if r == nil || r.tx == nil || in.RosterID == uuid.Nil || !validServerTime(in.CreatedAt) ||
		len(in.Slot.Attempts) != 0 || in.Slot.Position > math.MaxInt16 {
		return nil, domain.ErrValidation
	}
	if err := in.Slot.Validate(); err != nil {
		return nil, domain.WrapError(err, domain.ErrValidation)
	}
	row, err := r.tx.Querier(ctx).CreateGameSlot(ctx, sqlc.CreateGameSlotParams{
		ID:                          in.Slot.ID,
		SeriesID:                    in.Slot.SeriesID,
		RosterID:                    in.RosterID,
		SlotNumber:                  int16(in.Slot.Position), //nolint:gosec // validated against the PostgreSQL int2 range below.
		Category:                    string(in.Slot.Category),
		FirstParticipantWinsBefore:  int16(in.Slot.ScoreBefore.FirstParticipantWins),  //nolint:gosec // Game score validation bounds this value to 0..2.
		SecondParticipantWinsBefore: int16(in.Slot.ScoreBefore.SecondParticipantWins), //nolint:gosec // Game score validation bounds this value to 0..2.
		CreatedAt:                   tstz(in.CreatedAt),
	})
	if err != nil {
		return nil, mapRepositoryWriteError("GamePostgres - CreateSlot", err)
	}
	return gameSlotRecord(row, nil), nil
}

func (r *GamePostgres) AppendAttempt(
	ctx context.Context,
	in GameAttemptInput,
) (*GameAttemptRecord, error) {
	if err := validateGameAttemptInput(in); err != nil {
		return nil, err
	}
	row, err := r.tx.Querier(ctx).CreateGameAttempt(ctx, sqlc.CreateGameAttemptParams{
		ID:               in.Game.ID,
		SlotID:           in.Game.SlotID,
		SeriesID:         in.SeriesID,
		RosterID:         in.RosterID,
		AttemptNumber:    int32(in.Game.AttemptNo), //nolint:gosec // validateGameAttemptInput bounds the PostgreSQL int4 value.
		State:            string(in.Game.State),
		ResultReason:     gameResultReason(in.Game.ResultReason),
		WinnerID:         nullableUUID(in.Game.WinnerID),
		ResultRevisionID: nullableResultRevision(in.Game.ResultRevisionID),
		CreatedAt:        tstz(in.CreatedAt),
		StartedAt:        nullableTSTZ(in.StartedAt),
		FinishedAt:       nullableTSTZ(in.FinishedAt),
	})
	if err != nil {
		return nil, mapRepositoryWriteError("GamePostgres - AppendAttempt", err)
	}
	record, err := gameAttemptRecordFromCreate(row)
	if err != nil {
		return nil, fmt.Errorf("GamePostgres - AppendAttempt - map row: %w", err)
	}
	return record, nil
}

func (r *GamePostgres) Get(
	ctx context.Context,
	scope gamedomain.Scope,
) (*domain.Game, error) {
	record, err := r.GetAttemptRecord(ctx, scope)
	if err != nil {
		return nil, err
	}
	game := record.Game
	return &game, nil
}

func (r *GamePostgres) GetAttemptRecord(
	ctx context.Context,
	scope gamedomain.Scope,
) (*GameAttemptRecord, error) {
	if !validGameScope(scope) {
		return nil, domain.ErrValidation
	}
	row, err := r.tx.Querier(ctx).GetGameAttemptScoped(ctx, sqlc.GetGameAttemptScopedParams{
		ID:           scope.GameID,
		SlotID:       scope.SlotID,
		SeriesID:     scope.SeriesID,
		TournamentID: scope.TournamentID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrGameNotFound
		}
		return nil, fmt.Errorf("GamePostgres - GetAttemptRecord: %w", err)
	}
	record, err := gameAttemptRecordFromScoped(row)
	if err != nil {
		return nil, fmt.Errorf("GamePostgres - GetAttemptRecord - map row: %w", err)
	}
	return record, nil
}

func (r *GamePostgres) Update(
	ctx context.Context,
	scope gamedomain.Scope,
	expected domain.GameState,
	next domain.Game,
) (*domain.Game, bool, error) {
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

func (r *GamePostgres) Transition(
	ctx context.Context,
	scope gamedomain.Scope,
	expectedRevision int64,
	expectedState domain.GameState,
	next domain.Game,
	updatedAt time.Time,
) (*GameAttemptRecord, bool, error) {
	if !validGameScope(scope) || expectedRevision < 1 || !expectedState.IsValid() ||
		!validServerTime(updatedAt) || next.ID != scope.GameID || next.SlotID != scope.SlotID {
		return nil, false, domain.ErrValidation
	}
	if err := next.Validate(); err != nil {
		return nil, false, domain.WrapError(err, domain.ErrValidation)
	}
	row, err := r.tx.Querier(ctx).UpdateGameAttemptCAS(ctx, sqlc.UpdateGameAttemptCASParams{
		NextState:        string(next.State),
		ResultReason:     gameResultReason(next.ResultReason),
		WinnerID:         nullableUUID(next.WinnerID),
		ResultRevisionID: nullableResultRevision(next.ResultRevisionID),
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
		return nil, false, mapRepositoryWriteError("GamePostgres - Transition", err)
	}
	record, err := gameAttemptRecordFromCAS(row)
	if err != nil {
		return nil, false, fmt.Errorf("GamePostgres - Transition - map row: %w", err)
	}
	return record, true, nil
}

func (r *GamePostgres) GetSlot(
	ctx context.Context,
	tournamentID uuid.UUID,
	seriesID uuid.UUID,
	slotID uuid.UUID,
) (*GameSlotRecord, error) {
	if tournamentID == uuid.Nil || seriesID == uuid.Nil || slotID == uuid.Nil {
		return nil, domain.ErrValidation
	}
	querier := r.tx.Querier(ctx)
	slot, err := querier.GetGameSlot(ctx, sqlc.GetGameSlotParams{
		ID:           slotID,
		SeriesID:     seriesID,
		TournamentID: tournamentID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrGameNotFound
		}
		return nil, fmt.Errorf("GamePostgres - GetSlot: %w", err)
	}
	attemptRows, err := querier.ListGameAttempts(ctx, slotID)
	if err != nil {
		return nil, fmt.Errorf("GamePostgres - GetSlot - list attempts: %w", err)
	}
	attempts := make([]GameAttemptRecord, 0, len(attemptRows))
	domainAttempts := make([]domain.Game, 0, len(attemptRows))
	for _, row := range attemptRows {
		record, mapErr := gameAttemptRecordFromList(row)
		if mapErr != nil {
			return nil, fmt.Errorf("GamePostgres - GetSlot - map attempt: %w", mapErr)
		}
		attempts = append(attempts, *record)
		domainAttempts = append(domainAttempts, record.Game)
	}
	record := gameSlotRecord(slot, attempts)
	record.Slot.Attempts = domainAttempts
	if err := record.Slot.Validate(); err != nil {
		return nil, fmt.Errorf("GamePostgres - GetSlot - invalid snapshot: %w", err)
	}
	return record, nil
}

func validateGameAttemptInput(in GameAttemptInput) error {
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

func validGameScope(scope gamedomain.Scope) bool {
	return scope.TournamentID != uuid.Nil && scope.SeriesID != uuid.Nil && scope.SlotID != uuid.Nil &&
		scope.GameID != uuid.Nil
}

type gameAttemptRowFields struct {
	ID               uuid.UUID
	SlotID           uuid.UUID
	SeriesID         uuid.UUID
	RosterID         uuid.UUID
	AttemptNumber    int32
	State            string
	ResultReason     *string
	WinnerID         uuid.NullUUID
	ResultRevisionID uuid.NullUUID
	Revision         int64
	CreatedAt        pgtype.Timestamptz
	UpdatedAt        pgtype.Timestamptz
	StartedAt        pgtype.Timestamptz
	FinishedAt       pgtype.Timestamptz
}

func gameAttemptRecordFromCreate(row sqlc.CreateGameAttemptRow) (*GameAttemptRecord, error) {
	return gameAttemptRecord(gameAttemptRowFields{
		ID: row.ID, SlotID: row.SlotID, SeriesID: row.SeriesID, RosterID: row.RosterID,
		AttemptNumber: row.AttemptNumber, State: row.State, ResultReason: row.ResultReason,
		WinnerID: row.WinnerID, ResultRevisionID: row.ResultRevisionID, Revision: row.Revision,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt,
	})
}

func gameAttemptRecordFromScoped(row sqlc.GetGameAttemptScopedRow) (*GameAttemptRecord, error) {
	return gameAttemptRecord(gameAttemptRowFields{
		ID: row.ID, SlotID: row.SlotID, SeriesID: row.SeriesID, RosterID: row.RosterID,
		AttemptNumber: row.AttemptNumber, State: row.State, ResultReason: row.ResultReason,
		WinnerID: row.WinnerID, ResultRevisionID: row.ResultRevisionID, Revision: row.Revision,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt,
	})
}

func gameAttemptRecordFromCAS(row sqlc.UpdateGameAttemptCASRow) (*GameAttemptRecord, error) {
	return gameAttemptRecord(gameAttemptRowFields{
		ID: row.ID, SlotID: row.SlotID, SeriesID: row.SeriesID, RosterID: row.RosterID,
		AttemptNumber: row.AttemptNumber, State: row.State, ResultReason: row.ResultReason,
		WinnerID: row.WinnerID, ResultRevisionID: row.ResultRevisionID, Revision: row.Revision,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt,
	})
}

func gameAttemptRecordFromList(row sqlc.ListGameAttemptsRow) (*GameAttemptRecord, error) {
	return gameAttemptRecord(gameAttemptRowFields{
		ID: row.ID, SlotID: row.SlotID, SeriesID: row.SeriesID, RosterID: row.RosterID,
		AttemptNumber: row.AttemptNumber, State: row.State, ResultReason: row.ResultReason,
		WinnerID: row.WinnerID, ResultRevisionID: row.ResultRevisionID, Revision: row.Revision,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt,
	})
}

func gameAttemptRecord(row gameAttemptRowFields) (*GameAttemptRecord, error) {
	game := domain.Game{
		ID:           row.ID,
		SlotID:       row.SlotID,
		AttemptNo:    int(row.AttemptNumber),
		State:        domain.GameState(row.State),
		ResultReason: domain.GameResultReason(stringValue(row.ResultReason)),
	}
	if row.WinnerID.Valid {
		winnerID := row.WinnerID.UUID
		game.WinnerID = &winnerID
	}
	if row.ResultRevisionID.Valid {
		revisionID := domain.OfficialResultRevisionID(row.ResultRevisionID.UUID)
		game.ResultRevisionID = &revisionID
	}
	if err := game.Validate(); err != nil {
		return nil, err
	}
	return &GameAttemptRecord{
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

func gameSlotRecord(row sqlc.GameSlot, attempts []GameAttemptRecord) *GameSlotRecord {
	return &GameSlotRecord{
		Slot: domain.GameSlot{
			ID:       row.ID,
			SeriesID: row.SeriesID,
			Position: int(row.SlotNumber),
			Category: domain.Category(row.Category),
			ScoreBefore: domain.SeriesScore{
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

func gameResultReason(reason domain.GameResultReason) *string {
	if reason == "" {
		return nil
	}
	value := string(reason)
	return &value
}

func nullableResultRevision(id *domain.OfficialResultRevisionID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: id.UUID(), Valid: true}
}

func mapRepositoryWriteError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case pgUniqueViolation, pgForeignKeyViolation, "23514", "40001":
			return domain.WrapError(err, domain.ErrConflict)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
