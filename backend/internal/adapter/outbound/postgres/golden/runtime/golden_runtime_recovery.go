package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

// Recover advances every elapsed production Golden boundary in the same
// transaction that records recovery evidence. It is intentionally the only
// worker entry point for ready-window expiry, deadline closure, and reserve
// continuation.
//
//nolint:gocyclo // Boundary selection, replay, state change, and evidence must remain in one transaction.
func (repository *GoldenRuntimePostgres) Recover(ctx context.Context, tournamentID uuid.UUID, now time.Time) error {
	return repository.tx.Do(ctx, func(txCtx context.Context) error {
		q := repository.tx.Querier(txCtx)
		attempts, err := q.ListGoldenRuntimeRecoveryAttempts(txCtx, tournamentID)
		if err != nil {
			return goldenRuntimeReadError("list recovery attempts", err)
		}
		for _, attempt := range attempts {
			// User commands lock the runtime head before the attempt and its
			// memberships. The worker follows the same order so an exact-deadline
			// submission cannot pass while recovery is deciding the boundary.
			head, err := q.LockGoldenRuntimeHead(txCtx, sqlc.LockGoldenRuntimeHeadParams{
				TournamentID: attempt.TournamentID, RosterID: attempt.RosterID,
			})
			if err != nil {
				return goldenRuntimeReadError("lock Golden runtime head for recovery", err)
			}
			if _, err := q.LockGoldenAttempt(txCtx, sqlc.LockGoldenAttemptParams{
				ID: attempt.AttemptID, TournamentID: attempt.TournamentID, RosterID: attempt.RosterID,
			}); err != nil {
				return goldenRuntimeReadError("lock Golden attempt for recovery", err)
			}
			boundary, err := repository.goldenRuntimeRecoveryBoundary(txCtx, q, attempt, now)
			if err != nil {
				return err
			}
			if boundary == "" {
				continue
			}
			spec := goldenRuntimeRecoverySpec(attempt, boundary, head.Revision)
			replay, replayed, replayErr := goldenRuntimeRecoveryReplay(txCtx, q, spec)
			if replayErr != nil {
				return replayErr
			}
			if replayed {
				if replay.ResultingRuntimeRevision != head.Revision {
					return domain.ErrConflict
				}
				continue
			}
			if err := repository.applyGoldenRuntimeRecoveryBoundary(txCtx, q, attempt, boundary, now); err != nil {
				return err
			}
			if err := ensureGoldenRuntimeRecoveryStable(txCtx, q, attempt, now); err != nil {
				return err
			}
			assignment, err := q.GetGoldenRuntimeAssignment(txCtx, sqlc.GetGoldenRuntimeAssignmentParams{
				AttemptID: attempt.AttemptID, TournamentID: attempt.TournamentID,
			})
			if err != nil {
				return goldenRuntimeReadError("load recovered Golden assignment", err)
			}
			advanced, err := q.AdvanceGoldenRuntimeHead(txCtx, sqlc.AdvanceGoldenRuntimeHeadParams{
				TournamentID: attempt.TournamentID, RosterID: attempt.RosterID,
				ExpectedRevision: head.Revision, NextRevision: head.Revision + 1, UpdatedAt: tstz(now),
			})
			if err != nil {
				return goldenRuntimeWriteError("advance recovered Golden runtime head", err)
			}
			resultPayload, err := goldenRuntimeRecoveryResultPayload(
				attempt, boundary, assignment.State, advanced.Revision,
			)
			if err != nil {
				return err
			}
			if err := repository.appendGoldenRuntimeEvidence(
				txCtx, q, spec, advanced, "recovery", resultPayload, now,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

//nolint:gocyclo // The state machine enumerates every elapsed production boundary explicitly.
func (repository *GoldenRuntimePostgres) goldenRuntimeRecoveryBoundary(
	ctx context.Context,
	q *sqlc.Queries,
	attempt sqlc.ListGoldenRuntimeRecoveryAttemptsRow,
	now time.Time,
) (string, error) {
	switch attempt.State {
	case "prepared", "ready":
		if !attempt.ReadyWindowDeadline.Valid {
			return "", domain.ErrConflict
		}
		if now.Before(attempt.ReadyWindowDeadline.Time) {
			return "", nil
		}
		members, err := q.ListGoldenRuntimeAttemptMembers(ctx, sqlc.ListGoldenRuntimeAttemptMembersParams{
			AttemptID: attempt.AttemptID, TournamentID: attempt.TournamentID,
		})
		if err != nil {
			return "", goldenRuntimeReadError("load Golden recovery readiness", err)
		}
		for _, member := range members {
			if member.ReadyAt.Valid && !member.NoShowAt.Valid && !member.ExcludedAt.Valid {
				return "ready_timeout", nil
			}
		}
		return "no_show", nil
	case "active":
		if !attempt.StartedAt.Valid || !attempt.Deadline.Valid ||
			!attempt.Deadline.Time.Equal(attempt.StartedAt.Time.Add(goldenRuntimeDuration)) {
			return "", domain.ErrConflict
		}
		if now.Before(attempt.Deadline.Time) {
			return "", nil
		}
		members, err := q.ListGoldenRuntimeAttemptMembers(ctx, sqlc.ListGoldenRuntimeAttemptMembersParams{
			AttemptID: attempt.AttemptID, TournamentID: attempt.TournamentID,
		})
		if err != nil {
			return "", goldenRuntimeReadError("load Golden deadline recovery", err)
		}
		accepted := 0
		for _, member := range members {
			if member.SubmissionID.Valid && member.PositionCommitID.Valid {
				accepted++
			}
		}
		if accepted == 0 {
			return "technical_pause", nil
		}
		unresolved, err := q.ListGoldenRuntimeUnresolvedMembers(ctx, sqlc.ListGoldenRuntimeUnresolvedMembersParams{
			TournamentID: attempt.TournamentID, GroupRevisionID: attempt.GroupRevisionID,
		})
		if err != nil {
			return "", goldenRuntimeReadError("load Golden deadline survivors", err)
		}
		if len(unresolved) == 0 {
			return "completion", nil
		}
		if attempt.EdgePosition >= 3 {
			return "technical_pause", nil
		}
		return "reserve_creation", nil
	case "technical_pause":
		if !attempt.StartedAt.Valid || !attempt.Deadline.Valid {
			return "", domain.ErrConflict
		}
	default:
		return "", domain.ErrConflict
	}
	return "", nil
}

func (repository *GoldenRuntimePostgres) applyGoldenRuntimeRecoveryBoundary(
	ctx context.Context,
	q *sqlc.Queries,
	attempt sqlc.ListGoldenRuntimeRecoveryAttemptsRow,
	boundary string,
	now time.Time,
) error {
	switch boundary {
	case "ready_timeout", "no_show":
		return repository.expireGoldenRuntimeReadyWindow(ctx, q, attempt, now)
	case "technical_pause", "completion", "reserve_creation":
		return repository.closeGoldenRuntimeDeadline(ctx, q, attempt, now)
	default:
		return domain.ErrConflict
	}
}

func (repository *GoldenRuntimePostgres) expireGoldenRuntimeReadyWindow(
	ctx context.Context,
	q *sqlc.Queries,
	attempt sqlc.ListGoldenRuntimeRecoveryAttemptsRow,
	now time.Time,
) error {
	members, err := q.ListGoldenRuntimeAttemptMembers(ctx, sqlc.ListGoldenRuntimeAttemptMembersParams{
		AttemptID: attempt.AttemptID, TournamentID: attempt.TournamentID,
	})
	if err != nil {
		return goldenRuntimeReadError("load expired readiness", err)
	}
	readyCount := 0
	for _, member := range members {
		if member.ReadyAt.Valid && !member.NoShowAt.Valid && !member.ExcludedAt.Valid {
			readyCount++
			continue
		}
		if member.NoShowAt.Valid || member.ExcludedAt.Valid {
			continue
		}
		if _, err = q.MarkGoldenMembershipNoShow(ctx, sqlc.MarkGoldenMembershipNoShowParams{
			NoShowAt: tstz(now), ID: member.MembershipID, AttemptID: attempt.AttemptID,
			TournamentID: attempt.TournamentID, RosterID: attempt.RosterID,
		}); err != nil {
			return goldenRuntimeWriteError("mark Golden no-show", err)
		}
	}
	if readyCount == 0 {
		return repository.completeGoldenRuntimeNoShowGroup(ctx, q, attempt, now)
	}
	if attempt.State == "prepared" {
		if _, err = q.UpdateGoldenAttemptCAS(ctx, goldenAttemptUpdate(
			attempt.AttemptID, attempt.TournamentID, attempt.RosterID,
			"prepared", "ready", now, now, time.Time{}, time.Time{},
		)); err != nil {
			return goldenRuntimeWriteError("ready Golden no-show survivors", err)
		}
	}
	if _, err = q.ResetGoldenRuntimeReadyWindow(ctx, sqlc.ResetGoldenRuntimeReadyWindowParams{
		ReadyWindowID: uuid.New(), OpenedAt: tstz(now), AttemptID: attempt.AttemptID,
		TournamentID: attempt.TournamentID,
	}); err != nil {
		return goldenRuntimeWriteError("open post-no-show ready window", err)
	}
	return recordGoldenRuntimeRecovery(ctx, q, attempt, now)
}

func (repository *GoldenRuntimePostgres) completeGoldenRuntimeNoShowGroup(
	ctx context.Context,
	q *sqlc.Queries,
	attempt sqlc.ListGoldenRuntimeRecoveryAttemptsRow,
	now time.Time,
) error {
	if attempt.State == "prepared" {
		if _, err := q.UpdateGoldenAttemptCAS(ctx, goldenAttemptUpdate(
			attempt.AttemptID, attempt.TournamentID, attempt.RosterID,
			"prepared", "ready", now, now, time.Time{}, time.Time{},
		)); err != nil {
			return goldenRuntimeWriteError("ready empty Golden attempt", err)
		}
	}
	if _, err := q.StartGoldenRuntimeAssignment(ctx, sqlc.StartGoldenRuntimeAssignmentParams{
		StartedAt: tstz(now), AttemptID: attempt.AttemptID, TournamentID: attempt.TournamentID,
	}); err != nil {
		return goldenRuntimeWriteError("start empty Golden attempt", err)
	}
	if _, err := q.UpdateGoldenAttemptCAS(ctx, goldenAttemptUpdate(
		attempt.AttemptID, attempt.TournamentID, attempt.RosterID,
		"ready", "active", now, now, now, time.Time{},
	)); err != nil {
		return goldenRuntimeWriteError("activate empty Golden attempt", err)
	}
	if err := repository.appendGoldenRuntimeNoShowPositions(
		ctx, q, attempt.TournamentID, attempt.GroupRevisionID, now,
	); err != nil {
		return err
	}
	if err := repository.completeGoldenAttemptState(ctx, q, attempt.AttemptID, attempt.TournamentID, attempt.RosterID, now); err != nil {
		return err
	}
	return repository.finalizeGoldenRuntimeGroup(ctx, q, attempt.TournamentID, attempt.GroupRevisionID, now)
}

func (repository *GoldenRuntimePostgres) closeGoldenRuntimeDeadline(
	ctx context.Context,
	q *sqlc.Queries,
	attempt sqlc.ListGoldenRuntimeRecoveryAttemptsRow,
	now time.Time,
) error {
	members, err := q.ListGoldenRuntimeAttemptMembers(ctx, sqlc.ListGoldenRuntimeAttemptMembersParams{
		AttemptID: attempt.AttemptID, TournamentID: attempt.TournamentID,
	})
	if err != nil {
		return goldenRuntimeReadError("load deadline submissions", err)
	}
	accepted := 0
	for _, member := range members {
		if member.SubmissionID.Valid && member.PositionCommitID.Valid {
			accepted++
		}
	}
	if accepted == 0 {
		if err := repository.ensureGoldenRuntimeFailureEvidence(ctx, q, attempt, members, now); err != nil {
			return err
		}
		if err := appendGoldenRuntimeRecoveryState(ctx, q, attempt, "technical_pause", now); err != nil {
			return err
		}
		_, err = q.UpdateGoldenAttemptCAS(ctx, sqlc.UpdateGoldenAttemptCASParams{
			NextState: "technical_pause", DisclosedAt: tstz(attempt.StartedAt.Time),
			ReadyAt: tstz(attempt.StartedAt.Time), StartedAt: attempt.StartedAt, PausedAt: tstz(now),
			ID: attempt.AttemptID, TournamentID: attempt.TournamentID, RosterID: attempt.RosterID,
			ExpectedState: "active",
		})
		return goldenRuntimeWriteError("pause common Golden failure", err)
	}
	return repository.continueOrFinalizeGoldenRuntime(ctx, q, attempt, now)
}

func (repository *GoldenRuntimePostgres) continueOrFinalizeGoldenRuntime(
	ctx context.Context,
	q *sqlc.Queries,
	attempt sqlc.ListGoldenRuntimeRecoveryAttemptsRow,
	now time.Time,
) error {
	unresolved, err := q.ListGoldenRuntimeUnresolvedMembers(ctx, sqlc.ListGoldenRuntimeUnresolvedMembersParams{
		TournamentID: attempt.TournamentID, GroupRevisionID: attempt.GroupRevisionID,
	})
	if err != nil {
		return goldenRuntimeReadError("load unresolved Golden members", err)
	}
	if len(unresolved) == 0 {
		if err := repository.completeGoldenAttemptState(
			ctx, q, attempt.AttemptID, attempt.TournamentID, attempt.RosterID, now,
		); err != nil {
			return err
		}
		return repository.finalizeGoldenRuntimeGroup(ctx, q, attempt.TournamentID, attempt.GroupRevisionID, now)
	}
	if attempt.EdgePosition >= 3 {
		locked, lockErr := q.LockGoldenAttempt(ctx, sqlc.LockGoldenAttemptParams{
			ID: attempt.AttemptID, TournamentID: attempt.TournamentID, RosterID: attempt.RosterID,
		})
		if lockErr != nil {
			return goldenRuntimeReadError("lock exhausted Golden attempt", lockErr)
		}
		if locked.State == "technical_pause" {
			return nil
		}
		if locked.State != "active" {
			return domain.ErrConflict
		}
		attempt.StartedAt = locked.StartedAt
		if err = appendGoldenRuntimeRecoveryState(ctx, q, attempt, "technical_pause", now); err != nil {
			return err
		}
		_, err = q.UpdateGoldenAttemptCAS(ctx, sqlc.UpdateGoldenAttemptCASParams{
			NextState: "technical_pause", DisclosedAt: locked.DisclosedAt,
			ReadyAt: locked.ReadyAt, StartedAt: locked.StartedAt, PausedAt: tstz(now),
			ID: attempt.AttemptID, TournamentID: attempt.TournamentID, RosterID: attempt.RosterID,
			ExpectedState: "active",
		})
		return goldenRuntimeWriteError("pause exhausted Golden reserves", err)
	}
	group, err := repository.loadGoldenRuntimeGroup(ctx, q, attempt.TournamentID, attempt.GroupRevisionID, unresolved)
	if err != nil {
		return err
	}
	next, err := q.NextGoldenRuntimeAttempt(ctx, attempt.TournamentID)
	if err != nil {
		return goldenRuntimeReadError("allocate reserve attempt", err)
	}
	if err = repository.createGoldenRuntimeAttempt(
		ctx, q, group, next.AttemptNumber, next.PreviousAttemptID, attempt.EdgePosition+1, now,
	); err != nil {
		return err
	}
	return repository.completeGoldenAttemptState(
		ctx, q, attempt.AttemptID, attempt.TournamentID, attempt.RosterID, now,
	)
}

func (repository *GoldenRuntimePostgres) loadGoldenRuntimeGroup(
	ctx context.Context,
	q *sqlc.Queries,
	tournamentID uuid.UUID,
	groupRevisionID uuid.UUID,
	participantIDs []uuid.UUID,
) (goldenRuntimeGroup, error) {
	rows, err := q.ListGoldenRuntimeGroups(ctx, tournamentID)
	if err != nil {
		return goldenRuntimeGroup{}, goldenRuntimeReadError("load reserve group", err)
	}
	if len(rows) == 0 {
		return goldenRuntimeGroup{}, domain.ErrConflict
	}
	groups, err := goldenRuntimeGroups(rows, rows[0].SourceProjectionRevision)
	if err != nil {
		return goldenRuntimeGroup{}, err
	}
	for _, group := range groups {
		if group.groupRevisionID != groupRevisionID {
			continue
		}
		group.participantIDs = append([]uuid.UUID(nil), participantIDs...)
		return group, nil
	}
	return goldenRuntimeGroup{}, domain.ErrConflict
}

func (repository *GoldenRuntimePostgres) completeGoldenAttemptState(
	ctx context.Context,
	q *sqlc.Queries,
	attemptID uuid.UUID,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	now time.Time,
) error {
	attempt, err := q.LockGoldenAttempt(ctx, sqlc.LockGoldenAttemptParams{
		ID: attemptID, TournamentID: tournamentID, RosterID: rosterID,
	})
	if err != nil {
		return goldenRuntimeReadError("lock completed Golden attempt", err)
	}
	if attempt.State == "completed" {
		return nil
	}
	if attempt.State != "active" && attempt.State != "technical_pause" {
		return domain.ErrConflict
	}
	_, err = q.UpdateGoldenAttemptCAS(ctx, goldenAttemptUpdate(
		attemptID, tournamentID, rosterID, attempt.State, "completed",
		attempt.DisclosedAt.Time, attempt.ReadyAt.Time, attempt.StartedAt.Time, now,
	))
	return goldenRuntimeWriteError("complete Golden attempt", err)
}

func (repository *GoldenRuntimePostgres) ensureGoldenRuntimeFailureEvidence(
	ctx context.Context,
	q *sqlc.Queries,
	attempt sqlc.ListGoldenRuntimeRecoveryAttemptsRow,
	members []sqlc.ListGoldenRuntimeAttemptMembersRow,
	now time.Time,
) error {
	for _, member := range members {
		if member.SubmissionRevisionID.Valid {
			return nil
		}
	}
	if len(members) == 0 {
		return domain.ErrConflict
	}
	_, err := createGoldenRuntimeSyntheticSubmission(
		ctx, q, members[0].MembershipID, members[0].ParticipantID,
		attempt.AttemptID, attempt.TournamentID, attempt.RosterID, 1,
		"common_failure", now,
	)
	return err
}

func createGoldenRuntimeSyntheticSubmission(
	ctx context.Context,
	q *sqlc.Queries,
	membershipID uuid.UUID,
	participantID uuid.UUID,
	attemptID uuid.UUID,
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	position int16,
	reason string,
	now time.Time,
) (uuid.UUID, error) {
	revision, err := q.NextGoldenAttemptSubmissionRevision(ctx, attemptID)
	if err != nil {
		return uuid.Nil, goldenRuntimeReadError("allocate Golden terminal revision", err)
	}
	digest := goldenRuntimeDigest(struct {
		AttemptID     uuid.UUID `json:"attempt_id"`
		ParticipantID uuid.UUID `json:"participant_id"`
		Reason        string    `json:"reason"`
		RecordedAt    time.Time `json:"recorded_at"`
	}{attemptID, participantID, reason, now})
	submissionID := uuid.New()
	status := "rejected"
	rejectionReason := &reason
	if reason == "no_show_fallback" {
		status = "accepted"
		rejectionReason = nil
	}
	if _, err := q.CreateGoldenProvisionalSubmission(ctx, sqlc.CreateGoldenProvisionalSubmissionParams{
		ID: submissionID, AttemptID: attemptID, TournamentID: tournamentID, RosterID: rosterID,
		MembershipID: membershipID, ParticipantID: participantID, ServerSequence: revision.RevisionNumber,
		IdempotencyKey: uuid.New(), ProvisionalPosition: position, ElapsedMilliseconds: 0,
		Status: status, RejectionReason: rejectionReason, PayloadDigest: digest[:],
		SubmittedAt: tstz(now), ReceivedAt: tstz(now), CreatedAt: tstz(now),
	}); err != nil {
		return uuid.Nil, goldenRuntimeWriteError("create Golden terminal evidence", err)
	}
	revisionID := uuid.New()
	if _, err := q.CreateGoldenAttemptSubmissionRevision(ctx, sqlc.CreateGoldenAttemptSubmissionRevisionParams{
		RevisionID: revisionID, AttemptID: attemptID, TournamentID: tournamentID, RosterID: rosterID,
		MembershipID: membershipID, ParticipantID: participantID, RevisionNumber: revision.RevisionNumber,
		PreviousRevisionID: uuid.NullUUID{
			UUID: revision.PreviousRevisionID, Valid: revision.RevisionNumber > 1,
		},
		ProvisionalSubmissionID: submissionID, PayloadDigest: digest[:], CommittedAt: tstz(now), CreatedAt: tstz(now),
	}); err != nil {
		return uuid.Nil, goldenRuntimeWriteError("create Golden terminal revision", err)
	}
	return submissionID, nil
}

func (repository *GoldenRuntimePostgres) finalizeGoldenRuntimeGroup(
	ctx context.Context,
	q *sqlc.Queries,
	tournamentID uuid.UUID,
	groupRevisionID uuid.UUID,
	now time.Time,
) error {
	if err := repository.appendGoldenRuntimeNoShowPositions(ctx, q, tournamentID, groupRevisionID, now); err != nil {
		return err
	}
	attempts, evidence, err := loadGoldenRuntimeTerminalEvidence(ctx, q, tournamentID, groupRevisionID)
	if err != nil {
		return err
	}
	if len(attempts) == 0 {
		return domain.ErrConflict
	}
	finalID := uuid.New()
	settlementRevisionID := uuid.New()
	var previous uuid.NullUUID
	for revisionIndex := 0; revisionIndex <= len(attempts); revisionIndex++ {
		revisionID := uuid.New()
		if revisionIndex == len(attempts) {
			revisionID = finalID
		}
		included := revisionIndex + 1
		if included > len(attempts) {
			included = len(attempts)
		}
		digest := goldenRuntimeDigest(struct {
			RevisionID uuid.UUID                              `json:"revision_id"`
			PreviousID uuid.NullUUID                          `json:"previous_id"`
			Attempts   []goldenRuntimeTerminalAttemptEvidence `json:"attempts"`
		}{revisionID, previous, evidence[:included]})
		if _, err = q.CreateGoldenPositionLedgerRevision(ctx, sqlc.CreateGoldenPositionLedgerRevisionParams{
			RevisionID: revisionID, TournamentID: tournamentID, RosterID: attempts[0].RosterID,
			GroupRevisionID: groupRevisionID, RevisionNumber: int64(revisionIndex + 1),
			PreviousRevisionID: previous, PayloadDigest: digest[:], FinalizedAt: tstz(now), CreatedAt: tstz(now),
		}); err != nil {
			return goldenRuntimeWriteError("create cumulative Golden ledger", err)
		}
		for attemptIndex := range included {
			if err = persistGoldenRuntimeTerminalAttempt(ctx, q, revisionID, attempts[attemptIndex], evidence[attemptIndex], now); err != nil {
				return err
			}
		}
		if _, err = q.SealGoldenPositionLedgerRevision(ctx, sqlc.SealGoldenPositionLedgerRevisionParams{
			LedgerRevisionID: revisionID, TournamentID: tournamentID, RosterID: attempts[0].RosterID,
			PayloadDigest: digest[:], SealedAt: tstz(now),
		}); err != nil {
			return goldenRuntimeWriteError("seal cumulative Golden ledger", err)
		}
		previous = uuid.NullUUID{UUID: revisionID, Valid: true}
	}
	updated, err := q.FinalizeGoldenRuntimeGroupAssignments(ctx, sqlc.FinalizeGoldenRuntimeGroupAssignmentsParams{
		SettlementRevisionID: uuid.NullUUID{UUID: settlementRevisionID, Valid: true}, FinalizedAt: tstz(now),
		TournamentID: tournamentID, GroupRevisionID: groupRevisionID,
	})
	if err != nil {
		return goldenRuntimeWriteError("finalize Golden group assignments", err)
	}
	if updated != int64(len(attempts)) {
		return domain.ErrConflict
	}
	return nil
}

type goldenRuntimeTerminalAttemptEvidence struct {
	AttemptID            uuid.UUID
	SubmissionRevisionID uuid.UUID
	SubmissionRevision   int64
	Commits              []sqlc.ListGoldenRuntimeGroupEvidenceRow
}

func loadGoldenRuntimeTerminalEvidence(
	ctx context.Context,
	q *sqlc.Queries,
	tournamentID uuid.UUID,
	groupRevisionID uuid.UUID,
) ([]sqlc.ListGoldenRuntimeGroupAttemptsRow, []goldenRuntimeTerminalAttemptEvidence, error) {
	attempts, err := q.ListGoldenRuntimeGroupAttempts(ctx, sqlc.ListGoldenRuntimeGroupAttemptsParams{
		TournamentID: tournamentID, GroupRevisionID: groupRevisionID,
	})
	if err != nil {
		return nil, nil, goldenRuntimeReadError("load terminal Golden attempts", err)
	}
	rows, err := q.ListGoldenRuntimeGroupEvidence(ctx, sqlc.ListGoldenRuntimeGroupEvidenceParams{
		TournamentID: tournamentID, GroupRevisionID: groupRevisionID,
	})
	if err != nil {
		return nil, nil, goldenRuntimeReadError("load terminal Golden evidence", err)
	}
	byAttempt := make(map[uuid.UUID]*goldenRuntimeTerminalAttemptEvidence, len(attempts))
	evidence := make([]goldenRuntimeTerminalAttemptEvidence, len(attempts))
	for index, attempt := range attempts {
		if attempt.State != "completed" {
			return nil, nil, domain.ErrConflict
		}
		evidence[index].AttemptID = attempt.AttemptID
		byAttempt[attempt.AttemptID] = &evidence[index]
	}
	for _, row := range rows {
		current := byAttempt[row.AttemptID]
		if current == nil || !row.SubmissionRevisionID.Valid || row.SubmissionRevision == nil {
			continue
		}
		if current.SubmissionRevisionID == uuid.Nil || *row.SubmissionRevision > current.SubmissionRevision {
			current.SubmissionRevisionID = row.SubmissionRevisionID.UUID
			current.SubmissionRevision = *row.SubmissionRevision
		}
		if row.PositionCommitID.Valid {
			current.Commits = append(current.Commits, row)
		}
	}
	for _, current := range evidence {
		if current.SubmissionRevisionID == uuid.Nil {
			return nil, nil, domain.ErrConflict
		}
	}
	return attempts, evidence, nil
}

func persistGoldenRuntimeTerminalAttempt(
	ctx context.Context,
	q *sqlc.Queries,
	ledgerRevisionID uuid.UUID,
	attempt sqlc.ListGoldenRuntimeGroupAttemptsRow,
	evidence goldenRuntimeTerminalAttemptEvidence,
	now time.Time,
) error {
	if len(evidence.Commits) > domain.TournamentMaxParticipants {
		return domain.ErrConflict
	}
	orderCount := int16(len(evidence.Commits)) //nolint:gosec // Domain bounds cap Golden groups at 16 members.
	if _, err := q.CreateGoldenPositionLedgerAttempt(ctx, sqlc.CreateGoldenPositionLedgerAttemptParams{
		LedgerRevisionID: ledgerRevisionID, AttemptID: attempt.AttemptID,
		SubmissionRevisionID: evidence.SubmissionRevisionID, TournamentID: attempt.TournamentID,
		RosterID: attempt.RosterID, GroupRevisionID: attempt.GroupRevisionID,
		AttemptNumber: attempt.AttemptNumber, OrderCount: orderCount, CreatedAt: tstz(now),
	}); err != nil {
		return goldenRuntimeWriteError("create cumulative Golden ledger attempt", err)
	}
	for _, commit := range evidence.Commits {
		if commit.Position == nil || len(commit.PayloadDigest) != 32 {
			return domain.ErrConflict
		}
		if _, err := q.CreateGoldenPositionLedgerCommitBinding(ctx, sqlc.CreateGoldenPositionLedgerCommitBindingParams{
			LedgerRevisionID: ledgerRevisionID, AttemptID: attempt.AttemptID,
			PositionCommitID: commit.PositionCommitID.UUID, TournamentID: attempt.TournamentID,
			RosterID: attempt.RosterID, ParticipantID: commit.ParticipantID, Position: *commit.Position,
			EvidenceDigest: commit.PayloadDigest, CreatedAt: tstz(now),
		}); err != nil {
			return goldenRuntimeWriteError("bind cumulative Golden position", err)
		}
	}
	return nil
}

//nolint:gocyclo // No-show terminal evidence is validated and appended as one ordered operation.
func (repository *GoldenRuntimePostgres) appendGoldenRuntimeNoShowPositions(
	ctx context.Context,
	q *sqlc.Queries,
	tournamentID uuid.UUID,
	groupRevisionID uuid.UUID,
	now time.Time,
) error {
	rows, err := q.ListGoldenRuntimeGroupEvidence(ctx, sqlc.ListGoldenRuntimeGroupEvidenceParams{
		TournamentID: tournamentID, GroupRevisionID: groupRevisionID,
	})
	if err != nil {
		return goldenRuntimeReadError("load Golden no-show evidence", err)
	}
	committed := make(map[uuid.UUID]struct{})
	noShows := make(map[uuid.UUID]sqlc.ListGoldenRuntimeGroupEvidenceRow)
	maxPosition := int16(0)
	minSourcePosition := int16(domain.TournamentMaxParticipants + 1)
	for _, row := range rows {
		if row.SourcePosition < minSourcePosition {
			minSourcePosition = row.SourcePosition
		}
		if row.PositionCommitID.Valid {
			committed[row.ParticipantID] = struct{}{}
			if row.Position != nil && *row.Position > maxPosition {
				maxPosition = *row.Position
			}
		}
		if row.NoShowAt.Valid {
			if existing, found := noShows[row.ParticipantID]; !found || row.SourcePosition < existing.SourcePosition {
				noShows[row.ParticipantID] = row
			}
		}
	}
	if maxPosition == 0 {
		maxPosition = minSourcePosition - 1
	}
	ordered := make([]sqlc.ListGoldenRuntimeGroupEvidenceRow, 0, len(noShows))
	for participantID, row := range noShows {
		if _, found := committed[participantID]; !found {
			ordered = append(ordered, row)
		}
	}
	slices.SortFunc(ordered, func(left, right sqlc.ListGoldenRuntimeGroupEvidenceRow) int {
		return int(left.SourcePosition - right.SourcePosition)
	})
	for _, row := range ordered {
		maxPosition++
		submissionID, createErr := createGoldenRuntimeSyntheticSubmission(
			ctx, q, row.MembershipID, row.ParticipantID, row.AttemptID, tournamentID,
			row.RosterID, maxPosition, "no_show_fallback", now,
		)
		if createErr != nil {
			return createErr
		}
		if _, err = q.CreateGoldenPositionCommit(ctx, sqlc.CreateGoldenPositionCommitParams{
			ID: uuid.New(), AttemptID: row.AttemptID, TournamentID: tournamentID, RosterID: row.RosterID,
			MembershipID: row.MembershipID, ParticipantID: row.ParticipantID,
			ProvisionalSubmissionID: submissionID, Position: maxPosition, CommittedAt: tstz(now), CreatedAt: tstz(now),
		}); err != nil {
			return goldenRuntimeWriteError("commit Golden no-show fallback", err)
		}
	}
	return nil
}

func recordGoldenRuntimeRecovery(
	ctx context.Context,
	q *sqlc.Queries,
	attempt sqlc.ListGoldenRuntimeRecoveryAttemptsRow,
	now time.Time,
) error {
	return appendGoldenRuntimeRecoveryState(ctx, q, attempt, "stable", now)
}

func ensureGoldenRuntimeRecoveryStable(
	ctx context.Context,
	q *sqlc.Queries,
	attempt sqlc.ListGoldenRuntimeRecoveryAttemptsRow,
	now time.Time,
) error {
	latest, err := q.GetLatestGoldenRecoveryRevision(ctx, sqlc.GetLatestGoldenRecoveryRevisionParams{
		AttemptID: attempt.AttemptID, TournamentID: attempt.TournamentID, RosterID: attempt.RosterID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return recordGoldenRuntimeRecovery(ctx, q, attempt, now)
	}
	if err != nil {
		return goldenRuntimeReadError("load recovery state after boundary", err)
	}
	if latest.State == "technical_pause" {
		return nil
	}
	return recordGoldenRuntimeRecovery(ctx, q, attempt, now)
}

func appendGoldenRuntimeRecoveryState(
	ctx context.Context,
	q *sqlc.Queries,
	attempt sqlc.ListGoldenRuntimeRecoveryAttemptsRow,
	state string,
	now time.Time,
) error {
	head, err := q.GetLatestGoldenRecoveryRevision(ctx, sqlc.GetLatestGoldenRecoveryRevisionParams{
		AttemptID: attempt.AttemptID, TournamentID: attempt.TournamentID, RosterID: attempt.RosterID,
	})
	revisionNumber := int64(1)
	previousRevisionID := uuid.NullUUID{}
	if err == nil {
		if state == "stable" || head.State == state {
			return nil
		}
		revisionNumber = head.RevisionNumber + 1
		previousRevisionID = uuid.NullUUID{UUID: head.ID, Valid: true}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return goldenRuntimeReadError("load recovery head", err)
	}
	evidence, err := json.Marshal(map[string]any{
		"attempt_id": attempt.AttemptID, "state": state,
		"edge_position": attempt.EdgePosition, "recorded_at": now,
	})
	if err != nil {
		return domain.ErrInternal
	}
	_, err = q.CreateGoldenRecoveryRevision(ctx, sqlc.CreateGoldenRecoveryRevisionParams{
		ID: uuid.New(), AttemptID: attempt.AttemptID, TournamentID: attempt.TournamentID,
		RosterID: attempt.RosterID, RevisionNumber: revisionNumber,
		PreviousRevisionID: previousRevisionID, State: state,
		RecoveryEvidence: evidence, RecordedAt: tstz(now), CreatedAt: tstz(now),
	})
	return goldenRuntimeWriteError("record Golden recovery", err)
}

func (repository *GoldenRuntimePostgres) resumeGoldenRuntimeAfterTechnicalPause(
	ctx context.Context,
	q *sqlc.Queries,
	assignment sqlc.GetGoldenRuntimeAssignmentRow,
	now time.Time,
) error {
	if assignment.EdgePosition >= 3 {
		return domain.ErrConflict
	}
	locked, err := q.LockGoldenAttempt(ctx, sqlc.LockGoldenAttemptParams{
		ID: assignment.AttemptID, TournamentID: assignment.TournamentID, RosterID: assignment.RosterID,
	})
	if err != nil {
		return goldenRuntimeReadError("lock paused Golden attempt", err)
	}
	attempt := sqlc.ListGoldenRuntimeRecoveryAttemptsRow{
		AttemptID: assignment.AttemptID, TournamentID: assignment.TournamentID,
		RosterID: assignment.RosterID, GroupRevisionID: assignment.GroupRevisionID,
		EdgePosition: assignment.EdgePosition, StartedAt: locked.StartedAt,
	}
	if err = appendGoldenRuntimeRecoveryState(ctx, q, attempt, "resumed", now); err != nil {
		return err
	}
	if _, err = q.UpdateGoldenAttemptCAS(ctx, goldenAttemptUpdate(
		assignment.AttemptID, assignment.TournamentID, assignment.RosterID,
		"technical_pause", "active", locked.DisclosedAt.Time, locked.ReadyAt.Time, locked.StartedAt.Time, time.Time{},
	)); err != nil {
		return goldenRuntimeWriteError("resume Golden attempt", err)
	}
	return repository.continueOrFinalizeGoldenRuntime(ctx, q, attempt, now)
}
