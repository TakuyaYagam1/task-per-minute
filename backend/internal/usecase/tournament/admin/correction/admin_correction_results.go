package correction

import (
	"context"
	"crypto/sha256"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const maxCorrectionFields = 3

type ProjectionRevisionExpectation struct {
	ID                 uuid.UUID
	TournamentID       uuid.UUID
	ArtifactKind       string
	ArtifactID         uuid.UUID
	RevisionNo         int
	PreviousRevisionID *uuid.UUID
	PayloadDigest      [sha256.Size]byte
	CreatedAt          time.Time
}

type CorrectionProjectionIntent struct {
	ExpectedRevision ProjectionRevisionExpectation
	NextRevisionID   uuid.UUID
	DecisionID       uuid.UUID
	PayloadDigest    [sha256.Size]byte
}

type CorrectionUnlockIntent struct {
	ReservationID     uuid.UUID
	TournamentID      uuid.UUID
	OwnerID           uuid.UUID
	SourceRevisionID  uuid.UUID
	ExpectedRevision  int64
	ExpectedUsed      bool
	ExpectedDisclosed bool
	EvidenceDigest    [sha256.Size]byte
	BindingDigest     [sha256.Size]byte
}

type CorrectionPatch struct {
	State          domain.GameState
	Reason         domain.GameResultReason
	WinnerID       *uuid.UUID
	SolvedAt       *time.Time
	SubmissionID   *uuid.UUID
	EvidenceDigest [sha256.Size]byte
}

type CorrectionCommand struct {
	CommandScope

	SeriesID                   uuid.UUID
	GameID                     uuid.UUID
	SourceResultRevision       uuid.UUID
	ExpectedProjectionRevision int64
	Confirmed                  bool
	Reason                     string
	Explanation                string
	Fields                     []string
	Patch                      CorrectionPatch
	ProjectionIntents          []CorrectionProjectionIntent
	UnlockIntents              []CorrectionUnlockIntent
}

// CorrectionRejectionCode is the stable operator-facing reason returned for
// a correction preflight or commit conflict. It distinguishes a stale result
// compare-and-set from a stale derived projection compare-and-set.
type CorrectionRejectionCode string

const (
	CorrectionRejectionStaleProjection             CorrectionRejectionCode = "stale_projection"
	CorrectionRejectionStaleResult                 CorrectionRejectionCode = "stale_result"
	CorrectionRejectionIncompleteProjection        CorrectionRejectionCode = "incomplete_projection"
	CorrectionRejectionIncompleteUnlock            CorrectionRejectionCode = "incomplete_unlock"
	CorrectionRejectionTournamentTerminal          CorrectionRejectionCode = "tournament_terminal"
	CorrectionRejectionCutoff                      CorrectionRejectionCode = "cutoff"
	CorrectionRejectionCutoffWaveStarted           CorrectionRejectionCode = "cutoff_wave_started"
	CorrectionRejectionCutoffTaskDelivered         CorrectionRejectionCode = "cutoff_task_delivered"
	CorrectionRejectionCutoffNoShowRecorded        CorrectionRejectionCode = "cutoff_no_show_recorded"
	CorrectionRejectionCutoffForfeitRecorded       CorrectionRejectionCode = "cutoff_forfeit_recorded"
	CorrectionRejectionCutoffGoldenDirectAllocated CorrectionRejectionCode = "cutoff_golden_direct_allocated"
)

// CorrectionConflictError carries a safe, stable reason while preserving the
// ordinary conflict identity for existing callers and transaction handling.
type CorrectionConflictError struct {
	ExpectedRevision int64
	CurrentRevision  int64
	CurrentState     domain.TournamentState
	Code             CorrectionRejectionCode
}

func (e *CorrectionConflictError) Error() string {
	if e == nil {
		return ""
	}
	return string(e.Code) + ": correction conflict"
}

func (e *CorrectionConflictError) Unwrap() error { return domain.ErrConflict }

// As keeps compatibility with the older generic revision conflict contract
// while allowing new transports to inspect the stable correction code.
func (e *CorrectionConflictError) As(target any) bool {
	value, ok := target.(**RevisionConflictError)
	if !ok {
		return false
	}
	*value = &RevisionConflictError{
		ExpectedRevision: e.ExpectedRevision,
		CurrentRevision:  e.CurrentRevision,
		CurrentState:     e.CurrentState,
	}
	return true
}

type ProjectionSupersessionView struct {
	ArtifactKind          string
	ArtifactID            uuid.UUID
	PreviousRevisionID    uuid.UUID
	SuccessorRevisionID   uuid.UUID
	PreviousDecisionID    *uuid.UUID
	ReplacementDecisionID uuid.UUID
}

type CorrectionEvidence struct {
	CommandID        uuid.UUID
	TournamentID     uuid.UUID
	SeriesID         uuid.UUID
	GameID           uuid.UUID
	OperatorID       uuid.UUID
	Reason           string
	Fields           []string
	RequestedAt      time.Time
	ValidationDigest [sha256.Size]byte
	Supersessions    []ProjectionSupersessionView
	UnlockIntents    []CorrectionUnlockIntent
}

type CorrectionPort interface {
	CorrectGameResult(ctx context.Context, command CorrectionCommand) (CorrectionEvidence, error)
}

func validCorrectionCommand(command CorrectionCommand) bool {
	return validCorrectionCommandHeader(command) && validCorrectionIntents(command)
}

func validCorrectionCommandWithSource(command CorrectionCommand) bool {
	return validCorrectionCommand(command) && command.SourceResultRevision != uuid.Nil
}

func validCorrectionDraftCommand(command CorrectionCommand) bool {
	return validCorrectionCommandHeader(command) && command.SourceResultRevision != uuid.Nil &&
		len(command.ProjectionIntents) == 0 && len(command.UnlockIntents) == 0
}

func validCorrectionCommandHeader(command CorrectionCommand) bool {
	return validCommandScope(command.CommandScope) && command.SeriesID != uuid.Nil && command.GameID != uuid.Nil &&
		command.ExpectedProjectionRevision >= 1 && command.Confirmed && validCorrectionReason(command.Reason) &&
		validText(command.Explanation, maxReasonRunes) && validCorrectionFields(command.Fields) &&
		validCorrectionPatch(command.Patch) && len(command.ProjectionIntents) <= 4096 &&
		len(command.UnlockIntents) <= 4096
}

func validCorrectionIntents(command CorrectionCommand) bool {
	for _, intent := range command.ProjectionIntents {
		if !validProjectionIntent(intent, command.TournamentID) {
			return false
		}
	}
	for _, intent := range command.UnlockIntents {
		if !validUnlockIntent(intent, command.TournamentID) {
			return false
		}
	}
	return true
}

func validCorrectionFields(fields []string) bool {
	if len(fields) == 0 || len(fields) > maxCorrectionFields {
		return false
	}
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if field != "winner" && field != "result_reason" && field != "solve_metadata" {
			return false
		}
		if _, duplicate := seen[field]; duplicate {
			return false
		}
		seen[field] = struct{}{}
	}
	return true
}

func validCorrectionPatch(patch CorrectionPatch) bool {
	if !validCorrectionOutcome(patch) || !validCorrectionWinner(patch) {
		return false
	}
	if patch.Reason == domain.GameResultReasonSolved {
		return validSolvedMetadata(patch)
	}
	return patch.SolvedAt == nil && patch.SubmissionID == nil && patch.EvidenceDigest == ([32]byte{})
}

func validCorrectionOutcome(patch CorrectionPatch) bool {
	return patch.State.IsTerminal() && patch.State != domain.GameStateSuperseded && patch.Reason.IsLegalFor(patch.State)
}

func validCorrectionWinner(patch CorrectionPatch) bool {
	if patch.State != domain.GameStateCompleted {
		return patch.WinnerID == nil
	}
	return patch.WinnerID != nil && *patch.WinnerID != uuid.Nil
}

func validSolvedMetadata(patch CorrectionPatch) bool {
	return patch.SolvedAt != nil && domain.IsValidServerTime(*patch.SolvedAt) &&
		patch.SubmissionID != nil && *patch.SubmissionID != uuid.Nil && patch.EvidenceDigest != ([32]byte{})
}

func validCorrectionReason(reason string) bool {
	switch reason {
	case "scorekeeping_error", "verified_submission", "operator_ruling":
		return true
	default:
		return false
	}
}

func validProjectionIntent(intent CorrectionProjectionIntent, tournamentID uuid.UUID) bool {
	revision := intent.ExpectedRevision
	return revision.ID != uuid.Nil && revision.TournamentID == tournamentID &&
		revision.ArtifactID != uuid.Nil && domain.ArtifactKind(revision.ArtifactKind).IsValid() &&
		revision.RevisionNo >= 1 && revision.PayloadDigest != ([32]byte{}) &&
		domain.IsValidServerTime(revision.CreatedAt) &&
		(revision.PreviousRevisionID == nil || *revision.PreviousRevisionID != uuid.Nil) &&
		intent.NextRevisionID != uuid.Nil && intent.NextRevisionID != revision.ID &&
		intent.DecisionID != uuid.Nil && intent.PayloadDigest != ([32]byte{})
}

func validUnlockIntent(intent CorrectionUnlockIntent, tournamentID uuid.UUID) bool {
	return intent.ReservationID != uuid.Nil && intent.TournamentID == tournamentID && intent.OwnerID != uuid.Nil &&
		intent.SourceRevisionID != uuid.Nil && intent.ExpectedRevision >= 1 && !intent.ExpectedUsed &&
		!intent.ExpectedDisclosed && intent.EvidenceDigest != ([32]byte{}) && intent.BindingDigest != ([32]byte{})
}

func validCorrectionEvidence(evidence CorrectionEvidence, command CorrectionCommand) bool {
	if !validCorrectionEvidenceHeader(evidence, command) || !validSupersessions(evidence.Supersessions) {
		return false
	}
	return validEvidenceUnlocks(evidence.UnlockIntents, command.TournamentID)
}

func validCorrectionEvidenceHeader(evidence CorrectionEvidence, command CorrectionCommand) bool {
	return evidence.CommandID == command.CommandID && evidence.TournamentID == command.TournamentID &&
		evidence.SeriesID == command.SeriesID && evidence.GameID == command.GameID &&
		evidence.OperatorID == command.Operator.ActorID && evidence.Reason == command.Reason &&
		slices.Equal(evidence.Fields, command.Fields) && domain.IsValidServerTime(evidence.RequestedAt) &&
		evidence.ValidationDigest != ([32]byte{}) && evidence.Supersessions != nil && evidence.UnlockIntents != nil
}

func validSupersessions(items []ProjectionSupersessionView) bool {
	for _, item := range items {
		if !validSupersession(item) {
			return false
		}
	}
	return true
}

func validSupersession(item ProjectionSupersessionView) bool {
	return item.ArtifactID != uuid.Nil && domain.ArtifactKind(item.ArtifactKind).IsValid() &&
		item.PreviousRevisionID != uuid.Nil && item.SuccessorRevisionID != uuid.Nil &&
		item.ReplacementDecisionID != uuid.Nil &&
		(item.PreviousDecisionID == nil || *item.PreviousDecisionID != uuid.Nil)
}

func validEvidenceUnlocks(intents []CorrectionUnlockIntent, tournamentID uuid.UUID) bool {
	for _, intent := range intents {
		if !validUnlockIntent(intent, tournamentID) {
			return false
		}
	}
	return true
}
