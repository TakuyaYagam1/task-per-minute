package postgres

import (
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func TestProgressionReceiptRoundProofsRebuildsOnlyExactNormalizedBindings(t *testing.T) {
	t.Parallel()

	authority, roots, members, series, expected := progressionRoundProofFixture(t)

	proofs, err := progressionReceiptRoundProofs(authority, roots, members, series)
	require.NoError(t, err)
	require.Equal(t, expected, proofs[expected.RoundID])

	tamperedSeries := append([]sqlc.SwissRoundLockProofSeries(nil), series...)
	tamperedSeries[0].AssignmentRevision++
	_, err = progressionReceiptRoundProofs(authority, roots, members, tamperedSeries)
	require.ErrorIs(t, err, domain.ErrConflict)

	_, err = progressionReceiptRoundProofs(authority, roots, members[:len(members)-1], series)
	require.ErrorIs(t, err, domain.ErrConflict)
}

func progressionRoundProofFixture(
	t *testing.T,
) (
	authority tournamentprogression.Authority,
	roots []sqlc.SwissRoundLockProof,
	members []sqlc.SwissRoundLockProofMember,
	series []sqlc.SwissRoundLockProofSeries,
	expected swissusecase.RoundLockProof,
) {
	t.Helper()

	tournamentID := uuid.New()
	rosterID := uuid.New()
	roundID := uuid.New()
	waveID := uuid.New()
	participants := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	lockedSeries := []swissusecase.LockedSeries{
		{
			SeriesID: uuid.New(), PairingID: uuid.New(),
			FirstParticipantID: participants[0], SecondParticipantID: participants[1],
			CategoryRevisionID: uuid.New(), CategoryRevision: 2,
			AssignmentID: uuid.New(), AssignmentRevision: 2,
			AssignmentPlanID: uuid.New(), AssignmentPlanRevisionID: uuid.New(),
			ReservationID: uuid.New(), ReservationRevision: 2,
		},
		{
			SeriesID: uuid.New(), PairingID: uuid.New(),
			FirstParticipantID: participants[2], SecondParticipantID: participants[3],
			CategoryRevisionID: uuid.New(), CategoryRevision: 2,
			AssignmentID: uuid.New(), AssignmentRevision: 2,
			AssignmentPlanID: uuid.New(), AssignmentPlanRevisionID: uuid.New(),
			ReservationID: uuid.New(), ReservationRevision: 2,
		},
	}
	proofInput := swissusecase.RoundLockProofInput{
		TournamentID: tournamentID, RosterID: rosterID, RoundID: roundID,
		Preset: domain.TournamentPresetV1, RoundNumber: 1,
		SourceProjectionRevisionID: uuid.New(), PreflightRevisionID: uuid.New(),
		NormalPoolRevisionID: uuid.New(), WaveID: waveID,
		WaveRevisionID: domain.WaveRevisionID(uuid.New()),
		Revisions: swissusecase.RoundLockRevisions{
			Round: 2, SourceProjection: 2, Roster: 2, History: 1, NormalPool: 2, Wave: 2,
		},
		RosterParticipantIDs: participants, Series: lockedSeries,
	}
	proof, err := swissusecase.NewRoundLockProof(proofInput)
	require.NoError(t, err)
	proofHash, err := hex.DecodeString(proof.ProofHash)
	require.NoError(t, err)

	lockedAt := time.Date(2026, time.September, 7, 18, 0, 0, 0, time.UTC)
	timestamp := pgtype.Timestamptz{Time: lockedAt, Valid: true}
	authority = progressionReceiptChainAuthority(tournamentID, rosterID, uuid.New(), 1)
	roots = []sqlc.SwissRoundLockProof{{
		RoundID: roundID, TournamentID: tournamentID, RosterID: rosterID,
		Preset: string(domain.TournamentPresetV1), RoundNumber: 1,
		SourceProjectionRevisionID: proof.SourceProjectionRevisionID,
		PreflightRevisionID:        proof.PreflightRevisionID,
		NormalPoolRevisionID:       proof.NormalPoolRevisionID,
		WaveID:                     proof.WaveID, WaveRevisionID: proof.WaveRevisionID.UUID(),
		RoundRevision: proof.Revisions.Round, SourceProjectionRevision: proof.Revisions.SourceProjection,
		RosterRevision: proof.Revisions.Roster, HistoryRevision: proof.Revisions.History,
		NormalPoolRevision: proof.Revisions.NormalPool, WaveRevision: proof.Revisions.Wave,
		ProofHash: proofHash, LockedAt: timestamp, CreatedAt: timestamp,
	}}
	members = make([]sqlc.SwissRoundLockProofMember, len(participants))
	for index, participantID := range participants {
		members[index] = sqlc.SwissRoundLockProofMember{
			RoundID: roundID, RosterID: rosterID, ParticipantID: participantID, CreatedAt: timestamp,
		}
	}
	series = make([]sqlc.SwissRoundLockProofSeries, len(lockedSeries))
	for index, locked := range lockedSeries {
		series[index] = sqlc.SwissRoundLockProofSeries{
			RoundID: roundID, RosterID: rosterID,
			PairingID: locked.PairingID, SeriesID: locked.SeriesID,
			FirstParticipantID: locked.FirstParticipantID, SecondParticipantID: locked.SecondParticipantID,
			CategoryRevisionID: locked.CategoryRevisionID, CategoryRevision: locked.CategoryRevision,
			AssignmentID: locked.AssignmentID, AssignmentRevision: locked.AssignmentRevision,
			AssignmentPlanID: locked.AssignmentPlanID, AssignmentPlanRevisionID: locked.AssignmentPlanRevisionID,
			ReservationID: locked.ReservationID, ReservationRevision: locked.ReservationRevision, CreatedAt: timestamp,
		}
	}
	return authority, roots, members, series, proof
}
