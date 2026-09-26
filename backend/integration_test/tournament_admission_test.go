//go:build integration

package integration_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	admissionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/admission"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	admissionusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admission"
)

func TestTournamentAdmissionPromotesInvitedParticipantWhenRosterIsFull(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })
	prepareTournamentCreateReceiptContent(ctx, t)

	fixture := newRepositoryFixture()
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	tournament, roster := createRepositoryTournament(ctx, t, fixture, createdAt)
	players := createMigrationPlayers(ctx, t, 5)

	for index, playerID := range players[:4] {
		attendance := domain.AttendanceStateRegistered
		if index == 0 {
			attendance = domain.AttendanceStateInvited
		}
		addRepositoryParticipant(
			ctx,
			t,
			fixture,
			roster.ID,
			playerID,
			int32(index+1),
			attendance,
			createdAt.Add(time.Duration(index+1)*time.Second),
		)
	}
	tournament = transitionRepositoryTournament(
		ctx,
		t,
		fixture,
		tournament,
		domain.TournamentStateRegistration,
		createdAt.Add(5*time.Second),
	)

	repository := admissionrepo.NewTournamentAdmissionPostgres(fixture.tx)
	joined, changed, err := repository.Join(ctx, admissionusecase.JoinInput{
		TournamentID:  tournament.ID,
		PlayerID:      players[0],
		ParticipantID: uuid.New(),
		CommandID:     uuid.New(),
		JoinedAt:      createdAt.Add(6 * time.Second),
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, players[0], joined.PlayerID)
	require.Equal(t, 1, joined.Seed)
	require.Equal(t, domain.AttendanceStateRegistered, joined.Attendance)
	require.Equal(t, 4, joined.RosterSize)

	participants, err := fixture.roster.ListParticipants(ctx, roster.ID)
	require.NoError(t, err)
	require.Len(t, participants, 4)
	for _, participant := range participants {
		if participant.PlayerID == players[0] {
			require.Equal(t, domain.AttendanceStateRegistered, participant.Attendance)
		}
	}
}

func TestTournamentAdmissionConcurrentJoinsReserveOneFinalSlot(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })
	prepareTournamentCreateReceiptContent(ctx, t)

	fixture := newRepositoryFixture()
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	tournament, roster := createRepositoryTournament(ctx, t, fixture, createdAt)
	players := createMigrationPlayers(ctx, t, 5)
	for index, playerID := range players[:3] {
		addRepositoryParticipant(
			ctx,
			t,
			fixture,
			roster.ID,
			playerID,
			int32(index+1),
			domain.AttendanceStateRegistered,
			createdAt.Add(time.Duration(index+1)*time.Second),
		)
	}
	tournament = transitionRepositoryTournament(
		ctx,
		t,
		fixture,
		tournament,
		domain.TournamentStateRegistration,
		createdAt.Add(5*time.Second),
	)

	type result struct {
		changed bool
		err     error
	}
	results := make(chan result, 2)
	var group sync.WaitGroup
	for index, playerID := range players[3:5] {
		group.Add(1)
		go func(index int, playerID uuid.UUID) {
			defer group.Done()
			repository := admissionrepo.NewTournamentAdmissionPostgres(fixture.tx)
			_, changed, err := repository.Join(ctx, admissionusecase.JoinInput{
				TournamentID:  tournament.ID,
				PlayerID:      playerID,
				ParticipantID: uuid.New(),
				CommandID:     uuid.New(),
				JoinedAt:      createdAt.Add(time.Duration(6+index) * time.Second),
			})
			results <- result{changed: changed, err: err}
		}(index, playerID)
	}
	group.Wait()
	close(results)

	var admitted, full int
	for outcome := range results {
		if outcome.err == nil {
			require.True(t, outcome.changed)
			admitted++
			continue
		}
		require.ErrorIs(t, outcome.err, admissionusecase.ErrTournamentAdmissionFull)
		require.False(t, outcome.changed)
		full++
	}
	require.Equal(t, 1, admitted)
	require.Equal(t, 1, full)

	participants, err := fixture.roster.ListParticipants(ctx, roster.ID)
	require.NoError(t, err)
	require.Len(t, participants, 4)
	registered := 0
	for _, participant := range participants {
		if participant.Attendance == domain.AttendanceStateRegistered {
			registered++
		}
	}
	require.Equal(t, 4, registered)
}

func TestTournamentAdmissionDuplicateJoinAndCancelAreIdempotent(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })
	prepareTournamentCreateReceiptContent(ctx, t)

	fixture := newRepositoryFixture()
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	tournament, roster := createRepositoryTournament(ctx, t, fixture, createdAt)
	playerID := createMigrationPlayers(ctx, t, 1)[0]
	tournament = transitionRepositoryTournament(
		ctx, t, fixture, tournament, domain.TournamentStateRegistration, createdAt.Add(time.Second),
	)
	repository := admissionrepo.NewTournamentAdmissionPostgres(fixture.tx)

	first, changed, err := repository.Join(ctx, admissionusecase.JoinInput{
		TournamentID: tournament.ID, PlayerID: playerID, ParticipantID: uuid.New(), CommandID: uuid.New(),
		JoinedAt: createdAt.Add(2 * time.Second),
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.AttendanceStateRegistered, first.Attendance)

	second, changed, err := repository.Join(ctx, admissionusecase.JoinInput{
		TournamentID: tournament.ID, PlayerID: playerID, ParticipantID: uuid.New(), CommandID: uuid.New(),
		JoinedAt: createdAt.Add(3 * time.Second),
	})
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, first.ParticipantID, second.ParticipantID)
	require.Equal(t, 1, second.RosterSize)

	withdrawn, changed, err := repository.Cancel(ctx, admissionusecase.CancelInput{
		TournamentID: tournament.ID, PlayerID: playerID, CommandID: uuid.New(),
		CancelledAt: createdAt.Add(4 * time.Second),
	})
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.AttendanceStateWithdrawn, withdrawn.Attendance)

	repeated, changed, err := repository.Cancel(ctx, admissionusecase.CancelInput{
		TournamentID: tournament.ID, PlayerID: playerID, CommandID: uuid.New(),
		CancelledAt: createdAt.Add(5 * time.Second),
	})
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, withdrawn.ParticipantID, repeated.ParticipantID)
	require.Equal(t, domain.AttendanceStateWithdrawn, repeated.Attendance)

	participants, err := fixture.roster.ListParticipants(ctx, roster.ID)
	require.NoError(t, err)
	require.Len(t, participants, 1)
	require.Equal(t, domain.AttendanceStateWithdrawn, participants[0].Attendance)
}

func TestTournamentAdmissionHidesDraftAndRejectsClosedRoster(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })
	prepareTournamentCreateReceiptContent(ctx, t)

	fixture := newRepositoryFixture()
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	draft, _ := createRepositoryTournament(ctx, t, fixture, createdAt)
	playerID := createMigrationPlayers(ctx, t, 1)[0]
	repository := admissionrepo.NewTournamentAdmissionPostgres(fixture.tx)

	_, changed, err := repository.Join(ctx, admissionusecase.JoinInput{
		TournamentID: draft.ID, PlayerID: playerID, ParticipantID: uuid.New(), CommandID: uuid.New(),
		JoinedAt: createdAt.Add(time.Second),
	})
	require.ErrorIs(t, err, domain.ErrTournamentNotFound)
	require.False(t, changed)

	closed := transitionRepositoryTournament(
		ctx, t, fixture, draft, domain.TournamentStateRegistration, createdAt.Add(2*time.Second),
	)
	closed = transitionRepositoryTournament(
		ctx, t, fixture, closed, domain.TournamentStateRosterLocked, createdAt.Add(3*time.Second),
	)
	_, changed, err = repository.Join(ctx, admissionusecase.JoinInput{
		TournamentID: closed.ID, PlayerID: playerID, ParticipantID: uuid.New(), CommandID: uuid.New(),
		JoinedAt: createdAt.Add(4 * time.Second),
	})
	require.ErrorIs(t, err, admissionusecase.ErrTournamentAdmissionClosed)
	require.False(t, changed)
}

func TestTournamentAdmissionRejectsReservationConflictAcrossTournaments(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })
	prepareTournamentCreateReceiptContent(ctx, t)

	fixture := newRepositoryFixture()
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	firstTournament, firstRoster := createRepositoryTournament(ctx, t, fixture, createdAt)
	secondTournament, _ := createRepositoryTournament(ctx, t, fixture, createdAt.Add(time.Second))
	playerID := createMigrationPlayers(ctx, t, 1)[0]
	firstTournament = transitionRepositoryTournament(
		ctx, t, fixture, firstTournament, domain.TournamentStateRegistration, createdAt.Add(2*time.Second),
	)
	secondTournament = transitionRepositoryTournament(
		ctx, t, fixture, secondTournament, domain.TournamentStateRegistration, createdAt.Add(3*time.Second),
	)
	repository := admissionrepo.NewTournamentAdmissionPostgres(fixture.tx)
	joined, changed, err := repository.Join(ctx, admissionusecase.JoinInput{
		TournamentID: firstTournament.ID, PlayerID: playerID, ParticipantID: uuid.New(), CommandID: uuid.New(),
		JoinedAt: createdAt.Add(4 * time.Second),
	})
	require.NoError(t, err)
	require.True(t, changed)

	participant, err := fixture.roster.ListParticipants(ctx, firstRoster.ID)
	require.NoError(t, err)
	require.Len(t, participant, 1)
	updated, changed, err := fixture.roster.UpdateAttendance(
		ctx, participant[0].ID, domain.AttendanceStateRegistered, domain.AttendanceStateCheckedIn,
		createdAt.Add(5*time.Second),
	)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, joined.ParticipantID, updated.ID)
	firstRoster, err = fixture.roster.GetRoster(ctx, firstRoster.ID)
	require.NoError(t, err)
	_, changed, err = fixture.roster.LockRosterAndReserve(ctx, firstRoster.ID, firstRoster.Revision, createdAt.Add(6*time.Second))
	require.NoError(t, err)
	require.True(t, changed)

	_, changed, err = repository.Join(ctx, admissionusecase.JoinInput{
		TournamentID: secondTournament.ID, PlayerID: playerID, ParticipantID: uuid.New(), CommandID: uuid.New(),
		JoinedAt: createdAt.Add(7 * time.Second),
	})
	require.ErrorIs(t, err, admissionusecase.ErrTournamentAdmissionConflict)
	require.False(t, changed)
}
