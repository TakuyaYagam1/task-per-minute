package v1

import (
	"errors"
	"net/http"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/TakuyaYagam1/task-per-minute/internal/apperr"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/v1/request"
	"github.com/TakuyaYagam1/task-per-minute/internal/controller/restapi/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/openapi"
)

// (POST /api/v1/admin/login).
func (s *Server) AdminLogin(w http.ResponseWriter, r *http.Request) {
	if s.adminAuth == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	if !s.loginLimiter.Allow(middleware.ClientIPFromRequest(r)) {
		w.Header().Set("Retry-After", s.loginLimiter.RetryAfter())
		errmap.HandleError(w, r, apperr.ErrRateLimited)
		return
	}

	var body openapi.AdminLoginRequest
	if err := request.DecodeJSON(r, &body); err != nil || body.Password == nil {
		errmap.HandleError(w, r, apperr.ErrInvalidCredentials)
		return
	}

	pair, err := s.adminAuth.Login(r.Context(), *body.Password)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, response.TokenPair(pair, s.now()))
}

// (POST /api/v1/admin/logout).
func (s *Server) AdminLogout(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if s.adminAuth == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	var body openapi.AdminLogoutRequest
	if err := request.DecodeJSON(r, &body); err != nil || body.RefreshToken == "" {
		errmap.HandleError(w, r, apperr.ErrInvalidCredentials)
		return
	}

	if err := s.adminAuth.Logout(r.Context(), body.RefreshToken); err != nil {
		errmap.HandleError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// (POST /api/v1/admin/refresh).
func (s *Server) AdminRefresh(w http.ResponseWriter, r *http.Request) {
	if s.adminAuth == nil {
		errmap.HandleError(w, r, apperr.ErrInternal)
		return
	}

	var body openapi.AdminRefreshRequest
	if err := request.DecodeJSON(r, &body); err != nil || body.RefreshToken == "" {
		errmap.HandleError(w, r, apperr.ErrInvalidCredentials)
		return
	}

	pair, err := s.adminAuth.Refresh(r.Context(), body.RefreshToken)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}

	response.WriteJSON(w, http.StatusOK, response.TokenPair(pair, s.now()))
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
	if err := request.DecodeJSON(r, &body); err != nil {
		errmap.HandleError(w, r, apperr.ErrTaskValidation)
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

	var body updateTaskRequest
	if err := request.DecodeJSON(r, &body); err != nil {
		errmap.HandleError(w, r, apperr.ErrTaskValidation)
		return
	}

	updated, err := s.tasks.UpdateTask(r.Context(), id, updateTaskInput(existing, body))
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

	if err := s.tasks.DeleteTask(r.Context(), id); err != nil {
		errmap.HandleError(w, r, err)
		return
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
