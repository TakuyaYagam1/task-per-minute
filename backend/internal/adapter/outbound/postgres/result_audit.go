package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	defaultAuditPageSize = 50
	maxAuditPageSize     = 200
)

type AuditPostgres struct {
	tx *TxManager
}

type AuditCursor struct {
	OccurredAt    time.Time
	AuditEventID  uuid.UUID
	RevisionID    uuid.UUID
	SnapshotBound string
}

type AuditFilter struct {
	TournamentID uuid.UUID
	EntityKind   string
	EntityID     *uuid.UUID
	EventType    string
	ActorKind    string
	ActorID      *uuid.UUID
	ResultReason string
	OccurredFrom *time.Time
	OccurredTo   *time.Time
	Cursor       *AuditCursor
	PageSize     int
}

type AuditRecord struct {
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

type AuditPage struct {
	Records    []AuditRecord
	NextCursor *AuditCursor
}

func NewAuditPostgres(tx *TxManager) *AuditPostgres {
	return &AuditPostgres{tx: tx}
}

func (r *AuditPostgres) List(
	ctx context.Context,
	filter AuditFilter,
) (*AuditPage, error) {
	pageSize, ok := normalizedAuditPageSize(filter.PageSize)
	if r == nil || r.tx == nil || !ok || !validAuditFilter(filter) {
		return nil, domain.ErrValidation
	}

	rows, err := r.tx.Querier(ctx).ListResultAuditPage(ctx, sqlc.ListResultAuditPageParams{
		TournamentID:       filter.TournamentID,
		EntityKind:         optionalTrimmedString(filter.EntityKind),
		EntityID:           nullableUUID(filter.EntityID),
		EventType:          optionalTrimmedString(filter.EventType),
		ActorKind:          optionalTrimmedString(filter.ActorKind),
		ActorID:            nullableUUID(filter.ActorID),
		ResultReason:       optionalTrimmedString(filter.ResultReason),
		OccurredFrom:       nullableTSTZ(filter.OccurredFrom),
		OccurredTo:         nullableTSTZ(filter.OccurredTo),
		CursorOccurredAt:   auditCursorTime(filter.Cursor),
		CursorAuditEventID: auditCursorAuditID(filter.Cursor),
		CursorRevisionID:   auditCursorRevisionID(filter.Cursor),
		SnapshotBound:      auditCursorSnapshot(filter.Cursor),
		PageLimit:          int32(pageSize + 1), //nolint:gosec // pageSize is bounded by maxAuditPageSize.
	})
	if err != nil {
		return nil, fmt.Errorf("AuditPostgres - List: %w", err)
	}

	hasNext := len(rows) > pageSize
	if hasNext {
		rows = rows[:pageSize]
	}
	records := make([]AuditRecord, 0, len(rows))
	for _, row := range rows {
		records = append(records, auditRecord(row))
	}
	page := &AuditPage{Records: records}
	if hasNext {
		last := records[len(records)-1]
		page.NextCursor = &AuditCursor{
			OccurredAt: last.OccurredAt, AuditEventID: last.AuditEventID,
			RevisionID: last.OfficialResultRevisionID, SnapshotBound: rows[0].SnapshotBound,
		}
	}
	return page, nil
}

func validAuditFilter(filter AuditFilter) bool {
	if filter.TournamentID == uuid.Nil || !validAuditEntityFilter(filter) ||
		!validAuditActorFilter(filter) || !validAuditTextFilters(filter) {
		return false
	}
	return validAuditTimeRange(filter.OccurredFrom, filter.OccurredTo) && validAuditCursor(filter.Cursor)
}

func validAuditTimeRange(from, to *time.Time) bool {
	if from != nil && !validServerTime(*from) {
		return false
	}
	if to != nil && !validServerTime(*to) {
		return false
	}
	return from == nil || to == nil || !from.After(*to)
}

func validAuditCursor(cursor *AuditCursor) bool {
	return cursor == nil || (validServerTime(cursor.OccurredAt) &&
		cursor.AuditEventID != uuid.Nil && cursor.RevisionID != uuid.Nil &&
		len(cursor.SnapshotBound) <= 4096 && validAuditSnapshot(cursor.SnapshotBound))
}

func validAuditEntityFilter(filter AuditFilter) bool {
	if filter.EntityKind == "" {
		return filter.EntityID == nil
	}
	if filter.EntityKind != "game_attempt" && filter.EntityKind != "series" {
		return false
	}
	return filter.EntityID == nil || *filter.EntityID != uuid.Nil
}

func validAuditActorFilter(filter AuditFilter) bool {
	if filter.ActorKind == "" {
		return filter.ActorID == nil
	}
	if filter.ActorKind != resultActorServer && filter.ActorKind != resultActorOperator {
		return false
	}
	if filter.ActorID != nil && *filter.ActorID == uuid.Nil {
		return false
	}
	return filter.ActorKind != resultActorServer || filter.ActorID == nil
}

func validAuditTextFilters(filter AuditFilter) bool {
	return (filter.EventType == "" || (validTrimmedText(filter.EventType) && len(filter.EventType) <= 64)) &&
		(filter.ResultReason == "" || (validTrimmedText(filter.ResultReason) && len(filter.ResultReason) <= 40))
}

func normalizedAuditPageSize(value int) (int, bool) {
	if value == 0 {
		return defaultAuditPageSize, true
	}
	return value, value > 0 && value <= maxAuditPageSize
}

func auditRecord(row sqlc.ListResultAuditPageRow) AuditRecord {
	return AuditRecord{
		AuditEventID: row.AuditEventID, TournamentID: row.TournamentID,
		RosterID: row.RosterID, SeriesID: optionalUUIDValue(row.SeriesID),
		ResultEventID: optionalUUIDValue(row.ResultEventID),
		ActorKind:     row.ActorKind, ActorID: auditUUIDPointer(row.ActorID), EventType: row.EventType,
		RedactedPayload: append(json.RawMessage(nil), row.RedactedPayload...),
		OccurredAt:      row.OccurredAt.Time.UTC(), CreatedAt: row.CreatedAt.Time.UTC(),
		ResultState: row.ResultState, ResultReason: row.ResultReason, WinnerID: auditUUIDPointer(row.WinnerID),
		OfficialResultRevisionID: row.OfficialResultRevisionID, EntityKind: row.EntityKind,
		EntityID: row.EntityID, RevisionNumber: row.RevisionNumber,
		IsCurrent: row.IsCurrent, IsSuperseded: !row.IsCurrent,
	}
}

func auditCursorTime(cursor *AuditCursor) pgtype.Timestamptz {
	if cursor == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: cursor.OccurredAt, Valid: true}
}

func auditCursorAuditID(cursor *AuditCursor) uuid.NullUUID {
	if cursor == nil {
		return uuid.NullUUID{}
	}
	return nullableUUIDValue(cursor.AuditEventID)
}

func auditCursorRevisionID(cursor *AuditCursor) uuid.NullUUID {
	if cursor == nil {
		return uuid.NullUUID{}
	}
	return nullableUUIDValue(cursor.RevisionID)
}

func auditCursorSnapshot(cursor *AuditCursor) *string {
	if cursor == nil {
		return nil
	}
	return &cursor.SnapshotBound
}

func validAuditSnapshot(value string) bool {
	parts := strings.Split(value, ":")
	if len(parts) != 3 {
		return false
	}
	xmin, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || xmin == 0 {
		return false
	}
	xmax, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil || xmax == 0 || xmin > xmax {
		return false
	}
	if parts[2] == "" {
		return true
	}
	var previous uint64
	for index, raw := range strings.Split(parts[2], ",") {
		xid, parseErr := strconv.ParseUint(raw, 10, 64)
		if parseErr != nil || xid < xmin || xid >= xmax || (index > 0 && xid <= previous) {
			return false
		}
		previous = xid
	}
	return true
}

func auditUUIDPointer(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid {
		return nil
	}
	out := value.UUID
	return &out
}
