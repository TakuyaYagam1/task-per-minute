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

func TestProgressionGoldenEvidenceBuildsOnlySealedCompleteSettlement(t *testing.T) {
	t.Parallel()

	fixture := newProgressionGoldenEvidenceFixture()
	settlements, err := progressionGoldenSettlements(
		fixture.authority,
		fixture.settlementRows,
		fixture.attemptRows,
		fixture.commitRows,
		fixture.ledgerRows,
		fixture.seals,
	)

	require.NoError(t, err)
	require.Len(t, settlements, 1)
	require.Equal(t, domain.DerivedRevisionID(fixture.finalLedgerID), settlements[0].RevisionID)
	require.Equal(t, 2, settlements[0].RevisionNo)
	require.Equal(t, fixture.finalizedAt, settlements[0].FinalizedAt)
	require.NotNil(t, settlements[0].Positions)
	require.Equal(t, fixture.commitID, settlements[0].Positions.Positions()[0].CommitID)
	require.Equal(t, fixture.participantID, settlements[0].Positions.Positions()[0].ParticipantID)
}

func TestProgressionGoldenEvidenceOrdersSettlementsByPosition(t *testing.T) {
	t.Parallel()

	first := newProgressionGoldenEvidenceFixture()
	second := newProgressionGoldenEvidenceFixture()
	firstRevisionID := uuid.MustParse("ffffffff-ffff-ffff-ffff-ffffffffffff")
	secondRevisionID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	remapProgressionGoldenFixture(&first, first.authority, firstRevisionID, 1)
	remapProgressionGoldenFixture(&second, first.authority, secondRevisionID, 3)

	settlements, err := progressionGoldenSettlements(
		first.authority,
		append(first.settlementRows, second.settlementRows...),
		append(first.attemptRows, second.attemptRows...),
		append(first.commitRows, second.commitRows...),
		append(first.ledgerRows, second.ledgerRows...),
		append(first.seals, second.seals...),
	)

	require.NoError(t, err)
	require.Len(t, settlements, 2)
	require.Equal(t, 1, settlements[0].Positions.Positions()[0].Position)
	require.Equal(t, 3, settlements[1].Positions.Positions()[0].Position)
}

func remapProgressionGoldenFixture(
	fixture *progressionGoldenEvidenceFixture,
	authority tournamentprogression.Authority,
	groupRevisionID uuid.UUID,
	position int16,
) {
	fixture.authority = authority
	fixture.settlementRows[0].SourceProjectionRevisionID = authority.ProjectionRevisionID
	fixture.settlementRows[0].SourceProjectionRevision = authority.ProjectionRevision
	fixture.settlementRows[0].GroupRevisionID = groupRevisionID
	fixture.settlementRows[0].PositionFrom = position
	fixture.settlementRows[0].PositionTo = position
	fixture.settlementRows[0].Position = &position
	fixture.attemptRows[0].GroupRevisionID = groupRevisionID
	fixture.commitRows[0].GroupRevisionID = groupRevisionID
	fixture.commitRows[0].Position = position
	for index := range fixture.ledgerRows {
		fixture.ledgerRows[index].GroupRevisionID = groupRevisionID
		if fixture.ledgerRows[index].Position != nil {
			fixture.ledgerRows[index].Position = &position
		}
	}
	for index := range fixture.seals {
		fixture.seals[index].TournamentID = authority.Tournament.ID
		fixture.seals[index].RosterID = authority.Tournament.RosterID
	}
}

func TestProgressionGoldenEvidenceRejectsMissingOrTamperedSettlementAuthority(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		mutate func(*progressionGoldenEvidenceFixture)
	}{
		{
			name: "missing final ledger seal",
			mutate: func(fixture *progressionGoldenEvidenceFixture) {
				fixture.seals = fixture.seals[:1]
			},
		},
		{
			name: "spliced position commit",
			mutate: func(fixture *progressionGoldenEvidenceFixture) {
				fixture.commitRows[0].PositionCommitID = uuid.New()
			},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fixture := newProgressionGoldenEvidenceFixture()
			test.mutate(&fixture)
			_, err := progressionGoldenSettlements(
				fixture.authority,
				fixture.settlementRows,
				fixture.attemptRows,
				fixture.commitRows,
				fixture.ledgerRows,
				fixture.seals,
			)

			require.ErrorIs(t, err, domain.ErrConflict)
		})
	}
}

type progressionGoldenEvidenceFixture struct {
	authority      tournamentprogression.Authority
	settlementRows []sqlc.LockTournamentProgressionGoldenSettlementsRow
	attemptRows    []sqlc.LockTournamentProgressionGoldenAttemptsRow
	commitRows     []sqlc.LockTournamentProgressionGoldenPositionCommitsRow
	ledgerRows     []sqlc.LockTournamentProgressionGoldenPositionLedgerRow
	seals          []sqlc.GoldenPositionLedgerRevisionSeal

	finalLedgerID uuid.UUID
	commitID      uuid.UUID
	participantID uuid.UUID
	finalizedAt   time.Time
}

func newProgressionGoldenEvidenceFixture() progressionGoldenEvidenceFixture {
	tournamentID := uuid.New()
	rosterID := uuid.New()
	projectionID := uuid.New()
	groupID := uuid.New()
	groupRevisionID := uuid.New()
	initialLedgerID := uuid.New()
	finalLedgerID := uuid.New()
	attemptID := uuid.New()
	commitID := uuid.New()
	participantID := uuid.New()
	finalizedAt := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)
	ledgerDigest := sha256.Sum256([]byte("complete Golden ledger"))
	evidenceDigest := sha256.Sum256([]byte("committed Golden position"))
	submissionRevision := int64(1)
	attemptNumber := int32(1)
	orderCount := int16(1)
	position := int16(1)
	submissionID := int64(1)

	return progressionGoldenEvidenceFixture{
		authority: tournamentprogression.Authority{
			Tournament: usecase.TournamentView{
				ID: tournamentID, RosterID: rosterID, Revision: 3,
				Preset: domain.TournamentPresetV1, State: domain.TournamentStateGolden,
			},
			ProjectionRevisionID: projectionID,
			ProjectionRevision:   5,
		},
		settlementRows: []sqlc.LockTournamentProgressionGoldenSettlementsRow{{
			GroupRevisionNumber: 1,
			GroupRevisionID:     groupRevisionID, GroupID: groupID,
			SourceProjectionRevisionID: projectionID, SourceProjectionRevision: 5,
			PositionFrom: 1, PositionTo: 1,
			AttemptID:        uuid.NullUUID{UUID: attemptID, Valid: true},
			AttemptState:     stringPointer("completed"),
			PositionCommitID: uuid.NullUUID{UUID: commitID, Valid: true},
			ParticipantID:    uuid.NullUUID{UUID: participantID, Valid: true}, Position: &position,
		}},
		attemptRows: []sqlc.LockTournamentProgressionGoldenAttemptsRow{{
			GroupRevisionID: groupRevisionID, AttemptID: attemptID, AttemptState: "completed",
		}},
		commitRows: []sqlc.LockTournamentProgressionGoldenPositionCommitsRow{{
			GroupRevisionID: groupRevisionID, PositionCommitID: commitID,
			AttemptID: attemptID, ParticipantID: participantID, Position: position,
		}},
		ledgerRows: []sqlc.LockTournamentProgressionGoldenPositionLedgerRow{
			{
				LedgerRevisionID: initialLedgerID, GroupRevisionID: groupRevisionID, RevisionNumber: 1,
				PayloadDigest: ledgerDigest[:], FinalizedAt: pgtype.Timestamptz{Time: finalizedAt, Valid: true},
			},
			{
				LedgerRevisionID: finalLedgerID, GroupRevisionID: groupRevisionID, RevisionNumber: 2,
				PreviousRevisionID: uuid.NullUUID{UUID: initialLedgerID, Valid: true},
				PayloadDigest:      ledgerDigest[:], FinalizedAt: pgtype.Timestamptz{Time: finalizedAt, Valid: true},
				AttemptID:            uuid.NullUUID{UUID: attemptID, Valid: true},
				SubmissionRevisionID: uuid.NullUUID{UUID: uuid.New(), Valid: true}, SubmissionRevision: &submissionRevision,
				AttemptNumber: &attemptNumber, OrderCount: &orderCount,
				WaveID:           uuid.New(),
				AssignmentID:     uuid.New(),
				SnapshotID:       uuid.New(),
				TaskID:           uuid.New(),
				PositionCommitID: uuid.NullUUID{UUID: commitID, Valid: true},
				ParticipantID:    uuid.NullUUID{UUID: participantID, Valid: true}, Position: &position,
				EvidenceDigest: evidenceDigest[:], SubmissionID: &submissionID,
			},
		},
		seals: []sqlc.GoldenPositionLedgerRevisionSeal{
			{LedgerRevisionID: initialLedgerID, TournamentID: tournamentID, RosterID: rosterID, PayloadDigest: ledgerDigest[:], SealedAt: pgtype.Timestamptz{Time: finalizedAt, Valid: true}},
			{LedgerRevisionID: finalLedgerID, TournamentID: tournamentID, RosterID: rosterID, PayloadDigest: ledgerDigest[:], SealedAt: pgtype.Timestamptz{Time: finalizedAt, Valid: true}},
		},
		finalLedgerID: finalLedgerID,
		commitID:      commitID,
		participantID: participantID,
		finalizedAt:   finalizedAt,
	}
}
