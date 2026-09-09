package postgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

// persistFinalSwissReceipt joins the result publication transaction. It admits
// a receipt only after all preset rounds have immutable terminal Series heads.
func (r *TournamentProgressionPostgres) persistFinalSwissReceipt(
	ctx context.Context, scope ProjectionScope, projectionID uuid.UUID, now time.Time,
) error {
	q := r.tx.Querier(ctx)
	tournament, err := q.GetTournamentSummary(ctx, scope.TournamentID)
	if err != nil {
		return err
	}
	if tournament.State != string(domain.TournamentStateSwiss) {
		return nil
	}
	view, err := tournamentProgressionTournamentView(tournament)
	if err != nil {
		return err
	}
	return r.persistFinalSwissReceiptCurrent(ctx, scope, projectionID, now, view, nil, nil)
}

func (r *TournamentProgressionPostgres) persistCorrectionFinalSwissReceipt(
	ctx context.Context,
	scope ProjectionScope,
	projectionID, commandID uuid.UUID,
	predecessor correctionFinalSwissReceiptPredecessor,
	now time.Time,
) error {
	if commandID == uuid.Nil {
		return domain.ErrValidation
	}
	tournament, err := r.tx.Querier(ctx).GetTournamentSummary(ctx, scope.TournamentID)
	if err != nil {
		return err
	}
	if tournament.State != string(domain.TournamentStateGolden) &&
		tournament.State != string(domain.TournamentStatePlayoffs) {
		return domain.ErrConflict
	}
	view, err := tournamentProgressionTournamentView(tournament)
	if err != nil {
		return err
	}
	return r.persistFinalSwissReceiptCurrent(ctx, scope, projectionID, now, view, &commandID, &predecessor)
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func (r *TournamentProgressionPostgres) persistFinalSwissReceiptCurrent(
	ctx context.Context,
	scope ProjectionScope,
	projectionID uuid.UUID,
	now time.Time,
	view usecase.TournamentView,
	correctionCommandID *uuid.UUID,
	correctionPredecessor *correctionFinalSwissReceiptPredecessor,
) error {
	q := r.tx.Querier(ctx)
	participants, err := q.LockTournamentProgressionParticipants(ctx, scope.RosterID)
	if err != nil {
		return err
	}
	expectedRounds, err := view.Preset.SwissRounds(len(participants))
	if err != nil {
		return err
	}
	rounds, err := q.LockTournamentProgressionSwissRounds(ctx, sqlc.LockTournamentProgressionSwissRoundsParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil || len(rounds) != expectedRounds {
		return err
	}
	series, err := q.LockTournamentProgressionSwissSeries(ctx, sqlc.LockTournamentProgressionSwissSeriesParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return err
	}
	for _, item := range series {
		if !domain.SeriesState(item.State).IsTerminal() {
			return nil
		}
	}
	if _, _, _, _, err := progressionSwissCounts(rounds, series, nil); err != nil {
		return err
	}
	current, err := q.GetCurrentProjectionRevision(ctx, sqlc.GetCurrentProjectionRevisionParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return err
	}
	if current.ID != projectionID {
		return domain.ErrConflict
	}
	if correctionCommandID != nil {
		if correctionPredecessor == nil || correctionPredecessor.projection == nil ||
			correctionPredecessor.tournamentID != scope.TournamentID ||
			correctionPredecessor.rosterID != scope.RosterID {
			return domain.ErrConflict
		}
		correction, correctionErr := q.GetTournamentAdminCorrectionCommand(ctx, *correctionCommandID)
		if correctionErr != nil {
			return correctionErr
		}
		if correction.CommandID != *correctionCommandID || correction.TournamentID != scope.TournamentID ||
			correction.RosterID != scope.RosterID || correction.ResultingProjectionRevisionID != current.ID ||
			correction.ResultingProjectionRevision != current.RevisionNumber || !current.PreviousRevisionID.Valid ||
			correction.SourceProjectionRevisionID != current.PreviousRevisionID.UUID ||
			correction.SourceProjectionRevision+1 != current.RevisionNumber ||
			correction.SourceProjectionRevisionID != correctionPredecessor.correctionSourceID ||
			correction.SourceProjectionRevision != correctionPredecessor.correctionSourceRevision ||
			correctionPredecessor.physicalRevision > correctionPredecessor.correctionSourceRevision {
			return domain.ErrConflict
		}
	}
	authority := tournamentprogression.Authority{
		Tournament: view, ProjectionRevisionID: current.ID, ProjectionRevision: current.RevisionNumber,
	}
	rows, err := r.finalSwissPublicationRows(ctx, authority, now)
	if err != nil {
		return fmt.Errorf("load publication: %w", err)
	}
	if correctionCommandID != nil {
		rows.ledger, err = correctionFinalSwissReceiptLedger(rows.seriesEvidence, rows.ledger)
		if err != nil {
			return fmt.Errorf("select corrected Swiss ledger: %w", err)
		}
	}
	nodes, err := progressionReceiptNodeIndex(authority, rows.nodes, rows.logicalNodes)
	if err != nil {
		return fmt.Errorf("restore publication nodes: %w", err)
	}
	proofs, err := progressionReceiptRoundProofs(authority, rows.lockProofs, rows.lockProofMembers, rows.lockProofSeries)
	if err != nil {
		return fmt.Errorf("restore round proofs: %w", err)
	}
	sources, err := indexProgressionReceiptSources(rows.sourceProjections)
	if err != nil {
		return fmt.Errorf("restore source projections: %w", err)
	}
	root := rows.chain[0]
	var previous *playoff.FinalSwissProjection
	if correctionPredecessor != nil {
		previous = correctionPredecessor.projection
	} else {
		previous, err = r.finalSwissReceiptPredecessor(ctx, authority)
		if err != nil {
			return fmt.Errorf("restore predecessor receipt: %w", err)
		}
	}
	root, err = finalSwissReceiptLineage(root, previous)
	if err != nil {
		return err
	}
	input, err := progressionReceiptInput(authority, root, previous, rows, nodes, proofs, sources)
	if err != nil {
		return fmt.Errorf("restore publication input: %w", err)
	}
	plan, err := playoff.PlanFinalSwissReceipt(input)
	if err != nil {
		return fmt.Errorf("plan receipt: %w", err)
	}
	digest := plan.Projection().Revision().PayloadDigest()
	root.CanonicalPayloadDigest = digest[:]
	if err := writeFinalSwissReceipt(ctx, q, root, rows); err != nil {
		return fmt.Errorf("write receipt: %w", err)
	}
	retained, err := r.lockFinalSwissReceiptRows(ctx, authority)
	if err != nil {
		return fmt.Errorf("read receipt: %w", err)
	}
	_, err = progressionSwissInputFromReceipt(authority, retained)
	if err != nil {
		return fmt.Errorf("restore receipt: %w", err)
	}
	return nil
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func correctionFinalSwissReceiptLedger(
	series []sqlc.LockTournamentProgressionFinalSwissReceiptSeriesEvidenceRow,
	ledger []sqlc.LockTournamentProgressionFinalSwissReceiptLedgerRow,
) ([]sqlc.LockTournamentProgressionFinalSwissReceiptLedgerRow, error) {
	current := make(map[uuid.UUID]uuid.UUID, len(series))
	for _, evidence := range series {
		if evidence.SeriesID == uuid.Nil || evidence.SeriesResultRevisionID == uuid.Nil {
			return nil, domain.ErrConflict
		}
		if prior, found := current[evidence.SeriesID]; found && prior != evidence.SeriesResultRevisionID {
			return nil, domain.ErrConflict
		}
		current[evidence.SeriesID] = evidence.SeriesResultRevisionID
	}
	currentParticipants := make(map[uuid.UUID]map[uuid.UUID]struct{}, len(current))
	for _, row := range ledger {
		if row.SourceKind == "bye" {
			continue
		}
		if row.SourceKind != "series" || !row.SourceSeriesID.Valid || !row.SeriesResultRevisionID.Valid {
			return nil, domain.ErrConflict
		}
		resultID, found := current[row.SourceSeriesID.UUID]
		if !found {
			return nil, domain.ErrConflict
		}
		if resultID == row.SeriesResultRevisionID.UUID {
			participants := currentParticipants[row.SourceSeriesID.UUID]
			if participants == nil {
				participants = make(map[uuid.UUID]struct{}, 2)
				currentParticipants[row.SourceSeriesID.UUID] = participants
			}
			if row.ParticipantID == uuid.Nil {
				return nil, domain.ErrConflict
			}
			if _, duplicate := participants[row.ParticipantID]; duplicate {
				return nil, domain.ErrConflict
			}
			participants[row.ParticipantID] = struct{}{}
		}
	}
	for seriesID := range current {
		if len(currentParticipants[seriesID]) != 2 {
			return nil, domain.ErrConflict
		}
	}
	return append([]sqlc.LockTournamentProgressionFinalSwissReceiptLedgerRow(nil), ledger...), nil
}

// correctionFinalSwissReceiptPredecessor is captured while its physical
// result heads are still current. A correction must not reconstruct the
// immutable predecessor receipt after replacing those heads.
type correctionFinalSwissReceiptPredecessor struct {
	tournamentID             uuid.UUID
	rosterID                 uuid.UUID
	projectionID             uuid.UUID
	physicalRevision         int64
	correctionSourceID       uuid.UUID
	correctionSourceRevision int64
	projection               *playoff.FinalSwissProjection
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func (r *TournamentProgressionPostgres) prepareCorrectionFinalSwissReceiptPredecessor(
	ctx context.Context,
	scope ProjectionScope,
	projectionID uuid.UUID,
	physicalRevision int64,
) (correctionFinalSwissReceiptPredecessor, error) {
	if r == nil || r.tx == nil || ctx == nil || scope.TournamentID == uuid.Nil || scope.RosterID == uuid.Nil ||
		projectionID == uuid.Nil || physicalRevision < 1 {
		return correctionFinalSwissReceiptPredecessor{}, domain.ErrValidation
	}
	q := r.tx.Querier(ctx)
	tournament, err := q.GetTournamentSummary(ctx, scope.TournamentID)
	if err != nil {
		return correctionFinalSwissReceiptPredecessor{}, err
	}
	if tournament.State != string(domain.TournamentStateGolden) &&
		tournament.State != string(domain.TournamentStatePlayoffs) {
		return correctionFinalSwissReceiptPredecessor{}, fmt.Errorf("correction predecessor state %s: %w", tournament.State, domain.ErrConflict)
	}
	view, err := tournamentProgressionTournamentView(tournament)
	if err != nil {
		return correctionFinalSwissReceiptPredecessor{}, err
	}
	current, err := q.GetCurrentProjectionRevision(ctx, sqlc.GetCurrentProjectionRevisionParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return correctionFinalSwissReceiptPredecessor{}, err
	}
	if current.ID != projectionID || current.RevisionNumber != physicalRevision {
		return correctionFinalSwissReceiptPredecessor{}, fmt.Errorf("correction predecessor current projection differs: %w", domain.ErrConflict)
	}
	latest, err := q.LockLatestFinalSwissReceipt(ctx, sqlc.LockLatestFinalSwissReceiptParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
	})
	if err != nil {
		return correctionFinalSwissReceiptPredecessor{}, err
	}
	if latest.ProjectionRevisionID != projectionID || latest.RevisionNumber != physicalRevision {
		bridge, bridgeErr := q.GetCorrectionStageReceiptBridge(ctx, sqlc.GetCorrectionStageReceiptBridgeParams{
			TournamentID: scope.TournamentID, RosterID: scope.RosterID,
			ReceiptProjectionRevisionID: latest.ProjectionRevisionID,
			ReceiptProjectionRevision:   latest.RevisionNumber,
			CurrentProjectionRevisionID: nullableUUIDValue(projectionID),
			CurrentProjectionRevision:   &physicalRevision,
		})
		if bridgeErr != nil {
			return correctionFinalSwissReceiptPredecessor{}, fmt.Errorf("correction predecessor stage receipt bridge: %w", bridgeErr)
		}
		if bridge.CommandID == uuid.Nil || bridge.SourceProjectionRevisionID != latest.ProjectionRevisionID ||
			bridge.SourceProjectionRevision != latest.RevisionNumber ||
			!bridge.ResultingProjectionRevisionID.Valid || bridge.ResultingProjectionRevisionID.UUID != projectionID ||
			bridge.ResultingProjectionRevision == nil || *bridge.ResultingProjectionRevision != physicalRevision {
			return correctionFinalSwissReceiptPredecessor{}, fmt.Errorf("correction predecessor stage receipt bridge differs: %w", domain.ErrConflict)
		}
	}
	authority := tournamentprogression.Authority{
		Tournament: view, ProjectionRevisionID: latest.ProjectionRevisionID, ProjectionRevision: latest.RevisionNumber,
	}
	// This read reconstructs the immutable Final Swiss predecessor, not the
	// current Golden layout. A correction may have entered Golden from a later
	// Playoff projection, so current-stage Golden groups cannot be required to
	// bind the older receipt projection.
	receiptAuthority := authority
	receiptAuthority.Tournament.State = domain.TournamentStatePlayoffs
	rows, err := r.lockFinalSwissReceiptRows(ctx, receiptAuthority)
	if err != nil {
		return correctionFinalSwissReceiptPredecessor{}, fmt.Errorf("lock correction predecessor receipt: %w", err)
	}
	input, err := progressionSwissInputFromReceipt(authority, rows)
	if err != nil {
		return correctionFinalSwissReceiptPredecessor{}, fmt.Errorf("restore correction predecessor receipt: %w", err)
	}
	input.GoldenGroups = nil
	previous, err := playoff.PlanFinalSwissReceipt(input)
	if err != nil {
		return correctionFinalSwissReceiptPredecessor{}, fmt.Errorf("plan correction predecessor receipt: %w", err)
	}
	snapshot := previous.Snapshot()
	revision := snapshot.Projection().Revision()
	if snapshot.Validate() != nil || revision.ID().UUID() != latest.ProjectionRevisionID ||
		snapshot.PhysicalProjectionRevision() != int(latest.RevisionNumber) {
		return correctionFinalSwissReceiptPredecessor{}, fmt.Errorf("correction predecessor snapshot differs: %w", domain.ErrConflict)
	}
	return correctionFinalSwissReceiptPredecessor{
		tournamentID: scope.TournamentID, rosterID: scope.RosterID,
		projectionID: latest.ProjectionRevisionID, physicalRevision: latest.RevisionNumber,
		correctionSourceID: projectionID, correctionSourceRevision: physicalRevision, projection: &snapshot,
	}, nil
}

func (r *TournamentProgressionPostgres) finalSwissReceiptPredecessor(ctx context.Context, authority tournamentprogression.Authority) (*playoff.FinalSwissProjection, error) {
	latest, err := r.tx.Querier(ctx).LockLatestFinalSwissReceipt(ctx, sqlc.LockLatestFinalSwissReceiptParams{
		TournamentID: authority.Tournament.ID, RosterID: authority.Tournament.RosterID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if latest.RevisionNumber >= authority.ProjectionRevision {
		return nil, domain.ErrConflict
	}
	previousAuthority := authority
	previousAuthority.ProjectionRevisionID, previousAuthority.ProjectionRevision = latest.ProjectionRevisionID, latest.RevisionNumber
	retained, err := r.lockFinalSwissReceiptRows(ctx, previousAuthority)
	if err != nil {
		return nil, err
	}
	input, err := progressionSwissInputFromReceipt(previousAuthority, retained)
	if err != nil {
		return nil, err
	}
	input.GoldenGroups = nil
	previous, err := playoff.PlanFinalSwissReceipt(input)
	if err != nil {
		return nil, err
	}
	return &previous, nil
}

func writeFinalSwissReceipt(ctx context.Context, q *sqlc.Queries, root sqlc.LockTournamentProgressionFinalSwissReceiptChainRow, rows progressionFinalSwissReceiptRows) error {
	_, err := q.CreateFinalSwissProjectionReceipt(ctx, sqlc.CreateFinalSwissProjectionReceiptParams{
		ProjectionRevisionID: root.ProjectionRevisionID, TournamentID: root.TournamentID, RosterID: root.RosterID,
		ReceiptRevision: root.ReceiptRevision, CanonicalProjectionID: root.CanonicalProjectionID,
		PreviousReceiptProjectionRevisionID: root.PreviousReceiptProjectionRevisionID,
		SourceStandingsArtifactID:           root.SourceStandingsArtifactID,
		SourceStandingsPayloadDigest:        root.SourceStandingsPayloadDigest,
		CanonicalPayloadDigest:              root.CanonicalPayloadDigest, CreatedAt: root.CreatedAt,
	})
	if err != nil {
		return err
	}
	for _, row := range rows.participants {
		_, err = q.CreateFinalSwissProjectionReceiptParticipant(ctx, sqlc.CreateFinalSwissProjectionReceiptParticipantParams{
			ProjectionRevisionID: root.ProjectionRevisionID, TournamentID: root.TournamentID, RosterID: root.RosterID,
			ParticipantID: row.ParticipantID, StableSeed: row.StableSeed, CreatedAt: root.CreatedAt,
		})
		if err != nil {
			return err
		}
	}
	for _, row := range rows.rounds {
		_, err = q.CreateFinalSwissProjectionReceiptRound(ctx, sqlc.CreateFinalSwissProjectionReceiptRoundParams{
			ProjectionRevisionID: root.ProjectionRevisionID, TournamentID: root.TournamentID, RosterID: root.RosterID,
			RoundID: row.RoundID, RoundNumber: row.RoundNumber, CreatedAt: root.CreatedAt,
		})
		if err != nil {
			return err
		}
	}
	for _, row := range rows.seriesEvidence {
		_, err = q.CreateFinalSwissProjectionReceiptSeries(ctx, sqlc.CreateFinalSwissProjectionReceiptSeriesParams{
			ProjectionRevisionID: root.ProjectionRevisionID, TournamentID: root.TournamentID, RosterID: root.RosterID,
			RoundID: row.RoundID, SeriesID: row.SeriesID, TerminalSource: row.TerminalSource,
			SeriesResultRevisionID: row.SeriesResultRevisionID, ScoreRevisionID: row.ScoreRevisionID,
			SeriesResultNodeID: row.SeriesResultNodeID, ScoreNodeID: row.ScoreNodeID,
			NormalNoShowCommitID: row.NormalNoShowCommitID, OperatorForfeitCommitID: row.OperatorForfeitCommitID,
			CreatedAt: root.CreatedAt,
		})
		if err != nil {
			return err
		}
	}
	for _, row := range rows.gameEvidence {
		_, err = q.CreateFinalSwissProjectionReceiptGame(ctx, sqlc.CreateFinalSwissProjectionReceiptGameParams{
			ProjectionRevisionID: root.ProjectionRevisionID, TournamentID: root.TournamentID, RosterID: root.RosterID,
			SeriesID: row.SeriesID, GameAttemptID: row.GameAttemptID, GameResultRevisionID: row.GameResultRevisionID,
			GameResultNodeID: row.GameResultNodeID, CreatedAt: root.CreatedAt,
		})
		if err != nil {
			return err
		}
	}
	for _, row := range rows.ledger {
		_, err = q.CreateFinalSwissProjectionReceiptLedgerEntry(ctx, sqlc.CreateFinalSwissProjectionReceiptLedgerEntryParams{
			ProjectionRevisionID: root.ProjectionRevisionID, TournamentID: root.TournamentID, RosterID: root.RosterID,
			LedgerEntryID: row.LedgerEntryID, CreatedAt: root.CreatedAt,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func finalSwissPublicationRoot(record *ProjectionRecord, now time.Time) (sqlc.LockTournamentProgressionFinalSwissReceiptChainRow, error) {
	for _, item := range record.Artifacts {
		if item.Artifact.ArtifactKind != string(domain.ArtifactKindStandings) {
			continue
		}
		digest := sha256.Sum256(item.Artifact.Payload)
		return sqlc.LockTournamentProgressionFinalSwissReceiptChainRow{
			ProjectionRevisionID: record.Revision.ID, TournamentID: record.Revision.TournamentID,
			RosterID: record.Revision.RosterID, ReceiptRevision: 1,
			CanonicalProjectionID:     uuid.NewSHA1(record.Revision.TournamentID, []byte("final-swiss-projection")),
			SourceStandingsArtifactID: item.Artifact.ID, SourceStandingsPayloadDigest: digest[:],
			SourceStandingsArtifactDigest: item.Artifact.PayloadDigest, SourceStandingsPayload: item.Artifact.Payload,
			PhysicalProjectionRevision: record.Revision.RevisionNumber, CreatedAt: tstz(now),
		}, nil
	}
	return sqlc.LockTournamentProgressionFinalSwissReceiptChainRow{}, domain.ErrConflict
}

func finalSwissReceiptLineage(root sqlc.LockTournamentProgressionFinalSwissReceiptChainRow, previous *playoff.FinalSwissProjection) (sqlc.LockTournamentProgressionFinalSwissReceiptChainRow, error) {
	if previous == nil {
		return root, nil
	}
	prior := previous.Projection().Revision()
	if previous.Validate() != nil || prior.TournamentID() != root.TournamentID ||
		prior.ID().UUID() == root.ProjectionRevisionID || previous.PhysicalProjectionRevision() >= int(root.PhysicalProjectionRevision) ||
		!root.CreatedAt.Valid || root.CreatedAt.Time.Before(prior.CreatedAt()) {
		return root, domain.ErrConflict
	}
	root.ReceiptRevision = int64(prior.RevisionNo()) + 1
	root.PreviousReceiptProjectionRevisionID = nullableUUIDValue(prior.ID().UUID())
	root.CanonicalProjectionID = previous.GoldenSource().ProjectionID
	return root, nil
}
