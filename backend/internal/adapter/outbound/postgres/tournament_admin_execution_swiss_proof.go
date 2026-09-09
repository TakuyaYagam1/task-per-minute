package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	progression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

func loadRetainedSwissRoundProof(ctx context.Context, q *sqlc.Queries, tournamentID, rosterID uuid.UUID, source waveStartSwissRoundProof) (*swissusecase.RoundLockProof, error) {
	root, err := q.GetSwissRoundProofForUpdate(ctx, sqlc.GetSwissRoundProofForUpdateParams{RoundID: source.roundID, TournamentID: tournamentID, RosterID: rosterID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	members, err := q.ListSwissRoundProofMembers(ctx, sqlc.ListSwissRoundProofMembersParams{RoundID: source.roundID, RosterID: rosterID})
	if err != nil {
		return nil, err
	}
	series, err := q.ListSwissRoundProofSeries(ctx, sqlc.ListSwissRoundProofSeriesParams{RoundID: source.roundID, RosterID: rosterID})
	if err != nil {
		return nil, err
	}
	proofs, err := progressionReceiptRoundProofs(progression.Authority{Tournament: inbound.TournamentView{ID: tournamentID, RosterID: rosterID, Preset: source.preset}}, []sqlc.SwissRoundLockProof{root}, members, series)
	if err != nil {
		return nil, err
	}
	proof := proofs[source.roundID]
	return &proof, nil
}

// ensurePreStartSwissRoundProof freezes the exact pairing and assignment
// authority before a terminal action can change it. It never starts a Wave.
func ensurePreStartSwissRoundProof(ctx context.Context, tx *TxManager, tournamentID, seriesID uuid.UUID, at time.Time, origin swissRoundProofOrigin) error {
	q := tx.Querier(ctx)
	waves, err := q.LockPreStartSwissSeriesWave(ctx, sqlc.LockPreStartSwissSeriesWaveParams{TournamentID: tournamentID, SeriesID: seriesID})
	if err != nil || len(waves) == 0 {
		return err
	}
	if len(waves) != 1 {
		return domain.ErrConflict
	}
	header, err := q.LockWaveStartAuthority(ctx, sqlc.LockWaveStartAuthorityParams{TournamentID: tournamentID, WaveID: waves[0].WaveID})
	if err != nil {
		return err
	}
	if header.TournamentState != string(domain.TournamentStateSwiss) || header.RosterID != waves[0].RosterID ||
		!header.SwissRoundID.Valid || header.StartedAt.Valid || header.RevisionID == uuid.Nil {
		return domain.ErrConflict
	}
	source, err := q.LockWaveStartSwissRoundProof(ctx, sqlc.LockWaveStartSwissRoundProofParams{TournamentID: tournamentID, RosterID: header.RosterID, WaveID: header.ID, RoundID: header.SwissRoundID.UUID})
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
	if mapped.retained != nil && (mapped.retained.SourceProjectionRevisionID != header.ProjectionRevisionID && mapped.retained.Revisions.SourceProjection >= header.ProjectionRevision) {
		return domain.ErrConflict
	}
	members, err := q.LockWaveStartReadiness(ctx, sqlc.LockWaveStartReadinessParams{WaveID: header.ID, ReadyWindowID: nullableUUIDValue(header.ReadyWindowID)})
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
	snapshot := waveStartSnapshot{rosterID: header.RosterID, rosterRevision: header.RosterRevision, games: games, swissProof: &mapped, byeParticipantID: nullableWaveStartUUID(header.ByeParticipantID)}
	for _, member := range members {
		snapshot.authority.Wave.Members = append(snapshot.authority.Wave.Members, domain.WaveMember{ParticipantID: member.ParticipantID})
	}
	for index, game := range games {
		if game.SeriesID != membership[index].SeriesID || game.TournamentID != tournamentID || game.RosterID != header.RosterID ||
			game.SeriesFormat != string(domain.SeriesFormatBO1) || game.SlotNumber != 1 ||
			(mapped.retained == nil && game.GameState != string(domain.GameStatePlanned) && game.GameState != string(domain.GameStateReady)) {
			return domain.ErrConflict
		}
	}
	proof, err := buildSwissRoundLockProof(gameusecase.StartScope{TournamentID: tournamentID, WaveID: header.ID},
		domain.ReadyWindowSourceRevisions{ProjectionRevisionID: header.ProjectionRevisionID, ProjectionRevision: header.ProjectionRevision, WaveRevisionID: domain.WaveRevisionID(header.RevisionID)}, header.Revision, snapshot)
	if err != nil {
		return err
	}
	return ensureSwissRoundLockProof(ctx, q, proof, at, origin)
}
