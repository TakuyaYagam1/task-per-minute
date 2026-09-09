package postgres

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
	tournamentprogression "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/progression"
)

// progressionSwissInputFromReceipt rebuilds each receipt in canonical order,
// retaining the fully validated predecessor rather than following current
// mutable heads. A later correction can therefore supersede a physical
// projection without changing an earlier Final Swiss receipt.
func progressionSwissInputFromReceipt(
	command tournamentprogression.Command,
	authority tournamentprogression.Authority,
	rows progressionFinalSwissReceiptRows,
) (playoff.ProgressionSwissInput, error) {
	chain, err := progressionFinalSwissReceiptChain(authority, rows.chain)
	if err != nil {
		return playoff.ProgressionSwissInput{}, err
	}
	sources, err := indexProgressionReceiptSources(rows.sourceProjections)
	if err != nil {
		return playoff.ProgressionSwissInput{}, err
	}
	nodes, err := progressionReceiptNodeIndex(authority, rows.nodes, rows.logicalNodes)
	if err != nil {
		return playoff.ProgressionSwissInput{}, err
	}
	proofs, err := progressionReceiptRoundProofs(authority, rows.lockProofs, rows.lockProofMembers, rows.lockProofSeries)
	if err != nil {
		return playoff.ProgressionSwissInput{}, err
	}

	var previous *playoff.FinalSwissProjection
	var current playoff.ProgressionSwissInput
	for _, receipt := range chain {
		input, buildErr := progressionReceiptInput(command, authority, receipt, previous, rows, nodes, proofs, sources)
		if buildErr != nil {
			return playoff.ProgressionSwissInput{}, buildErr
		}
		planned, input, planErr := progressionPlanSwissReceipt(input, receipt, rows.goldenGroups)
		if planErr != nil {
			return playoff.ProgressionSwissInput{}, fmt.Errorf("Final Swiss receipt %s: %w", receipt.ProjectionRevisionID, planErr)
		}
		current = input
		previousSnapshot := planned.Snapshot()
		previous = &previousSnapshot
	}
	if previous == nil || current.RevisionID.UUID() != authority.ProjectionRevisionID ||
		current.PhysicalProjectionRevision != int(authority.ProjectionRevision) ||
		current.RevisionNo != int(chain[len(chain)-1].ReceiptRevision) {
		return playoff.ProgressionSwissInput{}, domain.ErrConflict
	}
	return current, nil
}

func progressionPlanSwissReceipt(input playoff.ProgressionSwissInput, receipt sqlc.LockTournamentProgressionFinalSwissReceiptChainRow, groups []sqlc.LockTournamentProgressionGoldenSettlementsRow) (playoff.FinalSwissProjection, playoff.ProgressionSwissInput, error) {
	var matched []sqlc.LockTournamentProgressionGoldenSettlementsRow
	for _, group := range groups {
		if group.SourceProjectionRevisionID == receipt.ProjectionRevisionID {
			matched = append(matched, group)
		}
	}
	if len(matched) > 0 {
		identities, err := progressionGoldenIdentities(tournamentprogression.Authority{ProjectionRevisionID: receipt.ProjectionRevisionID, ProjectionRevision: receipt.PhysicalProjectionRevision}, matched)
		if err != nil {
			return playoff.FinalSwissProjection{}, playoff.ProgressionSwissInput{}, err
		}
		input.GoldenGroups = identities
	}
	receiptInput := input
	receiptInput.GoldenGroups = nil
	planned, err := playoff.PlanFinalSwissReceipt(receiptInput)
	if err != nil {
		return playoff.FinalSwissProjection{}, playoff.ProgressionSwissInput{}, err
	}
	digest := sha256.Sum256(planned.Projection().Payload())
	canonical, valid := progressionDigest(receipt.CanonicalPayloadDigest)
	if !valid || digest != canonical {
		return playoff.FinalSwissProjection{}, playoff.ProgressionSwissInput{}, domain.ErrConflict
	}
	if len(matched) > 0 {
		if _, err := playoff.PlanFinalSwissProgression(input); err != nil {
			return playoff.FinalSwissProjection{}, playoff.ProgressionSwissInput{}, err
		}
	}
	return planned, input, nil
}

type progressionReceiptNodeKey struct {
	receiptID uuid.UUID
	nodeID    uuid.UUID
}

func progressionReceiptNodeIndex(
	authority tournamentprogression.Authority,
	rows []sqlc.LockTournamentProgressionFinalSwissReceiptProjectionNodesRow,
	logical []sqlc.LockTournamentProgressionFinalSwissReceiptLogicalResultNodesRow,
) (map[progressionReceiptNodeKey]domain.ProjectionRevision, error) {
	if len(rows) == 0 || len(logical) == 0 {
		return nil, domain.ErrConflict
	}
	result := make(map[progressionReceiptNodeKey]domain.ProjectionRevision, len(rows))
	metadata := make(map[progressionReceiptNodeKey]sqlc.LockTournamentProgressionFinalSwissReceiptProjectionNodesRow, len(rows))
	for _, row := range rows {
		if row.ProjectionRevisionID == uuid.Nil || row.ID == uuid.Nil || row.AuthorityID == uuid.Nil ||
			row.EntityID == uuid.Nil || row.RevisionNumber < 1 || !row.CreatedAt.Valid ||
			!domain.IsValidServerTime(row.CreatedAt.Time.UTC()) || len(row.Payload) == 0 {
			return nil, domain.ErrConflict
		}
		kind := domain.ArtifactKind(row.ArtifactKind)
		if !kind.IsValid() {
			return nil, domain.ErrConflict
		}
		payloadDigest, valid := progressionDigest(row.PayloadDigest)
		if !valid || sha256.Sum256(row.Payload) != payloadDigest {
			return nil, domain.ErrConflict
		}
		var previous *domain.DerivedRevisionID
		if row.PreviousNodeID.Valid {
			if row.PreviousNodeID.UUID == uuid.Nil || row.PreviousNodeID.UUID == row.ID {
				return nil, domain.ErrConflict
			}
			value := domain.DerivedRevisionID(row.PreviousNodeID.UUID)
			previous = &value
		}
		projection, err := domain.NewProjectionRevision(
			domain.DerivedRevisionID(row.ID), authority.Tournament.ID,
			domain.ArtifactRef{Kind: kind, EntityID: row.EntityID}, int(row.RevisionNumber),
			previous, row.CreatedAt.Time.UTC(), row.Payload,
		)
		if err != nil || projection.Revision().PayloadDigest() != payloadDigest {
			return nil, domain.ErrConflict
		}
		key := progressionReceiptNodeKey{receiptID: row.ProjectionRevisionID, nodeID: row.ID}
		if _, duplicate := result[key]; duplicate {
			return nil, domain.ErrConflict
		}
		result[key] = projection
		metadata[key] = row
	}
	for _, row := range logical {
		key := progressionReceiptNodeKey{receiptID: row.ReceiptProjectionRevisionID, nodeID: row.NodeID}
		projection, found := result[key]
		if !found || row.ReceiptProjectionRevisionID == uuid.Nil || row.NodeID == uuid.Nil || row.SourceID == uuid.Nil ||
			row.AuthorityID == uuid.Nil || row.EntityID == uuid.Nil || row.RevisionNumber < 1 ||
			!row.NodeCreatedAt.Valid || !domain.IsValidServerTime(row.NodeCreatedAt.Time.UTC()) || len(row.Payload) == 0 {
			return nil, domain.ErrConflict
		}
		kind := domain.ArtifactKind(row.ArtifactKind)
		stored := metadata[key]
		if !validProgressionReceiptNodeOrigin(row) || stored.AuthorityID != row.AuthorityID ||
			stored.PreviousNodeID != row.PreviousNodeID || !progressionBytesEqual(stored.PayloadDigest, row.PayloadDigest) ||
			!kind.IsValid() || projection.Revision().Artifact() != (domain.ArtifactRef{Kind: kind, EntityID: row.EntityID}) ||
			projection.Revision().RevisionNo() != int(row.RevisionNumber) ||
			!projection.Revision().CreatedAt().Equal(row.NodeCreatedAt.Time.UTC()) ||
			sha256.Sum256(row.Payload) != projection.Revision().PayloadDigest() {
			return nil, domain.ErrConflict
		}
	}
	return result, nil
}

func validProgressionReceiptNodeOrigin(row sqlc.LockTournamentProgressionFinalSwissReceiptLogicalResultNodesRow) bool {
	if row.HeadKind != row.ArtifactKind {
		return false
	}
	switch row.SourceKind {
	case "result_commit", "wave_initialization", "normal_no_show_commit", "operator_forfeit_commit":
		return row.SourceID == row.NodeID && !row.CorrectionCommandID.Valid &&
			(row.SourceKind != "wave_initialization" || row.ArtifactKind == string(domain.ArtifactKindSeriesScore))
	case "correction_commit":
		return row.SourceID != row.NodeID && row.CorrectionCommandID.Valid &&
			row.CorrectionCommandID.UUID != uuid.Nil && row.BindingCreatedAt.Valid
	default:
		return false
	}
}

func progressionReceiptRoundProofs(
	authority tournamentprogression.Authority,
	roots []sqlc.SwissRoundLockProof,
	members []sqlc.SwissRoundLockProofMember,
	series []sqlc.SwissRoundLockProofSeries,
) (map[uuid.UUID]swissusecase.RoundLockProof, error) {
	if len(roots) == 0 {
		return nil, domain.ErrConflict
	}
	membersByRound := make(map[uuid.UUID][]uuid.UUID, len(roots))
	for _, member := range members {
		if member.RoundID == uuid.Nil || member.RosterID != authority.Tournament.RosterID ||
			member.ParticipantID == uuid.Nil || !member.CreatedAt.Valid {
			return nil, domain.ErrConflict
		}
		membersByRound[member.RoundID] = append(membersByRound[member.RoundID], member.ParticipantID)
	}
	seriesByRound := make(map[uuid.UUID][]swissusecase.LockedSeries, len(roots))
	for _, row := range series {
		if row.RoundID == uuid.Nil || row.RosterID != authority.Tournament.RosterID || row.SeriesID == uuid.Nil ||
			row.PairingID == uuid.Nil || row.FirstParticipantID == uuid.Nil || row.SecondParticipantID == uuid.Nil ||
			row.FirstParticipantID == row.SecondParticipantID || row.CategoryRevisionID == uuid.Nil ||
			row.CategoryRevision < 1 || row.AssignmentID == uuid.Nil || row.AssignmentRevision < 1 ||
			row.AssignmentPlanID == uuid.Nil || row.AssignmentPlanRevisionID == uuid.Nil || row.ReservationID == uuid.Nil ||
			row.ReservationRevision < 1 || !row.CreatedAt.Valid {
			return nil, domain.ErrConflict
		}
		seriesByRound[row.RoundID] = append(seriesByRound[row.RoundID], swissusecase.LockedSeries{
			SeriesID: row.SeriesID, PairingID: row.PairingID, FirstParticipantID: row.FirstParticipantID,
			SecondParticipantID: row.SecondParticipantID, CategoryRevisionID: row.CategoryRevisionID,
			CategoryRevision: row.CategoryRevision, AssignmentID: row.AssignmentID, AssignmentRevision: row.AssignmentRevision,
			AssignmentPlanID: row.AssignmentPlanID, AssignmentPlanRevisionID: row.AssignmentPlanRevisionID,
			ReservationID: row.ReservationID, ReservationRevision: row.ReservationRevision,
		})
	}
	proofs := make(map[uuid.UUID]swissusecase.RoundLockProof, len(roots))
	for _, root := range roots {
		if root.RoundID == uuid.Nil || root.TournamentID != authority.Tournament.ID ||
			root.RosterID != authority.Tournament.RosterID || root.Preset != string(authority.Tournament.Preset) ||
			root.RoundNumber < 1 || root.SourceProjectionRevisionID == uuid.Nil || root.PreflightRevisionID == uuid.Nil ||
			root.NormalPoolRevisionID == uuid.Nil || root.WaveID == uuid.Nil || root.WaveRevisionID == uuid.Nil ||
			root.RoundRevision < 1 || root.SourceProjectionRevision < 1 || root.RosterRevision < 1 ||
			root.HistoryRevision < 0 || root.NormalPoolRevision < 1 || root.WaveRevision < 1 ||
			!root.LockedAt.Valid || !root.CreatedAt.Valid || len(root.ProofHash) != sha256.Size {
			return nil, domain.ErrConflict
		}
		bye := uuid.Nil
		if root.ByeParticipantID.Valid {
			bye = root.ByeParticipantID.UUID
		}
		proof, err := swissusecase.NewRoundLockProof(swissusecase.RoundLockProofInput{
			TournamentID: root.TournamentID, RosterID: root.RosterID, RoundID: root.RoundID,
			Preset: domain.TournamentPreset(root.Preset), RoundNumber: int(root.RoundNumber),
			SourceProjectionRevisionID: root.SourceProjectionRevisionID, PreflightRevisionID: root.PreflightRevisionID,
			NormalPoolRevisionID: root.NormalPoolRevisionID, WaveID: root.WaveID,
			WaveRevisionID: domain.WaveRevisionID(root.WaveRevisionID),
			Revisions: swissusecase.RoundLockRevisions{Round: root.RoundRevision, SourceProjection: root.SourceProjectionRevision,
				Roster: root.RosterRevision, History: root.HistoryRevision, NormalPool: root.NormalPoolRevision, Wave: root.WaveRevision},
			RosterParticipantIDs: membersByRound[root.RoundID], Series: seriesByRound[root.RoundID], ByeParticipantID: bye,
		})
		if err != nil || proof.ProofHash != hex.EncodeToString(root.ProofHash) {
			return nil, domain.ErrConflict
		}
		if _, duplicate := proofs[root.RoundID]; duplicate {
			return nil, domain.ErrConflict
		}
		proofs[root.RoundID] = proof
	}
	return proofs, nil
}

func progressionReceiptInput(
	command tournamentprogression.Command,
	authority tournamentprogression.Authority,
	receipt sqlc.LockTournamentProgressionFinalSwissReceiptChainRow,
	previous *playoff.FinalSwissProjection,
	rows progressionFinalSwissReceiptRows,
	nodes map[progressionReceiptNodeKey]domain.ProjectionRevision,
	proofs map[uuid.UUID]swissusecase.RoundLockProof,
	sources map[progressionReceiptSourceKey]progressionReceiptSourceProjection,
) (playoff.ProgressionSwissInput, error) {
	participants, seeds, err := progressionReceiptParticipants(receipt.ProjectionRevisionID, rows.participants)
	if err != nil {
		return playoff.ProgressionSwissInput{}, err
	}
	rounds, err := progressionReceiptRounds(command, authority, receipt, rows, nodes, proofs, sources)
	if err != nil {
		return playoff.ProgressionSwissInput{}, fmt.Errorf("restore rounds: %w", err)
	}
	if !receipt.CreatedAt.Valid || !domain.IsValidServerTime(receipt.CreatedAt.Time.UTC()) {
		return playoff.ProgressionSwissInput{}, domain.ErrConflict
	}
	return playoff.ProgressionSwissInput{
		TournamentID: authority.Tournament.ID, Preset: authority.Tournament.Preset,
		ProjectionID: receipt.CanonicalProjectionID,
		RevisionID:   domain.DerivedRevisionID(receipt.ProjectionRevisionID), RevisionNo: int(receipt.ReceiptRevision),
		PhysicalProjectionRevision: int(receipt.PhysicalProjectionRevision), Previous: previous,
		ParticipantIDs: participants, Seeds: seeds, Rounds: rounds, CreatedAt: receipt.CreatedAt.Time.UTC(),
	}, nil
}

func progressionReceiptParticipants(
	receiptID uuid.UUID,
	rows []sqlc.LockTournamentProgressionFinalSwissReceiptParticipantsRow,
) ([]uuid.UUID, []swissusecase.ParticipantSeed, error) {
	participants := make([]uuid.UUID, 0, len(rows))
	seeds := make([]swissusecase.ParticipantSeed, 0, len(rows))
	for _, row := range rows {
		if row.ProjectionRevisionID != receiptID {
			continue
		}
		if row.ParticipantID == uuid.Nil || row.StableSeed < 1 || !row.CreatedAt.Valid {
			return nil, nil, domain.ErrConflict
		}
		participants = append(participants, row.ParticipantID)
		seeds = append(seeds, swissusecase.ParticipantSeed{ParticipantID: row.ParticipantID, Seed: int(row.StableSeed)})
	}
	if len(participants) < domain.TournamentMinParticipants {
		return nil, nil, domain.ErrConflict
	}
	sort.Slice(seeds, func(i, j int) bool { return seeds[i].ParticipantID.String() < seeds[j].ParticipantID.String() })
	sort.Slice(participants, func(i, j int) bool { return participants[i].String() < participants[j].String() })
	for index := range participants {
		if participants[index] == uuid.Nil || (index > 0 && participants[index] == participants[index-1]) {
			return nil, nil, domain.ErrConflict
		}
	}
	return participants, seeds, nil
}

func progressionReceiptRounds(
	command tournamentprogression.Command,
	authority tournamentprogression.Authority,
	receipt sqlc.LockTournamentProgressionFinalSwissReceiptChainRow,
	rows progressionFinalSwissReceiptRows,
	nodes map[progressionReceiptNodeKey]domain.ProjectionRevision,
	proofs map[uuid.UUID]swissusecase.RoundLockProof,
	sources map[progressionReceiptSourceKey]progressionReceiptSourceProjection,
) ([]playoff.ProgressionSwissRound, error) {
	rounds := make([]playoff.ProgressionSwissRound, 0)
	for _, row := range rows.rounds {
		if row.ProjectionRevisionID != receipt.ProjectionRevisionID {
			continue
		}
		proof, found := proofs[row.RoundID]
		if !found || row.RoundID == uuid.Nil || row.RoundNumber < 1 || !row.CreatedAt.Valid ||
			proof.RoundID != row.RoundID || proof.RoundNumber != int(row.RoundNumber) ||
			proof.TournamentID != authority.Tournament.ID || proof.RosterID != authority.Tournament.RosterID ||
			proof.Preset != authority.Tournament.Preset || !row.CreatedAt.Time.UTC().Equal(receipt.CreatedAt.Time.UTC()) {
			return nil, domain.ErrConflict
		}
		series := make([]playoff.TerminalSeriesEvidence, 0)
		for _, evidence := range rows.seriesEvidence {
			if evidence.ProjectionRevisionID != receipt.ProjectionRevisionID || evidence.RoundID != row.RoundID {
				continue
			}
			terminal, err := progressionReceiptTerminalSeries(command, authority, receipt, evidence, rows, nodes, sources)
			if err != nil {
				return nil, fmt.Errorf("restore terminal Series %s: %w", evidence.SeriesID, err)
			}
			series = append(series, terminal)
		}
		if len(series) != len(proof.Series) {
			return nil, domain.ErrConflict
		}
		bye, err := progressionReceiptBye(receipt.ProjectionRevisionID, row.RoundID, int(row.RoundNumber), rows.ledger)
		if err != nil {
			return nil, err
		}
		rounds = append(rounds, playoff.ProgressionSwissRound{
			RoundID: row.RoundID, RoundNumber: int(row.RoundNumber), RevisionID: proof.SourceProjectionRevisionID,
			LockProof: proof, Series: series, Bye: bye,
		})
	}
	if len(rounds) == 0 {
		return nil, domain.ErrConflict
	}
	sort.Slice(rounds, func(i, j int) bool { return rounds[i].RoundNumber < rounds[j].RoundNumber })
	for index := range rounds {
		if rounds[index].RoundNumber != index+1 {
			return nil, domain.ErrConflict
		}
	}
	return rounds, nil
}

func progressionReceiptBye(
	receiptID, roundID uuid.UUID,
	roundNumber int,
	ledger []sqlc.LockTournamentProgressionFinalSwissReceiptLedgerRow,
) (*swissusecase.ByePointResult, error) {
	var found *swissusecase.ByePointResult
	for _, row := range ledger {
		if row.ProjectionRevisionID != receiptID || row.RoundID != roundID || row.SourceKind != string(swissusecase.PointSourceBye) {
			continue
		}
		if row.RoundNumber != int16(roundNumber) || !row.ByeRevisionID.Valid || row.ByeRevisionID.UUID == uuid.Nil ||
			row.SourceSeriesID.Valid || row.SeriesResultRevisionID.Valid || row.ResultLabel != nil || row.ParticipantID == uuid.Nil ||
			row.OpponentID.Valid || row.Points != swissusecase.StandingsByePoints || row.EffectiveTimeNs != 0 || row.AcceptedSolveTimeNs != nil || row.StableSeed < 1 {
			return nil, domain.ErrConflict
		}
		if found != nil {
			return nil, domain.ErrConflict
		}
		found = &swissusecase.ByePointResult{RoundID: roundID, RoundNumber: roundNumber, ParticipantID: row.ParticipantID, RevisionID: row.ByeRevisionID.UUID}
	}
	return found, nil
}

func progressionReceiptTerminalSeries(
	command tournamentprogression.Command,
	authority tournamentprogression.Authority,
	receipt sqlc.LockTournamentProgressionFinalSwissReceiptChainRow,
	evidence sqlc.LockTournamentProgressionFinalSwissReceiptSeriesEvidenceRow,
	rows progressionFinalSwissReceiptRows,
	nodes map[progressionReceiptNodeKey]domain.ProjectionRevision,
	sources map[progressionReceiptSourceKey]progressionReceiptSourceProjection,
) (playoff.TerminalSeriesEvidence, error) {
	if evidence.ProjectionRevisionID != receipt.ProjectionRevisionID || evidence.RoundID == uuid.Nil || evidence.SeriesID == uuid.Nil ||
		evidence.SeriesResultRevisionID == uuid.Nil || evidence.ScoreRevisionID == uuid.Nil || evidence.SeriesResultNodeID == uuid.Nil ||
		evidence.ScoreNodeID == uuid.Nil || evidence.FirstParticipantID == uuid.Nil || evidence.SecondParticipantID == uuid.Nil ||
		evidence.FirstParticipantID == evidence.SecondParticipantID || !domain.SeriesFormat(evidence.SeriesFormat).IsValid() {
		return playoff.TerminalSeriesEvidence{}, domain.ErrConflict
	}
	if err := progressionRequireReceiptSource(sources, receipt.ProjectionRevisionID, domain.ArtifactKindSeriesResult, evidence.SeriesID, evidence.SeriesResultRevisionID); err != nil {
		return playoff.TerminalSeriesEvidence{}, fmt.Errorf("Series result source: %w", err)
	}
	if err := progressionRequireReceiptSource(sources, receipt.ProjectionRevisionID, domain.ArtifactKindSeriesScore, evidence.SeriesID, evidence.ScoreRevisionID); err != nil {
		return playoff.TerminalSeriesEvidence{}, fmt.Errorf("Series score source: %w", err)
	}
	resultProjection, found := nodes[progressionReceiptNodeKey{receiptID: receipt.ProjectionRevisionID, nodeID: evidence.SeriesResultNodeID}]
	if !found || resultProjection.Revision().Artifact() != (domain.ArtifactRef{Kind: domain.ArtifactKindSeriesResult, EntityID: evidence.SeriesID}) {
		return playoff.TerminalSeriesEvidence{}, domain.ErrConflict
	}
	scoreProjection, found := nodes[progressionReceiptNodeKey{receiptID: receipt.ProjectionRevisionID, nodeID: evidence.ScoreNodeID}]
	if !found || scoreProjection.Revision().Artifact() != (domain.ArtifactRef{Kind: domain.ArtifactKindSeriesScore, EntityID: evidence.SeriesID}) {
		return playoff.TerminalSeriesEvidence{}, domain.ErrConflict
	}
	if err := progressionRequireLogicalReceiptNode(rows.logicalNodes, receipt.ProjectionRevisionID, "series_result", evidence.SeriesID, evidence.SeriesResultRevisionID, evidence.SeriesResultNodeID); err != nil {
		return playoff.TerminalSeriesEvidence{}, err
	}
	if err := progressionRequireLogicalReceiptNode(rows.logicalNodes, receipt.ProjectionRevisionID, "series_score", evidence.SeriesID, evidence.ScoreRevisionID, evidence.ScoreNodeID); err != nil {
		return playoff.TerminalSeriesEvidence{}, err
	}

	series, gameRows, err := progressionReceiptSeriesTopology(authority.Tournament.ID, receipt.ProjectionRevisionID, evidence, rows.gameEvidence)
	if err != nil {
		return playoff.TerminalSeriesEvidence{}, fmt.Errorf("Series topology: %w", err)
	}
	if err := progressionRequireReceiptGames(receipt.ProjectionRevisionID, evidence.SeriesID, gameRows, rows.games, rows.logicalNodes, sources); err != nil {
		return playoff.TerminalSeriesEvidence{}, fmt.Errorf("Series Games: %w", err)
	}
	point, err := progressionReceiptSeriesPoints(receipt.ProjectionRevisionID, evidence, rows.ledger)
	if err != nil {
		return playoff.TerminalSeriesEvidence{}, fmt.Errorf("Series points: %w", err)
	}
	if point.RoundID != evidence.RoundID || point.SeriesID != evidence.SeriesID || point.ResultRevisionID != domain.OfficialResultRevisionID(evidence.SeriesResultRevisionID) {
		return playoff.TerminalSeriesEvidence{}, domain.ErrConflict
	}

	input, noGameDigest, err := progressionReceiptOfficialResultInput(
		command, authority, receipt, evidence, series, gameRows, resultProjection, scoreProjection, rows, nodes, sources,
	)
	if err != nil {
		return playoff.TerminalSeriesEvidence{}, fmt.Errorf("Series official input: %w", err)
	}
	terminal, err := playoff.NewTerminalSeriesEvidence(playoff.TerminalSeriesEvidenceInput{
		Series: series, OfficialResult: input, Projection: resultProjection, Result: point, NoGameEvidenceDigest: noGameDigest,
	})
	if err != nil {
		return playoff.TerminalSeriesEvidence{}, fmt.Errorf("terminal evidence: %v: %w", err, domain.ErrConflict)
	}
	return terminal, nil
}

func progressionRequireReceiptSource(
	sources map[progressionReceiptSourceKey]progressionReceiptSourceProjection,
	receiptID uuid.UUID,
	kind domain.ArtifactKind,
	entityID, revisionID uuid.UUID,
) error {
	key := progressionReceiptSourceKey{receiptProjectionRevisionID: receiptID, resultKind: kind, entityID: entityID, resultRevisionID: revisionID}
	value, found := sources[key]
	if !found || value.physicalProjectionRevisionID == uuid.Nil || value.physicalProjectionRevision < 1 || len(value.artifacts) == 0 {
		return domain.ErrConflict
	}
	return nil
}

func progressionRequireReceiptGames(
	receiptID, seriesID uuid.UUID,
	evidence []sqlc.LockTournamentProgressionFinalSwissReceiptGameEvidenceRow,
	receiptGames []sqlc.LockTournamentProgressionFinalSwissReceiptGamesRow,
	logical []sqlc.LockTournamentProgressionFinalSwissReceiptLogicalResultNodesRow,
	sources map[progressionReceiptSourceKey]progressionReceiptSourceProjection,
) error {
	remaining := make(map[uuid.UUID]sqlc.LockTournamentProgressionFinalSwissReceiptGamesRow)
	for _, row := range receiptGames {
		if row.ProjectionRevisionID != receiptID || row.SeriesID != seriesID {
			continue
		}
		if row.GameAttemptID == uuid.Nil || row.GameResultRevisionID == uuid.Nil || row.GameResultNodeID == uuid.Nil || !row.CreatedAt.Valid {
			return domain.ErrConflict
		}
		if _, duplicate := remaining[row.GameAttemptID]; duplicate {
			return domain.ErrConflict
		}
		remaining[row.GameAttemptID] = row
	}
	if len(remaining) != len(evidence) {
		return domain.ErrConflict
	}
	for _, row := range evidence {
		stored, found := remaining[row.GameAttemptID]
		if !found || stored.GameResultRevisionID != row.GameResultRevisionID || stored.GameResultNodeID != row.GameResultNodeID {
			return domain.ErrConflict
		}
		if err := progressionRequireReceiptSource(sources, receiptID, domain.ArtifactKindGameResult, row.GameAttemptID, row.GameResultRevisionID); err != nil {
			return err
		}
		if err := progressionRequireLogicalReceiptNode(logical, receiptID, "game_result", row.GameAttemptID, row.GameResultRevisionID, row.GameResultNodeID); err != nil {
			return err
		}
		delete(remaining, row.GameAttemptID)
	}
	if len(remaining) != 0 {
		return domain.ErrConflict
	}
	return nil
}

func progressionRequireLogicalReceiptNode(
	rows []sqlc.LockTournamentProgressionFinalSwissReceiptLogicalResultNodesRow,
	receiptID uuid.UUID,
	headKind string,
	entityID, sourceID, nodeID uuid.UUID,
) error {
	matched := false
	for _, row := range rows {
		if row.ReceiptProjectionRevisionID != receiptID || row.HeadKind != headKind || row.EntityID != entityID || row.SourceID != sourceID {
			continue
		}
		if row.NodeID != nodeID || row.NodeID == uuid.Nil || row.AuthorityID == uuid.Nil || row.RevisionNumber < 1 ||
			!row.NodeCreatedAt.Valid || len(row.Payload) == 0 {
			return domain.ErrConflict
		}
		if matched {
			return domain.ErrConflict
		}
		matched = true
	}
	if !matched {
		return domain.ErrConflict
	}
	return nil
}

func progressionReceiptSeriesTopology(
	tournamentID uuid.UUID,
	receiptID uuid.UUID,
	evidence sqlc.LockTournamentProgressionFinalSwissReceiptSeriesEvidenceRow,
	gameEvidence []sqlc.LockTournamentProgressionFinalSwissReceiptGameEvidenceRow,
) (domain.Series, []sqlc.LockTournamentProgressionFinalSwissReceiptGameEvidenceRow, error) {
	slots := make(map[uuid.UUID]*domain.GameSlot)
	matched := make([]sqlc.LockTournamentProgressionFinalSwissReceiptGameEvidenceRow, 0)
	for _, row := range gameEvidence {
		if row.ProjectionRevisionID != receiptID || row.SeriesID != evidence.SeriesID {
			continue
		}
		if row.GameAttemptID == uuid.Nil || row.GameResultRevisionID == uuid.Nil || row.GameResultNodeID == uuid.Nil ||
			row.SlotID == uuid.Nil || row.SlotNumber < 1 || row.AttemptNumber < 1 ||
			row.FirstParticipantWinsBefore < 0 || row.SecondParticipantWinsBefore < 0 ||
			!domain.Category(row.Category).IsValid() || !row.GameFinishedAt.Valid || !row.GameResultCreatedAt.Valid ||
			!row.ResultOccurredAt.Valid || row.GameResultCommandID == uuid.Nil || row.GameResultRevisionNumber < 1 ||
			row.GameResultNodeRevision < 1 || len(row.GameResultPayload) == 0 ||
			sha256.Sum256(row.GameResultPayload) != progressionDigestMust(row.GameResultPayloadDigest) {
			return domain.Series{}, nil, fmt.Errorf("invalid Game evidence identity for %s: %w", row.GameAttemptID, domain.ErrConflict)
		}
		// A receipt chain may name a superseded Game head. Its immutable result
		// revision is authoritative; game_attempt is only topology and can now
		// expose the corrected current state.
		state := domain.GameState(row.ResultState)
		reason := domain.GameResultReason(row.ResultReason)
		if !state.IsTerminal() || !reason.IsLegalFor(state) {
			return domain.Series{}, nil, fmt.Errorf("invalid Game evidence state for %s: %w", row.GameAttemptID, domain.ErrConflict)
		}
		resultWinner := progressionUUIDPointer(row.ResultWinnerID)
		if (state == domain.GameStateCompleted && resultWinner == nil) || (state != domain.GameStateCompleted && resultWinner != nil) {
			return domain.Series{}, nil, fmt.Errorf("invalid Game evidence winner for %s: %w", row.GameAttemptID, domain.ErrConflict)
		}
		id := domain.OfficialResultRevisionID(row.GameResultRevisionID)
		attempt := domain.Game{ID: row.GameAttemptID, SlotID: row.SlotID, AttemptNo: int(row.AttemptNumber), State: state,
			ResultReason: reason, WinnerID: resultWinner, ResultRevisionID: &id}
		if attempt.Validate() != nil {
			return domain.Series{}, nil, fmt.Errorf("invalid restored Game %s: %w", row.GameAttemptID, domain.ErrConflict)
		}
		slot := slots[row.SlotID]
		if slot == nil {
			slot = &domain.GameSlot{ID: row.SlotID, SeriesID: evidence.SeriesID, Position: int(row.SlotNumber),
				Category: domain.Category(row.Category), ScoreBefore: domain.SeriesScore{FirstParticipantWins: int(row.FirstParticipantWinsBefore), SecondParticipantWins: int(row.SecondParticipantWinsBefore)}}
			slots[row.SlotID] = slot
		} else if slot.Position != int(row.SlotNumber) || slot.Category != domain.Category(row.Category) ||
			slot.ScoreBefore != (domain.SeriesScore{FirstParticipantWins: int(row.FirstParticipantWinsBefore), SecondParticipantWins: int(row.SecondParticipantWinsBefore)}) {
			return domain.Series{}, nil, fmt.Errorf("inconsistent Game slot %s: %w", row.SlotID, domain.ErrConflict)
		}
		for _, prior := range slot.Attempts {
			if prior.ID == attempt.ID || prior.AttemptNo == attempt.AttemptNo {
				return domain.Series{}, nil, fmt.Errorf("duplicate Game attempt %s: %w", row.GameAttemptID, domain.ErrConflict)
			}
		}
		slot.Attempts = append(slot.Attempts, attempt)
		matched = append(matched, row)
	}
	slotValues := make([]domain.GameSlot, 0, len(slots))
	for _, slot := range slots {
		sort.Slice(slot.Attempts, func(i, j int) bool { return slot.Attempts[i].AttemptNo < slot.Attempts[j].AttemptNo })
		slotValues = append(slotValues, *slot)
	}
	sort.Slice(slotValues, func(i, j int) bool { return slotValues[i].Position < slotValues[j].Position })
	for index := range slotValues {
		if slotValues[index].Position != index+1 || slotValues[index].Validate() != nil {
			return domain.Series{}, nil, fmt.Errorf("invalid Game slot position %d: %w", slotValues[index].Position, domain.ErrConflict)
		}
	}
	resultID := domain.OfficialResultRevisionID(evidence.SeriesResultRevisionID)
	scoreID := domain.SeriesScoreRevisionID(evidence.ScoreRevisionID)
	series := domain.Series{
		ID: evidence.SeriesID, TournamentID: tournamentID,
		FirstParticipantID: evidence.FirstParticipantID, SecondParticipantID: evidence.SecondParticipantID,
		Format: domain.SeriesFormat(evidence.SeriesFormat), State: domain.SeriesState(evidence.SeriesResultState),
		Score:    domain.SeriesScore{FirstParticipantWins: int(evidence.FirstParticipantWins), SecondParticipantWins: int(evidence.SecondParticipantWins)},
		WinnerID: progressionUUIDPointer(evidence.SeriesWinnerID), Slots: slotValues,
		CurrentScoreRevisionID: &scoreID, CurrentResultRevisionID: &resultID,
	}
	return series, matched, nil
}

func progressionReceiptSeriesPoints(
	receiptID uuid.UUID,
	evidence sqlc.LockTournamentProgressionFinalSwissReceiptSeriesEvidenceRow,
	ledger []sqlc.LockTournamentProgressionFinalSwissReceiptLedgerRow,
) (swissusecase.SeriesPointResult, error) {
	var first, second *sqlc.LockTournamentProgressionFinalSwissReceiptLedgerRow
	for index := range ledger {
		row := &ledger[index]
		if row.ProjectionRevisionID != receiptID || row.RoundID != evidence.RoundID || row.SourceKind != string(swissusecase.PointSourceSeries) {
			continue
		}
		if row.SourceSeriesID.Valid && row.SourceSeriesID.UUID != evidence.SeriesID {
			continue
		}
		if !row.SourceSeriesID.Valid || row.SourceSeriesID.UUID != evidence.SeriesID || !row.SeriesResultRevisionID.Valid {
			return swissusecase.SeriesPointResult{}, domain.ErrConflict
		}
		if row.SeriesResultRevisionID.UUID != evidence.SeriesResultRevisionID {
			continue
		}
		if row.ByeRevisionID.Valid || row.ResultLabel == nil ||
			row.RoundNumber < 1 || row.ParticipantID == uuid.Nil || !row.OpponentID.Valid || row.OpponentID.UUID == uuid.Nil ||
			row.Points < 0 || row.EffectiveTimeNs < 0 || row.StableSeed < 1 {
			return swissusecase.SeriesPointResult{}, domain.ErrConflict
		}
		if row.ParticipantID == evidence.FirstParticipantID {
			if first != nil || row.OpponentID.UUID != evidence.SecondParticipantID {
				return swissusecase.SeriesPointResult{}, domain.ErrConflict
			}
			first = row
		} else if row.ParticipantID == evidence.SecondParticipantID {
			if second != nil || row.OpponentID.UUID != evidence.FirstParticipantID {
				return swissusecase.SeriesPointResult{}, domain.ErrConflict
			}
			second = row
		} else {
			return swissusecase.SeriesPointResult{}, domain.ErrConflict
		}
	}
	if first == nil || second == nil || first.RoundNumber != second.RoundNumber || *first.ResultLabel != *second.ResultLabel ||
		first.AcceptedSolveTimeNs != nil && *first.AcceptedSolveTimeNs < 0 || second.AcceptedSolveTimeNs != nil && *second.AcceptedSolveTimeNs < 0 {
		return swissusecase.SeriesPointResult{}, domain.ErrConflict
	}
	label := swissusecase.SeriesResultLabel(*first.ResultLabel)
	var winner *uuid.UUID
	switch label {
	case swissusecase.SeriesResultPlayed, swissusecase.SeriesResultNoShow:
		if first.Points == swissusecase.SeriesWinPoints && second.Points == 0 {
			winner = progressionUUIDPointerValue(evidence.FirstParticipantID)
		} else if second.Points == swissusecase.SeriesWinPoints && first.Points == 0 {
			winner = progressionUUIDPointerValue(evidence.SecondParticipantID)
		} else {
			return swissusecase.SeriesPointResult{}, domain.ErrConflict
		}
	case swissusecase.SeriesResultVoid:
		if first.Points != 0 || second.Points != 0 {
			return swissusecase.SeriesPointResult{}, domain.ErrConflict
		}
	default:
		return swissusecase.SeriesPointResult{}, domain.ErrConflict
	}
	if !progressionUUIDPointerEqual(winner, progressionUUIDPointer(evidence.SeriesWinnerID)) {
		return swissusecase.SeriesPointResult{}, domain.ErrConflict
	}
	result := swissusecase.SeriesPointResult{RoundID: evidence.RoundID, RoundNumber: int(first.RoundNumber), SeriesID: evidence.SeriesID,
		ResultRevisionID: domain.OfficialResultRevisionID(evidence.SeriesResultRevisionID), FirstParticipantID: evidence.FirstParticipantID,
		SecondParticipantID: evidence.SecondParticipantID, WinnerID: winner, Label: label,
		FirstEffectiveTime: time.Duration(first.EffectiveTimeNs), SecondEffectiveTime: time.Duration(second.EffectiveTimeNs)}
	if first.AcceptedSolveTimeNs != nil {
		value := time.Duration(*first.AcceptedSolveTimeNs)
		result.FirstAcceptedSolveTime = &value
	}
	if second.AcceptedSolveTimeNs != nil {
		value := time.Duration(*second.AcceptedSolveTimeNs)
		result.SecondAcceptedSolveTime = &value
	}
	return result, nil
}

func progressionReceiptOfficialResultInput(
	command tournamentprogression.Command,
	authority tournamentprogression.Authority,
	receipt sqlc.LockTournamentProgressionFinalSwissReceiptChainRow,
	evidence sqlc.LockTournamentProgressionFinalSwissReceiptSeriesEvidenceRow,
	series domain.Series,
	gameRows []sqlc.LockTournamentProgressionFinalSwissReceiptGameEvidenceRow,
	resultProjection, scoreProjection domain.ProjectionRevision,
	rows progressionFinalSwissReceiptRows,
	nodes map[progressionReceiptNodeKey]domain.ProjectionRevision,
	sources map[progressionReceiptSourceKey]progressionReceiptSourceProjection,
) (resultprojection.OfficialResultProjectionInput, string, error) {
	switch evidence.TerminalSource {
	case string(resultprojection.TerminalResultSourceNormalNoShow):
		recorded, digest, err := progressionReceiptNoGame(
			command, authority, receipt, evidence, series, gameRows, resultProjection, scoreProjection, rows, nodes, sources,
		)
		if err != nil {
			return resultprojection.OfficialResultProjectionInput{}, "", err
		}
		return resultprojection.OfficialResultProjectionInput{TerminalSource: resultprojection.TerminalResultSourceNormalNoShow, NoGame: &recorded}, digest, nil
	case string(resultprojection.TerminalResultSourcePlayed), string(resultprojection.TerminalResultSourcePreStartForfeit):
		result, err := progressionReceiptSeriesResultHead(authority, evidence, resultProjection, rows.logicalNodes)
		if err != nil {
			return resultprojection.OfficialResultProjectionInput{}, "", err
		}
		score, err := progressionReceiptScoreHead(
			authority, receipt, evidence, series, gameRows, scoreProjection,
			rows.scoreAttempts, rows.scoreAdjudications, rows.logicalNodes,
		)
		if err != nil {
			return resultprojection.OfficialResultProjectionInput{}, "", err
		}
		terminalSource := resultprojection.TerminalResultSourcePlayed
		if evidence.TerminalSource == string(resultprojection.TerminalResultSourcePreStartForfeit) {
			terminalSource = resultprojection.TerminalResultSourcePreStartForfeit
		}
		return resultprojection.OfficialResultProjectionInput{
			TerminalSource: terminalSource, Result: result, ResultProjection: resultProjection,
			Score: &score, ScoreProjection: &scoreProjection,
		}, "", nil
	default:
		return resultprojection.OfficialResultProjectionInput{}, "", domain.ErrConflict
	}
}

func progressionReceiptSeriesResultHead(
	authority tournamentprogression.Authority,
	evidence sqlc.LockTournamentProgressionFinalSwissReceiptSeriesEvidenceRow,
	source domain.ProjectionRevision,
	logical []sqlc.LockTournamentProgressionFinalSwissReceiptLogicalResultNodesRow,
) (resultusecase.OfficialResultRevisionHead, error) {
	actor, err := progressionResultActor(evidence.SeriesResultActorKind, evidence.SeriesResultActorID)
	if err != nil || evidence.SeriesResultEventID == uuid.Nil || evidence.SeriesResultCommandID == uuid.Nil ||
		evidence.SeriesResultRevisionNumber < 1 || !evidence.SeriesResultCreatedAt.Valid || !evidence.SeriesResultOccurredAt.Valid {
		return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
	}
	state := domain.SeriesState(evidence.SeriesResultState)
	reason := domain.SeriesResultReason(evidence.SeriesResultReason)
	if !state.IsTerminal() || !reason.IsLegalFor(state) || source.Revision().Artifact() !=
		(domain.ArtifactRef{Kind: domain.ArtifactKindSeriesResult, EntityID: evidence.SeriesID}) ||
		source.Revision().ID().UUID() != evidence.SeriesResultNodeID {
		return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
	}
	var previous *domain.OfficialResultRevisionID
	if evidence.SeriesResultPreviousRevisionID.Valid {
		if evidence.SeriesResultPreviousRevisionID.UUID == uuid.Nil || evidence.SeriesResultPreviousRevisionID.UUID == evidence.SeriesResultRevisionID {
			return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
		}
		value := domain.OfficialResultRevisionID(evidence.SeriesResultPreviousRevisionID.UUID)
		previous = &value
	}
	if (evidence.SeriesResultRevisionNumber == 1) != (previous == nil) {
		return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
	}
	scoreID := domain.SeriesScoreRevisionID(evidence.ScoreRevisionID)
	recordedAt := progressionLatestTime(evidence.SeriesResultCreatedAt, evidence.SeriesResultOccurredAt)
	if recordedAt.IsZero() || recordedAt.Before(source.Revision().CreatedAt()) {
		return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
	}
	head := resultusecase.OfficialResultRevisionHead{
		Scope: resultusecase.OfficialResultScope{TournamentID: authority.Tournament.ID, SeriesID: evidence.SeriesID, Kind: resultusecase.OfficialResultSubjectSeries},
		ID:    domain.OfficialResultRevisionID(evidence.SeriesResultRevisionID), PreviousRevisionID: previous,
		Ordinal: int(evidence.SeriesResultRevisionNumber), CommandID: evidence.SeriesResultCommandID, Actor: actor,
		Outcome:          resultusecase.OfficialResultOutcome{SeriesState: state, SeriesReason: reason, WinnerID: progressionUUIDPointer(evidence.SeriesWinnerID), ScoreRevisionID: &scoreID},
		SourceProjection: source.Revision(), RecordedAt: recordedAt,
	}
	// The node index has proved ordinary authority for this exact identity pair.
	if source.Revision().ID().UUID() == evidence.SeriesResultRevisionID {
		restored, err := resultusecase.RestoreOrdinaryOfficialResultHead(head)
		if err != nil {
			return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
		}
		return restored, nil
	}
	binding, err := progressionPersistedCorrectionBinding(
		authority, receiptResultBindingInput{
			receiptID: evidence.ProjectionRevisionID, headKind: "series_result",
			seriesID: evidence.SeriesID, entityID: evidence.SeriesID, sourceID: evidence.SeriesResultRevisionID,
			nodeID: evidence.SeriesResultNodeID, headCommandID: evidence.SeriesResultCommandID,
			previousSourceID: evidence.SeriesResultPreviousRevisionID,
		}, logical,
	)
	if err != nil {
		return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
	}
	restored, err := resultusecase.RestoreCorrectionOfficialResultHead(head, binding)
	if err != nil {
		return resultusecase.OfficialResultRevisionHead{}, domain.ErrConflict
	}
	return restored, nil
}

func progressionReceiptScoreHead(
	authority tournamentprogression.Authority,
	receipt sqlc.LockTournamentProgressionFinalSwissReceiptChainRow,
	evidence sqlc.LockTournamentProgressionFinalSwissReceiptSeriesEvidenceRow,
	series domain.Series,
	gameRows []sqlc.LockTournamentProgressionFinalSwissReceiptGameEvidenceRow,
	source domain.ProjectionRevision,
	attemptRows []sqlc.LockTournamentProgressionFinalSwissReceiptScoreRevisionAttemptsRow,
	adjudications []sqlc.LockTournamentProgressionFinalSwissReceiptScoreRevisionAdjudicationsRow,
	logical []sqlc.LockTournamentProgressionFinalSwissReceiptLogicalResultNodesRow,
) (resultusecase.SeriesScoreRevisionHead, error) {
	if !evidence.ScoreResultEventID.Valid || evidence.ScoreResultEventID.UUID == uuid.Nil || evidence.ScoreCommandID == uuid.Nil || evidence.ScoreRevisionNumber < 1 ||
		!evidence.ScoreCreatedAt.Valid || !evidence.ScoreOccurredAt.Valid || source.Revision().Artifact() !=
		(domain.ArtifactRef{Kind: domain.ArtifactKindSeriesScore, EntityID: evidence.SeriesID}) ||
		source.Revision().ID().UUID() != evidence.ScoreNodeID {
		return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
	}
	actor, err := progressionResultActor(evidence.ScoreActorKind, evidence.ScoreActorID)
	if err != nil {
		return resultusecase.SeriesScoreRevisionHead{}, err
	}
	var previous *domain.SeriesScoreRevisionID
	if evidence.ScorePreviousRevisionID.Valid {
		if evidence.ScorePreviousRevisionID.UUID == uuid.Nil || evidence.ScorePreviousRevisionID.UUID == evidence.ScoreRevisionID {
			return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
		}
		value := domain.SeriesScoreRevisionID(evidence.ScorePreviousRevisionID.UUID)
		previous = &value
	}
	if (evidence.ScoreRevisionNumber == 1) != (previous == nil) {
		return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
	}
	attempts, err := progressionReceiptScoreAttempts(receipt.ProjectionRevisionID, evidence, attemptRows)
	if err != nil {
		return resultusecase.SeriesScoreRevisionHead{}, err
	}
	operation := resultusecase.SeriesScoreRevisionOperation(evidence.ScoreOperation)
	head := resultusecase.SeriesScoreRevisionHead{
		Scope: resultusecase.SeriesScoreRevisionScope{TournamentID: authority.Tournament.ID, SeriesID: evidence.SeriesID},
		ID:    domain.SeriesScoreRevisionID(evidence.ScoreRevisionID), PreviousRevisionID: previous, Ordinal: int(evidence.ScoreRevisionNumber),
		Operation: operation, CommandID: evidence.ScoreCommandID, Actor: actor,
		FirstParticipantID: evidence.FirstParticipantID, SecondParticipantID: evidence.SecondParticipantID,
		Format: domain.SeriesFormat(evidence.SeriesFormat), Score: series.Score, Attempts: attempts,
		SourceProjection: source.Revision(), RecordedAt: progressionLatestTime(evidence.ScoreCreatedAt, evidence.ScoreOccurredAt),
	}
	if head.RecordedAt.IsZero() || head.RecordedAt.Before(source.Revision().CreatedAt()) {
		return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
	}
	switch operation {
	case resultusecase.SeriesScoreRevisionOperationAppendAttempt, resultusecase.SeriesScoreRevisionOperationReplaceResult:
		if !evidence.ScoreCommandAttemptID.Valid {
			return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
		}
		for index := range attempts {
			if attempts[index].GameID == evidence.ScoreCommandAttemptID.UUID {
				value := attempts[index]
				head.CommandAttempt = &value
				break
			}
		}
		if head.CommandAttempt == nil {
			return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
		}
	case resultusecase.SeriesScoreRevisionOperationPreStartForfeit:
		adjudication, found := progressionReceiptScoreAdjudication(receipt.ProjectionRevisionID, evidence, adjudications)
		if !found || !evidence.OperatorForfeitCommitID.Valid || evidence.OperatorForfeitCommitID.UUID != adjudication.OperatorForfeitCommitID || actor.Kind != domain.ResultActorOperator {
			return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
		}
		head.TerminalEvidence = &resultusecase.SeriesScoreTerminalEvidence{Source: resultusecase.SeriesScoreTerminalSourcePreStartForfeit,
			CommitID: adjudication.OperatorForfeitCommitID, ForfeitingParticipantID: adjudication.ForfeitingParticipantID,
			WinnerID: adjudication.WinnerID, AnchorAttemptID: adjudication.AnchorAttemptID}
	case resultusecase.SeriesScoreRevisionOperationNoShow:
		if !evidence.NormalNoShowCommitID.Valid {
			return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
		}
		head.TerminalEvidence = &resultusecase.SeriesScoreTerminalEvidence{Source: resultusecase.SeriesScoreTerminalSourceNormalNoShow,
			CommitID: evidence.NormalNoShowCommitID.UUID}
	case resultusecase.SeriesScoreRevisionOperationInitialize:
		// Terminal receipts cannot name a genesis score head.
		return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
	default:
		return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
	}
	if source.Revision().ID().UUID() == evidence.ScoreRevisionID {
		restored, err := resultusecase.RestoreOrdinarySeriesScoreHead(head)
		if err != nil {
			return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
		}
		return restored, nil
	}
	binding, err := progressionPersistedCorrectionBinding(
		authority, receiptResultBindingInput{
			receiptID: evidence.ProjectionRevisionID, headKind: "series_score",
			seriesID: evidence.SeriesID, entityID: evidence.SeriesID, sourceID: evidence.ScoreRevisionID,
			nodeID: evidence.ScoreNodeID, headCommandID: evidence.ScoreCommandID,
			previousSourceID: evidence.ScorePreviousRevisionID,
		}, logical,
	)
	if err != nil {
		return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
	}
	restored, err := resultusecase.RestoreCorrectionSeriesScoreHead(head, binding)
	if err != nil {
		return resultusecase.SeriesScoreRevisionHead{}, domain.ErrConflict
	}
	return restored, nil
}

type receiptResultBindingInput struct {
	receiptID        uuid.UUID
	headKind         string
	seriesID         uuid.UUID
	entityID         uuid.UUID
	sourceID         uuid.UUID
	nodeID           uuid.UUID
	headCommandID    uuid.UUID
	previousSourceID uuid.NullUUID
}

func progressionPersistedCorrectionBinding(
	authority tournamentprogression.Authority,
	input receiptResultBindingInput,
	rows []sqlc.LockTournamentProgressionFinalSwissReceiptLogicalResultNodesRow,
) (resultusecase.PersistedCorrectionSourceBinding, error) {
	var matched *sqlc.LockTournamentProgressionFinalSwissReceiptLogicalResultNodesRow
	for index := range rows {
		row := &rows[index]
		if row.ReceiptProjectionRevisionID != input.receiptID || row.HeadKind != input.headKind ||
			row.EntityID != input.entityID || row.SourceID != input.sourceID || row.NodeID != input.nodeID {
			continue
		}
		if matched != nil {
			return resultusecase.PersistedCorrectionSourceBinding{}, domain.ErrConflict
		}
		matched = row
	}
	if matched == nil || matched.SourceKind != "correction_commit" || !matched.CorrectionCommandID.Valid ||
		matched.CorrectionCommandID.UUID == uuid.Nil || !matched.PreviousNodeID.Valid ||
		matched.PreviousNodeID.UUID == uuid.Nil || !input.previousSourceID.Valid ||
		input.previousSourceID.UUID == uuid.Nil || matched.RevisionNumber < 1 ||
		int64(int(matched.RevisionNumber)) != matched.RevisionNumber {
		return resultusecase.PersistedCorrectionSourceBinding{}, domain.ErrConflict
	}
	kind := domain.ArtifactKind(matched.ArtifactKind)
	return resultusecase.PersistedCorrectionSourceBinding{
		TournamentID: authority.Tournament.ID, RosterID: authority.Tournament.RosterID,
		SeriesID: input.seriesID, EntityID: input.entityID, ArtifactKind: kind,
		CorrectionCommandID: matched.CorrectionCommandID.UUID, HeadCommandID: input.headCommandID,
		SourceID: matched.SourceID,
		NodeID:   matched.NodeID, PreviousSourceID: input.previousSourceID.UUID,
		PreviousNodeID: matched.PreviousNodeID.UUID, NodeRevision: int(matched.RevisionNumber),
	}, nil
}

func progressionReceiptScoreAttempts(
	receiptID uuid.UUID,
	evidence sqlc.LockTournamentProgressionFinalSwissReceiptSeriesEvidenceRow,
	rows []sqlc.LockTournamentProgressionFinalSwissReceiptScoreRevisionAttemptsRow,
) ([]resultusecase.SeriesScoreAttemptReference, error) {
	attempts := make([]resultusecase.SeriesScoreAttemptReference, 0)
	for _, row := range rows {
		if row.ProjectionRevisionID != receiptID || row.SeriesID != evidence.SeriesID || row.ScoreRevisionID != evidence.ScoreRevisionID {
			continue
		}
		if row.Position < 1 || row.SlotID == uuid.Nil || row.SlotPosition < 1 || row.GameAttemptID == uuid.Nil ||
			row.AttemptNumber < 1 || row.GameResultRevisionID == uuid.Nil || row.ResultEventID == uuid.Nil ||
			!row.OccurredAt.Valid || !row.CreatedAt.Valid {
			return nil, domain.ErrConflict
		}
		state := domain.GameState(row.ResultState)
		reason := domain.GameResultReason(row.ResultReason)
		ref := resultusecase.SeriesScoreAttemptReference{SlotID: row.SlotID, SlotPosition: int(row.SlotPosition), GameID: row.GameAttemptID,
			AttemptNo: int(row.AttemptNumber), State: state, WinnerID: progressionUUIDPointer(row.WinnerID), Reason: reason,
			CurrentGameResultRevisionID: domain.OfficialResultRevisionID(row.GameResultRevisionID)}
		if ref.Validate() != nil || int(row.Position) != len(attempts)+1 {
			return nil, domain.ErrConflict
		}
		attempts = append(attempts, ref)
	}
	return attempts, nil
}

func progressionReceiptScoreAdjudication(
	receiptID uuid.UUID,
	evidence sqlc.LockTournamentProgressionFinalSwissReceiptSeriesEvidenceRow,
	rows []sqlc.LockTournamentProgressionFinalSwissReceiptScoreRevisionAdjudicationsRow,
) (sqlc.LockTournamentProgressionFinalSwissReceiptScoreRevisionAdjudicationsRow, bool) {
	var found sqlc.LockTournamentProgressionFinalSwissReceiptScoreRevisionAdjudicationsRow
	for _, row := range rows {
		if row.ProjectionRevisionID != receiptID || row.SeriesID != evidence.SeriesID || row.ScoreRevisionID != evidence.ScoreRevisionID {
			continue
		}
		if found.OperatorForfeitCommitID != uuid.Nil || row.OperatorForfeitCommitID == uuid.Nil || row.CommandID == uuid.Nil ||
			row.ActorID == uuid.Nil || row.AnchorAttemptID == uuid.Nil || row.ForfeitingParticipantID == uuid.Nil || row.WinnerID == uuid.Nil ||
			row.ForfeitingParticipantID == row.WinnerID || row.SourceProjectionRevisionID == uuid.Nil || row.SourceProjectionRevision < 1 ||
			!row.CreatedAt.Valid {
			return sqlc.LockTournamentProgressionFinalSwissReceiptScoreRevisionAdjudicationsRow{}, false
		}
		found = row
	}
	return found, found.OperatorForfeitCommitID != uuid.Nil
}

func progressionReceiptNoGame(
	command tournamentprogression.Command,
	authority tournamentprogression.Authority,
	receipt sqlc.LockTournamentProgressionFinalSwissReceiptChainRow,
	evidence sqlc.LockTournamentProgressionFinalSwissReceiptSeriesEvidenceRow,
	series domain.Series,
	gameRows []sqlc.LockTournamentProgressionFinalSwissReceiptGameEvidenceRow,
	resultProjection, scoreProjection domain.ProjectionRevision,
	rows progressionFinalSwissReceiptRows,
	nodes map[progressionReceiptNodeKey]domain.ProjectionRevision,
	sources map[progressionReceiptSourceKey]progressionReceiptSourceProjection,
) (resultprojection.RecordedNoGameResult, string, error) {
	if !evidence.NormalNoShowCommitID.Valid || evidence.NormalNoShowCommitID.UUID == uuid.Nil {
		return resultprojection.RecordedNoGameResult{}, "", domain.ErrConflict
	}
	commitRows := make([]sqlc.LockTournamentProgressionFinalSwissReceiptNormalNoShowCommitsRow, 0)
	for _, row := range rows.normalNoShowCommits {
		if row.ProjectionRevisionID == receipt.ProjectionRevisionID && row.SeriesID == evidence.SeriesID {
			commitRows = append(commitRows, row)
		}
	}
	if len(commitRows) == 0 {
		return resultprojection.RecordedNoGameResult{}, "", domain.ErrConflict
	}
	first := commitRows[0]
	resolvedAt, valid := progressionReceiptTime(first.ResolvedAt)
	if !valid || first.CommitID != evidence.NormalNoShowCommitID.UUID || first.RoundID != evidence.RoundID ||
		first.WaveID == uuid.Nil || first.ReadyWindowID == uuid.Nil || first.ReadyWindowRevisionID == uuid.Nil ||
		first.ResultEventID == uuid.Nil || first.SeriesScoreRevisionID != evidence.ScoreRevisionID ||
		first.SeriesResultRevisionID != evidence.SeriesResultRevisionID || first.CommandID == uuid.Nil ||
		!validNormalNoShowAction(first.Action) || len(first.PayloadDigest) != sha256.Size {
		return resultprojection.RecordedNoGameResult{}, "", domain.ErrConflict
	}
	digest := sha256.Sum256(first.PayloadDigest)
	if digest == ([sha256.Size]byte{}) {
		return resultprojection.RecordedNoGameResult{}, "", domain.ErrConflict
	}
	commitGames := make(map[uuid.UUID]sqlc.LockTournamentProgressionFinalSwissReceiptNormalNoShowCommitsRow, len(commitRows))
	for _, row := range commitRows {
		if row.CommitID != first.CommitID || row.WaveID != first.WaveID || row.ReadyWindowID != first.ReadyWindowID ||
			row.ReadyWindowRevisionID != first.ReadyWindowRevisionID || row.ResultEventID != first.ResultEventID ||
			row.SeriesScoreRevisionID != first.SeriesScoreRevisionID || row.SeriesResultRevisionID != first.SeriesResultRevisionID ||
			row.CommandID != first.CommandID || row.Action != first.Action || !row.ResolvedAt.Valid ||
			!row.ResolvedAt.Time.UTC().Equal(resolvedAt) || !progressionBytesEqual(row.PayloadDigest, first.PayloadDigest) ||
			row.GameAttemptID == uuid.Nil || row.GameResultRevisionID == uuid.Nil || row.Position < 1 {
			return resultprojection.RecordedNoGameResult{}, "", domain.ErrConflict
		}
		if _, duplicate := commitGames[row.GameAttemptID]; duplicate {
			return resultprojection.RecordedNoGameResult{}, "", domain.ErrConflict
		}
		commitGames[row.GameAttemptID] = row
	}
	if len(commitGames) != len(gameRows) {
		return resultprojection.RecordedNoGameResult{}, "", domain.ErrConflict
	}
	gameResults := make([]domain.NormalNoShowGameRevision, 0, len(gameRows))
	topology := make([]resultprojection.RecordedNoGameAttempt, 0, len(gameRows))
	gameSources := make([]domain.DerivedRevision, 0, len(gameRows))
	gameProjections := make([]domain.ProjectionRevision, 0, len(gameRows))
	dependencies := make([]domain.RevisionDependency, 0, len(gameRows))
	gameResultIDs := make([]domain.OfficialResultRevisionID, 0, len(gameRows))
	sort.Slice(gameRows, func(i, j int) bool {
		if gameRows[i].SlotNumber != gameRows[j].SlotNumber {
			return gameRows[i].SlotNumber < gameRows[j].SlotNumber
		}
		return gameRows[i].AttemptNumber < gameRows[j].AttemptNumber
	})
	for index, game := range gameRows {
		if err := progressionRequireReceiptSource(sources, receipt.ProjectionRevisionID, domain.ArtifactKindGameResult, game.GameAttemptID, game.GameResultRevisionID); err != nil {
			return resultprojection.RecordedNoGameResult{}, "", err
		}
		commit, found := commitGames[game.GameAttemptID]
		if !found || commit.GameResultRevisionID != game.GameResultRevisionID || int(commit.Position) != index+1 ||
			game.GameResultPreviousRevisionID.Valid || game.GameResultRevisionNumber != 1 ||
			!game.GameResultCreatedAt.Valid || !game.GameResultCreatedAt.Time.UTC().Equal(resolvedAt) ||
			!game.ResultOccurredAt.Valid || !game.ResultOccurredAt.Time.UTC().Equal(resolvedAt) ||
			domain.GameState(game.ResultState) != domain.GameStateCancelled ||
			domain.GameResultReason(game.ResultReason) != domain.GameResultReasonSeriesCancelled {

			return resultprojection.RecordedNoGameResult{}, "", domain.ErrConflict
		}
		projection, found := nodes[progressionReceiptNodeKey{receiptID: receipt.ProjectionRevisionID, nodeID: game.GameResultNodeID}]
		if !found || projection.Revision().Artifact() != (domain.ArtifactRef{Kind: domain.ArtifactKindGameResult, EntityID: game.GameAttemptID}) ||
			projection.Revision().RevisionNo() != int(game.GameResultNodeRevision) ||
			!projection.Revision().CreatedAt().Equal(game.GameResultCreatedAt.Time.UTC()) ||
			progressionRequireLogicalReceiptNode(rows.logicalNodes, receipt.ProjectionRevisionID, "game_result", game.GameAttemptID, game.GameResultRevisionID, game.GameResultNodeID) != nil {
			return resultprojection.RecordedNoGameResult{}, "", domain.ErrConflict
		}
		gameResults = append(gameResults, domain.NormalNoShowGameRevision{Ordinal: index + 1,
			ID: domain.OfficialResultRevisionID(game.GameResultRevisionID), GameID: game.GameAttemptID,
			State: domain.GameStateCancelled, Reason: domain.GameResultReasonSeriesCancelled, RecordedAt: resolvedAt})
		topology = append(topology, resultprojection.RecordedNoGameAttempt{SeriesID: evidence.SeriesID, SlotID: game.SlotID,
			SlotPosition: int(game.SlotNumber), GameID: game.GameAttemptID, AttemptNo: int(game.AttemptNumber),
			ResultRevisionID: domain.OfficialResultRevisionID(game.GameResultRevisionID)})
		gameSources = append(gameSources, projection.Revision())
		gameProjections = append(gameProjections, projection)
		gameResultIDs = append(gameResultIDs, domain.OfficialResultRevisionID(game.GameResultRevisionID))
		dependencies = append(dependencies, domain.RevisionDependency{SourceRevisionID: projection.Revision().ID(), DerivedRevisionID: scoreProjection.Revision().ID()})
	}
	if !progressionReceiptDependenciesExist(rows.dependencies, dependencies) {
		return resultprojection.RecordedNoGameResult{}, "", domain.ErrConflict
	}
	resultDependency := domain.RevisionDependency{SourceRevisionID: scoreProjection.Revision().ID(), DerivedRevisionID: resultProjection.Revision().ID()}
	if !progressionReceiptDependenciesExist(rows.dependencies, []domain.RevisionDependency{resultDependency}) {
		return resultprojection.RecordedNoGameResult{}, "", domain.ErrConflict
	}
	scorePrevious := progressionScoreRevisionPointer(evidence.ScorePreviousRevisionID)
	resultPrevious := progressionOfficialResultPointer(evidence.SeriesResultPreviousRevisionID)
	// SQL ordinals belong to each head. No-show event order is a separate
	// sequence: cancelled Games, then the score, then the Series result.
	if evidence.ScoreRevisionNumber != 2 || evidence.SeriesResultRevisionNumber != 1 || scorePrevious == nil || resultPrevious != nil ||
		!evidence.ScoreCreatedAt.Valid || !evidence.ScoreCreatedAt.Time.UTC().Equal(resolvedAt) ||
		!evidence.SeriesResultCreatedAt.Valid || !evidence.SeriesResultCreatedAt.Time.UTC().Equal(resolvedAt) {
		return resultprojection.RecordedNoGameResult{}, "", domain.ErrConflict
	}
	var ready *uuid.UUID
	action := domain.NormalNoShowAction(first.Action)
	if action == domain.NormalNoShowActionReopenWave {
		ready = progressionUUIDPointer(evidence.SeriesWinnerID)
		if ready == nil {
			return resultprojection.RecordedNoGameResult{}, "", domain.ErrConflict
		}
	}
	recorded := resultprojection.RecordedNoGameResult{
		Scope:     domain.NormalNoShowScope{TournamentID: authority.Tournament.ID, WaveID: first.WaveID, WindowID: first.ReadyWindowID, SeriesID: evidence.SeriesID},
		CommandID: first.CommandID, Action: action, Format: domain.SeriesFormat(evidence.SeriesFormat),
		FirstParticipantID: evidence.FirstParticipantID, SecondParticipantID: evidence.SecondParticipantID, ReadyParticipantID: ready,
		GameResults: gameResults, Topology: topology,
		Score: domain.NormalNoShowScoreRevision{Ordinal: len(gameResults) + 1, ID: domain.SeriesScoreRevisionID(evidence.ScoreRevisionID),
			SeriesID: evidence.SeriesID, PreviousRevisionID: scorePrevious, Score: series.Score, GameResultRevisionIDs: gameResultIDs, RecordedAt: resolvedAt},
		Series: domain.NormalNoShowSeriesRevision{Ordinal: len(gameResults) + 2, ID: domain.OfficialResultRevisionID(evidence.SeriesResultRevisionID),
			SeriesID: evidence.SeriesID, PreviousRevisionID: resultPrevious, State: domain.SeriesState(evidence.SeriesResultState),
			WinnerID: progressionUUIDPointer(evidence.SeriesWinnerID), ScoreRevisionID: domain.SeriesScoreRevisionID(evidence.ScoreRevisionID), RecordedAt: resolvedAt},
		GameSourceRevisions: gameSources, ScoreSourceRevision: scoreProjection.Revision(), ResultSourceRevision: resultProjection.Revision(),
		GameProjections: gameProjections, GameDependencies: dependencies, ScoreProjection: scoreProjection,
		ResultProjection: resultProjection, ResultDependency: resultDependency, ResolvedAt: resolvedAt,
	}
	recorded, err := resultprojection.RestoreSQLNoGameResult(recorded)
	if err != nil {
		return resultprojection.RecordedNoGameResult{}, "", fmt.Errorf("restore no-show source identity: %w", err)
	}
	if _, err := resultprojection.ProjectOfficialResult(resultprojection.OfficialResultProjectionInput{TerminalSource: resultprojection.TerminalResultSourceNormalNoShow, NoGame: &recorded}); err != nil {
		return resultprojection.RecordedNoGameResult{}, "", domain.ErrConflict
	}
	return recorded, hex.EncodeToString(first.PayloadDigest), nil
}

func validNormalNoShowAction(value string) bool {
	return value == string(domain.NormalNoShowActionReopenWave) || value == string(domain.NormalNoShowActionPauseWave)
}

func progressionReceiptDependenciesExist(rows []sqlc.LockTournamentProgressionFinalSwissReceiptProjectionDependenciesRow, wanted []domain.RevisionDependency) bool {
	for _, dependency := range wanted {
		found := false
		for _, row := range rows {
			if row.SourceNodeID == dependency.SourceRevisionID.UUID() && row.DerivedNodeID == dependency.DerivedRevisionID.UUID() {
				if found {
					return false
				}
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func progressionScoreRevisionPointer(value uuid.NullUUID) *domain.SeriesScoreRevisionID {
	if !value.Valid || value.UUID == uuid.Nil {
		return nil
	}
	result := domain.SeriesScoreRevisionID(value.UUID)
	return &result
}

func progressionOfficialResultPointer(value uuid.NullUUID) *domain.OfficialResultRevisionID {
	if !value.Valid || value.UUID == uuid.Nil {
		return nil
	}
	result := domain.OfficialResultRevisionID(value.UUID)
	return &result
}

func progressionResultActor(kind string, principal uuid.NullUUID) (domain.ResultActor, error) {
	actor := domain.ResultActor{Kind: domain.ResultActorKind(kind)}
	if principal.Valid {
		if principal.UUID == uuid.Nil {
			return domain.ResultActor{}, domain.ErrConflict
		}
		value := principal.UUID
		actor.PrincipalID = &value
	}
	if actor.Validate() != nil {
		return domain.ResultActor{}, domain.ErrConflict
	}
	return actor, nil
}

func progressionUUIDPointer(value uuid.NullUUID) *uuid.UUID {
	if !value.Valid || value.UUID == uuid.Nil {
		return nil
	}
	copy := value.UUID
	return &copy
}

func progressionUUIDPointerValue(value uuid.UUID) *uuid.UUID {
	if value == uuid.Nil {
		return nil
	}
	copy := value
	return &copy
}

func progressionUUIDPointerEqual(first, second *uuid.UUID) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func progressionLatestTime(values ...pgtype.Timestamptz) time.Time {
	var latest time.Time
	for _, value := range values {
		parsed, valid := progressionReceiptTime(value)
		if !valid {
			return time.Time{}
		}
		if parsed.After(latest) {
			latest = parsed
		}
	}
	return latest
}

// Keep pgtype imported in this file while the exact terminal mappers below
// use a uniform persisted timestamp predicate.
func progressionReceiptTime(value pgtype.Timestamptz) (time.Time, bool) {
	if !value.Valid || !domain.IsValidServerTime(value.Time.UTC()) {
		return time.Time{}, false
	}
	return value.Time.UTC(), true
}
