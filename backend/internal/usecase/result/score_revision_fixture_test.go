package result

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type task053ScoreFixture struct {
	command            SeriesScoreRevisionCommand
	authority          SeriesScoreRevisionAuthority
	current            SeriesScoreRevisionHead
	recordedAt         time.Time
	firstParticipantID uuid.UUID
}

func task053AppendScoreFixture(t *testing.T, state domain.GameState) task053ScoreFixture {
	t.Helper()
	tournamentID := task053PlannerID(101)
	seriesID := task053PlannerID(102)
	firstID := task053PlannerID(103)
	secondID := task053PlannerID(104)
	initialScoreID := domain.SeriesScoreRevisionID(task053PlannerID(105))
	recordedAt := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	source := task053Projection(t, task053PlannerID(106), tournamentID,
		domain.ArtifactKindSeriesScore, seriesID, 1, nil, "initial-score")
	persisted := domain.Series{
		ID:                     seriesID,
		TournamentID:           tournamentID,
		FirstParticipantID:     firstID,
		SecondParticipantID:    secondID,
		Format:                 domain.SeriesFormatBO3,
		State:                  domain.SeriesStateLocked,
		Score:                  domain.SeriesScore{},
		CurrentScoreRevisionID: seriesScoreRevisionIDPointer(initialScoreID),
		Slots: []domain.GameSlot{{
			ID:          task053PlannerID(150),
			SeriesID:    seriesID,
			Position:    1,
			Category:    domain.CategoryWeb,
			ScoreBefore: domain.SeriesScore{},
			Attempts: []domain.Game{{
				ID:        task053PlannerID(151),
				SlotID:    task053PlannerID(150),
				AttemptNo: 1,
				State:     domain.GameStatePlanned,
			}},
		}},
	}
	current := SeriesScoreRevisionHead{
		Scope: SeriesScoreRevisionScope{TournamentID: tournamentID, SeriesID: seriesID},
		ID:    initialScoreID, Ordinal: 1, Operation: SeriesScoreRevisionOperationInitialize,
		CommandID: task053PlannerID(107), Actor: domain.ResultActor{Kind: domain.ResultActorServer},
		FirstParticipantID: firstID, SecondParticipantID: secondID, Format: domain.SeriesFormatBO3,
		Score: domain.SeriesScore{}, Attempts: []SeriesScoreAttemptReference{}, SourceProjection: source, RecordedAt: recordedAt,
	}
	require.NoError(t, current.Validate())
	projected := cloneSeries(persisted)
	slotID := projected.Slots[0].ID
	gameID := projected.Slots[0].Attempts[0].ID
	gameResultID := domain.OfficialResultRevisionID(task053PlannerID(152))
	projected.State = domain.SeriesStateActive
	projected.CurrentScoreRevisionID = seriesScoreRevisionIDPointer(
		domain.SeriesScoreRevisionID(task053PlannerID(153)),
	)
	game := &projected.Slots[0].Attempts[0]
	game.State = state
	game.ResultRevisionID = officialResultRevisionIDPointer(gameResultID)
	attempt := SeriesScoreAttemptReference{
		SlotID:                      slotID,
		SlotPosition:                1,
		GameID:                      gameID,
		AttemptNo:                   1,
		State:                       state,
		CurrentGameResultRevisionID: gameResultID,
	}
	if state == domain.GameStateCompleted {
		game.ResultReason = domain.GameResultReasonSolved
		game.WinnerID = task053UUIDPointer(firstID)
		attempt.Reason = domain.GameResultReasonSolved
		attempt.WinnerID = task053UUIDPointer(firstID)
		projected.Score.FirstParticipantWins = 1
	} else {
		game.ResultReason = domain.GameResultReasonTaskFailure
		attempt.Reason = domain.GameResultReasonTaskFailure
	}
	previousSourceID := current.SourceProjection.ID()
	source = task053Projection(t, task053PlannerID(154), projected.TournamentID,
		domain.ArtifactKindSeriesScore, projected.ID, 2, &previousSourceID, "score-append")
	command := SeriesScoreRevisionCommand{
		Scope:                     current.Scope,
		Operation:                 SeriesScoreRevisionOperationAppendAttempt,
		CommandID:                 task053PlannerID(155),
		RevisionID:                *projected.CurrentScoreRevisionID,
		Actor:                     domain.ResultActor{Kind: domain.ResultActorServer},
		ExpectedCurrentRevisionID: seriesScoreRevisionIDPointer(current.ID),
		ExpectedSourceProjection:  source,
		Attempt:                   &attempt,
	}
	return task053ScoreFixture{
		command: command,
		authority: SeriesScoreRevisionAuthority{
			Scope:            command.Scope,
			PersistedSeries:  persisted,
			ProjectedSeries:  projected,
			SourceProjection: source,
			CurrentHead:      &current,
			SeriesRevision:   2,
			AttemptRevision:  1,
		},
		current:            current,
		recordedAt:         recordedAt.Add(time.Minute),
		firstParticipantID: firstID,
	}
}

func task053ReplaceScoreFixture(t *testing.T) task053ScoreFixture {
	t.Helper()
	appendFixture := task053AppendScoreFixture(t, domain.GameStateCompleted)
	secondPlan, err := PlanSeriesScoreRevision(
		appendFixture.command,
		appendFixture.authority,
		appendFixture.recordedAt,
	)
	require.NoError(t, err)
	current := secondPlan.Revision().Head()
	persisted := cloneSeries(appendFixture.authority.ProjectedSeries)
	projected := cloneSeries(persisted)
	revisionID := domain.SeriesScoreRevisionID(task053PlannerID(160))
	gameResultID := domain.OfficialResultRevisionID(task053PlannerID(161))
	projected.CurrentScoreRevisionID = &revisionID
	projected.Score = domain.SeriesScore{}
	game := &projected.Slots[0].Attempts[0]
	game.State = domain.GameStateVoid
	game.ResultReason = domain.GameResultReasonTaskFailure
	game.WinnerID = nil
	game.ResultRevisionID = officialResultRevisionIDPointer(gameResultID)
	attempt := SeriesScoreAttemptReference{
		SlotID:                      game.SlotID,
		SlotPosition:                1,
		GameID:                      game.ID,
		AttemptNo:                   game.AttemptNo,
		State:                       game.State,
		Reason:                      game.ResultReason,
		CurrentGameResultRevisionID: gameResultID,
	}
	previousSourceID := current.SourceProjection.ID()
	source := task053Projection(t, task053PlannerID(162), projected.TournamentID,
		domain.ArtifactKindSeriesScore, projected.ID, 3, &previousSourceID, "score-replace")
	command := SeriesScoreRevisionCommand{
		Scope:                     appendFixture.command.Scope,
		Operation:                 SeriesScoreRevisionOperationReplaceResult,
		CommandID:                 task053PlannerID(163),
		RevisionID:                revisionID,
		Actor:                     domain.ResultActor{Kind: domain.ResultActorOperator, PrincipalID: task053UUIDPointer(task053PlannerID(164))},
		ExpectedCurrentRevisionID: seriesScoreRevisionIDPointer(current.ID),
		ExpectedSourceProjection:  source,
		Attempt:                   &attempt,
	}
	return task053ScoreFixture{
		command: command,
		authority: SeriesScoreRevisionAuthority{
			Scope:            command.Scope,
			PersistedSeries:  persisted,
			ProjectedSeries:  projected,
			SourceProjection: source,
			CurrentHead:      &current,
			SeriesRevision:   3,
			AttemptRevision:  2,
		},
		current:            current,
		recordedAt:         appendFixture.recordedAt.Add(time.Minute),
		firstParticipantID: appendFixture.firstParticipantID,
	}
}

func seriesScoreRevisionIDPointer(
	value domain.SeriesScoreRevisionID,
) *domain.SeriesScoreRevisionID {
	clone := value
	return &clone
}

func task053ScoreHeadPointer(value SeriesScoreRevisionHead) *SeriesScoreRevisionHead {
	clone := value.Clone()
	return &clone
}

func cloneScorePlannerCommand(command SeriesScoreRevisionCommand) SeriesScoreRevisionCommand {
	clone := command
	clone.Actor = cloneResultActor(command.Actor)
	clone.ExpectedCurrentRevisionID = cloneSeriesScoreRevisionIDPointer(command.ExpectedCurrentRevisionID)
	clone.Attempt = cloneSeriesScoreAttemptReferencePointer(command.Attempt)
	return clone
}

func cloneScorePlannerAuthority(
	authority SeriesScoreRevisionAuthority,
) SeriesScoreRevisionAuthority {
	clone := authority
	clone.PersistedSeries = cloneSeries(authority.PersistedSeries)
	clone.ProjectedSeries = cloneSeries(authority.ProjectedSeries)
	if authority.CurrentHead != nil {
		current := authority.CurrentHead.Clone()
		clone.CurrentHead = &current
	}
	return clone
}
