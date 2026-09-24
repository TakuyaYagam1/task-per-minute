package runtime

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/sqlc"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type goldenRuntimeTerminalCommit struct {
	EvidenceID    uuid.UUID
	CommitID      uuid.UUID
	ParticipantID uuid.UUID
	Position      int16
	Digest        [sha256.Size]byte
}

type goldenRuntimeTerminalPayload struct {
	Reason          string    `json:"reason"`
	ID              uuid.UUID `json:"id"`
	TournamentID    uuid.UUID `json:"tournament_id"`
	RosterID        uuid.UUID `json:"roster_id"`
	GroupRevisionID uuid.UUID `json:"group_revision_id"`
	AttemptID       uuid.UUID `json:"attempt_id"`
	MembershipID    uuid.UUID `json:"membership_id"`
	ParticipantID   uuid.UUID `json:"participant_id"`
	RuntimeRevision int64     `json:"runtime_revision"`
	Deadline        time.Time `json:"deadline"`
	Position        int16     `json:"position"`
	RecordedAt      time.Time `json:"recorded_at"`
}

// commitGoldenRuntimeTerminalPosition is exclusive to deadline closure. The
// caller holds the runtime head, attempt and membership locks in that order.
func (repository *GoldenRuntimePostgres) commitGoldenRuntimeTerminalPosition(
	ctx context.Context,
	q *sqlc.Queries,
	attempt sqlc.ListGoldenRuntimeRecoveryAttemptsRow,
	members []sqlc.ListGoldenRuntimeAttemptMembersRow,
	runtimeRevision int64,
	now time.Time,
) error {
	unresolved, err := q.ListGoldenRuntimeUnresolvedMembers(ctx, sqlc.ListGoldenRuntimeUnresolvedMembersParams{
		TournamentID: attempt.TournamentID, GroupRevisionID: attempt.GroupRevisionID,
	})
	if err != nil {
		return goldenRuntimeReadError("load Golden terminal candidate", err)
	}
	if len(unresolved) != 1 {
		return nil
	}
	assignment, err := q.GetGoldenRuntimeAssignment(ctx, sqlc.GetGoldenRuntimeAssignmentParams{
		AttemptID: attempt.AttemptID, TournamentID: attempt.TournamentID,
	})
	if err != nil {
		return goldenRuntimeReadError("load Golden terminal deadline", err)
	}
	if !goldenRuntimeTerminalAssignmentMatches(assignment, attempt) ||
		!goldenRuntimeTerminalDeadlineReached(assignment, runtimeRevision, now) {
		return fmt.Errorf("validate Golden terminal deadline authority: %w", domain.ErrConflict)
	}
	member, err := goldenRuntimeTerminalMember(members, unresolved[0], assignment.StartedAt.Time, assignment.Deadline.Time)
	if err != nil {
		return err
	}
	committed, err := q.CountGoldenRuntimeGroupCommits(ctx, sqlc.CountGoldenRuntimeGroupCommitsParams{
		TournamentID: attempt.TournamentID, GroupRevisionID: attempt.GroupRevisionID,
	})
	if err != nil {
		return goldenRuntimeReadError("count Golden terminal positions", err)
	}
	if committed < 1 || committed > domain.TournamentMaxParticipants {
		return domain.ErrConflict
	}
	position := assignment.PositionFrom + int16(committed)
	if position < assignment.PositionFrom || position > assignment.PositionTo {
		return domain.ErrConflict
	}
	payload := goldenRuntimeTerminalPayload{
		Reason: "sole_unresolved", ID: uuid.New(), TournamentID: assignment.TournamentID,
		RosterID: assignment.RosterID, GroupRevisionID: assignment.GroupRevisionID, AttemptID: assignment.AttemptID,
		MembershipID: member.MembershipID, ParticipantID: member.ParticipantID, RuntimeRevision: runtimeRevision,
		Deadline: assignment.Deadline.Time.UTC(), Position: position, RecordedAt: now.UTC().Truncate(time.Microsecond),
	}
	return persistGoldenRuntimeTerminalPosition(ctx, q, payload)
}

func goldenRuntimeTerminalAssignmentMatches(assignment sqlc.GetGoldenRuntimeAssignmentRow, attempt sqlc.ListGoldenRuntimeRecoveryAttemptsRow) bool {
	return assignment.TournamentID == attempt.TournamentID && assignment.RosterID == attempt.RosterID &&
		assignment.GroupRevisionID == attempt.GroupRevisionID && assignment.AttemptID == attempt.AttemptID
}

func goldenRuntimeTerminalDeadlineReached(assignment sqlc.GetGoldenRuntimeAssignmentRow, runtimeRevision int64, now time.Time) bool {
	return assignment.State == "active" && runtimeRevision > 0 &&
		assignment.StartedAt.Valid && assignment.Deadline.Valid && domain.IsValidServerTime(now) &&
		assignment.Deadline.Time.Equal(assignment.StartedAt.Time.Add(goldenRuntimeDuration)) &&
		!now.Before(assignment.Deadline.Time)
}

func goldenRuntimeTerminalMember(
	members []sqlc.ListGoldenRuntimeAttemptMembersRow,
	participantID uuid.UUID,
	startedAt, deadline time.Time,
) (sqlc.ListGoldenRuntimeAttemptMembersRow, error) {
	var candidate sqlc.ListGoldenRuntimeAttemptMembersRow
	accepted := false
	for _, member := range members {
		if goldenRuntimeRealSubmission(member) && goldenRuntimeSubmissionWithinDeadline(member, startedAt, deadline) {
			accepted = true
		}
		if member.ParticipantID == participantID {
			if candidate.MembershipID != uuid.Nil {
				return sqlc.ListGoldenRuntimeAttemptMembersRow{}, domain.ErrConflict
			}
			candidate = member
		}
	}
	if !accepted || !goldenRuntimeUnresolvedMember(candidate) {
		return sqlc.ListGoldenRuntimeAttemptMembersRow{}, fmt.Errorf("validate Golden terminal candidate and real submission: %w", domain.ErrConflict)
	}
	return candidate, nil
}

func goldenRuntimeSubmissionWithinDeadline(member sqlc.ListGoldenRuntimeAttemptMembersRow, startedAt, deadline time.Time) bool {
	return member.CommittedAt.Valid && !member.CommittedAt.Time.Before(startedAt) && member.CommittedAt.Time.Before(deadline)
}

func goldenRuntimeUnresolvedMember(member sqlc.ListGoldenRuntimeAttemptMembersRow) bool {
	return member.MembershipID != uuid.Nil && member.ParticipantID != uuid.Nil &&
		member.ReadyAt.Valid && member.ParticipationEstablishedAt.Valid &&
		!member.NoShowAt.Valid && !member.ExcludedAt.Valid && !member.SubmissionID.Valid && !member.PositionCommitID.Valid
}

func goldenRuntimeRealSubmission(member sqlc.ListGoldenRuntimeAttemptMembersRow) bool {
	return member.SubmissionID.Valid && member.SubmissionID.UUID != uuid.Nil &&
		member.PositionCommitID.Valid && member.PositionCommitID.UUID != uuid.Nil &&
		member.SubmissionRevisionID.Valid && member.SubmissionRevisionID.UUID != uuid.Nil &&
		member.ReadyAt.Valid && member.ParticipationEstablishedAt.Valid &&
		!member.NoShowAt.Valid && !member.ExcludedAt.Valid
}

func persistGoldenRuntimeTerminalPosition(ctx context.Context, q *sqlc.Queries, payload goldenRuntimeTerminalPayload) error {
	digest := goldenRuntimeDigest(payload)
	if _, err := q.CreateGoldenTerminalPositionEvidence(ctx, sqlc.CreateGoldenTerminalPositionEvidenceParams{
		ID: payload.ID, TournamentID: payload.TournamentID, RosterID: payload.RosterID,
		GroupRevisionID: payload.GroupRevisionID, AttemptID: payload.AttemptID,
		MembershipID: payload.MembershipID, ParticipantID: payload.ParticipantID,
		RuntimeRevision: payload.RuntimeRevision, Deadline: tstz(payload.Deadline), Position: payload.Position,
		PayloadDigest: digest[:], RecordedAt: tstz(payload.RecordedAt), CreatedAt: tstz(payload.RecordedAt),
	}); err != nil {
		return goldenRuntimeWriteError("create Golden terminal position evidence", err)
	}
	_, err := q.CreateGoldenTerminalPositionCommit(ctx, sqlc.CreateGoldenTerminalPositionCommitParams{
		ID: uuid.New(), AttemptID: payload.AttemptID, TournamentID: payload.TournamentID, RosterID: payload.RosterID,
		MembershipID: payload.MembershipID, ParticipantID: payload.ParticipantID,
		TerminalEvidenceID: uuid.NullUUID{UUID: payload.ID, Valid: true}, Position: payload.Position,
		CommittedAt: tstz(payload.RecordedAt), CreatedAt: tstz(payload.RecordedAt),
	})
	return goldenRuntimeWriteError("commit Golden terminal position", err)
}

func loadGoldenRuntimeTerminalCommit(
	ctx context.Context,
	q *sqlc.Queries,
	tournamentID, groupRevisionID uuid.UUID,
	attempts []sqlc.ListGoldenRuntimeGroupAttemptsRow,
	byAttempt map[uuid.UUID]*goldenRuntimeTerminalAttemptEvidence,
) error {
	if len(attempts) == 0 {
		return domain.ErrConflict
	}
	row, err := readGoldenRuntimeTerminalPosition(ctx, q, tournamentID, attempts[0].RosterID, groupRevisionID)
	if err != nil {
		return err
	}
	if row == nil {
		return nil
	}
	current := byAttempt[row.AttemptID]
	if current == nil || !goldenRuntimeTerminalAttemptMatches(*row, attempts) {
		return fmt.Errorf("restore Golden terminal attempt identity: %w", domain.ErrConflict)
	}
	if err := goldenRuntimeTerminalWatermark(current); err != nil {
		return err
	}
	commit, err := goldenRuntimeTerminalCommitFromRow(*row)
	if err != nil {
		return err
	}
	current.Terminal = &commit
	return nil
}

func goldenRuntimeTerminalAttemptMatches(row sqlc.GetGoldenTerminalPositionEvidenceRow, attempts []sqlc.ListGoldenRuntimeGroupAttemptsRow) bool {
	for _, attempt := range attempts {
		if attempt.AttemptID == row.AttemptID {
			return attempt.TournamentID == row.TournamentID && attempt.RosterID == row.RosterID &&
				attempt.GroupRevisionID == row.GroupRevisionID && attempt.Deadline.Valid &&
				attempt.Deadline.Time.Equal(row.Deadline.Time)
		}
	}
	return false
}

func goldenRuntimeTerminalCommitFromRow(row sqlc.GetGoldenTerminalPositionEvidenceRow) (goldenRuntimeTerminalCommit, error) {
	if !goldenRuntimeTerminalRowTimesValid(row) {
		return goldenRuntimeTerminalCommit{}, fmt.Errorf("restore Golden terminal timestamps: %w", domain.ErrConflict)
	}
	if row.ID == uuid.Nil || row.MembershipID == uuid.Nil ||
		row.ParticipantID == uuid.Nil || !row.PositionCommitID.Valid || row.PositionCommitID.UUID == uuid.Nil ||
		row.Position < 1 || row.Position > domain.TournamentMaxParticipants {
		return goldenRuntimeTerminalCommit{}, fmt.Errorf("restore Golden terminal commit identity: %w", domain.ErrConflict)
	}
	payload := goldenRuntimeTerminalPayload{
		Reason: "sole_unresolved", ID: row.ID, TournamentID: row.TournamentID, RosterID: row.RosterID,
		GroupRevisionID: row.GroupRevisionID, AttemptID: row.AttemptID, MembershipID: row.MembershipID,
		ParticipantID: row.ParticipantID, RuntimeRevision: row.RuntimeRevision,
		Deadline: row.Deadline.Time.UTC(), Position: row.Position, RecordedAt: row.RecordedAt.Time.UTC(),
	}
	digest := goldenRuntimeDigest(payload)
	if len(row.PayloadDigest) != sha256.Size || digest != [sha256.Size]byte(row.PayloadDigest) {
		return goldenRuntimeTerminalCommit{}, fmt.Errorf("restore Golden terminal payload digest: %w", domain.ErrConflict)
	}
	return goldenRuntimeTerminalCommit{
		EvidenceID: row.ID, CommitID: row.PositionCommitID.UUID, ParticipantID: row.ParticipantID,
		Position: row.Position, Digest: digest,
	}, nil
}

func goldenRuntimeTerminalRowTimesValid(row sqlc.GetGoldenTerminalPositionEvidenceRow) bool {
	return row.RuntimeRevision > 0 && row.Deadline.Valid && row.RecordedAt.Valid && row.CreatedAt.Valid &&
		domain.IsValidServerTime(row.Deadline.Time.UTC()) && domain.IsValidServerTime(row.RecordedAt.Time.UTC()) &&
		!row.RecordedAt.Time.Before(row.Deadline.Time) && row.CreatedAt.Time.Equal(row.RecordedAt.Time)
}

func readGoldenRuntimeTerminalPosition(
	ctx context.Context,
	q *sqlc.Queries,
	tournamentID, rosterID, groupRevisionID uuid.UUID,
) (*sqlc.GetGoldenTerminalPositionEvidenceRow, error) {
	row, err := q.GetGoldenTerminalPositionEvidence(ctx, sqlc.GetGoldenTerminalPositionEvidenceParams{
		TournamentID: tournamentID, RosterID: rosterID, GroupRevisionID: groupRevisionID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, goldenRuntimeReadError("load Golden terminal position evidence", err)
	}
	if !row.PositionCommitID.Valid || row.PositionCommitID.UUID == uuid.Nil {
		return nil, domain.ErrConflict
	}
	return &row, nil
}

func goldenRuntimeTerminalWatermark(current *goldenRuntimeTerminalAttemptEvidence) error {
	var revisionID uuid.UUID
	var revision int64
	for _, commit := range current.Commits {
		if commit.NoShowAt.Valid || !commit.SubmissionID.Valid || !commit.SubmissionRevisionID.Valid || commit.SubmissionRevision == nil {
			continue
		}
		if *commit.SubmissionRevision > revision {
			revisionID = commit.SubmissionRevisionID.UUID
			revision = *commit.SubmissionRevision
		}
	}
	if revisionID == uuid.Nil || revision < 1 {
		return domain.ErrConflict
	}
	current.SubmissionRevisionID = revisionID
	current.SubmissionRevision = revision
	return nil
}

func persistGoldenRuntimeTerminalCommit(
	ctx context.Context,
	q *sqlc.Queries,
	ledgerID uuid.UUID,
	attempt sqlc.ListGoldenRuntimeGroupAttemptsRow,
	commit goldenRuntimeTerminalCommit,
	now time.Time,
) error {
	_, err := q.CreateGoldenPositionLedgerCommitBinding(ctx, sqlc.CreateGoldenPositionLedgerCommitBindingParams{
		LedgerRevisionID: ledgerID, AttemptID: attempt.AttemptID, PositionCommitID: commit.CommitID,
		TournamentID: attempt.TournamentID, RosterID: attempt.RosterID, ParticipantID: commit.ParticipantID,
		Position: commit.Position, EvidenceDigest: commit.Digest[:], CreatedAt: tstz(now),
	})
	return goldenRuntimeWriteError("bind Golden terminal position", err)
}
