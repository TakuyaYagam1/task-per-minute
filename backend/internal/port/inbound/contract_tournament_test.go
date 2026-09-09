package usecase

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestOperatorActorIDIsStableAndSubjectScoped(t *testing.T) {
	first, err := OperatorActorID(" admin ")
	if err != nil {
		t.Fatalf("OperatorActorID() error = %v", err)
	}
	repeated, err := OperatorActorID("admin")
	if err != nil {
		t.Fatalf("OperatorActorID() repeated error = %v", err)
	}
	other, err := OperatorActorID("other-admin")
	if err != nil {
		t.Fatalf("OperatorActorID() other error = %v", err)
	}
	if first == uuid.Nil || first != repeated || first == other {
		t.Fatalf("OperatorActorID() = %s, %s, %s; want stable subject-scoped IDs", first, repeated, other)
	}
}

func TestOperatorActorIDRejectsMissingSubject(t *testing.T) {
	if _, err := OperatorActorID("   "); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("OperatorActorID(empty) error = %v, want validation", err)
	}
}
