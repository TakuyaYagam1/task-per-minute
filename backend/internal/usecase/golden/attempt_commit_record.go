package golden

import (
	"crypto/sha256"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type GoldenAttemptCommitRecord struct {
	ID                  uuid.UUID
	CommandID           uuid.UUID
	CommandDigest       [sha256.Size]byte
	Scope               GoldenSubmissionScope
	ActiveExecution     GoldenWaveExecutionExpectation
	Assignment          GoldenAttemptAssignmentEvidence
	ExpectedSubmissions GoldenSubmissionLedgerExpectation
	ExpectedPositions   GoldenPositionLedgerExpectation
	SwissPoints         GoldenSwissPointLedgerSentinel
	Reason              GoldenAttemptTerminalReason
	FinishedAt          time.Time
	Attempt             domain.GoldenAttempt
	Group               domain.GoldenGroupState
	Wave                domain.Wave
	Ordering            GoldenAttemptOrderingEvidence
	PriorPositions      GoldenPositionLedger
	Positions           GoldenPositionLedger
	PayloadDigest       [sha256.Size]byte
}

func (r GoldenAttemptCommitRecord) Snapshot() GoldenAttemptCommitRecord {
	clone := r
	clone.ActiveExecution = CloneExecutionExpectation(r.ActiveExecution)
	clone.Assignment = r.Assignment.Snapshot()
	clone.Attempt = CloneAttempt(r.Attempt)
	clone.Group = CloneGroup(r.Group)
	clone.Wave = CloneExecution(r.Wave)
	clone.Ordering = r.Ordering.Snapshot()
	clone.PriorPositions = r.PriorPositions.Snapshot()
	clone.Positions = r.Positions.Snapshot()
	return clone
}

func (r GoldenAttemptCommitRecord) Validate() error {
	if !validGoldenAttemptCommitHeader(r) || !validGoldenAttemptCommitTerminal(r) {
		return goldenAttemptCommitError("invalid terminal record identity or evidence")
	}
	if !goldenAttemptCommitRetainsTerminalAttempt(r) {
		return goldenAttemptCommitError("completed group does not retain terminal attempt")
	}
	if err := r.Ordering.Validate(); err != nil {
		return err
	}
	if err := r.Positions.Validate(); err != nil {
		return err
	}
	if !goldenAttemptCommitPriorPositionsMatch(r) {
		return goldenAttemptCommitError("prior position commitment changed")
	}
	if err := validateGoldenAttemptPositionAuthority(r); err != nil {
		return err
	}
	if !goldenAttemptCommitOrderingMatches(r) {
		return goldenAttemptCommitError("terminal ordering is not bound to the attempt")
	}
	if err := validateGoldenAttemptPositionLinks(r); err != nil {
		return err
	}
	payload, err := goldenAttemptCommitPayload(r)
	if err != nil || r.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != r.PayloadDigest {
		return goldenAttemptCommitError("terminal record digest changed")
	}
	return nil
}

func goldenAttemptCommitRetainsTerminalAttempt(r GoldenAttemptCommitRecord) bool {
	if _, err := domain.NewGoldenGroup(r.Group); err != nil || len(r.Group.Attempts) == 0 {
		return false
	}
	return reflect.DeepEqual(r.Group.Attempts[len(r.Group.Attempts)-1], r.Attempt)
}

func goldenAttemptCommitPriorPositionsMatch(r GoldenAttemptCommitRecord) bool {
	return r.PriorPositions.Validate() == nil &&
		r.PriorPositions.Expectation().Equal(r.ExpectedPositions)
}

func validateGoldenAttemptPositionAuthority(record GoldenAttemptCommitRecord) error {
	if !PositionLedgerMatchesGroup(record.PriorPositions, record.Group) ||
		!PositionLedgerMatchesGroup(record.Positions, record.Group) {
		return goldenAttemptCommitError("position ledger is not bound to the terminal group")
	}
	members := make(map[uuid.UUID]struct{}, len(record.Group.Members))
	for _, member := range record.Group.Members {
		members[member.ParticipantID] = struct{}{}
	}
	if !PositionParticipantsBelongToGroup(record.PriorPositions, members) ||
		!PositionParticipantsBelongToGroup(record.Positions, members) {
		return goldenAttemptCommitError("position evidence contains a foreign group participant")
	}
	return nil
}

func PositionLedgerMatchesGroup(
	ledger GoldenPositionLedger,
	group domain.GoldenGroupState,
) bool {
	return ledger.Scope.TournamentID == group.TournamentID && ledger.Scope.GroupID == group.ID &&
		ledger.Scope.GroupRevisionID == group.RevisionID && ledger.PositionFrom == group.PositionFrom &&
		ledger.PositionTo == group.PositionTo && len(ledger.Positions) <= len(group.Members)
}

func PositionParticipantsBelongToGroup(
	ledger GoldenPositionLedger,
	members map[uuid.UUID]struct{},
) bool {
	for _, position := range ledger.Positions {
		if _, found := members[position.ParticipantID]; !found {
			return false
		}
	}
	for _, attempt := range ledger.Attempts {
		for _, item := range attempt.Order {
			if _, found := members[item.ParticipantID]; !found {
				return false
			}
		}
	}
	return true
}

func validGoldenAttemptCommitHeader(r GoldenAttemptCommitRecord) bool {
	return validGoldenAttemptCommitIdentity(r) && goldenAttemptCommitExecutionMatchesScope(r) &&
		goldenAttemptCommitAssignmentMatchesScope(r) && goldenAttemptCommitAssignmentMatchesExecution(r) &&
		goldenAttemptCommitHeadsMatchScope(r) && domain.IsValidServerTime(r.FinishedAt)
}

func validGoldenAttemptCommitIdentity(r GoldenAttemptCommitRecord) bool {
	return r.ID != uuid.Nil && r.CommandID != uuid.Nil && r.ID != r.CommandID && r.Scope.IsValid() &&
		r.CommandDigest != [sha256.Size]byte{}
}

func goldenAttemptCommitExecutionMatchesScope(r GoldenAttemptCommitRecord) bool {
	return r.ActiveExecution.Scope == r.Scope.State && r.ActiveExecution.Source.Scope == r.Scope.State &&
		r.ActiveExecution.AttemptID == r.Scope.AttemptID && r.ActiveExecution.WaveID == r.Scope.WaveID &&
		r.ActiveExecution.AssignmentID == r.Scope.AssignmentID && r.ActiveExecution.Started
}

func goldenAttemptCommitAssignmentMatchesScope(r GoldenAttemptCommitRecord) bool {
	return r.Assignment.Validate() == nil && r.Assignment.ID == r.Scope.AssignmentID &&
		r.Assignment.Scope == r.Scope.State && r.Assignment.AttemptID == r.Scope.AttemptID &&
		r.Assignment.WaveID == r.Scope.WaveID && r.Assignment.SnapshotID == r.Scope.SnapshotID &&
		r.Assignment.TaskID == r.Scope.TaskID
}

func goldenAttemptCommitAssignmentMatchesExecution(r GoldenAttemptCommitRecord) bool {
	return r.Assignment.RevisionID == r.ActiveExecution.AssignmentRevisionID &&
		r.Assignment.Revision == r.ActiveExecution.AssignmentRevision &&
		r.Assignment.MembershipID == r.ActiveExecution.MembershipID &&
		r.Assignment.ExecutionPayloadDigest == r.ActiveExecution.AssignmentDigest &&
		r.Assignment.Plan == r.ActiveExecution.Source.Plan &&
		EqualIDs(PrivateAssignmentParticipantIDs(r.Assignment.Private), r.Attempt.ParticipantIDs)
}

func goldenAttemptCommitHeadsMatchScope(r GoldenAttemptCommitRecord) bool {
	return r.ExpectedSubmissions.Scope == r.Scope && r.ExpectedPositions.Scope == r.Scope.State &&
		r.SwissPoints.Validate() == nil
}

func validGoldenAttemptCommitTerminal(r GoldenAttemptCommitRecord) bool {
	validAttempt := r.Attempt.Validate() == nil && r.Attempt.State == domain.GoldenAttemptStateCompleted &&
		r.Attempt.ID == r.Scope.AttemptID
	validWave := r.Wave.Validate() == nil && r.Wave.State == domain.WaveStateCompleted &&
		r.Wave.ID == r.Scope.WaveID
	validGroup := r.Group.ID == r.Scope.State.GroupID && r.Group.RevisionID == r.Scope.State.GroupRevisionID
	return validAttempt && validWave && validGroup
}

func goldenAttemptCommitOrderingMatches(r GoldenAttemptCommitRecord) bool {
	return r.Ordering.AttemptID == r.Attempt.ID && r.Ordering.AttemptNo == r.Attempt.AttemptNo &&
		r.Ordering.SubmissionHead.Equal(r.ExpectedSubmissions) &&
		r.Positions.Expectation().ScopeIs(r.Scope.State)
}

func (e GoldenPositionLedgerExpectation) ScopeIs(scope GoldenStateScope) bool {
	return e.Scope == scope
}
