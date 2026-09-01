package arena_test

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestOfficialResultProjection(t *testing.T) {
	t.Parallel()

	tournamentID := task055ID(1)
	seriesID := task055ID(2)
	gameID := task055ID(3)
	firstID := task055ID(4)
	secondID := task055ID(5)
	baseTime := time.Date(2026, time.September, 1, 10, 0, 0, 0, time.UTC)

	t.Run("derives exact Game status and nullability", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			state      domain.ArenaGameState
			reason     domain.ArenaGameResultReason
			winnerID   *uuid.UUID
			wantStatus arena.OfficialResultStatus
		}{
			{
				name: "solved", state: domain.ArenaGameStateCompleted,
				reason: domain.ArenaGameResultReasonSolved, winnerID: task055UUIDPointer(firstID),
				wantStatus: arena.OfficialResultStatusSolved,
			},
			{
				name: "surrender", state: domain.ArenaGameStateCompleted,
				reason: domain.ArenaGameResultReasonSurrender, winnerID: task055UUIDPointer(firstID),
				wantStatus: arena.OfficialResultStatusCompleted,
			},
			{
				name: "operator forfeit", state: domain.ArenaGameStateCompleted,
				reason: domain.ArenaGameResultReasonOperatorForfeit, winnerID: task055UUIDPointer(firstID),
				wantStatus: arena.OfficialResultStatusCompleted,
			},
			{
				name: "void", state: domain.ArenaGameStateVoid,
				reason:     domain.ArenaGameResultReasonTaskFailure,
				wantStatus: arena.OfficialResultStatusVoid,
			},
			{
				name: "cancelled", state: domain.ArenaGameStateCancelled,
				reason:     domain.ArenaGameResultReasonSeriesCancelled,
				wantStatus: arena.OfficialResultStatusCancelled,
			},
			{
				name: "superseded", state: domain.ArenaGameStateSuperseded,
				reason:     domain.ArenaGameResultReasonDerivedRevisionSuperseded,
				wantStatus: arena.OfficialResultStatusSuperseded,
			},
		}
		for index, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				recordedAt := baseTime.Add(time.Duration(index+2) * time.Minute)
				head := task055GameResultHead(
					t, tournamentID, seriesID, gameID, firstID, test.state, test.reason,
					test.winnerID, recordedAt, 20+index*10,
				)

				plan, err := arena.ProjectOfficialResult(arena.OfficialResultProjectionInput{
					Result: head,
					ResultProjection: task055ExactProjection(
						t, head.SourceProjection, fmt.Sprintf("game-%d", 20+index*10),
					),
				})
				require.NoError(t, err)
				require.NoError(t, plan.Validate())

				public := plan.Public()
				require.Equal(t, arena.OfficialResultSubjectGame, public.Subject)
				require.Equal(t, test.wantStatus, public.Status)
				require.Equal(t, tournamentID, public.TournamentID)
				require.Equal(t, seriesID, public.SeriesID)
				require.NotNil(t, public.GameID)
				require.Equal(t, gameID, *public.GameID)
				require.Equal(t, test.winnerID, public.WinnerID)
				require.Nil(t, public.Score)
				require.Equal(t, recordedAt, public.ResolvedAt)

				operator := plan.Operator()
				require.Equal(t, public, operator.Public)
				require.NotNil(t, operator.GameState)
				require.NotNil(t, operator.GameReason)
				require.Equal(t, test.state, *operator.GameState)
				require.Equal(t, test.reason, *operator.GameReason)
				require.Nil(t, operator.SeriesState)
				require.Nil(t, operator.SeriesReason)
				require.Nil(t, operator.ScoreRevisionID)
				require.Equal(t, head.ID, operator.ResultRevisionID)
				require.Equal(t, head.SourceProjection.ID(), operator.SourceProjectionRevisionID)
			})
		}
	})

	t.Run("derives exact BO1 BO3 and no Game Series fields", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name       string
			format     domain.ArenaSeriesFormat
			score      domain.ArenaSeriesScore
			state      domain.ArenaSeriesState
			reason     arena.ArenaSeriesResultReason
			winnerID   *uuid.UUID
			wantStatus arena.OfficialResultStatus
			attempts   int
		}{
			{
				name: "BO1", format: domain.ArenaSeriesFormatBO1,
				score: domain.ArenaSeriesScore{FirstParticipantWins: 1},
				state: domain.ArenaSeriesStateCompleted, reason: arena.ArenaSeriesResultReasonScoreComplete,
				winnerID: task055UUIDPointer(firstID), wantStatus: arena.OfficialResultStatusCompleted,
				attempts: 1,
			},
			{
				name: "BO3", format: domain.ArenaSeriesFormatBO3,
				score: domain.ArenaSeriesScore{SecondParticipantWins: 2},
				state: domain.ArenaSeriesStateCompleted, reason: arena.ArenaSeriesResultReasonScoreComplete,
				winnerID: task055UUIDPointer(secondID), wantStatus: arena.OfficialResultStatusCompleted,
				attempts: 2,
			},
			{
				name: "cancelled without Game", format: domain.ArenaSeriesFormatBO3,
				score: domain.ArenaSeriesScore{}, state: domain.ArenaSeriesStateCancelled,
				reason:     arena.ArenaSeriesResultReasonTournamentCancelled,
				wantStatus: arena.OfficialResultStatusVoid,
			},
		}
		for index, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				recordedAt := baseTime.Add(time.Duration(index+20) * time.Minute)
				score := task055ScoreHead(
					t, tournamentID, seriesID, firstID, secondID, test.format,
					test.score, test.attempts, recordedAt.Add(-time.Second), 100+index*20,
				)
				result := task055SeriesResultHead(
					t, tournamentID, seriesID, score.ID, test.state, test.reason,
					test.winnerID, recordedAt, 110+index*20,
				)

				plan, err := arena.ProjectOfficialResult(arena.OfficialResultProjectionInput{
					Result:           result,
					ResultProjection: task055ExactProjection(t, result.SourceProjection, fmt.Sprintf("series-%d", 110+index*20)),
					Score:            task055ScoreHeadPointer(score),
					ScoreProjection:  task055ProjectionPointer(task055ExactProjection(t, score.SourceProjection, fmt.Sprintf("score-%d", 100+index*20))),
				})
				require.NoError(t, err)
				require.NoError(t, plan.Validate())

				public := plan.Public()
				require.Equal(t, arena.OfficialResultSubjectSeries, public.Subject)
				require.Equal(t, test.wantStatus, public.Status)
				require.Nil(t, public.GameID)
				require.NotNil(t, public.Score)
				require.Equal(t, test.score, *public.Score)
				require.Equal(t, test.winnerID, public.WinnerID)
				require.Equal(t, recordedAt, public.ResolvedAt)

				operator := plan.Operator()
				require.Nil(t, operator.GameState)
				require.Nil(t, operator.GameReason)
				require.NotNil(t, operator.SeriesState)
				require.NotNil(t, operator.SeriesReason)
				require.Equal(t, test.state, *operator.SeriesState)
				require.Equal(t, test.reason, *operator.SeriesReason)
				require.NotNil(t, operator.ScoreRevisionID)
				require.Equal(t, score.ID, *operator.ScoreRevisionID)
			})
		}
	})

	t.Run("rejects a spliced Series score and returns defensive copies", func(t *testing.T) {
		t.Parallel()

		score := task055ScoreHead(
			t, tournamentID, seriesID, firstID, secondID, domain.ArenaSeriesFormatBO1,
			domain.ArenaSeriesScore{FirstParticipantWins: 1}, 1, baseTime.Add(time.Minute), 300,
		)
		result := task055SeriesResultHead(
			t, tournamentID, seriesID, score.ID, domain.ArenaSeriesStateCompleted,
			arena.ArenaSeriesResultReasonScoreComplete, task055UUIDPointer(firstID),
			baseTime.Add(2*time.Minute), 310,
		)
		wrong := score.Clone()
		wrong.ID = domain.ArenaSeriesScoreRevisionID(task055ID(399))
		_, err := arena.ProjectOfficialResult(arena.OfficialResultProjectionInput{
			Result:           result,
			ResultProjection: task055ExactProjection(t, result.SourceProjection, "series-310"),
			Score:            task055ScoreHeadPointer(wrong),
			ScoreProjection:  task055ProjectionPointer(task055ExactProjection(t, score.SourceProjection, "score-300")),
		})
		require.ErrorIs(t, err, arena.ErrInvalidOfficialResultProjection)

		plan, err := arena.ProjectOfficialResult(arena.OfficialResultProjectionInput{
			Result:           result,
			ResultProjection: task055ExactProjection(t, result.SourceProjection, "series-310"),
			Score:            task055ScoreHeadPointer(score),
			ScoreProjection:  task055ProjectionPointer(task055ExactProjection(t, score.SourceProjection, "score-300")),
		})
		require.NoError(t, err)
		public := plan.Public()
		*public.WinnerID = secondID
		public.Score.FirstParticipantWins = 0
		operator := plan.Operator()
		*operator.Public.WinnerID = secondID
		require.Equal(t, firstID, *plan.Public().WinnerID)
		require.Equal(t, 1, plan.Public().Score.FirstParticipantWins)
		require.Equal(t, firstID, *plan.Operator().Public.WinnerID)
	})

	t.Run("derives recorded BO1 and BO3 no-show without a scoring Game", func(t *testing.T) {
		t.Parallel()

		for index, format := range []domain.ArenaSeriesFormat{
			domain.ArenaSeriesFormatBO1,
			domain.ArenaSeriesFormatBO3,
		} {
			t.Run(string(format), func(t *testing.T) {
				t.Parallel()
				resolvedAt := baseTime.Add(time.Duration(40+index) * time.Minute)
				evidence := task055NoGameEvidence(
					t, format, resolvedAt, 600+index*30,
				)
				winnerID := evidence.FirstParticipantID

				plan, err := arena.ProjectOfficialResult(arena.OfficialResultProjectionInput{
					NoGame: &evidence,
				})
				require.NoError(t, err)
				public := plan.Public()
				require.Equal(t, arena.OfficialResultSubjectSeries, public.Subject)
				require.Equal(t, arena.OfficialResultStatusNoShow, public.Status)
				require.Nil(t, public.GameID)
				require.Equal(t, winnerID, *public.WinnerID)
				require.NotNil(t, public.Score)
				require.Equal(t, format.WinsRequired(), public.Score.FirstParticipantWins)
				require.Zero(t, public.Score.SecondParticipantWins)
				require.Equal(t, resolvedAt, public.ResolvedAt)
				require.Equal(t, arena.OfficialResultCauseNoShow, *plan.Operator().Cause)

				forged := evidence
				forged.Score.GameResultRevisionIDs = nil
				_, err = arena.ProjectOfficialResult(arena.OfficialResultProjectionInput{NoGame: &forged})
				require.ErrorIs(t, err, arena.ErrInvalidOfficialResultProjection)

				absentWinner := evidence.SecondParticipantID
				forged = evidence
				forged.Series.WinnerID = task055UUIDPointer(absentWinner)
				forged.Score.Score = domain.ArenaSeriesScore{SecondParticipantWins: format.WinsRequired()}
				_, err = arena.ProjectOfficialResult(arena.OfficialResultProjectionInput{NoGame: &forged})
				require.ErrorIs(t, err, arena.ErrInvalidOfficialResultProjection)

				foreign := evidence
				foreign.Topology = append([]arena.RecordedNoGameAttempt(nil), evidence.Topology...)
				foreign.Topology[0].SeriesID = task055ID(9998)
				_, err = arena.ProjectOfficialResult(arena.OfficialResultProjectionInput{NoGame: &foreign})
				require.ErrorIs(t, err, arena.ErrInvalidOfficialResultProjection)
			})
		}
	})

	t.Run("projects an atomically recorded two-absent no-show as void", func(t *testing.T) {
		t.Parallel()

		evidence := task055NoGameEvidenceWithReadiness(
			t, domain.ArenaSeriesFormatBO3, baseTime.Add(50*time.Minute), 760, false, false,
		)
		plan, err := arena.ProjectOfficialResult(arena.OfficialResultProjectionInput{NoGame: &evidence})
		require.NoError(t, err)

		public := plan.Public()
		require.Equal(t, arena.OfficialResultStatusVoid, public.Status)
		require.Nil(t, public.GameID)
		require.Nil(t, public.WinnerID)
		require.NotNil(t, public.Score)
		require.Nil(t, public.Score.Winner(
			evidence.FirstParticipantID, evidence.SecondParticipantID, evidence.Format,
		))

		operator := plan.Operator()
		require.Equal(t, arena.OfficialResultCauseNoShow, *operator.Cause)
		require.Equal(t, arena.NormalNoShowActionPauseWave, *operator.NoShowAction)
		require.Equal(t, domain.ArenaSeriesStateCancelled, *operator.SeriesState)
		require.Nil(t, operator.SeriesReason)
		require.Nil(t, operator.GameState)
		require.Nil(t, operator.GameReason)
	})

	t.Run("accepts recorded score and Series predecessors without exposing mutable rows", func(t *testing.T) {
		t.Parallel()

		evidence := task055NoGameEvidence(
			t, domain.ArenaSeriesFormatBO3, baseTime.Add(51*time.Minute), 790,
		)
		scorePrevious := domain.ArenaSeriesScoreRevisionID(task055ID(890))
		seriesPrevious := domain.ArenaOfficialResultRevisionID(task055ID(891))
		evidence.Score.PreviousRevisionID = &scorePrevious
		evidence.Score.Score = domain.ArenaSeriesScore{FirstParticipantWins: 2, SecondParticipantWins: 1}
		evidence.Series.PreviousRevisionID = &seriesPrevious

		plan, err := arena.ProjectOfficialResult(arena.OfficialResultProjectionInput{NoGame: &evidence})
		require.NoError(t, err)
		require.NoError(t, plan.Validate())
		require.Equal(t, 1, plan.Public().Score.SecondParticipantWins)
		require.Equal(t, seriesPrevious, *plan.Operator().PreviousResultRevisionID)
	})

	t.Run("rejects mixed spliced overflowed and non UTC evidence", func(t *testing.T) {
		t.Parallel()

		evidence := task055NoGameEvidence(
			t, domain.ArenaSeriesFormatBO1, baseTime.Add(52*time.Minute), 820,
		)
		mixed := arena.OfficialResultProjectionInput{
			NoGame: &evidence, ResultProjection: evidence.ResultProjection,
		}
		_, err := arena.ProjectOfficialResult(mixed)
		require.ErrorIs(t, err, arena.ErrInvalidOfficialResultProjection)

		overflowed := evidence
		overflowed.GameResults = append([]arena.NormalNoShowGameRevision(nil), evidence.GameResults...)
		overflowed.GameResults[0].Ordinal = math.MaxInt
		_, err = arena.ProjectOfficialResult(arena.OfficialResultProjectionInput{NoGame: &overflowed})
		require.ErrorIs(t, err, arena.ErrInvalidOfficialResultProjection)

		nonUTC := evidence
		nonUTC.ResolvedAt = evidence.ResolvedAt.In(time.FixedZone("same-instant", 3600))
		_, err = arena.ProjectOfficialResult(arena.OfficialResultProjectionInput{NoGame: &nonUTC})
		require.ErrorIs(t, err, arena.ErrInvalidOfficialResultProjection)

		score := task055ScoreHead(
			t, tournamentID, seriesID, firstID, secondID, domain.ArenaSeriesFormatBO1,
			domain.ArenaSeriesScore{FirstParticipantWins: 1}, 1, baseTime.Add(53*time.Minute), 850,
		)
		result := task055SeriesResultHead(
			t, tournamentID, seriesID, score.ID, domain.ArenaSeriesStateCompleted,
			arena.ArenaSeriesResultReasonScoreComplete, task055UUIDPointer(firstID),
			baseTime.Add(54*time.Minute), 870,
		)
		splicedResult := task055ProjectionWithPayload(t, result.SourceProjection, "spliced-result")
		_, err = arena.ProjectOfficialResult(arena.OfficialResultProjectionInput{
			Result: result, ResultProjection: splicedResult,
			Score:           task055ScoreHeadPointer(score),
			ScoreProjection: task055ProjectionPointer(task055ExactProjection(t, score.SourceProjection, "score-850")),
		})
		require.ErrorIs(t, err, arena.ErrInvalidOfficialResultProjection)

		splicedScore := task055ProjectionWithPayload(t, score.SourceProjection, "spliced-score")
		_, err = arena.ProjectOfficialResult(arena.OfficialResultProjectionInput{
			Result: result, ResultProjection: task055ExactProjection(t, result.SourceProjection, "series-870"),
			Score: task055ScoreHeadPointer(score), ScoreProjection: &splicedScore,
		})
		require.ErrorIs(t, err, arena.ErrInvalidOfficialResultProjection)
	})

	t.Run("clones recorded topology and projection evidence on ingress", func(t *testing.T) {
		t.Parallel()

		evidence := task055NoGameEvidence(
			t, domain.ArenaSeriesFormatBO3, baseTime.Add(55*time.Minute), 900,
		)
		plan, err := arena.ProjectOfficialResult(arena.OfficialResultProjectionInput{NoGame: &evidence})
		require.NoError(t, err)
		evidence.Topology[0].SeriesID = task055ID(9995)
		evidence.GameSourceRevisions[0] = domain.ArenaDerivedRevision{}
		evidence.ScoreSourceRevision = domain.ArenaDerivedRevision{}
		evidence.ResultSourceRevision = domain.ArenaDerivedRevision{}
		evidence.GameProjections[0] = domain.ArenaProjectionRevision{}
		evidence.GameDependencies[0] = domain.ArenaRevisionDependency{}
		require.NoError(t, plan.Validate())
		require.Equal(t, arena.OfficialResultStatusNoShow, plan.Public().Status)
	})

	t.Run("rejects no-show projection payloads spliced from recorded source revisions", func(t *testing.T) {
		t.Parallel()

		for _, subject := range []string{"Game", "score", "result"} {
			t.Run(subject, func(t *testing.T) {
				evidence := task055NoGameEvidence(
					t, domain.ArenaSeriesFormatBO3, baseTime.Add(56*time.Minute), 940,
				)
				switch subject {
				case "Game":
					evidence.GameProjections = append(
						[]domain.ArenaProjectionRevision(nil), evidence.GameProjections...,
					)
					evidence.GameProjections[0] = task055ProjectionWithPayload(
						t, evidence.GameSourceRevisions[0], "forged-no-show-game",
					)
				case "score":
					evidence.ScoreProjection = task055ProjectionWithPayload(
						t, evidence.ScoreSourceRevision, "forged-no-show-score",
					)
				case "result":
					evidence.ResultProjection = task055ProjectionWithPayload(
						t, evidence.ResultSourceRevision, "forged-no-show-result",
					)
				}

				_, err := arena.ProjectOfficialResult(arena.OfficialResultProjectionInput{NoGame: &evidence})
				require.ErrorIs(t, err, arena.ErrInvalidOfficialResultProjection)
			})
		}
	})
}

func task055GameResultHead(
	t *testing.T,
	tournamentID, seriesID, gameID, winnerParticipantID uuid.UUID,
	state domain.ArenaGameState,
	reason domain.ArenaGameResultReason,
	winnerID *uuid.UUID,
	recordedAt time.Time,
	idBase int,
) arena.OfficialResultRevisionHead {
	t.Helper()
	source := task055Projection(
		t, idBase, tournamentID, domain.ArenaArtifactKindGameResult, gameID,
		recordedAt.Add(-time.Minute), fmt.Sprintf("game-%d", idBase),
	).Revision()
	head := arena.OfficialResultRevisionHead{
		Scope: arena.OfficialResultScope{
			TournamentID: tournamentID, SeriesID: seriesID, GameID: gameID,
			Kind: arena.OfficialResultSubjectGame,
		},
		ID:        domain.ArenaOfficialResultRevisionID(task055ID(idBase + 1)),
		Ordinal:   1,
		CommandID: task055ID(idBase + 2),
		Actor:     arena.ArenaResultActor{Kind: arena.ArenaResultActorServer},
		Outcome: arena.OfficialResultOutcome{
			GameState: state, GameReason: reason, WinnerID: task055UUIDPointerOrNil(winnerID),
		},
		SourceProjection: source,
		RecordedAt:       recordedAt,
	}
	_ = winnerParticipantID
	require.NoError(t, head.Validate())
	return head
}

func task055SeriesResultHead(
	t *testing.T,
	tournamentID, seriesID uuid.UUID,
	scoreID domain.ArenaSeriesScoreRevisionID,
	state domain.ArenaSeriesState,
	reason arena.ArenaSeriesResultReason,
	winnerID *uuid.UUID,
	recordedAt time.Time,
	idBase int,
) arena.OfficialResultRevisionHead {
	t.Helper()
	source := task055Projection(
		t, idBase, tournamentID, domain.ArenaArtifactKindSeriesResult, seriesID,
		recordedAt.Add(-time.Minute), fmt.Sprintf("series-%d", idBase),
	).Revision()
	head := arena.OfficialResultRevisionHead{
		Scope: arena.OfficialResultScope{
			TournamentID: tournamentID, SeriesID: seriesID, Kind: arena.OfficialResultSubjectSeries,
		},
		ID:        domain.ArenaOfficialResultRevisionID(task055ID(idBase + 1)),
		Ordinal:   1,
		CommandID: task055ID(idBase + 2),
		Actor:     arena.ArenaResultActor{Kind: arena.ArenaResultActorServer},
		Outcome: arena.OfficialResultOutcome{
			SeriesState: state, SeriesReason: reason,
			WinnerID: task055UUIDPointerOrNil(winnerID), ScoreRevisionID: task055ScoreIDPointer(scoreID),
		},
		SourceProjection: source,
		RecordedAt:       recordedAt,
	}
	require.NoError(t, head.Validate())
	return head
}

func task055ScoreHead(
	t *testing.T,
	tournamentID, seriesID, firstID, secondID uuid.UUID,
	format domain.ArenaSeriesFormat,
	score domain.ArenaSeriesScore,
	attemptCount int,
	recordedAt time.Time,
	idBase int,
) arena.SeriesScoreRevisionHead {
	t.Helper()
	source := task055Projection(
		t, idBase, tournamentID, domain.ArenaArtifactKindSeriesScore, seriesID,
		recordedAt.Add(-time.Minute), fmt.Sprintf("score-%d", idBase),
	).Revision()
	head := arena.SeriesScoreRevisionHead{
		Scope:   arena.SeriesScoreRevisionScope{TournamentID: tournamentID, SeriesID: seriesID},
		ID:      domain.ArenaSeriesScoreRevisionID(task055ID(idBase + 1)),
		Ordinal: 1, Operation: arena.SeriesScoreRevisionOperationInitialize,
		CommandID: idForTask055(idBase + 2), Actor: arena.ArenaResultActor{Kind: arena.ArenaResultActorServer},
		FirstParticipantID: firstID, SecondParticipantID: secondID,
		Format: format, Score: score, SourceProjection: source, RecordedAt: recordedAt,
	}
	if attemptCount > 0 {
		head.Ordinal = 2
		previous := domain.ArenaSeriesScoreRevisionID(task055ID(idBase + 3))
		head.PreviousRevisionID = &previous
		head.Operation = arena.SeriesScoreRevisionOperationAppendAttempt
		head.Attempts = make([]arena.SeriesScoreAttemptReference, 0, attemptCount)
		for index := 0; index < attemptCount; index++ {
			winner := firstID
			if score.SecondParticipantWins > 0 {
				winner = secondID
			}
			reference := arena.SeriesScoreAttemptReference{
				SlotID: task055ID(idBase + 10 + index), SlotPosition: index + 1,
				GameID: task055ID(idBase + 20 + index), AttemptNo: 1,
				State: domain.ArenaGameStateCompleted, WinnerID: task055UUIDPointer(winner),
				Reason:                      domain.ArenaGameResultReasonSolved,
				CurrentGameResultRevisionID: domain.ArenaOfficialResultRevisionID(task055ID(idBase + 30 + index)),
			}
			head.Attempts = append(head.Attempts, reference)
		}
		commandAttempt := head.Attempts[len(head.Attempts)-1]
		head.CommandAttempt = &commandAttempt
	}
	require.NoError(t, head.Validate())
	return head
}

func task055Projection(
	t *testing.T,
	id int,
	tournamentID uuid.UUID,
	kind domain.ArenaArtifactKind,
	entityID uuid.UUID,
	createdAt time.Time,
	payload string,
) domain.ArenaProjectionRevision {
	t.Helper()
	projection, err := domain.NewArenaProjectionRevision(
		domain.ArenaDerivedRevisionID(task055ID(id)), tournamentID,
		domain.ArenaArtifactRef{Kind: kind, EntityID: entityID},
		1, nil, createdAt, []byte(payload),
	)
	require.NoError(t, err)
	return projection
}

func task055ExactProjection(
	t *testing.T,
	revision domain.ArenaDerivedRevision,
	payload string,
) domain.ArenaProjectionRevision {
	t.Helper()
	projection, err := domain.NewArenaProjectionRevision(
		revision.ID(), revision.TournamentID(), revision.Artifact(), revision.RevisionNo(),
		revision.PreviousRevisionID(), revision.CreatedAt(), []byte(payload),
	)
	require.NoError(t, err)
	require.Equal(t, revision.PayloadDigest(), projection.Revision().PayloadDigest())
	return projection
}

func task055ProjectionWithPayload(
	t *testing.T,
	revision domain.ArenaDerivedRevision,
	payload string,
) domain.ArenaProjectionRevision {
	t.Helper()
	projection, err := domain.NewArenaProjectionRevision(
		revision.ID(), revision.TournamentID(), revision.Artifact(), revision.RevisionNo(),
		revision.PreviousRevisionID(), revision.CreatedAt(), []byte(payload),
	)
	require.NoError(t, err)
	return projection
}

func task055NoGameEvidence(
	t *testing.T,
	format domain.ArenaSeriesFormat,
	recordedAt time.Time,
	idBase int,
) arena.RecordedNoGameResult {
	t.Helper()
	return task055NoGameEvidenceWithReadiness(t, format, recordedAt, idBase, true, false)
}

func task055NoGameEvidenceWithReadiness(
	t *testing.T,
	format domain.ArenaSeriesFormat,
	recordedAt time.Time,
	idBase int,
	firstReady bool,
	secondReady bool,
) arena.RecordedNoGameResult {
	t.Helper()
	authority, command := normalNoShowFixture(t, recordedAt, firstReady, secondReady)
	authority.Series.Series.Format = format
	command.ScoreRevisionID = domain.ArenaSeriesScoreRevisionID(task055ID(idBase + 2))
	command.SeriesResultRevisionID = domain.ArenaOfficialResultRevisionID(task055ID(idBase + 3))
	command.GameResultRevisionIDs = []domain.ArenaOfficialResultRevisionID{
		domain.ArenaOfficialResultRevisionID(task055ID(idBase + 1)),
	}
	resolution, changed, err := arena.NewNormalNoShowUseCase(
		&normalNoShowRepositoryFake{authority: authority}, fixedArenaClock{now: recordedAt},
	).Resolve(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, resolution.Validate())
	tournamentID := resolution.Scope.TournamentID
	seriesID := resolution.Scope.SeriesID
	scoreProjection := task055Projection(
		t, idBase+10, tournamentID, domain.ArenaArtifactKindSeriesScore, seriesID,
		recordedAt.Add(-2*time.Second), fmt.Sprintf("no-game-score-%d", idBase),
	)
	resultProjection := task055Projection(
		t, idBase+11, tournamentID, domain.ArenaArtifactKindSeriesResult, seriesID,
		recordedAt.Add(-time.Second), fmt.Sprintf("no-game-result-%d", idBase),
	)
	gameProjections := make([]domain.ArenaProjectionRevision, len(resolution.GameRevisions))
	topology := make([]arena.RecordedNoGameAttempt, len(resolution.GameRevisions))
	for index, game := range resolution.GameRevisions {
		gameProjections[index] = task055Projection(
			t, idBase+12+index, tournamentID, domain.ArenaArtifactKindGameResult,
			game.GameID, recordedAt.Add(-3*time.Second), fmt.Sprintf("no-game-%d-%d", idBase, index),
		)
		for _, slot := range resolution.Series.Series.Slots {
			for _, attempt := range slot.Attempts {
				if attempt.ID == game.GameID {
					topology[index] = arena.RecordedNoGameAttempt{
						SeriesID: resolution.Scope.SeriesID, SlotID: slot.ID, SlotPosition: slot.Position,
						GameID: game.GameID, AttemptNo: attempt.AttemptNo, ResultRevisionID: game.ID,
					}
				}
			}
		}
	}
	return arena.RecordedNoGameResult{
		Scope: resolution.Scope, CommandID: resolution.CommandID, Action: resolution.Action,
		Format:              resolution.Series.Series.Format,
		FirstParticipantID:  resolution.Series.Series.FirstParticipantID,
		SecondParticipantID: resolution.Series.Series.SecondParticipantID,
		ReadyParticipantID:  task055UUIDPointerOrNil(resolution.Series.Series.WinnerID),
		GameResults:         resolution.GameRevisions, Topology: topology, Score: resolution.ScoreRevision,
		Series: resolution.SeriesRevision,
		GameSourceRevisions: func() []domain.ArenaDerivedRevision {
			revisions := make([]domain.ArenaDerivedRevision, len(gameProjections))
			for index := range gameProjections {
				revisions[index] = gameProjections[index].Revision()
			}
			return revisions
		}(),
		ScoreSourceRevision:  scoreProjection.Revision(),
		ResultSourceRevision: resultProjection.Revision(),
		GameProjections:      gameProjections,
		GameDependencies: func() []domain.ArenaRevisionDependency {
			dependencies := make([]domain.ArenaRevisionDependency, len(gameProjections))
			for index := range gameProjections {
				dependencies[index] = domain.ArenaRevisionDependency{
					SourceRevisionID:  gameProjections[index].Revision().ID(),
					DerivedRevisionID: scoreProjection.Revision().ID(),
				}
			}
			return dependencies
		}(),
		ScoreProjection: scoreProjection, ResultProjection: resultProjection,
		ResultDependency: domain.ArenaRevisionDependency{
			SourceRevisionID:  scoreProjection.Revision().ID(),
			DerivedRevisionID: resultProjection.Revision().ID(),
		},
		ResolvedAt: resolution.ResolvedAt,
	}
}

func task055ID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("55000000-0000-0000-0000-%012x", value))
}

func idForTask055(value int) uuid.UUID {
	return task055ID(value)
}

func task055UUIDPointer(value uuid.UUID) *uuid.UUID {
	return &value
}

func task055UUIDPointerOrNil(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func task055ScoreIDPointer(value domain.ArenaSeriesScoreRevisionID) *domain.ArenaSeriesScoreRevisionID {
	return &value
}

func task055ScoreHeadPointer(value arena.SeriesScoreRevisionHead) *arena.SeriesScoreRevisionHead {
	clone := value.Clone()
	return &clone
}

func task055ProjectionPointer(value domain.ArenaProjectionRevision) *domain.ArenaProjectionRevision {
	clone, err := domain.NewArenaProjectionRevision(
		value.Revision().ID(), value.Revision().TournamentID(), value.Revision().Artifact(),
		value.Revision().RevisionNo(), value.Revision().PreviousRevisionID(),
		value.Revision().CreatedAt(), value.Payload(),
	)
	if err != nil {
		panic(err)
	}
	return &clone
}
