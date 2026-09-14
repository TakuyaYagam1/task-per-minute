//go:build integration

package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
)

func TestParticipantConnectionAuthorityRecoverySQL(t *testing.T) {
	ctx := context.Background()
	resetMigrationTables(ctx, t)
	t.Cleanup(func() { resetMigrationTables(ctx, t) })

	tournamentID := createMigrationTournament(ctx, t)
	rosterID := createMigrationRoster(ctx, t, tournamentID)
	playerIDs := createMigrationPlayers(ctx, t, 2)
	participantIDs := createSwissMigrationParticipants(ctx, t, rosterID, playerIDs)
	participantID := participantIDs[0]
	playerID := playerIDs[0]

	now := time.Now().UTC().Truncate(time.Microsecond)
	oldExpires := now.Add(-time.Second)
	oldHolderID, oldLeaseID := uuid.New(), uuid.New()
	currentHolderID, currentLeaseID := uuid.New(), uuid.New()
	_, err := sharedPool.Exec(ctx, `
		INSERT INTO execution_authority_leases (
			tournament_id, command_id, holder_id, lease_id, epoch,
			process_kind, revision, acquired_at, renewed_at, expires_at, created_at
		)
		VALUES ($1, $2, $3, $4, 1, 'authority', 1, $5, $5, $6, $5)`,
		tournamentID, uuid.New(), oldHolderID, oldLeaseID,
		now.Add(-time.Minute), oldExpires)
	require.NoError(t, err)
	_, err = sharedPool.Exec(ctx, `
		INSERT INTO execution_authority_leases (
			tournament_id, command_id, holder_id, lease_id, epoch,
			process_kind, revision, previous_revision, previous_lease_id, previous_epoch,
			acquired_at, renewed_at, expires_at, created_at
		)
		VALUES ($1, $2, $3, $4, 2, 'authority', 2, 1, $5, 1, $6, $6, $7, $6)`,
		tournamentID, uuid.New(), currentHolderID, currentLeaseID,
		oldLeaseID, oldExpires, now.Add(time.Minute))
	require.NoError(t, err)

	stampedLeaseID := uuid.New()
	connectedAt := now.Add(-time.Minute)
	insertLease := func(id, connectionID uuid.UUID, holderID, leaseID *uuid.UUID, epoch *int64) error {
		_, insertErr := sharedPool.Exec(ctx, `
			INSERT INTO participant_connection_leases (
				id, tournament_id, roster_id, participant_id, player_id,
				connection_id, connection_generation, state, revision,
				connected_at, updated_at, authority_holder_id, authority_lease_id, authority_epoch
			)
			VALUES ($1, $2, $3, $4, $5, $6, 1, 'active', 1, $7, $7, $8, $9, $10)`,
			id, tournamentID, rosterID, participantID, playerID,
			connectionID, connectedAt, holderID, leaseID, epoch)
		return insertErr
	}
	oldEpoch := int64(1)
	currentEpoch := int64(2)
	require.NoError(t, insertLease(stampedLeaseID, uuid.New(), &oldHolderID, &oldLeaseID, &oldEpoch))
	require.Error(t, insertLease(uuid.New(), uuid.New(), nil, nil, nil),
		"ownerless leases must fail closed after the authority migration")

	partialErr := insertLease(uuid.New(), uuid.New(), &oldHolderID, nil, nil)
	require.Error(t, partialErr, "partial authority stamps must fail closed")

	queries := sqlc.New(sharedPool)
	tournaments, err := queries.ListParticipantConnectionLeaseTournaments(ctx)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{tournamentID}, tournaments)

	candidates, err := queries.ListParticipantConnectionRecoveryCandidates(ctx, 32)
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.Equal(t, stampedLeaseID, candidates[0].ID)
	require.Equal(t, oldHolderID, candidates[0].AuthorityHolderID.UUID)

	_, err = queries.CloseParticipantConnectionLease(ctx, sqlc.CloseParticipantConnectionLeaseParams{
		TournamentID: tournamentID, RosterID: rosterID,
		ParticipantID: participantID, PlayerID: playerID,
		ConnectionID: candidates[0].ConnectionID, ConnectionGeneration: 1,
		AuthorityHolderID: uuid.NullUUID{UUID: currentHolderID, Valid: true},
		AuthorityLeaseID:  uuid.NullUUID{UUID: currentLeaseID, Valid: true},
		AuthorityEpoch:    &currentEpoch,
	})
	require.ErrorIs(t, err, pgx.ErrNoRows, "current authority cannot close a lease stamped by a stale owner")

	_, err = queries.CloseExpiredParticipantConnectionLease(ctx, sqlc.CloseExpiredParticipantConnectionLeaseParams{
		TournamentID: tournamentID, RosterID: rosterID,
		ParticipantID: participantID, PlayerID: playerID,
		ID: stampedLeaseID, ExpectedRevision: 1,
		ConnectionID: candidates[0].ConnectionID, ConnectionGeneration: 1,
		AuthorityHolderID: oldHolderID, AuthorityLeaseID: oldLeaseID, AuthorityEpoch: 1,
		CurrentAuthorityHolderID: currentHolderID, CurrentAuthorityLeaseID: currentLeaseID, CurrentAuthorityEpoch: 2,
	})
	require.NoError(t, err)

	candidates, err = queries.ListParticipantConnectionRecoveryCandidates(ctx, 32)
	require.NoError(t, err)
	require.Empty(t, candidates)
}
