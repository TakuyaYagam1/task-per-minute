package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	defaultArenaAuditPageSize = 50
	maxArenaAuditPageSize     = 200
)

type ArenaAuditPostgres struct {
	tx *TxManager
}

type ArenaAuditCursor struct {
	OccurredAt   time.Time
	AuditEventID uuid.UUID
	RevisionID   uuid.UUID
}

type ArenaAuditFilter struct {
	TournamentID uuid.UUID
	EntityKind   string
	EntityID     *uuid.UUID
	EventType    string
	ActorKind    string
	ActorID      *uuid.UUID
	ResultReason string
	OccurredFrom *time.Time
	OccurredTo   *time.Time
	Cursor       *ArenaAuditCursor
	PageSize     int
}

type ArenaAuditRecord struct {
	AuditEventID             uuid.UUID
	TournamentID             uuid.UUID
	RosterID                 uuid.UUID
	SeriesID                 uuid.UUID
	ResultEventID            uuid.UUID
	ActorKind                string
	ActorID                  *uuid.UUID
	EventType                string
	RedactedPayload          json.RawMessage
	OccurredAt               time.Time
	CreatedAt                time.Time
	ResultState              string
	ResultReason             string
	WinnerID                 *uuid.UUID
	OfficialResultRevisionID uuid.UUID
	EntityKind               string
	EntityID                 uuid.UUID
	RevisionNumber           int64
	IsCurrent                bool
	IsSuperseded             bool
}

type ArenaAuditPage struct {
	Records    []ArenaAuditRecord
	NextCursor *ArenaAuditCursor
}

func NewArenaAuditPostgres(tx *TxManager) *ArenaAuditPostgres {
	return &ArenaAuditPostgres{tx: tx}
}

func (r *ArenaAuditPostgres) List(
	ctx context.Context,
	filter ArenaAuditFilter,
) (*ArenaAuditPage, error) {
	pageSize, ok := normalizedArenaAuditPageSize(filter.PageSize)
	if r == nil || r.tx == nil || !ok || !validArenaAuditFilter(filter) {
		return nil, domain.ErrValidation
	}

	rows, err := r.tx.Querier(ctx).ListArenaAuditPage(ctx, sqlc.ListArenaAuditPageParams{
		TournamentID:       filter.TournamentID,
		EntityKind:         optionalTrimmedString(filter.EntityKind),
		EntityID:           nullableUUID(filter.EntityID),
		EventType:          optionalTrimmedString(filter.EventType),
		ActorKind:          optionalTrimmedString(filter.ActorKind),
		ActorID:            nullableUUID(filter.ActorID),
		ResultReason:       optionalTrimmedString(filter.ResultReason),
		OccurredFrom:       nullableTSTZ(filter.OccurredFrom),
		OccurredTo:         nullableTSTZ(filter.OccurredTo),
		CursorOccurredAt:   arenaAuditCursorTime(filter.Cursor),
		CursorAuditEventID: arenaAuditCursorAuditID(filter.Cursor),
		CursorRevisionID:   arenaAuditCursorRevisionID(filter.Cursor),
		PageLimit:          int32(pageSize + 1), //nolint:gosec // pageSize is bounded by maxArenaAuditPageSize.
	})
	if err != nil {
		return nil, fmt.Errorf("ArenaAuditPostgres - List: %w", err)
	}

	hasNext := len(rows) > pageSize
	if hasNext {
		rows = rows[:pageSize]
	}
	records := make([]ArenaAuditRecord, 0, len(rows))
	for _, row := range rows {
		records = append(records, arenaAuditRecord(row))
	}
	page := &ArenaAuditPage{Records: records}
	if hasNext {
		last := records[len(records)-1]
		page.NextCursor = &ArenaAuditCursor{
			OccurredAt: last.OccurredAt, AuditEventID: last.AuditEventID,
			RevisionID: last.OfficialResultRevisionID,
		}
	}
	return page, nil
}

func validArenaAuditFilter(filter ArenaAuditFilter) bool {
	if filter.TournamentID == uuid.Nil || !validArenaAuditEntityFilter(filter) ||
		!validArenaAuditActorFilter(filter) || !validArenaAuditTextFilters(filter) {
		return false
	}
	if filter.OccurredFrom != nil && !validServerTime(*filter.OccurredFrom) {
		return false
	}
	if filter.OccurredTo != nil && !validServerTime(*filter.OccurredTo) {
		return false
	}
	if filter.OccurredFrom != nil && filter.OccurredTo != nil && filter.OccurredFrom.After(*filter.OccurredTo) {
		return false
	}
	return filter.Cursor == nil || (validServerTime(filter.Cursor.OccurredAt) &&
		filter.Cursor.AuditEventID != uuid.Nil && filter.Cursor.RevisionID != uuid.Nil)
}

func validArenaAuditEntityFilter(filter ArenaAuditFilter) bool {
	if filter.EntityKind == "" {
		return filter.EntityID == nil
	}
	if filter.EntityKind != "game_attempt" && filter.EntityKind != "series" {
		return false
	}
	return filter.EntityID == nil || *filter.EntityID != uuid.Nil
}

func validArenaAuditActorFilter(filter ArenaAuditFilter) bool {
	if filter.ActorKind == "" {
		return filter.ActorID == nil
	}
	if filter.ActorKind != arenaResultActorServer && filter.ActorKind != arenaResultActorOperator {
		return false
	}
	if filter.ActorID != nil && *filter.ActorID == uuid.Nil {
		return false
	}
	return filter.ActorKind != arenaResultActorServer || filter.ActorID == nil
}

func validArenaAuditTextFilters(filter ArenaAuditFilter) bool {
	return (filter.EventType == "" || (validTrimmedText(filter.EventType) && len(filter.EventType) <= 64)) &&
		(filter.ResultReason == "" || (validTrimmedText(filter.ResultReason) && len(filter.ResultReason) <= 40))
}

func normalizedArenaAuditPageSize(value int) (int, bool) {
	if value == 0 {
		return defaultArenaAuditPageSize, true
	}
	return value, value > 0 && value <= maxArenaAuditPageSize
}

func arenaAuditRecord(row sqlc.ListArenaAuditPageRow) ArenaAuditRecord {
	return ArenaAuditRecord{
		AuditEventID: row.AuditEventID, TournamentID: row.TournamentID,
		RosterID: row.RosterID, SeriesID: row.SeriesID, ResultEventID: row.ResultEventID,
		ActorKind: row.ActorKind, ActorID: arenaAuditUUIDPointer(row.ActorID), EventType: row.EventType,
		RedactedPayload: append(json.RawMessage(nil), row.RedactedPayload...),
		OccurredAt:      row.OccurredAt.Time.UTC(), CreatedAt: row.CreatedAt.Time.UTC(),
		ResultState: row.ResultState, ResultReason: row.ResultReason, WinnerID: arenaAuditUUIDPointer(row.WinnerID),
		OfficialResultRevisionID: row.OfficialResultRevisionID, EntityKind: row.EntityKind,
		EntityID: row.EntityID, RevisionNumber: row.RevisionNumber,
		IsCurrent: row.IsCurrent, IsSuperseded: !row.IsCurrent,
	}
}

func arenaAuditCursorTime(cursor *ArenaAuditCursor) pgtype.Timestamptz {
	if cursor == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: cursor.OccurredAt, Valid: true}
}

func arenaAuditCursorAuditID(cursor *ArenaAuditCursor) uuid.NullUUID {
	if cursor == nil {
		return uuid.NullUUID{}
	}
	return nullableUUIDValue(cursor.AuditEventID)
}

func arenaAuditCursorRevisionID(cursor *ArenaAuditCursor) uuid.NullUUID {
	if cursor == nil {
		return uuid.NullUUID{}
	}
	return nullableUUIDValue(cursor.RevisionID)
}

func arenaAuditUUIDPointer(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	out := value.UUID
	return &out
}
