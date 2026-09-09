package audit

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const (
	maxIncidentBundleEvents                 = 4096
	IncidentBundleAlgorithmHMACSHA256V1     = "hmac-sha256-v1"
	maxIncidentBundleAuthenticationKeyIDLen = 64
)

var ErrInvalidIncidentBundle = errors.New("invalid result incident bundle")

type IncidentBundleSnapshot struct {
	TournamentID       uuid.UUID
	ProjectionRevision int64
	GeneratedAt        time.Time
	Events             []AuditEvent
}

type IncidentBundle struct {
	TournamentID       uuid.UUID
	ProjectionRevision int64
	GeneratedAt        time.Time
	CanonicalContent   []byte
	SHA256             [sha256.Size]byte
	Algorithm          string
	KeyID              string
	MAC                [sha256.Size]byte
}

type incidentBundleDocument struct {
	TournamentID       string                  `json:"tournament_id"`
	ProjectionRevision int64                   `json:"projection_revision"`
	GeneratedAt        string                  `json:"generated_at"`
	Events             []incidentAuditDocument `json:"audit_events"`
}

type incidentAuditDocument struct {
	AuditEventID             string          `json:"audit_event_id"`
	RosterID                 string          `json:"roster_id"`
	SeriesID                 string          `json:"series_id"`
	ResultEventID            string          `json:"result_event_id"`
	ActorKind                string          `json:"actor_kind"`
	ActorID                  *string         `json:"actor_id,omitempty"`
	EventType                string          `json:"event_type"`
	OccurredAt               string          `json:"occurred_at"`
	CreatedAt                string          `json:"created_at"`
	ResultState              string          `json:"result_state"`
	ResultReason             string          `json:"result_reason"`
	WinnerID                 *string         `json:"winner_id,omitempty"`
	OfficialResultRevisionID string          `json:"official_result_revision_id"`
	EntityKind               string          `json:"entity_kind"`
	EntityID                 string          `json:"entity_id"`
	RevisionNumber           int64           `json:"revision_number"`
	IsCurrent                bool            `json:"is_current"`
	IsSuperseded             bool            `json:"is_superseded"`
	RedactedPayload          json.RawMessage `json:"redacted_payload"`
}

func GenerateIncidentBundle(snapshot IncidentBundleSnapshot) (IncidentBundle, error) {
	if snapshot.TournamentID == uuid.Nil || snapshot.ProjectionRevision < 1 ||
		!domain.IsValidServerTime(snapshot.GeneratedAt) || len(snapshot.Events) == 0 ||
		len(snapshot.Events) > maxIncidentBundleEvents {
		return IncidentBundle{}, ErrInvalidIncidentBundle
	}
	ordered, err := validatedAuditEvents(snapshot.Events)
	if err != nil {
		return IncidentBundle{}, fmt.Errorf("%w: %w", ErrInvalidIncidentBundle, err)
	}
	document := incidentBundleDocument{
		TournamentID: snapshot.TournamentID.String(), ProjectionRevision: snapshot.ProjectionRevision,
		GeneratedAt: snapshot.GeneratedAt.Format(time.RFC3339Nano),
		Events:      make([]incidentAuditDocument, len(ordered)),
	}
	for index, event := range ordered {
		if event.TournamentID != snapshot.TournamentID || event.OccurredAt.After(snapshot.GeneratedAt) {
			return IncidentBundle{}, ErrInvalidIncidentBundle
		}
		redacted, redactErr := canonicalIncidentPayload(event.RedactedPayload)
		if redactErr != nil {
			return IncidentBundle{}, redactErr
		}
		document.Events[index] = incidentAuditEvent(event, redacted)
	}
	content, err := json.Marshal(document)
	if err != nil {
		return IncidentBundle{}, fmt.Errorf("%w: encode canonical content", ErrInvalidIncidentBundle)
	}
	return IncidentBundle{
		TournamentID: snapshot.TournamentID, ProjectionRevision: snapshot.ProjectionRevision,
		GeneratedAt: snapshot.GeneratedAt, CanonicalContent: content, SHA256: sha256.Sum256(content),
	}, nil
}

func VerifyIncidentBundle(bundle IncidentBundle) error {
	if !validIncidentBundleEnvelope(bundle) {
		return ErrInvalidIncidentBundle
	}
	document, err := decodeIncidentBundleDocument(bundle)
	if err != nil {
		return err
	}
	if err := validateIncidentBundlePayloads(document.Events); err != nil {
		return err
	}
	canonical, err := json.Marshal(document)
	if err != nil || !bytes.Equal(canonical, bundle.CanonicalContent) {
		return ErrInvalidIncidentBundle
	}
	return nil
}

func VerifyIncidentBundleAuthenticityEnvelope(bundle IncidentBundle) error {
	if bundle.Algorithm != IncidentBundleAlgorithmHMACSHA256V1 ||
		!validIncidentBundleKeyID(bundle.KeyID) || bundle.MAC == ([sha256.Size]byte{}) {
		return ErrInvalidIncidentBundle
	}
	return nil
}

func validIncidentBundleEnvelope(bundle IncidentBundle) bool {
	return bundle.TournamentID != uuid.Nil && bundle.ProjectionRevision >= 1 &&
		domain.IsValidServerTime(bundle.GeneratedAt) && len(bundle.CanonicalContent) > 0 &&
		bundle.SHA256 != ([sha256.Size]byte{}) && sha256.Sum256(bundle.CanonicalContent) == bundle.SHA256
}

//nolint:gocyclo // One cohesive audit boundary keeps cross-field invariants and fail-closed branches explicit.
func validIncidentBundleKeyID(value string) bool {
	if len(value) == 0 || len(value) > maxIncidentBundleAuthenticationKeyIDLen {
		return false
	}
	for index := range len(value) {
		current := value[index]
		letter := (current >= 'a' && current <= 'z') || (current >= 'A' && current <= 'Z')
		digit := current >= '0' && current <= '9'
		if (index == 0 && !letter && !digit) ||
			(index > 0 && !letter && !digit && current != '.' && current != '_' && current != '-') {
			return false
		}
	}
	return true
}

func decodeIncidentBundleDocument(bundle IncidentBundle) (incidentBundleDocument, error) {
	var document incidentBundleDocument
	if err := json.Unmarshal(bundle.CanonicalContent, &document); err != nil || len(document.Events) == 0 ||
		document.TournamentID != bundle.TournamentID.String() ||
		document.ProjectionRevision != bundle.ProjectionRevision ||
		document.GeneratedAt != bundle.GeneratedAt.Format(time.RFC3339Nano) {
		return incidentBundleDocument{}, ErrInvalidIncidentBundle
	}
	return document, nil
}

func validateIncidentBundlePayloads(events []incidentAuditDocument) error {
	for index := range events {
		redacted, err := canonicalIncidentPayload(events[index].RedactedPayload)
		if err != nil || !bytes.Equal(redacted, events[index].RedactedPayload) {
			return ErrInvalidIncidentBundle
		}
	}
	return nil
}

func incidentAuditEvent(event AuditEvent, payload json.RawMessage) incidentAuditDocument {
	return incidentAuditDocument{
		AuditEventID: event.AuditEventID.String(), RosterID: event.RosterID.String(),
		SeriesID: event.SeriesID.String(), ResultEventID: event.ResultEventID.String(),
		ActorKind: string(event.ActorKind), ActorID: incidentUUIDString(event.ActorID),
		EventType: event.EventType, OccurredAt: event.OccurredAt.Format(time.RFC3339Nano),
		CreatedAt: event.CreatedAt.Format(time.RFC3339Nano), ResultState: event.ResultState,
		ResultReason: event.ResultReason, WinnerID: incidentUUIDString(event.WinnerID),
		OfficialResultRevisionID: event.OfficialResultRevisionID.String(), EntityKind: string(event.EntityKind),
		EntityID: event.EntityID.String(), RevisionNumber: event.RevisionNumber,
		IsCurrent: event.IsCurrent, IsSuperseded: event.IsSuperseded, RedactedPayload: payload,
	}
}

func canonicalIncidentPayload(payload json.RawMessage) (json.RawMessage, error) {
	var source map[string]json.RawMessage
	if err := json.Unmarshal(payload, &source); err != nil || len(source) == 0 {
		return nil, ErrInvalidIncidentBundle
	}
	redacted := make(map[string]any)
	for key, raw := range source {
		value, keep, valueErr := incidentPayloadValue(key, raw)
		if valueErr != nil {
			return nil, ErrInvalidIncidentBundle
		}
		if keep {
			redacted[key] = value
		}
	}
	if len(redacted) == 0 {
		return nil, ErrInvalidIncidentBundle
	}
	canonical, err := json.Marshal(redacted)
	if err != nil {
		return nil, ErrInvalidIncidentBundle
	}
	return canonical, nil
}

func incidentPayloadValue(key string, raw json.RawMessage) (any, bool, error) {
	switch key {
	case "attempt_id", "entity_id", "previous_revision_id", "projection_revision_id",
		"series_id", "source_projection_revision_id", "tournament_id", "winner_id":
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return nil, true, ErrInvalidIncidentBundle
		}
		parsed, err := uuid.Parse(value)
		if err != nil || parsed == uuid.Nil {
			return nil, true, ErrInvalidIncidentBundle
		}
		return value, true, nil
	case "entity_kind", "reason", "result_reason", "state":
		var value string
		if json.Unmarshal(raw, &value) != nil || value == "" || value != strings.TrimSpace(value) || len(value) > 512 {
			return nil, true, ErrInvalidIncidentBundle
		}
		return value, true, nil
	case "revision_number":
		var number json.Number
		if json.Unmarshal(raw, &number) != nil {
			return nil, true, ErrInvalidIncidentBundle
		}
		value, err := strconv.ParseInt(number.String(), 10, 64)
		if err != nil || value <= 0 {
			return nil, true, ErrInvalidIncidentBundle
		}
		return value, true, nil
	default:
		return nil, false, nil
	}
}

func incidentUUIDString(value *uuid.UUID) *string {
	if value == nil {
		return nil
	}
	encoded := value.String()
	return &encoded
}
