package swiss

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	PairingByePoints = 1

	swissByeParticipantPrefix   = "participant:"
	swissByePointsPrefix        = "points:"
	swissByeBuchholzPrefix      = "buchholz:"
	swissByeHeadToHeadPrefix    = "head-to-head:"
	swissByeEffectiveTimePrefix = "effective-time-ns:"
	swissByeReceivedPrefix      = "received-bye:"
	swissByeManualPrefix        = "manual-selected:"
	swissByeNotApplicable       = "na"
)

var (
	ErrInvalidBye           = errors.New("invalid swiss bye selection")
	ErrManualByeNotEligible = errors.New("manual swiss bye is not a policy contender")
)

type ByeCandidate struct {
	ParticipantID        uuid.UUID
	Points               int
	ProvisionalBuchholz  int
	HeadToHeadPoints     int
	HeadToHeadApplicable bool
	EffectiveTime        time.Duration
	ReceivedBye          bool
	manualMarkerPresent  bool
	manualSelected       bool
}

type ByeSelection struct {
	ParticipantID uuid.UUID
	PointsAwarded int
	Evidence      domain.DecisionEvidence
}

func SelectBye(
	evidenceID uuid.UUID,
	roundID uuid.UUID,
	candidates []ByeCandidate,
	decidedAt time.Time,
) (ByeSelection, error) {
	inputs, err := BuildByeInputs(candidates)
	if err != nil {
		return ByeSelection{}, err
	}
	evidence, err := domain.NewDecisionEvidence(
		evidenceID,
		domain.DecisionPurposePairing,
		domain.DecisionAlgorithmV1,
		inputs,
		roundID,
		decidedAt,
	)
	if err != nil {
		return ByeSelection{}, fmt.Errorf("%w: %w", ErrInvalidBye, err)
	}
	return selectSwissByeFromEvidence(evidence)
}

// SelectManualBye records an operator-selected bye only when the requested
// participant remains among the deterministic policy contenders. The marker
// is part of the evidence input so ReplayBye can verify the same choice
// without relying on the random tie-break seed.
func SelectManualBye(
	evidenceID uuid.UUID,
	roundID uuid.UUID,
	candidates []ByeCandidate,
	requestedParticipantID uuid.UUID,
	decidedAt time.Time,
) (ByeSelection, error) {
	if requestedParticipantID == uuid.Nil {
		return ByeSelection{}, fmt.Errorf("%w: missing requested participant", ErrManualByeNotEligible)
	}
	inputs, err := buildByeInputs(candidates, &requestedParticipantID)
	if err != nil {
		return ByeSelection{}, err
	}
	evidence, err := domain.NewDecisionEvidence(
		evidenceID,
		domain.DecisionPurposePairing,
		domain.DecisionAlgorithmV1,
		inputs,
		roundID,
		decidedAt,
	)
	if err != nil {
		return ByeSelection{}, fmt.Errorf("%w: %w", ErrInvalidBye, err)
	}
	selection, err := selectSwissByeFromEvidence(evidence)
	if err != nil {
		return ByeSelection{}, err
	}
	if selection.ParticipantID != requestedParticipantID {
		return ByeSelection{}, fmt.Errorf("%w: selected participant does not match request", ErrManualByeNotEligible)
	}
	return selection, nil
}

func ReplayBye(selection ByeSelection) (ByeSelection, error) {
	replayed, err := selectSwissByeFromEvidence(selection.Evidence)
	if err != nil {
		return ByeSelection{}, err
	}
	if replayed.ParticipantID != selection.ParticipantID || replayed.PointsAwarded != selection.PointsAwarded {
		return ByeSelection{}, domain.ErrDecisionReplayMismatch
	}
	return replayed, nil
}

func BuildByeInputs(candidates []ByeCandidate) ([]string, error) {
	return buildByeInputs(candidates, nil)
}

func buildByeInputs(candidates []ByeCandidate, requestedParticipantID *uuid.UUID) ([]string, error) {
	if err := validateSwissByeCandidates(candidates); err != nil {
		return nil, err
	}
	if requestedParticipantID != nil {
		contenders := swissByePolicyContenders(candidates)
		eligible := false
		for _, candidate := range contenders {
			if candidate.ParticipantID == *requestedParticipantID {
				eligible = true
				break
			}
		}
		if !eligible {
			return nil, fmt.Errorf("%w: participant %s is not an eligible policy contender", ErrManualByeNotEligible, *requestedParticipantID)
		}
	}
	inputs := make([]string, len(candidates))
	for i, candidate := range candidates {
		headToHead := swissByeNotApplicable
		if candidate.HeadToHeadApplicable {
			headToHead = strconv.Itoa(candidate.HeadToHeadPoints)
		}
		fields := []string{
			swissByeParticipantPrefix + candidate.ParticipantID.String(),
			swissByePointsPrefix + strconv.Itoa(candidate.Points),
			swissByeBuchholzPrefix + strconv.Itoa(candidate.ProvisionalBuchholz),
			swissByeHeadToHeadPrefix + headToHead,
			swissByeEffectiveTimePrefix + strconv.FormatInt(int64(candidate.EffectiveTime), 10),
			swissByeReceivedPrefix + strconv.FormatBool(candidate.ReceivedBye),
		}
		if requestedParticipantID != nil {
			fields = append(fields, swissByeManualPrefix+strconv.FormatBool(candidate.ParticipantID == *requestedParticipantID))
		}
		inputs[i] = strings.Join(fields, "|")
	}
	return inputs, nil
}

func validateSwissByeCandidates(candidates []ByeCandidate) error {
	if len(candidates) < domain.TournamentMinParticipants ||
		len(candidates) > domain.TournamentMaxParticipants ||
		len(candidates)%2 == 0 {
		return fmt.Errorf("%w: roster size must be odd and between %d and %d",
			ErrInvalidBye, domain.TournamentMinParticipants, domain.TournamentMaxParticipants)
	}

	seen := make(map[uuid.UUID]struct{}, len(candidates))
	hasEligibleCandidate := false
	for _, candidate := range candidates {
		if candidate.ParticipantID == uuid.Nil {
			return fmt.Errorf("%w: missing participant identity", ErrInvalidBye)
		}
		if _, duplicate := seen[candidate.ParticipantID]; duplicate {
			return fmt.Errorf("%w: duplicate participant", ErrInvalidBye)
		}
		seen[candidate.ParticipantID] = struct{}{}
		if candidate.Points < 0 || candidate.ProvisionalBuchholz < 0 || candidate.EffectiveTime < 0 {
			return fmt.Errorf("%w: negative ranking metric", ErrInvalidBye)
		}
		if candidate.HeadToHeadPoints < 0 || (!candidate.HeadToHeadApplicable && candidate.HeadToHeadPoints != 0) {
			return fmt.Errorf("%w: invalid head-to-head metric", ErrInvalidBye)
		}
		if !candidate.ReceivedBye {
			hasEligibleCandidate = true
		}
	}
	if !hasEligibleCandidate {
		return fmt.Errorf("%w: every participant already received a bye", ErrInvalidBye)
	}
	return nil
}

func selectSwissByeFromEvidence(evidence domain.DecisionEvidence) (ByeSelection, error) {
	if evidence.Purpose != domain.DecisionPurposePairing {
		return ByeSelection{}, fmt.Errorf("%w: decision purpose is %q", ErrInvalidBye, evidence.Purpose)
	}
	orderedInputs, err := evidence.Replay()
	if err != nil {
		return ByeSelection{}, fmt.Errorf("%w: %w", ErrInvalidBye, err)
	}
	candidates := make([]ByeCandidate, len(orderedInputs))
	manualMarkerCount := 0
	manualSelectedCount := 0
	for i, input := range orderedInputs {
		candidate, parseErr := parseSwissByeInput(input)
		if parseErr != nil {
			return ByeSelection{}, parseErr
		}
		candidates[i] = candidate
		if candidate.manualMarkerPresent {
			manualMarkerCount++
			if candidate.manualSelected {
				manualSelectedCount++
			}
		}
	}
	if err := validateSwissByeCandidates(candidates); err != nil {
		return ByeSelection{}, err
	}
	if manualMarkerCount != 0 {
		if manualMarkerCount != len(candidates) || manualSelectedCount != 1 {
			return ByeSelection{}, fmt.Errorf("%w: manual selection marker must identify exactly one candidate", ErrInvalidBye)
		}
		contenders := swissByePolicyContenders(candidates)
		for _, candidate := range contenders {
			if candidate.manualSelected {
				return ByeSelection{
					ParticipantID: candidate.ParticipantID,
					PointsAwarded: PairingByePoints,
					Evidence:      evidence,
				}, nil
			}
		}
		return ByeSelection{}, fmt.Errorf("%w: marked participant is not an eligible policy contender", ErrManualByeNotEligible)
	}

	contenders := swissByePolicyContenders(candidates)

	return ByeSelection{
		ParticipantID: contenders[0].ParticipantID,
		PointsAwarded: PairingByePoints,
		Evidence:      evidence,
	}, nil
}

//nolint:gocyclo // Evidence decoding intentionally validates every signed candidate field in one pass.
func parseSwissByeInput(input string) (ByeCandidate, error) {
	parts := strings.Split(input, "|")
	if len(parts) != 6 && len(parts) != 7 {
		return ByeCandidate{}, fmt.Errorf("%w: invalid recorded candidate", ErrInvalidBye)
	}

	participantID, err := uuid.Parse(strings.TrimPrefix(parts[0], swissByeParticipantPrefix))
	if err != nil || !strings.HasPrefix(parts[0], swissByeParticipantPrefix) {
		return ByeCandidate{}, fmt.Errorf("%w: invalid recorded participant", ErrInvalidBye)
	}
	points, err := parseSwissByeInt(parts[1], swissByePointsPrefix)
	if err != nil {
		return ByeCandidate{}, err
	}
	buchholz, err := parseSwissByeInt(parts[2], swissByeBuchholzPrefix)
	if err != nil {
		return ByeCandidate{}, err
	}

	headToHeadValue, ok := strings.CutPrefix(parts[3], swissByeHeadToHeadPrefix)
	if !ok {
		return ByeCandidate{}, fmt.Errorf("%w: invalid recorded head-to-head metric", ErrInvalidBye)
	}
	headToHeadApplicable := headToHeadValue != swissByeNotApplicable
	headToHeadPoints := 0
	if headToHeadApplicable {
		headToHeadPoints, err = strconv.Atoi(headToHeadValue)
		if err != nil {
			return ByeCandidate{}, fmt.Errorf("%w: invalid recorded head-to-head metric", ErrInvalidBye)
		}
	}

	effectiveTimeValue, ok := strings.CutPrefix(parts[4], swissByeEffectiveTimePrefix)
	if !ok {
		return ByeCandidate{}, fmt.Errorf("%w: invalid recorded effective time", ErrInvalidBye)
	}
	effectiveTime, err := strconv.ParseInt(effectiveTimeValue, 10, 64)
	if err != nil {
		return ByeCandidate{}, fmt.Errorf("%w: invalid recorded effective time", ErrInvalidBye)
	}
	receivedValue, ok := strings.CutPrefix(parts[5], swissByeReceivedPrefix)
	if !ok {
		return ByeCandidate{}, fmt.Errorf("%w: invalid recorded bye history", ErrInvalidBye)
	}
	receivedBye, err := strconv.ParseBool(receivedValue)
	if err != nil {
		return ByeCandidate{}, fmt.Errorf("%w: invalid recorded bye history", ErrInvalidBye)
	}
	manualMarkerPresent := false
	manualSelected := false
	if len(parts) == 7 {
		manualValue, ok := strings.CutPrefix(parts[6], swissByeManualPrefix)
		if !ok {
			return ByeCandidate{}, fmt.Errorf("%w: invalid manual selection marker", ErrInvalidBye)
		}
		manualSelected, err = strconv.ParseBool(manualValue)
		if err != nil {
			return ByeCandidate{}, fmt.Errorf("%w: invalid manual selection marker", ErrInvalidBye)
		}
		manualMarkerPresent = true
	}

	return ByeCandidate{
		ParticipantID:        participantID,
		Points:               points,
		ProvisionalBuchholz:  buchholz,
		HeadToHeadPoints:     headToHeadPoints,
		HeadToHeadApplicable: headToHeadApplicable,
		EffectiveTime:        time.Duration(effectiveTime),
		ReceivedBye:          receivedBye,
		manualMarkerPresent:  manualMarkerPresent,
		manualSelected:       manualSelected,
	}, nil
}

func swissByePolicyContenders(candidates []ByeCandidate) []ByeCandidate {
	contenders := make([]ByeCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !candidate.ReceivedBye {
			contenders = append(contenders, candidate)
		}
	}
	contenders = keepLowestSwissByeMetric(contenders, func(candidate ByeCandidate) int {
		return candidate.Points
	})
	contenders = keepLowestSwissByeMetric(contenders, func(candidate ByeCandidate) int {
		return candidate.ProvisionalBuchholz
	})
	if swissByeHeadToHeadApplies(contenders) {
		contenders = keepLowestSwissByeMetric(contenders, func(candidate ByeCandidate) int {
			return candidate.HeadToHeadPoints
		})
	}
	return keepHighestSwissByeEffectiveTime(contenders)
}

func parseSwissByeInt(value, prefix string) (int, error) {
	recorded, ok := strings.CutPrefix(value, prefix)
	if !ok {
		return 0, fmt.Errorf("%w: invalid recorded ranking metric", ErrInvalidBye)
	}
	parsed, err := strconv.Atoi(recorded)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid recorded ranking metric", ErrInvalidBye)
	}
	return parsed, nil
}

func keepLowestSwissByeMetric(
	candidates []ByeCandidate,
	metric func(ByeCandidate) int,
) []ByeCandidate {
	lowest := metric(candidates[0])
	for _, candidate := range candidates[1:] {
		if value := metric(candidate); value < lowest {
			lowest = value
		}
	}
	filtered := make([]ByeCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if metric(candidate) == lowest {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func swissByeHeadToHeadApplies(candidates []ByeCandidate) bool {
	if len(candidates) < 2 {
		return false
	}
	for _, candidate := range candidates {
		if !candidate.HeadToHeadApplicable {
			return false
		}
	}
	return true
}

func keepHighestSwissByeEffectiveTime(candidates []ByeCandidate) []ByeCandidate {
	highest := candidates[0].EffectiveTime
	for _, candidate := range candidates[1:] {
		if candidate.EffectiveTime > highest {
			highest = candidate.EffectiveTime
		}
	}
	filtered := make([]ByeCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.EffectiveTime == highest {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}
