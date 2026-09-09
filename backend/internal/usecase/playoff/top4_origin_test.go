package playoff_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	result "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
)

func TestTop4AcceptsValidatedOrdinaryResultLineage(t *testing.T) {
	fixture := newPlayoffFixture(t, false)
	input := progressionSwissInput(fixture.command)
	var heads []playoff.TerminalSeriesEvidence
	for roundIndex := range input.Rounds {
		for index, evidence := range input.Rounds[roundIndex].Series {
			official := evidence.OfficialResult()
			resultHead, scoreHead := official.Result, *official.Score
			resultHead.CommandID = scoreHead.CommandID
			var previousResult *domain.DerivedRevisionID
			if resultHead.PreviousRevisionID != nil {
				value := domain.DerivedRevisionID(resultHead.PreviousRevisionID.UUID())
				previousResult = &value
			}
			resultNode, err := domain.NewProjectionRevision(domain.DerivedRevisionID(resultHead.ID.UUID()), input.TournamentID, official.ResultProjection.Revision().Artifact(), resultHead.Ordinal, previousResult, resultHead.RecordedAt, official.ResultProjection.Payload())
			require.NoError(t, err)
			var previousScore *domain.DerivedRevisionID
			if scoreHead.PreviousRevisionID != nil {
				value := domain.DerivedRevisionID(scoreHead.PreviousRevisionID.UUID())
				previousScore = &value
			}
			scoreNode, err := domain.NewProjectionRevision(domain.DerivedRevisionID(scoreHead.ID.UUID()), input.TournamentID, official.ScoreProjection.Revision().Artifact(), scoreHead.Ordinal, previousScore, scoreHead.RecordedAt, official.ScoreProjection.Payload())
			require.NoError(t, err)
			resultHead.SourceProjection, scoreHead.SourceProjection = resultNode.Revision(), scoreNode.Revision()
			official.Result, err = result.RestoreOrdinaryOfficialResultHead(resultHead)
			require.NoError(t, err)
			scoreHead, err = result.RestoreOrdinarySeriesScoreHead(scoreHead)
			require.NoError(t, err)
			official.Score, official.ResultProjection, official.ScoreProjection = &scoreHead, resultNode, &scoreNode
			restored, err := playoff.NewTerminalSeriesEvidence(playoff.TerminalSeriesEvidenceInput{Series: evidence.Series(), OfficialResult: official, Projection: resultNode, Result: evidence.PointResult()})
			require.NoError(t, err)
			input.Rounds[roundIndex].Series[index] = restored
			heads = append(heads, restored)
		}
	}
	source, err := playoff.PlanFinalSwissReceipt(input)
	require.NoError(t, err)
	command := playoff.Top4SnapshotCommand{TournamentID: input.TournamentID, RevisionID: domain.DerivedRevisionID(uuid.New()), RevisionNo: 1,
		Source: source, CurrentTerminalSeries: heads, CreatedAt: input.CreatedAt.Add(time.Second)}
	snapshot, err := playoff.PlanTop4Snapshot(command)
	require.NoError(t, err)
	require.NoError(t, snapshot.Validate())
	for _, id := range []uuid.UUID{heads[0].OfficialResult().Result.ID.UUID(), heads[0].OfficialResult().Score.CommandID, input.ParticipantIDs[0]} {
		changed := command
		changed.RevisionID = domain.DerivedRevisionID(id)
		_, err := playoff.PlanTop4Snapshot(changed)
		require.Error(t, err, "new Top4 identity cannot alias retained authority")
	}
	other := heads[1].OfficialResult()
	other.Result.CommandID = heads[0].OfficialResult().Result.CommandID
	other.Score.CommandID = other.Result.CommandID
	changed, err := playoff.NewTerminalSeriesEvidence(playoff.TerminalSeriesEvidenceInput{Series: heads[1].Series(), OfficialResult: other, Projection: heads[1].Projection(), Result: heads[1].PointResult()})
	require.NoError(t, err)
	command.CurrentTerminalSeries = append([]playoff.TerminalSeriesEvidence(nil), heads...)
	command.CurrentTerminalSeries[1] = changed
	_, err = playoff.PlanTop4Snapshot(command)
	require.Error(t, err, "one settlement command cannot authorize another Series")
}
