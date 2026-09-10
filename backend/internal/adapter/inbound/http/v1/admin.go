package v1

import (
	"errors"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
)

// (POST /api/v1/admin/login).
func (s *Server) LoginAdmin(w http.ResponseWriter, r *http.Request) {
	if s.adminAuth == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	if !s.enterPublicRequest(w, r, s.adminLoginPolicy()) {
		return
	}

	var body api.AdminLoginRequest
	if !decodeJSONBody(w, r, &body, domain.ErrInvalidCredentials) {
		s.logSecurityEvent(r, "admin.login", securityOutcomeFailure, logkitFields("error_code", domain.ErrorCodeInvalidCredentials))
		return
	}
	if body.Password == "" {
		s.logSecurityEvent(r, "admin.login", securityOutcomeFailure, logkitFields("error_code", domain.ErrorCodeInvalidCredentials))
		errmap.HandleError(w, r, domain.ErrInvalidCredentials)
		return
	}

	pair, err := s.adminAuth.Login(r.Context(), body.Password)
	if err != nil {
		s.logSecurityEvent(r, "admin.login", securityOutcomeFailure, logkitFields("error_code", securityErrorCode(err)))
		errmap.HandleError(w, r, err)
		return
	}

	s.logSecurityEvent(r, "admin.login", securityOutcomeSuccess, nil)
	if err := middleware.SetAdminSessionCookies(w, r, pair); err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, response.AdminSession(pair, s.now()))
}

// (POST /api/v1/admin/logout).
func (s *Server) LogoutAdmin(w http.ResponseWriter, r *http.Request, _ api.LogoutAdminParams) {
	actor, _ := adminActorFromRequest(r)
	if s.adminAuth == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	refreshToken, ok := middleware.AdminRefreshTokenFromRequest(r)
	if !ok {
		s.logSecurityEvent(r, "admin.logout", securityOutcomeFailure, adminSecurityFields(actor, domain.ErrorCodeInvalidCredentials))
		errmap.HandleError(w, r, domain.ErrInvalidCredentials)
		return
	}

	accessTokens := make([]string, 0, 1)
	if accessToken, ok := middleware.AdminAccessTokenFromRequest(r); ok {
		accessTokens = append(accessTokens, accessToken)
	}
	if err := s.adminAuth.Logout(r.Context(), refreshToken, accessTokens...); err != nil {
		s.logSecurityEvent(r, "admin.logout", securityOutcomeFailure, adminSecurityFields(actor, securityErrorCode(err)))
		errmap.HandleError(w, r, err)
		return
	}

	s.logSecurityEvent(r, "admin.logout", securityOutcomeSuccess, adminSecurityFields(actor, ""))
	middleware.ClearAdminSessionCookies(w, r)
	w.WriteHeader(http.StatusNoContent)
}

// (POST /api/v1/admin/refresh).
func (s *Server) RefreshAdminSession(w http.ResponseWriter, r *http.Request, _ api.RefreshAdminSessionParams) {
	if s.adminAuth == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	if !s.enterPublicRequest(w, r, s.adminRefreshPolicy()) {
		return
	}

	refreshToken, ok := middleware.AdminRefreshTokenFromRequest(r)
	if !ok {
		s.logSecurityEvent(r, "admin.refresh", securityOutcomeFailure, logkitFields("error_code", domain.ErrorCodeInvalidCredentials))
		errmap.HandleError(w, r, domain.ErrInvalidCredentials)
		return
	}

	accessTokens := make([]string, 0, 1)
	if accessToken, ok := middleware.AdminAccessTokenFromRequest(r); ok {
		accessTokens = append(accessTokens, accessToken)
	}
	pair, err := s.adminAuth.Refresh(r.Context(), refreshToken, accessTokens...)
	if err != nil {
		s.logSecurityEvent(r, "admin.refresh", securityOutcomeFailure, logkitFields("error_code", securityErrorCode(err)))
		errmap.HandleError(w, r, err)
		return
	}

	s.logSecurityEvent(r, "admin.refresh", securityOutcomeSuccess, nil)
	if err := middleware.SetAdminSessionCookies(w, r, pair); err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, response.AdminSession(pair, s.now()))
}

// (GET /api/v1/admin/players).
func (s *Server) ListPlayers(w http.ResponseWriter, r *http.Request, params api.ListPlayersParams) {
	if !requireAdmin(w, r) {
		return
	}
	if s.adminPlayers == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	includeDeleted := false
	if params.IncludeDeleted != nil {
		includeDeleted = *params.IncludeDeleted
	}
	players, err := s.adminPlayers.ListPlayers(r.Context(), includeDeleted)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, response.PlayerManagementList(players))
}

// (GET /api/v1/admin/players/{id}/audit).
func (s *Server) ListPlayerAuditEvents(
	w http.ResponseWriter,
	r *http.Request,
	id openapi_types.UUID,
	params api.ListPlayerAuditEventsParams,
) {
	if !requireAdmin(w, r) {
		return
	}
	if s.adminPlayers == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	limit := int32(50)
	if params.Limit != nil {
		if *params.Limit <= 0 {
			errmap.HandleError(w, r, domain.ErrValidation)
			return
		}
		limit = *params.Limit
	}
	if limit > 200 {
		limit = 200
	}
	events, err := s.adminPlayers.ListPlayerAudit(r.Context(), id, limit)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, response.PlayerAuditEvents(events))
}

// (PUT /api/v1/admin/players/{id}).
func (s *Server) UpdatePlayer(
	w http.ResponseWriter,
	r *http.Request,
	id openapi_types.UUID,
	_ api.UpdatePlayerParams,
) {
	if !requireAdmin(w, r) {
		return
	}
	actor, ok := adminActorFromRequest(r)
	if !ok {
		errmap.HandleError(w, r, domain.ErrInvalidCredentials)
		return
	}
	if s.adminPlayers == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.UpdatePlayerRequest
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}

	player, err := s.adminPlayers.UpdatePlayer(r.Context(), id, playerusecase.PlayerInput{
		Username:           body.Username,
		Wins:               int(body.Wins),
		AverageSolveTimeMs: body.AverageSolveTimeMs,
	}, actor)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, response.PlayerManagement(*player))
}

// (DELETE /api/v1/admin/players/{id}).
func (s *Server) DeletePlayer(
	w http.ResponseWriter,
	r *http.Request,
	id openapi_types.UUID,
	_ api.DeletePlayerParams,
) {
	if !requireAdmin(w, r) {
		return
	}
	actor, ok := adminActorFromRequest(r)
	if !ok {
		errmap.HandleError(w, r, domain.ErrInvalidCredentials)
		return
	}
	if s.adminPlayers == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	if err := s.adminPlayers.DeletePlayer(r.Context(), id, actor); err != nil {
		errmap.HandleError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// (GET /api/v1/admin/tasks).
func (s *Server) ListTasks(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if s.tasks == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	tasks, err := s.tasks.ListTasks(r.Context())
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, response.Tasks(tasks))
}

// (POST /api/v1/admin/tasks).
func (s *Server) CreateTask(w http.ResponseWriter, r *http.Request, _ api.CreateTaskParams) {
	if !requireAdmin(w, r) {
		return
	}
	if s.tasks == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	var body api.CreateTaskRequest
	if !decodeJSONBody(w, r, &body, domain.ErrTaskValidation) {
		return
	}

	task, err := s.tasks.CreateTask(r.Context(), createTaskInput(body))
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}

	response.WriteJSON(w, http.StatusCreated, response.Task(task))
}

// (GET /api/v1/admin/tasks/{id}).
func (s *Server) GetTask(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	if !requireAdmin(w, r) {
		return
	}
	if s.tasks == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	task, err := s.tasks.GetTask(r.Context(), id)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, response.Task(task))
}

// (PUT /api/v1/admin/tasks/{id}).
func (s *Server) UpdateTask(
	w http.ResponseWriter,
	r *http.Request,
	id openapi_types.UUID,
	_ api.UpdateTaskParams,
) {
	if !requireAdmin(w, r) {
		return
	}
	if s.tasks == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	existing, err := s.tasks.GetTask(r.Context(), id)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}

	var body api.UpdateTaskRequest
	if !decodeJSONBody(w, r, &body, domain.ErrTaskValidation) {
		return
	}

	input := updateTaskInput(existing, body)
	var updated *domain.Task
	if clearSourceFileRequested(body) {
		if s.upload == nil {
			errmap.HandleError(w, r, domain.ErrInternal)
			return
		}
		updated, err = s.upload.ClearSourceFile(r.Context(), id, input)
	} else {
		updated, err = s.tasks.UpdateTask(r.Context(), id, input)
	}
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, response.Task(updated))
}

// (DELETE /api/v1/admin/tasks/{id}).
func (s *Server) DeleteTask(
	w http.ResponseWriter,
	r *http.Request,
	id openapi_types.UUID,
	_ api.DeleteTaskParams,
) {
	if !requireAdmin(w, r) {
		return
	}
	if s.tasks == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	existing, err := s.tasks.GetTask(r.Context(), id)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	if existing.SourceFileURL != nil && s.upload == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	if err := s.tasks.DeleteTask(r.Context(), id); err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	if existing.SourceFileURL != nil {
		_ = s.upload.DeleteSourceFile(r.Context(), id, existing.SourceFileURL)
	}

	response.WriteJSON(w, http.StatusNoContent, nil)
}

// (GET /api/v1/admin/tasks/{id}/source).
func (s *Server) DownloadTaskSource(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	if !requireAdmin(w, r) {
		return
	}
	if s.upload == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	sourceURL, err := s.upload.PresignedSourceFileURL(r.Context(), id)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}

	http.Redirect(w, r, sourceURL, http.StatusFound)
}

// (POST /api/v1/admin/tasks/{id}/source).
func (s *Server) UploadTaskSource(
	w http.ResponseWriter,
	r *http.Request,
	id openapi_types.UUID,
	_ api.UploadTaskSourceParams,
) {
	if !requireAdmin(w, r) {
		return
	}
	if s.upload == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}

	file, header, err := parseSourceFile(w, r)
	if err != nil {
		if errors.Is(err, errUploadTooLarge) {
			writeProblem(w, r, http.StatusRequestEntityTooLarge, "request body is too large")
			return
		}
		errmap.HandleError(w, r, domain.ErrTaskValidation)
		return
	}
	defer func() {
		_ = file.Close()
	}()
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	sourceURL, err := s.upload.UploadSourceFile(
		r.Context(),
		id,
		file,
		header.Size,
		header.Header.Get("Content-Type"),
	)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, api.TaskSourceUploadResponse{SourceFileUrl: sourceURL})
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := middleware.GetAdminClaimsFromCtx(r.Context()); ok {
		return true
	}
	errmap.HandleError(w, r, domain.ErrInvalidCredentials)
	return false
}

func adminActorFromRequest(r *http.Request) (playerusecase.Actor, bool) {
	claims, ok := middleware.GetAdminClaimsFromCtx(r.Context())
	if !ok || claims == nil || claims.Subject == "" || claims.JTI == "" {
		return playerusecase.Actor{}, false
	}
	return playerusecase.Actor{
		Subject: claims.Subject,
		JTI:     claims.JTI,
	}, true
}
