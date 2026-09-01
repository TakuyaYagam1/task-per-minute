package arena_test

import (
	"crypto/sha256"
	"fmt"
	"math"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestCorrectionValidation(t *testing.T) {
	t.Run("accepts an exact confirmed terminal correction", func(t *testing.T) {
		t.Parallel()

		command, authority := task056CorrectionFixture(t)
		validation, err := arena.ValidateCorrection(command, authority)
		require.NoError(t, err)
		require.NoError(t, validation.Validate())
		require.Equal(t, command.Expected.TargetProjection.ID(), validation.TargetRevision().ID())
		require.Len(t, validation.Descendants(), 7)
		require.Len(t, validation.UnlockIntents(), 1)

		returned := validation.UnlockIntents()
		returned[0] = arena.CorrectionUnlockIntent{}
		require.Len(t, validation.UnlockIntents(), 1)
		require.NotEqual(t, arena.CorrectionUnlockIntent{}, validation.UnlockIntents()[0])
	})

	t.Run("rejects incomplete malformed stale aliased and incompatible commands stably", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			code   arena.CorrectionRejectionCode
			mutate func(*testing.T, *arena.CorrectionCommand, *arena.CorrectionAuthority)
		}{
			{
				name: "confirmation", code: arena.CorrectionRejectionIncomplete,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					command.Confirmed = false
				},
			},
			{
				name: "empty rationale", code: arena.CorrectionRejectionMalformed,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					command.Explanation = "  "
				},
			},
			{
				name: "control character in rationale", code: arena.CorrectionRejectionMalformed,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					command.Explanation = "verified\nreferee ruling"
				},
			},
			{
				name: "unknown reason", code: arena.CorrectionRejectionMalformed,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					command.Reason = arena.CorrectionReason("unknown")
				},
			},
			{
				name: "same projection ID with changed digest", code: arena.CorrectionRejectionStale,
				mutate: func(t *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					t.Helper()
					expected := command.Expected.TargetProjection
					changed, err := domain.NewArenaProjectionRevision(
						expected.ID(), expected.TournamentID(), expected.Artifact(), expected.RevisionNo(),
						expected.PreviousRevisionID(), expected.CreatedAt(), []byte("changed digest"),
					)
					require.NoError(t, err)
					command.Expected.TargetProjection = changed.Revision()
				},
			},
			{
				name: "missing projection intent", code: arena.CorrectionRejectionIncomplete,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					command.ProjectionIntents = command.ProjectionIntents[:len(command.ProjectionIntents)-1]
				},
			},
			{
				name: "unrelated projection intent replaces required intent", code: arena.CorrectionRejectionIncomplete,
				mutate: func(t *testing.T, command *arena.CorrectionCommand, authority *arena.CorrectionAuthority) {
					t.Helper()
					wanted := make(map[domain.ArenaDerivedRevisionID]struct{}, len(command.ProjectionIntents))
					for _, intent := range command.ProjectionIntents {
						wanted[intent.ExpectedRevision.ID()] = struct{}{}
					}
					for _, projection := range authority.DAG.Snapshot().Projections {
						if _, exists := wanted[projection.Revision().ID()]; exists {
							continue
						}
						index := len(command.ProjectionIntents) - 1
						command.ProjectionIntents[index] = arena.NewCorrectionProjectionIntent(
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
				name: "duplicate projection intent", code: arena.CorrectionRejectionIdentityAlias,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					command.ProjectionIntents = append(command.ProjectionIntents, command.ProjectionIntents[0].Clone())
				},
			},
			{
				name: "projection intent expected metadata splice", code: arena.CorrectionRejectionIncomplete,
				mutate: func(t *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					t.Helper()
					intent := command.ProjectionIntents[0]
					expected := intent.ExpectedRevision
					projection, err := domain.NewArenaProjectionRevision(
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
				name: "missing unlock", code: arena.CorrectionRejectionIncomplete,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					command.UnlockIntents = nil
				},
			},
			{
				name: "extra disclosed unlock", code: arena.CorrectionRejectionIncomplete,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, authority *arena.CorrectionAuthority) {
					command.UnlockIntents = append(
						command.UnlockIntents,
						arena.NewCorrectionUnlockIntent(authority.Reservations[1]),
					)
				},
			},
			{
				name: "duplicate unlock", code: arena.CorrectionRejectionIdentityAlias,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					command.UnlockIntents = append(command.UnlockIntents, command.UnlockIntents[0])
				},
			},
			{
				name: "reservation became used", code: arena.CorrectionRejectionStale,
				mutate: func(_ *testing.T, _ *arena.CorrectionCommand, authority *arena.CorrectionAuthority) {
					authority.Reservations[0].Used = true
				},
			},
			{
				name: "reservation became disclosed", code: arena.CorrectionRejectionStale,
				mutate: func(_ *testing.T, _ *arena.CorrectionCommand, authority *arena.CorrectionAuthority) {
					authority.Reservations[0].Disclosed = true
				},
			},
			{
				name: "cutoff expectation digest splice", code: arena.CorrectionRejectionStale,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					command.Expected.CutoffEventDigest = sha256.Sum256([]byte("spliced cutoff"))
				},
			},
			{
				name: "tournament revision expectation splice", code: arena.CorrectionRejectionStale,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					command.Expected.TournamentRevision++
				},
			},
			{
				name: "zero expected tournament revision", code: arena.CorrectionRejectionMalformed,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					command.Expected.TournamentRevision = 0
				},
			},
			{
				name: "negative expected tournament revision", code: arena.CorrectionRejectionMalformed,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					command.Expected.TournamentRevision = -1
				},
			},
			{
				name: "tournament authority revision advanced", code: arena.CorrectionRejectionStale,
				mutate: func(_ *testing.T, _ *arena.CorrectionCommand, authority *arena.CorrectionAuthority) {
					authority.TournamentRevision += 2
				},
			},
			{
				name: "zero tournament authority revision", code: arena.CorrectionRejectionMalformed,
				mutate: func(_ *testing.T, _ *arena.CorrectionCommand, authority *arena.CorrectionAuthority) {
					authority.TournamentRevision = 0
				},
			},
			{
				name: "negative tournament authority revision", code: arena.CorrectionRejectionMalformed,
				mutate: func(_ *testing.T, _ *arena.CorrectionCommand, authority *arena.CorrectionAuthority) {
					authority.TournamentRevision = -1
				},
			},
			{
				name: "terminal to nonterminal", code: arena.CorrectionRejectionTerminal,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					command.Patch.State = domain.ArenaGameStateActive
				},
			},
			{
				name: "unknown correction field", code: arena.CorrectionRejectionMalformed,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					command.Fields = append(command.Fields, arena.CorrectionField("state"))
				},
			},
			{
				name: "identity alias", code: arena.CorrectionRejectionIdentityAlias,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					command.NextResultRevisionID = domain.ArenaOfficialResultRevisionID(command.CommandID)
				},
			},
			{
				name: "cross tournament", code: arena.CorrectionRejectionCrossTournament,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					command.TournamentID = task055ID(42_000)
				},
			},
			{
				name: "cross tournament precedes oversized explanation", code: arena.CorrectionRejectionCrossTournament,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					command.TournamentID = task055ID(42_099)
					command.Explanation = strings.Repeat("x", 513)
				},
			},
			{
				name: "foreign Series identity", code: arena.CorrectionRejectionCrossTournament,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					command.SeriesID = task055ID(42_100)
				},
			},
			{
				name: "sibling Game identity", code: arena.CorrectionRejectionCrossTournament,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, authority *arena.CorrectionAuthority) {
					command.GameID = authority.Score.Attempts[1].GameID
				},
			},
			{
				name: "foreign readiness owner", code: arena.CorrectionRejectionCrossTournament,
				mutate: func(_ *testing.T, _ *arena.CorrectionCommand, authority *arena.CorrectionAuthority) {
					authority.Readiness.OwnerID = task055ID(42_001)
				},
			},
			{
				name: "foreign readiness participant", code: arena.CorrectionRejectionCrossTournament,
				mutate: func(_ *testing.T, _ *arena.CorrectionCommand, authority *arena.CorrectionAuthority) {
					authority.Readiness.ParticipantIDs[1] = task055ID(42_002)
				},
			},
			{
				name: "foreign reservation owner", code: arena.CorrectionRejectionCrossTournament,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, authority *arena.CorrectionAuthority) {
					authority.Reservations[0].OwnerID = task055ID(42_003)
					command.UnlockIntents[0] = arena.NewCorrectionUnlockIntent(authority.Reservations[0])
				},
			},
			{
				name: "same ID forged score head", code: arena.CorrectionRejectionStale,
				mutate: func(_ *testing.T, _ *arena.CorrectionCommand, authority *arena.CorrectionAuthority) {
					authority.Score.CommandID = task055ID(42_004)
				},
			},
			{
				name: "same ID forged Series result head", code: arena.CorrectionRejectionStale,
				mutate: func(_ *testing.T, _ *arena.CorrectionCommand, authority *arena.CorrectionAuthority) {
					authority.SeriesResult.CommandID = task055ID(42_005)
				},
			},
			{
				name: "current solve changed under the same result head", code: arena.CorrectionRejectionStale,
				mutate: func(_ *testing.T, _ *arena.CorrectionCommand, authority *arena.CorrectionAuthority) {
					authority.CurrentSolve.EvidenceDigest = sha256.Sum256([]byte("changed solve evidence"))
				},
			},
			{
				name: "reservation revision overflow", code: arena.CorrectionRejectionMalformed,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, authority *arena.CorrectionAuthority) {
					authority.Reservations[0].Revision = math.MaxInt64
					command.UnlockIntents[0] = arena.NewCorrectionUnlockIntent(authority.Reservations[0])
				},
			},
			{
				name: "correction predates current Series result", code: arena.CorrectionRejectionStale,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, authority *arena.CorrectionAuthority) {
					command.RequestedAt = authority.SeriesResult.RecordedAt.Add(-time.Nanosecond)
				},
			},
			{
				name: "solve submission aliases command", code: arena.CorrectionRejectionIdentityAlias,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, authority *arena.CorrectionAuthority) {
					solvedAt := command.RequestedAt.Add(-time.Second)
					command.Patch.Reason = domain.ArenaGameResultReasonSolved
					command.Patch.SolveMetadata = arena.CorrectionSolveMetadata{
						SolvedAt: &solvedAt, SubmissionID: &command.CommandID,
						EvidenceDigest: sha256.Sum256([]byte("replacement solve")),
					}
					command.Fields = []arena.CorrectionField{arena.CorrectionFieldSolveMetadata}
					_ = authority
				},
			},
			{
				name: "UnixNano collision is outside correction time domain", code: arena.CorrectionRejectionMalformed,
				mutate: func(t *testing.T, command *arena.CorrectionCommand, _ *arena.CorrectionAuthority) {
					t.Helper()
					aliased := command.RequestedAt.Add(time.Duration(math.MaxInt64)).
						Add(time.Duration(math.MaxInt64)).Add(2 * time.Nanosecond)
					require.Equal(t, command.RequestedAt.UnixNano(), aliased.UnixNano())
					require.False(t, command.RequestedAt.Equal(aliased))
					command.RequestedAt = aliased
				},
			},
			{
				name: "current solve predates source projection", code: arena.CorrectionRejectionMalformed,
				mutate: func(_ *testing.T, _ *arena.CorrectionCommand, authority *arena.CorrectionAuthority) {
					solvedAt := authority.GameResult.SourceProjection.CreatedAt().Add(-time.Nanosecond)
					authority.CurrentSolve.SolvedAt = &solvedAt
				},
			},
			{
				name: "replacement solve predates source projection", code: arena.CorrectionRejectionMalformed,
				mutate: func(_ *testing.T, command *arena.CorrectionCommand, authority *arena.CorrectionAuthority) {
					solvedAt := authority.GameResult.SourceProjection.CreatedAt().Add(-time.Nanosecond)
					submissionID := task055ID(42_101)
					command.Patch.Reason = domain.ArenaGameResultReasonSolved
					command.Patch.SolveMetadata = arena.CorrectionSolveMetadata{
						SolvedAt: &solvedAt, SubmissionID: &submissionID,
						EvidenceDigest: sha256.Sum256([]byte("replacement solve")),
					}
					command.Fields = []arena.CorrectionField{arena.CorrectionFieldSolveMetadata}
				},
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				command, authority := task056CorrectionFixture(t)
				test.mutate(t, &command, &authority)
				beforeCommand := task056CloneCommand(command)
				beforeAuthority := task056CloneAuthority(authority)

				validation, err := arena.ValidateCorrection(command, authority)
				require.ErrorIs(t, err, arena.ErrInvalidCorrection)
				require.Equal(t, test.code, arena.CorrectionCode(err))
				require.Equal(t, arena.CorrectionValidation{}, validation)
				require.Equal(t, beforeCommand, command)
				require.Equal(t, beforeAuthority.DAG.Snapshot(), authority.DAG.Snapshot())
				require.Equal(t, beforeAuthority.Reservations, authority.Reservations)
				require.Equal(t, beforeAuthority.Decisions, authority.Decisions)
			})
		}
	})

	t.Run("rejects oversized nested authority before clone allocation", func(t *testing.T) {
		command, authority := task056CorrectionFixture(t)
		oversized := make([]domain.ArenaGame, 1<<16)
		authority.Series.Slots[0].Attempts = oversized
		authority.Score.Attempts = make([]arena.SeriesScoreAttemptReference, 1<<16)
		authority.Readiness.ParticipantIDs = make([]uuid.UUID, 1<<16)

		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		for range 4 {
			validation, err := arena.ValidateCorrection(command, authority)
			require.ErrorIs(t, err, arena.ErrInvalidCorrection)
			require.Equal(t, arena.CorrectionRejectionMalformed, arena.CorrectionCode(err))
			require.Equal(t, arena.CorrectionValidation{}, validation)
		}
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(8<<20))
	})

	t.Run("rejects max plus one correction intents before clone allocation", func(t *testing.T) {
		command, authority := task056CorrectionFixture(t)
		seed := command.ProjectionIntents[0].ExpectedRevision
		command.ProjectionIntents = make([]arena.CorrectionProjectionIntent, 513)
		for index := range command.ProjectionIntents {
			expected, err := domain.NewArenaProjectionRevision(
				domain.ArenaDerivedRevisionID(task055ID(70_000+index)),
				seed.TournamentID(), seed.Artifact(), seed.RevisionNo(), seed.PreviousRevisionID(),
				seed.CreatedAt(), []byte{byte(index)},
			)
			require.NoError(t, err)
			command.ProjectionIntents[index] = arena.NewCorrectionProjectionIntent(
				expected.Revision(), domain.ArenaDerivedRevisionID(task055ID(71_000+index)),
				task055ID(72_000+index), []byte{byte(index)},
			)
		}
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		validation, err := arena.ValidateCorrection(command, authority)
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		require.ErrorIs(t, err, arena.ErrInvalidCorrection)
		require.Equal(t, arena.CorrectionRejectionMalformed, arena.CorrectionCode(err))
		require.Equal(t, arena.CorrectionValidation{}, validation)
		require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(1<<20))

		command, authority = task056CorrectionFixture(t)
		seed = command.ProjectionIntents[0].ExpectedRevision
		command.ProjectionIntents = make([]arena.CorrectionProjectionIntent, 9)
		for index := range command.ProjectionIntents {
			expected, constructErr := domain.NewArenaProjectionRevision(
				domain.ArenaDerivedRevisionID(task055ID(73_000+index)),
				seed.TournamentID(), seed.Artifact(), seed.RevisionNo(), seed.PreviousRevisionID(),
				seed.CreatedAt(), []byte{byte(index)},
			)
			require.NoError(t, constructErr)
			command.ProjectionIntents[index] = arena.NewCorrectionProjectionIntent(
				expected.Revision(), domain.ArenaDerivedRevisionID(task055ID(74_000+index)),
				task055ID(75_000+index), make([]byte, 60<<10),
			)
		}
		runtime.GC()
		runtime.ReadMemStats(&before)
		validation, err = arena.ValidateCorrection(command, authority)
		runtime.ReadMemStats(&after)
		require.ErrorIs(t, err, arena.ErrInvalidCorrection)
		require.Equal(t, arena.CorrectionRejectionMalformed, arena.CorrectionCode(err))
		require.Equal(t, arena.CorrectionValidation{}, validation)
		require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(1<<20))
	})

	t.Run("bounds correction explanation bytes before content validation", func(t *testing.T) {
		command, authority := task056CorrectionFixture(t)
		command.Explanation = strings.Repeat("x", 512)
		validation, err := arena.ValidateCorrection(command, authority)
		require.NoError(t, err)
		require.NoError(t, validation.Validate())

		command.Explanation += "x"
		validation, err = arena.ValidateCorrection(command, authority)
		require.ErrorIs(t, err, arena.ErrInvalidCorrection)
		require.Equal(t, arena.CorrectionRejectionMalformed, arena.CorrectionCode(err))
		require.Equal(t, arena.CorrectionValidation{}, validation)
	})
}

func task056CorrectionFixture(t *testing.T) (arena.CorrectionCommand, arena.CorrectionAuthority) {
	t.Helper()
	fixture := task055RevisionDAGFixture(t)
	dag, err := arena.BuildRevisionDAG(fixture)
	require.NoError(t, err)
	gameHead := fixture.Results[0].Result.Clone()
	scoreHead := fixture.Results[2].Score.Clone()
	seriesHead := fixture.Results[2].Result.Clone()
	series := task056Series(fixture, gameHead, scoreHead, seriesHead)
	tournamentID := gameHead.Scope.TournamentID
	readiness := arena.CorrectionReadiness{
		TournamentID: tournamentID, WaveID: task055ID(40_001), WindowID: task055ID(40_002),
		RevisionID: task055ID(40_003), Revision: 4, State: arena.CorrectionReadinessOpen,
		OwnerID:        series.ID,
		ParticipantIDs: []uuid.UUID{series.FirstParticipantID, series.SecondParticipantID},
	}
	reservations := []arena.CorrectionReservation{
		{
			ID: task055ID(40_010), TournamentID: tournamentID, OwnerID: series.ID,
			SourceRevisionID: gameHead.SourceProjection.ID(), Revision: 3,
			EvidenceDigest: sha256.Sum256([]byte("unused-undisclosed")),
		},
		{
			ID: task055ID(40_011), TournamentID: tournamentID, OwnerID: series.ID,
			SourceRevisionID: seriesHead.SourceProjection.ID(), Revision: 2, Disclosed: true,
			EvidenceDigest: sha256.Sum256([]byte("disclosed")),
		},
		{
			ID: task055ID(40_012), TournamentID: tournamentID, OwnerID: series.ID,
			SourceRevisionID: scoreHead.SourceProjection.ID(), Revision: 1, Used: true,
			EvidenceDigest: sha256.Sum256([]byte("used")),
		},
		{
			ID: task055ID(40_013), TournamentID: tournamentID, OwnerID: series.ID,
			SourceRevisionID: fixture.Results[1].Result.SourceProjection.ID(), Revision: 1,
			EvidenceDigest: sha256.Sum256([]byte("unrelated")),
		},
	}
	decisions := task056RecordedDecisions(t, fixture)
	solvedAt := gameHead.RecordedAt.Add(-time.Second)
	currentSolve := arena.CorrectionSolveMetadata{
		SolvedAt: &solvedAt, SubmissionID: task055UUIDPointer(task055ID(40_020)),
		EvidenceDigest: sha256.Sum256([]byte("verified solve")),
	}
	authority := arena.CorrectionAuthority{
		TournamentState: domain.ArenaTournamentStatePlayoffs, TournamentRevision: 11,
		DAG: dag, Series: series, GameResult: gameHead, Score: scoreHead, SeriesResult: seriesHead,
		SeriesRevision: 9, AttemptRevision: 7, CurrentSolve: currentSolve,
		Readiness: readiness, Reservations: reservations, Decisions: decisions,
	}
	expected, err := arena.NewCorrectionExpectation(authority, gameHead.SourceProjection.ID())
	require.NoError(t, err)
	cutoff, err := arena.EvaluateCorrectionCutoff(arena.CorrectionCutoffInput{
		DAG: authority.DAG, TournamentID: tournamentID,
		TargetRevisionID: gameHead.SourceProjection.ID(), TournamentState: authority.TournamentState,
	})
	require.NoError(t, err)
	revisions := append([]domain.ArenaDerivedRevision{expected.TargetProjection}, cutoff.Descendants()...)
	projectionIntents := make([]arena.CorrectionProjectionIntent, len(revisions))
	for index, revision := range revisions {
		projectionIntents[index] = arena.NewCorrectionProjectionIntent(
			revision, domain.ArenaDerivedRevisionID(task055ID(40_100+index)),
			task055ID(40_200+index), []byte(fmt.Sprintf("correction-%02d", index)),
		)
	}
	command := arena.CorrectionCommand{
		TournamentID: tournamentID, SeriesID: series.ID, GameID: gameHead.Scope.GameID,
		CommandID: task055ID(40_300), CascadeCommandID: task055ID(40_301),
		OperatorID: task055ID(40_302), Confirmed: true,
		Reason: arena.CorrectionReasonOperatorRuling, Explanation: "Verified referee ruling.",
		RequestedAt: gameHead.RecordedAt.Add(time.Minute),
		Expected:    expected,
		Patch: arena.CorrectionPatch{
			State:    domain.ArenaGameStateCompleted,
			Reason:   domain.ArenaGameResultReasonSurrender,
			WinnerID: task055UUIDPointer(series.FirstParticipantID),
		},
		Fields: []arena.CorrectionField{
			arena.CorrectionFieldResultReason,
			arena.CorrectionFieldSolveMetadata,
		},
		NextResultRevisionID:       domain.ArenaOfficialResultRevisionID(task055ID(40_310)),
		NextScoreRevisionID:        domain.ArenaSeriesScoreRevisionID(task055ID(40_311)),
		NextSeriesResultRevisionID: domain.ArenaOfficialResultRevisionID(task055ID(40_312)),
		NextReadinessRevisionID:    task055ID(40_313),
		ProjectionIntents:          projectionIntents,
		UnlockIntents: []arena.CorrectionUnlockIntent{
			arena.NewCorrectionUnlockIntent(reservations[0]),
		},
	}
	return command, authority
}

func task056Series(
	fixture arena.RevisionDAGInput,
	gameHead arena.OfficialResultRevisionHead,
	scoreHead arena.SeriesScoreRevisionHead,
	seriesHead arena.OfficialResultRevisionHead,
) domain.ArenaSeries {
	slots := make([]domain.ArenaGameSlot, len(scoreHead.Attempts))
	for index, attempt := range scoreHead.Attempts {
		scoreBefore := domain.ArenaSeriesScore{FirstParticipantWins: index}
		slots[index] = domain.ArenaGameSlot{
			ID: attempt.SlotID, SeriesID: gameHead.Scope.SeriesID, Position: attempt.SlotPosition,
			Category: domain.CategoryWeb, ScoreBefore: scoreBefore,
			Attempts: []domain.ArenaGame{{
				ID: attempt.GameID, SlotID: attempt.SlotID, AttemptNo: attempt.AttemptNo,
				State: attempt.State, ResultReason: attempt.Reason,
				WinnerID: task055UUIDPointerOrNil(attempt.WinnerID),
				ResultRevisionID: func() *domain.ArenaOfficialResultRevisionID {
					value := attempt.CurrentGameResultRevisionID
					return &value
				}(),
			}},
		}
	}
	series := domain.ArenaSeries{
		ID: gameHead.Scope.SeriesID, TournamentID: gameHead.Scope.TournamentID,
		FirstParticipantID:  scoreHead.FirstParticipantID,
		SecondParticipantID: scoreHead.SecondParticipantID,
		Format:              scoreHead.Format, State: domain.ArenaSeriesStateCompleted,
		Score: scoreHead.Score, WinnerID: task055UUIDPointerOrNil(seriesHead.Outcome.WinnerID),
		Slots: slots, CurrentScoreRevisionID: task055ScoreIDPointer(scoreHead.ID),
		CurrentResultRevisionID: func() *domain.ArenaOfficialResultRevisionID {
			value := seriesHead.ID
			return &value
		}(),
	}
	_ = fixture
	return series
}

func task056RecordedDecisions(
	t *testing.T,
	fixture arena.RevisionDAGInput,
) []arena.RecordedProjectionDecision {
	t.Helper()
	decisions := []arena.RecordedProjectionDecision{
		{
			ID: task055ID(40_030), Sequence: 1,
			ProjectionRevisionID: task055CurrentProjectionID(t, fixture.Graph, domain.ArenaArtifactKindTopFour),
			RecordedAt:           time.Date(2026, time.September, 1, 11, 0, 8, 0, time.UTC),
			Payload:              []byte(`{"order":[1,2,3,4]}`),
		},
		{
			ID: task055ID(40_031), Sequence: 2,
			ProjectionRevisionID: task055CurrentProjectionID(t, fixture.Graph, domain.ArenaArtifactKindChampion),
			RecordedAt:           time.Date(2026, time.September, 1, 11, 0, 10, 0, time.UTC),
			Payload:              []byte(`{"winner":"recorded"}`),
		},
	}
	for index := range decisions {
		decisions[index].PayloadDigest = sha256.Sum256(decisions[index].Payload)
	}
	return decisions
}

func task056CloneCommand(command arena.CorrectionCommand) arena.CorrectionCommand {
	clone := command
	clone.Patch.WinnerID = task055UUIDPointerOrNil(command.Patch.WinnerID)
	clone.Patch.SolveMetadata = command.Patch.SolveMetadata.Clone()
	clone.Fields = append([]arena.CorrectionField(nil), command.Fields...)
	clone.ProjectionIntents = make([]arena.CorrectionProjectionIntent, len(command.ProjectionIntents))
	for index := range command.ProjectionIntents {
		clone.ProjectionIntents[index] = command.ProjectionIntents[index].Clone()
	}
	clone.UnlockIntents = append([]arena.CorrectionUnlockIntent(nil), command.UnlockIntents...)
	return clone
}

func task056CloneAuthority(authority arena.CorrectionAuthority) arena.CorrectionAuthority {
	clone := authority
	clone.Reservations = append([]arena.CorrectionReservation(nil), authority.Reservations...)
	clone.Decisions = make([]arena.RecordedProjectionDecision, len(authority.Decisions))
	for index := range authority.Decisions {
		clone.Decisions[index] = authority.Decisions[index]
		clone.Decisions[index].Payload = append([]byte(nil), authority.Decisions[index].Payload...)
	}
	return clone
}
