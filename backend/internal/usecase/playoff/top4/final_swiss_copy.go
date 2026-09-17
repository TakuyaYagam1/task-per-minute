package top4

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func cloneFinalSwissAuthority(input finalSwissAuthority) finalSwissAuthority {
	clone := input
	if input.Previous != nil {
		clone.Previous = cloneFinalSwissPredecessorReceipt(input.Previous)
	}
	clone.ParticipantIDs = append([]uuid.UUID(nil), input.ParticipantIDs...)
	clone.Seeds = append([]swissusecase.ParticipantSeed(nil), input.Seeds...)
	clone.Rounds = cloneFinalSwissRounds(input.Rounds)
	clone.GoldenGroups = append([]FinalSwissGoldenGroupIdentity(nil), input.GoldenGroups...)
	return clone
}

func cloneFinalSwissPredecessorReceipt(
	input *finalSwissPredecessorReceipt,
) *finalSwissPredecessorReceipt {
	if input == nil {
		return nil
	}
	if len(input.Reserved) > maxPlayoffReservedIdentities {
		return &finalSwissPredecessorReceipt{
			Projection: cloneFinalSwissDomainProjection(input.Projection), ProjectionID: input.ProjectionID,
			PhysicalProjectionRevision: input.PhysicalProjectionRevision,
		}
	}
	return &finalSwissPredecessorReceipt{
		Projection: cloneFinalSwissDomainProjection(input.Projection), ProjectionID: input.ProjectionID,
		PhysicalProjectionRevision: input.PhysicalProjectionRevision,
		Reserved:                   append([]finalSwissReservedIdentity(nil), input.Reserved...),
	}
}

func cloneFinalSwissProjectionState(input finalSwissProjectionState) finalSwissProjectionState {
	clone := input
	clone.Authority = cloneFinalSwissAuthority(input.Authority)
	clone.Projection = cloneFinalSwissDomainProjection(input.Projection)
	clone.GoldenSource = input.GoldenSource.Snapshot()
	clone.Standings = cloneFinalSwissStandings(input.Standings)
	clone.TieGroups = cloneFinalSwissTieGroups(input.TieGroups)
	clone.GoldenGroups = cloneFinalSwissGoldenGroups(input.GoldenGroups)
	clone.SeriesDependencies = append([]domain.RevisionDependency(nil), input.SeriesDependencies...)
	clone.Dependencies = append([]domain.RevisionDependency(nil), input.Dependencies...)
	return clone
}

func cloneFinalSwissRounds(input []finalSwissRound) []finalSwissRound {
	clone := make([]finalSwissRound, len(input))
	for index, round := range input {
		clone[index] = round
		clone[index].LockProof = swissusecase.CloneRoundLockProof(round.LockProof)
		clone[index].Series = make([]terminalSeriesRecord, len(round.Series))
		for seriesIndex, head := range round.Series {
			clone[index].Series[seriesIndex] = cloneFinalSwissSeriesHead(head)
		}
		if round.Bye != nil {
			bye := *round.Bye
			clone[index].Bye = &bye
		}
	}
	return clone
}

func cloneFinalSwissSeriesHead(input terminalSeriesRecord) terminalSeriesRecord {
	clone := input
	clone.Series = cloneTerminalSeries(input.Series)
	official, err := input.OfficialResult.Clone()
	if err == nil {
		clone.OfficialResult = official
	}
	clone.Projection = cloneFinalSwissDomainProjection(input.Projection)
	clone.Result = swissusecase.CloneSeriesPointResult(input.Result)
	return clone
}

func cloneFinalSwissStandings(input []FinalSwissStanding) []FinalSwissStanding {
	clone := append([]FinalSwissStanding(nil), input...)
	for index := range clone {
		clone[index].AcceptedSolveTime = cloneFinalSwissDurationPointer(input[index].AcceptedSolveTime)
	}
	return clone
}

func cloneFinalSwissDurationPointer(value *time.Duration) *time.Duration {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneFinalSwissTieGroups(input []FinalSwissTieGroup) []FinalSwissTieGroup {
	clone := append([]FinalSwissTieGroup(nil), input...)
	for index := range clone {
		clone[index].ParticipantIDs = append([]uuid.UUID(nil), input[index].ParticipantIDs...)
	}
	return clone
}

func cloneFinalSwissGoldenGroups(input []FinalSwissGoldenGroup) []FinalSwissGoldenGroup {
	clone := make([]FinalSwissGoldenGroup, len(input))
	for index, group := range input {
		clone[index] = group
		clone[index].State.Members = append([]domain.GoldenMember(nil), group.State.Members...)
		clone[index].State.Attempts = append([]domain.GoldenAttempt(nil), group.State.Attempts...)
		clone[index].Revision = group.Revision.Snapshot()
		clone[index].Projection = cloneFinalSwissDomainProjection(group.Projection)
	}
	return clone
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
