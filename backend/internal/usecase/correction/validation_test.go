package correction_test

import (
	"crypto/sha256"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	correctionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction"
)

func TestCorrectionValidation(t *testing.T) {
	t.Run("accepts an exact confirmed terminal correction", func(t *testing.T) {
		t.Parallel()

		command, authority := task056CorrectionFixture(t)
		validation, err := correctionusecase.Validate(command, authority)
		require.NoError(t, err)
		require.NoError(t, validation.Validate())
		require.Equal(t, command.Expected.TargetProjection.ID(), validation.TargetRevision().ID())
		require.Len(t, validation.Descendants(), 7)
		require.Len(t, validation.UnlockIntents(), 1)

		returned := validation.UnlockIntents()
		returned[0] = correctionusecase.UnlockIntent{}
		require.Len(t, validation.UnlockIntents(), 1)
		require.NotEqual(t, correctionusecase.UnlockIntent{}, validation.UnlockIntents()[0])
	})

	t.Run("accepts the exact assignment-plan owner for an affected reservation", func(t *testing.T) {
		t.Parallel()

		command, authority := task056CorrectionFixture(t)
		authority.Reservations[0].OwnerID = correctionDAGTestID(42_003)
		command.UnlockIntents[0] = correctionusecase.NewUnlockIntent(authority.Reservations[0])
		expected, err := correctionusecase.NewExpectation(authority, command.Expected.TargetProjection.ID())
		require.NoError(t, err)
		command.Expected = expected

		validation, err := correctionusecase.Validate(command, authority)
		require.NoError(t, err)
		require.NoError(t, validation.Validate())
		require.Equal(t, authority.Reservations[0].OwnerID, validation.UnlockIntents()[0].OwnerID)
	})

	t.Run("rejects incomplete malformed stale aliased and incompatible commands stably", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			code   correctionusecase.RejectionCode
			mutate func(*testing.T, *correctionusecase.Command, *correctionusecase.Authority)
		}{
			{
				name: "confirmation", code: correctionusecase.RejectionIncomplete,
				mutate: func(_ *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					command.Confirmed = false
				},
			},
			{
				name: "empty rationale", code: correctionusecase.RejectionMalformed,
				mutate: func(_ *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					command.Explanation = "  "
				},
			},
			{
				name: "control character in rationale", code: correctionusecase.RejectionMalformed,
				mutate: func(_ *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					command.Explanation = "verified\nreferee ruling"
				},
			},
			{
				name: "unknown reason", code: correctionusecase.RejectionMalformed,
				mutate: func(_ *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					command.Reason = correctionusecase.Reason("unknown")
				},
			},
			{
				name: "same projection ID with changed digest", code: correctionusecase.RejectionStale,
				mutate: func(t *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					t.Helper()
					expected := command.Expected.TargetProjection
					changed, err := domain.NewProjectionRevision(
						expected.ID(), expected.TournamentID(), expected.Artifact(), expected.RevisionNo(),
						expected.PreviousRevisionID(), expected.CreatedAt(), []byte("changed digest"),
					)
					require.NoError(t, err)
					command.Expected.TargetProjection = changed.Revision()
				},
			},
			{
				name: "missing projection intent", code: correctionusecase.RejectionIncomplete,
				mutate: func(_ *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					command.ProjectionIntents = command.ProjectionIntents[:len(command.ProjectionIntents)-1]
				},
			},
			{
				name: "unrelated projection intent replaces required intent", code: correctionusecase.RejectionIncomplete,
				mutate: func(t *testing.T, command *correctionusecase.Command, authority *correctionusecase.Authority) {
					t.Helper()
					wanted := make(map[domain.DerivedRevisionID]struct{}, len(command.ProjectionIntents))
					for _, intent := range command.ProjectionIntents {
						wanted[intent.ExpectedRevision.ID()] = struct{}{}
					}
					for _, projection := range authority.DAG.Snapshot().Projections {
						if _, exists := wanted[projection.Revision().ID()]; exists {
							continue
						}
						index := len(command.ProjectionIntents) - 1
						command.ProjectionIntents[index] = correctionusecase.NewProjectionIntent(
							projection.Revision(), command.ProjectionIntents[index].NextRevisionID,
							command.ProjectionIntents[index].DecisionID,
							command.ProjectionIntents[index].Payload,
						)
						return
					}
					t.Fatal("fixture has no unrelated projection")
				},
			},
			{
				name: "duplicate projection intent", code: correctionusecase.RejectionIdentityAlias,
				mutate: func(_ *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					command.ProjectionIntents = append(command.ProjectionIntents, command.ProjectionIntents[0].Clone())
				},
			},
			{
				name: "projection intent expected metadata splice", code: correctionusecase.RejectionIncomplete,
				mutate: func(t *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					t.Helper()
					intent := command.ProjectionIntents[0]
					expected := intent.ExpectedRevision
					projection, err := domain.NewProjectionRevision(
						expected.ID(), expected.TournamentID(), expected.Artifact(), expected.RevisionNo(),
						expected.PreviousRevisionID(), expected.CreatedAt().Add(time.Nanosecond),
						[]byte("spliced expected payload"),
					)
					require.NoError(t, err)
					intent.ExpectedRevision = projection.Revision()
					command.ProjectionIntents[0] = intent
				},
			},
			{
				name: "missing unlock", code: correctionusecase.RejectionIncomplete,
				mutate: func(_ *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					command.UnlockIntents = nil
				},
			},
			{
				name: "extra disclosed unlock", code: correctionusecase.RejectionIncomplete,
				mutate: func(_ *testing.T, command *correctionusecase.Command, authority *correctionusecase.Authority) {
					command.UnlockIntents = append(
						command.UnlockIntents,
						correctionusecase.NewUnlockIntent(authority.Reservations[1]),
					)
				},
			},
			{
				name: "duplicate unlock", code: correctionusecase.RejectionIdentityAlias,
				mutate: func(_ *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					command.UnlockIntents = append(command.UnlockIntents, command.UnlockIntents[0])
				},
			},
			{
				name: "reservation became used", code: correctionusecase.RejectionStale,
				mutate: func(_ *testing.T, _ *correctionusecase.Command, authority *correctionusecase.Authority) {
					authority.Reservations[0].Used = true
				},
			},
			{
				name: "reservation became disclosed", code: correctionusecase.RejectionStale,
				mutate: func(_ *testing.T, _ *correctionusecase.Command, authority *correctionusecase.Authority) {
					authority.Reservations[0].Disclosed = true
				},
			},
			{
				name: "cutoff expectation digest splice", code: correctionusecase.RejectionStale,
				mutate: func(_ *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					command.Expected.CutoffEventDigest = sha256.Sum256([]byte("spliced cutoff"))
				},
			},
			{
				name: "tournament revision expectation splice", code: correctionusecase.RejectionStale,
				mutate: func(_ *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					command.Expected.TournamentRevision++
				},
			},
			{
				name: "zero expected tournament revision", code: correctionusecase.RejectionMalformed,
				mutate: func(_ *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					command.Expected.TournamentRevision = 0
				},
			},
			{
				name: "negative expected tournament revision", code: correctionusecase.RejectionMalformed,
				mutate: func(_ *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					command.Expected.TournamentRevision = -1
				},
			},
			{
				name: "tournament authority revision advanced", code: correctionusecase.RejectionStale,
				mutate: func(_ *testing.T, _ *correctionusecase.Command, authority *correctionusecase.Authority) {
					authority.TournamentRevision += 2
				},
			},
			{
				name: "zero tournament authority revision", code: correctionusecase.RejectionMalformed,
				mutate: func(_ *testing.T, _ *correctionusecase.Command, authority *correctionusecase.Authority) {
					authority.TournamentRevision = 0
				},
			},
			{
				name: "negative tournament authority revision", code: correctionusecase.RejectionMalformed,
				mutate: func(_ *testing.T, _ *correctionusecase.Command, authority *correctionusecase.Authority) {
					authority.TournamentRevision = -1
				},
			},
			{
				name: "terminal to nonterminal", code: correctionusecase.RejectionTerminal,
				mutate: func(_ *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					command.Patch.State = domain.GameStateActive
				},
			},
			{
				name: "unknown correction field", code: correctionusecase.RejectionMalformed,
				mutate: func(_ *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					command.Fields = append(command.Fields, correctionusecase.Field("state"))
				},
			},
			{
				name: "identity alias", code: correctionusecase.RejectionIdentityAlias,
				mutate: func(_ *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					command.NextResultRevisionID = domain.OfficialResultRevisionID(command.CommandID)
				},
			},
			{
				name: "cross tournament", code: correctionusecase.RejectionCrossTournament,
				mutate: func(_ *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					command.TournamentID = correctionDAGTestID(42_000)
				},
			},
			{
				name: "cross tournament precedes oversized explanation", code: correctionusecase.RejectionCrossTournament,
				mutate: func(_ *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					command.TournamentID = correctionDAGTestID(42_099)
					command.Explanation = strings.Repeat("x", 513)
				},
			},
			{
				name: "foreign Series identity", code: correctionusecase.RejectionCrossTournament,
				mutate: func(_ *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					command.SeriesID = correctionDAGTestID(42_100)
				},
			},
			{
				name: "sibling Game identity", code: correctionusecase.RejectionCrossTournament,
				mutate: func(_ *testing.T, command *correctionusecase.Command, authority *correctionusecase.Authority) {
					command.GameID = authority.Score.Attempts[1].GameID
				},
			},
			{
				name: "foreign readiness owner", code: correctionusecase.RejectionCrossTournament,
				mutate: func(_ *testing.T, _ *correctionusecase.Command, authority *correctionusecase.Authority) {
					authority.Readiness.OwnerID = correctionDAGTestID(42_001)
				},
			},
			{
				name: "foreign readiness participant", code: correctionusecase.RejectionCrossTournament,
				mutate: func(_ *testing.T, _ *correctionusecase.Command, authority *correctionusecase.Authority) {
					authority.Readiness.ParticipantIDs[1] = correctionDAGTestID(42_002)
				},
			},
			{
				name: "same ID forged score head", code: correctionusecase.RejectionStale,
				mutate: func(_ *testing.T, _ *correctionusecase.Command, authority *correctionusecase.Authority) {
					authority.Score.CommandID = correctionDAGTestID(42_004)
				},
			},
			{
				name: "same ID forged Series result head", code: correctionusecase.RejectionStale,
				mutate: func(_ *testing.T, _ *correctionusecase.Command, authority *correctionusecase.Authority) {
					authority.SeriesResult.CommandID = correctionDAGTestID(42_005)
				},
			},
			{
				name: "current solve changed under the same result head", code: correctionusecase.RejectionStale,
				mutate: func(_ *testing.T, _ *correctionusecase.Command, authority *correctionusecase.Authority) {
					authority.CurrentSolve.EvidenceDigest = sha256.Sum256([]byte("changed solve evidence"))
				},
			},
			{
				name: "reservation revision overflow", code: correctionusecase.RejectionMalformed,
				mutate: func(_ *testing.T, command *correctionusecase.Command, authority *correctionusecase.Authority) {
					authority.Reservations[0].Revision = math.MaxInt64
					command.UnlockIntents[0] = correctionusecase.NewUnlockIntent(authority.Reservations[0])
				},
			},
			{
				name: "correction predates current Series result", code: correctionusecase.RejectionStale,
				mutate: func(_ *testing.T, command *correctionusecase.Command, authority *correctionusecase.Authority) {
					command.RequestedAt = authority.SeriesResult.RecordedAt.Add(-time.Nanosecond)
				},
			},
			{
				name: "solve submission aliases command", code: correctionusecase.RejectionIdentityAlias,
				mutate: func(_ *testing.T, command *correctionusecase.Command, authority *correctionusecase.Authority) {
					solvedAt := command.RequestedAt.Add(-time.Second)
					command.Patch.Reason = domain.GameResultReasonSolved
					command.Patch.SolveMetadata = correctionusecase.SolveMetadata{
						SolvedAt: &solvedAt, SubmissionID: &command.CommandID,
						EvidenceDigest: sha256.Sum256([]byte("replacement solve")),
					}
					command.Fields = []correctionusecase.Field{correctionusecase.FieldSolveMetadata}
					_ = authority
				},
			},
			{
				name: "UnixNano collision is outside correction time domain", code: correctionusecase.RejectionMalformed,
				mutate: func(t *testing.T, command *correctionusecase.Command, _ *correctionusecase.Authority) {
					t.Helper()
					aliased := command.RequestedAt.Add(time.Duration(math.MaxInt64)).
						Add(time.Duration(math.MaxInt64)).Add(2 * time.Nanosecond)
					require.Equal(t, command.RequestedAt.UnixNano(), aliased.UnixNano())
					require.False(t, command.RequestedAt.Equal(aliased))
					command.RequestedAt = aliased
				},
			},
			{
				name: "current solve predates source projection", code: correctionusecase.RejectionMalformed,
				mutate: func(_ *testing.T, _ *correctionusecase.Command, authority *correctionusecase.Authority) {
					solvedAt := authority.GameResult.SourceProjection.CreatedAt().Add(-time.Nanosecond)
					authority.CurrentSolve.SolvedAt = &solvedAt
				},
			},
			{
				name: "replacement solve predates source projection", code: correctionusecase.RejectionMalformed,
				mutate: func(_ *testing.T, command *correctionusecase.Command, authority *correctionusecase.Authority) {
					solvedAt := authority.GameResult.SourceProjection.CreatedAt().Add(-time.Nanosecond)
					submissionID := correctionDAGTestID(42_101)
					command.Patch.Reason = domain.GameResultReasonSolved
					command.Patch.SolveMetadata = correctionusecase.SolveMetadata{
						SolvedAt: &solvedAt, SubmissionID: &submissionID,
						EvidenceDigest: sha256.Sum256([]byte("replacement solve")),
					}
					command.Fields = []correctionusecase.Field{correctionusecase.FieldSolveMetadata}
				},
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				command, authority := task056CorrectionFixture(t)
				test.mutate(t, &command, &authority)
				beforeCommand := task056CloneCommand(command)
				beforeAuthority := task056CloneAuthority(authority)

				validation, err := correctionusecase.Validate(command, authority)
				require.ErrorIs(t, err, correctionusecase.ErrInvalid)
				require.Equal(t, test.code, correctionusecase.Code(err))
				require.Equal(t, correctionusecase.Validation{}, validation)
				require.Equal(t, beforeCommand, command)
				require.Equal(t, beforeAuthority.DAG.Snapshot(), authority.DAG.Snapshot())
				require.Equal(t, beforeAuthority.Reservations, authority.Reservations)
				require.Equal(t, beforeAuthority.Decisions, authority.Decisions)
			})
		}
	})
}
