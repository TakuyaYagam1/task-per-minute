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
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestArenaCasualReservationRace(t *testing.T) {
	pool, _ := SetupTestDB(t)
	tx := postgres.NewTxManager(pool)
	players := postgres.NewPlayerPostgres(tx)
	tournaments := postgres.NewArenaTournamentPostgres(tx)
	ctx := context.Background()
	baseTime := time.Now().UTC().Truncate(time.Microsecond)

	for iteration := 0; iteration < 8; iteration++ {
		at := baseTime.Add(time.Duration(iteration) * time.Minute)
		player, err := players.Create(ctx, uniq("cross_mode"))
		require.NoError(t, err)
		tournament, roster, err := tournaments.Create(ctx, uuid.New(), uuid.New(), at)
		require.NoError(t, err)
		_, changed, err := tournaments.AddParticipant(ctx, postgres.ArenaParticipantInput{
			ID:         uuid.New(),
			RosterID:   roster.ID,
			PlayerID:   player.ID,
			Seed:       1,
			Attendance: domain.ArenaAttendanceStateCheckedIn,
			CreatedAt:  at,
		})
		require.NoError(t, err)
		require.True(t, changed)

		type arenaResult struct {
			roster  *postgres.ArenaRosterRecord
			changed bool
			err     error
		}
		type casualResult struct {
			reservation *domain.ParticipantReservation
			changed     bool
			err         error
		}
		var arenaOutcome arenaResult
		var casualOutcome casualResult
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			arenaOutcome.roster, arenaOutcome.changed, arenaOutcome.err = tournaments.LockRosterAndReserve(
				ctx,
				roster.ID,
				roster.Revision,
				at.Add(time.Second),
			)
		}()
		go func() {
			defer wg.Done()
			<-start
			casualOutcome.reservation, casualOutcome.changed, casualOutcome.err =
				players.AcquireParticipantReservation(
					ctx,
					player.ID,
					domain.ParticipantReservationOwnerCasualQueue,
					player.ID,
					at.Add(time.Second),
				)
		}()
		close(start)
		wg.Wait()

		require.NotEqual(t, arenaOutcome.changed, casualOutcome.changed)
		current, err := players.GetParticipantReservation(ctx, player.ID)
		require.NoError(t, err)
		require.NotNil(t, current)

		forged := *current
		forged.OwnerID = uuid.New()
		_, err = players.ReleaseParticipantReservation(ctx, forged)
		require.ErrorIs(t, err, domain.ErrPlayerReserved)
		stillOwned, err := players.GetParticipantReservation(ctx, player.ID)
		require.NoError(t, err)
		require.Equal(t, current.ReservationID, stillOwned.ReservationID)

		if arenaOutcome.changed {
			require.NoError(t, arenaOutcome.err)
			require.ErrorIs(t, casualOutcome.err, domain.ErrPlayerReserved)
			require.Equal(t, domain.ParticipantReservationOwnerArena, current.OwnerKind)
			require.Equal(t, tournament.ID, current.OwnerID)
			_, changed, err = tournaments.UnlockRosterAndRelease(
				ctx,
				roster.ID,
				arenaOutcome.roster.Revision,
				at.Add(2*time.Second),
			)
			require.NoError(t, err)
			require.True(t, changed)
		} else {
			require.ErrorIs(t, arenaOutcome.err, domain.ErrConflict)
			require.NoError(t, casualOutcome.err)
			require.Equal(t, domain.ParticipantReservationOwnerCasualQueue, current.OwnerKind)
			changed, err = players.ReleaseParticipantReservation(ctx, *casualOutcome.reservation)
			require.NoError(t, err)
			require.True(t, changed)
		}
		assertReservationMissing(t, ctx, players, player.ID)
	}
}
