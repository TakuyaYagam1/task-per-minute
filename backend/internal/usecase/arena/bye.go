package arena

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
	SwissByePoints = 1

	swissByeParticipantPrefix   = "participant:"
	swissByePointsPrefix        = "points:"
	swissByeBuchholzPrefix      = "buchholz:"
	swissByeHeadToHeadPrefix    = "head-to-head:"
	swissByeEffectiveTimePrefix = "effective-time-ns:"
	swissByeReceivedPrefix      = "received-bye:"
	swissByeNotApplicable       = "na"
)

var ErrInvalidSwissBye = errors.New("invalid swiss bye selection")

type SwissByeCandidate struct {
	ParticipantID        uuid.UUID
	Points               int
	ProvisionalBuchholz  int
	HeadToHeadPoints     int
	HeadToHeadApplicable bool
	EffectiveTime        time.Duration
	ReceivedBye          bool
}

type SwissByeSelection struct {
	ParticipantID uuid.UUID
	PointsAwarded int
	Evidence      domain.ArenaDecisionEvidence
}

func SelectSwissBye(
	evidenceID uuid.UUID,
	roundID uuid.UUID,
	candidates []SwissByeCandidate,
	decidedAt time.Time,
) (SwissByeSelection, error) {
	inputs, err := swissByeInputs(candidates)
	if err != nil {
		return SwissByeSelection{}, err
	}
	evidence, err := domain.NewArenaDecisionEvidence(
		evidenceID,
		domain.ArenaDecisionPurposePairing,
		domain.ArenaDecisionAlgorithmV1,
		inputs,
		roundID,
		decidedAt,
	)
	if err != nil {
		return SwissByeSelection{}, fmt.Errorf("%w: %w", ErrInvalidSwissBye, err)
	}
	return selectSwissByeFromEvidence(evidence)
}

func ReplaySwissBye(selection SwissByeSelection) (SwissByeSelection, error) {
	replayed, err := selectSwissByeFromEvidence(selection.Evidence)
	if err != nil {
		return SwissByeSelection{}, err
	}
	if replayed.ParticipantID != selection.ParticipantID || replayed.PointsAwarded != selection.PointsAwarded {
		return SwissByeSelection{}, domain.ErrArenaDecisionReplayMismatch
	}
	return replayed, nil
}

func swissByeInputs(candidates []SwissByeCandidate) ([]string, error) {
	if err := validateSwissByeCandidates(candidates); err != nil {
		return nil, err
	}
	inputs := make([]string, len(candidates))
	for i, candidate := range candidates {
		headToHead := swissByeNotApplicable
		if candidate.HeadToHeadApplicable {
			headToHead = strconv.Itoa(candidate.HeadToHeadPoints)
		}
		inputs[i] = strings.Join([]string{
			swissByeParticipantPrefix + candidate.ParticipantID.String(),
			swissByePointsPrefix + strconv.Itoa(candidate.Points),
			swissByeBuchholzPrefix + strconv.Itoa(candidate.ProvisionalBuchholz),
			swissByeHeadToHeadPrefix + headToHead,
			swissByeEffectiveTimePrefix + strconv.FormatInt(int64(candidate.EffectiveTime), 10),
			swissByeReceivedPrefix + strconv.FormatBool(candidate.ReceivedBye),
		}, "|")
	}
	return inputs, nil
}

func validateSwissByeCandidates(candidates []SwissByeCandidate) error {
	if len(candidates) < domain.ArenaMinParticipants ||
		len(candidates) > domain.ArenaMaxParticipants ||
		len(candidates)%2 == 0 {
		return fmt.Errorf("%w: roster size must be odd and between %d and %d",
			ErrInvalidSwissBye, domain.ArenaMinParticipants, domain.ArenaMaxParticipants)
	}

	seen := make(map[uuid.UUID]struct{}, len(candidates))
	hasEligibleCandidate := false
	for _, candidate := range candidates {
		if candidate.ParticipantID == uuid.Nil {
			return fmt.Errorf("%w: missing participant identity", ErrInvalidSwissBye)
		}
		if _, duplicate := seen[candidate.ParticipantID]; duplicate {
			return fmt.Errorf("%w: duplicate participant", ErrInvalidSwissBye)
		}
		seen[candidate.ParticipantID] = struct{}{}
		if candidate.Points < 0 || candidate.ProvisionalBuchholz < 0 || candidate.EffectiveTime < 0 {
			return fmt.Errorf("%w: negative ranking metric", ErrInvalidSwissBye)
		}
		if candidate.HeadToHeadPoints < 0 || (!candidate.HeadToHeadApplicable && candidate.HeadToHeadPoints != 0) {
			return fmt.Errorf("%w: invalid head-to-head metric", ErrInvalidSwissBye)
		}
		if !candidate.ReceivedBye {
			hasEligibleCandidate = true
		}
	}
	if !hasEligibleCandidate {
		return fmt.Errorf("%w: every participant already received a bye", ErrInvalidSwissBye)
	}
	return nil
}

func selectSwissByeFromEvidence(evidence domain.ArenaDecisionEvidence) (SwissByeSelection, error) {
	if evidence.Purpose != domain.ArenaDecisionPurposePairing {
		return SwissByeSelection{}, fmt.Errorf("%w: decision purpose is %q", ErrInvalidSwissBye, evidence.Purpose)
	}
	orderedInputs, err := evidence.Replay()
	if err != nil {
		return SwissByeSelection{}, fmt.Errorf("%w: %w", ErrInvalidSwissBye, err)
	}
	candidates := make([]SwissByeCandidate, len(orderedInputs))
	for i, input := range orderedInputs {
		candidate, parseErr := parseSwissByeInput(input)
		if parseErr != nil {
			return SwissByeSelection{}, parseErr
		}
		candidates[i] = candidate
	}
	if err := validateSwissByeCandidates(candidates); err != nil {
		return SwissByeSelection{}, err
	}

	contenders := make([]SwissByeCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !candidate.ReceivedBye {
			contenders = append(contenders, candidate)
		}
	}
	contenders = keepLowestSwissByeMetric(contenders, func(candidate SwissByeCandidate) int {
		return candidate.Points
	})
	contenders = keepLowestSwissByeMetric(contenders, func(candidate SwissByeCandidate) int {
		return candidate.ProvisionalBuchholz
	})
	if swissByeHeadToHeadApplies(contenders) {
		contenders = keepLowestSwissByeMetric(contenders, func(candidate SwissByeCandidate) int {
			return candidate.HeadToHeadPoints
		})
	}
	contenders = keepHighestSwissByeEffectiveTime(contenders)

	return SwissByeSelection{
		ParticipantID: contenders[0].ParticipantID,
		PointsAwarded: SwissByePoints,
		Evidence:      evidence,
	}, nil
}

func parseSwissByeInput(input string) (SwissByeCandidate, error) {
	parts := strings.Split(input, "|")
	if len(parts) != 6 {
		return SwissByeCandidate{}, fmt.Errorf("%w: invalid recorded candidate", ErrInvalidSwissBye)
	}

	participantID, err := uuid.Parse(strings.TrimPrefix(parts[0], swissByeParticipantPrefix))
	if err != nil || !strings.HasPrefix(parts[0], swissByeParticipantPrefix) {
		return SwissByeCandidate{}, fmt.Errorf("%w: invalid recorded participant", ErrInvalidSwissBye)
	}
	points, err := parseSwissByeInt(parts[1], swissByePointsPrefix)
	if err != nil {
		return SwissByeCandidate{}, err
	}
	buchholz, err := parseSwissByeInt(parts[2], swissByeBuchholzPrefix)
	if err != nil {
		return SwissByeCandidate{}, err
	}

	headToHeadValue, ok := strings.CutPrefix(parts[3], swissByeHeadToHeadPrefix)
	if !ok {
		return SwissByeCandidate{}, fmt.Errorf("%w: invalid recorded head-to-head metric", ErrInvalidSwissBye)
	}
	headToHeadApplicable := headToHeadValue != swissByeNotApplicable
	headToHeadPoints := 0
	if headToHeadApplicable {
		headToHeadPoints, err = strconv.Atoi(headToHeadValue)
		if err != nil {
			return SwissByeCandidate{}, fmt.Errorf("%w: invalid recorded head-to-head metric", ErrInvalidSwissBye)
		}
	}

	effectiveTimeValue, ok := strings.CutPrefix(parts[4], swissByeEffectiveTimePrefix)
	if !ok {
		return SwissByeCandidate{}, fmt.Errorf("%w: invalid recorded effective time", ErrInvalidSwissBye)
	}
	effectiveTime, err := strconv.ParseInt(effectiveTimeValue, 10, 64)
	if err != nil {
		return SwissByeCandidate{}, fmt.Errorf("%w: invalid recorded effective time", ErrInvalidSwissBye)
	}
	receivedValue, ok := strings.CutPrefix(parts[5], swissByeReceivedPrefix)
	if !ok {
		return SwissByeCandidate{}, fmt.Errorf("%w: invalid recorded bye history", ErrInvalidSwissBye)
	}
	receivedBye, err := strconv.ParseBool(receivedValue)
	if err != nil {
		return SwissByeCandidate{}, fmt.Errorf("%w: invalid recorded bye history", ErrInvalidSwissBye)
	}

	return SwissByeCandidate{
		ParticipantID:        participantID,
		Points:               points,
		ProvisionalBuchholz:  buchholz,
		HeadToHeadPoints:     headToHeadPoints,
		HeadToHeadApplicable: headToHeadApplicable,
		EffectiveTime:        time.Duration(effectiveTime),
		ReceivedBye:          receivedBye,
	}, nil
}

func parseSwissByeInt(value, prefix string) (int, error) {
	recorded, ok := strings.CutPrefix(value, prefix)
	if !ok {
		return 0, fmt.Errorf("%w: invalid recorded ranking metric", ErrInvalidSwissBye)
	}
	parsed, err := strconv.Atoi(recorded)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid recorded ranking metric", ErrInvalidSwissBye)
	}
	return parsed, nil
}

func keepLowestSwissByeMetric(
	candidates []SwissByeCandidate,
	metric func(SwissByeCandidate) int,
) []SwissByeCandidate {
	lowest := metric(candidates[0])
	for _, candidate := range candidates[1:] {
		if value := metric(candidate); value < lowest {
			lowest = value
		}
	}
	filtered := make([]SwissByeCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if metric(candidate) == lowest {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}

func swissByeHeadToHeadApplies(candidates []SwissByeCandidate) bool {
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

func keepHighestSwissByeEffectiveTime(candidates []SwissByeCandidate) []SwissByeCandidate {
	highest := candidates[0].EffectiveTime
	for _, candidate := range candidates[1:] {
		if candidate.EffectiveTime > highest {
			highest = candidate.EffectiveTime
		}
	}
	filtered := make([]SwissByeCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.EffectiveTime == highest {
			filtered = append(filtered, candidate)
		}
	}
	return filtered
}
