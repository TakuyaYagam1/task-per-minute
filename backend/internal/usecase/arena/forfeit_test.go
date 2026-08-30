package arena_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestSurrenderAndOperatorForfeit(t *testing.T) {
	t.Parallel()

	t.Run("connected participant surrenders an active BO3", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 21, 0, 0, 0, time.UTC)
		authority := task039ForfeitAuthority(
			domain.ArenaSeriesFormatBO3,
			domain.ArenaSeriesStateActive,
			domain.ArenaGameStateActive,
		)
		authority.Series.Series.Score = domain.ArenaSeriesScore{FirstParticipantWins: 1, SecondParticipantWins: 1}
		loserID := authority.Series.Series.SecondParticipantID
		authority.ConnectedParticipantIDs = []uuid.UUID{loserID}
		command := task039SurrenderCommand(authority, loserID)
		repository := newTask039ForfeitRepository(authority, 0)

		resolved, changed, err := arena.NewForfeitUseCase(repository, fixedArenaClock{now: now}).Surrender(
			t.Context(),
			command,
		)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, resolved.Validate())
		require.Equal(t, arena.ForfeitSourceSurrender, resolved.Source)
		require.Equal(t, domain.ArenaGameResultReasonSurrender, resolved.Reason)
		require.Equal(t, domain.ArenaGameStateCompleted, resolved.Game.State)
		require.Equal(t, authority.Series.Series.FirstParticipantID, *resolved.Game.WinnerID)
		require.Equal(t, domain.ArenaSeriesStateCompleted, resolved.Series.Series.State)
		require.Equal(t, domain.ArenaSeriesScore{FirstParticipantWins: 2, SecondParticipantWins: 1}, resolved.Series.Series.Score)
		require.Equal(t, authority.Series.Series.FirstParticipantID, *resolved.Series.Series.WinnerID)
		require.Nil(t, resolved.OperatorEvidence)
		require.Equal(t, 1, repository.writeCount())

		retried, changed, err := arena.NewForfeitUseCase(repository, fixedArenaClock{now: now}).Surrender(
			t.Context(),
			command,
		)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, resolved, retried)
		require.Equal(t, 1, repository.writeCount())
	})

	t.Run("connected participant surrenders a paused Game", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 21, 5, 0, 0, time.UTC)
		authority := task039ForfeitAuthority(
			domain.ArenaSeriesFormatBO1,
			domain.ArenaSeriesStateTechnicalPause,
			domain.ArenaGameStatePaused,
		)
		resumeState := domain.ArenaSeriesStateActive
		authority.Series.ResumeState = &resumeState
		loserID := authority.Series.Series.FirstParticipantID
		authority.ConnectedParticipantIDs = []uuid.UUID{loserID}
		repository := newTask039ForfeitRepository(authority, 0)

		resolved, changed, err := arena.NewForfeitUseCase(repository, fixedArenaClock{now: now}).Surrender(
			t.Context(),
			task039SurrenderCommand(authority, loserID),
		)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, authority.Series.Series.SecondParticipantID, *resolved.Series.Series.WinnerID)
		require.Nil(t, resolved.Series.ResumeState)
	})

	t.Run("surrender rejects foreign, disconnected, and pre-start actors", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 21, 10, 0, 0, time.UTC)
		authority := task039ForfeitAuthority(
			domain.ArenaSeriesFormatBO1,
			domain.ArenaSeriesStateActive,
			domain.ArenaGameStateActive,
		)
		loserID := authority.Series.Series.FirstParticipantID
		authority.ConnectedParticipantIDs = []uuid.UUID{loserID}
		command := task039SurrenderCommand(authority, loserID)

		foreign := command
		foreign.ActorParticipantID = task039ID(800)
		resolved, changed, err := arena.NewForfeitUseCase(
			newTask039ForfeitRepository(authority, 0),
			fixedArenaClock{now: now},
		).Surrender(t.Context(), foreign)
		require.Nil(t, resolved)
		require.False(t, changed)
		require.ErrorIs(t, err, domain.ErrArenaAssignmentParticipant)

		disconnected := authority
		disconnected.ConnectedParticipantIDs = nil
		resolved, changed, err = arena.NewForfeitUseCase(
			newTask039ForfeitRepository(disconnected, 0),
			fixedArenaClock{now: now},
		).Surrender(t.Context(), command)
		require.Nil(t, resolved)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrSurrenderDisconnected)

		preStart := task039ForfeitAuthority(
			domain.ArenaSeriesFormatBO1,
			domain.ArenaSeriesStateReady,
			domain.ArenaGameStateReady,
		)
		preStart.ConnectedParticipantIDs = []uuid.UUID{preStart.Series.Series.FirstParticipantID}
		resolved, changed, err = arena.NewForfeitUseCase(
			newTask039ForfeitRepository(preStart, 0),
			fixedArenaClock{now: now},
		).Surrender(t.Context(), task039SurrenderCommand(preStart, preStart.Series.Series.FirstParticipantID))
		require.Nil(t, resolved)
		require.False(t, changed)
		require.ErrorIs(t, err, arena.ErrForfeitUnavailable)
	})

	t.Run("authorized operator records a confirmed pre-start rule forfeit", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 21, 15, 0, 0, time.UTC)
		authority := task039ForfeitAuthority(
			domain.ArenaSeriesFormatBO1,
			domain.ArenaSeriesStateReady,
			domain.ArenaGameStateReady,
		)
		operatorID := task039ID(70)
		authority.AuthorizedOperatorIDs = []uuid.UUID{operatorID}
		loserID := authority.Series.Series.SecondParticipantID
		command := task039OperatorCommand(authority, operatorID, loserID, false)
		repository := newTask039ForfeitRepository(authority, 0)

		resolved, changed, err := arena.NewForfeitUseCase(repository, fixedArenaClock{now: now}).OperatorForfeit(
			t.Context(),
			command,
		)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, resolved.Validate())
		require.Equal(t, arena.ForfeitSourceOperator, resolved.Source)
		require.Equal(t, domain.ArenaGameResultReasonOperatorForfeit, resolved.Reason)
		require.Nil(t, resolved.Game)
		require.Nil(t, resolved.GameRevision)
		require.Equal(t, domain.ArenaGameStateReady, resolved.Series.Series.Slots[0].Attempts[0].State)
		require.Equal(t, authority.Series.Series.FirstParticipantID, *resolved.Series.Series.WinnerID)
		require.Equal(t, command.Evidence, *resolved.OperatorEvidence)
	})

	t.Run("authorized operator forfeits an active Game", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 21, 20, 0, 0, time.UTC)
		authority := task039ForfeitAuthority(
			domain.ArenaSeriesFormatBO3,
			domain.ArenaSeriesStateActive,
			domain.ArenaGameStateActive,
		)
		operatorID := task039ID(71)
		authority.AuthorizedOperatorIDs = []uuid.UUID{operatorID}
		command := task039OperatorCommand(authority, operatorID, authority.Series.Series.FirstParticipantID, true)
		repository := newTask039ForfeitRepository(authority, 0)

		resolved, changed, err := arena.NewForfeitUseCase(repository, fixedArenaClock{now: now}).OperatorForfeit(
			t.Context(),
			command,
		)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, domain.ArenaGameStateCompleted, resolved.Game.State)
		require.Equal(t, domain.ArenaGameResultReasonOperatorForfeit, resolved.Game.ResultReason)
		require.Equal(t, authority.Series.Series.SecondParticipantID, *resolved.Game.WinnerID)
		require.Equal(t, authority.Series.Series.SecondParticipantID, *resolved.Series.Series.WinnerID)
	})

	t.Run("operator forfeit requires authorization and complete non-technical evidence", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 21, 25, 0, 0, time.UTC)
		authority := task039ForfeitAuthority(
			domain.ArenaSeriesFormatBO1,
			domain.ArenaSeriesStateReady,
			domain.ArenaGameStateReady,
		)
		operatorID := task039ID(72)
		authority.AuthorizedOperatorIDs = []uuid.UUID{operatorID}
		base := task039OperatorCommand(authority, operatorID, authority.Series.Series.SecondParticipantID, false)

		tests := []struct {
			name    string
			mutate  func(*arena.OperatorForfeitCommand)
			wantErr error
		}{
			{
				name: "unauthorized operator",
				mutate: func(command *arena.OperatorForfeitCommand) {
					command.ActorOperatorID = task039ID(999)
				},
				wantErr: arena.ErrOperatorForfeitUnauthorized,
			},
			{
				name: "missing confirmation",
				mutate: func(command *arena.OperatorForfeitCommand) {
					command.Evidence.Confirmed = false
				},
				wantErr: arena.ErrInvalidForfeit,
			},
			{
				name: "missing reason",
				mutate: func(command *arena.OperatorForfeitCommand) {
					command.Evidence.Reason = ""
				},
				wantErr: arena.ErrInvalidForfeit,
			},
			{
				name: "missing rule",
				mutate: func(command *arena.OperatorForfeitCommand) {
					command.Evidence.RuleID = ""
				},
				wantErr: arena.ErrInvalidForfeit,
			},
			{
				name: "missing evidence",
				mutate: func(command *arena.OperatorForfeitCommand) {
					command.Evidence.EvidenceIDs = nil
				},
				wantErr: arena.ErrInvalidForfeit,
			},
			{
				name: "technical failure basis",
				mutate: func(command *arena.OperatorForfeitCommand) {
					command.Evidence.Basis = arena.OperatorForfeitBasis("technical_failure")
				},
				wantErr: arena.ErrInvalidForfeit,
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()

				command := base
				command.Evidence.EvidenceIDs = append([]uuid.UUID(nil), base.Evidence.EvidenceIDs...)
				test.mutate(&command)
				resolved, changed, err := arena.NewForfeitUseCase(
					newTask039ForfeitRepository(authority, 0),
					fixedArenaClock{now: now},
				).OperatorForfeit(t.Context(), command)
				require.Nil(t, resolved)
				require.False(t, changed)
				require.ErrorIs(t, err, test.wantErr)
			})
		}
	})

	t.Run("concurrent duplicate surrender commits once", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 21, 30, 0, 0, time.UTC)
		authority := task039ForfeitAuthority(
			domain.ArenaSeriesFormatBO1,
			domain.ArenaSeriesStateActive,
			domain.ArenaGameStateActive,
		)
		loserID := authority.Series.Series.SecondParticipantID
		authority.ConnectedParticipantIDs = []uuid.UUID{loserID}
		command := task039SurrenderCommand(authority, loserID)
		repository := newTask039ForfeitRepository(authority, 2)
		results := make(chan task039ForfeitResult, 2)
		var group sync.WaitGroup

		for range 2 {
			group.Add(1)
			go func() {
				defer group.Done()
				resolution, changed, err := arena.NewForfeitUseCase(
					repository,
					fixedArenaClock{now: now},
				).Surrender(context.Background(), command)
				results <- task039ForfeitResult{resolution: resolution, changed: changed, err: err}
			}()
		}
		group.Wait()
		close(results)

		var resolutions []*arena.ForfeitResolution
		changedCount := 0
		for result := range results {
			require.NoError(t, result.err)
			require.NotNil(t, result.resolution)
			resolutions = append(resolutions, result.resolution)
			if result.changed {
				changedCount++
			}
		}
		require.Equal(t, 1, changedCount)
		require.Equal(t, 1, repository.writeCount())
		require.Equal(t, resolutions[0], resolutions[1])
	})
}

type task039ForfeitResult struct {
	resolution *arena.ForfeitResolution
	changed    bool
	err        error
}

type task039ForfeitRepository struct {
	mu        sync.Mutex
	authority arena.ForfeitAuthority
	barrier   *sync.WaitGroup
	writes    int
}

func newTask039ForfeitRepository(
	authority arena.ForfeitAuthority,
	contenders int,
) *task039ForfeitRepository {
	repository := &task039ForfeitRepository{authority: task039CloneForfeitAuthority(authority)}
	if contenders > 0 {
		repository.barrier = &sync.WaitGroup{}
		repository.barrier.Add(contenders)
	}
	return repository
}

func (r *task039ForfeitRepository) LoadForfeitAuthority(
	_ context.Context,
	_ arena.ForfeitScope,
) (arena.ForfeitAuthority, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return task039CloneForfeitAuthority(r.authority), nil
}

func (r *task039ForfeitRepository) CommitForfeitResolution(
	_ context.Context,
	resolution arena.ForfeitResolution,
) (*arena.ForfeitResolution, bool, error) {
	if r.barrier != nil {
		r.barrier.Done()
		r.barrier.Wait()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if resolution.ExpectedAuthorityRevision != r.authority.Revision || r.authority.Current != nil {
		return nil, false, domain.ErrConflict
	}
	stored := task039CloneForfeitResolution(resolution)
	r.authority.Revision++
	r.authority.Series = task039CloneSeriesExecution(resolution.Series)
	r.authority.Current = &stored
	r.writes++
	result := task039CloneForfeitResolution(stored)
	return &result, true, nil
}

func (r *task039ForfeitRepository) writeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.writes
}

func task039ForfeitAuthority(
	format domain.ArenaSeriesFormat,
	seriesState domain.ArenaSeriesState,
	gameState domain.ArenaGameState,
) arena.ForfeitAuthority {
	series := task039SeriesExecution(format, seriesState, gameState)
	return arena.ForfeitAuthority{
		Scope: arena.ForfeitScope{
			TournamentID: series.Series.TournamentID,
			SeriesID:     series.Series.ID,
		},
		Revision:                     5,
		Series:                       series,
		CurrentOrdinal:               3,
		CurrentProjectionRevision:    7,
		CurrentGameResultRevisionIDs: nil,
	}
}

func task039SurrenderCommand(
	authority arena.ForfeitAuthority,
	loserID uuid.UUID,
) arena.SurrenderCommand {
	game := task039CurrentGame(authority.Series.Series)
	gameResultRevisionID := domain.ArenaOfficialResultRevisionID(task039ID(81))
	return arena.SurrenderCommand{
		Scope:                   authority.Scope,
		CommandID:               task039ID(80),
		ActorParticipantID:      loserID,
		ForfeitingParticipantID: loserID,
		ExpectedGame: arena.ForfeitGameExpectation{
			SlotID: game.SlotID, GameID: game.ID, AttemptNo: game.AttemptNo, State: game.State,
		},
		Revisions: arena.ForfeitRevisionSet{
			GameResultRevisionID:   &gameResultRevisionID,
			ScoreRevisionID:        domain.ArenaSeriesScoreRevisionID(task039ID(82)),
			SeriesResultRevisionID: domain.ArenaOfficialResultRevisionID(task039ID(83)),
			AuditEventID:           task039ID(84),
			OutboxEventID:          task039ID(85),
			ProjectionRevisionID:   task039ID(86),
		},
	}
}

func task039OperatorCommand(
	authority arena.ForfeitAuthority,
	operatorID uuid.UUID,
	loserID uuid.UUID,
	withGameResult bool,
) arena.OperatorForfeitCommand {
	game := task039CurrentGame(authority.Series.Series)
	command := arena.OperatorForfeitCommand{
		Scope:                   authority.Scope,
		CommandID:               task039ID(90),
		ActorOperatorID:         operatorID,
		ForfeitingParticipantID: loserID,
		ExpectedGame: &arena.ForfeitGameExpectation{
			SlotID: game.SlotID, GameID: game.ID, AttemptNo: game.AttemptNo, State: game.State,
		},
		Evidence: arena.OperatorForfeitEvidence{
			Confirmed: true,
			Basis:     arena.OperatorForfeitBasisRuleViolation,
			Reason:    "participant used prohibited tooling",
			RuleID:    "arena.rule.7",
			EvidenceIDs: []uuid.UUID{
				task039ID(91),
			},
		},
		Revisions: arena.ForfeitRevisionSet{
			ScoreRevisionID:        domain.ArenaSeriesScoreRevisionID(task039ID(93)),
			SeriesResultRevisionID: domain.ArenaOfficialResultRevisionID(task039ID(94)),
			AuditEventID:           task039ID(95),
			OutboxEventID:          task039ID(96),
			ProjectionRevisionID:   task039ID(97),
		},
	}
	if withGameResult {
		gameResultRevisionID := domain.ArenaOfficialResultRevisionID(task039ID(92))
		command.Revisions.GameResultRevisionID = &gameResultRevisionID
	}
	return command
}

func task039SeriesExecution(
	format domain.ArenaSeriesFormat,
	seriesState domain.ArenaSeriesState,
	gameState domain.ArenaGameState,
) arena.SeriesExecution {
	seriesID := task039ID(1)
	firstParticipantID := task039ID(2)
	secondParticipantID := task039ID(3)
	scoreRevisionID := domain.ArenaSeriesScoreRevisionID(task039ID(4))
	series := domain.ArenaSeries{
		ID: seriesID, TournamentID: task039ID(5),
		FirstParticipantID: firstParticipantID, SecondParticipantID: secondParticipantID,
		Format: format, State: seriesState, CurrentScoreRevisionID: &scoreRevisionID,
		Slots: []domain.ArenaGameSlot{{
			ID: task039ID(6), SeriesID: seriesID, Position: 1,
			Category: domain.CategoryWeb, ScoreBefore: domain.ArenaSeriesScore{},
			Attempts: []domain.ArenaGame{{
				ID: task039ID(7), SlotID: task039ID(6), AttemptNo: 1, State: gameState,
			}},
		}},
	}
	if gameState == domain.ArenaGameStateVoid {
		resultRevisionID := domain.ArenaOfficialResultRevisionID(task039ID(8))
		series.Slots[0].Attempts[0].ResultReason = domain.ArenaGameResultReasonNoSolve
		series.Slots[0].Attempts[0].ResultRevisionID = &resultRevisionID
	}
	return arena.SeriesExecution{Series: series}
}

func task039CurrentGame(series domain.ArenaSeries) domain.ArenaGame {
	slot := series.Slots[len(series.Slots)-1]
	return slot.Attempts[len(slot.Attempts)-1]
}

func task039CompleteCurrentGame(
	execution arena.SeriesExecution,
	winnerID uuid.UUID,
	reason domain.ArenaGameResultReason,
	resultRevisionID domain.ArenaOfficialResultRevisionID,
) arena.SeriesExecution {
	clone := task039CloneSeriesExecution(execution)
	slotIndex := len(clone.Series.Slots) - 1
	gameIndex := len(clone.Series.Slots[slotIndex].Attempts) - 1
	game := &clone.Series.Slots[slotIndex].Attempts[gameIndex]
	game.State = domain.ArenaGameStateCompleted
	game.ResultReason = reason
	game.WinnerID = &winnerID
	game.ResultRevisionID = &resultRevisionID
	return clone
}

func task039CloneForfeitAuthority(authority arena.ForfeitAuthority) arena.ForfeitAuthority {
	clone := authority
	clone.Series = task039CloneSeriesExecution(authority.Series)
	clone.ConnectedParticipantIDs = append([]uuid.UUID(nil), authority.ConnectedParticipantIDs...)
	clone.AuthorizedOperatorIDs = append([]uuid.UUID(nil), authority.AuthorizedOperatorIDs...)
	clone.CurrentGameResultRevisionIDs = append(
		[]domain.ArenaOfficialResultRevisionID(nil),
		authority.CurrentGameResultRevisionIDs...,
	)
	if authority.Current != nil {
		current := task039CloneForfeitResolution(*authority.Current)
		clone.Current = &current
	}
	return clone
}

func task039CloneForfeitResolution(resolution arena.ForfeitResolution) arena.ForfeitResolution {
	clone := resolution
	if resolution.ExpectedGame != nil {
		expectedGame := *resolution.ExpectedGame
		clone.ExpectedGame = &expectedGame
	}
	clone.Series = task039CloneSeriesExecution(resolution.Series)
	if resolution.Game != nil {
		game := *resolution.Game
		game.WinnerID = task039CloneUUIDPointer(game.WinnerID)
		game.ResultRevisionID = task039CloneResultRevisionPointer(game.ResultRevisionID)
		clone.Game = &game
	}
	if resolution.GameRevision != nil {
		gameRevision := *resolution.GameRevision
		clone.GameRevision = &gameRevision
	}
	clone.ScoreRevision.PreviousRevisionID = task039CloneScoreRevisionPointer(
		resolution.ScoreRevision.PreviousRevisionID,
	)
	clone.ScoreRevision.GameResultRevisionIDs = append(
		[]domain.ArenaOfficialResultRevisionID(nil),
		resolution.ScoreRevision.GameResultRevisionIDs...,
	)
	clone.SeriesRevision.PreviousRevisionID = task039CloneResultRevisionPointer(
		resolution.SeriesRevision.PreviousRevisionID,
	)
	clone.SeriesRevision.WinnerID = task039CloneUUIDPointer(resolution.SeriesRevision.WinnerID)
	if resolution.OperatorEvidence != nil {
		evidence := *resolution.OperatorEvidence
		evidence.EvidenceIDs = append([]uuid.UUID(nil), resolution.OperatorEvidence.EvidenceIDs...)
		clone.OperatorEvidence = &evidence
	}
	return clone
}

func task039CloneSeriesExecution(execution arena.SeriesExecution) arena.SeriesExecution {
	clone := execution
	clone.ResumeState = nil
	if execution.ResumeState != nil {
		resume := *execution.ResumeState
		clone.ResumeState = &resume
	}
	clone.Series.WinnerID = task039CloneUUIDPointer(execution.Series.WinnerID)
	clone.Series.CurrentScoreRevisionID = task039CloneScoreRevisionPointer(execution.Series.CurrentScoreRevisionID)
	clone.Series.CurrentResultRevisionID = task039CloneResultRevisionPointer(execution.Series.CurrentResultRevisionID)
	clone.Series.Slots = make([]domain.ArenaGameSlot, len(execution.Series.Slots))
	for slotIndex, slot := range execution.Series.Slots {
		clone.Series.Slots[slotIndex] = slot
		clone.Series.Slots[slotIndex].Attempts = make([]domain.ArenaGame, len(slot.Attempts))
		for gameIndex, game := range slot.Attempts {
			clone.Series.Slots[slotIndex].Attempts[gameIndex] = game
			clone.Series.Slots[slotIndex].Attempts[gameIndex].WinnerID = task039CloneUUIDPointer(game.WinnerID)
			clone.Series.Slots[slotIndex].Attempts[gameIndex].ResultRevisionID = task039CloneResultRevisionPointer(
				game.ResultRevisionID,
			)
		}
	}
	return clone
}

func task039CloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func task039CloneScoreRevisionPointer(
	value *domain.ArenaSeriesScoreRevisionID,
) *domain.ArenaSeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func task039CloneResultRevisionPointer(
	value *domain.ArenaOfficialResultRevisionID,
) *domain.ArenaOfficialResultRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func task039ID(value int) uuid.UUID {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("task-039-%d", value)))
}
