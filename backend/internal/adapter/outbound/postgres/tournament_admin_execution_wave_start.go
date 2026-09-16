package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

// Wave-start core lives in tournament/admin/execution. These private root
// shapes and proof writers remain as compatibility bridges for the unmoved
// pre-start Swiss proof workflow.
type waveStartSnapshot struct {
	authority          gameusecase.StartAuthority
	rosterID           uuid.UUID
	tournamentRevision int64
	rosterRevision     int64
	games              []sqlc.LockWaveStartGamesRow
	seriesIDs          []uuid.UUID
	swissProof         *waveStartSwissRoundProof
	byeParticipantID   *uuid.UUID
	byeRevisionID      *uuid.UUID
	byeStableSeed      int32
}

type waveStartSwissRoundProof struct {
	preset               domain.TournamentPreset
	roundID              uuid.UUID
	roundNumber          int
	roundRevision        int64
	historyRevision      int64
	preflightRevisionID  uuid.UUID
	normalPoolRevisionID uuid.UUID
	normalPoolRevision   int64
	retained             *swissusecase.RoundLockProof
}

type swissRoundProofOrigin struct {
	mode      string
	commandID uuid.UUID
}

func (r *TournamentAdminExecutionPostgres) ReadWaveStartTime(ctx context.Context) (time.Time, error) {
	if r == nil || r.inner == nil {
		return time.Time{}, domain.ErrValidation
	}
	return r.inner.ReadWaveStartTime(ctx)
}

func (r *TournamentAdminExecutionPostgres) LoadWaveStartAuthority(
	ctx context.Context,
	scope gameusecase.StartScope,
) (gameusecase.StartAuthority, error) {
	if r == nil || r.inner == nil {
		return gameusecase.StartAuthority{}, domain.ErrValidation
	}
	return r.inner.LoadWaveStartAuthority(ctx, scope)
}

func (r *TournamentAdminExecutionPostgres) CommitWaveStart(
	ctx context.Context,
	record gameusecase.StartRecord,
) (*gameusecase.StartRecord, bool, error) {
	if r == nil || r.inner == nil {
		return nil, false, domain.ErrValidation
	}
	return r.inner.CommitWaveStart(ctx, record)
}

func waveStartSwissRoundProofFromRow(
	row sqlc.LockWaveStartSwissRoundProofRow,
) (waveStartSwissRoundProof, error) {
	if !row.PreflightRevisionID.Valid || row.PreflightRevisionID.UUID == uuid.Nil {
		return waveStartSwissRoundProof{}, gameusecase.ErrWaveStartAuthorityConflict
	}
	proof := waveStartSwissRoundProof{
		preset:               domain.TournamentPreset(row.Preset),
		roundID:              row.RoundID,
		roundNumber:          int(row.RoundNumber),
		roundRevision:        row.RoundRevision,
		historyRevision:      row.HistoryRevision,
		preflightRevisionID:  row.PreflightRevisionID.UUID,
		normalPoolRevisionID: row.NormalPoolRevisionID,
		normalPoolRevision:   row.NormalPoolRevision,
	}
	if !proof.preset.IsValid() || proof.roundID == uuid.Nil || proof.roundNumber < 1 ||
		proof.roundRevision < 1 || proof.historyRevision < 0 || proof.normalPoolRevisionID == uuid.Nil ||
		proof.normalPoolRevision < 1 {
		return waveStartSwissRoundProof{}, gameusecase.ErrWaveStartAuthorityConflict
	}
	return proof, nil
}

func nullableWaveStartUUID(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid || value.UUID == uuid.Nil {
		return nil
	}
	result := value.UUID
	return &result
}

func nullableWaveStartUUIDValue(value uuid.UUID) uuid.NullUUID {
	if value == uuid.Nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: value, Valid: true}
}

func decodeRoundLockProofHash(value string) ([]byte, error) {
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != sha256.Size {
		return nil, domain.ErrInternal
	}
	var zero [sha256.Size]byte
	if string(decoded) == string(zero[:]) {
		return nil, domain.ErrInternal
	}
	return decoded, nil
}

func waveStartByeParticipantID(value *uuid.UUID) uuid.UUID {
	if value == nil {
		return uuid.Nil
	}
	return *value
}

//nolint:gocyclo // Compatibility proof persistence keeps one atomic evidence boundary.
func ensureSwissRoundLockProof(
	ctx context.Context,
	querier *sqlc.Queries,
	proof swissusecase.RoundLockProof,
	lockedAt time.Time,
	origins ...swissRoundProofOrigin,
) error {
	if proof.Validate() != nil || !domain.IsValidServerTime(lockedAt) {
		return domain.ErrConflict
	}
	retained, err := loadRetainedSwissRoundProof(ctx, querier, proof.TournamentID, proof.RosterID,
		waveStartSwissRoundProof{roundID: proof.RoundID, preset: proof.Preset})
	if err != nil {
		return err
	}
	if retained != nil {
		if retained.ProofHash != proof.ProofHash {
			return domain.ErrConflict
		}
		return nil
	}
	proofHash, err := decodeRoundLockProofHash(proof.ProofHash)
	if err != nil {
		return err
	}
	createdAt := tstz(lockedAt)
	var mode *string
	var commandID uuid.NullUUID
	if len(origins) == 1 {
		mode = &origins[0].mode
		commandID = nullableUUIDValue(origins[0].commandID)
	} else if len(origins) > 1 {
		return domain.ErrConflict
	}
	if err := querier.CreateSwissRoundLockProof(ctx, sqlc.CreateSwissRoundLockProofParams{
		RoundID: proof.RoundID, TournamentID: proof.TournamentID, RosterID: proof.RosterID,
		Preset: string(proof.Preset),
		//nolint:gosec // Domain validation bounds this value before the storage conversion.
		RoundNumber: int16(proof.RoundNumber), SourceProjectionRevisionID: proof.SourceProjectionRevisionID,
		PreflightRevisionID: proof.PreflightRevisionID, NormalPoolRevisionID: proof.NormalPoolRevisionID,
		WaveID: proof.WaveID, WaveRevisionID: uuid.UUID(proof.WaveRevisionID), RoundRevision: proof.Revisions.Round,
		SourceProjectionRevision: proof.Revisions.SourceProjection, RosterRevision: proof.Revisions.Roster,
		HistoryRevision: proof.Revisions.History, NormalPoolRevision: proof.Revisions.NormalPool,
		WaveRevision: proof.Revisions.Wave, ByeParticipantID: nullableWaveStartUUIDValue(proof.ByeParticipantID),
		ProofHash: proofHash, ProofMode: mode, TerminalCommandID: commandID,
		LockedAt: createdAt, CreatedAt: createdAt,
	}); err != nil {
		return waveStartCASWriteError("persist Swiss round lock proof", err)
	}
	for _, participantID := range proof.RosterParticipantIDs {
		if err := querier.CreateSwissRoundLockProofMember(ctx, sqlc.CreateSwissRoundLockProofMemberParams{
			RoundID: proof.RoundID, RosterID: proof.RosterID, ParticipantID: participantID, CreatedAt: createdAt,
		}); err != nil {
			return waveStartCASWriteError("persist Swiss round lock proof member", err)
		}
	}
	for _, series := range proof.Series {
		if err := querier.CreateSwissRoundLockProofSeries(ctx, sqlc.CreateSwissRoundLockProofSeriesParams{
			RoundID: proof.RoundID, RosterID: proof.RosterID, PairingID: series.PairingID,
			SeriesID: series.SeriesID, FirstParticipantID: series.FirstParticipantID,
			SecondParticipantID: series.SecondParticipantID, CategoryRevisionID: series.CategoryRevisionID,
			CategoryRevision: series.CategoryRevision, AssignmentID: series.AssignmentID,
			AssignmentRevision: series.AssignmentRevision, AssignmentPlanID: series.AssignmentPlanID,
			AssignmentPlanRevisionID: series.AssignmentPlanRevisionID, ReservationID: series.ReservationID,
			ReservationRevision: series.ReservationRevision, CreatedAt: createdAt,
		}); err != nil {
			return waveStartCASWriteError("persist Swiss round lock proof Series", err)
		}
	}
	locked, err := querier.LockSwissRoundCAS(ctx, sqlc.LockSwissRoundCASParams{
		LockedAt: createdAt, ID: proof.RoundID, ExpectedRevision: proof.Revisions.Round,
	})
	if err != nil {
		return waveStartCASWriteError("lock Swiss round", err)
	}
	if locked.ID != proof.RoundID || locked.Revision != proof.Revisions.Round || locked.LockRevision == nil ||
		*locked.LockRevision != proof.Revisions.Round || !locked.LockedAt.Valid ||
		!locked.LockedAt.Time.UTC().Equal(lockedAt) {
		return domain.ErrInternal
	}
	return nil
}

func buildSwissRoundLockProof(
	scope gameusecase.StartScope,
	revisions domain.ReadyWindowSourceRevisions,
	waveRevision int64,
	snapshot waveStartSnapshot,
) (swissusecase.RoundLockProof, error) {
	lockedSeries := make([]swissusecase.LockedSeries, len(snapshot.games))
	for index, row := range snapshot.games {
		if row.PairingID == uuid.Nil || row.CategoryRevisionID == uuid.Nil || row.CategoryRevision < 1 ||
			row.AssignmentID == uuid.Nil || row.AssignmentRevision < 1 || row.AssignmentPlanID == uuid.Nil ||
			row.PlanRevisionID == uuid.Nil || row.ReservationID == uuid.Nil || row.ReservationRevision < 1 {
			return swissusecase.RoundLockProof{}, gameusecase.ErrWaveStartAuthorityConflict
		}
		lockedSeries[index] = swissusecase.LockedSeries{
			SeriesID: row.SeriesID, PairingID: row.PairingID, FirstParticipantID: row.FirstParticipantID,
			SecondParticipantID: row.SecondParticipantID, CategoryRevisionID: row.CategoryRevisionID,
			CategoryRevision: row.CategoryRevision, AssignmentID: row.AssignmentID,
			AssignmentRevision: row.AssignmentRevision, AssignmentPlanID: row.AssignmentPlanID,
			AssignmentPlanRevisionID: row.PlanRevisionID, ReservationID: row.ReservationID,
			ReservationRevision: row.ReservationRevision,
		}
	}
	participantIDs := make([]uuid.UUID, len(snapshot.authority.Wave.Members))
	for index, member := range snapshot.authority.Wave.Members {
		participantIDs[index] = member.ParticipantID
	}
	input := swissusecase.RoundLockProofInput{
		TournamentID: scope.TournamentID, RosterID: snapshot.rosterID, RoundID: snapshot.swissProof.roundID,
		Preset: snapshot.swissProof.preset, RoundNumber: snapshot.swissProof.roundNumber,
		SourceProjectionRevisionID: revisions.ProjectionRevisionID,
		PreflightRevisionID:        snapshot.swissProof.preflightRevisionID,
		NormalPoolRevisionID:       snapshot.swissProof.normalPoolRevisionID, WaveID: scope.WaveID,
		WaveRevisionID: revisions.WaveRevisionID,
		Revisions: swissusecase.RoundLockRevisions{
			SourceProjection: revisions.ProjectionRevision, Roster: snapshot.rosterRevision,
			Round: snapshot.swissProof.roundRevision, History: snapshot.swissProof.historyRevision,
			NormalPool: snapshot.swissProof.normalPoolRevision, Wave: waveRevision,
		},
		RosterParticipantIDs: participantIDs, Series: lockedSeries,
		ByeParticipantID: waveStartByeParticipantID(snapshot.byeParticipantID),
	}
	if retained := snapshot.swissProof.retained; retained != nil {
		input.SourceProjectionRevisionID = retained.SourceProjectionRevisionID
		input.Revisions.SourceProjection = retained.Revisions.SourceProjection
		input.WaveRevisionID, input.Revisions.Wave = retained.WaveRevisionID, retained.Revisions.Wave
	}
	proof, err := swissusecase.NewRoundLockProof(input)
	if err != nil {
		return swissusecase.RoundLockProof{}, gameusecase.ErrWaveStartAuthorityConflict
	}
	return proof, nil
}

func waveStartCASWriteError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return mapRepositoryWriteError("TournamentAdminExecutionPostgres - "+operation, err)
}
