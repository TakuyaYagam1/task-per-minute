package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"

	assignmentrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/assignment"
	projectionrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/projection"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const goldenRuntimePlanAlgorithm = "golden-runtime-exact-v1"

type goldenRuntimePlanCandidate struct {
	version  sqlc.TaskVersion
	digest   [sha256.Size]byte
	snapshot domain.AssignmentTaskSnapshot
}

//nolint:gocyclo // Golden planning validates and seals one immutable production authority boundary.
func (repository *GoldenRuntimePostgres) materializeGoldenRuntimePlan(
	ctx context.Context,
	querier *sqlc.Queries,
	groups []goldenRuntimeGroup,
	now time.Time,
) error {
	if repository == nil || repository.tx == nil || querier == nil || len(groups) == 0 ||
		!domain.IsValidServerTime(now) {
		return domain.ErrValidation
	}
	first := groups[0]
	content, err := querier.GetCurrentTournamentContentConfiguration(ctx, first.tournamentID)
	if err != nil {
		return goldenRuntimeReadError("load Golden content", err)
	}
	source, err := querier.LoadGoldenRuntimePlanRoster(ctx, first.tournamentID)
	if err != nil {
		return goldenRuntimeReadError("load Golden plan roster", err)
	}
	if source.RosterID != first.rosterID || source.RosterRevision < 1 ||
		content.TournamentID != first.tournamentID || content.GoldenPoolRevisionID == uuid.Nil ||
		content.GoldenPoolRevision < 1 {
		return domain.ErrConflict
	}
	exists, err := querier.HasGoldenRuntimePlanSnapshot(ctx, sqlc.HasGoldenRuntimePlanSnapshotParams{
		TournamentID: first.tournamentID, RosterID: first.rosterID,
		SourceProjectionRevisionID: first.sourceProjectionRevisionID,
		SourceProjectionRevision:   first.sourceProjectionRevision,
	})
	if err != nil {
		return goldenRuntimeReadError("load Golden exact plan", err)
	}
	if exists {
		return nil
	}

	candidates, err := repository.goldenRuntimePlanCandidates(ctx, querier, first.tournamentID, content.GoldenPoolRevisionID)
	if err != nil {
		return err
	}
	requiredCandidates := len(groups) * (domain.AssignmentReserveCount + 1)
	if len(candidates) < requiredCandidates {
		return domain.ErrConflict
	}
	candidates = candidates[:requiredCandidates]

	exactPlanID := goldenRuntimePlanID(first.sourceProjectionRevisionID, "exact")
	exactRevisionID := goldenRuntimePlanID(exactPlanID, "revision")
	branches := make([]assignmentrepo.AssignmentBranchInput, len(groups))
	decisionInputs := make([]string, 0, requiredCandidates)
	for groupIndex, group := range groups {
		if group.tournamentID != first.tournamentID || group.rosterID != first.rosterID ||
			group.sourceProjectionRevisionID != first.sourceProjectionRevisionID ||
			group.sourceProjectionRevision != first.sourceProjectionRevision {
			return domain.ErrConflict
		}
		selected := candidates[groupIndex*(domain.AssignmentReserveCount+1) : (groupIndex+1)*(domain.AssignmentReserveCount+1)]
		categories := make([]domain.Category, 0, len(selected))
		for _, candidate := range selected {
			if !slices.Contains(categories, candidate.snapshot.Category) {
				categories = append(categories, candidate.snapshot.Category)
			}
		}
		branch := assignmentrepo.AssignmentBranchInput{
			ID: goldenRuntimePlanID(group.groupRevisionID, "branch"), Key: "golden-" + strconv.Itoa(groupIndex+1),
			Categories: categories, Edges: make([]assignmentrepo.AssignmentEdgeInput, len(selected)),
		}
		for edgeIndex, candidate := range selected {
			edgeID := goldenRuntimePlanID(group.groupRevisionID, "edge-"+strconv.Itoa(edgeIndex+1))
			reservationID := goldenRuntimePlanID(edgeID, "reservation")
			snapshot := candidate.snapshot
			snapshot.SnapshotID = goldenRuntimePlanID(edgeID, "snapshot")
			branch.Edges[edgeIndex] = assignmentrepo.AssignmentEdgeInput{
				ID: edgeID, ReservationID: reservationID, Position: edgeIndex + 1,
				Snapshot: snapshot, ContentDigest: candidate.digest,
				SelectionEvidence: map[string]any{
					"algorithm": goldenRuntimePlanAlgorithm,
					"group_id":  group.groupID.String(),
				},
			}
			decisionInputs = append(decisionInputs, fmt.Sprintf("%s@%d", candidate.snapshot.TaskID, candidate.snapshot.Version))
		}
		branches[groupIndex] = branch
	}
	decision, err := domain.NewDecisionEvidence(
		goldenRuntimePlanID(exactPlanID, "decision"), domain.DecisionPurposeTask,
		domain.DecisionAlgorithmV1, decisionInputs, exactPlanID, now,
	)
	if err != nil {
		return fmt.Errorf("materialize Golden decision: %w", err)
	}
	if err := repository.persistGoldenRuntimeAssignmentPlan(
		ctx, querier, first, source.RosterRevision, content.GoldenPoolRevisionID,
		exactPlanID, exactRevisionID, decision, branches, now,
	); err != nil {
		return err
	}
	return repository.persistGoldenRuntimePlanSnapshot(
		ctx, querier, groups, branches, candidates, content, exactPlanID, exactRevisionID, now,
	)
}

func goldenRuntimeReservedTaskVersions(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
) (map[domain.TaskVersionRef]struct{}, error) {
	reservedRows, err := querier.ListTournamentReservedTaskVersions(ctx, tournamentID)
	if err != nil {
		return nil, goldenRuntimeReadError("load Golden reservations", err)
	}
	reserved := make(map[domain.TaskVersionRef]struct{}, len(reservedRows))
	for _, row := range reservedRows {
		if row.TaskID == uuid.Nil || row.TaskVersion < 1 {
			return nil, domain.ErrConflict
		}
		reserved[domain.TaskVersionRef{TaskID: row.TaskID, Version: int(row.TaskVersion)}] = struct{}{}
	}
	return reserved, nil
}

func (repository *GoldenRuntimePostgres) goldenRuntimePlanCandidates(
	ctx context.Context,
	querier *sqlc.Queries,
	tournamentID uuid.UUID,
	poolRevisionID uuid.UUID,
) ([]goldenRuntimePlanCandidate, error) {
	reserved, err := goldenRuntimeReservedTaskVersions(ctx, querier, tournamentID)
	if err != nil {
		return nil, err
	}
	health, err := querier.ListTaskPoolVersionHealth(ctx, []uuid.UUID{poolRevisionID})
	if err != nil {
		return nil, goldenRuntimeReadError("load Golden candidates", err)
	}
	candidates := make([]goldenRuntimePlanCandidate, 0, len(health))
	for _, item := range health {
		if item.PoolRevisionID != poolRevisionID || item.PoolKind != string(domain.TaskKindGolden) ||
			!item.TaskExists || !item.TaskEnabled || !item.TaskHealthy || item.TaskPubliclyExposed {
			continue
		}
		if _, unavailable := reserved[domain.TaskVersionRef{TaskID: item.TaskID, Version: int(item.TaskVersion)}]; unavailable {
			continue
		}
		version, loadErr := querier.GetTaskVersion(ctx, sqlc.GetTaskVersionParams{
			TaskID: item.TaskID, Version: item.TaskVersion,
		})
		if loadErr != nil {
			return nil, goldenRuntimeReadError("load Golden task version", loadErr)
		}
		if len(version.ContentDigest) != sha256.Size || version.TimeLimit != int32(goldenRuntimeDuration/time.Second) {
			continue
		}
		var digest [sha256.Size]byte
		copy(digest[:], version.ContentDigest)
		snapshot := domain.AssignmentTaskSnapshot{
			TaskID: version.TaskID, Version: int(version.Version), Kind: domain.AssignmentTaskKindGolden,
			Title: version.Title, Description: version.Description, Category: domain.Category(version.Category),
			Difficulty: domain.Difficulty(version.Difficulty), TimeLimit: int(version.TimeLimit), Flag: version.Flag,
			Hints:   taskHintsToDomain(version.Hint1, version.Hint2, version.Hint3),
			TaskURL: version.TaskUrl, SourceFileURL: version.SourceFileUrl,
		}
		candidates = append(candidates, goldenRuntimePlanCandidate{
			version: version, digest: digest, snapshot: snapshot,
		})
	}
	return candidates, nil
}

func (repository *GoldenRuntimePostgres) persistGoldenRuntimeAssignmentPlan(
	ctx context.Context,
	querier *sqlc.Queries,
	group goldenRuntimeGroup,
	rosterRevision int64,
	poolRevisionID uuid.UUID,
	planID uuid.UUID,
	planRevisionID uuid.UUID,
	decision domain.DecisionEvidence,
	branches []assignmentrepo.AssignmentBranchInput,
	now time.Time,
) error {
	constraintGraph, err := requiredJSONObject(map[string]any{
		"kind": "golden", "group_count": len(branches),
	})
	if err != nil {
		return err
	}
	proofEvidence, err := requiredJSONObject(map[string]any{
		"source_projection_revision": group.sourceProjectionRevision,
	})
	if err != nil {
		return err
	}
	decisionInputs, err := marshalJSON("Golden plan decision inputs", decision.NormalizedInputs)
	if err != nil {
		return err
	}
	decisionResult, err := marshalJSON("Golden plan decision result", decision.Result)
	if err != nil {
		return err
	}
	proofDigest := goldenRuntimePlanDigest("assignment-proof", planID, group.sourceProjectionRevisionID)
	proofHash := hex.EncodeToString(proofDigest[:])
	if err := querier.CreateGoldenRuntimeAssignmentPlan(ctx, sqlc.CreateGoldenRuntimeAssignmentPlanParams{
		ID: planID, TournamentID: group.tournamentID, RosterID: group.rosterID,
		RevisionID: planRevisionID, SourceRosterRevision: rosterRevision,
		SourcePoolRevisionID: poolRevisionID, ReachableBranchCount: int32(len(branches)), //nolint:gosec // Golden groups are bounded by the roster size.
		ConstraintGraph: constraintGraph, ProofEvidence: proofEvidence, ProofHash: &proofHash,
		DecisionEvidenceID:       uuid.NullUUID{UUID: decision.ID, Valid: true},
		DecisionAlgorithmVersion: &decision.AlgorithmVersion, DecisionInputs: decisionInputs,
		DecisionSeed: append([]byte(nil), decision.Seed[:]...), DecisionResult: decisionResult,
		DecisionReplayDigest: append([]byte(nil), decision.ReplayDigest[:]...),
		DecisionOwnerID:      uuid.NullUUID{UUID: decision.OwnerID, Valid: true},
		DecidedAt:            tstz(decision.DecidedAt), CreatedAt: tstz(now),
	}); err != nil {
		return goldenRuntimeWriteError("create Golden assignment plan", err)
	}
	for _, branch := range branches {
		categories, err := categoryJSON(branch.Categories)
		if err != nil {
			return err
		}
		if err := querier.CreateGoldenRuntimeAssignmentBranch(ctx, sqlc.CreateGoldenRuntimeAssignmentBranchParams{
			ID: branch.ID, PlanID: planID, BranchKey: branch.Key,
			CategorySequence: categories, CreatedAt: tstz(now),
		}); err != nil {
			return goldenRuntimeWriteError("create Golden assignment branch", err)
		}
		for _, edge := range branch.Edges {
			selectionEvidence, err := requiredJSONObject(edge.SelectionEvidence)
			if err != nil {
				return err
			}
			if _, err := querier.CreateAssignmentPlanEdge(ctx, sqlc.CreateAssignmentPlanEdgeParams{
				ID: edge.ID, PlanID: planID, BranchID: branch.ID,
				Position: int16(edge.Position), TaskID: edge.Snapshot.TaskID, //nolint:gosec // Edge positions are generated in the closed interval 1..3.
				TaskVersion: int32(edge.Snapshot.Version), SelectionEvidence: selectionEvidence, //nolint:gosec // Persisted task versions are PostgreSQL int4 values.
				CreatedAt: tstz(now),
			}); err != nil {
				return goldenRuntimeWriteError("create Golden assignment edge", err)
			}
			if _, err := querier.CreateAssignmentTaskVersionReservation(ctx, sqlc.CreateAssignmentTaskVersionReservationParams{
				ID: edge.ReservationID, EdgeID: edge.ID, PlanID: planID, BranchID: branch.ID,
				TaskID: edge.Snapshot.TaskID, TaskVersion: int32(edge.Snapshot.Version), //nolint:gosec // Persisted task versions are PostgreSQL int4 values.
				CreatedAt: tstz(now),
			}); err != nil {
				return goldenRuntimeWriteError("create Golden task reservation", err)
			}
			snapshot, err := taskSnapshotParams(edge, now)
			if err != nil {
				return err
			}
			if _, err := querier.CreateAssignmentTaskSnapshot(ctx, snapshot); err != nil {
				return goldenRuntimeWriteError("create Golden task snapshot", err)
			}
		}
	}
	return nil
}

//nolint:gocyclo // The sealed Golden snapshot keeps every source revision and reservation fail-closed in one transaction.
func (repository *GoldenRuntimePostgres) persistGoldenRuntimePlanSnapshot(
	ctx context.Context,
	querier *sqlc.Queries,
	groups []goldenRuntimeGroup,
	branches []assignmentrepo.AssignmentBranchInput,
	candidates []goldenRuntimePlanCandidate,
	content sqlc.GetCurrentTournamentContentConfigurationRow,
	planID uuid.UUID,
	planRevisionID uuid.UUID,
	now time.Time,
) error {
	first := groups[0]
	record, err := projectionrepo.LoadProjectionRecord(ctx, querier, projectionrepo.ProjectionScope{
		TournamentID: first.tournamentID, RosterID: first.rosterID,
	}, first.sourceProjectionRevisionID)
	if err != nil {
		return err
	}
	if record.Revision.RevisionNumber != first.sourceProjectionRevision {
		return domain.ErrConflict
	}
	var standings projectionrepo.ProjectionArtifactRecord
	foundStandings := false
	for _, artifact := range record.Artifacts {
		if artifact.Artifact.ArtifactKind == string(domain.ArtifactKindStandings) {
			standings = artifact
			foundStandings = true
			break
		}
	}
	if !foundStandings || len(standings.Artifact.PayloadDigest) != sha256.Size {
		return domain.ErrConflict
	}
	proofDigest := goldenRuntimePlanDigest("proof", planID, first.sourceProjectionRevisionID)
	rootDigest := goldenRuntimePlanDigest("authority", planID, content.GoldenPoolRevisionID)
	if _, err = querier.CreateGoldenExactPlanSnapshot(ctx, sqlc.CreateGoldenExactPlanSnapshotParams{
		PlanID: planID, PlanRevisionID: planRevisionID, TournamentID: first.tournamentID, RosterID: first.rosterID,
		PlanSetID: goldenRuntimePlanID(planID, "set"), SourceProjectionRevisionID: first.sourceProjectionRevisionID,
		SourceProjectionRevision:           first.sourceProjectionRevision,
		SourceProjectionPreviousRevisionID: record.Revision.PreviousRevisionID,
		SourceStandingsArtifactID:          standings.Artifact.ID, SourceStandingsPayloadDigest: standings.Artifact.PayloadDigest,
		GroupSetRevisionID: goldenRuntimePlanID(planID, "groups"), GroupSetRevision: 1,
		PoolRevisionID: content.GoldenPoolRevisionID, PoolRevision: content.GoldenPoolRevision,
		HistoryRevisionID: goldenRuntimePlanID(planID, "history"), HistoryRevision: 1,
		TaskHealthRevisionID: goldenRuntimePlanID(planID, "health"), TaskHealthRevision: 1,
		ArtifactRevisionID: goldenRuntimePlanID(planID, "artifacts"), ArtifactRevision: 1,
		ReservationRevisionID: goldenRuntimePlanID(planID, "reservations"), ReservationRevision: 1,
		MembershipRevisionID: goldenRuntimePlanID(planID, "memberships"), MembershipRevision: 1,
		SourcePayloadDigest: standings.Artifact.PayloadDigest, GroupDigest: rootDigest[:], PoolDigest: rootDigest[:],
		HistoryDigest: rootDigest[:], TaskHealthDigest: rootDigest[:], ArtifactDigest: rootDigest[:],
		ReservationDigest: rootDigest[:], MembershipDigest: rootDigest[:], ProofHash: hex.EncodeToString(proofDigest[:]),
		CreatedAt: tstz(now),
	}); err != nil {
		return goldenRuntimeWriteError("create Golden exact plan root", err)
	}

	for groupIndex, group := range groups {
		if _, err = querier.CreateGoldenExactPlanSnapshotGroup(ctx, sqlc.CreateGoldenExactPlanSnapshotGroupParams{
			PlanID: planID, TournamentID: first.tournamentID, RosterID: first.rosterID,
			GroupID: group.groupID, GroupRevisionID: group.groupRevisionID,
			SourceProjectionRevisionID: first.sourceProjectionRevisionID,
			SourceProjectionRevision:   first.sourceProjectionRevision,
			PositionFrom:               group.positionFrom, PositionTo: group.positionTo, GroupOrdinal: int16(groupIndex + 1),
			DefinitionDigest: group.definitionDigest[:], CreatedAt: tstz(now),
		}); err != nil {
			return goldenRuntimeWriteError("create Golden exact plan group", err)
		}
		for memberIndex, participantID := range group.participantIDs {
			if _, err = querier.CreateGoldenExactPlanSnapshotMember(ctx, sqlc.CreateGoldenExactPlanSnapshotMemberParams{
				PlanID: planID, GroupRevisionID: group.groupRevisionID, TournamentID: first.tournamentID,
				RosterID: first.rosterID, ParticipantID: participantID,
				Position: group.positionFrom + int16(memberIndex), CreatedAt: tstz(now),
			}); err != nil {
				return goldenRuntimeWriteError("create Golden exact plan member", err)
			}
		}
		for _, edge := range branches[groupIndex].Edges {
			if _, err = querier.CreateGoldenExactPlanSnapshotEdge(ctx, sqlc.CreateGoldenExactPlanSnapshotEdgeParams{
				PlanID: planID, GroupRevisionID: group.groupRevisionID, TournamentID: first.tournamentID,
				RosterID: first.rosterID, EdgeID: edge.ID, ReservationID: edge.ReservationID,
				SnapshotID: edge.Snapshot.SnapshotID, TaskID: edge.Snapshot.TaskID,
				TaskVersion:   int32(edge.Snapshot.Version), //nolint:gosec // Candidate versions originate from PostgreSQL int4 rows.
				Position:      int16(edge.Position),         //nolint:gosec // Golden branches contain exactly three reserved edges.
				ContentDigest: edge.ContentDigest[:], CreatedAt: tstz(now),
			}); err != nil {
				return goldenRuntimeWriteError("create Golden exact plan edge", err)
			}
		}
	}
	for _, candidate := range candidates {
		if _, err = querier.CreateGoldenExactPlanSnapshotCandidate(ctx, sqlc.CreateGoldenExactPlanSnapshotCandidateParams{
			PlanID: planID, TournamentID: first.tournamentID, RosterID: first.rosterID,
			PoolRevisionID: content.GoldenPoolRevisionID, TaskID: candidate.version.TaskID,
			TaskVersion: candidate.version.Version, ExistsInSource: true, Enabled: true, Healthy: true,
			MutationLocked: false, PubliclyExposed: false, ArtifactDigest: candidate.digest[:], CreatedAt: tstz(now),
		}); err != nil {
			return goldenRuntimeWriteError("create Golden exact plan candidate", err)
		}
	}
	for _, branch := range branches {
		for _, edge := range branch.Edges {
			if _, err = querier.CreateGoldenExactPlanSnapshotReservation(ctx, sqlc.CreateGoldenExactPlanSnapshotReservationParams{
				PlanID: planID, TournamentID: first.tournamentID, RosterID: first.rosterID,
				TaskID:        edge.Snapshot.TaskID,
				TaskVersion:   int32(edge.Snapshot.Version), //nolint:gosec // Candidate versions originate from PostgreSQL int4 rows.
				ReservationID: edge.ReservationID, OwnerPlanID: planID, OwnerPlanRevisionID: planRevisionID,
				CreatedAt: tstz(now),
			}); err != nil {
				return goldenRuntimeWriteError("create Golden exact plan reservation", err)
			}
		}
	}
	participantReservations, err := querier.ListGoldenRuntimePlanParticipantReservations(ctx, first.tournamentID)
	if err != nil {
		return goldenRuntimeReadError("load Golden participant reservations", err)
	}
	memberCount := 0
	for _, group := range groups {
		memberCount += len(group.participantIDs)
	}
	if len(participantReservations) != memberCount {
		return domain.ErrConflict
	}
	for _, reservation := range participantReservations {
		if !reservation.AcquiredAt.Valid || !reservation.UpdatedAt.Valid {
			return domain.ErrConflict
		}
		if _, err = querier.CreateGoldenExactPlanSnapshotParticipantReservation(
			ctx,
			sqlc.CreateGoldenExactPlanSnapshotParticipantReservationParams{
				PlanID: planID, TournamentID: first.tournamentID, RosterID: first.rosterID,
				ParticipantID: reservation.ParticipantID, PlayerID: reservation.PlayerID,
				ReservationID: reservation.ReservationID, Revision: reservation.Revision,
				AcquiredAt: reservation.AcquiredAt, UpdatedAt: reservation.UpdatedAt, CreatedAt: tstz(now),
			},
		); err != nil {
			return goldenRuntimeWriteError("create Golden participant reservation snapshot", err)
		}
	}
	if _, err = querier.SealGoldenExactPlanSnapshot(ctx, sqlc.SealGoldenExactPlanSnapshotParams{
		PlanID: planID, TournamentID: first.tournamentID, RosterID: first.rosterID,
		ProofHash: hex.EncodeToString(proofDigest[:]), SealedAt: tstz(now),
	}); err != nil {
		return goldenRuntimeWriteError("seal Golden exact plan", err)
	}
	return nil
}

func goldenRuntimePlanID(namespace uuid.UUID, role string) uuid.UUID {
	return uuid.NewSHA1(namespace, []byte("golden-runtime-plan:"+role))
}

func goldenRuntimePlanDigest(role string, ids ...uuid.UUID) [sha256.Size]byte {
	hash := sha256.New()
	_, _ = hash.Write([]byte(role))
	for _, id := range ids {
		_, _ = hash.Write(id[:])
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}
