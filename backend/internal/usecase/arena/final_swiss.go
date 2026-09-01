package arena

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
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

const (
	finalSwissTop4Cutoff   = 4
	maxFinalSwissPayload   = 64 << 10
	finalSwissRevisionRole = "projection revision"
	maxTask051ReservedIDs  = maxRevisionDAGProjections
)

var ErrInvalidFinalSwissProjection = errors.New("invalid final Swiss projection")

type SwissBuchholzStatus string

const (
	SwissBuchholzProvisional SwissBuchholzStatus = "provisional"
	SwissBuchholzFinal       SwissBuchholzStatus = "final"
)

type FinalSwissSeriesHead struct {
	Series         domain.ArenaSeries
	OfficialResult OfficialResultProjectionInput
	Projection     domain.ArenaProjectionRevision
	Result         SwissSeriesPointResult
}

type FinalSwissRound struct {
	RoundID     uuid.UUID
	RoundNumber int
	RevisionID  uuid.UUID
	LockProof   SwissRoundLockProof
	Series      []FinalSwissSeriesHead
	Bye         *SwissByePointResult
}

type FinalSwissGoldenGroupIdentity struct {
	PositionFrom int                           `json:"position_from"`
	PositionTo   int                           `json:"position_to"`
	GroupID      uuid.UUID                     `json:"group_id"`
	RevisionID   domain.ArenaDerivedRevisionID `json:"revision_id"`
}

type FinalSwissProjectionCommand struct {
	TournamentID   uuid.UUID
	Preset         domain.ArenaPreset
	ProjectionID   uuid.UUID
	RevisionID     domain.ArenaDerivedRevisionID
	RevisionNo     int
	Previous       *FinalSwissProjection
	ParticipantIDs []uuid.UUID
	Seeds          []SwissParticipantSeed
	Rounds         []FinalSwissRound
	GoldenGroups   []FinalSwissGoldenGroupIdentity
	CreatedAt      time.Time
}

type FinalSwissStanding struct {
	ParticipantID     uuid.UUID           `json:"participant_id"`
	Position          int                 `json:"position"`
	Points            int                 `json:"points"`
	PointsLabel       SwissPointsLabel    `json:"points_label"`
	Buchholz          int                 `json:"buchholz"`
	BuchholzStatus    SwissBuchholzStatus `json:"buchholz_status"`
	HeadToHeadPoints  int                 `json:"head_to_head_points"`
	HeadToHeadApplied bool                `json:"head_to_head_applied"`
	EffectiveTime     time.Duration       `json:"effective_time"`
	AcceptedSolveTime *time.Duration      `json:"accepted_solve_time,omitempty"`
	StableSeed        int                 `json:"stable_seed"`
}

type FinalSwissTieGroup struct {
	PositionFrom   int         `json:"position_from"`
	PositionTo     int         `json:"position_to"`
	Impactful      bool        `json:"impactful"`
	ParticipantIDs []uuid.UUID `json:"participant_ids"`
}

type FinalSwissGoldenGroup struct {
	State      domain.ArenaGoldenGroupState
	Revision   GoldenGroupRevision
	Projection domain.ArenaProjectionRevision
	Dependency domain.ArenaRevisionDependency
}

type finalSwissAuthority struct {
	TournamentID   uuid.UUID
	Preset         domain.ArenaPreset
	ProjectionID   uuid.UUID
	RevisionID     domain.ArenaDerivedRevisionID
	RevisionNo     int
	Previous       *finalSwissPredecessorReceipt
	ParticipantIDs []uuid.UUID
	Seeds          []SwissParticipantSeed
	Rounds         []FinalSwissRound
	GoldenGroups   []FinalSwissGoldenGroupIdentity
	CreatedAt      time.Time
}

type finalSwissReservedIdentity struct {
	ID   uuid.UUID
	Role string
}

type finalSwissPredecessorReceipt struct {
	Projection   domain.ArenaProjectionRevision
	ProjectionID uuid.UUID
	Reserved     []finalSwissReservedIdentity
}

type finalSwissProjectionState struct {
	Authority          finalSwissAuthority
	Projection         domain.ArenaProjectionRevision
	GoldenSource       GoldenStandingsProjection
	Standings          []FinalSwissStanding
	TieGroups          []FinalSwissTieGroup
	GoldenGroups       []FinalSwissGoldenGroup
	SeriesDependencies []domain.ArenaRevisionDependency
	Dependencies       []domain.ArenaRevisionDependency
	AdvanceDirectly    bool
}

type FinalSwissProjection struct {
	state finalSwissProjectionState
}

// The persistence adapter must publish this plan with one CAS over the current
// round, bye, terminal Series result, and predecessor standings heads.
func PlanFinalSwissProjection(command FinalSwissProjectionCommand) (FinalSwissProjection, error) {
	authority, err := canonicalFinalSwissAuthority(command)
	if err != nil {
		return FinalSwissProjection{}, err
	}
	projection, err := buildFinalSwissProjection(authority)
	if err != nil {
		return FinalSwissProjection{}, err
	}
	return projection.Snapshot(), nil
}

func (p FinalSwissProjection) Validate() error {
	if p.state.Authority.TournamentID == uuid.Nil {
		return finalSwissError("missing projection state")
	}
	rebuilt, err := buildFinalSwissProjection(cloneFinalSwissAuthority(p.state.Authority))
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(p.state, rebuilt.state) {
		return finalSwissError("projection evidence changed")
	}
	return nil
}

func (p FinalSwissProjection) Snapshot() FinalSwissProjection {
	return FinalSwissProjection{state: cloneFinalSwissProjectionState(p.state)}
}

func (p FinalSwissProjection) Projection() domain.ArenaProjectionRevision {
	return cloneFinalSwissDomainProjection(p.state.Projection)
}

func (p FinalSwissProjection) GoldenSource() GoldenStandingsProjection {
	return p.state.GoldenSource.Snapshot()
}

func (p FinalSwissProjection) Standings() []FinalSwissStanding {
	return cloneFinalSwissStandings(p.state.Standings)
}

func (p FinalSwissProjection) TieGroups() []FinalSwissTieGroup {
	return cloneFinalSwissTieGroups(p.state.TieGroups)
}

func (p FinalSwissProjection) GoldenGroups() []FinalSwissGoldenGroup {
	return cloneFinalSwissGoldenGroups(p.state.GoldenGroups)
}

func (p FinalSwissProjection) TerminalSeriesDependencies() []domain.ArenaRevisionDependency {
	return append([]domain.ArenaRevisionDependency(nil), p.state.SeriesDependencies...)
}

func (p FinalSwissProjection) Dependencies() []domain.ArenaRevisionDependency {
	return append([]domain.ArenaRevisionDependency(nil), p.state.Dependencies...)
}

func (p FinalSwissProjection) AdvanceDirectly() bool {
	return p.state.AdvanceDirectly
}

func canonicalFinalSwissAuthority(command FinalSwissProjectionCommand) (finalSwissAuthority, error) {
	if err := validateFinalSwissCommandHeader(command); err != nil {
		return finalSwissAuthority{}, err
	}
	if err := preflightFinalSwissCardinality(command); err != nil {
		return finalSwissAuthority{}, err
	}
	participants, err := canonicalSwissLedgerParticipants(command.ParticipantIDs)
	if err != nil || !command.Preset.ValidRosterSize(len(participants)) {
		return finalSwissAuthority{}, finalSwissError("invalid roster: %v", err)
	}
	seeds, err := canonicalFinalSwissSeeds(participants, command.Seeds)
	if err != nil {
		return finalSwissAuthority{}, err
	}
	roundCount, err := command.Preset.SwissRounds(len(participants))
	if err != nil || len(command.Rounds) != roundCount {
		return finalSwissAuthority{}, finalSwissError("Swiss round coverage is incomplete")
	}
	rounds := canonicalFinalSwissRounds(command.Rounds)
	goldenGroups := canonicalFinalSwissGoldenIdentities(command.GoldenGroups)
	previous, err := validateFinalSwissPredecessor(command)
	if err != nil {
		return finalSwissAuthority{}, err
	}
	authority := finalSwissAuthority{
		TournamentID: command.TournamentID, Preset: command.Preset, ProjectionID: command.ProjectionID,
		RevisionID: command.RevisionID, RevisionNo: command.RevisionNo,
		Previous: previous, ParticipantIDs: participants, Seeds: seeds, Rounds: rounds,
		GoldenGroups: goldenGroups, CreatedAt: command.CreatedAt,
	}
	if err := validateFinalSwissIdentityRoles(authority); err != nil {
		return finalSwissAuthority{}, err
	}
	return authority, nil
}

func preflightFinalSwissCardinality(command FinalSwissProjectionCommand) error {
	participantCount := len(command.ParticipantIDs)
	if participantCount < domain.ArenaMinParticipants || participantCount > domain.ArenaMaxParticipants ||
		len(command.Seeds) != participantCount || len(command.GoldenGroups) > finalSwissTop4Cutoff {
		return finalSwissError("authority cardinality exceeds Arena bounds")
	}
	roundCount, err := command.Preset.SwissRounds(participantCount)
	if err != nil || len(command.Rounds) != roundCount {
		return finalSwissError("Swiss round coverage is incomplete")
	}
	for _, round := range command.Rounds {
		if err := preflightFinalSwissRound(round, participantCount); err != nil {
			return err
		}
	}
	return nil
}

func preflightFinalSwissRound(round FinalSwissRound, participantCount int) error {
	if len(round.Series) != participantCount/2 ||
		(participantCount%2 == 0 && round.Bye != nil) ||
		(participantCount%2 == 1 && round.Bye == nil) {
		return finalSwissError("round membership cardinality is invalid")
	}
	if len(round.LockProof.RosterParticipantIDs) > domain.ArenaMaxParticipants ||
		len(round.LockProof.Series) > domain.ArenaMaxParticipants/2 {
		return finalSwissError("round lock evidence exceeds Arena bounds")
	}
	for _, head := range round.Series {
		if err := preflightFinalSwissHead(head); err != nil {
			return err
		}
	}
	return nil
}

func preflightFinalSwissHead(head FinalSwissSeriesHead) error {
	if len(head.Series.Slots) > head.Series.Format.WinsRequired()*2-1 {
		return finalSwissError("Series topology exceeds its format")
	}
	attemptCount := 0
	for _, slot := range head.Series.Slots {
		if len(slot.Attempts) > domain.ArenaMaxParticipants-attemptCount {
			return finalSwissError("Series attempt topology exceeds Arena bounds")
		}
		attemptCount += len(slot.Attempts)
	}
	if head.OfficialResult.NoGame != nil {
		if !boundedFinalSwissNoGame(*head.OfficialResult.NoGame) {
			return finalSwissError("no-game evidence exceeds Arena bounds")
		}
		return nil
	}
	if head.OfficialResult.Score != nil &&
		(len(head.OfficialResult.Score.Attempts) > domain.ArenaMaxParticipants ||
			(head.OfficialResult.Score.CommandAttempt != nil &&
				head.OfficialResult.Score.CommandAttempt.AttemptNo > domain.ArenaMaxParticipants)) {
		return finalSwissError("score evidence exceeds Arena bounds")
	}
	return nil
}

func boundedFinalSwissNoGame(recorded RecordedNoGameResult) bool {
	limit := domain.ArenaMaxParticipants
	return len(recorded.GameResults) <= limit && len(recorded.Topology) <= limit &&
		len(recorded.GameSourceRevisions) <= limit && len(recorded.GameProjections) <= limit &&
		len(recorded.GameDependencies) <= limit && len(recorded.Score.GameResultRevisionIDs) <= limit
}

func validateFinalSwissCommandHeader(command FinalSwissProjectionCommand) error {
	if command.TournamentID == uuid.Nil || !command.Preset.IsValid() || command.ProjectionID == uuid.Nil ||
		command.RevisionID.IsZero() || command.RevisionNo < 1 || command.RevisionNo == math.MaxInt ||
		!validArenaServerTime(command.CreatedAt) {
		return finalSwissError("invalid projection identity or clock")
	}
	return nil
}

func canonicalFinalSwissRounds(input []FinalSwissRound) []FinalSwissRound {
	rounds := cloneFinalSwissRounds(input)
	sort.Slice(rounds, func(i, j int) bool { return rounds[i].RoundNumber < rounds[j].RoundNumber })
	for index := range rounds {
		sort.Slice(rounds[index].Series, func(i, j int) bool {
			return bytes.Compare(
				rounds[index].Series[i].Result.SeriesID[:], rounds[index].Series[j].Result.SeriesID[:],
			) < 0
		})
	}
	return rounds
}

func canonicalFinalSwissGoldenIdentities(
	input []FinalSwissGoldenGroupIdentity,
) []FinalSwissGoldenGroupIdentity {
	groups := append([]FinalSwissGoldenGroupIdentity(nil), input...)
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].PositionFrom != groups[j].PositionFrom {
			return groups[i].PositionFrom < groups[j].PositionFrom
		}
		return groups[i].PositionTo < groups[j].PositionTo
	})
	return groups
}

func canonicalFinalSwissSeeds(
	participants []uuid.UUID,
	input []SwissParticipantSeed,
) ([]SwissParticipantSeed, error) {
	if _, err := swissSeedMap(participants, input); err != nil {
		return nil, finalSwissError("invalid stable seeds: %v", err)
	}
	seeds := append([]SwissParticipantSeed(nil), input...)
	sort.Slice(seeds, func(i, j int) bool {
		return bytes.Compare(seeds[i].ParticipantID[:], seeds[j].ParticipantID[:]) < 0
	})
	return seeds, nil
}

func validateFinalSwissPredecessor(
	command FinalSwissProjectionCommand,
) (*finalSwissPredecessorReceipt, error) {
	if command.RevisionNo == 1 {
		if command.Previous != nil {
			return nil, finalSwissError("initial projection has a predecessor")
		}
		return nil, nil
	}
	if command.Previous == nil || command.Previous.Validate() != nil {
		return nil, finalSwissError("derived projection lacks a valid predecessor")
	}
	previous := command.Previous.Projection()
	revision := previous.Revision()
	if command.Previous.state.Authority.ProjectionID != command.ProjectionID ||
		revision.TournamentID() != command.TournamentID ||
		revision.Artifact() != (domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindStandings, EntityID: command.TournamentID}) ||
		revision.RevisionNo()+1 != command.RevisionNo ||
		command.CreatedAt.Before(revision.CreatedAt()) {
		return nil, finalSwissError("predecessor is not the current standings lineage head")
	}
	reserved, err := finalSwissAuthorityIdentitySet(command.Previous.state.Authority)
	if err != nil {
		return nil, finalSwissError("invalid predecessor identity lineage")
	}
	if command.Previous.state.Authority.Previous != nil {
		if err := mergeFinalSwissReservedIdentities(
			reserved, command.Previous.state.Authority.Previous.Reserved,
		); err != nil {
			return nil, err
		}
	}
	if len(reserved) > maxTask051ReservedIDs {
		return nil, finalSwissError("standings predecessor identity receipt exceeds DAG bounds")
	}
	return &finalSwissPredecessorReceipt{
		Projection: cloneFinalSwissDomainProjection(previous), ProjectionID: command.ProjectionID,
		Reserved: canonicalFinalSwissReservedIdentities(reserved),
	}, nil
}

func buildFinalSwissProjection(authority finalSwissAuthority) (FinalSwissProjection, error) {
	if err := validateFinalSwissPredecessorReceipt(authority); err != nil {
		return FinalSwissProjection{}, err
	}
	progressionRounds, sourceHeads, err := validateFinalSwissRounds(authority)
	if err != nil {
		return FinalSwissProjection{}, err
	}
	history, err := deriveSwissProgressionHistory(
		authority.ParticipantIDs,
		authority.Seeds,
		progressionRounds,
		true,
	)
	if err != nil {
		return FinalSwissProjection{}, finalSwissError("derive canonical ledger: %v", err)
	}
	standings := finalSwissStandings(history.standings)
	goldenStandings := finalSwissNormalStandings(standings)
	var previousID *domain.ArenaDerivedRevisionID
	if authority.Previous != nil {
		value := authority.Previous.Projection.Revision().ID()
		previousID = &value
	}
	goldenSource, err := NewGoldenStandingsProjection(
		authority.TournamentID, authority.ProjectionID, authority.RevisionID,
		authority.RevisionNo, previousID, true, goldenStandings,
	)
	if err != nil {
		return FinalSwissProjection{}, finalSwissError("build canonical Golden standings source: %v", err)
	}
	partition, err := PartitionGoldenTies(goldenSource)
	if err != nil {
		return FinalSwissProjection{}, finalSwissError("partition final point ties: %v", err)
	}
	tieGroups := finalSwissTies(partition)
	if err := validateFinalSwissGoldenIdentities(authority.GoldenGroups, tieGroups); err != nil {
		return FinalSwissProjection{}, err
	}
	payload, err := finalSwissPayload(authority, standings, tieGroups, sourceHeads, goldenSource)
	if err != nil || len(payload) == 0 || len(payload) > maxFinalSwissPayload {
		return FinalSwissProjection{}, finalSwissError("encode bounded standings payload")
	}
	projection, err := domain.NewArenaProjectionRevision(
		authority.RevisionID,
		authority.TournamentID,
		domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindStandings, EntityID: authority.TournamentID},
		authority.RevisionNo,
		previousID,
		authority.CreatedAt,
		payload,
	)
	if err != nil {
		return FinalSwissProjection{}, finalSwissError("build standings revision: %v", err)
	}
	seriesDependencies := finalSwissSeriesDependencies(sourceHeads, authority.RevisionID)
	dependencies := append([]domain.ArenaRevisionDependency(nil), seriesDependencies...)
	if authority.Previous != nil {
		dependencies = append(dependencies, domain.ArenaRevisionDependency{
			SourceRevisionID:  authority.Previous.Projection.Revision().ID(),
			DerivedRevisionID: authority.RevisionID,
		})
	}
	goldenGroups, err := buildFinalSwissGoldenGroups(authority, goldenSource, partition.GoldenGroups())
	if err != nil {
		return FinalSwissProjection{}, err
	}
	return FinalSwissProjection{state: finalSwissProjectionState{
		Authority: cloneFinalSwissAuthority(authority), Projection: projection,
		GoldenSource: goldenSource.Snapshot(),
		Standings:    cloneFinalSwissStandings(standings), TieGroups: cloneFinalSwissTieGroups(tieGroups),
		GoldenGroups:       cloneFinalSwissGoldenGroups(goldenGroups),
		SeriesDependencies: append([]domain.ArenaRevisionDependency(nil), seriesDependencies...),
		Dependencies:       append([]domain.ArenaRevisionDependency(nil), dependencies...),
		AdvanceDirectly:    len(goldenGroups) == 0,
	}}, nil
}

func validateFinalSwissPredecessorReceipt(authority finalSwissAuthority) error {
	if authority.Previous == nil {
		if authority.RevisionNo != 1 {
			return finalSwissError("missing bounded standings predecessor receipt")
		}
		return nil
	}
	receipt := authority.Previous
	revision := receipt.Projection.Revision()
	if receipt.Projection.Validate() != nil || receipt.ProjectionID != authority.ProjectionID ||
		revision.TournamentID() != authority.TournamentID ||
		revision.Artifact() != (domain.ArenaArtifactRef{
			Kind: domain.ArenaArtifactKindStandings, EntityID: authority.TournamentID,
		}) || revision.RevisionNo()+1 != authority.RevisionNo || authority.CreatedAt.Before(revision.CreatedAt()) {
		return finalSwissError("invalid bounded standings predecessor receipt")
	}
	return validateFinalSwissReservedReceipt(receipt, revision.ID().UUID())
}

func validateFinalSwissReservedReceipt(
	receipt *finalSwissPredecessorReceipt,
	revisionID uuid.UUID,
) error {
	if len(receipt.Reserved) == 0 || len(receipt.Reserved) > maxTask051ReservedIDs {
		return finalSwissError("standings predecessor identity receipt exceeds DAG bounds")
	}
	foundProjection, foundRevision := false, false
	for index, identity := range receipt.Reserved {
		if identity.ID == uuid.Nil || identity.Role == "" ||
			(index > 0 && bytes.Compare(receipt.Reserved[index-1].ID[:], identity.ID[:]) >= 0) {
			return finalSwissError("non-canonical standings predecessor identity receipt")
		}
		foundProjection = foundProjection || identity.ID == receipt.ProjectionID
		foundRevision = foundRevision || identity.ID == revisionID
	}
	if !foundProjection || !foundRevision {
		return finalSwissError("incomplete standings predecessor identity receipt")
	}
	return nil
}

func mergeFinalSwissReservedIdentities(
	reserved map[uuid.UUID]string,
	retained []finalSwissReservedIdentity,
) error {
	if len(reserved) > maxTask051ReservedIDs || len(retained) > maxTask051ReservedIDs {
		return finalSwissError("standings predecessor identity receipt exceeds DAG bounds")
	}
	for _, identity := range retained {
		if identity.ID == uuid.Nil || identity.Role == "" {
			return finalSwissError("invalid retained predecessor identity")
		}
		if role, exists := reserved[identity.ID]; exists {
			if role != identity.Role {
				return finalSwissError("retained identity role changed from %s to %s", identity.Role, role)
			}
			continue
		}
		if len(reserved) >= maxTask051ReservedIDs {
			return finalSwissError("standings predecessor identity receipt exceeds DAG bounds")
		}
		reserved[identity.ID] = identity.Role
	}
	return nil
}

func validateFinalSwissRounds(
	authority finalSwissAuthority,
) ([]SwissProgressionRound, []FinalSwissSeriesHead, error) {
	progression := make([]SwissProgressionRound, len(authority.Rounds))
	heads := make([]FinalSwissSeriesHead, 0)
	seenSeries := make(map[uuid.UUID]struct{})
	seenOfficial := make(map[domain.ArenaOfficialResultRevisionID]struct{})
	seenProjections := make(map[domain.ArenaDerivedRevisionID]struct{})
	for index, round := range authority.Rounds {
		if round.RoundNumber != index+1 || round.RoundID == uuid.Nil || round.RevisionID == uuid.Nil ||
			!validFinalSwissRoundLock(authority, round) {
			return nil, nil, finalSwissError("round lineage is not contiguous")
		}
		progression[index] = SwissProgressionRound{
			RoundID: round.RoundID, RoundNumber: round.RoundNumber, RevisionID: round.RevisionID,
			Series: make([]SwissSeriesPointResult, len(round.Series)),
		}
		for seriesIndex, head := range round.Series {
			if err := validateFinalSwissSeriesHead(authority, round, head); err != nil {
				return nil, nil, err
			}
			if _, duplicate := seenSeries[head.Result.SeriesID]; duplicate {
				return nil, nil, finalSwissError("Series head is duplicated")
			}
			officialID := finalSwissOfficialResultID(head)
			if _, duplicate := seenOfficial[officialID]; duplicate {
				return nil, nil, finalSwissError("official result head is duplicated")
			}
			projectionID := head.Projection.Revision().ID()
			if _, duplicate := seenProjections[projectionID]; duplicate {
				return nil, nil, finalSwissError("Series result projection head is duplicated")
			}
			seenSeries[head.Result.SeriesID] = struct{}{}
			seenOfficial[officialID] = struct{}{}
			seenProjections[projectionID] = struct{}{}
			progression[index].Series[seriesIndex] = cloneSwissSeriesPointResult(head.Result)
			heads = append(heads, cloneFinalSwissSeriesHead(head))
		}
		if round.Bye != nil {
			bye := *round.Bye
			progression[index].Bye = &bye
		}
	}
	return progression, heads, nil
}

func validFinalSwissRoundLock(authority finalSwissAuthority, round FinalSwissRound) bool {
	proof := round.LockProof
	if proof.Validate() != nil || proof.TournamentID != authority.TournamentID || proof.Preset != authority.Preset ||
		proof.RoundID != round.RoundID || proof.RoundNumber != round.RoundNumber ||
		proof.PlanRevisionID != round.RevisionID ||
		!reflect.DeepEqual(proof.RosterParticipantIDs, authority.ParticipantIDs) {
		return false
	}
	wantSeries := make([]SwissLockedSeries, len(round.Series))
	for index, head := range round.Series {
		wantSeries[index] = SwissLockedSeries{
			SeriesID: head.Series.ID, PairingID: proofSeriesPairingID(proof.Series, head.Series.ID),
			FirstParticipantID:  head.Series.FirstParticipantID,
			SecondParticipantID: head.Series.SecondParticipantID,
		}
		if wantSeries[index].PairingID == uuid.Nil {
			return false
		}
	}
	sort.Slice(wantSeries, func(i, j int) bool {
		return bytes.Compare(wantSeries[i].SeriesID[:], wantSeries[j].SeriesID[:]) < 0
	})
	if !reflect.DeepEqual(proof.Series, wantSeries) {
		return false
	}
	wantBye := uuid.Nil
	if round.Bye != nil {
		wantBye = round.Bye.ParticipantID
	}
	return proof.ByeParticipantID == wantBye
}

func proofSeriesPairingID(series []SwissLockedSeries, seriesID uuid.UUID) uuid.UUID {
	for _, locked := range series {
		if locked.SeriesID == seriesID {
			return locked.PairingID
		}
	}
	return uuid.Nil
}

func validateFinalSwissSeriesHead(
	authority finalSwissAuthority,
	round FinalSwissRound,
	head FinalSwissSeriesHead,
) error {
	if !validFinalSwissSeriesProjection(authority, round, head) ||
		!validFinalSwissOfficialBinding(authority, round, head) || !validFinalSwissScoreBinding(authority, head) {
		return finalSwissError("Series result is not one exact current terminal head")
	}
	if head.Series.State == domain.ArenaSeriesStateCompleted && head.Result.Label == SwissSeriesResultVoid {
		return finalSwissError("completed Series has a void result")
	}
	if head.Series.State == domain.ArenaSeriesStateCancelled && head.Result.Label != SwissSeriesResultVoid {
		return finalSwissError("cancelled Series has a decisive result")
	}
	if head.OfficialResult.NoGame != nil && !validFinalSwissNoGamePointShape(head) {
		return finalSwissError("no-game point label or timing is spliced")
	}
	return nil
}

func finalSwissOfficialResultID(head FinalSwissSeriesHead) domain.ArenaOfficialResultRevisionID {
	if head.OfficialResult.NoGame != nil {
		return head.OfficialResult.NoGame.Series.ID
	}
	return head.OfficialResult.Result.ID
}

func validFinalSwissSeriesProjection(
	authority finalSwissAuthority,
	round FinalSwissRound,
	head FinalSwissSeriesHead,
) bool {
	result := head.Result
	series := head.Series
	revision := head.Projection.Revision()
	return series.Validate() == nil && series.State.IsTerminal() &&
		series.ID == result.SeriesID && series.TournamentID == authority.TournamentID &&
		series.FirstParticipantID == result.FirstParticipantID &&
		series.SecondParticipantID == result.SecondParticipantID &&
		result.RoundID == round.RoundID && result.RoundNumber == round.RoundNumber &&
		head.Projection.Validate() == nil && revision.TournamentID() == authority.TournamentID &&
		revision.Artifact() == (domain.ArenaArtifactRef{
			Kind: domain.ArenaArtifactKindSeriesResult, EntityID: result.SeriesID,
		}) && !revision.CreatedAt().After(authority.CreatedAt)
}

func validFinalSwissOfficialBinding(
	authority finalSwissAuthority,
	round FinalSwissRound,
	head FinalSwissSeriesHead,
) bool {
	input := head.OfficialResult
	plan, err := ProjectOfficialResult(input)
	if err != nil || plan.Validate() != nil {
		return false
	}
	if input.NoGame != nil {
		return validFinalSwissNoGameBinding(authority, round, head, plan)
	}
	if !reflect.DeepEqual(input.ResultProjection, head.Projection) {
		return false
	}
	official := input.Result
	series := head.Series
	return series.CurrentResultRevisionID != nil && *series.CurrentResultRevisionID == official.ID &&
		official.ID == head.Result.ResultRevisionID &&
		official.Scope == (OfficialResultScope{
			TournamentID: authority.TournamentID, SeriesID: head.Result.SeriesID,
			Kind: OfficialResultSubjectSeries,
		}) && reflect.DeepEqual(official.SourceProjection, head.Projection.Revision()) &&
		!official.RecordedAt.After(authority.CreatedAt) && official.Outcome.SeriesState == series.State &&
		official.Outcome.SeriesReason.IsLegalFor(series.State) &&
		equalFinalSwissUUIDPointers(official.Outcome.WinnerID, series.WinnerID) &&
		equalFinalSwissUUIDPointers(head.Result.WinnerID, series.WinnerID) &&
		equalFinalSwissScoreRevisionPointers(official.Outcome.ScoreRevisionID, series.CurrentScoreRevisionID)
}

func validFinalSwissNoGameBinding(
	authority finalSwissAuthority,
	round FinalSwissRound,
	head FinalSwissSeriesHead,
	plan OfficialResultProjectionPlan,
) bool {
	recorded := head.OfficialResult.NoGame
	if recorded == nil || recorded.Scope.WaveID != round.LockProof.WaveID ||
		!validFinalSwissNoGameAuthority(authority, head, *recorded) ||
		!validFinalSwissNoGameTerminal(head, *recorded) {
		return false
	}
	return validFinalSwissNoGameProjection(authority, head, *recorded, plan) &&
		validFinalSwissNoGameTopology(head.Series, *recorded)
}

func validFinalSwissNoGameAuthority(
	authority finalSwissAuthority,
	head FinalSwissSeriesHead,
	recorded RecordedNoGameResult,
) bool {
	wantScope := NormalNoShowScope{
		TournamentID: authority.TournamentID,
		WaveID:       recorded.Scope.WaveID,
		WindowID:     recorded.Scope.WindowID,
		SeriesID:     head.Series.ID,
	}
	return reflect.DeepEqual(recorded.ResultProjection, head.Projection) && recorded.Scope == wantScope &&
		recorded.FirstParticipantID == head.Series.FirstParticipantID &&
		recorded.SecondParticipantID == head.Series.SecondParticipantID &&
		recorded.Format == head.Series.Format && !recorded.ResolvedAt.After(authority.CreatedAt)
}

func validFinalSwissNoGameTerminal(head FinalSwissSeriesHead, recorded RecordedNoGameResult) bool {
	return recorded.Series.ID == head.Result.ResultRevisionID &&
		recorded.Series.State == head.Series.State &&
		equalFinalSwissUUIDPointers(recorded.Series.WinnerID, head.Series.WinnerID) &&
		!recorded.Score.ID.IsZero() && recorded.Score.Score == head.Series.Score &&
		head.Series.CurrentResultRevisionID != nil &&
		*head.Series.CurrentResultRevisionID == recorded.Series.ID &&
		head.Series.CurrentScoreRevisionID != nil &&
		*head.Series.CurrentScoreRevisionID == recorded.Score.ID
}

func validFinalSwissNoGameProjection(
	authority finalSwissAuthority,
	head FinalSwissSeriesHead,
	recorded RecordedNoGameResult,
	plan OfficialResultProjectionPlan,
) bool {
	public := plan.Public()
	operator := plan.Operator()
	return public.Subject == OfficialResultSubjectSeries && public.TournamentID == authority.TournamentID &&
		public.SeriesID == head.Series.ID && public.ResolvedAt.Equal(recorded.ResolvedAt) &&
		equalFinalSwissUUIDPointers(public.WinnerID, head.Series.WinnerID) && public.Score != nil &&
		*public.Score == head.Series.Score && operator.ResultRevisionID == recorded.Series.ID &&
		operator.ScoreRevisionID != nil && *operator.ScoreRevisionID == recorded.Score.ID &&
		operator.SourceProjectionRevisionID == head.Projection.Revision().ID() &&
		operator.CommandID == recorded.CommandID
}

func validFinalSwissNoGameTopology(series domain.ArenaSeries, recorded RecordedNoGameResult) bool {
	if len(series.Slots) != len(recorded.Topology) || len(recorded.Topology) != len(recorded.GameResults) {
		return false
	}
	for index, binding := range recorded.Topology {
		if index >= len(series.Slots) {
			return false
		}
		if !validFinalSwissNoGameAttempt(series.Slots[index], binding, recorded.GameResults[index]) {
			return false
		}
	}
	return true
}

func validFinalSwissNoGameAttempt(
	slot domain.ArenaGameSlot,
	binding RecordedNoGameAttempt,
	recordedGame NormalNoShowGameRevision,
) bool {
	if slot.ID != binding.SlotID || slot.SeriesID != binding.SeriesID ||
		slot.Position != binding.SlotPosition || len(slot.Attempts) != 1 {
		return false
	}
	game := slot.Attempts[0]
	return game.ID == binding.GameID && game.SlotID == binding.SlotID &&
		game.AttemptNo == binding.AttemptNo && game.State == recordedGame.State &&
		game.ResultReason == recordedGame.Reason && game.WinnerID == nil &&
		game.ResultRevisionID != nil && *game.ResultRevisionID == binding.ResultRevisionID &&
		binding.ResultRevisionID == recordedGame.ID && recordedGame.GameID == game.ID
}

func validFinalSwissNoGamePointShape(head FinalSwissSeriesHead) bool {
	if head.Result.FirstEffectiveTime != 0 || head.Result.SecondEffectiveTime != 0 ||
		head.Result.FirstAcceptedSolveTime != nil || head.Result.SecondAcceptedSolveTime != nil {
		return false
	}
	recorded := head.OfficialResult.NoGame
	if recorded == nil {
		return false
	}
	switch recorded.Action {
	case NormalNoShowActionReopenWave:
		return head.Result.Label == SwissSeriesResultNoShow &&
			equalFinalSwissUUIDPointers(head.Result.WinnerID, recorded.Series.WinnerID)
	case NormalNoShowActionPauseWave:
		return head.Result.Label == SwissSeriesResultVoid && head.Result.WinnerID == nil
	default:
		return false
	}
}

func validFinalSwissScoreBinding(authority finalSwissAuthority, head FinalSwissSeriesHead) bool {
	input := head.OfficialResult
	if input.NoGame != nil {
		return validFinalSwissNoGameScore(head.Series, *input.NoGame)
	}
	return validFinalSwissOrdinaryScore(authority, head.Series, input)
}

func validFinalSwissNoGameScore(series domain.ArenaSeries, recorded RecordedNoGameResult) bool {
	return series.CurrentScoreRevisionID != nil && recorded.Score.SeriesID == series.ID &&
		recorded.Series.SeriesID == series.ID && recorded.Score.ID == *series.CurrentScoreRevisionID &&
		recorded.Score.Score == series.Score && recorded.Score.RecordedAt.Equal(recorded.ResolvedAt) &&
		recorded.Series.ScoreRevisionID == recorded.Score.ID &&
		recorded.Series.RecordedAt.Equal(recorded.ResolvedAt)
}

func validFinalSwissOrdinaryScore(
	authority finalSwissAuthority,
	series domain.ArenaSeries,
	input OfficialResultProjectionInput,
) bool {
	if input.Score == nil || input.ScoreProjection == nil || series.CurrentScoreRevisionID == nil {
		return false
	}
	return input.Score.ID == *series.CurrentScoreRevisionID &&
		input.Score.Scope == (SeriesScoreRevisionScope{
			TournamentID: authority.TournamentID, SeriesID: series.ID,
		}) && input.Score.FirstParticipantID == series.FirstParticipantID &&
		input.Score.SecondParticipantID == series.SecondParticipantID &&
		input.Score.Format == series.Format && input.Score.Score == series.Score
}

func equalFinalSwissUUIDPointers(first, second *uuid.UUID) bool {
	return (first == nil && second == nil) ||
		(first != nil && second != nil && *first == *second)
}

func equalFinalSwissScoreRevisionPointers(
	first, second *domain.ArenaSeriesScoreRevisionID,
) bool {
	return (first == nil && second == nil) ||
		(first != nil && second != nil && *first == *second)
}

func finalSwissStandings(normal []SwissNormalStanding) []FinalSwissStanding {
	standings := make([]FinalSwissStanding, len(normal))
	for index, standing := range normal {
		standings[index] = FinalSwissStanding{
			ParticipantID: standing.ParticipantID, Points: standing.Points,
			PointsLabel: standing.PointsLabel, Buchholz: standing.Buchholz,
			BuchholzStatus:   SwissBuchholzFinal,
			HeadToHeadPoints: standing.HeadToHeadPoints, HeadToHeadApplied: standing.HeadToHeadApplied,
			EffectiveTime:     standing.EffectiveTime,
			AcceptedSolveTime: cloneDurationPointer(standing.AcceptedSolveTime), StableSeed: standing.Seed,
		}
	}
	for index := range standings {
		standings[index].Position = index + 1
	}
	return standings
}

func finalSwissNormalStandings(standings []FinalSwissStanding) []SwissNormalStanding {
	normal := make([]SwissNormalStanding, len(standings))
	for index, standing := range standings {
		normal[index] = SwissNormalStanding{
			ParticipantID: standing.ParticipantID, Position: standing.Position,
			Points: standing.Points, PointsLabel: standing.PointsLabel,
			Buchholz: standing.Buchholz, HeadToHeadPoints: standing.HeadToHeadPoints,
			HeadToHeadApplied: standing.HeadToHeadApplied, EffectiveTime: standing.EffectiveTime,
			AcceptedSolveTime: cloneDurationPointer(standing.AcceptedSolveTime), Seed: standing.StableSeed,
		}
	}
	return normal
}

func finalSwissTies(partition GoldenTiePartition) []FinalSwissTieGroup {
	groups := make([]FinalSwissTieGroup, 0)
	for _, segment := range partition.Segments {
		count := segment.PositionTo - segment.PositionFrom + 1
		if count < 2 {
			continue
		}
		participants := make([]uuid.UUID, count)
		if segment.Golden {
			for index, member := range segment.Members {
				participants[index] = member.ParticipantID
			}
		} else {
			for index, standing := range segment.NormalStandings {
				participants[index] = standing.ParticipantID
			}
		}
		groups = append(groups, FinalSwissTieGroup{
			PositionFrom: segment.PositionFrom, PositionTo: segment.PositionTo,
			Impactful: segment.Golden, ParticipantIDs: participants,
		})
	}
	return groups
}

func validateFinalSwissGoldenIdentities(
	identities []FinalSwissGoldenGroupIdentity,
	ties []FinalSwissTieGroup,
) error {
	impactful := make([]FinalSwissTieGroup, 0, len(ties))
	for _, tie := range ties {
		if tie.Impactful {
			impactful = append(impactful, tie)
		}
	}
	if len(identities) != len(impactful) {
		return finalSwissError("Golden identities do not cover exact maximal impactful ties")
	}
	for index, identity := range identities {
		if identity.GroupID == uuid.Nil || identity.RevisionID.IsZero() ||
			identity.PositionFrom != impactful[index].PositionFrom || identity.PositionTo != impactful[index].PositionTo {
			return finalSwissError("Golden identity does not match an exact maximal impactful tie")
		}
	}
	return nil
}

func buildFinalSwissGoldenGroups(
	authority finalSwissAuthority,
	source GoldenStandingsProjection,
	seeds []GoldenTieGroupSeed,
) ([]FinalSwissGoldenGroup, error) {
	groups := make([]FinalSwissGoldenGroup, len(seeds))
	used := make([]GoldenRevisionIdentity, 0, len(seeds))
	for index, seed := range seeds {
		identity := authority.GoldenGroups[index]
		revision, err := BuildGoldenGroupRevision(
			GoldenGroupRevisionCommand{
				TournamentID: authority.TournamentID, GroupID: identity.GroupID,
				RevisionID: identity.RevisionID, RevisionNo: 1,
				ExpectedSourceRevisionID:    source.RevisionID,
				ExpectedSourcePayloadDigest: source.PayloadDigest,
				PositionFrom:                seed.PositionFrom, PositionTo: seed.PositionTo,
			},
			source, seed, nil, used,
		)
		if err != nil {
			return nil, finalSwissError("build canonical Golden topology: %v", err)
		}
		used = append(used, GoldenRevisionIdentity{GroupID: identity.GroupID, RevisionID: identity.RevisionID})
		members := make([]domain.ArenaGoldenMember, len(seed.Members))
		for memberIndex, member := range seed.Members {
			members[memberIndex] = domain.ArenaGoldenMember{ParticipantID: member.ParticipantID}
		}
		state := domain.ArenaGoldenGroupState{
			ID: identity.GroupID, TournamentID: authority.TournamentID,
			RevisionID: identity.RevisionID, SourceProjectionRevisionID: authority.RevisionID,
			PositionFrom: seed.PositionFrom, PositionTo: seed.PositionTo, Members: members,
		}
		group, err := domain.NewArenaGoldenGroup(state)
		if err != nil {
			return nil, finalSwissError("build Golden group: %v", err)
		}
		state = group.Snapshot()
		payload := revision.Payload()
		if len(payload) == 0 || len(payload) > maxFinalSwissPayload {
			return nil, finalSwissError("encode bounded Golden group payload")
		}
		projection, err := domain.NewArenaProjectionRevision(
			identity.RevisionID,
			authority.TournamentID,
			domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindGoldenGroup, EntityID: identity.GroupID},
			1, nil, authority.CreatedAt, payload,
		)
		if err != nil {
			return nil, finalSwissError("build Golden projection: %v", err)
		}
		groups[index] = FinalSwissGoldenGroup{
			State: state, Revision: revision.Snapshot(), Projection: projection,
			Dependency: domain.ArenaRevisionDependency{
				SourceRevisionID: authority.RevisionID, DerivedRevisionID: identity.RevisionID,
			},
		}
	}
	return groups, nil
}

func finalSwissSeriesDependencies(
	heads []FinalSwissSeriesHead,
	derived domain.ArenaDerivedRevisionID,
) []domain.ArenaRevisionDependency {
	dependencies := make([]domain.ArenaRevisionDependency, len(heads))
	for index, head := range heads {
		dependencies[index] = domain.ArenaRevisionDependency{
			SourceRevisionID: head.Projection.Revision().ID(), DerivedRevisionID: derived,
		}
	}
	sort.Slice(dependencies, func(i, j int) bool {
		first := dependencies[i].SourceRevisionID.UUID()
		second := dependencies[j].SourceRevisionID.UUID()
		return bytes.Compare(
			first[:],
			second[:],
		) < 0
	})
	return dependencies
}

type finalSwissPayloadDocument struct {
	TournamentID       uuid.UUID                       `json:"tournament_id"`
	ProjectionID       uuid.UUID                       `json:"projection_id"`
	Preset             domain.ArenaPreset              `json:"preset"`
	RevisionNo         int                             `json:"revision_no"`
	PreviousRevisionID *uuid.UUID                      `json:"previous_revision_id,omitempty"`
	CreatedAt          time.Time                       `json:"created_at"`
	Standings          []FinalSwissStanding            `json:"standings"`
	TieGroups          []FinalSwissTieGroup            `json:"tie_groups"`
	GoldenGroups       []FinalSwissGoldenGroupIdentity `json:"golden_groups"`
	GoldenSourceDigest string                          `json:"golden_source_digest"`
	Rounds             []finalSwissRoundPayload        `json:"rounds"`
	Series             []finalSwissSeriesPayload       `json:"series"`
}

type finalSwissRoundPayload struct {
	RoundID          uuid.UUID           `json:"round_id"`
	RoundNumber      int                 `json:"round_number"`
	RevisionID       uuid.UUID           `json:"revision_id"`
	LockProof        SwissRoundLockProof `json:"lock_proof"`
	ByeRevisionID    *uuid.UUID          `json:"bye_revision_id,omitempty"`
	ByeParticipantID *uuid.UUID          `json:"bye_participant_id,omitempty"`
}

type finalSwissSeriesPayload struct {
	RoundID                  uuid.UUID              `json:"round_id"`
	RoundNumber              int                    `json:"round_number"`
	SeriesID                 uuid.UUID              `json:"series_id"`
	FirstParticipantID       uuid.UUID              `json:"first_participant_id"`
	SecondParticipantID      uuid.UUID              `json:"second_participant_id"`
	ScoreRevisionID          uuid.UUID              `json:"score_revision_id"`
	ScoreCommandID           uuid.UUID              `json:"score_command_id"`
	ScoreProjectionRevision  uuid.UUID              `json:"score_projection_revision"`
	ScoreProjectionDigest    string                 `json:"score_projection_digest"`
	OfficialCommandID        uuid.UUID              `json:"official_command_id"`
	OfficialResultRevision   uuid.UUID              `json:"official_result_revision"`
	OfficialSourceDigest     string                 `json:"official_source_digest"`
	ProjectionRevision       uuid.UUID              `json:"projection_revision"`
	ProjectionPayloadDigest  string                 `json:"projection_payload_digest"`
	State                    string                 `json:"state"`
	WinnerID                 uuid.UUID              `json:"winner_id"`
	OfficialResultRecordedAt time.Time              `json:"official_result_recorded_at"`
	ResultLabel              SwissSeriesResultLabel `json:"result_label"`
	FirstEffectiveTime       int64                  `json:"first_effective_time_ns"`
	SecondEffectiveTime      int64                  `json:"second_effective_time_ns"`
	FirstAcceptedSolveTime   *int64                 `json:"first_accepted_solve_time_ns,omitempty"`
	SecondAcceptedSolveTime  *int64                 `json:"second_accepted_solve_time_ns,omitempty"`
	NoGame                   bool                   `json:"no_game"`
	NoGameEvidenceDigest     string                 `json:"no_game_evidence_digest,omitempty"`
}

type finalSwissProjectionEvidence struct {
	ID         uuid.UUID                `json:"id"`
	PreviousID *uuid.UUID               `json:"previous_id,omitempty"`
	Digest     string                   `json:"digest"`
	CreatedAt  time.Time                `json:"created_at"`
	Kind       domain.ArenaArtifactKind `json:"kind"`
	EntityID   uuid.UUID                `json:"entity_id"`
}

type finalSwissNoGameDigestDocument struct {
	RecordedEvidence []byte                         `json:"recorded_evidence"`
	Projections      []finalSwissProjectionEvidence `json:"projections"`
}

type finalSwissNoGameRecordedEvidence struct {
	Scope               NormalNoShowScope
	CommandID           uuid.UUID
	Action              NormalNoShowAction
	Format              domain.ArenaSeriesFormat
	FirstParticipantID  uuid.UUID
	SecondParticipantID uuid.UUID
	ReadyParticipantID  *uuid.UUID
	GameResults         []NormalNoShowGameRevision
	Topology            []RecordedNoGameAttempt
	Score               NormalNoShowScoreRevision
	Series              NormalNoShowSeriesRevision
	GameDependencies    []domain.ArenaRevisionDependency
	ResultDependency    domain.ArenaRevisionDependency
	ResolvedAt          time.Time
}

func finalSwissPayload(
	authority finalSwissAuthority,
	standings []FinalSwissStanding,
	ties []FinalSwissTieGroup,
	heads []FinalSwissSeriesHead,
	goldenSource GoldenStandingsProjection,
) ([]byte, error) {
	document := finalSwissPayloadDocument{
		TournamentID: authority.TournamentID, ProjectionID: authority.ProjectionID, Preset: authority.Preset,
		RevisionNo: authority.RevisionNo, CreatedAt: authority.CreatedAt,
		Standings: cloneFinalSwissStandings(standings), TieGroups: cloneFinalSwissTieGroups(ties),
		GoldenGroups: append([]FinalSwissGoldenGroupIdentity(nil), authority.GoldenGroups...),
		Rounds:       make([]finalSwissRoundPayload, len(authority.Rounds)),
		Series:       make([]finalSwissSeriesPayload, len(heads)),
	}
	document.GoldenSourceDigest = hex.EncodeToString(goldenSource.PayloadDigest[:])
	for index, round := range authority.Rounds {
		document.Rounds[index] = finalSwissRoundPayload{
			RoundID: round.RoundID, RoundNumber: round.RoundNumber, RevisionID: round.RevisionID,
			LockProof: cloneSwissRoundLockProof(round.LockProof),
		}
		if round.Bye != nil {
			value := round.Bye.RevisionID
			document.Rounds[index].ByeRevisionID = &value
			participantID := round.Bye.ParticipantID
			document.Rounds[index].ByeParticipantID = &participantID
		}
	}
	if authority.Previous != nil {
		value := authority.Previous.Projection.Revision().ID().UUID()
		document.PreviousRevisionID = &value
	}
	for index, head := range heads {
		seriesPayload, err := finalSwissSeriesPayloadFromHead(head)
		if err != nil {
			return nil, err
		}
		document.Series[index] = seriesPayload
	}
	sort.Slice(document.Series, func(i, j int) bool {
		return bytes.Compare(document.Series[i].SeriesID[:], document.Series[j].SeriesID[:]) < 0
	})
	return json.Marshal(document)
}

func finalSwissSeriesPayloadFromHead(head FinalSwissSeriesHead) (finalSwissSeriesPayload, error) {
	revision := head.Projection.Revision()
	digest := revision.PayloadDigest()
	scoreRevisionID := uuid.Nil
	if head.Series.CurrentScoreRevisionID != nil {
		scoreRevisionID = head.Series.CurrentScoreRevisionID.UUID()
	}
	winnerID := uuid.Nil
	if head.Series.WinnerID != nil {
		winnerID = *head.Series.WinnerID
	}
	officialResultID := head.OfficialResult.Result.ID.UUID()
	officialCommandID := head.OfficialResult.Result.CommandID
	officialRecordedAt := head.OfficialResult.Result.RecordedAt
	officialDigest := head.OfficialResult.Result.SourceProjection.PayloadDigest()
	scoreCommandID := uuid.Nil
	scoreProjection := head.OfficialResult.ScoreProjection
	noGameEvidenceDigest := ""
	if head.OfficialResult.NoGame != nil {
		recorded := *head.OfficialResult.NoGame
		officialResultID = recorded.Series.ID.UUID()
		officialCommandID = recorded.CommandID
		officialRecordedAt = recorded.ResolvedAt
		officialDigest = recorded.ResultProjection.Revision().PayloadDigest()
		scoreCommandID = recorded.CommandID
		scoreProjection = &recorded.ScoreProjection
		var err error
		noGameEvidenceDigest, err = finalSwissNoGameEvidenceDigest(recorded)
		if err != nil {
			return finalSwissSeriesPayload{}, err
		}
	} else if head.OfficialResult.Score != nil {
		scoreCommandID = head.OfficialResult.Score.CommandID
	}
	if scoreProjection == nil {
		return finalSwissSeriesPayload{}, finalSwissError("terminal Series has no score projection payload evidence")
	}
	scoreProjectionRevision := scoreProjection.Revision()
	scoreProjectionDigest := scoreProjectionRevision.PayloadDigest()
	return finalSwissSeriesPayload{
		RoundID: head.Result.RoundID, RoundNumber: head.Result.RoundNumber,
		SeriesID:                head.Result.SeriesID,
		FirstParticipantID:      head.Result.FirstParticipantID,
		SecondParticipantID:     head.Result.SecondParticipantID,
		ScoreRevisionID:         scoreRevisionID,
		ScoreCommandID:          scoreCommandID,
		ScoreProjectionRevision: scoreProjectionRevision.ID().UUID(),
		ScoreProjectionDigest:   hex.EncodeToString(scoreProjectionDigest[:]),
		OfficialCommandID:       officialCommandID,
		OfficialResultRevision:  officialResultID,
		OfficialSourceDigest:    hex.EncodeToString(officialDigest[:]),
		ProjectionRevision:      revision.ID().UUID(),
		ProjectionPayloadDigest: hex.EncodeToString(digest[:]),
		State:                   string(head.Series.State), WinnerID: winnerID,
		OfficialResultRecordedAt: officialRecordedAt,
		ResultLabel:              head.Result.Label,
		FirstEffectiveTime:       int64(head.Result.FirstEffectiveTime),
		SecondEffectiveTime:      int64(head.Result.SecondEffectiveTime),
		FirstAcceptedSolveTime:   finalSwissDurationNanos(head.Result.FirstAcceptedSolveTime),
		SecondAcceptedSolveTime:  finalSwissDurationNanos(head.Result.SecondAcceptedSolveTime),
		NoGame:                   head.OfficialResult.NoGame != nil,
		NoGameEvidenceDigest:     noGameEvidenceDigest,
	}, nil
}

func finalSwissDurationNanos(value *time.Duration) *int64 {
	if value == nil {
		return nil
	}
	nanos := int64(*value)
	return &nanos
}

func finalSwissNoGameEvidenceDigest(recorded RecordedNoGameResult) (string, error) {
	var recordedEvidence bytes.Buffer
	if err := gob.NewEncoder(&recordedEvidence).Encode(finalSwissNoGameRecordedEvidence{
		Scope: recorded.Scope, CommandID: recorded.CommandID, Action: recorded.Action, Format: recorded.Format,
		FirstParticipantID: recorded.FirstParticipantID, SecondParticipantID: recorded.SecondParticipantID,
		ReadyParticipantID: recorded.ReadyParticipantID,
		GameResults:        recorded.GameResults, Topology: recorded.Topology,
		Score: recorded.Score, Series: recorded.Series,
		GameDependencies: recorded.GameDependencies, ResultDependency: recorded.ResultDependency,
		ResolvedAt: recorded.ResolvedAt,
	}); err != nil {
		return "", finalSwissError("encode no-game evidence: %v", err)
	}
	projections := make([]domain.ArenaProjectionRevision, 0, len(recorded.GameProjections)+2)
	projections = append(projections, recorded.GameProjections...)
	projections = append(projections, recorded.ScoreProjection, recorded.ResultProjection)
	references := make([]finalSwissProjectionEvidence, len(projections))
	for index, projection := range projections {
		revision := projection.Revision()
		digest := revision.PayloadDigest()
		var previous *uuid.UUID
		if value := revision.PreviousRevisionID(); value != nil {
			id := value.UUID()
			previous = &id
		}
		references[index] = finalSwissProjectionEvidence{
			ID: revision.ID().UUID(), PreviousID: previous,
			Digest: hex.EncodeToString(digest[:]), CreatedAt: revision.CreatedAt(),
			Kind: revision.Artifact().Kind, EntityID: revision.Artifact().EntityID,
		}
	}
	payload, err := json.Marshal(finalSwissNoGameDigestDocument{
		RecordedEvidence: recordedEvidence.Bytes(), Projections: references,
	})
	if err != nil {
		return "", finalSwissError("encode no-game audit evidence: %v", err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

type finalSwissIdentityRegistry struct {
	roles map[uuid.UUID]string
}

func validateFinalSwissIdentityRoles(authority finalSwissAuthority) error {
	_, err := finalSwissAuthorityIdentitySet(authority)
	if err != nil || authority.Previous == nil {
		return err
	}
	previous := make(map[uuid.UUID]string, len(authority.Previous.Reserved))
	for _, identity := range authority.Previous.Reserved {
		if identity.ID == uuid.Nil || identity.Role == "" {
			return finalSwissError("invalid predecessor identity lineage")
		}
		if _, duplicate := previous[identity.ID]; duplicate {
			return finalSwissError("duplicate predecessor identity lineage")
		}
		previous[identity.ID] = identity.Role
	}
	candidates := make([]uuid.UUID, 0, len(authority.GoldenGroups)*2+1)
	candidates = append(candidates, authority.RevisionID.UUID())
	for _, group := range authority.GoldenGroups {
		candidates = append(candidates, group.GroupID, group.RevisionID.UUID())
	}
	seen := make(map[uuid.UUID]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, exists := previous[candidate]; exists {
			return finalSwissError("new revision or group aliases predecessor authority")
		}
		if _, exists := seen[candidate]; exists {
			return finalSwissError("new revision and group identities alias each other")
		}
		seen[candidate] = struct{}{}
	}
	return nil
}

func finalSwissAuthorityIdentitySet(
	authority finalSwissAuthority,
) (map[uuid.UUID]string, error) {
	registry := finalSwissIdentityRegistry{roles: make(map[uuid.UUID]string)}
	if err := defineFinalSwissBaseIdentities(&registry, authority); err != nil {
		return nil, err
	}
	for _, round := range authority.Rounds {
		if err := defineFinalSwissRoundIdentities(&registry, round); err != nil {
			return nil, err
		}
	}
	for _, group := range authority.GoldenGroups {
		if err := registry.define(group.GroupID, "Golden group"); err != nil {
			return nil, err
		}
		if err := registry.define(group.RevisionID.UUID(), finalSwissRevisionRole); err != nil {
			return nil, err
		}
	}
	return registry.roles, nil
}

func defineFinalSwissBaseIdentities(
	registry *finalSwissIdentityRegistry,
	authority finalSwissAuthority,
) error {
	for _, identity := range []struct {
		id   uuid.UUID
		role string
	}{
		{id: authority.TournamentID, role: "tournament"},
		{id: authority.ProjectionID, role: "standings projection"},
		{id: authority.RevisionID.UUID(), role: finalSwissRevisionRole},
	} {
		if err := registry.define(identity.id, identity.role); err != nil {
			return err
		}
	}
	if authority.Previous != nil {
		if err := registry.define(authority.Previous.Projection.Revision().ID().UUID(), finalSwissRevisionRole); err != nil {
			return err
		}
	}
	for _, participantID := range authority.ParticipantIDs {
		if err := registry.define(participantID, "participant"); err != nil {
			return err
		}
	}
	return nil
}

func defineFinalSwissRoundIdentities(
	registry *finalSwissIdentityRegistry,
	round FinalSwissRound,
) error {
	if err := registry.define(round.RoundID, "round"); err != nil {
		return err
	}
	if err := registry.define(round.RevisionID, "round revision"); err != nil {
		return err
	}
	for _, identity := range []struct {
		id   uuid.UUID
		role string
	}{
		{id: round.LockProof.RosterID, role: "round roster authority"},
		{id: round.LockProof.SourceRevisionID, role: "round source revision:" + round.RoundID.String()},
		{id: round.LockProof.CategoryRevisionID, role: "round category revision"},
		{id: round.LockProof.PoolRevisionID, role: "round pool revision"},
		{id: round.LockProof.PreflightRevisionID, role: "round preflight revision:" + round.RoundID.String()},
		{id: round.LockProof.WaveID, role: "round Wave:" + round.RoundID.String()},
		{id: round.LockProof.WaveRevisionID.UUID(), role: "round Wave revision:" + round.RoundID.String()},
	} {
		if err := registry.defineShared(identity.id, identity.role); err != nil {
			return err
		}
	}
	for _, series := range round.LockProof.Series {
		if err := registry.defineShared(series.PairingID, "round pairing:"+series.SeriesID.String()); err != nil {
			return err
		}
	}
	for _, head := range round.Series {
		if err := defineFinalSwissHeadIdentities(registry, round, head); err != nil {
			return err
		}
	}
	if round.Bye != nil {
		return registry.define(round.Bye.RevisionID, "bye revision")
	}
	return nil
}

func defineFinalSwissHeadIdentities(
	registry *finalSwissIdentityRegistry,
	round FinalSwissRound,
	head FinalSwissSeriesHead,
) error {
	if head.OfficialResult.NoGame != nil {
		return defineFinalSwissNoGameIdentities(registry, round, head)
	}
	if head.Series.CurrentScoreRevisionID == nil || head.OfficialResult.Score == nil ||
		head.OfficialResult.ScoreProjection == nil {
		return finalSwissError("terminal Series has no typed score proof")
	}
	identities := []struct {
		id   uuid.UUID
		role string
	}{
		{id: head.Result.SeriesID, role: "Series"},
		{id: head.OfficialResult.Result.ID.UUID(), role: "official result revision"},
		{id: head.OfficialResult.Result.CommandID, role: "official result command"},
		{id: head.Series.CurrentScoreRevisionID.UUID(), role: "score revision"},
		{id: head.OfficialResult.Score.CommandID, role: "score command"},
		{id: head.OfficialResult.ScoreProjection.Revision().ID().UUID(), role: finalSwissRevisionRole},
		{id: head.Projection.Revision().ID().UUID(), role: finalSwissRevisionRole},
	}
	for _, identity := range identities {
		if err := registry.define(identity.id, identity.role); err != nil {
			return err
		}
	}
	if err := defineFinalSwissPredecessorIdentities(registry, head); err != nil {
		return err
	}
	if err := defineFinalSwissProjectionPredecessor(registry, head.Projection); err != nil {
		return err
	}
	if err := defineFinalSwissProjectionPredecessor(registry, *head.OfficialResult.ScoreProjection); err != nil {
		return err
	}
	return defineFinalSwissAttemptIdentities(registry, head.OfficialResult.Score.Attempts)
}

func defineFinalSwissNoGameIdentities(
	registry *finalSwissIdentityRegistry,
	round FinalSwissRound,
	head FinalSwissSeriesHead,
) error {
	recorded := head.OfficialResult.NoGame
	if recorded == nil {
		return finalSwissError("terminal no-game evidence is missing")
	}
	if len(recorded.Topology) != len(recorded.GameResults) ||
		len(recorded.GameProjections) != len(recorded.GameResults) {
		return finalSwissError("no-game identity evidence is incomplete")
	}
	identities := []struct {
		id   uuid.UUID
		role string
	}{
		{id: head.Series.ID, role: "Series"},
		{id: recorded.Scope.WindowID, role: "ready window"},
		{id: recorded.CommandID, role: "no-game command"},
		{id: recorded.Score.ID.UUID(), role: "score revision"},
		{id: recorded.Series.ID.UUID(), role: "official result revision"},
		{id: recorded.ScoreProjection.Revision().ID().UUID(), role: finalSwissRevisionRole},
		{id: recorded.ResultProjection.Revision().ID().UUID(), role: finalSwissRevisionRole},
	}
	if err := registry.defineShared(
		recorded.Scope.WaveID, "round Wave:"+round.RoundID.String(),
	); err != nil {
		return err
	}
	for _, identity := range identities {
		if err := registry.define(identity.id, identity.role); err != nil {
			return err
		}
	}
	return defineFinalSwissNoGameLineage(registry, *recorded)
}

func defineFinalSwissNoGameLineage(
	registry *finalSwissIdentityRegistry,
	recorded RecordedNoGameResult,
) error {
	if recorded.Score.PreviousRevisionID != nil {
		if err := registry.define(recorded.Score.PreviousRevisionID.UUID(), "score predecessor"); err != nil {
			return err
		}
	}
	if recorded.Series.PreviousRevisionID != nil {
		if err := registry.define(recorded.Series.PreviousRevisionID.UUID(), "official result predecessor"); err != nil {
			return err
		}
	}
	for index := range recorded.GameResults {
		if err := defineFinalSwissNoGameAttemptIdentities(registry, recorded, index); err != nil {
			return err
		}
	}
	if err := defineFinalSwissProjectionPredecessor(registry, recorded.ScoreProjection); err != nil {
		return err
	}
	return defineFinalSwissProjectionPredecessor(registry, recorded.ResultProjection)
}

func defineFinalSwissNoGameAttemptIdentities(
	registry *finalSwissIdentityRegistry,
	recorded RecordedNoGameResult,
	index int,
) error {
	for _, identity := range []struct {
		id   uuid.UUID
		role string
	}{
		{id: recorded.Topology[index].SlotID, role: "Game slot"},
		{id: recorded.Topology[index].GameID, role: "Game"},
		{id: recorded.GameResults[index].ID.UUID(), role: "Game result revision"},
		{id: recorded.GameProjections[index].Revision().ID().UUID(), role: finalSwissRevisionRole},
	} {
		if err := registry.define(identity.id, identity.role); err != nil {
			return err
		}
	}
	if recorded.GameResults[index].PreviousRevisionID != nil {
		if err := registry.define(
			recorded.GameResults[index].PreviousRevisionID.UUID(), "Game result predecessor",
		); err != nil {
			return err
		}
	}
	return defineFinalSwissProjectionPredecessor(registry, recorded.GameProjections[index])
}

func defineFinalSwissProjectionPredecessor(
	registry *finalSwissIdentityRegistry,
	projection domain.ArenaProjectionRevision,
) error {
	previous := projection.Revision().PreviousRevisionID()
	if previous == nil {
		return nil
	}
	return registry.define(previous.UUID(), "projection predecessor")
}

func defineFinalSwissPredecessorIdentities(
	registry *finalSwissIdentityRegistry,
	head FinalSwissSeriesHead,
) error {
	if head.OfficialResult.Result.PreviousRevisionID != nil {
		if err := registry.define(
			head.OfficialResult.Result.PreviousRevisionID.UUID(), "official result predecessor",
		); err != nil {
			return err
		}
	}
	if head.OfficialResult.Score.PreviousRevisionID != nil {
		return registry.define(head.OfficialResult.Score.PreviousRevisionID.UUID(), "score predecessor")
	}
	return nil
}

func defineFinalSwissAttemptIdentities(
	registry *finalSwissIdentityRegistry,
	attempts []SeriesScoreAttemptReference,
) error {
	for _, attempt := range attempts {
		for _, identity := range []struct {
			id   uuid.UUID
			role string
		}{
			{id: attempt.SlotID, role: "Game slot"},
			{id: attempt.GameID, role: "Game"},
			{id: attempt.CurrentGameResultRevisionID.UUID(), role: "Game result revision"},
		} {
			if err := registry.define(identity.id, identity.role); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *finalSwissIdentityRegistry) define(id uuid.UUID, role string) error {
	if id == uuid.Nil {
		return finalSwissError("missing %s identity", role)
	}
	if existing, duplicate := r.roles[id]; duplicate {
		return finalSwissError("identity aliases %s and %s", existing, role)
	}
	r.roles[id] = role
	return nil
}

func (r *finalSwissIdentityRegistry) defineShared(id uuid.UUID, role string) error {
	if id == uuid.Nil {
		return finalSwissError("missing %s identity", role)
	}
	if existing, duplicate := r.roles[id]; duplicate {
		if existing == role {
			return nil
		}
		return finalSwissError("identity aliases %s and %s", existing, role)
	}
	r.roles[id] = role
	return nil
}

func cloneFinalSwissAuthority(input finalSwissAuthority) finalSwissAuthority {
	clone := input
	if input.Previous != nil {
		clone.Previous = cloneFinalSwissPredecessorReceipt(input.Previous)
	}
	clone.ParticipantIDs = append([]uuid.UUID(nil), input.ParticipantIDs...)
	clone.Seeds = append([]SwissParticipantSeed(nil), input.Seeds...)
	clone.Rounds = cloneFinalSwissRounds(input.Rounds)
	clone.GoldenGroups = append([]FinalSwissGoldenGroupIdentity(nil), input.GoldenGroups...)
	return clone
}

func canonicalFinalSwissReservedIdentities(
	input map[uuid.UUID]string,
) []finalSwissReservedIdentity {
	if len(input) > maxTask051ReservedIDs {
		return nil
	}
	reserved := make([]finalSwissReservedIdentity, 0, len(input))
	for id, role := range input {
		reserved = append(reserved, finalSwissReservedIdentity{ID: id, Role: role})
	}
	sort.Slice(reserved, func(i, j int) bool {
		comparison := bytes.Compare(reserved[i].ID[:], reserved[j].ID[:])
		if comparison != 0 {
			return comparison < 0
		}
		return reserved[i].Role < reserved[j].Role
	})
	return reserved
}

func cloneFinalSwissPredecessorReceipt(
	input *finalSwissPredecessorReceipt,
) *finalSwissPredecessorReceipt {
	if input == nil {
		return nil
	}
	if len(input.Reserved) > maxTask051ReservedIDs {
		return &finalSwissPredecessorReceipt{
			Projection: cloneFinalSwissDomainProjection(input.Projection), ProjectionID: input.ProjectionID,
		}
	}
	return &finalSwissPredecessorReceipt{
		Projection: cloneFinalSwissDomainProjection(input.Projection), ProjectionID: input.ProjectionID,
		Reserved: append([]finalSwissReservedIdentity(nil), input.Reserved...),
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
	clone.SeriesDependencies = append([]domain.ArenaRevisionDependency(nil), input.SeriesDependencies...)
	clone.Dependencies = append([]domain.ArenaRevisionDependency(nil), input.Dependencies...)
	return clone
}

func cloneFinalSwissRounds(input []FinalSwissRound) []FinalSwissRound {
	clone := make([]FinalSwissRound, len(input))
	for index, round := range input {
		clone[index] = round
		clone[index].LockProof = cloneSwissRoundLockProof(round.LockProof)
		clone[index].Series = make([]FinalSwissSeriesHead, len(round.Series))
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

func cloneFinalSwissSeriesHead(input FinalSwissSeriesHead) FinalSwissSeriesHead {
	clone := input
	clone.Series = cloneRevisionArenaSeries(input.Series)
	official, err := cloneOfficialResultProjectionInput(input.OfficialResult)
	if err == nil {
		clone.OfficialResult = official
	}
	clone.Projection = cloneFinalSwissDomainProjection(input.Projection)
	clone.Result = cloneSwissSeriesPointResult(input.Result)
	return clone
}

func cloneFinalSwissStandings(input []FinalSwissStanding) []FinalSwissStanding {
	clone := append([]FinalSwissStanding(nil), input...)
	for index := range clone {
		clone[index].AcceptedSolveTime = cloneDurationPointer(input[index].AcceptedSolveTime)
	}
	return clone
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
		clone[index].State.Members = append([]domain.ArenaGoldenMember(nil), group.State.Members...)
		clone[index].State.Attempts = append([]domain.ArenaGoldenAttempt(nil), group.State.Attempts...)
		clone[index].Revision = group.Revision.Snapshot()
		clone[index].Projection = cloneFinalSwissDomainProjection(group.Projection)
	}
	return clone
}

func cloneFinalSwissDomainProjection(input domain.ArenaProjectionRevision) domain.ArenaProjectionRevision {
	revision := input.Revision()
	clone, err := domain.NewArenaProjectionRevision(
		revision.ID(), revision.TournamentID(), revision.Artifact(), revision.RevisionNo(),
		revision.PreviousRevisionID(), revision.CreatedAt(), input.Payload(),
	)
	if err != nil {
		return domain.ArenaProjectionRevision{}
	}
	return clone
}

func finalSwissError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidFinalSwissProjection, fmt.Sprintf(format, arguments...))
}
