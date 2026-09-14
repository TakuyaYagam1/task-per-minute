package recovery

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
)

type planRecordingTerminalAdvancer struct {
	commands []playoff.TerminalSeriesCommand
}

func (advancer *planRecordingTerminalAdvancer) AdvanceAfterSeriesSettlement(
	_ context.Context,
	command playoff.TerminalSeriesCommand,
) (playoff.TerminalReceipt, error) {
	advancer.commands = append(advancer.commands, command)
	return playoff.TerminalReceipt{}, nil
}

func TestAdvanceTerminalPlanAdvancesCompletedReadyWindowSeriesOnly(t *testing.T) {
	tournamentID := uuid.New()
	completedSeriesID := uuid.New()
	cancelledSeriesID := uuid.New()
	advancer := &planRecordingTerminalAdvancer{}
	handler := NewTerminalDeadlineHandlerWithDependencies(nil, nil, nil, advancer)
	plan := DeadlineTerminalPlan{
		ReadyWindow: []gameusecase.NoShowResolution{
			{
				Scope: domain.NormalNoShowScope{
					TournamentID: tournamentID,
					SeriesID:     completedSeriesID,
				},
				Series: seriesdomain.Execution{Series: domain.Series{
					ID:           completedSeriesID,
					State:        domain.SeriesStateCompleted,
					TournamentID: tournamentID,
				}},
			},
			{
				Scope: domain.NormalNoShowScope{
					TournamentID: tournamentID,
					SeriesID:     cancelledSeriesID,
				},
				Series: seriesdomain.Execution{Series: domain.Series{
					ID:           cancelledSeriesID,
					State:        domain.SeriesStateCancelled,
					TournamentID: tournamentID,
				}},
			},
		},
	}

	require.NoError(t, handler.advanceTerminalPlan(context.Background(), plan))
	require.Equal(t, []playoff.TerminalSeriesCommand{{
		TournamentID: tournamentID,
		SeriesID:     completedSeriesID,
	}}, advancer.commands)
}
