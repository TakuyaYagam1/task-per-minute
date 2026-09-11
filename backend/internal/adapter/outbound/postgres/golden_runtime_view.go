package postgres

import (
	"context"
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
)

func (repository *GoldenRuntimePostgres) OperatorView(
	ctx context.Context,
	query usecase.GoldenOperatorQuery,
) (usecase.GoldenOperatorView, error) {
	rows, err := repository.tx.Querier(ctx).ListGoldenRuntimeView(ctx, query.TournamentID)
	if err != nil {
		return usecase.GoldenOperatorView{}, fmt.Errorf("GoldenRuntimePostgres - OperatorView: %w", err)
	}
	return goldenOperatorRuntimeView(query.TournamentID, rows, time.Now().UTC())
}

func goldenOperatorRuntimeView(
	tournamentID uuid.UUID,
	rows []sqlc.ListGoldenRuntimeViewRow,
	observedAt time.Time,
) (usecase.GoldenOperatorView, error) {
	view := usecase.GoldenOperatorView{TournamentID: tournamentID, Groups: []usecase.GoldenOperatorGroupView{}, ObservedAt: observedAt}
	for _, row := range rows {
		if row.TournamentID != tournamentID || row.GroupID == uuid.Nil || row.AttemptID == uuid.Nil {
			return usecase.GoldenOperatorView{}, domain.ErrConflict
		}
		if len(view.Groups) == 0 || view.Groups[len(view.Groups)-1].GroupRevisionID != row.GroupRevisionID {
			view.Groups = append(view.Groups, usecase.GoldenOperatorGroupView{
				GroupID: row.GroupID, GroupRevisionID: row.GroupRevisionID, AttemptID: row.AttemptID,
				State: row.State, PositionFrom: int(row.PositionFrom), PositionTo: int(row.PositionTo),
				StartedAt: goldenRuntimeTime(row.StartedAt), Deadline: goldenRuntimeTime(row.Deadline),
				Members: []usecase.GoldenMemberView{},
			})
		}
		group := &view.Groups[len(view.Groups)-1]
		if group.AttemptID != row.AttemptID || group.State != row.State {
			return usecase.GoldenOperatorView{}, domain.ErrConflict
		}
		group.Members = append(group.Members, usecase.GoldenMemberView{
			ParticipantID: row.ParticipantID, Ready: row.ReadyAt.Valid,
			Submitted: row.SubmissionID.Valid, Position: goldenRuntimePosition(row.Position),
		})
	}
	return view, nil
}

func (repository *GoldenRuntimePostgres) ParticipantView(
	ctx context.Context,
	query usecase.GoldenParticipantQuery,
) (usecase.GoldenParticipantView, error) {
	rows, err := repository.tx.Querier(ctx).ListGoldenRuntimeView(ctx, query.TournamentID)
	if err != nil {
		return usecase.GoldenParticipantView{}, fmt.Errorf("GoldenRuntimePostgres - ParticipantView: %w", err)
	}
	for _, row := range rows {
		if row.PlayerID != query.PlayerID {
			continue
		}
		view := usecase.GoldenParticipantView{
			TournamentID: row.TournamentID, ParticipantID: row.ParticipantID,
			GroupID: row.GroupID, GroupRevisionID: row.GroupRevisionID, AttemptID: row.AttemptID,
			State: row.State, Ready: row.ReadyAt.Valid, Submitted: row.SubmissionID.Valid,
			Position: goldenRuntimePosition(row.Position), StartedAt: goldenRuntimeTime(row.StartedAt),
			Deadline: goldenRuntimeTime(row.Deadline),
		}
		if row.State != "prepared" {
			view.Task = &usecase.GoldenTaskView{
				AssignmentID: row.AssignmentID, SnapshotID: row.SnapshotID, TaskID: row.TaskID,
				Title: row.Title, Category: row.Category, Difficulty: row.Difficulty,
				TimeLimitSeconds: int(row.TimeLimitSeconds),
			}
		}
		return view, nil
	}
	return usecase.GoldenParticipantView{}, domain.ErrTournamentNotFound
}

func goldenRuntimeTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid || value.Time.IsZero() {
		return nil
	}
	result := value.Time.UTC()
	return &result
}

func goldenRuntimePosition(position *int16) *int {
	if position == nil {
		return nil
	}
	value := int(*position)
	return &value
}

//nolint:gocyclo // Terminal evidence validation and both chained revisions form one atomic operation.
func (repository *GoldenRuntimePostgres) persistGoldenRuntimeLedgerRows(
	ctx context.Context,
	q *sqlc.Queries,
	participant sqlc.LockGoldenRuntimeParticipantRow,
	members []sqlc.ListGoldenRuntimeAttemptMembersRow,
	now time.Time,
) error {
	assignment, err := q.GetGoldenRuntimeAssignment(ctx, sqlc.GetGoldenRuntimeAssignmentParams{
		AttemptID: participant.AttemptID, TournamentID: participant.TournamentID,
	})
	if err != nil {
		return goldenRuntimeReadError("load terminal assignment", err)
	}
	if len(members) < 2 {
		return domain.ErrConflict
	}
	for _, member := range members {
		if !member.SubmissionID.Valid || member.ServerSequence == nil || !member.PositionCommitID.Valid ||
			member.Position == nil || !member.SubmissionRevisionID.Valid || member.SubmissionRevision == nil ||
			len(member.PayloadDigest) != 32 {
			return domain.ErrConflict
		}
	}
	type ledgerMemberEvidence struct {
		ParticipantID      uuid.UUID `json:"participant_id"`
		ServerSequence     int64     `json:"server_sequence"`
		Position           int16     `json:"position"`
		SubmissionRevision int64     `json:"submission_revision"`
		PayloadDigest      []byte    `json:"payload_digest"`
	}
	evidence := make([]ledgerMemberEvidence, len(members))
	for index, member := range members {
		evidence[index] = ledgerMemberEvidence{
			ParticipantID: member.ParticipantID, ServerSequence: *member.ServerSequence,
			Position: *member.Position, SubmissionRevision: *member.SubmissionRevision,
			PayloadDigest: append([]byte(nil), member.PayloadDigest...),
		}
	}
	rootID := uuid.New()
	finalID := uuid.New()
	rootDigest := goldenRuntimeDigest(struct {
		RevisionID uuid.UUID              `json:"revision_id"`
		AttemptID  uuid.UUID              `json:"attempt_id"`
		Members    []ledgerMemberEvidence `json:"members"`
	}{rootID, participant.AttemptID, evidence})
	if err = persistGoldenRuntimeLedgerRevision(
		ctx, q, participant, assignment, members, rootID, 1, uuid.NullUUID{}, rootDigest, now,
	); err != nil {
		return err
	}
	finalDigest := goldenRuntimeDigest(struct {
		RevisionID uuid.UUID              `json:"revision_id"`
		PreviousID uuid.UUID              `json:"previous_id"`
		AttemptID  uuid.UUID              `json:"attempt_id"`
		Members    []ledgerMemberEvidence `json:"members"`
	}{finalID, rootID, participant.AttemptID, evidence})
	if err = persistGoldenRuntimeLedgerRevision(
		ctx, q, participant, assignment, members, finalID, 2,
		uuid.NullUUID{UUID: rootID, Valid: true}, finalDigest, now,
	); err != nil {
		return err
	}
	settlementRevisionID := uuid.New()
	if _, err = q.FinalizeGoldenRuntimeAssignment(ctx, sqlc.FinalizeGoldenRuntimeAssignmentParams{
		SettlementRevisionID: uuid.NullUUID{UUID: settlementRevisionID, Valid: true}, FinalizedAt: tstz(now),
		AttemptID: participant.AttemptID, TournamentID: participant.TournamentID,
	}); err != nil {
		return goldenRuntimeWriteError("finalize runtime assignment", err)
	}
	attempt, err := q.LockGoldenAttempt(ctx, sqlc.LockGoldenAttemptParams{
		ID: participant.AttemptID, TournamentID: participant.TournamentID, RosterID: participant.RosterID,
	})
	if err != nil {
		return goldenRuntimeReadError("lock terminal attempt", err)
	}
	_, err = q.UpdateGoldenAttemptCAS(ctx, goldenAttemptUpdate(
		participant.AttemptID, participant.TournamentID, participant.RosterID,
		"active", "completed", attempt.DisclosedAt.Time, attempt.ReadyAt.Time, attempt.StartedAt.Time, now,
	))
	return goldenRuntimeWriteError("complete attempt", err)
}

func persistGoldenRuntimeLedgerRevision(
	ctx context.Context,
	q *sqlc.Queries,
	participant sqlc.LockGoldenRuntimeParticipantRow,
	assignment sqlc.GetGoldenRuntimeAssignmentRow,
	members []sqlc.ListGoldenRuntimeAttemptMembersRow,
	revisionID uuid.UUID,
	revisionNumber int64,
	previousRevisionID uuid.NullUUID,
	digest [32]byte,
	now time.Time,
) error {
	if _, err := q.CreateGoldenPositionLedgerRevision(ctx, sqlc.CreateGoldenPositionLedgerRevisionParams{
		RevisionID: revisionID, TournamentID: participant.TournamentID, RosterID: participant.RosterID,
		GroupRevisionID: participant.GroupRevisionID, RevisionNumber: revisionNumber,
		PreviousRevisionID: previousRevisionID, PayloadDigest: digest[:],
		FinalizedAt: tstz(now), CreatedAt: tstz(now),
	}); err != nil {
		return goldenRuntimeWriteError("create ledger revision", err)
	}
	last := members[len(members)-1]
	if _, err := q.CreateGoldenPositionLedgerAttempt(ctx, sqlc.CreateGoldenPositionLedgerAttemptParams{
		LedgerRevisionID: revisionID, AttemptID: participant.AttemptID,
		SubmissionRevisionID: last.SubmissionRevisionID.UUID, TournamentID: participant.TournamentID,
		RosterID: participant.RosterID, GroupRevisionID: participant.GroupRevisionID,
		AttemptNumber: assignment.AttemptNumber,
		//nolint:gosec // Golden groups are domain-bounded to at most 16 members.
		OrderCount: int16(len(members)), CreatedAt: tstz(now),
	}); err != nil {
		return goldenRuntimeWriteError("create ledger attempt", err)
	}
	for _, member := range members {
		if _, err := q.CreateGoldenPositionLedgerCommitBinding(ctx, sqlc.CreateGoldenPositionLedgerCommitBindingParams{
			LedgerRevisionID: revisionID, AttemptID: participant.AttemptID,
			PositionCommitID: member.PositionCommitID.UUID, TournamentID: participant.TournamentID,
			RosterID: participant.RosterID, ParticipantID: member.ParticipantID, Position: *member.Position,
			EvidenceDigest: member.PayloadDigest, CreatedAt: tstz(now),
		}); err != nil {
			return goldenRuntimeWriteError("bind ledger position", err)
		}
	}
	if _, err := q.SealGoldenPositionLedgerRevision(ctx, sqlc.SealGoldenPositionLedgerRevisionParams{
		LedgerRevisionID: revisionID, TournamentID: participant.TournamentID, RosterID: participant.RosterID,
		PayloadDigest: digest[:], SealedAt: tstz(now),
	}); err != nil {
		return goldenRuntimeWriteError("seal ledger revision", err)
	}
	return nil
}

func (repository *GoldenRuntimePostgres) Recover(ctx context.Context, tournamentID uuid.UUID, now time.Time) error {
	return repository.tx.Do(ctx, func(txCtx context.Context) error {
		q := repository.tx.Querier(txCtx)
		attempts, err := q.ListGoldenRuntimeRecoveryAttempts(txCtx, tournamentID)
		if err != nil {
			return goldenRuntimeReadError("list recovery attempts", err)
		}
		for _, attempt := range attempts {
			if !attempt.StartedAt.Valid || !attempt.Deadline.Valid ||
				!attempt.Deadline.Time.Equal(attempt.StartedAt.Time.Add(goldenRuntimeDuration)) {
				return domain.ErrConflict
			}
			_, err = q.GetLatestGoldenRecoveryRevision(txCtx, sqlc.GetLatestGoldenRecoveryRevisionParams{
				AttemptID: attempt.AttemptID, TournamentID: tournamentID, RosterID: attempt.RosterID,
			})
			if err == nil {
				continue
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return goldenRuntimeReadError("load recovery head", err)
			}
			evidence, marshalErr := json.Marshal(map[string]any{
				"attempt_id": attempt.AttemptID, "state": attempt.State,
				"started_at": attempt.StartedAt.Time.UTC(), "deadline": attempt.Deadline.Time.UTC(),
			})
			if marshalErr != nil {
				return domain.ErrInternal
			}
			if _, err = q.CreateGoldenRecoveryRevision(txCtx, sqlc.CreateGoldenRecoveryRevisionParams{
				ID: uuid.New(), AttemptID: attempt.AttemptID, TournamentID: tournamentID,
				RosterID: attempt.RosterID, RevisionNumber: 1, State: "stable",
				RecoveryEvidence: evidence, RecordedAt: tstz(now), CreatedAt: tstz(now),
			}); err != nil {
				return goldenRuntimeWriteError("record recovery", err)
			}
		}
		return nil
	})
}
