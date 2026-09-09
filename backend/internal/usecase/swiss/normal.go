package swiss

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidNormalOrdering = errors.New("invalid Swiss normal ordering")

type PointsLabel string

const (
	PointsProvisional PointsLabel = "provisional"
	PointsFinal       PointsLabel = "final"
)

type ParticipantSeed struct {
	ParticipantID uuid.UUID
	Seed          int
}

type NormalOrderingInput struct {
	Ledger        PointLedger
	Seeds         []ParticipantSeed
	SwissComplete bool
}

type NormalStanding struct {
	ParticipantID     uuid.UUID
	Position          int
	Points            int
	PointsLabel       PointsLabel
	Buchholz          int
	HeadToHeadPoints  int
	HeadToHeadApplied bool
	EffectiveTime     time.Duration
	AcceptedSolveTime *time.Duration
	Seed              int
}

type standingGroupKey struct {
	points   int
	buchholz int
}

func OrderNormalStandings(input NormalOrderingInput) ([]NormalStanding, error) {
	if err := validateSwissPointLedger(input.Ledger); err != nil {
		return nil, err
	}
	seeds, err := SeedMap(input.Ledger.ParticipantIDs, input.Seeds)
	if err != nil {
		return nil, err
	}
	points := make(map[uuid.UUID]int, len(input.Ledger.Totals))
	for _, total := range input.Ledger.Totals {
		points[total.ParticipantID] = total.Points
	}
	buchholz := swissBuchholz(input.Ledger.Entries, points)
	label := PointsProvisional
	if input.SwissComplete {
		label = PointsFinal
	}
	standings := make([]NormalStanding, len(input.Ledger.Totals))
	for index, total := range input.Ledger.Totals {
		standings[index] = NormalStanding{
			ParticipantID: total.ParticipantID,
			Points:        total.Points, PointsLabel: label, Buchholz: buchholz[total.ParticipantID],
			EffectiveTime:     total.EffectiveTime,
			AcceptedSolveTime: cloneDurationPointer(total.AcceptedSolveTime), Seed: seeds[total.ParticipantID],
		}
	}
	applySwissHeadToHead(standings, input.Ledger.Entries)
	sort.Slice(standings, func(i, j int) bool { return StandingLess(standings[i], standings[j]) })
	for index := range standings {
		standings[index].Position = index + 1
	}
	return standings, nil
}

func validateSwissPointLedger(ledger PointLedger) error {
	rebuilt, rebuildErr := rebuildSwissPointLedger(ledger)
	if rebuildErr != nil {
		return rebuildErr
	}
	if !reflect.DeepEqual(rebuilt, ledger) {
		return swissNormalOrderingError("ledger is not canonical")
	}
	participants, err := CanonicalLedgerParticipants(ledger.ParticipantIDs)
	if err != nil || len(participants) != len(ledger.ParticipantIDs) {
		return swissNormalOrderingError("ledger roster is invalid")
	}
	for index := range participants {
		if participants[index] != ledger.ParticipantIDs[index] {
			return swissNormalOrderingError("ledger roster is not canonical")
		}
	}
	if len(ledger.Totals) != len(participants) {
		return swissNormalOrderingError("ledger totals do not cover the roster")
	}
	if !sort.SliceIsSorted(ledger.Entries, func(i, j int) bool {
		return compareSwissLedgerEntries(ledger.Entries[i], ledger.Entries[j]) < 0
	}) {
		return swissNormalOrderingError("ledger entries are not canonical")
	}
	if err := validateSwissLedgerEntryIdentity(ledger.Entries); err != nil {
		return err
	}
	want, err := calculateSwissPointTotals(participants, ledger.Entries)
	if err != nil || !equalSwissPointTotals(want, ledger.Totals) {
		return swissNormalOrderingError("ledger totals do not match entries")
	}
	return nil
}

func equalSwissPointTotals(first, second []PointTotal) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index].ParticipantID != second[index].ParticipantID ||
			first[index].Points != second[index].Points ||
			first[index].EffectiveTime != second[index].EffectiveTime ||
			!equalDurationPointers(first[index].AcceptedSolveTime, second[index].AcceptedSolveTime) {
			return false
		}
	}
	return true
}

func equalDurationPointers(first, second *time.Duration) bool {
	return (first == nil && second == nil) || (first != nil && second != nil && *first == *second)
}

func SeedMap(participants []uuid.UUID, values []ParticipantSeed) (map[uuid.UUID]int, error) {
	if len(values) != len(participants) {
		return nil, swissNormalOrderingError("seeds do not cover the roster")
	}
	roster := make(map[uuid.UUID]struct{}, len(participants))
	for _, participantID := range participants {
		roster[participantID] = struct{}{}
	}
	seeds := make(map[uuid.UUID]int, len(values))
	used := make(map[int]struct{}, len(values))
	for _, value := range values {
		if _, exists := roster[value.ParticipantID]; !exists || value.Seed < 1 {
			return nil, swissNormalOrderingError("seed has invalid participant or value")
		}
		if _, duplicate := seeds[value.ParticipantID]; duplicate {
			return nil, swissNormalOrderingError("participant seed is duplicated")
		}
		if _, duplicate := used[value.Seed]; duplicate {
			return nil, swissNormalOrderingError("stable seed is duplicated")
		}
		seeds[value.ParticipantID] = value.Seed
		used[value.Seed] = struct{}{}
	}
	return seeds, nil
}

func swissBuchholz(entries []PointLedgerEntry, points map[uuid.UUID]int) map[uuid.UUID]int {
	buchholz := make(map[uuid.UUID]int, len(points))
	for _, entry := range entries {
		if entry.SourceKind != PointSourceSeries {
			continue
		}
		for _, award := range entry.Awards {
			buchholz[award.ParticipantID] += points[award.OpponentID]
		}
	}
	return buchholz
}

func applySwissHeadToHead(standings []NormalStanding, entries []PointLedgerEntry) {
	groups := make(map[standingGroupKey][]int, len(standings))
	for index, standing := range standings {
		key := standingGroupKey{points: standing.Points, buchholz: standing.Buchholz}
		groups[key] = append(groups[key], index)
	}
	for _, indexes := range groups {
		if len(indexes) != 2 {
			continue
		}
		first := indexes[0]
		second := indexes[1]
		firstPoints, secondPoints, meetings := swissHeadToHeadPoints(
			standings[first].ParticipantID, standings[second].ParticipantID, entries,
		)
		if meetings == 0 {
			continue
		}
		standings[first].HeadToHeadApplied = true
		standings[first].HeadToHeadPoints = firstPoints
		standings[second].HeadToHeadApplied = true
		standings[second].HeadToHeadPoints = secondPoints
	}
}

func swissHeadToHeadPoints(
	first uuid.UUID,
	second uuid.UUID,
	entries []PointLedgerEntry,
) (int, int, int) {
	firstPoints := 0
	secondPoints := 0
	meetings := 0
	for _, entry := range entries {
		if entry.SourceKind != PointSourceSeries || len(entry.Awards) != 2 {
			continue
		}
		if !swissAwardsMatchParticipants(entry.Awards, first, second) {
			continue
		}
		meetings++
		for _, award := range entry.Awards {
			if award.ParticipantID == first {
				firstPoints += award.Points
			} else {
				secondPoints += award.Points
			}
		}
	}
	return firstPoints, secondPoints, meetings
}

func swissAwardsMatchParticipants(awards []PointAward, first, second uuid.UUID) bool {
	return (awards[0].ParticipantID == first && awards[1].ParticipantID == second) ||
		(awards[0].ParticipantID == second && awards[1].ParticipantID == first)
}

func StandingLess(first, second NormalStanding) bool {
	if first.Points != second.Points {
		return first.Points > second.Points
	}
	if first.Buchholz != second.Buchholz {
		return first.Buchholz > second.Buchholz
	}
	if first.HeadToHeadApplied && second.HeadToHeadApplied && first.HeadToHeadPoints != second.HeadToHeadPoints {
		return first.HeadToHeadPoints > second.HeadToHeadPoints
	}
	if first.EffectiveTime != second.EffectiveTime {
		return first.EffectiveTime < second.EffectiveTime
	}
	return first.Seed < second.Seed
}

func swissNormalOrderingError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidNormalOrdering, message)
}
