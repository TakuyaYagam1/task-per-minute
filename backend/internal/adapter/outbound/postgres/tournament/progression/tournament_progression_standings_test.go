package progression

import (
	"crypto/sha256"
	"math/big"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestProgressionCurrentStandingsRetainsVerifiedPayload(t *testing.T) {
	authority := progressionReceiptChainAuthority(uuid.New(), uuid.New(), uuid.New(), 7)
	payload := []byte(`{"standings":[]}`)
	digest := sha256.Sum256(payload)
	row := sqlc.LockTournamentProgressionCurrentStandingsRow{
		ProjectionRevisionID: authority.ProjectionRevisionID, ProjectionRevision: authority.ProjectionRevision,
		ProjectionState: "published", ArtifactID: uuid.New(), Payload: payload, PayloadDigest: digest[:],
		ParticipantID: uuid.New(), Position: 1, Score: pgtype.Numeric{Int: big.NewInt(3000), Exp: -3, Valid: true},
	}
	reference, err := progressionCurrentStandings(authority, []sqlc.LockTournamentProgressionCurrentStandingsRow{row})
	require.NoError(t, err)
	require.Equal(t, payload, reference.Payload)
	reference.Payload[0] = '['
	require.Equal(t, byte('{'), row.Payload[0], "returned bytes must be owned by the snapshot")
	row.Payload = []byte(`{"standings":[1]}`)
	_, err = progressionCurrentStandings(authority, []sqlc.LockTournamentProgressionCurrentStandingsRow{row})
	require.ErrorIs(t, err, domain.ErrConflict)
}
