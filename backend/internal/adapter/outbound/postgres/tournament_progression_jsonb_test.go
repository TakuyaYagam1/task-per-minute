package postgres

import (
	"crypto/sha256"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func TestProgressionSourceArtifactJSONBRoundtrip(t *testing.T) {
	payload := []byte(`{"entries":[1],"version":2}`)
	digest := sha256.Sum256(payload)
	command := tournamentprogression.Command{TournamentID: uuid.New(), RosterID: uuid.New()}
	plan := tournamentprogression.Plan{Record: tournamentprogression.Record{Command: command, Source: tournamentprogression.ProjectionReference{ArtifactID: uuid.New(), Kind: domain.ArtifactKindStandings, Digest: digest, Payload: payload}}}
	for _, test := range []struct {
		name    string
		payload string
		valid   bool
	}{
		{"original", string(payload), true},
		{"jsonb normalization", `{ "version": 2, "entries": [1] }`, true},
		{"changed semantics", `{"version":2,"entries":[2]}`, false},
		{"invalid JSON", `{"entries":[1]`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			artifact := sqlc.ProjectionArtifact{ID: plan.Record.Source.ArtifactID, TournamentID: command.TournamentID, RosterID: command.RosterID, ArtifactKind: string(domain.ArtifactKindStandings), Payload: []byte(test.payload), PayloadDigest: digest[:]}
			require.Equal(t, test.valid, progressionSourceArtifactMatches(plan, artifact))
			artifact.PayloadDigest = make([]byte, sha256.Size)
			require.False(t, progressionSourceArtifactMatches(plan, artifact))
		})
	}
}

func TestProgressionPublishedRecordJSONBRoundtrip(t *testing.T) {
	for _, test := range []struct {
		name    string
		payload string
		valid   bool
	}{
		{"original", `{"entries":[1],"version":2}`, true},
		{"jsonb normalization", `{ "version": 2, "entries": [1] }`, true},
		{"changed semantics", `{"version":2,"entries":[2]}`, false},
		{"invalid JSON", `{"entries":[1]`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan, input, record := progressionPublicationFixture()
			input.Artifacts[0].Payload = []byte(`{"entries":[1],"version":2}`)
			input.Artifacts[0].PayloadDigest = sha256.Sum256(input.Artifacts[0].Payload)
			record.Artifacts[0].Artifact.Payload = []byte(test.payload)
			record.Artifacts[0].Artifact.PayloadDigest = input.Artifacts[0].PayloadDigest[:]
			err := progressionPublishedRecord(plan, input, record)
			if test.valid {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, domain.ErrConflict)
			}
			record.Artifacts[0].Artifact.PayloadDigest = make([]byte, sha256.Size)
			require.ErrorIs(t, progressionPublishedRecord(plan, input, record), domain.ErrConflict)
		})
	}
}

func TestProgressionStandingsRepublicationUsesCanonicalBytes(t *testing.T) {
	payload := []byte(`{"entries":[1],"version":2}`)
	digest := sha256.Sum256(payload)
	command := tournamentprogression.Command{CommandID: uuid.New(), TournamentID: uuid.New(), RosterID: uuid.New()}
	ids, err := tournamentprogression.PlayoffPublicationIdentity(command.CommandID)
	require.NoError(t, err)
	plan := tournamentprogression.Plan{Record: tournamentprogression.Record{Command: command, Source: tournamentprogression.ProjectionReference{ArtifactID: uuid.New(), Kind: domain.ArtifactKindStandings, Digest: digest, Payload: payload}}, PublicationIDs: ids}
	source := sqlc.ProjectionArtifact{ID: plan.Record.Source.ArtifactID, TournamentID: command.TournamentID, RosterID: command.RosterID, ArtifactKind: string(domain.ArtifactKindStandings), Payload: []byte(`{"version": 2, "entries": [1]}`), PayloadDigest: digest[:]}
	artifact, err := progressionStandingsArtifact(plan, source)
	require.NoError(t, err)
	require.JSONEq(t, `{"entries":[1],"version":2}`, string(artifact.Payload))
	require.Equal(t, digest, sha256.Sum256(artifact.Payload))
	require.Equal(t, digest, artifact.PayloadDigest)
	artifact.Payload[0] = ' '
	require.Equal(t, digest, sha256.Sum256(plan.Record.Source.Payload))
}
