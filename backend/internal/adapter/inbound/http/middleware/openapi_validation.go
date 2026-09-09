package middleware

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/google/uuid"
	openapimiddleware "github.com/oapi-codegen/nethttp-middleware"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
)

const maxOpenAPIRequestBodyBytes int64 = 1 << 20

// OpenAPIRequestValidator validates REST requests against the bundled OpenAPI
// contract. Authentication and authorization remain owned by the existing
// application middleware.
func OpenAPIRequestValidator(ctx context.Context, log logkit.Logger) (func(http.Handler) http.Handler, error) {
	swagger, err := api.GetSpec()
	if err != nil {
		return nil, fmt.Errorf("load OpenAPI spec: %w", err)
	}

	if err := swagger.Validate(ctx); err != nil {
		return nil, fmt.Errorf("validate OpenAPI spec: %w", err)
	}
	prepareOpenAPIValidationSpec(swagger)

	validator := openapimiddleware.OapiRequestValidatorWithOptions(swagger, &openapimiddleware.Options{
		Options: openapi3filter.Options{
			AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
		},
		ErrorHandlerWithOpts:  openAPIValidationErrorHandler(log),
		DoNotValidateServers:  true,
		SilenceServersWarning: true,
	})

	return func(next http.Handler) http.Handler {
		validated := validator(next)

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if shouldSkipOpenAPIValidation(r) {
				next.ServeHTTP(w, r)
				return
			}

			if r.Body != nil && r.Body != http.NoBody {
				r.Body = http.MaxBytesReader(w, r.Body, maxOpenAPIRequestBodyBytes)
			}
			normalizeOpenAPIContentType(r)

			validated.ServeHTTP(w, r)
		})
	}, nil
}

func normalizeOpenAPIContentType(r *http.Request) {
	raw := r.Header.Get("Content-Type")
	if raw == "" {
		return
	}

	mediaType, params, err := mime.ParseMediaType(raw)
	if err != nil {
		return
	}
	contentType := mime.FormatMediaType(strings.ToLower(mediaType), params)
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
}

func prepareOpenAPIValidationSpec(swagger *openapi3.T) {
	// Runtime authentication is authoritative. The validation copy only owns
	// request shape and routing constraints.
	swagger.Servers = nil
	swagger.Security = nil
	stripOpenAPIOperationSecurity(swagger.Paths)
}

func stripOpenAPIOperationSecurity(paths *openapi3.Paths) {
	if paths == nil {
		return
	}

	for _, pathItem := range paths.Map() {
		if pathItem == nil {
			continue
		}
		for _, operation := range pathItem.Operations() {
			if operation != nil {
				operation.Security = nil
			}
		}
	}
}

func openAPIValidationErrorHandler(log logkit.Logger) openapimiddleware.ErrorHandlerWithOpts {
	return func(ctx context.Context, err error, w http.ResponseWriter, r *http.Request, opts openapimiddleware.ErrorHandlerOpts) {
		status := openAPIValidationStatus(err, r, opts)
		if log != nil {
			fields := logkit.Fields{
				"error_type": fmt.Sprintf("%T", err),
				"method":     r.Method,
				"path":       r.URL.Path,
				"status":     status,
			}
			if requestID := GetRequestIDFromCtx(ctx); requestID != "" {
				fields["request_id"] = requestID
			}
			log.WarnContext(ctx, "openapi request validation failed", fields)
		}

		writeProblem(w, r, status, http.StatusText(status), "invalid request")
	}
}

func openAPIValidationStatus(err error, r *http.Request, opts openapimiddleware.ErrorHandlerOpts) int {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return http.StatusRequestEntityTooLarge
	}
	if hasUnsupportedOpenAPIMediaType(err, r) {
		return http.StatusUnsupportedMediaType
	}

	switch opts.StatusCode {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed:
		return opts.StatusCode
	default:
		return http.StatusInternalServerError
	}
}

func hasUnsupportedOpenAPIMediaType(err error, r *http.Request) bool {
	if r.Body == nil || r.Body == http.NoBody || r.ContentLength == 0 {
		return false
	}

	var requestErr *openapi3filter.RequestError
	if !errors.As(err, &requestErr) || requestErr.RequestBody == nil {
		return false
	}
	content := requestErr.RequestBody.Content
	if len(content) == 0 {
		return false
	}
	if content.Get(r.Header.Get("Content-Type")) == nil {
		return true
	}

	var parseErr *openapi3filter.ParseError

	return errors.As(requestErr.Err, &parseErr) && parseErr.Kind == openapi3filter.KindUnsupportedFormat
}

func shouldSkipOpenAPIValidation(r *http.Request) bool {
	if isTournamentRealtimePath(r.URL.Path) {
		return true
	}
	if r.Method != http.MethodPost {
		return false
	}

	const prefix = "/api/v1/admin/tasks/"
	tail, ok := strings.CutPrefix(r.URL.Path, prefix)
	if !ok {
		return false
	}
	taskID, endpoint, ok := strings.Cut(tail, "/")
	if _, err := uuid.Parse(taskID); err != nil {
		return false
	}

	return ok && endpoint == "source"
}

func isTournamentRealtimePath(requestPath string) bool {
	prefixes := []string{
		"/api/v1/tournaments/",
		"/api/v1/admin/tournaments/",
	}
	for _, prefix := range prefixes {
		tail, ok := strings.CutPrefix(requestPath, prefix)
		if !ok {
			continue
		}
		parts := strings.Split(strings.Trim(tail, "/"), "/")
		if len(parts) < 2 {
			return false
		}
		if _, err := uuid.Parse(parts[0]); err != nil {
			return false
		}
		if len(parts) == 2 && parts[1] == "realtime" {
			return true
		}
		return prefix == "/api/v1/tournaments/" && len(parts) == 3 &&
			parts[1] == "participant" && parts[2] == "realtime"
	}
	return false
}
