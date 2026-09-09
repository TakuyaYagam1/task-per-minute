package golden

import (
	"fmt"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

func validateGoldenRecoveryGroup(group GoldenRecoveryGroup) error {
	if group.ID == uuid.Nil || group.TournamentID == uuid.Nil || group.RevisionID.IsZero() ||
		group.SourceProjectionRevisionID.IsZero() || group.RevisionID == group.SourceProjectionRevisionID ||
		group.PositionFrom < 1 || group.PositionTo < group.PositionFrom ||
		group.PositionTo-group.PositionFrom+1 != len(group.ParticipantIDs) || len(group.ParticipantIDs) < 2 {
		return goldenRecoveryError("invalid group identity", nil)
	}
	seen := make(map[uuid.UUID]struct{}, len(group.ParticipantIDs))
	for _, participantID := range group.ParticipantIDs {
		if participantID == uuid.Nil {
			return goldenRecoveryError("missing participant identity", nil)
		}
		if _, exists := seen[participantID]; exists {
			return goldenRecoveryError("duplicate participant", nil)
		}
		seen[participantID] = struct{}{}
	}
	return nil
}

func goldenRecoveryLiveIndex(attempts []GoldenRecoveryAttempt) (int, error) {
	liveIndex := -1
	for i := range attempts {
		if attempts[i].Attempt.State.IsTerminal() {
			continue
		}
		if liveIndex >= 0 {
			return -1, fmt.Errorf("%w: %w", ErrInvalidGoldenRecovery, ErrAmbiguousGoldenRecovery)
		}
		liveIndex = i
	}
	if liveIndex >= 0 && liveIndex != len(attempts)-1 {
		return -1, fmt.Errorf("%w: %w", ErrInvalidGoldenRecovery, ErrAmbiguousGoldenRecovery)
	}
	return liveIndex, nil
}

func validateGoldenRecoveryAttempt(
	record GoldenRecoveryAttempt,
	group GoldenRecoveryGroup,
	groupMemberIDs map[uuid.UUID]struct{},
	permanentlyExcluded map[uuid.UUID]struct{},
) error {
	if err := record.Attempt.Validate(); err != nil {
		return goldenRecoveryError("invalid attempt", err)
	}
	if record.Attempt.GroupID != group.ID || record.Attempt.GroupRevisionID != group.RevisionID {
		return goldenRecoveryError("attempt belongs to another group revision", nil)
	}
	memberships, err := validateGoldenRecoveryMemberships(record, groupMemberIDs, permanentlyExcluded)
	if err != nil {
		return err
	}
	submissions, err := validateGoldenRecoverySubmissions(record, memberships)
	if err != nil {
		return err
	}
	if err := validateGoldenRecoveryCommits(record, submissions); err != nil {
		return err
	}
	return validateGoldenRecoveryDeadlines(record)
}

//nolint:gocyclo // Membership reconstruction checks identity, chronology, selection, and permanent exclusion together.
func validateGoldenRecoveryMemberships(
	record GoldenRecoveryAttempt,
	groupMemberIDs map[uuid.UUID]struct{},
	permanentlyExcluded map[uuid.UUID]struct{},
) (map[uuid.UUID]GoldenRecoveryMembership, error) {
	if len(record.Memberships) != len(groupMemberIDs) {
		return nil, goldenRecoveryError("membership evidence is incomplete", nil)
	}
	attemptParticipants := make(map[uuid.UUID]struct{}, len(record.Attempt.ParticipantIDs))
	for _, participantID := range record.Attempt.ParticipantIDs {
		attemptParticipants[participantID] = struct{}{}
	}
	memberships := make(map[uuid.UUID]GoldenRecoveryMembership, len(record.Memberships))
	seenMembershipIDs := make(map[uuid.UUID]struct{}, len(record.Memberships))
	reservePositions := make(map[int]struct{})
	for _, membership := range record.Memberships {
		if err := validateGoldenRecoveryMembership(membership); err != nil {
			return nil, err
		}
		if record.Attempt.StartedAt != nil &&
			((membership.ReadyAt != nil && membership.ReadyAt.After(*record.Attempt.StartedAt)) ||
				(membership.ParticipationEstablishedAt != nil && membership.ParticipationEstablishedAt.After(*record.Attempt.StartedAt))) {
			return nil, goldenRecoveryError("readiness postdates attempt start", nil)
		}
		if record.ReadyDeadline != nil && membership.ReadyAt != nil && membership.ReadyAt.After(*record.ReadyDeadline) {
			return nil, goldenRecoveryError("readiness exceeds durable deadline", nil)
		}
		if _, exists := groupMemberIDs[membership.ParticipantID]; !exists {
			return nil, goldenRecoveryError("membership has foreign participant", nil)
		}
		if _, exists := memberships[membership.ParticipantID]; exists {
			return nil, goldenRecoveryError("duplicate participant membership", nil)
		}
		if _, exists := seenMembershipIDs[membership.ID]; exists {
			return nil, goldenRecoveryError("duplicate membership identity", nil)
		}
		if membership.Selection == GoldenRecoverySelectionReserve {
			if _, exists := reservePositions[membership.ReservePosition]; exists {
				return nil, goldenRecoveryError("duplicate reserve position", nil)
			}
			reservePositions[membership.ReservePosition] = struct{}{}
		}
		_, wasExcluded := permanentlyExcluded[membership.ParticipantID]
		_, participates := attemptParticipants[membership.ParticipantID]
		if wasExcluded && membership.Selection != GoldenRecoverySelectionExcluded {
			return nil, goldenRecoveryError("permanent exclusion was reversed", nil)
		}
		if (membership.Selection != GoldenRecoverySelectionExcluded) != participates {
			return nil, goldenRecoveryError("membership does not match attempt participants", nil)
		}
		if membership.Selection == GoldenRecoverySelectionExcluded {
			permanentlyExcluded[membership.ParticipantID] = struct{}{}
		}
		memberships[membership.ParticipantID] = membership
		seenMembershipIDs[membership.ID] = struct{}{}
	}
	for participantID := range attemptParticipants {
		membership, exists := memberships[participantID]
		if !exists || membership.Selection == GoldenRecoverySelectionExcluded {
			return nil, goldenRecoveryError("attempt participant has no active membership", nil)
		}
		if record.Attempt.State == domain.GoldenAttemptStateActive && membership.ParticipationEstablishedAt == nil {
			return nil, goldenRecoveryError("live participant is not established", nil)
		}
	}
	return memberships, nil
}

//nolint:gocyclo // One flat validator keeps all mutually exclusive membership evidence explicit.
func validateGoldenRecoveryMembership(membership GoldenRecoveryMembership) error {
	if membership.ID == uuid.Nil || membership.ParticipantID == uuid.Nil || !goldenRecoveryUTC(membership.SelectedAt) {
		return goldenRecoveryError("invalid membership identity", nil)
	}
	if !goldenRecoveryOptionalTime(membership.ReadyAt) ||
		!goldenRecoveryOptionalTime(membership.ParticipationEstablishedAt) ||
		!goldenRecoveryOptionalTime(membership.PromotedAt) ||
		!goldenRecoveryOptionalTime(membership.ExcludedAt) {
		return goldenRecoveryError("invalid membership time", nil)
	}
	if membership.ReadyAt != nil && membership.ReadyAt.Before(membership.SelectedAt) {
		return goldenRecoveryError("readiness predates selection", nil)
	}
	if membership.ParticipationEstablishedAt != nil &&
		(membership.ReadyAt == nil || membership.ParticipationEstablishedAt.Before(*membership.ReadyAt)) {
		return goldenRecoveryError("participation lacks readiness", nil)
	}
	switch membership.Selection {
	case GoldenRecoverySelectionDirect:
		if membership.ReservePosition != 0 || membership.PromotedAt != nil || membership.ExcludedAt != nil {
			return goldenRecoveryError("invalid direct membership", nil)
		}
	case GoldenRecoverySelectionReserve:
		if membership.ReservePosition < 1 || membership.ExcludedAt != nil ||
			(membership.ParticipationEstablishedAt != nil && membership.PromotedAt == nil) {
			return goldenRecoveryError("invalid reserve membership", nil)
		}
	case GoldenRecoverySelectionExcluded:
		if membership.ReservePosition != 0 || membership.ReadyAt != nil ||
			membership.ParticipationEstablishedAt != nil || membership.PromotedAt != nil ||
			membership.ExcludedAt == nil || membership.ExcludedAt.Before(membership.SelectedAt) {
			return goldenRecoveryError("invalid excluded membership", nil)
		}
	default:
		return goldenRecoveryError("unknown membership selection", nil)
	}
	return nil
}

//nolint:gocyclo // Submission validation binds identity, order, membership, and attempt timing in one pass.
func validateGoldenRecoverySubmissions(
	record GoldenRecoveryAttempt,
	memberships map[uuid.UUID]GoldenRecoveryMembership,
) (map[uuid.UUID]GoldenRecoverySubmission, error) {
	if len(record.Submissions) > 0 && record.Attempt.StartedAt == nil {
		return nil, goldenRecoveryError("submission exists before attempt start", nil)
	}
	submissions := make(map[uuid.UUID]GoldenRecoverySubmission, len(record.Submissions))
	sequences := make(map[int64]struct{}, len(record.Submissions))
	for _, submission := range record.Submissions {
		membership, exists := memberships[submission.ParticipantID]
		if submission.ID == uuid.Nil || submission.MembershipID == uuid.Nil ||
			submission.ParticipantID == uuid.Nil || submission.ServerSequence < 1 ||
			submission.Position < 1 || submission.Position > len(record.Attempt.ParticipantIDs) ||
			!goldenRecoveryUTC(submission.AcceptedAt) || !exists || membership.ID != submission.MembershipID ||
			membership.ParticipationEstablishedAt == nil ||
			(record.Attempt.StartedAt != nil && submission.AcceptedAt.Before(*record.Attempt.StartedAt)) ||
			(record.Attempt.FinishedAt != nil && submission.AcceptedAt.After(*record.Attempt.FinishedAt)) {
			return nil, goldenRecoveryError("invalid provisional submission", nil)
		}
		if _, exists := submissions[submission.ID]; exists {
			return nil, goldenRecoveryError("duplicate submission identity", nil)
		}
		if _, exists := sequences[submission.ServerSequence]; exists {
			return nil, goldenRecoveryError("duplicate server sequence", nil)
		}
		submissions[submission.ID] = submission
		sequences[submission.ServerSequence] = struct{}{}
	}
	return submissions, nil
}

func validateGoldenRecoveryCommits(
	record GoldenRecoveryAttempt,
	submissions map[uuid.UUID]GoldenRecoverySubmission,
) error {
	commitIDs := make(map[uuid.UUID]struct{}, len(record.PositionCommits))
	participants := make(map[uuid.UUID]struct{}, len(record.PositionCommits))
	positions := make(map[int]struct{}, len(record.PositionCommits))
	for _, commit := range record.PositionCommits {
		submission, exists := submissions[commit.SubmissionID]
		if commit.ID == uuid.Nil || commit.AttemptID != record.Attempt.ID ||
			commit.ParticipantID == uuid.Nil || commit.Position < 1 ||
			!goldenRecoveryUTC(commit.CommittedAt) || !exists ||
			submission.ParticipantID != commit.ParticipantID || submission.Position != commit.Position ||
			commit.CommittedAt.Before(submission.AcceptedAt) {
			return goldenRecoveryError("invalid position commit", nil)
		}
		if _, exists := commitIDs[commit.ID]; exists {
			return goldenRecoveryError("duplicate position commit", nil)
		}
		if _, exists := participants[commit.ParticipantID]; exists {
			return goldenRecoveryError("participant has multiple position commits", nil)
		}
		if _, exists := positions[commit.Position]; exists {
			return goldenRecoveryError("position was committed twice", nil)
		}
		commitIDs[commit.ID] = struct{}{}
		participants[commit.ParticipantID] = struct{}{}
		positions[commit.Position] = struct{}{}
	}
	return nil
}

func validateGoldenRecoveryDeadlines(record GoldenRecoveryAttempt) error {
	if record.Attempt.State.IsTerminal() {
		if record.ReadyDeadline != nil || record.ExecutionDeadline != nil {
			return goldenRecoveryError("terminal attempt has live deadlines", nil)
		}
		return nil
	}
	if !goldenRecoveryOptionalTime(record.ReadyDeadline) || record.ReadyDeadline == nil {
		return goldenRecoveryError("live attempt lacks readiness deadline", nil)
	}
	if record.Attempt.State == domain.GoldenAttemptStateActive {
		if !goldenRecoveryOptionalTime(record.ExecutionDeadline) || record.ExecutionDeadline == nil ||
			record.Attempt.StartedAt == nil || !record.ExecutionDeadline.After(*record.Attempt.StartedAt) {
			return goldenRecoveryError("active attempt lacks execution deadline", nil)
		}
		return nil
	}
	if record.ExecutionDeadline != nil {
		return goldenRecoveryError("unstarted attempt has execution deadline", nil)
	}
	return nil
}
