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
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	inbound "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

type tournamentController struct {
	catalog inbound.TournamentUseCase
	admin   inbound.TournamentAdminUseCase
}

func newTournamentController(
	catalog inbound.TournamentUseCase,
	admin inbound.TournamentAdminUseCase,
) *tournamentController {
	return &tournamentController{catalog: catalog, admin: admin}
}

func (c *tournamentController) ListTournaments(
	w http.ResponseWriter,
	r *http.Request,
	params api.ListTournamentsParams,
) {
	operator, ok := operatorFromRequest(w, r)
	if !ok {
		return
	}
	if c == nil || c.catalog == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	command := inbound.TournamentListCommand{Operator: operator}
	if params.State != nil {
		command.State = domain.TournamentState(*params.State)
	}
	if params.Cursor != nil {
		after, err := decodeTournamentCursor(*params.Cursor)
		if err != nil {
			errmap.HandleError(w, r, domain.ErrValidation)
			return
		}
		command.After = after
	}
	if params.PageSize != nil {
		command.PageSize = int(*params.PageSize)
	}

	result, err := c.catalog.ListTournaments(r.Context(), command)
	if err != nil {
		writeTournamentError(w, r, err)
		return
	}
	payload, err := tournamentListResponse(result)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (c *tournamentController) GetTournamentContent(
	w http.ResponseWriter,
	r *http.Request,
) {
	operator, ok := operatorFromRequest(w, r)
	if !ok {
		return
	}
	if c == nil || c.catalog == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	view, err := c.catalog.GetTournamentContent(r.Context(), operator)
	if err != nil {
		writeTournamentError(w, r, err)
		return
	}
	payload, err := tournamentContentResponse(view)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, payload)
}

func (c *tournamentController) CreateTournament(
	w http.ResponseWriter,
	r *http.Request,
	params api.CreateTournamentParams,
) {
	operator, ok := operatorFromRequest(w, r)
	if !ok {
		return
	}
	if c == nil || c.catalog == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.CreateTournamentRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	result, err := c.catalog.CreateTournament(r.Context(), inbound.TournamentCreateCommand{
		Operator:          operator,
		IdempotencyKey:    params.IdempotencyKey,
		ExpectedRevision:  body.ExpectedRevision,
		Preset:            domain.TournamentPreset(body.Preset),
		Name:              body.Name,
		PublicID:          body.PublicId,
		PlannedRosterSize: int(body.PlannedRosterSize),
		ContentRevision:   body.ContentRevision,
	})
	if err != nil {
		writeTournamentError(w, r, err)
		return
	}
	payload, err := tournamentResponse(result.Tournament)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusCreated, payload)
}

func operatorFromRequest(w http.ResponseWriter, r *http.Request) (inbound.OperatorIdentity, bool) {
	actor, ok := adminActorFromRequest(r)
	if !ok {
		errmap.HandleError(w, r, domain.ErrInvalidCredentials)
		return inbound.OperatorIdentity{}, false
	}
	actorID, err := inbound.OperatorActorID(actor.Subject)
	if err != nil {
		errmap.HandleError(w, r, domain.ErrInvalidCredentials)
		return inbound.OperatorIdentity{}, false
	}
	return inbound.OperatorIdentity{ActorID: actorID}, true
}

type tournamentCursorPayload struct {
	CreatedAt    time.Time `json:"created_at"`
	TournamentID uuid.UUID `json:"tournament_id"`
}

func decodeTournamentCursor(value string) (*inbound.TournamentCursor, error) {
	encoded := strings.TrimSpace(value)
	if encoded == "" || len(encoded) > 1024 {
		return nil, domain.ErrValidation
	}
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, domain.ErrValidation
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var payload tournamentCursorPayload
	if err := decoder.Decode(&payload); err != nil {
		return nil, domain.ErrValidation
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, domain.ErrValidation
	}
	if payload.TournamentID == uuid.Nil || !domain.IsValidServerTime(payload.CreatedAt) {
		return nil, domain.ErrValidation
	}
	return &inbound.TournamentCursor{
		CreatedAt: payload.CreatedAt, TournamentID: payload.TournamentID,
	}, nil
}

func encodeTournamentCursor(cursor *inbound.TournamentCursor) (string, error) {
	if cursor == nil {
		return "", nil
	}
	if cursor.TournamentID == uuid.Nil || !domain.IsValidServerTime(cursor.CreatedAt) {
		return "", domain.ErrInternal
	}
	payload, err := json.Marshal(tournamentCursorPayload{
		CreatedAt: cursor.CreatedAt, TournamentID: cursor.TournamentID,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func tournamentListResponse(page inbound.TournamentPage) (api.TournamentListResponse, error) {
	items := make([]api.Tournament, len(page.Items))
	for index, item := range page.Items {
		mapped, err := tournamentResponse(item)
		if err != nil {
			return api.TournamentListResponse{}, err
		}
		items[index] = mapped
	}
	result := api.TournamentListResponse{Items: items}
	if page.Next == nil {
		return result, nil
	}
	next, err := encodeTournamentCursor(page.Next)
	if err != nil {
		return api.TournamentListResponse{}, err
	}
	result.NextCursor = &next
	return result, nil
}

func tournamentResponse(view inbound.TournamentView) (api.Tournament, error) {
	if view.RosterSize < 0 || view.RosterSize > math.MaxInt32 ||
		view.PlannedRosterSize < 0 || view.PlannedRosterSize > math.MaxInt32 {
		return api.Tournament{}, domain.ErrInternal
	}
	var pausedFromState *api.TournamentState
	if view.PausedFromState != nil {
		state := api.TournamentState(*view.PausedFromState)
		pausedFromState = &state
	}
	return api.Tournament{
		Id:       view.ID,
		RosterId: view.RosterID,
		Preset:   api.TournamentPreset(view.Preset),
		Name:     view.Name,
		PublicId: view.PublicID,

		PlannedRosterSize: int32(view.PlannedRosterSize),
		ContentRevision:   view.ContentRevision,
		State:             api.TournamentState(view.State),
		PausedFromState:   pausedFromState,
		Revision:          view.Revision,
		RosterSize:        int32(view.RosterSize),
		CreatedAt:         view.CreatedAt,
		UpdatedAt:         view.UpdatedAt,
		StartedAt:         cloneTimePointer(view.StartedAt),
		FinishedAt:        cloneTimePointer(view.FinishedAt),
	}, nil
}

// tournamentContentResponse is an explicit allowlist. Content discovery must
// never serialize task payloads or internal publication metadata that may be
// added to the usecase view later.
func tournamentContentResponse(view inbound.TournamentContentView) (api.TournamentContentSelection, error) {
	if view.ContentRevision < 1 || view.PublicationID == uuid.Nil ||
		view.NormalPoolRevisionID == uuid.Nil || view.GoldenPoolRevisionID == uuid.Nil ||
		view.NormalPoolRevisionID == view.GoldenPoolRevisionID ||
		!domain.IsValidServerTime(view.PublishedAt) {
		return api.TournamentContentSelection{}, domain.ErrInternal
	}
	return api.TournamentContentSelection{
		ContentRevision:      view.ContentRevision,
		PublicationId:        view.PublicationID,
		PublishedAt:          view.PublishedAt,
		NormalPoolRevisionId: view.NormalPoolRevisionID,
		GoldenPoolRevisionId: view.GoldenPoolRevisionID,
	}, nil
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func writeTournamentError(w http.ResponseWriter, r *http.Request, err error) {
	var conflict *inbound.TournamentRevisionConflictError
	if errors.As(err, &conflict) {
		if conflict.ExpectedRevision < 0 || conflict.CurrentRevision < 1 {
			errmap.HandleError(w, r, domain.ErrInternal)
			return
		}
		detail, instance, requestID := tournamentProblemContext(r, "tournament revision conflict")
		payload := api.TournamentRevisionProblem{
			Type: "about:blank", Title: http.StatusText(http.StatusConflict),
			Status: int32(http.StatusConflict), Detail: &detail, Instance: &instance, RequestId: &requestID,
			ExpectedRevision: conflict.ExpectedRevision,
			CurrentRevision:  conflict.CurrentRevision,
		}
		if conflict.CurrentState != "" {
			state := api.TournamentState(conflict.CurrentState)
			if !state.Valid() {
				errmap.HandleError(w, r, domain.ErrInternal)
				return
			}
			payload.CurrentState = &state
		}
		response.WriteProblem(w, http.StatusConflict, payload)
		return
	}
	errmap.HandleError(w, r, err)
}

func tournamentProblemContext(r *http.Request, detail string) (string, string, string) {
	instance := ""
	requestID := ""
	if r != nil {
		if r.URL != nil {
			instance = r.URL.Path
		}
		requestID = middleware.GetRequestIDFromCtx(r.Context())
	}
	return detail, instance, requestID
}
