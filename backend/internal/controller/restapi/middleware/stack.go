package middleware

import (
	"bytes"
	"context"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	httpkitmw "github.com/wahrwelt-kit/go-httpkit/httputil/middleware"
	logkit "github.com/wahrwelt-kit/go-logkit"
)

const (
	defaultStackTimeout = 30 * time.Second
	stackCSP            = "default-src 'self'"
)

// StackOption configures the REST middleware stack.
type StackOption func(*stackConfig)

type stackConfig struct {
	trustedProxyCIDRs []string
	allowedOrigins    []string
	timeout           time.Duration
}

// WithTrustedProxyCIDRs configures CIDRs that are allowed to supply client IP headers.
func WithTrustedProxyCIDRs(cidrs []string) StackOption {
	return func(cfg *stackConfig) {
		cfg.trustedProxyCIDRs = append([]string(nil), cidrs...)
	}
}

// WithAllowedOrigins configures exact origins accepted by CSRF Origin checks.
func WithAllowedOrigins(origins []string) StackOption {
	return func(cfg *stackConfig) {
		cfg.allowedOrigins = append([]string(nil), origins...)
	}
}

// WithTimeout overrides the default request timeout.
func WithTimeout(timeout time.Duration) StackOption {
	return func(cfg *stackConfig) {
		if timeout > 0 {
			cfg.timeout = timeout
		}
	}
}

// Build creates the REST middleware stack.
func Build(log logkit.Logger, opts ...StackOption) func(http.Handler) http.Handler {
	return build(log, true, opts...)
}

// BuildStreaming creates the common safety stack for long-lived streaming
// endpoints without wrapping the handler in a request timeout goroutine.
func BuildStreaming(log logkit.Logger, opts ...StackOption) func(http.Handler) http.Handler {
	return build(log, false, opts...)
}

func build(log logkit.Logger, withTimeout bool, opts ...StackOption) func(http.Handler) http.Handler {
	cfg := stackConfig{timeout: defaultStackTimeout}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	clientIP, err := httpkitmw.ClientIP(cfg.trustedProxyCIDRs)
	if err != nil {
		if log != nil {
			log.Warn("invalid trusted proxy CIDRs, using RemoteAddr only", logkit.Fields{"error": err.Error()})
		}
		clientIP, _ = httpkitmw.ClientIP(nil)
	}

	middlewares := []func(http.Handler) http.Handler{
		httpkitmw.RequestID(),
		clientIP,
		ForwardedProto(cfg.trustedProxyCIDRs, log),
		Logger(log),
		Recoverer(log),
		httpkitmw.SecurityHeaders(false, httpkitmw.WithCSP(stackCSP)),
		NoStoreSensitiveResponses(),
		OriginGuard(cfg.allowedOrigins),
		CSRFGuard(),
	}
	if withTimeout {
		middlewares = append(middlewares, Timeout(cfg.timeout, log))
	}
	return chain(middlewares...)
}

// GetRequestIDFromCtx returns the request ID stored by the REST stack.
func GetRequestIDFromCtx(ctx context.Context) string {
	return httpkitmw.GetRequestID(ctx)
}

func chain(middlewares ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		for i := len(middlewares) - 1; i >= 0; i-- {
			next = middlewares[i](next)
		}
		return next
	}
}

// Logger writes one structured JSON log entry for each HTTP request.
func Logger(log logkit.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if log == nil {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			recorder := newStatusRecorder(w)

			next.ServeHTTP(recorder, r)

			duration := time.Since(start)
			fields := logkit.Fields{
				"request_id":  httpkitmw.GetRequestID(r.Context()),
				"method":      r.Method,
				"path":        r.URL.Path,
				"status":      recorder.Status(),
				"duration":    duration.String(),
				"duration_ms": duration.Milliseconds(),
			}
			if ip := httpkitmw.GetClientIPFromContext(r.Context()); ip != "" {
				fields["client_ip"] = ip
			}

			switch {
			case recorder.Status() >= http.StatusInternalServerError:
				log.Error("http request failed", fields)
			case recorder.Status() >= http.StatusBadRequest:
				log.Warn("http request error", fields)
			default:
				log.Info("http request", fields)
			}
		})
	}
}

// Recoverer converts panics into a stable internal error response and logs details.
func Recoverer(log logkit.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requestID := httpkitmw.GetRequestID(r.Context())
			method := r.Method
			path := r.URL.Path

			defer func() {
				panicValue := recover()
				if panicValue == nil {
					return
				}

				logRecoveredPanic(log, requestID, method, path, panicValue, nil)

				writeProblem(w, r, http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError), "internal")
			}()

			next.ServeHTTP(w, r)
		})
	}
}

func logRecoveredPanic(
	log logkit.Logger,
	requestID string,
	method string,
	path string,
	panicValue any,
	extra logkit.Fields,
) {
	if log == nil {
		return
	}
	fields := logkit.Fields{
		"request_id": requestID,
		"method":     method,
		"path":       path,
		"panic":      panicValue,
		"stack":      string(debug.Stack()),
	}
	for key, value := range extra {
		fields[key] = value
	}
	log.Error("panic recovered", fields)
}

// Timeout runs a handler with a request context deadline and returns 503 on expiry.
func Timeout(timeout time.Duration, logs ...logkit.Logger) func(http.Handler) http.Handler {
	if timeout <= 0 {
		timeout = defaultStackTimeout
	}
	var log logkit.Logger
	if len(logs) > 0 {
		log = logs[0]
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			writer := newTimeoutWriter(w, r)
			done := make(chan timeoutResult, 1)
			requestID := httpkitmw.GetRequestID(r.Context())
			method := r.Method
			path := r.URL.Path

			go func() {
				defer func() {
					panicValue := recover()
					timedOut := ctx.Err() != nil
					if panicValue != nil && timedOut {
						logRecoveredPanic(log, requestID, method, path, panicValue, logkit.Fields{
							"after_timeout": true,
						})
					}
					done <- timeoutResult{panicValue: panicValue, timedOut: timedOut}
				}()
				next.ServeHTTP(writer, r.WithContext(ctx))
			}()

			select {
			case result := <-done:
				if result.timedOut {
					writer.writeTimeout()
					return
				}
				if result.panicValue != nil {
					panic(result.panicValue)
				}
				writer.flush()
			case <-ctx.Done():
				writer.writeTimeout()
			}
		})
	}
}

type timeoutResult struct {
	panicValue any
	timedOut   bool
}

type timeoutWriter struct {
	dst    http.ResponseWriter
	req    *http.Request
	header http.Header

	mu       sync.Mutex
	status   int
	body     bytes.Buffer
	done     bool
	timedOut bool
}

func newTimeoutWriter(dst http.ResponseWriter, req *http.Request) *timeoutWriter {
	return &timeoutWriter{
		dst:    dst,
		req:    req,
		header: make(http.Header),
	}
}

func (w *timeoutWriter) Header() http.Header {
	return w.header
}

func (w *timeoutWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.done || w.timedOut || w.status != 0 {
		return
	}
	w.status = status
}

func (w *timeoutWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.timedOut {
		return 0, context.DeadlineExceeded
	}
	if w.done {
		return 0, http.ErrHandlerTimeout
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}

func (w *timeoutWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.done || w.timedOut {
		return
	}
	w.done = true
	if w.status == 0 {
		w.status = http.StatusOK
	}

	copyHeader(w.dst.Header(), w.header)
	w.dst.WriteHeader(w.status)
	_, _ = w.body.WriteTo(w.dst)
}

func (w *timeoutWriter) writeTimeout() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.done || w.timedOut {
		return
	}
	w.timedOut = true

	writeProblem(w.dst, w.req, http.StatusServiceUnavailable, http.StatusText(http.StatusServiceUnavailable), "timeout")
}

func copyHeader(dst, src http.Header) {
	for key, values := range src {
		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

type statusRecorder struct {
	http.ResponseWriter

	mu          sync.Mutex
	status      int
	wroteHeader bool
}

func (w *statusRecorder) WriteHeader(status int) {
	w.mu.Lock()
	if w.wroteHeader {
		w.mu.Unlock()
		return
	}
	w.wroteHeader = true
	w.status = status
	w.mu.Unlock()

	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(data []byte) (int, error) {
	w.mu.Lock()
	shouldWriteHeader := !w.wroteHeader
	if shouldWriteHeader {
		w.wroteHeader = true
		w.status = http.StatusOK
	}
	w.mu.Unlock()

	if shouldWriteHeader {
		w.ResponseWriter.WriteHeader(http.StatusOK)
	}

	return w.ResponseWriter.Write(data)
}

func (w *statusRecorder) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

type responseStatusRecorder interface {
	http.ResponseWriter
	Status() int
}

func newStatusRecorder(w http.ResponseWriter) responseStatusRecorder {
	recorder := &statusRecorder{ResponseWriter: w}
	if _, ok := w.(http.Flusher); ok {
		return &flushStatusRecorder{statusRecorder: recorder}
	}
	return recorder
}

type flushStatusRecorder struct {
	*statusRecorder
}

func (w *flushStatusRecorder) Flush() {
	if !w.hasWrittenHeader() {
		w.WriteHeader(http.StatusOK)
	}
	w.ResponseWriter.(http.Flusher).Flush()
}

func (w *statusRecorder) hasWrittenHeader() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.wroteHeader
}
