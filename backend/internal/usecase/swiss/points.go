package swiss

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

const (
	SeriesWinPoints    = 1
	StandingsByePoints = 1
)

var ErrInvalidPointLedger = errors.New("invalid Swiss point ledger")

type PointSourceKind string

const (
	PointSourceSeries PointSourceKind = "series"
	PointSourceBye    PointSourceKind = "bye"
)

type SeriesResultLabel string

const (
	SeriesResultPlayed SeriesResultLabel = "played"
	SeriesResultNoShow SeriesResultLabel = "no_show"
	SeriesResultVoid   SeriesResultLabel = "void"
)

type SeriesPointResult struct {
	RoundID                 uuid.UUID
	RoundNumber             int
	SeriesID                uuid.UUID
	ResultRevisionID        domain.OfficialResultRevisionID
	FirstParticipantID      uuid.UUID
	SecondParticipantID     uuid.UUID
	WinnerID                *uuid.UUID
	Label                   SeriesResultLabel
	FirstEffectiveTime      time.Duration
	SecondEffectiveTime     time.Duration
	FirstAcceptedSolveTime  *time.Duration
	SecondAcceptedSolveTime *time.Duration
}

// CloneSeriesPointResult returns an independent copy of a result value.
func CloneSeriesPointResult(result SeriesPointResult) SeriesPointResult {
	cloned := result
	if result.WinnerID != nil {
		winner := *result.WinnerID
		cloned.WinnerID = &winner
	}
	cloned.FirstAcceptedSolveTime = cloneDurationPointer(result.FirstAcceptedSolveTime)
	cloned.SecondAcceptedSolveTime = cloneDurationPointer(result.SecondAcceptedSolveTime)
	return cloned
}

type ByePointResult struct {
	RoundID       uuid.UUID
	RoundNumber   int
	ParticipantID uuid.UUID
	RevisionID    uuid.UUID
}

type PointLedgerInput struct {
	ParticipantIDs []uuid.UUID
	Series         []SeriesPointResult
	Byes           []ByePointResult
}

type PointAward struct {
	ParticipantID     uuid.UUID
	OpponentID        uuid.UUID
	Points            int
	EffectiveTime     time.Duration
	AcceptedSolveTime *time.Duration
}

type PointLedgerEntry struct {
	SourceKind  PointSourceKind
	SourceID    uuid.UUID
	RevisionID  uuid.UUID
	RoundID     uuid.UUID
	RoundNumber int
	Label       SeriesResultLabel
	Awards      []PointAward
}

type PointTotal struct {
	ParticipantID     uuid.UUID
	Points            int
	EffectiveTime     time.Duration
	AcceptedSolveTime *time.Duration
}

type PointLedger struct {
	ParticipantIDs []uuid.UUID
	Entries        []PointLedgerEntry
	Totals         []PointTotal
}

func BuildPointLedger(input PointLedgerInput) (PointLedger, error) {
	participants, err := CanonicalLedgerParticipants(input.ParticipantIDs)
	if err != nil {
		return PointLedger{}, err
	}
	roster := make(map[uuid.UUID]struct{}, len(participants))
	for _, participantID := range participants {
		roster[participantID] = struct{}{}
	}

	entries := make([]PointLedgerEntry, 0, len(input.Series)+len(input.Byes))
	for _, result := range input.Series {
		entry, buildErr := swissSeriesLedgerEntry(result, roster)
		if buildErr != nil {
			return PointLedger{}, buildErr
		}
		entries = append(entries, entry)
	}
	for _, result := range input.Byes {
		entry, buildErr := swissByeLedgerEntry(result, roster)
		if buildErr != nil {
			return PointLedger{}, buildErr
		}
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return compareSwissLedgerEntries(entries[i], entries[j]) < 0 })
	if err := validateSwissLedgerEntryIdentity(entries); err != nil {
		return PointLedger{}, err
	}

	ledger := PointLedger{
		ParticipantIDs: participants,
		Entries:        cloneSwissPointEntries(entries),
	}
	ledger.Totals, err = calculateSwissPointTotals(participants, entries)
	if err != nil {
		return PointLedger{}, err
	}
	return ledger, nil
}

func rebuildSwissPointLedger(ledger PointLedger) (PointLedger, error) {
	input := PointLedgerInput{
		ParticipantIDs: append([]uuid.UUID(nil), ledger.ParticipantIDs...),
		Series:         make([]SeriesPointResult, 0, len(ledger.Entries)),
		Byes:           make([]ByePointResult, 0, len(ledger.Entries)),
	}
	for _, entry := range ledger.Entries {
		switch entry.SourceKind {
		case PointSourceSeries:
			result, err := swissSeriesResultFromLedgerEntry(entry)
			if err != nil {
				return PointLedger{}, err
			}
			input.Series = append(input.Series, result)
		case PointSourceBye:
			if len(entry.Awards) != 1 || entry.SourceID != entry.RoundID ||
				entry.Label != "" || entry.Awards[0].Points != StandingsByePoints ||
				entry.Awards[0].OpponentID != uuid.Nil || entry.Awards[0].EffectiveTime != 0 ||
				entry.Awards[0].AcceptedSolveTime != nil {
				return PointLedger{}, swissPointLedgerError("bye entry is malformed")
			}
			input.Byes = append(input.Byes, ByePointResult{
				RoundID: entry.RoundID, RoundNumber: entry.RoundNumber,
				ParticipantID: entry.Awards[0].ParticipantID, RevisionID: entry.RevisionID,
			})
		default:
			return PointLedger{}, swissPointLedgerError("entry has an unknown source kind")
		}
	}
	return BuildPointLedger(input)
}

func swissSeriesResultFromLedgerEntry(
	entry PointLedgerEntry,
) (SeriesPointResult, error) {
	if len(entry.Awards) != 2 {
		return SeriesPointResult{}, swissPointLedgerError("Series entry has invalid awards")
	}
	first := entry.Awards[0]
	second := entry.Awards[1]
	var winnerID *uuid.UUID
	switch entry.Label {
	case SeriesResultPlayed, SeriesResultNoShow:
		switch {
		case first.Points == SeriesWinPoints && second.Points == 0:
			winner := first.ParticipantID
			winnerID = &winner
		case second.Points == SeriesWinPoints && first.Points == 0:
			winner := second.ParticipantID
			winnerID = &winner
		default:
			return SeriesPointResult{}, swissPointLedgerError("decisive Series entry has invalid points")
		}
	case SeriesResultVoid:
		if first.Points != 0 || second.Points != 0 {
			return SeriesPointResult{}, swissPointLedgerError("void Series entry has points")
		}
	default:
		return SeriesPointResult{}, swissPointLedgerError("Series entry has an unknown label")
	}
	return SeriesPointResult{
		RoundID: entry.RoundID, RoundNumber: entry.RoundNumber, SeriesID: entry.SourceID,
		ResultRevisionID:   domain.OfficialResultRevisionID(entry.RevisionID),
		FirstParticipantID: first.ParticipantID, SecondParticipantID: second.ParticipantID,
		WinnerID: winnerID, Label: entry.Label,
		FirstEffectiveTime: first.EffectiveTime, SecondEffectiveTime: second.EffectiveTime,
		FirstAcceptedSolveTime:  cloneDurationPointer(first.AcceptedSolveTime),
		SecondAcceptedSolveTime: cloneDurationPointer(second.AcceptedSolveTime),
	}, nil
}

func CanonicalLedgerParticipants(participantIDs []uuid.UUID) ([]uuid.UUID, error) {
	if len(participantIDs) < domain.TournamentMinParticipants || len(participantIDs) > domain.TournamentMaxParticipants {
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
	result SeriesPointResult,
	roster map[uuid.UUID]struct{},
) (PointLedgerEntry, error) {
	if result.RoundID == uuid.Nil || result.RoundNumber < 1 || result.SeriesID == uuid.Nil ||
		result.ResultRevisionID.IsZero() || result.FirstParticipantID == uuid.Nil ||
		result.SecondParticipantID == uuid.Nil || result.FirstParticipantID == result.SecondParticipantID {
		return PointLedgerEntry{}, swissPointLedgerError("Series result has invalid identity")
	}
	if _, exists := roster[result.FirstParticipantID]; !exists {
		return PointLedgerEntry{}, swissPointLedgerError("Series result contains a foreign participant")
	}
	if _, exists := roster[result.SecondParticipantID]; !exists {
		return PointLedgerEntry{}, swissPointLedgerError("Series result contains a foreign participant")
	}
	if err := validateSwissSeriesResultLabel(result); err != nil {
		return PointLedgerEntry{}, err
	}
	if err := validateSwissPointTimes(
		result.FirstEffectiveTime, result.FirstAcceptedSolveTime,
		result.SecondEffectiveTime, result.SecondAcceptedSolveTime,
	); err != nil {
		return PointLedgerEntry{}, err
	}

	firstPoints := 0
	secondPoints := 0
	if result.WinnerID != nil {
		if *result.WinnerID == result.FirstParticipantID {
			firstPoints = SeriesWinPoints
		} else {
			secondPoints = SeriesWinPoints
		}
	}
	awards := []PointAward{
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
	slices.SortFunc(awards, func(first, second PointAward) int {
		return compareUUID(first.ParticipantID, second.ParticipantID)
	})
	return PointLedgerEntry{
		SourceKind: PointSourceSeries, SourceID: result.SeriesID,
		RevisionID: result.ResultRevisionID.UUID(), RoundID: result.RoundID,
		RoundNumber: result.RoundNumber, Label: result.Label, Awards: awards,
	}, nil
}

func validateSwissSeriesResultLabel(result SeriesPointResult) error {
	switch result.Label {
	case SeriesResultPlayed, SeriesResultNoShow:
		if result.WinnerID == nil ||
			(*result.WinnerID != result.FirstParticipantID && *result.WinnerID != result.SecondParticipantID) {
			return swissPointLedgerError("decisive Series result has no valid winner")
		}
	case SeriesResultVoid:
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
	result ByePointResult,
	roster map[uuid.UUID]struct{},
) (PointLedgerEntry, error) {
	if result.RoundID == uuid.Nil || result.RoundNumber < 1 || result.ParticipantID == uuid.Nil ||
		result.RevisionID == uuid.Nil {
		return PointLedgerEntry{}, swissPointLedgerError("bye result has invalid identity")
	}
	if _, exists := roster[result.ParticipantID]; !exists {
		return PointLedgerEntry{}, swissPointLedgerError("bye result contains a foreign participant")
	}
	return PointLedgerEntry{
		SourceKind: PointSourceBye, SourceID: result.RoundID, RevisionID: result.RevisionID,
		RoundID: result.RoundID, RoundNumber: result.RoundNumber,
		Awards: []PointAward{{ParticipantID: result.ParticipantID, Points: StandingsByePoints}},
	}, nil
}

func validateSwissLedgerEntryIdentity(entries []PointLedgerEntry) error {
	type sourceKey struct {
		kind PointSourceKind
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
	entries []PointLedgerEntry,
) ([]PointTotal, error) {
	totalsByID := make(map[uuid.UUID]*PointTotal, len(participants))
	totals := make([]PointTotal, len(participants))
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

func compareSwissLedgerEntries(first, second PointLedgerEntry) int {
	if first.RoundNumber != second.RoundNumber {
		return first.RoundNumber - second.RoundNumber
	}
	if first.SourceKind != second.SourceKind {
		if first.SourceKind == PointSourceSeries {
			return -1
		}
		return 1
	}
	return compareUUID(first.SourceID, second.SourceID)
}

func compareUUID(first, second uuid.UUID) int {
	return bytes.Compare(first[:], second[:])
}

func cloneSwissPointEntries(entries []PointLedgerEntry) []PointLedgerEntry {
	cloned := make([]PointLedgerEntry, len(entries))
	for index, entry := range entries {
		cloned[index] = entry
		cloned[index].Awards = make([]PointAward, len(entry.Awards))
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
	return fmt.Errorf("%w: %s", ErrInvalidPointLedger, message)
}
