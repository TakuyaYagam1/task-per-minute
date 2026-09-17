package revision_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/revision"
)

func task055LongCurrentPayloadFixture(t *testing.T) projection.RevisionDAGInput {
	t.Helper()
	input := task055RevisionDAGFixture(t)
	tournamentID := input.Results[0].Result.Scope.TournamentID
	artifact := domain.ArtifactRef{
		Kind: domain.ArtifactKindChampion, EntityID: tournamentID,
	}
	previous, exists := input.Graph.CurrentRevision(artifact)
	require.True(t, exists)
	projections := input.Graph.Projections()
	dependencies := input.Graph.Dependencies()

	const revisionCount = 128
	for index := 0; index < revisionCount; index++ {
		previousRevision := previous.Revision()
		previousID := previousRevision.ID()
		payload := []byte{byte(index)}
		if index == revisionCount-1 {
			payload = make([]byte, 128<<10)
		}
		next, err := domain.NewProjectionRevision(
			domain.DerivedRevisionID(task055ID(10_000+index)), tournamentID,
			artifact, previousRevision.RevisionNo()+1, &previousID,
			previousRevision.CreatedAt().Add(time.Nanosecond), payload,
		)
		require.NoError(t, err)
		projections = append(projections, next)
		dependencies = append(dependencies, domain.RevisionDependency{
			SourceRevisionID: previousID, DerivedRevisionID: next.Revision().ID(),
		})
		previous = next
	}
	dependencies = append(dependencies, domain.RevisionDependency{
		SourceRevisionID:  task055CurrentProjectionID(t, input.Graph, domain.ArtifactKindBracket),
		DerivedRevisionID: previous.Revision().ID(),
	})
	graph, err := domain.NewRevisionGraph(projections, dependencies)
	require.NoError(t, err)
	input.Graph = graph
	return input
}

func task055DistinctGameProjectionInput(
	t *testing.T,
	base projection.OfficialResultProjectionInput,
	idBase int,
) projection.OfficialResultProjectionInput {
	t.Helper()
	clone := base.Result.Clone()
	clone.Scope.GameID = task055ID(idBase)
	clone.ID = domain.OfficialResultRevisionID(task055ID(idBase + 1))
	clone.CommandID = task055ID(idBase + 2)
	revision := task055Projection(
		t, idBase+3, clone.Scope.TournamentID, domain.ArtifactKindGameResult,
		clone.Scope.GameID, clone.SourceProjection.CreatedAt(), fmt.Sprintf("distinct-game-%d", idBase),
	)
	clone.SourceProjection = revision.Revision()
	require.NoError(t, clone.Validate())
	return projection.OfficialResultProjectionInput{
		TerminalSource:   projection.TerminalResultSourcePlayed,
		Result:           clone,
		ResultProjection: revision,
	}
}

func task055NoGameRevisionDAGFixture(t *testing.T) projection.RevisionDAGInput {
	t.Helper()
	resolvedAt := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	recorded := task055NoGameEvidence(t, domain.SeriesFormatBO3, resolvedAt, 14_000)
	projections := append([]domain.ProjectionRevision(nil), recorded.GameProjections...)
	projections = append(projections, recorded.ScoreProjection, recorded.ResultProjection)
	kinds := []domain.ArtifactKind{
		domain.ArtifactKindStandings,
		domain.ArtifactKindGoldenGroup,
		domain.ArtifactKindTopFour,
		domain.ArtifactKindBracket,
		domain.ArtifactKindChampion,
	}
	for index, kind := range kinds {
		projections = append(projections, task055Projection(
			t, 14_100+index, recorded.Scope.TournamentID, kind, recorded.Scope.TournamentID,
			resolvedAt.Add(time.Duration(index)*time.Second), fmt.Sprintf("no-show-downstream-%d", index),
		))
	}
	dependencies := append([]domain.RevisionDependency(nil), recorded.GameDependencies...)
	dependencies = append(dependencies, recorded.ResultDependency)
	for index := len(recorded.GameProjections) + 2; index < len(projections); index++ {
		sourceID := recorded.ResultProjection.Revision().ID()
		if index > len(recorded.GameProjections)+2 {
			sourceID = projections[index-1].Revision().ID()
		}
		dependencies = append(dependencies, domain.RevisionDependency{
			SourceRevisionID: sourceID, DerivedRevisionID: projections[index].Revision().ID(),
		})
	}
	graph, err := domain.NewRevisionGraph(projections, dependencies)
	require.NoError(t, err)
	return projection.RevisionDAGInput{
		Graph: graph,
		Results: []projection.OfficialResultProjectionInput{{
			TerminalSource: projection.TerminalResultSourceNormalNoShow,
			NoGame:         &recorded,
		}},
	}
}

func task055RepeatedNoGameProjectionFixture(t *testing.T) projection.RevisionDAGInput {
	t.Helper()
	input := task055NoGameRevisionDAGFixture(t)
	recorded := *input.Results[0].NoGame
	originalProjection := recorded.GameProjections[0]
	originalRevision := originalProjection.Revision()
	largeProjection, err := domain.NewProjectionRevision(
		originalRevision.ID(), originalRevision.TournamentID(), originalRevision.Artifact(),
		originalRevision.RevisionNo(), originalRevision.PreviousRevisionID(),
		originalRevision.CreatedAt(), make([]byte, 128<<10),
	)
	require.NoError(t, err)

	const gameCount = 16
	firstOrdinal := recorded.GameResults[0].Ordinal
	recorded.GameResults = make([]domain.NormalNoShowGameRevision, gameCount)
	recorded.Topology = make([]projection.RecordedNoGameAttempt, gameCount)
	recorded.GameSourceRevisions = make([]domain.DerivedRevision, gameCount)
	recorded.GameProjections = make([]domain.ProjectionRevision, gameCount)
	recorded.GameDependencies = make([]domain.RevisionDependency, gameCount)
	recorded.Score.GameResultRevisionIDs = make([]domain.OfficialResultRevisionID, gameCount)
	for index := 0; index < gameCount; index++ {
		resultID := domain.OfficialResultRevisionID(task055ID(19_000 + index))
		gameID := task055ID(19_100 + index)
		recorded.GameResults[index] = domain.NormalNoShowGameRevision{
			ID: resultID, Ordinal: firstOrdinal + index, GameID: gameID,
			State:      domain.GameStateCancelled,
			Reason:     domain.GameResultReasonSeriesCancelled,
			RecordedAt: recorded.ResolvedAt,
		}
		recorded.Topology[index] = projection.RecordedNoGameAttempt{
			SeriesID: recorded.Scope.SeriesID, SlotID: task055ID(19_200 + index),
			SlotPosition: index + 1, GameID: gameID, AttemptNo: 1,
			ResultRevisionID: resultID,
		}
		recorded.GameSourceRevisions[index] = largeProjection.Revision()
		recorded.GameProjections[index] = largeProjection
		recorded.GameDependencies[index] = domain.RevisionDependency{
			SourceRevisionID:  largeProjection.Revision().ID(),
			DerivedRevisionID: recorded.ScoreProjection.Revision().ID(),
		}
		recorded.Score.GameResultRevisionIDs[index] = resultID
	}
	recorded.Score.Ordinal = firstOrdinal + gameCount
	recorded.Series.Ordinal = firstOrdinal + gameCount + 1
	input.Results[0].NoGame = &recorded

	projections := input.Graph.Projections()
	for index := range projections {
		if projections[index].Revision().ID() == largeProjection.Revision().ID() {
			projections[index] = largeProjection
		}
	}
	graph, err := domain.NewRevisionGraph(projections, input.Graph.Dependencies())
	require.NoError(t, err)
	input.Graph = graph
	return input
}

func task055RevisionDAGFixture(t *testing.T) projection.RevisionDAGInput {
	t.Helper()
	tournamentID := task055ID(1000)
	seriesID := task055ID(1001)
	gameID := task055ID(1002)
	secondGameID := task055ID(1005)
	firstID := task055ID(1003)
	secondID := task055ID(1004)
	baseTime := time.Date(2026, time.September, 1, 11, 0, 0, 0, time.UTC)

	game := task055GameResultHead(
		t, tournamentID, seriesID, gameID, firstID,
		domain.GameStateCompleted, domain.GameResultReasonSolved,
		task055UUIDPointer(firstID), baseTime.Add(time.Second), 1100,
	)
	gameProjection := task055ExactProjection(t, game.SourceProjection, "game-1100")
	secondGame := task055GameResultHead(
		t, tournamentID, seriesID, secondGameID, firstID,
		domain.GameStateCompleted, domain.GameResultReasonSolved,
		task055UUIDPointer(firstID), baseTime.Add(2*time.Second), 1110,
	)
	secondGameProjection := task055ExactProjection(t, secondGame.SourceProjection, "game-1110")
	score := task055ScoreHead(
		t, tournamentID, seriesID, firstID, secondID,
		domain.SeriesFormatBO3, domain.SeriesScore{FirstParticipantWins: 2},
		2, baseTime.Add(3*time.Second), 1120,
	)
	score.Attempts[0].CurrentGameResultRevisionID = game.ID
	score.Attempts[0].GameID = gameID
	score.Attempts[1].CurrentGameResultRevisionID = secondGame.ID
	score.Attempts[1].GameID = secondGameID
	score.CommandID = secondGame.CommandID
	commandAttempt := score.Attempts[1]
	score.CommandAttempt = &commandAttempt
	require.NoError(t, score.Validate())
	scoreProjection := task055ExactProjection(t, score.SourceProjection, "score-1120")
	series := task055SeriesResultHead(
		t, tournamentID, seriesID, score.ID,
		domain.SeriesStateCompleted, domain.SeriesResultReasonScoreComplete,
		task055UUIDPointer(firstID), baseTime.Add(5*time.Second), 1140,
	)
	seriesProjection := task055ExactProjection(t, series.SourceProjection, "series-1140")

	projections := make([]domain.ProjectionRevision, 0, 9)
	projections = append(projections, gameProjection, secondGameProjection, scoreProjection, seriesProjection)
	kinds := []domain.ArtifactKind{
		domain.ArtifactKindStandings,
		domain.ArtifactKindGoldenGroup,
		domain.ArtifactKindTopFour,
		domain.ArtifactKindBracket,
		domain.ArtifactKindChampion,
	}
	for index, kind := range kinds {
		projections = append(projections, task055Projection(
			t, 1160+index, tournamentID, kind, tournamentID,
			baseTime.Add(time.Duration(7+index)*time.Second), fmt.Sprintf("downstream-%d", index),
		))
	}
	dependencies := []domain.RevisionDependency{
		{SourceRevisionID: gameProjection.Revision().ID(), DerivedRevisionID: scoreProjection.Revision().ID()},
		{SourceRevisionID: secondGameProjection.Revision().ID(), DerivedRevisionID: scoreProjection.Revision().ID()},
		{SourceRevisionID: scoreProjection.Revision().ID(), DerivedRevisionID: seriesProjection.Revision().ID()},
	}
	for index := 4; index < len(projections); index++ {
		dependencies = append(dependencies, domain.RevisionDependency{
			SourceRevisionID:  projections[index-1].Revision().ID(),
			DerivedRevisionID: projections[index].Revision().ID(),
		})
	}
	graph, err := domain.NewRevisionGraph(projections, dependencies)
	require.NoError(t, err)

	return projection.RevisionDAGInput{
		Graph: graph,
		Results: []projection.OfficialResultProjectionInput{
			{TerminalSource: projection.TerminalResultSourcePlayed, Result: game, ResultProjection: gameProjection},
			{TerminalSource: projection.TerminalResultSourcePlayed, Result: secondGame, ResultProjection: secondGameProjection},
			{
				TerminalSource: projection.TerminalResultSourcePlayed,
				Result:         series, ResultProjection: seriesProjection,
				Score: task055ScoreHeadPointer(score), ScoreProjection: task055ProjectionPointer(scoreProjection),
			},
		},
	}
}

func task055SuccessorProjection(
	t *testing.T,
	previous domain.ProjectionRevision,
	id int,
	createdAt time.Time,
	payload string,
) domain.ProjectionRevision {
	t.Helper()
	previousRevision := previous.Revision()
	previousID := previousRevision.ID()
	projection, err := domain.NewProjectionRevision(
		domain.DerivedRevisionID(task055ID(id)), previousRevision.TournamentID(),
		previousRevision.Artifact(), previousRevision.RevisionNo()+1, &previousID,
		createdAt, []byte(payload),
	)
	require.NoError(t, err)
	return projection
}

func task055CurrentProjectionID(
	t *testing.T,
	graph domain.RevisionGraph,
	kind domain.ArtifactKind,
) domain.DerivedRevisionID {
	t.Helper()
	for _, projection := range graph.Projections() {
		if projection.Revision().Artifact().Kind == kind {
			return projection.Revision().ID()
		}
	}
	t.Fatalf("missing %s projection", kind)
	return domain.DerivedRevisionID{}
}
