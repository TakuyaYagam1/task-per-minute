package playoff_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

type playoffFixture struct {
	command      playoff.FinalSwissProjectionCommand
	participants []uuid.UUID
}

func newPlayoffFixture(t *testing.T, tied bool) playoffFixture {
	t.Helper()

	participants := []uuid.UUID{playoffID(101), playoffID(102), playoffID(103), playoffID(104)}
	createdAt := time.Date(2026, time.September, 1, 9, 0, 0, 0, time.UTC)
	seeds := []swissusecase.ParticipantSeed{
		{ParticipantID: participants[0], Seed: 4},
		{ParticipantID: participants[1], Seed: 3},
		{ParticipantID: participants[2], Seed: 2},
		{ParticipantID: participants[3], Seed: 1},
	}
	times := map[uuid.UUID]time.Duration{
		participants[0]: time.Second,
		participants[1]: time.Second,
		participants[2]: time.Second,
		participants[3]: time.Second,
	}
	if !tied {
		times[participants[1]] = 2 * time.Second
		times[participants[2]] = 3 * time.Second
		times[participants[3]] = 4 * time.Second
	}
	roundTwoWinner := participants[2]
	if !tied {
		roundTwoWinner = participants[0]
	}
	rounds := []playoff.FinalSwissRound{
		{
			RoundID: playoffID(501), RoundNumber: 1, RevisionID: playoffID(601),
			Series: []playoff.TerminalSeriesEvidence{
				newTerminalSeriesEvidence(t, 1, 1, participants[0], participants[1], participants[0], times[participants[0]], times[participants[1]], createdAt),
				newTerminalSeriesEvidence(t, 1, 2, participants[2], participants[3], participants[2], times[participants[2]], times[participants[3]], createdAt),
			},
		},
		{
			RoundID: playoffID(502), RoundNumber: 2, RevisionID: playoffID(602),
			Series: []playoff.TerminalSeriesEvidence{
				newTerminalSeriesEvidence(t, 2, 3, participants[0], participants[2], roundTwoWinner, times[participants[0]], times[participants[2]], createdAt),
				newTerminalSeriesEvidence(t, 2, 4, participants[1], participants[3], participants[1], times[participants[1]], times[participants[3]], createdAt),
			},
		},
		{
			RoundID: playoffID(503), RoundNumber: 3, RevisionID: playoffID(603),
			Series: []playoff.TerminalSeriesEvidence{
				newTerminalSeriesEvidence(t, 3, 5, participants[0], participants[3], participants[0], times[participants[0]], times[participants[3]], createdAt),
				newTerminalSeriesEvidence(t, 3, 6, participants[1], participants[2], participants[1], times[participants[1]], times[participants[2]], createdAt),
			},
		},
	}
	command := playoff.FinalSwissProjectionCommand{
		TournamentID: playoffID(1), Preset: domain.TournamentPresetV1,
		ProjectionID: playoffID(9), RevisionID: playoffRevisionID(10), RevisionNo: 1,
		PhysicalProjectionRevision: 1,
		ParticipantIDs:             participants, Seeds: seeds, Rounds: rounds, CreatedAt: createdAt,
	}
	if tied {
		command.GoldenGroups = []playoff.FinalSwissGoldenGroupIdentity{{
			PositionFrom: 1, PositionTo: 3,
			GroupID: playoffID(20), RevisionID: playoffRevisionID(21),
		}}
	}
	attachRoundProofs(t, &command, 20000)
	return playoffFixture{command: command, participants: participants}
}

func newTerminalSeriesEvidence(
	t *testing.T,
	round, number int,
	first, second, winner uuid.UUID,
	firstTime, secondTime time.Duration,
	createdAt time.Time,
) playoff.TerminalSeriesEvidence {
	t.Helper()

	seriesID := playoffID(200 + number)
	resultID := playoffOfficialID(300 + number)
	scoreID := domain.SeriesScoreRevisionID(playoffID(330 + number))
	previousScoreID := domain.SeriesScoreRevisionID(playoffID(340 + number))
	resultProjection, err := domain.NewProjectionRevision(
		playoffRevisionID(400+number), playoffID(1),
		domain.ArtifactRef{Kind: domain.ArtifactKindSeriesResult, EntityID: seriesID},
		1, nil, createdAt.Add(-time.Duration(20-number)*time.Minute),
		[]byte("terminal-series-"+seriesID.String()),
	)
	require.NoError(t, err)
	scoreProjection, err := domain.NewProjectionRevision(
		playoffRevisionID(430+number), playoffID(1),
		domain.ArtifactRef{Kind: domain.ArtifactKindSeriesScore, EntityID: seriesID},
		1, nil, resultProjection.Revision().CreatedAt().Add(-2*time.Second),
		[]byte("terminal-score-"+seriesID.String()),
	)
	require.NoError(t, err)
	score := domain.SeriesScore{}
	if winner == first {
		score.FirstParticipantWins = 1
	} else {
		score.SecondParticipantWins = 1
	}
	series := domain.Series{
		ID: seriesID, TournamentID: playoffID(1),
		FirstParticipantID: first, SecondParticipantID: second,
		Format: domain.SeriesFormatBO1, State: domain.SeriesStateCompleted,
		Score: score, WinnerID: playoffUUID(winner),
		CurrentScoreRevisionID: &scoreID, CurrentResultRevisionID: &resultID,
	}
	attempt := resultusecase.SeriesScoreAttemptReference{
		SlotID: playoffID(470 + number), SlotPosition: 1,
		GameID: playoffID(480 + number), AttemptNo: 1,
		State: domain.GameStateCompleted, WinnerID: playoffUUID(winner),
		Reason:                      domain.GameResultReasonSolved,
		CurrentGameResultRevisionID: playoffOfficialID(490 + number),
	}
	scoreHead := resultusecase.SeriesScoreRevisionHead{
		Scope: resultusecase.SeriesScoreRevisionScope{TournamentID: playoffID(1), SeriesID: seriesID},
		ID:    scoreID, PreviousRevisionID: &previousScoreID, Ordinal: 2,
		Operation:      resultusecase.SeriesScoreRevisionOperationAppendAttempt,
		CommandID:      playoffID(370 + number),
		Actor:          domain.ResultActor{Kind: domain.ResultActorServer},
		CommandAttempt: &attempt, FirstParticipantID: first, SecondParticipantID: second,
		Format: domain.SeriesFormatBO1, Score: score,
		Attempts:         []resultusecase.SeriesScoreAttemptReference{attempt},
		SourceProjection: scoreProjection.Revision(),
		RecordedAt:       scoreProjection.Revision().CreatedAt().Add(time.Second),
	}
	official := resultusecase.OfficialResultRevisionHead{
		Scope: resultusecase.OfficialResultScope{
			TournamentID: playoffID(1), SeriesID: seriesID,
			Kind: resultusecase.OfficialResultSubjectSeries,
		},
		ID: resultID, Ordinal: 1, CommandID: playoffID(360 + number),
		Actor: domain.ResultActor{Kind: domain.ResultActorServer},
		Outcome: resultusecase.OfficialResultOutcome{
			SeriesState:  domain.SeriesStateCompleted,
			SeriesReason: domain.SeriesResultReasonScoreComplete,
			WinnerID:     playoffUUID(winner), ScoreRevisionID: &scoreID,
		},
		SourceProjection: resultProjection.Revision(),
		RecordedAt:       resultProjection.Revision().CreatedAt(),
	}
	require.NoError(t, series.Validate())
	require.NoError(t, scoreHead.Validate())
	require.NoError(t, official.Validate())
	evidence, err := playoff.NewTerminalSeriesEvidence(playoff.TerminalSeriesEvidenceInput{
		Series: series,
		OfficialResult: resultprojection.OfficialResultProjectionInput{
			TerminalSource: resultprojection.TerminalResultSourcePlayed,
			Result:         official, ResultProjection: resultProjection,
			Score: &scoreHead, ScoreProjection: &scoreProjection,
		},
		Projection: resultProjection,
		Result: swissusecase.SeriesPointResult{
			RoundID: playoffID(500 + round), RoundNumber: round,
			SeriesID: seriesID, ResultRevisionID: resultID,
			FirstParticipantID: first, SecondParticipantID: second,
			WinnerID: playoffUUID(winner), Label: swissusecase.SeriesResultPlayed,
			FirstEffectiveTime: firstTime, SecondEffectiveTime: secondTime,
		},
	})
	require.NoError(t, err)
	return evidence
}

func attachRoundProofs(
	t *testing.T,
	command *playoff.FinalSwissProjectionCommand,
	base int,
) {
	t.Helper()
	for roundIndex := range command.Rounds {
		round := &command.Rounds[roundIndex]
		proofBase := base + roundIndex*100
		series := make([]swissusecase.LockedSeries, len(round.Series))
		for index, head := range round.Series {
			current := head.Series()
			series[index] = swissusecase.LockedSeries{
				SeriesID: current.ID, PairingID: playoffID(proofBase + 20 + index),
				FirstParticipantID:  current.FirstParticipantID,
				SecondParticipantID: current.SecondParticipantID,
				CategoryRevisionID:  playoffID(proofBase + 30 + index), CategoryRevision: 1,
				AssignmentID: playoffID(proofBase + 40 + index), AssignmentRevision: 1,
				AssignmentPlanID:         playoffID(proofBase + 50 + index),
				AssignmentPlanRevisionID: playoffID(proofBase + 60 + index),
				ReservationID:            playoffID(proofBase + 70 + index), ReservationRevision: 1,
			}
		}
		byeParticipantID := uuid.Nil
		if round.Bye != nil {
			byeParticipantID = round.Bye.ParticipantID
		}
		sourceProjectionRevisionID := playoffID(proofBase + 5)
		proof, err := swissusecase.NewRoundLockProof(swissusecase.RoundLockProofInput{
			TournamentID: command.TournamentID, RosterID: playoffID(proofBase + 1),
			RoundID: round.RoundID, Preset: command.Preset, RoundNumber: round.RoundNumber,
			SourceProjectionRevisionID: sourceProjectionRevisionID,
			NormalPoolRevisionID:       playoffID(proofBase + 4),
			PreflightRevisionID:        playoffID(proofBase + 6), WaveID: playoffID(proofBase + 7),
			WaveRevisionID: domain.WaveRevisionID(playoffID(proofBase + 8)),
			Revisions: swissusecase.RoundLockRevisions{
				Round: 1, SourceProjection: 1, Roster: 1, NormalPool: 1, History: 1, Wave: 1,
			},
			RosterParticipantIDs: command.ParticipantIDs,
			Series:               series, ByeParticipantID: byeParticipantID,
		})
		require.NoError(t, err)
		round.RevisionID = sourceProjectionRevisionID
		round.LockProof = proof
	}
}

func terminalEvidence(command playoff.FinalSwissProjectionCommand) []playoff.TerminalSeriesEvidence {
	result := make([]playoff.TerminalSeriesEvidence, 0)
	for _, round := range command.Rounds {
		result = append(result, round.Series...)
	}
	return result
}

func noGameTerminalEvidence(
	t *testing.T,
	head playoff.TerminalSeriesEvidence,
	waveID uuid.UUID,
	base int,
	action domain.NormalNoShowAction,
) playoff.TerminalSeriesEvidence {
	t.Helper()

	series := head.Series()
	point := head.PointResult()
	resolvedAt := head.Projection().Revision().CreatedAt()
	gameID := playoffID(base + 1)
	slotID := playoffID(base + 2)
	gameResultID := playoffOfficialID(base + 3)
	scoreID := domain.SeriesScoreRevisionID(playoffID(base + 4))
	resultID := playoffOfficialID(base + 5)
	previousScoreID := domain.SeriesScoreRevisionID(playoffID(base + 6))
	previousResultID := playoffOfficialID(base + 7)
	gameProjection, err := domain.NewProjectionRevision(
		playoffRevisionID(base+8), series.TournamentID,
		domain.ArtifactRef{Kind: domain.ArtifactKindGameResult, EntityID: gameID},
		1, nil, resolvedAt.Add(-3*time.Second), []byte("task051-no-game"),
	)
	require.NoError(t, err)
	scoreProjectionPrevious := playoffRevisionID(base + 9)
	scoreProjection, err := domain.NewProjectionRevision(
		playoffRevisionID(base+10), series.TournamentID,
		domain.ArtifactRef{Kind: domain.ArtifactKindSeriesScore, EntityID: series.ID},
		2, &scoreProjectionPrevious, resolvedAt.Add(-2*time.Second), []byte("task051-no-game-score"),
	)
	require.NoError(t, err)
	resultProjectionPrevious := playoffRevisionID(base + 11)
	resultProjection, err := domain.NewProjectionRevision(
		playoffRevisionID(base+12), series.TournamentID,
		domain.ArtifactRef{Kind: domain.ArtifactKindSeriesResult, EntityID: series.ID},
		2, &resultProjectionPrevious, resolvedAt.Add(-time.Second), []byte("task051-no-game-result"),
	)
	require.NoError(t, err)
	score := domain.SeriesScore{}
	state := domain.SeriesStateCancelled
	var winnerID *uuid.UUID
	var readyParticipantID *uuid.UUID
	label := swissusecase.SeriesResultVoid
	if action == domain.NormalNoShowActionReopenWave {
		score.FirstParticipantWins = 1
		state = domain.SeriesStateCompleted
		winnerID = playoffUUID(series.FirstParticipantID)
		readyParticipantID = playoffUUID(series.FirstParticipantID)
		label = swissusecase.SeriesResultNoShow
	}
	recorded := resultprojection.RecordedNoGameResult{
		Scope: domain.NormalNoShowScope{
			TournamentID: series.TournamentID, WaveID: waveID,
			WindowID: playoffID(base + 14), SeriesID: series.ID,
		},
		CommandID: playoffID(base + 15), Action: action,
		Format:             domain.SeriesFormatBO1,
		FirstParticipantID: series.FirstParticipantID, SecondParticipantID: series.SecondParticipantID,
		ReadyParticipantID: readyParticipantID,
		GameResults: []domain.NormalNoShowGameRevision{{
			Ordinal: 1, ID: gameResultID, GameID: gameID,
			State: domain.GameStateCancelled, Reason: domain.GameResultReasonSeriesCancelled,
			RecordedAt: resolvedAt,
		}},
		Topology: []resultprojection.RecordedNoGameAttempt{{
			SeriesID: series.ID, SlotID: slotID, SlotPosition: 1,
			GameID: gameID, AttemptNo: 1, ResultRevisionID: gameResultID,
		}},
		Score: domain.NormalNoShowScoreRevision{
			Ordinal: 2, ID: scoreID, SeriesID: series.ID, PreviousRevisionID: &previousScoreID,
			Score: score, GameResultRevisionIDs: []domain.OfficialResultRevisionID{gameResultID},
			RecordedAt: resolvedAt,
		},
		Series: domain.NormalNoShowSeriesRevision{
			Ordinal: 3, ID: resultID, SeriesID: series.ID, PreviousRevisionID: &previousResultID,
			State: state, WinnerID: winnerID,
			ScoreRevisionID: scoreID, RecordedAt: resolvedAt,
		},
		GameSourceRevisions: []domain.DerivedRevision{gameProjection.Revision()},
		ScoreSourceRevision: scoreProjection.Revision(), ResultSourceRevision: resultProjection.Revision(),
		GameProjections: []domain.ProjectionRevision{gameProjection},
		GameDependencies: []domain.RevisionDependency{{
			SourceRevisionID:  gameProjection.Revision().ID(),
			DerivedRevisionID: scoreProjection.Revision().ID(),
		}},
		ScoreProjection: scoreProjection, ResultProjection: resultProjection,
		ResultDependency: domain.RevisionDependency{
			SourceRevisionID:  scoreProjection.Revision().ID(),
			DerivedRevisionID: resultProjection.Revision().ID(),
		},
		ResolvedAt: resolvedAt,
	}
	series.State = state
	series.Score = score
	series.WinnerID = winnerID
	series.Slots = []domain.GameSlot{{
		ID: slotID, SeriesID: series.ID, Position: 1, Category: domain.CategoryWeb,
		Attempts: []domain.Game{{
			ID: gameID, SlotID: slotID, AttemptNo: 1,
			State: domain.GameStateCancelled, ResultReason: domain.GameResultReasonSeriesCancelled,
			ResultRevisionID: &gameResultID,
		}},
	}}
	series.CurrentScoreRevisionID = &scoreID
	series.CurrentResultRevisionID = &resultID
	point.ResultRevisionID = resultID
	point.WinnerID = winnerID
	point.Label = label
	point.FirstEffectiveTime = 0
	point.SecondEffectiveTime = 0
	point.FirstAcceptedSolveTime = nil
	point.SecondAcceptedSolveTime = nil
	require.NoError(t, series.Validate())
	official := resultprojection.OfficialResultProjectionInput{
		TerminalSource: resultprojection.TerminalResultSourceNormalNoShow,
		NoGame:         &recorded,
	}
	plan, err := resultprojection.ProjectOfficialResult(official)
	require.NoError(t, err)
	require.NoError(t, plan.Validate())
	digest := sha256.Sum256([]byte("tournament-no-game-evidence"))
	evidence, err := playoff.NewTerminalSeriesEvidence(playoff.TerminalSeriesEvidenceInput{
		Series: series, OfficialResult: official, Projection: resultProjection, Result: point,
		NoGameEvidenceDigest: hex.EncodeToString(digest[:]),
	})
	require.NoError(t, err)
	return evidence
}
