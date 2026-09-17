package revision_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/revision"
)

func task055GameResultHead(
	t *testing.T,
	tournamentID, seriesID, gameID, winnerParticipantID uuid.UUID,
	state domain.GameState,
	reason domain.GameResultReason,
	winnerID *uuid.UUID,
	recordedAt time.Time,
	idBase int,
) resultusecase.OfficialResultRevisionHead {
	t.Helper()
	source := task055Projection(
		t, idBase, tournamentID, domain.ArtifactKindGameResult, gameID,
		recordedAt.Add(-time.Minute), fmt.Sprintf("game-%d", idBase),
	).Revision()
	head := resultusecase.OfficialResultRevisionHead{
		Scope: resultusecase.OfficialResultScope{
			TournamentID: tournamentID, SeriesID: seriesID, GameID: gameID,
			Kind: resultusecase.OfficialResultSubjectGame,
		},
		ID:        domain.OfficialResultRevisionID(task055ID(idBase + 1)),
		Ordinal:   1,
		CommandID: task055ID(idBase + 2),
		Actor:     domain.ResultActor{Kind: domain.ResultActorServer},
		Outcome: resultusecase.OfficialResultOutcome{
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
	scoreID domain.SeriesScoreRevisionID,
	state domain.SeriesState,
	reason domain.SeriesResultReason,
	winnerID *uuid.UUID,
	recordedAt time.Time,
	idBase int,
) resultusecase.OfficialResultRevisionHead {
	t.Helper()
	source := task055Projection(
		t, idBase, tournamentID, domain.ArtifactKindSeriesResult, seriesID,
		recordedAt.Add(-time.Minute), fmt.Sprintf("series-%d", idBase),
	).Revision()
	head := resultusecase.OfficialResultRevisionHead{
		Scope: resultusecase.OfficialResultScope{
			TournamentID: tournamentID, SeriesID: seriesID, Kind: resultusecase.OfficialResultSubjectSeries,
		},
		ID:        domain.OfficialResultRevisionID(task055ID(idBase + 1)),
		Ordinal:   1,
		CommandID: task055ID(idBase + 2),
		Actor:     domain.ResultActor{Kind: domain.ResultActorServer},
		Outcome: resultusecase.OfficialResultOutcome{
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
	format domain.SeriesFormat,
	score domain.SeriesScore,
	attemptCount int,
	recordedAt time.Time,
	idBase int,
) resultusecase.SeriesScoreRevisionHead {
	t.Helper()
	source := task055Projection(
		t, idBase, tournamentID, domain.ArtifactKindSeriesScore, seriesID,
		recordedAt.Add(-time.Minute), fmt.Sprintf("score-%d", idBase),
	).Revision()
	head := resultusecase.SeriesScoreRevisionHead{
		Scope:   resultusecase.SeriesScoreRevisionScope{TournamentID: tournamentID, SeriesID: seriesID},
		ID:      domain.SeriesScoreRevisionID(task055ID(idBase + 1)),
		Ordinal: 1, Operation: resultusecase.SeriesScoreRevisionOperationInitialize,
		CommandID: idForTask055(idBase + 2), Actor: domain.ResultActor{Kind: domain.ResultActorServer},
		FirstParticipantID: firstID, SecondParticipantID: secondID,
		Format: format, Score: score, SourceProjection: source, RecordedAt: recordedAt,
	}
	if attemptCount > 0 {
		head.Ordinal = 2
		previous := domain.SeriesScoreRevisionID(task055ID(idBase + 3))
		head.PreviousRevisionID = &previous
		head.Operation = resultusecase.SeriesScoreRevisionOperationAppendAttempt
		head.Attempts = make([]resultusecase.SeriesScoreAttemptReference, 0, attemptCount)
		for index := 0; index < attemptCount; index++ {
			winner := firstID
			if score.SecondParticipantWins > 0 {
				winner = secondID
			}
			reference := resultusecase.SeriesScoreAttemptReference{
				SlotID: task055ID(idBase + 10 + index), SlotPosition: index + 1,
				GameID: task055ID(idBase + 20 + index), AttemptNo: 1,
				State: domain.GameStateCompleted, WinnerID: task055UUIDPointer(winner),
				Reason:                      domain.GameResultReasonSolved,
				CurrentGameResultRevisionID: domain.OfficialResultRevisionID(task055ID(idBase + 30 + index)),
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
	kind domain.ArtifactKind,
	entityID uuid.UUID,
	createdAt time.Time,
	payload string,
) domain.ProjectionRevision {
	t.Helper()
	projection, err := domain.NewProjectionRevision(
		domain.DerivedRevisionID(task055ID(id)), tournamentID,
		domain.ArtifactRef{Kind: kind, EntityID: entityID},
		1, nil, createdAt, []byte(payload),
	)
	require.NoError(t, err)
	return projection
}

func task055ExactProjection(
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

func task055ProjectionWithPayload(
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
	return projection
}

func task055NoGameEvidence(
	t *testing.T,
	format domain.SeriesFormat,
	recordedAt time.Time,
	idBase int,
) projection.RecordedNoGameResult {
	t.Helper()
	return task055NoGameEvidenceWithReadiness(t, format, recordedAt, idBase, true, false)
}

func task055NoGameEvidenceWithReadiness(
	t *testing.T,
	format domain.SeriesFormat,
	recordedAt time.Time,
	idBase int,
	firstReady bool,
	secondReady bool,
) projection.RecordedNoGameResult {
	t.Helper()
	tournamentID := task055NoShowID(idBase + 100)
	seriesID := task055NoShowID(idBase + 101)
	firstParticipantID := task055NoShowID(idBase + 102)
	secondParticipantID := task055NoShowID(idBase + 103)
	gameID := task055NoShowID(idBase + 104)
	gameResultID := domain.OfficialResultRevisionID(task055ID(idBase + 1))
	scoreRevisionID := domain.SeriesScoreRevisionID(task055ID(idBase + 2))
	seriesResultID := domain.OfficialResultRevisionID(task055ID(idBase + 3))
	action := domain.NormalNoShowActionPauseWave
	seriesState := domain.SeriesStateCancelled
	score := domain.SeriesScore{}
	var readyParticipantID *uuid.UUID
	if firstReady != secondReady {
		action = domain.NormalNoShowActionReopenWave
		seriesState = domain.SeriesStateCompleted
		winnerID := firstParticipantID
		if secondReady {
			winnerID = secondParticipantID
			score.SecondParticipantWins = format.WinsRequired()
		} else {
			score.FirstParticipantWins = format.WinsRequired()
		}
		readyParticipantID = &winnerID
	}
	scope := domain.NormalNoShowScope{
		TournamentID: tournamentID,
		WaveID:       task055NoShowID(idBase + 105),
		WindowID:     task055NoShowID(idBase + 106),
		SeriesID:     seriesID,
	}
	gameResults := []domain.NormalNoShowGameRevision{{
		Ordinal: 1, ID: gameResultID, GameID: gameID,
		State: domain.GameStateCancelled, Reason: domain.GameResultReasonSeriesCancelled,
		RecordedAt: recordedAt,
	}}
	scoreRevision := domain.NormalNoShowScoreRevision{
		Ordinal: 2, ID: scoreRevisionID, SeriesID: seriesID, Score: score,
		GameResultRevisionIDs: []domain.OfficialResultRevisionID{gameResultID},
		RecordedAt:            recordedAt,
	}
	seriesRevision := domain.NormalNoShowSeriesRevision{
		Ordinal: 3, ID: seriesResultID, SeriesID: seriesID, State: seriesState,
		WinnerID: task055UUIDPointerOrNil(readyParticipantID), ScoreRevisionID: scoreRevisionID,
		RecordedAt: recordedAt,
	}
	scoreProjection := task055Projection(
		t, idBase+10, tournamentID, domain.ArtifactKindSeriesScore, seriesID,
		recordedAt.Add(-2*time.Second), fmt.Sprintf("no-game-score-%d", idBase),
	)
	resultProjection := task055Projection(
		t, idBase+11, tournamentID, domain.ArtifactKindSeriesResult, seriesID,
		recordedAt.Add(-time.Second), fmt.Sprintf("no-game-result-%d", idBase),
	)
	gameProjections := make([]domain.ProjectionRevision, len(gameResults))
	topology := make([]projection.RecordedNoGameAttempt, len(gameResults))
	for index, game := range gameResults {
		gameProjections[index] = task055Projection(
			t, idBase+12+index, tournamentID, domain.ArtifactKindGameResult,
			game.GameID, recordedAt.Add(-3*time.Second), fmt.Sprintf("no-game-%d-%d", idBase, index),
		)
		topology[index] = projection.RecordedNoGameAttempt{
			SeriesID: seriesID, SlotID: task055NoShowID(idBase + 107 + index), SlotPosition: index + 1,
			GameID: game.GameID, AttemptNo: 1, ResultRevisionID: game.ID,
		}
	}
	return projection.RecordedNoGameResult{
		Scope: scope, CommandID: task055NoShowID(idBase + 108), Action: action,
		Format:              format,
		FirstParticipantID:  firstParticipantID,
		SecondParticipantID: secondParticipantID,
		ReadyParticipantID:  task055UUIDPointerOrNil(readyParticipantID),
		GameResults:         gameResults, Topology: topology, Score: scoreRevision,
		Series: seriesRevision,
		GameSourceRevisions: func() []domain.DerivedRevision {
			revisions := make([]domain.DerivedRevision, len(gameProjections))
			for index := range gameProjections {
				revisions[index] = gameProjections[index].Revision()
			}
			return revisions
		}(),
		ScoreSourceRevision:  scoreProjection.Revision(),
		ResultSourceRevision: resultProjection.Revision(),
		GameProjections:      gameProjections,
		GameDependencies: func() []domain.RevisionDependency {
			dependencies := make([]domain.RevisionDependency, len(gameProjections))
			for index := range gameProjections {
				dependencies[index] = domain.RevisionDependency{
					SourceRevisionID:  gameProjections[index].Revision().ID(),
					DerivedRevisionID: scoreProjection.Revision().ID(),
				}
			}
			return dependencies
		}(),
		ScoreProjection: scoreProjection, ResultProjection: resultProjection,
		ResultDependency: domain.RevisionDependency{
			SourceRevisionID:  scoreProjection.Revision().ID(),
			DerivedRevisionID: resultProjection.Revision().ID(),
		},
		ResolvedAt: recordedAt,
	}
}

func task055ID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("55000000-0000-0000-0000-%012x", value))
}

func task055NoShowID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("36000000-0000-0000-0000-%012x", value))
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

func task055ScoreIDPointer(value domain.SeriesScoreRevisionID) *domain.SeriesScoreRevisionID {
	return &value
}

func task055ScoreHeadPointer(value resultusecase.SeriesScoreRevisionHead) *resultusecase.SeriesScoreRevisionHead {
	clone := value.Clone()
	return &clone
}

func task055ProjectionPointer(value domain.ProjectionRevision) *domain.ProjectionRevision {
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
