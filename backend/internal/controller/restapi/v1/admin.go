package v1

import (
	"context"
	"errors"
	"net/http"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase"
)

// (POST /api/v1/admin/login).
func (s *Server) AdminLogin(w http.ResponseWriter, r *http.Request) {
	if s.adminAuth == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	if !s.loginLimiter.Allow(middleware.ClientIPFromRequest(r)) {
		w.Header().Set("Retry-After", s.loginLimiter.RetryAfter())
		s.logSecurityEvent(r, "admin.login", securityOutcomeRateLimited, nil)
		errmap.HandleError(w, r, apperr.ErrRateLimited)
		return
	}

	var body openapi.AdminLoginRequest
	if !decodeJSONBody(w, r, &body, apperr.ErrInvalidCredentials) {
		s.logSecurityEvent(r, "admin.login", securityOutcomeFailure, logkitFields("error_code", apperr.CodeInvalidCredentials))
		return
	}
	if body.Password == nil {
		s.logSecurityEvent(r, "admin.login", securityOutcomeFailure, logkitFields("error_code", apperr.CodeInvalidCredentials))
		errmap.HandleError(w, r, apperr.ErrInvalidCredentials)
		return
	}

	pair, err := s.adminAuth.Login(r.Context(), *body.Password)
	if err != nil {
		s.logSecurityEvent(r, "admin.login", securityOutcomeFailure, logkitFields("error_code", securityErrorCode(err)))
		errmap.HandleError(w, r, err)
		return
	}

	s.logSecurityEvent(r, "admin.login", securityOutcomeSuccess, nil)
	if err := middleware.SetAdminSessionCookies(w, r, pair); err != nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, adminTokenResponse(r, pair, middleware.IsBrowserSourcedRequest(r), s.now()))
}

// (POST /api/v1/admin/logout).
func (s *Server) AdminLogout(w http.ResponseWriter, r *http.Request) {
	actor, _ := adminActorFromRequest(r)
	if s.adminAuth == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	var body openapi.AdminLogoutRequest
	if !decodeJSONBody(w, r, &body, apperr.ErrInvalidCredentials) {
		s.logSecurityEvent(r, "admin.logout", securityOutcomeFailure, adminSecurityFields(actor, apperr.CodeInvalidCredentials))
		return
	}
	refreshToken := body.RefreshToken
	if refreshToken == "" {
		if cookieToken, ok := middleware.AdminRefreshTokenFromRequest(r); ok {
			refreshToken = cookieToken
		}
	}
	if refreshToken == "" {
		s.logSecurityEvent(r, "admin.logout", securityOutcomeFailure, adminSecurityFields(actor, apperr.CodeInvalidCredentials))
		errmap.HandleError(w, r, apperr.ErrInvalidCredentials)
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
func (s *Server) AdminRefresh(w http.ResponseWriter, r *http.Request) {
	if s.adminAuth == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	if !s.refreshLimiter.Allow(middleware.ClientIPFromRequest(r)) {
		w.Header().Set("Retry-After", s.refreshLimiter.RetryAfter())
		s.logSecurityEvent(r, "admin.refresh", securityOutcomeRateLimited, nil)
		errmap.HandleError(w, r, apperr.ErrRateLimited)
		return
	}

	var body openapi.AdminRefreshRequest
	if !decodeJSONBody(w, r, &body, apperr.ErrInvalidCredentials) {
		s.logSecurityEvent(r, "admin.refresh", securityOutcomeFailure, logkitFields("error_code", apperr.CodeInvalidCredentials))
		return
	}
	refreshToken := body.RefreshToken
	cookieRefresh := false
	if refreshToken == "" {
		if cookieToken, ok := middleware.AdminRefreshTokenFromRequest(r); ok {
			refreshToken = cookieToken
			cookieRefresh = true
		}
	}
	if refreshToken == "" {
		s.logSecurityEvent(r, "admin.refresh", securityOutcomeFailure, logkitFields("error_code", apperr.CodeInvalidCredentials))
		errmap.HandleError(w, r, apperr.ErrInvalidCredentials)
		return
	}

	pair, err := s.adminAuth.Refresh(r.Context(), refreshToken)
	if err != nil {
		s.logSecurityEvent(r, "admin.refresh", securityOutcomeFailure, logkitFields("error_code", securityErrorCode(err)))
		errmap.HandleError(w, r, err)
		return
	}

	s.logSecurityEvent(r, "admin.refresh", securityOutcomeSuccess, nil)
	if err := middleware.SetAdminSessionCookies(w, r, pair); err != nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, adminTokenResponse(r, pair, cookieRefresh, s.now()))
}

func adminTokenResponse(r *http.Request, pair *usecase.TokenPair, cookieSession bool, now time.Time) openapi.AdminTokenResponse {
	if cookieSession || middleware.IsBrowserSourcedRequest(r) {
		return response.CookieSessionTokenPair(pair, now)
	}
	return response.TokenPair(pair, now)
}

// (GET /api/v1/admin/players).
func (s *Server) ListAdminPlayers(w http.ResponseWriter, r *http.Request, params openapi.ListAdminPlayersParams) {
	if !requireAdmin(w, r) {
		return
	}
	if s.adminPlayers == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
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

	response.WriteJSON(w, http.StatusOK, response.AdminPlayers(players))
}

// (GET /api/v1/admin/players/{id}/audit).
func (s *Server) ListAdminPlayerAudit(
	w http.ResponseWriter,
	r *http.Request,
	id openapi_types.UUID,
	params openapi.ListAdminPlayerAuditParams,
) {
	if !requireAdmin(w, r) {
		return
	}
	if s.adminPlayers == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	limit := int32(50)
	if params.Limit != nil {
		if *params.Limit <= 0 {
			errmap.HandleError(w, r, apperr.ErrValidation)
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

	response.WriteJSON(w, http.StatusOK, response.AdminPlayerAuditEvents(events))
}

// (PUT /api/v1/admin/players/{id}).
func (s *Server) UpdateAdminPlayer(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	if !requireAdmin(w, r) {
		return
	}
	actor, ok := adminActorFromRequest(r)
	if !ok {
		errmap.HandleError(w, r, apperr.ErrInvalidCredentials)
		return
	}
	if s.adminPlayers == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	var body openapi.UpdateAdminPlayerRequest
	if !decodeJSONBody(w, r, &body, apperr.ErrValidation) {
		return
	}

	player, err := s.adminPlayers.UpdatePlayer(r.Context(), id, usecase.AdminPlayerInput{
		Username:           body.Username,
		Wins:               int(body.Wins),
		AverageSolveTimeMs: body.AverageSolveTimeMs,
	}, actor)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, response.AdminPlayer(*player))
}

// (DELETE /api/v1/admin/players/{id}).
func (s *Server) DeleteAdminPlayer(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	if !requireAdmin(w, r) {
		return
	}
	actor, ok := adminActorFromRequest(r)
	if !ok {
		errmap.HandleError(w, r, apperr.ErrInvalidCredentials)
		return
	}
	if s.adminPlayers == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
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
		errmap.HandleError(w, r, apperr.ErrInternal)
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
func (s *Server) CreateTask(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if s.tasks == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	var body openapi.CreateTaskRequest
	if !decodeJSONBody(w, r, &body, apperr.ErrTaskValidation) {
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
		errmap.HandleError(w, r, apperr.ErrInternal)
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
func (s *Server) UpdateTask(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	if !requireAdmin(w, r) {
		return
	}
	if s.tasks == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	existing, err := s.tasks.GetTask(r.Context(), id)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}

	var body openapi.UpdateTaskRequest
	if !decodeJSONBody(w, r, &body, apperr.ErrTaskValidation) {
		return
	}
	if !isValidUpdateTaskRequest(body) {
		errmap.HandleError(w, r, apperr.ErrTaskValidation)
		return
	}

	input := updateTaskInput(existing, body)
	var updated *domain.Task
	if body.SourceFileUrl.IsSet() {
		if s.upload == nil {
			errmap.HandleError(w, r, apperr.ErrInternal)
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
func (s *Server) DeleteTask(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	if !requireAdmin(w, r) {
		return
	}
	if s.tasks == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	existing, err := s.tasks.GetTask(r.Context(), id)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	if existing.SourceFileURL != nil && s.upload == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	if err := s.tasks.DeleteTask(r.Context(), id); err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	if existing.SourceFileURL != nil {
		_ = s.upload.DeleteSourceFile(context.WithoutCancel(r.Context()), id, existing.SourceFileURL)
	}

	response.WriteJSON(w, http.StatusNoContent, nil)
}

// (POST /api/v1/admin/tasks/{id}/source).
func (s *Server) UploadTaskSource(w http.ResponseWriter, r *http.Request, id openapi_types.UUID) {
	if !requireAdmin(w, r) {
		return
	}
	if s.upload == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	file, header, err := parseSourceFile(w, r)
	if err != nil {
		if errors.Is(err, errUploadTooLarge) {
			writeProblem(w, r, http.StatusRequestEntityTooLarge, "request body is too large")
			return
		}
		errmap.HandleError(w, r, apperr.ErrTaskValidation)
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

	response.WriteJSON(w, http.StatusOK, openapi.UploadSourceResponse{SourceFileUrl: sourceURL})
}

func requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := middleware.GetAdminClaimsFromCtx(r.Context()); ok {
		return true
	}
	errmap.HandleError(w, r, apperr.ErrInvalidCredentials)
	return false
}

func adminActorFromRequest(r *http.Request) (usecase.AdminActor, bool) {
	claims, ok := middleware.GetAdminClaimsFromCtx(r.Context())
	if !ok || claims == nil || claims.Subject == "" || claims.JTI == "" {
		return usecase.AdminActor{}, false
	}
	return usecase.AdminActor{
		Subject: claims.Subject,
		JTI:     claims.JTI,
	}, true
}
