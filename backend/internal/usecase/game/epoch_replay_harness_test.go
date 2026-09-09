package game_test

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/mocks"
)

type mutableEpochReplayTimeSource struct {
	mu   sync.Mutex
	now  time.Time
	mock *gamemocks.MockAuthorityTimeSource
}

func newMutableEpochReplayTimeSource(t *testing.T, now time.Time) *mutableEpochReplayTimeSource {
	t.Helper()
	state := &mutableEpochReplayTimeSource{now: now}
	timeSource := gamemocks.NewMockAuthorityTimeSource(t)
	timeSource.EXPECT().AuthorityTime(mock.Anything).RunAndReturn(state.current).Maybe()
	state.mock = timeSource
	return state
}

func (source *mutableEpochReplayTimeSource) current(context.Context) (time.Time, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.now, nil
}

type executionEpochReplayRepositoryOptions struct {
	transactionNow      time.Time
	advanceBeforeCommit time.Duration
}

type executionEpochReplayRepositoryState struct {
	mu                  sync.Mutex
	authority           gameusecase.EpochReplayAuthority
	hideCurrentReplay   bool
	writes              int
	commits             int
	transactionNow      time.Time
	advanceBeforeCommit time.Duration
}

type executionEpochReplayRepositoryHarness struct {
	*gamemocks.MockEpochReplayRepository

	state *executionEpochReplayRepositoryState
}

func newExecutionEpochReplayRepositoryHarness(
	t *testing.T,
	authority gameusecase.EpochReplayAuthority,
	options executionEpochReplayRepositoryOptions,
) *executionEpochReplayRepositoryHarness {
	t.Helper()
	state := &executionEpochReplayRepositoryState{
		authority:           authority,
		transactionNow:      options.transactionNow,
		advanceBeforeCommit: options.advanceBeforeCommit,
	}
	repository := gamemocks.NewMockEpochReplayRepository(t)
	repository.EXPECT().
		FindEpochReplay(mock.Anything, mock.Anything).
		RunAndReturn(state.findReplay).
		Maybe()
	repository.EXPECT().
		LoadEpochReplayAuthority(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(state.loadAuthority).
		Maybe()
	repository.EXPECT().
		CommitEpochReplay(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(state.commitReplay).
		Maybe()
	return &executionEpochReplayRepositoryHarness{
		MockEpochReplayRepository: repository,
		state:                     state,
	}
}

func (s *executionEpochReplayRepositoryState) findReplay(
	_ context.Context,
	_ domain.FailedAttemptScope,
) (*gameusecase.EpochReplayRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.hideCurrentReplay || s.authority.Current == nil {
		return nil, nil
	}
	clone := *s.authority.Current
	return &clone, nil
}

func (h *executionEpochReplayRepositoryHarness) hideCurrentReplay() {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	h.state.hideCurrentReplay = true
}

func (s *executionEpochReplayRepositoryState) loadAuthority(
	_ context.Context,
	_ domain.FailedAttemptScope,
	_ uuid.UUID,
) (gameusecase.EpochReplayAuthority, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.authority, nil
}

func (s *executionEpochReplayRepositoryState) commitReplay(
	_ context.Context,
	condition gameusecase.EpochReplayCommitCondition,
	record gameusecase.EpochReplayRecord,
) (*gameusecase.EpochReplayRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commits++
	transactionNow := s.transactionNow
	if transactionNow.IsZero() {
		transactionNow = record.Attempt.TerminalizedAt
	}
	if s.advanceBeforeCommit != 0 {
		transactionNow = transactionNow.Add(s.advanceBeforeCommit)
		s.transactionNow = transactionNow
		s.advanceBeforeCommit = 0
	}
	if condition.Validate() != nil || s.authority.Current != nil ||
		condition.CurrentAuthority != record.CurrentAuthority ||
		condition.ExpectedLeaseRevision != record.ExpectedLeaseRevision ||
		condition.BrokenAuthority != record.BrokenAuthority ||
		condition.ExpectedAttemptRevision != record.Attempt.ExpectedAuthorityRevision ||
		condition.ExpectedLeaseRevision != s.authority.Lease.Revision ||
		!s.authority.Lease.Proves(condition.CurrentAuthority, transactionNow) ||
		condition.BrokenAuthority != s.authority.BoundAuthority ||
		condition.ExpectedAttemptRevision != s.authority.Attempt.Revision ||
		s.authority.Attempt.Current != nil {
		return nil, false, domain.ErrConflict
	}
	stored := record
	s.authority.Attempt.Current = &stored.Attempt
	s.authority.Current = &stored
	s.writes++
	return &stored, true, nil
}

func (h *executionEpochReplayRepositoryHarness) writeCount() int {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	return h.state.writes
}

func (h *executionEpochReplayRepositoryHarness) commitCount() int {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	return h.state.commits
}

func (h *executionEpochReplayRepositoryHarness) currentState() (bool, bool) {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	return h.state.authority.Current != nil, h.state.authority.Attempt.Current != nil
}

func (h *executionEpochReplayRepositoryHarness) update(
	update func(*gameusecase.EpochReplayAuthority),
) {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	update(&h.state.authority)
}

func task042ReplayAuthority(
	t *testing.T,
	now time.Time,
) (gameusecase.EpochReplayAuthority, gameusecase.EpochReplayCommand) {
	t.Helper()
	attemptAuthority, attemptCommand := task042FailedAttemptFixture(t, now)
	attemptCommand.FailureClass = gamedomain.FailureExecutionEpochBreak
	lease := task042Lease(now, attemptCommand.Scope.TournamentID, 2)
	broken := authoritydomain.Stamp{LeaseID: task042ID(102), Epoch: 1}
	rosterID := task042ID(120)
	return gameusecase.EpochReplayAuthority{
			Lease: lease, BoundAuthority: broken, RosterID: rosterID, Attempt: attemptAuthority,
		}, gameusecase.EpochReplayCommand{
			CurrentAuthority: lease.Identity(), BrokenAuthority: broken, RosterID: rosterID, Attempt: attemptCommand,
		}
}

func task042FailedAttemptFixture(
	t *testing.T,
	now time.Time,
) (gameusecase.AttemptAuthority, gameusecase.AttemptCommand) {
	t.Helper()

	tournamentID := task042ID(201)
	waveID := task042ID(202)
	seriesID := task042ID(203)
	slotID := task042ID(204)
	gameID := task042ID(205)
	participants := [2]uuid.UUID{task042ID(206), task042ID(207)}
	currentScoreRevisionID := domain.SeriesScoreRevisionID(task042ID(208))
	series := seriesdomain.Execution{Series: domain.Series{
		ID: seriesID, TournamentID: tournamentID,
		FirstParticipantID: participants[0], SecondParticipantID: participants[1],
		Format: domain.SeriesFormatBO3, State: domain.SeriesStateActive,
		CurrentScoreRevisionID: &currentScoreRevisionID,
		Slots: []domain.GameSlot{{
			ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
			ScoreBefore: domain.SeriesScore{},
			Attempts: []domain.Game{{
				ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.GameStateActive,
			}},
		}},
	}}
	require.NoError(t, series.Validate())
	wave := task042ActiveWave(t, tournamentID, waveID, participants, now)
	scope := domain.FailedAttemptScope{
		TournamentID: tournamentID, WaveID: waveID, SeriesID: seriesID,
		SlotID: slotID, GameID: gameID, AssignmentID: task042ID(209),
		AssignmentAttemptID: task042ID(210),
	}
	authority := gameusecase.AttemptAuthority{
		Scope: scope, Revision: 7, Wave: wave, Series: series,
		ActiveSnapshotID: task042ID(211), CurrentOrdinal: 1,
		CurrentProjectionRevision: 3,
	}
	command := gameusecase.AttemptCommand{
		Scope: scope, CommandID: task042ID(212), FailureClass: gamedomain.FailureNoSolve,
		Expected: gameusecase.Expectation{
			AttemptNo: 1, State: domain.GameStateActive,
			SnapshotID: authority.ActiveSnapshotID, Category: domain.CategoryWeb,
		},
		Revisions: gameusecase.AttemptRevisionSet{
			GameResultRevisionID: domain.OfficialResultRevisionID(task042ID(213)),
			ScoreRevisionID:      domain.SeriesScoreRevisionID(task042ID(214)),
			RouteEvidenceID:      task042ID(215), AuditEventID: task042ID(216),
			OutboxEventID: task042ID(217), ProjectionRevisionID: task042ID(218),
		},
	}
	return authority, command
}

func task042ActiveWave(
	t *testing.T,
	tournamentID uuid.UUID,
	waveID uuid.UUID,
	participants [2]uuid.UUID,
	now time.Time,
) domain.Wave {
	t.Helper()
	wave := domain.Wave{
		ID: waveID, TournamentID: tournamentID,
		RevisionID: domain.WaveRevisionID(task042ID(219)),
		State:      domain.WaveStatePlanned,
		Members: []domain.WaveMember{
			{ParticipantID: participants[0]}, {ParticipantID: participants[1]},
		},
	}
	openedAt := now.Add(-20 * time.Second)
	require.NoError(t, wave.OpenReadyWindow(
		task042ID(220),
		domain.ReadyWindowRevisionID(task042ID(221)),
		openedAt,
		now.Add(10*time.Second),
	))
	for _, participantID := range participants {
		_, err := wave.MarkReady(wave.ReadyWindow.ID, participantID, openedAt.Add(time.Second))
		require.NoError(t, err)
	}
	_, err := wave.Start(wave.ReadyWindow.ID, now.Add(-5*time.Second))
	require.NoError(t, err)
	require.NoError(t, wave.Validate())
	return wave
}

func task042MaxInt() int {
	return int(math.MaxInt)
}

func task042Lease(
	now time.Time,
	tournamentID uuid.UUID,
	epoch int64,
) authoritydomain.Lease {
	return authoritydomain.Lease{
		TournamentID: tournamentID,
		HolderID:     task042ID(60),
		LeaseID:      task042ID(61),
		Epoch:        epoch,
		ProcessKind:  authoritydomain.ProcessAuthority,
		Revision:     4,
		CommandID:    task042ID(62),
		Previous:     &authoritydomain.Stamp{LeaseID: task042ID(63), Epoch: epoch - 1},
		AcquiredAt:   now.Add(-time.Second),
		RenewedAt:    now.Add(-time.Second),
		ExpiresAt:    now.Add(time.Minute),
	}
}

func task042ID(value int) uuid.UUID {
	name := fmt.Sprintf("task-042-%s", time.Unix(int64(value), 0).UTC().Format(time.RFC3339))
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name))
}
