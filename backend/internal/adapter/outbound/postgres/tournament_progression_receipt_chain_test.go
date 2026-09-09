package postgres

import (
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func TestProgressionFinalSwissReceiptChainAcceptsInitialReceiptAtPhysicalRevisionAboveOne(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	rosterID := uuid.New()
	physicalRevisionID := uuid.New()
	authority := progressionReceiptChainAuthority(tournamentID, rosterID, physicalRevisionID, 7)
	rows := []sqlc.LockTournamentProgressionFinalSwissReceiptChainRow{
		progressionReceiptChainRow(tournamentID, rosterID, physicalRevisionID, 1, 7, uuid.Nil),
	}

	chain, err := progressionFinalSwissReceiptChain(authority, rows)

	require.NoError(t, err)
	require.Len(t, chain, 1)
	require.Equal(t, physicalRevisionID, chain[0].ProjectionRevisionID)
	require.EqualValues(t, 1, chain[0].ReceiptRevision)
	require.False(t, chain[0].PreviousReceiptProjectionRevisionID.Valid)
}

func TestProgressionFinalSwissReceiptChainRejectsSuccessorWithoutExactPredecessor(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	rosterID := uuid.New()
	predecessorID := uuid.New()
	currentID := uuid.New()
	authority := progressionReceiptChainAuthority(tournamentID, rosterID, currentID, 8)
	rows := []sqlc.LockTournamentProgressionFinalSwissReceiptChainRow{
		progressionReceiptChainRow(tournamentID, rosterID, currentID, 2, 8, predecessorID),
	}

	_, err := progressionFinalSwissReceiptChain(authority, rows)

	require.ErrorIs(t, err, domain.ErrConflict)
}

func TestProgressionFinalSwissReceiptChainAcceptsPhysicalPredecessorRevisionGap(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	rosterID := uuid.New()
	predecessorID := uuid.New()
	currentID := uuid.New()
	canonicalProjectionID := uuid.New()
	authority := progressionReceiptChainAuthority(tournamentID, rosterID, currentID, 9)
	predecessor := progressionReceiptChainRow(tournamentID, rosterID, predecessorID, 1, 4, uuid.Nil)
	predecessor.CanonicalProjectionID = canonicalProjectionID
	current := progressionReceiptChainRow(tournamentID, rosterID, currentID, 2, 9, predecessorID)
	current.CanonicalProjectionID = canonicalProjectionID

	chain, err := progressionFinalSwissReceiptChain(authority, []sqlc.LockTournamentProgressionFinalSwissReceiptChainRow{
		predecessor, current,
	})

	require.NoError(t, err)
	require.Len(t, chain, 2)
	require.EqualValues(t, 4, chain[0].PhysicalProjectionRevision)
	require.EqualValues(t, 9, chain[1].PhysicalProjectionRevision)
}

func TestProgressionFinalSwissReceiptChainRejectsArtifactPayloadDigestMismatch(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	rosterID := uuid.New()
	physicalRevisionID := uuid.New()
	authority := progressionReceiptChainAuthority(tournamentID, rosterID, physicalRevisionID, 7)
	row := progressionReceiptChainRow(tournamentID, rosterID, physicalRevisionID, 1, 7, uuid.Nil)
	otherDigest := sha256.Sum256([]byte("other-standings-artifact"))
	row.SourceStandingsArtifactDigest = otherDigest[:]

	_, err := progressionFinalSwissReceiptChain(authority, []sqlc.LockTournamentProgressionFinalSwissReceiptChainRow{row})

	require.ErrorIs(t, err, domain.ErrConflict)
}

func progressionReceiptChainAuthority(
	tournamentID, rosterID, projectionRevisionID uuid.UUID,
	projectionRevision int64,
) tournamentprogression.Authority {
	return tournamentprogression.Authority{
		Tournament: usecase.TournamentView{
			ID: tournamentID, RosterID: rosterID, Preset: domain.TournamentPresetV1,
			State: domain.TournamentStateSwiss, Revision: 3, RosterSize: domain.TournamentMinParticipants,
		},
		ProjectionRevisionID: projectionRevisionID,
		ProjectionRevision:   projectionRevision,
	}
}

func progressionReceiptChainRow(
	tournamentID, rosterID, projectionRevisionID uuid.UUID,
	receiptRevision int64,
	physicalProjectionRevision int64,
	predecessorID uuid.UUID,
) sqlc.LockTournamentProgressionFinalSwissReceiptChainRow {
	payload := []byte("receipt-standings-artifact")
	digest := sha256.Sum256(payload)
	row := sqlc.LockTournamentProgressionFinalSwissReceiptChainRow{
		ProjectionRevisionID:          projectionRevisionID,
		TournamentID:                  tournamentID,
		RosterID:                      rosterID,
		ReceiptRevision:               receiptRevision,
		CanonicalProjectionID:         uuid.New(),
		SourceStandingsArtifactID:     uuid.New(),
		SourceStandingsPayloadDigest:  digest[:],
		CanonicalPayloadDigest:        digest[:],
		CreatedAt:                     pgtype.Timestamptz{Time: time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC), Valid: true},
		PhysicalProjectionRevision:    physicalProjectionRevision,
		SourceStandingsPayload:        payload,
		SourceStandingsArtifactDigest: digest[:],
	}
	if predecessorID != uuid.Nil {
		row.PreviousReceiptProjectionRevisionID = uuid.NullUUID{UUID: predecessorID, Valid: true}
	}
	return row
}
