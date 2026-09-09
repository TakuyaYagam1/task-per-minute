package postgres

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func TestProgressionGoldenReceiptRebuildsImpactfulTieBeforePlanning(t *testing.T) {
	input := progressionTiedSwissInput(t)
	identity := playoff.FinalSwissGoldenGroupIdentity{PositionFrom: 1, PositionTo: 3, GroupID: uuid.New(), RevisionID: domain.DerivedRevisionID(uuid.New())}
	expectedInput := input
	expectedInput.GoldenGroups = []playoff.FinalSwissGoldenGroupIdentity{identity}
	expected, err := playoff.PlanFinalSwissProgression(expectedInput)
	require.NoError(t, err)
	require.False(t, expected.AdvanceDirectly())
	// This is the former reader input: terminal evidence was complete, but
	// planning before attaching the persisted identity rejected the tie.
	_, err = playoff.PlanFinalSwissProgression(input)
	require.ErrorIs(t, err, playoff.ErrInvalidFinalSwissProjection)
	identityFree, err := playoff.PlanFinalSwissReceipt(input)
	require.NoError(t, err)
	digest := sha256.Sum256(identityFree.Projection().Payload())
	receipt := sqlc.LockTournamentProgressionFinalSwissReceiptChainRow{ProjectionRevisionID: input.RevisionID.UUID(), PhysicalProjectionRevision: int64(input.PhysicalProjectionRevision), CanonicalPayloadDigest: digest[:]}
	groups := []sqlc.LockTournamentProgressionGoldenSettlementsRow{{GroupID: identity.GroupID, GroupRevisionID: identity.RevisionID.UUID(), PositionFrom: 1, PositionTo: 3, SourceProjectionRevisionID: input.RevisionID.UUID(), SourceProjectionRevision: int64(input.PhysicalProjectionRevision)}}
	rebuilt, mapped, err := progressionPlanSwissReceipt(input, receipt, groups)
	require.NoError(t, err)
	require.Equal(t, identityFree.Projection().Payload(), rebuilt.Projection().Payload())
	require.Equal(t, expectedInput.GoldenGroups, mapped.GoldenGroups)
	require.Empty(t, input.GoldenGroups)

	for _, mutate := range []func(*sqlc.LockTournamentProgressionFinalSwissReceiptChainRow, []sqlc.LockTournamentProgressionGoldenSettlementsRow){
		func(_ *sqlc.LockTournamentProgressionFinalSwissReceiptChainRow, g []sqlc.LockTournamentProgressionGoldenSettlementsRow) {
			g[0].GroupRevisionID = uuid.Nil
		},
		func(_ *sqlc.LockTournamentProgressionFinalSwissReceiptChainRow, g []sqlc.LockTournamentProgressionGoldenSettlementsRow) {
			g[0].SourceProjectionRevision++
		},
		func(r *sqlc.LockTournamentProgressionFinalSwissReceiptChainRow, _ []sqlc.LockTournamentProgressionGoldenSettlementsRow) {
			r.CanonicalPayloadDigest = make([]byte, 32)
		},
	} {
		changedReceipt := receipt
		changedGroups := append([]sqlc.LockTournamentProgressionGoldenSettlementsRow(nil), groups...)
		mutate(&changedReceipt, changedGroups)
		_, _, err := progressionPlanSwissReceipt(input, changedReceipt, changedGroups)
		require.Error(t, err)
	}
}

func TestProgressionOrdinaryReceiptIdentity(t *testing.T) {
	input := progressionTiedSwissInput(t)
	for roundIndex := range input.Rounds {
		for seriesIndex, evidence := range input.Rounds[roundIndex].Series {
			official := evidence.OfficialResult()
			resultHead, scoreHead := official.Result, *official.Score
			resultHead.CommandID = scoreHead.CommandID // One authoritative result commit.
			resultNode, err := domain.NewProjectionRevision(domain.DerivedRevisionID(resultHead.ID.UUID()), input.TournamentID, official.ResultProjection.Revision().Artifact(), resultHead.Ordinal, nil, official.ResultProjection.Revision().CreatedAt(), official.ResultProjection.Payload())
			require.NoError(t, err)
			previous := domain.DerivedRevisionID(scoreHead.PreviousRevisionID.UUID())
			scoreNode, err := domain.NewProjectionRevision(domain.DerivedRevisionID(scoreHead.ID.UUID()), input.TournamentID, official.ScoreProjection.Revision().Artifact(), scoreHead.Ordinal, &previous, official.ScoreProjection.Revision().CreatedAt(), official.ScoreProjection.Payload())
			require.NoError(t, err)
			resultHead.SourceProjection, scoreHead.SourceProjection = resultNode.Revision(), scoreNode.Revision()
			official.Result, err = resultusecase.RestoreOrdinaryOfficialResultHead(resultHead)
			require.NoError(t, err)
			scoreHead, err = resultusecase.RestoreOrdinarySeriesScoreHead(scoreHead)
			require.NoError(t, err)
			official.Score, official.ResultProjection, official.ScoreProjection = &scoreHead, resultNode, &scoreNode
			input.Rounds[roundIndex].Series[seriesIndex], err = playoff.NewTerminalSeriesEvidence(playoff.TerminalSeriesEvidenceInput{Series: evidence.Series(), OfficialResult: official, Projection: resultNode, Result: evidence.PointResult()})
			require.NoError(t, err)
		}
	}
	receipt, err := playoff.PlanFinalSwissReceipt(input)
	require.NoError(t, err)
	require.NoError(t, receipt.Validate())
	other := input.Rounds[0].Series[1]
	official := other.OfficialResult()
	official.Result.CommandID = input.Rounds[0].Series[0].OfficialResult().Result.CommandID
	official.Score.CommandID = official.Result.CommandID
	require.NoError(t, official.Result.Validate())
	require.NoError(t, official.Score.Validate())
	input.Rounds[0].Series[1], err = playoff.NewTerminalSeriesEvidence(playoff.TerminalSeriesEvidenceInput{
		Series: other.Series(), OfficialResult: official, Projection: other.Projection(), Result: other.PointResult(),
	})
	require.NoError(t, err)
	_, err = playoff.PlanFinalSwissReceipt(input)
	require.ErrorContains(t, err, "identity aliases result commit command", "one commit cannot authorize two Series")
}

func progressionTiedSwissInput(t *testing.T) playoff.ProgressionSwissInput {
	t.Helper()
	now := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)
	participants := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	input := playoff.ProgressionSwissInput{TournamentID: uuid.New(), Preset: domain.TournamentPresetV1, ProjectionID: uuid.New(), RevisionID: domain.DerivedRevisionID(uuid.New()), RevisionNo: 1, PhysicalProjectionRevision: 1, ParticipantIDs: participants, CreatedAt: now}
	for i, id := range participants {
		input.Seeds = append(input.Seeds, swissusecase.ParticipantSeed{ParticipantID: id, Seed: 4 - i})
	}
	// Each of the first three players has two wins and identical tie-breaks.
	pairs := [][2][3]int{{{0, 1, 0}, {2, 3, 2}}, {{0, 2, 2}, {1, 3, 1}}, {{0, 3, 0}, {1, 2, 1}}}
	rosterID := uuid.New()
	for roundIndex, matches := range pairs {
		round := playoff.ProgressionSwissRound{RoundID: uuid.New(), RoundNumber: roundIndex + 1, RevisionID: uuid.New()}
		locked := make([]swissusecase.LockedSeries, 0, 2)
		for _, match := range matches {
			evidence := progressionTerminalFixture(t, input.TournamentID, round.RoundID, round.RoundNumber, participants[match[0]], participants[match[1]], participants[match[2]], now)
			round.Series = append(round.Series, evidence)
			series := evidence.Series()
			locked = append(locked, swissusecase.LockedSeries{SeriesID: series.ID, PairingID: uuid.New(), FirstParticipantID: series.FirstParticipantID, SecondParticipantID: series.SecondParticipantID, CategoryRevisionID: uuid.New(), CategoryRevision: 1, AssignmentID: uuid.New(), AssignmentRevision: 1, AssignmentPlanID: uuid.New(), AssignmentPlanRevisionID: uuid.New(), ReservationID: uuid.New(), ReservationRevision: 1})
		}
		proof, err := swissusecase.NewRoundLockProof(swissusecase.RoundLockProofInput{TournamentID: input.TournamentID, RosterID: rosterID, RoundID: round.RoundID, Preset: input.Preset, RoundNumber: round.RoundNumber, SourceProjectionRevisionID: round.RevisionID, NormalPoolRevisionID: uuid.New(), PreflightRevisionID: uuid.New(), WaveID: uuid.New(), WaveRevisionID: domain.WaveRevisionID(uuid.New()), Revisions: swissusecase.RoundLockRevisions{Round: 1, SourceProjection: 1, Roster: 1, NormalPool: 1, History: 1, Wave: 1}, RosterParticipantIDs: participants, Series: locked})
		require.NoError(t, err)
		round.LockProof = proof
		input.Rounds = append(input.Rounds, round)
	}
	return input
}

func progressionTerminalFixture(t *testing.T, tournamentID, roundID uuid.UUID, round int, first, second, winner uuid.UUID, now time.Time) playoff.TerminalSeriesEvidence {
	t.Helper()
	seriesID := uuid.New()
	resultID := domain.OfficialResultRevisionID(uuid.New())
	scoreID, previousScoreID := domain.SeriesScoreRevisionID(uuid.New()), domain.SeriesScoreRevisionID(uuid.New())
	resultProjection, err := domain.NewProjectionRevision(domain.DerivedRevisionID(uuid.New()), tournamentID, domain.ArtifactRef{Kind: domain.ArtifactKindSeriesResult, EntityID: seriesID}, 1, nil, now.Add(-time.Minute), []byte("terminal result"))
	require.NoError(t, err)
	scoreProjection, err := domain.NewProjectionRevision(domain.DerivedRevisionID(uuid.New()), tournamentID, domain.ArtifactRef{Kind: domain.ArtifactKindSeriesScore, EntityID: seriesID}, 1, nil, now.Add(-time.Minute-2*time.Second), []byte("terminal score"))
	require.NoError(t, err)
	score := domain.SeriesScore{}
	if winner == first {
		score.FirstParticipantWins = 1
	} else {
		score.SecondParticipantWins = 1
	}
	series := domain.Series{ID: seriesID, TournamentID: tournamentID, FirstParticipantID: first, SecondParticipantID: second, Format: domain.SeriesFormatBO1, State: domain.SeriesStateCompleted, Score: score, WinnerID: &winner, CurrentScoreRevisionID: &scoreID, CurrentResultRevisionID: &resultID}
	attempt := resultusecase.SeriesScoreAttemptReference{SlotID: uuid.New(), SlotPosition: 1, GameID: uuid.New(), AttemptNo: 1, State: domain.GameStateCompleted, WinnerID: &winner, Reason: domain.GameResultReasonSolved, CurrentGameResultRevisionID: domain.OfficialResultRevisionID(uuid.New())}
	scoreHead := resultusecase.SeriesScoreRevisionHead{Scope: resultusecase.SeriesScoreRevisionScope{TournamentID: tournamentID, SeriesID: seriesID}, ID: scoreID, PreviousRevisionID: &previousScoreID, Ordinal: 2, Operation: resultusecase.SeriesScoreRevisionOperationAppendAttempt, CommandID: uuid.New(), Actor: domain.ResultActor{Kind: domain.ResultActorServer}, CommandAttempt: &attempt, FirstParticipantID: first, SecondParticipantID: second, Format: domain.SeriesFormatBO1, Score: score, Attempts: []resultusecase.SeriesScoreAttemptReference{attempt}, SourceProjection: scoreProjection.Revision(), RecordedAt: now.Add(-time.Minute - time.Second)}
	official := resultusecase.OfficialResultRevisionHead{Scope: resultusecase.OfficialResultScope{TournamentID: tournamentID, SeriesID: seriesID, Kind: resultusecase.OfficialResultSubjectSeries}, ID: resultID, Ordinal: 1, CommandID: uuid.New(), Actor: domain.ResultActor{Kind: domain.ResultActorServer}, Outcome: resultusecase.OfficialResultOutcome{SeriesState: domain.SeriesStateCompleted, SeriesReason: domain.SeriesResultReasonScoreComplete, WinnerID: &winner, ScoreRevisionID: &scoreID}, SourceProjection: resultProjection.Revision(), RecordedAt: now.Add(-time.Minute)}
	evidence, err := playoff.NewTerminalSeriesEvidence(playoff.TerminalSeriesEvidenceInput{Series: series, OfficialResult: resultprojection.OfficialResultProjectionInput{TerminalSource: resultprojection.TerminalResultSourcePlayed, Result: official, ResultProjection: resultProjection, Score: &scoreHead, ScoreProjection: &scoreProjection}, Projection: resultProjection, Result: swissusecase.SeriesPointResult{RoundID: roundID, RoundNumber: round, SeriesID: seriesID, ResultRevisionID: resultID, FirstParticipantID: first, SecondParticipantID: second, WinnerID: &winner, Label: swissusecase.SeriesResultPlayed, FirstEffectiveTime: time.Second, SecondEffectiveTime: time.Second}})
	require.NoError(t, err)
	return evidence
}
