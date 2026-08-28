//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

type arenaRepositoryFixture struct {
	tx          *postgres.TxManager
	tournaments *postgres.ArenaTournamentPostgres
	swiss       *postgres.ArenaSwissPostgres
}

func newArenaRepositoryFixture() *arenaRepositoryFixture {
	tx := postgres.NewTxManager(sharedPool)
	return &arenaRepositoryFixture{
		tx:          tx,
		tournaments: postgres.NewArenaTournamentPostgres(tx),
		swiss:       postgres.NewArenaSwissPostgres(tx),
	}
}

func TestArenaTournamentRepository(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })
	fixture := newArenaRepositoryFixture()
	baseTime := time.Now().UTC().Truncate(time.Microsecond)

	firstTournament, firstRoster := createArenaRepositoryTournament(t, ctx, fixture, baseTime)
	secondTournament, secondRoster := createArenaRepositoryTournament(t, ctx, fixture, baseTime.Add(time.Second))
	playerIDs := createArenaMigrationPlayers(t, ctx, 5)

	firstParticipant := addArenaRepositoryParticipant(
		t,
		ctx,
		fixture,
		firstRoster.ID,
		playerIDs[0],
		1,
		domain.ArenaAttendanceStateInvited,
		baseTime.Add(2*time.Second),
	)
	updated, changed, err := fixture.tournaments.UpdateAttendance(
		ctx,
		firstParticipant.ID,
		domain.ArenaAttendanceStateInvited,
		domain.ArenaAttendanceStateRegistered,
		baseTime.Add(3*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.ArenaAttendanceStateRegistered, updated.Attendance)
	updated, changed, err = fixture.tournaments.UpdateAttendance(
		ctx,
		firstParticipant.ID,
		domain.ArenaAttendanceStateRegistered,
		domain.ArenaAttendanceStateCheckedIn,
		baseTime.Add(4*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.ArenaAttendanceStateCheckedIn, updated.Attendance)

	addArenaRepositoryParticipant(
		t, ctx, fixture, firstRoster.ID, playerIDs[1], 2,
		domain.ArenaAttendanceStateCheckedIn, baseTime.Add(2*time.Second),
	)
	addArenaRepositoryParticipant(
		t, ctx, fixture, firstRoster.ID, playerIDs[4], 3,
		domain.ArenaAttendanceStateWithdrawn, baseTime.Add(2*time.Second),
	)
	addArenaRepositoryParticipant(
		t, ctx, fixture, secondRoster.ID, playerIDs[1], 1,
		domain.ArenaAttendanceStateCheckedIn, baseTime.Add(2*time.Second),
	)
	addArenaRepositoryParticipant(
		t, ctx, fixture, secondRoster.ID, playerIDs[2], 2,
		domain.ArenaAttendanceStateCheckedIn, baseTime.Add(2*time.Second),
	)

	participants, err := fixture.tournaments.ListParticipants(ctx, firstRoster.ID)
	require.NoError(t, err)
	require.Len(t, participants, 3)
	require.Equal(t, firstTournament.ID, participants[0].TournamentID)
	require.Equal(t, []int{1, 2, 3}, []int{participants[0].Seed, participants[1].Seed, participants[2].Seed})

	locked, changed, err := fixture.tournaments.LockRosterAndReserve(
		ctx,
		firstRoster.ID,
		firstRoster.Revision,
		baseTime.Add(5*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.EqualValues(t, 2, locked.Revision)
	require.NotNil(t, locked.LockedAt)
	reservations, err := fixture.tournaments.ListReservations(ctx, firstTournament.ID)
	require.NoError(t, err)
	require.Len(t, reservations, 2, "withdrawn participants must not be reserved")

	_, changed, err = fixture.tournaments.LockRosterAndReserve(
		ctx,
		secondRoster.ID,
		secondRoster.Revision,
		baseTime.Add(5*time.Second),
	)
	require.ErrorIs(t, err, domain.ErrConflict)
	require.False(t, changed)
	secondAfterConflict, err := fixture.tournaments.GetRoster(ctx, secondRoster.ID)
	require.NoError(t, err)
	require.Nil(t, secondAfterConflict.LockedAt)
	reservations, err = fixture.tournaments.ListReservations(ctx, secondTournament.ID)
	require.NoError(t, err)
	require.Empty(t, reservations, "reservation conflict must roll back every insert")

	unlocked, changed, err := fixture.tournaments.UnlockRosterAndRelease(
		ctx,
		firstRoster.ID,
		locked.Revision,
		baseTime.Add(6*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, unlocked.LockedAt)
	reservations, err = fixture.tournaments.ListReservations(ctx, firstTournament.ID)
	require.NoError(t, err)
	require.Empty(t, reservations)

	secondLocked, changed, err := fixture.tournaments.LockRosterAndReserve(
		ctx,
		secondRoster.ID,
		secondRoster.Revision,
		baseTime.Add(7*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)
	started, changed, err := fixture.tournaments.MarkRosterExecutionStarted(
		ctx,
		secondRoster.ID,
		secondLocked.Revision,
		baseTime.Add(8*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, started.ExecutionStartedAt)
	_, changed, err = fixture.tournaments.UnlockRosterAndRelease(
		ctx,
		secondRoster.ID,
		started.Revision,
		baseTime.Add(9*time.Second),
	)
	require.NoError(t, err)
	require.False(t, changed, "a started roster cannot use the approved unlock path")
	reservations, err = fixture.tournaments.ListReservations(ctx, secondTournament.ID)
	require.NoError(t, err)
	require.Len(t, reservations, 2)

	finishedAt := baseTime.Add(10 * time.Second)
	cancelled, changed, err := fixture.tournaments.Transition(ctx, postgres.ArenaTournamentTransitionInput{
		ID:               secondTournament.ID,
		ExpectedRevision: secondTournament.Revision,
		ExpectedState:    secondTournament.State,
		NextState:        domain.ArenaTournamentStateCancelled,
		UpdatedAt:        finishedAt,
		FinishedAt:       &finishedAt,
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.ArenaTournamentStateCancelled, cancelled.State)
	reservations, err = fixture.tournaments.ListReservations(ctx, secondTournament.ID)
	require.NoError(t, err)
	require.Empty(t, reservations, "cancellation must release Arena reservations atomically")

	firstTournament = transitionArenaRepositoryTournament(
		t, ctx, fixture, firstTournament, domain.ArenaTournamentStateRegistration, baseTime.Add(11*time.Second),
	)
	firstTournament = transitionArenaRepositoryTournament(
		t, ctx, fixture, firstTournament, domain.ArenaTournamentStateRosterLocked, baseTime.Add(12*time.Second),
	)
	firstTournament = transitionArenaRepositoryTournament(
		t, ctx, fixture, firstTournament, domain.ArenaTournamentStateSwiss, baseTime.Add(13*time.Second),
	)
	active, err := fixture.tournaments.Active(ctx)
	require.NoError(t, err)
	require.Equal(t, firstTournament.ID, active.ID)

	thirdTournament, _ := createArenaRepositoryTournament(t, ctx, fixture, baseTime.Add(14*time.Second))
	thirdTournament = transitionArenaRepositoryTournament(
		t, ctx, fixture, thirdTournament, domain.ArenaTournamentStateRegistration, baseTime.Add(15*time.Second),
	)
	thirdTournament = transitionArenaRepositoryTournament(
		t, ctx, fixture, thirdTournament, domain.ArenaTournamentStateRosterLocked, baseTime.Add(16*time.Second),
	)
	startedAt := baseTime.Add(17 * time.Second)
	_, changed, err = fixture.tournaments.Transition(ctx, postgres.ArenaTournamentTransitionInput{
		ID:               thirdTournament.ID,
		ExpectedRevision: thirdTournament.Revision,
		ExpectedState:    thirdTournament.State,
		NextState:        domain.ArenaTournamentStateSwiss,
		UpdatedAt:        startedAt,
		StartedAt:        &startedAt,
	})
	require.ErrorIs(t, err, domain.ErrConflict)
	require.False(t, changed)

	tournaments, err := fixture.tournaments.List(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(tournaments), 3)

	concurrentFirstTournament, concurrentFirstRoster := createArenaRepositoryTournament(
		t, ctx, fixture, baseTime.Add(18*time.Second),
	)
	concurrentSecondTournament, concurrentSecondRoster := createArenaRepositoryTournament(
		t, ctx, fixture, baseTime.Add(19*time.Second),
	)
	concurrentPlayers := createArenaMigrationPlayers(t, ctx, 2)
	for _, targetRoster := range []*postgres.ArenaRosterRecord{concurrentFirstRoster, concurrentSecondRoster} {
		seed := int32(1)
		for _, playerID := range concurrentPlayers {
			addArenaRepositoryParticipant(
				t,
				ctx,
				fixture,
				targetRoster.ID,
				playerID,
				seed,
				domain.ArenaAttendanceStateCheckedIn,
				baseTime.Add(20*time.Second),
			)
			seed++
		}
	}
	type concurrentLockResult struct {
		tournamentID uuid.UUID
		rosterID     uuid.UUID
		roster       *postgres.ArenaRosterRecord
		changed      bool
		err          error
	}
	startLocks := make(chan struct{})
	lockResults := make(chan concurrentLockResult, 2)
	for _, target := range []struct {
		tournamentID uuid.UUID
		roster       *postgres.ArenaRosterRecord
	}{
		{concurrentFirstTournament.ID, concurrentFirstRoster},
		{concurrentSecondTournament.ID, concurrentSecondRoster},
	} {
		go func() {
			<-startLocks
			lockedRoster, lockChanged, lockErr := fixture.tournaments.LockRosterAndReserve(
				ctx,
				target.roster.ID,
				target.roster.Revision,
				baseTime.Add(21*time.Second),
			)
			lockResults <- concurrentLockResult{
				tournamentID: target.tournamentID,
				rosterID:     target.roster.ID,
				roster:       lockedRoster,
				changed:      lockChanged,
				err:          lockErr,
			}
		}()
	}
	close(startLocks)
	concurrentResults := []concurrentLockResult{<-lockResults, <-lockResults}
	var winner, loser concurrentLockResult
	for _, result := range concurrentResults {
		if result.changed {
			winner = result
			continue
		}
		loser = result
	}
	require.NotNil(t, winner.roster)
	require.NoError(t, winner.err)
	require.ErrorIs(t, loser.err, domain.ErrConflict)
	require.False(t, loser.changed)
	winnerReservations, err := fixture.tournaments.ListReservations(ctx, winner.tournamentID)
	require.NoError(t, err)
	require.Len(t, winnerReservations, 2)
	loserReservations, err := fixture.tournaments.ListReservations(ctx, loser.tournamentID)
	require.NoError(t, err)
	require.Empty(t, loserReservations)
	_, changed, err = fixture.tournaments.UnlockRosterAndRelease(
		ctx,
		winner.rosterID,
		winner.roster.Revision,
		baseTime.Add(22*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)

	rollbackTournament, rollbackRoster := createArenaRepositoryTournament(
		t,
		ctx,
		fixture,
		baseTime.Add(23*time.Second),
	)
	rollbackPlayers := createArenaMigrationPlayers(t, ctx, 2)
	seed := int32(1)
	for _, playerID := range rollbackPlayers {
		addArenaRepositoryParticipant(
			t,
			ctx,
			fixture,
			rollbackRoster.ID,
			playerID,
			seed,
			domain.ArenaAttendanceStateCheckedIn,
			baseTime.Add(24*time.Second),
		)
		seed++
	}
	rollbackCause := errors.New("force outer rollback")
	err = fixture.tx.Do(ctx, func(txCtx context.Context) error {
		_, innerChanged, lockErr := fixture.tournaments.LockRosterAndReserve(
			txCtx,
			rollbackRoster.ID,
			rollbackRoster.Revision,
			baseTime.Add(25*time.Second),
		)
		require.NoError(t, lockErr)
		require.True(t, innerChanged)
		return rollbackCause
	})
	require.ErrorIs(t, err, rollbackCause)
	rolledBackRoster, err := fixture.tournaments.GetRoster(ctx, rollbackRoster.ID)
	require.NoError(t, err)
	require.Equal(t, rollbackRoster.Revision, rolledBackRoster.Revision)
	require.Nil(t, rolledBackRoster.LockedAt)
	reservations, err = fixture.tournaments.ListReservations(ctx, rollbackTournament.ID)
	require.NoError(t, err)
	require.Empty(t, reservations, "nested repository work must reuse and roll back with the outer transaction")
}

func TestArenaTournamentUseCases(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })
	fixture := newArenaRepositoryFixture()
	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := fixedArenaIntegrationClock{now: now}
	catalog := arena.NewTournamentUseCase(fixture.tournaments, clock)
	attendance := arena.NewAttendanceUseCase(fixture.tournaments, clock)
	rosterLock := arena.NewRosterLockUseCase(fixture.tournaments, clock)
	command := arena.TournamentCreateCommand{TournamentID: uuid.New(), RosterID: uuid.New()}

	created, changed, err := catalog.CreateTournament(ctx, command)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.ArenaPresetV1, created.Preset)
	require.Equal(t, command.RosterID, created.RosterID)
	retried, changed, err := catalog.CreateTournament(ctx, command)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, created.ID, retried.ID)

	playerIDs := createArenaMigrationPlayers(t, ctx, 5)
	participantIDs := make([]uuid.UUID, 4)
	for index := range participantIDs {
		participantIDs[index] = uuid.New()
		participant, inviteChanged, inviteErr := attendance.InviteParticipant(ctx, arena.ParticipantInvitationCommand{
			ParticipantID: participantIDs[index], RosterID: command.RosterID,
			PlayerID: playerIDs[index], Seed: index + 1,
		})
		require.NoError(t, inviteErr)
		require.True(t, inviteChanged)
		participant, attendanceChanged, attendanceErr := attendance.ChangeAttendance(ctx, arena.AttendanceChangeCommand{
			ParticipantID: participant.ID, Expected: domain.ArenaAttendanceStateInvited,
			Next: domain.ArenaAttendanceStateRegistered,
		})
		require.NoError(t, attendanceErr)
		require.True(t, attendanceChanged)
		_, attendanceChanged, attendanceErr = attendance.ChangeAttendance(ctx, arena.AttendanceChangeCommand{
			ParticipantID: participant.ID, Expected: domain.ArenaAttendanceStateRegistered,
			Next: domain.ArenaAttendanceStateCheckedIn,
		})
		require.NoError(t, attendanceErr)
		require.True(t, attendanceChanged)
	}

	_, changed, err = attendance.ChangeAttendance(ctx, arena.AttendanceChangeCommand{
		ParticipantID: participantIDs[0], Expected: domain.ArenaAttendanceStateCheckedIn,
		Next: domain.ArenaAttendanceStateWithdrawn,
	})
	require.NoError(t, err)
	require.True(t, changed)
	replacementID := uuid.New()
	replacement, changed, err := attendance.ReplaceWithdrawnParticipant(ctx, arena.ParticipantReplacementCommand{
		WithdrawnParticipantID: participantIDs[0], ReplacementParticipantID: replacementID,
		RosterID: command.RosterID, ReplacementPlayerID: playerIDs[4],
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 1, replacement.Seed)
	require.Equal(t, domain.ArenaAttendanceStateInvited, replacement.Attendance)
	replacement, changed, err = attendance.ChangeAttendance(ctx, arena.AttendanceChangeCommand{
		ParticipantID: replacementID, Expected: domain.ArenaAttendanceStateInvited,
		Next: domain.ArenaAttendanceStateRegistered,
	})
	require.NoError(t, err)
	require.True(t, changed)
	_, changed, err = attendance.ChangeAttendance(ctx, arena.AttendanceChangeCommand{
		ParticipantID: replacement.ID, Expected: domain.ArenaAttendanceStateRegistered,
		Next: domain.ArenaAttendanceStateCheckedIn,
	})
	require.NoError(t, err)
	require.True(t, changed)

	listed, err := catalog.ListTournaments(ctx, arena.TournamentListFilter{
		States: []domain.ArenaTournamentState{domain.ArenaTournamentStateDraft},
	})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, command.RosterID, listed[0].RosterID)
	require.Equal(t, 4, listed[0].RosterSize)

	currentRoster, err := fixture.tournaments.GetRoster(ctx, command.RosterID)
	require.NoError(t, err)
	checkedInPlayerIDs := []uuid.UUID{playerIDs[1], playerIDs[2], playerIDs[3], playerIDs[4]}
	wrongEvidence := arena.RosterPreflightEvidence{
		RosterID: command.RosterID, RosterRevision: currentRoster.Revision,
		CheckedInPlayerIDs: []uuid.UUID{playerIDs[0], playerIDs[1], playerIDs[2], playerIDs[3]}, Approved: true,
	}
	_, changed, err = rosterLock.LockRoster(ctx, arena.RosterLockCommand{Preflight: wrongEvidence})
	require.ErrorIs(t, err, domain.ErrConflict)
	require.False(t, changed)
	reservations, err := fixture.tournaments.ListReservations(ctx, command.TournamentID)
	require.NoError(t, err)
	require.Empty(t, reservations)

	locked, changed, err := rosterLock.LockRoster(ctx, arena.RosterLockCommand{Preflight: arena.RosterPreflightEvidence{
		RosterID: command.RosterID, RosterRevision: currentRoster.Revision,
		CheckedInPlayerIDs: checkedInPlayerIDs, Approved: true,
	}})
	require.NoError(t, err)
	require.True(t, changed)
	reservations, err = fixture.tournaments.ListReservations(ctx, command.TournamentID)
	require.NoError(t, err)
	require.Len(t, reservations, 4)

	unlocked, changed, err := rosterLock.UnlockRoster(ctx, arena.RosterUnlockCommand{
		RosterID: command.RosterID, ExpectedRevision: locked.Revision,
		ActorID: uuid.New(), Reason: "replace pre-start participant",
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, unlocked.LockedAt)
	require.Equal(t, locked.Revision+1, unlocked.Revision)
	reservations, err = fixture.tournaments.ListReservations(ctx, command.TournamentID)
	require.NoError(t, err)
	require.Empty(t, reservations)
}

func TestArenaTournamentLifecycleUseCase(t *testing.T) {
	ctx := context.Background()
	resetArenaMigrationTables(t)
	t.Cleanup(func() { resetArenaMigrationTables(t) })
	fixture := newArenaRepositoryFixture()
	now := time.Now().UTC().Truncate(time.Microsecond)
	useCase := arena.NewTournamentLifecycleUseCase(fixture.tournaments, fixedArenaIntegrationClock{now: now})

	first, _ := createArenaRepositoryTournament(t, ctx, fixture, now.Add(-2*time.Minute))
	second, _ := createArenaRepositoryTournament(t, ctx, fixture, now.Add(-time.Minute))
	for _, id := range []uuid.UUID{first.ID, second.ID} {
		registration, changed, err := useCase.Transition(ctx, arena.TournamentLifecycleCommand{
			TournamentID: id, ExpectedRevision: 1, NextState: domain.ArenaTournamentStateRegistration,
		})
		require.NoError(t, err)
		require.True(t, changed)
		_, changed, err = useCase.Transition(ctx, arena.TournamentLifecycleCommand{
			TournamentID: id, ExpectedRevision: registration.Revision,
			NextState: domain.ArenaTournamentStateRosterLocked,
		})
		require.NoError(t, err)
		require.True(t, changed)
	}

	type startResult struct {
		id      uuid.UUID
		record  *arena.TournamentRecord
		changed bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan startResult, 2)
	for _, id := range []uuid.UUID{first.ID, second.ID} {
		go func() {
			<-start
			record, changed, err := useCase.Transition(ctx, arena.TournamentLifecycleCommand{
				TournamentID: id, ExpectedRevision: 3, NextState: domain.ArenaTournamentStateSwiss,
			})
			results <- startResult{id: id, record: record, changed: changed, err: err}
		}()
	}
	close(start)
	concurrent := []startResult{<-results, <-results}
	var winner, loser startResult
	for _, result := range concurrent {
		if result.changed {
			winner = result
		} else {
			loser = result
		}
	}
	require.NotNil(t, winner.record)
	require.NoError(t, winner.err)
	require.ErrorIs(t, loser.err, domain.ErrConflict)
	require.False(t, loser.changed)

	retried, changed, err := useCase.Transition(ctx, arena.TournamentLifecycleCommand{
		TournamentID: winner.id, ExpectedRevision: 3, NextState: domain.ArenaTournamentStateSwiss,
	})
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, winner.id, retried.ID)

	playoffs, changed, err := useCase.Transition(ctx, arena.TournamentLifecycleCommand{
		TournamentID: winner.id, ExpectedRevision: winner.record.Revision,
		NextState: domain.ArenaTournamentStatePlayoffs,
	})
	require.NoError(t, err)
	require.True(t, changed)
	_, changed, err = useCase.Transition(ctx, arena.TournamentLifecycleCommand{
		TournamentID: winner.id, ExpectedRevision: playoffs.Revision,
		NextState: domain.ArenaTournamentStateCompleted,
	})
	require.NoError(t, err)
	require.True(t, changed)

	started, changed, err := useCase.Transition(ctx, arena.TournamentLifecycleCommand{
		TournamentID: loser.id, ExpectedRevision: 3, NextState: domain.ArenaTournamentStateSwiss,
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, loser.id, started.ID)
}

type fixedArenaIntegrationClock struct {
	now time.Time
}

func (c fixedArenaIntegrationClock) Now() time.Time {
	return c.now
}

func createArenaRepositoryTournament(
	t testing.TB,
	ctx context.Context,
	fixture *arenaRepositoryFixture,
	createdAt time.Time,
) (*postgres.ArenaTournamentRecord, *postgres.ArenaRosterRecord) {
	t.Helper()
	tournament, roster, err := fixture.tournaments.Create(ctx, uuid.New(), uuid.New(), createdAt)
	require.NoError(t, err)
	require.Equal(t, domain.ArenaTournamentStateDraft, tournament.State)
	require.EqualValues(t, 1, tournament.Revision)
	require.EqualValues(t, 1, roster.Revision)
	return tournament, roster
}

func addArenaRepositoryParticipant(
	t testing.TB,
	ctx context.Context,
	fixture *arenaRepositoryFixture,
	rosterID uuid.UUID,
	playerID uuid.UUID,
	seed int32,
	attendance domain.ArenaAttendanceState,
	createdAt time.Time,
) *postgres.ArenaParticipantRecord {
	t.Helper()
	participant, changed, err := fixture.tournaments.AddParticipant(ctx, postgres.ArenaParticipantInput{
		ID:         uuid.New(),
		RosterID:   rosterID,
		PlayerID:   playerID,
		Seed:       seed,
		Attendance: attendance,
		CreatedAt:  createdAt,
	})
	require.NoError(t, err)
	require.True(t, changed)
	return participant
}

func transitionArenaRepositoryTournament(
	t testing.TB,
	ctx context.Context,
	fixture *arenaRepositoryFixture,
	current *postgres.ArenaTournamentRecord,
	next domain.ArenaTournamentState,
	at time.Time,
) *postgres.ArenaTournamentRecord {
	t.Helper()
	input := postgres.ArenaTournamentTransitionInput{
		ID:               current.ID,
		ExpectedRevision: current.Revision,
		ExpectedState:    current.State,
		NextState:        next,
		UpdatedAt:        at,
		StartedAt:        current.StartedAt,
		FinishedAt:       current.FinishedAt,
	}
	if next == domain.ArenaTournamentStateSwiss && input.StartedAt == nil {
		input.StartedAt = &at
	}
	updated, changed, err := fixture.tournaments.Transition(ctx, input)
	require.NoError(t, err)
	require.True(t, changed)
	return updated
}
