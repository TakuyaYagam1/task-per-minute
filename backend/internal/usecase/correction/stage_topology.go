package correction

import (
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

// planServerOwnedStageLayout derives the only permitted correction topology
// from the locked normalized Swiss ledger after applying the result successor
// already contained in the correction plan. It never accepts an operator
// layout, Golden identity, or artifact payload.
func planServerOwnedStageLayout(correction Plan, snapshot StageSnapshot) (StageLayout, error) {
	if snapshot.Layout.Mode != StageModePlayoff && snapshot.Layout.Mode != StageModeGolden {
		return StageLayout{}, invalidCorrectionStage("unknown locked stage mode", nil)
	}
	standings, err := correctionStageCanonicalStandings(correction, snapshot.Swiss)
	if err != nil {
		return StageLayout{}, err
	}
	source, err := correctionStageStandingsRevision(correction)
	if err != nil {
		return StageLayout{}, err
	}
	candidates, err := correctionStageGoldenGroups(correction, snapshot.TournamentID, source, standings)
	if err != nil {
		return StageLayout{}, err
	}
	if len(candidates) == 0 {
		return StageLayout{Mode: StageModePlayoff}, nil
	}
	if snapshot.Layout.Mode == StageModeGolden && correctionStageTopologyEqual(
		snapshot.Layout.GoldenGroups, candidates,
	) {
		// A correction can change standings details without changing any
		// impactful tie boundary. Existing immutable Golden evidence remains
		// the authoritative topology in that case.
		return cloneCorrectionStageLayout(snapshot.Layout), nil
	}
	return StageLayout{Mode: StageModeGolden, GoldenGroups: candidates}, nil
}

func correctionStageCanonicalStandings(
	correction Plan,
	authority StageSwissAuthority,
) ([]swissusecase.NormalStanding, error) {
	if !authority.Complete || len(authority.Participants) == 0 || len(authority.Ledger) == 0 {
		return nil, invalidCorrectionStage("missing complete normalized Swiss authority", nil)
	}
	ledger, err := correctionStageApplySwissSuccessor(correction, authority.Ledger)
	if err != nil {
		return nil, err
	}
	rounds, err := resultprojection.BuildCanonicalSwissRounds(ledger)
	if err != nil {
		return nil, invalidCorrectionStage("rebuild corrected Swiss rounds", err)
	}
	participants := make([]uuid.UUID, len(authority.Participants))
	seeds := make([]swissusecase.ParticipantSeed, len(authority.Participants))
	for index, participant := range authority.Participants {
		if participant.ID == uuid.Nil || participant.StableSeed < 1 {
			return nil, invalidCorrectionStage("invalid normalized Swiss participant", nil)
		}
		participants[index] = participant.ID
		seeds[index] = swissusecase.ParticipantSeed{ParticipantID: participant.ID, Seed: participant.StableSeed}
	}
	standings, err := swissusecase.DeriveRoundStandings(participants, seeds, rounds, true)
	if err != nil {
		return nil, invalidCorrectionStage("derive corrected canonical standings", err)
	}
	return standings, nil
}

func correctionStageApplySwissSuccessor(
	correction Plan,
	entries []resultprojection.CanonicalSwissPointLedgerEntry,
) ([]resultprojection.CanonicalSwissPointLedgerEntry, error) {
	series := correction.Series()
	if series.ID == uuid.Nil || correction.Audit().SeriesID != series.ID ||
		series.CurrentResultRevisionID == nil || series.CurrentResultRevisionID.IsZero() {
		return nil, invalidCorrectionStage("correction has no successor Series authority", nil)
	}
	label, winner, err := correctionStageSeriesLedgerOutcome(correction, series)
	if err != nil {
		return nil, err
	}
	updated := make([]resultprojection.CanonicalSwissPointLedgerEntry, len(entries))
	matched := 0
	for index, entry := range entries {
		clone := cloneCorrectionCanonicalSwissLedgerEntry(entry)
		if clone.SourceKind == swissusecase.PointSourceSeries && clone.SourceSeriesID == series.ID {
			matched++
			clone.SeriesResultRevisionID = series.CurrentResultRevisionID.UUID()
			clone.ResultLabel = label
			if winner == nil {
				clone.Points = 0
			} else if clone.ParticipantID == *winner {
				clone.Points = swissusecase.SeriesWinPoints
			} else {
				clone.Points = 0
			}
		}
		updated[index] = clone
	}
	if matched != 2 {
		return nil, invalidCorrectionStage("corrected Series does not have one exact Swiss ledger pair", nil)
	}
	return updated, nil
}

// ApplyServerOwnedSwissSuccessor returns the normalized Swiss ledger used by
// both stage planning and projection materialization for the same correction.
func ApplyServerOwnedSwissSuccessor(
	correction Plan,
	entries []resultprojection.CanonicalSwissPointLedgerEntry,
) ([]resultprojection.CanonicalSwissPointLedgerEntry, error) {
	return correctionStageApplySwissSuccessor(correction, entries)
}

func correctionStageSeriesLedgerOutcome(
	correction Plan,
	series domain.Series,
) (swissusecase.SeriesResultLabel, *uuid.UUID, error) {
	game := correction.GameResultRevision().Revision().Outcome()
	switch series.State {
	case domain.SeriesStateCompleted:
		if series.WinnerID == nil || *series.WinnerID == uuid.Nil {
			return "", nil, invalidCorrectionStage("completed corrected Series has no winner", nil)
		}
		winner := *series.WinnerID
		if game.GameReason == domain.GameResultReasonNoShow {
			return swissusecase.SeriesResultNoShow, &winner, nil
		}
		return swissusecase.SeriesResultPlayed, &winner, nil
	case domain.SeriesStateCancelled:
		return swissusecase.SeriesResultVoid, nil, nil
	default:
		return "", nil, invalidCorrectionStage("corrected Swiss Series is not terminal", nil)
	}
}

func correctionStageGoldenGroups(
	correction Plan,
	tournamentID uuid.UUID,
	source domain.DerivedRevisionID,
	standings []swissusecase.NormalStanding,
) ([]domain.GoldenGroupState, error) {
	if tournamentID == uuid.Nil || source.IsZero() || len(standings) < 4 {
		return nil, invalidCorrectionStage("invalid corrected standings topology authority", nil)
	}
	groups := make([]domain.GoldenGroupState, 0)
	for first := 0; first < len(standings); {
		last := first + 1
		for last < len(standings) && standings[last].Points == standings[first].Points {
			last++
		}
		positionFrom, positionTo := first+1, last
		if last-first > 1 && positionFrom <= 4 {
			members := make([]domain.GoldenMember, last-first)
			for index, standing := range standings[first:last] {
				if standing.Position != positionFrom+index || standing.ParticipantID == uuid.Nil {
					return nil, invalidCorrectionStage("corrected standings are not canonical", nil)
				}
				members[index] = domain.GoldenMember{ParticipantID: standing.ParticipantID}
			}
			identity := correctionStageGoldenTopologyKey(positionFrom, positionTo, members)
			groupID := uuid.NewSHA1(correction.Audit().CommandID, []byte("tournament-correction:golden-group:"+identity))
			revisionID := domain.DerivedRevisionID(uuid.NewSHA1(
				correction.Audit().CommandID, []byte("tournament-correction:golden-group-revision:"+identity),
			))
			group := domain.GoldenGroupState{
				ID: groupID, TournamentID: tournamentID, RevisionID: revisionID,
				SourceProjectionRevisionID: source, PositionFrom: positionFrom, PositionTo: positionTo,
				Members: members,
			}
			if _, err := domain.NewGoldenGroup(group); err != nil {
				return nil, invalidCorrectionStage("build corrected Golden group", err)
			}
			groups = append(groups, group)
		}
		first = last
	}
	return groups, nil
}

func correctionStageGoldenTopologyKey(positionFrom, positionTo int, members []domain.GoldenMember) string {
	ids := make([]string, len(members))
	for index, member := range members {
		ids[index] = member.ParticipantID.String()
	}
	sort.Strings(ids)
	return fmt.Sprintf("%d:%d:%v", positionFrom, positionTo, ids)
}

func correctionStageTopologyEqual(current, candidate []domain.GoldenGroupState) bool {
	if len(current) != len(candidate) {
		return false
	}
	for _, group := range current {
		matched := false
		for _, prospective := range candidate {
			if correctionStageGroupShapeEqual(group, prospective) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func correctionStageGroupShapeEqual(first, second domain.GoldenGroupState) bool {
	if first.PositionFrom != second.PositionFrom || first.PositionTo != second.PositionTo ||
		len(first.Members) != len(second.Members) {
		return false
	}
	firstIDs := correctionStageMemberIDs(first.Members)
	secondIDs := correctionStageMemberIDs(second.Members)
	for index := range firstIDs {
		if firstIDs[index] != secondIDs[index] {
			return false
		}
	}
	return true
}

func correctionStageServerSupersessionIntents(
	correction Plan,
	current, corrected StageLayout,
) []StageGroupSupersessionIntent {
	affected := correctionStageAffectedGroups(current.GoldenGroups, corrected.GoldenGroups)
	intents := make([]StageGroupSupersessionIntent, len(affected))
	for index, group := range affected {
		intents[index] = StageGroupSupersessionIntent{
			GroupID: group.ID,
			RevisionID: domain.DerivedRevisionID(uuid.NewSHA1(
				correction.Audit().CommandID,
				[]byte("tournament-correction:golden-supersession:"+group.ID.String()),
			)),
		}
	}
	return intents
}
