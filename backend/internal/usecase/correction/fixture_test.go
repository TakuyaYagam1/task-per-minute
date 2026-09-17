package correction_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/revision"
)

func correctionDAGTestRevisionDAGFixture(t *testing.T) resultprojection.RevisionDAGInput {
	t.Helper()
	tournamentID := correctionDAGTestID(1000)
	seriesID := correctionDAGTestID(1001)
	gameID := correctionDAGTestID(1002)
	secondGameID := correctionDAGTestID(1005)
	firstID := correctionDAGTestID(1003)
	secondID := correctionDAGTestID(1004)
	baseTime := time.Date(2026, time.September, 1, 11, 0, 0, 0, time.UTC)

	game := correctionDAGTestGameResultHead(
		t, tournamentID, seriesID, gameID, domain.GameStateCompleted,
		domain.GameResultReasonSolved, correctionDAGTestUUIDPointer(firstID), baseTime.Add(time.Second), 1100,
	)
	gameProjection := correctionDAGTestExactProjection(t, game.SourceProjection, "game-1100")
	secondGame := correctionDAGTestGameResultHead(
		t, tournamentID, seriesID, secondGameID, domain.GameStateCompleted,
		domain.GameResultReasonSolved, correctionDAGTestUUIDPointer(firstID), baseTime.Add(2*time.Second), 1110,
	)
	secondGameProjection := correctionDAGTestExactProjection(t, secondGame.SourceProjection, "game-1110")
	score := correctionDAGTestScoreHead(
		t, tournamentID, seriesID, firstID, secondID, domain.SeriesFormatBO3,
		domain.SeriesScore{FirstParticipantWins: 2}, 2, baseTime.Add(3*time.Second), 1120,
	)
	score.Attempts[0].CurrentGameResultRevisionID = game.ID
	score.Attempts[0].GameID = gameID
	score.Attempts[1].CurrentGameResultRevisionID = secondGame.ID
	score.Attempts[1].GameID = secondGameID
	score.CommandID = secondGame.CommandID
	commandAttempt := score.Attempts[1]
	score.CommandAttempt = &commandAttempt
	require.NoError(t, score.Validate())
	scoreProjection := correctionDAGTestExactProjection(t, score.SourceProjection, "score-1120")
	series := correctionDAGTestSeriesResultHead(
		t, tournamentID, seriesID, score.ID, domain.SeriesStateCompleted,
		domain.SeriesResultReasonScoreComplete, correctionDAGTestUUIDPointer(firstID), baseTime.Add(5*time.Second), 1140,
	)
	seriesProjection := correctionDAGTestExactProjection(t, series.SourceProjection, "series-1140")

	projections := make([]domain.ProjectionRevision, 0, 9)
	projections = append(projections, gameProjection, secondGameProjection, scoreProjection, seriesProjection)
	for index, kind := range []domain.ArtifactKind{
		domain.ArtifactKindStandings,
		domain.ArtifactKindGoldenGroup,
		domain.ArtifactKindTopFour,
		domain.ArtifactKindBracket,
		domain.ArtifactKindChampion,
	} {
		projections = append(projections, correctionDAGTestProjection(
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
	return resultprojection.RevisionDAGInput{
		Graph: graph,
		Results: []resultprojection.OfficialResultProjectionInput{
			{TerminalSource: resultprojection.TerminalResultSourcePlayed, Result: game, ResultProjection: gameProjection},
			{TerminalSource: resultprojection.TerminalResultSourcePlayed, Result: secondGame, ResultProjection: secondGameProjection},
			{
				TerminalSource: resultprojection.TerminalResultSourcePlayed,
				Result:         series, ResultProjection: seriesProjection,
				Score: correctionDAGTestScoreHeadPointer(score), ScoreProjection: correctionDAGTestProjectionPointer(scoreProjection),
			},
		},
	}
}

func correctionDAGTestGameResultHead(
	t *testing.T,
	tournamentID, seriesID, gameID uuid.UUID,
	state domain.GameState,
	reason domain.GameResultReason,
	winnerID *uuid.UUID,
	recordedAt time.Time,
	idBase int,
) resultusecase.OfficialResultRevisionHead {
	t.Helper()
	source := correctionDAGTestProjection(
		t, idBase, tournamentID, domain.ArtifactKindGameResult, gameID,
		recordedAt.Add(-time.Minute), fmt.Sprintf("game-%d", idBase),
	).Revision()
	head := resultusecase.OfficialResultRevisionHead{
		Scope: resultusecase.OfficialResultScope{
			TournamentID: tournamentID, SeriesID: seriesID, GameID: gameID,
			Kind: resultusecase.OfficialResultSubjectGame,
		},
		ID:        domain.OfficialResultRevisionID(correctionDAGTestID(idBase + 1)),
		Ordinal:   1,
		CommandID: correctionDAGTestID(idBase + 2),
		Actor:     domain.ResultActor{Kind: domain.ResultActorServer},
		Outcome: resultusecase.OfficialResultOutcome{
			GameState: state, GameReason: reason, WinnerID: correctionDAGTestUUIDPointerOrNil(winnerID),
		},
		SourceProjection: source,
		RecordedAt:       recordedAt,
	}
	require.NoError(t, head.Validate())
	return head
}

func correctionDAGTestSeriesResultHead(
	t *testing.T,
	tournamentID, seriesID uuid.UUID,
	scoreID domain.SeriesScoreRevisionID,
	state domain.SeriesState,
	reason domain.SeriesResultReason,
	winnerID *uuid.UUID,
	recordedAt time.Time,
	idBase int,
) resultusecase.OfficialResultRevisionHead {
	t.Helper()
	source := correctionDAGTestProjection(
		t, idBase, tournamentID, domain.ArtifactKindSeriesResult, seriesID,
		recordedAt.Add(-time.Minute), fmt.Sprintf("series-%d", idBase),
	).Revision()
	head := resultusecase.OfficialResultRevisionHead{
		Scope: resultusecase.OfficialResultScope{
			TournamentID: tournamentID, SeriesID: seriesID, Kind: resultusecase.OfficialResultSubjectSeries,
		},
		ID:        domain.OfficialResultRevisionID(correctionDAGTestID(idBase + 1)),
		Ordinal:   1,
		CommandID: correctionDAGTestID(idBase + 2),
		Actor:     domain.ResultActor{Kind: domain.ResultActorServer},
		Outcome: resultusecase.OfficialResultOutcome{
			SeriesState: state, SeriesReason: reason, WinnerID: correctionDAGTestUUIDPointerOrNil(winnerID),
			ScoreRevisionID: correctionDAGTestScoreIDPointer(scoreID),
		},
		SourceProjection: source,
		RecordedAt:       recordedAt,
	}
	require.NoError(t, head.Validate())
	return head
}

func correctionDAGTestScoreHead(
	t *testing.T,
	tournamentID, seriesID, firstID, secondID uuid.UUID,
	format domain.SeriesFormat,
	score domain.SeriesScore,
	attemptCount int,
	recordedAt time.Time,
	idBase int,
) resultusecase.SeriesScoreRevisionHead {
	t.Helper()
	source := correctionDAGTestProjection(
		t, idBase, tournamentID, domain.ArtifactKindSeriesScore, seriesID,
		recordedAt.Add(-time.Minute), fmt.Sprintf("score-%d", idBase),
	).Revision()
	head := resultusecase.SeriesScoreRevisionHead{
		Scope: resultusecase.SeriesScoreRevisionScope{TournamentID: tournamentID, SeriesID: seriesID},
		ID:    domain.SeriesScoreRevisionID(correctionDAGTestID(idBase + 1)), Ordinal: 1,
		Operation: resultusecase.SeriesScoreRevisionOperationInitialize,
		CommandID: correctionDAGTestID(idBase + 2), Actor: domain.ResultActor{Kind: domain.ResultActorServer},
		FirstParticipantID: firstID, SecondParticipantID: secondID,
		Format: format, Score: score, SourceProjection: source, RecordedAt: recordedAt,
	}
	if attemptCount > 0 {
		head.Ordinal = 2
		previous := domain.SeriesScoreRevisionID(correctionDAGTestID(idBase + 3))
		head.PreviousRevisionID = &previous
		head.Operation = resultusecase.SeriesScoreRevisionOperationAppendAttempt
		for index := 0; index < attemptCount; index++ {
			winner := firstID
			if score.SecondParticipantWins > 0 {
				winner = secondID
			}
			head.Attempts = append(head.Attempts, resultusecase.SeriesScoreAttemptReference{
				SlotID: correctionDAGTestID(idBase + 10 + index), SlotPosition: index + 1,
				GameID: correctionDAGTestID(idBase + 20 + index), AttemptNo: 1,
				State: domain.GameStateCompleted, WinnerID: correctionDAGTestUUIDPointer(winner),
				Reason:                      domain.GameResultReasonSolved,
				CurrentGameResultRevisionID: domain.OfficialResultRevisionID(correctionDAGTestID(idBase + 30 + index)),
			})
		}
		commandAttempt := head.Attempts[len(head.Attempts)-1]
		head.CommandAttempt = &commandAttempt
	}
	require.NoError(t, head.Validate())
	return head
}

func correctionDAGTestProjection(
	t *testing.T,
	id int,
	tournamentID uuid.UUID,
	kind domain.ArtifactKind,
	entityID uuid.UUID,
	createdAt time.Time,
	payload string,
) domain.ProjectionRevision {
	t.Helper()
	projection, err := domain.NewProjectionRevision(
		domain.DerivedRevisionID(correctionDAGTestID(id)), tournamentID,
		domain.ArtifactRef{Kind: kind, EntityID: entityID}, 1, nil, createdAt, []byte(payload),
	)
	require.NoError(t, err)
	return projection
}

func correctionDAGTestExactProjection(
	t *testing.T,
	revision domain.DerivedRevision,
	payload string,
) domain.ProjectionRevision {
	t.Helper()
	projection, err := domain.NewProjectionRevision(
		revision.ID(), revision.TournamentID(), revision.Artifact(), revision.RevisionNo(),
		revision.PreviousRevisionID(), revision.CreatedAt(), []byte(payload),
	)
	require.NoError(t, err)
	require.Equal(t, revision.PayloadDigest(), projection.Revision().PayloadDigest())
	return projection
}

func correctionDAGTestSuccessorProjection(
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
		domain.DerivedRevisionID(correctionDAGTestID(id)), previousRevision.TournamentID(),
		previousRevision.Artifact(), previousRevision.RevisionNo()+1, &previousID,
		createdAt, []byte(payload),
	)
	require.NoError(t, err)
	return projection
}

func correctionDAGTestCurrentProjectionID(
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

func correctionDAGTestID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("55000000-0000-0000-0000-%012x", value))
}

func correctionDAGTestUUIDPointer(value uuid.UUID) *uuid.UUID {
	return &value
}

func correctionDAGTestUUIDPointerOrNil(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func correctionDAGTestScoreIDPointer(value domain.SeriesScoreRevisionID) *domain.SeriesScoreRevisionID {
	return &value
}

func correctionDAGTestScoreHeadPointer(value resultusecase.SeriesScoreRevisionHead) *resultusecase.SeriesScoreRevisionHead {
	clone := value.Clone()
	return &clone
}

func correctionDAGTestProjectionPointer(value domain.ProjectionRevision) *domain.ProjectionRevision {
	clone, err := domain.NewProjectionRevision(
		value.Revision().ID(), value.Revision().TournamentID(), value.Revision().Artifact(),
		value.Revision().RevisionNo(), value.Revision().PreviousRevisionID(),
		value.Revision().CreatedAt(), value.Payload(),
	)
	if err != nil {
		panic(err)
	}
	return &clone
}
