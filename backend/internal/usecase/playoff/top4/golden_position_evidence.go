package top4

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
)

var ErrInvalidGoldenPositionEvidence = errors.New("invalid Golden position evidence")

type GoldenPositionCommitEvidence struct {
	Position           int
	ParticipantID      uuid.UUID
	AttemptID          uuid.UUID
	AttemptNo          int
	SubmissionID       uint64
	TerminalEvidenceID uuid.UUID
	EvidenceDigest     [sha256.Size]byte
	CommitID           uuid.UUID
}

type GoldenPositionAttemptEvidence struct {
	AttemptID            uuid.UUID
	AttemptNo            int
	SubmissionRevisionID uuid.UUID
	SubmissionRevision   int64
	WaveID               uuid.UUID
	AssignmentID         uuid.UUID
	SnapshotID           uuid.UUID
	TaskID               uuid.UUID
	OrderCount           int
}

type GoldenPositionEvidenceInput struct {
	Scope              goldenstate.GoldenStateScope
	RevisionID         uuid.UUID
	Revision           int64
	PreviousRevisionID *uuid.UUID
	RevisionIDs        []uuid.UUID
	PositionFrom       int
	PositionTo         int
	Positions          []GoldenPositionCommitEvidence
	Attempts           []GoldenPositionAttemptEvidence
	PayloadDigest      [sha256.Size]byte
}

type goldenPositionEvidenceState struct {
	scope              goldenstate.GoldenStateScope
	revisionID         uuid.UUID
	revision           int64
	previousRevisionID *uuid.UUID
	revisionIDs        []uuid.UUID
	positionFrom       int
	positionTo         int
	positions          []GoldenPositionCommitEvidence
	attempts           []GoldenPositionAttemptEvidence
	payloadDigest      [sha256.Size]byte
}

type GoldenPositionEvidence struct {
	state goldenPositionEvidenceState
}

func NewGoldenPositionEvidence(input GoldenPositionEvidenceInput) (GoldenPositionEvidence, error) {
	evidence := GoldenPositionEvidence{state: goldenPositionEvidenceState{
		scope: input.Scope, revisionID: input.RevisionID, revision: input.Revision,
		previousRevisionID: cloneGoldenPositionRevisionID(input.PreviousRevisionID),
		revisionIDs:        append([]uuid.UUID(nil), input.RevisionIDs...),
		positionFrom:       input.PositionFrom, positionTo: input.PositionTo,
		positions:     append([]GoldenPositionCommitEvidence(nil), input.Positions...),
		attempts:      append([]GoldenPositionAttemptEvidence(nil), input.Attempts...),
		payloadDigest: input.PayloadDigest,
	}}
	if err := evidence.Validate(); err != nil {
		return GoldenPositionEvidence{}, err
	}
	return evidence.Snapshot(), nil
}

func (e GoldenPositionEvidence) Validate() error {
	state := e.state
	if !goldenstate.ValidStateScope(state.scope) || state.revisionID == uuid.Nil ||
		state.revision < 1 || state.revision > int64(math.MaxInt) ||
		state.positionFrom < 1 || state.positionTo < state.positionFrom ||
		state.positionTo == math.MaxInt || state.payloadDigest == [sha256.Size]byte{} {
		return invalidGoldenPositionEvidence("invalid evidence header")
	}
	if err := validateGoldenPositionRevisionLineage(state); err != nil {
		return err
	}
	if err := validateGoldenPositionAttempts(state); err != nil {
		return err
	}
	return validateGoldenPositionCommitments(state)
}

func (e GoldenPositionEvidence) Snapshot() GoldenPositionEvidence {
	state := e.state
	state.previousRevisionID = cloneGoldenPositionRevisionID(e.state.previousRevisionID)
	state.revisionIDs = append([]uuid.UUID(nil), e.state.revisionIDs...)
	state.positions = append([]GoldenPositionCommitEvidence(nil), e.state.positions...)
	state.attempts = append([]GoldenPositionAttemptEvidence(nil), e.state.attempts...)
	return GoldenPositionEvidence{state: state}
}

func cloneGoldenPositionRevisionID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func (e GoldenPositionEvidence) Scope() goldenstate.GoldenStateScope {
	return e.state.scope
}

func (e GoldenPositionEvidence) RevisionIDs() []uuid.UUID {
	return append([]uuid.UUID(nil), e.state.revisionIDs...)
}

func (e GoldenPositionEvidence) PositionInterval() (int, int) {
	return e.state.positionFrom, e.state.positionTo
}

func (e GoldenPositionEvidence) Positions() []GoldenPositionCommitEvidence {
	return append([]GoldenPositionCommitEvidence(nil), e.state.positions...)
}

func (e GoldenPositionEvidence) Attempts() []GoldenPositionAttemptEvidence {
	return append([]GoldenPositionAttemptEvidence(nil), e.state.attempts...)
}

func (e GoldenPositionEvidence) PayloadDigest() [sha256.Size]byte {
	return e.state.payloadDigest
}

func validateGoldenPositionRevisionLineage(state goldenPositionEvidenceState) error {
	if int64(len(state.revisionIDs)) != state.revision ||
		len(state.attempts) != len(state.revisionIDs)-1 ||
		len(state.revisionIDs) == 0 || state.revisionIDs[len(state.revisionIDs)-1] != state.revisionID {
		return invalidGoldenPositionEvidence("invalid revision lineage")
	}
	seen := make(map[uuid.UUID]struct{}, len(state.revisionIDs))
	for _, revisionID := range state.revisionIDs {
		if revisionID == uuid.Nil {
			return invalidGoldenPositionEvidence("missing revision identity")
		}
		if _, duplicate := seen[revisionID]; duplicate {
			return invalidGoldenPositionEvidence("duplicate revision identity")
		}
		seen[revisionID] = struct{}{}
	}
	if state.revision == 1 {
		if state.previousRevisionID != nil {
			return invalidGoldenPositionEvidence("initial revision has a predecessor")
		}
		return nil
	}
	if state.previousRevisionID == nil ||
		*state.previousRevisionID != state.revisionIDs[len(state.revisionIDs)-2] {
		return invalidGoldenPositionEvidence("revision predecessor changed")
	}
	return nil
}

func validateGoldenPositionAttempts(state goldenPositionEvidenceState) error {
	seen := make(map[uuid.UUID]struct{}, len(state.attempts))
	previousAttemptNo := 0
	for _, attempt := range state.attempts {
		if attempt.AttemptID == uuid.Nil || attempt.AttemptNo <= previousAttemptNo ||
			attempt.SubmissionRevisionID == uuid.Nil || attempt.SubmissionRevision < 1 ||
			attempt.WaveID == uuid.Nil || attempt.AssignmentID == uuid.Nil ||
			attempt.SnapshotID == uuid.Nil || attempt.TaskID == uuid.Nil ||
			attempt.OrderCount < 0 || attempt.OrderCount > domain.TournamentMaxParticipants {
			return invalidGoldenPositionEvidence("invalid attempt evidence")
		}
		if _, duplicate := seen[attempt.AttemptID]; duplicate {
			return invalidGoldenPositionEvidence("duplicate attempt identity")
		}
		seen[attempt.AttemptID] = struct{}{}
		previousAttemptNo = attempt.AttemptNo
	}
	return nil
}

func validateGoldenPositionCommitments(state goldenPositionEvidenceState) error {
	if len(state.positions) > state.positionTo-state.positionFrom+1 {
		return invalidGoldenPositionEvidence("position evidence exceeds its interval")
	}
	participants := make(map[uuid.UUID]struct{}, len(state.positions))
	positionIndex := 0
	terminalSeen := false
	for _, attempt := range state.attempts {
		for range attempt.OrderCount {
			if positionIndex >= len(state.positions) {
				return invalidGoldenPositionEvidence("attempt evidence exceeds committed positions")
			}
			position := state.positions[positionIndex]
			if !validGoldenCommittedPosition(position, attempt, state.positionFrom+positionIndex) {
				return invalidGoldenPositionEvidence("invalid committed position")
			}
			if position.TerminalEvidenceID != uuid.Nil {
				if terminalSeen {
					return invalidGoldenPositionEvidence("multiple terminal positions")
				}
				terminalSeen = true
			}
			if _, duplicate := participants[position.ParticipantID]; duplicate {
				return invalidGoldenPositionEvidence("duplicate committed participant")
			}
			participants[position.ParticipantID] = struct{}{}
			positionIndex++
		}
	}
	if positionIndex != len(state.positions) {
		return invalidGoldenPositionEvidence("committed position lacks attempt evidence")
	}
	return nil
}

func validGoldenCommittedPosition(position GoldenPositionCommitEvidence, attempt GoldenPositionAttemptEvidence, expectedPosition int) bool {
	return position.Position == expectedPosition && position.ParticipantID != uuid.Nil &&
		position.AttemptID == attempt.AttemptID && position.AttemptNo == attempt.AttemptNo &&
		(position.SubmissionID != 0) != (position.TerminalEvidenceID != uuid.Nil) &&
		position.EvidenceDigest != [sha256.Size]byte{} && position.CommitID != uuid.Nil
}

func invalidGoldenPositionEvidence(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenPositionEvidence, message)
}
