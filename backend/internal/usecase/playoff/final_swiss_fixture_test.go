package playoff_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func newTerminalSeriesEvidenceFor(
	t *testing.T,
	tournamentID, roundID uuid.UUID,
	round int,
	first, second, winner uuid.UUID,
	base int,
	createdAt time.Time,
	firstTime, secondTime time.Duration,
) playoff.TerminalSeriesEvidence {
	t.Helper()

	seriesID := playoffID(base)
	resultID := playoffOfficialID(base + 1)
	scoreID := domain.SeriesScoreRevisionID(playoffID(base + 2))
	previousScoreID := domain.SeriesScoreRevisionID(playoffID(base + 3))
	resultProjection, err := domain.NewProjectionRevision(
		playoffRevisionID(base+4), tournamentID,
		domain.ArtifactRef{Kind: domain.ArtifactKindSeriesResult, EntityID: seriesID},
		1, nil, createdAt.Add(-time.Minute), []byte("playoff-series-result"),
	)
	require.NoError(t, err)
	scoreProjection, err := domain.NewProjectionRevision(
		playoffRevisionID(base+5), tournamentID,
		domain.ArtifactRef{Kind: domain.ArtifactKindSeriesScore, EntityID: seriesID},
		1, nil, createdAt.Add(-time.Minute-time.Second), []byte("playoff-series-score"),
	)
	require.NoError(t, err)
	score := domain.SeriesScore{}
	if winner == first {
		score.FirstParticipantWins = 1
	} else {
		score.SecondParticipantWins = 1
	}
	series := domain.Series{
		ID: seriesID, TournamentID: tournamentID,
		FirstParticipantID: first, SecondParticipantID: second,
		Format: domain.SeriesFormatBO1, State: domain.SeriesStateCompleted,
		Score: score, WinnerID: playoffUUID(winner),
		CurrentScoreRevisionID: &scoreID, CurrentResultRevisionID: &resultID,
	}
	attempt := resultusecase.SeriesScoreAttemptReference{
		SlotID: playoffID(base + 6), SlotPosition: 1,
		GameID: playoffID(base + 7), AttemptNo: 1,
		State: domain.GameStateCompleted, WinnerID: playoffUUID(winner),
		Reason:                      domain.GameResultReasonSolved,
		CurrentGameResultRevisionID: playoffOfficialID(base + 8),
	}
	scoreHead := resultusecase.SeriesScoreRevisionHead{
		Scope: resultusecase.SeriesScoreRevisionScope{TournamentID: tournamentID, SeriesID: seriesID},
		ID:    scoreID, PreviousRevisionID: &previousScoreID, Ordinal: 2,
		Operation:      resultusecase.SeriesScoreRevisionOperationAppendAttempt,
		CommandID:      playoffID(base + 9),
		Actor:          domain.ResultActor{Kind: domain.ResultActorServer},
		CommandAttempt: &attempt, FirstParticipantID: first, SecondParticipantID: second,
		Format: domain.SeriesFormatBO1, Score: score,
		Attempts:         []resultusecase.SeriesScoreAttemptReference{attempt},
		SourceProjection: scoreProjection.Revision(), RecordedAt: createdAt.Add(-30 * time.Second),
	}
	official := resultusecase.OfficialResultRevisionHead{
		Scope: resultusecase.OfficialResultScope{
			TournamentID: tournamentID, SeriesID: seriesID, Kind: resultusecase.OfficialResultSubjectSeries,
		},
		ID: resultID, Ordinal: 1, CommandID: playoffID(base + 10),
		Actor: domain.ResultActor{Kind: domain.ResultActorServer},
		Outcome: resultusecase.OfficialResultOutcome{
			SeriesState:  domain.SeriesStateCompleted,
			SeriesReason: domain.SeriesResultReasonScoreComplete,
			WinnerID:     playoffUUID(winner), ScoreRevisionID: &scoreID,
		},
		SourceProjection: resultProjection.Revision(), RecordedAt: createdAt.Add(-20 * time.Second),
	}
	evidence, err := playoff.NewTerminalSeriesEvidence(playoff.TerminalSeriesEvidenceInput{
		Series: series,
		OfficialResult: resultprojection.OfficialResultProjectionInput{
			TerminalSource: resultprojection.TerminalResultSourcePlayed,
			Result:         official, ResultProjection: resultProjection,
			Score: &scoreHead, ScoreProjection: &scoreProjection,
		},
		Projection: resultProjection,
		Result: swissusecase.SeriesPointResult{
			RoundID: roundID, RoundNumber: round, SeriesID: seriesID,
			ResultRevisionID: resultID, FirstParticipantID: first, SecondParticipantID: second,
			WinnerID: playoffUUID(winner), Label: swissusecase.SeriesResultPlayed,
			FirstEffectiveTime: firstTime, SecondEffectiveTime: secondTime,
		},
	})
	require.NoError(t, err)
	return evidence
}

func newSizedPlayoffFixture(t *testing.T, size, base int) playoffFixture {
	t.Helper()

	participants := make([]uuid.UUID, size)
	seeds := make([]swissusecase.ParticipantSeed, size)
	for index := range size {
		participants[index] = playoffID(base + 10 + index)
		seeds[index] = swissusecase.ParticipantSeed{ParticipantID: participants[index], Seed: index + 1}
	}
	createdAt := time.Date(2026, time.September, 2, 9, 0, 0, 0, time.UTC)
	roundCount, err := domain.TournamentPresetV1.SwissRounds(size)
	require.NoError(t, err)
	rotation := append([]uuid.UUID(nil), participants...)
	if size%2 == 1 {
		rotation = append(rotation, uuid.Nil)
	}
	rounds := make([]playoff.FinalSwissRound, roundCount)
	seriesNumber := 0
	for roundIndex := range roundCount {
		roundID := playoffID(base + 100 + roundIndex)
		rounds[roundIndex] = playoff.FinalSwissRound{
			RoundID: roundID, RoundNumber: roundIndex + 1,
			RevisionID: playoffID(base + 120 + roundIndex),
		}
		for pairIndex := 0; pairIndex < len(rotation)/2; pairIndex++ {
			first := rotation[pairIndex]
			second := rotation[len(rotation)-1-pairIndex]
			if first == uuid.Nil || second == uuid.Nil {
				participantID := first
				if participantID == uuid.Nil {
					participantID = second
				}
				rounds[roundIndex].Bye = &swissusecase.ByePointResult{
					RoundID: roundID, RoundNumber: roundIndex + 1,
					ParticipantID: participantID, RevisionID: playoffID(base + 140 + roundIndex),
				}
				continue
			}
			seriesNumber++
			rounds[roundIndex].Series = append(rounds[roundIndex].Series,
				newTerminalSeriesEvidenceFor(
					t, playoffID(base), roundID, roundIndex+1, first, second, first,
					base+200+seriesNumber*20, createdAt, 0, 0,
				),
			)
		}
		last := rotation[len(rotation)-1]
		copy(rotation[2:], rotation[1:len(rotation)-1])
		rotation[1] = last
	}
	fixture := playoffFixture{
		participants: participants,
		command: playoff.FinalSwissProjectionCommand{
			TournamentID: playoffID(base), Preset: domain.TournamentPresetV1,
			ProjectionID: playoffID(base + 1), RevisionID: playoffRevisionID(base + 2), RevisionNo: 1,
			PhysicalProjectionRevision: 1,
			ParticipantIDs:             participants, Seeds: seeds, Rounds: rounds, CreatedAt: createdAt,
		},
	}
	attachRoundProofs(t, &fixture.command, base+4000)
	setFinalSwissGoldenIdentities(t, &fixture, base+2000)
	return fixture
}

func newCutoffTieFixture(t *testing.T) playoffFixture {
	t.Helper()

	const base = 12000
	fixture := newSizedPlayoffFixture(t, 5, base)
	winners := []uuid.UUID{
		fixture.participants[4], fixture.participants[2], fixture.participants[4],
		fixture.participants[2], fixture.participants[0], fixture.participants[2],
	}
	seriesIndex := 0
	for roundIndex := range fixture.command.Rounds {
		round := &fixture.command.Rounds[roundIndex]
		for index, evidence := range round.Series {
			seriesIndex++
			series := evidence.Series()
			round.Series[index] = newTerminalSeriesEvidenceFor(
				t, fixture.command.TournamentID, round.RoundID, round.RoundNumber,
				series.FirstParticipantID, series.SecondParticipantID, winners[seriesIndex-1],
				base+400+seriesIndex*20, fixture.command.CreatedAt, 0, 0,
			)
		}
	}
	setFinalSwissGoldenIdentities(t, &fixture, base+3000)
	attachRoundProofs(t, &fixture.command, base+5000)
	return fixture
}

func newLowerTieFixture(t *testing.T) playoffFixture {
	t.Helper()

	participants := []uuid.UUID{
		playoffID(111), playoffID(112), playoffID(113),
		playoffID(114), playoffID(115), playoffID(116),
	}
	createdAt := time.Date(2026, time.September, 1, 10, 0, 0, 0, time.UTC)
	seeds := make([]swissusecase.ParticipantSeed, len(participants))
	for index, participantID := range participants {
		seeds[index] = swissusecase.ParticipantSeed{ParticipantID: participantID, Seed: len(participants) - index}
	}
	fast := map[uuid.UUID]time.Duration{
		participants[0]: time.Second,
		participants[1]: 2 * time.Second,
		participants[2]: 3 * time.Second,
		participants[3]: 4 * time.Second,
		participants[4]: 5 * time.Second,
		participants[5]: 5 * time.Second,
	}
	series := func(round, number int, first, second, winner uuid.UUID) playoff.TerminalSeriesEvidence {
		return newTerminalSeriesEvidenceFor(
			t, playoffID(2), playoffID(1700+round), round, first, second, winner,
			1200+number*20, createdAt, fast[first], fast[second],
		)
	}
	rounds := []playoff.FinalSwissRound{
		{RoundID: playoffID(1701), RoundNumber: 1, RevisionID: playoffID(1801), Series: []playoff.TerminalSeriesEvidence{
			series(1, 1, participants[0], participants[5], participants[0]),
			series(1, 2, participants[1], participants[4], participants[1]),
			series(1, 3, participants[2], participants[3], participants[3]),
		}},
		{RoundID: playoffID(1702), RoundNumber: 2, RevisionID: playoffID(1802), Series: []playoff.TerminalSeriesEvidence{
			series(2, 4, participants[0], participants[4], participants[0]),
			series(2, 5, participants[5], participants[2], participants[2]),
			series(2, 6, participants[1], participants[3], participants[1]),
		}},
		{RoundID: playoffID(1703), RoundNumber: 3, RevisionID: playoffID(1803), Series: []playoff.TerminalSeriesEvidence{
			series(3, 7, participants[0], participants[3], participants[0]),
			series(3, 8, participants[4], participants[2], participants[2]),
			series(3, 9, participants[5], participants[1], participants[1]),
		}},
	}
	fixture := playoffFixture{
		participants: participants,
		command: playoff.FinalSwissProjectionCommand{
			TournamentID: playoffID(2), Preset: domain.TournamentPresetV1,
			ProjectionID: playoffID(1899), RevisionID: playoffRevisionID(1900), RevisionNo: 1,
			PhysicalProjectionRevision: 1,
			ParticipantIDs:             participants, Seeds: seeds, Rounds: rounds,
			GoldenGroups: []playoff.FinalSwissGoldenGroupIdentity{{
				PositionFrom: 1, PositionTo: 2,
				GroupID: playoffID(1910), RevisionID: playoffRevisionID(1911),
			}},
			CreatedAt: createdAt,
		},
	}
	attachRoundProofs(t, &fixture.command, 23000)
	return fixture
}

func newTwoTieFixture(t *testing.T) playoffFixture {
	t.Helper()

	fixture := newPlayoffFixture(t, true)
	fixture.command.Rounds[0].Series[0] = cancelledTerminalSeriesEvidence(t, fixture.command.Rounds[0].Series[0])
	fixture.command.Rounds[0].Series[1] = cancelledTerminalSeriesEvidence(t, fixture.command.Rounds[0].Series[1])
	fixture.command.Rounds[1].Series[0] = terminalSeriesWithWinner(
		t, fixture.command.Rounds[1].Series[0], fixture.participants[0],
	)
	fixture.command.GoldenGroups = []playoff.FinalSwissGoldenGroupIdentity{
		{PositionFrom: 1, PositionTo: 2, GroupID: playoffID(22), RevisionID: playoffRevisionID(23)},
		{PositionFrom: 3, PositionTo: 4, GroupID: playoffID(24), RevisionID: playoffRevisionID(25)},
	}
	return fixture
}

func setFinalSwissGoldenIdentities(t *testing.T, fixture *playoffFixture, base int) {
	t.Helper()

	input := swissusecase.PointLedgerInput{ParticipantIDs: fixture.participants}
	for _, round := range fixture.command.Rounds {
		for _, evidence := range round.Series {
			input.Series = append(input.Series, evidence.PointResult())
		}
		if round.Bye != nil {
			input.Byes = append(input.Byes, *round.Bye)
		}
	}
	ledger, err := swissusecase.BuildPointLedger(input)
	require.NoError(t, err)
	standings, err := swissusecase.OrderNormalStandings(swissusecase.NormalOrderingInput{
		Ledger: ledger, Seeds: fixture.command.Seeds, SwissComplete: true,
	})
	require.NoError(t, err)
	source, err := goldenusecase.NewStandingsProjection(
		fixture.command.TournamentID, fixture.command.ProjectionID, fixture.command.RevisionID,
		fixture.command.RevisionNo, nil, true, standings,
	)
	require.NoError(t, err)
	partition, err := goldenusecase.PartitionTies(source)
	require.NoError(t, err)
	groups := partition.Groups()
	fixture.command.GoldenGroups = make([]playoff.FinalSwissGoldenGroupIdentity, len(groups))
	for index, group := range groups {
		fixture.command.GoldenGroups[index] = playoff.FinalSwissGoldenGroupIdentity{
			PositionFrom: group.PositionFrom, PositionTo: group.PositionTo,
			GroupID: playoffID(base + index*2), RevisionID: playoffRevisionID(base + 1 + index*2),
		}
	}
}

func terminalSeriesEvidenceInput(evidence playoff.TerminalSeriesEvidence) playoff.TerminalSeriesEvidenceInput {
	return playoff.TerminalSeriesEvidenceInput{
		Series: evidence.Series(), OfficialResult: evidence.OfficialResult(),
		Projection: evidence.Projection(), Result: evidence.PointResult(),
		NoGameEvidenceDigest: evidence.NoGameEvidenceDigest(),
	}
}

func terminalSeriesWithWinner(
	t *testing.T,
	evidence playoff.TerminalSeriesEvidence,
	winner uuid.UUID,
) playoff.TerminalSeriesEvidence {
	t.Helper()

	input := terminalSeriesEvidenceInput(evidence)
	score := domain.SeriesScore{}
	if winner == input.Series.FirstParticipantID {
		score.FirstParticipantWins = 1
	} else {
		score.SecondParticipantWins = 1
	}
	input.Series.State = domain.SeriesStateCompleted
	input.Series.Score = score
	input.Series.WinnerID = playoffUUID(winner)
	input.OfficialResult.Score.Score = score
	input.OfficialResult.Score.CommandAttempt.WinnerID = playoffUUID(winner)
	input.OfficialResult.Score.Attempts[0].WinnerID = playoffUUID(winner)
	input.OfficialResult.Result.Outcome.SeriesState = domain.SeriesStateCompleted
	input.OfficialResult.Result.Outcome.SeriesReason = domain.SeriesResultReasonScoreComplete
	input.OfficialResult.Result.Outcome.WinnerID = playoffUUID(winner)
	input.Result.WinnerID = playoffUUID(winner)
	input.Result.Label = swissusecase.SeriesResultPlayed
	result, err := playoff.NewTerminalSeriesEvidence(input)
	require.NoError(t, err)
	return result
}

func cancelledTerminalSeriesEvidence(
	t *testing.T,
	evidence playoff.TerminalSeriesEvidence,
) playoff.TerminalSeriesEvidence {
	t.Helper()

	input := terminalSeriesEvidenceInput(evidence)
	input.Series.State = domain.SeriesStateCancelled
	input.Series.Score = domain.SeriesScore{}
	input.Series.WinnerID = nil
	input.OfficialResult.Score.Ordinal = 1
	input.OfficialResult.Score.PreviousRevisionID = nil
	input.OfficialResult.Score.Operation = resultusecase.SeriesScoreRevisionOperationInitialize
	input.OfficialResult.Score.CommandAttempt = nil
	input.OfficialResult.Score.Score = domain.SeriesScore{}
	input.OfficialResult.Score.Attempts = nil
	input.OfficialResult.Result.Outcome.SeriesState = domain.SeriesStateCancelled
	input.OfficialResult.Result.Outcome.SeriesReason = domain.SeriesResultReasonSeriesCancelled
	input.OfficialResult.Result.Outcome.WinnerID = nil
	input.Result.WinnerID = nil
	input.Result.Label = swissusecase.SeriesResultVoid
	result, err := playoff.NewTerminalSeriesEvidence(input)
	require.NoError(t, err)
	return result
}
