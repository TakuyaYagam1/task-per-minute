package domain_test

import (
	"strings"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestTournamentMetadataValidate(t *testing.T) {
	t.Parallel()

	valid := domain.TournamentMetadata{
		Name: "September Invitational", PublicID: "september-invitational",
		PlannedRosterSize: 8, ContentRevision: 3,
	}
	for name, mutate := range map[string]func(*domain.TournamentMetadata){
		"blank name":        func(value *domain.TournamentMetadata) { value.Name = " " },
		"long name":         func(value *domain.TournamentMetadata) { value.Name = strings.Repeat("a", 121) },
		"invalid public id": func(value *domain.TournamentMetadata) { value.PublicID = "September Invitational" },
		"roster below min":  func(value *domain.TournamentMetadata) { value.PlannedRosterSize = 3 },
		"roster above max":  func(value *domain.TournamentMetadata) { value.PlannedRosterSize = 17 },
		"missing content":   func(value *domain.TournamentMetadata) { value.ContentRevision = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			candidate := valid
			mutate(&candidate)
			if err := candidate.Validate(domain.TournamentPresetV1); err == nil {
				t.Fatal("Validate() error = nil, want invalid metadata")
			}
		})
	}
	for _, size := range []int{4, 16} {
		candidate := valid
		candidate.PlannedRosterSize = size
		if err := candidate.Validate(domain.TournamentPresetV1); err != nil {
			t.Fatalf("Validate() roster size %d error = %v", size, err)
		}
	}
}
