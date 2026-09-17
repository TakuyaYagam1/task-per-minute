package top4

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenplan "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/plan"
	goldenstate "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/state"
)

func buildTop4Snapshot(authority top4Authority) (Top4Snapshot, error) {
	if err := validateTop4PredecessorReceipt(authority); err != nil {
		return Top4Snapshot{}, err
	}
	if err := validateTop4TerminalHeads(authority); err != nil {
		return Top4Snapshot{}, err
	}
	ordered := authority.Source.Standings()
	if len(ordered) < finalSwissTop4Cutoff {
		return Top4Snapshot{}, top4Error("fewer than four final standings")
	}
	qualificationProjections, qualificationDependencies, dependencies, err :=
		buildTop4Qualifications(authority, ordered)
	if err != nil {
		return Top4Snapshot{}, err
	}
	participants := make([]Top4Participant, finalSwissTop4Cutoff)
	for index := range participants {
		participants[index] = Top4Participant{Seed: index + 1, ParticipantID: ordered[index].ParticipantID}
	}
	payload, err := top4Payload(
		authority, participants, qualificationProjections, qualificationDependencies,
	)
	if err != nil || len(payload) == 0 || len(payload) > maxTop4Payload {
		return Top4Snapshot{}, top4Error("encode bounded Top 4 payload")
	}
	var previousID *domain.DerivedRevisionID
	if authority.Previous != nil {
		value := authority.Previous.Projection.Revision().ID()
		previousID = &value
	}
	projection, err := domain.NewProjectionRevision(
		authority.RevisionID, authority.TournamentID,
		domain.ArtifactRef{Kind: domain.ArtifactKindTopFour, EntityID: authority.TournamentID},
		authority.RevisionNo, previousID, authority.CreatedAt, payload,
	)
	if err != nil {
		return Top4Snapshot{}, top4Error("build Top 4 revision: %v", err)
	}
	if authority.Previous != nil {
		dependencies = append(dependencies, domain.RevisionDependency{
			SourceRevisionID:  authority.Previous.Projection.Revision().ID(),
			DerivedRevisionID: authority.RevisionID,
		})
	}
	terminalReferences := make([]Top4TerminalSeriesReference, len(authority.CurrentTerminalSeries))
	for index, head := range authority.CurrentTerminalSeries {
		revision := head.Projection.Revision()
		terminalReferences[index] = Top4TerminalSeriesReference{
			SeriesID:                 head.Series.ID,
			OfficialResultRevisionID: finalSwissOfficialResultID(head),
			ProjectionRevisionID:     revision.ID(), ProjectionPayloadDigest: revision.PayloadDigest(),
		}
	}
	sort.Slice(terminalReferences, func(i, j int) bool {
		return bytes.Compare(terminalReferences[i].SeriesID[:], terminalReferences[j].SeriesID[:]) < 0
	})
	return Top4Snapshot{state: top4SnapshotState{
		Authority: cloneTop4Authority(authority), Projection: projection,
		Participants:              append([]Top4Participant(nil), participants...),
		Dependencies:              append([]domain.RevisionDependency(nil), dependencies...),
		QualificationDependencies: append([]domain.RevisionDependency(nil), qualificationDependencies...),
		QualificationProjections:  cloneTop4Projections(qualificationProjections),
		TerminalReferences:        append([]Top4TerminalSeriesReference(nil), terminalReferences...),
	}}, nil
}

func buildTop4Qualifications(
	authority top4Authority,
	ordered []FinalSwissStanding,
) ([]domain.ProjectionRevision, []domain.RevisionDependency, []domain.RevisionDependency, error) {
	partition, err := goldenplan.PartitionTies(authority.Source.GoldenSource())
	if err != nil {
		return nil, nil, nil, top4Error("repartition final standings: %v", err)
	}
	seeds := partition.Groups()
	groups := authority.Source.GoldenGroups()
	if err := validateTop4GoldenTopologies(groups, seeds); err != nil {
		return nil, nil, nil, err
	}
	if len(groups) == 0 {
		if len(authority.GoldenSettlements) != 0 || !authority.Source.AdvanceDirectly() {
			return nil, nil, nil, top4Error("unexpected or unresolved Golden settlement")
		}
		return nil, nil, []domain.RevisionDependency{{
			SourceRevisionID:  authority.Source.Projection().Revision().ID(),
			DerivedRevisionID: authority.RevisionID,
		}}, nil
	}
	if len(authority.GoldenSettlements) != len(groups) {
		return nil, nil, nil, top4Error("every impactful tie requires one final settlement")
	}
	projections := make([]domain.ProjectionRevision, len(groups))
	lineage := make([]domain.RevisionDependency, len(groups)*2)
	direct := make([]domain.RevisionDependency, len(groups))
	for index, group := range groups {
		settlement := authority.GoldenSettlements[index]
		positions, payload, err := validateTop4Settlement(authority, group, settlement)
		if err != nil {
			return nil, nil, nil, err
		}
		previousID := group.Projection.Revision().ID()
		projection, err := domain.NewProjectionRevision(
			settlement.RevisionID, authority.TournamentID,
			domain.ArtifactRef{Kind: domain.ArtifactKindGoldenGroup, EntityID: group.State.ID},
			settlement.RevisionNo, &previousID, settlement.FinalizedAt, payload,
		)
		if err != nil {
			return nil, nil, nil, top4Error("build finalized Golden revision: %v", err)
		}
		for position, participantID := range positions {
			ordered[position-1].ParticipantID = participantID
		}
		projections[index] = projection
		lineage[index*2] = domain.RevisionDependency{
			SourceRevisionID:  authority.Source.Projection().Revision().ID(),
			DerivedRevisionID: settlement.RevisionID,
		}
		lineage[index*2+1] = domain.RevisionDependency{
			SourceRevisionID: previousID, DerivedRevisionID: settlement.RevisionID,
		}
		direct[index] = domain.RevisionDependency{
			SourceRevisionID: settlement.RevisionID, DerivedRevisionID: authority.RevisionID,
		}
	}
	return projections, lineage, direct, nil
}

func validateTop4GoldenTopologies(
	groups []FinalSwissGoldenGroup,
	seeds []goldenplan.TieGroupSeed,
) error {
	if len(groups) != len(seeds) {
		return top4Error("Golden topology set does not match the canonical point partition")
	}
	for index, group := range groups {
		seed := seeds[index]
		positionFrom, positionTo := group.Revision.Positions()
		if group.Revision.Validate() != nil || group.Revision.GroupID() != group.State.ID ||
			group.Revision.RevisionID() != group.State.RevisionID ||
			group.Revision.SourceProjectionRevisionID() != seed.SourceProjectionRevisionID ||
			group.Revision.SourceProjectionPayloadDigest() != seed.SourceProjectionPayloadDigest ||
			positionFrom != seed.PositionFrom || positionTo != seed.PositionTo ||
			!reflect.DeepEqual(group.Revision.Members(), seed.Members) {
			return top4Error("Golden topology is stale or does not match an exact maximal point tie")
		}
	}
	return nil
}

func validateTop4Settlement(
	authority top4Authority,
	group FinalSwissGoldenGroup,
	settlement Top4GoldenSettlement,
) (map[int]uuid.UUID, []byte, error) {
	seedRevision := group.Projection.Revision()
	if !validTop4SettlementHeader(authority, group, settlement, seedRevision) {
		return nil, nil, top4Error("invalid finalized Golden revision identity or evidence")
	}
	wantScope := goldenstate.GoldenStateScope{
		TournamentID: authority.TournamentID, GroupID: group.State.ID,
		GroupRevisionID: group.State.RevisionID,
	}
	positions, settlementKind, evidenceDigest, err := top4SettlementPositions(
		authority, group, settlement, wantScope,
	)
	if err != nil {
		return nil, nil, err
	}
	resolved, err := exactTop4GoldenPositions(group.State, positions)
	if err != nil {
		return nil, nil, err
	}
	payload, err := finalizedTop4GoldenPayload(group, settlement, positions, settlementKind, evidenceDigest)
	if err != nil {
		return nil, nil, err
	}
	return resolved, payload, nil
}

func validTop4SettlementHeader(
	authority top4Authority,
	group FinalSwissGoldenGroup,
	settlement Top4GoldenSettlement,
	seedRevision domain.DerivedRevision,
) bool {
	return group.Revision.Validate() == nil && group.Revision.RevisionID() == seedRevision.ID() &&
		!settlement.RevisionID.IsZero() && settlement.RevisionID != seedRevision.ID() &&
		settlement.RevisionNo == seedRevision.RevisionNo()+1 && validPlayoffTime(settlement.FinalizedAt) &&
		!settlement.FinalizedAt.Before(seedRevision.CreatedAt()) && !settlement.FinalizedAt.After(authority.CreatedAt) &&
		(settlement.Positions == nil) != (settlement.State == nil)
}

func top4SettlementPositions(
	authority top4Authority,
	group FinalSwissGoldenGroup,
	settlement Top4GoldenSettlement,
	wantScope goldenstate.GoldenStateScope,
) ([]goldenstate.GoldenPositionAllocation, string, [sha256.Size]byte, error) {
	if settlement.Positions != nil {
		return top4LedgerSettlementPositions(group, *settlement.Positions, wantScope)
	}
	return top4AllocationStatePositions(authority, group, settlement, wantScope)
}

func top4LedgerSettlementPositions(
	group FinalSwissGoldenGroup,
	input GoldenPositionEvidence,
	wantScope goldenstate.GoldenStateScope,
) ([]goldenstate.GoldenPositionAllocation, string, [sha256.Size]byte, error) {
	ledger := input.Snapshot()
	if ledger.Validate() != nil || ledger.state.scope != wantScope ||
		ledger.state.positionFrom != group.State.PositionFrom ||
		ledger.state.positionTo != group.State.PositionTo ||
		len(ledger.state.positions) != group.State.PositionTo-group.State.PositionFrom+1 {
		return nil, "", [sha256.Size]byte{}, top4Error(
			"Golden position ledger is partial or belongs to another group",
		)
	}
	positions := make([]goldenstate.GoldenPositionAllocation, len(ledger.state.positions))
	for index, position := range ledger.state.positions {
		positions[index] = goldenstate.GoldenPositionAllocation{
			Position: position.Position, ParticipantID: position.ParticipantID, Kind: goldenstate.GoldenPositionDirect,
		}
	}
	return positions, "positions", ledger.state.payloadDigest, nil
}

func top4AllocationStatePositions(
	authority top4Authority,
	group FinalSwissGoldenGroup,
	settlement Top4GoldenSettlement,
	wantScope goldenstate.GoldenStateScope,
) ([]goldenstate.GoldenPositionAllocation, string, [sha256.Size]byte, error) {
	if settlement.State == nil {
		return nil, "", [sha256.Size]byte{}, top4Error("terminal Golden state is missing")
	}
	state := settlement.State.Snapshot()
	if !validTop4AllocationStateBinding(authority, group, settlement, wantScope, state) {
		return nil, "", [sha256.Size]byte{}, top4Error("terminal Golden allocation state is stale or invalid")
	}
	if len(state.Allocation.Positions) == 0 {
		return nil, "", [sha256.Size]byte{}, top4ZeroParticipationError()
	}
	if len(state.Allocation.Positions) != group.State.PositionTo-group.State.PositionFrom+1 {
		return nil, "", [sha256.Size]byte{}, top4Error("Golden allocation is incomplete")
	}
	return append([]goldenstate.GoldenPositionAllocation(nil), state.Allocation.Positions...),
		"allocation_state", state.PayloadDigest, nil
}

func validTop4AllocationStateBinding(
	authority top4Authority,
	group FinalSwissGoldenGroup,
	settlement Top4GoldenSettlement,
	wantScope goldenstate.GoldenStateScope,
	state goldenstate.GoldenState,
) bool {
	return state.Validate() == nil && state.Scope == wantScope && state.Allocation != nil &&
		reflect.DeepEqual(state.Topology, group.Revision) &&
		state.Group.ID == group.State.ID && state.Group.TournamentID == group.State.TournamentID &&
		state.Group.RevisionID == group.State.RevisionID &&
		state.Group.SourceProjectionRevisionID == group.State.SourceProjectionRevisionID &&
		state.Group.PositionFrom == group.State.PositionFrom && state.Group.PositionTo == group.State.PositionTo &&
		top4GoldenStateMembersMatch(group.State.Members, state.Group.Members) &&
		!state.Allocation.AllocatedAt.After(settlement.FinalizedAt) &&
		!state.Allocation.AllocatedAt.After(authority.CreatedAt)
}

func top4GoldenStateMembersMatch(
	want []domain.GoldenMember,
	actual []domain.GoldenMember,
) bool {
	if len(want) != len(actual) {
		return false
	}
	wantIDs := make([]uuid.UUID, len(want))
	actualIDs := make([]uuid.UUID, len(actual))
	for index := range want {
		wantIDs[index] = want[index].ParticipantID
		actualIDs[index] = actual[index].ParticipantID
	}
	sort.Slice(wantIDs, func(i, j int) bool { return bytes.Compare(wantIDs[i][:], wantIDs[j][:]) < 0 })
	sort.Slice(actualIDs, func(i, j int) bool { return bytes.Compare(actualIDs[i][:], actualIDs[j][:]) < 0 })
	return reflect.DeepEqual(wantIDs, actualIDs)
}

func finalizedTop4GoldenPayload(
	group FinalSwissGoldenGroup,
	settlement Top4GoldenSettlement,
	positions []goldenstate.GoldenPositionAllocation,
	settlementKind string,
	evidenceDigest [sha256.Size]byte,
) ([]byte, error) {
	for index, position := range positions {
		if position.Position != group.State.PositionFrom+index {
			return nil, top4Error("Golden settlement positions are not a complete canonical interval")
		}
	}
	seedDigest := group.Projection.Revision().PayloadDigest()
	positionPayloads := make([]top4PositionPayload, len(positions))
	for index, position := range positions {
		positionPayloads[index] = top4PositionPayload(position)
	}
	payload, err := json.Marshal(struct {
		GroupID        uuid.UUID             `json:"group_id"`
		PositionFrom   int                   `json:"position_from"`
		PositionTo     int                   `json:"position_to"`
		Positions      []top4PositionPayload `json:"positions"`
		Settlement     string                `json:"settlement"`
		EvidenceDigest string                `json:"evidence_digest"`
		SeedDigest     string                `json:"seed_digest"`
		FinalizedAt    time.Time             `json:"finalized_at"`
	}{
		GroupID: group.State.ID, PositionFrom: group.State.PositionFrom, PositionTo: group.State.PositionTo,
		Positions: positionPayloads, Settlement: settlementKind,
		EvidenceDigest: hex.EncodeToString(evidenceDigest[:]), SeedDigest: hex.EncodeToString(seedDigest[:]),
		FinalizedAt: settlement.FinalizedAt,
	})
	if err != nil || len(payload) == 0 || len(payload) > maxTop4Payload {
		return nil, top4Error("encode finalized Golden payload")
	}
	return payload, nil
}

type top4PositionPayload struct {
	Position      int                            `json:"position"`
	ParticipantID uuid.UUID                      `json:"participant_id"`
	Kind          goldenstate.GoldenPositionKind `json:"kind"`
}

func exactTop4GoldenPositions(
	state domain.GoldenGroupState,
	positions []goldenstate.GoldenPositionAllocation,
) (map[int]uuid.UUID, error) {
	members := make(map[uuid.UUID]struct{}, len(state.Members))
	for _, member := range state.Members {
		members[member.ParticipantID] = struct{}{}
	}
	resolved := make(map[int]uuid.UUID, len(positions))
	seenParticipants := make(map[uuid.UUID]struct{}, len(positions))
	for _, position := range positions {
		if position.Position < state.PositionFrom || position.Position > state.PositionTo {
			return nil, top4Error("Golden position is outside its exact interval")
		}
		if _, ok := members[position.ParticipantID]; !ok {
			return nil, top4Error("Golden position participant is not a group member")
		}
		if _, duplicate := resolved[position.Position]; duplicate {
			return nil, top4Error("Golden position is duplicated")
		}
		if _, duplicate := seenParticipants[position.ParticipantID]; duplicate {
			return nil, top4Error("Golden participant is duplicated")
		}
		resolved[position.Position] = position.ParticipantID
		seenParticipants[position.ParticipantID] = struct{}{}
	}
	if len(resolved) != state.PositionTo-state.PositionFrom+1 || len(seenParticipants) != len(members) {
		return nil, top4Error("Golden settlement does not cover the full exact member interval")
	}
	return resolved, nil
}
