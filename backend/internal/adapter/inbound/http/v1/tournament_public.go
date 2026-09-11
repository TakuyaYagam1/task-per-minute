package v1

import (
	"errors"
	"math"
	"net/http"
	"strings"

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
		if item.Rank < 1 || item.Rank > math.MaxInt32 || item.Points < 0 || item.Points > math.MaxInt32 ||
			item.Buchholz < 0 || item.Buchholz > math.MaxInt32 || item.EffectiveTimeMS < 0 ||
			strings.TrimSpace(item.DisplayName) == "" {
			return api.PublicScoreboardResponse{}, domain.ErrInternal
		}
		entries[index] = api.PublicScoreboardEntry{
			Rank:            int32(item.Rank),
			DisplayName:     item.DisplayName,
			Points:          int32(item.Points),
			Buchholz:        int32(item.Buchholz),
			EffectiveTimeMs: item.EffectiveTimeMS,
		}
	}
	return api.PublicScoreboardResponse{
		TournamentId:       view.Tournament.TournamentID,
		ProjectionRevision: view.Cursor.ProjectionRevision,
		Entries:            entries,
	}, nil
}

func publicBracketResponse(
	view tournamentsnapshot.PublicSnapshotView,
) (api.PublicBracketResponse, error) {
	matches := make([]api.PublicBracketMatch, len(view.Bracket))
	for index, item := range view.Bracket {
		stage := api.PublicBracketMatchStage(item.Stage)
		state := api.SeriesState(item.State)
		if item.Position < 1 || item.Position > math.MaxInt32 || item.FirstWins < 0 ||
			item.FirstWins > math.MaxInt32 || item.SecondWins < 0 || item.SecondWins > math.MaxInt32 ||
			strings.TrimSpace(item.FirstDisplayName) == "" || strings.TrimSpace(item.SecondDisplayName) == "" ||
			!stage.Valid() || !state.Valid() {
			return api.PublicBracketResponse{}, domain.ErrInternal
		}
		matches[index] = api.PublicBracketMatch{
			Stage:             stage,
			Position:          int32(item.Position),
			FirstDisplayName:  item.FirstDisplayName,
			SecondDisplayName: item.SecondDisplayName,
			State:             state,
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

func publicLiveSeriesResponse(
	view tournamentsnapshot.PublicSnapshotView,
) ([]api.PublicLiveSeries, error) {
	series := make([]api.PublicLiveSeries, len(view.LiveSeries))
	for index, item := range view.LiveSeries {
		format := api.SeriesFormat(item.Format)
		state := api.SeriesState(item.State)
		if item.SeriesID == uuid.Nil || !format.Valid() || !state.Valid() ||
			strings.TrimSpace(item.FirstDisplayName) == "" || strings.TrimSpace(item.SecondDisplayName) == "" ||
			item.FirstWins < 0 || item.FirstWins > 2 || item.SecondWins < 0 || item.SecondWins > 2 ||
			item.CurrentGamePosition < 0 || item.CurrentGamePosition > 3 {
			return nil, domain.ErrInternal
		}
		var currentGamePosition *int32
		if item.CurrentGamePosition > 0 {
			value := int32(item.CurrentGamePosition)
			currentGamePosition = &value
		}
		series[index] = api.PublicLiveSeries{
			SeriesId:            item.SeriesID,
			Format:              format,
			State:               state,
			FirstDisplayName:    item.FirstDisplayName,
			SecondDisplayName:   item.SecondDisplayName,
			Score:               api.PublicSeriesScore{FirstWins: int32(item.FirstWins), SecondWins: int32(item.SecondWins)},
			CurrentGamePosition: currentGamePosition,
		}
	}
	return series, nil
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
	if draft.SeriesID == uuid.Nil || !format.Valid() || !state.Valid() {
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
		if item.Turn < 1 || item.Turn > math.MaxInt32 || !action.Valid() || !category.Valid() ||
			strings.TrimSpace(item.ActorDisplayName) == "" || !domain.IsValidServerTime(item.OccurredAt) {
			return nil, domain.ErrInternal
		}
		actions[index] = api.PublicDraftAction{
			Turn:             int32(item.Turn),
			Action:           action,
			Category:         category,
			ActorDisplayName: item.ActorDisplayName,
			OccurredAt:       item.OccurredAt,
		}
	}
	return &api.PublicLiveDraftResponse{
		TournamentId:       view.Tournament.TournamentID,
		ProjectionRevision: view.Cursor.ProjectionRevision,
		SeriesId:           draft.SeriesID,
		Format:             format,
		State:              state,
		Pool:               pool,
		SelectedCategories: selected,
		Actions:            actions,
	}, nil
}
