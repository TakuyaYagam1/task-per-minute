package top4

import (
	"bytes"
	"math"
	"reflect"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
)

const (
	goldenFailureAttemptLimit = domain.MaxAssignmentReserveCount + 1
	goldenFailureReceiptLimit = domain.TournamentMaxParticipants * 2
)

func canonicalTop4Authority(command Top4SnapshotCommand) (top4Authority, error) {
	if command.TournamentID == uuid.Nil || command.RevisionID.IsZero() || command.RevisionNo < 1 ||
		command.RevisionNo == math.MaxInt ||
		!validPlayoffTime(command.CreatedAt) || command.Source.Validate() != nil {
		return top4Authority{}, top4Error("invalid snapshot identity, clock, or source")
	}
	if err := preflightTop4Cardinality(command); err != nil {
		return top4Authority{}, err
	}
	sourceRevision := command.Source.Projection().Revision()
	if sourceRevision.TournamentID() != command.TournamentID ||
		sourceRevision.Artifact() != (domain.ArtifactRef{
			Kind: domain.ArtifactKindStandings, EntityID: command.TournamentID,
		}) || command.CreatedAt.Before(sourceRevision.CreatedAt()) {
		return top4Authority{}, top4Error("final standings source is not current for the tournament")
	}
	previous, err := validateTop4Predecessor(command)
	if err != nil {
		return top4Authority{}, err
	}
	authority := top4Authority{
		TournamentID: command.TournamentID, RevisionID: command.RevisionID,
		RevisionNo: command.RevisionNo, Previous: previous, Source: command.Source.Snapshot(),
		CurrentTerminalSeries: terminalSeriesRecords(command.CurrentTerminalSeries),
		GoldenSettlements:     cloneTop4Settlements(command.GoldenSettlements), CreatedAt: command.CreatedAt,
	}
	if err := validateTop4IdentityRoles(authority); err != nil {
		return top4Authority{}, err
	}
	return authority, nil
}

func preflightTop4Cardinality(command Top4SnapshotCommand) error {
	expectedHeads := 0
	for _, round := range command.Source.state.Authority.Rounds {
		expectedHeads += len(round.Series)
	}
	expectedSettlements := len(command.Source.GoldenGroups())
	if len(command.CurrentTerminalSeries) != expectedHeads ||
		len(command.GoldenSettlements) > expectedSettlements ||
		expectedHeads > domain.TournamentMaxParticipants*4 || expectedSettlements > finalSwissTop4Cutoff {
		return top4Error("authority cardinality exceeds frozen source bounds")
	}
	for _, head := range command.CurrentTerminalSeries {
		if err := head.Validate(); err != nil {
			return top4Error("terminal series evidence exceeds tournament bounds")
		}
	}
	for _, settlement := range command.GoldenSettlements {
		if settlement.Positions != nil && !boundedTop4PositionLedger(*settlement.Positions) {
			return top4Error("golden position evidence exceeds tournament bounds")
		}
		if settlement.State != nil && !boundedTop4GoldenState(*settlement.State) {
			return top4Error("golden state evidence exceeds tournament bounds")
		}
	}
	return nil
}

func boundedTop4PositionLedger(ledger GoldenPositionEvidence) bool {
	if ledger.Validate() != nil || len(ledger.state.revisionIDs) > goldenFailureAttemptLimit+1 ||
		len(ledger.state.positions) > domain.TournamentMaxParticipants ||
		len(ledger.state.attempts) > goldenFailureAttemptLimit {
		return false
	}
	for _, attempt := range ledger.state.attempts {
		if attempt.OrderCount > domain.TournamentMaxParticipants {
			return false
		}
	}
	return true
}

func boundedTop4GoldenState(state goldenstate.GoldenState) bool {
	return boundedTop4GoldenStateRoot(state) && boundedTop4GoldenAttempts(state) &&
		boundedTop4GoldenWindows(state) && boundedTop4GoldenNoShows(state) &&
		boundedTop4GoldenExactPlan(state) && boundedTop4GoldenAllocation(state)
}

func boundedTop4GoldenStateRoot(state goldenstate.GoldenState) bool {
	planLimit := domain.TournamentMaxParticipants * (domain.MaxAssignmentReserveCount + 1)
	return len(state.Group.Members) <= domain.TournamentMaxParticipants &&
		len(state.Group.Attempts) <= goldenFailureAttemptLimit &&
		len(state.Windows) <= goldenFailureAttemptLimit && len(state.NoShows) <= goldenFailureAttemptLimit &&
		len(state.ReadyEvents) <= domain.TournamentMaxParticipants*goldenFailureAttemptLimit &&
		len(state.ExactPlan.Groups) <= domain.TournamentMaxParticipants &&
		len(state.ExactPlan.Authority.Groups) <= domain.TournamentMaxParticipants &&
		len(state.ExactPlan.Authority.History) <= goldenFailureReceiptLimit &&
		len(state.ExactPlan.Authority.ParticipantReservations) <= domain.TournamentMaxParticipants &&
		len(state.ExactPlan.Authority.ExistingTaskReservations) <= planLimit &&
		len(state.ExactPlan.Authority.Candidates) <= planLimit &&
		len(state.ExactPlan.Authority.Pool.Versions) <= planLimit
}

func boundedTop4GoldenAttempts(state goldenstate.GoldenState) bool {
	for _, attempt := range state.Group.Attempts {
		if len(attempt.ParticipantIDs) > domain.TournamentMaxParticipants {
			return false
		}
	}
	return true
}

func boundedTop4GoldenWindows(state goldenstate.GoldenState) bool {
	for _, window := range state.Windows {
		if len(window.ReadyParticipantIDs) > domain.TournamentMaxParticipants ||
			len(window.BasePresentParticipantIDs) > domain.TournamentMaxParticipants ||
			len(window.PresentParticipantIDs) > domain.TournamentMaxParticipants {
			return false
		}
	}
	return true
}

func boundedTop4GoldenNoShows(state goldenstate.GoldenState) bool {
	for _, resolution := range state.NoShows {
		if len(resolution.ReadyParticipantIDs) > domain.TournamentMaxParticipants ||
			len(resolution.PresentParticipantIDs) > domain.TournamentMaxParticipants ||
			len(resolution.ExcludedParticipantIDs) > domain.TournamentMaxParticipants {
			return false
		}
	}
	return true
}

func boundedTop4GoldenExactPlan(state goldenstate.GoldenState) bool {
	for _, group := range state.ExactPlan.Groups {
		if len(group.ParticipantIDs) > domain.TournamentMaxParticipants ||
			len(group.Edges) > domain.MaxAssignmentReserveCount+1 {
			return false
		}
	}
	for _, group := range state.ExactPlan.Authority.Groups {
		if len(group.ActiveParticipantIDs) > domain.TournamentMaxParticipants ||
			len(group.Revision.Members()) > domain.TournamentMaxParticipants {
			return false
		}
	}
	for _, candidate := range state.ExactPlan.Authority.Candidates {
		if len(candidate.Task.Hints) > domain.TournamentMaxParticipants {
			return false
		}
	}
	return true
}

func boundedTop4GoldenAllocation(state goldenstate.GoldenState) bool {
	return state.Allocation == nil ||
		(len(state.Allocation.Positions) <= domain.TournamentMaxParticipants &&
			len(state.Allocation.OrderingInputs) <= domain.TournamentMaxParticipants)
}

func validateTop4Predecessor(command Top4SnapshotCommand) (*top4PredecessorReceipt, error) {
	if command.RevisionNo == 1 {
		if command.Previous != nil {
			return nil, top4Error("initial Top 4 revision has a predecessor")
		}
		return nil, nil
	}
	if command.Previous == nil || command.Previous.Validate() != nil {
		return nil, top4Error("Top 4 revision lacks a valid predecessor")
	}
	previous := command.Previous.Projection()
	revision := previous.Revision()
	if revision.TournamentID() != command.TournamentID ||
		revision.Artifact() != (domain.ArtifactRef{
			Kind: domain.ArtifactKindTopFour, EntityID: command.TournamentID,
		}) || revision.RevisionNo()+1 != command.RevisionNo || command.CreatedAt.Before(revision.CreatedAt()) {
		return nil, top4Error("Top 4 predecessor is not the exact current head")
	}
	reserved := make(map[uuid.UUID]struct{})
	if err := addTop4SnapshotReserved(reserved, command.Previous); err != nil {
		return nil, err
	}
	return &top4PredecessorReceipt{
		Projection: cloneFinalSwissDomainProjection(previous), Reserved: canonicalTop4ReservedIDs(reserved),
	}, nil
}

func validateTop4PredecessorReceipt(authority top4Authority) error {
	if authority.Previous == nil {
		if authority.RevisionNo != 1 {
			return top4Error("missing bounded Top 4 predecessor receipt")
		}
		return nil
	}
	receipt := authority.Previous
	if len(receipt.Reserved) == 0 || len(receipt.Reserved) > maxPlayoffReservedIdentities {
		return top4Error("Top 4 predecessor identity receipt exceeds DAG bounds")
	}
	revision := receipt.Projection.Revision()
	if receipt.Projection.Validate() != nil || revision.TournamentID() != authority.TournamentID ||
		revision.Artifact() != (domain.ArtifactRef{
			Kind: domain.ArtifactKindTopFour, EntityID: authority.TournamentID,
		}) || revision.RevisionNo()+1 != authority.RevisionNo || authority.CreatedAt.Before(revision.CreatedAt()) {
		return top4Error("invalid bounded Top 4 predecessor receipt")
	}
	return validateTop4ReservedReceipt(receipt, revision.ID().UUID())
}

func validateTop4ReservedReceipt(receipt *top4PredecessorReceipt, revisionID uuid.UUID) error {
	foundRevision := false
	for index, id := range receipt.Reserved {
		if id == uuid.Nil || (index > 0 && bytes.Compare(receipt.Reserved[index-1][:], id[:]) >= 0) {
			return top4Error("non-canonical Top 4 predecessor identity receipt")
		}
		foundRevision = foundRevision || id == revisionID
	}
	if !foundRevision {
		return top4Error("incomplete Top 4 predecessor identity receipt")
	}
	return nil
}

func validateTop4TerminalHeads(authority top4Authority) error {
	expectedCount := 0
	for _, round := range authority.Source.state.Authority.Rounds {
		expectedCount += len(round.Series)
	}
	expected := make([]terminalSeriesRecord, 0, expectedCount)
	for _, round := range authority.Source.state.Authority.Rounds {
		expected = append(expected, cloneFinalSwissHeads(round.Series)...)
	}
	actual := cloneFinalSwissHeads(authority.CurrentTerminalSeries)
	if len(actual) != len(expected) {
		return top4Error("terminal Series head set is incomplete")
	}
	sortFinalSwissHeads(expected)
	sortFinalSwissHeads(actual)
	for index := range actual {
		if index > 0 && actual[index-1].Result.SeriesID == actual[index].Result.SeriesID {
			return top4Error("terminal Series head is duplicated")
		}
		if !reflect.DeepEqual(actual[index], expected[index]) {
			return top4Error("terminal Series head is stale or spliced")
		}
	}
	return nil
}
