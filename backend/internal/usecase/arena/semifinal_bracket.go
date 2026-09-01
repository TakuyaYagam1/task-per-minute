package arena

import (
	"bytes"
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

const maxSemifinalBracketPayload = 32 << 10

var ErrInvalidSemifinalBracket = errors.New("invalid strength-matched semifinal bracket")

type SemifinalWinnerPath string

const SemifinalWinnerToFinal SemifinalWinnerPath = "final"

type SemifinalLoserPath string

const SemifinalLoserEliminated SemifinalLoserPath = "eliminated"

type SemifinalMatch struct {
	Position   int
	Series     domain.ArenaSeries
	WinnerPath SemifinalWinnerPath
	LoserPath  SemifinalLoserPath
}

type SemifinalBracketCommand struct {
	TournamentID uuid.UUID
	RevisionID   domain.ArenaDerivedRevisionID
	RevisionNo   int
	Previous     *SemifinalBracket
	Top4         Top4Snapshot
	SeriesIDs    [2]uuid.UUID
	CreatedAt    time.Time
}

type semifinalBracketAuthority struct {
	TournamentID uuid.UUID
	RevisionID   domain.ArenaDerivedRevisionID
	RevisionNo   int
	Previous     *semifinalBracketPredecessorReceipt
	Top4         Top4Snapshot
	SeriesIDs    [2]uuid.UUID
	CreatedAt    time.Time
}

type semifinalBracketPredecessorReceipt struct {
	Projection     domain.ArenaProjectionRevision
	Top4RevisionID domain.ArenaDerivedRevisionID
	Reserved       []uuid.UUID
	Semifinals     []SemifinalMatch
	LockedAt       time.Time
}

type semifinalBracketState struct {
	Authority    semifinalBracketAuthority
	Projection   domain.ArenaProjectionRevision
	Dependencies []domain.ArenaRevisionDependency
	Semifinals   []SemifinalMatch
	LockedAt     time.Time
}

type SemifinalBracket struct {
	state semifinalBracketState
}

// The persistence adapter must publish this plan with one CAS over the current
// Top 4 and predecessor bracket heads.
func PlanStrengthMatchedSemifinals(command SemifinalBracketCommand) (SemifinalBracket, error) {
	authority, err := canonicalSemifinalBracketAuthority(command)
	if err != nil {
		return SemifinalBracket{}, err
	}
	bracket, err := buildSemifinalBracket(authority)
	if err != nil {
		return SemifinalBracket{}, err
	}
	return bracket.Snapshot(), nil
}

func (b SemifinalBracket) Validate() error {
	if b.state.Authority.TournamentID == uuid.Nil {
		return semifinalBracketError("missing bracket state")
	}
	rebuilt, err := buildSemifinalBracket(cloneSemifinalBracketAuthority(b.state.Authority))
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(b.state, rebuilt.state) {
		return semifinalBracketError("bracket evidence changed")
	}
	return nil
}

func (b SemifinalBracket) Snapshot() SemifinalBracket {
	return SemifinalBracket{state: cloneSemifinalBracketState(b.state)}
}

func (b SemifinalBracket) Projection() domain.ArenaProjectionRevision {
	return cloneFinalSwissDomainProjection(b.state.Projection)
}

func (b SemifinalBracket) Dependencies() []domain.ArenaRevisionDependency {
	return append([]domain.ArenaRevisionDependency(nil), b.state.Dependencies...)
}

func (b SemifinalBracket) Semifinals() []SemifinalMatch {
	return cloneSemifinalMatches(b.state.Semifinals)
}

func (b SemifinalBracket) Locked() bool {
	return !b.state.LockedAt.IsZero()
}

func (b SemifinalBracket) LockedAt() time.Time {
	return b.state.LockedAt
}

func (b SemifinalBracket) HasLowerBracket() bool {
	return false
}

func canonicalSemifinalBracketAuthority(
	command SemifinalBracketCommand,
) (semifinalBracketAuthority, error) {
	if command.TournamentID == uuid.Nil || command.RevisionID.IsZero() || command.RevisionNo < 1 ||
		command.RevisionNo == math.MaxInt ||
		!validArenaServerTime(command.CreatedAt) || command.Top4.Validate() != nil {
		return semifinalBracketAuthority{}, semifinalBracketError("invalid bracket identity, clock, or Top 4 source")
	}
	top4Revision := command.Top4.Projection().Revision()
	if top4Revision.TournamentID() != command.TournamentID ||
		top4Revision.Artifact() != (domain.ArenaArtifactRef{
			Kind: domain.ArenaArtifactKindTopFour, EntityID: command.TournamentID,
		}) || command.CreatedAt.Before(top4Revision.CreatedAt()) {
		return semifinalBracketAuthority{}, semifinalBracketError("Top 4 source is stale or belongs to another tournament")
	}
	previous, err := validateSemifinalBracketPredecessor(command)
	if err != nil {
		return semifinalBracketAuthority{}, err
	}
	authority := semifinalBracketAuthority{
		TournamentID: command.TournamentID, RevisionID: command.RevisionID,
		RevisionNo: command.RevisionNo, Previous: previous, Top4: command.Top4.Snapshot(),
		SeriesIDs: command.SeriesIDs, CreatedAt: command.CreatedAt,
	}
	if err := validateSemifinalBracketIdentityRoles(authority); err != nil {
		return semifinalBracketAuthority{}, err
	}
	return authority, nil
}

func validateSemifinalBracketPredecessor(
	command SemifinalBracketCommand,
) (*semifinalBracketPredecessorReceipt, error) {
	if command.RevisionNo == 1 {
		if command.Previous != nil {
			return nil, semifinalBracketError("initial bracket revision has a predecessor")
		}
		return nil, nil
	}
	if command.Previous == nil || command.Previous.Validate() != nil {
		return nil, semifinalBracketError("bracket revision lacks a valid predecessor")
	}
	previous := command.Previous.Projection()
	revision := previous.Revision()
	if revision.TournamentID() != command.TournamentID ||
		revision.Artifact() != (domain.ArenaArtifactRef{
			Kind: domain.ArenaArtifactKindBracket, EntityID: command.TournamentID,
		}) || revision.RevisionNo()+1 != command.RevisionNo || command.CreatedAt.Before(revision.CreatedAt()) {
		return nil, semifinalBracketError("bracket predecessor is not the exact current head")
	}
	previousTop4 := command.Previous.state.Authority.Top4.Projection().Revision().ID()
	currentTop4 := command.Top4.Projection().Revision().ID()
	participants := command.Top4.Participants()
	if previousTop4 != currentTop4 || validateSemifinalParticipants(participants) != nil {
		return nil, semifinalBracketError("bracket successor must retain its exact Top 4 source")
	}
	expected := []SemifinalMatch{
		newSemifinalMatch(1, command.SeriesIDs[0], command.TournamentID, participants[0], participants[1]),
		newSemifinalMatch(2, command.SeriesIDs[1], command.TournamentID, participants[2], participants[3]),
	}
	previousMatches := command.Previous.Semifinals()
	if command.Previous.LockedAt().IsZero() || !reflect.DeepEqual(previousMatches, expected) {
		return nil, semifinalBracketError("bracket successor cannot replace locked semifinal topology")
	}
	return newSemifinalPredecessorReceipt(command, previous, previousTop4, previousMatches)
}

func newSemifinalPredecessorReceipt(
	command SemifinalBracketCommand,
	previous domain.ArenaProjectionRevision,
	previousTop4 domain.ArenaDerivedRevisionID,
	previousMatches []SemifinalMatch,
) (*semifinalBracketPredecessorReceipt, error) {
	reserved := make(map[uuid.UUID]struct{})
	if command.Previous.state.Authority.Previous != nil &&
		!mergeTask051ReservedIDs(reserved, command.Previous.state.Authority.Previous.Reserved) {
		return nil, semifinalBracketError("retained bracket identity lineage exceeds DAG bounds")
	}
	if !reserveTask051ID(reserved, previous.Revision().ID().UUID()) {
		return nil, semifinalBracketError("retained bracket identity lineage exceeds DAG bounds")
	}
	for _, match := range previousMatches {
		if !reserveTask051ID(reserved, match.Series.ID) {
			return nil, semifinalBracketError("retained bracket identity lineage exceeds DAG bounds")
		}
	}
	if err := addSemifinalTop4Reserved(reserved, command.Previous.state.Authority.Top4); err != nil {
		return nil, err
	}
	return &semifinalBracketPredecessorReceipt{
		Projection: cloneFinalSwissDomainProjection(previous), Top4RevisionID: previousTop4,
		Reserved: canonicalSemifinalReservedIDs(reserved), Semifinals: cloneSemifinalMatches(previousMatches),
		LockedAt: command.Previous.LockedAt(),
	}, nil
}

func buildSemifinalBracket(authority semifinalBracketAuthority) (SemifinalBracket, error) {
	participants := authority.Top4.Participants()
	if err := validateSemifinalParticipants(participants); err != nil {
		return SemifinalBracket{}, err
	}
	if err := validateSemifinalPredecessorReceipt(authority, participants); err != nil {
		return SemifinalBracket{}, err
	}
	lockedAt := authority.CreatedAt
	matches := []SemifinalMatch{
		newSemifinalMatch(1, authority.SeriesIDs[0], authority.TournamentID, participants[0], participants[1]),
		newSemifinalMatch(2, authority.SeriesIDs[1], authority.TournamentID, participants[2], participants[3]),
	}
	if authority.Previous != nil {
		matches = cloneSemifinalMatches(authority.Previous.Semifinals)
		lockedAt = authority.Previous.LockedAt
	}
	if err := validateSemifinalMatches(matches); err != nil {
		return SemifinalBracket{}, err
	}
	payload, err := semifinalBracketPayload(authority, matches, lockedAt)
	if err != nil || len(payload) == 0 || len(payload) > maxSemifinalBracketPayload {
		return SemifinalBracket{}, semifinalBracketError("encode bounded semifinal bracket payload")
	}
	var previousID *domain.ArenaDerivedRevisionID
	if authority.Previous != nil {
		value := authority.Previous.Projection.Revision().ID()
		previousID = &value
	}
	projection, err := domain.NewArenaProjectionRevision(
		authority.RevisionID, authority.TournamentID,
		domain.ArenaArtifactRef{Kind: domain.ArenaArtifactKindBracket, EntityID: authority.TournamentID},
		authority.RevisionNo, previousID, authority.CreatedAt, payload,
	)
	if err != nil {
		return SemifinalBracket{}, semifinalBracketError("build bracket revision: %v", err)
	}
	dependencies := []domain.ArenaRevisionDependency{{
		SourceRevisionID: authority.Top4.Projection().Revision().ID(), DerivedRevisionID: authority.RevisionID,
	}}
	if authority.Previous != nil {
		dependencies = append(dependencies, domain.ArenaRevisionDependency{
			SourceRevisionID:  authority.Previous.Projection.Revision().ID(),
			DerivedRevisionID: authority.RevisionID,
		})
	}
	return SemifinalBracket{state: semifinalBracketState{
		Authority: cloneSemifinalBracketAuthority(authority), Projection: projection,
		Dependencies: dependencies, Semifinals: cloneSemifinalMatches(matches), LockedAt: lockedAt,
	}}, nil
}

func validateSemifinalPredecessorReceipt(
	authority semifinalBracketAuthority,
	participants []Top4Participant,
) error {
	if authority.Previous == nil {
		return nil
	}
	previous := authority.Previous
	revision := previous.Projection.Revision()
	if previous.Projection.Validate() != nil || previous.LockedAt.IsZero() ||
		previous.Top4RevisionID != authority.Top4.Projection().Revision().ID() ||
		revision.TournamentID() != authority.TournamentID ||
		revision.Artifact() != (domain.ArenaArtifactRef{
			Kind: domain.ArenaArtifactKindBracket, EntityID: authority.TournamentID,
		}) || revision.RevisionNo()+1 != authority.RevisionNo || authority.CreatedAt.Before(revision.CreatedAt()) {
		return semifinalBracketError("invalid bounded bracket predecessor receipt")
	}
	expected := []SemifinalMatch{
		newSemifinalMatch(1, authority.SeriesIDs[0], authority.TournamentID, participants[0], participants[1]),
		newSemifinalMatch(2, authority.SeriesIDs[1], authority.TournamentID, participants[2], participants[3]),
	}
	if !reflect.DeepEqual(previous.Semifinals, expected) {
		return semifinalBracketError("bracket successor cannot replace locked semifinal topology")
	}
	if err := validateSemifinalReservedReceipt(previous, revision.ID().UUID()); err != nil {
		return err
	}
	return validateSemifinalMatches(previous.Semifinals)
}

func validateSemifinalReservedReceipt(
	previous *semifinalBracketPredecessorReceipt,
	revisionID uuid.UUID,
) error {
	if len(previous.Reserved) == 0 || len(previous.Reserved) > maxTask051ReservedIDs {
		return semifinalBracketError("bracket predecessor identity receipt exceeds DAG bounds")
	}
	foundRevision, foundFirst, foundSecond := false, false, false
	for index, id := range previous.Reserved {
		if id == uuid.Nil || (index > 0 && bytes.Compare(previous.Reserved[index-1][:], id[:]) >= 0) {
			return semifinalBracketError("non-canonical bracket predecessor identity receipt")
		}
		foundRevision = foundRevision || id == revisionID
		foundFirst = foundFirst || id == previous.Semifinals[0].Series.ID
		foundSecond = foundSecond || id == previous.Semifinals[1].Series.ID
	}
	if !foundRevision || !foundFirst || !foundSecond {
		return semifinalBracketError("incomplete bracket predecessor identity receipt")
	}
	return nil
}

func validateSemifinalParticipants(participants []Top4Participant) error {
	if len(participants) != finalSwissTop4Cutoff {
		return semifinalBracketError("Top 4 source does not contain exactly four participants")
	}
	for index, participant := range participants {
		if participant.Seed != index+1 || participant.ParticipantID == uuid.Nil {
			return semifinalBracketError("Top 4 source is not exactly ordered")
		}
	}
	return nil
}

func validateSemifinalMatches(matches []SemifinalMatch) error {
	for _, match := range matches {
		series := match.Series
		if series.Validate() != nil || series.State != domain.ArenaSeriesStateLocked ||
			series.Format != domain.ArenaSeriesFormatBO1 || len(series.Slots) != 0 ||
			series.WinnerID != nil || series.CurrentScoreRevisionID != nil ||
			series.CurrentResultRevisionID != nil {
			return semifinalBracketError("semifinal is not one locked unstarted BO1 Series")
		}
	}
	return nil
}

func newSemifinalMatch(
	position int,
	seriesID uuid.UUID,
	tournamentID uuid.UUID,
	first Top4Participant,
	second Top4Participant,
) SemifinalMatch {
	return SemifinalMatch{
		Position: position,
		Series: domain.ArenaSeries{
			ID: seriesID, TournamentID: tournamentID,
			FirstParticipantID: first.ParticipantID, SecondParticipantID: second.ParticipantID,
			Format: domain.ArenaSeriesFormatBO1, State: domain.ArenaSeriesStateLocked,
			Slots: []domain.ArenaGameSlot{},
		},
		WinnerPath: SemifinalWinnerToFinal, LoserPath: SemifinalLoserEliminated,
	}
}

type semifinalBracketPayloadDocument struct {
	TournamentID       uuid.UUID               `json:"tournament_id"`
	RevisionNo         int                     `json:"revision_no"`
	PreviousRevisionID *uuid.UUID              `json:"previous_revision_id,omitempty"`
	Top4RevisionID     uuid.UUID               `json:"top4_revision_id"`
	Top4Digest         string                  `json:"top4_digest"`
	Locked             bool                    `json:"locked"`
	LockedAt           time.Time               `json:"locked_at"`
	LowerBracket       bool                    `json:"lower_bracket"`
	Semifinals         []semifinalMatchPayload `json:"semifinals"`
}

type semifinalMatchPayload struct {
	Position            int                      `json:"position"`
	SeriesID            uuid.UUID                `json:"series_id"`
	FirstParticipantID  uuid.UUID                `json:"first_participant_id"`
	SecondParticipantID uuid.UUID                `json:"second_participant_id"`
	Format              domain.ArenaSeriesFormat `json:"format"`
	State               domain.ArenaSeriesState  `json:"state"`
	FirstWins           int                      `json:"first_wins"`
	SecondWins          int                      `json:"second_wins"`
	WinnerPath          SemifinalWinnerPath      `json:"winner_path"`
	LoserPath           SemifinalLoserPath       `json:"loser_path"`
}

func semifinalBracketPayload(
	authority semifinalBracketAuthority,
	matches []SemifinalMatch,
	lockedAt time.Time,
) ([]byte, error) {
	top4Revision := authority.Top4.Projection().Revision()
	digest := top4Revision.PayloadDigest()
	document := semifinalBracketPayloadDocument{
		TournamentID: authority.TournamentID, RevisionNo: authority.RevisionNo,
		Top4RevisionID: top4Revision.ID().UUID(), Top4Digest: hex.EncodeToString(digest[:]),
		Locked: true, LockedAt: lockedAt, LowerBracket: false,
		Semifinals: make([]semifinalMatchPayload, len(matches)),
	}
	for index, match := range matches {
		document.Semifinals[index] = semifinalMatchPayload{
			Position: match.Position, SeriesID: match.Series.ID,
			FirstParticipantID:  match.Series.FirstParticipantID,
			SecondParticipantID: match.Series.SecondParticipantID,
			Format:              match.Series.Format, State: match.Series.State,
			FirstWins:  match.Series.Score.FirstParticipantWins,
			SecondWins: match.Series.Score.SecondParticipantWins,
			WinnerPath: match.WinnerPath, LoserPath: match.LoserPath,
		}
	}
	if authority.Previous != nil {
		value := authority.Previous.Projection.Revision().ID().UUID()
		document.PreviousRevisionID = &value
	}
	return json.Marshal(document)
}

func validateSemifinalBracketIdentityRoles(authority semifinalBracketAuthority) error {
	if err := validateSemifinalFreshIDs(authority); err != nil {
		return err
	}
	roles := make(map[uuid.UUID]string)
	define := func(id uuid.UUID, role string) error {
		if id == uuid.Nil {
			return semifinalBracketError("missing %s identity", role)
		}
		if existing, duplicate := roles[id]; duplicate {
			return semifinalBracketError("identity aliases %s and %s", existing, role)
		}
		roles[id] = role
		return nil
	}
	if err := claimSemifinalBracketBase(define, authority); err != nil {
		return err
	}
	return claimSemifinalBracketPredecessor(define, authority.Previous)
}

func claimSemifinalBracketBase(
	define func(uuid.UUID, string) error,
	authority semifinalBracketAuthority,
) error {
	for _, identity := range []struct {
		id   uuid.UUID
		role string
	}{
		{id: authority.TournamentID, role: "tournament"},
		{id: authority.RevisionID.UUID(), role: "bracket revision"},
		{id: authority.Top4.Projection().Revision().ID().UUID(), role: "Top 4 revision"},
	} {
		if err := define(identity.id, identity.role); err != nil {
			return err
		}
	}
	for _, participant := range authority.Top4.Participants() {
		if err := define(participant.ParticipantID, "participant"); err != nil {
			return err
		}
	}
	if err := claimSemifinalTop4SourceIdentities(define, authority.Top4); err != nil {
		return err
	}
	for _, seriesID := range authority.SeriesIDs {
		if err := define(seriesID, "semifinal Series"); err != nil {
			return err
		}
	}
	return nil
}

func claimSemifinalBracketPredecessor(
	define func(uuid.UUID, string) error,
	previous *semifinalBracketPredecessorReceipt,
) error {
	if previous == nil {
		return nil
	}
	return define(previous.Projection.Revision().ID().UUID(), "previous bracket revision")
}

func validateSemifinalFreshIDs(authority semifinalBracketAuthority) error {
	reserved := make(map[uuid.UUID]struct{})
	if err := addSemifinalTop4Reserved(reserved, authority.Top4); err != nil {
		return err
	}
	if authority.Previous != nil {
		if !mergeTask051ReservedIDs(reserved, authority.Previous.Reserved) {
			return semifinalBracketError("bracket predecessor identity receipt exceeds DAG bounds")
		}
	}
	candidates := []uuid.UUID{authority.RevisionID.UUID()}
	if authority.Previous == nil {
		candidates = append(candidates, authority.SeriesIDs[0], authority.SeriesIDs[1])
	}
	seen := make(map[uuid.UUID]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, exists := reserved[candidate]; exists {
			return semifinalBracketError("new bracket identity aliases retained authority")
		}
		if _, exists := seen[candidate]; exists {
			return semifinalBracketError("new bracket identities alias each other")
		}
		seen[candidate] = struct{}{}
	}
	return nil
}

func addSemifinalTop4Reserved(reserved map[uuid.UUID]struct{}, snapshot Top4Snapshot) error {
	if snapshot.state.Authority.Previous != nil &&
		!mergeTask051ReservedIDs(reserved, snapshot.state.Authority.Previous.Reserved) {
		return semifinalBracketError("retained Top 4 identity lineage exceeds DAG bounds")
	}
	if !reserveTask051ID(reserved, snapshot.Projection().Revision().ID().UUID()) {
		return semifinalBracketError("retained Top 4 identity lineage exceeds DAG bounds")
	}
	if err := addTop4FinalSourceReserved(reserved, snapshot.state.Authority.Source); err != nil {
		return semifinalBracketError("invalid retained standings identity lineage")
	}
	for _, projection := range snapshot.QualificationProjections() {
		if !reserveTask051ID(reserved, projection.Revision().ID().UUID()) {
			return semifinalBracketError("retained Top 4 identity lineage exceeds DAG bounds")
		}
	}
	for _, settlement := range snapshot.state.Authority.GoldenSettlements {
		if !reserveTask051ID(reserved, settlement.RevisionID.UUID()) {
			return semifinalBracketError("retained Top 4 identity lineage exceeds DAG bounds")
		}
		if settlement.State != nil {
			if !addSemifinalGoldenStateReserved(reserved, *settlement.State) {
				return semifinalBracketError("retained Top 4 identity lineage exceeds DAG bounds")
			}
		}
	}
	return nil
}

func addSemifinalGoldenStateReserved(reserved map[uuid.UUID]struct{}, state GoldenState) bool {
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

func claimSemifinalTop4SourceIdentities(
	claim func(uuid.UUID, string) error,
	snapshot Top4Snapshot,
) error {
	source := snapshot.state.Authority.Source
	for _, identity := range []struct {
		id   uuid.UUID
		role string
	}{
		{id: source.state.Authority.ProjectionID, role: "standings projection"},
		{id: source.Projection().Revision().ID().UUID(), role: "standings revision"},
	} {
		if err := claim(identity.id, identity.role); err != nil {
			return err
		}
	}
	for _, round := range source.state.Authority.Rounds {
		if err := claimSemifinalSwissRoundIdentities(claim, round); err != nil {
			return err
		}
	}
	for _, group := range source.GoldenGroups() {
		if err := claim(group.State.ID, "Golden group"); err != nil {
			return err
		}
		if err := claim(group.State.RevisionID.UUID(), "Golden seed revision"); err != nil {
			return err
		}
	}
	for _, projection := range snapshot.QualificationProjections() {
		if err := claim(projection.Revision().ID().UUID(), "finalized Golden revision"); err != nil {
			return err
		}
	}
	for _, settlement := range snapshot.state.Authority.GoldenSettlements {
		if err := claimTop4SettlementIdentities(claim, settlement); err != nil {
			return err
		}
	}
	return nil
}

func claimSemifinalSwissRoundIdentities(
	claim func(uuid.UUID, string) error,
	round FinalSwissRound,
) error {
	for _, identity := range []struct {
		id   uuid.UUID
		role string
	}{
		{id: round.RoundID, role: "round"},
		{id: round.RevisionID, role: "round revision"},
	} {
		if err := claim(identity.id, identity.role); err != nil {
			return err
		}
	}
	for _, head := range round.Series {
		if err := claimSemifinalSwissHeadIdentities(claim, head); err != nil {
			return err
		}
	}
	if round.Bye != nil {
		return claim(round.Bye.RevisionID, "bye revision")
	}
	return nil
}

func claimSemifinalSwissHeadIdentities(
	claim func(uuid.UUID, string) error,
	head FinalSwissSeriesHead,
) error {
	if head.OfficialResult.NoGame != nil {
		return claimSemifinalNoGameHeadIdentities(claim, head)
	}
	return claimSemifinalOrdinaryHeadIdentities(claim, head)
}

func claimSemifinalOrdinaryHeadIdentities(
	claim func(uuid.UUID, string) error,
	head FinalSwissSeriesHead,
) error {
	identities := []struct {
		id   uuid.UUID
		role string
	}{
		{id: head.Series.ID, role: "Swiss Series"},
		{id: head.OfficialResult.Result.ID.UUID(), role: "official result revision"},
		{id: head.OfficialResult.Result.CommandID, role: "official result command"},
		{id: head.Projection.Revision().ID().UUID(), role: "Series result projection"},
	}
	if head.OfficialResult.Score != nil && head.OfficialResult.ScoreProjection != nil {
		identities = append(identities,
			struct {
				id   uuid.UUID
				role string
			}{id: head.OfficialResult.Score.ID.UUID(), role: "score revision"},
			struct {
				id   uuid.UUID
				role string
			}{id: head.OfficialResult.Score.CommandID, role: "score command"},
			struct {
				id   uuid.UUID
				role string
			}{id: head.OfficialResult.ScoreProjection.Revision().ID().UUID(), role: "score projection"},
		)
	}
	for _, identity := range identities {
		if err := claim(identity.id, identity.role); err != nil {
			return err
		}
	}
	if previous := head.OfficialResult.Result.PreviousRevisionID; previous != nil {
		if err := claim(previous.UUID(), "official result predecessor"); err != nil {
			return err
		}
	}
	if err := claimSemifinalOrdinaryScoreIdentities(claim, head); err != nil {
		return err
	}
	return claimSemifinalProjectionPredecessors(claim, head)
}

func claimSemifinalOrdinaryScoreIdentities(
	claim func(uuid.UUID, string) error,
	head FinalSwissSeriesHead,
) error {
	if head.OfficialResult.Score != nil {
		if previous := head.OfficialResult.Score.PreviousRevisionID; previous != nil {
			if err := claim(previous.UUID(), "score predecessor"); err != nil {
				return err
			}
		}
		for _, attempt := range head.OfficialResult.Score.Attempts {
			for _, identity := range []struct {
				id   uuid.UUID
				role string
			}{
				{id: attempt.SlotID, role: "Swiss Game slot"},
				{id: attempt.GameID, role: "Swiss Game"},
				{id: attempt.CurrentGameResultRevisionID.UUID(), role: "Game result revision"},
			} {
				if err := claim(identity.id, identity.role); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func claimSemifinalProjectionPredecessors(
	claim func(uuid.UUID, string) error,
	head FinalSwissSeriesHead,
) error {
	for _, projection := range []*domain.ArenaProjectionRevision{
		&head.Projection, head.OfficialResult.ScoreProjection,
	} {
		if projection == nil {
			continue
		}
		if previous := projection.Revision().PreviousRevisionID(); previous != nil {
			if err := claim(previous.UUID(), "projection predecessor"); err != nil {
				return err
			}
		}
	}
	return nil
}

func claimSemifinalNoGameHeadIdentities(
	claim func(uuid.UUID, string) error,
	head FinalSwissSeriesHead,
) error {
	recorded := head.OfficialResult.NoGame
	if recorded == nil {
		return semifinalBracketError("missing no-game terminal authority")
	}
	identities := []struct {
		id   uuid.UUID
		role string
	}{
		{id: head.Series.ID, role: "Swiss Series"},
		{id: recorded.Scope.WaveID, role: "Swiss no-game Wave"},
		{id: recorded.Scope.WindowID, role: "Swiss no-game window"},
		{id: recorded.CommandID, role: "Swiss no-game command"},
		{id: recorded.Score.ID.UUID(), role: "score revision"},
		{id: recorded.Series.ID.UUID(), role: "official result revision"},
		{id: recorded.ScoreProjection.Revision().ID().UUID(), role: "score projection"},
		{id: recorded.ResultProjection.Revision().ID().UUID(), role: "Series result projection"},
	}
	for _, identity := range identities {
		if err := claim(identity.id, identity.role); err != nil {
			return err
		}
	}
	if err := claimSemifinalNoGameAttemptIdentities(claim, *recorded); err != nil {
		return err
	}
	return claimSemifinalNoGamePredecessors(claim, *recorded)
}

func claimSemifinalNoGameAttemptIdentities(
	claim func(uuid.UUID, string) error,
	recorded RecordedNoGameResult,
) error {
	for index := range recorded.Topology {
		for _, identity := range []struct {
			id   uuid.UUID
			role string
		}{
			{id: recorded.Topology[index].SlotID, role: "Swiss Game slot"},
			{id: recorded.Topology[index].GameID, role: "Swiss Game"},
			{id: recorded.GameResults[index].ID.UUID(), role: "Game result revision"},
			{id: recorded.GameProjections[index].Revision().ID().UUID(), role: "Game result projection"},
		} {
			if err := claim(identity.id, identity.role); err != nil {
				return err
			}
		}
		if previous := recorded.GameResults[index].PreviousRevisionID; previous != nil {
			if err := claim(previous.UUID(), "Game result predecessor"); err != nil {
				return err
			}
		}
		if previous := recorded.GameProjections[index].Revision().PreviousRevisionID(); previous != nil {
			if err := claim(previous.UUID(), "projection predecessor"); err != nil {
				return err
			}
		}
	}
	return nil
}

func claimSemifinalNoGamePredecessors(
	claim func(uuid.UUID, string) error,
	recorded RecordedNoGameResult,
) error {
	for _, previous := range []uuid.UUID{
		finalSwissScorePredecessor(recorded.Score), finalSwissResultPredecessor(recorded.Series),
	} {
		if previous != uuid.Nil {
			if err := claim(previous, "terminal predecessor"); err != nil {
				return err
			}
		}
	}
	for _, projection := range []domain.ArenaProjectionRevision{
		recorded.ScoreProjection, recorded.ResultProjection,
	} {
		if previous := projection.Revision().PreviousRevisionID(); previous != nil {
			if err := claim(previous.UUID(), "projection predecessor"); err != nil {
				return err
			}
		}
	}
	return nil
}

func finalSwissScorePredecessor(score NormalNoShowScoreRevision) uuid.UUID {
	if score.PreviousRevisionID == nil {
		return uuid.Nil
	}
	return score.PreviousRevisionID.UUID()
}

func finalSwissResultPredecessor(result NormalNoShowSeriesRevision) uuid.UUID {
	if result.PreviousRevisionID == nil {
		return uuid.Nil
	}
	return result.PreviousRevisionID.UUID()
}

func cloneSemifinalBracketAuthority(input semifinalBracketAuthority) semifinalBracketAuthority {
	clone := input
	if input.Previous != nil {
		clone.Previous = cloneSemifinalPredecessorReceipt(input.Previous)
	}
	clone.Top4 = input.Top4.Snapshot()
	return clone
}

func canonicalSemifinalReservedIDs(input map[uuid.UUID]struct{}) []uuid.UUID {
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

func cloneSemifinalPredecessorReceipt(
	input *semifinalBracketPredecessorReceipt,
) *semifinalBracketPredecessorReceipt {
	if input == nil {
		return nil
	}
	if len(input.Reserved) > maxTask051ReservedIDs {
		return &semifinalBracketPredecessorReceipt{
			Projection: cloneFinalSwissDomainProjection(input.Projection), Top4RevisionID: input.Top4RevisionID,
			Semifinals: cloneSemifinalMatches(input.Semifinals), LockedAt: input.LockedAt,
		}
	}
	return &semifinalBracketPredecessorReceipt{
		Projection: cloneFinalSwissDomainProjection(input.Projection), Top4RevisionID: input.Top4RevisionID,
		Reserved:   append([]uuid.UUID(nil), input.Reserved...),
		Semifinals: cloneSemifinalMatches(input.Semifinals), LockedAt: input.LockedAt,
	}
}

func cloneSemifinalBracketState(input semifinalBracketState) semifinalBracketState {
	clone := input
	clone.Authority = cloneSemifinalBracketAuthority(input.Authority)
	clone.Projection = cloneFinalSwissDomainProjection(input.Projection)
	clone.Dependencies = append([]domain.ArenaRevisionDependency(nil), input.Dependencies...)
	clone.Semifinals = cloneSemifinalMatches(input.Semifinals)
	return clone
}

func cloneSemifinalMatches(input []SemifinalMatch) []SemifinalMatch {
	clone := make([]SemifinalMatch, len(input))
	for index, match := range input {
		clone[index] = match
		clone[index].Series = cloneRevisionArenaSeries(match.Series)
	}
	return clone
}

func semifinalBracketError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidSemifinalBracket, fmt.Sprintf(format, arguments...))
}
