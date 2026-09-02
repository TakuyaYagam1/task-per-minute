package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	maxArenaMutationBodyBytes  int64 = 1 << 20
	defaultArenaIdempotencyTTL       = 24 * time.Hour
)

type ArenaEndpointLimit struct {
	Requests int
	Window   time.Duration
}

type ArenaLimitConfig struct {
	PublicRead          ArenaEndpointLimit
	OperatorRead        ArenaEndpointLimit
	OperatorMutation    ArenaEndpointLimit
	ParticipantRead     ArenaEndpointLimit
	ParticipantMutation ArenaEndpointLimit
	IdempotencyTTL      time.Duration
}

type ArenaRequestLimits struct {
	config ArenaLimitConfig

	mu          sync.Mutex
	buckets     map[string]arenaLimitBucket
	idempotency map[string]time.Time
	now         func() time.Time
}

type arenaLimitBucket struct {
	startedAt time.Time
	count     int
}

type arenaEndpointClass string

const (
	arenaEndpointPublicRead          arenaEndpointClass = "public_read"
	arenaEndpointOperatorRead        arenaEndpointClass = "operator_read"
	arenaEndpointOperatorMutation    arenaEndpointClass = "operator_mutation"
	arenaEndpointParticipantRead     arenaEndpointClass = "participant_read"
	arenaEndpointParticipantMutation arenaEndpointClass = "participant_mutation"
)

func NewArenaRequestLimits(config ArenaLimitConfig) *ArenaRequestLimits {
	if config.IdempotencyTTL <= 0 {
		config.IdempotencyTTL = defaultArenaIdempotencyTTL
	}
	return &ArenaRequestLimits{
		config:      config,
		buckets:     make(map[string]arenaLimitBucket),
		idempotency: make(map[string]time.Time),
		now:         time.Now,
	}
}

func (l *ArenaRequestLimits) Middleware(next http.Handler) http.Handler {
	if l == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		class, arena := classifyArenaEndpoint(r)
		if !arena {
			next.ServeHTTP(w, r)
			return
		}

		actorKey := arenaLimitActorKey(r)
		var idempotencyKey uuid.UUID
		if isArenaMutation(class) {
			key, err := uuid.Parse(strings.TrimSpace(r.Header.Get("Idempotency-Key")))
			if err != nil || key == uuid.Nil {
				writeProblem(w, r, http.StatusBadRequest, http.StatusText(http.StatusBadRequest), "invalid idempotency key")
				return
			}
			idempotencyKey = key
			if !validateArenaMutationEvidence(w, r, class) {
				return
			}
		}

		allowed, replay := l.allow(class, actorKey, idempotencyKey)
		if replay {
			writeProblem(w, r, http.StatusConflict, http.StatusText(http.StatusConflict), "arena command already received")
			return
		}
		if !allowed {
			limit := l.endpointLimit(class)
			w.Header().Set("Retry-After", retryAfterSeconds(limit.Window))
			writeProblem(w, r, http.StatusTooManyRequests, http.StatusText(http.StatusTooManyRequests), "arena request rate limit exceeded")
			return
		}

		writer := &arenaStatusWriter{ResponseWriter: w}
		completed := false
		defer func() {
			if idempotencyKey != uuid.Nil && (!completed || writer.status >= http.StatusBadRequest) {
				l.releaseIdempotency(actorKey, idempotencyKey)
			}
		}()
		next.ServeHTTP(writer, r)
		completed = true
	})
}

type arenaStatusWriter struct {
	http.ResponseWriter

	status int
}

func (w *arenaStatusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *arenaStatusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

func (l *ArenaRequestLimits) allow(class arenaEndpointClass, actorKey string, idempotencyKey uuid.UUID) (bool, bool) {
	now := l.now()
	limit := l.endpointLimit(class)
	idempotencyScope := ""
	if idempotencyKey != uuid.Nil {
		idempotencyScope = actorKey + ":" + idempotencyKey.String()
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if idempotencyScope != "" {
		if expiresAt, exists := l.idempotency[idempotencyScope]; exists && now.Before(expiresAt) {
			return false, true
		}
	}
	if limit.Requests > 0 && limit.Window > 0 {
		bucketKey := string(class) + ":" + actorKey
		bucket := l.buckets[bucketKey]
		if bucket.startedAt.IsZero() || now.Sub(bucket.startedAt) >= limit.Window {
			bucket = arenaLimitBucket{startedAt: now}
		}
		if bucket.count >= limit.Requests {
			return false, false
		}
		bucket.count++
		l.buckets[bucketKey] = bucket
	}
	if idempotencyScope != "" {
		l.idempotency[idempotencyScope] = now.Add(l.config.IdempotencyTTL)
	}
	return true, false
}

func (l *ArenaRequestLimits) endpointLimit(class arenaEndpointClass) ArenaEndpointLimit {
	switch class {
	case arenaEndpointPublicRead:
		return l.config.PublicRead
	case arenaEndpointOperatorRead:
		return l.config.OperatorRead
	case arenaEndpointOperatorMutation:
		return l.config.OperatorMutation
	case arenaEndpointParticipantRead:
		return l.config.ParticipantRead
	case arenaEndpointParticipantMutation:
		return l.config.ParticipantMutation
	default:
		return ArenaEndpointLimit{}
	}
}

func (l *ArenaRequestLimits) releaseIdempotency(actorKey string, idempotencyKey uuid.UUID) {
	l.mu.Lock()
	delete(l.idempotency, actorKey+":"+idempotencyKey.String())
	l.mu.Unlock()
}

func classifyArenaEndpoint(r *http.Request) (arenaEndpointClass, bool) {
	if r == nil || !strings.HasPrefix(r.URL.Path, "/api/v1/arena/") {
		return "", false
	}
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/arena/")
	switch {
	case strings.HasPrefix(path, "public/"):
		return arenaEndpointPublicRead, read
	case strings.HasPrefix(path, "operator/") && read:
		return arenaEndpointOperatorRead, true
	case strings.HasPrefix(path, "operator/"):
		return arenaEndpointOperatorMutation, true
	case strings.Contains(path, "/participant/") && read:
		return arenaEndpointParticipantRead, true
	case strings.Contains(path, "/participant/"):
		return arenaEndpointParticipantMutation, true
	default:
		return "", false
	}
}

func isArenaMutation(class arenaEndpointClass) bool {
	return class == arenaEndpointOperatorMutation || class == arenaEndpointParticipantMutation
}

func arenaLimitActorKey(r *http.Request) string {
	if claims, ok := GetAdminClaimsFromCtx(r.Context()); ok && claims.Subject != "" {
		return "operator:" + claims.Subject
	}
	if player, ok := GetPlayerFromCtx(r.Context()); ok && player.ID != uuid.Nil {
		return "participant:" + player.ID.String()
	}
	return "client:" + ClientIPFromRequest(r)
}

type arenaMutationEvidence struct {
	Action    string  `json:"action"`
	Confirmed *bool   `json:"confirmed"`
	Reason    *string `json:"reason"`
}

func validateArenaMutationEvidence(w http.ResponseWriter, r *http.Request, class arenaEndpointClass) bool {
	if r.Body == nil || r.Body == http.NoBody {
		return true
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxArenaMutationBodyBytes+1))
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, http.StatusText(http.StatusBadRequest), "invalid arena request")
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	if int64(len(body)) > maxArenaMutationBodyBytes {
		writeProblem(w, r, http.StatusRequestEntityTooLarge, http.StatusText(http.StatusRequestEntityTooLarge), "arena request body is too large")
		return false
	}
	if class != arenaEndpointOperatorMutation || len(bytes.TrimSpace(body)) == 0 {
		return true
	}

	var evidence arenaMutationEvidence
	if err := json.Unmarshal(body, &evidence); err != nil {
		return true
	}
	if evidence.Confirmed != nil && !*evidence.Confirmed {
		writeProblem(w, r, http.StatusBadRequest, http.StatusText(http.StatusBadRequest), "operator confirmation is required")
		return false
	}
	if strings.EqualFold(strings.TrimSpace(evidence.Action), "pause") {
		if evidence.Confirmed == nil || !*evidence.Confirmed || evidence.Reason == nil || strings.TrimSpace(*evidence.Reason) == "" {
			writeProblem(w, r, http.StatusBadRequest, http.StatusText(http.StatusBadRequest), "technical pause evidence is required")
			return false
		}
	}
	return true
}

func ArenaSecurity(authorizer ArenaScopeAuthorizer, limits *ArenaRequestLimits) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if limits != nil {
			next = limits.Middleware(next)
		}
		return ArenaAuthorization(authorizer)(next)
	}
}
