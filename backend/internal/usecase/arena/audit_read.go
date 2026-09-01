package arena

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	defaultAuditPageSize = 50
	maxAuditPageSize     = 200
	maxAuditPayloadBytes = 64 << 10
)

var ErrInvalidAuditLookup = errors.New("invalid Arena audit lookup")

type AuditEntityKind string

const (
	AuditEntityGameAttempt AuditEntityKind = "game_attempt"
	AuditEntitySeries      AuditEntityKind = "series"
)

type AuditCursor struct {
	OccurredAt   time.Time
	AuditEventID uuid.UUID
	RevisionID   uuid.UUID
}

type AuditFilter struct {
	TournamentID uuid.UUID
	EntityKind   AuditEntityKind
	EntityID     *uuid.UUID
	EventType    string
	ActorKind    ArenaResultActorKind
	ActorID      *uuid.UUID
	ResultReason string
	OccurredFrom *time.Time
	OccurredTo   *time.Time
	Cursor       *AuditCursor
	PageSize     int
}

type AuditEvent struct {
	AuditEventID             uuid.UUID
	TournamentID             uuid.UUID
	RosterID                 uuid.UUID
	SeriesID                 uuid.UUID
	ResultEventID            uuid.UUID
	ActorKind                ArenaResultActorKind
	ActorID                  *uuid.UUID
	EventType                string
	RedactedPayload          json.RawMessage
	OccurredAt               time.Time
	CreatedAt                time.Time
	ResultState              string
	ResultReason             string
	WinnerID                 *uuid.UUID
	OfficialResultRevisionID uuid.UUID
	EntityKind               AuditEntityKind
	EntityID                 uuid.UUID
	RevisionNumber           int64
	IsCurrent                bool
	IsSuperseded             bool
}

type AuditPage struct {
	Events     []AuditEvent
	NextCursor *AuditCursor
}

func LookupAudit(events []AuditEvent, filter AuditFilter) (AuditPage, error) {
	pageSize, ok := auditPageSize(filter.PageSize)
	if !ok || !validAuditFilter(filter) {
		return AuditPage{}, ErrInvalidAuditLookup
	}
	ordered, err := validatedAuditEvents(events)
	if err != nil {
		return AuditPage{}, err
	}

	matched := make([]AuditEvent, 0, min(pageSize+1, len(ordered)))
	for _, event := range ordered {
		if !auditEventMatches(event, filter) {
			continue
		}
		matched = append(matched, cloneAuditEvent(event))
		if len(matched) == pageSize+1 {
			break
		}
	}
	page := AuditPage{Events: matched}
	if len(page.Events) > pageSize {
		page.Events = page.Events[:pageSize]
		last := page.Events[len(page.Events)-1]
		page.NextCursor = &AuditCursor{
			OccurredAt: last.OccurredAt, AuditEventID: last.AuditEventID,
			RevisionID: last.OfficialResultRevisionID,
		}
	}
	return page, nil
}

func validatedAuditEvents(events []AuditEvent) ([]AuditEvent, error) {
	ordered := make([]AuditEvent, len(events))
	seen := make(map[string]struct{}, len(events))
	for index := range events {
		if !validAuditEvent(events[index]) {
			return nil, fmt.Errorf("%w: malformed event", ErrInvalidAuditLookup)
		}
		key := auditOrderKey(events[index])
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("%w: duplicate cursor tuple", ErrInvalidAuditLookup)
		}
		seen[key] = struct{}{}
		ordered[index] = cloneAuditEvent(events[index])
	}
	sort.Slice(ordered, func(first, second int) bool {
		return compareAuditOrder(ordered[first], ordered[second]) > 0
	})
	return ordered, nil
}

func validAuditEvent(event AuditEvent) bool {
	if !validAuditEventIdentity(event) || !validAuditEventMetadata(event) {
		return false
	}
	if event.WinnerID != nil && *event.WinnerID == uuid.Nil {
		return false
	}
	actor := ArenaResultActor{Kind: event.ActorKind, PrincipalID: event.ActorID}
	var payload map[string]json.RawMessage
	return actor.Validate() == nil && json.Unmarshal(event.RedactedPayload, &payload) == nil && len(payload) > 0
}

func validAuditEventIdentity(event AuditEvent) bool {
	return event.AuditEventID != uuid.Nil && event.TournamentID != uuid.Nil && event.RosterID != uuid.Nil &&
		event.SeriesID != uuid.Nil && event.ResultEventID != uuid.Nil &&
		event.OfficialResultRevisionID != uuid.Nil && event.EntityID != uuid.Nil && event.EntityKind.valid()
}

func validAuditEventMetadata(event AuditEvent) bool {
	return event.RevisionNumber >= 1 && event.IsCurrent != event.IsSuperseded &&
		validArenaServerTime(event.OccurredAt) && validArenaServerTime(event.CreatedAt) &&
		!event.CreatedAt.Before(event.OccurredAt) && validAuditText(event.EventType, 64) &&
		validAuditText(event.ResultState, 40) && validAuditText(event.ResultReason, 40) &&
		len(event.RedactedPayload) > 0 && len(event.RedactedPayload) <= maxAuditPayloadBytes
}

func validAuditFilter(filter AuditFilter) bool {
	if filter.TournamentID == uuid.Nil || !validAuditFilterEntity(filter) || !validAuditFilterActor(filter) ||
		(filter.EventType != "" && !validAuditText(filter.EventType, 64)) ||
		(filter.ResultReason != "" && !validAuditText(filter.ResultReason, 40)) {
		return false
	}
	return validAuditFilterTimes(filter) && validAuditCursor(filter.Cursor)
}

func validAuditFilterTimes(filter AuditFilter) bool {
	if filter.OccurredFrom != nil && !validArenaServerTime(*filter.OccurredFrom) {
		return false
	}
	if filter.OccurredTo != nil && !validArenaServerTime(*filter.OccurredTo) {
		return false
	}
	return filter.OccurredFrom == nil || filter.OccurredTo == nil ||
		!filter.OccurredFrom.After(*filter.OccurredTo)
}

func validAuditCursor(cursor *AuditCursor) bool {
	return cursor == nil || (validArenaServerTime(cursor.OccurredAt) &&
		cursor.AuditEventID != uuid.Nil && cursor.RevisionID != uuid.Nil)
}

func validAuditFilterEntity(filter AuditFilter) bool {
	if filter.EntityKind == "" {
		return filter.EntityID == nil
	}
	return filter.EntityKind.valid() && (filter.EntityID == nil || *filter.EntityID != uuid.Nil)
}

func validAuditFilterActor(filter AuditFilter) bool {
	if filter.ActorKind == "" {
		return filter.ActorID == nil
	}
	if filter.ActorKind != ArenaResultActorServer && filter.ActorKind != ArenaResultActorOperator {
		return false
	}
	if filter.ActorID != nil && *filter.ActorID == uuid.Nil {
		return false
	}
	return filter.ActorKind != ArenaResultActorServer || filter.ActorID == nil
}

func auditEventMatches(event AuditEvent, filter AuditFilter) bool {
	return auditEventMatchesEntity(event, filter) && auditEventMatchesDetails(event, filter) &&
		auditEventMatchesTime(event, filter)
}

func auditEventMatchesEntity(event AuditEvent, filter AuditFilter) bool {
	return event.TournamentID == filter.TournamentID &&
		(filter.EntityKind == "" || event.EntityKind == filter.EntityKind) &&
		(filter.EntityID == nil || event.EntityID == *filter.EntityID)
}

func auditEventMatchesDetails(event AuditEvent, filter AuditFilter) bool {
	actorMatches := filter.ActorID == nil || (event.ActorID != nil && *event.ActorID == *filter.ActorID)
	return (filter.EventType == "" || event.EventType == filter.EventType) &&
		(filter.ActorKind == "" || event.ActorKind == filter.ActorKind) && actorMatches &&
		(filter.ResultReason == "" || event.ResultReason == filter.ResultReason)
}

func auditEventMatchesTime(event AuditEvent, filter AuditFilter) bool {
	return (filter.OccurredFrom == nil || !event.OccurredAt.Before(*filter.OccurredFrom)) &&
		(filter.OccurredTo == nil || !event.OccurredAt.After(*filter.OccurredTo)) &&
		(filter.Cursor == nil || auditEventBeforeCursor(event, *filter.Cursor))
}

func auditEventBeforeCursor(event AuditEvent, cursor AuditCursor) bool {
	if !event.OccurredAt.Equal(cursor.OccurredAt) {
		return event.OccurredAt.Before(cursor.OccurredAt)
	}
	if compared := bytes.Compare(event.AuditEventID[:], cursor.AuditEventID[:]); compared != 0 {
		return compared < 0
	}
	return bytes.Compare(event.OfficialResultRevisionID[:], cursor.RevisionID[:]) < 0
}

func compareAuditOrder(first, second AuditEvent) int {
	if !first.OccurredAt.Equal(second.OccurredAt) {
		if first.OccurredAt.After(second.OccurredAt) {
			return 1
		}
		return -1
	}
	if compared := bytes.Compare(first.AuditEventID[:], second.AuditEventID[:]); compared != 0 {
		return compared
	}
	return bytes.Compare(first.OfficialResultRevisionID[:], second.OfficialResultRevisionID[:])
}

func auditOrderKey(event AuditEvent) string {
	return event.OccurredAt.Format(time.RFC3339Nano) + "/" + event.AuditEventID.String() + "/" +
		event.OfficialResultRevisionID.String()
}

func auditPageSize(value int) (int, bool) {
	if value == 0 {
		return defaultAuditPageSize, true
	}
	return value, value > 0 && value <= maxAuditPageSize
}

func (kind AuditEntityKind) valid() bool {
	return kind == AuditEntityGameAttempt || kind == AuditEntitySeries
}

func validAuditText(value string, maximum int) bool {
	return value != "" && value == strings.TrimSpace(value) && len(value) <= maximum
}

func cloneAuditEvent(event AuditEvent) AuditEvent {
	clone := event
	clone.ActorID = cloneUUIDPointer(event.ActorID)
	clone.WinnerID = cloneUUIDPointer(event.WinnerID)
	clone.RedactedPayload = append(json.RawMessage(nil), event.RedactedPayload...)
	return clone
}
