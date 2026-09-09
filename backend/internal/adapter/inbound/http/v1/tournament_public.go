package v1

import (
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
	_ api.GetPublicSnapshotParams,
) {
	view, ok := s.readPublicTournamentSnapshot(w, r, tournamentID)
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
	draft, err := publicLiveDraftResponse(view)
	if err != nil {
		return api.PublicRecoverySnapshot{}, err
	}
	if view.Cursor.EventSequence < 0 {
		return api.PublicRecoverySnapshot{}, domain.ErrInternal
	}
	return api.PublicRecoverySnapshot{
		Tournament: tournament,
		Scoreboard: scoreboard,
		Bracket:    bracket,
		LiveDraft:  draft,
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
