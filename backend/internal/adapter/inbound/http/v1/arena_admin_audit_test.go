package v1

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
)

func TestArenaAuditAndIncidentBundleHandlers(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.MustParse("26b50137-f5af-49c0-8a36-9fc0dd98c689")
	entityID := uuid.MustParse("93dd5195-ded5-40b9-8fb2-a6f1265d0584")
	actorID := uuid.MustParse("c8c2394a-1341-4622-aaeb-cee54dfe5e52")
	auditID := uuid.MustParse("4072e692-eb85-4a26-a050-f252211fab87")
	revisionID := uuid.MustParse("f7fe09f3-c87d-40d1-bdba-8cb051d1db81")
	occurredAt := time.Date(2026, 9, 2, 14, 0, 0, 0, time.UTC)
	createdAt := occurredAt.Add(time.Second)
	from := occurredAt.Add(-time.Hour)
	to := occurredAt.Add(time.Hour)
	entityKind := api.ArenaAuditEntityKindGameAttempt
	eventType := "game.corrected"
	actorKind := api.Operator
	resultReason := "no_show"
	pageSize := int32(25)
	cursor := api.ArenaAuditCursor{OccurredAt: occurredAt, AuditEventId: auditID, RevisionId: revisionID}

	t.Run("passes every audit filter and redacts payload fields", func(t *testing.T) {
		current := true
		superseded := false
		revisionNo := int64(4)
		service := &arenaAuditServiceStub{
			arenaAdminServiceStub: &arenaAdminServiceStub{},
			auditResult: api.ArenaAuditPage{
				Events: []api.ArenaAuditEvent{{
					AuditEventId: &auditID, TournamentId: &tournamentID, ActorKind: api.Operator, ActorId: &actorID,
					EntityKind: entityKind, EntityId: &entityID, EventType: &eventType,
					OccurredAt: &occurredAt, CreatedAt: &createdAt, OfficialResultRevisionId: &revisionID,
					RevisionNumber: &revisionNo, IsCurrent: &current, IsSuperseded: &superseded,
					RedactedPayload: &map[string]any{
						"action": "correct", "result_reason": "no_show", "submitted_flag": "never-return-this",
					},
				}},
				NextCursor: &cursor,
			},
		}
		controller := newArenaAdminController(service)
		params := api.ListArenaOperatorAuditParams{
			TournamentId: tournamentID, EntityKind: &entityKind, EntityId: &entityID,
			EventType: &eventType, ActorKind: &actorKind, ActorId: &actorID, ResultReason: &resultReason,
			OccurredFrom: &from, OccurredTo: &to, Cursor: &cursor, PageSize: &pageSize,
		}

		recorder := authenticatedArenaRequest(t, http.MethodGet, "/api/v1/arena/operator/audit", "", func(w http.ResponseWriter, r *http.Request) {
			controller.ListArenaOperatorAudit(w, r, params)
		})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, service.auditCalls)
		require.Equal(t, "admin", service.auditOperator.Subject)
		require.Equal(t, params, service.auditParams)
		require.NotContains(t, recorder.Body.String(), "never-return-this")
		require.NotContains(t, recorder.Body.String(), "submitted_flag")

		var page api.ArenaAuditPage
		require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &page))
		require.Len(t, page.Events, 1)
		require.Equal(t, "correct", (*page.Events[0].RedactedPayload)["action"])
		require.Equal(t, cursor, *page.NextCursor)
	})

	t.Run("rejects an invalid audit range before lookup", func(t *testing.T) {
		service := &arenaAuditServiceStub{arenaAdminServiceStub: &arenaAdminServiceStub{}}
		controller := newArenaAdminController(service)
		params := api.ListArenaOperatorAuditParams{TournamentId: tournamentID, OccurredFrom: &to, OccurredTo: &from}

		recorder := authenticatedArenaRequest(t, http.MethodGet, "/audit", "", func(w http.ResponseWriter, r *http.Request) {
			controller.ListArenaOperatorAudit(w, r, params)
		})

		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Zero(t, service.auditCalls)
	})

	t.Run("returns an incident bundle with consistent canonical metadata", func(t *testing.T) {
		content := []byte(`{"events":[],"projection_revision":23}`)
		sum := sha256.Sum256(content)
		digest := hex.EncodeToString(sum[:])
		length := int64(len(content))
		revision := int64(23)
		contentType := api.Applicationjson
		encoding := api.Base64
		service := &arenaAuditServiceStub{
			arenaAdminServiceStub: &arenaAdminServiceStub{},
			incidentResult: api.ArenaIncidentBundle{
				TournamentId: &tournamentID, ProjectionRevision: &revision, GeneratedAt: &createdAt,
				CanonicalContent: &content, CanonicalContentType: &contentType, CanonicalContentEncoding: &encoding,
				CanonicalContentLength: &length, Sha256: &digest,
			},
		}
		controller := newArenaAdminController(service)

		recorder := authenticatedArenaRequest(t, http.MethodGet, "/incident-export", "", func(w http.ResponseWriter, r *http.Request) {
			controller.ExportArenaOperatorIncident(w, r, tournamentID)
		})

		require.Equal(t, http.StatusOK, recorder.Code)
		require.Equal(t, 1, service.incidentCalls)
		require.Equal(t, tournamentID, service.incidentTournamentID)
		require.Contains(t, recorder.Body.String(), digest)
		require.NotContains(t, recorder.Body.String(), "authorization")
	})

	t.Run("fails closed when incident hash metadata is inconsistent", func(t *testing.T) {
		content := []byte(`{"events":[]}`)
		badDigest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		length := int64(len(content))
		revision := int64(23)
		contentType := api.Applicationjson
		encoding := api.Base64
		service := &arenaAuditServiceStub{
			arenaAdminServiceStub: &arenaAdminServiceStub{},
			incidentResult: api.ArenaIncidentBundle{
				TournamentId: &tournamentID, ProjectionRevision: &revision, GeneratedAt: &createdAt,
				CanonicalContent: &content, CanonicalContentType: &contentType, CanonicalContentEncoding: &encoding,
				CanonicalContentLength: &length, Sha256: &badDigest,
			},
		}
		controller := newArenaAdminController(service)

		recorder := authenticatedArenaRequest(t, http.MethodGet, "/incident-export", "", func(w http.ResponseWriter, r *http.Request) {
			controller.ExportArenaOperatorIncident(w, r, tournamentID)
		})

		require.Equal(t, http.StatusInternalServerError, recorder.Code)
		require.NotContains(t, recorder.Body.String(), badDigest)
		require.NotContains(t, recorder.Body.String(), "sha256")
	})

	t.Run("fails closed when incident content names sensitive fields", func(t *testing.T) {
		content := []byte(`{"events":[{"flag":"private-value"}],"projection_revision":23}`)
		sum := sha256.Sum256(content)
		digest := hex.EncodeToString(sum[:])
		length := int64(len(content))
		revision := int64(23)
		contentType := api.Applicationjson
		encoding := api.Base64
		service := &arenaAuditServiceStub{
			arenaAdminServiceStub: &arenaAdminServiceStub{},
			incidentResult: api.ArenaIncidentBundle{
				TournamentId: &tournamentID, ProjectionRevision: &revision, GeneratedAt: &createdAt,
				CanonicalContent: &content, CanonicalContentType: &contentType, CanonicalContentEncoding: &encoding,
				CanonicalContentLength: &length, Sha256: &digest,
			},
		}
		controller := newArenaAdminController(service)

		recorder := authenticatedArenaRequest(t, http.MethodGet, "/incident-export", "", func(w http.ResponseWriter, r *http.Request) {
			controller.ExportArenaOperatorIncident(w, r, tournamentID)
		})

		require.Equal(t, http.StatusInternalServerError, recorder.Code)
		require.NotContains(t, recorder.Body.String(), "private-value")
		require.NotContains(t, recorder.Body.String(), digest)
	})

	t.Run("requires operator authentication before audit access", func(t *testing.T) {
		service := &arenaAuditServiceStub{arenaAdminServiceStub: &arenaAdminServiceStub{}}
		controller := newArenaAdminController(service)
		recorder := httptest.NewRecorder()

		controller.ListArenaOperatorAudit(
			recorder,
			httptest.NewRequest(http.MethodGet, "/audit", nil),
			api.ListArenaOperatorAuditParams{TournamentId: tournamentID},
		)

		require.Equal(t, http.StatusUnauthorized, recorder.Code)
		require.Zero(t, service.auditCalls)
	})
}

type arenaAuditServiceStub struct {
	*arenaAdminServiceStub

	auditCalls    int
	auditOperator ArenaOperatorIdentity
	auditParams   api.ListArenaOperatorAuditParams
	auditResult   api.ArenaAuditPage
	auditErr      error

	incidentCalls        int
	incidentOperator     ArenaOperatorIdentity
	incidentTournamentID uuid.UUID
	incidentResult       api.ArenaIncidentBundle
	incidentErr          error
}

func (s *arenaAuditServiceStub) ListAudit(
	_ context.Context,
	operator ArenaOperatorIdentity,
	params api.ListArenaOperatorAuditParams,
) (api.ArenaAuditPage, error) {
	s.auditCalls++
	s.auditOperator = operator
	s.auditParams = params
	return s.auditResult, s.auditErr
}

func (s *arenaAuditServiceStub) ExportIncident(
	_ context.Context,
	operator ArenaOperatorIdentity,
	tournamentID uuid.UUID,
) (api.ArenaIncidentBundle, error) {
	s.incidentCalls++
	s.incidentOperator = operator
	s.incidentTournamentID = tournamentID
	return s.incidentResult, s.incidentErr
}
