//go:build integration

package integration_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	tournamentlifecyclerepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/lifecycle"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	attendanceusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/attendance"
	catalogusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/roster"
)

func TestTournamentUseCases(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })
	contentRevision := prepareTournamentCreateReceiptContent(ctx, t)
	fixture := newRepositoryFixture()
	now := time.Now().UTC().Truncate(time.Microsecond)
	clock := newTournamentClock(t, now)
	catalog := catalogusecase.NewTournamentUseCase(postgres.NewTournamentCatalogPostgres(fixture.tournaments), clock)
	attendance := attendanceusecase.NewAttendanceUseCase(postgres.NewTournamentAttendancePostgres(fixture.tournaments), clock)
	rosterLock := rosterusecase.NewRosterLockUseCase(postgres.NewTournamentRosterPostgres(fixture.tournaments), clock)
	command := catalogusecase.TournamentCreateCommand{
		TournamentID: uuid.New(), RosterID: uuid.New(), Name: "Repository Tournament",
		PublicID: uuid.NewString(), PlannedRosterSize: 4, ContentRevision: contentRevision,
	}

	created, changed, err := catalog.CreateTournament(ctx, command)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.TournamentPresetV1, created.Preset)
	require.Equal(t, command.RosterID, created.RosterID)
	require.Equal(t, command.Name, created.Name)
	require.Equal(t, command.PublicID, created.PublicID)
	require.Equal(t, command.PlannedRosterSize, created.PlannedRosterSize)
	require.Equal(t, command.ContentRevision, created.ContentRevision)

	duplicatePublicID := command
	duplicatePublicID.TournamentID = uuid.New()
	duplicatePublicID.RosterID = uuid.New()
	_, changed, err = catalog.CreateTournament(ctx, duplicatePublicID)
	require.ErrorIs(t, err, domain.ErrConflict)
	require.False(t, changed)

	unknownContent := command
	unknownContent.TournamentID = uuid.New()
	unknownContent.RosterID = uuid.New()
	unknownContent.PublicID = uuid.NewString()
	unknownContent.ContentRevision++
	_, changed, err = catalog.CreateTournament(ctx, unknownContent)
	require.ErrorIs(t, err, domain.ErrInvalidContentConfiguration)
	require.False(t, changed)
	retried, changed, err := catalog.CreateTournament(ctx, command)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, created.ID, retried.ID)

	playerIDs := createMigrationPlayers(ctx, t, 5)
	participantIDs := make([]uuid.UUID, 4)
	for index := range participantIDs {
		participantIDs[index] = uuid.New()
		participant, inviteChanged, inviteErr := attendance.InviteParticipant(ctx, attendanceusecase.ParticipantInvitationCommand{
			ParticipantID: participantIDs[index], RosterID: command.RosterID,
			PlayerID: playerIDs[index], Seed: index + 1,
		})
		require.NoError(t, inviteErr)
		require.True(t, inviteChanged)
		participant, attendanceChanged, attendanceErr := attendance.ChangeAttendance(ctx, attendanceusecase.AttendanceChangeCommand{
			ParticipantID: participant.ID, Expected: domain.AttendanceStateInvited,
			Next: domain.AttendanceStateRegistered,
		})
		require.NoError(t, attendanceErr)
		require.True(t, attendanceChanged)
		_, attendanceChanged, attendanceErr = attendance.ChangeAttendance(ctx, attendanceusecase.AttendanceChangeCommand{
			ParticipantID: participant.ID, Expected: domain.AttendanceStateRegistered,
			Next: domain.AttendanceStateCheckedIn,
		})
		require.NoError(t, attendanceErr)
		require.True(t, attendanceChanged)
	}

	_, changed, err = attendance.ChangeAttendance(ctx, attendanceusecase.AttendanceChangeCommand{
		ParticipantID: participantIDs[0], Expected: domain.AttendanceStateCheckedIn,
		Next: domain.AttendanceStateWithdrawn,
	})
	require.NoError(t, err)
	require.True(t, changed)
	replacementID := uuid.New()
	replacement, changed, err := attendance.ReplaceWithdrawnParticipant(ctx, attendanceusecase.ParticipantReplacementCommand{
		WithdrawnParticipantID: participantIDs[0], ReplacementParticipantID: replacementID,
		RosterID: command.RosterID, ReplacementPlayerID: playerIDs[4],
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, 1, replacement.Seed)
	require.Equal(t, domain.AttendanceStateInvited, replacement.Attendance)
	replacement, changed, err = attendance.ChangeAttendance(ctx, attendanceusecase.AttendanceChangeCommand{
		ParticipantID: replacementID, Expected: domain.AttendanceStateInvited,
		Next: domain.AttendanceStateRegistered,
	})
	require.NoError(t, err)
	require.True(t, changed)
	_, changed, err = attendance.ChangeAttendance(ctx, attendanceusecase.AttendanceChangeCommand{
		ParticipantID: replacement.ID, Expected: domain.AttendanceStateRegistered,
		Next: domain.AttendanceStateCheckedIn,
	})
	require.NoError(t, err)
	require.True(t, changed)

	listed, err := catalog.ListTournaments(ctx, catalogusecase.TournamentListFilter{
		States: []domain.TournamentState{domain.TournamentStateDraft},
	})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	require.Equal(t, command.RosterID, listed[0].RosterID)
	require.Equal(t, 4, listed[0].RosterSize)

	currentRoster, err := fixture.tournaments.GetRoster(ctx, command.RosterID)
	require.NoError(t, err)
	checkedInPlayerIDs := []uuid.UUID{playerIDs[1], playerIDs[2], playerIDs[3], playerIDs[4]}
	wrongEvidence := rosterusecase.RosterPreflightEvidence{
		RosterID: command.RosterID, RosterRevision: currentRoster.Revision,
		CheckedInPlayerIDs: []uuid.UUID{playerIDs[0], playerIDs[1], playerIDs[2], playerIDs[3]}, Approved: true,
	}
	_, changed, err = rosterLock.LockRoster(ctx, rosterusecase.RosterLockCommand{Preflight: wrongEvidence})
	require.ErrorIs(t, err, domain.ErrConflict)
	require.False(t, changed)
	reservations, err := fixture.tournaments.ListReservations(ctx, command.TournamentID)
	require.NoError(t, err)
	require.Empty(t, reservations)

	locked, changed, err := rosterLock.LockRoster(ctx, rosterusecase.RosterLockCommand{Preflight: rosterusecase.RosterPreflightEvidence{
		RosterID: command.RosterID, RosterRevision: currentRoster.Revision,
		CheckedInPlayerIDs: checkedInPlayerIDs, Approved: true,
	}})
	require.NoError(t, err)
	require.True(t, changed)
	reservations, err = fixture.tournaments.ListReservations(ctx, command.TournamentID)
	require.NoError(t, err)
	require.Len(t, reservations, 4)

	unlocked, changed, err := rosterLock.UnlockRoster(ctx, rosterusecase.RosterUnlockCommand{
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

func TestTournamentLifecycleUseCase(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })
	fixture := newRepositoryFixture()
	now := time.Now().UTC().Truncate(time.Microsecond)
	useCase := lifecycleusecase.NewTournamentLifecycleUseCase(
		tournamentlifecyclerepo.NewTournamentLifecyclePostgres(fixture.tx),
		newTournamentClock(t, now),
	)

	first, _ := createRepositoryTournament(ctx, t, fixture, now.Add(-2*time.Minute))
	second, _ := createRepositoryTournament(ctx, t, fixture, now.Add(-time.Minute))
	for _, id := range []uuid.UUID{first.ID, second.ID} {
		registration, changed, err := useCase.Transition(ctx, lifecycleusecase.TournamentLifecycleCommand{
			TournamentID: id, ExpectedRevision: 1, NextState: domain.TournamentStateRegistration,
		})
		require.NoError(t, err)
		require.True(t, changed)
		_, changed, err = useCase.Transition(ctx, lifecycleusecase.TournamentLifecycleCommand{
			TournamentID: id, ExpectedRevision: registration.Revision,
			NextState: domain.TournamentStateRosterLocked,
		})
		require.NoError(t, err)
		require.True(t, changed)
	}

	type startResult struct {
		id      uuid.UUID
		record  *lifecycleusecase.LifecycleTournamentRecord
		changed bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan startResult, 2)
	for _, id := range []uuid.UUID{first.ID, second.ID} {
		go func() {
			<-start
			record, changed, err := useCase.Transition(ctx, lifecycleusecase.TournamentLifecycleCommand{
				TournamentID: id, ExpectedRevision: 3, NextState: domain.TournamentStateSwiss,
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

	retried, changed, err := useCase.Transition(ctx, lifecycleusecase.TournamentLifecycleCommand{
		TournamentID: winner.id, ExpectedRevision: 3, NextState: domain.TournamentStateSwiss,
	})
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, winner.id, retried.ID)

	playoffs, changed, err := useCase.Transition(ctx, lifecycleusecase.TournamentLifecycleCommand{
		TournamentID: winner.id, ExpectedRevision: winner.record.Revision,
		NextState: domain.TournamentStatePlayoffs,
	})
	require.NoError(t, err)
	require.True(t, changed)
	_, changed, err = useCase.Transition(ctx, lifecycleusecase.TournamentLifecycleCommand{
		TournamentID: winner.id, ExpectedRevision: playoffs.Revision,
		NextState: domain.TournamentStateCompleted,
	})
	require.NoError(t, err)
	require.True(t, changed)

	started, changed, err := useCase.Transition(ctx, lifecycleusecase.TournamentLifecycleCommand{
		TournamentID: loser.id, ExpectedRevision: 3, NextState: domain.TournamentStateSwiss,
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, loser.id, started.ID)
}

func TestTournamentAttendanceCapsConcurrentInvitations(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })
	fixture := newRepositoryFixture()
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, roster := createRepositoryTournament(ctx, t, fixture, now)
	attendance := attendanceusecase.NewAttendanceUseCase(
		postgres.NewTournamentAttendancePostgres(fixture.tournaments),
		newTournamentClock(t, now),
	)
	playerIDs := createMigrationPlayers(ctx, t, domain.TournamentMaxParticipants+1)
	for index := 0; index < domain.TournamentMaxParticipants-1; index++ {
		_, changed, err := attendance.InviteParticipant(ctx, attendanceusecase.ParticipantInvitationCommand{
			ParticipantID: uuid.New(), RosterID: roster.ID, PlayerID: playerIDs[index], Seed: index + 1,
		})
		require.NoError(t, err)
		require.True(t, changed)
	}

	type inviteResult struct {
		changed bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan inviteResult, 2)
	var callers sync.WaitGroup
	for index := domain.TournamentMaxParticipants - 1; index <= domain.TournamentMaxParticipants; index++ {
		callers.Add(1)
		go func(index int) {
			defer callers.Done()
			<-start
			_, changed, err := attendance.InviteParticipant(ctx, attendanceusecase.ParticipantInvitationCommand{
				ParticipantID: uuid.New(), RosterID: roster.ID, PlayerID: playerIDs[index], Seed: index + 1,
			})
			results <- inviteResult{changed: changed, err: err}
		}(index)
	}
	close(start)
	callers.Wait()
	close(results)

	successes := 0
	for result := range results {
		if result.changed {
			successes++
			require.NoError(t, result.err)
			continue
		}
		require.ErrorIs(t, result.err, domain.ErrConflict)
	}
	require.Equal(t, 1, successes)
	participants, err := fixture.tournaments.ListParticipants(ctx, roster.ID)
	require.NoError(t, err)
	require.Len(t, participants, domain.TournamentMaxParticipants)
}

func TestTournamentAttendanceAndRosterLockSerialize(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })
	fixture := newRepositoryFixture()
	now := time.Now().UTC().Truncate(time.Microsecond)
	_, roster := createRepositoryTournament(ctx, t, fixture, now)
	clock := newTournamentClock(t, now)
	attendance := attendanceusecase.NewAttendanceUseCase(
		postgres.NewTournamentAttendancePostgres(fixture.tournaments),
		clock,
	)
	rosterLock := rosterusecase.NewRosterLockUseCase(postgres.NewTournamentRosterPostgres(fixture.tournaments), clock)
	playerIDs := createMigrationPlayers(ctx, t, domain.TournamentMaxParticipants)
	checkedInPlayerIDs := make([]uuid.UUID, domain.TournamentMaxParticipants-1)
	for index := range checkedInPlayerIDs {
		participantID := uuid.New()
		checkedInPlayerIDs[index] = playerIDs[index]
		participant, changed, err := attendance.InviteParticipant(ctx, attendanceusecase.ParticipantInvitationCommand{
			ParticipantID: participantID, RosterID: roster.ID, PlayerID: playerIDs[index], Seed: index + 1,
		})
		require.NoError(t, err)
		require.True(t, changed)
		_, changed, err = attendance.ChangeAttendance(ctx, attendanceusecase.AttendanceChangeCommand{
			ParticipantID: participant.ID, Expected: domain.AttendanceStateInvited, Next: domain.AttendanceStateRegistered,
		})
		require.NoError(t, err)
		require.True(t, changed)
		_, changed, err = attendance.ChangeAttendance(ctx, attendanceusecase.AttendanceChangeCommand{
			ParticipantID: participant.ID, Expected: domain.AttendanceStateRegistered, Next: domain.AttendanceStateCheckedIn,
		})
		require.NoError(t, err)
		require.True(t, changed)
	}
	currentRoster, err := fixture.tournaments.GetRoster(ctx, roster.ID)
	require.NoError(t, err)

	type operationResult struct {
		changed bool
		err     error
	}
	start := make(chan struct{})
	inviteResult := make(chan operationResult, 1)
	lockResult := make(chan operationResult, 1)
	go func() {
		<-start
		_, changed, err := attendance.InviteParticipant(ctx, attendanceusecase.ParticipantInvitationCommand{
			ParticipantID: uuid.New(), RosterID: roster.ID,
			PlayerID: playerIDs[domain.TournamentMaxParticipants-1], Seed: domain.TournamentMaxParticipants,
		})
		inviteResult <- operationResult{changed: changed, err: err}
	}()
	go func() {
		<-start
		_, changed, err := rosterLock.LockRoster(ctx, rosterusecase.RosterLockCommand{Preflight: rosterusecase.RosterPreflightEvidence{
			RosterID: roster.ID, RosterRevision: currentRoster.Revision,
			CheckedInPlayerIDs: checkedInPlayerIDs, Approved: true,
		}})
		lockResult <- operationResult{changed: changed, err: err}
	}()
	close(start)
	invite := <-inviteResult
	locked := <-lockResult

	require.NotEqual(t, invite.changed, locked.changed)
	if invite.changed {
		require.NoError(t, invite.err)
		require.ErrorIs(t, locked.err, domain.ErrConflict)
	} else {
		require.ErrorIs(t, invite.err, domain.ErrConflict)
		require.NoError(t, locked.err)
	}
	participants, err := fixture.tournaments.ListParticipants(ctx, roster.ID)
	require.NoError(t, err)
	updatedRoster, err := fixture.tournaments.GetRoster(ctx, roster.ID)
	require.NoError(t, err)
	if locked.changed {
		require.Len(t, participants, domain.TournamentMaxParticipants-1)
		require.NotNil(t, updatedRoster.LockedAt)
	} else {
		require.Len(t, participants, domain.TournamentMaxParticipants)
		require.Nil(t, updatedRoster.LockedAt)
	}
}
