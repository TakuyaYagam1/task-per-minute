package arena

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const maxTop4Payload = 64 << 10

var (
	ErrInvalidTop4Snapshot         = errors.New("invalid Top 4 snapshot")
	ErrTop4GoldenZeroParticipation = errors.New("terminal Golden fallback has zero participation")
)

type Top4Participant struct {
	Seed          int       `json:"seed"`
	ParticipantID uuid.UUID `json:"participant_id"`
}

type Top4GoldenSettlement struct {
	RevisionID  domain.ArenaDerivedRevisionID
	RevisionNo  int
	Positions   *GoldenPositionLedger
	State       *GoldenState
	FinalizedAt time.Time
}

type Top4TerminalSeriesReference struct {
	SeriesID                 uuid.UUID
	OfficialResultRevisionID domain.ArenaOfficialResultRevisionID
	ProjectionRevisionID     domain.ArenaDerivedRevisionID
	ProjectionPayloadDigest  [sha256.Size]byte
}

type Top4SnapshotCommand struct {
	TournamentID          uuid.UUID
	RevisionID            domain.ArenaDerivedRevisionID
	RevisionNo            int
	Previous              *Top4Snapshot
	Source                FinalSwissProjection
	CurrentTerminalSeries []FinalSwissSeriesHead
	GoldenSettlements     []Top4GoldenSettlement
	CreatedAt             time.Time
}

type top4Authority struct {
	TournamentID          uuid.UUID
	RevisionID            domain.ArenaDerivedRevisionID
	RevisionNo            int
	Previous              *top4PredecessorReceipt
	Source                FinalSwissProjection
	CurrentTerminalSeries []FinalSwissSeriesHead
	GoldenSettlements     []Top4GoldenSettlement
	CreatedAt             time.Time
}

type top4PredecessorReceipt struct {
	Projection domain.ArenaProjectionRevision
	Reserved   []uuid.UUID
}

type top4SnapshotState struct {
	Authority                 top4Authority
	Projection                domain.ArenaProjectionRevision
	Participants              []Top4Participant
	Dependencies              []domain.ArenaRevisionDependency
	QualificationDependencies []domain.ArenaRevisionDependency
	QualificationProjections  []domain.ArenaProjectionRevision
	TerminalReferences        []Top4TerminalSeriesReference
}

type Top4Snapshot struct {
	state top4SnapshotState
}

// The persistence adapter must publish this plan with one CAS over the final
// standings, terminal Series audit heads, Golden states, and predecessor Top 4.
func PlanTop4Snapshot(command Top4SnapshotCommand) (Top4Snapshot, error) {
	authority, err := canonicalTop4Authority(command)
	if err != nil {
		return Top4Snapshot{}, err
	}
	snapshot, err := buildTop4Snapshot(authority)
	if err != nil {
		return Top4Snapshot{}, err
	}
	return snapshot.Snapshot(), nil
}

func (s Top4Snapshot) Validate() error {
	if s.state.Authority.TournamentID == uuid.Nil {
		return top4Error("missing snapshot state")
	}
	rebuilt, err := buildTop4Snapshot(cloneTop4Authority(s.state.Authority))
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(s.state, rebuilt.state) {
		return top4Error("snapshot evidence changed")
	}
	return nil
}

func (s Top4Snapshot) Snapshot() Top4Snapshot {
	return Top4Snapshot{state: cloneTop4State(s.state)}
}

func (s Top4Snapshot) Projection() domain.ArenaProjectionRevision {
	return cloneFinalSwissDomainProjection(s.state.Projection)
}

func (s Top4Snapshot) Participants() []Top4Participant {
	return append([]Top4Participant(nil), s.state.Participants...)
}

func (s Top4Snapshot) Dependencies() []domain.ArenaRevisionDependency {
	return append([]domain.ArenaRevisionDependency(nil), s.state.Dependencies...)
}

func (s Top4Snapshot) QualificationDependencies() []domain.ArenaRevisionDependency {
	return append([]domain.ArenaRevisionDependency(nil), s.state.QualificationDependencies...)
}

func (s Top4Snapshot) QualificationProjections() []domain.ArenaProjectionRevision {
	return cloneTop4Projections(s.state.QualificationProjections)
}

func (s Top4Snapshot) TerminalSeriesReferences() []Top4TerminalSeriesReference {
	return append([]Top4TerminalSeriesReference(nil), s.state.TerminalReferences...)
}

func canonicalTop4Authority(command Top4SnapshotCommand) (top4Authority, error) {
	if command.TournamentID == uuid.Nil || command.RevisionID.IsZero() || command.RevisionNo < 1 ||
		command.RevisionNo == math.MaxInt ||
		!validArenaServerTime(command.CreatedAt) || command.Source.Validate() != nil {
		return top4Authority{}, top4Error("invalid snapshot identity, clock, or source")
	}
	if err := preflightTop4Cardinality(command); err != nil {
		return top4Authority{}, err
	}
	sourceRevision := command.Source.Projection().Revision()
	if sourceRevision.TournamentID() != command.TournamentID ||
		sourceRevision.Artifact() != (domain.ArenaArtifactRef{
			Kind: domain.ArenaArtifactKindStandings, EntityID: command.TournamentID,
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
		CurrentTerminalSeries: cloneFinalSwissHeads(command.CurrentTerminalSeries),
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
		expectedHeads > domain.ArenaMaxParticipants*4 || expectedSettlements > finalSwissTop4Cutoff {
		return top4Error("authority cardinality exceeds frozen source bounds")
	}
	for _, head := range command.CurrentTerminalSeries {
		if err := preflightFinalSwissHead(head); err != nil {
			return top4Error("terminal Series evidence exceeds Arena bounds")
		}
	}
	for _, settlement := range command.GoldenSettlements {
		if settlement.Positions != nil && !boundedTop4PositionLedger(*settlement.Positions) {
			return top4Error("Golden position evidence exceeds Arena bounds")
		}
		if settlement.State != nil && !boundedTop4GoldenState(*settlement.State) {
			return top4Error("Golden state evidence exceeds Arena bounds")
		}
	}
	return nil
}

func boundedTop4PositionLedger(ledger GoldenPositionLedger) bool {
	if len(ledger.RevisionIDs) > goldenFailureAttemptLimit+1 ||
		len(ledger.Positions) > domain.ArenaMaxParticipants || len(ledger.Attempts) > goldenFailureAttemptLimit {
		return false
	}
	for _, attempt := range ledger.Attempts {
		if len(attempt.Order) > domain.ArenaMaxParticipants {
			return false
		}
	}
	return true
}

func boundedTop4GoldenState(state GoldenState) bool {
	return boundedTop4GoldenStateRoot(state) && boundedTop4GoldenAttempts(state) &&
		boundedTop4GoldenWindows(state) && boundedTop4GoldenNoShows(state) &&
		boundedTop4GoldenExactPlan(state) && boundedTop4GoldenAllocation(state)
}

func boundedTop4GoldenStateRoot(state GoldenState) bool {
	planLimit := domain.ArenaMaxParticipants * (domain.ArenaAssignmentReserveCount + 1)
	return len(state.Group.Members) <= domain.ArenaMaxParticipants &&
		len(state.Group.Attempts) <= goldenFailureAttemptLimit &&
		len(state.Windows) <= goldenFailureAttemptLimit && len(state.NoShows) <= goldenFailureAttemptLimit &&
		len(state.ReadyEvents) <= domain.ArenaMaxParticipants*goldenFailureAttemptLimit &&
		len(state.ExactPlan.Groups) <= domain.ArenaMaxParticipants &&
		len(state.ExactPlan.Authority.Groups) <= domain.ArenaMaxParticipants &&
		len(state.ExactPlan.Authority.History) <= goldenFailureReceiptLimit &&
		len(state.ExactPlan.Authority.ParticipantReservations) <= domain.ArenaMaxParticipants &&
		len(state.ExactPlan.Authority.ExistingTaskReservations) <= planLimit &&
		len(state.ExactPlan.Authority.Candidates) <= planLimit &&
		len(state.ExactPlan.Authority.Pool.Versions) <= planLimit
}

func boundedTop4GoldenAttempts(state GoldenState) bool {
	for _, attempt := range state.Group.Attempts {
		if len(attempt.ParticipantIDs) > domain.ArenaMaxParticipants {
			return false
		}
	}
	return true
}

func boundedTop4GoldenWindows(state GoldenState) bool {
	for _, window := range state.Windows {
		if len(window.ReadyParticipantIDs) > domain.ArenaMaxParticipants ||
			len(window.BasePresentParticipantIDs) > domain.ArenaMaxParticipants ||
			len(window.PresentParticipantIDs) > domain.ArenaMaxParticipants {
			return false
		}
	}
	return true
}

func boundedTop4GoldenNoShows(state GoldenState) bool {
	for _, resolution := range state.NoShows {
		if len(resolution.ReadyParticipantIDs) > domain.ArenaMaxParticipants ||
			len(resolution.PresentParticipantIDs) > domain.ArenaMaxParticipants ||
			len(resolution.ExcludedParticipantIDs) > domain.ArenaMaxParticipants {
			return false
		}
	}
	return true
}

func boundedTop4GoldenExactPlan(state GoldenState) bool {
	for _, group := range state.ExactPlan.Groups {
		if len(group.ParticipantIDs) > domain.ArenaMaxParticipants ||
			len(group.Edges) > domain.ArenaAssignmentReserveCount+1 {
			return false
		}
	}
	for _, group := range state.ExactPlan.Authority.Groups {
		if len(group.ActiveParticipantIDs) > domain.ArenaMaxParticipants ||
			len(group.Revision.Members()) > domain.ArenaMaxParticipants {
			return false
		}
	}
	for _, candidate := range state.ExactPlan.Authority.Candidates {
		if len(candidate.Task.Hints) > domain.ArenaMaxParticipants {
			return false
		}
	}
	return true
}

func boundedTop4GoldenAllocation(state GoldenState) bool {
	return state.Allocation == nil ||
		(len(state.Allocation.Positions) <= domain.ArenaMaxParticipants &&
			len(state.Allocation.OrderingInputs) <= domain.ArenaMaxParticipants)
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
		revision.Artifact() != (domain.ArenaArtifactRef{
			Kind: domain.ArenaArtifactKindTopFour, EntityID: command.TournamentID,
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
	var previousID *domain.ArenaDerivedRevisionID
	if authority.Previous != nil {
		value := authority.Previous.Projection.Revision().ID()
		previousID = &value
	}
	projection, err := domain.NewArenaProjectionRevision(
		authority.RevisionID, authority.TournamentID,
		domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindTopFour, EntityID: authority.TournamentID},
		authority.RevisionNo, previousID, authority.CreatedAt, payload,
	)
	if err != nil {
		return Top4Snapshot{}, top4Error("build Top 4 revision: %v", err)
	}
	if authority.Previous != nil {
		dependencies = append(dependencies, domain.ArenaRevisionDependency{
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
		Dependencies:              append([]domain.ArenaRevisionDependency(nil), dependencies...),
		QualificationDependencies: append([]domain.ArenaRevisionDependency(nil), qualificationDependencies...),
		QualificationProjections:  cloneTop4Projections(qualificationProjections),
		TerminalReferences:        append([]Top4TerminalSeriesReference(nil), terminalReferences...),
	}}, nil
}

func validateTop4PredecessorReceipt(authority top4Authority) error {
	if authority.Previous == nil {
		if authority.RevisionNo != 1 {
			return top4Error("missing bounded Top 4 predecessor receipt")
		}
		return nil
	}
	receipt := authority.Previous
	if len(receipt.Reserved) == 0 || len(receipt.Reserved) > maxTask051ReservedIDs {
		return top4Error("Top 4 predecessor identity receipt exceeds DAG bounds")
	}
	revision := receipt.Projection.Revision()
	if receipt.Projection.Validate() != nil || revision.TournamentID() != authority.TournamentID ||
		revision.Artifact() != (domain.ArenaArtifactRef{
			Kind: domain.ArenaArtifactKindTopFour, EntityID: authority.TournamentID,
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
	expected := make([]FinalSwissSeriesHead, 0, expectedCount)
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

func buildTop4Qualifications(
	authority top4Authority,
	ordered []FinalSwissStanding,
) ([]domain.ArenaProjectionRevision, []domain.ArenaRevisionDependency, []domain.ArenaRevisionDependency, error) {
	partition, err := PartitionGoldenTies(authority.Source.GoldenSource())
	if err != nil {
		return nil, nil, nil, top4Error("repartition final standings: %v", err)
	}
	seeds := partition.GoldenGroups()
	groups := authority.Source.GoldenGroups()
	if err := validateTop4GoldenTopologies(groups, seeds); err != nil {
		return nil, nil, nil, err
	}
	if len(groups) == 0 {
		if len(authority.GoldenSettlements) != 0 || !authority.Source.AdvanceDirectly() {
			return nil, nil, nil, top4Error("unexpected or unresolved Golden settlement")
		}
		return nil, nil, []domain.ArenaRevisionDependency{{
			SourceRevisionID:  authority.Source.Projection().Revision().ID(),
			DerivedRevisionID: authority.RevisionID,
		}}, nil
	}
	if len(authority.GoldenSettlements) != len(groups) {
		return nil, nil, nil, top4Error("every impactful tie requires one final settlement")
	}
	projections := make([]domain.ArenaProjectionRevision, len(groups))
	lineage := make([]domain.ArenaRevisionDependency, len(groups)*2)
	direct := make([]domain.ArenaRevisionDependency, len(groups))
	for index, group := range groups {
		settlement := authority.GoldenSettlements[index]
		positions, payload, err := validateTop4Settlement(authority, group, settlement)
		if err != nil {
			return nil, nil, nil, err
		}
		previousID := group.Projection.Revision().ID()
		projection, err := domain.NewArenaProjectionRevision(
			settlement.RevisionID, authority.TournamentID,
			domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindGoldenGroup, EntityID: group.State.ID},
			settlement.RevisionNo, &previousID, settlement.FinalizedAt, payload,
		)
		if err != nil {
			return nil, nil, nil, top4Error("build finalized Golden revision: %v", err)
		}
		for position, participantID := range positions {
			ordered[position-1].ParticipantID = participantID
		}
		projections[index] = projection
		lineage[index*2] = domain.ArenaRevisionDependency{
			SourceRevisionID:  authority.Source.Projection().Revision().ID(),
			DerivedRevisionID: settlement.RevisionID,
		}
		lineage[index*2+1] = domain.ArenaRevisionDependency{
			SourceRevisionID: previousID, DerivedRevisionID: settlement.RevisionID,
		}
		direct[index] = domain.ArenaRevisionDependency{
			SourceRevisionID: settlement.RevisionID, DerivedRevisionID: authority.RevisionID,
		}
	}
	return projections, lineage, direct, nil
}

func validateTop4GoldenTopologies(
	groups []FinalSwissGoldenGroup,
	seeds []GoldenTieGroupSeed,
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
	wantScope := GoldenStateScope{
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
	seedRevision domain.ArenaDerivedRevision,
) bool {
	return group.Revision.Validate() == nil && group.Revision.RevisionID() == seedRevision.ID() &&
		!settlement.RevisionID.IsZero() && settlement.RevisionID != seedRevision.ID() &&
		settlement.RevisionNo == seedRevision.RevisionNo()+1 && validArenaServerTime(settlement.FinalizedAt) &&
		!settlement.FinalizedAt.Before(seedRevision.CreatedAt()) && !settlement.FinalizedAt.After(authority.CreatedAt) &&
		(settlement.Positions == nil) != (settlement.State == nil)
}

func top4SettlementPositions(
	authority top4Authority,
	group FinalSwissGoldenGroup,
	settlement Top4GoldenSettlement,
	wantScope GoldenStateScope,
) ([]GoldenPositionAllocation, string, [sha256.Size]byte, error) {
	if settlement.Positions != nil {
		return top4LedgerSettlementPositions(group, *settlement.Positions, wantScope)
	}
	return top4AllocationStatePositions(authority, group, settlement, wantScope)
}

func top4LedgerSettlementPositions(
	group FinalSwissGoldenGroup,
	input GoldenPositionLedger,
	wantScope GoldenStateScope,
) ([]GoldenPositionAllocation, string, [sha256.Size]byte, error) {
	ledger := input.Snapshot()
	if ledger.Validate() != nil || ledger.Scope != wantScope ||
		ledger.PositionFrom != group.State.PositionFrom || ledger.PositionTo != group.State.PositionTo ||
		len(ledger.Positions) != group.State.PositionTo-group.State.PositionFrom+1 {
		return nil, "", [sha256.Size]byte{}, top4Error(
			"Golden position ledger is partial or belongs to another group",
		)
	}
	positions := make([]GoldenPositionAllocation, len(ledger.Positions))
	for index, position := range ledger.Positions {
		positions[index] = GoldenPositionAllocation{
			Position: position.Position, ParticipantID: position.ParticipantID, Kind: GoldenPositionDirect,
		}
	}
	return positions, "positions", ledger.PayloadDigest, nil
}

func top4AllocationStatePositions(
	authority top4Authority,
	group FinalSwissGoldenGroup,
	settlement Top4GoldenSettlement,
	wantScope GoldenStateScope,
) ([]GoldenPositionAllocation, string, [sha256.Size]byte, error) {
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
	return append([]GoldenPositionAllocation(nil), state.Allocation.Positions...),
		"allocation_state", state.PayloadDigest, nil
}

func validTop4AllocationStateBinding(
	authority top4Authority,
	group FinalSwissGoldenGroup,
	settlement Top4GoldenSettlement,
	wantScope GoldenStateScope,
	state GoldenState,
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
	want []domain.ArenaGoldenMember,
	actual []domain.ArenaGoldenMember,
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
	positions []GoldenPositionAllocation,
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
	Position      int                `json:"position"`
	ParticipantID uuid.UUID          `json:"participant_id"`
	Kind          GoldenPositionKind `json:"kind"`
}

func exactTop4GoldenPositions(
	state domain.ArenaGoldenGroupState,
	positions []GoldenPositionAllocation,
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

type top4PayloadDocument struct {
	TournamentID              uuid.UUID                 `json:"tournament_id"`
	RevisionNo                int                       `json:"revision_no"`
	PreviousRevisionID        *uuid.UUID                `json:"previous_revision_id,omitempty"`
	CreatedAt                 time.Time                 `json:"created_at"`
	FinalStandingsRevisionID  uuid.UUID                 `json:"final_standings_revision_id"`
	FinalStandingsDigest      string                    `json:"final_standings_digest"`
	Participants              []Top4Participant         `json:"participants"`
	QualificationRevisionIDs  []uuid.UUID               `json:"qualification_revision_ids"`
	QualificationDigests      []string                  `json:"qualification_digests"`
	QualificationDependencies []top4DependencyPayload   `json:"qualification_dependencies"`
	TerminalSeries            []finalSwissSeriesPayload `json:"terminal_series"`
}

type top4DependencyPayload struct {
	SourceRevisionID  uuid.UUID `json:"source_revision_id"`
	DerivedRevisionID uuid.UUID `json:"derived_revision_id"`
}

func top4Payload(
	authority top4Authority,
	participants []Top4Participant,
	qualifications []domain.ArenaProjectionRevision,
	lineage []domain.ArenaRevisionDependency,
) ([]byte, error) {
	sourceRevision := authority.Source.Projection().Revision()
	sourceDigest := sourceRevision.PayloadDigest()
	document := top4PayloadDocument{
		TournamentID: authority.TournamentID, RevisionNo: authority.RevisionNo, CreatedAt: authority.CreatedAt,
		FinalStandingsRevisionID:  sourceRevision.ID().UUID(),
		FinalStandingsDigest:      hex.EncodeToString(sourceDigest[:]),
		Participants:              append([]Top4Participant(nil), participants...),
		QualificationRevisionIDs:  make([]uuid.UUID, len(qualifications)),
		QualificationDigests:      make([]string, len(qualifications)),
		QualificationDependencies: make([]top4DependencyPayload, len(lineage)),
		TerminalSeries:            make([]finalSwissSeriesPayload, len(authority.CurrentTerminalSeries)),
	}
	for index, dependency := range lineage {
		document.QualificationDependencies[index] = top4DependencyPayload{
			SourceRevisionID:  dependency.SourceRevisionID.UUID(),
			DerivedRevisionID: dependency.DerivedRevisionID.UUID(),
		}
	}
	if authority.Previous != nil {
		value := authority.Previous.Projection.Revision().ID().UUID()
		document.PreviousRevisionID = &value
	}
	for index, qualification := range qualifications {
		revision := qualification.Revision()
		digest := revision.PayloadDigest()
		document.QualificationRevisionIDs[index] = revision.ID().UUID()
		document.QualificationDigests[index] = hex.EncodeToString(digest[:])
	}
	for index, head := range authority.CurrentTerminalSeries {
		payload, err := finalSwissSeriesPayloadFromHead(head)
		if err != nil {
			return nil, err
		}
		document.TerminalSeries[index] = payload
	}
	sort.Slice(document.TerminalSeries, func(i, j int) bool {
		return bytes.Compare(document.TerminalSeries[i].SeriesID[:], document.TerminalSeries[j].SeriesID[:]) < 0
	})
	return json.Marshal(document)
}

func validateTop4IdentityRoles(authority top4Authority) error {
	if err := validateTop4FreshRevisionIDs(authority); err != nil {
		return err
	}
	roles := make(map[uuid.UUID]string)
	claim := func(id uuid.UUID, role string) error {
		if id == uuid.Nil {
			return top4Error("missing %s identity", role)
		}
		if existing, ok := roles[id]; ok {
			return top4Error("identity aliases %s and %s", existing, role)
		}
		roles[id] = role
		return nil
	}
	if err := claim(authority.TournamentID, "tournament"); err != nil {
		return err
	}
	if err := claim(authority.RevisionID.UUID(), "Top 4 revision"); err != nil {
		return err
	}
	if err := claim(authority.Source.Projection().Revision().ID().UUID(), "standings revision"); err != nil {
		return err
	}
	if err := claim(authority.Source.state.Authority.ProjectionID, "standings projection"); err != nil {
		return err
	}
	for _, participant := range authority.Source.Standings() {
		if err := claim(participant.ParticipantID, "participant"); err != nil {
			return err
		}
	}
	if err := claimTop4SwissSourceIdentities(claim, authority.Source); err != nil {
		return err
	}
	if err := claimTop4GoldenSourceIdentities(claim, authority); err != nil {
		return err
	}
	if authority.Previous != nil {
		if err := claim(authority.Previous.Projection.Revision().ID().UUID(), "Top 4 revision"); err != nil {
			return err
		}
	}
	return nil
}

func validateTop4FreshRevisionIDs(authority top4Authority) error {
	reserved := make(map[uuid.UUID]struct{})
	for _, settlement := range authority.GoldenSettlements {
		if settlement.State != nil {
			if !addTop4GoldenStateReserved(reserved, *settlement.State) {
				return top4Error("Golden identity authority exceeds DAG bounds")
			}
		}
	}
	if err := addTop4FinalSourceReserved(reserved, authority.Source); err != nil {
		return err
	}
	if authority.Previous != nil {
		if !mergeTask051ReservedIDs(reserved, authority.Previous.Reserved) {
			return top4Error("Top 4 predecessor identity receipt exceeds DAG bounds")
		}
	}
	return validateTop4FreshCandidates(reserved, authority)
}

func addTop4FinalSourceReserved(
	reserved map[uuid.UUID]struct{},
	source FinalSwissProjection,
) error {
	if source.state.Authority.Previous != nil &&
		len(source.state.Authority.Previous.Reserved) > maxTask051ReservedIDs {
		return top4Error("retained standings identity lineage exceeds DAG bounds")
	}
	identities, err := finalSwissAuthorityIdentitySet(source.state.Authority)
	if err != nil {
		return top4Error("invalid retained standings identity lineage")
	}
	for id := range identities {
		if !reserveTask051ID(reserved, id) {
			return top4Error("retained standings identity lineage exceeds DAG bounds")
		}
	}
	if source.state.Authority.Previous != nil {
		for _, identity := range source.state.Authority.Previous.Reserved {
			if !reserveTask051ID(reserved, identity.ID) {
				return top4Error("retained standings identity lineage exceeds DAG bounds")
			}
		}
	}
	return nil
}

func addTop4GoldenStateReserved(reserved map[uuid.UUID]struct{}, state GoldenState) bool {
	roles := goldenCoreIdentityRoles(state)
	roles = append(roles, goldenPlanIdentityRoles(state)...)
	roles = append(roles, goldenWindowIdentityRoles(state)...)
	roles = append(roles, goldenTransitionIdentityRoles(state)...)
	for _, role := range roles {
		if role.value != uuid.Nil {
			if !reserveTask051ID(reserved, role.value) {
				return false
			}
		}
	}
	return true
}

func addTop4SnapshotReserved(
	reserved map[uuid.UUID]struct{},
	previous *Top4Snapshot,
) error {
	if previous == nil {
		return nil
	}
	previousAuthority := previous.state.Authority
	if previousAuthority.Previous != nil &&
		!mergeTask051ReservedIDs(reserved, previousAuthority.Previous.Reserved) {
		return top4Error("retained Top 4 identity lineage exceeds DAG bounds")
	}
	if !reserveTask051ID(reserved, previous.Projection().Revision().ID().UUID()) {
		return top4Error("retained Top 4 identity lineage exceeds DAG bounds")
	}
	for _, projection := range previous.QualificationProjections() {
		if !reserveTask051ID(reserved, projection.Revision().ID().UUID()) {
			return top4Error("retained Top 4 identity lineage exceeds DAG bounds")
		}
	}
	for _, settlement := range previousAuthority.GoldenSettlements {
		if !reserveTask051ID(reserved, settlement.RevisionID.UUID()) ||
			(settlement.State != nil && !addTop4GoldenStateReserved(reserved, *settlement.State)) {
			return top4Error("retained Top 4 identity lineage exceeds DAG bounds")
		}
	}
	return addTop4FinalSourceReserved(reserved, previousAuthority.Source)
}

func validateTop4FreshCandidates(reserved map[uuid.UUID]struct{}, authority top4Authority) error {
	candidates := make([]uuid.UUID, 0, len(authority.GoldenSettlements)+1)
	candidates = append(candidates, authority.RevisionID.UUID())
	for _, settlement := range authority.GoldenSettlements {
		candidates = append(candidates, settlement.RevisionID.UUID())
	}
	seen := make(map[uuid.UUID]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, exists := reserved[candidate]; exists {
			return top4Error("new revision aliases retained Golden or predecessor authority")
		}
		if _, exists := seen[candidate]; exists {
			return top4Error("new revisions alias each other")
		}
		seen[candidate] = struct{}{}
	}
	return nil
}

func claimTop4SwissSourceIdentities(
	claim func(uuid.UUID, string) error,
	source FinalSwissProjection,
) error {
	for _, round := range source.state.Authority.Rounds {
		if err := claimSemifinalSwissRoundIdentities(claim, round); err != nil {
			return err
		}
	}
	return nil
}

func claimTop4GoldenSourceIdentities(
	claim func(uuid.UUID, string) error,
	authority top4Authority,
) error {
	groups := authority.Source.GoldenGroups()
	for index, settlement := range authority.GoldenSettlements {
		if err := claim(settlement.RevisionID.UUID(), "finalized Golden revision"); err != nil {
			return err
		}
		if index < len(groups) {
			if err := claim(groups[index].State.ID, "Golden group"); err != nil {
				return err
			}
			if err := claim(groups[index].State.RevisionID.UUID(), "Golden seed revision"); err != nil {
				return err
			}
		}
		if err := claimTop4SettlementIdentities(claim, settlement); err != nil {
			return err
		}
	}
	return nil
}

func claimTop4SettlementIdentities(
	claim func(uuid.UUID, string) error,
	settlement Top4GoldenSettlement,
) error {
	if settlement.Positions != nil {
		ledger := settlement.Positions
		for _, revisionID := range ledger.RevisionIDs {
			if err := claim(revisionID, "Golden position ledger revision"); err != nil {
				return err
			}
		}
		for _, attempt := range ledger.Attempts {
			for _, identity := range []struct {
				id   uuid.UUID
				role string
			}{
				{id: attempt.AttemptID, role: "Golden attempt"},
				{id: attempt.SubmissionHead.RevisionID, role: "Golden submission revision"},
				{id: attempt.SubmissionHead.Scope.WaveID, role: "Golden wave"},
				{id: attempt.SubmissionHead.Scope.AssignmentID, role: "Golden assignment"},
				{id: attempt.SubmissionHead.Scope.SnapshotID, role: "Golden snapshot"},
				{id: attempt.SubmissionHead.Scope.TaskID, role: "Golden task"},
			} {
				if err := claim(identity.id, identity.role); err != nil {
					return err
				}
			}
		}
		commitIDs := make(map[uuid.UUID]struct{})
		for _, position := range ledger.Positions {
			if _, seen := commitIDs[position.CommitID]; seen {
				continue
			}
			commitIDs[position.CommitID] = struct{}{}
			if err := claim(position.CommitID, "Golden position commit"); err != nil {
				return err
			}
		}
	}
	return nil
}

func cloneTop4Authority(input top4Authority) top4Authority {
	clone := input
	if input.Previous != nil {
		clone.Previous = cloneTop4PredecessorReceipt(input.Previous)
	}
	clone.Source = input.Source.Snapshot()
	clone.CurrentTerminalSeries = cloneFinalSwissHeads(input.CurrentTerminalSeries)
	clone.GoldenSettlements = cloneTop4Settlements(input.GoldenSettlements)
	return clone
}

func canonicalTop4ReservedIDs(input map[uuid.UUID]struct{}) []uuid.UUID {
	if len(input) > maxTask051ReservedIDs {
		return nil
	}
	reserved := make([]uuid.UUID, 0, len(input))
	for id := range input {
		reserved = append(reserved, id)
	}
	sort.Slice(reserved, func(i, j int) bool {
		return bytes.Compare(reserved[i][:], reserved[j][:]) < 0
	})
	return reserved
}

func cloneTop4PredecessorReceipt(input *top4PredecessorReceipt) *top4PredecessorReceipt {
	if input == nil {
		return nil
	}
	if len(input.Reserved) > maxTask051ReservedIDs {
		return &top4PredecessorReceipt{Projection: cloneFinalSwissDomainProjection(input.Projection)}
	}
	return &top4PredecessorReceipt{
		Projection: cloneFinalSwissDomainProjection(input.Projection),
		Reserved:   append([]uuid.UUID(nil), input.Reserved...),
	}
}

func reserveTask051ID(reserved map[uuid.UUID]struct{}, id uuid.UUID) bool {
	if id == uuid.Nil {
		return false
	}
	if _, exists := reserved[id]; exists {
		return true
	}
	if len(reserved) >= maxTask051ReservedIDs {
		return false
	}
	reserved[id] = struct{}{}
	return true
}

func mergeTask051ReservedIDs(reserved map[uuid.UUID]struct{}, retained []uuid.UUID) bool {
	if len(reserved) > maxTask051ReservedIDs || len(retained) > maxTask051ReservedIDs {
		return false
	}
	for _, id := range retained {
		if !reserveTask051ID(reserved, id) {
			return false
		}
	}
	return true
}

func cloneTop4State(input top4SnapshotState) top4SnapshotState {
	clone := input
	clone.Authority = cloneTop4Authority(input.Authority)
	clone.Projection = cloneFinalSwissDomainProjection(input.Projection)
	clone.Participants = append([]Top4Participant(nil), input.Participants...)
	clone.Dependencies = append([]domain.ArenaRevisionDependency(nil), input.Dependencies...)
	clone.QualificationDependencies = append([]domain.ArenaRevisionDependency(nil), input.QualificationDependencies...)
	clone.QualificationProjections = cloneTop4Projections(input.QualificationProjections)
	clone.TerminalReferences = append([]Top4TerminalSeriesReference(nil), input.TerminalReferences...)
	return clone
}

func cloneTop4Settlements(input []Top4GoldenSettlement) []Top4GoldenSettlement {
	clone := append([]Top4GoldenSettlement(nil), input...)
	for index := range clone {
		if input[index].Positions != nil {
			value := input[index].Positions.Snapshot()
			clone[index].Positions = &value
		}
		if input[index].State != nil {
			value := input[index].State.Snapshot()
			clone[index].State = &value
		}
	}
	return clone
}

func cloneFinalSwissHeads(input []FinalSwissSeriesHead) []FinalSwissSeriesHead {
	clone := make([]FinalSwissSeriesHead, len(input))
	for index, head := range input {
		clone[index] = cloneFinalSwissSeriesHead(head)
	}
	return clone
}

func cloneTop4Projections(input []domain.ArenaProjectionRevision) []domain.ArenaProjectionRevision {
	clone := make([]domain.ArenaProjectionRevision, len(input))
	for index, projection := range input {
		clone[index] = cloneFinalSwissDomainProjection(projection)
	}
	return clone
}

func sortFinalSwissHeads(heads []FinalSwissSeriesHead) {
	sort.Slice(heads, func(i, j int) bool {
		return bytes.Compare(heads[i].Result.SeriesID[:], heads[j].Result.SeriesID[:]) < 0
	})
}

func top4Error(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidTop4Snapshot, fmt.Sprintf(format, arguments...))
}

func top4ZeroParticipationError() error {
	return fmt.Errorf("%w: %w", ErrInvalidTop4Snapshot, ErrTop4GoldenZeroParticipation)
}
