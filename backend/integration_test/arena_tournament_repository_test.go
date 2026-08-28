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
