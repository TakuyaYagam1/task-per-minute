package arena

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type ArenaPreflightCode string

const (
	PreflightCodeRosterComplete       ArenaPreflightCode = "arena.preflight.structure.roster_complete"
	PreflightCodeAttendance           ArenaPreflightCode = "arena.preflight.structure.attendance"
	PreflightCodeParticipantExclusive ArenaPreflightCode = "arena.preflight.structure.participant_exclusive"
	PreflightCodePreset               ArenaPreflightCode = "arena.preflight.structure.preset"
	PreflightCodeCategories           ArenaPreflightCode = "arena.preflight.structure.categories"
	PreflightCodePairings             ArenaPreflightCode = "arena.preflight.structure.pairings"
	PreflightCodeByes                 ArenaPreflightCode = "arena.preflight.structure.byes"
	PreflightCodeOverrides            ArenaPreflightCode = "arena.preflight.structure.overrides"
)

type ArenaPreflightCheck struct {
	Code        ArenaPreflightCode
	Passed      bool
	Explanation string
	Evidence    []string
}

type ArenaPreflightReport struct {
	Checks []ArenaPreflightCheck
}

type StructuralParticipant struct {
	ParticipantID        uuid.UUID
	PlayerID             uuid.UUID
	Seed                 int
	Attendance           domain.ArenaAttendanceState
	ReservedTournamentID uuid.UUID
}

type StructuralOverrideEvidence struct {
	Pair      SwissPair
	ActorID   uuid.UUID
	Confirmed bool
	Reason    string
}

type StructuralPreflightInput struct {
	TournamentID       uuid.UUID
	Preset             domain.ArenaPreset
	ExpectedRosterSize int
	Participants       []StructuralParticipant
	CategoryPools      []CategoryPoolRevision
	Pairings           []SwissPair
	ByeParticipantID   uuid.UUID
	RepeatedPairings   []SwissPair
	Overrides          []StructuralOverrideEvidence
}

func (r ArenaPreflightReport) Passed() bool {
	if len(r.Checks) == 0 {
		return false
	}
	for _, check := range r.Checks {
		if !check.Passed {
			return false
		}
	}
	return true
}

func EvaluateStructuralPreflight(in StructuralPreflightInput) ArenaPreflightReport {
	return ArenaPreflightReport{Checks: []ArenaPreflightCheck{
		checkRosterCompleteness(in),
		checkAttendance(in.Participants),
		checkParticipantExclusivity(in),
		checkPreset(in.Preset, in.ExpectedRosterSize),
		checkCategories(in.CategoryPools),
		checkPairings(in),
		checkBye(in),
		checkOverrides(in),
	}}
}

func checkRosterCompleteness(in StructuralPreflightInput) ArenaPreflightCheck {
	issues := make([]string, 0)
	if in.TournamentID == uuid.Nil {
		issues = append(issues, "tournament:missing")
	}
	if in.ExpectedRosterSize < 1 || len(in.Participants) != in.ExpectedRosterSize {
		issues = append(issues, fmt.Sprintf("count:%d:expected:%d", len(in.Participants), in.ExpectedRosterSize))
	}
	seeds := make(map[int]int, len(in.Participants))
	for _, participant := range in.Participants {
		if participant.ParticipantID == uuid.Nil || participant.PlayerID == uuid.Nil {
			issues = append(issues, "participant:missing_identity")
		}
		seeds[participant.Seed]++
	}
	for seed := 1; seed <= in.ExpectedRosterSize; seed++ {
		if seeds[seed] != 1 {
			issues = append(issues, fmt.Sprintf("seed:%d:count:%d", seed, seeds[seed]))
		}
	}
	return newArenaPreflightCheck(
		PreflightCodeRosterComplete,
		"Roster has the expected participants and one stable seed per slot.",
		issues,
		[]string{fmt.Sprintf("count:%d", len(in.Participants)), fmt.Sprintf("expected:%d", in.ExpectedRosterSize)},
	)
}

func checkAttendance(participants []StructuralParticipant) ArenaPreflightCheck {
	issues := make([]string, 0)
	for _, participant := range participants {
		if participant.Attendance != domain.ArenaAttendanceStateCheckedIn {
			issues = append(issues, participantEvidence(participant.ParticipantID)+":attendance:"+string(participant.Attendance))
		}
	}
	return newArenaPreflightCheck(
		PreflightCodeAttendance,
		"Every roster participant is checked in.",
		issues,
		[]string{fmt.Sprintf("checked_in:%d", len(participants))},
	)
}

func checkParticipantExclusivity(in StructuralPreflightInput) ArenaPreflightCheck {
	issues := make([]string, 0)
	participantCounts := make(map[uuid.UUID]int, len(in.Participants))
	playerCounts := make(map[uuid.UUID]int, len(in.Participants))
	for _, participant := range in.Participants {
		participantCounts[participant.ParticipantID]++
		playerCounts[participant.PlayerID]++
		if participant.ReservedTournamentID != in.TournamentID {
			issues = append(issues, participantEvidence(participant.ParticipantID)+":reservation:mismatch")
		}
	}
	for participantID, count := range participantCounts {
		if participantID != uuid.Nil && count > 1 {
			issues = append(issues, participantEvidence(participantID)+":count:"+strconv.Itoa(count))
		}
	}
	for playerID, count := range playerCounts {
		if playerID != uuid.Nil && count > 1 {
			issues = append(issues, "player:"+playerID.String()+":count:"+strconv.Itoa(count))
		}
	}
	return newArenaPreflightCheck(
		PreflightCodeParticipantExclusive,
		"Participant and player identities are unique and reserved for this tournament.",
		issues,
		[]string{fmt.Sprintf("exclusive:%d", len(in.Participants))},
	)
}

func checkPreset(preset domain.ArenaPreset, rosterSize int) ArenaPreflightCheck {
	issues := make([]string, 0)
	if !preset.IsValid() {
		issues = append(issues, "preset:unknown")
	} else if !preset.ValidRosterSize(rosterSize) {
		issues = append(issues, fmt.Sprintf("roster_size:%d:unsupported", rosterSize))
	}
	return newArenaPreflightCheck(
		PreflightCodePreset,
		"Tournament preset accepts the configured roster size.",
		issues,
		[]string{"preset:" + preset.String(), fmt.Sprintf("roster_size:%d", rosterSize)},
	)
}

func checkCategories(pools []CategoryPoolRevision) ArenaPreflightCheck {
	normalized, err := normalizeCategoryPoolRevisions(pools)
	issues := make([]string, 0)
	if err != nil {
		issues = append(issues, "category_pools:invalid")
	}
	evidence := make([]string, 0, len(normalized))
	for _, pool := range normalized {
		evidence = append(evidence, fmt.Sprintf("%s:%s:revision:%d", pool.Format, pool.ID, pool.Revision))
	}
	return newArenaPreflightCheck(
		PreflightCodeCategories,
		"BO1 and BO3 category pools have valid immutable revisions and category shapes.",
		issues,
		evidence,
	)
}

func checkPairings(in StructuralPreflightInput) ArenaPreflightCheck {
	issues := make([]string, 0)
	roster := participantIDSet(in.Participants)
	used := make(map[uuid.UUID]int, len(in.Participants))
	if len(in.Pairings) != in.ExpectedRosterSize/2 {
		issues = append(issues, fmt.Sprintf("pairing_count:%d:expected:%d", len(in.Pairings), in.ExpectedRosterSize/2))
	}
	for _, pair := range in.Pairings {
		key, err := validatedPairKey(pair)
		if err != nil {
			issues = append(issues, "pairing:invalid")
			continue
		}
		if _, exists := roster[key.first]; !exists {
			issues = append(issues, participantEvidence(key.first)+":foreign")
		}
		if _, exists := roster[key.second]; !exists {
			issues = append(issues, participantEvidence(key.second)+":foreign")
		}
		used[key.first]++
		used[key.second]++
	}
	for participantID := range roster {
		expected := 1
		if participantID == in.ByeParticipantID {
			expected = 0
		}
		if used[participantID] != expected {
			issues = append(issues, fmt.Sprintf("%s:pairing_count:%d", participantEvidence(participantID), used[participantID]))
		}
	}
	return newArenaPreflightCheck(
		PreflightCodePairings,
		"Pairings cover every non-bye participant exactly once without self-pairs.",
		issues,
		pairEvidence(in.Pairings),
	)
}

func checkBye(in StructuralPreflightInput) ArenaPreflightCheck {
	issues := make([]string, 0)
	roster := participantIDSet(in.Participants)
	paired := make(map[uuid.UUID]struct{}, len(in.Participants))
	for _, pair := range in.Pairings {
		paired[pair.FirstParticipantID] = struct{}{}
		paired[pair.SecondParticipantID] = struct{}{}
	}
	if in.ExpectedRosterSize%2 == 0 {
		if in.ByeParticipantID != uuid.Nil {
			issues = append(issues, "bye:unexpected")
		}
	} else {
		if in.ByeParticipantID == uuid.Nil {
			issues = append(issues, "bye:missing")
		} else if _, exists := roster[in.ByeParticipantID]; !exists {
			issues = append(issues, "bye:foreign")
		} else if _, exists := paired[in.ByeParticipantID]; exists {
			issues = append(issues, "bye:paired")
		}
	}
	evidence := []string{"bye:none"}
	if in.ByeParticipantID != uuid.Nil {
		evidence = []string{"bye:" + in.ByeParticipantID.String()}
	}
	return newArenaPreflightCheck(
		PreflightCodeByes,
		"Bye evidence matches roster parity and names one unpaired participant when required.",
		issues,
		evidence,
	)
}

func checkOverrides(in StructuralPreflightInput) ArenaPreflightCheck {
	issues := make([]string, 0)
	repeated := make(map[swissPairKey]struct{}, len(in.RepeatedPairings))
	for _, pair := range in.RepeatedPairings {
		key, err := validatedPairKey(pair)
		if err != nil {
			issues = append(issues, "repeat:invalid")
			continue
		}
		if _, duplicate := repeated[key]; duplicate {
			issues = append(issues, pairKeyEvidence(key)+":repeat_duplicate")
		}
		repeated[key] = struct{}{}
	}
	overrides := make(map[swissPairKey]StructuralOverrideEvidence, len(in.Overrides))
	for _, override := range in.Overrides {
		key, err := validatedPairKey(override.Pair)
		if err != nil {
			issues = append(issues, "override:invalid_pair")
			continue
		}
		if _, duplicate := overrides[key]; duplicate {
			issues = append(issues, pairKeyEvidence(key)+":override_duplicate")
		}
		overrides[key] = override
	}
	for key := range repeated {
		override, exists := overrides[key]
		if !exists {
			issues = append(issues, pairKeyEvidence(key)+":override_missing")
			continue
		}
		if !override.Confirmed || override.ActorID == uuid.Nil || strings.TrimSpace(override.Reason) == "" {
			issues = append(issues, pairKeyEvidence(key)+":override_unconfirmed")
		}
	}
	for key := range overrides {
		if _, required := repeated[key]; !required {
			issues = append(issues, pairKeyEvidence(key)+":override_not_required")
		}
	}
	return newArenaPreflightCheck(
		PreflightCodeOverrides,
		"Every repeated pairing has one confirmed reasoned override and no extra override.",
		issues,
		[]string{fmt.Sprintf("required:%d", len(repeated)), fmt.Sprintf("recorded:%d", len(overrides))},
	)
}

func newArenaPreflightCheck(
	code ArenaPreflightCode,
	explanation string,
	issues []string,
	passEvidence []string,
) ArenaPreflightCheck {
	passed := len(issues) == 0
	evidence := issues
	if passed {
		evidence = passEvidence
	}
	return ArenaPreflightCheck{
		Code: code, Passed: passed, Explanation: explanation, Evidence: normalizePreflightEvidence(evidence),
	}
}

func normalizePreflightEvidence(evidence []string) []string {
	normalized := make([]string, 0, len(evidence))
	for _, item := range evidence {
		item = strings.TrimSpace(item)
		if item != "" {
			normalized = append(normalized, item)
		}
	}
	sort.Strings(normalized)
	write := 0
	for _, item := range normalized {
		if write == 0 || normalized[write-1] != item {
			normalized[write] = item
			write++
		}
	}
	return normalized[:write]
}

func participantIDSet(participants []StructuralParticipant) map[uuid.UUID]struct{} {
	result := make(map[uuid.UUID]struct{}, len(participants))
	for _, participant := range participants {
		if participant.ParticipantID != uuid.Nil {
			result[participant.ParticipantID] = struct{}{}
		}
	}
	return result
}

func validatedPairKey(pair SwissPair) (swissPairKey, error) {
	if pair.FirstParticipantID == uuid.Nil || pair.SecondParticipantID == uuid.Nil ||
		pair.FirstParticipantID == pair.SecondParticipantID {
		return swissPairKey{}, fmt.Errorf("invalid pair")
	}
	return newSwissPairKey(pair.FirstParticipantID, pair.SecondParticipantID), nil
}

func pairEvidence(pairings []SwissPair) []string {
	evidence := make([]string, 0, len(pairings))
	for _, pair := range pairings {
		key, err := validatedPairKey(pair)
		if err == nil {
			evidence = append(evidence, pairKeyEvidence(key))
		}
	}
	return evidence
}

func pairKeyEvidence(key swissPairKey) string {
	first, second := key.first, key.second
	if bytes.Compare(first[:], second[:]) > 0 {
		first, second = second, first
	}
	return "pair:" + first.String() + ":" + second.String()
}

func participantEvidence(participantID uuid.UUID) string {
	if participantID == uuid.Nil {
		return "participant:missing"
	}
	return "participant:" + participantID.String()
}
