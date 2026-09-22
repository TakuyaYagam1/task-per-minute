package v1

import (
	"errors"
	"math"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentsnapshot "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

func (s *Server) GetPublicTournament(w http.ResponseWriter, r *http.Request, tournamentID api.TournamentId) {
	view, ok := s.readPublicTournamentSnapshot(w, r, tournamentID)
	if !ok {
		return
	}
	payload, err := publicTournamentResponse(view)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) GetPublicScoreboard(w http.ResponseWriter, r *http.Request, tournamentID api.TournamentId) {
	view, ok := s.readPublicTournamentSnapshot(w, r, tournamentID)
	if !ok {
		return
	}
	payload, err := publicScoreboardResponse(view)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) GetPublicBracket(w http.ResponseWriter, r *http.Request, tournamentID api.TournamentId) {
	view, ok := s.readPublicTournamentSnapshot(w, r, tournamentID)
	if !ok {
		return
	}
	payload, err := publicBracketResponse(view)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) GetPublicLiveDraft(w http.ResponseWriter, r *http.Request, tournamentID api.TournamentId) {
	view, ok := s.readPublicTournamentSnapshot(w, r, tournamentID)
	if !ok {
		return
	}
	payload, err := publicLiveDraftResponse(view)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	if payload == nil {
		errmap.HandleError(w, r, domain.ErrTournamentProjectionNotFound)
		return
	}
	response.WriteJSON(w, http.StatusOK, *payload)
}

func (s *Server) GetPublicSnapshot(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.TournamentId,
	params api.GetPublicSnapshotParams,
) {
	view, ok := s.readPublicTournamentSnapshotWithCursor(w, r, tournamentID, params.Cursor)
	if !ok {
		return
	}
	payload, err := publicRecoverySnapshotResponse(view)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) readPublicTournamentSnapshot(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID uuid.UUID,
) (tournamentsnapshot.PublicSnapshotView, bool) {
	if s == nil || s.tournamentSnapshots == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return tournamentsnapshot.PublicSnapshotView{}, false
	}
	view, err := s.tournamentSnapshots.PublicSnapshot(r.Context(), tournamentsnapshot.PublicSnapshotQuery{
		TournamentID: tournamentID,
	})
	if err != nil {
		errmap.HandleError(w, r, err)
		return tournamentsnapshot.PublicSnapshotView{}, false
	}
	return view, true
}

func (s *Server) readPublicTournamentSnapshotWithCursor(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID uuid.UUID,
	requestedCursor *api.PublicRecoveryCursor,
) (tournamentsnapshot.PublicSnapshotView, bool) {
	if s == nil || s.tournamentSnapshots == nil {
		writePublicSnapshotError(w, r, domain.ErrInternal)
		return tournamentsnapshot.PublicSnapshotView{}, false
	}
	view, err := s.tournamentSnapshots.PublicSnapshot(r.Context(), tournamentsnapshot.PublicSnapshotQuery{
		TournamentID: tournamentID,
		Cursor:       publicSnapshotCursor(requestedCursor),
	})
	if err != nil {
		writePublicSnapshotError(w, r, err)
		return tournamentsnapshot.PublicSnapshotView{}, false
	}
	return view, true
}

func writePublicSnapshotError(w http.ResponseWriter, r *http.Request, err error) {
	var conflict *tournamentsnapshot.PublicSnapshotCursorConflictError
	if errors.As(err, &conflict) {
		if conflict == nil || conflict.TournamentID == uuid.Nil ||
			conflict.RequestedProjectionRevision < 1 || conflict.RequestedEventSequence < 0 ||
			conflict.CurrentProjectionRevision < 1 || conflict.CurrentEventSequence < 0 {
			errmap.HandleError(w, r, domain.ErrInternal)
			return
		}
		detail, instance, requestID := tournamentProblemContext(r, "public recovery cursor conflict")
		payload := api.PublicRecoveryCursorConflictProblem{
			Type: "about:blank", Title: http.StatusText(http.StatusConflict),
			Status: int32(http.StatusConflict), Detail: &detail, Instance: &instance, RequestId: &requestID,
			RequestedCursor: api.PublicRecoveryCursor{
				ProjectionRevision: conflict.RequestedProjectionRevision,
				EventSequence:      conflict.RequestedEventSequence,
			},
			CurrentCursor: api.PublicRecoveryCursor{
				ProjectionRevision: conflict.CurrentProjectionRevision,
				EventSequence:      conflict.CurrentEventSequence,
			},
		}
		response.WriteProblem(w, http.StatusConflict, payload)
		return
	}
	errmap.HandleError(w, r, err)
}

func publicSnapshotCursor(cursor *api.PublicRecoveryCursor) *tournamentsnapshot.SnapshotCursor {
	if cursor == nil {
		return nil
	}
	return &tournamentsnapshot.SnapshotCursor{
		ProjectionRevision: cursor.ProjectionRevision,
		EventSequence:      cursor.EventSequence,
	}
}

func publicRecoverySnapshotResponse(
	view tournamentsnapshot.PublicSnapshotView,
) (api.PublicRecoverySnapshot, error) {
	tournament, err := publicTournamentResponse(view)
	if err != nil {
		return api.PublicRecoverySnapshot{}, err
	}
	scoreboard, err := publicScoreboardResponse(view)
	if err != nil {
		return api.PublicRecoverySnapshot{}, err
	}
	bracket, err := publicBracketResponse(view)
	if err != nil {
		return api.PublicRecoverySnapshot{}, err
	}
	swissRounds, err := publicSwissRoundsResponse(view)
	if err != nil {
		return api.PublicRecoverySnapshot{}, err
	}
	liveSeries, err := publicLiveSeriesResponse(view)
	if err != nil {
		return api.PublicRecoverySnapshot{}, err
	}
	officialResults, err := publicOfficialResultsResponse(view)
	if err != nil {
		return api.PublicRecoverySnapshot{}, err
	}
	draft, err := publicLiveDraftResponse(view)
	if err != nil {
		return api.PublicRecoverySnapshot{}, err
	}
	if view.Cursor.EventSequence < 0 {
		return api.PublicRecoverySnapshot{}, domain.ErrInternal
	}
	return api.PublicRecoverySnapshot{
		Tournament:      tournament,
		Scoreboard:      scoreboard,
		Bracket:         bracket,
		SwissRounds:     swissRounds,
		LiveSeries:      liveSeries,
		OfficialResults: officialResults,
		LiveDraft:       draft,
		NextCursor: api.PublicRecoveryCursor{
			ProjectionRevision: view.Cursor.ProjectionRevision,
			EventSequence:      view.Cursor.EventSequence,
		},
	}, nil
}

func publicTournamentResponse(
	view tournamentsnapshot.PublicSnapshotView,
) (api.PublicTournamentResponse, error) {
	preset := api.TournamentPreset(view.Tournament.Preset)
	state := api.TournamentState(view.Tournament.State)
	if view.Tournament.TournamentID == uuid.Nil || view.Cursor.ProjectionRevision < 1 ||
		view.Tournament.RosterSize < 0 || view.Tournament.RosterSize > math.MaxInt32 ||
		!preset.Valid() || !state.Valid() {
		return api.PublicTournamentResponse{}, domain.ErrInternal
	}
	return api.PublicTournamentResponse{
		TournamentId:       view.Tournament.TournamentID,
		ProjectionRevision: view.Cursor.ProjectionRevision,
		Preset:             preset,
		State:              state,
		RosterSize:         int32(view.Tournament.RosterSize),
		StartedAt:          cloneTimePointer(view.Tournament.StartedAt),
		FinishedAt:         cloneTimePointer(view.Tournament.FinishedAt),
	}, nil
}

func publicScoreboardResponse(
	view tournamentsnapshot.PublicSnapshotView,
) (api.PublicScoreboardResponse, error) {
	entries := make([]api.PublicScoreboardEntry, len(view.Scoreboard))
	for index, item := range view.Scoreboard {
		entry, err := publicScoreboardEntryResponse(item)
		if err != nil {
			return api.PublicScoreboardResponse{}, domain.ErrInternal
		}
		entries[index] = entry
	}
	return api.PublicScoreboardResponse{
		TournamentId:       view.Tournament.TournamentID,
		ProjectionRevision: view.Cursor.ProjectionRevision,
		Entries:            entries,
	}, nil
}

func publicScoreboardEntryResponse(
	item tournamentsnapshot.PublicScoreboardEntryView,
) (api.PublicScoreboardEntry, error) {
	qualificationStatus := api.PublicQualificationStatus(item.QualificationStatus)
	if !publicScoreboardIntegerRange(item) || item.EffectiveTimeMS < 0 ||
		strings.TrimSpace(item.DisplayName) == "" || !qualificationStatus.Valid() {
		return api.PublicScoreboardEntry{}, domain.ErrInternal
	}
	return api.PublicScoreboardEntry{
		Rank: response.IntToInt32(item.Rank), DisplayName: item.DisplayName, Points: response.IntToInt32(item.Points),
		Wins: response.IntToInt32(item.Wins), Losses: response.IntToInt32(item.Losses),
		ByeCount: response.IntToInt32(item.ByeCount), Buchholz: response.IntToInt32(item.Buchholz),
		EffectiveTimeMs: item.EffectiveTimeMS,
		ProvisionalTie:  item.ProvisionalTie, QualificationStatus: qualificationStatus,
	}, nil
}

func publicScoreboardIntegerRange(item tournamentsnapshot.PublicScoreboardEntryView) bool {
	if item.Rank < 1 || item.Rank > math.MaxInt32 {
		return false
	}
	for _, value := range []int{item.Points, item.Wins, item.Losses, item.ByeCount, item.Buchholz} {
		if value < 0 || value > math.MaxInt32 {
			return false
		}
	}
	return true
}

//nolint:gocyclo // The public boundary validates topology, enum, score, name, winner, and timestamp invariants together.
func publicBracketResponse(
	view tournamentsnapshot.PublicSnapshotView,
) (api.PublicBracketResponse, error) {
	if !publicBracketTopologyValid(view.Bracket) {
		return api.PublicBracketResponse{}, domain.ErrInternal
	}
	matches := make([]api.PublicBracketMatch, len(view.Bracket))
	for index, item := range view.Bracket {
		stage := api.PublicBracketMatchStage(item.Stage)
		format := api.SeriesFormat(item.Format)
		state := api.SeriesState(item.State)
		if item.Position < 1 || item.Position > math.MaxInt32 || item.FirstWins < 0 ||
			item.FirstWins > math.MaxInt32 || item.SecondWins < 0 || item.SecondWins > math.MaxInt32 ||
			!stage.Valid() || !format.Valid() || !state.Valid() ||
			!publicBracketNamesValid(item, stage) || !publicBracketScoreValid(item, stage, format, state) ||
			(item.ScheduledAt != nil && !domain.IsValidServerTime(*item.ScheduledAt)) {
			return api.PublicBracketResponse{}, domain.ErrInternal
		}
		matches[index] = api.PublicBracketMatch{
			Stage:             stage,
			Position:          int32(item.Position),
			Format:            format,
			FirstDisplayName:  cloneStringPointer(item.FirstDisplayName),
			SecondDisplayName: cloneStringPointer(item.SecondDisplayName),
			State:             state,
			ScheduledAt:       cloneTimePointer(item.ScheduledAt),
			WinnerDisplayName: cloneStringPointer(item.WinnerDisplayName),
			Score: api.SeriesScore{
				FirstParticipantWins:  int32(item.FirstWins),
				SecondParticipantWins: int32(item.SecondWins),
			},
		}
	}
	return api.PublicBracketResponse{
		TournamentId:       view.Tournament.TournamentID,
		ProjectionRevision: view.Cursor.ProjectionRevision,
		Matches:            matches,
	}, nil
}

func publicBracketTopologyValid(matches []tournamentsnapshot.PublicBracketMatchView) bool {
	if len(matches) == 0 {
		return true
	}
	if len(matches) != 3 {
		return false
	}
	seenSemifinalOne := false
	seenSemifinalTwo := false
	seenFinal := false
	for _, match := range matches {
		switch {
		case match.Stage == "semifinal" && match.Position == 1 && !seenSemifinalOne:
			seenSemifinalOne = true
		case match.Stage == "semifinal" && match.Position == 2 && !seenSemifinalTwo:
			seenSemifinalTwo = true
		case match.Stage == "final" && match.Position == 1 && !seenFinal:
			seenFinal = true
		default:
			return false
		}
	}
	return seenSemifinalOne && seenSemifinalTwo && seenFinal
}

func publicBracketNamesValid(
	item tournamentsnapshot.PublicBracketMatchView,
	stage api.PublicBracketMatchStage,
) bool {
	if stage == api.PublicBracketMatchStageFinal && item.FirstDisplayName == nil && item.SecondDisplayName == nil {
		return item.WinnerDisplayName == nil && item.FirstWins == 0 && item.SecondWins == 0 && item.State == "planned"
	}
	return item.FirstDisplayName != nil && item.SecondDisplayName != nil &&
		publicDisplayNameValid(*item.FirstDisplayName) && publicDisplayNameValid(*item.SecondDisplayName)
}

//nolint:gocyclo // Stage-specific score and winner validation intentionally stays at the HTTP boundary.
func publicBracketScoreValid(
	item tournamentsnapshot.PublicBracketMatchView,
	stage api.PublicBracketMatchStage,
	format api.SeriesFormat,
	state api.SeriesState,
) bool {
	var maxWins int
	switch {
	case stage == api.PublicBracketMatchStageSemifinal && format == api.Bo1:
		maxWins = 1
	case stage == api.PublicBracketMatchStageFinal && format == api.Bo3:
		maxWins = 2
	default:
		return false
	}
	if item.FirstWins > maxWins || item.SecondWins > maxWins {
		return false
	}
	if state == api.SeriesStateCompleted {
		if item.WinnerDisplayName == nil || item.FirstWins == item.SecondWins {
			return false
		}
		if !publicDisplayNameValid(*item.WinnerDisplayName) {
			return false
		}
		winner := strings.TrimSpace(*item.WinnerDisplayName)
		return (item.FirstWins > item.SecondWins && item.FirstDisplayName != nil && winner == *item.FirstDisplayName) ||
			(item.SecondWins > item.FirstWins && item.SecondDisplayName != nil && winner == *item.SecondDisplayName)
	}
	return item.WinnerDisplayName == nil
}

func publicSwissRoundsResponse(
	view tournamentsnapshot.PublicSnapshotView,
) ([]api.PublicSwissRound, error) {
	rounds := make([]api.PublicSwissRound, len(view.SwissRounds))
	previousRound := 0
	for index, item := range view.SwissRounds {
		state := api.WaveState(item.State)
		if item.RoundNumber < 1 || item.RoundNumber > 4 || item.RoundNumber <= previousRound || !state.Valid() {
			return nil, domain.ErrInternal
		}
		previousRound = item.RoundNumber
		round := api.PublicSwissRound{RoundNumber: int32(item.RoundNumber), State: state}
		if item.Bye != nil {
			if !publicDisplayNameValid(item.Bye.DisplayName) || item.Bye.PointsAwarded != 1 {
				return nil, domain.ErrInternal
			}
			round.Bye = &api.PublicSwissBye{
				DisplayName:   item.Bye.DisplayName,
				PointsAwarded: int32(item.Bye.PointsAwarded),
			}
		}
		rounds[index] = round
	}
	return rounds, nil
}

func publicDisplayNameValid(value string) bool {
	return strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= 64
}

func publicLiveSeriesResponse(
	view tournamentsnapshot.PublicSnapshotView,
) ([]api.PublicLiveSeries, error) {
	series := make([]api.PublicLiveSeries, len(view.LiveSeries))
	for index, item := range view.LiveSeries {
		mapped, err := publicLiveSeriesItem(item)
		if err != nil {
			return nil, err
		}
		series[index] = mapped
	}
	return series, nil
}

func publicLiveSeriesItem(item tournamentsnapshot.PublicSeriesView) (api.PublicLiveSeries, error) {
	stage := api.PublicLiveSeriesStage(item.Stage)
	format := api.SeriesFormat(item.Format)
	state := api.SeriesState(item.State)
	if !validPublicLiveSeries(item, stage, format, state) {
		return api.PublicLiveSeries{}, domain.ErrInternal
	}
	var currentGamePosition *int32
	if item.CurrentGamePosition > 0 {
		value := int32(item.CurrentGamePosition) //nolint:gosec // the public position range is validated immediately above
		currentGamePosition = &value
	}
	var roundNumber *int32
	if item.RoundNumber != nil {
		value := int32(*item.RoundNumber) //nolint:gosec // the public round range is validated immediately above
		roundNumber = &value
	}
	var currentGame *api.PublicLiveGame
	if item.CurrentGame != nil {
		game, ok := publicLiveGameItem(*item.CurrentGame)
		if !ok {
			return api.PublicLiveSeries{}, domain.ErrInternal
		}
		currentGame = &game
	}
	firstWins := int32(item.FirstWins)   //nolint:gosec // the public score range is validated immediately above
	secondWins := int32(item.SecondWins) //nolint:gosec // the public score range is validated immediately above
	return api.PublicLiveSeries{
		SeriesId:            item.SeriesID,
		Stage:               stage,
		RoundNumber:         roundNumber,
		Format:              format,
		State:               state,
		FirstDisplayName:    item.FirstDisplayName,
		SecondDisplayName:   item.SecondDisplayName,
		Score:               api.PublicSeriesScore{FirstWins: firstWins, SecondWins: secondWins},
		CurrentGamePosition: currentGamePosition,
		CurrentGame:         currentGame,
		ScheduledAt:         cloneTimePointer(item.ScheduledAt),
	}, nil
}

func publicLiveGameItem(item tournamentsnapshot.PublicCurrentGameView) (api.PublicLiveGame, bool) {
	state := api.GameState(item.State)
	category := api.Category(item.Category)
	firstStatus := api.PublicConnectionStatus(item.FirstConnectionStatus)
	secondStatus := api.PublicConnectionStatus(item.SecondConnectionStatus)
	if !validPublicLiveGameIdentity(item, category, state, firstStatus, secondStatus) ||
		!validPublicLiveGameLifecycle(item, state) {
		return api.PublicLiveGame{}, false
	}
	reason, ok := publicLiveGameResultReason(item, state)
	if !ok {
		return api.PublicLiveGame{}, false
	}
	winner, ok := publicLiveGameWinner(item, state)
	if !ok {
		return api.PublicLiveGame{}, false
	}
	return api.PublicLiveGame{
		Position:               int32(item.Position), //nolint:gosec // position range is validated above
		Category:               category,
		State:                  state,
		StartedAt:              cloneTimePointer(item.StartedAt),
		EffectiveDeadline:      cloneTimePointer(item.EffectiveDeadline),
		FinishedAt:             cloneTimePointer(item.FinishedAt),
		ResultReason:           reason,
		WinnerDisplayName:      winner,
		FirstConnectionStatus:  firstStatus,
		SecondConnectionStatus: secondStatus,
	}, true
}

func validPublicLiveGameIdentity(
	item tournamentsnapshot.PublicCurrentGameView,
	category api.Category,
	state api.GameState,
	firstStatus api.PublicConnectionStatus,
	secondStatus api.PublicConnectionStatus,
) bool {
	return item.Position >= 1 && item.Position <= 3 && category.Valid() && state.Valid() &&
		firstStatus.Valid() && secondStatus.Valid() &&
		(item.StartedAt == nil || domain.IsValidServerTime(*item.StartedAt)) &&
		(item.EffectiveDeadline == nil || domain.IsValidServerTime(*item.EffectiveDeadline)) &&
		(item.FinishedAt == nil || domain.IsValidServerTime(*item.FinishedAt))
}

func validPublicLiveGameLifecycle(item tournamentsnapshot.PublicCurrentGameView, state api.GameState) bool {
	if !stateTerminal(state) && item.FinishedAt != nil {
		return false
	}
	if stateTerminal(state) && item.FinishedAt == nil {
		return false
	}
	if (state == api.GameStateActive || state == api.GameStatePaused) && item.StartedAt == nil {
		return false
	}
	if state == api.GameStateActive && item.EffectiveDeadline == nil {
		return false
	}
	if state == api.GameStatePaused && item.EffectiveDeadline != nil {
		return false
	}
	return !stateTerminal(state) || item.EffectiveDeadline == nil
}

func publicLiveGameResultReason(item tournamentsnapshot.PublicCurrentGameView, state api.GameState) (*string, bool) {
	if item.ResultReason == nil {
		return nil, !stateTerminal(state)
	}
	if !domain.GameResultReason(*item.ResultReason).IsLegalFor(domain.GameState(state)) {
		return nil, false
	}
	value := *item.ResultReason
	return &value, true
}

func publicLiveGameWinner(item tournamentsnapshot.PublicCurrentGameView, state api.GameState) (*string, bool) {
	if item.WinnerDisplayName == nil {
		return nil, state != api.GameStateCompleted
	}
	if !publicDisplayNameValid(*item.WinnerDisplayName) || state != api.GameStateCompleted {
		return nil, false
	}
	value := *item.WinnerDisplayName
	return &value, true
}

func stateTerminal(state api.GameState) bool {
	switch state {
	case api.GameStateCompleted, api.GameStateVoid, api.GameStateCancelled, api.GameStateSuperseded:
		return true
	case api.GameStateActive, api.GameStatePaused, api.GameStatePlanned, api.GameStateReady:
		return false
	}
	return false
}

func validPublicLiveSeries(
	item tournamentsnapshot.PublicSeriesView,
	stage api.PublicLiveSeriesStage,
	format api.SeriesFormat,
	state api.SeriesState,
) bool {
	return validPublicLiveSeriesIdentity(item, stage, format, state) &&
		validPublicLiveSeriesScore(item) &&
		validPublicLiveSeriesRound(stage, item.RoundNumber) &&
		(item.CurrentGame == nil || item.CurrentGamePosition == item.CurrentGame.Position) &&
		(item.ScheduledAt == nil || domain.IsValidServerTime(*item.ScheduledAt))
}

func validPublicLiveSeriesIdentity(
	item tournamentsnapshot.PublicSeriesView,
	stage api.PublicLiveSeriesStage,
	format api.SeriesFormat,
	state api.SeriesState,
) bool {
	return item.SeriesID != uuid.Nil && stage.Valid() && format.Valid() && state.Valid() &&
		strings.TrimSpace(item.FirstDisplayName) != "" && strings.TrimSpace(item.SecondDisplayName) != ""
}

func validPublicLiveSeriesScore(item tournamentsnapshot.PublicSeriesView) bool {
	return item.FirstWins >= 0 && item.FirstWins <= 2 && item.SecondWins >= 0 && item.SecondWins <= 2 &&
		item.CurrentGamePosition >= 0 && item.CurrentGamePosition <= 3
}

func validPublicLiveSeriesRound(stage api.PublicLiveSeriesStage, roundNumber *int) bool {
	if roundNumber != nil && (*roundNumber < 1 || *roundNumber > 4) {
		return false
	}
	if stage == api.PublicLiveSeriesStageSwiss {
		return roundNumber != nil
	}
	return roundNumber == nil
}

func publicOfficialResultsResponse(
	view tournamentsnapshot.PublicSnapshotView,
) ([]api.PublicOfficialResult, error) {
	results := make([]api.PublicOfficialResult, len(view.OfficialResults))
	for index, item := range view.OfficialResults {
		state := api.SeriesState(item.State)
		if item.RevisionID == uuid.Nil || item.SeriesID == uuid.Nil || !state.Valid() ||
			item.FirstWins < 0 || item.FirstWins > 2 || item.SecondWins < 0 || item.SecondWins > 2 ||
			!domain.IsValidServerTime(item.RecordedAt) {
			return nil, domain.ErrInternal
		}
		var winnerDisplayName *string
		if strings.TrimSpace(item.WinnerDisplayName) != "" {
			value := item.WinnerDisplayName
			winnerDisplayName = &value
		}
		results[index] = api.PublicOfficialResult{
			RevisionId:        item.RevisionID,
			SeriesId:          item.SeriesID,
			State:             state,
			WinnerDisplayName: winnerDisplayName,
			Score:             api.PublicSeriesScore{FirstWins: int32(item.FirstWins), SecondWins: int32(item.SecondWins)},
			RecordedAt:        item.RecordedAt,
		}
	}
	return results, nil
}

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
func publicLiveDraftResponse(
	view tournamentsnapshot.PublicSnapshotView,
) (*api.PublicLiveDraftResponse, error) {
	if view.Draft == nil {
		return nil, nil
	}
	draft := view.Draft
	format := api.SeriesFormat(draft.Format)
	state := api.DraftState(draft.State)
	if draft.SeriesID == uuid.Nil || !format.Valid() || !state.Valid() ||
		!publicDisplayNameValid(draft.FirstActorDisplayName) ||
		!validPublicDraftCardinalityHTTP(format, draft.Pool, draft.SelectedCategories, draft.Actions, state) {
		return nil, domain.ErrInternal
	}
	pool := make([]api.Category, len(draft.Pool))
	for index, value := range draft.Pool {
		pool[index] = api.Category(value)
		if !pool[index].Valid() {
			return nil, domain.ErrInternal
		}
	}
	selected := make([]api.Category, len(draft.SelectedCategories))
	for index, value := range draft.SelectedCategories {
		selected[index] = api.Category(value)
		if !selected[index].Valid() {
			return nil, domain.ErrInternal
		}
	}
	actions := make([]api.PublicDraftAction, len(draft.Actions))
	for index, item := range draft.Actions {
		action := api.DraftActionType(item.Action)
		category := api.Category(item.Category)
		if item.Turn != index+1 || item.Turn < 1 || item.Turn > math.MaxInt32 || !action.Valid() || !category.Valid() ||
			!publicDisplayNameValid(item.ActorDisplayName) || !domain.IsValidServerTime(item.OccurredAt) {
			return nil, domain.ErrInternal
		}
		actions[index] = api.PublicDraftAction{
			Turn:             int32(item.Turn),
			Action:           action,
			Category:         category,
			ActorDisplayName: item.ActorDisplayName,
			OccurredAt:       item.OccurredAt,
			Automatic:        item.Automatic,
		}
	}
	var currentTurn *int32
	if draft.CurrentTurn != nil {
		if *draft.CurrentTurn < 1 || *draft.CurrentTurn > 4 {
			return nil, domain.ErrInternal
		}
		value := int32(*draft.CurrentTurn)
		currentTurn = &value
	}
	var currentAction *api.DraftActionType
	if draft.CurrentAction != nil {
		action := api.DraftActionType(*draft.CurrentAction)
		if !action.Valid() {
			return nil, domain.ErrInternal
		}
		currentAction = &action
	}
	if draft.CurrentActorDisplayName != nil && !publicDisplayNameValid(*draft.CurrentActorDisplayName) {
		return nil, domain.ErrInternal
	}
	if draft.TurnDeadline != nil && !domain.IsValidServerTime(*draft.TurnDeadline) {
		return nil, domain.ErrInternal
	}
	if state == api.DraftStateActive || state == api.DraftStatePaused || state == api.DraftStateRecoveryRequired {
		if currentTurn == nil || currentAction == nil || draft.CurrentActorDisplayName == nil {
			return nil, domain.ErrInternal
		}
		if state == api.DraftStateActive && draft.TurnDeadline == nil {
			return nil, domain.ErrInternal
		}
		if state != api.DraftStateActive && draft.TurnDeadline != nil {
			return nil, domain.ErrInternal
		}
	} else if currentTurn != nil || currentAction != nil || draft.CurrentActorDisplayName != nil || draft.TurnDeadline != nil || draft.AutoActionPending {
		return nil, domain.ErrInternal
	}
	return &api.PublicLiveDraftResponse{
		TournamentId:            view.Tournament.TournamentID,
		ProjectionRevision:      view.Cursor.ProjectionRevision,
		SeriesId:                draft.SeriesID,
		Format:                  format,
		State:                   state,
		FirstActorDisplayName:   draft.FirstActorDisplayName,
		CurrentTurn:             currentTurn,
		CurrentAction:           currentAction,
		CurrentActorDisplayName: cloneStringPointer(draft.CurrentActorDisplayName),
		TurnDeadline:            cloneTimePointer(draft.TurnDeadline),
		AutoActionPending:       draft.AutoActionPending,
		Pool:                    pool,
		SelectedCategories:      selected,
		Actions:                 actions,
	}, nil
}

func validPublicDraftCardinalityHTTP(
	format api.SeriesFormat,
	pool []string,
	selected []string,
	actions []tournamentsnapshot.PublicDraftActionView,
	state api.DraftState,
) bool {
	var poolCount, actionCount, selectedCount int
	switch format {
	case api.Bo1:
		poolCount, actionCount, selectedCount = 3, 2, 1
	case api.Bo3:
		poolCount, actionCount, selectedCount = 5, 4, 3
	default:
		return false
	}
	if len(pool) != poolCount || len(selected) > selectedCount || len(actions) > actionCount {
		return false
	}
	seenPool := make(map[string]struct{}, len(pool))
	for _, value := range pool {
		if !api.Category(value).Valid() {
			return false
		}
		if _, exists := seenPool[value]; exists {
			return false
		}
		seenPool[value] = struct{}{}
	}
	seenSelected := make(map[string]struct{}, len(selected))
	for _, value := range selected {
		if _, exists := seenPool[value]; !exists {
			return false
		}
		if _, exists := seenSelected[value]; exists {
			return false
		}
		seenSelected[value] = struct{}{}
	}
	if state == api.DraftStateCompleted {
		return len(actions) == actionCount && len(selected) == selectedCount
	}
	return len(selected) == 0
}
