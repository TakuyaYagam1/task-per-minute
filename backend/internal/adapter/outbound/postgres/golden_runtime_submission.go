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
		if _, disconnectErr := q.LockOpenGoldenReadyDisconnect(txCtx, sqlc.LockOpenGoldenReadyDisconnectParams{
			MembershipID: participant.MembershipID, AttemptID: participant.AttemptID,
			TournamentID: command.TournamentID, RosterID: participant.RosterID,
		}); disconnectErr == nil {
			return domain.ErrConflict
		} else if !errors.Is(disconnectErr, pgx.ErrNoRows) {
			return goldenRuntimeReadError("load Golden connection", disconnectErr)
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
		eligible := 0
		for _, member := range members {
			if member.NoShowAt.Valid || member.ExcludedAt.Valid {
				continue
			}
			eligible++
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
		committed, err := q.CountGoldenRuntimeGroupCommits(txCtx, sqlc.CountGoldenRuntimeGroupCommitsParams{
			TournamentID: command.TournamentID, GroupRevisionID: participant.GroupRevisionID,
		})
		if err != nil {
			return goldenRuntimeReadError("count committed Golden positions", err)
		}
		position := participant.PositionFrom + int16(committed) //nolint:gosec // Golden groups are bounded to 16 members.
		if position > participant.PositionTo {
			return domain.ErrConflict
		}
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
		if accepted+1 == eligible {
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
	return repository.continueOrFinalizeGoldenRuntime(ctx, q, sqlc.ListGoldenRuntimeRecoveryAttemptsRow{
		AttemptID: participant.AttemptID, TournamentID: participant.TournamentID,
		RosterID: participant.RosterID, GroupRevisionID: participant.GroupRevisionID,
		EdgePosition: participant.EdgePosition,
	}, now)
}
