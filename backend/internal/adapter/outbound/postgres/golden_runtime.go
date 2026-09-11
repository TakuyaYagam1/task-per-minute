package postgres

import (
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

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
	goldenusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden"
)

const goldenRuntimeDuration = 180 * time.Second

type GoldenRuntimePostgres struct{ tx *TxManager }

var _ goldenusecase.RuntimeRepository = (*GoldenRuntimePostgres)(nil)

func NewGoldenRuntimePostgres(tx *TxManager) *GoldenRuntimePostgres {
	return &GoldenRuntimePostgres{tx: tx}
}

func (repository *GoldenRuntimePostgres) Open(
	ctx context.Context,
	command usecase.GoldenOpenCommand,
	now time.Time,
) (usecase.GoldenOperatorView, error) {
	if repository == nil || repository.tx == nil {
		return usecase.GoldenOperatorView{}, domain.ErrInternal
	}
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
			return nil
		}
		rows, err := q.ListGoldenRuntimeGroups(txCtx, command.TournamentID)
		if err != nil {
			return goldenRuntimeReadError("load groups", err)
		}
		groups, err := goldenRuntimeGroups(rows, command.ExpectedProjectionRevision)
		if err != nil {
			return err
		}
		for _, group := range groups {
			attempt, err := q.NextGoldenRuntimeAttempt(txCtx, command.TournamentID)
			if err != nil {
				return goldenRuntimeReadError("allocate attempt number", err)
			}
			if err := repository.createGoldenRuntimeGroup(txCtx, q, group, attempt.AttemptNumber, attempt.PreviousAttemptID, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return usecase.GoldenOperatorView{}, fmt.Errorf("GoldenRuntimePostgres - Open: %w", err)
	}
	return repository.OperatorView(ctx, usecase.GoldenOperatorQuery{TournamentID: command.TournamentID, OperatorID: command.CommandID})
}

type goldenRuntimeGroup struct {
	tournamentID    uuid.UUID
	rosterID        uuid.UUID
	groupID         uuid.UUID
	groupRevisionID uuid.UUID
	positionFrom    int16
	positionTo      int16
	participantIDs  []uuid.UUID
}

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
			groups = append(groups, goldenRuntimeGroup{
				tournamentID: row.TournamentID, rosterID: row.RosterID, groupID: row.GroupID,
				groupRevisionID: row.GroupRevisionID, positionFrom: row.PositionFrom, positionTo: row.PositionTo,
			})
		}
		group := &groups[len(groups)-1]
		if group.tournamentID != row.TournamentID || group.rosterID != row.RosterID || group.groupID != row.GroupID ||
			group.positionFrom != row.PositionFrom || group.positionTo != row.PositionTo {
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

func (repository *GoldenRuntimePostgres) createGoldenRuntimeGroup(
	ctx context.Context,
	q *sqlc.Queries,
	group goldenRuntimeGroup,
	attemptNumber int32,
	previousAttemptID uuid.UUID,
	now time.Time,
) error {
	task, err := q.SelectGoldenRuntimeTask(ctx, sqlc.SelectGoldenRuntimeTaskParams{
		TournamentID: group.tournamentID, GroupRevisionID: group.groupRevisionID,
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
		GroupRevisionID: group.groupRevisionID, WaveID: task.EdgeID,
		AssignmentID: task.ReservationID, SnapshotID: task.SnapshotID,
		TaskID: task.TaskID, TaskVersion: task.Version, Title: task.Title, Category: task.Category,
		Difficulty: task.Difficulty, SourceDigest: task.ContentDigest, CreatedAt: tstz(now),
	}); err != nil {
		return goldenRuntimeWriteError("create assignment", err)
	}
	for _, participantID := range group.participantIDs {
		if _, err = q.CreateGoldenMembership(ctx, sqlc.CreateGoldenMembershipParams{
			ID: uuid.New(), AttemptID: attemptID, TournamentID: group.tournamentID, RosterID: group.rosterID,
			ParticipantID: participantID, SelectionKind: "direct", SelectedAt: tstz(now), CreatedAt: tstz(now),
		}); err != nil {
			return goldenRuntimeWriteError("create membership", err)
		}
	}
	return nil
}

func (repository *GoldenRuntimePostgres) SetReady(
	ctx context.Context,
	command usecase.GoldenReadyCommand,
	now time.Time,
) (usecase.GoldenParticipantView, error) {
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		q := repository.tx.Querier(txCtx)
		participant, err := q.LockGoldenRuntimeParticipant(txCtx, sqlc.LockGoldenRuntimeParticipantParams{
			TournamentID: command.TournamentID, PlayerID: command.PlayerID,
		})
		if err != nil {
			return goldenRuntimeReadError("load participant", err)
		}
		if participant.State != "prepared" && participant.State != "ready" {
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
		allReady := len(members) >= 2
		for _, member := range members {
			allReady = allReady && member.ReadyAt.Valid
		}
		if allReady && participant.State == "prepared" {
			_, err = q.UpdateGoldenAttemptCAS(txCtx, goldenAttemptUpdate(participant.AttemptID, command.TournamentID, participant.RosterID, "prepared", "ready", now, now, time.Time{}, time.Time{}))
			if err != nil {
				return goldenRuntimeWriteError("ready attempt", err)
			}
		}
		return nil
	})
	if err != nil {
		return usecase.GoldenParticipantView{}, fmt.Errorf("GoldenRuntimePostgres - SetReady: %w", err)
	}
	return repository.ParticipantView(ctx, usecase.GoldenParticipantQuery{TournamentID: command.TournamentID, PlayerID: command.PlayerID})
}

func (repository *GoldenRuntimePostgres) Start(
	ctx context.Context,
	command usecase.GoldenStartCommand,
	now time.Time,
) (usecase.GoldenOperatorView, error) {
	err := repository.tx.Do(ctx, func(txCtx context.Context) error {
		q := repository.tx.Querier(txCtx)
		assignment, err := q.GetGoldenRuntimeAssignment(txCtx, sqlc.GetGoldenRuntimeAssignmentParams{AttemptID: command.AttemptID, TournamentID: command.TournamentID})
		if err != nil {
			return goldenRuntimeReadError("load assignment", err)
		}
		attempt, err := q.LockGoldenAttempt(txCtx, sqlc.LockGoldenAttemptParams{ID: command.AttemptID, TournamentID: command.TournamentID, RosterID: assignment.RosterID})
		if err != nil {
			return goldenRuntimeReadError("lock attempt", err)
		}
		if attempt.State == "active" {
			return nil
		}
		if attempt.State != "ready" {
			return domain.ErrConflict
		}
		members, err := q.ListGoldenRuntimeAttemptMembers(txCtx, sqlc.ListGoldenRuntimeAttemptMembersParams{AttemptID: command.AttemptID, TournamentID: command.TournamentID})
		if err != nil {
			return goldenRuntimeReadError("load members", err)
		}
		for _, member := range members {
			if !member.ReadyAt.Valid {
				return domain.ErrConflict
			}
			if _, err = q.EstablishGoldenParticipation(txCtx, sqlc.EstablishGoldenParticipationParams{
				EstablishedAt: tstz(now), ID: member.MembershipID, AttemptID: command.AttemptID,
				TournamentID: command.TournamentID, RosterID: assignment.RosterID,
			}); err != nil {
				return goldenRuntimeWriteError("establish participation", err)
			}
		}
		if _, err = q.StartGoldenRuntimeAssignment(txCtx, sqlc.StartGoldenRuntimeAssignmentParams{StartedAt: tstz(now), AttemptID: command.AttemptID, TournamentID: command.TournamentID}); err != nil {
			return goldenRuntimeWriteError("start assignment", err)
		}
		_, err = q.UpdateGoldenAttemptCAS(txCtx, goldenAttemptUpdate(command.AttemptID, command.TournamentID, assignment.RosterID, "ready", "active", attempt.DisclosedAt.Time, attempt.ReadyAt.Time, now, time.Time{}))
		return goldenRuntimeWriteError("start attempt", err)
	})
	if err != nil {
		return usecase.GoldenOperatorView{}, fmt.Errorf("GoldenRuntimePostgres - Start: %w", err)
	}
	return repository.OperatorView(ctx, usecase.GoldenOperatorQuery{TournamentID: command.TournamentID, OperatorID: command.CommandID})
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
