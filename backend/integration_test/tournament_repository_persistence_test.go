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
	attendancerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/attendance"
	catalogrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/catalog"
	rosterrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/roster"
	swissrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/swiss"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type repositoryFixture struct {
	tx          *postgres.TxManager
	tournaments *catalogrepo.TournamentCatalogPostgres
	attendance  *attendancerepo.TournamentAttendancePostgres
	roster      *rosterrepo.RosterPostgres
	swiss       *swissrepo.SwissPostgres
}

func newRepositoryFixture() *repositoryFixture {
	tx := postgres.NewTxManager(sharedPool)
	return &repositoryFixture{
		tx:          tx,
		tournaments: catalogrepo.NewTournamentCatalogPostgres(tx),
		attendance:  attendancerepo.NewTournamentAttendancePostgres(tx),
		roster:      rosterrepo.NewRosterPostgres(tx),
		swiss:       swissrepo.NewSwissPostgres(tx),
	}
}

func TestTournamentRepository(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })
	prepareTournamentCreateReceiptContent(ctx, t)
	fixture := newRepositoryFixture()
	baseTime := time.Now().UTC().Truncate(time.Microsecond)

	firstTournament, firstRoster := createRepositoryTournament(ctx, t, fixture, baseTime)
	secondTournament, secondRoster := createRepositoryTournament(ctx, t, fixture, baseTime.Add(time.Second))
	playerIDs := createMigrationPlayers(ctx, t, 5)

	firstParticipant := addRepositoryParticipant(
		ctx, t,
		fixture,
		firstRoster.ID,
		playerIDs[0],
		1,
		domain.AttendanceStateInvited,
		baseTime.Add(2*time.Second),
	)
	updated, changed, err := fixture.roster.UpdateAttendance(
		ctx,
		firstParticipant.ID,
		domain.AttendanceStateInvited,
		domain.AttendanceStateRegistered,
		baseTime.Add(3*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.AttendanceStateRegistered, updated.Attendance)
	updated, changed, err = fixture.roster.UpdateAttendance(
		ctx,
		firstParticipant.ID,
		domain.AttendanceStateRegistered,
		domain.AttendanceStateCheckedIn,
		baseTime.Add(4*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.AttendanceStateCheckedIn, updated.Attendance)

	addRepositoryParticipant(
		ctx, t, fixture, firstRoster.ID, playerIDs[1], 2,
		domain.AttendanceStateCheckedIn, baseTime.Add(2*time.Second),
	)
	addRepositoryParticipant(
		ctx, t, fixture, firstRoster.ID, playerIDs[4], 3,
		domain.AttendanceStateWithdrawn, baseTime.Add(2*time.Second),
	)
	addRepositoryParticipant(
		ctx, t, fixture, secondRoster.ID, playerIDs[1], 1,
		domain.AttendanceStateCheckedIn, baseTime.Add(2*time.Second),
	)
	addRepositoryParticipant(
		ctx, t, fixture, secondRoster.ID, playerIDs[2], 2,
		domain.AttendanceStateCheckedIn, baseTime.Add(2*time.Second),
	)

	participants, err := fixture.roster.ListParticipants(ctx, firstRoster.ID)
	require.NoError(t, err)
	require.Len(t, participants, 3)
	require.Equal(t, firstTournament.ID, participants[0].TournamentID)
	require.Equal(t, []int{1, 2, 3}, []int{participants[0].Seed, participants[1].Seed, participants[2].Seed})
	firstRoster, err = fixture.roster.GetRoster(ctx, firstRoster.ID)
	require.NoError(t, err)
	secondRoster, err = fixture.roster.GetRoster(ctx, secondRoster.ID)
	require.NoError(t, err)

	locked, changed, err := fixture.roster.LockRosterAndReserve(
		ctx,
		firstRoster.ID,
		firstRoster.Revision,
		baseTime.Add(5*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, firstRoster.Revision+1, locked.Revision)
	require.NotNil(t, locked.LockedAt)
	reservations, err := fixture.roster.ListReservations(ctx, firstTournament.ID)
	require.NoError(t, err)
	require.Len(t, reservations, 2, "withdrawn participants must not be reserved")

	_, changed, err = fixture.roster.LockRosterAndReserve(
		ctx,
		secondRoster.ID,
		secondRoster.Revision,
		baseTime.Add(5*time.Second),
	)
	require.ErrorIs(t, err, domain.ErrConflict)
	require.False(t, changed)
	secondAfterConflict, err := fixture.roster.GetRoster(ctx, secondRoster.ID)
	require.NoError(t, err)
	require.Nil(t, secondAfterConflict.LockedAt)
	reservations, err = fixture.roster.ListReservations(ctx, secondTournament.ID)
	require.NoError(t, err)
	require.Empty(t, reservations, "reservation conflict must roll back every insert")

	unlocked, changed, err := fixture.roster.UnlockRosterAndRelease(
		ctx,
		firstRoster.ID,
		locked.Revision,
		baseTime.Add(6*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Nil(t, unlocked.LockedAt)
	reservations, err = fixture.roster.ListReservations(ctx, firstTournament.ID)
	require.NoError(t, err)
	require.Empty(t, reservations)

	secondLocked, changed, err := fixture.roster.LockRosterAndReserve(
		ctx,
		secondRoster.ID,
		secondRoster.Revision,
		baseTime.Add(7*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)
	started, changed, err := fixture.roster.MarkRosterExecutionStarted(
		ctx,
		secondRoster.ID,
		secondLocked.Revision,
		baseTime.Add(8*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotNil(t, started.ExecutionStartedAt)
	_, changed, err = fixture.roster.UnlockRosterAndRelease(
		ctx,
		secondRoster.ID,
		started.Revision,
		baseTime.Add(9*time.Second),
	)
	require.NoError(t, err)
	require.False(t, changed, "a started roster cannot use the approved unlock path")
	reservations, err = fixture.roster.ListReservations(ctx, secondTournament.ID)
	require.NoError(t, err)
	require.Len(t, reservations, 2)

	finishedAt := baseTime.Add(10 * time.Second)
	_, changed, err = fixture.tournaments.Transition(ctx, catalogrepo.TournamentTransitionInput{
		ID:               secondTournament.ID,
		ExpectedRevision: secondTournament.Revision,
		ExpectedState:    secondTournament.State,
		NextState:        domain.TournamentStateCancelled,
		UpdatedAt:        finishedAt,
		FinishedAt:       &finishedAt,
	})
	require.NoError(t, err)
	require.True(t, changed)
	cancelled, err := fixture.tournaments.GetTournament(ctx, secondTournament.ID)
	require.NoError(t, err)
	require.Equal(t, domain.TournamentStateCancelled, cancelled.State)
	reservations, err = fixture.roster.ListReservations(ctx, secondTournament.ID)
	require.NoError(t, err)
	require.Empty(t, reservations, "cancellation must release tournament reservations atomically")

	firstTournament = transitionRepositoryTournament(
		ctx, t, fixture, firstTournament, domain.TournamentStateRegistration, baseTime.Add(11*time.Second),
	)
	firstTournament = transitionRepositoryTournament(
		ctx, t, fixture, firstTournament, domain.TournamentStateRosterLocked, baseTime.Add(12*time.Second),
	)
	firstTournament = transitionRepositoryTournament(
		ctx, t, fixture, firstTournament, domain.TournamentStateSwiss, baseTime.Add(13*time.Second),
	)
	thirdTournament, _ := createRepositoryTournament(ctx, t, fixture, baseTime.Add(14*time.Second))
	thirdTournament = transitionRepositoryTournament(
		ctx, t, fixture, thirdTournament, domain.TournamentStateRegistration, baseTime.Add(15*time.Second),
	)
	thirdTournament = transitionRepositoryTournament(
		ctx, t, fixture, thirdTournament, domain.TournamentStateRosterLocked, baseTime.Add(16*time.Second),
	)
	startedAt := baseTime.Add(17 * time.Second)
	_, changed, err = fixture.tournaments.Transition(ctx, catalogrepo.TournamentTransitionInput{
		ID:               thirdTournament.ID,
		ExpectedRevision: thirdTournament.Revision,
		ExpectedState:    thirdTournament.State,
		NextState:        domain.TournamentStateSwiss,
		UpdatedAt:        startedAt,
		StartedAt:        &startedAt,
	})
	require.NoError(t, err)
	require.True(t, changed)
	thirdTournament, err = fixture.tournaments.GetTournament(ctx, thirdTournament.ID)
	require.NoError(t, err)
	require.Equal(t, domain.TournamentStateSwiss, thirdTournament.State)

	tournaments, err := fixture.tournaments.List(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(tournaments), 3)

	concurrentFirstTournament, concurrentFirstRoster := createRepositoryTournament(
		ctx, t, fixture, baseTime.Add(18*time.Second),
	)
	concurrentSecondTournament, concurrentSecondRoster := createRepositoryTournament(
		ctx, t, fixture, baseTime.Add(19*time.Second),
	)
	concurrentPlayers := createMigrationPlayers(ctx, t, 2)
	for _, targetRoster := range []*rosterrepo.RosterRecord{concurrentFirstRoster, concurrentSecondRoster} {
		seed := int32(1)
		for _, playerID := range concurrentPlayers {
			addRepositoryParticipant(
				ctx, t,
				fixture,
				targetRoster.ID,
				playerID,
				seed,
				domain.AttendanceStateCheckedIn,
				baseTime.Add(20*time.Second),
			)
			seed++
		}
	}
	concurrentFirstRoster, err = fixture.roster.GetRoster(ctx, concurrentFirstRoster.ID)
	require.NoError(t, err)
	concurrentSecondRoster, err = fixture.roster.GetRoster(ctx, concurrentSecondRoster.ID)
	require.NoError(t, err)
	type concurrentLockResult struct {
		tournamentID uuid.UUID
		rosterID     uuid.UUID
		roster       *rosterrepo.RosterRecord
		changed      bool
		err          error
	}
	startLocks := make(chan struct{})
	lockResults := make(chan concurrentLockResult, 2)
	for _, target := range []struct {
		tournamentID uuid.UUID
		roster       *rosterrepo.RosterRecord
	}{
		{concurrentFirstTournament.ID, concurrentFirstRoster},
		{concurrentSecondTournament.ID, concurrentSecondRoster},
	} {
		go func() {
			<-startLocks
			lockedRoster, lockChanged, lockErr := fixture.roster.LockRosterAndReserve(
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
	winnerReservations, err := fixture.roster.ListReservations(ctx, winner.tournamentID)
	require.NoError(t, err)
	require.Len(t, winnerReservations, 2)
	loserReservations, err := fixture.roster.ListReservations(ctx, loser.tournamentID)
	require.NoError(t, err)
	require.Empty(t, loserReservations)
	_, changed, err = fixture.roster.UnlockRosterAndRelease(
		ctx,
		winner.rosterID,
		winner.roster.Revision,
		baseTime.Add(22*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)

	rollbackTournament, rollbackRoster := createRepositoryTournament(
		ctx, t,
		fixture,
		baseTime.Add(23*time.Second),
	)
	rollbackPlayers := createMigrationPlayers(ctx, t, 2)
	seed := int32(1)
	for _, playerID := range rollbackPlayers {
		addRepositoryParticipant(
			ctx, t,
			fixture,
			rollbackRoster.ID,
			playerID,
			seed,
			domain.AttendanceStateCheckedIn,
			baseTime.Add(24*time.Second),
		)
		seed++
	}
	rollbackRoster, err = fixture.roster.GetRoster(ctx, rollbackRoster.ID)
	require.NoError(t, err)
	rollbackCause := errors.New("force outer rollback")
	err = fixture.tx.Do(ctx, func(txCtx context.Context) error {
		_, innerChanged, lockErr := fixture.roster.LockRosterAndReserve(
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
	rolledBackRoster, err := fixture.roster.GetRoster(ctx, rollbackRoster.ID)
	require.NoError(t, err)
	require.Equal(t, rollbackRoster.Revision, rolledBackRoster.Revision)
	require.Nil(t, rolledBackRoster.LockedAt)
	reservations, err = fixture.roster.ListReservations(ctx, rollbackTournament.ID)
	require.NoError(t, err)
	require.Empty(t, reservations, "nested repository work must reuse and roll back with the outer transaction")
}
