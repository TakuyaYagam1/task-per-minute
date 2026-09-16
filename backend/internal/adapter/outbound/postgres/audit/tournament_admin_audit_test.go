package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin"
)

func TestTournamentAuditPagePreservesStableCursorAndRedactsPayload(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	record := tournamentAuditRecordFixture(tournamentID)
	record.RedactedPayload = json.RawMessage(`{
		"attempt_id":"10000000-0000-0000-0000-000000000001",
		"secret_token":"must-not-leak",
		"submitted_flag":"must-not-leak"
	}`)
	cursor := &AuditCursor{
		OccurredAt: record.OccurredAt, AuditEventID: record.AuditEventID,
		RevisionID: record.OfficialResultRevisionID, SnapshotBound: "42:47:42,45",
	}

	page, err := tournamentAuditPage(&AuditPage{
		Records: []AuditRecord{record}, NextCursor: cursor,
	}, tournamentID)
	if err != nil {
		t.Fatalf("tournamentAuditPage() error = %v", err)
	}
	if len(page.Events) != 1 || page.NextCursor == nil {
		t.Fatalf("tournamentAuditPage() = %+v, want one event and cursor", page)
	}
	if page.NextCursor.SnapshotBound != cursor.SnapshotBound {
		t.Fatalf("SnapshotBound = %q, want %q", page.NextCursor.SnapshotBound, cursor.SnapshotBound)
	}
	if bytes.Contains(page.Events[0].RedactedPayload, []byte("secret_token")) ||
		bytes.Contains(page.Events[0].RedactedPayload, []byte("submitted_flag")) {
		t.Fatalf("redacted payload leaked a forbidden field: %s", page.Events[0].RedactedPayload)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(page.Events[0].RedactedPayload, &payload); err != nil {
		t.Fatalf("decode redacted payload: %v", err)
	}
	if len(payload) != 1 || payload["attempt_id"] == nil {
		t.Fatalf("redacted payload = %v, want only attempt_id", payload)
	}
}

func TestTournamentAuditPageRejectsCursorOutsideReturnedPage(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	record := tournamentAuditRecordFixture(tournamentID)
	_, err := tournamentAuditPage(&AuditPage{
		Records: []AuditRecord{record},
		NextCursor: &AuditCursor{
			OccurredAt: record.OccurredAt, AuditEventID: uuid.New(),
			RevisionID: record.OfficialResultRevisionID, SnapshotBound: "42:47:",
		},
	}, tournamentID)
	if !errors.Is(err, domain.ErrInternal) {
		t.Fatalf("tournamentAuditPage() error = %v, want %v", err, domain.ErrInternal)
	}
}

func TestValidTournamentAdminAuditQueryRequiresDatabaseSnapshot(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	cursor := &audit.Cursor{
		OccurredAt:   time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
		AuditEventID: uuid.New(), RevisionID: uuid.New(), SnapshotBound: "42:47:",
	}
	query := tournamentadmin.AuditQuery{
		Operator: tournamentadmin.OperatorIdentity{ActorID: uuid.New()},
		Filter: audit.AuditFilter{
			TournamentID: tournamentID, Cursor: cursor,
		},
	}
	if !validTournamentAdminAuditQuery(query) {
		t.Fatal("complete audit query must be valid")
	}
	query.Filter.Cursor.SnapshotBound = ""
	if validTournamentAdminAuditQuery(query) {
		t.Fatal("cursor without a database snapshot must be rejected")
	}
	query.Filter.Cursor.SnapshotBound = "42:47:" + string(bytes.Repeat([]byte("1"), 4096))
	if validTournamentAdminAuditQuery(query) {
		t.Fatal("oversized database snapshot must be rejected")
	}
}

func TestRedactTournamentAuditPayloadRejectsUnknownOnlyPayload(t *testing.T) {
	t.Parallel()

	payload, err := redactTournamentAuditPayload(json.RawMessage(`{"session_token":"must-not-leak"}`))
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("redactTournamentAuditPayload() error = %v, want %v", err, domain.ErrValidation)
	}
	if payload != nil {
		t.Fatalf("redactTournamentAuditPayload() = %s, want nil", payload)
	}
}

func TestCollectTournamentIncidentEventsFailsClosedBeyondBundleLimit(t *testing.T) {
	t.Parallel()

	remaining := maxTournamentIncidentEvents + 1
	_, err := collectTournamentIncidentEvents(func(pageSize int, _ *audit.Cursor) (audit.AuditPage, error) {
		count := min(pageSize, remaining)
		remaining -= count
		page := audit.AuditPage{Events: make([]audit.AuditEvent, count)}
		if remaining > 0 {
			page.NextCursor = &audit.Cursor{}
		}
		return page, nil
	})
	require.ErrorIs(t, err, domain.ErrInternal)
}

func TestCollectTournamentIncidentEventsTraversesAllPages(t *testing.T) {
	t.Parallel()

	next := &audit.Cursor{AuditEventID: uuid.New()}
	calls := 0
	events, err := collectTournamentIncidentEvents(func(pageSize int, cursor *audit.Cursor) (audit.AuditPage, error) {
		calls++
		switch calls {
		case 1:
			require.Equal(t, maxAuditPageSize, pageSize)
			require.Nil(t, cursor)
			return audit.AuditPage{
				Events:     make([]audit.AuditEvent, maxAuditPageSize),
				NextCursor: next,
			}, nil
		case 2:
			require.Equal(t, maxAuditPageSize, pageSize)
			require.Same(t, next, cursor)
			return audit.AuditPage{Events: make([]audit.AuditEvent, 2)}, nil
		default:
			t.Fatalf("unexpected page call %d", calls)
			return audit.AuditPage{}, nil
		}
	})
	require.NoError(t, err)
	require.Len(t, events, maxAuditPageSize+2)
	require.Equal(t, 2, calls)
}

func tournamentAuditRecordFixture(tournamentID uuid.UUID) AuditRecord {
	occurredAt := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	return AuditRecord{
		AuditEventID: uuid.New(), TournamentID: tournamentID, RosterID: uuid.New(),
		SeriesID: uuid.New(), ResultEventID: uuid.New(), ActorKind: string(domain.ResultActorServer),
		EventType: "tournament.result.settled", RedactedPayload: json.RawMessage(`{"state":"completed"}`),
		OccurredAt: occurredAt, CreatedAt: occurredAt, ResultState: "completed",
		ResultReason: "winner", OfficialResultRevisionID: uuid.New(),
		EntityKind: string(audit.AuditEntityGameAttempt), EntityID: uuid.New(),
		RevisionNumber: 1, IsCurrent: true,
	}
}
