package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestArenaPreset(t *testing.T) {
	t.Parallel()

	preset := domain.ArenaPresetV1
	if !preset.IsValid() {
		t.Fatal("ArenaPresetV1.IsValid() = false, want true")
	}
	if got := preset.String(); got != "arena_v1" {
		t.Errorf("ArenaPresetV1.String() = %q, want %q", got, "arena_v1")
	}
	if got := preset.MinParticipants(); got != 4 {
		t.Errorf("ArenaPresetV1.MinParticipants() = %d, want 4", got)
	}
	if got := preset.MaxParticipants(); got != 16 {
		t.Errorf("ArenaPresetV1.MaxParticipants() = %d, want 16", got)
	}
	if got := preset.TaskDuration(); got != 180*time.Second {
		t.Errorf("ArenaPresetV1.TaskDuration() = %s, want 180s", got)
	}
	if got := preset.NominalDuration(); got != 60*time.Minute {
		t.Errorf("ArenaPresetV1.NominalDuration() = %s, want 60m", got)
	}
	if preset.EnforcesNominalDuration() {
		t.Error("ArenaPresetV1.EnforcesNominalDuration() = true, want false")
	}
}

func TestArenaPresetRosterAndRoundBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		participants int
		wantValid    bool
		wantRounds   int
	}{
		{name: "below minimum", participants: 3},
		{name: "minimum", participants: 4, wantValid: true, wantRounds: 3},
		{name: "upper three-round bound", participants: 8, wantValid: true, wantRounds: 3},
		{name: "lower four-round bound", participants: 9, wantValid: true, wantRounds: 4},
		{name: "maximum", participants: 16, wantValid: true, wantRounds: 4},
		{name: "above maximum", participants: 17},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			preset := domain.ArenaPresetV1
			if got := preset.ValidRosterSize(tt.participants); got != tt.wantValid {
				t.Errorf("ValidRosterSize(%d) = %v, want %v", tt.participants, got, tt.wantValid)
			}

			got, err := preset.SwissRounds(tt.participants)
			if !tt.wantValid {
				if !errors.Is(err, domain.ErrInvalidArenaRosterSize) {
					t.Fatalf("SwissRounds(%d) error = %v, want ErrInvalidArenaRosterSize", tt.participants, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("SwissRounds(%d) error = %v", tt.participants, err)
			}
			if got != tt.wantRounds {
				t.Errorf("SwissRounds(%d) = %d, want %d", tt.participants, got, tt.wantRounds)
			}
		})
	}
}

func TestArenaPresetRejectsUnknownPreset(t *testing.T) {
	t.Parallel()

	preset := domain.ArenaPreset("arena_custom")
	if preset.IsValid() {
		t.Error("unknown preset IsValid() = true, want false")
	}
	if preset.ValidRosterSize(8) {
		t.Error("unknown preset ValidRosterSize(8) = true, want false")
	}
	if _, err := preset.SwissRounds(8); !errors.Is(err, domain.ErrInvalidArenaRosterSize) {
		t.Fatalf("unknown preset SwissRounds(8) error = %v, want ErrInvalidArenaRosterSize", err)
	}
	if preset.MinParticipants() != 0 || preset.MaxParticipants() != 0 {
		t.Error("unknown preset participant bounds must be zero")
	}
	if preset.TaskDuration() != 0 || preset.NominalDuration() != 0 {
		t.Error("unknown preset durations must be zero")
	}
}
