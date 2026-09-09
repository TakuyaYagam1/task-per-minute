package observability

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	logkit "github.com/wahrwelt-kit/go-logkit"
)

const (
	TournamentOutcomeSuccess  = "success"
	TournamentOutcomeRetry    = "retry"
	TournamentOutcomeRejected = "rejected"
	TournamentOutcomeFailure  = "failure"

	tournamentLogMessage     = "tournament event"
	tournamentStringMaxBytes = 256
	tournamentReasonMaxBytes = 64
)

// TournamentEventInput contains the allowlisted fields accepted by tournament logging.
type TournamentEventInput struct {
	Event         string
	Outcome       string
	CorrelationID string
	CommandID     string
	TournamentID  string
	EntityKind    string
	EntityID      string
	Stage         string
	Transition    string
	Duration      time.Duration
	ReasonCode    string
	Revision      int64
}

// TournamentEvent is a validated structured tournament event.
type TournamentEvent struct {
	Event         string
	Outcome       string
	CorrelationID string
	CommandID     string
	TournamentID  string
	EntityKind    string
	EntityID      string
	Stage         string
	Transition    string
	Duration      time.Duration
	ReasonCode    string
	Revision      int64
}

// TournamentEventObserver consumes validated tournament events.
type TournamentEventObserver interface {
	ObserveTournamentEvent(ctx context.Context, event TournamentEvent)
}

// TournamentLagObserver consumes one bounded server-side lag measurement.
type TournamentLagObserver interface {
	ObserveTournamentLag(kind string, lag time.Duration)
}

// NewTournamentEvent validates an input and returns its canonical event.
func NewTournamentEvent(input TournamentEventInput) (TournamentEvent, error) {
	fields := []struct {
		name     string
		value    string
		required bool
		maxBytes int
	}{
		{name: "event", value: input.Event, required: true, maxBytes: tournamentStringMaxBytes},
		{name: "outcome", value: input.Outcome, required: true, maxBytes: tournamentStringMaxBytes},
		{name: "correlation_id", value: input.CorrelationID, required: true, maxBytes: tournamentStringMaxBytes},
		{name: "command_id", value: input.CommandID, maxBytes: tournamentStringMaxBytes},
		{name: "tournament_id", value: input.TournamentID, required: true, maxBytes: tournamentStringMaxBytes},
		{name: "entity_kind", value: input.EntityKind, required: true, maxBytes: tournamentStringMaxBytes},
		{name: "entity_id", value: input.EntityID, required: true, maxBytes: tournamentStringMaxBytes},
		{name: "stage", value: input.Stage, required: true, maxBytes: tournamentStringMaxBytes},
		{name: "transition", value: input.Transition, required: true, maxBytes: tournamentStringMaxBytes},
		{name: "reason_code", value: input.ReasonCode, required: true, maxBytes: tournamentReasonMaxBytes},
	}
	for _, field := range fields {
		if !validTournamentLogString(field.value, field.required, field.maxBytes) {
			return TournamentEvent{}, fmt.Errorf("invalid tournament event %s", field.name)
		}
	}
	if !validTournamentOutcome(input.Outcome) {
		return TournamentEvent{}, fmt.Errorf("invalid tournament event outcome")
	}
	if !validTournamentReasonCode(input.ReasonCode) {
		return TournamentEvent{}, fmt.Errorf("invalid tournament event reason_code")
	}
	if tournamentCommandOutcome(input.Event) && input.CommandID == "" {
		return TournamentEvent{}, fmt.Errorf("invalid tournament event command_id")
	}
	if input.Duration < 0 {
		return TournamentEvent{}, fmt.Errorf("invalid tournament event duration")
	}
	if input.Revision < 0 {
		return TournamentEvent{}, fmt.Errorf("invalid tournament event revision")
	}

	return TournamentEvent(input), nil
}

// EmitTournamentEvent validates input before sending it to an optional observer.
func EmitTournamentEvent(ctx context.Context, observer TournamentEventObserver, input TournamentEventInput) error {
	event, err := NewTournamentEvent(input)
	if err != nil {
		return err
	}
	if !nilTournamentEventObserver(observer) {
		observer.ObserveTournamentEvent(ctx, event)
	}
	return nil
}

// FirstTournamentEventObserver returns the first non-nil observer.
func FirstTournamentEventObserver(observers ...TournamentEventObserver) TournamentEventObserver {
	for _, observer := range observers {
		if !nilTournamentEventObserver(observer) {
			return observer
		}
	}
	return nil
}

// NewTournamentEventFanout sends each validated event to every non-nil observer.
func NewTournamentEventFanout(observers ...TournamentEventObserver) TournamentEventObserver {
	filtered := make([]TournamentEventObserver, 0, len(observers))
	for _, observer := range observers {
		if !nilTournamentEventObserver(observer) {
			filtered = append(filtered, observer)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return tournamentEventFanout(filtered)
}

// ObserveTournamentLag forwards lag only to observers that explicitly support it.
func ObserveTournamentLag(observer TournamentEventObserver, kind string, lag time.Duration) {
	if nilTournamentEventObserver(observer) {
		return
	}
	if lagObserver, ok := observer.(TournamentLagObserver); ok {
		lagObserver.ObserveTournamentLag(kind, lag)
	}
}

type tournamentEventFanout []TournamentEventObserver

func (fanout tournamentEventFanout) ObserveTournamentEvent(ctx context.Context, event TournamentEvent) {
	for _, observer := range fanout {
		observeTournamentEventSafely(observer, ctx, event)
	}
}

func (fanout tournamentEventFanout) ObserveTournamentLag(kind string, lag time.Duration) {
	for _, observer := range fanout {
		if lagObserver, ok := observer.(TournamentLagObserver); ok {
			observeTournamentLagSafely(lagObserver, kind, lag)
		}
	}
}

// NewTournamentStructuredLogger adapts a logkit logger to tournament events.
func NewTournamentStructuredLogger(log logkit.Logger) TournamentEventObserver {
	if log == nil {
		return nil
	}
	return tournamentStructuredLogger{log: log}
}

type tournamentStructuredLogger struct {
	log logkit.Logger
}

func (l tournamentStructuredLogger) ObserveTournamentEvent(ctx context.Context, event TournamentEvent) {
	validated, err := NewTournamentEvent(TournamentEventInput(event))
	if err != nil || l.log == nil {
		return
	}

	fields := logkit.Fields{
		"event":          validated.Event,
		"outcome":        validated.Outcome,
		"correlation_id": validated.CorrelationID,
		"tournament_id":  validated.TournamentID,
		"entity_kind":    validated.EntityKind,
		"entity_id":      validated.EntityID,
		"stage":          validated.Stage,
		"transition":     validated.Transition,
		"duration_ms":    validated.Duration.Milliseconds(),
		"reason_code":    validated.ReasonCode,
		"revision":       validated.Revision,
	}
	if validated.CommandID != "" {
		fields["command_id"] = validated.CommandID
	}

	switch validated.Outcome {
	case TournamentOutcomeSuccess:
		l.log.InfoContext(ctx, tournamentLogMessage, fields)
	case TournamentOutcomeRetry, TournamentOutcomeRejected:
		l.log.WarnContext(ctx, tournamentLogMessage, fields)
	case TournamentOutcomeFailure:
		l.log.ErrorContext(ctx, tournamentLogMessage, fields)
	}
}

func validTournamentLogString(value string, required bool, maxBytes int) bool {
	if value == "" {
		return !required
	}
	if len(value) > maxBytes || value != strings.TrimSpace(value) || !utf8.ValidString(value) ||
		strings.IndexFunc(value, unicode.IsControl) != -1 {
		return false
	}
	return !containsTournamentSensitiveConcept(value)
}

func validTournamentOutcome(outcome string) bool {
	switch outcome {
	case TournamentOutcomeSuccess, TournamentOutcomeRetry, TournamentOutcomeRejected, TournamentOutcomeFailure:
		return true
	default:
		return false
	}
}

func validTournamentReasonCode(reason string) bool {
	for _, character := range reason {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '.' || character == '_' ||
			character == ':' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func tournamentCommandOutcome(event string) bool {
	return tournamentConceptWords(event)["command"]
}

func containsTournamentSensitiveConcept(value string) bool {
	lower := strings.ToLower(value)
	for _, concept := range []string{
		"authorization", "cookie", "credential", "credentials", "flag", "flags", "password", "token", "tokens",
	} {
		if strings.Contains(lower, concept) {
			return true
		}
	}
	compact := strings.Map(func(character rune) rune {
		if character >= 'a' && character <= 'z' {
			return character
		}
		return -1
	}, lower)
	return strings.Contains(compact, "taskpayload") || strings.Contains(compact, "taskcontent")
}

func tournamentConceptWords(value string) map[string]bool {
	words := strings.FieldsFunc(strings.ToLower(value), func(character rune) bool {
		return character < 'a' || character > 'z'
	})
	result := make(map[string]bool, len(words))
	for _, word := range words {
		result[word] = true
	}
	return result
}

func nilTournamentEventObserver(observer TournamentEventObserver) bool {
	if observer == nil {
		return true
	}
	value := reflect.ValueOf(observer)
	kind := value.Kind()
	return (kind == reflect.Chan || kind == reflect.Func || kind == reflect.Interface ||
		kind == reflect.Map || kind == reflect.Pointer || kind == reflect.Slice) && value.IsNil()
}
