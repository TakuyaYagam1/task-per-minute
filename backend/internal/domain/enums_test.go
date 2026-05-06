package domain_test

import (
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestPlayerStatus_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status domain.PlayerStatus
		want   bool
	}{
		{"idle", domain.PlayerStatusIdle, true},
		{"queued", domain.PlayerStatusQueued, true},
		{"in_duel", domain.PlayerStatusInDuel, true},
		{"empty", domain.PlayerStatus(""), false},
		{"unknown", domain.PlayerStatus("offline"), false},
		{"case mismatch", domain.PlayerStatus("Idle"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.status.IsValid(); got != tt.want {
				t.Errorf("PlayerStatus(%q).IsValid() = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestDifficulty_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    domain.Difficulty
		want bool
	}{
		{"easy", domain.DifficultyEasy, true},
		{"medium", domain.DifficultyMedium, true},
		{"hard", domain.DifficultyHard, true},
		{"empty", domain.Difficulty(""), false},
		{"unknown", domain.Difficulty("insane"), false},
		{"case mismatch", domain.Difficulty("Easy"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.d.IsValid(); got != tt.want {
				t.Errorf("Difficulty(%q).IsValid() = %v, want %v", tt.d, got, tt.want)
			}
		})
	}
}

func TestCategory_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		c    domain.Category
		want bool
	}{
		{"web", domain.CategoryWeb, true},
		{"crypto", domain.CategoryCrypto, true},
		{"forensics", domain.CategoryForensics, true},
		{"reverse", domain.CategoryReverse, true},
		{"pwn", domain.CategoryPwn, true},
		{"steganography", domain.CategoryStego, true},
		{"ppc", domain.CategoryPPC, true},
		{"osint", domain.CategoryOSINT, true},
		{"mobile", domain.CategoryMobile, true},
		{"hardware", domain.CategoryHardware, true},
		{"misc", domain.CategoryMisc, true},
		{"empty", domain.Category(""), false},
		{"unknown", domain.Category("network"), false},
		{"case mismatch", domain.Category("Web"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.c.IsValid(); got != tt.want {
				t.Errorf("Category(%q).IsValid() = %v, want %v", tt.c, got, tt.want)
			}
		})
	}
}

func TestDuelStatus_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    domain.DuelStatus
		want bool
	}{
		{"active", domain.DuelStatusActive, true},
		{"finished", domain.DuelStatusFinished, true},
		{"empty", domain.DuelStatus(""), false},
		{"unknown", domain.DuelStatus("paused"), false},
		{"case mismatch", domain.DuelStatus("Active"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.s.IsValid(); got != tt.want {
				t.Errorf("DuelStatus(%q).IsValid() = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}

func TestEnums_StringRoundTrip(t *testing.T) {
	t.Parallel()

	if domain.PlayerStatusIdle.String() != "idle" {
		t.Errorf("PlayerStatusIdle.String() = %q, want %q", domain.PlayerStatusIdle.String(), "idle")
	}
	if domain.DifficultyHard.String() != "hard" {
		t.Errorf("DifficultyHard.String() = %q, want %q", domain.DifficultyHard.String(), "hard")
	}
	if domain.CategoryForensics.String() != "forensics" {
		t.Errorf("CategoryForensics.String() = %q, want %q", domain.CategoryForensics.String(), "forensics")
	}
	if domain.DuelStatusFinished.String() != "finished" {
		t.Errorf("DuelStatusFinished.String() = %q, want %q", domain.DuelStatusFinished.String(), "finished")
	}
}
