package v1

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	usecase "github.com/TakuyaYagam1/task-per-minute/internal/port/inbound"
)

const (
	maxAvatarFileBytes    int64 = 5 << 20
	maxAvatarRequestBytes int64 = maxAvatarFileBytes + 64<<10
)

func (s *Server) GetPlayerAccountSettings(w http.ResponseWriter, r *http.Request) {
	setPrivateNoStore(w)
	playerID, sessionToken, ok := s.currentPlayerSession(w, r)
	if !ok {
		return
	}
	if s.accountSettings == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	settings, err := s.accountSettings.GetAccountSettings(r.Context(), playerID, sessionToken)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	if settings == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	if err := middleware.EnsurePlayerCSRFCookie(w, r, sessionToken); err != nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, accountSettingsResponse(settings))
}

func (s *Server) ChangePlayerUsername(w http.ResponseWriter, r *http.Request, _ api.ChangePlayerUsernameParams) {
	setPrivateNoStore(w)
	if !s.allowAccountSensitiveOperation(w, r, "player.account.username") {
		return
	}
	playerID, sessionToken, ok := s.currentPlayerSession(w, r)
	if !ok {
		return
	}
	if s.accountSettings == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	var body api.ChangePlayerUsernameJSONRequestBody
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	player, err := s.accountSettings.ChangeUsername(r.Context(), playerID, sessionToken, usecase.ChangeUsernameCommand{
		CurrentPassword: accountRequestString(body.CurrentPassword),
		Username:        body.Username,
	})
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	if player == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, response.Player(player))
}

func (s *Server) ChangePlayerPassword(w http.ResponseWriter, r *http.Request, _ api.ChangePlayerPasswordParams) {
	setPrivateNoStore(w)
	if !s.allowAccountSensitiveOperation(w, r, "player.account.password") {
		return
	}
	playerID, sessionToken, ok := s.currentPlayerSession(w, r)
	if !ok {
		return
	}
	if s.accountSettings == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	var body api.ChangePlayerPasswordJSONRequestBody
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	player, err := s.accountSettings.ChangePassword(r.Context(), playerID, sessionToken, usecase.ChangePasswordCommand{
		CurrentPassword: accountRequestString(body.CurrentPassword),
		NewPassword:     accountRequestString(body.NewPassword),
	})
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	if !rotatePlayerSession(w, r, player) {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) BeginPlayerEmailChange(w http.ResponseWriter, r *http.Request, _ api.BeginPlayerEmailChangeParams) {
	setPrivateNoStore(w)
	if !s.allowAccountSensitiveOperation(w, r, "player.account.email.begin") {
		return
	}
	playerID, sessionToken, ok := s.currentPlayerSession(w, r)
	if !ok {
		return
	}
	if s.accountSettings == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	var body api.BeginPlayerEmailChangeJSONRequestBody
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	settings, err := s.accountSettings.BeginEmailChange(r.Context(), playerID, sessionToken, usecase.BeginEmailChangeCommand{
		CurrentPassword: accountRequestString(body.CurrentPassword),
		NewEmail:        string(body.NewEmail),
	})
	if err != nil {
		setEmailChangeRetryAfter(w, err)
		errmap.HandleError(w, r, err)
		return
	}
	writeAccountSettings(w, r, http.StatusAccepted, settings)
}

func (s *Server) ResendPlayerEmailChange(w http.ResponseWriter, r *http.Request, _ api.ResendPlayerEmailChangeParams) {
	setPrivateNoStore(w)
	if !s.allowAccountSensitiveOperation(w, r, "player.account.email.resend") {
		return
	}
	playerID, sessionToken, ok := s.currentPlayerSession(w, r)
	if !ok {
		return
	}
	if s.accountSettings == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	settings, err := s.accountSettings.ResendEmailChange(r.Context(), playerID, sessionToken)
	if err != nil {
		setEmailChangeRetryAfter(w, err)
		errmap.HandleError(w, r, err)
		return
	}
	writeAccountSettings(w, r, http.StatusAccepted, settings)
}

func (s *Server) CancelPlayerEmailChange(w http.ResponseWriter, r *http.Request, _ api.CancelPlayerEmailChangeParams) {
	setPrivateNoStore(w)
	if !s.allowAccountSensitiveOperation(w, r, "player.account.email.cancel") {
		return
	}
	playerID, sessionToken, ok := s.currentPlayerSession(w, r)
	if !ok {
		return
	}
	if s.accountSettings == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	settings, err := s.accountSettings.CancelEmailChange(r.Context(), playerID, sessionToken)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	writeAccountSettings(w, r, http.StatusOK, settings)
}

func (s *Server) ConfirmPlayerEmailChange(w http.ResponseWriter, r *http.Request, _ api.ConfirmPlayerEmailChangeParams) {
	setPrivateNoStore(w)
	if !s.allowAccountSensitiveOperation(w, r, "player.account.email.confirm") {
		return
	}
	playerID, sessionToken, ok := s.currentPlayerSession(w, r)
	if !ok {
		return
	}
	if s.accountSettings == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	var body api.ConfirmPlayerEmailChangeJSONRequestBody
	if !decodeJSONBody(w, r, &body, domain.ErrValidation) {
		return
	}
	result, err := s.accountSettings.ConfirmEmailChange(r.Context(), playerID, sessionToken, usecase.ConfirmEmailChangeCommand{Code: body.Code})
	if err != nil {
		setEmailChangeRetryAfter(w, err)
		errmap.HandleError(w, r, err)
		return
	}
	if result == nil || !rotatePlayerSession(w, r, result.Player) {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, api.PlayerAccountEmailChangeConfirmedResponse{
		Email:                 openapi_types.Email(result.Email),
		PreviousEmailNotified: result.PreviousEmailNotified,
	})
}

func (s *Server) GetPlayerAccountAvatar(w http.ResponseWriter, r *http.Request) {
	setPrivateNoStore(w)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	playerID, sessionToken, ok := s.currentPlayerSession(w, r)
	if !ok {
		return
	}
	if s.playerAvatars == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	avatar, err := s.playerAvatars.GetAvatar(r.Context(), playerID, sessionToken)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	if len(avatar.Data) == 0 || !validAvatarMediaType(avatar.ContentType) {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	w.Header().Set("Content-Type", avatar.ContentType)
	w.Header().Set("Content-Length", int64Header(len(avatar.Data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(avatar.Data)
}

func (s *Server) ReplacePlayerAccountAvatar(w http.ResponseWriter, r *http.Request, _ api.ReplacePlayerAccountAvatarParams) {
	setPrivateNoStore(w)
	if !s.allowAvatarMutation(w, r) {
		return
	}
	if !s.acquireAvatarProcessingSlot(w, r) {
		return
	}
	defer s.releaseAvatarProcessingSlot()
	playerID, sessionToken, ok := s.currentPlayerSession(w, r)
	if !ok {
		return
	}
	if s.playerAvatars == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	declaredContentType, data, err := readAvatarUpload(w, r)
	if err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	if err := s.playerAvatars.ReplaceAvatar(r.Context(), playerID, sessionToken, declaredContentType, data); err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) DeletePlayerAccountAvatar(w http.ResponseWriter, r *http.Request, _ api.DeletePlayerAccountAvatarParams) {
	setPrivateNoStore(w)
	if !s.allowAvatarMutation(w, r) {
		return
	}
	playerID, sessionToken, ok := s.currentPlayerSession(w, r)
	if !ok {
		return
	}
	if s.playerAvatars == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	if err := s.playerAvatars.DeleteAvatar(r.Context(), playerID, sessionToken); err != nil {
		errmap.HandleError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) currentPlayerSession(w http.ResponseWriter, r *http.Request) (playerID, sessionToken uuid.UUID, ok bool) {
	player, found := middleware.GetPlayerFromCtx(r.Context())
	if !found || player == nil || player.ID == uuid.Nil || player.SessionToken == nil || *player.SessionToken == uuid.Nil {
		errmap.HandleError(w, r, domain.ErrInvalidSession)
		return uuid.Nil, uuid.Nil, false
	}
	return player.ID, *player.SessionToken, true
}

func accountSettingsResponse(settings *domain.AccountSettings) api.PlayerAccountSettingsResponse {
	response := api.PlayerAccountSettingsResponse{
		Username:               settings.Username,
		Email:                  openapi_types.Email(settings.Email),
		EmailResendAvailableAt: settings.EmailResendAvailableAt,
		PendingEmailExpiresAt:  settings.PendingEmailExpiresAt,
	}
	if settings.PendingEmail != nil {
		pendingEmail := openapi_types.Email(*settings.PendingEmail)
		response.PendingEmail = &pendingEmail
	}
	return response
}

func writeAccountSettings(w http.ResponseWriter, r *http.Request, status int, settings *domain.AccountSettings) {
	if settings == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, status, accountSettingsResponse(settings))
}

func rotatePlayerSession(w http.ResponseWriter, r *http.Request, player *domain.Player) bool {
	if player == nil || player.SessionToken == nil || *player.SessionToken == uuid.Nil {
		return false
	}
	csrfToken, err := middleware.NewPlayerCSRFToken(*player.SessionToken)
	if err != nil {
		return false
	}
	middleware.SetPlayerSessionCookie(w, r, *player.SessionToken)
	middleware.SetPlayerCSRFCookie(w, r, csrfToken)
	return true
}

func setPrivateNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
	// Avoid retaining private account or image responses in shared intermediaries.
	w.Header().Set("Pragma", "no-cache")
}

func setEmailChangeRetryAfter(w http.ResponseWriter, err error) {
	var retryAfterErr interface{ RetryAfter() time.Duration }
	if errors.As(err, &retryAfterErr) {
		wait := retryAfterErr.RetryAfter()
		seconds := int64(wait / time.Second)
		if wait%time.Second != 0 {
			seconds++
		}
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
		return
	}
	if errors.Is(err, domain.ErrEmailChangeRateLimited) {
		w.Header().Set("Retry-After", "60")
	} else if errors.Is(err, domain.ErrEmailChangeAttemptsExceeded) {
		w.Header().Set("Retry-After", "3600")
	}
}

func readAvatarUpload(w http.ResponseWriter, r *http.Request) (string, []byte, error) {
	if r.ContentLength > maxAvatarRequestBytes {
		return "", nil, domain.ErrAvatarTooLarge
	}
	mediaType, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") || parameters["boundary"] == "" {
		return "", nil, domain.ErrAvatarUnsupportedMediaType
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAvatarRequestBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			return "", nil, domain.ErrAvatarTooLarge
		}
		return "", nil, domain.ErrValidation
	}
	reader := multipart.NewReader(bytes.NewReader(body), parameters["boundary"])
	part, err := reader.NextPart()
	if err != nil || part.FormName() != "file" || part.FileName() == "" {
		return "", nil, domain.ErrValidation
	}
	declaredContentType, _, err := mime.ParseMediaType(part.Header.Get("Content-Type"))
	if err != nil || !validAvatarMediaType(declaredContentType) {
		return "", nil, domain.ErrAvatarUnsupportedMediaType
	}
	data, err := io.ReadAll(io.LimitReader(part, maxAvatarFileBytes+1))
	if err != nil {
		return "", nil, domain.ErrValidation
	}
	if int64(len(data)) > maxAvatarFileBytes {
		return "", nil, domain.ErrAvatarTooLarge
	}
	if _, err := reader.NextPart(); err != io.EOF {
		return "", nil, domain.ErrValidation
	}
	return strings.ToLower(declaredContentType), data, nil
}

func validAvatarMediaType(contentType string) bool {
	switch strings.ToLower(strings.TrimSpace(contentType)) {
	case "image/jpeg", "image/png", "image/gif", "video/mp4":
		return true
	default:
		return false
	}
}

func int64Header(value int) string {
	return strconv.FormatInt(int64(value), 10)
}
