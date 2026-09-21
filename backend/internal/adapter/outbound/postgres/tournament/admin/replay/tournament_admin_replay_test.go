package replay

import (
	"errors"
	"testing"

	tournamentadminreplay "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/replay"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/taskexec"
)

func TestTournamentAdminReplayPostgresImplementsWorkflowRepository(t *testing.T) {
	t.Parallel()

	var repository tournamentadminreplay.ReplayWorkflowRepository = NewTournamentAdminReplayPostgres(nil)
	require.NotNil(t, repository)
}

func TestReplayReplacementLookupState(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		err       error
		wantFound bool
		wantErr   bool
	}{
		"existing command":   {wantFound: true},
		"no current command": {err: pgx.ErrNoRows},
		"query failure":      {err: errors.New("database unavailable"), wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			found, err := replayReplacementLookupState(test.err)

			require.Equal(t, test.wantFound, found)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestCanonicalReserveCandidateDigestBindsSnapshotIdentityAndContent(t *testing.T) {
	t.Parallel()

	taskURL := "https://tasks.example.test/challenge"
	sourceURL := "https://tasks.example.test/source"
	candidate := domain.AssignmentTaskSnapshot{
		SnapshotID: uuid.New(), TaskID: uuid.New(), Version: 3, Kind: domain.AssignmentTaskKindNormal,
		Title: "Reserve candidate", Description: "Solve the reserve task.", Category: domain.CategoryWeb,
		Difficulty: domain.DifficultyEasy, TimeLimit: 60, Flag: "FLAG{reserve}",
		Hints: []string{"first", "second", "third"}, TaskURL: &taskURL, SourceFileURL: &sourceURL,
	}

	want, err := taskexec.SnapshotDigest(candidate)
	require.NoError(t, err)
	got, err := canonicalReserveCandidateDigest(candidate)
	require.NoError(t, err)
	require.Equal(t, want, got)

	changed := candidate
	changed.SnapshotID = uuid.New()
	changedDigest, err := canonicalReserveCandidateDigest(changed)
	require.NoError(t, err)
	require.NotEqual(t, got, changedDigest)

	changed = candidate
	changed.Description = "tampered reserve task"
	changedDigest, err = canonicalReserveCandidateDigest(changed)
	require.NoError(t, err)
	require.NotEqual(t, got, changedDigest)

	changed = candidate
	changed.Title = ""
	_, err = canonicalReserveCandidateDigest(changed)
	require.Error(t, err)
}
