package progression

import (
	"crypto/sha256"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestProgressionGoldenTerminalCommitPreservesSource(t *testing.T) {
	t.Parallel()
	fixture := newProgressionGoldenEvidenceFixture()
	row := fixture.ledgerRows[1]
	row.SubmissionID = nil
	row.TerminalEvidenceID = uuid.NullUUID{UUID: uuid.New(), Valid: true}
	row.TerminalPayloadDigest = append([]byte(nil), row.EvidenceDigest...)
	fixture.ledgerRows[1] = row
	settlements, err := progressionGoldenSettlements(
		fixture.authority, fixture.settlementRows, fixture.attemptRows, fixture.commitRows, fixture.ledgerRows, fixture.seals,
	)
	require.NoError(t, err)
	require.Len(t, settlements, 1)
	positions := settlements[0].Positions.Positions()
	require.Len(t, positions, 1)
	require.Equal(t, row.TerminalEvidenceID.UUID, positions[0].TerminalEvidenceID)
	require.Zero(t, positions[0].SubmissionID)
	require.Equal(t, [sha256.Size]byte(row.EvidenceDigest), positions[0].EvidenceDigest)
}

func TestProgressionGoldenTerminalCommitRejectsInvalidSource(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*sqlc.LockTournamentProgressionGoldenPositionLedgerRow)
	}{
		{name: "both sources", mutate: func(row *sqlc.LockTournamentProgressionGoldenPositionLedgerRow) {
			id := int64(1)
			row.SubmissionID = &id
		}},
		{name: "no source", mutate: func(row *sqlc.LockTournamentProgressionGoldenPositionLedgerRow) {
			row.TerminalEvidenceID = uuid.NullUUID{}
			row.TerminalPayloadDigest = nil
		}},
		{name: "zero terminal identity", mutate: func(row *sqlc.LockTournamentProgressionGoldenPositionLedgerRow) {
			row.TerminalEvidenceID.UUID = uuid.Nil
		}},
		{name: "missing digest", mutate: func(row *sqlc.LockTournamentProgressionGoldenPositionLedgerRow) {
			row.TerminalPayloadDigest = nil
		}},
		{name: "mismatched digest", mutate: func(row *sqlc.LockTournamentProgressionGoldenPositionLedgerRow) {
			row.TerminalPayloadDigest = make([]byte, sha256.Size)
			row.TerminalPayloadDigest[0] = 1
		}},
		{name: "orphan terminal digest", mutate: func(row *sqlc.LockTournamentProgressionGoldenPositionLedgerRow) {
			id := int64(1)
			row.SubmissionID = &id
			row.TerminalEvidenceID = uuid.NullUUID{}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			row := newProgressionGoldenEvidenceFixture().ledgerRows[1]
			row.SubmissionID = nil
			row.TerminalEvidenceID = uuid.NullUUID{UUID: uuid.New(), Valid: true}
			row.TerminalPayloadDigest = append([]byte(nil), row.EvidenceDigest...)
			test.mutate(&row)
			_, err := progressionGoldenLedgerRow(row)
			require.ErrorIs(t, err, domain.ErrConflict)
		})
	}
}

func TestProgressionGoldenLedgerRejectsOrphanTerminalEvidence(t *testing.T) {
	t.Parallel()
	for _, attemptPresent := range []bool{false, true} {
		fixture := newProgressionGoldenEvidenceFixture()
		row := fixture.ledgerRows[0]
		if attemptPresent {
			row = fixture.ledgerRows[1]
			zero := int16(0)
			row.OrderCount = &zero
			row.PositionCommitID = uuid.NullUUID{}
			row.ParticipantID = uuid.NullUUID{}
			row.Position = nil
			row.EvidenceDigest = nil
			row.SubmissionID = nil
		}
		row.TerminalEvidenceID = uuid.NullUUID{UUID: uuid.New(), Valid: true}
		row.TerminalPayloadDigest = fixture.ledgerRows[1].EvidenceDigest
		_, err := progressionGoldenLedgerRow(row)
		require.ErrorIs(t, err, domain.ErrConflict)
	}
}
