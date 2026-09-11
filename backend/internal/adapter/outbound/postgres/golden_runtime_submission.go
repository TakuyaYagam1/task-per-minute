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
			Scope: "participant", Kind: "submit", AttemptID: command.ExpectedAttemptID,
			ParticipantID: participant.ParticipantID, ExpectedRuntimeRevision: command.ExpectedRuntimeRevision,
			ExpectedReadyWindowID: command.ExpectedReadyWindowID,
			Payload: struct {
				SubmittedFlag string `json:"submitted_flag"`
			}{SubmittedFlag: command.SubmittedFlag},
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
		if participant.State != "active" || !participant.ReadyAt.Valid || !participant.StartedAt.Valid ||
			!participant.Deadline.Valid || !now.Before(participant.Deadline.Time) {
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
				return domain.ErrConflict
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
			Flag          string    `json:"flag"`
		}{participant.AttemptID, participant.ParticipantID, sequence, now, command.SubmittedFlag})
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
			if err := repository.completeGoldenRuntimeAttempt(txCtx, q, participant, now); err != nil {
				return err
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
		resultPayload, err := goldenRuntimeResultPayload(result)
		if err != nil {
			return err
		}
		return repository.appendGoldenRuntimeEvidence(txCtx, q, spec, advanced, "participant", resultPayload, now)
	})
	if err != nil {
		return usecase.GoldenParticipantView{}, fmt.Errorf("GoldenRuntimePostgres - Submit: %w", err)
	}
	return result, nil
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
