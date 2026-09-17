package playoff

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	top4usecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/playoff/top4"
)

const (
	finalSwissTop4Cutoff         = 4
	maxPlayoffReservedIdentities = 65536
)

var (
	ErrInvalidTop4Snapshot           = top4usecase.ErrInvalidTop4Snapshot
	ErrTop4GoldenZeroParticipation   = top4usecase.ErrTop4GoldenZeroParticipation
	ErrInvalidFinalSwissProjection   = top4usecase.ErrInvalidFinalSwissProjection
	ErrInvalidTerminalSeriesEvidence = top4usecase.ErrInvalidTerminalSeriesEvidence
	ErrInvalidGoldenPositionEvidence = top4usecase.ErrInvalidGoldenPositionEvidence
)

type Top4Participant = top4usecase.Top4Participant
type Top4GoldenSettlement = top4usecase.Top4GoldenSettlement
type Top4TerminalSeriesReference = top4usecase.Top4TerminalSeriesReference
type Top4SnapshotCommand = top4usecase.Top4SnapshotCommand
type Top4Snapshot = top4usecase.Top4Snapshot

type SwissBuchholzStatus = top4usecase.SwissBuchholzStatus

const (
	SwissBuchholzProvisional = top4usecase.SwissBuchholzProvisional
	SwissBuchholzFinal       = top4usecase.SwissBuchholzFinal
)

type FinalSwissGoldenGroupIdentity = top4usecase.FinalSwissGoldenGroupIdentity
type FinalSwissProjectionCommand = top4usecase.FinalSwissProjectionCommand
type FinalSwissStanding = top4usecase.FinalSwissStanding
type FinalSwissTieGroup = top4usecase.FinalSwissTieGroup
type FinalSwissGoldenGroup = top4usecase.FinalSwissGoldenGroup
type FinalSwissProjection = top4usecase.FinalSwissProjection

type TerminalSeriesEvidenceInput = top4usecase.TerminalSeriesEvidenceInput
type TerminalSeriesEvidence = top4usecase.TerminalSeriesEvidence
type FinalSwissRound = top4usecase.FinalSwissRound

type GoldenPositionCommitEvidence = top4usecase.GoldenPositionCommitEvidence
type GoldenPositionAttemptEvidence = top4usecase.GoldenPositionAttemptEvidence
type GoldenPositionEvidenceInput = top4usecase.GoldenPositionEvidenceInput
type GoldenPositionEvidence = top4usecase.GoldenPositionEvidence

type ProgressionSwissRound = top4usecase.ProgressionSwissRound
type ProgressionSwissInput = top4usecase.ProgressionSwissInput
type ImpactfulGoldenTieRange = top4usecase.ImpactfulGoldenTieRange

func PlanTop4Snapshot(command Top4SnapshotCommand) (Top4Snapshot, error) {
	return top4usecase.PlanTop4Snapshot(command)
}

func PlanFinalSwissReceipt(input ProgressionSwissInput) (FinalSwissProjection, error) {
	return top4usecase.PlanFinalSwissReceipt(input)
}

func PlanFinalSwissProjection(command FinalSwissProjectionCommand) (FinalSwissProjection, error) {
	return top4usecase.PlanFinalSwissProjection(command)
}

func NewTerminalSeriesEvidence(input TerminalSeriesEvidenceInput) (TerminalSeriesEvidence, error) {
	return top4usecase.NewTerminalSeriesEvidence(input)
}

func NewGoldenPositionEvidence(input GoldenPositionEvidenceInput) (GoldenPositionEvidence, error) {
	return top4usecase.NewGoldenPositionEvidence(input)
}

func DeriveImpactfulGoldenTieRanges(input ProgressionSwissInput) ([]ImpactfulGoldenTieRange, error) {
	return top4usecase.DeriveImpactfulGoldenTieRanges(input)
}

func PlanFinalSwissProgression(input ProgressionSwissInput) (FinalSwissProjection, error) {
	return top4usecase.PlanFinalSwissProgression(input)
}

func validPlayoffTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC && value.Equal(value.Round(0))
}

func reservePlayoffIdentity(reserved map[uuid.UUID]struct{}, id uuid.UUID) bool {
	if id == uuid.Nil {
		return false
	}
	if _, exists := reserved[id]; exists {
		return true
	}
	if len(reserved) >= maxPlayoffReservedIdentities {
		return false
	}
	reserved[id] = struct{}{}
	return true
}

func mergePlayoffReservedIdentities(reserved map[uuid.UUID]struct{}, retained []uuid.UUID) bool {
	if len(reserved) > maxPlayoffReservedIdentities || len(retained) > maxPlayoffReservedIdentities {
		return false
	}
	for _, id := range retained {
		if !reservePlayoffIdentity(reserved, id) {
			return false
		}
	}
	return true
}

func cloneFinalSwissDomainProjection(input domain.ProjectionRevision) domain.ProjectionRevision {
	revision := input.Revision()
	clone, err := domain.NewProjectionRevision(
		revision.ID(), revision.TournamentID(), revision.Artifact(), revision.RevisionNo(),
		revision.PreviousRevisionID(), revision.CreatedAt(), input.Payload(),
	)
	if err != nil {
		return domain.ProjectionRevision{}
	}
	return clone
}

func cloneTerminalSeries(value domain.Series) domain.Series {
	clone := value
	clone.WinnerID = cloneTerminalSeriesUUIDPointer(value.WinnerID)
	clone.CurrentScoreRevisionID = cloneTerminalSeriesScoreRevisionID(value.CurrentScoreRevisionID)
	clone.CurrentResultRevisionID = cloneTerminalSeriesResultRevisionID(value.CurrentResultRevisionID)
	clone.Slots = make([]domain.GameSlot, len(value.Slots))
	for index := range value.Slots {
		clone.Slots[index] = cloneTerminalSeriesGameSlot(value.Slots[index])
	}
	return clone
}

func cloneTerminalSeriesGameSlot(value domain.GameSlot) domain.GameSlot {
	clone := value
	clone.Attempts = make([]domain.Game, len(value.Attempts))
	for index, attempt := range value.Attempts {
		clone.Attempts[index] = attempt
		clone.Attempts[index].WinnerID = cloneTerminalSeriesUUIDPointer(attempt.WinnerID)
		clone.Attempts[index].ResultRevisionID = cloneTerminalSeriesResultRevisionID(attempt.ResultRevisionID)
	}
	return clone
}

func cloneTerminalSeriesUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneTerminalSeriesScoreRevisionID(value *domain.SeriesScoreRevisionID) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneTerminalSeriesResultRevisionID(value *domain.OfficialResultRevisionID) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
