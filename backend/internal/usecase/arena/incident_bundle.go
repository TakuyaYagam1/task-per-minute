package arena

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
)

const maxIncidentBundleEvents = 4096

var ErrInvalidIncidentBundle = errors.New("invalid Arena incident bundle")

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
		!validArenaServerTime(snapshot.GeneratedAt) || len(snapshot.Events) == 0 ||
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

func validIncidentBundleEnvelope(bundle IncidentBundle) bool {
	return bundle.TournamentID != uuid.Nil && bundle.ProjectionRevision >= 1 &&
		validArenaServerTime(bundle.GeneratedAt) && len(bundle.CanonicalContent) > 0 &&
		bundle.SHA256 != ([sha256.Size]byte{}) && sha256.Sum256(bundle.CanonicalContent) == bundle.SHA256
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
		value, keep := incidentPayloadValue(key, raw)
		if keep {
			redacted[key] = value
		}
	}
	canonical, err := json.Marshal(redacted)
	if err != nil {
		return nil, ErrInvalidIncidentBundle
	}
	return canonical, nil
}

func incidentPayloadValue(key string, raw json.RawMessage) (any, bool) {
	switch key {
	case "attempt_id", "entity_id", "previous_revision_id", "projection_revision_id",
		"series_id", "source_projection_revision_id", "tournament_id", "winner_id":
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return nil, false
		}
		parsed, err := uuid.Parse(value)
		return value, err == nil && parsed != uuid.Nil
	case "entity_kind", "reason", "result_reason", "state":
		var value string
		if json.Unmarshal(raw, &value) != nil || value == "" || value != strings.TrimSpace(value) || len(value) > 512 {
			return nil, false
		}
		return value, true
	case "revision_number":
		var number json.Number
		if json.Unmarshal(raw, &number) != nil {
			return nil, false
		}
		value, err := strconv.ParseInt(number.String(), 10, 64)
		return value, err == nil && value > 0
	default:
		return nil, false
	}
}

func incidentUUIDString(value *uuid.UUID) *string {
	if value == nil {
		return nil
	}
	encoded := value.String()
	return &encoded
}
