package incidentauth

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/audit"
)

func TestHMACAuthenticatorSignsAndVerifiesIncidentBundle(t *testing.T) {
	t.Parallel()

	authenticator, err := NewHMACAuthenticator(HMACConfig{
		KeyID:  "incident-2026-09",
		Secret: []byte(strings.Repeat("a", 32)),
	})
	require.NoError(t, err)

	bundle := incidentBundleFixture(t)
	signed, err := authenticator.Sign(bundle)
	require.NoError(t, err)
	require.Equal(t, "hmac-sha256-v1", signed.Algorithm)
	require.Equal(t, "incident-2026-09", signed.KeyID)
	require.NotEqual(t, [32]byte{}, signed.MAC)
	require.NoError(t, authenticator.Verify(signed))

	for name, mutate := range map[string]func(*audit.IncidentBundle){
		"content": func(value *audit.IncidentBundle) {
			value.CanonicalContent = append([]byte(nil), value.CanonicalContent...)
			value.CanonicalContent[len(value.CanonicalContent)-1] ^= 1
		},
		"checksum":  func(value *audit.IncidentBundle) { value.SHA256[0] ^= 1 },
		"algorithm": func(value *audit.IncidentBundle) { value.Algorithm = "sha256" },
		"key id":    func(value *audit.IncidentBundle) { value.KeyID = "incident-2027-01" },
		"mac":       func(value *audit.IncidentBundle) { value.MAC[0] ^= 1 },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			candidate := signed
			mutate(&candidate)
			require.Error(t, authenticator.Verify(candidate))
		})
	}
}

func TestHMACAuthenticatorRejectsInvalidKeyMaterial(t *testing.T) {
	t.Parallel()

	for name, cfg := range map[string]HMACConfig{
		"empty key id":      {KeyID: "", Secret: []byte(strings.Repeat("a", 32))},
		"malformed key id":  {KeyID: "incident key", Secret: []byte(strings.Repeat("a", 32))},
		"short secret":      {KeyID: "incident-2026-09", Secret: []byte(strings.Repeat("a", 31))},
		"empty secret":      {KeyID: "incident-2026-09"},
		"overlong key id":   {KeyID: strings.Repeat("a", 65), Secret: []byte(strings.Repeat("a", 32))},
		"newline in key id": {KeyID: "incident\n2026", Secret: []byte(strings.Repeat("a", 32))},
		"unicode in key id": {KeyID: "incident-ключ", Secret: []byte(strings.Repeat("a", 32))},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := NewHMACAuthenticator(cfg)
			require.Error(t, err)
		})
	}
}

func TestHMACAuthenticatorPreservesKeyRotationIdentity(t *testing.T) {
	t.Parallel()

	first, err := NewHMACAuthenticator(HMACConfig{
		KeyID: "incident-2026-09", Secret: []byte(strings.Repeat("a", 32)),
	})
	require.NoError(t, err)
	second, err := NewHMACAuthenticator(HMACConfig{
		KeyID: "incident-2026-10", Secret: []byte(strings.Repeat("b", 32)),
	})
	require.NoError(t, err)
	wrongKey, err := NewHMACAuthenticator(HMACConfig{
		KeyID: "incident-2026-09", Secret: []byte(strings.Repeat("b", 32)),
	})
	require.NoError(t, err)

	bundle := incidentBundleFixture(t)
	firstBundle, err := first.Sign(bundle)
	require.NoError(t, err)
	secondBundle, err := second.Sign(bundle)
	require.NoError(t, err)

	require.NotEqual(t, firstBundle.KeyID, secondBundle.KeyID)
	require.NotEqual(t, firstBundle.MAC, secondBundle.MAC)
	require.NoError(t, first.Verify(firstBundle))
	require.NoError(t, second.Verify(secondBundle))
	require.Error(t, first.Verify(secondBundle))
	require.Error(t, second.Verify(firstBundle))
	require.Error(t, wrongKey.Verify(firstBundle))
}

func incidentBundleFixture(t *testing.T) audit.IncidentBundle {
	t.Helper()

	tournamentID := uuid.New()
	generatedAt := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)
	bundle, err := audit.GenerateIncidentBundle(audit.IncidentBundleSnapshot{
		TournamentID:       tournamentID,
		ProjectionRevision: 1,
		GeneratedAt:        generatedAt,
		Events: []audit.AuditEvent{{
			AuditEventID: uuid.New(), TournamentID: tournamentID, RosterID: uuid.New(),
			SeriesID: uuid.New(), ResultEventID: uuid.New(), ActorKind: domain.ResultActorServer,
			EventType: "tournament.result.settled", RedactedPayload: []byte(`{"state":"completed"}`),
			OccurredAt: generatedAt.Add(-time.Second), CreatedAt: generatedAt.Add(-time.Second),
			ResultState: "completed", ResultReason: "winner", OfficialResultRevisionID: uuid.New(),
			EntityKind: audit.AuditEntityGameAttempt, EntityID: uuid.New(), RevisionNumber: 1, IsCurrent: true,
		}},
	})
	require.NoError(t, err)
	return bundle
}
