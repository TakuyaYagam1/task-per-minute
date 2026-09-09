package domain_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

func TestDecisionEvidenceNormalizesAndReplaysByteEquivalentResult(t *testing.T) {
	evidence := newDecisionEvidence(t, []string{" task-c ", "task-a", " task-b"})

	wantInputs := []string{"task-a", "task-b", "task-c"}
	assertStrings(t, evidence.NormalizedInputs, wantInputs)
	replayed, err := evidence.Replay()
	if err != nil {
		t.Fatalf("replay decision: %v", err)
	}
	assertStrings(t, replayed, evidence.Result)
	recordedBytes, err := evidence.CanonicalResult()
	if err != nil {
		t.Fatalf("canonical recorded result: %v", err)
	}
	replayedEvidence := evidence
	replayedEvidence.Result = replayed
	replayedBytes, err := replayedEvidence.CanonicalResult()
	if err != nil {
		t.Fatalf("canonical replayed result: %v", err)
	}
	if !bytes.Equal(recordedBytes, replayedBytes) {
		t.Fatalf("result bytes differ: %x != %x", recordedBytes, replayedBytes)
	}
}

func TestDecisionEvidenceUsesUnpredictableStoredSeed(t *testing.T) {
	first := newDecisionEvidence(t, []string{"a", "b", "c"})
	second := newDecisionEvidence(t, []string{"a", "b", "c"})

	if first.Seed == ([domain.DecisionSeedSize]byte{}) || second.Seed == ([domain.DecisionSeedSize]byte{}) {
		t.Fatal("generated seed is empty")
	}
	if first.Seed == second.Seed {
		t.Fatal("independent decisions reused a seed")
	}
}

func TestDecisionEvidenceRejectsTampering(t *testing.T) {
	base := newDecisionEvidence(t, []string{"a", "b", "c"})
	tests := map[string]func(*domain.DecisionEvidence){
		"result": func(e *domain.DecisionEvidence) {
			e.Result[0], e.Result[1] = e.Result[1], e.Result[0]
		},
		"owner": func(e *domain.DecisionEvidence) {
			e.OwnerID = uuid.New()
		},
		"timestamp": func(e *domain.DecisionEvidence) {
			e.DecidedAt = e.DecidedAt.Add(time.Second)
		},
		"seed": func(e *domain.DecisionEvidence) {
			e.Seed[0] ^= 0xff
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			evidence := base
			evidence.Result = append([]string(nil), base.Result...)
			mutate(&evidence)
			if err := evidence.Validate(); !errors.Is(err, domain.ErrDecisionReplayMismatch) {
				t.Fatalf("tamper error = %v", err)
			}
		})
	}
}

func TestDecisionEvidenceRejectsNonCanonicalInputs(t *testing.T) {
	for name, inputs := range map[string][]string{
		"empty":     {},
		"blank":     {"a", " "},
		"duplicate": {"a", " a "},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := domain.NewDecisionEvidence(
				uuid.New(),
				domain.DecisionPurposeTask,
				domain.DecisionAlgorithmV1,
				inputs,
				uuid.New(),
				time.Now().UTC(),
			)
			if !errors.Is(err, domain.ErrInvalidDecisionEvidence) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func newDecisionEvidence(t *testing.T, inputs []string) domain.DecisionEvidence {
	t.Helper()
	evidence, err := domain.NewDecisionEvidence(
		uuid.New(),
		domain.DecisionPurposeTask,
		domain.DecisionAlgorithmV1,
		inputs,
		uuid.New(),
		time.Date(2026, time.August, 27, 12, 0, 0, 123, time.UTC),
	)
	if err != nil {
		t.Fatalf("new decision evidence: %v", err)
	}
	return evidence
}

func assertStrings(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("length = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("item %d = %q, want %q", i, got[i], want[i])
		}
	}
}
