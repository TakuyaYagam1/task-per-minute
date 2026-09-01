package arena_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestAuditLookupAppliesEveryFilter(t *testing.T) {
	t.Parallel()

	tournamentID := task057ID(1)
	operatorID := task057ID(2)
	entityID := task057ID(3)
	occurredAt := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	target := task057AuditEvent(10, tournamentID, occurredAt)
	target.EntityKind = arena.AuditEntityGameAttempt
	target.EntityID = entityID
	target.EventType = "result_superseded"
	target.ActorKind = arena.ArenaResultActorOperator
	target.ActorID = &operatorID
	target.ResultReason = "derived_revision_superseded"
	target.ResultState = "superseded"
	target.IsCurrent = false
	target.IsSuperseded = true

	otherTournament := task057AuditEvent(11, task057ID(99), occurredAt)
	otherEntityKind := task057AuditEvent(12, tournamentID, occurredAt)
	otherEntityKind.EntityKind = arena.AuditEntitySeries
	otherEntityKind.EntityID = otherEntityKind.SeriesID
	otherEntityID := task057AuditEvent(13, tournamentID, occurredAt)
	otherEventType := task057AuditEvent(14, tournamentID, occurredAt)
	otherEventType.EventType = "result_recorded"
	serverActor := task057AuditEvent(15, tournamentID, occurredAt)
	serverActor.ActorKind = arena.ArenaResultActorServer
	serverActor.ActorID = nil
	otherActorID := task057AuditEvent(16, tournamentID, occurredAt)
	otherOperatorID := task057ID(4)
	otherActorID.ActorID = &otherOperatorID
	otherReason := task057AuditEvent(17, tournamentID, occurredAt)
	otherReason.ResultReason = "series_cancelled"
	older := task057AuditEvent(18, tournamentID, occurredAt.Add(-time.Hour))
	newer := task057AuditEvent(19, tournamentID, occurredAt.Add(time.Hour))
	events := []arena.AuditEvent{
		otherTournament, otherEntityKind, otherEntityID, otherEventType, serverActor,
		otherActorID, otherReason, older, newer, target,
	}

	tests := []struct {
		name   string
		filter arena.AuditFilter
		wantID uuid.UUID
	}{
		{name: "tournament", filter: arena.AuditFilter{TournamentID: tournamentID}, wantID: newer.AuditEventID},
		{name: "entity kind", filter: arena.AuditFilter{TournamentID: tournamentID, EntityKind: arena.AuditEntitySeries}, wantID: otherEntityKind.AuditEventID},
		{name: "entity identity", filter: arena.AuditFilter{TournamentID: tournamentID, EntityKind: arena.AuditEntityGameAttempt, EntityID: &entityID}, wantID: target.AuditEventID},
		{name: "event type", filter: arena.AuditFilter{TournamentID: tournamentID, EventType: "result_recorded"}, wantID: otherEventType.AuditEventID},
		{name: "actor kind", filter: arena.AuditFilter{TournamentID: tournamentID, ActorKind: arena.ArenaResultActorServer}, wantID: serverActor.AuditEventID},
		{name: "actor identity", filter: arena.AuditFilter{TournamentID: tournamentID, ActorKind: arena.ArenaResultActorOperator, ActorID: &operatorID}, wantID: target.AuditEventID},
		{name: "result reason", filter: arena.AuditFilter{TournamentID: tournamentID, ResultReason: "series_cancelled"}, wantID: otherReason.AuditEventID},
		{name: "inclusive time range", filter: arena.AuditFilter{TournamentID: tournamentID, OccurredFrom: &occurredAt, OccurredTo: &occurredAt}, wantID: target.AuditEventID},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			page, err := arena.LookupAudit(events, test.filter)
			require.NoError(t, err)
			require.NotEmpty(t, page.Events)
			require.Contains(t, auditEventIDs(page.Events), test.wantID)
			for _, event := range page.Events {
				require.Equal(t, tournamentID, event.TournamentID)
				if test.filter.EntityKind != "" {
					require.Equal(t, test.filter.EntityKind, event.EntityKind)
				}
				if test.filter.EntityID != nil {
					require.Equal(t, *test.filter.EntityID, event.EntityID)
				}
				if test.filter.EventType != "" {
					require.Equal(t, test.filter.EventType, event.EventType)
				}
				if test.filter.ActorKind != "" {
					require.Equal(t, test.filter.ActorKind, event.ActorKind)
				}
				if test.filter.ActorID != nil {
					require.NotNil(t, event.ActorID)
					require.Equal(t, *test.filter.ActorID, *event.ActorID)
				}
				if test.filter.ResultReason != "" {
					require.Equal(t, test.filter.ResultReason, event.ResultReason)
				}
				if test.filter.OccurredFrom != nil {
					require.False(t, event.OccurredAt.Before(*test.filter.OccurredFrom))
				}
				if test.filter.OccurredTo != nil {
					require.False(t, event.OccurredAt.After(*test.filter.OccurredTo))
				}
			}
		})
	}

	combined, err := arena.LookupAudit(events, arena.AuditFilter{
		TournamentID: tournamentID,
		EntityKind:   arena.AuditEntityGameAttempt,
		EntityID:     &entityID,
		EventType:    "result_superseded",
		ActorKind:    arena.ArenaResultActorOperator,
		ActorID:      &operatorID,
		ResultReason: "derived_revision_superseded",
		OccurredFrom: &occurredAt,
		OccurredTo:   &occurredAt,
	})
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{target.AuditEventID}, auditEventIDs(combined.Events))
	require.True(t, combined.Events[0].IsSuperseded)
	require.False(t, combined.Events[0].IsCurrent)
}

func TestAuditLookupUsesStableCursor(t *testing.T) {
	t.Parallel()

	tournamentID := task057ID(100)
	newest := time.Date(2026, time.September, 1, 13, 0, 0, 0, time.UTC)
	events := []arena.AuditEvent{
		task057AuditEvent(201, tournamentID, newest.Add(-time.Second)),
		task057AuditEvent(203, tournamentID, newest),
		task057AuditEvent(202, tournamentID, newest),
		task057AuditEvent(204, tournamentID, newest),
		task057AuditEvent(200, tournamentID, newest.Add(-2*time.Second)),
	}
	events[3].AuditEventID = events[1].AuditEventID
	events[3].OfficialResultRevisionID = task057ID(9999)

	first, err := arena.LookupAudit(events, arena.AuditFilter{TournamentID: tournamentID, PageSize: 2})
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{events[3].AuditEventID, events[1].AuditEventID}, auditEventIDs(first.Events))
	require.NotNil(t, first.NextCursor)
	require.Equal(t, events[1].OfficialResultRevisionID, first.NextCursor.RevisionID)

	second, err := arena.LookupAudit(events, arena.AuditFilter{
		TournamentID: tournamentID, PageSize: 2, Cursor: first.NextCursor,
	})
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{events[2].AuditEventID, events[0].AuditEventID}, auditEventIDs(second.Events))
	require.NotNil(t, second.NextCursor)

	third, err := arena.LookupAudit(events, arena.AuditFilter{
		TournamentID: tournamentID, PageSize: 2, Cursor: second.NextCursor,
	})
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{events[4].AuditEventID}, auditEventIDs(third.Events))
	require.Nil(t, third.NextCursor)

	first.Events[0].RedactedPayload[0] = '['
	repeated, err := arena.LookupAudit(events, arena.AuditFilter{TournamentID: tournamentID, PageSize: 2})
	require.NoError(t, err)
	require.True(t, json.Valid(repeated.Events[0].RedactedPayload))
}

func TestAuditLookupRejectsInvalidBoundaries(t *testing.T) {
	t.Parallel()

	tournamentID := task057ID(300)
	events := []arena.AuditEvent{task057AuditEvent(301, tournamentID, time.Date(2026, time.September, 1, 14, 0, 0, 0, time.UTC))}
	invalidCursor := &arena.AuditCursor{OccurredAt: events[0].OccurredAt, AuditEventID: events[0].AuditEventID}

	for _, filter := range []arena.AuditFilter{
		{},
		{TournamentID: tournamentID, PageSize: 201},
		{TournamentID: tournamentID, EntityID: &events[0].EntityID},
		{TournamentID: tournamentID, ActorID: events[0].ActorID},
		{TournamentID: tournamentID, Cursor: invalidCursor},
	} {
		_, err := arena.LookupAudit(events, filter)
		require.ErrorIs(t, err, arena.ErrInvalidAuditLookup)
	}
}

func task057AuditEvent(value int, tournamentID uuid.UUID, occurredAt time.Time) arena.AuditEvent {
	operatorID := task057ID(8000)
	return arena.AuditEvent{
		AuditEventID: task057ID(value), TournamentID: tournamentID,
		RosterID: task057ID(value + 1000), SeriesID: task057ID(value + 2000),
		ResultEventID: task057ID(value + 3000), ActorKind: arena.ArenaResultActorOperator,
		ActorID: &operatorID, EventType: "result_superseded",
		RedactedPayload: json.RawMessage(fmt.Sprintf(`{"entity_id":%q,"reason":"correction"}`, task057ID(value+4000))),
		OccurredAt:      occurredAt, CreatedAt: occurredAt.Add(time.Second), ResultState: "superseded",
		ResultReason: "derived_revision_superseded", OfficialResultRevisionID: task057ID(value + 5000),
		EntityKind: arena.AuditEntityGameAttempt, EntityID: task057ID(value + 4000), RevisionNumber: 2,
		IsCurrent: false, IsSuperseded: true,
	}
}

func auditEventIDs(events []arena.AuditEvent) []uuid.UUID {
	result := make([]uuid.UUID, len(events))
	for index := range events {
		result[index] = events[index].AuditEventID
	}
	return result
}

func task057ID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("05700000-0000-4000-8000-%012d", value))
}
