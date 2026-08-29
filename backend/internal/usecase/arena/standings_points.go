package arena

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const SwissSeriesWinPoints = 1

var ErrInvalidSwissPointLedger = errors.New("invalid Swiss point ledger")

type SwissPointSourceKind string

const (
	SwissPointSourceSeries SwissPointSourceKind = "series"
	SwissPointSourceBye    SwissPointSourceKind = "bye"
)

type SwissSeriesResultLabel string

const (
	SwissSeriesResultPlayed SwissSeriesResultLabel = "played"
	SwissSeriesResultNoShow SwissSeriesResultLabel = "no_show"
	SwissSeriesResultVoid   SwissSeriesResultLabel = "void"
)

type SwissSeriesPointResult struct {
	RoundID                 uuid.UUID
	RoundNumber             int
	SeriesID                uuid.UUID
	ResultRevisionID        domain.ArenaOfficialResultRevisionID
	FirstParticipantID      uuid.UUID
	SecondParticipantID     uuid.UUID
	WinnerID                *uuid.UUID
	Label                   SwissSeriesResultLabel
	FirstEffectiveTime      time.Duration
	SecondEffectiveTime     time.Duration
	FirstAcceptedSolveTime  *time.Duration
	SecondAcceptedSolveTime *time.Duration
}

type SwissByePointResult struct {
	RoundID       uuid.UUID
	RoundNumber   int
	ParticipantID uuid.UUID
	RevisionID    uuid.UUID
}

type SwissPointLedgerInput struct {
	ParticipantIDs []uuid.UUID
	Series         []SwissSeriesPointResult
	Byes           []SwissByePointResult
}

type SwissPointAward struct {
	ParticipantID     uuid.UUID
	OpponentID        uuid.UUID
	Points            int
	EffectiveTime     time.Duration
	AcceptedSolveTime *time.Duration
}

type SwissPointLedgerEntry struct {
	SourceKind  SwissPointSourceKind
	SourceID    uuid.UUID
	RevisionID  uuid.UUID
	RoundID     uuid.UUID
	RoundNumber int
	Label       SwissSeriesResultLabel
	Awards      []SwissPointAward
}

type SwissPointTotal struct {
	ParticipantID     uuid.UUID
	Points            int
	EffectiveTime     time.Duration
	AcceptedSolveTime *time.Duration
}

type SwissPointLedger struct {
	ParticipantIDs []uuid.UUID
	Entries        []SwissPointLedgerEntry
	Totals         []SwissPointTotal
}

func BuildSwissPointLedger(input SwissPointLedgerInput) (SwissPointLedger, error) {
	participants, err := canonicalSwissLedgerParticipants(input.ParticipantIDs)
	if err != nil {
		return SwissPointLedger{}, err
	}
	roster := make(map[uuid.UUID]struct{}, len(participants))
	for _, participantID := range participants {
		roster[participantID] = struct{}{}
	}

	entries := make([]SwissPointLedgerEntry, 0, len(input.Series)+len(input.Byes))
	for _, result := range input.Series {
		entry, buildErr := swissSeriesLedgerEntry(result, roster)
		if buildErr != nil {
			return SwissPointLedger{}, buildErr
		}
		entries = append(entries, entry)
	}
	for _, result := range input.Byes {
		entry, buildErr := swissByeLedgerEntry(result, roster)
		if buildErr != nil {
			return SwissPointLedger{}, buildErr
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return compareSwissLedgerEntries(entries[i], entries[j]) < 0 })
	if err := validateSwissLedgerEntryIdentity(entries); err != nil {
		return SwissPointLedger{}, err
	}

	ledger := SwissPointLedger{
		ParticipantIDs: participants,
		Entries:        cloneSwissPointEntries(entries),
	}
	ledger.Totals, err = calculateSwissPointTotals(participants, entries)
	if err != nil {
		return SwissPointLedger{}, err
	}
	return ledger, nil
}

func rebuildSwissPointLedger(ledger SwissPointLedger) (SwissPointLedger, error) {
	input := SwissPointLedgerInput{
		ParticipantIDs: append([]uuid.UUID(nil), ledger.ParticipantIDs...),
		Series:         make([]SwissSeriesPointResult, 0, len(ledger.Entries)),
		Byes:           make([]SwissByePointResult, 0, len(ledger.Entries)),
	}
	for _, entry := range ledger.Entries {
		switch entry.SourceKind {
		case SwissPointSourceSeries:
			result, err := swissSeriesResultFromLedgerEntry(entry)
			if err != nil {
				return SwissPointLedger{}, err
			}
			input.Series = append(input.Series, result)
		case SwissPointSourceBye:
			if len(entry.Awards) != 1 || entry.SourceID != entry.RoundID ||
				entry.Label != "" || entry.Awards[0].Points != SwissByePoints ||
				entry.Awards[0].OpponentID != uuid.Nil || entry.Awards[0].EffectiveTime != 0 ||
				entry.Awards[0].AcceptedSolveTime != nil {
				return SwissPointLedger{}, swissPointLedgerError("bye entry is malformed")
			}
			input.Byes = append(input.Byes, SwissByePointResult{
				RoundID: entry.RoundID, RoundNumber: entry.RoundNumber,
				ParticipantID: entry.Awards[0].ParticipantID, RevisionID: entry.RevisionID,
			})
		default:
			return SwissPointLedger{}, swissPointLedgerError("entry has an unknown source kind")
		}
	}
	return BuildSwissPointLedger(input)
}

func swissSeriesResultFromLedgerEntry(
	entry SwissPointLedgerEntry,
) (SwissSeriesPointResult, error) {
	if len(entry.Awards) != 2 {
		return SwissSeriesPointResult{}, swissPointLedgerError("Series entry has invalid awards")
	}
	first := entry.Awards[0]
	second := entry.Awards[1]
	var winnerID *uuid.UUID
	switch entry.Label {
	case SwissSeriesResultPlayed, SwissSeriesResultNoShow:
		switch {
		case first.Points == SwissSeriesWinPoints && second.Points == 0:
			winner := first.ParticipantID
			winnerID = &winner
		case second.Points == SwissSeriesWinPoints && first.Points == 0:
			winner := second.ParticipantID
			winnerID = &winner
		default:
			return SwissSeriesPointResult{}, swissPointLedgerError("decisive Series entry has invalid points")
		}
	case SwissSeriesResultVoid:
		if first.Points != 0 || second.Points != 0 {
			return SwissSeriesPointResult{}, swissPointLedgerError("void Series entry has points")
		}
	default:
		return SwissSeriesPointResult{}, swissPointLedgerError("Series entry has an unknown label")
	}
	return SwissSeriesPointResult{
		RoundID: entry.RoundID, RoundNumber: entry.RoundNumber, SeriesID: entry.SourceID,
		ResultRevisionID:   domain.ArenaOfficialResultRevisionID(entry.RevisionID),
		FirstParticipantID: first.ParticipantID, SecondParticipantID: second.ParticipantID,
		WinnerID: winnerID, Label: entry.Label,
		FirstEffectiveTime: first.EffectiveTime, SecondEffectiveTime: second.EffectiveTime,
		FirstAcceptedSolveTime:  cloneDurationPointer(first.AcceptedSolveTime),
		SecondAcceptedSolveTime: cloneDurationPointer(second.AcceptedSolveTime),
	}, nil
}

func canonicalSwissLedgerParticipants(participantIDs []uuid.UUID) ([]uuid.UUID, error) {
	if len(participantIDs) < domain.ArenaMinParticipants || len(participantIDs) > domain.ArenaMaxParticipants {
		return nil, swissPointLedgerError("invalid roster size")
	}
	participants := append([]uuid.UUID(nil), participantIDs...)
	slices.SortFunc(participants, compareUUID)
	for index, participantID := range participants {
		if participantID == uuid.Nil || (index > 0 && participantID == participants[index-1]) {
			return nil, swissPointLedgerError("roster identity is missing or duplicated")
		}
	}
	return participants, nil
}

func swissSeriesLedgerEntry(
	result SwissSeriesPointResult,
	roster map[uuid.UUID]struct{},
) (SwissPointLedgerEntry, error) {
	if result.RoundID == uuid.Nil || result.RoundNumber < 1 || result.SeriesID == uuid.Nil ||
		result.ResultRevisionID.IsZero() || result.FirstParticipantID == uuid.Nil ||
		result.SecondParticipantID == uuid.Nil || result.FirstParticipantID == result.SecondParticipantID {
		return SwissPointLedgerEntry{}, swissPointLedgerError("Series result has invalid identity")
	}
	if _, exists := roster[result.FirstParticipantID]; !exists {
		return SwissPointLedgerEntry{}, swissPointLedgerError("Series result contains a foreign participant")
	}
	if _, exists := roster[result.SecondParticipantID]; !exists {
		return SwissPointLedgerEntry{}, swissPointLedgerError("Series result contains a foreign participant")
	}
	if err := validateSwissSeriesResultLabel(result); err != nil {
		return SwissPointLedgerEntry{}, err
	}
	if err := validateSwissPointTimes(
		result.FirstEffectiveTime, result.FirstAcceptedSolveTime,
		result.SecondEffectiveTime, result.SecondAcceptedSolveTime,
	); err != nil {
		return SwissPointLedgerEntry{}, err
	}

	firstPoints := 0
	secondPoints := 0
	if result.WinnerID != nil {
		if *result.WinnerID == result.FirstParticipantID {
			firstPoints = SwissSeriesWinPoints
		} else {
			secondPoints = SwissSeriesWinPoints
		}
	}
	awards := []SwissPointAward{
		{
			ParticipantID: result.FirstParticipantID, OpponentID: result.SecondParticipantID,
			Points: firstPoints, EffectiveTime: result.FirstEffectiveTime,
			AcceptedSolveTime: cloneDurationPointer(result.FirstAcceptedSolveTime),
		},
		{
			ParticipantID: result.SecondParticipantID, OpponentID: result.FirstParticipantID,
			Points: secondPoints, EffectiveTime: result.SecondEffectiveTime,
			AcceptedSolveTime: cloneDurationPointer(result.SecondAcceptedSolveTime),
		},
	}
	slices.SortFunc(awards, func(first, second SwissPointAward) int {
		return compareUUID(first.ParticipantID, second.ParticipantID)
	})
	return SwissPointLedgerEntry{
		SourceKind: SwissPointSourceSeries, SourceID: result.SeriesID,
		RevisionID: result.ResultRevisionID.UUID(), RoundID: result.RoundID,
		RoundNumber: result.RoundNumber, Label: result.Label, Awards: awards,
	}, nil
}

func validateSwissSeriesResultLabel(result SwissSeriesPointResult) error {
	switch result.Label {
	case SwissSeriesResultPlayed, SwissSeriesResultNoShow:
		if result.WinnerID == nil ||
			(*result.WinnerID != result.FirstParticipantID && *result.WinnerID != result.SecondParticipantID) {
			return swissPointLedgerError("decisive Series result has no valid winner")
		}
	case SwissSeriesResultVoid:
		if result.WinnerID != nil {
			return swissPointLedgerError("void Series result has a winner")
		}
	default:
		return swissPointLedgerError("Series result has an unknown label")
	}
	return nil
}

func validateSwissPointTimes(
	firstEffective time.Duration,
	firstAccepted *time.Duration,
	secondEffective time.Duration,
	secondAccepted *time.Duration,
) error {
	if !validSwissPointTime(firstEffective, firstAccepted) ||
		!validSwissPointTime(secondEffective, secondAccepted) {
		return swissPointLedgerError("Series result has invalid time evidence")
	}
	return nil
}

func validSwissPointTime(effective time.Duration, accepted *time.Duration) bool {
	return effective >= 0 && (accepted == nil || (*accepted >= 0 && *accepted <= effective))
}

func swissByeLedgerEntry(
	result SwissByePointResult,
	roster map[uuid.UUID]struct{},
) (SwissPointLedgerEntry, error) {
	if result.RoundID == uuid.Nil || result.RoundNumber < 1 || result.ParticipantID == uuid.Nil ||
		result.RevisionID == uuid.Nil {
		return SwissPointLedgerEntry{}, swissPointLedgerError("bye result has invalid identity")
	}
	if _, exists := roster[result.ParticipantID]; !exists {
		return SwissPointLedgerEntry{}, swissPointLedgerError("bye result contains a foreign participant")
	}
	return SwissPointLedgerEntry{
		SourceKind: SwissPointSourceBye, SourceID: result.RoundID, RevisionID: result.RevisionID,
		RoundID: result.RoundID, RoundNumber: result.RoundNumber,
		Awards: []SwissPointAward{{ParticipantID: result.ParticipantID, Points: SwissByePoints}},
	}, nil
}

func validateSwissLedgerEntryIdentity(entries []SwissPointLedgerEntry) error {
	type sourceKey struct {
		kind SwissPointSourceKind
		id   uuid.UUID
	}
	sources := make(map[sourceKey]struct{}, len(entries))
	revisions := make(map[uuid.UUID]struct{}, len(entries))
	rounds := make(map[uuid.UUID]int, len(entries))
	for _, entry := range entries {
		key := sourceKey{kind: entry.SourceKind, id: entry.SourceID}
		if _, duplicate := sources[key]; duplicate {
			return swissPointLedgerError("official source is duplicated")
		}
		if _, duplicate := revisions[entry.RevisionID]; duplicate {
			return swissPointLedgerError("official revision is duplicated")
		}
		if roundNumber, exists := rounds[entry.RoundID]; exists && roundNumber != entry.RoundNumber {
			return swissPointLedgerError("round identity has conflicting numbers")
		}
		sources[key] = struct{}{}
		revisions[entry.RevisionID] = struct{}{}
		rounds[entry.RoundID] = entry.RoundNumber
	}
	return nil
}

func calculateSwissPointTotals(
	participants []uuid.UUID,
	entries []SwissPointLedgerEntry,
) ([]SwissPointTotal, error) {
	totalsByID := make(map[uuid.UUID]*SwissPointTotal, len(participants))
	totals := make([]SwissPointTotal, len(participants))
	for index, participantID := range participants {
		totals[index].ParticipantID = participantID
		totalsByID[participantID] = &totals[index]
	}
	for _, entry := range entries {
		for _, award := range entry.Awards {
			total := totalsByID[award.ParticipantID]
			if total == nil || total.Points > math.MaxInt-award.Points ||
				award.EffectiveTime > time.Duration(math.MaxInt64)-total.EffectiveTime {
				return nil, swissPointLedgerError("point or time total overflows")
			}
			total.Points += award.Points
			total.EffectiveTime += award.EffectiveTime
			if award.AcceptedSolveTime != nil {
				if total.AcceptedSolveTime == nil {
					total.AcceptedSolveTime = new(time.Duration)
				}
				if *award.AcceptedSolveTime > time.Duration(math.MaxInt64)-*total.AcceptedSolveTime {
					return nil, swissPointLedgerError("accepted solve time total overflows")
				}
				*total.AcceptedSolveTime += *award.AcceptedSolveTime
			}
		}
	}
	return totals, nil
}

func compareSwissLedgerEntries(first, second SwissPointLedgerEntry) int {
	if first.RoundNumber != second.RoundNumber {
		return first.RoundNumber - second.RoundNumber
	}
	if first.SourceKind != second.SourceKind {
		if first.SourceKind == SwissPointSourceSeries {
			return -1
		}
		return 1
	}
	return compareUUID(first.SourceID, second.SourceID)
}

func compareUUID(first, second uuid.UUID) int {
	return bytes.Compare(first[:], second[:])
}

func cloneSwissPointEntries(entries []SwissPointLedgerEntry) []SwissPointLedgerEntry {
	cloned := make([]SwissPointLedgerEntry, len(entries))
	for index, entry := range entries {
		cloned[index] = entry
		cloned[index].Awards = make([]SwissPointAward, len(entry.Awards))
		for awardIndex, award := range entry.Awards {
			cloned[index].Awards[awardIndex] = award
			cloned[index].Awards[awardIndex].AcceptedSolveTime = cloneDurationPointer(award.AcceptedSolveTime)
		}
	}
	return cloned
}

func cloneDurationPointer(value *time.Duration) *time.Duration {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func swissPointLedgerError(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidSwissPointLedger, message)
}
