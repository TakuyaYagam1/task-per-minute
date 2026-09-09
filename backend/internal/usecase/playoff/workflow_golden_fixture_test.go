package playoff_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

func newGoldenPositionEvidence(
	t *testing.T,
	fixture playoffFixture,
	ordered []uuid.UUID,
) playoff.GoldenPositionEvidence {
	t.Helper()
	return newGoldenPositionEvidenceForGroup(t, fixture, 0, ordered, len(ordered))
}

func newGoldenPositionEvidenceForGroup(
	t *testing.T,
	fixture playoffFixture,
	groupIndex int,
	ordered []uuid.UUID,
	count int,
) playoff.GoldenPositionEvidence {
	t.Helper()
	require.GreaterOrEqual(t, len(ordered), count)
	require.Less(t, groupIndex, len(fixture.command.GoldenGroups))
	group := fixture.command.GoldenGroups[groupIndex]
	scope := goldenusecase.GoldenStateScope{
		TournamentID: fixture.command.TournamentID,
		GroupID:      group.GroupID, GroupRevisionID: group.RevisionID,
	}
	idOffset := groupIndex * 100
	attemptID := playoffID(730 + idOffset)
	previousRevisionID := playoffID(737 + idOffset)
	digestBytes, err := hex.DecodeString("7664eb81ca8bb9a43a74bec2c887bcb702ef14f6de414630a362b9133b456d3b")
	require.NoError(t, err)
	var payloadDigest [sha256.Size]byte
	copy(payloadDigest[:], digestBytes)
	positions := make([]playoff.GoldenPositionCommitEvidence, count)
	for index, participantID := range ordered[:count] {
		positions[index] = playoff.GoldenPositionCommitEvidence{
			Position: group.PositionFrom + index, ParticipantID: participantID,
			AttemptID: attemptID, AttemptNo: 1, SubmissionID: uint64(index + 1),
			EvidenceDigest: sha256.Sum256([]byte{byte(index + 1)}), CommitID: playoffID(736 + idOffset),
		}
	}
	evidence, err := playoff.NewGoldenPositionEvidence(playoff.GoldenPositionEvidenceInput{
		Scope: scope, RevisionID: playoffID(738 + idOffset), Revision: 2,
		PreviousRevisionID: &previousRevisionID,
		RevisionIDs:        []uuid.UUID{previousRevisionID, playoffID(738 + idOffset)},
		PositionFrom:       group.PositionFrom, PositionTo: group.PositionTo,
		Positions: positions,
		Attempts: []playoff.GoldenPositionAttemptEvidence{{
			AttemptID: attemptID, AttemptNo: 1,
			SubmissionRevisionID: playoffID(735 + idOffset), SubmissionRevision: int64(count + 1),
			WaveID: playoffID(731 + idOffset), AssignmentID: playoffID(732 + idOffset),
			SnapshotID: playoffID(733 + idOffset), TaskID: playoffID(734 + idOffset), OrderCount: count,
		}},
		PayloadDigest: payloadDigest,
	})
	require.NoError(t, err)
	return evidence
}

func newSemifinalBracket(t *testing.T) playoff.SemifinalBracket {
	t.Helper()
	fixture := newPlayoffFixture(t, false)
	finalSwiss, err := playoff.PlanFinalSwissProjection(fixture.command)
	require.NoError(t, err)
	top4, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
		TournamentID: fixture.command.TournamentID,
		RevisionID:   playoffRevisionID(5201), RevisionNo: 1,
		Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
		CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
	})
	require.NoError(t, err)
	bracket, err := playoff.PlanStrengthMatchedSemifinals(playoff.SemifinalBracketCommand{
		TournamentID: fixture.command.TournamentID,
		RevisionID:   playoffRevisionID(5202), RevisionNo: 1, Top4: top4,
		SeriesIDs: [2]uuid.UUID{semifinalID(1), semifinalID(2)},
		CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute),
	})
	require.NoError(t, err)
	return bracket
}

func completedSemifinal(series domain.Series, firstWins bool, suffix int) domain.Series {
	series.State = domain.SeriesStateCompleted
	winnerID := series.SecondParticipantID
	series.Score.SecondParticipantWins = 1
	if firstWins {
		winnerID = series.FirstParticipantID
		series.Score = domain.SeriesScore{FirstParticipantWins: 1}
	}
	scoreRevisionID := domain.SeriesScoreRevisionID(semifinalID(100 + suffix))
	resultRevisionID := domain.OfficialResultRevisionID(semifinalID(200 + suffix))
	series.WinnerID = &winnerID
	series.CurrentScoreRevisionID = &scoreRevisionID
	series.CurrentResultRevisionID = &resultRevisionID
	return series
}

func newFinal(t *testing.T) playoff.Final {
	t.Helper()
	bracket := newSemifinalBracket(t)
	matches := bracket.Semifinals()
	advanced, changed, err := playoff.AdvanceSemifinalResults(
		playoff.SemifinalAdvancement{}, bracket,
		[]domain.Series{
			completedSemifinal(matches[0].Series, true, 1),
			completedSemifinal(matches[1].Series, false, 2),
		},
	)
	require.NoError(t, err)
	require.True(t, changed)
	seriesID := semifinalID(300)
	participants := advanced.FinalParticipants()
	createdAt := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	revision := finalCategoryRevision(t, seriesID, bracket.Projection().Revision().TournamentID(), createdAt)
	drafted, err := draft.NewBO3Final(revision, draft.StartCommand{
		DraftID: semifinalID(302), FirstParticipantID: participants[0],
		SecondParticipantID: participants[1], FirstDeadline: createdAt.Add(time.Minute),
	})
	require.NoError(t, err)
	actions := []draft.ActionCommand{
		{ExpectedTurn: 1, ActorID: participants[0], Action: domain.DraftActionBan, Category: domain.CategoryCrypto, OccurredAt: createdAt.Add(10 * time.Second), NextDeadline: createdAt.Add(2 * time.Minute)},
		{ExpectedTurn: 2, ActorID: participants[1], Action: domain.DraftActionBan, Category: domain.CategoryForensics, OccurredAt: createdAt.Add(70 * time.Second), NextDeadline: createdAt.Add(3 * time.Minute)},
		{ExpectedTurn: 3, ActorID: participants[0], Action: domain.DraftActionPick, Category: domain.CategoryPwn, OccurredAt: createdAt.Add(130 * time.Second), NextDeadline: createdAt.Add(4 * time.Minute)},
		{ExpectedTurn: 4, ActorID: participants[1], Action: domain.DraftActionPick, Category: domain.CategoryReverse, OccurredAt: createdAt.Add(190 * time.Second)},
	}
	for _, action := range actions {
		drafted, err = draft.ApplyBO3FinalAction(drafted, action)
		require.NoError(t, err)
	}
	final, err := playoff.NewFinal(playoff.FinalCommand{
		SeriesID: seriesID, Advancement: advanced, Draft: drafted,
		InitialScoreRevisionID: domain.SeriesScoreRevisionID(semifinalID(303)),
		FirstSlotID:            semifinalID(304), FirstGameID: semifinalID(305),
	})
	require.NoError(t, err)
	return final
}

func finalCategoryRevision(
	t *testing.T,
	seriesID, tournamentID uuid.UUID,
	createdAt time.Time,
) draft.CategoryRevision {
	t.Helper()
	revision := draft.CategoryRevision{
		ID: semifinalID(301), TournamentID: tournamentID, SeriesID: seriesID,
		RosterID: semifinalID(901), Revision: 1,
		Stage: domain.TournamentStageFinal, Format: domain.SeriesFormatBO3,
		Mode: domain.CategoryModeDraft, SourceContentRevision: 1,
		CategoryPool: domain.CategoryPoolRevision{
			ID: semifinalID(902), Revision: 1, Format: domain.SeriesFormatBO3,
			Categories: []domain.Category{
				domain.CategoryCrypto, domain.CategoryForensics, domain.CategoryPwn,
				domain.CategoryReverse, domain.CategoryWeb,
			},
		},
		CreatedAt: createdAt,
	}
	require.NoError(t, revision.Validate())
	return revision
}

func finalProgression(
	final playoff.Final,
	winner int,
	terminal bool,
) playoff.FinalProgressionCommand {
	execution := final.Execution()
	series := execution.Series
	slot := series.Slots[len(series.Slots)-1]
	currentGame := slot.Attempts[len(slot.Attempts)-1]
	winnerID := series.FirstParticipantID
	if winner == 2 {
		winnerID = series.SecondParticipantID
	}
	resultRevisionID := domain.OfficialResultRevisionID(semifinalID(400 + slot.Position))
	currentGame.State = domain.GameStateCompleted
	currentGame.ResultReason = domain.GameResultReasonSolved
	currentGame.WinnerID = &winnerID
	currentGame.ResultRevisionID = &resultRevisionID
	scoreAfter := series.Score
	if winner == 1 {
		scoreAfter.FirstParticipantWins++
	} else {
		scoreAfter.SecondParticipantWins++
	}
	scoreRevisionID := domain.SeriesScoreRevisionID(semifinalID(410 + slot.Position))
	command := playoff.FinalProgressionCommand{Progression: seriesdomain.ScoreProgressionCommand{
		Game: currentGame,
		ScoreRevision: seriesdomain.ScoreRevision{
			ID: scoreRevisionID, SeriesID: series.ID,
			FirstParticipantID:  series.FirstParticipantID,
			SecondParticipantID: series.SecondParticipantID,
			PreviousRevisionID:  series.CurrentScoreRevisionID,
			Ordinal:             slot.Position + 1, Format: series.Format,
			ScoreBefore: series.Score, ScoreAfter: scoreAfter,
			GameResultRevisionIDs: []domain.OfficialResultRevisionID{resultRevisionID},
			RecordedAt:            time.Date(2026, time.August, 31, 13, slot.Position, 0, 0, time.UTC),
		},
	}}
	if terminal {
		seriesResultRevisionID := domain.OfficialResultRevisionID(semifinalID(420 + slot.Position))
		command.Progression.TerminalResultRevisionID = &seriesResultRevisionID
		command.ChampionRevisionID = domain.DerivedRevisionID(semifinalID(430))
		command.RecordedAt = time.Date(2026, time.August, 31, 14, 0, 0, 0, time.UTC)
		return command
	}
	nextPosition := slot.Position + 1
	command.Progression.Next = &seriesdomain.NextGameWave{
		WaveID:         semifinalID(500 + nextPosition),
		WaveRevisionID: domain.WaveRevisionID(semifinalID(510 + nextPosition)),
		SlotID:         semifinalID(520 + nextPosition), GameID: semifinalID(530 + nextPosition),
		Category: final.GameCategories()[nextPosition-1],
	}
	return command
}

func playoffID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", number))
}

func semifinalID(number int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("52000000-0000-0000-0000-%012d", number))
}

func playoffRevisionID(number int) domain.DerivedRevisionID {
	return domain.DerivedRevisionID(playoffID(number))
}

func playoffOfficialID(number int) domain.OfficialResultRevisionID {
	return domain.OfficialResultRevisionID(playoffID(number))
}

func playoffUUID(value uuid.UUID) *uuid.UUID {
	return &value
}
