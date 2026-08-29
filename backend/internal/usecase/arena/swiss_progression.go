package arena

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidSwissRoundProgression = errors.New("invalid Swiss round progression")
	ErrSwissRoundIncomplete         = errors.New("swiss round is not officially complete")
	ErrSwissRoundNotEditable        = errors.New("swiss round is not editable")
	ErrSwissRoundsComplete          = errors.New("swiss rounds are complete")
)

type SwissProgressionRound struct {
	RoundID     uuid.UUID
	RoundNumber int
	RevisionID  uuid.UUID
	Series      []SwissSeriesPointResult
	Bye         *SwissByePointResult
}

type SwissRoundProgressionCommand struct {
	TournamentID          uuid.UUID
	RosterID              uuid.UUID
	RoundID               uuid.UUID
	Preset                domain.ArenaPreset
	RosterParticipantIDs  []uuid.UUID
	Seeds                 []SwissParticipantSeed
	HistoryRevision       int64
	History               []SwissProgressionRound
	ExpectedRoundRevision int64
	PairingEvidenceID     uuid.UUID
	ByeEvidenceID         uuid.UUID
	GeneratedAt           time.Time
}

type SwissRoundProgressionPlan struct {
	TournamentID            uuid.UUID               `json:"tournament_id"`
	RosterID                uuid.UUID               `json:"roster_id"`
	RoundID                 uuid.UUID               `json:"round_id"`
	RoundNumber             int                     `json:"round_number"`
	Preset                  domain.ArenaPreset      `json:"preset"`
	RosterParticipantIDs    []uuid.UUID             `json:"roster_participant_ids"`
	Seeds                   []SwissParticipantSeed  `json:"seeds"`
	HistoryRevision         int64                   `json:"history_revision"`
	History                 []SwissProgressionRound `json:"history"`
	SourceRoundRevisionIDs  []uuid.UUID             `json:"source_round_revision_ids"`
	SourceResultRevisionIDs []uuid.UUID             `json:"source_result_revision_ids"`
	SourceByeRevisionIDs    []uuid.UUID             `json:"source_bye_revision_ids"`
	ExpectedRoundRevision   int64                   `json:"expected_round_revision"`
	GeneratedAt             time.Time               `json:"generated_at"`
	Standings               []SwissNormalStanding   `json:"standings"`
	Pairing                 AutomaticSwissPairing   `json:"pairing"`
	Bye                     *SwissByeSelection      `json:"bye"`
	ProofHash               string                  `json:"proof_hash"`
}

type SwissRoundDraft struct {
	Plan      SwissRoundProgressionPlan
	Revision  int64
	LockedAt  *time.Time
	StartedAt *time.Time
}

type SwissRoundProgressionInput struct {
	Plan SwissRoundProgressionPlan
}

// SwissRoundProgressionRepository owns the progression transaction. It must
// compare the history revision and every round, result, and bye revision in
// the plan with current official heads. It may create or replace only the
// named unlocked and unstarted round through ExpectedRoundRevision CAS.
type SwissRoundProgressionRepository interface {
	SaveSwissRoundDraft(
		ctx context.Context,
		input SwissRoundProgressionInput,
	) (*SwissRoundDraft, bool, error)
}

type SwissRoundProgressionUseCase struct {
	repository SwissRoundProgressionRepository
}

type swissProgressionHistory struct {
	ledger            SwissPointLedger
	standings         []SwissNormalStanding
	previousMeetings  []SwissPair
	receivedByes      map[uuid.UUID]struct{}
	roundRevisionIDs  []uuid.UUID
	resultRevisionIDs []uuid.UUID
	byeRevisionIDs    []uuid.UUID
}

func NewSwissRoundProgressionUseCase(
	repository SwissRoundProgressionRepository,
) *SwissRoundProgressionUseCase {
	return &SwissRoundProgressionUseCase{repository: repository}
}

func PrepareSwissRoundProgression(
	command SwissRoundProgressionCommand,
) (SwissRoundProgressionPlan, error) {
	canonical, roundCount, err := canonicalSwissProgressionCommand(command)
	if err != nil {
		return SwissRoundProgressionPlan{}, err
	}
	history, err := deriveSwissProgressionHistory(
		canonical.RosterParticipantIDs,
		canonical.Seeds,
		canonical.History,
		len(canonical.History) == roundCount,
	)
	if err != nil {
		return SwissRoundProgressionPlan{}, err
	}
	if len(canonical.History) == roundCount {
		return SwissRoundProgressionPlan{}, ErrSwissRoundsComplete
	}

	bye, eligible, err := prepareSwissProgressionBye(canonical, history)
	if err != nil {
		return SwissRoundProgressionPlan{}, err
	}
	meetings := filterSwissProgressionMeetings(history.previousMeetings, eligible)
	pairing, err := GenerateAutomaticSwissPairing(
		canonical.PairingEvidenceID,
		canonical.RoundID,
		eligible,
		meetings,
		canonical.GeneratedAt,
	)
	if err != nil {
		return SwissRoundProgressionPlan{}, fmt.Errorf("%w: pairing: %w", ErrInvalidSwissRoundProgression, err)
	}

	plan := SwissRoundProgressionPlan{
		TournamentID: canonical.TournamentID, RosterID: canonical.RosterID,
		RoundID: canonical.RoundID, RoundNumber: len(canonical.History) + 1, Preset: canonical.Preset,
		RosterParticipantIDs:    append([]uuid.UUID(nil), canonical.RosterParticipantIDs...),
		Seeds:                   append([]SwissParticipantSeed(nil), canonical.Seeds...),
		HistoryRevision:         canonical.HistoryRevision,
		History:                 cloneSwissProgressionRounds(canonical.History),
		SourceRoundRevisionIDs:  append([]uuid.UUID(nil), history.roundRevisionIDs...),
		SourceResultRevisionIDs: append([]uuid.UUID(nil), history.resultRevisionIDs...),
		SourceByeRevisionIDs:    append([]uuid.UUID(nil), history.byeRevisionIDs...),
		ExpectedRoundRevision:   canonical.ExpectedRoundRevision,
		GeneratedAt:             canonical.GeneratedAt,
		Standings:               cloneSwissNormalStandings(history.standings),
		Pairing:                 cloneAutomaticSwissPairing(pairing),
		Bye:                     cloneSwissByeSelection(bye),
	}
	plan.ProofHash, err = swissRoundProgressionProofHash(plan)
	if err != nil {
		return SwissRoundProgressionPlan{}, err
	}
	return plan, nil
}

func (p SwissRoundProgressionPlan) Validate() error {
	canonical, err := canonicalSwissProgressionPlanCommand(p)
	if err != nil {
		return err
	}
	history, err := deriveSwissProgressionHistory(
		canonical.RosterParticipantIDs,
		canonical.Seeds,
		canonical.History,
		false,
	)
	if err != nil {
		return err
	}
	return validateSwissProgressionPlanEvidence(p, history)
}

func canonicalSwissProgressionPlanCommand(
	plan SwissRoundProgressionPlan,
) (SwissRoundProgressionCommand, error) {
	command := SwissRoundProgressionCommand{
		TournamentID: plan.TournamentID, RosterID: plan.RosterID, RoundID: plan.RoundID, Preset: plan.Preset,
		RosterParticipantIDs: plan.RosterParticipantIDs, Seeds: plan.Seeds,
		HistoryRevision: plan.HistoryRevision, History: plan.History,
		ExpectedRoundRevision: plan.ExpectedRoundRevision,
		PairingEvidenceID:     plan.Pairing.Evidence.ID, GeneratedAt: plan.GeneratedAt,
	}
	if plan.Bye != nil {
		command.ByeEvidenceID = plan.Bye.Evidence.ID
	}
	canonical, roundCount, err := canonicalSwissProgressionCommand(command)
	if err != nil || len(canonical.History) >= roundCount || plan.RoundNumber != len(canonical.History)+1 {
		return SwissRoundProgressionCommand{}, swissRoundProgressionError("round identity or history length is invalid")
	}
	if !swissProgressionCommandMatchesPlan(canonical, plan) {
		return SwissRoundProgressionCommand{}, swissRoundProgressionError("plan inputs are not canonical")
	}
	return canonical, nil
}

func validateSwissProgressionPlanEvidence(
	plan SwissRoundProgressionPlan,
	history swissProgressionHistory,
) error {
	if !slices.Equal(plan.SourceRoundRevisionIDs, history.roundRevisionIDs) ||
		!slices.Equal(plan.SourceResultRevisionIDs, history.resultRevisionIDs) ||
		!slices.Equal(plan.SourceByeRevisionIDs, history.byeRevisionIDs) ||
		!reflect.DeepEqual(plan.Standings, history.standings) {
		return swissRoundProgressionError("derived history evidence does not match")
	}
	bye, eligible, err := validateSwissProgressionBye(plan, history)
	if err != nil {
		return err
	}
	if !equalOptionalSwissBye(bye, plan.Bye) {
		return swissRoundProgressionError("bye decision does not match current standings")
	}
	if err := validateSwissProgressionPairing(plan, eligible, history.previousMeetings); err != nil {
		return err
	}
	wantHash, err := swissRoundProgressionProofHash(plan)
	if err != nil || !validCapacityDigest(plan.ProofHash) || plan.ProofHash != wantHash {
		return swissRoundProgressionError("proof hash does not match the official history")
	}
	return nil
}

func (u *SwissRoundProgressionUseCase) Apply(
	ctx context.Context,
	plan SwissRoundProgressionPlan,
) (*SwissRoundDraft, bool, error) {
	if u == nil || u.repository == nil {
		return nil, false, domain.ErrValidation
	}
	if err := plan.Validate(); err != nil {
		return nil, false, err
	}
	record, changed, err := u.repository.SaveSwissRoundDraft(ctx, SwissRoundProgressionInput{
		Plan: cloneSwissRoundProgressionPlan(plan),
	})
	if err != nil {
		return nil, false, swissRoundProgressionMutationError(err)
	}
	if !changed {
		return reconcileSwissRoundProgression(record, plan)
	}
	if !validChangedSwissRoundDraft(record, plan) {
		return nil, false, domain.ErrInternal
	}
	return cloneSwissRoundDraft(record), true, nil
}

func canonicalSwissProgressionCommand(
	command SwissRoundProgressionCommand,
) (SwissRoundProgressionCommand, int, error) {
	if err := validateSwissProgressionCommandBase(command); err != nil {
		return SwissRoundProgressionCommand{}, 0, err
	}
	participants, err := canonicalSwissLedgerParticipants(command.RosterParticipantIDs)
	if err != nil {
		return SwissRoundProgressionCommand{}, 0, swissRoundProgressionError("roster is invalid")
	}
	roundCount, err := command.Preset.SwissRounds(len(participants))
	if err != nil || len(command.History) > roundCount {
		return SwissRoundProgressionCommand{}, 0, swissRoundProgressionError("history exceeds the preset plan")
	}
	if err := validateSwissProgressionDecisionIDs(command, len(participants)); err != nil {
		return SwissRoundProgressionCommand{}, 0, err
	}
	if _, err := swissSeedMap(participants, command.Seeds); err != nil {
		return SwissRoundProgressionCommand{}, 0, err
	}
	canonical := command
	canonical.RosterParticipantIDs = participants
	canonical.Seeds = append([]SwissParticipantSeed(nil), command.Seeds...)
	slices.SortFunc(canonical.Seeds, func(first, second SwissParticipantSeed) int {
		return compareUUID(first.ParticipantID, second.ParticipantID)
	})
	canonical.History = cloneSwissProgressionRounds(command.History)
	canonicalizeSwissProgressionRounds(canonical.History)
	canonical.GeneratedAt = command.GeneratedAt.Round(0).UTC()
	if err := validateSwissProgressionUniqueIDs(canonical); err != nil {
		return SwissRoundProgressionCommand{}, 0, err
	}
	return canonical, roundCount, nil
}

func validateSwissProgressionCommandBase(command SwissRoundProgressionCommand) error {
	if command.TournamentID == uuid.Nil || command.RosterID == uuid.Nil || command.RoundID == uuid.Nil ||
		command.PairingEvidenceID == uuid.Nil || command.HistoryRevision < 1 ||
		command.ExpectedRoundRevision < 0 || !validArenaServerTime(command.GeneratedAt) {
		return swissRoundProgressionError("missing progression identity")
	}
	if command.TournamentID == command.RosterID || command.TournamentID == command.RoundID ||
		command.RosterID == command.RoundID || command.PairingEvidenceID == command.RoundID {
		return swissRoundProgressionError("progression identities must differ")
	}
	return nil
}

func validateSwissProgressionDecisionIDs(command SwissRoundProgressionCommand, rosterSize int) error {
	if rosterSize%2 == 0 {
		if command.ByeEvidenceID != uuid.Nil {
			return swissRoundProgressionError("even roster has unused bye evidence")
		}
		return nil
	}
	if command.ByeEvidenceID == uuid.Nil || command.ByeEvidenceID == command.PairingEvidenceID ||
		command.ByeEvidenceID == command.RoundID {
		return swissRoundProgressionError("bye decision identity is invalid")
	}
	return nil
}

func validateSwissProgressionUniqueIDs(command SwissRoundProgressionCommand) error {
	seen := make(map[uuid.UUID]struct{})
	add := func(id uuid.UUID) bool {
		if id == uuid.Nil {
			return true
		}
		if _, duplicate := seen[id]; duplicate {
			return false
		}
		seen[id] = struct{}{}
		return true
	}
	for _, id := range []uuid.UUID{
		command.TournamentID, command.RosterID, command.RoundID,
		command.PairingEvidenceID,
	} {
		if !add(id) {
			return swissRoundProgressionError("progression identity is reused")
		}
	}
	if command.ByeEvidenceID != uuid.Nil && !add(command.ByeEvidenceID) {
		return swissRoundProgressionError("bye decision identity is reused")
	}
	for _, round := range command.History {
		if !add(round.RoundID) || !add(round.RevisionID) {
			return swissRoundProgressionError("round identity is reused")
		}
		for _, series := range round.Series {
			if !add(series.SeriesID) || !add(series.ResultRevisionID.UUID()) {
				return swissRoundProgressionError("Series identity is reused")
			}
		}
		if round.Bye != nil && !add(round.Bye.RevisionID) {
			return swissRoundProgressionError("bye revision identity is reused")
		}
	}
	return nil
}

func deriveSwissProgressionHistory(
	participants []uuid.UUID,
	seeds []SwissParticipantSeed,
	rounds []SwissProgressionRound,
	swissComplete bool,
) (swissProgressionHistory, error) {
	series := make([]SwissSeriesPointResult, 0)
	byes := make([]SwissByePointResult, 0)
	result := swissProgressionHistory{
		previousMeetings: make([]SwissPair, 0),
		receivedByes:     make(map[uuid.UUID]struct{}),
		roundRevisionIDs: make([]uuid.UUID, 0, len(rounds)),
		byeRevisionIDs:   make([]uuid.UUID, 0, len(rounds)),
	}
	seenRoundIDs := make(map[uuid.UUID]struct{}, len(rounds))
	seenRoundRevisions := make(map[uuid.UUID]struct{}, len(rounds))
	for index, round := range rounds {
		if err := validateSwissProgressionRound(participants, round, index+1); err != nil {
			return swissProgressionHistory{}, err
		}
		if _, duplicate := seenRoundIDs[round.RoundID]; duplicate {
			return swissProgressionHistory{}, swissRoundIncompleteError("round identity is duplicated")
		}
		if _, duplicate := seenRoundRevisions[round.RevisionID]; duplicate {
			return swissProgressionHistory{}, swissRoundIncompleteError("round revision is duplicated")
		}
		seenRoundIDs[round.RoundID] = struct{}{}
		seenRoundRevisions[round.RevisionID] = struct{}{}
		result.roundRevisionIDs = append(result.roundRevisionIDs, round.RevisionID)
		for _, official := range round.Series {
			series = append(series, cloneSwissSeriesPointResult(official))
			result.resultRevisionIDs = append(result.resultRevisionIDs, official.ResultRevisionID.UUID())
			result.previousMeetings = append(result.previousMeetings, SwissPair{
				FirstParticipantID:  official.FirstParticipantID,
				SecondParticipantID: official.SecondParticipantID,
			})
		}
		if round.Bye != nil {
			byes = append(byes, *round.Bye)
			result.byeRevisionIDs = append(result.byeRevisionIDs, round.Bye.RevisionID)
			result.receivedByes[round.Bye.ParticipantID] = struct{}{}
		}
	}
	ledger, err := BuildSwissPointLedger(SwissPointLedgerInput{
		ParticipantIDs: participants,
		Series:         series,
		Byes:           byes,
	})
	if err != nil {
		return swissProgressionHistory{}, swissRoundIncompleteError(err.Error())
	}
	standings, err := OrderSwissNormalStandings(SwissNormalOrderingInput{
		Ledger: ledger, Seeds: seeds, SwissComplete: swissComplete,
	})
	if err != nil {
		return swissProgressionHistory{}, swissRoundIncompleteError(err.Error())
	}
	result.ledger = ledger
	result.standings = standings
	return result, nil
}

func validateSwissProgressionRound(
	participants []uuid.UUID,
	round SwissProgressionRound,
	wantNumber int,
) error {
	if round.RoundID == uuid.Nil || round.RevisionID == uuid.Nil || round.RoundNumber != wantNumber ||
		len(round.Series) != len(participants)/2 {
		return swissRoundIncompleteError("round identity or Series count is incomplete")
	}
	roster := make(map[uuid.UUID]struct{}, len(participants))
	for _, participantID := range participants {
		roster[participantID] = struct{}{}
	}
	used := make(map[uuid.UUID]struct{}, len(participants))
	for _, official := range round.Series {
		if official.RoundID != round.RoundID || official.RoundNumber != round.RoundNumber ||
			official.ResultRevisionID.IsZero() {
			return swissRoundIncompleteError("Series has no current official revision")
		}
		for _, participantID := range []uuid.UUID{official.FirstParticipantID, official.SecondParticipantID} {
			if _, exists := roster[participantID]; !exists {
				return swissRoundIncompleteError("Series contains a foreign participant")
			}
			if _, duplicate := used[participantID]; duplicate {
				return swissRoundIncompleteError("participant appears more than once")
			}
			used[participantID] = struct{}{}
		}
	}
	if err := validateSwissProgressionRoundBye(participants, round, roster, used); err != nil {
		return err
	}
	if len(used) != len(roster) {
		return swissRoundIncompleteError("round membership does not cover the roster")
	}
	return nil
}

func validateSwissProgressionRoundBye(
	participants []uuid.UUID,
	round SwissProgressionRound,
	roster map[uuid.UUID]struct{},
	used map[uuid.UUID]struct{},
) error {
	if len(participants)%2 == 0 {
		if round.Bye != nil {
			return swissRoundIncompleteError("even roster has a bye")
		}
		return nil
	}
	if round.Bye == nil || round.Bye.RoundID != round.RoundID ||
		round.Bye.RoundNumber != round.RoundNumber || round.Bye.RevisionID == uuid.Nil {
		return swissRoundIncompleteError("odd roster has no current official bye")
	}
	if _, exists := roster[round.Bye.ParticipantID]; !exists {
		return swissRoundIncompleteError("bye contains a foreign participant")
	}
	if _, duplicate := used[round.Bye.ParticipantID]; duplicate {
		return swissRoundIncompleteError("bye participant also appears in a Series")
	}
	used[round.Bye.ParticipantID] = struct{}{}
	return nil
}

func prepareSwissProgressionBye(
	command SwissRoundProgressionCommand,
	history swissProgressionHistory,
) (*SwissByeSelection, []uuid.UUID, error) {
	eligible := append([]uuid.UUID(nil), command.RosterParticipantIDs...)
	if len(eligible)%2 == 0 {
		return nil, eligible, nil
	}
	candidates := swissProgressionByeCandidates(history.standings, history.receivedByes)
	selection, err := SelectSwissBye(
		command.ByeEvidenceID,
		command.RoundID,
		candidates,
		command.GeneratedAt,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: bye: %w", ErrInvalidSwissRoundProgression, err)
	}
	return &selection, removeSwissProgressionParticipant(eligible, selection.ParticipantID), nil
}

func validateSwissProgressionBye(
	plan SwissRoundProgressionPlan,
	history swissProgressionHistory,
) (*SwissByeSelection, []uuid.UUID, error) {
	eligible := append([]uuid.UUID(nil), plan.RosterParticipantIDs...)
	if len(eligible)%2 == 0 {
		if plan.Bye != nil {
			return nil, nil, swissRoundProgressionError("even roster has a bye decision")
		}
		return nil, eligible, nil
	}
	if plan.Bye == nil || plan.Bye.Evidence.OwnerID != plan.RoundID ||
		!plan.Bye.Evidence.DecidedAt.Equal(plan.GeneratedAt) {
		return nil, nil, swissRoundProgressionError("bye decision metadata does not match the round")
	}
	candidates := swissProgressionByeCandidates(history.standings, history.receivedByes)
	wantInputs, err := swissByeInputs(candidates)
	if err != nil {
		return nil, nil, err
	}
	wantInputs, err = domain.NormalizeArenaDecisionInputs(wantInputs)
	if err != nil || !slices.Equal(wantInputs, plan.Bye.Evidence.NormalizedInputs) {
		return nil, nil, swissRoundProgressionError("bye decision inputs do not match current standings")
	}
	replayed, err := ReplaySwissBye(*plan.Bye)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: bye replay: %w", ErrInvalidSwissRoundProgression, err)
	}
	return &replayed, removeSwissProgressionParticipant(eligible, replayed.ParticipantID), nil
}

func swissProgressionByeCandidates(
	standings []SwissNormalStanding,
	received map[uuid.UUID]struct{},
) []SwissByeCandidate {
	candidates := make([]SwissByeCandidate, len(standings))
	for index, standing := range standings {
		_, hadBye := received[standing.ParticipantID]
		candidates[index] = SwissByeCandidate{
			ParticipantID: standing.ParticipantID, Points: standing.Points,
			ProvisionalBuchholz: standing.Buchholz,
			HeadToHeadPoints:    standing.HeadToHeadPoints, HeadToHeadApplicable: standing.HeadToHeadApplied,
			EffectiveTime: standing.EffectiveTime, ReceivedBye: hadBye,
		}
	}
	return candidates
}

func removeSwissProgressionParticipant(values []uuid.UUID, removed uuid.UUID) []uuid.UUID {
	filtered := make([]uuid.UUID, 0, len(values)-1)
	for _, value := range values {
		if value != removed {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func filterSwissProgressionMeetings(meetings []SwissPair, eligible []uuid.UUID) []SwissPair {
	allowed := make(map[uuid.UUID]struct{}, len(eligible))
	for _, participantID := range eligible {
		allowed[participantID] = struct{}{}
	}
	filtered := make([]SwissPair, 0, len(meetings))
	for _, meeting := range meetings {
		if _, firstAllowed := allowed[meeting.FirstParticipantID]; !firstAllowed {
			continue
		}
		if _, secondAllowed := allowed[meeting.SecondParticipantID]; !secondAllowed {
			continue
		}
		filtered = append(filtered, meeting)
	}
	return filtered
}

func validateSwissProgressionPairing(
	plan SwissRoundProgressionPlan,
	eligible []uuid.UUID,
	meetings []SwissPair,
) error {
	filteredMeetings := filterSwissProgressionMeetings(meetings, eligible)
	wantInputs, err := automaticPairingInputs(eligible, filteredMeetings)
	if err != nil {
		return err
	}
	wantInputs, err = domain.NormalizeArenaDecisionInputs(wantInputs)
	if err != nil || plan.Pairing.Evidence.OwnerID != plan.RoundID ||
		!plan.Pairing.Evidence.DecidedAt.Equal(plan.GeneratedAt) ||
		!slices.Equal(wantInputs, plan.Pairing.Evidence.NormalizedInputs) {
		return swissRoundProgressionError("pairing evidence does not match current history")
	}
	if _, err := ReplayAutomaticSwissPairing(plan.Pairing); err != nil {
		return fmt.Errorf("%w: pairing replay: %w", ErrInvalidSwissRoundProgression, err)
	}
	return nil
}

func swissProgressionCommandMatchesPlan(
	command SwissRoundProgressionCommand,
	plan SwissRoundProgressionPlan,
) bool {
	return command.TournamentID == plan.TournamentID && command.RosterID == plan.RosterID &&
		command.RoundID == plan.RoundID && command.Preset == plan.Preset &&
		command.HistoryRevision == plan.HistoryRevision &&
		command.ExpectedRoundRevision == plan.ExpectedRoundRevision &&
		command.GeneratedAt.Equal(plan.GeneratedAt) &&
		slices.Equal(command.RosterParticipantIDs, plan.RosterParticipantIDs) &&
		reflect.DeepEqual(command.Seeds, plan.Seeds) && reflect.DeepEqual(command.History, plan.History)
}

func validChangedSwissRoundDraft(
	record *SwissRoundDraft,
	plan SwissRoundProgressionPlan,
) bool {
	return record != nil && record.Revision == plan.ExpectedRoundRevision+1 &&
		record.LockedAt == nil && record.StartedAt == nil &&
		record.Plan.ProofHash == plan.ProofHash && record.Plan.Validate() == nil
}

func reconcileSwissRoundProgression(
	record *SwissRoundDraft,
	plan SwissRoundProgressionPlan,
) (*SwissRoundDraft, bool, error) {
	if record == nil {
		return nil, false, domain.ErrConflict
	}
	if record.Plan.RoundID != plan.RoundID || record.Revision < 1 {
		return nil, false, domain.ErrInternal
	}
	if record.LockedAt != nil || record.StartedAt != nil {
		return nil, false, ErrSwissRoundNotEditable
	}
	if record.Plan.ProofHash == plan.ProofHash && record.Plan.Validate() == nil {
		return cloneSwissRoundDraft(record), false, nil
	}
	return nil, false, domain.ErrConflict
}

func swissRoundProgressionMutationError(err error) error {
	if errors.Is(err, domain.ErrConflict) || errors.Is(err, ErrSwissRoundNotEditable) {
		return err
	}
	return fmt.Errorf("SwissRoundProgressionUseCase - SaveSwissRoundDraft: %w", err)
}

func swissRoundProgressionProofHash(plan SwissRoundProgressionPlan) (string, error) {
	plan.ProofHash = ""
	document := map[string]any{
		"tournament_id":              plan.TournamentID,
		"roster_id":                  plan.RosterID,
		"round_id":                   plan.RoundID,
		"round_number":               plan.RoundNumber,
		"preset":                     plan.Preset,
		"roster_participant_ids":     plan.RosterParticipantIDs,
		"seeds":                      plan.Seeds,
		"history_revision":           plan.HistoryRevision,
		"history":                    plan.History,
		"source_round_revision_ids":  plan.SourceRoundRevisionIDs,
		"source_result_revision_ids": plan.SourceResultRevisionIDs,
		"source_bye_revision_ids":    plan.SourceByeRevisionIDs,
		"expected_round_revision":    plan.ExpectedRoundRevision,
		"generated_at":               plan.GeneratedAt,
		"standings":                  plan.Standings,
		"pairing":                    plan.Pairing,
		"bye":                        plan.Bye,
		"proof_hash":                 plan.ProofHash,
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return "", fmt.Errorf("%w: encode proof: %w", ErrInvalidSwissRoundProgression, err)
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func canonicalizeSwissProgressionRounds(rounds []SwissProgressionRound) {
	sort.Slice(rounds, func(i, j int) bool { return rounds[i].RoundNumber < rounds[j].RoundNumber })
	for index := range rounds {
		sort.Slice(rounds[index].Series, func(i, j int) bool {
			return bytes.Compare(rounds[index].Series[i].SeriesID[:], rounds[index].Series[j].SeriesID[:]) < 0
		})
	}
}

func cloneSwissProgressionRounds(rounds []SwissProgressionRound) []SwissProgressionRound {
	cloned := make([]SwissProgressionRound, len(rounds))
	for index, round := range rounds {
		cloned[index] = round
		cloned[index].Series = make([]SwissSeriesPointResult, len(round.Series))
		for seriesIndex, result := range round.Series {
			cloned[index].Series[seriesIndex] = cloneSwissSeriesPointResult(result)
		}
		if round.Bye != nil {
			bye := *round.Bye
			cloned[index].Bye = &bye
		}
	}
	return cloned
}

func cloneSwissSeriesPointResult(result SwissSeriesPointResult) SwissSeriesPointResult {
	cloned := result
	if result.WinnerID != nil {
		winner := *result.WinnerID
		cloned.WinnerID = &winner
	}
	cloned.FirstAcceptedSolveTime = cloneDurationPointer(result.FirstAcceptedSolveTime)
	cloned.SecondAcceptedSolveTime = cloneDurationPointer(result.SecondAcceptedSolveTime)
	return cloned
}

func cloneSwissNormalStandings(values []SwissNormalStanding) []SwissNormalStanding {
	cloned := make([]SwissNormalStanding, len(values))
	for index, value := range values {
		cloned[index] = value
		cloned[index].AcceptedSolveTime = cloneDurationPointer(value.AcceptedSolveTime)
	}
	return cloned
}

func cloneAutomaticSwissPairing(value AutomaticSwissPairing) AutomaticSwissPairing {
	cloned := value
	cloned.Pairings = append([]SwissPair(nil), value.Pairings...)
	cloned.Evidence.NormalizedInputs = append([]string(nil), value.Evidence.NormalizedInputs...)
	cloned.Evidence.Result = append([]string(nil), value.Evidence.Result...)
	return cloned
}

func cloneSwissByeSelection(value *SwissByeSelection) *SwissByeSelection {
	if value == nil {
		return nil
	}
	cloned := *value
	cloned.Evidence.NormalizedInputs = append([]string(nil), value.Evidence.NormalizedInputs...)
	cloned.Evidence.Result = append([]string(nil), value.Evidence.Result...)
	return &cloned
}

func cloneSwissRoundProgressionPlan(plan SwissRoundProgressionPlan) SwissRoundProgressionPlan {
	cloned := plan
	cloned.RosterParticipantIDs = append([]uuid.UUID(nil), plan.RosterParticipantIDs...)
	cloned.Seeds = append([]SwissParticipantSeed(nil), plan.Seeds...)
	cloned.History = cloneSwissProgressionRounds(plan.History)
	cloned.SourceRoundRevisionIDs = append([]uuid.UUID(nil), plan.SourceRoundRevisionIDs...)
	cloned.SourceResultRevisionIDs = append([]uuid.UUID(nil), plan.SourceResultRevisionIDs...)
	cloned.SourceByeRevisionIDs = append([]uuid.UUID(nil), plan.SourceByeRevisionIDs...)
	cloned.Standings = cloneSwissNormalStandings(plan.Standings)
	cloned.Pairing = cloneAutomaticSwissPairing(plan.Pairing)
	cloned.Bye = cloneSwissByeSelection(plan.Bye)
	return cloned
}

func cloneSwissRoundDraft(record *SwissRoundDraft) *SwissRoundDraft {
	if record == nil {
		return nil
	}
	cloned := *record
	cloned.Plan = cloneSwissRoundProgressionPlan(record.Plan)
	if record.LockedAt != nil {
		value := *record.LockedAt
		cloned.LockedAt = &value
	}
	if record.StartedAt != nil {
		value := *record.StartedAt
		cloned.StartedAt = &value
	}
	return &cloned
}

func equalOptionalSwissBye(first, second *SwissByeSelection) bool {
	return reflect.DeepEqual(first, second)
}

func swissRoundProgressionError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidSwissRoundProgression, message)
}

func swissRoundIncompleteError(message string) error {
	return fmt.Errorf("%w: %s", ErrSwissRoundIncomplete, message)
}
