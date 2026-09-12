package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/capacity"
	assignmentusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/assignment"
)

// ExactNormalAssignmentPostgres persists one executable assignment graph for
// a live BO1 series.  The source row is an immutable retry boundary: it keeps
// the exact authority used for selection while generic assignment rows remain
// the authority consumed by existing participant and admin endpoints.
type ExactNormalAssignmentPostgres struct {
	tx *TxManager
}

var _ assignmentusecase.ExactNormalAssignmentRepository = (*ExactNormalAssignmentPostgres)(nil)

// exactNormalStage is the common projection of the playoff and Swiss source
// rows.  Both paths feed the same participant, history, candidate, and
// reservation locks, so source selection cannot alter the authority shape.
type exactNormalStage struct {
	CommandID                     uuid.UUID
	TournamentID                  uuid.UUID
	RosterID                      uuid.UUID
	SeriesID                      uuid.UUID
	FirstParticipantID            uuid.UUID
	SecondParticipantID           uuid.UUID
	SeriesRevision                int64
	RosterRevision                int64
	CategoryRevisionID            uuid.UUID
	CategoryRevision              int64
	SourcePoolRevisionID          uuid.UUID
	PoolRevision                  int64
	Category                      string
	PublishedProjectionRevisionID uuid.UUID
	PublishedProjectionRevision   int64
	GraphDigest                   []byte
	ArtifactDigest                []byte
}

func exactNormalStageFromPlayoff(row sqlc.LockExactNormalAssignmentStageRow) exactNormalStage {
	return exactNormalStage{
		CommandID: row.CommandID, TournamentID: row.TournamentID, RosterID: row.RosterID,
		SeriesID: row.SeriesID, FirstParticipantID: row.FirstParticipantID,
		SecondParticipantID: row.SecondParticipantID, SeriesRevision: row.SeriesRevision,
		RosterRevision: row.RosterRevision, CategoryRevisionID: row.CategoryRevisionID,
		CategoryRevision: row.CategoryRevision, SourcePoolRevisionID: row.SourcePoolRevisionID,
		PoolRevision: row.PoolRevision, Category: row.Category,
		PublishedProjectionRevisionID: row.PublishedProjectionRevisionID,
		PublishedProjectionRevision:   row.PublishedProjectionRevision,
		GraphDigest:                   row.GraphDigest, ArtifactDigest: row.ArtifactDigest,
	}
}

func exactNormalStageFromSwiss(row sqlc.LockExactNormalAssignmentSwissStageRow) exactNormalStage {
	return exactNormalStage{
		CommandID: row.CommandID, TournamentID: row.TournamentID, RosterID: row.RosterID,
		SeriesID: row.SeriesID, FirstParticipantID: row.FirstParticipantID,
		SecondParticipantID: row.SecondParticipantID, SeriesRevision: row.SeriesRevision,
		RosterRevision: row.RosterRevision, CategoryRevisionID: row.CategoryRevisionID,
		CategoryRevision: row.CategoryRevision, SourcePoolRevisionID: row.SourcePoolRevisionID,
		PoolRevision: row.PoolRevision, Category: row.Category,
		PublishedProjectionRevisionID: row.PublishedProjectionRevisionID,
		PublishedProjectionRevision:   row.PublishedProjectionRevision,
		GraphDigest:                   row.GraphDigest, ArtifactDigest: row.ArtifactDigest,
	}
}

func NewExactNormalAssignmentPostgres(tx *TxManager) *ExactNormalAssignmentPostgres {
	return &ExactNormalAssignmentPostgres{tx: tx}
}

func (r *ExactNormalAssignmentPostgres) LoadExactNormalAssignmentAuthority(
	ctx context.Context,
	scope assignmentusecase.ExactNormalAssignmentScope,
) (assignmentusecase.ExactNormalAssignmentAuthority, error) {
	if ctx == nil || r == nil || r.tx == nil || !validExactNormalScope(scope) {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, domain.ErrValidation
	}
	var authority assignmentusecase.ExactNormalAssignmentAuthority
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		querier := r.tx.Querier(txCtx)
		stored, findErr := querier.FindExactNormalAssignmentSourceByScope(txCtx, sqlc.FindExactNormalAssignmentSourceByScopeParams{
			TournamentID: scope.TournamentID, RosterID: scope.RosterID, SeriesID: scope.SeriesID,
			SlotID: scope.SlotID, CategoryLockID: scope.CategoryLockID,
		})
		if findErr == nil {
			if stored.State != "committed" {
				return domain.ErrConflict
			}
			source, err := querier.LockExactNormalAssignmentSource(txCtx, stored.PlanID)
			if err != nil {
				return err
			}
			authority, err = exactNormalAuthorityFromSource(source)
			return err
		}
		if !errors.Is(findErr, pgx.ErrNoRows) {
			return findErr
		}
		loaded, loadErr := r.loadExactNormalAuthorityTx(txCtx, scope)
		authority = loaded
		return loadErr
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, domain.ErrConflict
	}
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, fmt.Errorf(
			"ExactNormalAssignmentPostgres - LoadExactNormalAssignmentAuthority: %w", err,
		)
	}
	return authority, nil
}

func (r *ExactNormalAssignmentPostgres) loadExactNormalAuthorityTx(
	ctx context.Context,
	scope assignmentusecase.ExactNormalAssignmentScope,
) (assignmentusecase.ExactNormalAssignmentAuthority, error) {
	querier := r.tx.Querier(ctx)
	stage, err := r.lockExactNormalStage(ctx, querier, scope)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, fmt.Errorf("lock exact normal stage: %w", err)
	}
	if stage.TournamentID != scope.TournamentID || stage.RosterID != scope.RosterID ||
		stage.SeriesID != scope.SeriesID || stage.CategoryRevisionID != scope.CategoryLockID ||
		stage.FirstParticipantID == uuid.Nil || stage.SecondParticipantID == uuid.Nil ||
		stage.FirstParticipantID == stage.SecondParticipantID {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, domain.ErrConflict
	}
	if err := querier.EnsureExactNormalAssignmentHistoryHead(ctx, sqlc.EnsureExactNormalAssignmentHistoryHeadParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		SeriesID: scope.SeriesID, SlotID: scope.SlotID,
	}); err != nil {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, fmt.Errorf("ensure exact normal history head: %w", err)
	}
	head, err := querier.LockExactNormalAssignmentHistoryHead(ctx, sqlc.LockExactNormalAssignmentHistoryHeadParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID,
		SeriesID: scope.SeriesID, SlotID: scope.SlotID,
	})
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, fmt.Errorf("lock exact normal history head: %w", err)
	}
	participants, err := querier.LockExactNormalAssignmentParticipants(ctx, sqlc.LockExactNormalAssignmentParticipantsParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID, SeriesID: scope.SeriesID,
	})
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, fmt.Errorf("lock exact normal participants: %w", err)
	}
	history, err := querier.LockExactNormalAssignmentHistory(ctx, sqlc.LockExactNormalAssignmentHistoryParams{
		TournamentID: scope.TournamentID, RosterID: scope.RosterID, SeriesID: scope.SeriesID,
	})
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, fmt.Errorf("lock exact normal history: %w", err)
	}
	candidates, err := querier.LockExactNormalAssignmentCandidates(ctx, sqlc.LockExactNormalAssignmentCandidatesParams{
		CategoryLockID: scope.CategoryLockID, SeriesID: scope.SeriesID, RosterID: scope.RosterID,
	})
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, fmt.Errorf("lock exact normal candidates: %w", err)
	}
	authority, err := exactNormalAuthorityFromRows(scope, stage, head, participants, history, candidates)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, fmt.Errorf("validate exact normal authority: %w", err)
	}
	return authority, nil
}

func (r *ExactNormalAssignmentPostgres) lockExactNormalStage(
	ctx context.Context,
	querier *sqlc.Queries,
	scope assignmentusecase.ExactNormalAssignmentScope,
) (exactNormalStage, error) {
	playoff, err := querier.LockExactNormalAssignmentStage(ctx, sqlc.LockExactNormalAssignmentStageParams{
		SlotID: scope.SlotID, CategoryLockID: scope.CategoryLockID,
		TournamentID: scope.TournamentID, RosterID: scope.RosterID, SeriesID: scope.SeriesID,
	})
	if err == nil {
		return exactNormalStageFromPlayoff(playoff), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return exactNormalStage{}, err
	}
	swiss, err := querier.LockExactNormalAssignmentSwissStage(ctx, sqlc.LockExactNormalAssignmentSwissStageParams{
		SlotID: scope.SlotID, CategoryLockID: scope.CategoryLockID,
		TournamentID: scope.TournamentID, RosterID: scope.RosterID, SeriesID: scope.SeriesID,
	})
	if err != nil {
		return exactNormalStage{}, err
	}
	return exactNormalStageFromSwiss(swiss), nil
}

//nolint:gocyclo // One cohesive authority boundary keeps cross-row assignment invariants fail-closed.
func exactNormalAuthorityFromRows(
	scope assignmentusecase.ExactNormalAssignmentScope,
	stage exactNormalStage,
	head sqlc.LockExactNormalAssignmentHistoryHeadRow,
	participantRows []sqlc.LockExactNormalAssignmentParticipantsRow,
	historyRows []sqlc.LockExactNormalAssignmentHistoryRow,
	candidateRows []sqlc.LockExactNormalAssignmentCandidatesRow,
) (assignmentusecase.ExactNormalAssignmentAuthority, error) {
	if len(participantRows) != 2 || head.RevisionID == uuid.Nil || head.Revision < 1 ||
		!head.CreatedAt.Valid || !head.UpdatedAt.Valid {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, fmt.Errorf("validate authority cardinality: %w", domain.ErrConflict)
	}
	graphDigest, err := exactNormalDigest(stage.GraphDigest)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, fmt.Errorf("validate graph digest: %w", err)
	}
	artifactDigest, err := exactNormalDigest(stage.ArtifactDigest)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, fmt.Errorf("validate artifact digest: %w", err)
	}
	reservations, participantIDs, err := exactNormalParticipants(stage, participantRows)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, fmt.Errorf("validate participants: %w", err)
	}
	history, err := exactNormalHistory(participantIDs, historyRows)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, fmt.Errorf("validate history: %w", err)
	}
	pool, taskVersions, err := exactNormalCandidates(candidateRows)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, fmt.Errorf("validate candidates: %w", err)
	}
	if stage.Category == "" || !domain.Category(stage.Category).IsValid() ||
		stage.SeriesRevision < 1 || stage.RosterRevision < 1 || stage.CategoryRevision < 1 ||
		stage.PoolRevision < 1 || stage.PublishedProjectionRevisionID == uuid.Nil ||
		stage.PublishedProjectionRevision < 1 || stage.SourcePoolRevisionID != pool.ID ||
		stage.PoolRevision != pool.Revision {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, fmt.Errorf("validate source revisions: %w", domain.ErrConflict)
	}
	return assignmentusecase.ExactNormalAssignmentAuthority{
		Scope:    scope,
		Category: domain.Category(stage.Category),
		Revisions: assignmentusecase.ExactNormalAssignmentSourceRevisions{
			SeriesRevision: stage.SeriesRevision, PoolRevisionID: stage.SourcePoolRevisionID,
			PoolRevision: stage.PoolRevision, HistoryRevisionID: head.RevisionID,
			HistoryRevision: head.Revision, RosterRevision: stage.RosterRevision,
			ArtifactRevisionID: stage.PublishedProjectionRevisionID,
			ArtifactRevision:   stage.PublishedProjectionRevision,
			CategoryRevisionID: stage.CategoryRevisionID, CategoryRevision: stage.CategoryRevision,
		},
		Pool: pool, ParticipantIDs: participantIDs, ParticipantReservations: reservations,
		History: history, Candidates: taskVersions, GraphDigest: graphDigest, ArtifactDigest: artifactDigest,
	}, nil
}

func exactNormalParticipants(
	stage exactNormalStage,
	rows []sqlc.LockExactNormalAssignmentParticipantsRow,
) ([]assignmentusecase.ExactNormalParticipantReservation, []uuid.UUID, error) {
	result := make([]assignmentusecase.ExactNormalParticipantReservation, len(rows))
	seen := make(map[uuid.UUID]struct{}, len(rows))
	for index, row := range rows {
		if row.ParticipantID == uuid.Nil || row.PlayerID == uuid.Nil || row.ReservationID == uuid.Nil ||
			row.TournamentID != stage.TournamentID || row.ReservationRevision < 1 ||
			!row.AcquiredAt.Valid || !row.UpdatedAt.Valid || row.UpdatedAt.Time.Before(row.AcquiredAt.Time) ||
			(row.ParticipantID != stage.FirstParticipantID && row.ParticipantID != stage.SecondParticipantID) {
			return nil, nil, domain.ErrConflict
		}
		if _, ok := seen[row.ParticipantID]; ok {
			return nil, nil, domain.ErrConflict
		}
		seen[row.ParticipantID] = struct{}{}
		result[index] = assignmentusecase.ExactNormalParticipantReservation{
			ParticipantID: row.ParticipantID, PlayerID: row.PlayerID,
			Reservation: domain.ParticipantReservation{
				PlayerID: row.PlayerID, ReservationID: row.ReservationID,
				TournamentID: row.TournamentID, Revision: row.ReservationRevision,
				AcquiredAt: row.AcquiredAt.Time.UTC(), UpdatedAt: row.UpdatedAt.Time.UTC(),
			},
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ParticipantID.String() < result[j].ParticipantID.String() })
	ids := make([]uuid.UUID, len(result))
	for index, participant := range result {
		ids[index] = participant.ParticipantID
	}
	return result, ids, nil
}

func exactNormalHistory(
	participantIDs []uuid.UUID,
	rows []sqlc.LockExactNormalAssignmentHistoryRow,
) ([]capacity.TaskUse, error) {
	allowed := make(map[uuid.UUID]struct{}, len(participantIDs))
	for _, participantID := range participantIDs {
		allowed[participantID] = struct{}{}
	}
	type historyKey struct {
		participantID uuid.UUID
		ref           domain.TaskVersionRef
	}
	seen := make(map[historyKey]struct{}, len(rows))
	result := make([]capacity.TaskUse, len(rows))
	for index, row := range rows {
		if row.ParticipantID == uuid.Nil || row.TaskID == uuid.Nil || row.TaskVersion < 1 {
			return nil, domain.ErrConflict
		}
		if _, ok := allowed[row.ParticipantID]; !ok {
			return nil, domain.ErrConflict
		}
		ref := domain.TaskVersionRef{TaskID: row.TaskID, Version: int(row.TaskVersion)}
		key := historyKey{participantID: row.ParticipantID, ref: ref}
		if _, ok := seen[key]; ok {
			return nil, domain.ErrConflict
		}
		seen[key] = struct{}{}
		result[index] = capacity.TaskUse{ParticipantID: row.ParticipantID, TaskID: row.TaskID, Version: int(row.TaskVersion)}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ParticipantID != result[j].ParticipantID {
			return result[i].ParticipantID.String() < result[j].ParticipantID.String()
		}
		return domain.CompareTaskVersionRefs(
			domain.TaskVersionRef{TaskID: result[i].TaskID, Version: result[i].Version},
			domain.TaskVersionRef{TaskID: result[j].TaskID, Version: result[j].Version},
		) < 0
	})
	return result, nil
}

func exactNormalCandidates(
	rows []sqlc.LockExactNormalAssignmentCandidatesRow,
) (domain.TaskPoolRevision, []assignmentusecase.ExactNormalTaskVersion, error) {
	if len(rows) < domain.AssignmentReserveCount+1 {
		return domain.TaskPoolRevision{}, nil, domain.ErrConflict
	}
	pool := domain.TaskPoolRevision{
		ID: rows[0].PoolRevisionID, Revision: rows[0].PoolRevision,
		Kind: domain.AssignmentTaskKindNormal, Versions: make([]domain.TaskVersionRef, len(rows)),
	}
	result := make([]assignmentusecase.ExactNormalTaskVersion, len(rows))
	seen := make(map[uuid.UUID]struct{}, len(rows))
	for index, row := range rows {
		if row.TaskID == uuid.Nil || row.TaskVersion < 1 || row.PoolRevisionID != pool.ID ||
			row.PoolRevision != pool.Revision || row.Unavailable {
			return domain.TaskPoolRevision{}, nil, domain.ErrConflict
		}
		if _, ok := seen[row.TaskID]; ok {
			return domain.TaskPoolRevision{}, nil, domain.ErrConflict
		}
		seen[row.TaskID] = struct{}{}
		pool.Versions[index] = domain.TaskVersionRef{TaskID: row.TaskID, Version: int(row.TaskVersion)}
		result[index] = assignmentusecase.ExactNormalTaskVersion{
			PoolRevisionID: row.PoolRevisionID, Version: int(row.TaskVersion),
			Task: domain.Task{
				ID: row.TaskID, Title: row.Title, Description: row.Description,
				Category: domain.Category(row.Category), Difficulty: domain.Difficulty(row.Difficulty),
				TimeLimit: int(row.TimeLimit), Flag: row.Flag,
				Hints: taskHintsToDomain(row.Hint1, row.Hint2, row.Hint3), TaskURL: row.TaskUrl,
				SourceFileURL: row.SourceFileUrl, Kind: domain.TaskKindNormal, Enabled: true,
				CurrentVersion: int(row.TaskVersion), CreatedAt: row.TaskCreatedAt.Time.UTC(),
			},
		}
	}
	normalized, err := domain.NormalizeTaskPoolRevision(pool, domain.AssignmentTaskKindNormal)
	if err != nil {
		return domain.TaskPoolRevision{}, nil, err
	}
	sort.Slice(result, func(i, j int) bool {
		return domain.CompareTaskVersionRefs(
			domain.TaskVersionRef{TaskID: result[i].Task.ID, Version: result[i].Version},
			domain.TaskVersionRef{TaskID: result[j].Task.ID, Version: result[j].Version},
		) < 0
	})
	return normalized, result, nil
}

func (r *ExactNormalAssignmentPostgres) CommitExactNormalAssignment(
	ctx context.Context,
	plan assignmentusecase.ExactNormalAssignmentPlan,
) (*assignmentusecase.ExactNormalAssignmentPlan, bool, error) {
	if ctx == nil || r == nil || r.tx == nil || plan.Validate() != nil {
		return nil, false, domain.ErrValidation
	}
	var result assignmentusecase.ExactNormalAssignmentPlan
	changed := false
	err := r.tx.Do(ctx, func(txCtx context.Context) error {
		var err error
		result, changed, err = r.commitExactNormalAssignmentTx(txCtx, plan)
		return err
	})
	if err != nil {
		if errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrValidation) {
			return nil, false, err
		}
		return nil, false, mapRepositoryWriteError("ExactNormalAssignmentPostgres - CommitExactNormalAssignment", err)
	}
	return &result, changed, nil
}

//nolint:gocyclo // The assignment plan and all reserved snapshots must remain one transactional write boundary.
func (r *ExactNormalAssignmentPostgres) commitExactNormalAssignmentTx(
	ctx context.Context,
	plan assignmentusecase.ExactNormalAssignmentPlan,
) (assignmentusecase.ExactNormalAssignmentPlan, bool, error) {
	querier := r.tx.Querier(ctx)
	stored, findErr := querier.FindExactNormalAssignmentSourceByScope(ctx, sqlc.FindExactNormalAssignmentSourceByScopeParams{
		TournamentID: plan.Scope.TournamentID, RosterID: plan.Scope.RosterID,
		SeriesID: plan.Scope.SeriesID, SlotID: plan.Scope.SlotID,
		CategoryLockID: plan.Scope.CategoryLockID,
	})
	if findErr == nil {
		if stored.PlanID != plan.PlanID || stored.ProofHash != plan.ProofHash || stored.State != "committed" {
			return assignmentusecase.ExactNormalAssignmentPlan{}, false, domain.ErrConflict
		}
		return plan, false, nil
	}
	if !errors.Is(findErr, pgx.ErrNoRows) {
		return assignmentusecase.ExactNormalAssignmentPlan{}, false, findErr
	}
	authority, err := r.loadExactNormalAuthorityTx(ctx, plan.Scope)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, false, err
	}
	if !exactNormalPlanMatchesAuthority(plan, authority) {
		return assignmentusecase.ExactNormalAssignmentPlan{}, false, domain.ErrConflict
	}
	constraintGraph, proofEvidence, err := exactNormalPlanEvidence(plan)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, false, err
	}
	decisionInputs, err := marshalJSON("ExactNormalAssignmentPostgres - decision inputs", plan.DecisionEvidence.NormalizedInputs)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, false, err
	}
	decisionResult, err := marshalJSON("ExactNormalAssignmentPostgres - decision result", plan.DecisionEvidence.Result)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, false, err
	}
	proofHash := plan.ProofHash
	evidence := plan.DecisionEvidence
	if err := querier.CreateExactNormalAssignmentPlan(ctx, sqlc.CreateExactNormalAssignmentPlanParams{
		ID: plan.PlanID, TournamentID: plan.Scope.TournamentID, RosterID: plan.Scope.RosterID,
		RevisionID: plan.PlanRevisionID, SourceRosterRevision: plan.Revisions.RosterRevision,
		SourcePoolRevisionID: plan.Revisions.PoolRevisionID, ConstraintGraph: constraintGraph,
		ProofEvidence: proofEvidence, ProofHash: &proofHash,
		DecisionEvidenceID:       uuid.NullUUID{UUID: evidence.ID, Valid: true},
		DecisionAlgorithmVersion: &evidence.AlgorithmVersion, DecisionInputs: decisionInputs,
		DecisionSeed: append([]byte(nil), evidence.Seed[:]...), DecisionResult: decisionResult,
		DecisionReplayDigest: append([]byte(nil), evidence.ReplayDigest[:]...),
		DecisionOwnerID:      uuid.NullUUID{UUID: evidence.OwnerID, Valid: true},
		DecidedAt:            tstz(evidence.DecidedAt), CreatedAt: tstz(plan.CreatedAt),
	}); err != nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, false, err
	}
	categorySequence, err := categoryJSON([]domain.Category{plan.Category})
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, false, err
	}
	if err := querier.CreateExactNormalAssignmentBranch(ctx, sqlc.CreateExactNormalAssignmentBranchParams{
		ID: plan.BranchID, PlanID: plan.PlanID, DraftID: uuid.NullUUID{}, DraftRevisionID: uuid.NullUUID{},
		BranchKey: "primary", CategorySequence: categorySequence, CreatedAt: tstz(plan.CreatedAt),
	}); err != nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, false, err
	}
	for _, edge := range plan.SelectedEdges {
		selectionEvidence, err := requiredJSONObject(map[string]any{
			"decision_evidence_id": evidence.ID.String(), "proof_hash": plan.ProofHash,
			"position": edge.Position, "graph_digest": hex.EncodeToString(plan.GraphDigest[:]),
		})
		if err != nil {
			return assignmentusecase.ExactNormalAssignmentPlan{}, false, err
		}
		if _, err := querier.CreateAssignmentPlanEdge(ctx, sqlc.CreateAssignmentPlanEdgeParams{
			ID: edge.ID, PlanID: plan.PlanID, BranchID: plan.BranchID,
			Position:          int16(edge.Position), //nolint:gosec // Exact-plan validation bounds positions to the three reserved edges.
			TaskID:            edge.Snapshot.TaskID,
			TaskVersion:       int32(edge.Snapshot.Version), //nolint:gosec // Candidate versions originate from PostgreSQL int4 rows.
			SelectionEvidence: selectionEvidence, CreatedAt: tstz(plan.CreatedAt),
		}); err != nil {
			return assignmentusecase.ExactNormalAssignmentPlan{}, false, err
		}
		if _, err := querier.CreateAssignmentTaskVersionReservation(ctx, sqlc.CreateAssignmentTaskVersionReservationParams{
			ID: edge.ReservationID, EdgeID: edge.ID, PlanID: plan.PlanID, BranchID: plan.BranchID,
			TaskID:      edge.Snapshot.TaskID,
			TaskVersion: int32(edge.Snapshot.Version), //nolint:gosec // Candidate versions originate from PostgreSQL int4 rows.
			CreatedAt:   tstz(plan.CreatedAt),
		}); err != nil {
			return assignmentusecase.ExactNormalAssignmentPlan{}, false, err
		}
		snapshotParams, err := taskSnapshotParams(AssignmentEdgeInput{
			ReservationID: edge.ReservationID, Snapshot: edge.Snapshot, ContentDigest: edge.ContentDigest,
		}, plan.CreatedAt)
		if err != nil {
			return assignmentusecase.ExactNormalAssignmentPlan{}, false, err
		}
		if _, err := querier.CreateAssignmentTaskSnapshot(ctx, snapshotParams); err != nil {
			return assignmentusecase.ExactNormalAssignmentPlan{}, false, err
		}
	}
	if err := r.insertExactNormalSource(ctx, querier, plan); err != nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, false, err
	}
	committed, err := querier.CommitAssignmentBranchReservations(ctx, sqlc.CommitAssignmentBranchReservationsParams{
		CommittedAt: tstz(plan.CreatedAt), PlanID: plan.PlanID, BranchID: plan.BranchID,
	})
	if err != nil || len(committed) != domain.AssignmentReserveCount+1 {
		if err != nil {
			return assignmentusecase.ExactNormalAssignmentPlan{}, false, err
		}
		return assignmentusecase.ExactNormalAssignmentPlan{}, false, domain.ErrConflict
	}
	if err := activateAssignmentBranch(ctx, querier, plan.PlanID, plan.BranchID, plan.CreatedAt); err != nil {
		return assignmentusecase.ExactNormalAssignmentPlan{}, false, err
	}
	if _, err := querier.CommitExactNormalAssignmentPlanCAS(ctx, sqlc.CommitExactNormalAssignmentPlanCASParams{
		ActiveBranchID: uuid.NullUUID{UUID: plan.BranchID, Valid: true}, CommittedAt: tstz(plan.CreatedAt), ID: plan.PlanID,
		ExpectedRevisionID: plan.PlanRevisionID, ExpectedRosterRevision: plan.Revisions.RosterRevision,
	}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return assignmentusecase.ExactNormalAssignmentPlan{}, false, domain.ErrConflict
		}
		return assignmentusecase.ExactNormalAssignmentPlan{}, false, err
	}
	return plan, true, nil
}

func (r *ExactNormalAssignmentPostgres) insertExactNormalSource(
	ctx context.Context,
	querier *sqlc.Queries,
	plan assignmentusecase.ExactNormalAssignmentPlan,
) error {
	pool, err := marshalJSON("ExactNormalAssignmentPostgres - source pool", plan.Pool)
	if err != nil {
		return err
	}
	participantIDs, err := marshalJSON("ExactNormalAssignmentPostgres - source participants", plan.ParticipantIDs)
	if err != nil {
		return err
	}
	participantReservations, err := marshalJSON("ExactNormalAssignmentPostgres - source reservations", plan.ParticipantReservations)
	if err != nil {
		return err
	}
	history, err := marshalJSON("ExactNormalAssignmentPostgres - source history", plan.History)
	if err != nil {
		return err
	}
	candidates, err := marshalJSON("ExactNormalAssignmentPostgres - source candidates", plan.CandidateTaskVersions)
	if err != nil {
		return err
	}
	return querier.CreateExactNormalAssignmentSource(ctx, sqlc.CreateExactNormalAssignmentSourceParams{
		PlanID: plan.PlanID, TournamentID: plan.Scope.TournamentID, RosterID: plan.Scope.RosterID,
		SeriesID: plan.Scope.SeriesID, SlotID: plan.Scope.SlotID, CategoryLockID: plan.Scope.CategoryLockID,
		Category: string(plan.Category), SeriesRevision: plan.Revisions.SeriesRevision,
		PoolRevisionID: plan.Revisions.PoolRevisionID, PoolRevision: plan.Revisions.PoolRevision,
		HistoryRevisionID: plan.Revisions.HistoryRevisionID, HistoryRevision: plan.Revisions.HistoryRevision,
		RosterRevision: plan.Revisions.RosterRevision, ArtifactRevisionID: plan.Revisions.ArtifactRevisionID,
		ArtifactRevision: plan.Revisions.ArtifactRevision, CategoryRevisionID: plan.Revisions.CategoryRevisionID,
		CategoryRevision: plan.Revisions.CategoryRevision, Pool: pool, ParticipantIds: participantIDs,
		ParticipantReservations: participantReservations, History: history, Candidates: candidates,
		GraphDigest: append([]byte(nil), plan.GraphDigest[:]...), ArtifactDigest: append([]byte(nil), plan.ArtifactDigest[:]...),
		ProofHash: plan.ProofHash, CreatedAt: tstz(plan.CreatedAt),
	})
}

func exactNormalPlanEvidence(plan assignmentusecase.ExactNormalAssignmentPlan) ([]byte, []byte, error) {
	graph, err := marshalJSON("ExactNormalAssignmentPostgres - graph evidence", map[string]any{
		"kind": "exact_normal", "series_id": plan.Scope.SeriesID.String(), "slot_id": plan.Scope.SlotID.String(),
		"category_lock_id": plan.Scope.CategoryLockID.String(), "graph_digest": hex.EncodeToString(plan.GraphDigest[:]),
	})
	if err != nil {
		return nil, nil, err
	}
	proof, err := marshalJSON("ExactNormalAssignmentPostgres - proof evidence", map[string]any{
		"proof_hash": plan.ProofHash, "artifact_digest": hex.EncodeToString(plan.ArtifactDigest[:]),
		"history_revision_id": plan.Revisions.HistoryRevisionID.String(),
		"history_revision":    plan.Revisions.HistoryRevision,
	})
	return graph, proof, err
}

func exactNormalPlanMatchesAuthority(
	plan assignmentusecase.ExactNormalAssignmentPlan,
	authority assignmentusecase.ExactNormalAssignmentAuthority,
) bool {
	if plan.Scope != authority.Scope || plan.Category != authority.Category ||
		plan.Revisions != authority.Revisions || !reflect.DeepEqual(plan.Pool, authority.Pool) ||
		!reflect.DeepEqual(plan.ParticipantIDs, authority.ParticipantIDs) ||
		!reflect.DeepEqual(plan.ParticipantReservations, authority.ParticipantReservations) ||
		!slices.Equal(plan.History, authority.History) ||
		plan.GraphDigest != authority.GraphDigest || plan.ArtifactDigest != authority.ArtifactDigest {
		return false
	}
	return assignmentusecase.ValidateExactNormalAssignmentCandidates(plan, authority.Candidates, nil) == nil
}

func exactNormalCandidateRefs(candidates []assignmentusecase.ExactNormalTaskVersion) []domain.TaskVersionRef {
	refs := make([]domain.TaskVersionRef, len(candidates))
	for index, candidate := range candidates {
		refs[index] = domain.TaskVersionRef{TaskID: candidate.Task.ID, Version: candidate.Version}
	}
	sort.Slice(refs, func(i, j int) bool { return domain.CompareTaskVersionRefs(refs[i], refs[j]) < 0 })
	return refs
}

//nolint:gocyclo // One cohesive replay boundary validates every persisted authority component before reuse.
func exactNormalAuthorityFromSource(
	row sqlc.LockExactNormalAssignmentSourceRow,
) (assignmentusecase.ExactNormalAssignmentAuthority, error) {
	if row.PlanID == uuid.Nil || row.PlanRevisionID == uuid.Nil || row.State != "committed" ||
		row.CategoryLockID != row.CategoryRevisionID || row.Category == "" {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, domain.ErrConflict
	}
	var authority assignmentusecase.ExactNormalAssignmentAuthority
	//nolint:musttag // Domain validation below verifies this versioned application-owned document.
	if err := json.Unmarshal(row.Pool, &authority.Pool); err != nil {
		return authority, domain.ErrConflict
	}
	if err := json.Unmarshal(row.ParticipantIds, &authority.ParticipantIDs); err != nil {
		return authority, domain.ErrConflict
	}
	//nolint:musttag // Domain validation below verifies this versioned application-owned document.
	if err := json.Unmarshal(row.ParticipantReservations, &authority.ParticipantReservations); err != nil {
		return authority, domain.ErrConflict
	}
	if err := json.Unmarshal(row.History, &authority.History); err != nil {
		return authority, domain.ErrConflict
	}
	//nolint:musttag // Domain validation below verifies this versioned application-owned document.
	if err := json.Unmarshal(row.Candidates, &authority.Candidates); err != nil {
		return authority, domain.ErrConflict
	}
	if authority.Pool.Kind != domain.AssignmentTaskKindNormal || len(authority.ParticipantIDs) != 2 ||
		len(authority.Candidates) < domain.AssignmentReserveCount+1 {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, domain.ErrConflict
	}
	normalizedPool, err := domain.NormalizeTaskPoolRevision(authority.Pool, domain.AssignmentTaskKindNormal)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, domain.ErrConflict
	}
	authority.Pool = normalizedPool
	for _, participant := range authority.ParticipantReservations {
		if participant.ParticipantID == uuid.Nil || participant.PlayerID == uuid.Nil ||
			!participant.Reservation.IsValid() {
			return assignmentusecase.ExactNormalAssignmentAuthority{}, domain.ErrConflict
		}
	}
	graphDigest, err := exactNormalDigest(row.GraphDigest)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, err
	}
	artifactDigest, err := exactNormalDigest(row.ArtifactDigest)
	if err != nil {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, err
	}
	authority.Scope = assignmentusecase.ExactNormalAssignmentScope{
		TournamentID: row.TournamentID, RosterID: row.RosterID, SeriesID: row.SeriesID,
		SlotID: row.SlotID, CategoryLockID: row.CategoryLockID,
	}
	authority.Category = domain.Category(row.Category)
	authority.Revisions = assignmentusecase.ExactNormalAssignmentSourceRevisions{
		SeriesRevision: row.SeriesRevision, PoolRevisionID: row.PoolRevisionID, PoolRevision: row.PoolRevision,
		HistoryRevisionID: row.HistoryRevisionID, HistoryRevision: row.HistoryRevision,
		RosterRevision: row.RosterRevision, ArtifactRevisionID: row.ArtifactRevisionID,
		ArtifactRevision: row.ArtifactRevision, CategoryRevisionID: row.CategoryRevisionID,
		CategoryRevision: row.CategoryRevision,
	}
	authority.GraphDigest, authority.ArtifactDigest = graphDigest, artifactDigest
	if !authority.Category.IsValid() || !validExactNormalSourceRevisions(authority.Revisions) {
		return assignmentusecase.ExactNormalAssignmentAuthority{}, domain.ErrConflict
	}
	return authority, nil
}

func exactNormalDigest(value []byte) ([sha256.Size]byte, error) {
	if len(value) != sha256.Size {
		return [sha256.Size]byte{}, domain.ErrConflict
	}
	var digest [sha256.Size]byte
	copy(digest[:], value)
	if digest == [sha256.Size]byte{} {
		return [sha256.Size]byte{}, domain.ErrConflict
	}
	return digest, nil
}

func validExactNormalScope(scope assignmentusecase.ExactNormalAssignmentScope) bool {
	return scope.TournamentID != uuid.Nil && scope.RosterID != uuid.Nil && scope.SeriesID != uuid.Nil &&
		scope.SlotID != uuid.Nil && scope.CategoryLockID != uuid.Nil
}

func validExactNormalSourceRevisions(revisions assignmentusecase.ExactNormalAssignmentSourceRevisions) bool {
	return revisions.SeriesRevision >= 1 && revisions.PoolRevisionID != uuid.Nil && revisions.PoolRevision >= 1 &&
		revisions.HistoryRevisionID != uuid.Nil && revisions.HistoryRevision >= 1 && revisions.RosterRevision >= 1 &&
		revisions.ArtifactRevisionID != uuid.Nil && revisions.ArtifactRevision >= 1 &&
		revisions.CategoryRevisionID != uuid.Nil && revisions.CategoryRevision >= 1
}
