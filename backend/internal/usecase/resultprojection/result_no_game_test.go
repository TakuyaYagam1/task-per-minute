package resultprojection_test

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

func TestRecordedNoGameSQLOrigin(t *testing.T) {
	evidence := task055NoGameEvidence(t, domain.SeriesFormatBO1, time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC), 910)
	previous := domain.SeriesScoreRevisionID(uuid.New())
	evidence.Score.PreviousRevisionID = &previous
	bind := func(id uuid.UUID, original domain.ProjectionRevision, ordinal int, predecessor *domain.DerivedRevisionID) domain.ProjectionRevision {
		node, err := domain.NewProjectionRevision(domain.DerivedRevisionID(id), evidence.Scope.TournamentID, original.Revision().Artifact(), ordinal, predecessor, evidence.ResolvedAt, original.Payload())
		require.NoError(t, err)
		return node
	}
	previousNode := domain.DerivedRevisionID(previous.UUID())
	evidence.ScoreProjection = bind(evidence.Score.ID.UUID(), evidence.ScoreProjection, 2, &previousNode)
	evidence.ScoreSourceRevision = evidence.ScoreProjection.Revision()
	evidence.ResultProjection = bind(evidence.Series.ID.UUID(), evidence.ResultProjection, 1, nil)
	evidence.ResultSourceRevision = evidence.ResultProjection.Revision()
	evidence.ResultDependency = domain.RevisionDependency{SourceRevisionID: evidence.ScoreSourceRevision.ID(), DerivedRevisionID: evidence.ResultSourceRevision.ID()}
	for index := range evidence.GameResults {
		evidence.GameProjections[index] = bind(evidence.GameResults[index].ID.UUID(), evidence.GameProjections[index], 1, nil)
		evidence.GameSourceRevisions[index] = evidence.GameProjections[index].Revision()
		evidence.GameDependencies[index] = domain.RevisionDependency{SourceRevisionID: evidence.GameProjections[index].Revision().ID(), DerivedRevisionID: evidence.ScoreSourceRevision.ID()}
	}
	_, err := projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{TerminalSource: projection.TerminalResultSourceNormalNoShow, NoGame: &evidence})
	require.ErrorIs(t, err, projection.ErrInvalidOfficialResultProjection, "literal aliases remain invalid")
	restored, err := projection.RestoreSQLNoGameResult(evidence)
	require.NoError(t, err)
	require.True(t, restored.HasSQLSourceIdentity())
	planned, err := projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{TerminalSource: projection.TerminalResultSourceNormalNoShow, NoGame: &restored})
	require.NoError(t, err)
	require.NoError(t, planned.Validate(), "cloned source keeps validated origin")
	for _, mutate := range []func(*projection.RecordedNoGameResult){
		func(v *projection.RecordedNoGameResult) { v.Scope.SeriesID = uuid.New() },
		func(v *projection.RecordedNoGameResult) { v.Scope.TournamentID = uuid.New() },
		func(v *projection.RecordedNoGameResult) { v.CommandID = v.Score.ID.UUID() },
		func(v *projection.RecordedNoGameResult) { v.Score.ID = domain.SeriesScoreRevisionID(uuid.New()) },
		func(v *projection.RecordedNoGameResult) { v.Score.PreviousRevisionID = nil },
		func(v *projection.RecordedNoGameResult) {
			id := domain.SeriesScoreRevisionID(uuid.New())
			v.Score.PreviousRevisionID = &id
		},
		func(v *projection.RecordedNoGameResult) { v.Series.Ordinal = 1 },
		func(v *projection.RecordedNoGameResult) { v.ResolvedAt = v.ResolvedAt.Add(time.Second) },
	} {
		forged := restored
		mutate(&forged)
		require.False(t, forged.HasSQLSourceIdentity())
		_, err := projection.RestoreSQLNoGameResult(forged)
		require.ErrorIs(t, err, projection.ErrInvalidOfficialResultProjection)
	}
}

func TestRecordedNoGameResultProjection(t *testing.T) {
	t.Parallel()

	tournamentID := task055ID(1)
	seriesID := task055ID(2)
	firstID := task055ID(4)
	secondID := task055ID(5)
	baseTime := time.Date(2026, time.September, 1, 10, 0, 0, 0, time.UTC)

	t.Run("requires an explicit matching terminal source", func(t *testing.T) {
		t.Parallel()

		evidence := task055NoGameEvidence(t, domain.SeriesFormatBO1, baseTime.Add(30*time.Minute), 500)
		_, err := projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{
			TerminalSource: projection.TerminalResultSourcePlayed,
			NoGame:         &evidence,
		})
		require.ErrorIs(t, err, projection.ErrInvalidOfficialResultProjection)

		_, err = projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{
			NoGame: &evidence,
		})
		require.ErrorIs(t, err, projection.ErrInvalidOfficialResultProjection)
	})

	t.Run("derives recorded BO1 and BO3 no-show without a scoring Game", func(t *testing.T) {
		t.Parallel()

		for index, format := range []domain.SeriesFormat{
			domain.SeriesFormatBO1,
			domain.SeriesFormatBO3,
		} {
			t.Run(string(format), func(t *testing.T) {
				t.Parallel()
				resolvedAt := baseTime.Add(time.Duration(40+index) * time.Minute)
				evidence := task055NoGameEvidence(
					t, format, resolvedAt, 600+index*30,
				)
				winnerID := evidence.FirstParticipantID

				plan, err := projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{
					TerminalSource: projection.TerminalResultSourceNormalNoShow,
					NoGame:         &evidence,
				})
				require.NoError(t, err)
				public := plan.Public()
				require.Equal(t, resultusecase.OfficialResultSubjectSeries, public.Subject)
				require.Equal(t, projection.OfficialResultStatusNoShow, public.Status)
				require.Nil(t, public.GameID)
				require.Equal(t, winnerID, *public.WinnerID)
				require.NotNil(t, public.Score)
				require.Equal(t, format.WinsRequired(), public.Score.FirstParticipantWins)
				require.Zero(t, public.Score.SecondParticipantWins)
				require.Equal(t, resolvedAt, public.ResolvedAt)
				require.Equal(t, projection.OfficialResultCauseNoShow, *plan.Operator().Cause)

				forged := evidence
				forged.Score.GameResultRevisionIDs = nil
				_, err = projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{
					TerminalSource: projection.TerminalResultSourceNormalNoShow, NoGame: &forged,
				})
				require.ErrorIs(t, err, projection.ErrInvalidOfficialResultProjection)

				absentWinner := evidence.SecondParticipantID
				forged = evidence
				forged.Series.WinnerID = task055UUIDPointer(absentWinner)
				forged.Score.Score = domain.SeriesScore{SecondParticipantWins: format.WinsRequired()}
				_, err = projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{
					TerminalSource: projection.TerminalResultSourceNormalNoShow, NoGame: &forged,
				})
				require.ErrorIs(t, err, projection.ErrInvalidOfficialResultProjection)

				foreign := evidence
				foreign.Topology = append([]projection.RecordedNoGameAttempt(nil), evidence.Topology...)
				foreign.Topology[0].SeriesID = task055ID(9998)
				_, err = projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{
					TerminalSource: projection.TerminalResultSourceNormalNoShow, NoGame: &foreign,
				})
				require.ErrorIs(t, err, projection.ErrInvalidOfficialResultProjection)
			})
		}
	})

	t.Run("projects an atomically recorded two-absent no-show as void", func(t *testing.T) {
		t.Parallel()

		evidence := task055NoGameEvidenceWithReadiness(
			t, domain.SeriesFormatBO3, baseTime.Add(50*time.Minute), 760, false, false,
		)
		plan, err := projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{
			TerminalSource: projection.TerminalResultSourceNormalNoShow, NoGame: &evidence,
		})
		require.NoError(t, err)

		public := plan.Public()
		require.Equal(t, projection.OfficialResultStatusVoid, public.Status)
		require.Nil(t, public.GameID)
		require.Nil(t, public.WinnerID)
		require.NotNil(t, public.Score)
		require.Nil(t, public.Score.Winner(
			evidence.FirstParticipantID, evidence.SecondParticipantID, evidence.Format,
		))

		operator := plan.Operator()
		require.Equal(t, projection.OfficialResultCauseNoShow, *operator.Cause)
		require.Equal(t, domain.NormalNoShowActionPauseWave, *operator.NoShowAction)
		require.Equal(t, domain.SeriesStateCancelled, *operator.SeriesState)
		require.Nil(t, operator.SeriesReason)
		require.Nil(t, operator.GameState)
		require.Nil(t, operator.GameReason)
	})

	t.Run("accepts recorded score and Series predecessors without exposing mutable rows", func(t *testing.T) {
		t.Parallel()

		evidence := task055NoGameEvidence(
			t, domain.SeriesFormatBO3, baseTime.Add(51*time.Minute), 790,
		)
		scorePrevious := domain.SeriesScoreRevisionID(task055ID(890))
		seriesPrevious := domain.OfficialResultRevisionID(task055ID(891))
		evidence.Score.PreviousRevisionID = &scorePrevious
		evidence.Score.Score = domain.SeriesScore{FirstParticipantWins: 2, SecondParticipantWins: 1}
		evidence.Series.PreviousRevisionID = &seriesPrevious

		plan, err := projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{
			TerminalSource: projection.TerminalResultSourceNormalNoShow, NoGame: &evidence,
		})
		require.NoError(t, err)
		require.NoError(t, plan.Validate())
		require.Equal(t, 1, plan.Public().Score.SecondParticipantWins)
		require.Equal(t, seriesPrevious, *plan.Operator().PreviousResultRevisionID)
	})

	t.Run("rejects mixed spliced overflowed and non UTC evidence", func(t *testing.T) {
		t.Parallel()

		evidence := task055NoGameEvidence(
			t, domain.SeriesFormatBO1, baseTime.Add(52*time.Minute), 820,
		)
		mixed := projection.OfficialResultProjectionInput{
			TerminalSource: projection.TerminalResultSourceNormalNoShow,
			NoGame:         &evidence, ResultProjection: evidence.ResultProjection,
		}
		_, err := projection.ProjectOfficialResult(mixed)
		require.ErrorIs(t, err, projection.ErrInvalidOfficialResultProjection)

		overflowed := evidence
		overflowed.GameResults = append([]domain.NormalNoShowGameRevision(nil), evidence.GameResults...)
		overflowed.GameResults[0].Ordinal = math.MaxInt
		_, err = projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{
			TerminalSource: projection.TerminalResultSourceNormalNoShow, NoGame: &overflowed,
		})
		require.ErrorIs(t, err, projection.ErrInvalidOfficialResultProjection)

		nonUTC := evidence
		nonUTC.ResolvedAt = evidence.ResolvedAt.In(time.FixedZone("same-instant", 3600))
		_, err = projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{
			TerminalSource: projection.TerminalResultSourceNormalNoShow, NoGame: &nonUTC,
		})
		require.ErrorIs(t, err, projection.ErrInvalidOfficialResultProjection)

		score := task055ScoreHead(
			t, tournamentID, seriesID, firstID, secondID, domain.SeriesFormatBO1,
			domain.SeriesScore{FirstParticipantWins: 1}, 1, baseTime.Add(53*time.Minute), 850,
		)
		resultHead := task055SeriesResultHead(
			t, tournamentID, seriesID, score.ID, domain.SeriesStateCompleted,
			domain.SeriesResultReasonScoreComplete, task055UUIDPointer(firstID),
			baseTime.Add(54*time.Minute), 870,
		)
		splicedResult := task055ProjectionWithPayload(t, resultHead.SourceProjection, "spliced-result")
		_, err = projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{
			TerminalSource: projection.TerminalResultSourcePlayed,
			Result:         resultHead, ResultProjection: splicedResult,
			Score:           task055ScoreHeadPointer(score),
			ScoreProjection: task055ProjectionPointer(task055ExactProjection(t, score.SourceProjection, "score-850")),
		})
		require.ErrorIs(t, err, projection.ErrInvalidOfficialResultProjection)

		splicedScore := task055ProjectionWithPayload(t, score.SourceProjection, "spliced-score")
		_, err = projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{
			TerminalSource: projection.TerminalResultSourcePlayed,
			Result:         resultHead, ResultProjection: task055ExactProjection(t, resultHead.SourceProjection, "series-870"),
			Score: task055ScoreHeadPointer(score), ScoreProjection: &splicedScore,
		})
		require.ErrorIs(t, err, projection.ErrInvalidOfficialResultProjection)
	})

	t.Run("clones recorded topology and projection evidence on ingress", func(t *testing.T) {
		t.Parallel()

		evidence := task055NoGameEvidence(
			t, domain.SeriesFormatBO3, baseTime.Add(55*time.Minute), 900,
		)
		plan, err := projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{
			TerminalSource: projection.TerminalResultSourceNormalNoShow, NoGame: &evidence,
		})
		require.NoError(t, err)
		*evidence.ReadyParticipantID = task055ID(9994)
		evidence.GameResults[0].GameID = task055ID(9993)
		evidence.Topology[0].SeriesID = task055ID(9995)
		evidence.Score.GameResultRevisionIDs[0] = domain.OfficialResultRevisionID(task055ID(9992))
		*evidence.Series.WinnerID = task055ID(9991)
		evidence.GameSourceRevisions[0] = domain.DerivedRevision{}
		evidence.ScoreSourceRevision = domain.DerivedRevision{}
		evidence.ResultSourceRevision = domain.DerivedRevision{}
		evidence.GameProjections[0] = domain.ProjectionRevision{}
		evidence.GameDependencies[0] = domain.RevisionDependency{}
		require.NoError(t, plan.Validate())
		require.Equal(t, projection.OfficialResultStatusNoShow, plan.Public().Status)
	})

	t.Run("rejects no-show projection payloads spliced from recorded source revisions", func(t *testing.T) {
		t.Parallel()

		for _, subject := range []string{"Game", "score", "result"} {
			t.Run(subject, func(t *testing.T) {
				evidence := task055NoGameEvidence(
					t, domain.SeriesFormatBO3, baseTime.Add(56*time.Minute), 940,
				)
				switch subject {
				case "Game":
					evidence.GameProjections = append(
						[]domain.ProjectionRevision(nil), evidence.GameProjections...,
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

				_, err := projection.ProjectOfficialResult(projection.OfficialResultProjectionInput{
					TerminalSource: projection.TerminalResultSourceNormalNoShow, NoGame: &evidence,
				})
				require.ErrorIs(t, err, projection.ErrInvalidOfficialResultProjection)
			})
		}
	})
}
