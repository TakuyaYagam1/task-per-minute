package top4

import (
	"crypto/sha256"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
)

func TestGoldenPositionEvidenceRequiresOneCommitSource(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		submissionID uint64
		terminal     bool
		valid        bool
	}{
		{name: "accepted submission", submissionID: 2, valid: true},
		{name: "terminal evidence", terminal: true, valid: true},
		{name: "both sources", submissionID: 2, terminal: true},
		{name: "missing source"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := goldenPositionSourceInput()
			input.Positions[1].SubmissionID = test.submissionID
			if test.terminal {
				input.Positions[1].TerminalEvidenceID = uuid.New()
			}
			evidence, err := NewGoldenPositionEvidence(input)
			if !test.valid {
				require.ErrorIs(t, err, ErrInvalidGoldenPositionEvidence)
				return
			}
			require.NoError(t, err)
			require.Equal(t, input.Positions, evidence.Positions())
			require.Equal(t, int64(1), evidence.Attempts()[0].SubmissionRevision)
			input.Positions[1].TerminalEvidenceID = uuid.Nil
			if test.terminal {
				require.NotEqual(t, uuid.Nil, evidence.Positions()[1].TerminalEvidenceID)
				require.Zero(t, evidence.Positions()[1].SubmissionID)
			}
		})
	}
}

func goldenPositionSourceInput() GoldenPositionEvidenceInput {
	previous, current, attempt := uuid.New(), uuid.New(), uuid.New()
	digest := sha256.Sum256([]byte("Golden position evidence"))
	return GoldenPositionEvidenceInput{
		Scope: goldenstate.GoldenStateScope{
			TournamentID: uuid.New(), GroupID: uuid.New(), GroupRevisionID: domain.DerivedRevisionID(uuid.New()),
		},
		RevisionID: current, Revision: 2, PreviousRevisionID: &previous,
		RevisionIDs: []uuid.UUID{previous, current}, PositionFrom: 3, PositionTo: 4, PayloadDigest: digest,
		Attempts: []GoldenPositionAttemptEvidence{{
			AttemptID: attempt, AttemptNo: 1, SubmissionRevisionID: uuid.New(), SubmissionRevision: 1,
			WaveID: uuid.New(), AssignmentID: uuid.New(), SnapshotID: uuid.New(), TaskID: uuid.New(), OrderCount: 2,
		}},
		Positions: []GoldenPositionCommitEvidence{
			{Position: 3, ParticipantID: uuid.New(), AttemptID: attempt, AttemptNo: 1, SubmissionID: 1, EvidenceDigest: digest, CommitID: uuid.New()},
			{Position: 4, ParticipantID: uuid.New(), AttemptID: attempt, AttemptNo: 1, EvidenceDigest: digest, CommitID: uuid.New()},
		},
	}
}

func TestGoldenPositionEvidenceRejectsMultipleTerminalSources(t *testing.T) {
	t.Parallel()
	input := goldenPositionSourceInput()
	for index := range input.Positions {
		input.Positions[index].SubmissionID = 0
		input.Positions[index].TerminalEvidenceID = uuid.New()
	}
	_, err := NewGoldenPositionEvidence(input)
	require.ErrorIs(t, err, ErrInvalidGoldenPositionEvidence)
}
