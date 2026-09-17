package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/internal/db"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	goldenruntime "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/runtime"
)

const goldenRuntimeDuration = 180 * time.Second

type GoldenRuntimePostgres struct{ tx *db.TxManager }

var _ goldenruntime.RuntimeRepository = (*GoldenRuntimePostgres)(nil)

func NewGoldenRuntimePostgres(tx *db.TxManager) *GoldenRuntimePostgres {
	return &GoldenRuntimePostgres{tx: tx}
}

//nolint:gocyclo // Initialization, replay, fencing, materialization, and evidence share one transaction.
func (repository *GoldenRuntimePostgres) Open(
	ctx context.Context,
	command usecase.GoldenOpenCommand,
	now time.Time,
) (usecase.GoldenOperatorView, error) {
	if repository == nil || repository.tx == nil {
		return usecase.GoldenOperatorView{}, domain.ErrInternal
	}
	var result usecase.GoldenOperatorView
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		q := repository.tx.Querier(txCtx)
		tournament, err := q.LockGoldenRuntimeTournament(txCtx, command.TournamentID)
		if err != nil {
			return goldenRuntimeReadError("load tournament", err)
		}
		if tournament.State != string(domain.TournamentStateGolden) {
			return domain.ErrConflict
		}
		existing, err := q.ListGoldenRuntimeView(txCtx, command.TournamentID)
		if err != nil {
			return goldenRuntimeReadError("load existing runtime", err)
		}
		if len(existing) != 0 {
			head, headErr := q.LockGoldenRuntimeHead(txCtx, sqlc.LockGoldenRuntimeHeadParams{
				TournamentID: command.TournamentID, RosterID: existing[0].RosterID,
			})
			if errors.Is(headErr, pgx.ErrNoRows) {
				return domain.ErrConflict
			}
			if headErr != nil {
				return goldenRuntimeReadError("load Golden runtime head", headErr)
			}
			spec := goldenRuntimeCommandSpec{
				CommandID: command.CommandID, TournamentID: command.TournamentID, RosterID: head.RosterID,
				ActorKind: goldenRuntimeCommandActorKind(true), ActorID: command.ActorID,
				Scope: "operator", Kind: "open", ExpectedRuntimeRevision: command.ExpectedRuntimeRevision,
				Payload: struct {
					ExpectedProjectionRevision int64 `json:"expected_projection_revision"`
				}{command.ExpectedProjectionRevision},
			}
			replay, replayed, replayErr := goldenRuntimeReplay(txCtx, q, spec)
			if replayErr != nil {
				return replayErr
			}
			if replayed {
				result, replayErr = goldenRuntimeDecodeResult[usecase.GoldenOperatorView](replay.ResultPayload)
				return replayErr
			}
			if authorityErr := goldenRuntimeAuthorityConflict(command.ExpectedRuntimeRevision, head.Revision, uuid.Nil, uuid.Nil); authorityErr != nil {
				return authorityErr
			}
			return domain.ErrConflict
		}
		if command.ExpectedRuntimeRevision != 0 {
			return &usecase.GoldenAuthorityConflictError{ExpectedRevision: command.ExpectedRuntimeRevision, CurrentRevision: 0}
		}
		// A command identifier that was previously committed to another Golden
		// scope must never be silently accepted by a fresh tournament open.
		if _, priorErr := q.GetGoldenRuntimeCommand(txCtx, command.CommandID); priorErr == nil {
			return &usecase.GoldenCommandReuseConflictError{CommandID: command.CommandID}
		} else if !errors.Is(priorErr, pgx.ErrNoRows) {
			return goldenRuntimeReadError("load Golden command identity", priorErr)
		}
		rows, err := q.ListGoldenRuntimeGroups(txCtx, command.TournamentID)
		if err != nil {
			return goldenRuntimeReadError("load groups", err)
		}
		groups, err := goldenRuntimeGroups(rows, command.ExpectedProjectionRevision)
		if err != nil {
			return err
		}
		if len(groups) == 0 || groups[0].sourceProjectionRevisionID == uuid.Nil || groups[0].sourceProjectionRevision < 1 {
			return domain.ErrConflict
		}
		for _, group := range groups {
			if group.sourceProjectionRevisionID != groups[0].sourceProjectionRevisionID ||
				group.sourceProjectionRevision != groups[0].sourceProjectionRevision {
				return domain.ErrConflict
			}
		}
		if err := repository.materializeGoldenRuntimePlan(txCtx, q, groups, now); err != nil {
			return err
		}
		for _, group := range groups {
			attempt, err := q.NextGoldenRuntimeAttempt(txCtx, command.TournamentID)
			if err != nil {
				return goldenRuntimeReadError("allocate attempt number", err)
			}
			if err := repository.createGoldenRuntimeAttempt(
				txCtx, q, group, attempt.AttemptNumber, attempt.PreviousAttemptID,
				1, now,
			); err != nil {
				return err
			}
		}
		head, err := q.CreateGoldenRuntimeHead(txCtx, sqlc.CreateGoldenRuntimeHeadParams{
			TournamentID: command.TournamentID, RosterID: groups[0].rosterID,
			Revision: 1, SourceProjectionRevisionID: groups[0].sourceProjectionRevisionID,
			SourceProjectionRevision: groups[0].sourceProjectionRevision, UpdatedAt: tstz(now),
		})
		if err != nil {
			return goldenRuntimeWriteError("create Golden runtime head", err)
		}
		spec := goldenRuntimeCommandSpec{
			CommandID: command.CommandID, TournamentID: command.TournamentID, RosterID: head.RosterID,
			ActorKind: goldenRuntimeCommandActorKind(true), ActorID: command.ActorID,
			Scope: "operator", Kind: "open", ExpectedRuntimeRevision: command.ExpectedRuntimeRevision,
			Payload: struct {
				ExpectedProjectionRevision int64 `json:"expected_projection_revision"`
			}{command.ExpectedProjectionRevision},
		}
		result, err = goldenOperatorRuntimeViewWithin(txCtx, q, command.TournamentID, now)
		if err != nil {
			return err
		}
		payload, err := goldenRuntimeResultPayload(result)
		if err != nil {
			return err
		}
		return repository.appendGoldenRuntimeEvidence(txCtx, q, spec, head, "operator", payload, now)
	})
	if err != nil {
		return usecase.GoldenOperatorView{}, fmt.Errorf("GoldenRuntimePostgres - Open: %w", err)
	}
	return result, nil
}

type goldenRuntimeGroup struct {
	tournamentID               uuid.UUID
	rosterID                   uuid.UUID
	groupID                    uuid.UUID
	groupRevisionID            uuid.UUID
	sourceProjectionRevisionID uuid.UUID
	sourceProjectionRevision   int64
	positionFrom               int16
	positionTo                 int16
	definitionDigest           [sha256.Size]byte
	participantIDs             []uuid.UUID
}

//nolint:gocyclo // Every denormalized row must be checked against the same source authority.
func goldenRuntimeGroups(rows []sqlc.ListGoldenRuntimeGroupsRow, expectedRevision int64) ([]goldenRuntimeGroup, error) {
	if len(rows) == 0 {
		return nil, domain.ErrConflict
	}
	groups := make([]goldenRuntimeGroup, 0)
	for _, row := range rows {
		if row.SourceProjectionRevision != expectedRevision || row.GroupRevisionID == uuid.Nil || row.ParticipantID == uuid.Nil {
			return nil, domain.ErrConflict
		}
		if len(groups) == 0 || groups[len(groups)-1].groupRevisionID != row.GroupRevisionID {
			if len(row.DefinitionDigest) != sha256.Size {
				return nil, domain.ErrConflict
			}
			var definitionDigest [sha256.Size]byte
			copy(definitionDigest[:], row.DefinitionDigest)
			groups = append(groups, goldenRuntimeGroup{
				tournamentID: row.TournamentID, rosterID: row.RosterID, groupID: row.GroupID,
				groupRevisionID:            row.GroupRevisionID,
				sourceProjectionRevisionID: row.SourceProjectionRevisionID,
				sourceProjectionRevision:   row.SourceProjectionRevision,
				positionFrom:               row.PositionFrom, positionTo: row.PositionTo,
				definitionDigest: definitionDigest,
			})
		}
		group := &groups[len(groups)-1]
		if group.tournamentID != row.TournamentID || group.rosterID != row.RosterID || group.groupID != row.GroupID ||
			group.positionFrom != row.PositionFrom || group.positionTo != row.PositionTo ||
			group.sourceProjectionRevisionID != row.SourceProjectionRevisionID ||
			group.sourceProjectionRevision != row.SourceProjectionRevision ||
			!bytes.Equal(group.definitionDigest[:], row.DefinitionDigest) {
			return nil, domain.ErrConflict
		}
		group.participantIDs = append(group.participantIDs, row.ParticipantID)
	}
	for _, group := range groups {
		if len(group.participantIDs) != int(group.positionTo-group.positionFrom+1) {
			return nil, domain.ErrConflict
		}
	}
	return groups, nil
}

func (repository *GoldenRuntimePostgres) createGoldenRuntimeAttempt(
	ctx context.Context,
	q *sqlc.Queries,
	group goldenRuntimeGroup,
	attemptNumber int32,
	previousAttemptID uuid.UUID,
	edgePosition int16,
	now time.Time,
) error {
	task, err := q.SelectGoldenRuntimeTask(ctx, sqlc.SelectGoldenRuntimeTaskParams{
		TournamentID: group.tournamentID, GroupRevisionID: group.groupRevisionID,
		EdgePosition: edgePosition,
	})
	if err != nil {
		return goldenRuntimeReadError("select task", err)
	}
	attemptID := uuid.New()
	if _, err = q.CreateGoldenAttempt(ctx, sqlc.CreateGoldenAttemptParams{
		ID: attemptID, TournamentID: group.tournamentID, RosterID: group.rosterID,
		AttemptNumber:     attemptNumber,
		PreviousAttemptID: uuid.NullUUID{UUID: previousAttemptID, Valid: previousAttemptID != uuid.Nil},
		CreatedAt:         tstz(now),
	}); err != nil {
		return goldenRuntimeWriteError("create attempt", err)
	}
	if _, err = q.CreateGoldenAttemptStageGroup(ctx, sqlc.CreateGoldenAttemptStageGroupParams{
		AttemptID: attemptID, TournamentID: group.tournamentID, RosterID: group.rosterID,
		GroupRevisionID: group.groupRevisionID, BoundAt: tstz(now),
	}); err != nil {
		return goldenRuntimeWriteError("bind attempt", err)
	}
	if _, err = q.CreateGoldenRuntimeAssignment(ctx, sqlc.CreateGoldenRuntimeAssignmentParams{
		AttemptID: attemptID, TournamentID: group.tournamentID, RosterID: group.rosterID,
		GroupRevisionID: group.groupRevisionID, PlanID: task.PlanID, EdgePosition: task.Position,
		WaveID:       task.EdgeID,
		AssignmentID: task.ReservationID, SnapshotID: task.SnapshotID,
		TaskID: task.TaskID, TaskVersion: task.Version, Title: task.Title, Category: task.Category,
		Difficulty: task.Difficulty, SourceDigest: task.ContentDigest,
		ReadyWindowID: uuid.New(), CreatedAt: tstz(now),
	}); err != nil {
		return goldenRuntimeWriteError("create assignment", err)
	}
	for _, participantID := range group.participantIDs {
		if _, err = q.CreateGoldenMembership(ctx, sqlc.CreateGoldenMembershipParams{
			ID: uuid.New(), AttemptID: attemptID, TournamentID: group.tournamentID, RosterID: group.rosterID,
			ParticipantID: participantID, SelectionKind: "direct",
			SelectedAt: tstz(now), CreatedAt: tstz(now),
		}); err != nil {
			return goldenRuntimeWriteError("create membership", err)
		}
	}
	return nil
}

//nolint:gocyclo // Readiness and disconnect evidence must be checked and committed atomically.
func (repository *GoldenRuntimePostgres) SetReady(
	ctx context.Context,
	command usecase.GoldenReadyCommand,
	now time.Time,
) (usecase.GoldenParticipantView, error) {
	var result usecase.GoldenParticipantView
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		q := repository.tx.Querier(txCtx)
		scope, err := q.SelectGoldenRuntimeParticipantScope(txCtx, sqlc.SelectGoldenRuntimeParticipantScopeParams{
			TournamentID: command.TournamentID, PlayerID: command.PlayerID,
		})
		if err != nil {
			return goldenRuntimeReadError("load participant scope", err)
		}
		head, err := q.LockGoldenRuntimeHead(txCtx, sqlc.LockGoldenRuntimeHeadParams{
			TournamentID: command.TournamentID, RosterID: scope.RosterID,
		})
		if err != nil {
			return goldenRuntimeReadError("lock Golden runtime head", err)
		}
		participant, err := q.LockGoldenRuntimeParticipant(txCtx, sqlc.LockGoldenRuntimeParticipantParams{
			TournamentID: command.TournamentID, PlayerID: command.PlayerID,
		})
		if err != nil {
			return goldenRuntimeReadError("load participant", err)
		}
		spec := goldenRuntimeCommandSpec{
			CommandID: command.CommandID, TournamentID: command.TournamentID, RosterID: participant.RosterID,
			ActorKind: goldenRuntimeCommandActorKind(false), ActorID: command.ActorID,
			Scope: "participant", Kind: "ready", AttemptID: command.ExpectedAttemptID,
			ParticipantID: participant.ParticipantID, ExpectedRuntimeRevision: command.ExpectedRuntimeRevision,
			ExpectedReadyWindowID: command.ExpectedReadyWindowID,
			Payload: struct {
				Ready bool `json:"ready"`
			}{Ready: command.Ready},
		}
		replay, replayed, replayErr := goldenRuntimeReplay(txCtx, q, spec)
		if replayErr != nil {
			return replayErr
		}
		if replayed {
			result, replayErr = goldenRuntimeDecodeResult[usecase.GoldenParticipantView](replay.ResultPayload)
			return replayErr
		}
		if authorityErr := goldenRuntimeAuthorityConflictForTarget(
			command.ExpectedRuntimeRevision, head.Revision,
			command.ExpectedReadyWindowID, participant.ReadyWindowID,
			command.ExpectedAttemptID, participant.AttemptID,
		); authorityErr != nil {
			return authorityErr
		}
		if participant.State != "prepared" {
			return domain.ErrConflict
		}
		if _, disconnectErr := q.LockOpenGoldenReadyDisconnect(txCtx, sqlc.LockOpenGoldenReadyDisconnectParams{
			MembershipID: participant.MembershipID, AttemptID: participant.AttemptID,
			TournamentID: command.TournamentID, RosterID: participant.RosterID,
		}); disconnectErr == nil {
			return domain.ErrConflict
		} else if !errors.Is(disconnectErr, pgx.ErrNoRows) {
			return goldenRuntimeReadError("load Golden connection", disconnectErr)
		}
		if participant.State == "prepared" && !now.Before(participant.ReadyWindowDeadline.Time) {
			return domain.ErrConflict
		}
		if !participant.ReadyAt.Valid {
			if _, err = q.MarkGoldenMembershipReady(txCtx, sqlc.MarkGoldenMembershipReadyParams{
				ReadyAt: tstz(now), ID: participant.MembershipID, AttemptID: participant.AttemptID,
				TournamentID: command.TournamentID, RosterID: participant.RosterID,
			}); err != nil {
				return goldenRuntimeWriteError("mark ready", err)
			}
		}
		members, err := q.ListGoldenRuntimeAttemptMembers(txCtx, sqlc.ListGoldenRuntimeAttemptMembersParams{
			AttemptID: participant.AttemptID, TournamentID: command.TournamentID,
		})
		if err != nil {
			return goldenRuntimeReadError("load readiness", err)
		}
		allReady := false
		eligible := 0
		for _, member := range members {
			if member.NoShowAt.Valid || member.ExcludedAt.Valid {
				continue
			}
			eligible++
			if eligible == 1 {
				allReady = true
			}
			allReady = allReady && member.ReadyAt.Valid
		}
		if eligible > 0 && allReady && participant.State == "prepared" {
			_, err = q.UpdateGoldenAttemptCAS(txCtx, goldenAttemptUpdate(participant.AttemptID, command.TournamentID, participant.RosterID, "prepared", "ready", now, now, time.Time{}, time.Time{}))
			if err != nil {
				return goldenRuntimeWriteError("ready attempt", err)
			}
		}
		advanced, err := q.AdvanceGoldenRuntimeHead(txCtx, sqlc.AdvanceGoldenRuntimeHeadParams{
			TournamentID: command.TournamentID, RosterID: participant.RosterID,
			ExpectedRevision: head.Revision, NextRevision: head.Revision + 1, UpdatedAt: tstz(now),
		})
		if err != nil {
			return goldenRuntimeWriteError("advance Golden runtime head", err)
		}
		result, err = goldenParticipantRuntimeViewWithin(txCtx, q, command.TournamentID, command.PlayerID)
		if err != nil {
			return err
		}
		payload, err := goldenRuntimeResultPayload(result)
		if err != nil {
			return err
		}
		return repository.appendGoldenRuntimeEvidence(txCtx, q, spec, advanced, "participant", payload, now)
	})
	if err != nil {
		return usecase.GoldenParticipantView{}, fmt.Errorf("GoldenRuntimePostgres - SetReady: %w", err)
	}
	return result, nil
}

//nolint:gocyclo // Start validates the full ready window and connection set in one transaction.
func (repository *GoldenRuntimePostgres) Start(
	ctx context.Context,
	command usecase.GoldenStartCommand,
	now time.Time,
) (usecase.GoldenOperatorView, error) {
	var result usecase.GoldenOperatorView
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		q := repository.tx.Querier(txCtx)
		assignment, err := q.GetGoldenRuntimeAssignment(txCtx, sqlc.GetGoldenRuntimeAssignmentParams{AttemptID: command.AttemptID, TournamentID: command.TournamentID})
		if err != nil {
			return goldenRuntimeReadError("load assignment", err)
		}
		head, err := q.LockGoldenRuntimeHead(txCtx, sqlc.LockGoldenRuntimeHeadParams{
			TournamentID: command.TournamentID, RosterID: assignment.RosterID,
		})
		if err != nil {
			return goldenRuntimeReadError("lock Golden runtime head", err)
		}
		spec := goldenRuntimeCommandSpec{
			CommandID: command.CommandID, TournamentID: command.TournamentID, RosterID: assignment.RosterID,
			ActorKind: goldenRuntimeCommandActorKind(true), ActorID: command.ActorID,
			Scope: "operator", Kind: "start", AttemptID: command.AttemptID,
			ExpectedRuntimeRevision: command.ExpectedRuntimeRevision,
			ExpectedReadyWindowID:   command.ExpectedReadyWindowID, Payload: struct{}{},
		}
		replay, replayed, replayErr := goldenRuntimeReplay(txCtx, q, spec)
		if replayErr != nil {
			return replayErr
		}
		if replayed {
			result, replayErr = goldenRuntimeDecodeResult[usecase.GoldenOperatorView](replay.ResultPayload)
			return replayErr
		}
		if authorityErr := goldenRuntimeAuthorityConflict(
			command.ExpectedRuntimeRevision, head.Revision,
			command.ExpectedReadyWindowID, assignment.ReadyWindowID,
		); authorityErr != nil {
			return authorityErr
		}
		attempt, err := q.LockGoldenAttempt(txCtx, sqlc.LockGoldenAttemptParams{ID: command.AttemptID, TournamentID: command.TournamentID, RosterID: assignment.RosterID})
		if err != nil {
			return goldenRuntimeReadError("lock attempt", err)
		}
		if attempt.State == "active" {
			return domain.ErrConflict
		}
		if attempt.State == "technical_pause" {
			if err := repository.resumeGoldenRuntimeAfterTechnicalPause(txCtx, q, assignment, now); err != nil {
				return err
			}
		} else {
			if attempt.State != "ready" {
				return domain.ErrConflict
			}
			if !assignment.ReadyWindowDeadline.Valid || !now.Before(assignment.ReadyWindowDeadline.Time) {
				return domain.ErrConflict
			}
			members, err := q.ListGoldenRuntimeAttemptMembers(txCtx, sqlc.ListGoldenRuntimeAttemptMembersParams{AttemptID: command.AttemptID, TournamentID: command.TournamentID})
			if err != nil {
				return goldenRuntimeReadError("load members", err)
			}
			activeMembers := 0
			for _, member := range members {
				if member.NoShowAt.Valid || member.ExcludedAt.Valid {
					continue
				}
				activeMembers++
				if !member.ReadyAt.Valid {
					return domain.ErrConflict
				}
				if _, disconnectErr := q.LockOpenGoldenReadyDisconnect(txCtx, sqlc.LockOpenGoldenReadyDisconnectParams{
					MembershipID: member.MembershipID, AttemptID: command.AttemptID,
					TournamentID: command.TournamentID, RosterID: assignment.RosterID,
				}); disconnectErr == nil {
					return domain.ErrConflict
				} else if !errors.Is(disconnectErr, pgx.ErrNoRows) {
					return goldenRuntimeReadError("load Golden connection", disconnectErr)
				}
				if _, err = q.EstablishGoldenParticipation(txCtx, sqlc.EstablishGoldenParticipationParams{
					EstablishedAt: tstz(now), ID: member.MembershipID, AttemptID: command.AttemptID,
					TournamentID: command.TournamentID, RosterID: assignment.RosterID,
				}); err != nil {
					return goldenRuntimeWriteError("establish participation", err)
				}
			}
			if activeMembers == 0 {
				return domain.ErrConflict
			}
			if _, err = q.StartGoldenRuntimeAssignment(txCtx, sqlc.StartGoldenRuntimeAssignmentParams{StartedAt: tstz(now), AttemptID: command.AttemptID, TournamentID: command.TournamentID}); err != nil {
				return goldenRuntimeWriteError("start assignment", err)
			}
			if _, err = q.UpdateGoldenAttemptCAS(txCtx, goldenAttemptUpdate(command.AttemptID, command.TournamentID, assignment.RosterID, "ready", "active", attempt.DisclosedAt.Time, attempt.ReadyAt.Time, now, time.Time{})); err != nil {
				return goldenRuntimeWriteError("start attempt", err)
			}
		}
		advanced, err := q.AdvanceGoldenRuntimeHead(txCtx, sqlc.AdvanceGoldenRuntimeHeadParams{
			TournamentID: command.TournamentID, RosterID: assignment.RosterID,
			ExpectedRevision: head.Revision, NextRevision: head.Revision + 1, UpdatedAt: tstz(now),
		})
		if err != nil {
			return goldenRuntimeWriteError("advance Golden runtime head", err)
		}
		result, err = goldenOperatorRuntimeViewWithin(txCtx, q, command.TournamentID, now)
		if err != nil {
			return err
		}
		payload, err := goldenRuntimeResultPayload(result)
		if err != nil {
			return err
		}
		return repository.appendGoldenRuntimeEvidence(txCtx, q, spec, advanced, "operator", payload, now)
	})
	if err != nil {
		return usecase.GoldenOperatorView{}, fmt.Errorf("GoldenRuntimePostgres - Start: %w", err)
	}
	return result, nil
}

func goldenAttemptUpdate(attemptID, tournamentID, rosterID uuid.UUID, expected, next string, disclosedAt, readyAt, startedAt, completedAt time.Time) sqlc.UpdateGoldenAttemptCASParams {
	return sqlc.UpdateGoldenAttemptCASParams{
		NextState: next, DisclosedAt: goldenRuntimeTimestamp(disclosedAt), ReadyAt: goldenRuntimeTimestamp(readyAt),
		StartedAt: goldenRuntimeTimestamp(startedAt), CompletedAt: goldenRuntimeTimestamp(completedAt),
		ID: attemptID, TournamentID: tournamentID, RosterID: rosterID,
		ExpectedState: expected,
	}
}

func goldenRuntimeTimestamp(value time.Time) pgtype.Timestamptz {
	if value.IsZero() {
		return pgtype.Timestamptz{}
	}
	return tstz(value)
}

func goldenRuntimeDigest(value any) [sha256.Size]byte {
	payload, err := json.Marshal(value)
	if err != nil {
		return sha256.Sum256([]byte("invalid-golden-runtime-evidence"))
	}
	return sha256.Sum256(payload)
}

func goldenRuntimeReadError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, domain.ErrTournamentNotFound)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func goldenRuntimeWriteError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func validGoldenFlag(submitted, expected string) bool {
	return len(submitted) == len(expected) && subtle.ConstantTimeCompare([]byte(submitted), []byte(expected)) == 1
}
