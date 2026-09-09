package playoff_test

import (
	"crypto/sha256"
	"math"
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

func TestTerminalSeriesEvidenceIsImmutable(t *testing.T) {
	t.Parallel()

	fixture := newPlayoffFixture(t, false)
	evidence := fixture.command.Rounds[0].Series[0]
	seriesID := evidence.Series().ID
	series := evidence.Series()
	series.ID = uuid.Nil
	require.Equal(t, uuid.Nil, series.ID)
	point := evidence.PointResult()
	point.SeriesID = uuid.Nil
	require.Equal(t, uuid.Nil, point.SeriesID)
	official := evidence.OfficialResult()
	official.Result.CommandID = uuid.Nil
	require.NoError(t, evidence.Validate())
	require.Equal(t, seriesID, evidence.Series().ID)
	require.Equal(t, seriesID, evidence.PointResult().SeriesID)
	require.NotEqual(t, uuid.Nil, evidence.OfficialResult().Result.CommandID)

	input := playoff.TerminalSeriesEvidenceInput{
		Series: evidence.Series(), OfficialResult: evidence.OfficialResult(),
		Projection: evidence.Projection(), Result: evidence.PointResult(),
	}
	input.Result.SeriesID = uuid.Nil
	invalid, err := playoff.NewTerminalSeriesEvidence(input)
	require.ErrorIs(t, err, playoff.ErrInvalidTerminalSeriesEvidence)
	require.Equal(t, playoff.TerminalSeriesEvidence{}, invalid)

	zero, err := playoff.NewTerminalSeriesEvidence(playoff.TerminalSeriesEvidenceInput{})
	require.ErrorIs(t, err, playoff.ErrInvalidTerminalSeriesEvidence)
	require.Equal(t, playoff.TerminalSeriesEvidence{}, zero)
}

func TestGoldenPositionEvidenceIsImmutable(t *testing.T) {
	t.Parallel()

	fixture := newPlayoffFixture(t, true)
	ordered := []uuid.UUID{fixture.participants[2], fixture.participants[0], fixture.participants[1]}
	evidence := newGoldenPositionEvidence(t, fixture, ordered)
	wantDigest := evidence.PayloadDigest()
	revisions := evidence.RevisionIDs()
	revisions[0] = uuid.Nil
	positions := evidence.Positions()
	positions[0].ParticipantID = uuid.Nil
	attempts := evidence.Attempts()
	attempts[0].OrderCount = 0
	require.NoError(t, evidence.Validate())
	require.NotEqual(t, uuid.Nil, evidence.RevisionIDs()[0])
	require.Equal(t, ordered[0], evidence.Positions()[0].ParticipantID)
	require.Equal(t, 3, evidence.Attempts()[0].OrderCount)
	require.Equal(t, wantDigest, evidence.PayloadDigest())

	input := playoff.GoldenPositionEvidenceInput{
		Scope: evidence.Scope(), RevisionID: evidence.RevisionIDs()[1], Revision: 2,
		PreviousRevisionID: &revisions[0], RevisionIDs: evidence.RevisionIDs(),
		PositionFrom: 1, PositionTo: 3, Positions: evidence.Positions(), Attempts: evidence.Attempts(),
		PayloadDigest: [sha256.Size]byte{},
	}
	invalid, err := playoff.NewGoldenPositionEvidence(input)
	require.ErrorIs(t, err, playoff.ErrInvalidGoldenPositionEvidence)
	require.Equal(t, playoff.GoldenPositionEvidence{}, invalid)
}

func TestGoldenPositionEvidenceRejectsInvalidBoundsAndLineage(t *testing.T) {
	t.Parallel()

	fixture := newPlayoffFixture(t, true)
	evidence := newGoldenPositionEvidence(
		t, fixture,
		[]uuid.UUID{fixture.participants[0], fixture.participants[1], fixture.participants[2]},
	)
	tests := []struct {
		name   string
		mutate func(*playoff.GoldenPositionEvidenceInput)
	}{
		{name: "oversized positions", mutate: func(input *playoff.GoldenPositionEvidenceInput) {
			input.PositionTo = input.PositionFrom + domain.TournamentMaxParticipants
			input.Positions = make([]playoff.GoldenPositionCommitEvidence, domain.TournamentMaxParticipants+1)
		}},
		{name: "oversized attempt order", mutate: func(input *playoff.GoldenPositionEvidenceInput) {
			input.Attempts[0].OrderCount = domain.TournamentMaxParticipants + 1
		}},
		{name: "duplicate revision", mutate: func(input *playoff.GoldenPositionEvidenceInput) {
			input.RevisionIDs[1] = input.RevisionIDs[0]
		}},
		{name: "missing predecessor", mutate: func(input *playoff.GoldenPositionEvidenceInput) {
			input.PreviousRevisionID = nil
		}},
		{name: "zero digest", mutate: func(input *playoff.GoldenPositionEvidenceInput) {
			input.PayloadDigest = [sha256.Size]byte{}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := goldenPositionInput(evidence)
			test.mutate(&input)
			invalid, err := playoff.NewGoldenPositionEvidence(input)
			require.Equal(t, playoff.GoldenPositionEvidence{}, invalid)
			require.ErrorIs(t, err, playoff.ErrInvalidGoldenPositionEvidence)
		})
	}
}

func TestTerminalSeriesEvidenceRejectsInvalidOrdinaryEvidence(t *testing.T) {
	t.Parallel()

	fixture := newPlayoffFixture(t, false)
	base := fixture.command.Rounds[0].Series[0]
	tests := []struct {
		name   string
		mutate func(*playoff.TerminalSeriesEvidenceInput)
	}{
		{name: "non-terminal Series", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.Series.State = domain.SeriesStateActive
		}},
		{name: "stale result projection", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.Series.CurrentResultRevisionID = playoffOfficialPointer(900)
		}},
		{name: "spliced typed winner proof", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.OfficialResult.Result.Outcome.WinnerID = playoffUUID(input.Result.SecondParticipantID)
		}},
		{name: "spliced typed score proof", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.OfficialResult.Score.Score = domain.SeriesScore{SecondParticipantWins: 1}
		}},
		{name: "forged point result", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.Result.WinnerID = playoffUUID(input.Result.SecondParticipantID)
		}},
		{name: "negative effective time", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.Result.FirstEffectiveTime = -time.Nanosecond
		}},
		{name: "accepted solve exceeds effective time", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			value := input.Result.FirstEffectiveTime + time.Nanosecond
			input.Result.FirstAcceptedSolveTime = &value
		}},
		{name: "ordinary result has no-game digest", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.NoGameEvidenceDigest = "00"
		}},
		{name: "oversized score evidence", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.OfficialResult.Score.Attempts = make(
				[]resultusecase.SeriesScoreAttemptReference, domain.TournamentMaxParticipants+1,
			)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := terminalSeriesEvidenceInput(base)
			test.mutate(&input)
			evidence, err := playoff.NewTerminalSeriesEvidence(input)
			require.Equal(t, playoff.TerminalSeriesEvidence{}, evidence)
			require.ErrorIs(t, err, playoff.ErrInvalidTerminalSeriesEvidence)
		})
	}
}

func TestTerminalSeriesEvidenceRejectsSplicedNoGameEvidence(t *testing.T) {
	t.Parallel()

	fixture := newPlayoffFixture(t, false)
	base := noGameTerminalEvidence(
		t, fixture.command.Rounds[0].Series[0], fixture.command.Rounds[0].LockProof.WaveID,
		8300, domain.NormalNoShowActionReopenWave,
	)
	other := fixture.command.Rounds[0].Series[1]
	tests := []struct {
		name   string
		mutate func(*playoff.TerminalSeriesEvidenceInput)
	}{
		{name: "ordinary result mixed in", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.OfficialResult.Result = other.OfficialResult().Result
		}},
		{name: "head projection", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.Projection = other.Projection()
		}},
		{name: "current result", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.Series.CurrentResultRevisionID = playoffOfficialPointer(8390)
		}},
		{name: "current score", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			scoreID := domain.SeriesScoreRevisionID(playoffID(8391))
			input.Series.CurrentScoreRevisionID = &scoreID
		}},
		{name: "played label", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.Result.Label = swissusecase.SeriesResultPlayed
		}},
		{name: "effective time", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.Result.SecondEffectiveTime = time.Nanosecond
		}},
		{name: "accepted solve time", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			value := time.Duration(0)
			input.Result.FirstAcceptedSolveTime = &value
		}},
		{name: "topology", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.OfficialResult.NoGame.Topology[0].SlotPosition++
		}},
		{name: "dependency", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.OfficialResult.NoGame.ResultDependency.SourceRevisionID = playoffRevisionID(8392)
		}},
		{name: "score recorded before resolution", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.OfficialResult.NoGame.Score.RecordedAt = input.OfficialResult.NoGame.ResolvedAt.Add(-time.Nanosecond)
		}},
		{name: "result recorded before resolution", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.OfficialResult.NoGame.Series.RecordedAt = input.OfficialResult.NoGame.ResolvedAt.Add(-time.Nanosecond)
		}},
		{name: "oversized topology", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.OfficialResult.NoGame.Topology = make(
				[]resultprojection.RecordedNoGameAttempt, domain.TournamentMaxParticipants+1,
			)
		}},
		{name: "duration overflow", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.Result.FirstEffectiveTime = time.Duration(math.MaxInt64)
			input.Result.SecondEffectiveTime = time.Second
		}},
		{name: "missing digest", mutate: func(input *playoff.TerminalSeriesEvidenceInput) {
			input.NoGameEvidenceDigest = ""
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := terminalSeriesEvidenceInput(base)
			test.mutate(&input)
			evidence, err := playoff.NewTerminalSeriesEvidence(input)
			require.Equal(t, playoff.TerminalSeriesEvidence{}, evidence)
			require.ErrorIs(t, err, playoff.ErrInvalidTerminalSeriesEvidence)
		})
	}
}

func playoffOfficialPointer(number int) *domain.OfficialResultRevisionID {
	id := playoffOfficialID(number)
	return &id
}

func goldenPositionInput(evidence playoff.GoldenPositionEvidence) playoff.GoldenPositionEvidenceInput {
	revisionIDs := evidence.RevisionIDs()
	positionFrom, positionTo := evidence.PositionInterval()
	previousRevisionID := revisionIDs[len(revisionIDs)-2]
	return playoff.GoldenPositionEvidenceInput{
		Scope: evidence.Scope(), RevisionID: revisionIDs[len(revisionIDs)-1],
		Revision: int64(len(revisionIDs)), PreviousRevisionID: &previousRevisionID,
		RevisionIDs: revisionIDs, PositionFrom: positionFrom, PositionTo: positionTo,
		Positions: evidence.Positions(), Attempts: evidence.Attempts(), PayloadDigest: evidence.PayloadDigest(),
	}
}
