package wavestart

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	resultrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/result"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	progressionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/tournament/progression"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/start"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	progression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

// SwissRoundProofOrigin identifies the terminal action that admitted a
// pre-start Swiss proof.
type SwissRoundProofOrigin struct {
	Mode      string
	CommandID uuid.UUID
}

type waveStartSnapshot struct {
	authority        gameusecase.StartAuthority
	rosterID         uuid.UUID
	rosterRevision   int64
	games            []sqlc.LockWaveStartGamesRow
	seriesIDs        []uuid.UUID
	swissProof       *waveStartSwissRoundProof
	byeParticipantID *uuid.UUID
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

// EnsurePreStartSwissRoundProofForCommand adapts command fields to the proof
// origin used by result and recovery repositories.
func EnsurePreStartSwissRoundProofForCommand(
	ctx context.Context,
	tx *db.TxManager,
	tournamentID, seriesID uuid.UUID,
	at time.Time,
	mode string,
	commandID uuid.UUID,
) error {
	return EnsurePreStartSwissRoundProof(
		ctx,
		tx,
		tournamentID,
		seriesID,
		at,
		SwissRoundProofOrigin{Mode: mode, CommandID: commandID},
	)
}

// EnsurePreStartSwissRoundProof freezes the exact pairing and assignment
// authority before a terminal action can change it. It never starts a Wave.
func EnsurePreStartSwissRoundProof(
	ctx context.Context,
	tx *db.TxManager,
	tournamentID, seriesID uuid.UUID,
	at time.Time,
	origin SwissRoundProofOrigin,
) error {
	q := tx.Querier(ctx)
	waves, err := q.LockPreStartSwissSeriesWave(ctx, sqlc.LockPreStartSwissSeriesWaveParams{
		TournamentID: tournamentID,
		SeriesID:     seriesID,
	})
	if err != nil || len(waves) == 0 {
		return err
	}
	if len(waves) != 1 {
		return domain.ErrConflict
	}
	header, err := q.LockWaveStartAuthority(ctx, sqlc.LockWaveStartAuthorityParams{
		TournamentID: tournamentID,
		WaveID:       waves[0].WaveID,
	})
	if err != nil {
		return err
	}
	if header.TournamentState != string(domain.TournamentStateSwiss) || header.RosterID != waves[0].RosterID ||
		!header.SwissRoundID.Valid || header.StartedAt.Valid || header.RevisionID == uuid.Nil {
		return domain.ErrConflict
	}
	source, err := q.LockWaveStartSwissRoundProof(ctx, sqlc.LockWaveStartSwissRoundProofParams{
		TournamentID: tournamentID,
		RosterID:     header.RosterID,
		WaveID:       header.ID,
		RoundID:      header.SwissRoundID.UUID,
	})
	if err != nil {
		return err
	}
	mapped, err := waveStartSwissRoundProofFromRow(source)
	if err != nil {
		return err
	}
	mapped.retained, err = loadRetainedSwissRoundProof(ctx, q, tournamentID, header.RosterID, mapped)
	if err != nil {
		return err
	}
	if mapped.retained != nil &&
		(mapped.retained.SourceProjectionRevisionID != header.ProjectionRevisionID &&
			mapped.retained.Revisions.SourceProjection >= header.ProjectionRevision) {
		return domain.ErrConflict
	}
	members, err := q.LockWaveStartReadiness(ctx, sqlc.LockWaveStartReadinessParams{
		WaveID:        header.ID,
		ReadyWindowID: nullableUUIDValue(header.ReadyWindowID),
	})
	if err != nil {
		return err
	}
	membership, err := q.LockWaveStartSeriesMemberships(ctx, header.ID)
	if err != nil {
		return err
	}
	games, err := q.LockWaveStartGames(ctx, header.ID)
	if err != nil {
		return err
	}
	if len(games) == 0 || len(games) != len(membership) {
		return domain.ErrConflict
	}
	snapshot := waveStartSnapshot{
		rosterID: header.RosterID, rosterRevision: header.RosterRevision,
		games: games, swissProof: &mapped,
		byeParticipantID: nullableWaveStartUUID(header.ByeParticipantID),
	}
	for _, member := range members {
		snapshot.authority.Wave.Members = append(snapshot.authority.Wave.Members, domain.WaveMember{
			ParticipantID: member.ParticipantID,
		})
	}
	for index, game := range games {
		if game.SeriesID != membership[index].SeriesID || game.TournamentID != tournamentID ||
			game.RosterID != header.RosterID || game.SeriesFormat != string(domain.SeriesFormatBO1) ||
			game.SlotNumber != 1 ||
			(mapped.retained == nil && game.GameState != string(domain.GameStatePlanned) &&
				game.GameState != string(domain.GameStateReady)) {
			return domain.ErrConflict
		}
	}
	proof, err := buildSwissRoundLockProof(
		gameusecase.StartScope{TournamentID: tournamentID, WaveID: header.ID},
		domain.ReadyWindowSourceRevisions{
			ProjectionRevisionID: header.ProjectionRevisionID,
			ProjectionRevision:   header.ProjectionRevision,
			WaveRevisionID:       domain.WaveRevisionID(header.RevisionID),
		},
		header.Revision,
		snapshot,
	)
	if err != nil {
		return err
	}
	return EnsureSwissRoundLockProof(ctx, q, proof, at, origin)
}

// EnsureSwissRoundLockProof persists the immutable Swiss round lock proof and
// atomically advances the round lock revision.
func EnsureSwissRoundLockProof(
	ctx context.Context,
	querier *sqlc.Queries,
	proof swissusecase.RoundLockProof,
	lockedAt time.Time,
	origins ...SwissRoundProofOrigin,
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
		origin := origins[0]
		mode = &origin.Mode
		commandID = nullableUUIDValue(origin.CommandID)
	} else if len(origins) > 1 {
		return domain.ErrConflict
	}
	if err := querier.CreateSwissRoundLockProof(ctx, sqlc.CreateSwissRoundLockProofParams{
		RoundID: proof.RoundID, TournamentID: proof.TournamentID, RosterID: proof.RosterID,
		Preset: string(proof.Preset),
		//nolint:gosec // Domain validation bounds the persisted round number.
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

func loadRetainedSwissRoundProof(
	ctx context.Context,
	q *sqlc.Queries,
	tournamentID, rosterID uuid.UUID,
	source waveStartSwissRoundProof,
) (*swissusecase.RoundLockProof, error) {
	root, err := q.GetSwissRoundProofForUpdate(ctx, sqlc.GetSwissRoundProofForUpdateParams{
		RoundID: source.roundID, TournamentID: tournamentID, RosterID: rosterID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	members, err := q.ListSwissRoundProofMembers(ctx, sqlc.ListSwissRoundProofMembersParams{
		RoundID: source.roundID, RosterID: rosterID,
	})
	if err != nil {
		return nil, err
	}
	series, err := q.ListSwissRoundProofSeries(ctx, sqlc.ListSwissRoundProofSeriesParams{
		RoundID: source.roundID, RosterID: rosterID,
	})
	if err != nil {
		return nil, err
	}
	proofs, err := progressionrepo.ProgressionReceiptRoundProofs(
		progression.Authority{Tournament: inbound.TournamentView{
			ID: tournamentID, RosterID: rosterID, Preset: source.preset,
		}},
		[]sqlc.SwissRoundLockProof{root}, members, series,
	)
	if err != nil {
		return nil, err
	}
	proof := proofs[source.roundID]
	return &proof, nil
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
		NormalPoolRevisionID:       snapshot.swissProof.normalPoolRevisionID,
		WaveID:                     scope.WaveID, WaveRevisionID: revisions.WaveRevisionID,
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

func waveStartByeParticipantID(value *uuid.UUID) uuid.UUID {
	if value == nil {
		return uuid.Nil
	}
	return *value
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

func waveStartCASWriteError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	return mapRepositoryWriteError("TournamentAdminExecutionPostgres - "+operation, err)
}

func mapRepositoryWriteError(operation string, err error) error {
	return resultrepo.MapRepositoryWriteError(operation, err)
}

func nullableUUIDValue(value uuid.UUID) uuid.NullUUID {
	return uuid.NullUUID{UUID: value, Valid: value != uuid.Nil}
}

func tstz(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}
