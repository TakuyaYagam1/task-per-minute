package forfeit_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	forfeitusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/forfeit"
)

func TestSurrenderAndOperatorForfeit(t *testing.T) {
	t.Parallel()

	t.Run("connected participant surrenders an active BO3", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 21, 0, 0, 0, time.UTC)
		authority := forfeitAuthorityFixture(
			domain.SeriesFormatBO3,
			domain.SeriesStateActive,
			domain.GameStateActive,
		)
		authority.Series.Series.Score = domain.SeriesScore{FirstParticipantWins: 1, SecondParticipantWins: 1}
		loserID := authority.Series.Series.SecondParticipantID
		authority.ConnectedParticipantIDs = []uuid.UUID{loserID}
		command := surrenderCommandFixture(authority, loserID)
		repository := newForfeitRepositoryHarness(t, authority, 0)

		resolved, changed, err := forfeitusecase.ForfeitNewUseCase(repository.repository, forfeitNewGameClock(t, now)).Surrender(
			t.Context(),
			command,
		)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, resolved.Validate())
		require.Equal(t, forfeitusecase.SourceSurrender, resolved.Source)
		require.Equal(t, domain.GameResultReasonSurrender, resolved.Reason)
		require.Equal(t, domain.GameStateCompleted, resolved.Game.State)
		require.Equal(t, authority.Series.Series.FirstParticipantID, *resolved.Game.WinnerID)
		require.Equal(t, domain.SeriesStateCompleted, resolved.Series.Series.State)
		require.Equal(t, domain.SeriesScore{FirstParticipantWins: 2, SecondParticipantWins: 1}, resolved.Series.Series.Score)
		require.Equal(t, authority.Series.Series.FirstParticipantID, *resolved.Series.Series.WinnerID)
		require.Nil(t, resolved.OperatorEvidence)
		require.Equal(t, 1, repository.writeCount())

		retried, changed, err := forfeitusecase.ForfeitNewUseCase(repository.repository, forfeitNewGameClock(t, now)).Surrender(
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
		authority := forfeitAuthorityFixture(
			domain.SeriesFormatBO1,
			domain.SeriesStateTechnicalPause,
			domain.GameStatePaused,
		)
		resumeState := domain.SeriesStateActive
		authority.Series.ResumeState = &resumeState
		loserID := authority.Series.Series.FirstParticipantID
		authority.ConnectedParticipantIDs = []uuid.UUID{loserID}
		repository := newForfeitRepositoryHarness(t, authority, 0)

		resolved, changed, err := forfeitusecase.ForfeitNewUseCase(repository.repository, forfeitNewGameClock(t, now)).Surrender(
			t.Context(),
			surrenderCommandFixture(authority, loserID),
		)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, authority.Series.Series.SecondParticipantID, *resolved.Series.Series.WinnerID)
		require.Nil(t, resolved.Series.ResumeState)
	})

	t.Run("first terminal Series revision starts at one after prior Game and score revisions", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 21, 7, 0, 0, time.UTC)
		authority := forfeitAuthorityFixture(
			domain.SeriesFormatBO3,
			domain.SeriesStateActive,
			domain.GameStateActive,
		)
		authority.CurrentOrdinal = 9
		authority.CurrentSeriesResultOrdinal = 0
		loserID := authority.Series.Series.SecondParticipantID
		authority.ConnectedParticipantIDs = []uuid.UUID{loserID}

		resolved, changed, err := forfeitusecase.ForfeitNewUseCase(
			newForfeitRepositoryHarness(t, authority, 0).repository,
			forfeitNewGameClock(t, now),
		).Surrender(t.Context(), surrenderCommandFixture(authority, loserID))

		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, 11, resolved.ScoreRevision.Ordinal)
		require.Equal(t, 1, resolved.SeriesRevision.Ordinal)
		require.Nil(t, resolved.SeriesRevision.PreviousRevisionID)
		require.NoError(t, resolved.Validate())
	})

	t.Run("surrender rejects foreign, disconnected, and pre-start actors", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 21, 10, 0, 0, time.UTC)
		authority := forfeitAuthorityFixture(
			domain.SeriesFormatBO1,
			domain.SeriesStateActive,
			domain.GameStateActive,
		)
		loserID := authority.Series.Series.FirstParticipantID
		authority.ConnectedParticipantIDs = []uuid.UUID{loserID}
		command := surrenderCommandFixture(authority, loserID)

		foreign := command
		foreign.ActorParticipantID = forfeitID(800)
		resolved, changed, err := forfeitusecase.ForfeitNewUseCase(
			newForfeitRepositoryHarness(t, authority, 0).repository,
			forfeitNewGameClock(t, now),
		).Surrender(t.Context(), foreign)
		require.Nil(t, resolved)
		require.False(t, changed)
		require.ErrorIs(t, err, domain.ErrAssignmentParticipant)

		disconnected := authority
		disconnected.ConnectedParticipantIDs = nil
		resolved, changed, err = forfeitusecase.ForfeitNewUseCase(
			newForfeitRepositoryHarness(t, disconnected, 0).repository,
			forfeitNewGameClock(t, now),
		).Surrender(t.Context(), command)
		require.Nil(t, resolved)
		require.False(t, changed)
		require.ErrorIs(t, err, forfeitusecase.ErrSurrenderDisconnected)

		preStart := forfeitAuthorityFixture(
			domain.SeriesFormatBO1,
			domain.SeriesStateReady,
			domain.GameStateReady,
		)
		preStart.ConnectedParticipantIDs = []uuid.UUID{preStart.Series.Series.FirstParticipantID}
		resolved, changed, err = forfeitusecase.ForfeitNewUseCase(
			newForfeitRepositoryHarness(t, preStart, 0).repository,
			forfeitNewGameClock(t, now),
		).Surrender(t.Context(), surrenderCommandFixture(preStart, preStart.Series.Series.FirstParticipantID))
		require.Nil(t, resolved)
		require.False(t, changed)
		require.ErrorIs(t, err, forfeitusecase.ErrForfeitUnavailable)
	})

	t.Run("authorized operator records a confirmed pre-start rule forfeit", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 21, 15, 0, 0, time.UTC)
		authority := forfeitAuthorityFixture(
			domain.SeriesFormatBO1,
			domain.SeriesStateReady,
			domain.GameStateReady,
		)
		operatorID := forfeitID(70)
		authority.AuthorizedOperatorIDs = []uuid.UUID{operatorID}
		loserID := authority.Series.Series.SecondParticipantID
		command := operatorCommandFixture(authority, operatorID, loserID, false)
		repository := newForfeitRepositoryHarness(t, authority, 0)

		resolved, changed, err := forfeitusecase.ForfeitNewUseCase(repository.repository, forfeitNewGameClock(t, now)).OperatorForfeit(
			t.Context(),
			command,
		)
		require.NoError(t, err)
		require.True(t, changed)
		require.NoError(t, resolved.Validate())
		require.Equal(t, forfeitusecase.SourceOperator, resolved.Source)
		require.Equal(t, domain.GameResultReasonOperatorForfeit, resolved.Reason)
		require.Nil(t, resolved.Game)
		require.Nil(t, resolved.GameRevision)
		require.Equal(t, domain.GameStateReady, resolved.Series.Series.Slots[0].Attempts[0].State)
		require.Equal(t, authority.Series.Series.FirstParticipantID, *resolved.Series.Series.WinnerID)
		require.Equal(t, command.Evidence, *resolved.OperatorEvidence)
	})

	t.Run("authorized operator forfeits an active Game", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 21, 20, 0, 0, time.UTC)
		authority := forfeitAuthorityFixture(
			domain.SeriesFormatBO3,
			domain.SeriesStateActive,
			domain.GameStateActive,
		)
		operatorID := forfeitID(71)
		authority.AuthorizedOperatorIDs = []uuid.UUID{operatorID}
		command := operatorCommandFixture(authority, operatorID, authority.Series.Series.FirstParticipantID, true)
		repository := newForfeitRepositoryHarness(t, authority, 0)

		resolved, changed, err := forfeitusecase.ForfeitNewUseCase(repository.repository, forfeitNewGameClock(t, now)).OperatorForfeit(
			t.Context(),
			command,
		)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, domain.GameStateCompleted, resolved.Game.State)
		require.Equal(t, domain.GameResultReasonOperatorForfeit, resolved.Game.ResultReason)
		require.Equal(t, authority.Series.Series.SecondParticipantID, *resolved.Game.WinnerID)
		require.Equal(t, authority.Series.Series.SecondParticipantID, *resolved.Series.Series.WinnerID)
	})

	t.Run("operator forfeit requires authorization and complete non-technical evidence", func(t *testing.T) {
		t.Parallel()

		now := time.Date(2026, 8, 30, 21, 25, 0, 0, time.UTC)
		authority := forfeitAuthorityFixture(
			domain.SeriesFormatBO1,
			domain.SeriesStateReady,
			domain.GameStateReady,
		)
		operatorID := forfeitID(72)
		authority.AuthorizedOperatorIDs = []uuid.UUID{operatorID}
		base := operatorCommandFixture(authority, operatorID, authority.Series.Series.SecondParticipantID, false)

		tests := []struct {
			name    string
			mutate  func(*forfeitusecase.OperatorCommand)
			wantErr error
		}{
			{
				name: "unauthorized operator",
				mutate: func(command *forfeitusecase.OperatorCommand) {
					command.ActorOperatorID = forfeitID(999)
				},
				wantErr: forfeitusecase.ErrOperatorForfeitUnauthorized,
			},
			{
				name: "missing confirmation",
				mutate: func(command *forfeitusecase.OperatorCommand) {
					command.Evidence.Confirmed = false
				},
				wantErr: forfeitusecase.ErrInvalidForfeit,
			},
			{
				name: "missing reason",
				mutate: func(command *forfeitusecase.OperatorCommand) {
					command.Evidence.Reason = ""
				},
				wantErr: forfeitusecase.ErrInvalidForfeit,
			},
			{
				name: "missing rule",
				mutate: func(command *forfeitusecase.OperatorCommand) {
					command.Evidence.RuleID = ""
				},
				wantErr: forfeitusecase.ErrInvalidForfeit,
			},
			{
				name: "missing evidence",
				mutate: func(command *forfeitusecase.OperatorCommand) {
					command.Evidence.EvidenceIDs = nil
				},
				wantErr: forfeitusecase.ErrInvalidForfeit,
			},
			{
				name: "technical failure basis",
				mutate: func(command *forfeitusecase.OperatorCommand) {
					command.Evidence.Basis = forfeitusecase.OperatorBasis("technical_failure")
				},
				wantErr: forfeitusecase.ErrInvalidForfeit,
			},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()

				command := base
				command.Evidence.EvidenceIDs = append([]uuid.UUID(nil), base.Evidence.EvidenceIDs...)
				test.mutate(&command)
				resolved, changed, err := forfeitusecase.ForfeitNewUseCase(
					newForfeitRepositoryHarness(t, authority, 0).repository,
					forfeitNewGameClock(t, now),
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
		authority := forfeitAuthorityFixture(
			domain.SeriesFormatBO1,
			domain.SeriesStateActive,
			domain.GameStateActive,
		)
		loserID := authority.Series.Series.SecondParticipantID
		authority.ConnectedParticipantIDs = []uuid.UUID{loserID}
		command := surrenderCommandFixture(authority, loserID)
		repository := newForfeitRepositoryHarness(t, authority, 2)
		clock := forfeitNewGameClock(t, now)
		results := make(chan forfeitResult, 2)
		var group sync.WaitGroup

		for range 2 {
			group.Add(1)
			go func() {
				defer group.Done()
				resolution, changed, err := forfeitusecase.ForfeitNewUseCase(
					repository.repository,
					clock,
				).Surrender(context.Background(), command)
				results <- forfeitResult{resolution: resolution, changed: changed, err: err}
			}()
		}
		group.Wait()
		close(results)

		var resolutions []*forfeitusecase.ForfeitResolution
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

func forfeitAuthorityFixture(
	format domain.SeriesFormat,
	seriesState domain.SeriesState,
	gameState domain.GameState,
) forfeitusecase.ForfeitAuthority {
	series := seriesExecutionFixture(format, seriesState, gameState)
	return forfeitusecase.ForfeitAuthority{
		Scope: forfeitusecase.Scope{
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

func surrenderCommandFixture(
	authority forfeitusecase.ForfeitAuthority,
	loserID uuid.UUID,
) forfeitusecase.SurrenderCommand {
	currentGame := currentGame(authority.Series.Series)
	gameResultRevisionID := domain.OfficialResultRevisionID(forfeitID(81))
	return forfeitusecase.SurrenderCommand{
		Scope:                   authority.Scope,
		CommandID:               forfeitID(80),
		ActorParticipantID:      loserID,
		ForfeitingParticipantID: loserID,
		ExpectedGame: forfeitusecase.GameExpectation{
			SlotID: currentGame.SlotID, GameID: currentGame.ID,
			AttemptNo: currentGame.AttemptNo, State: currentGame.State,
		},
		Revisions: forfeitusecase.ForfeitRevisionSet{
			GameResultRevisionID:   &gameResultRevisionID,
			ScoreRevisionID:        domain.SeriesScoreRevisionID(forfeitID(82)),
			SeriesResultRevisionID: domain.OfficialResultRevisionID(forfeitID(83)),
			AuditEventID:           forfeitID(84),
			OutboxEventID:          forfeitID(85),
			ProjectionRevisionID:   forfeitID(86),
		},
	}
}

func operatorCommandFixture(
	authority forfeitusecase.ForfeitAuthority,
	operatorID uuid.UUID,
	loserID uuid.UUID,
	withGameResult bool,
) forfeitusecase.OperatorCommand {
	currentGame := currentGame(authority.Series.Series)
	command := forfeitusecase.OperatorCommand{
		Scope:                   authority.Scope,
		CommandID:               forfeitID(90),
		ActorOperatorID:         operatorID,
		ForfeitingParticipantID: loserID,
		ExpectedGame: &forfeitusecase.GameExpectation{
			SlotID: currentGame.SlotID, GameID: currentGame.ID,
			AttemptNo: currentGame.AttemptNo, State: currentGame.State,
		},
		Evidence: forfeitusecase.OperatorEvidence{
			Confirmed: true,
			Basis:     forfeitusecase.OperatorBasisRuleViolation,
			Reason:    "participant used prohibited tooling",
			RuleID:    "game.rule.7",
			EvidenceIDs: []uuid.UUID{
				forfeitID(91),
			},
		},
		Revisions: forfeitusecase.ForfeitRevisionSet{
			ScoreRevisionID:        domain.SeriesScoreRevisionID(forfeitID(93)),
			SeriesResultRevisionID: domain.OfficialResultRevisionID(forfeitID(94)),
			AuditEventID:           forfeitID(95),
			OutboxEventID:          forfeitID(96),
			ProjectionRevisionID:   forfeitID(97),
		},
	}
	if withGameResult {
		gameResultRevisionID := domain.OfficialResultRevisionID(forfeitID(92))
		command.Revisions.GameResultRevisionID = &gameResultRevisionID
	}
	return command
}

func currentGame(series domain.Series) domain.Game {
	slot := series.Slots[len(series.Slots)-1]
	return slot.Attempts[len(slot.Attempts)-1]
}

func cloneForfeitAuthority(authority forfeitusecase.ForfeitAuthority) forfeitusecase.ForfeitAuthority {
	clone := authority
	clone.Series = seriesdomain.CloneExecution(authority.Series)
	clone.ConnectedParticipantIDs = append([]uuid.UUID(nil), authority.ConnectedParticipantIDs...)
	clone.AuthorizedOperatorIDs = append([]uuid.UUID(nil), authority.AuthorizedOperatorIDs...)
	clone.CurrentGameResultRevisionIDs = append(
		[]domain.OfficialResultRevisionID(nil),
		authority.CurrentGameResultRevisionIDs...,
	)
	if authority.Current != nil {
		current := cloneForfeitResolution(*authority.Current)
		clone.Current = &current
	}
	return clone
}

func cloneForfeitResolution(resolution forfeitusecase.ForfeitResolution) forfeitusecase.ForfeitResolution {
	clone := resolution
	if resolution.ExpectedGame != nil {
		expectedGame := *resolution.ExpectedGame
		clone.ExpectedGame = &expectedGame
	}
	clone.Series = seriesdomain.CloneExecution(resolution.Series)
	if resolution.Game != nil {
		game := *resolution.Game
		game.WinnerID = forfeitCloneUUIDPointer(game.WinnerID)
		game.ResultRevisionID = cloneResultRevisionPointer(game.ResultRevisionID)
		clone.Game = &game
	}
	if resolution.GameRevision != nil {
		gameRevision := *resolution.GameRevision
		clone.GameRevision = &gameRevision
	}
	clone.ScoreRevision.PreviousRevisionID = cloneScoreRevisionPointer(
		resolution.ScoreRevision.PreviousRevisionID,
	)
	clone.ScoreRevision.GameResultRevisionIDs = append(
		[]domain.OfficialResultRevisionID(nil),
		resolution.ScoreRevision.GameResultRevisionIDs...,
	)
	clone.SeriesRevision.PreviousRevisionID = cloneResultRevisionPointer(
		resolution.SeriesRevision.PreviousRevisionID,
	)
	clone.SeriesRevision.WinnerID = forfeitCloneUUIDPointer(resolution.SeriesRevision.WinnerID)
	if resolution.OperatorEvidence != nil {
		evidence := *resolution.OperatorEvidence
		evidence.EvidenceIDs = append([]uuid.UUID(nil), resolution.OperatorEvidence.EvidenceIDs...)
		clone.OperatorEvidence = &evidence
	}
	return clone
}
