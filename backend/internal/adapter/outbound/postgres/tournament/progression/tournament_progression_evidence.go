package progression

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	projection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/canonical"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func (r *TournamentProgressionPostgres) LoadSwissEvidence(
	ctx context.Context,
	authority tournamentprogression.Authority,
) (tournamentprogression.SwissEvidence, error) {
	return r.loadSwissEvidence(ctx, authority, nil)
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func (r *TournamentProgressionPostgres) loadSwissEvidence(
	ctx context.Context,
	authority tournamentprogression.Authority,
	beforeProjection func(context.Context) error,
) (tournamentprogression.SwissEvidence, error) {
	if r == nil || r.tx == nil || ctx == nil || !validTournamentProgressionAuthority(authority) {
		return tournamentprogression.SwissEvidence{}, domain.ErrValidation
	}

	var evidence tournamentprogression.SwissEvidence
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		rounds, err := querier.LockTournamentProgressionSwissRounds(txCtx,
			sqlc.LockTournamentProgressionSwissRoundsParams{
				TournamentID: authority.Tournament.ID, RosterID: authority.Tournament.RosterID,
			},
		)
		if err != nil {
			return tournamentProgressionReadError("lock Swiss rounds", err)
		}
		proofs, err := querier.LockTournamentProgressionSwissRoundLockProofs(txCtx,
			sqlc.LockTournamentProgressionSwissRoundLockProofsParams{
				TournamentID: authority.Tournament.ID, RosterID: authority.Tournament.RosterID,
			},
		)
		if err != nil {
			return tournamentProgressionReadError("lock Swiss round proofs", err)
		}
		series, err := querier.LockTournamentProgressionSwissSeries(txCtx,
			sqlc.LockTournamentProgressionSwissSeriesParams{
				TournamentID: authority.Tournament.ID, RosterID: authority.Tournament.RosterID,
			},
		)
		if err != nil {
			return tournamentProgressionReadError("lock Swiss Series", err)
		}
		// Preserve the documented lock order before loading the current projection
		// artifact. The terminal reader consumes the same locked heads next.
		if _, err = querier.LockTournamentProgressionSwissResultHeads(txCtx,
			sqlc.LockTournamentProgressionSwissResultHeadsParams{
				TournamentID: authority.Tournament.ID, RosterID: authority.Tournament.RosterID,
			},
		); err != nil {
			return tournamentProgressionReadError("lock Swiss result heads", err)
		}
		if _, err = querier.LockTournamentProgressionSwissScoreHeads(txCtx,
			sqlc.LockTournamentProgressionSwissScoreHeadsParams{
				TournamentID: authority.Tournament.ID, RosterID: authority.Tournament.RosterID,
			},
		); err != nil {
			return tournamentProgressionReadError("lock Swiss score heads", err)
		}
		if _, err = querier.LockTournamentProgressionSwissGameHeads(txCtx,
			sqlc.LockTournamentProgressionSwissGameHeadsParams{
				TournamentID: authority.Tournament.ID, RosterID: authority.Tournament.RosterID,
			},
		); err != nil {
			return tournamentProgressionReadError("lock Swiss game heads", err)
		}
		if beforeProjection != nil {
			if err := beforeProjection(txCtx); err != nil {
				return err
			}
		}
		currentRows, err := querier.LockTournamentProgressionCurrentStandings(txCtx,
			sqlc.LockTournamentProgressionCurrentStandingsParams{
				ProjectionRevisionID: authority.ProjectionRevisionID,
				ProjectionRevision:   authority.ProjectionRevision,
				TournamentID:         authority.Tournament.ID,
				RosterID:             authority.Tournament.RosterID,
			},
		)
		if err != nil {
			return tournamentProgressionReadError("lock current standings", err)
		}
		current, err := progressionCurrentStandings(authority, currentRows)
		if err != nil {
			return fmt.Errorf("restore current Swiss standings: %w", err)
		}
		participants, err := querier.LockTournamentProgressionParticipants(txCtx, authority.Tournament.RosterID)
		if err != nil {
			return tournamentProgressionReadError("lock Swiss participants", err)
		}
		ledger, err := querier.LockTournamentProgressionSwissLedger(txCtx,
			sqlc.LockTournamentProgressionSwissLedgerParams{
				TournamentID: authority.Tournament.ID, RosterID: authority.Tournament.RosterID,
			},
		)
		if err != nil {
			return tournamentProgressionReadError("lock Swiss ledger", err)
		}
		canonical, err := progressionCanonicalSwissInput(
			authority.Tournament.ID,
			progressionRoundRevisionIDs(rounds, proofs),
			participants,
			ledger,
		)
		if err != nil {
			return fmt.Errorf("restore canonical Swiss input: %w", err)
		}
		expectedRounds, terminalRounds, expectedSeries, terminalSeries, err := progressionSwissCounts(rounds, series, proofs)
		if err != nil {
			return fmt.Errorf("restore Swiss counts: %w", err)
		}
		evidence = tournamentprogression.SwissEvidence{
			Current:        current,
			ExpectedRounds: expectedRounds,
			TerminalRounds: terminalRounds,
			ExpectedWaves:  expectedRounds,
			TerminalWaves:  terminalRounds,
			ExpectedSeries: expectedSeries,
			TerminalSeries: terminalSeries,
			Canonical:      canonical,
		}
		return nil
	})
	if err != nil {
		return tournamentprogression.SwissEvidence{}, err
	}
	return evidence, nil
}

func validTournamentProgressionAuthority(authority tournamentprogression.Authority) bool {
	return authority.Tournament.ID != uuid.Nil && authority.Tournament.RosterID != uuid.Nil &&
		authority.Tournament.Preset.IsValid() && authority.Tournament.Revision >= 1 &&
		authority.ProjectionRevisionID != uuid.Nil && authority.ProjectionRevision >= 1
}

// progressionFinalSwissReceiptChain validates the persisted canonical receipt
// ancestry in receipt order. Physical projection revisions may begin at any
// number; only the separate receipt counter is required to begin at one.
//
//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func progressionFinalSwissReceiptChain(
	authority tournamentprogression.Authority,
	rows []sqlc.LockTournamentProgressionFinalSwissReceiptChainRow,
) ([]sqlc.LockTournamentProgressionFinalSwissReceiptChainRow, error) {
	if !validTournamentProgressionAuthority(authority) || len(rows) == 0 {
		return nil, domain.ErrConflict
	}
	chain := append([]sqlc.LockTournamentProgressionFinalSwissReceiptChainRow(nil), rows...)
	var canonicalProjectionID uuid.UUID
	for index, row := range chain {
		if row.LineageCycle || row.ProjectionRevisionID == uuid.Nil || row.TournamentID != authority.Tournament.ID ||
			row.RosterID != authority.Tournament.RosterID || row.ReceiptRevision != int64(index+1) ||
			row.CanonicalProjectionID == uuid.Nil || row.SourceStandingsArtifactID == uuid.Nil ||
			row.PhysicalProjectionRevision < 1 || len(row.SourceStandingsPayload) == 0 ||
			!row.CreatedAt.Valid || !domain.IsValidServerTime(row.CreatedAt.Time.UTC()) {
			return nil, domain.ErrConflict
		}
		receiptDigest, ok := progressionDigest(row.SourceStandingsPayloadDigest)
		if !ok {
			return nil, domain.ErrConflict
		}
		artifactDigest, ok := progressionDigest(row.SourceStandingsArtifactDigest)
		if !ok || receiptDigest != artifactDigest || sha256.Sum256(row.SourceStandingsPayload) != receiptDigest {
			return nil, domain.ErrConflict
		}
		if _, ok := progressionDigest(row.CanonicalPayloadDigest); !ok {
			return nil, domain.ErrConflict
		}
		if index == 0 {
			if row.PreviousReceiptProjectionRevisionID.Valid {
				return nil, domain.ErrConflict
			}
			canonicalProjectionID = row.CanonicalProjectionID
			continue
		}
		if !row.PreviousReceiptProjectionRevisionID.Valid ||
			row.PreviousReceiptProjectionRevisionID.UUID != chain[index-1].ProjectionRevisionID ||
			row.CanonicalProjectionID != canonicalProjectionID ||
			row.PhysicalProjectionRevision <= chain[index-1].PhysicalProjectionRevision {
			return nil, domain.ErrConflict
		}
	}
	if chain[len(chain)-1].ProjectionRevisionID != authority.ProjectionRevisionID ||
		chain[len(chain)-1].PhysicalProjectionRevision != authority.ProjectionRevision {
		return nil, domain.ErrConflict
	}
	return chain, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func progressionCurrentStandings(
	authority tournamentprogression.Authority,
	rows []sqlc.LockTournamentProgressionCurrentStandingsRow,
) (tournamentprogression.ProjectionReference, error) {
	if len(rows) == 0 {
		return tournamentprogression.ProjectionReference{}, domain.ErrConflict
	}
	first := rows[0]
	digest, ok := progressionDigest(first.PayloadDigest)
	if !ok || first.ProjectionRevisionID != authority.ProjectionRevisionID ||
		first.ProjectionRevision != authority.ProjectionRevision || first.ProjectionState != "published" ||
		first.SupersededByRevisionID.Valid || first.ArtifactID == uuid.Nil || len(first.Payload) == 0 || sha256.Sum256(first.Payload) != digest {
		return tournamentprogression.ProjectionReference{}, domain.ErrConflict
	}
	members := make([]tournamentprogression.ProjectionMember, len(rows))
	seen := make(map[uuid.UUID]struct{}, len(rows))
	for index, row := range rows {
		if row.ProjectionRevisionID != first.ProjectionRevisionID || row.ProjectionRevision != first.ProjectionRevision ||
			row.ProjectionState != first.ProjectionState || row.ArtifactID != first.ArtifactID ||
			!progressionBytesEqual(row.PayloadDigest, first.PayloadDigest) || !progressionBytesEqual(row.Payload, first.Payload) || row.ParticipantID == uuid.Nil ||
			row.Position != int32(index+1) {
			return tournamentprogression.ProjectionReference{}, domain.ErrConflict
		}
		if _, duplicate := seen[row.ParticipantID]; duplicate {
			return tournamentprogression.ProjectionReference{}, domain.ErrConflict
		}
		seen[row.ParticipantID] = struct{}{}
		score, scoreErr := progressionScoreMilli(row.Score)
		if scoreErr != nil || score == nil || *score < 0 {
			return tournamentprogression.ProjectionReference{}, domain.ErrConflict
		}
		members[index] = tournamentprogression.ProjectionMember{
			ParticipantID: row.ParticipantID, Position: int(row.Position), ScoreMilli: score,
		}
	}
	return tournamentprogression.ProjectionReference{
		ArtifactID: first.ArtifactID, RevisionID: first.ProjectionRevisionID,
		Revision: first.ProjectionRevision, Kind: domain.ArtifactKindStandings,
		Digest: digest, Payload: append([]byte(nil), first.Payload...), Members: members,
	}, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func progressionSwissCounts(
	rounds []sqlc.LockTournamentProgressionSwissRoundsRow,
	series []sqlc.LockTournamentProgressionSwissSeriesRow,
	proofs []sqlc.SwissRoundLockProof,
) (int, int, int, int, error) {
	if len(rounds) == 0 || len(series) == 0 {
		return 0, 0, 0, 0, domain.ErrConflict
	}
	roundIDs := make(map[uuid.UUID]struct{}, len(rounds))
	terminalRounds := 0
	for index, round := range rounds {
		if round.ID == uuid.Nil || round.RoundNumber != int16(index+1) || round.Revision < 1 ||
			round.LockRevision == nil || *round.LockRevision < 1 || !round.LockedAt.Valid ||
			!domain.IsValidServerTime(round.LockedAt.Time.UTC()) || round.WaveID == uuid.Nil ||
			round.WaveRevisionID == uuid.Nil || round.WaveRevision < 1 {
			return 0, 0, 0, 0, domain.ErrConflict
		}
		if _, duplicate := roundIDs[round.ID]; duplicate {
			return 0, 0, 0, 0, domain.ErrConflict
		}
		roundIDs[round.ID] = struct{}{}
	}
	terminalSeries := 0
	seriesByRound := make(map[uuid.UUID]int, len(rounds))
	terminalByRound := make(map[uuid.UUID]int, len(rounds))
	seenSeries := make(map[uuid.UUID]struct{}, len(series))
	for _, value := range series {
		if value.RoundID == uuid.Nil || value.SeriesID == uuid.Nil ||
			value.FirstParticipantID == uuid.Nil || value.SecondParticipantID == uuid.Nil ||
			value.FirstParticipantID == value.SecondParticipantID || !domain.SeriesFormat(value.Format).IsValid() ||
			!domain.SeriesState(value.State).IsValid() || !value.CurrentScoreRevisionID.Valid ||
			!value.CurrentResultRevisionID.Valid || !value.ResultRevisionID.Valid ||
			value.CurrentResultRevisionID.UUID != value.ResultRevisionID.UUID ||
			!roundIDsContains(roundIDs, value.RoundID) {
			return 0, 0, 0, 0, domain.ErrConflict
		}
		if _, duplicate := seenSeries[value.SeriesID]; duplicate {
			return 0, 0, 0, 0, domain.ErrConflict
		}
		seenSeries[value.SeriesID] = struct{}{}
		seriesByRound[value.RoundID]++
		if domain.SeriesState(value.State).IsTerminal() {
			terminalSeries++
			terminalByRound[value.RoundID]++
		}
	}
	terminalProofs := make(map[uuid.UUID]bool, len(proofs))
	waveByRound := make(map[uuid.UUID]uuid.UUID, len(rounds))
	for _, round := range rounds {
		waveByRound[round.ID] = round.WaveID
	}
	for _, proof := range proofs {
		if proof.WaveID == uuid.Nil || waveByRound[proof.RoundID] != proof.WaveID {
			return 0, 0, 0, 0, domain.ErrConflict
		}
		if _, duplicate := terminalProofs[proof.RoundID]; duplicate {
			return 0, 0, 0, 0, domain.ErrConflict
		}
		terminalProofs[proof.RoundID] = proof.ProofMode == "wave_start" || (proof.TerminalCommandID.Valid &&
			(proof.ProofMode == "pre_start_forfeit" || proof.ProofMode == "normal_no_show"))
	}
	for _, round := range rounds {
		if seriesByRound[round.ID] > 0 && terminalByRound[round.ID] == seriesByRound[round.ID] &&
			((round.WaveState == "completed" && round.WaveClosedAt.Valid) || terminalProofs[round.ID]) {
			terminalRounds++
		}
	}
	return len(rounds), terminalRounds, len(series), terminalSeries, nil
}

func progressionRoundRevisionIDs(
	rounds []sqlc.LockTournamentProgressionSwissRoundsRow,
	proofs []sqlc.SwissRoundLockProof,
) map[uuid.UUID]uuid.UUID {
	proofByRound := make(map[uuid.UUID]sqlc.SwissRoundLockProof, len(proofs))
	for _, proof := range proofs {
		proofByRound[proof.RoundID] = proof
	}
	result := make(map[uuid.UUID]uuid.UUID, len(rounds))
	for _, round := range rounds {
		proof, found := proofByRound[round.ID]
		if !found || proof.SourceProjectionRevisionID == uuid.Nil {
			return nil
		}
		result[round.ID] = proof.SourceProjectionRevisionID
	}
	return result
}

func roundIDsContains(values map[uuid.UUID]struct{}, value uuid.UUID) bool {
	_, found := values[value]
	return found
}

func progressionCanonicalSwissInput(
	tournamentID uuid.UUID,
	roundRevisionIDs map[uuid.UUID]uuid.UUID,
	participants []sqlc.LockTournamentProgressionParticipantsRow,
	ledger []sqlc.LockTournamentProgressionSwissLedgerRow,
) (projection.CanonicalMaterializationInput, error) {
	if tournamentID == uuid.Nil || len(roundRevisionIDs) == 0 || len(participants) == 0 || len(ledger) == 0 {
		return projection.CanonicalMaterializationInput{}, domain.ErrConflict
	}
	canonicalParticipants := make([]projection.CanonicalSwissParticipant, len(participants))
	seenParticipants := make(map[uuid.UUID]struct{}, len(participants))
	for index, participant := range participants {
		if participant.ID == uuid.Nil || participant.Seed < 1 {
			return projection.CanonicalMaterializationInput{}, domain.ErrConflict
		}
		if _, duplicate := seenParticipants[participant.ID]; duplicate {
			return projection.CanonicalMaterializationInput{}, domain.ErrConflict
		}
		seenParticipants[participant.ID] = struct{}{}
		canonicalParticipants[index] = projection.CanonicalSwissParticipant{
			ID: participant.ID, StableSeed: int(participant.Seed),
		}
	}
	canonicalLedger := make([]projection.CanonicalSwissPointLedgerEntry, len(ledger))
	for index, row := range ledger {
		entry, err := progressionCanonicalSwissLedgerEntry(row, roundRevisionIDs[row.RoundID])
		if err != nil {
			return projection.CanonicalMaterializationInput{}, err
		}
		canonicalLedger[index] = entry
	}
	return projection.CanonicalMaterializationInput{
		TournamentID: tournamentID, Participants: canonicalParticipants, SwissLedger: canonicalLedger,
		SwissComplete: true, ArtifactKinds: []domain.ArtifactKind{domain.ArtifactKindStandings},
	}, nil
}

func progressionCanonicalSwissLedgerEntry(
	row sqlc.LockTournamentProgressionSwissLedgerRow,
	roundRevisionID uuid.UUID,
) (projection.CanonicalSwissPointLedgerEntry, error) {
	if row.ID == uuid.Nil || row.RoundID == uuid.Nil || row.RoundNumber < 1 || row.ParticipantID == uuid.Nil ||
		roundRevisionID == uuid.Nil || row.StableSeed < 1 || row.Points < 0 || row.EffectiveTimeNs < 0 {
		return projection.CanonicalSwissPointLedgerEntry{}, domain.ErrConflict
	}
	entry := projection.CanonicalSwissPointLedgerEntry{
		RoundID: row.RoundID, RoundRevisionID: roundRevisionID, RoundNumber: int(row.RoundNumber),
		SourceKind: swissusecase.PointSourceKind(row.SourceKind), ResultLabel: swissusecase.SeriesResultLabel(stringValue(row.ResultLabel)),
		ParticipantID: row.ParticipantID, Points: int(row.Points), EffectiveTime: time.Duration(row.EffectiveTimeNs),
		StableSeed: int(row.StableSeed),
	}
	if row.SourceSeriesID.Valid {
		entry.SourceSeriesID = row.SourceSeriesID.UUID
	}
	if row.SeriesResultRevisionID.Valid {
		entry.SeriesResultRevisionID = row.SeriesResultRevisionID.UUID
	}
	if row.ByeRevisionID.Valid {
		entry.ByeRevisionID = row.ByeRevisionID.UUID
	}
	if row.OpponentID.Valid {
		value := row.OpponentID.UUID
		entry.OpponentID = &value
	}
	if row.AcceptedSolveTimeNs != nil {
		value := time.Duration(*row.AcceptedSolveTimeNs)
		entry.AcceptedSolveTime = &value
	}
	return entry, nil
}

func progressionDigest(value []byte) ([sha256.Size]byte, bool) {
	if len(value) != sha256.Size {
		return [sha256.Size]byte{}, false
	}
	var digest [sha256.Size]byte
	copy(digest[:], value)
	return digest, true
}

func progressionBytesEqual(left, right []byte) bool {
	return len(left) == len(right) && string(left) == string(right)
}

func progressionScoreMilli(value pgtype.Numeric) (*int64, error) {
	if !value.Valid || value.Int == nil || value.NaN || value.InfinityModifier != pgtype.Finite {
		return nil, fmt.Errorf("invalid projection score")
	}
	// PostgreSQL may normalize the exponent, notably for zero. Convert the
	// exact numeric value to milli-points, rejecting rounding and overflow.
	scaled := new(big.Int).Set(value.Int)
	exponent := int64(value.Exp) + 3
	if scaled.Sign() == 0 {
		result := int64(0)
		return &result, nil
	}
	if exponent < -18 || exponent > 18 {
		return nil, fmt.Errorf("invalid projection score scale")
	}
	if exponent > 0 {
		scaled.Mul(scaled, new(big.Int).Exp(big.NewInt(10), big.NewInt(exponent), nil))
	} else if exponent < 0 {
		var remainder big.Int
		scaled.QuoRem(scaled, new(big.Int).Exp(big.NewInt(10), big.NewInt(-exponent), nil), &remainder)
		if remainder.Sign() != 0 {
			return nil, fmt.Errorf("fractional projection milli-score")
		}
	}
	if !scaled.IsInt64() {
		return nil, fmt.Errorf("projection score overflow")
	}
	result := scaled.Int64()
	return &result, nil
}

func tournamentProgressionReadError(operation string, err error) error {
	return fmt.Errorf("TournamentProgressionPostgres - %s: %w", operation, err)
}
