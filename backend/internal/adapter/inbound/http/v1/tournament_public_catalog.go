package v1

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

type publicTournamentCatalogCursorPayload struct {
	Search       string                                     `json:"search"`
	Group        inbound.PublicTournamentCatalogFilterGroup `json:"group"`
	Sort         inbound.PublicTournamentCatalogSort        `json:"sort"`
	GroupRank    int                                        `json:"group_rank"`
	Name         string                                     `json:"name"`
	CreatedAt    time.Time                                  `json:"created_at"`
	TournamentID uuid.UUID                                  `json:"tournament_id"`
}

func (s *Server) ListPublicTournaments(
	w http.ResponseWriter,
	r *http.Request,
	params api.ListPublicTournamentsParams,
) {
	if s == nil || s.publicTournamentCatalog == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	query, err := publicTournamentCatalogQuery(params)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}
	page, err := s.publicTournamentCatalog.ListPublicTournaments(r.Context(), query)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	payload, err := publicTournamentCatalogResponse(page)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (s *Server) GetPublicTournamentByPublicID(
	w http.ResponseWriter,
	r *http.Request,
	publicID string,
) {
	if s == nil || s.publicTournamentCatalog == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	view, err := s.publicTournamentCatalog.GetPublicTournamentByPublicID(r.Context(), publicID)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	payload, err := publicTournamentCatalogItemResponse(view)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func publicTournamentCatalogQuery(params api.ListPublicTournamentsParams) (inbound.PublicTournamentCatalogQuery, error) {
	query := inbound.PublicTournamentCatalogQuery{}
	if params.Q != nil {
		query.Search = strings.ToLower(strings.TrimSpace(*params.Q))
	}
	if params.Group != nil {
		query.Group = inbound.PublicTournamentCatalogFilterGroup(*params.Group)
	}
	if params.Sort != nil {
		query.Sort = inbound.PublicTournamentCatalogSort(*params.Sort)
	}
	if params.Limit != nil {
		if *params.Limit < 1 {
			return inbound.PublicTournamentCatalogQuery{}, domain.ErrValidation
		}
		query.Limit = int(*params.Limit)
	}
	if params.Cursor != nil {
		cursor, err := decodePublicTournamentCatalogCursor(*params.Cursor)
		if err != nil {
			return inbound.PublicTournamentCatalogQuery{}, err
		}
		query.After = cursor
	}
	return query, nil
}

func decodePublicTournamentCatalogCursor(value string) (*inbound.PublicTournamentCatalogCursor, error) {
	encoded := strings.TrimSpace(value)
	if encoded == "" || len(encoded) > 2048 {
		return nil, domain.ErrValidation
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, domain.ErrValidation
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var payload publicTournamentCatalogCursorPayload
	if err := decoder.Decode(&payload); err != nil {
		return nil, domain.ErrValidation
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, domain.ErrValidation
	}
	if payload.TournamentID == uuid.Nil || !domain.IsValidServerTime(payload.CreatedAt) ||
		!payload.Group.IsValid() || !payload.Sort.IsValid() || payload.GroupRank < 0 || payload.GroupRank > 2 ||
		len([]rune(payload.Search)) > 80 {
		return nil, domain.ErrValidation
	}
	return &inbound.PublicTournamentCatalogCursor{
		Search: payload.Search, Group: payload.Group, Sort: payload.Sort,
		GroupRank: payload.GroupRank, Name: payload.Name,
		CreatedAt: payload.CreatedAt, TournamentID: payload.TournamentID,
	}, nil
}

func encodePublicTournamentCatalogCursor(cursor *inbound.PublicTournamentCatalogCursor) (string, error) {
	if cursor == nil {
		return "", nil
	}
	if cursor.TournamentID == uuid.Nil || !domain.IsValidServerTime(cursor.CreatedAt) ||
		!cursor.Group.IsValid() || !cursor.Sort.IsValid() || cursor.GroupRank < 0 || cursor.GroupRank > 2 ||
		len([]rune(cursor.Search)) > 80 {
		return "", domain.ErrInternal
	}
	data, err := json.Marshal(publicTournamentCatalogCursorPayload{
		Search: cursor.Search, Group: cursor.Group, Sort: cursor.Sort,
		GroupRank: cursor.GroupRank, Name: cursor.Name,
		CreatedAt: cursor.CreatedAt, TournamentID: cursor.TournamentID,
	})
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(data)
	if len(encoded) > 2048 {
		return "", domain.ErrInternal
	}
	return encoded, nil
}

func publicTournamentCatalogResponse(page inbound.PublicTournamentCatalogPage) (api.PublicTournamentCatalogResponse, error) {
	items := make([]api.PublicTournamentCatalogItem, len(page.Items))
	for index, item := range page.Items {
		mapped, err := publicTournamentCatalogItemResponse(item)
		if err != nil {
			return api.PublicTournamentCatalogResponse{}, err
		}
		items[index] = mapped
	}
	payload := api.PublicTournamentCatalogResponse{Items: items}
	if page.Next == nil {
		return payload, nil
	}
	cursor, err := encodePublicTournamentCatalogCursor(page.Next)
	if err != nil {
		return api.PublicTournamentCatalogResponse{}, err
	}
	payload.NextCursor = &cursor
	return payload, nil
}

func publicTournamentCatalogItemResponse(view inbound.PublicTournamentCatalogView) (api.PublicTournamentCatalogItem, error) {
	if err := validatePublicTournamentCatalogItem(view); err != nil {
		return api.PublicTournamentCatalogItem{}, domain.ErrInternal
	}
	state, group, stage, preset, err := publicTournamentCatalogItemEnums(view)
	if err != nil {
		return api.PublicTournamentCatalogItem{}, domain.ErrInternal
	}
	return api.PublicTournamentCatalogItem{
		TournamentId:      view.TournamentID,
		PublicId:          view.PublicID,
		Name:              view.Name,
		Preset:            preset,
		State:             state,
		Group:             group,
		Stage:             stage,
		PlannedRosterSize: response.IntToInt32(view.PlannedRosterSize),
		RosterSize:        response.IntToInt32(view.RosterSize),
		CreatedAt:         view.CreatedAt,
		StartedAt:         cloneTimePointer(view.StartedAt),
		FinishedAt:        cloneTimePointer(view.FinishedAt),
		ScheduledAt:       cloneTimePointer(view.ScheduledAt),
	}, nil
}

func validatePublicTournamentCatalogItem(view inbound.PublicTournamentCatalogView) error {
	if view.TournamentID == uuid.Nil || view.Name == "" || view.PublicID == "" {
		return domain.ErrInternal
	}
	if view.PlannedRosterSize < 4 || view.PlannedRosterSize > math.MaxInt32 ||
		view.RosterSize < 0 || view.RosterSize > math.MaxInt32 {
		return domain.ErrInternal
	}
	if !domain.IsValidServerTime(view.CreatedAt) || !view.State.IsValid() ||
		!view.Group.IsValid() || !view.Stage.IsValid() || view.Stage == domain.TournamentStateTechnicalPause {
		return domain.ErrInternal
	}
	return nil
}

func publicTournamentCatalogItemEnums(
	view inbound.PublicTournamentCatalogView,
) (api.TournamentState, api.TournamentCatalogGroup, api.TournamentCatalogStage, api.TournamentPreset, error) {
	state := api.TournamentState(view.State)
	group := api.TournamentCatalogGroup(view.Group)
	stage := api.TournamentCatalogStage(view.Stage)
	preset := api.TournamentPreset(view.Preset)
	if !state.Valid() || !group.Valid() || !stage.Valid() || !preset.Valid() {
		return "", "", "", "", domain.ErrInternal
	}
	return state, group, stage, preset, nil
}
