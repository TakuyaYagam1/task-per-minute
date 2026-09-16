package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func TestGoldenRuntimeCommandIdentityBindsScopeAndPayload(t *testing.T) {
	t.Parallel()

	spec := goldenRuntimeCommandSpec{
		CommandID: uuid.New(), TournamentID: uuid.New(), RosterID: uuid.New(),
		ActorKind: "participant", ActorID: uuid.New(), Scope: "participant", Kind: "submit",
		AttemptID: uuid.New(), ParticipantID: uuid.New(), ExpectedRuntimeRevision: 7,
		ExpectedReadyWindowID: uuid.New(), Payload: struct {
			SubmittedFlag string `json:"submitted_flag"`
		}{SubmittedFlag: "TPM{correct}"},
	}
	digest := spec.digest()
	row := sqlc.GoldenRuntimeCommand{
		CommandID: spec.CommandID, TournamentID: spec.TournamentID, RosterID: spec.RosterID,
		ActorKind: spec.ActorKind, ActorID: goldenRuntimeNullUUID(spec.ActorID),
		CommandScope: spec.Scope, CommandKind: spec.Kind,
		AttemptID: goldenRuntimeNullUUID(spec.AttemptID), ParticipantID: goldenRuntimeNullUUID(spec.ParticipantID),
		ExpectedRuntimeRevision: spec.ExpectedRuntimeRevision,
		ExpectedReadyWindowID:   goldenRuntimeNullUUID(spec.ExpectedReadyWindowID), CommandDigest: digest[:],
	}
	require.True(t, goldenRuntimeCommandMatches(row, spec))

	changedActor := spec
	changedActor.ActorID = uuid.New()
	require.False(t, goldenRuntimeCommandMatches(row, changedActor))
	changedPayload := spec
	changedPayload.Payload = struct {
		SubmittedFlag string `json:"submitted_flag"`
	}{SubmittedFlag: "TPM{other}"}
	require.False(t, goldenRuntimeCommandMatches(row, changedPayload))
}

func TestGoldenRuntimeAuthorityConflictBindsAttemptWindowAndRevision(t *testing.T) {
	t.Parallel()

	expectedAttempt := uuid.New()
	currentAttempt := uuid.New()
	expectedWindow := uuid.New()
	currentWindow := uuid.New()
	err := goldenRuntimeAuthorityConflictForTarget(4, 5, expectedWindow, currentWindow, expectedAttempt, currentAttempt)
	var conflict *usecase.GoldenAuthorityConflictError
	require.ErrorAs(t, err, &conflict)
	require.Equal(t, int64(4), conflict.ExpectedRevision)
	require.Equal(t, int64(5), conflict.CurrentRevision)
	require.Equal(t, expectedAttempt, conflict.ExpectedAttempt)
	require.Equal(t, currentAttempt, conflict.CurrentAttempt)
	require.Equal(t, expectedWindow, conflict.ExpectedWindow)
	require.Equal(t, currentWindow, conflict.CurrentWindow)
}

func TestGoldenRuntimeEvidencePayloadDoesNotContainSecretFlag(t *testing.T) {
	t.Parallel()

	spec := goldenRuntimeCommandSpec{
		CommandID: uuid.New(), TournamentID: uuid.New(), RosterID: uuid.New(),
		ActorKind: "participant", ActorID: uuid.New(), Scope: "participant", Kind: "submit",
		AttemptID: uuid.New(), ParticipantID: uuid.New(), ExpectedRuntimeRevision: 1,
		ExpectedReadyWindowID: uuid.New(), Payload: struct {
			SubmittedFlag string `json:"submitted_flag"`
		}{SubmittedFlag: "TPM{secret}"},
	}
	audit, err := goldenRuntimeAuditPayload(spec, 2)
	require.NoError(t, err)
	event, err := goldenRuntimeEventPayload(spec, 2)
	require.NoError(t, err)
	for _, payload := range [][]byte{audit, event} {
		var object map[string]any
		require.NoError(t, json.Unmarshal(payload, &object))
		encoded, marshalErr := json.Marshal(object)
		require.NoError(t, marshalErr)
		require.NotContains(t, string(encoded), "TPM{secret}")
		require.NotContains(t, string(encoded), "submitted_flag")
	}
}

func TestGoldenRuntimeEventPayloadCarriesConsecutiveProjectionFence(t *testing.T) {
	t.Parallel()

	spec := goldenRuntimeCommandSpec{
		CommandID: uuid.New(), TournamentID: uuid.New(), RosterID: uuid.New(),
		ActorKind: "server", Scope: "recovery", Kind: "completion",
		AttemptID: uuid.New(), ExpectedRuntimeRevision: 1, ExpectedReadyWindowID: uuid.New(),
	}
	firstPayload, err := goldenRuntimeEventPayload(spec, 1)
	require.NoError(t, err)
	secondPayload, err := goldenRuntimeEventPayload(spec, 2)
	require.NoError(t, err)
	var first, second struct {
		Schema          string    `json:"schema"`
		CommandID       uuid.UUID `json:"command_id"`
		CommandKind     string    `json:"command_kind"`
		RuntimeRevision int64     `json:"runtime_revision"`
		Source          string    `json:"source"`
	}
	require.NoError(t, json.Unmarshal(firstPayload, &first))
	require.NoError(t, json.Unmarshal(secondPayload, &second))
	require.Equal(t, "golden-runtime-event-v1", first.Schema)
	require.Equal(t, first.Schema, second.Schema)
	require.Equal(t, spec.CommandID, first.CommandID)
	require.Equal(t, first.CommandID, second.CommandID)
	require.Equal(t, spec.Kind, first.CommandKind)
	require.Equal(t, first.CommandKind, second.CommandKind)
	require.Equal(t, "golden-runtime", first.Source)
	require.Equal(t, first.Source, second.Source)
	require.Equal(t, int64(1), first.RuntimeRevision)
	require.Equal(t, first.RuntimeRevision+1, second.RuntimeRevision)
}

func TestGoldenRuntimeRecoveryCommandIdentityIsPersistedBoundaryDerived(t *testing.T) {
	t.Parallel()

	attempt := sqlc.ListGoldenRuntimeRecoveryAttemptsRow{
		AttemptID: uuid.New(), TournamentID: uuid.New(), RosterID: uuid.New(),
		GroupRevisionID: uuid.New(), EdgePosition: 2, ReadyWindowID: uuid.New(), State: "active",
		ReadyWindowDeadline: goldenRuntimeTestTimestamp(time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)),
		StartedAt:           goldenRuntimeTestTimestamp(time.Date(2026, time.September, 11, 12, 1, 0, 0, time.UTC)),
		Deadline:            goldenRuntimeTestTimestamp(time.Date(2026, time.September, 11, 12, 4, 0, 0, time.UTC)),
	}

	first := goldenRuntimeRecoverySpec(attempt, "reserve_creation", 4)
	restarted := goldenRuntimeRecoverySpec(attempt, "reserve_creation", 9)
	require.Equal(t, first.CommandID, restarted.CommandID)
	require.Equal(t, first.Payload, restarted.Payload)
	require.NotEqual(t, first.ExpectedRuntimeRevision, restarted.ExpectedRuntimeRevision)

	changedBoundary := goldenRuntimeRecoverySpec(attempt, "completion", 4)
	require.NotEqual(t, first.CommandID, changedBoundary.CommandID)
	changedWindow := attempt
	changedWindow.ReadyWindowID = uuid.New()
	require.NotEqual(t, first.CommandID, goldenRuntimeRecoveryCommandID(changedWindow, "reserve_creation"))
}

func TestGoldenRuntimeMigrationBackfillAndRollbackContracts(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "..", "..", "db", "migrations")
	migration, err := os.ReadFile(filepath.Join(root, "000015_golden_runtime_fencing.sql"))
	require.NoError(t, err)
	canonical, err := os.ReadFile(filepath.Join(root, "000011_projection_schema.sql"))
	require.NoError(t, err)

	migrationText := string(migration)
	require.Contains(t, migrationText, "WITH runtime_groups AS (")
	require.Contains(t, migrationText, "runtime.group_revision_id")
	require.Contains(t, migrationText, "AND group_revision.roster_id = runtime_group.roster_id")
	require.Contains(t, migrationText, "SELECT runtime_group.tournament_id,\n    (array_agg(group_scope.roster_id")
	require.Contains(t, migrationText, "GROUP BY runtime_group.tournament_id;")
	require.NotContains(t, migrationText, "SELECT group_scope.tournament_id,\n    (array_agg(group_scope.roster_id")
	require.Contains(t, migrationText, "cannot backfill Golden runtime heads with inconsistent source authorities")
	require.Contains(t, migrationText, "revision,\n    source_projection_revision_id")
	require.Contains(t, migrationText, "\n    1,\n")
	require.NotContains(t, migrationText, "Revision zero marks legacy runtime state")
	require.Contains(t, migrationText, "runtime_revision is the authoritative Golden projection fence")
	require.Contains(t, migrationText, "revision bigint NOT NULL DEFAULT 1")
	require.Contains(t, migrationText, "CHECK (revision >= 1)")
	require.Contains(t, migrationText, "CONSTRAINT outbox_golden_runtime_sources_revision_key UNIQUE (tournament_id, runtime_revision)")
	require.Contains(t, migrationText, "projection_ordinal")
	require.Contains(t, migrationText, "cannot remove Golden runtime fencing while durable runtime evidence exists")

	downIndex := strings.Index(migrationText, "-- +goose Down")
	require.GreaterOrEqual(t, downIndex, 0)
	down := migrationText[downIndex:]
	for _, name := range []string{"validate_outbox_event_source", "validate_outbox_source_membership"} {
		require.Equal(t, sqlFunctionBody(string(canonical), name), sqlFunctionBody(down, name), name)
	}
	eventBody := sqlFunctionBody(down, "validate_outbox_event_source")
	membershipBody := sqlFunctionBody(down, "validate_outbox_source_membership")
	require.NotContains(t, eventBody, "golden_runtime")
	require.NotContains(t, membershipBody, "golden_runtime")
}

func goldenRuntimeTestTimestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func sqlFunctionBody(source, name string) string {
	start := strings.Index(source, "CREATE FUNCTION public."+name)
	if start < 0 {
		start = strings.Index(source, "CREATE OR REPLACE FUNCTION public."+name)
	}
	if start < 0 {
		return ""
	}
	bodyStart := strings.Index(source[start:], "LANGUAGE plpgsql")
	if bodyStart < 0 {
		return ""
	}
	bodyStart += start
	bodyEnd := strings.Index(source[bodyStart:], "$$;")
	if bodyEnd < 0 {
		return ""
	}
	bodyEnd += bodyStart + len("$$;")
	return source[bodyStart:bodyEnd]
}
