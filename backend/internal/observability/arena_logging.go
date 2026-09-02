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
	ArenaOutcomeSuccess  = "success"
	ArenaOutcomeRetry    = "retry"
	ArenaOutcomeRejected = "rejected"
	ArenaOutcomeFailure  = "failure"

	arenaLogMessage     = "arena event"
	arenaStringMaxBytes = 256
	arenaReasonMaxBytes = 64
)

// ArenaEventInput contains the allowlisted fields accepted by arena logging.
type ArenaEventInput struct {
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

// ArenaEvent is a validated structured arena event.
type ArenaEvent struct {
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

// ArenaEventObserver consumes validated arena events.
type ArenaEventObserver interface {
	ObserveArenaEvent(ctx context.Context, event ArenaEvent)
}

// ArenaLagObserver consumes one bounded server-side lag measurement.
type ArenaLagObserver interface {
	ObserveArenaLag(kind string, lag time.Duration)
}

// NewArenaEvent validates an input and returns its canonical event.
func NewArenaEvent(input ArenaEventInput) (ArenaEvent, error) {
	fields := []struct {
		name     string
		value    string
		required bool
		maxBytes int
	}{
		{name: "event", value: input.Event, required: true, maxBytes: arenaStringMaxBytes},
		{name: "outcome", value: input.Outcome, required: true, maxBytes: arenaStringMaxBytes},
		{name: "correlation_id", value: input.CorrelationID, required: true, maxBytes: arenaStringMaxBytes},
		{name: "command_id", value: input.CommandID, maxBytes: arenaStringMaxBytes},
		{name: "tournament_id", value: input.TournamentID, required: true, maxBytes: arenaStringMaxBytes},
		{name: "entity_kind", value: input.EntityKind, required: true, maxBytes: arenaStringMaxBytes},
		{name: "entity_id", value: input.EntityID, required: true, maxBytes: arenaStringMaxBytes},
		{name: "stage", value: input.Stage, required: true, maxBytes: arenaStringMaxBytes},
		{name: "transition", value: input.Transition, required: true, maxBytes: arenaStringMaxBytes},
		{name: "reason_code", value: input.ReasonCode, required: true, maxBytes: arenaReasonMaxBytes},
	}
	for _, field := range fields {
		if !validArenaLogString(field.value, field.required, field.maxBytes) {
			return ArenaEvent{}, fmt.Errorf("invalid arena event %s", field.name)
		}
	}
	if !validArenaOutcome(input.Outcome) {
		return ArenaEvent{}, fmt.Errorf("invalid arena event outcome")
	}
	if !validArenaReasonCode(input.ReasonCode) {
		return ArenaEvent{}, fmt.Errorf("invalid arena event reason_code")
	}
	if arenaCommandOutcome(input.Event) && input.CommandID == "" {
		return ArenaEvent{}, fmt.Errorf("invalid arena event command_id")
	}
	if input.Duration < 0 {
		return ArenaEvent{}, fmt.Errorf("invalid arena event duration")
	}
	if input.Revision < 0 {
		return ArenaEvent{}, fmt.Errorf("invalid arena event revision")
	}

	return ArenaEvent(input), nil
}

// EmitArenaEvent validates input before sending it to an optional observer.
func EmitArenaEvent(ctx context.Context, observer ArenaEventObserver, input ArenaEventInput) error {
	event, err := NewArenaEvent(input)
	if err != nil {
		return err
	}
	if !nilArenaEventObserver(observer) {
		observer.ObserveArenaEvent(ctx, event)
	}
	return nil
}

// FirstArenaEventObserver returns the first non-nil observer.
func FirstArenaEventObserver(observers ...ArenaEventObserver) ArenaEventObserver {
	for _, observer := range observers {
		if !nilArenaEventObserver(observer) {
			return observer
		}
	}
	return nil
}

// NewArenaEventFanout sends each validated event to every non-nil observer.
func NewArenaEventFanout(observers ...ArenaEventObserver) ArenaEventObserver {
	filtered := make([]ArenaEventObserver, 0, len(observers))
	for _, observer := range observers {
		if !nilArenaEventObserver(observer) {
			filtered = append(filtered, observer)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return arenaEventFanout(filtered)
}

// ObserveArenaLag forwards lag only to observers that explicitly support it.
func ObserveArenaLag(observer ArenaEventObserver, kind string, lag time.Duration) {
	if nilArenaEventObserver(observer) {
		return
	}
	if lagObserver, ok := observer.(ArenaLagObserver); ok {
		lagObserver.ObserveArenaLag(kind, lag)
	}
}

type arenaEventFanout []ArenaEventObserver

func (fanout arenaEventFanout) ObserveArenaEvent(ctx context.Context, event ArenaEvent) {
	for _, observer := range fanout {
		observer.ObserveArenaEvent(ctx, event)
	}
}

func (fanout arenaEventFanout) ObserveArenaLag(kind string, lag time.Duration) {
	for _, observer := range fanout {
		if lagObserver, ok := observer.(ArenaLagObserver); ok {
			lagObserver.ObserveArenaLag(kind, lag)
		}
	}
}

// NewArenaStructuredLogger adapts a logkit logger to arena events.
func NewArenaStructuredLogger(log logkit.Logger) ArenaEventObserver {
	if log == nil {
		return nil
	}
	return arenaStructuredLogger{log: log}
}

type arenaStructuredLogger struct {
	log logkit.Logger
}

func (l arenaStructuredLogger) ObserveArenaEvent(ctx context.Context, event ArenaEvent) {
	validated, err := NewArenaEvent(ArenaEventInput(event))
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
	case ArenaOutcomeSuccess:
		l.log.InfoContext(ctx, arenaLogMessage, fields)
	case ArenaOutcomeRetry, ArenaOutcomeRejected:
		l.log.WarnContext(ctx, arenaLogMessage, fields)
	case ArenaOutcomeFailure:
		l.log.ErrorContext(ctx, arenaLogMessage, fields)
	}
}

func validArenaLogString(value string, required bool, maxBytes int) bool {
	if value == "" {
		return !required
	}
	if len(value) > maxBytes || value != strings.TrimSpace(value) || !utf8.ValidString(value) ||
		strings.IndexFunc(value, unicode.IsControl) != -1 {
		return false
	}
	return !containsArenaSensitiveConcept(value)
}

func validArenaOutcome(outcome string) bool {
	switch outcome {
	case ArenaOutcomeSuccess, ArenaOutcomeRetry, ArenaOutcomeRejected, ArenaOutcomeFailure:
		return true
	default:
		return false
	}
}

func validArenaReasonCode(reason string) bool {
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

func arenaCommandOutcome(event string) bool {
	return arenaConceptWords(event)["command"]
}

func containsArenaSensitiveConcept(value string) bool {
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

func arenaConceptWords(value string) map[string]bool {
	words := strings.FieldsFunc(strings.ToLower(value), func(character rune) bool {
		return character < 'a' || character > 'z'
	})
	result := make(map[string]bool, len(words))
	for _, word := range words {
		result[word] = true
	}
	return result
}

func nilArenaEventObserver(observer ArenaEventObserver) bool {
	if observer == nil {
		return true
	}
	value := reflect.ValueOf(observer)
	kind := value.Kind()
	return (kind == reflect.Chan || kind == reflect.Func || kind == reflect.Interface ||
		kind == reflect.Map || kind == reflect.Pointer || kind == reflect.Slice) && value.IsNil()
}
