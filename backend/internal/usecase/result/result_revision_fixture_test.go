package result

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type task053OfficialFixture struct {
	command             OfficialResultRevisionCommand
	authority           OfficialResultRevisionAuthority
	recordedAt          time.Time
	firstParticipantID  uuid.UUID
	secondParticipantID uuid.UUID
}

func task053GameResultFixture(t *testing.T) task053OfficialFixture {
	t.Helper()
	tournamentID := task053PlannerID(1)
	seriesID := task053PlannerID(2)
	firstID := task053PlannerID(3)
	secondID := task053PlannerID(4)
	slotID := task053PlannerID(5)
	gameID := task053PlannerID(6)
	revisionID := domain.OfficialResultRevisionID(task053PlannerID(7))
	scope := OfficialResultScope{
		TournamentID: tournamentID,
		SeriesID:     seriesID,
		GameID:       gameID,
		Kind:         OfficialResultSubjectGame,
	}
	persisted := domain.Series{
		ID:                  seriesID,
		TournamentID:        tournamentID,
		FirstParticipantID:  firstID,
		SecondParticipantID: secondID,
		Format:              domain.SeriesFormatBO1,
		State:               domain.SeriesStateActive,
		Score:               domain.SeriesScore{},
		Slots: []domain.GameSlot{{
			ID:          slotID,
			SeriesID:    seriesID,
			Position:    1,
			Category:    domain.CategoryWeb,
			ScoreBefore: domain.SeriesScore{},
			Attempts: []domain.Game{{
				ID:        gameID,
				SlotID:    slotID,
				AttemptNo: 1,
				State:     domain.GameStateActive,
			}},
		}},
	}
	projected := cloneSeries(persisted)
	projectedGame := &projected.Slots[0].Attempts[0]
	projectedGame.State = domain.GameStateCompleted
	projectedGame.ResultReason = domain.GameResultReasonSolved
	projectedGame.WinnerID = task053UUIDPointer(firstID)
	projectedGame.ResultRevisionID = officialResultRevisionIDPointer(revisionID)
	source := task053Projection(t, task053PlannerID(8), tournamentID,
		domain.ArtifactKindGameResult, gameID, 1, nil, "game-result")
	outcome := OfficialResultOutcome{
		GameState:  domain.GameStateCompleted,
		GameReason: domain.GameResultReasonSolved,
		WinnerID:   task053UUIDPointer(firstID),
	}
	return task053OfficialFixture{
		command: OfficialResultRevisionCommand{
			Scope:                    scope,
			CommandID:                task053PlannerID(9),
			RevisionID:               revisionID,
			Actor:                    domain.ResultActor{Kind: domain.ResultActorServer},
			ExpectedSourceProjection: source,
			Outcome:                  outcome,
		},
		authority: OfficialResultRevisionAuthority{
			Scope:            scope,
			PersistedSeries:  persisted,
			ProjectedSeries:  projected,
			SourceProjection: source,
			SeriesRevision:   1,
			AttemptRevision:  1,
		},
		recordedAt:          time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC),
		firstParticipantID:  firstID,
		secondParticipantID: secondID,
	}
}

func task053SeriesResultFixture(t *testing.T) task053OfficialFixture {
	t.Helper()
	fixture := task053GameResultFixture(t)
	seriesID := fixture.authority.PersistedSeries.ID
	tournamentID := fixture.authority.PersistedSeries.TournamentID
	scoreRevisionID := domain.SeriesScoreRevisionID(task053PlannerID(60))
	gameRevisionID := domain.OfficialResultRevisionID(task053PlannerID(61))
	resultRevisionID := domain.OfficialResultRevisionID(task053PlannerID(62))
	persisted := cloneSeries(fixture.authority.ProjectedSeries)
	persisted.Score = domain.SeriesScore{FirstParticipantWins: 1}
	persisted.CurrentScoreRevisionID = &scoreRevisionID
	persisted.Slots[0].Attempts[0].ResultRevisionID = &gameRevisionID
	projected := cloneSeries(persisted)
	projected.State = domain.SeriesStateCompleted
	projected.WinnerID = task053UUIDPointer(fixture.firstParticipantID)
	projected.CurrentResultRevisionID = &resultRevisionID
	scope := OfficialResultScope{
		TournamentID: tournamentID,
		SeriesID:     seriesID,
		Kind:         OfficialResultSubjectSeries,
	}
	source := task053Projection(t, task053PlannerID(63), tournamentID,
		domain.ArtifactKindSeriesResult, seriesID, 1, nil, "series-result")
	outcome := OfficialResultOutcome{
		SeriesState:     domain.SeriesStateCompleted,
		SeriesReason:    domain.SeriesResultReasonScoreComplete,
		WinnerID:        task053UUIDPointer(fixture.firstParticipantID),
		ScoreRevisionID: &scoreRevisionID,
	}
	return task053OfficialFixture{
		command: OfficialResultRevisionCommand{
			Scope:                    scope,
			CommandID:                task053PlannerID(64),
			RevisionID:               resultRevisionID,
			Actor:                    domain.ResultActor{Kind: domain.ResultActorServer},
			ExpectedSourceProjection: source,
			Outcome:                  outcome,
		},
		authority: OfficialResultRevisionAuthority{
			Scope:                 scope,
			PersistedSeries:       persisted,
			ProjectedSeries:       projected,
			ProjectedSeriesReason: domain.SeriesResultReasonScoreComplete,
			SourceProjection:      source,
			SeriesRevision:        1,
		},
		recordedAt:          fixture.recordedAt,
		firstParticipantID:  fixture.firstParticipantID,
		secondParticipantID: fixture.secondParticipantID,
	}
}

func task053GameCorrectionFixture(t *testing.T) task053OfficialFixture {
	t.Helper()
	fixture := task053GameResultFixture(t)
	first, err := PlanOfficialResultRevision(fixture.command, fixture.authority, fixture.recordedAt)
	require.NoError(t, err)
	current := first.Revision().Head()
	fixture.authority.PersistedSeries = cloneSeries(fixture.authority.ProjectedSeries)
	fixture.authority.ProjectedSeries = cloneSeries(fixture.authority.PersistedSeries)
	fixture.authority.CurrentHead = &current
	fixture.authority.SeriesRevision = 2
	fixture.authority.AttemptRevision = 2
	fixture.command.CommandID = task053PlannerID(81)
	fixture.command.RevisionID = domain.OfficialResultRevisionID(task053PlannerID(82))
	fixture.command.ExpectedCurrentRevisionID = officialResultRevisionIDPointer(current.ID)
	operatorID := task053PlannerID(83)
	fixture.command.Actor = domain.ResultActor{Kind: domain.ResultActorOperator, PrincipalID: &operatorID}
	fixture.command.Outcome.GameReason = domain.GameResultReasonOperatorForfeit
	fixture.command.Outcome.WinnerID = task053UUIDPointer(fixture.secondParticipantID)
	projectedGame := &fixture.authority.ProjectedSeries.Slots[0].Attempts[0]
	projectedGame.ResultReason = fixture.command.Outcome.GameReason
	projectedGame.WinnerID = task053UUIDPointer(fixture.secondParticipantID)
	projectedGame.ResultRevisionID = officialResultRevisionIDPointer(fixture.command.RevisionID)
	previousSourceID := current.SourceProjection.ID()
	fixture.authority.SourceProjection = task053Projection(t, task053PlannerID(84), fixture.command.Scope.TournamentID,
		domain.ArtifactKindGameResult, fixture.command.Scope.GameID, 2, &previousSourceID, "game-correction")
	fixture.command.ExpectedSourceProjection = fixture.authority.SourceProjection
	fixture.recordedAt = fixture.recordedAt.Add(time.Minute)
	return fixture
}

func task053PlannerID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", number))
}

func task053Projection(
	t *testing.T,
	id uuid.UUID,
	tournamentID uuid.UUID,
	kind domain.ArtifactKind,
	entityID uuid.UUID,
	revisionNo int,
	previous *domain.DerivedRevisionID,
	payload string,
) domain.DerivedRevision {
	t.Helper()
	projection, err := domain.NewProjectionRevision(
		domain.DerivedRevisionID(id),
		tournamentID,
		domain.ArtifactRef{Kind: kind, EntityID: entityID},
		revisionNo,
		previous,
		time.Date(2026, time.August, 31, 11, revisionNo, 0, 0, time.UTC),
		[]byte(payload),
	)
	require.NoError(t, err)
	return projection.Revision()
}

func task053UUIDPointer(value uuid.UUID) *uuid.UUID {
	clone := value
	return &clone
}

func officialResultRevisionIDPointer(
	value domain.OfficialResultRevisionID,
) *domain.OfficialResultRevisionID {
	clone := value
	return &clone
}

func cloneOfficialPlannerCommand(command OfficialResultRevisionCommand) OfficialResultRevisionCommand {
	clone := command
	clone.Actor = cloneResultActor(command.Actor)
	clone.ExpectedCurrentRevisionID = cloneOfficialResultRevisionIDPointer(command.ExpectedCurrentRevisionID)
	clone.Outcome = cloneOfficialResultOutcome(command.Outcome)
	return clone
}

func cloneOfficialPlannerAuthority(
	authority OfficialResultRevisionAuthority,
) OfficialResultRevisionAuthority {
	clone := authority
	clone.PersistedSeries = cloneSeries(authority.PersistedSeries)
	clone.ProjectedSeries = cloneSeries(authority.ProjectedSeries)
	if authority.CurrentHead != nil {
		current := authority.CurrentHead.Clone()
		clone.CurrentHead = &current
	}
	return clone
}
