package top4

import (
	"reflect"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection/revision"
	swissusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/swiss"
)

func validateFinalSwissRounds(
	authority finalSwissAuthority,
) ([]swissusecase.Round, []terminalSeriesRecord, error) {
	progression := make([]swissusecase.Round, len(authority.Rounds))
	heads := make([]terminalSeriesRecord, 0)
	seenSeries := make(map[uuid.UUID]struct{})
	seenOfficial := make(map[domain.OfficialResultRevisionID]struct{})
	seenProjections := make(map[domain.DerivedRevisionID]struct{})
	for index, round := range authority.Rounds {
		if round.RoundNumber != index+1 || round.RoundID == uuid.Nil || round.RevisionID == uuid.Nil ||
			!validFinalSwissRoundLock(authority, round) {
			return nil, nil, finalSwissError("round lineage is not contiguous")
		}
		progression[index] = swissusecase.Round{
			RoundID: round.RoundID, RoundNumber: round.RoundNumber, RevisionID: round.RevisionID,
			Series: make([]swissusecase.SeriesPointResult, len(round.Series)),
		}
		for seriesIndex, head := range round.Series {
			if err := validateFinalSwissSeriesHead(authority, round, head); err != nil {
				return nil, nil, err
			}
			if _, duplicate := seenSeries[head.Result.SeriesID]; duplicate {
				return nil, nil, finalSwissError("Series head is duplicated")
			}
			officialID := finalSwissOfficialResultID(head)
			if _, duplicate := seenOfficial[officialID]; duplicate {
				return nil, nil, finalSwissError("official result head is duplicated")
			}
			projectionID := head.Projection.Revision().ID()
			if _, duplicate := seenProjections[projectionID]; duplicate {
				return nil, nil, finalSwissError("Series result projection head is duplicated")
			}
			seenSeries[head.Result.SeriesID] = struct{}{}
			seenOfficial[officialID] = struct{}{}
			seenProjections[projectionID] = struct{}{}
			progression[index].Series[seriesIndex] = swissusecase.CloneSeriesPointResult(head.Result)
			heads = append(heads, cloneFinalSwissSeriesHead(head))
		}
		if round.Bye != nil {
			bye := *round.Bye
			progression[index].Bye = &bye
		}
	}
	return progression, heads, nil
}

func validFinalSwissRoundLock(authority finalSwissAuthority, round finalSwissRound) bool {
	proof := round.LockProof
	if proof.Validate() != nil || proof.TournamentID != authority.TournamentID || proof.Preset != authority.Preset ||
		proof.RoundID != round.RoundID || proof.RoundNumber != round.RoundNumber ||
		proof.SourceProjectionRevisionID != round.RevisionID ||
		!reflect.DeepEqual(proof.RosterParticipantIDs, authority.ParticipantIDs) {
		return false
	}
	if len(proof.Series) != len(round.Series) {
		return false
	}
	for _, head := range round.Series {
		series, found := proofSeries(proof.Series, head.Series.ID)
		if !found || series.FirstParticipantID != head.Series.FirstParticipantID ||
			series.SecondParticipantID != head.Series.SecondParticipantID {
			return false
		}
	}
	wantBye := uuid.Nil
	if round.Bye != nil {
		wantBye = round.Bye.ParticipantID
	}
	return proof.ByeParticipantID == wantBye
}

func proofSeries(series []swissusecase.LockedSeries, seriesID uuid.UUID) (swissusecase.LockedSeries, bool) {
	for _, item := range series {
		if item.SeriesID == seriesID {
			return item, true
		}
	}
	return swissusecase.LockedSeries{}, false
}

func validateFinalSwissSeriesHead(
	authority finalSwissAuthority,
	round finalSwissRound,
	head terminalSeriesRecord,
) error {
	if !validFinalSwissSeriesProjection(authority, round, head) ||
		!validFinalSwissOfficialBinding(authority, round, head) || !validFinalSwissScoreBinding(authority, head) {
		return finalSwissError("Series result is not one exact current terminal head")
	}
	if head.Series.State == domain.SeriesStateCompleted && head.Result.Label == swissusecase.SeriesResultVoid {
		return finalSwissError("completed Series has a void result")
	}
	if head.Series.State == domain.SeriesStateCancelled && head.Result.Label != swissusecase.SeriesResultVoid {
		return finalSwissError("cancelled Series has a decisive result")
	}
	if head.OfficialResult.NoGame != nil && !validFinalSwissNoGamePointShape(head) {
		return finalSwissError("no-game point label or timing is spliced")
	}
	return nil
}

func finalSwissOfficialResultID(head terminalSeriesRecord) domain.OfficialResultRevisionID {
	if head.OfficialResult.NoGame != nil {
		return head.OfficialResult.NoGame.Series.ID
	}
	return head.OfficialResult.Result.ID
}

func validFinalSwissSeriesProjection(
	authority finalSwissAuthority,
	round finalSwissRound,
	head terminalSeriesRecord,
) bool {
	result := head.Result
	series := head.Series
	revision := head.Projection.Revision()
	return series.Validate() == nil && series.State.IsTerminal() &&
		series.ID == result.SeriesID && series.TournamentID == authority.TournamentID &&
		series.FirstParticipantID == result.FirstParticipantID &&
		series.SecondParticipantID == result.SecondParticipantID &&
		result.RoundID == round.RoundID && result.RoundNumber == round.RoundNumber &&
		head.Projection.Validate() == nil && revision.TournamentID() == authority.TournamentID &&
		revision.Artifact() == (domain.ArtifactRef{
			Kind: domain.ArtifactKindSeriesResult, EntityID: result.SeriesID,
		}) && !revision.CreatedAt().After(authority.CreatedAt)
}

func validFinalSwissOfficialBinding(
	authority finalSwissAuthority,
	round finalSwissRound,
	head terminalSeriesRecord,
) bool {
	input := head.OfficialResult
	plan, err := resultprojection.ProjectOfficialResult(input)
	if err != nil || plan.Validate() != nil {
		return false
	}
	if input.NoGame != nil {
		return validFinalSwissNoGameBinding(authority, round, head, plan)
	}
	if !reflect.DeepEqual(input.ResultProjection, head.Projection) {
		return false
	}
	official := input.Result
	series := head.Series
	return series.CurrentResultRevisionID != nil && *series.CurrentResultRevisionID == official.ID &&
		official.ID == head.Result.ResultRevisionID &&
		official.Scope == (resultusecase.OfficialResultScope{
			TournamentID: authority.TournamentID, SeriesID: head.Result.SeriesID,
			Kind: resultusecase.OfficialResultSubjectSeries,
		}) && reflect.DeepEqual(official.SourceProjection, head.Projection.Revision()) &&
		!official.RecordedAt.After(authority.CreatedAt) && official.Outcome.SeriesState == series.State &&
		official.Outcome.SeriesReason.IsLegalFor(series.State) &&
		equalFinalSwissUUIDPointers(official.Outcome.WinnerID, series.WinnerID) &&
		equalFinalSwissUUIDPointers(head.Result.WinnerID, series.WinnerID) &&
		equalFinalSwissScoreRevisionPointers(official.Outcome.ScoreRevisionID, series.CurrentScoreRevisionID)
}

func validFinalSwissNoGameBinding(
	authority finalSwissAuthority,
	round finalSwissRound,
	head terminalSeriesRecord,
	plan resultprojection.OfficialResultProjectionPlan,
) bool {
	recorded := head.OfficialResult.NoGame
	if recorded == nil || recorded.Scope.WaveID != round.LockProof.WaveID ||
		!validFinalSwissNoGameAuthority(authority, head, *recorded) ||
		!validFinalSwissNoGameTerminal(head, *recorded) {
		return false
	}
	return validFinalSwissNoGameProjection(authority, head, *recorded, plan) &&
		validFinalSwissNoGameTopology(head.Series, *recorded)
}

func validFinalSwissNoGameAuthority(
	authority finalSwissAuthority,
	head terminalSeriesRecord,
	recorded resultprojection.RecordedNoGameResult,
) bool {
	wantScope := domain.NormalNoShowScope{
		TournamentID: authority.TournamentID,
		WaveID:       recorded.Scope.WaveID,
		WindowID:     recorded.Scope.WindowID,
		SeriesID:     head.Series.ID,
	}
	return reflect.DeepEqual(recorded.ResultProjection, head.Projection) && recorded.Scope == wantScope &&
		recorded.FirstParticipantID == head.Series.FirstParticipantID &&
		recorded.SecondParticipantID == head.Series.SecondParticipantID &&
		recorded.Format == head.Series.Format && !recorded.ResolvedAt.After(authority.CreatedAt)
}

func validFinalSwissNoGameTerminal(head terminalSeriesRecord, recorded resultprojection.RecordedNoGameResult) bool {
	return recorded.Series.ID == head.Result.ResultRevisionID &&
		recorded.Series.State == head.Series.State &&
		equalFinalSwissUUIDPointers(recorded.Series.WinnerID, head.Series.WinnerID) &&
		!recorded.Score.ID.IsZero() && recorded.Score.Score == head.Series.Score &&
		head.Series.CurrentResultRevisionID != nil &&
		*head.Series.CurrentResultRevisionID == recorded.Series.ID &&
		head.Series.CurrentScoreRevisionID != nil &&
		*head.Series.CurrentScoreRevisionID == recorded.Score.ID
}

func validFinalSwissNoGameProjection(
	authority finalSwissAuthority,
	head terminalSeriesRecord,
	recorded resultprojection.RecordedNoGameResult,
	plan resultprojection.OfficialResultProjectionPlan,
) bool {
	public := plan.Public()
	operator := plan.Operator()
	return public.Subject == resultusecase.OfficialResultSubjectSeries && public.TournamentID == authority.TournamentID &&
		public.SeriesID == head.Series.ID && public.ResolvedAt.Equal(recorded.ResolvedAt) &&
		equalFinalSwissUUIDPointers(public.WinnerID, head.Series.WinnerID) && public.Score != nil &&
		*public.Score == head.Series.Score && operator.ResultRevisionID == recorded.Series.ID &&
		operator.ScoreRevisionID != nil && *operator.ScoreRevisionID == recorded.Score.ID &&
		operator.SourceProjectionRevisionID == head.Projection.Revision().ID() &&
		operator.CommandID == recorded.CommandID
}

func validFinalSwissNoGameTopology(series domain.Series, recorded resultprojection.RecordedNoGameResult) bool {
	if len(series.Slots) != len(recorded.Topology) || len(recorded.Topology) != len(recorded.GameResults) {
		return false
	}
	for index, binding := range recorded.Topology {
		if index >= len(series.Slots) {
			return false
		}
		if !validFinalSwissNoGameAttempt(series.Slots[index], binding, recorded.GameResults[index]) {
			return false
		}
	}
	return true
}

func validFinalSwissNoGameAttempt(
	slot domain.GameSlot,
	binding resultprojection.RecordedNoGameAttempt,
	recordedGame domain.NormalNoShowGameRevision,
) bool {
	if slot.ID != binding.SlotID || slot.SeriesID != binding.SeriesID ||
		slot.Position != binding.SlotPosition || len(slot.Attempts) != 1 {
		return false
	}
	game := slot.Attempts[0]
	return game.ID == binding.GameID && game.SlotID == binding.SlotID &&
		game.AttemptNo == binding.AttemptNo && game.State == recordedGame.State &&
		game.ResultReason == recordedGame.Reason && game.WinnerID == nil &&
		game.ResultRevisionID != nil && *game.ResultRevisionID == binding.ResultRevisionID &&
		binding.ResultRevisionID == recordedGame.ID && recordedGame.GameID == game.ID
}

func validFinalSwissNoGamePointShape(head terminalSeriesRecord) bool {
	if head.Result.FirstEffectiveTime != 0 || head.Result.SecondEffectiveTime != 0 ||
		head.Result.FirstAcceptedSolveTime != nil || head.Result.SecondAcceptedSolveTime != nil {
		return false
	}
	recorded := head.OfficialResult.NoGame
	if recorded == nil {
		return false
	}
	switch recorded.Action {
	case domain.NormalNoShowActionReopenWave:
		return head.Result.Label == swissusecase.SeriesResultNoShow &&
			equalFinalSwissUUIDPointers(head.Result.WinnerID, recorded.Series.WinnerID)
	case domain.NormalNoShowActionPauseWave:
		return head.Result.Label == swissusecase.SeriesResultVoid && head.Result.WinnerID == nil
	default:
		return false
	}
}

func validFinalSwissScoreBinding(authority finalSwissAuthority, head terminalSeriesRecord) bool {
	input := head.OfficialResult
	if input.NoGame != nil {
		return validFinalSwissNoGameScore(head.Series, *input.NoGame)
	}
	return validFinalSwissOrdinaryScore(authority, head.Series, input)
}

func validFinalSwissNoGameScore(series domain.Series, recorded resultprojection.RecordedNoGameResult) bool {
	return series.CurrentScoreRevisionID != nil && recorded.Score.SeriesID == series.ID &&
		recorded.Series.SeriesID == series.ID && recorded.Score.ID == *series.CurrentScoreRevisionID &&
		recorded.Score.Score == series.Score && recorded.Score.RecordedAt.Equal(recorded.ResolvedAt) &&
		recorded.Series.ScoreRevisionID == recorded.Score.ID &&
		recorded.Series.RecordedAt.Equal(recorded.ResolvedAt)
}

func validFinalSwissOrdinaryScore(
	authority finalSwissAuthority,
	series domain.Series,
	input resultprojection.OfficialResultProjectionInput,
) bool {
	if input.Score == nil || input.ScoreProjection == nil || series.CurrentScoreRevisionID == nil {
		return false
	}
	return input.Score.ID == *series.CurrentScoreRevisionID &&
		input.Score.Scope == (resultusecase.SeriesScoreRevisionScope{
			TournamentID: authority.TournamentID, SeriesID: series.ID,
		}) && input.Score.FirstParticipantID == series.FirstParticipantID &&
		input.Score.SecondParticipantID == series.SecondParticipantID &&
		input.Score.Format == series.Format && input.Score.Score == series.Score
}

func equalFinalSwissUUIDPointers(first, second *uuid.UUID) bool {
	return (first == nil && second == nil) ||
		(first != nil && second != nil && *first == *second)
}

func equalFinalSwissScoreRevisionPointers(
	first, second *domain.SeriesScoreRevisionID,
) bool {
	return (first == nil && second == nil) ||
		(first != nil && second != nil && *first == *second)
}
