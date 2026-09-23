package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	tournamentincident "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/incident"
)

const (
	maxTournamentIncidentEvents    = 4096
	tournamentAuditPayloadFieldCap = 13
)

// TournamentAdminAuditPostgres exposes the durable result audit projection to
// the transport-neutral tournament administration application.
type TournamentAdminAuditPostgres struct {
	tx    *db.TxManager
	audit *AuditPostgres
}

func NewTournamentAdminAuditPostgres(tx *db.TxManager) *TournamentAdminAuditPostgres {
	return &TournamentAdminAuditPostgres{tx: tx, audit: NewAuditPostgres(tx)}
}

func (r *TournamentAdminAuditPostgres) ListAudit(
	ctx context.Context,
	query tournamentincident.AuditQuery,
) (audit.AuditPage, error) {
	if ctx == nil || r == nil || r.tx == nil || r.audit == nil || !validTournamentAdminAuditQuery(query) {
		return audit.AuditPage{}, domain.ErrValidation
	}

	page, err := r.audit.List(ctx, tournamentAuditFilter(query.Filter))
	if err != nil {
		return audit.AuditPage{}, fmt.Errorf("TournamentAdminAuditPostgres - ListAudit: %w", err)
	}
	result, err := tournamentAuditPage(page, query.Filter.TournamentID)
	if err != nil {
		return audit.AuditPage{}, err
	}
	result.Cancellation, err = r.cancellationAudit(ctx, query.Filter)
	if err != nil {
		return audit.AuditPage{}, err
	}
	return result, nil
}

//nolint:gocyclo // Cancellation filtering must preserve the audit query contract and fail closed.
func (r *TournamentAdminAuditPostgres) cancellationAudit(
	ctx context.Context,
	filter audit.AuditFilter,
) (*audit.CancellationAuditEvent, error) {
	if filter.Cursor != nil || filter.EntityKind != "" || filter.EntityID != nil ||
		filter.ResultReason != "" ||
		(filter.EventType != "" && filter.EventType != "tournament.cancelled") ||
		(filter.ActorKind != "" && filter.ActorKind != domain.ResultActorOperator) ||
		(filter.ActorID != nil && *filter.ActorID == uuid.Nil) {
		return nil, nil
	}
	row, err := r.tx.Querier(ctx).GetTournamentCancellationAudit(ctx, filter.TournamentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("TournamentAdminAuditPostgres - ListAudit - load cancellation: %w", err)
	}
	if filter.ActorID != nil && *filter.ActorID != row.ActorID {
		return nil, nil
	}
	if filter.OccurredFrom != nil && row.CancelledAt.Time.Before(*filter.OccurredFrom) {
		return nil, nil
	}
	if filter.OccurredTo != nil && row.CancelledAt.Time.After(*filter.OccurredTo) {
		return nil, nil
	}
	event := audit.CancellationAuditEvent{
		CommandID: row.CommandID, TournamentID: row.TournamentID, RosterID: row.RosterID,
		SourceRevision: row.SourceRevision, ResultingRevision: row.ResultingRevision,
		SourceProjectionRevisionID: row.SourceProjectionRevisionID,
		SourceProjectionRevision:   row.SourceProjectionRevision, ActorID: row.ActorID,
		Reason: row.Reason, AuditEventID: row.AuditEventID, OccurredAt: row.CancelledAt.Time.UTC(),
	}
	if !audit.ValidCancellationAuditEvent(event) {
		return nil, fmt.Errorf("TournamentAdminAuditPostgres - invalid cancellation audit: %w", domain.ErrInternal)
	}
	return &event, nil
}

func (r *TournamentAdminAuditPostgres) LoadIncidentSnapshot(
	ctx context.Context,
	query tournamentincident.IncidentQuery,
) (audit.IncidentBundleSnapshot, error) {
	if ctx == nil || r == nil || r.tx == nil || r.audit == nil ||
		query.Operator.ActorID == uuid.Nil || query.TournamentID == uuid.Nil {
		return audit.IncidentBundleSnapshot{}, domain.ErrValidation
	}

	var snapshot audit.IncidentBundleSnapshot
	err := r.tx.ReadSnapshot(ctx, func(snapshotCtx context.Context) error {
		header, err := tournamentIncidentHeader(snapshotCtx, r.tx.Querier(snapshotCtx), query.TournamentID)
		if err != nil {
			return err
		}
		events, err := r.tournamentIncidentEvents(snapshotCtx, query.TournamentID)
		if err != nil {
			return err
		}
		cancellation, err := tournamentIncidentCancellation(snapshotCtx, r.tx.Querier(snapshotCtx), query.TournamentID)
		if err != nil {
			return err
		}
		snapshot = audit.IncidentBundleSnapshot{
			TournamentID:       query.TournamentID,
			ProjectionRevision: header.projectionRevision,
			GeneratedAt:        header.observedAt,
			Events:             events,
			Cancellation:       cancellation,
		}
		return nil
	})
	if err != nil {
		return audit.IncidentBundleSnapshot{}, err
	}
	return snapshot, nil
}

func tournamentIncidentCancellation(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) (*audit.CancellationAuditEvent, error) {
	row, err := querier.GetTournamentCancellationAudit(ctx, tournamentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf(
			"TournamentAdminAuditPostgres - LoadIncidentSnapshot - load cancellation: %w",
			err,
		)
	}
	if !row.CancelledAt.Valid {
		return nil, fmt.Errorf(
			"TournamentAdminAuditPostgres - LoadIncidentSnapshot - invalid cancellation timestamp: %w",
			domain.ErrInternal,
		)
	}
	return &audit.CancellationAuditEvent{
		CommandID:                  row.CommandID,
		TournamentID:               row.TournamentID,
		RosterID:                   row.RosterID,
		SourceRevision:             row.SourceRevision,
		ResultingRevision:          row.ResultingRevision,
		ActorID:                    row.ActorID,
		Reason:                     row.Reason,
		AuditEventID:               row.AuditEventID,
		SourceProjectionRevisionID: row.SourceProjectionRevisionID,
		SourceProjectionRevision:   row.SourceProjectionRevision,
		OccurredAt:                 row.CancelledAt.Time.UTC(),
	}, nil
}

type tournamentIncidentSnapshot struct {
	projectionRevision int64
	observedAt         time.Time
}

func tournamentIncidentHeader(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) (tournamentIncidentSnapshot, error) {
	row, err := querier.GetTournamentReadCursor(ctx, tournamentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return tournamentIncidentSnapshot{}, domain.ErrTournamentProjectionNotFound
	}
	if err != nil {
		return tournamentIncidentSnapshot{}, fmt.Errorf(
			"TournamentAdminAuditPostgres - LoadIncidentSnapshot - load projection: %w",
			err,
		)
	}
	if row.ProjectionRevision < 1 || !row.ObservedAt.Valid {
		return tournamentIncidentSnapshot{}, fmt.Errorf(
			"TournamentAdminAuditPostgres - LoadIncidentSnapshot - invalid projection snapshot: %w",
			domain.ErrInternal,
		)
	}
	observedAt := row.ObservedAt.Time.UTC()
	if !domain.IsValidServerTime(observedAt) {
		return tournamentIncidentSnapshot{}, fmt.Errorf(
			"TournamentAdminAuditPostgres - LoadIncidentSnapshot - invalid observed time: %w",
			domain.ErrInternal,
		)
	}
	return tournamentIncidentSnapshot{
		projectionRevision: row.ProjectionRevision,
		observedAt:         observedAt,
	}, nil
}

func (r *TournamentAdminAuditPostgres) tournamentIncidentEvents(
	ctx context.Context,
	tournamentID uuid.UUID,
) ([]audit.AuditEvent, error) {
	return collectTournamentIncidentEvents(func(pageSize int, cursor *audit.Cursor) (audit.AuditPage, error) {
		var auditCursor *AuditCursor
		if cursor != nil {
			auditCursor = &AuditCursor{
				OccurredAt: cursor.OccurredAt, AuditEventID: cursor.AuditEventID,
				RevisionID: cursor.RevisionID, SnapshotBound: cursor.SnapshotBound,
			}
		}
		page, err := r.audit.List(ctx, AuditFilter{
			TournamentID: tournamentID, Cursor: auditCursor, PageSize: pageSize,
		})
		if err != nil {
			return audit.AuditPage{}, fmt.Errorf(
				"TournamentAdminAuditPostgres - LoadIncidentSnapshot - list audit: %w", err,
			)
		}
		return tournamentAuditPage(page, tournamentID)
	})
}

func collectTournamentIncidentEvents(
	loadPage func(pageSize int, cursor *audit.Cursor) (audit.AuditPage, error),
) ([]audit.AuditEvent, error) {
	events := make([]audit.AuditEvent, 0, min(maxAuditPageSize, maxTournamentIncidentEvents))
	var cursor *audit.Cursor

	for {
		remaining := maxTournamentIncidentEvents - len(events)
		if remaining <= 0 {
			return nil, fmt.Errorf(
				"TournamentAdminAuditPostgres - incident event limit exceeded: %w", domain.ErrInternal,
			)
		}
		pageSize := min(maxAuditPageSize, remaining)
		page, err := loadPage(pageSize, cursor)
		if err != nil {
			return nil, err
		}
		if len(page.Events) > pageSize {
			return nil, fmt.Errorf(
				"TournamentAdminAuditPostgres - invalid incident page size: %w", domain.ErrInternal,
			)
		}
		events = append(events, page.Events...)
		if page.NextCursor == nil {
			return events, nil
		}
		if len(page.Events) == 0 || len(events) >= maxTournamentIncidentEvents {
			return nil, fmt.Errorf(
				"TournamentAdminAuditPostgres - incident event limit exceeded: %w", domain.ErrInternal,
			)
		}
		cursor = page.NextCursor
	}
}

func validTournamentAdminAuditQuery(query tournamentincident.AuditQuery) bool {
	if query.Operator.ActorID == uuid.Nil {
		return false
	}
	if _, err := audit.LookupAudit(nil, query.Filter); err != nil {
		return false
	}
	return query.Filter.Cursor == nil ||
		(len(query.Filter.Cursor.SnapshotBound) <= 4096 && validAuditSnapshot(query.Filter.Cursor.SnapshotBound))
}

func tournamentAuditFilter(filter audit.AuditFilter) AuditFilter {
	var cursor *AuditCursor
	if filter.Cursor != nil {
		cursor = &AuditCursor{
			OccurredAt: filter.Cursor.OccurredAt, AuditEventID: filter.Cursor.AuditEventID,
			RevisionID: filter.Cursor.RevisionID, SnapshotBound: filter.Cursor.SnapshotBound,
		}
	}
	return AuditFilter{
		TournamentID: filter.TournamentID, EntityKind: string(filter.EntityKind),
		EntityID: cloneAuditUUID(filter.EntityID), EventType: filter.EventType,
		ActorKind: string(filter.ActorKind), ActorID: cloneAuditUUID(filter.ActorID),
		ResultReason: filter.ResultReason, OccurredFrom: cloneAuditTime(filter.OccurredFrom),
		OccurredTo: cloneAuditTime(filter.OccurredTo), Cursor: cursor, PageSize: filter.PageSize,
	}
}

func tournamentAuditPage(page *AuditPage, tournamentID uuid.UUID) (audit.AuditPage, error) {
	if page == nil || page.Records == nil || len(page.Records) > maxAuditPageSize || tournamentID == uuid.Nil {
		return audit.AuditPage{}, fmt.Errorf(
			"TournamentAdminAuditPostgres - invalid audit page: %w",
			domain.ErrInternal,
		)
	}
	events, err := tournamentAuditEvents(page.Records, tournamentID)
	if err != nil {
		return audit.AuditPage{}, err
	}
	nextCursor, err := tournamentAuditNextCursor(page.NextCursor, events)
	if err != nil {
		return audit.AuditPage{}, err
	}
	return audit.AuditPage{Events: events, NextCursor: nextCursor}, nil
}

func tournamentAuditEvents(records []AuditRecord, tournamentID uuid.UUID) ([]audit.AuditEvent, error) {
	events := make([]audit.AuditEvent, len(records))
	for index := range records {
		var err error
		events[index], err = tournamentAuditEvent(records[index])
		if err != nil {
			return nil, err
		}
	}
	if len(events) == 0 {
		return events, nil
	}
	validated, err := audit.LookupAudit(events, audit.AuditFilter{
		TournamentID: tournamentID,
		PageSize:     len(events),
	})
	if err != nil || len(validated.Events) != len(events) {
		return nil, fmt.Errorf(
			"TournamentAdminAuditPostgres - invalid audit records: %w",
			domain.ErrInternal,
		)
	}
	return validated.Events, nil
}

func tournamentAuditNextCursor(cursor *AuditCursor, events []audit.AuditEvent) (*audit.Cursor, error) {
	if cursor == nil {
		return nil, nil
	}
	if len(events) == 0 || len(cursor.SnapshotBound) > 4096 || !validAuditCursor(cursor) {
		return nil, fmt.Errorf(
			"TournamentAdminAuditPostgres - invalid audit cursor: %w",
			domain.ErrInternal,
		)
	}
	last := events[len(events)-1]
	if !last.OccurredAt.Equal(cursor.OccurredAt) || last.AuditEventID != cursor.AuditEventID ||
		last.OfficialResultRevisionID != cursor.RevisionID {
		return nil, fmt.Errorf(
			"TournamentAdminAuditPostgres - mismatched audit cursor: %w",
			domain.ErrInternal,
		)
	}
	return &audit.Cursor{
		OccurredAt: cursor.OccurredAt, AuditEventID: cursor.AuditEventID,
		RevisionID: cursor.RevisionID, SnapshotBound: cursor.SnapshotBound,
	}, nil
}

func tournamentAuditEvent(record AuditRecord) (audit.AuditEvent, error) {
	payload, err := redactTournamentAuditPayload(record.RedactedPayload)
	if err != nil {
		return audit.AuditEvent{}, fmt.Errorf(
			"TournamentAdminAuditPostgres - invalid redacted payload: %w",
			domain.ErrInternal,
		)
	}
	return audit.AuditEvent{
		AuditEventID: record.AuditEventID, TournamentID: record.TournamentID,
		RosterID: record.RosterID, SeriesID: record.SeriesID, ResultEventID: record.ResultEventID,
		ActorKind: domain.ResultActorKind(record.ActorKind), ActorID: cloneAuditUUID(record.ActorID),
		EventType: record.EventType, RedactedPayload: payload, OccurredAt: record.OccurredAt,
		CreatedAt: record.CreatedAt, ResultState: record.ResultState, ResultReason: record.ResultReason,
		WinnerID: cloneAuditUUID(record.WinnerID), OfficialResultRevisionID: record.OfficialResultRevisionID,
		EntityKind: audit.EntityKind(record.EntityKind), EntityID: record.EntityID,
		RevisionNumber: record.RevisionNumber, IsCurrent: record.IsCurrent,
		IsSuperseded: record.IsSuperseded,
	}, nil
}

func redactTournamentAuditPayload(payload json.RawMessage) (json.RawMessage, error) {
	var source map[string]json.RawMessage
	if len(payload) == 0 || len(payload) > 64<<10 || json.Unmarshal(payload, &source) != nil || len(source) == 0 {
		return nil, domain.ErrValidation
	}
	redacted := make(map[string]json.RawMessage, tournamentAuditPayloadFieldCap)
	for key, value := range source {
		if isTournamentAuditPayloadField(key) && string(value) != "null" {
			redacted[key] = append(json.RawMessage(nil), value...)
		}
	}
	if len(redacted) == 0 {
		return nil, domain.ErrValidation
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return nil, fmt.Errorf("encode redacted audit payload: %w", err)
	}
	return encoded, nil
}

func isTournamentAuditPayloadField(key string) bool {
	switch key {
	case "attempt_id", "entity_id", "entity_kind", "previous_revision_id", "projection_revision_id",
		"reason", "result_reason", "revision_number", "series_id", "source_projection_revision_id",
		"state", "tournament_id", "winner_id":
		return true
	default:
		return false
	}
}

func cloneAuditUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneAuditTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

var _ tournamentincident.AuditPort = (*TournamentAdminAuditPostgres)(nil)
var _ tournamentincident.IncidentSnapshotPort = (*TournamentAdminAuditPostgres)(nil)
