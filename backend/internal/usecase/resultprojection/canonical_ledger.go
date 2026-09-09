package resultprojection

import (
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

// CanonicalSwissPointLedgerEntry is one immutable, server-written Swiss point
// award. RoundRevisionID identifies the locked completed Wave which exposed
// the round. A Series has two complementary entries; a bye has one.
type CanonicalSwissPointLedgerEntry struct {
	RoundID                uuid.UUID
	RoundRevisionID        uuid.UUID
	RoundNumber            int
	SourceKind             swissusecase.PointSourceKind
	SourceSeriesID         uuid.UUID
	SeriesResultRevisionID uuid.UUID
	ByeRevisionID          uuid.UUID
	ResultLabel            swissusecase.SeriesResultLabel
	ParticipantID          uuid.UUID
	OpponentID             *uuid.UUID
	Points                 int
	EffectiveTime          time.Duration
	AcceptedSolveTime      *time.Duration
	StableSeed             int
}

// BuildCanonicalSwissRounds reconstructs the canonical Swiss input from
// normalized ledger rows. It deliberately rejects partial Series pairs and
// mixed round lineage before standings code can consume them.
func BuildCanonicalSwissRounds(entries []CanonicalSwissPointLedgerEntry) ([]swissusecase.Round, error) {
	if len(entries) == 0 {
		return nil, ErrInvalidCanonicalMaterialization
	}
	type roundIdentity struct {
		id         uuid.UUID
		revisionID uuid.UUID
		number     int
	}
	type seriesIdentity struct {
		roundID    uuid.UUID
		seriesID   uuid.UUID
		revisionID uuid.UUID
	}
	roundRows := make(map[roundIdentity][]CanonicalSwissPointLedgerEntry)
	seriesRows := make(map[seriesIdentity][]CanonicalSwissPointLedgerEntry)
	byeRows := make(map[roundIdentity]CanonicalSwissPointLedgerEntry)
	for _, entry := range entries {
		if !validCanonicalSwissLedgerEntry(entry) {
			return nil, ErrInvalidCanonicalMaterialization
		}
		round := roundIdentity{id: entry.RoundID, revisionID: entry.RoundRevisionID, number: entry.RoundNumber}
		roundRows[round] = append(roundRows[round], cloneCanonicalSwissLedgerEntry(entry))
		switch entry.SourceKind {
		case swissusecase.PointSourceSeries:
			identity := seriesIdentity{
				roundID: entry.RoundID, seriesID: entry.SourceSeriesID, revisionID: entry.SeriesResultRevisionID,
			}
			seriesRows[identity] = append(seriesRows[identity], cloneCanonicalSwissLedgerEntry(entry))
		case swissusecase.PointSourceBye:
			if _, exists := byeRows[round]; exists {
				return nil, ErrInvalidCanonicalMaterialization
			}
			byeRows[round] = cloneCanonicalSwissLedgerEntry(entry)
		default:
			return nil, ErrInvalidCanonicalMaterialization
		}
	}

	rounds := make([]swissusecase.Round, 0, len(roundRows))
	for identity := range roundRows {
		rounds = append(rounds, swissusecase.Round{
			RoundID: identity.id, RoundNumber: identity.number, RevisionID: identity.revisionID,
		})
	}
	sort.Slice(rounds, func(first, second int) bool { return rounds[first].RoundNumber < rounds[second].RoundNumber })
	for index := range rounds {
		round := &rounds[index]
		if round.RoundNumber != index+1 {
			return nil, ErrInvalidCanonicalMaterialization
		}
		for identity, rows := range seriesRows {
			if identity.roundID != round.RoundID {
				continue
			}
			result, err := canonicalSwissSeriesResult(rows)
			if err != nil {
				return nil, err
			}
			round.Series = append(round.Series, result)
		}
		sort.Slice(round.Series, func(first, second int) bool {
			return round.Series[first].SeriesID.String() < round.Series[second].SeriesID.String()
		})
		if bye, exists := byeRows[roundIdentity{
			id: round.RoundID, revisionID: round.RevisionID, number: round.RoundNumber,
		}]; exists {
			round.Bye = &swissusecase.ByePointResult{
				RoundID: round.RoundID, RoundNumber: round.RoundNumber,
				ParticipantID: bye.ParticipantID, RevisionID: bye.ByeRevisionID,
			}
		}
	}
	return rounds, nil
}

func validCanonicalSwissLedgerEntry(entry CanonicalSwissPointLedgerEntry) bool {
	if entry.RoundID == uuid.Nil || entry.RoundRevisionID == uuid.Nil || entry.RoundNumber < 1 ||
		entry.ParticipantID == uuid.Nil || entry.StableSeed < 1 || entry.Points < 0 || entry.EffectiveTime < 0 ||
		(entry.AcceptedSolveTime != nil && (*entry.AcceptedSolveTime < 0 || *entry.AcceptedSolveTime > entry.EffectiveTime)) {
		return false
	}
	switch entry.SourceKind {
	case swissusecase.PointSourceSeries:
		return entry.SourceSeriesID != uuid.Nil && entry.SeriesResultRevisionID != uuid.Nil &&
			entry.ByeRevisionID == uuid.Nil && entry.OpponentID != nil && *entry.OpponentID != uuid.Nil &&
			*entry.OpponentID != entry.ParticipantID &&
			(entry.ResultLabel == swissusecase.SeriesResultPlayed || entry.ResultLabel == swissusecase.SeriesResultNoShow ||
				entry.ResultLabel == swissusecase.SeriesResultVoid) && entry.Points <= swissusecase.SeriesWinPoints
	case swissusecase.PointSourceBye:
		return entry.SourceSeriesID == uuid.Nil && entry.SeriesResultRevisionID == uuid.Nil &&
			entry.ByeRevisionID != uuid.Nil && entry.ResultLabel == "" && entry.OpponentID == nil &&
			entry.Points == swissusecase.StandingsByePoints && entry.EffectiveTime == 0 && entry.AcceptedSolveTime == nil
	default:
		return false
	}
}

func canonicalSwissSeriesResult(rows []CanonicalSwissPointLedgerEntry) (swissusecase.SeriesPointResult, error) {
	if len(rows) != 2 {
		return swissusecase.SeriesPointResult{}, ErrInvalidCanonicalMaterialization
	}
	first, second := rows[0], rows[1]
	if first.RoundID != second.RoundID || first.RoundRevisionID != second.RoundRevisionID ||
		first.RoundNumber != second.RoundNumber || first.SourceSeriesID != second.SourceSeriesID ||
		first.SeriesResultRevisionID != second.SeriesResultRevisionID || first.ResultLabel != second.ResultLabel ||
		first.OpponentID == nil || second.OpponentID == nil || *first.OpponentID != second.ParticipantID ||
		*second.OpponentID != first.ParticipantID {
		return swissusecase.SeriesPointResult{}, ErrInvalidCanonicalMaterialization
	}
	if first.ParticipantID.String() > second.ParticipantID.String() {
		first, second = second, first
	}
	var winnerID *uuid.UUID
	switch first.ResultLabel {
	case swissusecase.SeriesResultPlayed, swissusecase.SeriesResultNoShow:
		switch {
		case first.Points == swissusecase.SeriesWinPoints && second.Points == 0:
			winner := first.ParticipantID
			winnerID = &winner
		case second.Points == swissusecase.SeriesWinPoints && first.Points == 0:
			winner := second.ParticipantID
			winnerID = &winner
		default:
			return swissusecase.SeriesPointResult{}, ErrInvalidCanonicalMaterialization
		}
	case swissusecase.SeriesResultVoid:
		if first.Points != 0 || second.Points != 0 {
			return swissusecase.SeriesPointResult{}, ErrInvalidCanonicalMaterialization
		}
	default:
		return swissusecase.SeriesPointResult{}, ErrInvalidCanonicalMaterialization
	}
	return swissusecase.SeriesPointResult{
		RoundID: first.RoundID, RoundNumber: first.RoundNumber, SeriesID: first.SourceSeriesID,
		ResultRevisionID:   domain.OfficialResultRevisionID(first.SeriesResultRevisionID),
		FirstParticipantID: first.ParticipantID, SecondParticipantID: second.ParticipantID, WinnerID: winnerID,
		Label: first.ResultLabel, FirstEffectiveTime: first.EffectiveTime, SecondEffectiveTime: second.EffectiveTime,
		FirstAcceptedSolveTime:  cloneCanonicalDuration(first.AcceptedSolveTime),
		SecondAcceptedSolveTime: cloneCanonicalDuration(second.AcceptedSolveTime),
	}, nil
}

func cloneCanonicalSwissLedgerEntry(entry CanonicalSwissPointLedgerEntry) CanonicalSwissPointLedgerEntry {
	cloned := entry
	if entry.OpponentID != nil {
		opponent := *entry.OpponentID
		cloned.OpponentID = &opponent
	}
	cloned.AcceptedSolveTime = cloneCanonicalDuration(entry.AcceptedSolveTime)
	return cloned
}

func cloneCanonicalDuration(value *time.Duration) *time.Duration {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
