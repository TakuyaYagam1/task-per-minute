package catalog

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestDeterministicIDGenerator(t *testing.T) {
	namespace := uuid.MustParse("3a22cb38-c032-4c29-82ad-013d5477d756")
	commandID := uuid.MustParse("fa684298-b53b-483a-97d4-bdb2d4df8dc5")
	generator, err := NewDeterministicIDGenerator(namespace)
	if err != nil {
		t.Fatalf("NewDeterministicIDGenerator() error = %v", err)
	}

	input := IDInput{Scope: IDScopeTournament, IdempotencyKey: commandID}
	first, err := generator.Derive(input)
	if err != nil {
		t.Fatalf("Derive() first error = %v", err)
	}
	second, err := generator.Derive(input)
	if err != nil {
		t.Fatalf("Derive() second error = %v", err)
	}
	if first == uuid.Nil || first != second {
		t.Fatalf("Derive() = %s, %s; want one stable non-nil ID", first, second)
	}

	rosterID, err := generator.Derive(IDInput{Scope: IDScopeRoster, IdempotencyKey: commandID})
	if err != nil {
		t.Fatalf("Derive(roster) error = %v", err)
	}
	if rosterID == first {
		t.Fatal("different scopes produced the same ID")
	}
}

func TestDeterministicIDGeneratorRejectsInvalidInput(t *testing.T) {
	if _, err := NewDeterministicIDGenerator(uuid.Nil); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("NewDeterministicIDGenerator(nil) error = %v, want validation", err)
	}
	generator, err := NewDeterministicIDGenerator(uuid.New())
	if err != nil {
		t.Fatalf("NewDeterministicIDGenerator() error = %v", err)
	}
	if _, err := generator.Derive(IDInput{Scope: "unknown", IdempotencyKey: uuid.New()}); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("Derive(unknown scope) error = %v, want validation", err)
	}
}
