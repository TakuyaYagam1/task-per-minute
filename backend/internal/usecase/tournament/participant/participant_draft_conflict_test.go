package participant_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	tournamentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant"
	participantmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/participant/mocks"
)

type draftConflictWorkflow struct {
	err error
}

func (workflow draftConflictWorkflow) Apply(_ context.Context, _ draftusecase.PlayerActionCommand) (draftusecase.ActionResult, error) {
	return draftusecase.ActionResult{}, workflow.err
}

func TestCommandCoordinatorNormalizesDraftActionErrors(t *testing.T) {
	t.Parallel()

	actor := inbound.Identity{PlayerID: uuid.New()}
	command := inbound.DraftActionCommand{
		Actor:                      actor,
		TournamentID:               uuid.New(),
		SeriesID:                   uuid.New(),
		CommandID:                  uuid.New(),
		ExpectedProjectionRevision: 9,
		ExpectedDraftRevision:      2,
		ExpectedTurn:               2,
		Action:                     domain.DraftActionBan,
		Category:                   domain.CategoryPwn,
	}
	authority := tournamentusecase.ParticipantCommandAuthority{
		TournamentID:         command.TournamentID,
		RosterID:             uuid.New(),
		PlayerID:             actor.PlayerID,
		ParticipantID:        uuid.New(),
		TournamentState:      domain.TournamentStateSwiss,
		ProjectionRevisionID: uuid.New(),
		ProjectionRevision:   command.ExpectedProjectionRevision,
	}
	resolved := tournamentusecase.ResolvedDraftAction{
		Authority: authority,
		SeriesID:  command.SeriesID,
		Command: draftusecase.PlayerActionCommand{
			DraftID:              uuid.New(),
			ExpectedRevisionID:   uuid.New(),
			ExpectedRevision:     command.ExpectedDraftRevision,
			ExpectedServiceEpoch: uuid.New(),
			ExpectedTurn:         command.ExpectedTurn,
			CommandID:            command.CommandID,
			ResultRevisionID:     uuid.New(),
			ActionID:             uuid.New(),
			ActorID:              authority.ParticipantID,
			Action:               command.Action,
			Category:             command.Category,
		},
	}

	persistenceErr := errors.New("persistence write failed")
	tests := []struct {
		name         string
		err          error
		wantConflict bool
		wantErrorIs  error
		wantSame     bool
	}{
		{name: "deadline", err: domain.ErrDraftDeadline, wantConflict: true},
		{name: "stale turn", err: domain.ErrDraftStaleTurn, wantConflict: true},
		{name: "completed", err: domain.ErrDraftCompleted, wantConflict: true},
		{name: "action conflict", err: draftusecase.ErrActionConflict, wantConflict: true},
		{name: "internal", err: domain.ErrInternal, wantErrorIs: domain.ErrInternal, wantSame: true},
		{
			name:        "wrapped illegal action",
			err:         fmt.Errorf("BO3 final action: %w", domain.ErrDraftIllegalAction),
			wantErrorIs: domain.ErrValidation,
		},
		{
			name:        "wrapped category used",
			err:         fmt.Errorf("BO3 final action: %w", domain.ErrDraftCategoryUsed),
			wantErrorIs: domain.ErrValidation,
		},
		{
			name:        "wrapped persistence error",
			err:         fmt.Errorf("draft repository write: %w", persistenceErr),
			wantErrorIs: persistenceErr,
			wantSame:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			transactions := participantmocks.NewMockParticipantTransactionManager(t)
			transactions.EXPECT().
				Do(mock.Anything, mock.Anything).
				RunAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
					return fn(ctx)
				}).
				Once()
			authorityMock := participantmocks.NewMockCommandAuthority(t)
			authorityMock.EXPECT().
				ResolveDraftAction(mock.Anything, command).
				Return(resolved, nil).
				Once()

			coordinator := tournamentusecase.NewCommandCoordinator(tournamentusecase.CommandCoordinatorDependencies{
				Transactions: transactions,
				Authority:    authorityMock,
				Draft:        draftConflictWorkflow{err: test.err},
				Postseason:   participantmocks.NewMockParticipantPostseasonWorkflow(t),
			})

			_, err := coordinator.SubmitDraftAction(context.Background(), command)
			if !test.wantConflict {
				if test.wantSame {
					require.Same(t, test.err, err)
				}
				require.ErrorIs(t, err, test.wantErrorIs)
				var conflict *inbound.RevisionConflictError
				require.NotErrorAs(t, err, &conflict)
				return
			}

			var conflict *inbound.RevisionConflictError
			require.ErrorAs(t, err, &conflict)
			require.ErrorIs(t, err, domain.ErrConflict)
			require.Equal(t, command.TournamentID, conflict.TournamentID)
			require.Equal(t, command.ExpectedProjectionRevision, conflict.ExpectedRevision)
			require.Equal(t, authority.ProjectionRevision, conflict.CurrentRevision)
			require.Equal(t, authority.TournamentState, conflict.CurrentState)
		})
	}
}
