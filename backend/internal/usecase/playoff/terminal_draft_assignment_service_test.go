package playoff_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
	draftusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft"
	draftmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/draft/mocks"
	playoff "github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	playoffmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff/mocks"
)

func TestFinalDraftAssignmentService_RehydrateFinalBindingsRejectsAuthoritativePlanInconsistency(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		plan      *assignmentusecase.ExactDraftBranchPlan
		readerErr error
	}{
		{name: "nil plan"},
		{name: "reader not found", readerErr: assignmentusecase.ErrExactDraftBranchPlanNotFound},
		{name: "wrong plan identity", plan: &assignmentusecase.ExactDraftBranchPlan{ID: uuid.New()}},
		{name: "wrong plan revision", plan: &assignmentusecase.ExactDraftBranchPlan{RevisionID: uuid.New()}},
		{name: "wrong source draft", plan: &assignmentusecase.ExactDraftBranchPlan{SourceDraft: draftusecase.Execution{ID: uuid.New()}}},
		{name: "wrong completion revision", plan: &assignmentusecase.ExactDraftBranchPlan{CompletionDraftRevisionID: uuid.New()}},
		{name: "not committed", plan: &assignmentusecase.ExactDraftBranchPlan{State: assignmentusecase.ExactDraftBranchPlanStatePlanned}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			authority := terminalRehydrateAuthorityFixture(t)
			authority.Bindings = []playoff.FinalGameBinding{terminalRehydrateBindingFixture(authority.IDs, 1)}
			reader := playoffmocks.NewMockExactDraftCommittedPlanReader(t)
			reader.EXPECT().LoadCommittedExactDraftPlan(mock.Anything, authority.IDs.DraftAssignmentPlanID).
				Return(test.plan, test.readerErr).Once()
			service := playoff.NewFinalDraftAssignmentService(nil, nil, reader)

			bindings, err := service.RehydrateFinalBindings(t.Context(), authority)

			require.Nil(t, bindings)
			require.ErrorIs(t, err, domain.ErrConflict)
		})
	}
}

func TestFinalDraftAssignmentService_RehydrateFinalBindingsRejectsNonPrefixBindings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		bindings func(playoff.FinalStageIDs) []playoff.FinalGameBinding
	}{
		{
			name: "missing first binding",
			bindings: func(ids playoff.FinalStageIDs) []playoff.FinalGameBinding {
				return []playoff.FinalGameBinding{terminalRehydrateBindingFixture(ids, 2)}
			},
		},
		{
			name: "premature second binding",
			bindings: func(ids playoff.FinalStageIDs) []playoff.FinalGameBinding {
				return []playoff.FinalGameBinding{
					terminalRehydrateBindingFixture(ids, 1),
					terminalRehydrateBindingFixture(ids, 2),
				}
			},
		},
		{
			name: "duplicate binding",
			bindings: func(ids playoff.FinalStageIDs) []playoff.FinalGameBinding {
				binding := terminalRehydrateBindingFixture(ids, 1)
				return []playoff.FinalGameBinding{binding, binding}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			authority := terminalRehydrateAuthorityFixture(t)
			authority.Bindings = test.bindings(authority.IDs)
			service := playoff.NewFinalDraftAssignmentService(
				nil,
				nil,
				playoffmocks.NewMockExactDraftCommittedPlanReader(t),
			)

			bindings, err := service.RehydrateFinalBindings(t.Context(), authority)

			require.Nil(t, bindings)
			require.ErrorIs(t, err, domain.ErrConflict)
		})
	}
}

func terminalRehydrateBindingFixture(ids playoff.FinalStageIDs, position int) playoff.FinalGameBinding {
	gameIDs := [...]uuid.UUID{ids.FirstGameID, ids.SecondGameID, ids.ThirdGameID}
	digest := sha256.Sum256([]byte{byte(position)})
	return playoff.FinalGameBinding{
		GameID:             gameIDs[position-1],
		AssignmentID:       ids.GameAssignmentID(position),
		AssignmentRevision: 1,
		PlanID:             ids.DraftAssignmentPlanID,
		PlanRevisionID:     ids.DraftAssignmentRevisionID,
		BranchID:           ids.DraftAssignmentChildBranchID("completed-final", position),
		ReservationID:      ids.DraftAssignmentReservationID("completed-final", position, 1),
		SnapshotID:         ids.DraftAssignmentSnapshotID("completed-final", position, 1),
		ContentDigest:      digest,
		DeadlineSeconds:    60,
	}
}

func terminalRehydrateAuthorityFixture(t *testing.T) playoff.FinalSettlementAuthority {
	t.Helper()

	ids, err := playoff.FinalStageIdentity(uuid.New())
	require.NoError(t, err)
	createdAt := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)
	draft := terminalRehydrateCompletedDraft(t, ids, createdAt)
	gameWinnerID := draft.FirstParticipantID
	gameResultID := domain.OfficialResultRevisionID(uuid.New())
	scoreRevisionID := domain.SeriesScoreRevisionID(uuid.New())
	return playoff.FinalSettlementAuthority{
		StageCommandID: uuid.New(),
		RosterID:       uuid.New(),
		Bracket:        terminalRehydrateBracketFixture(),
		Advancement:    []playoff.SemifinalAdvancementResult{{}, {}},
		Draft:          draft,
		IDs:            ids,
		Progression: playoff.FinalProgressionCommand{Progression: seriesdomain.ScoreProgressionCommand{
			Game: domain.Game{
				ID: ids.FirstGameID, SlotID: ids.FirstSlotID, AttemptNo: 1,
				State: domain.GameStateCompleted, ResultReason: domain.GameResultReasonSolved,
				WinnerID: &gameWinnerID, ResultRevisionID: &gameResultID,
			},
			ScoreRevision: seriesdomain.ScoreRevision{
				ID: scoreRevisionID, SeriesID: ids.FinalSeriesID,
				RecordedAt: createdAt.Add(10 * time.Minute),
			},
			Next: &seriesdomain.NextGameWave{
				WaveID: ids.SecondWaveID, WaveRevisionID: ids.SecondWaveRevisionID,
				SlotID: ids.SecondSlotID, GameID: ids.SecondGameID, Category: domain.CategoryPwn,
			},
		}},
		RecordedAt: createdAt.Add(10 * time.Minute),
	}
}

func terminalRehydrateCompletedDraft(
	t *testing.T,
	ids playoff.FinalStageIDs,
	createdAt time.Time,
) draftusecase.Execution {
	t.Helper()

	category := draftusecase.CategoryRevision{
		ID: ids.CategoryRevisionID, TournamentID: uuid.New(), SeriesID: ids.FinalSeriesID,
		RosterID: uuid.New(), Revision: 1, Stage: domain.TournamentStageFinal,
		Format: domain.SeriesFormatBO3, Mode: domain.CategoryModeDraft, SourceContentRevision: 1,
		CategoryPool: domain.CategoryPoolRevision{
			ID: uuid.New(), Revision: 1, Format: domain.SeriesFormatBO3,
			Categories: []domain.Category{
				domain.CategoryCrypto, domain.CategoryForensics, domain.CategoryPwn,
				domain.CategoryReverse, domain.CategoryWeb,
			},
		},
		CreatedAt: createdAt,
	}
	require.NoError(t, category.Validate())
	initial, err := draftusecase.StartExecution(draftusecase.ExecutionStartCommand{
		CategoryRevision: category, DraftID: ids.DraftID, InitialRevisionID: ids.DraftInitialRevisionID,
		DecisionEvidenceID: ids.DraftOrderDecisionID,
		ParticipantIDs:     [2]uuid.UUID{uuid.New(), uuid.New()},
		ServiceEpoch:       ids.DraftServiceEpochID, CommandID: uuid.New(), StartedAt: createdAt,
	})
	require.NoError(t, err)

	state := draftusecase.CloneExecution(initial)
	repository := draftmocks.NewMockRepository(t)
	repository.EXPECT().FindDraftCommand(mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil).Maybe()
	repository.EXPECT().LoadDraft(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ uuid.UUID) (*draftusecase.Execution, error) {
			current := draftusecase.CloneExecution(state)
			return &current, nil
		}).Maybe()
	repository.EXPECT().CommitDraftRevisions(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			_ draftusecase.RevisionExpectation,
			revisions []draftusecase.Execution,
		) (*draftusecase.Execution, bool, error) {
			state = draftusecase.CloneExecution(revisions[len(revisions)-1])
			current := draftusecase.CloneExecution(state)
			return &current, true, nil
		}).Maybe()

	for _, category := range []domain.Category{
		domain.CategoryCrypto,
		domain.CategoryForensics,
		domain.CategoryPwn,
		domain.CategoryReverse,
	} {
		clock := draftmocks.NewMockClock(t)
		clock.EXPECT().Now().Return(state.TurnDeadline.Add(-time.Second)).Once()
		result, applyErr := draftusecase.NewActionUseCase(repository, clock).Apply(t.Context(), draftusecase.PlayerActionCommand{
			DraftID: state.ID, ExpectedRevisionID: state.RevisionID, ExpectedRevision: state.Revision,
			ExpectedServiceEpoch: state.ServiceEpoch, ExpectedTurn: state.Turn,
			CommandID: uuid.New(), ResultRevisionID: uuid.New(), ActionID: uuid.New(),
			ActorID: *state.CurrentActorID, Action: *state.CurrentAction, Category: category,
		})
		require.NoError(t, applyErr)
		state = result.Draft
	}
	require.Equal(t, draftusecase.ExecutionStateCompleted, state.State)
	return state
}

func terminalRehydrateBracketFixture() playoff.SemifinalAdvancementAuthority {
	tournamentID := uuid.New()
	semifinals := make([]playoff.SemifinalMatch, 2)
	for index := range semifinals {
		semifinals[index] = playoff.SemifinalMatch{
			Position: index + 1,
			Series: domain.Series{
				ID: uuid.New(), TournamentID: tournamentID,
				FirstParticipantID: uuid.New(), SecondParticipantID: uuid.New(),
				Format: domain.SeriesFormatBO1, State: domain.SeriesStateLocked,
			},
			WinnerPath: playoff.SemifinalWinnerToFinal, LoserPath: playoff.SemifinalLoserEliminated,
		}
	}
	return playoff.SemifinalAdvancementAuthority{
		TournamentID: tournamentID, BracketRevisionID: domain.DerivedRevisionID(uuid.New()), Semifinals: semifinals,
	}
}
