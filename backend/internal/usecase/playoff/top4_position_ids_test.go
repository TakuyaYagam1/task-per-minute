package playoff_test

import (
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestTop4GoldenPositionCommitIDsAreSealedCopies(t *testing.T) {
	fixture := newPlayoffFixture(t, true)
	finalSwiss, err := playoff.PlanFinalSwissProjection(fixture.command)
	require.NoError(t, err)
	positions := newGoldenPositionEvidence(t, fixture, []uuid.UUID{fixture.participants[2], fixture.participants[0], fixture.participants[1]})
	snapshot, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{TournamentID: fixture.command.TournamentID, RevisionID: playoffRevisionID(702), RevisionNo: 1, Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command), GoldenSettlements: []playoff.Top4GoldenSettlement{{RevisionID: playoffRevisionID(701), RevisionNo: 2, Positions: &positions, FinalizedAt: fixture.command.CreatedAt.Add(time.Minute)}}, CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute)})
	require.NoError(t, err)
	ids := snapshot.GoldenPositionCommitIDs()
	wanted := make([]uuid.UUID, 0, len(positions.Positions()))
	for _, position := range positions.Positions() {
		wanted = append(wanted, position.CommitID)
	}
	require.ElementsMatch(t, wanted, ids)
	ids[0] = uuid.Nil
	require.ElementsMatch(t, wanted, snapshot.GoldenPositionCommitIDs())
	require.NoError(t, snapshot.Validate())

	directFixture := newPlayoffFixture(t, false)
	directSwiss, err := playoff.PlanFinalSwissProjection(directFixture.command)
	require.NoError(t, err)
	direct, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{TournamentID: directFixture.command.TournamentID, RevisionID: playoffRevisionID(702), RevisionNo: 1, Source: directSwiss, CurrentTerminalSeries: terminalEvidence(directFixture.command), CreatedAt: directFixture.command.CreatedAt.Add(time.Minute)})
	require.NoError(t, err)
	require.Empty(t, direct.GoldenPositionCommitIDs())
}
