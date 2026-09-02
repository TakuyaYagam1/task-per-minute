package recovery

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidGoldenRecovery   = errors.New("invalid Golden recovery")
	ErrAmbiguousGoldenRecovery = errors.New("ambiguous live Golden attempts")
)

type GoldenRecoverySelection string

const (
	GoldenRecoverySelectionDirect   GoldenRecoverySelection = "direct"
	GoldenRecoverySelectionReserve  GoldenRecoverySelection = "reserve"
	GoldenRecoverySelectionExcluded GoldenRecoverySelection = "excluded"
)

type GoldenRecoveryGroup struct {
	ID                         uuid.UUID
	TournamentID               uuid.UUID
	RevisionID                 domain.ArenaDerivedRevisionID
	SourceProjectionRevisionID domain.ArenaDerivedRevisionID
	PositionFrom               int
	PositionTo                 int
	ParticipantIDs             []uuid.UUID
}

type GoldenRecoveryMembership struct {
	ID                         uuid.UUID
	ParticipantID              uuid.UUID
	Selection                  GoldenRecoverySelection
	ReservePosition            int
	SelectedAt                 time.Time
	ReadyAt                    *time.Time
	ParticipationEstablishedAt *time.Time
	PromotedAt                 *time.Time
	ExcludedAt                 *time.Time
}

type GoldenRecoverySubmission struct {
	ID             uuid.UUID
	MembershipID   uuid.UUID
	ParticipantID  uuid.UUID
	ServerSequence int64
	Position       int
	AcceptedAt     time.Time
}

type GoldenRecoveryPositionCommit struct {
	ID            uuid.UUID
	AttemptID     uuid.UUID
	SubmissionID  uuid.UUID
	ParticipantID uuid.UUID
	Position      int
	CommittedAt   time.Time
}

type GoldenRecoveryAttempt struct {
	Attempt           domain.ArenaGoldenAttempt
	Memberships       []GoldenRecoveryMembership
	Submissions       []GoldenRecoverySubmission
	PositionCommits   []GoldenRecoveryPositionCommit
	ReadyDeadline     *time.Time
	ExecutionDeadline *time.Time
}

type GoldenRecoveryInput struct {
	Group    GoldenRecoveryGroup
	Attempts []GoldenRecoveryAttempt
}

type GoldenRecoveryCommittedAttempt struct {
	AttemptID uuid.UUID
	AttemptNo int
	Positions []GoldenRecoveryPositionCommit
}

type GoldenRecoveryReserve struct {
	MembershipID  uuid.UUID
	ParticipantID uuid.UUID
	Position      int
	PromotedAt    *time.Time
}

type GoldenRecoveryProvisional struct {
	SubmissionID   uuid.UUID
	ParticipantID  uuid.UUID
	ServerSequence int64
	Position       int
}

type GoldenRecoveryDeadlines struct {
	Ready     time.Time
	Execution *time.Time
}

type GoldenRecoveryLiveAttempt struct {
	AttemptID           uuid.UUID
	Reserves            []GoldenRecoveryReserve
	ReadyParticipantIDs []uuid.UUID
	ProvisionalOrder    []GoldenRecoveryProvisional
	Deadlines           GoldenRecoveryDeadlines
}

type GoldenRecoveryResult struct {
	Group                      domain.ArenaGoldenGroup
	RetainedPreStartAttemptIDs []uuid.UUID
	CommittedPriorAttempts     []GoldenRecoveryCommittedAttempt
	PermanentExclusions        []uuid.UUID
	LiveAttempt                *GoldenRecoveryLiveAttempt
}

func RecoverGoldenGroup(input GoldenRecoveryInput) (GoldenRecoveryResult, error) {
	if err := validateGoldenRecoveryGroup(input.Group); err != nil {
		return GoldenRecoveryResult{}, err
	}
	liveIndex, err := goldenRecoveryLiveIndex(input.Attempts)
	if err != nil {
		return GoldenRecoveryResult{}, err
	}

	memberIDs := make(map[uuid.UUID]struct{}, len(input.Group.ParticipantIDs))
	for _, participantID := range input.Group.ParticipantIDs {
		memberIDs[participantID] = struct{}{}
	}
	excluded := make(map[uuid.UUID]struct{})
	attempts := make([]domain.ArenaGoldenAttempt, len(input.Attempts))
	for i := range input.Attempts {
		if err := validateGoldenRecoveryAttempt(input.Attempts[i], input.Group, memberIDs, excluded); err != nil {
			return GoldenRecoveryResult{}, err
		}
		attempts[i] = cloneGoldenRecoveryAttempt(input.Attempts[i].Attempt)
	}

	groupState := domain.ArenaGoldenGroupState{
		ID: input.Group.ID, TournamentID: input.Group.TournamentID,
		RevisionID:                 input.Group.RevisionID,
		SourceProjectionRevisionID: input.Group.SourceProjectionRevisionID,
		PositionFrom:               input.Group.PositionFrom, PositionTo: input.Group.PositionTo,
		ParticipationEstablished: goldenRecoveryParticipationEstablished(input.Attempts),
		Members:                  make([]domain.ArenaGoldenMember, len(input.Group.ParticipantIDs)),
		Attempts:                 attempts,
	}
	for i, participantID := range input.Group.ParticipantIDs {
		_, isExcluded := excluded[participantID]
		groupState.Members[i] = domain.ArenaGoldenMember{ParticipantID: participantID, Excluded: isExcluded}
	}
	group, err := domain.NewArenaGoldenGroup(groupState)
	if err != nil {
		return GoldenRecoveryResult{}, goldenRecoveryError("reconstruct group", err)
	}

	result := GoldenRecoveryResult{
		Group:                      group,
		RetainedPreStartAttemptIDs: goldenRecoveryRetainedAttempts(input.Attempts),
		CommittedPriorAttempts:     goldenRecoveryPriorCommits(input.Attempts, liveIndex),
		PermanentExclusions:        goldenRecoverySortedIDs(excluded),
	}
	if liveIndex >= 0 {
		result.LiveAttempt = buildGoldenRecoveryLiveAttempt(input.Attempts[liveIndex])
	}
	return result, nil
}

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
		if record.Attempt.State == domain.ArenaGoldenAttemptStateActive && membership.ParticipationEstablishedAt == nil {
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
	if record.Attempt.State == domain.ArenaGoldenAttemptStateActive {
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

func goldenRecoveryParticipationEstablished(attempts []GoldenRecoveryAttempt) bool {
	for _, record := range attempts {
		if record.Attempt.StartedAt != nil {
			return true
		}
		for _, membership := range record.Memberships {
			if membership.ParticipationEstablishedAt != nil {
				return true
			}
		}
	}
	return false
}

func goldenRecoveryRetainedAttempts(attempts []GoldenRecoveryAttempt) []uuid.UUID {
	retained := make([]uuid.UUID, 0, len(attempts))
	for _, record := range attempts {
		if record.Attempt.RetainedAt != nil {
			retained = append(retained, record.Attempt.ID)
		}
	}
	return retained
}

func goldenRecoveryPriorCommits(attempts []GoldenRecoveryAttempt, liveIndex int) []GoldenRecoveryCommittedAttempt {
	limit := len(attempts)
	if liveIndex >= 0 {
		limit = liveIndex
	}
	committed := make([]GoldenRecoveryCommittedAttempt, 0, limit)
	for i := 0; i < limit; i++ {
		if len(attempts[i].PositionCommits) == 0 {
			continue
		}
		positions := append([]GoldenRecoveryPositionCommit(nil), attempts[i].PositionCommits...)
		sort.Slice(positions, func(left, right int) bool {
			if positions[left].Position != positions[right].Position {
				return positions[left].Position < positions[right].Position
			}
			return bytes.Compare(positions[left].ID[:], positions[right].ID[:]) < 0
		})
		committed = append(committed, GoldenRecoveryCommittedAttempt{
			AttemptID: attempts[i].Attempt.ID, AttemptNo: attempts[i].Attempt.AttemptNo,
			Positions: positions,
		})
	}
	return committed
}

func buildGoldenRecoveryLiveAttempt(record GoldenRecoveryAttempt) *GoldenRecoveryLiveAttempt {
	live := &GoldenRecoveryLiveAttempt{
		AttemptID: record.Attempt.ID,
		Deadlines: GoldenRecoveryDeadlines{
			Ready: *record.ReadyDeadline, Execution: cloneGoldenRecoveryTime(record.ExecutionDeadline),
		},
	}
	for _, membership := range record.Memberships {
		if membership.Selection == GoldenRecoverySelectionReserve {
			live.Reserves = append(live.Reserves, GoldenRecoveryReserve{
				MembershipID: membership.ID, ParticipantID: membership.ParticipantID,
				Position: membership.ReservePosition, PromotedAt: cloneGoldenRecoveryTime(membership.PromotedAt),
			})
		}
		if membership.ReadyAt != nil && membership.Selection != GoldenRecoverySelectionExcluded {
			live.ReadyParticipantIDs = append(live.ReadyParticipantIDs, membership.ParticipantID)
		}
	}
	for _, submission := range record.Submissions {
		live.ProvisionalOrder = append(live.ProvisionalOrder, GoldenRecoveryProvisional{
			SubmissionID: submission.ID, ParticipantID: submission.ParticipantID,
			ServerSequence: submission.ServerSequence, Position: submission.Position,
		})
	}
	sort.Slice(live.Reserves, func(left, right int) bool {
		if live.Reserves[left].Position != live.Reserves[right].Position {
			return live.Reserves[left].Position < live.Reserves[right].Position
		}
		return bytes.Compare(live.Reserves[left].ParticipantID[:], live.Reserves[right].ParticipantID[:]) < 0
	})
	sort.Slice(live.ReadyParticipantIDs, func(left, right int) bool {
		return bytes.Compare(live.ReadyParticipantIDs[left][:], live.ReadyParticipantIDs[right][:]) < 0
	})
	sort.Slice(live.ProvisionalOrder, func(left, right int) bool {
		if live.ProvisionalOrder[left].ServerSequence != live.ProvisionalOrder[right].ServerSequence {
			return live.ProvisionalOrder[left].ServerSequence < live.ProvisionalOrder[right].ServerSequence
		}
		return bytes.Compare(live.ProvisionalOrder[left].SubmissionID[:], live.ProvisionalOrder[right].SubmissionID[:]) < 0
	})
	return live
}

func goldenRecoverySortedIDs(ids map[uuid.UUID]struct{}) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Slice(result, func(left, right int) bool {
		return bytes.Compare(result[left][:], result[right][:]) < 0
	})
	return result
}

func goldenRecoveryUTC(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

func goldenRecoveryOptionalTime(value *time.Time) bool {
	return value == nil || goldenRecoveryUTC(*value)
}

func cloneGoldenRecoveryTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneGoldenRecoveryAttempt(attempt domain.ArenaGoldenAttempt) domain.ArenaGoldenAttempt {
	clone := attempt
	clone.ParticipantIDs = append([]uuid.UUID(nil), attempt.ParticipantIDs...)
	clone.PreviousAttemptID = cloneGoldenRecoveryUUID(attempt.PreviousAttemptID)
	clone.RetainedAt = cloneGoldenRecoveryTime(attempt.RetainedAt)
	clone.StartedAt = cloneGoldenRecoveryTime(attempt.StartedAt)
	clone.FinishedAt = cloneGoldenRecoveryTime(attempt.FinishedAt)
	return clone
}

func cloneGoldenRecoveryUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func goldenRecoveryError(message string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", ErrInvalidGoldenRecovery, message)
	}
	return fmt.Errorf("%w: %s: %w", ErrInvalidGoldenRecovery, message, cause)
}
