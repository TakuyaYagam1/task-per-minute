package playoff_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

func testTop4SnapshotLineage(t *testing.T) {
	t.Helper()
	t.Run("exports a graph-valid predecessor edge", func(t *testing.T) {
		fixture := newPlayoffFixture(t, false)
		finalSwiss, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		first, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(7710), RevisionNo: 1,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)
		second, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(7711), RevisionNo: 2, Previous: &first,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute),
		})
		require.NoError(t, err)
		require.Contains(t, second.Dependencies(), domain.RevisionDependency{
			SourceRevisionID:  first.Projection().Revision().ID(),
			DerivedRevisionID: second.Projection().Revision().ID(),
		})
		graph, err := domain.NewRevisionGraph(
			[]domain.ProjectionRevision{finalSwiss.Projection(), first.Projection(), second.Projection()},
			append(first.Dependencies(), second.Dependencies()...),
		)
		require.NoError(t, err)
		require.True(t, graph.DependsOn(second.Projection().Revision().ID(), first.Projection().Revision().ID()))
		for _, dependency := range second.Dependencies() {
			for _, reference := range second.TerminalSeriesReferences() {
				require.False(t, dependency.SourceRevisionID == reference.ProjectionRevisionID &&
					dependency.DerivedRevisionID == second.Projection().Revision().ID())
			}
		}
		gap, gapErr := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(7712), RevisionNo: 3, Previous: &first,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(3 * time.Minute),
		})
		require.Equal(t, playoff.Top4Snapshot{}, gap)
		require.ErrorIs(t, gapErr, playoff.ErrInvalidTop4Snapshot)
	})

	t.Run("keeps a long successor chain bounded", func(t *testing.T) {
		fixture := newPlayoffFixture(t, false)
		finalSwiss, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		current, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(13001), RevisionNo: 1,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)
		payloadLimit := len(current.Projection().Payload()) + 128
		for revisionNo := 2; revisionNo <= 48; revisionNo++ {
			current, err = playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
				TournamentID: fixture.command.TournamentID,
				RevisionID:   playoffRevisionID(13000 + revisionNo), RevisionNo: revisionNo,
				Previous: &current, Source: finalSwiss,
				CurrentTerminalSeries: terminalEvidence(fixture.command),
				CreatedAt:             fixture.command.CreatedAt.Add(time.Duration(revisionNo) * time.Minute),
			})
			require.NoError(t, err)
			require.NoError(t, current.Validate())
			require.LessOrEqual(t, len(current.Projection().Payload()), payloadLimit)
		}
	})

	t.Run("rejects transitive Top 4 and nested standings identity reuse", func(t *testing.T) {
		fixture := newPlayoffFixture(t, false)
		finalSwiss, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		first, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(13101), RevisionNo: 1,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.NoError(t, err)
		second, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(13102), RevisionNo: 2, Previous: &first,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(2 * time.Minute),
		})
		require.NoError(t, err)
		aliased, aliasErr := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   first.Projection().Revision().ID(), RevisionNo: 3, Previous: &second,
			Source: finalSwiss, CurrentTerminalSeries: terminalEvidence(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(3 * time.Minute),
		})
		require.Equal(t, playoff.Top4Snapshot{}, aliased)
		require.ErrorIs(t, aliasErr, playoff.ErrInvalidTop4Snapshot)

		firstFinal := finalSwiss
		currentFinal := firstFinal
		for revisionNo := 2; revisionNo <= 4; revisionNo++ {
			command := fixture.command
			command.RevisionID = playoffRevisionID(13200 + revisionNo)
			command.RevisionNo = revisionNo
			command.PhysicalProjectionRevision = revisionNo
			command.Previous = &currentFinal
			command.CreatedAt = fixture.command.CreatedAt.Add(time.Duration(revisionNo) * time.Minute)
			currentFinal, err = playoff.PlanFinalSwissProjection(command)
			require.NoError(t, err)
		}
		aliased, aliasErr = playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   firstFinal.Projection().Revision().ID(), RevisionNo: 1,
			Source: currentFinal, CurrentTerminalSeries: terminalEvidence(fixture.command),
			CreatedAt: fixture.command.CreatedAt.Add(5 * time.Minute),
		})
		require.Equal(t, playoff.Top4Snapshot{}, aliased)
		require.ErrorIs(t, aliasErr, playoff.ErrInvalidTop4Snapshot)

		tied := newPlayoffFixture(t, true)
		tiedFirst, err := playoff.PlanFinalSwissProjection(tied.command)
		require.NoError(t, err)
		tiedCurrent := tiedFirst
		for revisionNo := 2; revisionNo <= 4; revisionNo++ {
			command := tied.command
			command.RevisionID = playoffRevisionID(13300 + revisionNo)
			for index := range command.GoldenGroups {
				command.GoldenGroups[index].GroupID = playoffID(13320 + revisionNo*10 + index*2)
				command.GoldenGroups[index].RevisionID = playoffRevisionID(13321 + revisionNo*10 + index*2)
			}
			command.RevisionNo = revisionNo
			command.PhysicalProjectionRevision = revisionNo
			command.Previous = &tiedCurrent
			command.CreatedAt = tied.command.CreatedAt.Add(time.Duration(revisionNo) * time.Minute)
			tiedCurrent, err = playoff.PlanFinalSwissProjection(command)
			require.NoError(t, err)
			tied.command.GoldenGroups = command.GoldenGroups
		}
		positions := newGoldenPositionEvidence(
			t, tied,
			[]uuid.UUID{tied.participants[0], tied.participants[1], tied.participants[2]},
		)
		aliased, aliasErr = playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: tied.command.TournamentID,
			RevisionID:   playoffRevisionID(13310), RevisionNo: 1,
			Source: tiedCurrent, CurrentTerminalSeries: terminalEvidence(tied.command),
			GoldenSettlements: []playoff.Top4GoldenSettlement{{
				RevisionID: tiedFirst.Projection().Revision().ID(), RevisionNo: 2,
				Positions: &positions, FinalizedAt: tied.command.CreatedAt.Add(5 * time.Minute),
			}},
			CreatedAt: tied.command.CreatedAt.Add(6 * time.Minute),
		})
		require.Equal(t, playoff.Top4Snapshot{}, aliased)
		require.ErrorIs(t, aliasErr, playoff.ErrInvalidTop4Snapshot)
	})

	t.Run("rejects oversized authority before cloning evidence", func(t *testing.T) {
		fixture := newPlayoffFixture(t, false)
		finalSwiss, err := playoff.PlanFinalSwissProjection(fixture.command)
		require.NoError(t, err)
		heads := make([]playoff.TerminalSeriesEvidence, domain.TournamentMaxParticipants*8)
		snapshot, err := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: fixture.command.TournamentID,
			RevisionID:   playoffRevisionID(7720), RevisionNo: 1,
			Source: finalSwiss, CurrentTerminalSeries: heads,
			CreatedAt: fixture.command.CreatedAt.Add(time.Minute),
		})
		require.Equal(t, playoff.Top4Snapshot{}, snapshot)
		require.ErrorIs(t, err, playoff.ErrInvalidTop4Snapshot)

		tied := newPlayoffFixture(t, true)
		tiedFinal, err := playoff.PlanFinalSwissProjection(tied.command)
		require.NoError(t, err)
		settlements := make([]playoff.Top4GoldenSettlement, len(tiedFinal.GoldenGroups())+1)
		_, err = playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
			TournamentID: tied.command.TournamentID,
			RevisionID:   playoffRevisionID(7727), RevisionNo: 1,
			Source: tiedFinal, CurrentTerminalSeries: terminalEvidence(tied.command),
			GoldenSettlements: settlements,
			CreatedAt:         tied.command.CreatedAt.Add(2 * time.Minute),
		})
		require.ErrorIs(t, err, playoff.ErrInvalidTop4Snapshot)

		for name, mutate := range map[string]func(*goldenusecase.GoldenState){
			"ready windows": func(state *goldenusecase.GoldenState) {
				state.Windows = make([]goldenusecase.GoldenReadyWindow, domain.TournamentMaxParticipants+1)
			},
			"task history": func(state *goldenusecase.GoldenState) {
				state.ExactPlan.Authority.History = make(
					[]assignmentusecase.TaskReceiptRef, domain.TournamentMaxParticipants*2+1,
				)
			},
		} {
			t.Run(name, func(t *testing.T) {
				state := newTop4GoldenAllocationState(t, tiedFinal, true)
				mutate(&state)
				_, planErr := playoff.PlanTop4Snapshot(playoff.Top4SnapshotCommand{
					TournamentID: tied.command.TournamentID,
					RevisionID:   playoffRevisionID(7730), RevisionNo: 1,
					Source: tiedFinal, CurrentTerminalSeries: terminalEvidence(tied.command),
					GoldenSettlements: []playoff.Top4GoldenSettlement{{
						RevisionID: playoffRevisionID(7731), RevisionNo: 2,
						State: &state, FinalizedAt: tied.command.CreatedAt.Add(time.Minute),
					}},
					CreatedAt: tied.command.CreatedAt.Add(2 * time.Minute),
				})
				require.ErrorIs(t, planErr, playoff.ErrInvalidTop4Snapshot)
			})
		}
	})
}
