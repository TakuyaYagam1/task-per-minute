package inbound

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
	tournamentpreflight "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/preflight"
)

func TestInboundMappingsCopyLeafResults(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)
	actorID := inboundMappingID(1)
	winnerID := inboundMappingID(2)
	expectedActorID := actorID
	expectedWinnerID := winnerID
	leafReport := tournamentpreflight.ReportRevision{
		ID: inboundMappingID(3), TournamentID: inboundMappingID(4), EvaluatedAt: now,
		NormalizedInputs: []string{"a"}, Revisions: []tournamentpreflight.SourceRevision{{Source: "roster", Value: "1"}},
		Checks: []tournamentpreflight.Check{{Code: tournamentpreflight.CodeRosterComplete, Passed: true, Explanation: "ok", Evidence: []string{"proof"}}},
	}
	leafPage := audit.AuditPage{Events: []audit.AuditEvent{{
		AuditEventID: inboundMappingID(5), TournamentID: inboundMappingID(4), RosterID: inboundMappingID(6),
		SeriesID: inboundMappingID(7), ResultEventID: inboundMappingID(8), ActorKind: domain.ResultActorOperator,
		ActorID: &actorID, EventType: "result", RedactedPayload: []byte(`{"state":"completed"}`),
		OccurredAt: now, CreatedAt: now, ResultState: "completed", ResultReason: "solved", WinnerID: &winnerID,
		OfficialResultRevisionID: inboundMappingID(9), EntityKind: audit.AuditEntitySeries, EntityID: inboundMappingID(10),
		RevisionNumber: 1, IsCurrent: true,
	}}, NextCursor: &audit.Cursor{OccurredAt: now, AuditEventID: inboundMappingID(5), RevisionID: inboundMappingID(9)}}
	content := []byte("canonical")
	leafBundle := audit.IncidentBundle{TournamentID: inboundMappingID(4), ProjectionRevision: 1, GeneratedAt: now,
		CanonicalContent: content, SHA256: sha256.Sum256(content), Algorithm: "hmac-sha256-v1", KeyID: "main", MAC: sha256.Sum256([]byte("mac"))}

	report := mapPreflightReport(leafReport)
	page := mapAuditPage(leafPage)
	bundle := mapIncidentBundle(leafBundle)
	leafReport.NormalizedInputs[0] = "changed"
	leafReport.Revisions[0].Source = "changed"
	leafReport.Checks[0].Evidence[0] = "changed"
	leafPage.Events[0].RedactedPayload[0] = '['
	*leafPage.Events[0].ActorID = uuid.Nil
	*leafPage.Events[0].WinnerID = uuid.Nil
	leafBundle.CanonicalContent[0] = 'x'

	require.Equal(t, "a", report.NormalizedInputs[0])
	require.Equal(t, "roster", report.Revisions[0].Source)
	require.Equal(t, "proof", report.Checks[0].Evidence[0])
	require.Equal(t, byte('{'), page.Events[0].RedactedPayload[0])
	require.Equal(t, expectedActorID, *page.Events[0].ActorID)
	require.Equal(t, expectedWinnerID, *page.Events[0].WinnerID)
	require.Equal(t, byte('c'), bundle.CanonicalContent[0])
}

func inboundMappingID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("71000000-0000-4000-8000-%012d", value))
}

func TestInboundMappingsPreserveNil(t *testing.T) {
	t.Parallel()
	report := mapPreflightReport(tournamentpreflight.ReportRevision{})
	page := mapAuditPage(audit.AuditPage{})
	bundle := mapIncidentBundle(audit.IncidentBundle{})

	require.Nil(t, report.NormalizedInputs)
	require.Nil(t, report.Revisions)
	require.Nil(t, report.Checks)
	require.Nil(t, page.Events)
	require.Nil(t, page.NextCursor)
	require.Nil(t, bundle.CanonicalContent)
}
