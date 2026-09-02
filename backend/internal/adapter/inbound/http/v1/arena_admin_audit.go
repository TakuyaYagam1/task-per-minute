package v1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/errmap"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1/response"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const maxArenaIncidentContentBytes = 8 << 20

type ArenaAuditService interface {
	ListAudit(ctx context.Context, operator ArenaOperatorIdentity, params api.ListArenaOperatorAuditParams) (api.ArenaAuditPage, error)
	ExportIncident(ctx context.Context, operator ArenaOperatorIdentity, tournamentID uuid.UUID) (api.ArenaIncidentBundle, error)
}

func (c *arenaAdminController) ListArenaOperatorAudit(
	w http.ResponseWriter,
	r *http.Request,
	params api.ListArenaOperatorAuditParams,
) {
	operator, ok := arenaOperatorFromRequest(w, r)
	if !ok {
		return
	}
	service, ok := c.service.(ArenaAuditService)
	if !ok || service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	if !validArenaAuditParams(params) {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}

	result, err := service.ListAudit(r.Context(), operator, params)
	if err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	redacted, ok := redactArenaAuditPage(result, params.TournamentId, params.PageSize)
	if !ok {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, redacted)
}

func (c *arenaAdminController) ExportArenaOperatorIncident(
	w http.ResponseWriter,
	r *http.Request,
	tournamentID api.ArenaTournamentId,
) {
	operator, ok := arenaOperatorFromRequest(w, r)
	if !ok {
		return
	}
	service, ok := c.service.(ArenaAuditService)
	if !ok || service == nil {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	if tournamentID == uuid.Nil {
		errmap.HandleError(w, r, domain.ErrValidation)
		return
	}

	result, err := service.ExportIncident(r.Context(), operator, tournamentID)
	if err != nil {
		writeArenaAdminError(w, r, err)
		return
	}
	if !validArenaIncidentBundle(result, tournamentID) {
		errmap.HandleError(w, r, domain.ErrInternal)
		return
	}
	response.WriteJSON(w, http.StatusOK, result)
}

//nolint:gocyclo // One flat ingress boundary validates every optional audit filter before repository access.
func validArenaAuditParams(params api.ListArenaOperatorAuditParams) bool {
	if params.TournamentId == uuid.Nil ||
		(params.EntityId != nil && *params.EntityId == uuid.Nil) ||
		(params.ActorId != nil && *params.ActorId == uuid.Nil) {
		return false
	}
	if params.EntityKind != nil {
		switch *params.EntityKind {
		case api.ArenaAuditEntityKindGameAttempt, api.ArenaAuditEntityKindSeries:
		default:
			return false
		}
	}
	if params.ActorKind != nil {
		switch *params.ActorKind {
		case api.Operator, api.Server:
		default:
			return false
		}
	}
	if !validArenaAuditFilterString(params.EventType, 64) || !validArenaAuditFilterString(params.ResultReason, 40) {
		return false
	}
	if params.OccurredFrom != nil && params.OccurredTo != nil && params.OccurredFrom.After(*params.OccurredTo) {
		return false
	}
	if params.PageSize != nil && (*params.PageSize < 1 || *params.PageSize > 200) {
		return false
	}
	if params.Cursor != nil && (params.Cursor.OccurredAt.IsZero() ||
		params.Cursor.AuditEventId == uuid.Nil || params.Cursor.RevisionId == uuid.Nil) {
		return false
	}
	return true
}

func validArenaAuditFilterString(value *string, maximum int) bool {
	if value == nil {
		return true
	}
	trimmed := strings.TrimSpace(*value)
	return trimmed != "" && len(trimmed) <= maximum
}

func redactArenaAuditPage(
	page api.ArenaAuditPage,
	tournamentID uuid.UUID,
	pageSize *int32,
) (api.ArenaAuditPage, bool) {
	limit := 200
	if pageSize != nil {
		limit = int(*pageSize)
	}
	if len(page.Events) > limit {
		return api.ArenaAuditPage{}, false
	}

	events := make([]api.ArenaAuditEvent, len(page.Events))
	for index, event := range page.Events {
		if event.TournamentId == nil || *event.TournamentId != tournamentID ||
			event.AuditEventId == nil || *event.AuditEventId == uuid.Nil ||
			event.OfficialResultRevisionId == nil || *event.OfficialResultRevisionId == uuid.Nil {
			return api.ArenaAuditPage{}, false
		}
		copyEvent := event
		if event.RedactedPayload != nil {
			payload := allowlistedArenaAuditPayload(*event.RedactedPayload)
			copyEvent.RedactedPayload = &payload
		}
		events[index] = copyEvent
	}
	return api.ArenaAuditPage{Events: events, NextCursor: page.NextCursor}, true
}

func allowlistedArenaAuditPayload(payload map[string]any) map[string]any {
	allowed := map[string]struct{}{
		"action": {}, "reason": {}, "result_reason": {}, "state": {},
		"previous_state": {}, "current_state": {}, "confirmed": {},
		"command_id": {}, "entity_id": {}, "revision": {}, "revision_id": {},
		"evidence_digest": {}, "validation_digest": {},
	}
	redacted := make(map[string]any, len(payload))
	for key, value := range payload {
		if _, ok := allowed[key]; ok {
			redacted[key] = value
		}
	}
	return redacted
}

//nolint:gocyclo // The export boundary verifies every envelope, digest, and redaction invariant together.
func validArenaIncidentBundle(bundle api.ArenaIncidentBundle, tournamentID uuid.UUID) bool {
	if bundle.TournamentId == nil || *bundle.TournamentId != tournamentID ||
		bundle.ProjectionRevision == nil || *bundle.ProjectionRevision < 1 ||
		bundle.GeneratedAt == nil || bundle.GeneratedAt.IsZero() ||
		bundle.CanonicalContent == nil || len(*bundle.CanonicalContent) == 0 ||
		len(*bundle.CanonicalContent) > maxArenaIncidentContentBytes ||
		bundle.CanonicalContentLength == nil || *bundle.CanonicalContentLength != int64(len(*bundle.CanonicalContent)) ||
		bundle.CanonicalContentType == nil || *bundle.CanonicalContentType != api.Applicationjson ||
		bundle.CanonicalContentEncoding == nil || *bundle.CanonicalContentEncoding != api.Base64 ||
		bundle.Sha256 == nil || !validSHA256Hex(*bundle.Sha256) {
		return false
	}

	sum := sha256.Sum256(*bundle.CanonicalContent)
	if hex.EncodeToString(sum[:]) != *bundle.Sha256 {
		return false
	}
	var content any
	if json.Unmarshal(*bundle.CanonicalContent, &content) != nil || containsSensitiveArenaField(content) {
		return false
	}
	return true
}

func containsSensitiveArenaField(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if sensitiveArenaFieldName(key) || containsSensitiveArenaField(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsSensitiveArenaField(child) {
				return true
			}
		}
	}
	return false
}

func sensitiveArenaFieldName(value string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(value, "-", "_"))
	for _, token := range []string{
		"authorization", "cookie", "credential", "credentials", "password", "secret", "token",
		"flag", "flags", "submitted_flag", "task_secret", "task_url", "source_url", "presigned_url",
	} {
		if normalized == token || strings.HasSuffix(normalized, "_"+token) {
			return true
		}
	}
	return false
}
