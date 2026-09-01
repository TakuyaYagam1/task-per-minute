package arena_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestIncidentBundleIsCanonicalRedactedAndDeterministic(t *testing.T) {
	t.Parallel()

	tournamentID := task057ID(400)
	generatedAt := time.Date(2026, time.September, 1, 15, 0, 0, 123, time.UTC)
	newer := task057AuditEvent(402, tournamentID, generatedAt.Add(-time.Minute))
	older := task057AuditEvent(401, tournamentID, generatedAt.Add(-2*time.Minute))
	newer.RedactedPayload = json.RawMessage(`{
		"state":"superseded",
		"entity_id":"05700000-0000-4000-8000-000000004402",
		"flag":"raw-flag-must-not-leak",
		"credentials":"must-not-leak",
		"session_token":"must-not-leak"
	}`)
	snapshot := arena.IncidentBundleSnapshot{
		TournamentID: tournamentID, ProjectionRevision: 17, GeneratedAt: generatedAt,
		Events: []arena.AuditEvent{older, newer},
	}

	first, err := arena.GenerateIncidentBundle(snapshot)
	require.NoError(t, err)
	second, err := arena.GenerateIncidentBundle(snapshot)
	require.NoError(t, err)
	require.Equal(t, first, second)
	require.True(t, json.Valid(first.CanonicalContent))
	require.False(t, bytes.Contains(first.CanonicalContent, []byte("raw-flag")))
	require.False(t, bytes.Contains(first.CanonicalContent, []byte("credentials")))
	require.False(t, bytes.Contains(first.CanonicalContent, []byte("session_token")))
	require.Less(t, bytes.Index(first.CanonicalContent, []byte(newer.AuditEventID.String())), bytes.Index(first.CanonicalContent, []byte(older.AuditEventID.String())))
	require.Equal(t, sha256.Sum256(first.CanonicalContent), first.SHA256)
	require.NoError(t, arena.VerifyIncidentBundle(first))

	permuted := snapshot
	permuted.Events = []arena.AuditEvent{newer, older}
	third, err := arena.GenerateIncidentBundle(permuted)
	require.NoError(t, err)
	require.Equal(t, first, third)

	semantic := snapshot
	semantic.Events = []arena.AuditEvent{older, newer}
	semantic.Events[1].RedactedPayload = json.RawMessage(`{
		"session_token":"discarded",
		"entity_id":"05700000-0000-4000-8000-000000004402",
		"state":"superseded"
	}`)
	fourth, err := arena.GenerateIncidentBundle(semantic)
	require.NoError(t, err)
	require.Equal(t, first, fourth)

	first.CanonicalContent[0] = '['
	require.Equal(t, second.SHA256, sha256.Sum256(second.CanonicalContent))
	require.NoError(t, arena.VerifyIncidentBundle(second))
}

func TestIncidentBundleVerifiesStoredHashAndTimestamp(t *testing.T) {
	t.Parallel()

	tournamentID := task057ID(500)
	generatedAt := time.Date(2026, time.September, 1, 16, 0, 0, 0, time.UTC)
	bundle, err := arena.GenerateIncidentBundle(arena.IncidentBundleSnapshot{
		TournamentID: tournamentID, ProjectionRevision: 19, GeneratedAt: generatedAt,
		Events: []arena.AuditEvent{task057AuditEvent(501, tournamentID, generatedAt.Add(-time.Second))},
	})
	require.NoError(t, err)
	require.NoError(t, arena.VerifyIncidentBundle(bundle))

	tamperedContent := bundle
	tamperedContent.CanonicalContent = append([]byte(nil), bundle.CanonicalContent...)
	tamperedContent.CanonicalContent[len(tamperedContent.CanonicalContent)-1] ^= 1
	require.ErrorIs(t, arena.VerifyIncidentBundle(tamperedContent), arena.ErrInvalidIncidentBundle)

	tamperedHash := bundle
	tamperedHash.SHA256[0] ^= 1
	require.ErrorIs(t, arena.VerifyIncidentBundle(tamperedHash), arena.ErrInvalidIncidentBundle)

	tamperedTimestamp := bundle
	tamperedTimestamp.GeneratedAt = generatedAt.Add(time.Second)
	require.ErrorIs(t, arena.VerifyIncidentBundle(tamperedTimestamp), arena.ErrInvalidIncidentBundle)
}

func TestIncidentBundleRejectsInvalidSnapshot(t *testing.T) {
	t.Parallel()

	tournamentID := task057ID(600)
	generatedAt := time.Date(2026, time.September, 1, 17, 0, 0, 0, time.UTC)
	event := task057AuditEvent(601, tournamentID, generatedAt.Add(-time.Second))

	for _, snapshot := range []arena.IncidentBundleSnapshot{
		{TournamentID: tournamentID, ProjectionRevision: 0, GeneratedAt: generatedAt, Events: []arena.AuditEvent{event}},
		{TournamentID: tournamentID, ProjectionRevision: 1, GeneratedAt: time.Time{}, Events: []arena.AuditEvent{event}},
		{TournamentID: tournamentID, ProjectionRevision: 1, GeneratedAt: generatedAt},
		{TournamentID: task057ID(999), ProjectionRevision: 1, GeneratedAt: generatedAt, Events: []arena.AuditEvent{event}},
	} {
		_, err := arena.GenerateIncidentBundle(snapshot)
		require.ErrorIs(t, err, arena.ErrInvalidIncidentBundle)
	}
}
