package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

//nolint:gocyclo // The transaction deliberately keeps replay, validation, and evidence writes atomic.
func (repository *GoldenRuntimePostgres) Submit(
	ctx context.Context,
	command usecase.GoldenSubmissionCommand,
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
		if replay, err := q.GetGoldenSubmissionByIdempotencyKey(txCtx, command.CommandID); err == nil {
			if replay.TournamentID != command.TournamentID || replay.ParticipantID != participant.ParticipantID ||
				replay.AttemptID != participant.AttemptID {
				return domain.ErrConflict
			}
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return goldenRuntimeReadError("load submission replay", err)
		}
		if participant.State != "active" || !participant.ReadyAt.Valid || !participant.StartedAt.Valid ||
			!participant.Deadline.Valid || now.After(participant.Deadline.Time) {
			return domain.ErrConflict
		}
		if !validGoldenFlag(command.SubmittedFlag, participant.Flag) {
			return domain.ErrValidation
		}
		members, err := q.ListGoldenRuntimeAttemptMembers(txCtx, sqlc.ListGoldenRuntimeAttemptMembersParams{
			AttemptID: participant.AttemptID, TournamentID: command.TournamentID,
		})
		if err != nil {
			return goldenRuntimeReadError("load submissions", err)
		}
		var previousRevision uuid.NullUUID
		accepted := 0
		for _, member := range members {
			if member.ParticipantID == participant.ParticipantID && member.SubmissionID.Valid {
				return nil
			}
			if member.SubmissionID.Valid {
				accepted++
				if member.SubmissionRevisionID.Valid {
					previousRevision = member.SubmissionRevisionID
				}
			}
		}
		sequence := int64(accepted + 1)
		position := participant.PositionFrom + int16(accepted)
		digest := goldenRuntimeDigest(struct {
			AttemptID     uuid.UUID `json:"attempt_id"`
			ParticipantID uuid.UUID `json:"participant_id"`
			Sequence      int64     `json:"sequence"`
			ReceivedAt    time.Time `json:"received_at"`
		}{participant.AttemptID, participant.ParticipantID, sequence, now})
		submissionID := uuid.New()
		if _, err = q.CreateGoldenProvisionalSubmission(txCtx, sqlc.CreateGoldenProvisionalSubmissionParams{
			ID: submissionID, AttemptID: participant.AttemptID, TournamentID: command.TournamentID,
			RosterID: participant.RosterID, MembershipID: participant.MembershipID,
			ParticipantID: participant.ParticipantID, ServerSequence: sequence,
			IdempotencyKey: command.CommandID, ProvisionalPosition: position,
			ElapsedMilliseconds: now.Sub(participant.StartedAt.Time).Milliseconds(), Status: "accepted",
			PayloadDigest: digest[:], SubmittedAt: tstz(now), ReceivedAt: tstz(now), CreatedAt: tstz(now),
		}); err != nil {
			return goldenRuntimeWriteError("create submission", err)
		}
		revisionID := uuid.New()
		if _, err = q.CreateGoldenAttemptSubmissionRevision(txCtx, sqlc.CreateGoldenAttemptSubmissionRevisionParams{
			RevisionID: revisionID, AttemptID: participant.AttemptID, TournamentID: command.TournamentID,
			RosterID: participant.RosterID, MembershipID: participant.MembershipID,
			ParticipantID: participant.ParticipantID, RevisionNumber: sequence,
			PreviousRevisionID: previousRevision, ProvisionalSubmissionID: submissionID,
			PayloadDigest: digest[:], CommittedAt: tstz(now), CreatedAt: tstz(now),
		}); err != nil {
			return goldenRuntimeWriteError("create submission revision", err)
		}
		commitID := uuid.New()
		if _, err = q.CreateGoldenPositionCommit(txCtx, sqlc.CreateGoldenPositionCommitParams{
			ID: commitID, AttemptID: participant.AttemptID, TournamentID: command.TournamentID,
			RosterID: participant.RosterID, MembershipID: participant.MembershipID,
			ParticipantID: participant.ParticipantID, ProvisionalSubmissionID: submissionID,
			Position: position, CommittedAt: tstz(now), CreatedAt: tstz(now),
		}); err != nil {
			return goldenRuntimeWriteError("create position commit", err)
		}
		if accepted+1 == len(members) {
			return repository.completeGoldenRuntimeAttempt(txCtx, q, participant, now)
		}
		return nil
	})
	if err != nil {
		return usecase.GoldenParticipantView{}, fmt.Errorf("GoldenRuntimePostgres - Submit: %w", err)
	}
	return repository.ParticipantView(ctx, usecase.GoldenParticipantQuery{TournamentID: command.TournamentID, PlayerID: command.PlayerID})
}

func (repository *GoldenRuntimePostgres) completeGoldenRuntimeAttempt(
	ctx context.Context,
	q *sqlc.Queries,
	participant sqlc.LockGoldenRuntimeParticipantRow,
	now time.Time,
) error {
	members, err := q.ListGoldenRuntimeAttemptMembers(ctx, sqlc.ListGoldenRuntimeAttemptMembersParams{
		AttemptID: participant.AttemptID, TournamentID: participant.TournamentID,
	})
	if err != nil {
		return goldenRuntimeReadError("reload completed submissions", err)
	}
	return repository.persistGoldenRuntimeLedger(ctx, q, participant, members, now)
}

func (repository *GoldenRuntimePostgres) persistGoldenRuntimeLedger(
	ctx context.Context,
	q *sqlc.Queries,
	participant sqlc.LockGoldenRuntimeParticipantRow,
	members []sqlc.ListGoldenRuntimeAttemptMembersRow,
	now time.Time,
) error {
	// Implemented in golden_runtime_view.go, where the tournament-scoped view
	// and terminal ledger share one normalized mapper.
	return repository.persistGoldenRuntimeLedgerRows(ctx, q, participant, members, now)
}
